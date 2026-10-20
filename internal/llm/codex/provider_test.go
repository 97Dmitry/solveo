package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
)

func TestPromptSeparatesContextAndUserText(t *testing.T) {
	c := New("codex", "", time.Minute)
	r := llm.Request{Config: chat.Config{Context: "Interview Go", Instructions: "Explain briefly"}, ConfigRevision: 3, Turn: chat.Message{Text: "What is a mutex?"}}
	p := c.prompt(r)
	for _, s := range []string{"Interview Go", "Explain briefly", "What is a mutex?", "3"} {
		if !strings.Contains(p, s) {
			t.Fatalf("missing %q", s)
		}
	}
	if strings.Contains(p, "Ответ начни с полного кода") {
		t.Fatal("coding-first prompt")
	}
}

func TestGenerateRejectsMissingImageBeforeLaunch(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "codex-must-not-run"), "", time.Second)
	r := llm.Request{Turn: chat.Message{Text: "task"}, ConfigRevision: 1, Images: []llm.Image{{Path: filepath.Join(t.TempDir(), "missing.png")}}}
	if _, err := c.Generate(context.Background(), r, nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing image error, got %v", err)
	}
}
