package llm

import (
	"context"

	"github.com/97Dmitry/solveo/internal/chat"
)

// ChatPreparation is a snapshot for an empty local chat; it contains no question
// or attachments and must never start a model turn or generate an answer.
type ChatPreparation struct {
	ChatID             string
	Config             chat.Config
	GlobalInstructions string
}

// ChatPreparer is optional. PrepareChat owns its resource until Generate claims
// it or ctx is canceled, then releases unused resources before returning. Calls
// run on controller workers, never on the UI thread. State is never archived.
type ChatPreparer interface {
	PrepareChat(context.Context, ChatPreparation) error
}
