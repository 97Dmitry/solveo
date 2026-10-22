//go:build dockersmoke

package sttdocker

import (
	"context"
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
	if err := s.Open(ctx, "smoke", "system", []string{"JWT"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
