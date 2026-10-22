// Package audio captures system loopback and microphone PCM. Frames keep the device's native
// format; the speech worker downmixes and resamples.
package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"time"
)

type Source string

const (
	System Source = "system"
	Mic    Source = "mic"
)

// Frame is up to 100 ms of interleaved float32 samples owned by the receiver.
type Frame struct {
	Rate, Channels int
	Samples        []float32
}

type Device struct{ Name string }

// Capturer starts one source. sink and fail are called from the capture goroutine and must not block;
// fail reports a capture that ended on its own (device removed, access lost). stop is idempotent and waits
// for the goroutine to exit.
type Capturer interface {
	Start(ctx context.Context, src Source, sink func(Frame), fail func(error)) (Device, func(), error)
}

var errUnsupportedFormat = errors.New("неподдерживаемый формат звука")

// ErrNoDevice means Windows has no default endpoint for the source.
var ErrNoDevice = errors.New("устройство звука не найдено")

type sampleFormat int

const (
	formatFloat32 sampleFormat = iota
	formatPCM16
)

func convert(data []byte, f sampleFormat) ([]float32, error) {
	switch f {
	case formatFloat32:
		out := make([]float32, len(data)/4)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[4*i:]))
		}
		return out, nil
	case formatPCM16:
		out := make([]float32, len(data)/2)
		for i := range out {
			out[i] = float32(int16(binary.LittleEndian.Uint16(data[2*i:]))) / 32768
		}
		return out, nil
	}
	return nil, errUnsupportedFormat
}

// MaxFrameBytes bounds the samples of one frame: the worker accepts messages of at most 64 KiB,
// including the length prefix and the JSON header.
const MaxFrameBytes = 60 << 10

// framer splits captured packets into frames of at most 100 ms and MaxFrameBytes.
type framer struct {
	rate, channels int
	sink           func(Frame)
}

func (f *framer) push(samples []float32) {
	limit := min(f.rate/10, MaxFrameBytes/4/f.channels) * f.channels
	for len(samples) > 0 {
		n := min(limit, len(samples))
		f.sink(Frame{Rate: f.rate, Channels: f.channels, Samples: append([]float32(nil), samples[:n]...)})
		samples = samples[n:]
	}
}

// silenceFiller keeps the loopback timeline continuous: Windows delivers no packets while nothing plays,
// but the worker's VAD needs elapsed silence to end an utterance.
type silenceFiller struct {
	rate, channels int
	covered        time.Time
}

const packetJitter = 30 * time.Millisecond

func (s *silenceFiller) packet(now time.Time) { s.covered = now }

func (s *silenceFiller) fill(now time.Time) []float32 {
	gap := now.Sub(s.covered)
	if s.covered.IsZero() || gap <= packetJitter {
		return nil
	}
	frames := int(gap * time.Duration(s.rate) / time.Second)
	s.covered = s.covered.Add(time.Duration(frames) * time.Second / time.Duration(s.rate))
	return make([]float32, frames*s.channels)
}
