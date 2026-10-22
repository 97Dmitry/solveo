package app

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/hotkeys"
	"github.com/97Dmitry/solveo/internal/settings"
)

// hotkeySuspendLimit bounds a recording pause the renderer failed to end.
const hotkeySuspendLimit = 60 * time.Second

// registerHotkeys replaces the native registration with set and returns a
// user-facing error per action that could not be registered. Callers hold
// settingsMu, which serializes every native hotkey change.
func (c *Controller) registerHotkeys(set map[string]string) (map[string]string, error) {
	out := map[string]string{}
	if c.deps.NativeHost == nil {
		return out, nil
	}
	failed, e := c.deps.NativeHost.SetHotkeys(hotkeys.Bindings(set))
	if e != nil {
		for _, action := range hotkeys.Actions {
			if set[action] != "" {
				out[action] = fmt.Sprintf("%s: не удалось зарегистрировать (%v)", set[action], e)
			}
		}
		return out, e
	}
	for action, err := range failed {
		out[action] = fmt.Sprintf("%s занято другим приложением или системой (%v)", set[action], err)
	}
	return out, nil
}
func (c *Controller) setHotkeyState(errs map[string]string, suspended bool) {
	c.mu.Lock()
	c.hotkeyErrors = errs
	c.hotkeysSuspended = suspended
	if !suspended && c.resumeTimer != nil {
		c.resumeTimer.Stop()
		c.resumeTimer = nil
	}
	c.changedLocked()
	c.mu.Unlock()
}

// applyHotkeysLocked registers a changed set before it is saved. On any
// conflict the previous registration is restored and nothing is saved.
func (c *Controller) applyHotkeysLocked(old, next settings.Config) error {
	errs, e := c.registerHotkeys(next.Hotkeys)
	if e == nil && len(errs) == 0 {
		c.setHotkeyState(errs, false)
		return nil
	}
	restored, _ := c.registerHotkeys(old.Hotkeys)
	c.setHotkeyState(restored, false)
	c.publish()
	if e != nil {
		return fmt.Errorf("не удалось применить горячие клавиши: %w", e)
	}
	lines := []string{}
	for _, action := range hotkeys.Actions {
		if msg, ok := errs[action]; ok {
			lines = append(lines, fmt.Sprintf("«%s»: %s", hotkeys.Label(action), msg))
		}
	}
	return errors.New("горячие клавиши не изменены. " + strings.Join(lines, "; "))
}

// SuspendHotkeys pauses global hotkeys while the renderer records a new
// shortcut: a registered combination never reaches the focused window.
// The pause ends on request, on renderer reload or after hotkeySuspendLimit.
func (c *Controller) SuspendHotkeys(suspend bool) error {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("приложение завершается")
	}
	current, suspended := c.settings.Hotkeys, c.hotkeysSuspended
	c.mu.Unlock()
	if !suspend {
		if !suspended {
			return nil
		}
		errs, e := c.registerHotkeys(current)
		c.setHotkeyState(errs, false)
		c.publish()
		return e
	}
	if c.deps.NativeHost == nil {
		return nil
	}
	if _, e := c.deps.NativeHost.SetHotkeys(nil); e != nil {
		return fmt.Errorf("не удалось приостановить горячие клавиши: %w", e)
	}
	c.mu.Lock()
	c.hotkeysSuspended = true
	if c.resumeTimer != nil {
		c.resumeTimer.Stop()
	}
	c.resumeTimer = time.AfterFunc(hotkeySuspendLimit, func() { _ = c.SuspendHotkeys(false) })
	c.changedLocked()
	c.mu.Unlock()
	c.publish()
	return nil
}
func (c *Controller) hotkeyErrorsLocked() map[string]string {
	if c.hotkeyErrors == nil {
		return map[string]string{}
	}
	return maps.Clone(c.hotkeyErrors)
}
