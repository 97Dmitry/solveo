// Package hotkeys defines Solveo's global shortcuts and their portable
// "Ctrl+Alt+Shift+Win+Key" text form. Values are Win32 RegisterHotKey
// modifiers and virtual-key codes; registration itself is native.
package hotkeys

import (
	"errors"
	"fmt"
	"strings"
)

// Actions in registration order; the native host derives hotkey IDs from it.
const (
	QuickRegion = "quick-region"
	Toggle      = "toggle"
	QueueRegion = "queue-region"
	Submit      = "submit"
	QuickFull   = "quick-full"
)

var Actions = []string{QuickRegion, Toggle, QueueRegion, Submit, QuickFull}

// labels match the settings form (frontend/src/settings/HotkeySettings.tsx).
var labels = map[string]string{
	QuickRegion: "Снимок области и ответ",
	Toggle:      "Показать / скрыть окно",
	QueueRegion: "Добавить область в очередь",
	Submit:      "Отправить вопрос и очередь",
	QuickFull:   "Снимок всего экрана и ответ",
}

const (
	ModAlt   uint32 = 0x1
	ModCtrl  uint32 = 0x2
	ModShift uint32 = 0x4
	ModWin   uint32 = 0x8
)

type Binding struct {
	Action    string
	Modifiers uint32
	Key       uint32
}

func Defaults() map[string]string {
	return map[string]string{
		QuickRegion: "Ctrl+Alt+S",
		Toggle:      "Ctrl+Alt+O",
		QueueRegion: "Ctrl+Alt+A",
		Submit:      "Ctrl+Alt+Enter",
		QuickFull:   "Ctrl+Alt+F",
	}
}

// Label is the user-facing action name used in errors.
func Label(action string) string { return labels[action] }

var modifiers = []struct {
	name string
	mod  uint32
}{{"Ctrl", ModCtrl}, {"Alt", ModAlt}, {"Shift", ModShift}, {"Win", ModWin}}

// keys excludes layout-dependent punctuation and Escape, which cancels recording.
var keys = func() map[string]uint32 {
	m := map[string]uint32{
		"Enter": 0x0D, "Space": 0x20, "Tab": 0x09, "Backspace": 0x08,
		"Insert": 0x2D, "Delete": 0x2E, "Home": 0x24, "End": 0x23,
		"PageUp": 0x21, "PageDown": 0x22, "Left": 0x25, "Up": 0x26,
		"Right": 0x27, "Down": 0x28, "PrintScreen": 0x2C, "Pause": 0x13,
	}
	for c := 'A'; c <= 'Z'; c++ {
		m[string(c)] = uint32(c)
	}
	for c := '0'; c <= '9'; c++ {
		m[string(c)] = uint32(c)
	}
	for i := 1; i <= 24; i++ {
		m[fmt.Sprintf("F%d", i)] = 0x6F + uint32(i)
	}
	return m
}()

// Parse reads one shortcut and returns it in canonical modifier order.
// The empty string means the action is disabled.
func Parse(text string) (Binding, string, error) {
	if text == "" {
		return Binding{}, "", nil
	}
	parts := strings.Split(text, "+")
	key := parts[len(parts)-1]
	var mods uint32
	for _, p := range parts[:len(parts)-1] {
		found := false
		for _, m := range modifiers {
			if strings.EqualFold(p, m.name) && mods&m.mod == 0 {
				mods |= m.mod
				found = true
			}
		}
		if !found {
			return Binding{}, "", fmt.Errorf("неизвестный или повторный модификатор «%s» в «%s»", p, text)
		}
	}
	vk := uint32(0)
	for name, code := range keys {
		if strings.EqualFold(name, key) {
			key, vk = name, code
		}
	}
	if vk == 0 {
		return Binding{}, "", fmt.Errorf("клавиша «%s» не поддерживается", key)
	}
	if mods&(ModCtrl|ModAlt|ModWin) == 0 {
		return Binding{}, "", fmt.Errorf("сочетание «%s» должно включать Ctrl, Alt или Win", text)
	}
	names := []string{}
	for _, m := range modifiers {
		if mods&m.mod != 0 {
			names = append(names, m.name)
		}
	}
	return Binding{Modifiers: mods, Key: vk}, strings.Join(append(names, key), "+"), nil
}

// Normalize validates a complete set of actions and returns canonical text.
// Two actions may not share a shortcut.
func Normalize(set map[string]string) (map[string]string, error) {
	if len(set) != len(Actions) {
		return nil, errors.New("набор горячих клавиш неполный или содержит неизвестные действия")
	}
	out := map[string]string{}
	used := map[string]string{}
	for _, action := range Actions {
		text, ok := set[action]
		if !ok {
			return nil, errors.New("набор горячих клавиш неполный или содержит неизвестные действия")
		}
		_, canonical, e := Parse(text)
		if e != nil {
			return nil, fmt.Errorf("%s: %w", Label(action), e)
		}
		if other, ok := used[canonical]; ok && canonical != "" {
			return nil, fmt.Errorf("%s уже назначено действию «%s» и «%s»", canonical, Label(other), Label(action))
		}
		used[canonical] = action
		out[action] = canonical
	}
	return out, nil
}

// Bindings converts a normalized set; disabled actions are omitted.
func Bindings(set map[string]string) []Binding {
	out := []Binding{}
	for _, action := range Actions {
		b, _, e := Parse(set[action])
		if e == nil && set[action] != "" {
			b.Action = action
			out = append(out, b)
		}
	}
	return out
}
