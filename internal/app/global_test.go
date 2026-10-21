package app

import (
	"context"
	"testing"

	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/settings"
)

func TestGlobalSnapshotOnStartupAndNewChat(t *testing.T) {
	store, err := history.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ss := settings.Open(store.Dir)
	cfg, _ := ss.Load()
	cfg.GlobalInstructions = "original"
	if err = ss.Save(cfg); err != nil {
		t.Fatal(err)
	}
	f := &fakeProvider{started: make(chan llm.Request, 5), finish: make(chan error, 5)}
	c, err := New(Dependencies{Store: store, SettingsStore: ss, Provider: f, Work: openWork(t, store)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	first := c.Snapshot().ActiveChatID
	cfg.GlobalInstructions = "next"
	if _, err = c.UpdateSettings(cfg); err != nil {
		t.Fatal(err)
	}
	old, _ := store.Load(first)
	if old.GlobalInstructions != "original" {
		t.Fatalf("existing empty chat changed: %+v", old)
	}
	next, err := c.NewChat()
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := store.Load(next.ActiveChatID)
	if doc.GlobalInstructions != "next" {
		t.Fatal("new snapshot missing")
	}
	if f.calls != 0 {
		t.Fatal("settings called model")
	}
}

func setGlobal(t *testing.T, c *Controller, text string) {
	t.Helper()
	cfg := c.Snapshot().Settings
	cfg.GlobalInstructions = text
	if _, err := c.UpdateSettings(cfg); err != nil {
		t.Fatal(err)
	}
}
