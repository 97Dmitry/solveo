// Package stt is the Go side of the solveo-stt worker protocol v1 (see internal/stt/worker).
package stt

import "errors"

// Update kinds. A final closes its utterance; partial text may still change.
const (
	KindStarted = "started"
	KindPartial = "partial"
	KindFinal   = "final"
)

// ErrBackpressure means the frame was dropped because the worker is not keeping up.
var ErrBackpressure = errors.New("распознавание не успевает принимать звук")

// Update is a transcript change for one utterance of one stream.
type Update struct {
	StreamID    string `json:"stream_id"`
	UtteranceID string `json:"utterance_id"`
	Revision    int    `json:"revision"`
	Kind        string `json:"kind"`
	StartSample int64  `json:"start_sample"`
	EndSample   int64  `json:"end_sample"`
	Text        string `json:"text"`
}

// StreamError ends one stream; the session stays usable.
type StreamError struct {
	StreamID string
	Message  string
}

const (
	maxQueuedFrames  = 50 // 5 s of 100 ms frames
	maxServerMessage = 256 << 10
	eventBuffer      = 256
)

type serverMessage struct {
	Type      string `json:"type"`
	StreamID  string `json:"stream_id"`
	RequestID string `json:"request_id"`
	Message   string `json:"message"`
	Update
}
