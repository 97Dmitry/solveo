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

func TestAnalyzeWithFakeCodex(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "codex")
	const fakeScript = `#!/bin/sh
out=""
images=0
prev=""
for arg in "$@"; do
  if [ "$prev" = "--output-last-message" ]; then out="$arg"; fi
  if [ "$prev" = "--image" ]; then
    [ -f "$arg" ] || exit 2
    images=$((images + 1))
  fi
  prev="$arg"
done
[ "$images" -eq 2 ] || exit 2
[ -n "$out" ] || exit 3
read -r line || exit 4
[ -n "$line" ] || exit 5
grep -q 'Язык программирования для решения и кода: Go.' || exit 6
printf 'Тестовый ответ на задачу' > "$out"
`
	if err := os.WriteFile(fake, []byte(fakeScript), 0700); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(dir, "first screenshot.png")
	if err := os.WriteFile(first, []byte("first image"), 0600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dir, "second screenshot.png")
	if err := os.WriteFile(second, []byte("second image"), 0600); err != nil {
		t.Fatal(err)
	}

	client := Client{Binary: fake, Timeout: 2 * time.Second, Language: "Go"}
	answer, err := client.AnalyzeImages(context.Background(), []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "Тестовый ответ") {
		t.Fatalf("unexpected response: %q", answer)
	}
}

func TestAnalyzeImagesRejectsEmptyList(t *testing.T) {
	_, err := (Client{}).AnalyzeImages(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty screenshot list")
	}
}

func TestAnalyzeStreamsProgressBeforeAnswer(t *testing.T) {
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
	image := filepath.Join(dir, "image.png")
	if err := os.WriteFile(image, []byte("image"), 0600); err != nil {
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
		}}).Analyze(context.Background(), image)
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

func TestAnalyzeFailsOnMissingImage(t *testing.T) {
	_, err := (Client{}).Analyze(context.Background(), filepath.Join(t.TempDir(), "missing.png"))
	if err == nil {
		t.Fatal("expected error for a missing image")
	}
}
