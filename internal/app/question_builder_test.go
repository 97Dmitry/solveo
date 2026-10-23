package app

import (
	"context"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/questionbuilder"
	"github.com/97Dmitry/solveo/internal/stt"
)

func installReadyQuestion(t *testing.T, c *Controller) QuestionView {
	t.Helper()
	c.mu.Lock()
	b := c.questionLocked()
	b.Enable(true)
	b.Observe(questionbuilder.Segment{ID: "s", Track: "system", Text: "Explain channels", At: time.Now().UTC().Format(time.RFC3339Nano), Final: true})
	in, ok := b.Begin(time.Now())
	if !ok {
		t.Fatal("no analysis")
	}
	if err := b.Apply(in, questionbuilder.Result{HasQuestion: true, Ready: true, Text: "Explain channels", SourceIDs: []string{"s"}}, nil); err != nil {
		t.Fatal(err)
	}
	c.mu.Unlock()
	return c.QuestionSnapshot()
}
func TestQuestionSubmitPreservesDraftAndDeduplicatesVersion(t *testing.T) {
	c, f, s := setup(t)
	v := installReadyQuestion(t, c)
	err := c.SetDraft(DraftInput{ChatID: v.ChatID, Text: "keep me", Revision: 8})
	if err != nil {
		t.Fatal(err)
	}
	cmd := QuestionCommand{ChatID: v.ChatID, ID: v.ID, Revision: v.Revision, Version: v.Version}
	_, err = c.SendQuestion(cmd)
	if err != nil {
		t.Fatal(err)
	}
	req := <-f.started
	if !req.Turn.FromTranscript || req.Turn.Question == nil || len(req.Turn.Question.Sources) != 1 {
		t.Fatal(req.Turn)
	}
	if _, err = c.SendQuestion(cmd); err == nil {
		t.Fatal("duplicate accepted")
	}
	f.finish <- nil
	waitIdle(t, c)
	if c.Snapshot().Draft.Text != "keep me" {
		t.Fatal("draft consumed")
	}
	doc, err := s.Load(v.ChatID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Messages[0].Question == nil || doc.Messages[0].Question.Origin != questionbuilder.OriginAutomatic {
		t.Fatal("metadata lost")
	}
}
func TestQuestionRejectsBusyWithoutConsumingCard(t *testing.T) {
	c, f, _ := setup(t)
	v := installReadyQuestion(t, c)
	_, err := c.Submit(SubmitInput{ChatID: v.ChatID, RequestID: chat.ID(), Text: "ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	<-f.started
	if _, err = c.SendQuestion(QuestionCommand{ChatID: v.ChatID, ID: v.ID, Revision: v.Revision, Version: v.Version}); err == nil {
		t.Fatal("busy accepted")
	}
	if !c.QuestionSnapshot().CanSend {
		t.Fatal("card consumed")
	}
	f.finish <- nil
	waitIdle(t, c)
}
func TestQuestionNavigationPausesAndKeepsCard(t *testing.T) {
	c, _, _ := setup(t)
	v := installReadyQuestion(t, c)
	if _, err := c.NewChat(); err != nil {
		t.Fatal(err)
	}
	if c.QuestionSnapshot().Text != "" {
		t.Fatal("cross-chat card")
	}
	if _, err := c.OpenChat(v.ChatID); err != nil {
		t.Fatal(err)
	}
	got := c.QuestionSnapshot()
	if got.Enabled || got.Text != v.Text {
		t.Fatal(got)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type questionFake struct {
	*fakeProvider
	inputs  chan questionbuilder.Input
	results chan questionbuilder.Result
}

func (f *questionFake) AnalyzeQuestion(ctx context.Context, in questionbuilder.Input, _ chat.Config) (questionbuilder.Result, error) {
	f.inputs <- in
	select {
	case r := <-f.results:
		return r, nil
	case <-ctx.Done():
		return questionbuilder.Result{}, ctx.Err()
	}
}
func TestQuestionAnalysisDoesNotOccupyMainSlotAndIgnoresLateNavigation(t *testing.T) {
	c, f, _ := setup(t)
	q := &questionFake{fakeProvider: f, inputs: make(chan questionbuilder.Input, 4), results: make(chan questionbuilder.Result, 4)}
	c.mu.Lock()
	c.deps.Provider = q
	c.settings.Audio.BuilderModel = "gpt-test"
	v := c.questionSnapshotLocked()
	c.mu.Unlock()
	if err := c.QuestionCommand(QuestionCommand{Action: "enable", ChatID: v.ChatID, ID: v.ID, Revision: v.Revision, Version: v.Version}); err != nil {
		t.Fatal(err)
	}
	c.audio.mu.Lock()
	c.audio.tracks["stream"] = "system"
	c.audio.mu.Unlock()
	c.applyAudioUpdate(stt.Update{Kind: stt.KindFinal, StreamID: "stream", UtteranceID: "speech", Revision: 1, Text: "question"})
	c.mu.Lock()
	if c.question.timer != nil {
		c.question.timer.Stop()
	}
	c.mu.Unlock()
	c.analyzeQuestion()
	in := <-q.inputs
	if len(in.Segments) != 1 || in.Segments[0].ID != "speech" {
		t.Fatal(in)
	}
	_, err := c.Submit(SubmitInput{ChatID: v.ChatID, RequestID: chat.ID(), Text: "main"})
	if err != nil {
		t.Fatal(err)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	if _, err = c.NewChat(); err != nil {
		t.Fatal(err)
	}
	q.results <- questionbuilder.Result{HasQuestion: true, Ready: true, Text: "late", SourceIDs: []string{"speech"}}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		running := c.question.running
		c.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if c.QuestionSnapshot().Text != "" {
		t.Fatal("late cross-chat result")
	}
}

func TestQuestionCannotSaveOldLocalEditIntoUneditedCard(t *testing.T) {
	c, _, _ := setup(t)
	v := installReadyQuestion(t, c)
	err := c.QuestionCommand(QuestionCommand{Action: "save", ChatID: v.ChatID, ID: v.ID, Revision: v.Revision, Version: v.Version, Text: "stale local edit"})
	if err == nil || c.QuestionSnapshot().Text != v.Text {
		t.Fatal("unowned edit overwrote card", err)
	}
}

func TestQuestionFollowupKeepsExactTargetForParallelApproach(t *testing.T) {
	c, p, _ := parallelSetup(t)
	v := installReadyQuestion(t, c)
	if _, err := c.SendQuestion(QuestionCommand{ChatID: v.ChatID, ID: v.ID, Revision: v.Revision, Version: v.Version}); err != nil {
		t.Fatal(err)
	}
	first := nextParallelRequest(t, p)
	_ = nextParallelRequest(t, p)
	p.approach <- answerCompletion{result: llm.Result{Text: "approach"}}
	p.solution <- answerCompletion{result: llm.Result{Text: "solution"}}
	waitIdle(t, c)
	c.mu.Lock()
	b := c.questionLocked()
	b.Observe(questionbuilder.Segment{ID: "follow", Track: "system", Text: "what about closing", At: time.Now().UTC().Format(time.RFC3339Nano), Final: true})
	in, _ := b.Begin(time.Now())
	err := b.Apply(in, questionbuilder.Result{HasQuestion: true, Ready: true, Text: "And closing?", SourceIDs: []string{"follow"}, FollowUp: true}, nil)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	v = c.QuestionSnapshot()
	if _, err = c.SendQuestion(QuestionCommand{ChatID: v.ChatID, ID: v.ID, Revision: v.Revision, Version: v.Version}); err != nil {
		t.Fatal(err)
	}
	a, s := nextParallelRequest(t, p), nextParallelRequest(t, p)
	if a.Purpose != chat.AnswerApproach {
		a, s = s, a
	}
	_ = s
	if len(a.History) != 1 || a.History[0].ID != first.Turn.ID || a.Turn.Question.PreviousQuestionID != first.Turn.ID {
		t.Fatal("approach lost exact question", a)
	}
	p.approach <- answerCompletion{result: llm.Result{Text: "approach"}}
	p.solution <- answerCompletion{result: llm.Result{Text: "solution"}}
	waitIdle(t, c)
}

func TestMicFinalDoesNotInvalidateDisplayedSendVersion(t *testing.T) {
	c, f, _ := setup(t)
	v := installReadyQuestion(t, c)
	c.observeQuestion(v.ChatID, questionbuilder.Segment{ID: "mic", Track: "mic", Text: "candidate reply", Final: true, At: time.Now().UTC().Format(time.RFC3339Nano)})
	if _, err := c.SendQuestion(QuestionCommand{ChatID: v.ChatID, ID: v.ID, Revision: v.Revision, Version: v.Version}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
}

func TestQuestionDraftCanBeConfirmedWithoutEditingOrSubmitting(t *testing.T) {
	c, f, _ := setup(t)
	c.mu.Lock()
	b := c.questionLocked()
	b.Enable(true)
	b.Observe(questionbuilder.Segment{ID: "s", Track: "system", Text: "Что такое map и как работает поиск? Дальше обсудим ещё один момент.", Final: true, At: time.Now().UTC().Format(time.RFC3339Nano)})
	in, ok := b.Begin(time.Now())
	if !ok {
		c.mu.Unlock()
		t.Fatal("no input")
	}
	err := b.Apply(in, questionbuilder.Result{HasQuestion: true, Ready: false, Text: "Что такое map и как работает поиск?", SourceIDs: []string{"s"}}, nil)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	v := c.QuestionSnapshot()
	if v.CanSend || v.Status != "draft" {
		t.Fatal(v)
	}
	if err = c.QuestionCommand(QuestionCommand{Action: "confirm", ChatID: v.ChatID, ID: v.ID, Revision: v.Revision, Version: v.Version}); err != nil {
		t.Fatal(err)
	}
	confirmed := c.QuestionSnapshot()
	if !confirmed.CanSend || !confirmed.Manual || confirmed.Origin != questionbuilder.OriginAutomatic || confirmed.Text != v.Text {
		t.Fatal(confirmed)
	}
	select {
	case <-f.started:
		t.Fatal("confirmation sent a provider request")
	default:
	}
}
