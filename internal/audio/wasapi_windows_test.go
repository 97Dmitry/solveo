package audio

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Runs on Windows only; skips where no audio endpoint exists (CI runners).
func TestCaptureStartsAndStops(t *testing.T) {
	for _, src := range []Source{System, Mic} {
		t.Run(string(src), func(t *testing.T) {
			var mu sync.Mutex
			var frames []Frame
			dev, stop, err := NewCapturer().Start(context.Background(), src, func(f Frame) {
				mu.Lock()
				frames = append(frames, f)
				mu.Unlock()
			}, func(err error) { t.Errorf("capture failed: %v", err) })
			if errors.Is(err, ErrNoDevice) {
				t.Skip("no default endpoint")
			}
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
			stop()
			stop()
			mu.Lock()
			defer mu.Unlock()
			total := 0
			for _, f := range frames {
				if len(f.Samples) > f.Rate/10*f.Channels {
					t.Fatalf("frame longer than 100 ms: %d samples", len(f.Samples))
				}
				total += len(f.Samples) / f.Channels
			}
			// Loopback fills silence; the microphone delivers packets continuously.
			if len(frames) == 0 || total < frames[0].Rate/4 {
				t.Fatalf("%s %q: %d frames, %d samples per channel in 500 ms", src, dev.Name, len(frames), total)
			}
			t.Logf("%s: %q %d Hz x%d, %d samples per channel", src, dev.Name, frames[0].Rate, frames[0].Channels, total)
		})
	}
}
