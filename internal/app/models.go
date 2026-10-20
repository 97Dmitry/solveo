package app

import (
	"errors"

	"github.com/97Dmitry/solveo/internal/llm"
)

// ListModels is independent of chat operations and creates no LLM turn.
// The frontend keeps a successful catalog for the lifetime of the settings form.
func (c *Controller) ListModels() ([]llm.Model, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("приложение завершается")
	}
	provider, ok := c.deps.Provider.(llm.ModelCatalog)
	if !ok {
		c.mu.Unlock()
		return nil, errors.New("подключение не поддерживает загрузку списка моделей")
	}
	c.workers.Add(1)
	ctx := c.ctx
	c.mu.Unlock()
	defer c.workers.Done()
	return provider.Models(ctx)
}
