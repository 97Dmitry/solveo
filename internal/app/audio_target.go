package app

import (
	"slices"

	"github.com/97Dmitry/solveo/internal/hotkeys"
)

// AudioTarget contains no history or draft; the auxiliary renderer polls it frequently.
type AudioTarget struct {
	Question QuestionView
	Pinned   bool
	ChatID   string
	Title    string
	CanSend  bool
	Disabled bool
	Theme    string
	UIScale  int
	// Dictionary is the saved recognition dictionary edited in the speech window.
	Dictionary []string
	// SendHotkey is the shortcut of «send new phrases», shown as a hint ("" when unassigned).
	SendHotkey string
}

func (c *Controller) AudioWindowTarget() AudioTarget {
	c.mu.Lock()
	defer c.mu.Unlock()
	idle := c.op.Phase == phaseIdle && !c.closed
	dictionary := slices.Clone(c.settings.Audio.ContextPhrases)
	if dictionary == nil {
		dictionary = []string{}
	}
	return AudioTarget{Question: c.questionSnapshotLocked(), Pinned: c.settings.AlwaysOnTop, ChatID: c.active.ID, Title: c.active.Title, CanSend: idle && c.pending == nil && c.active.ProviderID == c.deps.Provider.Describe().ID, Disabled: !idle, Theme: c.settings.Theme, UIScale: c.settings.UIScale, Dictionary: dictionary, SendHotkey: c.settings.Hotkeys[hotkeys.SendTranscript]}
}

// SetContextPhrases saves the recognition dictionary. It is the only writer of
// Audio.ContextPhrases; the list applies at the next listening start.
func (c *Controller) SetContextPhrases(phrases []string) error {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	c.mu.Lock()
	cfg := c.settings
	c.mu.Unlock()
	cfg.Audio.ContextPhrases = slices.Clone(phrases)
	_, err := c.saveSettingsLocked(cfg)
	return err
}
