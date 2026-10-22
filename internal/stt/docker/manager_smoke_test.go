//go:build dockersmoke

package sttdocker

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/stt"
)

// Opt-in: SOLVEO_STT_MODELS=<dir containing Qwen3-ASR-1.7B-7278e1e> go test -tags dockersmoke -run Smoke -v ./internal/stt/docker
// Uses the real docker CLI and GPU; builds the image if missing and starts/stops the real container.
func TestSmokePrepareStartStop(t *testing.T) {
	dir := os.Getenv("SOLVEO_STT_MODELS")
	if dir == "" {
		t.Skip("SOLVEO_STT_MODELS not set")
	}
	m := &Manager{Runner: ExecRunner{}, ModelsDir: dir, Getenv: os.Getenv, Health: stt.Health}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	logf := func(l string) { t.Log(l) }
	if err := m.Prepare(ctx, logf); err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Status(ctx) {
		t.Logf("%s=%s %s", c.ID, c.State, c.Message)
	}
	started := time.Now()
	ep, err := m.Start(ctx, logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ready at %s after %s", ep.Addr, time.Since(started))
	s, err := stt.Dial(ctx, ep.Addr, ep.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Open(ctx, "smoke", "system", []string{"context cancellation", "goroutine"}); err != nil {
		t.Fatal(err)
	}
	if wav := os.Getenv("SOLVEO_STT_WAV"); wav != "" {
		streamWAV(t, s, wav)
	}
	_ = s.Close()
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

// streamWAV pushes a 16-bit PCM WAV in 100 ms frames at real-time pace, flushes and expects a final for the stream.
func streamWAV(t *testing.T, s *stt.Session, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rate := int(binary.LittleEndian.Uint32(data[24:]))
	channels := int(binary.LittleEndian.Uint16(data[22:]))
	at := bytes.Index(data, []byte("data"))
	pcm := data[at+8:]
	samples := make([]float32, len(pcm)/2)
	for i := range samples {
		samples[i] = float32(int16(binary.LittleEndian.Uint16(pcm[2*i:]))) / 32768
	}
	step := rate / 10 * channels
	for i := 0; i < len(samples); i += step {
		if err := s.Push("smoke", rate, channels, samples[i:min(i+step, len(samples))]); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := s.Flush(context.Background(), "smoke"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-s.Events():
			if u, ok := ev.(stt.Update); ok {
				t.Logf("%s %s rev %d stream %q: %q", u.Kind, u.UtteranceID, u.Revision, u.StreamID, u.Text)
				if u.StreamID != "smoke" {
					t.Fatalf("update without its stream id: %+v", u)
				}
				if u.Kind == stt.KindFinal {
					return
				}
			}
		case <-deadline:
			t.Fatal("no final phrase")
		}
	}
}
