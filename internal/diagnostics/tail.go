package diagnostics

import (
	"strings"
	"sync"
)

// Tail keeps the last Limit bytes written to it, for example a child
// process's stderr, where a Go panic trace ends up.
type Tail struct {
	Limit int
	mu    sync.Mutex
	buf   []byte
	cut   bool
}

func (t *Tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if extra := len(t.buf) - t.Limit; t.Limit > 0 && extra > 0 {
		t.buf = append(t.buf[:0], t.buf[extra:]...)
		t.cut = true
	}
	return len(p), nil
}

// String returns the kept text with valid UTF-8; a cut head is marked with "…".
func (t *Tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.ToValidUTF8(string(t.buf), "")
	if t.cut {
		return "…" + s
	}
	return s
}
