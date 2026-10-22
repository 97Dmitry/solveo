package app

import (
	"context"
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/hotkeys"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/settings"
)

func setupHotkeys(t *testing.T, taken ...string) (*Controller, *fakeHost) {
	t.Helper()
	s, e := history.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	host := &fakeHost{taken: map[uint32]bool{}}
	for _, text := range taken {
		b, _, _ := hotkeys.Parse(text)
		host.taken[b.Modifiers<<16|b.Key] = true
	}
	f := &fakeProvider{started: make(chan llm.Request, 1), finish: make(chan error, 1), store: s}
	c, e := New(Dependencies{Store: s, SettingsStore: settings.Open(s.Dir), Provider: f, Work: openWork(t, s), NativeHost: host})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c, host
}
func registered(h *fakeHost) map[string]bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]bool{}
	for _, b := range h.hotkeys {
		out[b.Action] = true
	}
	return out
}
func TestTakenHotkeyDoesNotBlockStartup(t *testing.T) {
	c, host := setupHotkeys(t, "Ctrl+Alt+S")
	errs := c.Snapshot().HotkeyErrors
	if len(errs) != 1 || !strings.Contains(errs[hotkeys.QuickRegion], "Ctrl+Alt+S") {
		t.Fatal(errs)
	}
	if len(registered(host)) != len(hotkeys.Actions) {
		t.Fatal("other hotkeys must still be registered")
	}
}
func TestConflictingHotkeysAreNotSavedAndOldOnesRestored(t *testing.T) {
	c, host := setupHotkeys(t, "Ctrl+Alt+K")
	cfg := c.Snapshot().Settings
	cfg.Hotkeys = hotkeys.Defaults()
	cfg.Hotkeys[hotkeys.Toggle] = "ctrl+alt+k"
	_, e := c.UpdateSettings(cfg)
	if e == nil || !strings.Contains(e.Error(), "Показать / скрыть окно") {
		t.Fatal(e)
	}
	snap := c.Snapshot()
	if snap.Settings.Hotkeys[hotkeys.Toggle] != "Ctrl+Alt+O" || len(snap.HotkeyErrors) != 0 {
		t.Fatal(snap.Settings.Hotkeys, snap.HotkeyErrors)
	}
	host.mu.Lock()
	restored := host.hotkeys
	host.mu.Unlock()
	if len(restored) != len(hotkeys.Actions) || restored[1].Key != 'O' {
		t.Fatal("previous hotkeys not restored", restored)
	}
	saved, _ := settings.Open(c.deps.Store.(*history.Store).Dir).Load()
	if saved.Hotkeys[hotkeys.Toggle] != "Ctrl+Alt+O" {
		t.Fatal("conflicting hotkeys saved")
	}
	cfg.Hotkeys[hotkeys.Toggle] = "Ctrl+Alt+S"
	if _, e := c.UpdateSettings(cfg); e == nil {
		t.Fatal("duplicate within the set accepted")
	}
}
func TestChangedAndDisabledHotkeysPersist(t *testing.T) {
	c, host := setupHotkeys(t)
	cfg := c.Snapshot().Settings
	cfg.Hotkeys = hotkeys.Defaults()
	cfg.Hotkeys[hotkeys.Toggle] = "win+shift+h"
	cfg.Hotkeys[hotkeys.QuickFull] = ""
	if _, e := c.UpdateSettings(cfg); e != nil {
		t.Fatal(e)
	}
	if got := registered(host); len(got) != 4 || got[hotkeys.QuickFull] {
		t.Fatal(got)
	}
	saved, _ := settings.Open(c.deps.Store.(*history.Store).Dir).Load()
	if saved.Hotkeys[hotkeys.Toggle] != "Shift+Win+H" || saved.Hotkeys[hotkeys.QuickFull] != "" || saved.Theme != cfg.Theme {
		t.Fatal(saved)
	}
	// Unrelated settings changes keep the registration as is.
	if _, e := c.SetAlwaysOnTop(true); e != nil || len(registered(host)) != 4 {
		t.Fatal(e)
	}
}
func TestRecordingPauseEndsOnRequestAndRendererReload(t *testing.T) {
	c, host := setupHotkeys(t)
	if e := c.SuspendHotkeys(true); e != nil || len(registered(host)) != 0 {
		t.Fatal("hotkeys still registered while recording", e)
	}
	if e := c.SuspendHotkeys(false); e != nil || len(registered(host)) != len(hotkeys.Actions) {
		t.Fatal("hotkeys not restored", e)
	}
	_ = c.SuspendHotkeys(true)
	c.RendererReady()
	if len(registered(host)) != len(hotkeys.Actions) {
		t.Fatal("reload left hotkeys suspended")
	}
	c.mu.Lock()
	timer := c.resumeTimer
	c.mu.Unlock()
	if timer != nil {
		t.Fatal("resume timer left running")
	}
}
