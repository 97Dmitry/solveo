package stt

import "context"

// Check states.
const (
	StateOK      = "ok"
	StateMissing = "missing"
	StateError   = "error"
	StateUnknown = "unknown"
)

// Check is one readiness item of a provider; Label is shown to the user as is.
type Check struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	State   string `json:"state"`
	Message string `json:"message"`
}

// Ready reports whether every check passed.
func Ready(checks []Check) bool {
	for _, c := range checks {
		if c.State != StateOK {
			return false
		}
	}
	return len(checks) > 0
}

// Description tells the application how to present a provider; it never changes at run time.
type Description struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Local providers keep audio on this computer; cloud providers send it to their service.
	Local bool `json:"local"`
	// Preparable providers install something before the first start (image, model).
	Preparable     bool   `json:"preparable"`
	StartLabel     string `json:"start_label"`
	StopLabel      string `json:"stop_label"`
	MaxPhrases     int    `json:"max_phrases"`
	MaxPhraseRunes int    `json:"max_phrase_runes"`
}

// SessionAPI is a running recognizer: streams of PCM in, Update/StreamError/error events out.
type SessionAPI interface {
	Open(ctx context.Context, streamID, track string, phrases []string) error
	Push(streamID string, rate, channels int, samples []float32) error
	Flush(ctx context.Context, streamID string) error
	CloseStream(streamID string) error
	Events() <-chan any
	Close() error
}

// Provider owns everything specific to one recognizer: readiness, installation and connection.
type Provider interface {
	Describe() Description
	Status(ctx context.Context) []Check
	Prepare(ctx context.Context, progress func(string)) error
	Start(ctx context.Context, progress func(string)) (SessionAPI, error)
	Stop(ctx context.Context) error
}

var _ SessionAPI = (*Session)(nil)
