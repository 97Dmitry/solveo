// Package meeting owns local meeting recordings and post-recording processing.
package meeting

import (
	"context"
	"time"
)

const (
	Version           = 1
	PartSeconds       = 300
	MaxSeconds        = 8 * 60 * 60
	MaxBytes    int64 = 8 << 30
	MaxDocument       = 64 << 20
)

type Info struct {
	ID        string    `json:"id"`
	ChatID    string    `json:"chat_id"`
	Title     string    `json:"title"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	Duration  float64   `json:"duration"`
	Bytes     int64     `json:"bytes"`
	Revision  uint64    `json:"revision"`
}
type Part struct {
	File        string  `json:"file"`
	Track       string  `json:"track"`
	Rate        int     `json:"rate"`
	Offset      float64 `json:"offset"`
	Samples     int64   `json:"samples"`
	Complete    bool    `json:"complete"`
	Transcribed bool    `json:"transcribed"`
}
type Segment struct {
	ID      string  `json:"id"`
	Track   string  `json:"track"`
	Part    string  `json:"part"`
	Speaker string  `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
}
type Item struct {
	Text     string   `json:"text"`
	Owner    string   `json:"owner"`
	Deadline string   `json:"deadline"`
	Sources  []string `json:"sources"`
}
type Summary struct {
	Overview  string   `json:"overview"`
	Topics    []string `json:"topics"`
	Decisions []Item   `json:"decisions"`
	Tasks     []Item   `json:"tasks"`
	Questions []string `json:"questions"`
	Partial   bool     `json:"partial"`
	Revision  uint64   `json:"revision"`
	Model     string   `json:"model"`
}
type Document struct {
	Info
	Version  int                `json:"version"`
	Tracks   []string           `json:"tracks"`
	Parts    []Part             `json:"parts"`
	Segments []Segment          `json:"segments"`
	Summary  *Summary           `json:"summary"`
	Leaves   map[string]Summary `json:"leaves"`
}
type Word struct {
	Text, Speaker string
	Start, End    float64
}
type AudioInput struct {
	Path     string
	Duration float64
}
type Processor interface {
	Transcribe(context.Context, AudioInput) ([]Word, error)
	Summarize(context.Context, []Segment) (Summary, error)
	Cleanup(context.Context) error
}
type State struct {
	Paused       *PausedOperation `json:"paused,omitempty"`
	RecordingID  string           `json:"recording_id"`
	ProcessingID string           `json:"processing_id"`
	Stage        string           `json:"stage"`
	Completed    int              `json:"completed"`
	Total        int              `json:"total"`
	Error        string           `json:"error"`
	Notice       string           `json:"notice"`
	Revision     uint64           `json:"revision"`
}
