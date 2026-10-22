package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/97Dmitry/solveo/internal/hotkeys"
)

func TestSettingsRoundTrip(t *testing.T) {
	s := Open(t.TempDir())
	want := Config{Theme: "light", FontSize: 18, UIScale: 100, AlwaysOnTop: true, Hotkeys: hotkeys.Defaults()}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestDefaultAndLegacyFontIncreaseOnlyOnce(t *testing.T) {
	s := Open(t.TempDir())
	defaults, err := s.Load()
	if err != nil || defaults.FontSize != 17 {
		t.Fatalf("default: %+v %v", defaults, err)
	}
	if err = os.WriteFile(filepath.Join(s.Dir, "settings.json"), []byte(`{"theme":"dark","font_size":15,"always_on_top":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	migrated, err := s.Load()
	if err != nil || migrated.FontSize != 17 || !migrated.AlwaysOnTop {
		t.Fatalf("migration: %+v %v", migrated, err)
	}
	if err = s.Save(migrated); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load()
	if err != nil || !reflect.DeepEqual(loaded, migrated) {
		t.Fatalf("font grew twice: %+v %v", loaded, err)
	}
}

func TestUIScalePersistsBesideArchiveAndDefaultsForOlderSettings(t *testing.T) {
	s := Open(t.TempDir())
	for _, data := range []string{
		`{"theme":"dark","font_size":15}`,
		`{"schema_version":1,"theme":"light","font_size":19}`,
	} {
		if err := os.WriteFile(filepath.Join(s.Dir, "settings.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := s.Load()
		if err != nil || cfg.UIScale != 100 {
			t.Fatalf("legacy default: %+v %v", cfg, err)
		}
	}
	cfg, _ := s.Load()
	cfg.UIScale = 125
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Open(s.Dir).Load()
	if err != nil || loaded.UIScale != 125 || loaded.FontSize != 19 {
		t.Fatalf("restart: %+v %v", loaded, err)
	}
	for _, scale := range []int{0, 79, 151} {
		bad := cfg
		bad.UIScale = scale
		if err := s.Save(bad); err == nil {
			t.Fatalf("accepted scale %d", scale)
		}
	}
	again, err := s.Load()
	if err != nil || again.UIScale != 125 {
		t.Fatalf("invalid save overwrote settings: %+v %v", again, err)
	}
}

func TestRemovedStreamingSettingIsIgnored(t *testing.T) {
	s := Open(t.TempDir())
	if err := os.WriteFile(filepath.Join(s.Dir, "settings.json"), []byte(`{"schema_version":1,"theme":"light","font_size":18,"ui_scale":110,"experimental_streaming":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(got, Config{Theme: "light", FontSize: 18, UIScale: 110, Hotkeys: hotkeys.Defaults()}) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestHotkeysDefaultMigrateAndRejectInvalid(t *testing.T) {
	s := Open(t.TempDir())
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(s.Dir, "settings.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"schema_version":1,"theme":"light","font_size":19,"ui_scale":120,"always_on_top":true,"hotkeys":{"toggle":"alt+ctrl+h","submit":"","future":"Ctrl+Alt+Z"}}`)
	got, err := s.Load()
	want := hotkeys.Defaults()
	want[hotkeys.Toggle], want[hotkeys.Submit] = "Ctrl+Alt+H", ""
	if err != nil || !reflect.DeepEqual(got.Hotkeys, want) || got.Theme != "light" || got.FontSize != 19 || got.UIScale != 120 || !got.AlwaysOnTop {
		t.Fatalf("%+v %v", got, err)
	}
	write(`{"schema_version":1,"theme":"dark","font_size":17,"ui_scale":100,"hotkeys":{"toggle":"Ctrl+Alt+S"}}`)
	if _, err := s.Load(); err == nil {
		t.Fatal("conflicting hotkeys accepted")
	}
	bad := got
	bad.Hotkeys = map[string]string{hotkeys.Toggle: "Ctrl+Alt+H"}
	if err := s.Save(bad); err == nil {
		t.Fatal("incomplete hotkeys saved")
	}
	bad.Hotkeys = hotkeys.Defaults()
	bad.Hotkeys[hotkeys.Toggle] = "alt+ctrl+h"
	if err := s.Save(bad); err == nil {
		t.Fatal("non-canonical hotkeys saved")
	}
}
