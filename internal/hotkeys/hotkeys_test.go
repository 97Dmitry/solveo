package hotkeys

import (
	"strings"
	"testing"
)

func TestParseCanonicalizes(t *testing.T) {
	for in, want := range map[string]struct {
		text string
		mods uint32
		key  uint32
	}{
		"Ctrl+Alt+S":        {"Ctrl+Alt+S", ModCtrl | ModAlt, 'S'},
		"alt+ctrl+enter":    {"Ctrl+Alt+Enter", ModCtrl | ModAlt, 0x0D},
		"Shift+Win+F12":     {"Shift+Win+F12", ModShift | ModWin, 0x7B},
		"Ctrl+Shift+1":      {"Ctrl+Shift+1", ModCtrl | ModShift, '1'},
		"Win+PrintScreen":   {"Win+PrintScreen", ModWin, 0x2C},
		"Ctrl+Alt+pagedown": {"Ctrl+Alt+PageDown", ModCtrl | ModAlt, 0x22},
	} {
		b, text, err := Parse(in)
		if err != nil || text != want.text || b.Modifiers != want.mods || b.Key != want.key {
			t.Fatalf("%s: %+v %q %v", in, b, text, err)
		}
	}
}
func TestParseRejectsUnsafeShortcuts(t *testing.T) {
	for _, in := range []string{"S", "Shift+S", "Ctrl+Ctrl+S", "Ctrl+Alt+Escape", "Ctrl+Alt+;", "Hyper+S", "Ctrl+", "Ctrl+Alt+S+X"} {
		if _, _, err := Parse(in); err == nil {
			t.Fatal("accepted", in)
		}
	}
	if b, text, err := Parse(""); err != nil || text != "" || b.Key != 0 {
		t.Fatal("empty must disable")
	}
}
func TestNormalizeDetectsConflictsAndGaps(t *testing.T) {
	set := Defaults()
	got, err := Normalize(set)
	if err != nil || got[Submit] != "Ctrl+Alt+Enter" {
		t.Fatal(got, err)
	}
	set[QuickFull] = "alt+ctrl+s"
	if _, err := Normalize(set); err == nil || !strings.Contains(err.Error(), "Снимок области и ответ") || !strings.Contains(err.Error(), "Снимок всего экрана и ответ") {
		t.Fatal(err)
	}
	set = Defaults()
	set[QuickFull], set[Toggle] = "", ""
	got, err = Normalize(set)
	if err != nil || len(Bindings(got)) != 3 {
		t.Fatal("disabled actions must not conflict", got, err)
	}
	set = Defaults()
	delete(set, Submit)
	set["unknown"] = "Ctrl+Alt+U"
	if _, err := Normalize(set); err == nil {
		t.Fatal("unknown action accepted")
	}
}
