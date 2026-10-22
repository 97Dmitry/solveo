package codex

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/app"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/profiles"
	"github.com/97Dmitry/solveo/internal/settings"
	"github.com/97Dmitry/solveo/internal/workstate"
)

// Exercise the actual controller -> provider -> fake app-server -> archive path,
// including a restart. No real provider or native host is involved.
func TestControllerConsumesPreparationAndResumesExactThread(t *testing.T) {
	var env fakeEnv
	provider := fakeClient(t, &env)
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := settings.Open(store.Dir)
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.GlobalInstructions = "immutable preference"
	if err = st.Save(cfg); err != nil {
		t.Fatal(err)
	}
	work, err := workstate.Open(filepath.Join(store.Dir, "work"))
	if err != nil {
		t.Fatal(err)
	}
	controller, err := app.New(app.Dependencies{Store: store, SettingsStore: st, Work: work, Provider: provider})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close(context.Background()) })
	prepared := awaitPreparation(t, provider)
	original := controller.Snapshot()
	if count(readLog(t, env.log), "turn/start") != 0 {
		t.Fatal("startup model turn")
	}
	submit := func(c *app.Controller) {
		t.Helper()
		s := c.Snapshot()
		if _, e := c.Submit(app.SubmitInput{RequestID: chat.ID(), ChatID: s.ActiveChatID, Text: "[show-instructions] question"}); e != nil {
			t.Fatal(e)
		}
		deadline := time.After(5 * time.Second)
		for {
			s = c.Snapshot()
			if s.Operation.Phase == "idle" {
				break
			}
			select {
			case <-deadline:
				t.Fatalf("not idle: %+v", s.Operation)
			case <-time.After(time.Millisecond):
			}
		}
		if s.Error != "" || len(s.Messages) == 0 || !strings.Contains(s.Messages[len(s.Messages)-1].Text, "immutable preference") {
			t.Fatalf("lost response/preferences: %+v", s.Messages)
		}
	}
	submit(controller)
	saved, err := store.Load(original.ActiveChatID)
	if err != nil {
		t.Fatal(err)
	}
	if decodeState(t, saved.ProviderState).ThreadID != prepared.id {
		t.Fatal("controller did not consume prepared thread")
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(saved.Messages) != 2 {
		t.Fatal("preparation added history")
	}
	if err = controller.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	next := fakeClient(t, &env)
	reopened, err := app.New(app.Dependencies{Store: store, SettingsStore: st, Work: work, Provider: next})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	submit(reopened)
	saved, err = store.Load(original.ActiveChatID)
	if err != nil {
		t.Fatal(err)
	}
	if decodeState(t, saved.ProviderState).ThreadID != prepared.id || len(saved.Messages) != 4 {
		t.Fatal("restart replaced conversation")
	}
	lines := readLog(t, env.log)
	if count(lines, "thread/start") != 1 || count(lines, "thread/resume") != 1 || count(lines, "turn/start") != 2 {
		t.Fatalf("unexpected lifecycle %+v", lines)
	}
}

func TestProfileCreationPreparesWithoutModelTurn(t *testing.T) {
	var env fakeEnv
	provider := fakeClient(t, &env)
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work, err := workstate.Open(filepath.Join(store.Dir, "work"))
	if err != nil {
		t.Fatal(err)
	}
	controller, err := app.New(app.Dependencies{Store: store, SettingsStore: settings.Open(store.Dir), ProfilesStore: profiles.New(store.Dir), Provider: provider, Work: work})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close(context.Background()) })
	prepared := awaitPreparation(t, provider)
	original := controller.Snapshot()
	cfg := original.Config
	cfg.Experience = "Built billing"
	saved, err := controller.SaveProfile(app.ProfileInput{Name: "Backend", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	provider.app.mu.Lock()
	same := provider.app.prepared == prepared
	provider.app.mu.Unlock()
	if !same {
		t.Fatal("profile save disturbed current preparation")
	}
	p := saved.Profiles[0]
	created, err := controller.NewChatFromProfile(p.ID, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		next := awaitPreparation(t, provider)
		if next.key.chatID == created.ActiveChatID {
			break
		}
		select {
		case <-deadline:
			t.Fatal("profile preparation missing")
		case <-time.After(time.Millisecond):
		}
	}
	if count(readLog(t, env.log), "turn/start") != 0 {
		t.Fatal("profile creation generated a model turn")
	}
	if len(created.Messages) != 0 || created.Config.Experience != "Built billing" {
		t.Fatal("profile was not copied")
	}
}
