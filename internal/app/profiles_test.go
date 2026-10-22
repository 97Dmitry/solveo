package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/profiles"
	"github.com/97Dmitry/solveo/internal/settings"
)

func setupProfiles(t *testing.T) (*Controller, *fakeProvider, *history.Store) {
	t.Helper()
	s, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeProvider{started: make(chan llm.Request, 5), finish: make(chan error, 5)}
	c, err := New(Dependencies{Store: s, SettingsStore: settings.Open(s.Dir), ProfilesStore: profiles.New(s.Dir), Provider: f, Work: openWork(t, s)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c, f, s
}
func TestProfileCopyGlobalsAndPersistence(t *testing.T) {
	c, f, store := setupProfiles(t)
	original := c.Snapshot()
	cfg := original.Config
	cfg.Position = "Backend"
	cfg.Experience = "Built billing"
	cfg.ParallelAnswers = true
	cfg.FastModel = "light"
	cfg.Model = "strong"
	cfg.Reasoning = "high"
	snap, err := c.SaveProfile(ProfileInput{Name: " Interview ", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Profiles) != 1 || snap.Profiles[0].Name != "Interview" || snap.ActiveChatID != original.ActiveChatID || snap.Config != original.Config {
		t.Fatalf("profile changed chat: %+v", snap)
	}
	p := snap.Profiles[0]
	setGlobal(t, c, "new global")
	if err = c.SetDraft(DraftInput{ChatID: original.ActiveChatID, Text: "keep draft", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	created, err := c.NewChatFromProfile(p.ID, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.Load(created.ActiveChatID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Config != cfg || doc.GlobalInstructions != "new global" || doc.Title != "Interview" || len(doc.Messages) != 0 || len(doc.ProviderState) != 0 {
		t.Fatalf("bad copy: %+v", doc)
	}
	cfg.Experience = "changed"
	updated, err := c.SaveProfile(ProfileInput{ID: p.ID, ExpectedRevision: 1, Name: p.Name, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Profiles[0].Revision != 2 || updated.Config.Experience != "Built billing" {
		t.Fatal("profile update mutated chat")
	}
	if _, err = c.DeleteProfile(p.ID, 1); !errors.Is(err, ErrStale) {
		t.Fatal("stale delete accepted", err)
	}
	if _, err = c.NewChatFromProfile(p.ID, 1); !errors.Is(err, ErrStale) {
		t.Fatal("stale apply accepted", err)
	}
	list, err := profiles.New(store.Dir).Load()
	if err != nil || len(list) != 1 || list[0].Config.Experience != "changed" {
		t.Fatalf("not persisted: %+v %v", list, err)
	}
	if _, err = c.DeleteProfile(p.ID, 2); err != nil {
		t.Fatal(err)
	}
	doc, err = store.Load(created.ActiveChatID)
	if err != nil || doc.Config.Experience != "Built billing" {
		t.Fatal("deletion mutated chat")
	}
	back, err := c.OpenChat(original.ActiveChatID)
	if err != nil || back.Draft.Text != "keep draft" {
		t.Fatalf("lost old draft: %+v %v", back.Draft, err)
	}
	if f.calls != 0 {
		t.Fatal("profile CRUD generated answers")
	}
}
func TestCorruptProfileLibraryDoesNotBlockChat(t *testing.T) {
	c, f, s := setupProfiles(t)
	if err := os.WriteFile(filepath.Join(s.Dir, "profiles.json"), []byte(`{"version":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReloadProfiles(); err == nil {
		t.Fatal("future library accepted")
	}
	if c.Snapshot().ProfilesError == "" {
		t.Fatal("missing library error")
	}
	if _, err := c.SaveProfile(ProfileInput{Name: "x", Config: chat.New().Config}); err == nil {
		t.Fatal("overwrote future library")
	}
	snap := c.Snapshot()
	if _, err := c.Submit(SubmitInput{ChatID: snap.ActiveChatID, RequestID: chat.ID(), Text: "question"}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	f.finish <- nil
	waitIdle(t, c)
	b, err := os.ReadFile(filepath.Join(s.Dir, "profiles.json"))
	if err != nil || string(b) != `{"version":99}` {
		t.Fatal("lost future document")
	}
}

type failingProfiles struct {
	ProfilesStore
	fail bool
}

func (s *failingProfiles) Save(p []profiles.Profile) error {
	if s.fail {
		return errors.New("write failed")
	}
	return s.ProfilesStore.Save(p)
}
func TestProfileWriteFailureAndBusy(t *testing.T) {
	c, _, _ := setupProfiles(t)
	store := &failingProfiles{ProfilesStore: c.deps.ProfilesStore}
	c.deps.ProfilesStore = store
	first, err := c.SaveProfile(ProfileInput{Name: "first", Config: chat.New().Config})
	if err != nil {
		t.Fatal(err)
	}
	p := first.Profiles[0]
	store.fail = true
	if _, err = c.SaveProfile(ProfileInput{ID: p.ID, ExpectedRevision: 1, Name: "changed", Config: p.Config}); err == nil {
		t.Fatal("write failure hidden")
	}
	if got := c.Snapshot().Profiles[0]; got != p {
		t.Fatal("failed write changed accepted version")
	}
	c.mu.Lock()
	c.op.Phase = phaseRequesting
	c.mu.Unlock()
	if _, err = c.DeleteProfile(p.ID, 1); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if _, err = c.NewChatFromProfile(p.ID, 1); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.op.Phase = phaseIdle
	c.mu.Unlock()
}

func TestProfileArchiveFailureKeepsActiveChatAndQueue(t *testing.T) {
	c, _, s := setupProfiles(t)
	snap := c.Snapshot()
	saved, err := c.SaveProfile(ProfileInput{Name: "profile", Config: snap.Config})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetDraft(DraftInput{ChatID: snap.ActiveChatID, Text: "keep", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.queues[snap.ActiveChatID] = []queued{{ID: "retained", Label: "queue"}}
	c.mu.Unlock()
	// A file at the archive destination makes every new-chat Save fail.
	if err = os.Rename(filepath.Join(s.Dir, "chats"), filepath.Join(s.Dir, "chats-old")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Dir, "chats"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	p := saved.Profiles[0]
	if _, err = c.NewChatFromProfile(p.ID, p.Revision); err == nil {
		t.Fatal("archive error hidden")
	}
	got := c.Snapshot()
	if got.ActiveChatID != snap.ActiveChatID || got.Draft.Text != "keep" || len(got.Queue) != 1 {
		t.Fatal("failed creation lost active work")
	}
	// Restore temporary archive for the normal shutdown path.
	if err = os.Remove(filepath.Join(s.Dir, "chats")); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(s.Dir, "chats-old"), filepath.Join(s.Dir, "chats")); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	delete(c.queues, snap.ActiveChatID)
	c.mu.Unlock()
}

func TestProfileConfigFrozenForBothRequestsAndRetry(t *testing.T) {
	c, _, _ := setupProfiles(t)
	p := &parallelProvider{started: make(chan llm.Request, 10), approach: make(chan answerCompletion, 5), solution: make(chan answerCompletion, 5)}
	c.deps.Provider = p
	cfg := c.Snapshot().Config
	cfg.ParallelAnswers = true
	cfg.Experience = "original experience"
	cfg.Position = "Go developer"
	saved, err := c.SaveProfile(ProfileInput{Name: "interview", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	profile := saved.Profiles[0]
	snap, err := c.NewChatFromProfile(profile.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Submit(SubmitInput{ChatID: snap.ActiveChatID, RequestID: chat.ID(), Text: "experience?"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		r := nextParallelRequest(t, p)
		if r.Config.Experience != "original experience" || r.Config.Position != "Go developer" {
			t.Fatal("lost interview facts in request")
		}
	}
	p.approach <- answerCompletion{result: llm.Result{Text: "preliminary"}}
	waitApproach(t, c)
	p.solution <- answerCompletion{err: errors.New("retry me")}
	waitIdle(t, c)
	snap = c.Snapshot()
	cfg = snap.Config
	cfg.Experience = "new experience"
	if _, err = c.SaveChatConfig(ConfigInput{ChatID: snap.ActiveChatID, Title: snap.Title, Config: cfg, ExpectedRevision: snap.ConfigRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Submit(SubmitInput{ChatID: snap.ActiveChatID, RequestID: chat.ID(), RetryMessageID: snap.Messages[0].ID}); err != nil {
		t.Fatal(err)
	}
	r := nextParallelRequest(t, p)
	if r.Config.Experience != "original experience" || r.Purpose != chat.AnswerSolution {
		t.Fatal("retry replaced frozen profile")
	}
	p.solution <- answerCompletion{result: llm.Result{Text: "answer"}}
	waitIdle(t, c)
}

func TestProfilesSurviveControllerRestart(t *testing.T) {
	c, f, s := setupProfiles(t)
	cfg := c.Snapshot().Config
	cfg.Experience = "resume"
	cfg.Stack = "Go SQL"
	saved, err := c.SaveProfile(ProfileInput{Name: "restart", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Dependencies{Store: s, SettingsStore: settings.Open(s.Dir), ProfilesStore: profiles.New(s.Dir), Provider: f, Work: openWork(t, s)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close(context.Background()) }()
	if got := reopened.Snapshot().Profiles; len(got) != 1 || got[0].Config != cfg {
		t.Fatalf("lost profile: %+v", got)
	}
	p := saved.Profiles[0]
	first, err := reopened.NewChatFromProfile(p.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	setGlobal(t, reopened, "later global")
	second, err := reopened.NewChatFromProfile(p.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	firstDoc, err := s.Load(first.ActiveChatID)
	if err != nil {
		t.Fatal(err)
	}
	secondDoc, err := s.Load(second.ActiveChatID)
	if err != nil {
		t.Fatal(err)
	}
	if firstDoc.ID == secondDoc.ID || firstDoc.GlobalInstructions != "" || secondDoc.GlobalInstructions != "later global" || firstDoc.Config != cfg || secondDoc.Config != cfg {
		t.Fatal("shared chat snapshots")
	}
}
