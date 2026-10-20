package app

import (
	"context"
	"errors"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/settings"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeProvider struct {
	mu      sync.Mutex
	calls   int
	started chan llm.Request
	finish  chan error
	store   *history.Store
}

func (f *fakeProvider) Describe() llm.Descriptor {
	return llm.Descriptor{ID: "codex", Label: "fake", Capabilities: llm.Capabilities{Images: true}}
}
func (f *fakeProvider) Generate(ctx context.Context, r llm.Request, emit func(llm.Event)) (llm.Result, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	f.started <- r
	select {
	case e := <-f.finish:
		return llm.Result{Text: "answer"}, e
	case <-ctx.Done():
		return llm.Result{}, ctx.Err()
	}
}
func (f *fakeProvider) Check(context.Context, chat.Config, func(llm.Event)) (llm.CheckResult, error) {
	return llm.CheckResult{Text: "ok"}, nil
}
func setup(t *testing.T) (*Controller, *fakeProvider, *history.Store) {
	t.Helper()
	s, e := history.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	f := &fakeProvider{started: make(chan llm.Request, 5), finish: make(chan error, 5), store: s}
	c, e := New(Dependencies{Store: s, SettingsStore: settings.Open(s.Dir), Provider: f, TempDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.DiscardUnsaved(); c.Close(context.Background()) })
	return c, f, s
}
func waitIdle(t *testing.T, c *Controller) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s := c.Snapshot()
		if s.Operation.Phase == "idle" || s.Operation.Phase == "save-pending" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation not finished")
}
func TestConfigSaveNeverCallsProvider(t *testing.T) {
	c, f, s := setup(t)
	snap := c.Snapshot()
	cfg := snap.Config
	cfg.Context = "Go interview"
	_, e := c.SaveChatConfig(ConfigInput{ChatID: snap.ActiveChatID, Title: "Interview", Config: cfg, ExpectedRevision: snap.ConfigRevision})
	if e != nil {
		t.Fatal(e)
	}
	loaded, e := s.Load(snap.ActiveChatID)
	if e != nil || loaded.Config.Context != cfg.Context || len(loaded.Messages) != 0 {
		t.Fatalf("%+v %v", loaded, e)
	}
	if f.calls != 0 {
		t.Fatal("configuration invoked LLM")
	}
}
func TestPersistsBeforeGenerate(t *testing.T) {
	c, f, s := setup(t)
	snap := c.Snapshot()
	_, e := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: snap.ActiveChatID, Text: "question"})
	if e != nil {
		t.Fatal(e)
	}
	r := <-f.started
	stored, e := s.Load(snap.ActiveChatID)
	if e != nil || len(stored.Messages) != 1 || stored.Messages[0].ID != r.Turn.ID {
		t.Fatalf("%+v %v", stored, e)
	}
	f.finish <- nil
	waitIdle(t, c)
	if len(c.Snapshot().Messages) != 2 {
		t.Fatal("missing answer")
	}
}
func TestSaveFailureKeepsAnswerAndSources(t *testing.T) {
	c, f, s := setup(t)
	snap := c.Snapshot()
	_, e := c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: snap.ActiveChatID, Text: "question"})
	if e != nil {
		t.Fatal(e)
	}
	<-f.started
	backup := filepath.Join(s.Dir, "chats-backup")
	os.Rename(filepath.Join(s.Dir, "chats"), backup)
	os.WriteFile(filepath.Join(s.Dir, "chats"), []byte("blocked"), 0600)
	f.finish <- nil
	waitIdle(t, c)
	snap = c.Snapshot()
	if snap.Operation.Phase != "save-pending" || len(snap.Messages) != 2 {
		t.Fatal(snap)
	}
	if _, e = c.NewChat(); !errors.Is(e, ErrSavePending) {
		t.Fatal(e)
	}
	os.Remove(filepath.Join(s.Dir, "chats"))
	os.Rename(backup, filepath.Join(s.Dir, "chats"))
	if e = c.RetrySave(); e != nil {
		t.Fatal(e)
	}
	stored, e := s.Load(snap.ActiveChatID)
	if e != nil || len(stored.Messages) != 2 {
		t.Fatalf("%+v %v", stored, e)
	}
}
func TestDuplicateRequestAndRetryUseOneUserTurn(t *testing.T) {
	c, f, _ := setup(t)
	s := c.Snapshot()
	in := SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: "question"}
	c.Submit(in)
	<-f.started
	f.finish <- errors.New("offline")
	waitIdle(t, c)
	if _, e := c.Submit(in); !errors.Is(e, ErrStale) {
		t.Fatal(e)
	}
	s = c.Snapshot()
	id := s.Messages[0].ID
	c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, RetryMessageID: id})
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	s = c.Snapshot()
	if len(s.Messages) != 2 || len(s.Messages[0].Attempts) != 2 {
		t.Fatal(s.Messages)
	}
}
func TestCloseDuringConfigSaveDoesNotExit(t *testing.T) {
	c, _, _ := setup(t)
	c.mu.Lock()
	c.op.Phase = "saving"
	c.op.Kind = "config"
	c.mu.Unlock()
	if e := c.Close(context.Background()); !errors.Is(e, ErrBusy) {
		t.Fatalf("closed during synchronous persistence: %v", e)
	}
	c.mu.Lock()
	c.op.Phase = "idle"
	c.mu.Unlock()
}

type fakeWindow struct{ showCalls, pinCalls int }

func (w *fakeWindow) SetAlwaysOnTop(bool) { w.pinCalls++ }
func (w *fakeWindow) Show()               { w.showCalls++ }
func (*fakeWindow) Hide()                 {}
func (*fakeWindow) Toggle()               {}
func TestPinDoesNotShowWindow(t *testing.T) {
	c, _, _ := setup(t)
	w := &fakeWindow{}
	c.deps.Window = w
	if _, e := c.SetAlwaysOnTop(true); e != nil {
		t.Fatal(e)
	}
	if w.showCalls != 0 || w.pinCalls != 1 || !c.Snapshot().Settings.AlwaysOnTop {
		t.Fatal(w)
	}
}
func TestBoundedLog(t *testing.T) {
	c, _, _ := setup(t)
	c.appendLog(strings.Repeat("x", 100<<10))
	if len(c.Snapshot().Log) > 64<<10 {
		t.Fatal("unbounded log")
	}
}
func TestFastSubmitClearsAcknowledgedDraft(t *testing.T) {
	c, f, _ := setup(t)
	s := c.Snapshot()
	c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: "fast question", DraftRevision: 1})
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	if d := c.Snapshot().Draft; d.Text != "" || d.Revision != 2 {
		t.Fatalf("submitted draft was not cleared: %+v", d)
	}
	c.SetDraft(DraftInput{ChatID: s.ActiveChatID, Text: "fast question", Revision: 1})
	if c.Snapshot().Draft.Text != "" {
		t.Fatal("late debounce restored submitted text")
	}
}
func TestNewTypingSurvivesCompletion(t *testing.T) {
	c, f, _ := setup(t)
	s := c.Snapshot()
	c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: "first", DraftRevision: 1})
	<-f.started
	c.SetDraft(DraftInput{ChatID: s.ActiveChatID, Text: "next", Revision: 2})
	f.finish <- nil
	waitIdle(t, c)
	if c.Snapshot().Draft.Text != "next" {
		t.Fatal("completion erased next question")
	}
}
func TestDiscardUnsavedKeepsUnarchivedScreenshot(t *testing.T) {
	c, f, store := setup(t)
	c.deps.Capturer = fakeCapture{}
	oldID := c.Snapshot().ActiveChatID
	os.Rename(filepath.Join(store.Dir, "chats"), filepath.Join(store.Dir, "chats-old"))
	os.WriteFile(filepath.Join(store.Dir, "chats"), []byte("blocked"), 0600)
	if e := c.BeginCapture("quick-full"); e != nil {
		t.Fatal(e)
	}
	waitIdle(t, c)
	s := c.Snapshot()
	if s.Operation.Phase != "save-pending" || len(s.Queue) != 1 {
		t.Fatal(s)
	}
	path := c.queues[oldID][0].Path
	next, e := c.DiscardUnsaved()
	if e != nil {
		t.Fatal(e)
	}
	if next.ActiveChatID == oldID || len(next.Queue) != 1 {
		t.Fatal("recovery lost queue")
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("recovery deleted only screenshot copy")
	}
	if f.calls != 0 {
		t.Fatal("recovery invoked provider")
	}
}
func TestFirstQuestionBecomesTitle(t *testing.T) {
	c, f, _ := setup(t)
	s := c.Snapshot()
	c.Submit(SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: "Explain Go interfaces"})
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	if c.Snapshot().Title != "Explain Go interfaces" {
		t.Fatal("history title is not useful")
	}
}
func TestViewerRebindFailureKeepsSourceUntilExit(t *testing.T) {
	c, f, _ := setup(t)
	c.deps.Capturer = fakeCapture{}
	c.deps.NativeHost = &fakeHost{rebindErr: errors.New("host unavailable")}
	c.BeginCapture("quick-full")
	r := <-f.started
	s := c.Snapshot()
	source := c.queues[s.ActiveChatID][0].Path
	if len(r.Images) != 1 {
		t.Fatal(r)
	}
	f.finish <- nil
	waitIdle(t, c)
	c.workers.Wait()
	if _, e := os.Stat(source); e != nil {
		t.Fatal("source deleted before viewer rebind")
	}
}
