//go:build !windows

package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"read -r prompt || [ -n \"$prompt\" ] || exit 9",
		"if [ \"$resume\" -eq 1 ]; then",
		"  [ \"$images\" -eq 0 ] || exit 10",
		"  [ \"$thread\" = \"" + testThreadID + "\" ] || exit 11",
		"  [ \"$prompt\" = \"Объясни сложность\" ] || exit 12",
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

func TestAnalyzeThenFollowUpWithoutImage(t *testing.T) {
	binary := writeFakeSessionCLI(t, false)
	image := filepath.Join(t.TempDir(), "task.png")
	if err := os.WriteFile(image, []byte("image contents"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Client{Binary: binary, Language: "Go"}
	answer, id, err := c.AnalyzeImagesSession(context.Background(), []string{image})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Первый ответ" || id != testThreadID {
		t.Fatalf("initial answer = %q, session = %q", answer, id)
	}
	followAnswer, err := c.FollowUp(context.Background(), id, "  Объясни сложность  ")
	if err != nil {
		t.Fatal(err)
	}
	if followAnswer != "Уточнение принято" {
		t.Fatalf("follow-up answer = %q", followAnswer)
	}
}

func TestFollowUpRejectsUnexpectedResumedThread(t *testing.T) {
	client := Client{Binary: writeFakeSessionCLI(t, true)}
	_, err := client.FollowUp(context.Background(), testThreadID, "Объясни сложность")
	if err == nil || !strings.Contains(err.Error(), "different session") {
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
	_, _, err := (Client{Binary: script}).AnalyzeImagesSession(context.Background(), []string{image})
	if err == nil || !strings.Contains(err.Error(), "session ID") {
		t.Fatalf("expected missing session ID error, got %v", err)
	}
}
