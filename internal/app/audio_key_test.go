package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/history"
	"github.com/97Dmitry/solveo/internal/llm"
	"github.com/97Dmitry/solveo/internal/secrets"
	"github.com/97Dmitry/solveo/internal/settings"
	"github.com/97Dmitry/solveo/internal/stt"
)

type keyedRuntime struct{ fakeRuntime }

func (*keyedRuntime) Describe() stt.Description {
	return stt.Description{ID: "cloud", Label: "Облако", NeedsKey: true, StartLabel: "Подключить", StopLabel: "Отключить", MaxPhrases: 100, MaxPhraseRunes: 50}
}

func setupKeyed(t *testing.T) (*Controller, secrets.Store, *[]string) {
	t.Helper()
	s, e := history.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	store := secrets.NewMemory()
	var events []string
	c, e := New(Dependencies{
		Store: s, SettingsStore: settings.Open(s.Dir), Work: openWork(t, s),
		Provider: &fakeProvider{started: make(chan llm.Request, 5), finish: make(chan error, 5), store: s},
		Audio:    AudioDeps{Providers: []stt.Provider{&keyedRuntime{fakeRuntime: fakeRuntime{checks: readyChecks()}}}, Capturer: newFakeCapturer(), Secrets: store},
		Emit: func(name string, data interface{}) {
			b, _ := json.Marshal(data)
			events = append(events, string(b))
		},
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c, store, &events
}

func TestSTTKeyIsStoredAndNeverPublished(t *testing.T) {
	c, store, events := setupKeyed(t)
	if c.AudioSnapshot().KeyConfigured {
		t.Fatal("no key yet")
	}
	if err := c.SetSTTKey("cloud", "  xai-very-secret  "); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := store.Get(stt.KeyName("cloud")); !ok || v != "xai-very-secret" {
		t.Fatalf("stored %q %v", v, ok)
	}
	view := c.AudioSnapshot()
	if !view.KeyConfigured {
		t.Fatal("key not reported as configured")
	}
	snapshot, _ := json.Marshal(c.Snapshot())
	audio, _ := json.Marshal(view)
	for _, text := range append([]string{string(snapshot), string(audio)}, *events...) {
		if strings.Contains(text, "xai-very-secret") {
			t.Fatal("the key reached the renderer")
		}
	}
	if err := c.DeleteSTTKey("cloud"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get(stt.KeyName("cloud")); ok || c.AudioSnapshot().KeyConfigured {
		t.Fatal("key not deleted")
	}
}

func TestSTTKeyValidation(t *testing.T) {
	c, _, _ := setupKeyed(t)
	long := strings.Repeat("k", 600)
	cases := map[string][2]string{
		"empty":   {"cloud", "   "},
		"long":    {"cloud", long},
		"unknown": {"nope", "key"},
	}
	for name, tc := range cases {
		err := c.SetSTTKey(tc[0], tc[1])
		if err == nil {
			t.Errorf("%s: expected an error", name)
		} else if strings.Contains(err.Error(), "kkkk") {
			t.Errorf("%s: error repeats the key", name)
		}
	}
}

func TestKeyChangeRefreshesChecks(t *testing.T) {
	c, _, _ := setupKeyed(t)
	if len(c.AudioSnapshot().Checks) != 0 {
		t.Fatal("no checks before the first refresh")
	}
	if err := c.SetSTTKey("cloud", "xai-key"); err != nil {
		t.Fatal(err)
	}
	waitAudio(t, c, "checks after saving the key", func(v AudioView) bool { return len(v.Checks) > 0 && v.Busy == "" })
}
