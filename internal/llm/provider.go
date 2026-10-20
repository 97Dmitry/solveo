// Package llm defines the application-owned provider boundary.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/97Dmitry/solveo/internal/chat"
)

var ErrOutcomeUnknown = errors.New("external turn outcome is unknown")

type Capabilities struct {
	Images    bool `json:"images"`
	Streaming bool `json:"streaming"`
	Reasoning bool `json:"reasoning"`
}
type Descriptor struct {
	ID           string       `json:"id"`
	Label        string       `json:"label"`
	Capabilities Capabilities `json:"capabilities"`
}
type Image struct{ ID, Path string }
type Request struct {
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
type Event struct{ Kind, Text string }
type CheckResult struct{ Text string }
type Provider interface {
	Describe() Descriptor
	Generate(context.Context, Request, func(Event)) (Result, error)
	Check(context.Context, chat.Config, func(Event)) (CheckResult, error)
}
