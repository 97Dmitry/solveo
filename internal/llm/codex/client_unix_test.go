//go:build !windows

package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckConnectionStreamsProgressBeforeAnswer(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "codex")
	script := `#!/bin/sh
out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--output-last-message" ]; then out="$arg"; fi
  prev="$arg"
done
echo 'waiting for model' >&2
sleep 1
printf 'ready' > "$out"
`
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	progress := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		_, err := (Client{Binary: fake, Timeout: 3 * time.Second, Progress: func(s string) {
			select {
			case progress <- s:
			default:
			}
		}}).CheckConnection(context.Background())
		done <- err
	}()
	select {
	case line := <-progress:
		if !strings.Contains(line, "waiting for model") {
			t.Fatalf("unexpected progress: %q", line)
		}
	case err := <-done:
		t.Fatalf("request finished before progress: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("progress did not arrive")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCheckConnectionUsesCLIWithoutImages(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "codex")
	script := `#!/bin/sh
out=""
prev=""
for arg in "$@"; do
  [ "$arg" != "--image" ] || exit 2
  if [ "$prev" = "--output-last-message" ]; then out="$arg"; fi
  prev="$arg"
done
read -r prompt || [ -n "$prompt" ] || exit 3
[ "$prompt" = "Ответь одним словом: OK." ] || exit 4
printf 'OK' > "$out"
`
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	answer, err := (Client{Binary: fake, Model: "gpt-6-sol", Reasoning: "medium"}).CheckConnection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if answer != "OK" {
		t.Fatalf("unexpected check response: %q", answer)
	}
}
