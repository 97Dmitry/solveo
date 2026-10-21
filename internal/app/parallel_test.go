package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/platform"
)

type answerCompletion struct {
	result llm.Result
	err    error
}
type parallelProvider struct {
	ignoreCancellation bool
	started            chan llm.Request
	approach           chan answerCompletion
	solution           chan answerCompletion
}

func (*parallelProvider) Describe() llm.Descriptor { return llm.Descriptor{ID: "codex"} }
func (p *parallelProvider) Generate(ctx context.Context, r llm.Request, _ func(llm.Event)) (llm.Result, error) {
	p.started <- r
	ch := p.solution
	if r.Purpose == chat.AnswerApproach {
		ch = p.approach
	}
	if p.ignoreCancellation {
		out := <-ch
		return out.result, out.err
	}
	select {
	case out := <-ch:
		return out.result, out.err
	case <-ctx.Done():
		return llm.Result{}, ctx.Err()
	}
}
func (*parallelProvider) Check(context.Context, chat.Config, func(llm.Event)) (llm.CheckResult, error) {
	return llm.CheckResult{}, nil
}
func parallelSetup(t *testing.T) (*Controller, *parallelProvider, *history.Store) {
	t.Helper()
	c, _, store := setup(t)
	p := &parallelProvider{started: make(chan llm.Request, 10), approach: make(chan answerCompletion, 5), solution: make(chan answerCompletion, 5)}
	c.deps.Provider = p
	s := c.Snapshot()
	cfg := s.Config
	cfg.ParallelAnswers = true
	cfg.FastModel = "light"
	cfg.FastReasoning = "none"
	cfg.Model = "strong"
	cfg.Reasoning = "high"
	if _, err := c.SaveChatConfig(ConfigInput{ChatID: s.ActiveChatID, Config: cfg, ExpectedRevision: s.ConfigRevision}); err != nil {
		t.Fatal(err)
	}
	return c, p, store
}
func nextParallelRequest(t *testing.T, p *parallelProvider) llm.Request {
	t.Helper()
	select {
	case r := <-p.started:
		return r
	case <-time.After(time.Second):
		t.Fatal("second request did not start concurrently")
		return llm.Request{}
	}
}
func waitApproach(t *testing.T, c *Controller) Snapshot {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s := c.Snapshot()
		if len(s.Messages) > 0 && len(s.Messages[0].Answers) == 2 && s.Messages[0].Answers[0].Status == chat.StatusComplete {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("approach not visible before solution")
	return Snapshot{}
}
func startParallel(t *testing.T, c *Controller, p *parallelProvider) (llm.Request, llm.Request) {
	t.Helper()
	s := c.Snapshot()
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: "solve", DraftRevision: 1}); err != nil {
		t.Fatal(err)
	}
	a, b := nextParallelRequest(t, p), nextParallelRequest(t, p)
	if a.Purpose == chat.AnswerSolution {
		a, b = b, a
	}
	if a.Purpose != chat.AnswerApproach || b.Purpose != chat.AnswerSolution {
		t.Fatal(a.Purpose, b.Purpose)
	}
	return a, b
}
func TestParallelApproachVisibleAndArchivedBeforeMain(t *testing.T) {
	c, p, store := parallelSetup(t)
	a, b := startParallel(t, c, p)
	if len(a.State) != 0 || a.Config.Model != "light" || a.Config.Reasoning != "none" || b.Config.Model != "strong" || b.Config.Reasoning != "high" {
		t.Fatal(a, b)
	}
	p.approach <- answerCompletion{result: llm.Result{Text: "Use a map", State: json.RawMessage(`{"fast":true}`)}}
	waitApproach(t, c)
	deadline := time.Now().Add(time.Second)
	for {
		stored, err := store.Load(c.Snapshot().ActiveChatID)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored.Messages[0].Answers) == 2 && stored.Messages[0].Answers[0].Text == "Use a map" {
			if len(stored.ProviderState) != 0 {
				t.Fatal("fast state polluted chat")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("approach not checkpointed")
		}
		time.Sleep(time.Millisecond)
	}
	p.solution <- answerCompletion{result: llm.Result{Text: "main", State: json.RawMessage(`{"main":true}`)}}
	waitIdle(t, c)
	s := c.Snapshot()
	if s.Messages[0].Status != "complete" || s.Messages[0].Answers[1].Text != "main" {
		t.Fatal(s.Messages)
	}
	stored, err := store.Load(s.ActiveChatID)
	if err != nil || !strings.Contains(string(stored.ProviderState), `"main": true`) {
		t.Fatal(stored, err)
	}
}
func TestParallelRetryOnlyMainUsesFrozenConfig(t *testing.T) {
	c, p, _ := parallelSetup(t)
	startParallel(t, c, p)
	p.approach <- answerCompletion{result: llm.Result{Text: "approach"}}
	waitApproach(t, c)
	p.solution <- answerCompletion{result: llm.Result{State: json.RawMessage(`{"main":true}`)}, err: llm.ErrOutcomeUnknown}
	waitIdle(t, c)
	s := c.Snapshot()
	cfg := s.Config
	cfg.Model = "changed"
	cfg.ParallelAnswers = false
	if _, err := c.SaveChatConfig(ConfigInput{ChatID: s.ActiveChatID, Config: cfg, ExpectedRevision: s.ConfigRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, RetryMessageID: s.Messages[0].ID}); err != nil {
		t.Fatal(err)
	}
	r := nextParallelRequest(t, p)
	if r.Purpose != "solution" || r.Config.Model != "strong" || string(r.State) != `{"main":true}` {
		t.Fatal(r)
	}
	p.solution <- answerCompletion{result: llm.Result{Text: "retry main"}}
	waitIdle(t, c)
	s = c.Snapshot()
	if len(p.started) != 0 || len(s.Messages) != 1 || s.Messages[0].Answers[0].Text != "approach" || s.Messages[0].Answers[1].Text != "retry main" || len(s.Messages[0].Attempts) != 2 {
		t.Fatal(s.Messages)
	}
}
func TestParallelMainFirstCancelsSlowApproach(t *testing.T) {
	c, p, _ := parallelSetup(t)
	startParallel(t, c, p)
	p.solution <- answerCompletion{result: llm.Result{Text: "solution"}}
	waitIdle(t, c)
	s := c.Snapshot()
	if s.Messages[0].Status != "complete" || s.Messages[0].Answers[0].Status != "canceled" || s.Messages[0].Answers[0].Text != "" {
		t.Fatal(s.Messages)
	}
}
func TestParallelCancelStopsBoth(t *testing.T) {
	c, p, _ := parallelSetup(t)
	startParallel(t, c, p)
	if err := c.Cancel(c.Snapshot().Operation.ID); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, c)
	s := c.Snapshot()
	if s.Messages[0].Status != "canceled" || s.Messages[0].Answers[0].Status != "canceled" || s.Messages[0].Answers[1].Status != "canceled" {
		t.Fatal(s.Messages)
	}
}
func TestParallelFastFailureDoesNotFailMain(t *testing.T) {
	c, p, _ := parallelSetup(t)
	startParallel(t, c, p)
	p.approach <- answerCompletion{err: errors.New("fast offline")}
	p.solution <- answerCompletion{result: llm.Result{Text: "main"}}
	waitIdle(t, c)
	s := c.Snapshot()
	if s.Messages[0].Status != "complete" || s.Messages[0].Answers[1].Text != "main" {
		t.Fatal(s.Messages)
	}
}
func TestApproachContextKeepsRecentConstraintsAndMainAnswer(t *testing.T) {
	text := approachContext([]chat.Message{{Role: chat.RoleUser, Text: strings.Repeat("old", 20000)}, {Role: chat.RoleUser, Text: "array is sorted"}, {Role: chat.RoleUser, Text: "duplicates?", Answers: []chat.Answer{{Kind: chat.AnswerApproach, Text: "unchecked guess", Status: chat.StatusComplete}, {Kind: chat.AnswerSolution, Text: "two pointers", Status: chat.StatusComplete}}}})
	if !strings.Contains(text, "array is sorted") || !strings.Contains(text, "two pointers") || strings.Contains(text, "unchecked guess") || len(text) > 70000 {
		t.Fatal("incorrect bounded context")
	}
}

type checkpointFailArchive struct {
	Archive
	entered chan struct{}
	release chan struct{}
}

func (s *checkpointFailArchive) Save(in history.SaveInput) (chat.Chat, error) {
	if len(in.Chat.Messages) > 0 {
		answers := in.Chat.Messages[len(in.Chat.Messages)-1].Answers
		if len(answers) == 2 && answers[0].Status == chat.StatusComplete && answers[1].Status == chat.StatusRequesting {
			close(s.entered)
			<-s.release
			return in.Chat, errors.New("disk full")
		}
	}
	return s.Archive.Save(in)
}
func TestParallelCheckpointFailureDrainsMainAndRetrySaveDoesNotGenerate(t *testing.T) {
	c, p, store := parallelSetup(t)
	p.ignoreCancellation = true
	gate := &checkpointFailArchive{Archive: store, entered: make(chan struct{}), release: make(chan struct{})}
	c.deps.Store = gate
	startParallel(t, c, p)
	p.approach <- answerCompletion{result: llm.Result{Text: "approach"}}
	select {
	case <-gate.entered:
	case <-time.After(time.Second):
		t.Fatal("checkpoint did not start")
	}
	p.solution <- answerCompletion{result: llm.Result{Text: "main already returned", State: json.RawMessage(`{"main":true}`)}}
	// Allow a concurrent completion while the sole archive writer is blocked.
	close(gate.release)
	waitIdle(t, c)
	s := c.Snapshot()
	if s.Operation.Phase != "save-pending" || s.Messages[0].Answers[0].Text != "approach" || s.Messages[0].Answers[1].Text != "main already returned" {
		t.Fatal(s)
	}
	if _, err := c.NewChat(); !errors.Is(err, ErrSavePending) {
		t.Fatal(err)
	}
	c.deps.Store = store
	if err := c.RetrySave(); err != nil {
		t.Fatal(err)
	}
	if len(p.started) != 0 {
		t.Fatal("retry saving called provider")
	}
	if c.Snapshot().Messages[0].Answers[0].Text != "approach" {
		t.Fatal("lost approach during recovery")
	}
}

func TestParallelFollowupCarriesPreviousScreenshotAndMainContext(t *testing.T) {
	c, p, _ := parallelSetup(t)
	c.deps.Capturer = fakeCapture{}
	if err := c.BeginCapture("quick-full"); err != nil {
		t.Fatal(err)
	}
	a, b := nextParallelRequest(t, p), nextParallelRequest(t, p)
	if len(a.Images) != 1 || len(b.Images) != 1 || a.Images[0].ID != b.Images[0].ID {
		t.Fatal("requests have different screenshots")
	}
	imageID := a.Images[0].ID
	p.approach <- answerCompletion{result: llm.Result{Text: "approach"}}
	waitApproach(t, c)
	p.solution <- answerCompletion{result: llm.Result{Text: "two pointers"}}
	waitIdle(t, c)
	s := c.Snapshot()
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: "what about duplicates?"}); err != nil {
		t.Fatal(err)
	}
	a, b = nextParallelRequest(t, p), nextParallelRequest(t, p)
	if a.Purpose == chat.AnswerSolution {
		a, b = b, a
	}
	if len(a.Images) != 1 || a.Images[0].ID != imageID || !strings.Contains(a.ContextText, "two pointers") || len(b.Images) != 0 {
		t.Fatal("followup lost the original task", a, b)
	}
	p.approach <- answerCompletion{result: llm.Result{Text: "clarification"}}
	p.solution <- answerCompletion{result: llm.Result{Text: "full clarification"}}
	waitIdle(t, c)
}

func TestParallelReloadedInterruptedTurnRetriesOnlyMain(t *testing.T) {
	c, p, store := parallelSetup(t)
	startParallel(t, c, p)
	p.approach <- answerCompletion{result: llm.Result{Text: "saved approach"}}
	waitApproach(t, c)
	// Loading the actual checkpoint simulates startup after a crash while main runs.
	var loaded chat.Chat
	deadline := time.Now().Add(time.Second)
	for {
		var err error
		loaded, err = store.Load(c.Snapshot().ActiveChatID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Messages[0].Answers[0].Status == chat.StatusComplete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("checkpoint missing")
		}
		time.Sleep(time.Millisecond)
	}
	if loaded.Messages[0].Answers[1].Status != chat.StatusInterrupted {
		t.Fatal(loaded)
	}
	if err := c.Cancel(c.Snapshot().Operation.ID); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, c)
	c.mu.Lock()
	c.active = loaded
	c.mu.Unlock()
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: loaded.ID, RetryMessageID: loaded.Messages[0].ID}); err != nil {
		t.Fatal(err)
	}
	r := nextParallelRequest(t, p)
	if r.Purpose != chat.AnswerSolution {
		t.Fatal("repeated approach after reload")
	}
	p.solution <- answerCompletion{result: llm.Result{Text: "restored solution"}}
	waitIdle(t, c)
	if len(p.started) != 0 || c.Snapshot().Messages[0].Answers[0].Text != "saved approach" {
		t.Fatal("lost checkpointed approach")
	}
}

func TestSingleRetryKeepsAnswerConfigRevisionConsistent(t *testing.T) {
	c, p, _ := setup(t)
	s := c.Snapshot()
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: "original"}); err != nil {
		t.Fatal(err)
	}
	<-p.started
	p.finish <- errors.New("offline")
	waitIdle(t, c)
	s = c.Snapshot()
	originalRevision := s.Messages[0].ConfigRevision
	cfg := s.Config
	cfg.Model = "changed"
	if _, err := c.SaveChatConfig(ConfigInput{ChatID: s.ActiveChatID, Config: cfg, ExpectedRevision: s.ConfigRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, RetryMessageID: s.Messages[0].ID}); err != nil {
		t.Fatal(err)
	}
	request := <-p.started
	if request.ConfigRevision != originalRevision || request.Config.Model != "" {
		t.Fatal("retry did not preserve frozen configuration")
	}
	p.finish <- nil
	waitIdle(t, c)
	for _, m := range c.Snapshot().Messages {
		if m.ConfigRevision != originalRevision {
			t.Fatalf("answer revision %d differs from request %d", m.ConfigRevision, originalRevision)
		}
	}
}

func TestParallelMainFirstDiscardsLateApproach(t *testing.T) {
	c, p, _ := parallelSetup(t)
	p.ignoreCancellation = true
	startParallel(t, c, p)
	p.solution <- answerCompletion{result: llm.Result{Text: "solution"}}
	deadline := time.Now().Add(time.Second)
	for c.Snapshot().Messages[0].Answers[1].Status != chat.StatusComplete {
		if time.Now().After(deadline) {
			t.Fatal("main not visible")
		}
		time.Sleep(time.Millisecond)
	}
	p.approach <- answerCompletion{result: llm.Result{Text: "late conflicting approach"}}
	waitIdle(t, c)
	a := c.Snapshot().Messages[0].Answers[0]
	if a.Status != chat.StatusCanceled || a.Text != "" {
		t.Fatal("obsolete approach appeared", a)
	}
}

func TestParallelQuickCaptureFailureKeepsQueueAndNewDraft(t *testing.T) {
	c, p, _ := parallelSetup(t)
	c.deps.Capturer = fakeCapture{}
	c.deps.NativeHost = &fakeHost{}
	if err := c.BeginCapture("queue-region"); err != nil {
		t.Fatal(err)
	}
	c.HandleNativeEvent(platform.Event{Kind: "selection", OperationID: c.Snapshot().Operation.ID})
	waitIdle(t, c)
	initial := c.Snapshot()
	originalID := initial.Queue[0].ID
	if err := c.SetDraft(DraftInput{ChatID: initial.ActiveChatID, Text: "next question", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if err := c.BeginCapture("quick-full"); err != nil {
		t.Fatal(err)
	}
	a, b := nextParallelRequest(t, p), nextParallelRequest(t, p)
	if len(a.Images) != 1 || len(b.Images) != 1 || a.Images[0].ID == originalID {
		t.Fatal("quick capture consumed earlier queue")
	}
	if err := c.SetDraft(DraftInput{ChatID: initial.ActiveChatID, Text: "new typing", Revision: 2}); err != nil {
		t.Fatal(err)
	}
	p.approach <- answerCompletion{result: llm.Result{Text: "approach"}}
	waitApproach(t, c)
	p.solution <- answerCompletion{err: errors.New("offline")}
	waitIdle(t, c)
	s := c.Snapshot()
	if len(s.Queue) != 2 || s.Queue[0].ID != originalID || s.Draft.Text != "new typing" {
		t.Fatal("failure lost queue or new typing", s)
	}
	for _, q := range c.queues[s.ActiveChatID] {
		if _, err := os.Stat(q.Path); err != nil {
			t.Fatal("source removed on failure", err)
		}
	}
	if _, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, RetryMessageID: s.Messages[0].ID}); err != nil {
		t.Fatal(err)
	}
	retry := nextParallelRequest(t, p)
	if retry.Purpose != chat.AnswerSolution || len(retry.Images) != 1 || retry.Images[0].ID != a.Images[0].ID {
		t.Fatal("retry changed quick capture input", retry)
	}
	p.solution <- answerCompletion{result: llm.Result{Text: "solution"}}
	waitIdle(t, c)
	s = c.Snapshot()
	if len(s.Queue) != 1 || s.Queue[0].ID != originalID || s.Draft.Text != "new typing" {
		t.Fatal("success cleared unrelated queue/draft", s)
	}
}
