package app

import (
	"context"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
)

type interruptedQuestionProvider struct {
	*fakeProvider
	interrupted chan struct{}
	drain       chan struct{}
}

func (p *interruptedQuestionProvider) Generate(ctx context.Context, req llm.Request, _ func(llm.Event)) (llm.Result, error) {
	if len(req.Turn.Attempts) != 1 {
		return p.fakeProvider.Generate(ctx, req, nil)
	}
	p.started <- req
	<-ctx.Done()
	close(p.interrupted)
	<-p.drain
	return llm.Result{}, ctx.Err()
}

func nextAutomaticRequest(t *testing.T, f *fakeProvider) llm.Request {
	t.Helper()
	select {
	case req := <-f.started:
		return req
	case <-time.After(time.Second):
		t.Fatal("automatic question was not sent")
		return llm.Request{}
	}
}

func TestAutomaticQuestionStartsWithoutManualSend(t *testing.T) {
	c, f, _ := setup(t)
	c.mu.Lock()
	c.deps.Provider = &questionFake{fakeProvider: f}
	c.settings.Audio.BuilderModel = "gpt-test"
	c.autoSession = true
	c.mu.Unlock()
	v := installReadyQuestion(t, c)
	err := c.QuestionCommand(QuestionCommand{Action: "auto-on", ChatID: v.ChatID, ID: v.ID, Revision: v.Revision})
	if err != nil {
		t.Fatal(err)
	}
	req := nextAutomaticRequest(t, f)
	if req.Turn.Text != v.Text || req.Turn.Question == nil {
		t.Fatal(req.Turn)
	}
	f.finish <- nil
	waitIdle(t, c)
}

func TestAutomaticQuestionRechecksDraftOnce(t *testing.T) {
	for _, answerable := range []bool{true, false} {
		name := "answerable"
		if !answerable {
			name = "needs_more_speech"
		}
		t.Run(name, func(t *testing.T) {
			c, f, _ := setup(t)
			p := &questionFake{fakeProvider: f, inputs: make(chan questionbuilder.Input, 4), results: make(chan questionbuilder.Result, 4)}
			c.mu.Lock()
			c.deps.Provider = p
			c.settings.Audio.BuilderModel = "gpt-test"
			c.autoSession = true
			v := c.questionSnapshotLocked()
			c.mu.Unlock()
			if err := c.QuestionCommand(QuestionCommand{Action: "auto-on", ChatID: v.ChatID}); err != nil {
				t.Fatal(err)
			}
			c.observeQuestion(v.ChatID, questionbuilder.Segment{
				ID: "speech", Track: "system", Text: "Расскажи, какие есть типы данных",
				At: time.Now().UTC().Format(time.RFC3339Nano), Final: true,
			})
			nextAnalysis := func() questionbuilder.Input {
				t.Helper()
				select {
				case in := <-p.inputs:
					return in
				case <-time.After(4 * time.Second):
					t.Fatal("the draft was left without a readiness check")
					return questionbuilder.Input{}
				}
			}
			if first := nextAnalysis(); !first.Automatic || first.DraftCheck {
				t.Fatal("first analysis must use automatic question rules", first)
			}
			draft := questionbuilder.Result{HasQuestion: true, Text: "Какие есть типы данных?", SourceIDs: []string{"speech"}}
			p.results <- draft
			if check := nextAnalysis(); !check.DraftCheck || check.Current.Text != draft.Text || len(check.Current.Sources) != 1 {
				t.Fatal("readiness check lost the draft evidence", check)
			}
			draft.Ready = answerable
			p.results <- draft
			if answerable {
				if req := nextAutomaticRequest(t, f); req.Turn.Text != draft.Text {
					t.Fatal(req.Turn)
				}
				f.finish <- nil
				waitIdle(t, c)
				return
			}
			select {
			case <-p.inputs:
				t.Fatal("automatic readiness checks must not repeat without new speech")
			case <-f.started:
				t.Fatal("incomplete draft was automatically submitted")
			case <-time.After(2300 * time.Millisecond):
			}
			if view := c.QuestionSnapshot(); !view.CanConfirm || view.Text != draft.Text {
				t.Fatal("incomplete draft should still be available for manual confirmation", view)
			}
		})
	}
}

func applyAutomaticAnalysis(t *testing.T, c *Controller, speech string, result questionbuilder.Result) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.questionLocked()
	b.Observe(questionbuilder.Segment{ID: result.SourceIDs[0], Track: "system", Text: speech, At: time.Now().UTC().Format(time.RFC3339Nano), Final: true})
	in, ok := b.Begin(time.Now())
	if !ok {
		t.Fatal("analysis did not begin")
	}
	if err := b.Apply(in, result, nil); err != nil {
		t.Fatal(err)
	}
	c.changedLocked()
}

func TestAutomaticQuestionRewritesLatestAnswer(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "streaming"
		if completed {
			name = "completed"
		}
		t.Run(name, func(t *testing.T) {
			c, provider, _ := streamingSetup(t)
			v := installReadyQuestion(t, c)
			c.mu.Lock()
			c.question.automatic = true
			c.scheduleAutomaticQuestionLocked()
			c.mu.Unlock()
			first := nextCall(t, provider)
			first.emit(llm.Event{Kind: llm.EventAnswerText, Text: "obsolete preview"})
			var answerID string
			if completed {
				provider.finish[""] <- llm.Result{Text: "obsolete answer"}
				waitIdle(t, c)
				answerID = c.Snapshot().Messages[1].ID
			}

			applyAutomaticAnalysis(t, c, "Only buffered channels", questionbuilder.Result{
				HasQuestion: true, Ready: true, Text: "Explain buffered channels",
				SourceIDs: []string{"clarification"}, FollowUp: true, Refinement: true,
			})
			second := nextCall(t, provider)
			card := second.req.Turn.Question
			if second.req.Turn.ID != first.req.Turn.ID || len(second.req.History) != 0 || card == nil ||
				!card.Refinement || card.PreviousQuestionID != "" || len(card.Sources) != 2 ||
				card.Sources[0].ID != "s" || card.Sources[1].ID != "clarification" {
				t.Fatal("the rewrite lost the turn or the original evidence", second.req)
			}
			first.emit(llm.Event{Kind: llm.EventAnswerText, Text: "late obsolete preview"})
			second.emit(llm.Event{Kind: llm.EventAnswerText, Text: "fresh preview"})
			if blocks := c.StreamSnapshot().Blocks; len(blocks) != 1 || blocks[0].Text != "fresh preview" {
				t.Fatal("obsolete preview survived", blocks)
			}
			provider.finish[""] <- llm.Result{Text: "revised answer"}
			waitIdle(t, c)
			doc, err := c.deps.Store.Load(v.ChatID)
			if err != nil {
				t.Fatal(err)
			}
			if len(doc.Messages) != 2 || doc.Messages[0].Text != "Explain buffered channels" || doc.Messages[1].Text != "revised answer" ||
				(answerID != "" && doc.Messages[1].ID != answerID) {
				t.Fatal("expected one revised exchange in the archive", doc.Messages)
			}
			status := chat.StatusCanceled
			if completed {
				status = chat.StatusComplete
			}
			attempts := doc.Messages[0].Attempts
			if len(attempts) != 2 || attempts[0].Status != status || attempts[1].Status != chat.StatusComplete {
				t.Fatal(attempts)
			}
		})
	}
}

func TestAutomaticQuestionPreservesIndependentFollowup(t *testing.T) {
	c, f, _ := setup(t)
	installReadyQuestion(t, c)
	c.mu.Lock()
	c.question.automatic = true
	c.scheduleAutomaticQuestionLocked()
	c.mu.Unlock()
	first := nextAutomaticRequest(t, f)
	applyAutomaticAnalysis(t, c, "How do you close a channel?", questionbuilder.Result{
		HasQuestion: true, Ready: true, Text: "How do you close a channel?",
		SourceIDs: []string{"new-question"}, FollowUp: true,
	})
	f.finish <- nil
	second := nextAutomaticRequest(t, f)
	if second.Turn.ID == first.Turn.ID || len(second.History) != 2 || second.Turn.Question.PreviousQuestionID != first.Turn.ID {
		t.Fatal("a separate follow-up must keep the original exchange", second)
	}
	f.finish <- nil
	waitIdle(t, c)
}

func TestAutomaticQuestionRewritesBothParallelBlocks(t *testing.T) {
	c, p, _ := parallelSetup(t)
	installReadyQuestion(t, c)
	c.mu.Lock()
	c.question.automatic = true
	c.scheduleAutomaticQuestionLocked()
	c.mu.Unlock()
	first := nextParallelRequest(t, p)
	_ = nextParallelRequest(t, p)
	applyAutomaticAnalysis(t, c, "Only buffered channels", questionbuilder.Result{
		HasQuestion: true, Ready: true, Text: "Explain buffered channels",
		SourceIDs: []string{"clarification"}, FollowUp: true, Refinement: true,
	})
	for range 2 {
		req := nextParallelRequest(t, p)
		if req.Turn.ID != first.Turn.ID || !req.Turn.Question.Refinement || len(req.Turn.Attempts) != 2 {
			t.Fatal("parallel answer did not restart inside the same message", req.Turn)
		}
	}
	p.approach <- answerCompletion{result: llm.Result{Text: "new approach"}}
	p.solution <- answerCompletion{result: llm.Result{Text: "new solution"}}
	waitIdle(t, c)
	messages := c.Snapshot().Messages
	if len(messages) != 1 || messages[0].Status != chat.StatusComplete || messages[0].Answers[1].Text != "new solution" {
		t.Fatal(messages)
	}
}

func TestAutomaticQuestionStopsAfterUnknownOutcome(t *testing.T) {
	c, f, _ := setup(t)
	installReadyQuestion(t, c)
	c.mu.Lock()
	c.question.automatic = true
	c.scheduleAutomaticQuestionLocked()
	c.mu.Unlock()
	_ = nextAutomaticRequest(t, f)
	f.finish <- llm.ErrOutcomeUnknown
	waitIdle(t, c)
	if c.QuestionSnapshot().Automatic {
		t.Fatal("auto mode is still armed after an unknown outcome")
	}
}

func TestAutomaticRefinementSurvivesNewSpeechWhileInterruptDrains(t *testing.T) {
	c, f, _ := setup(t)
	v := installReadyQuestion(t, c)
	p := &interruptedQuestionProvider{fakeProvider: f, interrupted: make(chan struct{}), drain: make(chan struct{})}
	t.Cleanup(func() { close(p.drain) })
	c.mu.Lock()
	c.deps.Provider = p
	c.question.automatic = true
	c.scheduleAutomaticQuestionLocked()
	c.mu.Unlock()
	first := nextAutomaticRequest(t, f)
	applyAutomaticAnalysis(t, c, "Only buffered channels", questionbuilder.Result{
		HasQuestion: true, Ready: true, Text: "Explain buffered channels",
		SourceIDs: []string{"clarification"}, FollowUp: true, Refinement: true,
	})
	select {
	case <-p.interrupted:
	case <-time.After(time.Second):
		t.Fatal("interruption was not requested")
	}
	c.observeQuestion(v.ChatID, questionbuilder.Segment{
		ID: "new-phrase", Track: "system", Text: "And another question", At: time.Now().UTC().Format(time.RFC3339Nano),
	})
	p.drain <- struct{}{}
	second := nextAutomaticRequest(t, f)
	if second.Turn.ID != first.Turn.ID || second.Turn.Text != "Explain buffered channels" {
		t.Fatal("new speech displaced the accepted correction", second.Turn)
	}
	if !c.QuestionSnapshot().Unincluded {
		t.Fatal("new partial was swallowed by the replacement")
	}
	f.finish <- nil
	waitIdle(t, c)
}
