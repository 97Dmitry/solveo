package llm

import (
	"context"

	"github.com/97Dmitry/solveo/internal/chat"
)

// ModelCatalog is optional: each provider discovers its own available models.
type ModelCatalog interface {
	Models(context.Context) ([]Model, error)
}
type ReasoningEffort struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}
type Model struct {
	ID               string            `json:"id"`
	Label            string            `json:"label"`
	Description      string            `json:"description"`
	IsDefault        bool              `json:"is_default"`
	DefaultReasoning string            `json:"default_reasoning"`
	ReasoningEfforts []ReasoningEffort `json:"reasoning_efforts"`
}

// ValidReasoning accepts provider-advertised tokens without a fixed effort enum.
// Restricted tokens are safe in Codex's TOML config override argument.
func ValidReasoning(value string) bool { return chat.ValidReasoning(value) }
