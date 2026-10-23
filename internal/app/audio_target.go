package app

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
}

func (c *Controller) AudioWindowTarget() AudioTarget {
	c.mu.Lock()
	defer c.mu.Unlock()
	idle := c.op.Phase == phaseIdle && !c.closed
	return AudioTarget{Question: c.questionSnapshotLocked(), Pinned: c.settings.AlwaysOnTop, ChatID: c.active.ID, Title: c.active.Title, CanSend: idle && c.pending == nil && c.active.ProviderID == c.deps.Provider.Describe().ID, Disabled: !idle, Theme: c.settings.Theme, UIScale: c.settings.UIScale}
}
