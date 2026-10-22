package app

import (
	"context"
	"fmt"

	"github.com/97Dmitry/solveo/internal/llm"
)

// prepareChatLocked replaces the desired empty-chat snapshot without doing I/O
// under c.mu. Cancelled workers drain provider cleanup before Close completes.
func (c *Controller) prepareChatLocked() {
	if c.preparationCancel != nil {
		c.preparationCancel()
		c.preparationCancel = nil
	}
	provider, ok := c.deps.Provider.(llm.ChatPreparer)
	if !ok || c.closed || len(c.active.Messages) != 0 || len(c.active.ProviderState) != 0 || c.active.ProviderID != c.deps.Provider.Describe().ID {
		return
	}
	in := llm.ChatPreparation{ChatID: c.active.ID, Config: c.active.Config, GlobalInstructions: c.active.GlobalInstructions}
	ctx, cancel := context.WithCancel(c.ctx)
	c.preparationCancel = cancel
	c.workers.Add(1)
	go func() {
		defer c.workers.Done()
		if err := provider.PrepareChat(ctx, in); err != nil && ctx.Err() == nil {
			c.appendLog(fmt.Sprintf("Подготовка чата: %v\n", err))
		}
	}()
}
