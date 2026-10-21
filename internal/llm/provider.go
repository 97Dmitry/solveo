// Package llm defines the application-owned provider boundary.
package llm

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/97Dmitry/solveo/internal/chat"
)

var ErrOutcomeUnknown = errors.New("исход запроса неизвестен")

type Capabilities struct {
	Images    bool `json:"images"`
	Reasoning bool `json:"reasoning"`
}
type Descriptor struct {
	ID           string       `json:"id"`
	Label        string       `json:"label"`
	Capabilities Capabilities `json:"capabilities"`
}
type Image struct{ ID, Path string }
type Request struct {
	// Purpose is empty for ordinary chat, or an answer kind for parallel requests.
	Purpose        string
	ContextText    string
	History        []chat.Message
	Turn           chat.Message
	Config         chat.Config
	ConfigRevision uint64
	State          json.RawMessage
	Images         []Image
}
type Result struct {
	Text  string
	State json.RawMessage
}

// Event kinds. Progress is technical output for the log only. AnswerText carries
// the full accumulated text of the current, not yet completed answer; it never
// includes reasoning and is not a final result.
const (
	EventProgress   = "progress"
	EventAnswerText = "answer_text"
)

type Event struct{ Kind, Text string }
type CheckResult struct{ Text string }
type Provider interface {
	Describe() Descriptor
	Generate(context.Context, Request, func(Event)) (Result, error)
	Check(context.Context, chat.Config, func(Event)) (CheckResult, error)
}
