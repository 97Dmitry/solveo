package app

import (
	"context"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/settings"
)

type preparationCall struct {
	snapshot llm.ChatPreparation
	ctx      context.Context
}
type preparingProvider struct {
	*fakeProvider
	preparations chan preparationCall
}

func (p *preparingProvider) PrepareChat(ctx context.Context, in llm.ChatPreparation) error {
	select {
	case p.preparations <- preparationCall{in, ctx}:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}
func preparingSetup(t *testing.T) (*Controller, *preparingProvider, *history.Store) {
	t.Helper()
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &preparingProvider{fakeProvider: &fakeProvider{started: make(chan llm.Request, 5), finish: make(chan error, 5), store: store}, preparations: make(chan preparationCall, 10)}
	c, err := New(Dependencies{Store: store, SettingsStore: settings.Open(store.Dir), Provider: p, Work: openWork(t, store)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c, p, store
}
func nextPreparation(t *testing.T, p *preparingProvider) preparationCall {
	t.Helper()
	select {
	case call := <-p.preparations:
		return call
	case <-time.After(time.Second):
		t.Fatal("no preparation")
		return preparationCall{}
	}
}
func canceledPreparation(t *testing.T, call preparationCall) {
	t.Helper()
	select {
	case <-call.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stale preparation survived")
	}
}
func TestPreparationFollowsChatAndConfigSnapshots(t *testing.T) {
	c, p, store := preparingSetup(t)
	initial := nextPreparation(t, p)
	s := c.Snapshot()
	if initial.snapshot.ChatID != s.ActiveChatID || initial.snapshot.Config != s.Config {
		t.Fatal("wrong initial snapshot")
	}
	before, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	setGlobal(t, c, "new chats only")
	cfg := s.Config
	cfg.Model = "other-model"
	cfg.Reasoning = "high"
	cfg.Instructions = "new local"
	if _, err = c.SaveChatConfig(ConfigInput{ChatID: s.ActiveChatID, ExpectedRevision: s.ConfigRevision, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	canceledPreparation(t, initial)
	changed := nextPreparation(t, p)
	if changed.snapshot.Config != cfg || changed.snapshot.GlobalInstructions != "" {
		t.Fatal("config/global snapshot drift")
	}
	if _, err = c.NewChat(); err != nil {
		t.Fatal(err)
	}
	canceledPreparation(t, changed)
	next := nextPreparation(t, p)
	if next.snapshot.GlobalInstructions != "new chats only" || next.snapshot.ChatID == s.ActiveChatID {
		t.Fatal("new chat snapshot lost")
	}
	if _, err = c.OpenChat(s.ActiveChatID); err != nil {
		t.Fatal(err)
	}
	canceledPreparation(t, next)
	reopened := nextPreparation(t, p)
	if reopened.snapshot.ChatID != s.ActiveChatID || reopened.snapshot.Config != cfg {
		t.Fatal("wrong reopened preparation")
	}
	after, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 || p.calls != 0 {
		t.Fatal("background preparation added history/model turns")
	}
	if err = c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	canceledPreparation(t, reopened)
}

func (*preparingProvider) Prepare(context.Context) error { return nil }
func (*preparingProvider) Close(context.Context) error   { return nil }
func (*preparingProvider) Account(context.Context) (llm.Account, error) {
	return llm.Account{SignedIn: true}, nil
}
func (*preparingProvider) StartLogin(_ context.Context, done func(error)) (llm.LoginPrompt, error) {
	done(nil)
	return llm.LoginPrompt{ID: "login", URL: "https://auth.example.test", Code: "code"}, nil
}
func (*preparingProvider) CancelLogin(context.Context, string) error { return nil }
func TestSuccessfulLoginRestartsEmptyChatPreparation(t *testing.T) {
	c, p, _ := preparingSetup(t)
	old := nextPreparation(t, p)
	if _, err := c.StartLogin(); err != nil {
		t.Fatal(err)
	}
	canceledPreparation(t, old)
	next := nextPreparation(t, p)
	if next.snapshot != old.snapshot {
		t.Fatal("login changed the preparation snapshot")
	}
}

func TestConnectionCheckRestoresEmptyChatPreparation(t *testing.T) {
	c, p, _ := preparingSetup(t)
	old := nextPreparation(t, p)
	if _, err := c.CheckConnection(); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, c)
	canceledPreparation(t, old)
	next := nextPreparation(t, p)
	if next.snapshot != old.snapshot {
		t.Fatal("check changed the preparation snapshot")
	}
}
