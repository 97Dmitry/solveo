//go:build !windows

package codex

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProviderTextStartAndImageContinuation(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake")
	script := `#!/bin/sh
out=""; prev=""; images=0
for arg in "$@"; do
 [ "$arg" != "--ephemeral" ] || exit 8
 [ "$arg" != "--image" ] || images=$((images+1))
 [ "$prev" != "--output-last-message" ] || out="$arg"
 prev="$arg"
done
cat > /dev/null
printf 'answer:%s' "$images" > "$out"
printf '{"type":"thread.started","thread_id":"12345678-1234-1234-1234-123456789abc"}\n{"type":"turn.completed"}\n'
`
	os.WriteFile(bin, []byte(script), 0700)
	c := New(bin, "", time.Second)
	r := llm.Request{Turn: chat.Message{Text: "hello"}, ConfigRevision: 1}
	out, e := c.Generate(context.Background(), r, nil)
	if e != nil || out.Text != "answer:0" {
		t.Fatalf("%+v %v", out, e)
	}
	r.State = out.State
	for i := 0; i < 2; i++ {
		p := filepath.Join(dir, chat.ID()+".png")
		os.WriteFile(p, []byte("png"), 0600)
		r.Images = append(r.Images, llm.Image{Path: p})
	}
	out, e = c.Generate(context.Background(), r, nil)
	if e != nil || out.Text != "answer:2" {
		t.Fatalf("%+v %v", out, e)
	}
}
func TestPartialStateOnTimeout(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "fake")
	os.WriteFile(bin, []byte("#!/bin/sh\nprintf '{\"type\":\"thread.started\",\"thread_id\":\"12345678-1234-1234-1234-123456789abc\"}\\n'\nexec sleep 10\n"), 0700)
	c := New(bin, "", 100*time.Millisecond)
	out, e := c.Generate(context.Background(), llm.Request{Turn: chat.Message{Text: "hi"}}, nil)
	if !errors.Is(e, llm.ErrOutcomeUnknown) || !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("%v", e)
	}
	var state sessionState
	if e = json.Unmarshal(out.State, &state); e != nil || state.SessionID == "" {
		t.Fatalf("state lost: %s %v", out.State, e)
	}
}
