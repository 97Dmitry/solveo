//go:build !windows

package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
)

func writeFakeSessionCLI(t *testing.T, wrongThread bool) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "fake-codex")
	thread := testThreadID
	if wrongThread {
		thread = "fedcba98-7654-4321-8abc-def012345678"
	}
	script := strings.Join([]string{
		"#!/bin/sh",
		"out=\"\"",
		"prev=\"\"",
		"images=0",
		"json=0",
		"resume=0",
		"thread=\"\"",
		"for arg in \"$@\"; do",
		"  [ \"$arg\" != \"--ephemeral\" ] || exit 7",
		"  [ \"$arg\" != \"--json\" ] || json=1",
		"  [ \"$arg\" != \"resume\" ] || resume=1",
		"  [ \"$arg\" != \"--image\" ] || images=$((images+1))",
		"  [ \"$prev\" != \"--output-last-message\" ] || out=\"$arg\"",
		"  [ \"$prev\" != \"resume\" ] || thread=\"$arg\"",
		"  prev=\"$arg\"",
		"done",
		"[ \"$json\" -eq 1 ] && [ -n \"$out\" ] || exit 8",
		"cat > /dev/null",
		"if [ \"$resume\" -eq 1 ]; then",
		"  [ \"$images\" -eq 0 ] || exit 10",
		"  [ \"$thread\" = \"" + testThreadID + "\" ] || exit 11",
		"  printf 'Уточнение принято' > \"$out\"",
		"else",
		"  [ \"$images\" -eq 1 ] || exit 13",
		"  printf 'Первый ответ' > \"$out\"",
		"fi",
		"printf '{\"type\":\"thread.started\",\"thread_id\":\"" + thread + "\"}\\n'",
		"printf '{\"type\":\"turn.completed\"}\\n'",
	}, "\n") + "\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return binary
}

func TestGenerateResumesExactSessionWithoutImages(t *testing.T) {
	binary := writeFakeSessionCLI(t, false)
	image := filepath.Join(t.TempDir(), "task.png")
	if err := os.WriteFile(image, []byte("image contents"), 0600); err != nil {
		t.Fatal(err)
	}
	c := New(binary, "", time.Second)
	r := llm.Request{Turn: chat.Message{Text: "Реши задачу"}, ConfigRevision: 1, Images: []llm.Image{{Path: image}}}
	first, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != "Первый ответ" {
		t.Fatalf("initial answer = %q", first.Text)
	}
	r = llm.Request{Turn: chat.Message{Text: "Объясни сложность"}, ConfigRevision: 1, State: first.State}
	next, err := c.Generate(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Text != "Уточнение принято" {
		t.Fatalf("follow-up answer = %q", next.Text)
	}
}

func TestGenerateRejectsUnexpectedResumedThread(t *testing.T) {
	state, _ := json.Marshal(sessionState{1, testThreadID})
	c := New(writeFakeSessionCLI(t, true), "", time.Second)
	_, err := c.Generate(context.Background(), llm.Request{Turn: chat.Message{Text: "Объясни сложность"}, ConfigRevision: 1, State: state}, nil)
	if !errors.Is(err, llm.ErrOutcomeUnknown) || !strings.Contains(err.Error(), "другую сессию") {
		t.Fatalf("expected different-session error, got %v", err)
	}
}

func TestSessionFailsWhenNoThreadIDEmitted(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex")
	data := strings.Join([]string{
		"#!/bin/sh",
		"out=\"\"",
		"prev=\"\"",
		"for arg in \"$@\"; do",
		"  [ \"$prev\" != \"--output-last-message\" ] || out=\"$arg\"",
		"  prev=\"$arg\"",
		"done",
		"printf 'ok' > \"$out\"",
	}, "\n") + "\n"
	if err := os.WriteFile(script, []byte(data), 0700); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(dir, "image.png")
	if err := os.WriteFile(image, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := (Client{Binary: script}).runSession(context.Background(), []string{image}, "prompt", "")
	if err == nil || !strings.Contains(err.Error(), "ID сессии") {
		t.Fatalf("expected missing session ID error, got %v", err)
	}
}
