package settings

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/hotkeys"
)

func TestAudioDictionaryDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"schema_version":1,"theme":"dark","font_size":17,"ui_scale":100}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Open(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Audio.ContextPhrases == nil || len(c.Audio.ContextPhrases) != 0 {
		t.Fatalf("old settings must load an empty, non-nil dictionary: %#v", c.Audio.ContextPhrases)
	}
}

func TestAudioDictionaryNormalizesAndRoundTrips(t *testing.T) {
	c, err := Normalize(Config{Theme: "dark", FontSize: 17, UIScale: 100, Hotkeys: hotkeys.Defaults(), Audio: Audio{ContextPhrases: []string{"  NestJS ", "", "JWT", "NestJS", "PostgreSQL"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Audio.ContextPhrases, []string{"NestJS", "JWT", "PostgreSQL"}) {
		t.Fatalf("normalized %v", c.Audio.ContextPhrases)
	}
	store := Open(t.TempDir())
	if err := store.Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Audio.ContextPhrases, c.Audio.ContextPhrases) {
		t.Fatalf("round trip %v", got.Audio.ContextPhrases)
	}
}

func TestAudioDictionaryLimits(t *testing.T) {
	base := Config{Theme: "dark", FontSize: 17, UIScale: 100, Hotkeys: hotkeys.Defaults()}
	many := make([]string, 257)
	for i := range many {
		many[i] = "t" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	cases := map[string][]string{
		"too many": many,
		"too long": {strings.Repeat("ж", 129)},
		"NUL":      {"a\x00b"},
		"invalid":  {string([]byte{0xff})},
		"total bytes": func() []string {
			var out []string
			for i := range 40 {
				out = append(out, strings.Repeat("я", 50)+string(rune('A'+i)))
			}
			return out
		}(),
	}
	for name, phrases := range cases {
		c := base
		c.Audio.ContextPhrases = phrases
		if err := Validate(c); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	ok := base
	ok.Audio.ContextPhrases = []string{strings.Repeat("ж", 128)}
	if err := Validate(ok); err != nil {
		t.Fatalf("128 code points must pass: %v", err)
	}
}
