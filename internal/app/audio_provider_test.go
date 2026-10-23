package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/settings"
	"github.com/97Dmitry/solveo/internal/stt"
)

type namedRuntime struct {
	fakeRuntime
	id string
}

func (r *namedRuntime) Describe() stt.Description {
	return stt.Description{ID: r.id, Label: "Провайдер " + r.id, Local: r.id == "local", StartLabel: "Старт " + r.id}
}

func setupTwoProviders(t *testing.T, stored string) (*Controller, *namedRuntime, *namedRuntime, *fakeSession) {
	t.Helper()
	s, e := history.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if stored != "" {
		cfg, _ := settings.Open(s.Dir).Load()
		cfg.Audio.Provider = stored
		if err := settings.Open(s.Dir).Save(cfg); err != nil {
			t.Fatal(err)
		}
	}
	session := newFakeSession()
	local := &namedRuntime{fakeRuntime: fakeRuntime{checks: readyChecks(), session: session}, id: "local"}
	cloud := &namedRuntime{fakeRuntime: fakeRuntime{checks: readyChecks(), session: session}, id: "cloud"}
	c, e := New(Dependencies{
		Store: s, SettingsStore: settings.Open(s.Dir), Work: openWork(t, s),
		Provider: &fakeProvider{started: make(chan llm.Request, 5), finish: make(chan error, 5), store: s},
		Audio:    AudioDeps{Providers: []stt.Provider{local, cloud}, Capturer: newFakeCapturer()},
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c, local, cloud, session
}

func TestSelectedProviderIsUsedForStart(t *testing.T) {
	c, local, cloud, _ := setupTwoProviders(t, "")
	if c.AudioSnapshot().Provider.ID != "local" {
		t.Fatal("the first provider is the default")
	}
	if err := c.SetAudioProvider("cloud"); err != nil {
		t.Fatal(err)
	}
	if v := c.AudioSnapshot(); v.Provider.ID != "cloud" || v.Provider.StartLabel != "Старт cloud" || len(v.Checks) != 0 {
		t.Fatalf("view after switch %+v", v.Provider)
	}
	if got := c.Snapshot().Settings.Audio.Provider; got != "cloud" {
		t.Fatalf("choice not saved: %q", got)
	}
	if err := c.StartAudioModel(); err != nil {
		t.Fatal(err)
	}
	waitAudio(t, c, "ready", func(v AudioView) bool { return v.Model == "ready" && v.Busy == "" })
	if local.started != 0 || cloud.started != 1 {
		t.Fatalf("started local %d cloud %d", local.started, cloud.started)
	}
}

func TestProviderChangeRequiresStop(t *testing.T) {
	c, _, _, _ := setupTwoProviders(t, "")
	if err := c.StartAudioModel(); err != nil {
		t.Fatal(err)
	}
	waitAudio(t, c, "ready", func(v AudioView) bool { return v.Model == "ready" && v.Busy == "" })
	if err := c.SetAudioProvider("cloud"); err == nil || !strings.Contains(err.Error(), "Сначала остановите распознавание") {
		t.Fatalf("switch while running: %v", err)
	}
	if err := c.SetAudioProvider("nope"); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

func TestUnknownStoredProviderFallsBack(t *testing.T) {
	c, _, _, _ := setupTwoProviders(t, "removed-provider")
	if got := c.AudioSnapshot().Provider.ID; got != "local" {
		t.Fatalf("fell back to %q", got)
	}
	c2, _, _, _ := setupTwoProviders(t, "cloud")
	if got := c2.AudioSnapshot().Provider.ID; got != "cloud" {
		t.Fatalf("stored choice ignored: %q", got)
	}
}

func TestUpdateSettingsKeepsTheProviderChoice(t *testing.T) {
	c, _, _, _ := setupTwoProviders(t, "cloud")
	cfg := c.Snapshot().Settings
	cfg.Audio.Provider = "local"
	cfg.Audio.ContextPhrases = []string{"JWT"}
	if _, err := c.UpdateSettings(cfg); err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot().Settings.Audio; got.Provider != "cloud" || len(got.ContextPhrases) != 0 {
		t.Fatalf("settings %+v", got)
	}
	if c.AudioSnapshot().Provider.ID != "cloud" {
		t.Fatal("provider changed outside SetAudioProvider")
	}
}

func TestDictionaryChangesOnlyFromSpeechWindow(t *testing.T) {
	c, _, _, _ := setupTwoProviders(t, "")
	if err := c.SetContextPhrases([]string{" Kafka ", "gRPC", "Kafka", ""}); err != nil {
		t.Fatal(err)
	}
	if got := c.AudioWindowTarget().Dictionary; !slices.Equal(got, []string{"Kafka", "gRPC"}) {
		t.Fatalf("dictionary %v", got)
	}
	// A main-window save carrying an older dictionary must not overwrite the speech window's edit.
	cfg := c.Snapshot().Settings
	cfg.Audio.ContextPhrases = nil
	cfg.Theme = "light"
	if _, err := c.UpdateSettings(cfg); err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot().Settings; got.Theme != "light" || !slices.Equal(got.Audio.ContextPhrases, []string{"Kafka", "gRPC"}) {
		t.Fatalf("settings %+v", got)
	}
	if err := c.SetContextPhrases([]string{strings.Repeat("x", 129)}); err == nil {
		t.Fatal("overlong term accepted")
	}
	if got := c.AudioWindowTarget().Dictionary; len(got) != 2 {
		t.Fatalf("rejected dictionary replaced the saved one: %v", got)
	}
	if err := c.SetContextPhrases(nil); err != nil || c.AudioWindowTarget().Dictionary == nil {
		t.Fatal("cleared dictionary must stay an empty list", err)
	}
}

func TestProviderSwitchWaitsForRunningWork(t *testing.T) {
	c, local, _, _ := setupTwoProviders(t, "")
	local.gate = make(chan struct{})
	if err := c.PrepareAudio(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetAudioProvider("cloud"); err == nil {
		t.Fatal("switched while a preparation was running")
	}
	close(local.gate)
	waitAudio(t, c, "prepared", func(v AudioView) bool { return v.Busy == "" })
	if c.AudioSnapshot().Provider.ID != "local" {
		t.Fatal("provider changed under running work")
	}
	if err := c.SetAudioProvider("cloud"); err != nil {
		t.Fatal(err)
	}
	if v := c.AudioSnapshot(); v.Provider.ID != "cloud" || v.Busy != "" {
		t.Fatalf("after switch %+v busy %q", v.Provider, v.Busy)
	}
}

// Run with -race: the provider of a starting session must be chosen under the audio lock.
func TestProviderSwitchAndStartDoNotRace(t *testing.T) {
	c, _, _, _ := setupTwoProviders(t, "")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 50 {
			_ = c.SetAudioProvider([]string{"cloud", "local"}[i%2])
		}
	}()
	for range 50 {
		if c.StartAudioModel() == nil {
			waitAudio(t, c, "settled", func(v AudioView) bool { return v.Busy == "" })
			v := c.AudioSnapshot()
			if v.Model == "ready" {
				_ = c.StopAudioModel()
				waitAudio(t, c, "stopped", func(v AudioView) bool { return v.Busy == "" })
			}
		}
	}
	<-done
}
