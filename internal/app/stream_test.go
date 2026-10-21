package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/settings"
)

type streamCall struct {
	req  llm.Request
	emit func(llm.Event)
}

// streamingProvider lets a test drive previews and completion of each request.
type streamingProvider struct {
	mu       sync.Mutex
	calls    chan streamCall
	finish   map[string]chan llm.Result // by purpose ("" for ordinary chat)
	prepared int
	closed   int
	// loginNow completes the login inside StartLogin, before it returns.
	loginNow bool
}

func (*streamingProvider) Describe() llm.Descriptor {
	return llm.Descriptor{ID: "codex", Capabilities: llm.Capabilities{Images: true}}
}
func (p *streamingProvider) Generate(ctx context.Context, r llm.Request, emit func(llm.Event)) (llm.Result, error) {
	p.calls <- streamCall{r, emit}
	select {
	case out := <-p.finish[r.Purpose]:
		return out, nil
	case <-ctx.Done():
		return llm.Result{}, ctx.Err()
	}
}
func (*streamingProvider) Check(context.Context, chat.Config, func(llm.Event)) (llm.CheckResult, error) {
	return llm.CheckResult{}, nil
}
func (*streamingProvider) Account(context.Context) (llm.Account, error) {
	return llm.Account{SignedIn: true, Kind: "chatgpt"}, nil
}
func (p *streamingProvider) StartLogin(_ context.Context, done func(error)) (llm.LoginPrompt, error) {
	if p.loginNow {
		done(nil)
		return llm.LoginPrompt{ID: "login-1", URL: "https://auth.example.test/device", Code: "ABCD-1234"}, nil
	}
	go func() { time.Sleep(20 * time.Millisecond); done(nil) }()
	return llm.LoginPrompt{ID: "login-1", URL: "https://auth.example.test/device", Code: "ABCD-1234"}, nil
}
func (*streamingProvider) CancelLogin(context.Context, string) error { return nil }
func (p *streamingProvider) Prepare(context.Context) error {
	p.mu.Lock()
	p.prepared++
	p.mu.Unlock()
	return nil
}
func (p *streamingProvider) Close(context.Context) error {
	p.mu.Lock()
	p.closed++
	p.mu.Unlock()
	return nil
}

type emitted struct {
	mu     sync.Mutex
	stream []StreamSnapshot
}

func (e *emitted) record(name string, data interface{}) {
	if name != "solveo:stream" {
		return
	}
	e.mu.Lock()
	e.stream = append(e.stream, data.(StreamSnapshot))
	e.mu.Unlock()
}
func (e *emitted) all() []StreamSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]StreamSnapshot(nil), e.stream...)
}

func streamingSetup(t *testing.T) (*Controller, *streamingProvider, *emitted) {
	t.Helper()
	s, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := settings.Open(s.Dir)
	if err = st.Save(settings.Config{Theme: "dark", FontSize: 17, UIScale: 100}); err != nil {
		t.Fatal(err)
	}
	p := &streamingProvider{calls: make(chan streamCall, 4), finish: map[string]chan llm.Result{"": make(chan llm.Result, 1), chat.AnswerApproach: make(chan llm.Result, 1), chat.AnswerSolution: make(chan llm.Result, 1)}}
	ev := &emitted{}
	c, err := New(Dependencies{Store: s, SettingsStore: st, Provider: p, TempDir: t.TempDir(), Emit: ev.record})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c, p, ev
}

func nextCall(t *testing.T, p *streamingProvider) streamCall {
	t.Helper()
	select {
	case call := <-p.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("request did not start")
		return streamCall{}
	}
}

func submitText(t *testing.T, c *Controller, text string) string {
	t.Helper()
	s := c.Snapshot()
	id, err := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: text, DraftRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func waitFlush() { time.Sleep(3 * streamFlushInterval) }

func TestStreamPreviewIsCoalescedBoundAndClearedOnCompletion(t *testing.T) {
	c, p, ev := streamingSetup(t)
	opID := submitText(t, c, "вопрос")
	call := nextCall(t, p)
	for i := 1; i <= 50; i++ {
		call.emit(llm.Event{Kind: llm.EventAnswerText, Text: "часть " + string(rune('a'+i%26))})
	}
	call.emit(llm.Event{Kind: llm.EventAnswerText, Text: "почти готово"})
	waitFlush()
	got := ev.all()
	if len(got) == 0 || len(got) > 3 {
		t.Fatalf("expected coalesced updates, got %d", len(got))
	}
	last := got[len(got)-1]
	snap := c.Snapshot()
	turn := snap.Messages[0]
	if len(last.Blocks) != 1 || last.Blocks[0].Text != "почти готово" || last.Blocks[0].Block != blockMessage ||
		last.Blocks[0].OperationID != opID || last.Blocks[0].MessageID != turn.ID || last.Blocks[0].AttemptID != turn.Attempts[0].ID || last.Blocks[0].ChatID != snap.ActiveChatID {
		t.Fatalf("preview %+v turn %+v", last, turn)
	}
	if turn.Status != chat.StatusRequesting || len(snap.Messages) != 1 {
		t.Fatalf("preview became an answer: %+v", snap.Messages)
	}
	if r := c.StreamSnapshot(); len(r.Blocks) != 1 || r.Revision < last.Revision {
		t.Fatalf("reload snapshot %+v", r)
	}
	p.finish[""] <- llm.Result{Text: "итог"}
	waitIdle(t, c)
	// A late update after completion is ignored.
	call.emit(llm.Event{Kind: llm.EventAnswerText, Text: "поздний текст"})
	waitFlush()
	if r := c.StreamSnapshot(); len(r.Blocks) != 0 {
		t.Fatalf("preview not cleared: %+v", r)
	}
	if final := ev.all(); len(final[len(final)-1].Blocks) != 0 {
		t.Fatalf("renderer not told to clear preview: %+v", final[len(final)-1])
	}
	snap = c.Snapshot()
	if snap.Messages[1].Text != "итог" || snap.Messages[0].Status != chat.StatusComplete {
		t.Fatalf("final answer must come from the result: %+v", snap.Messages)
	}
}

func TestParallelPreviewsAreSeparateAndApproachStopsAfterSolution(t *testing.T) {
	c, p, _ := streamingSetup(t)
	s := c.Snapshot()
	cfg := s.Config
	cfg.ParallelAnswers = true
	if _, err := c.SaveChatConfig(ConfigInput{ChatID: s.ActiveChatID, Config: cfg, ExpectedRevision: s.ConfigRevision}); err != nil {
		t.Fatal(err)
	}
	submitText(t, c, "задача")
	calls := map[string]streamCall{}
	for range 2 {
		call := nextCall(t, p)
		calls[call.req.Purpose] = call
	}
	calls[chat.AnswerApproach].emit(llm.Event{Kind: llm.EventAnswerText, Text: "идея"})
	calls[chat.AnswerSolution].emit(llm.Event{Kind: llm.EventAnswerText, Text: "код"})
	waitFlush()
	blocks := map[string]string{}
	for _, b := range c.StreamSnapshot().Blocks {
		blocks[b.Block] = b.Text
	}
	if blocks[blockApproach] != "идея" || blocks[blockSolution] != "код" || len(blocks) != 2 {
		t.Fatalf("blocks %v", blocks)
	}
	p.finish[chat.AnswerSolution] <- llm.Result{Text: "решение"}
	waitIdle(t, c)
	calls[chat.AnswerApproach].emit(llm.Event{Kind: llm.EventAnswerText, Text: "поздняя идея"})
	waitFlush()
	if r := c.StreamSnapshot(); len(r.Blocks) != 0 {
		t.Fatalf("late approach preview shown: %+v", r)
	}
	snap := c.Snapshot()
	if snap.Messages[0].Answers[0].Text != "" || snap.Messages[0].Answers[1].Text != "решение" {
		t.Fatalf("answers %+v", snap.Messages[0].Answers)
	}
}

func TestConnectionPreparedAtStartupAndClosedOnExit(t *testing.T) {
	c, p, _ := streamingSetup(t)
	deadline := time.Now().Add(time.Second)
	for {
		p.mu.Lock()
		n := p.prepared
		p.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection not prepared at startup")
		}
		time.Sleep(time.Millisecond)
	}
	// Preparing never starts a model request.
	select {
	case call := <-p.calls:
		t.Fatalf("unexpected request %+v", call.req)
	default:
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed != 1 {
		t.Fatalf("closed %d times", p.closed)
	}
}

func TestLoginStateIsOwnedBySnapshot(t *testing.T) {
	c, _, _ := streamingSetup(t)
	snap, err := c.StartLogin()
	if err != nil || snap.Login.State != loginPending || snap.Login.Code != "ABCD-1234" || snap.Login.ID != "login-1" {
		t.Fatalf("login %+v %v", snap.Login, err)
	}
	deadline := time.Now().Add(time.Second)
	for c.Snapshot().Login.State != loginDone {
		if time.Now().After(deadline) {
			t.Fatalf("login not completed: %+v", c.Snapshot().Login)
		}
		time.Sleep(time.Millisecond)
	}
	if snap, err = c.CancelLogin(); err != nil || snap.Login != (LoginView{}) {
		t.Fatalf("dismiss %+v %v", snap.Login, err)
	}
	if a, err := c.Account(); err != nil || !a.SignedIn {
		t.Fatalf("account %+v %v", a, err)
	}
}

func TestLoginCompletedBeforeStartLoginReturnsIsKept(t *testing.T) {
	c, p, _ := streamingSetup(t)
	p.loginNow = true
	snap, err := c.StartLogin()
	if err != nil || snap.Login.State != loginDone || snap.Login.ID != "login-1" {
		t.Fatalf("login %+v %v", snap.Login, err)
	}
}
