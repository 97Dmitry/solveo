package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	s := Open(t.TempDir())
	want := Config{Theme: "light", FontSize: 18, UIScale: 100, AlwaysOnTop: true}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || got != want {
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
	if err != nil || loaded != migrated {
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
	if err != nil || got != (Config{Theme: "light", FontSize: 18, UIScale: 110}) {
		t.Fatalf("%+v %v", got, err)
	}
}
