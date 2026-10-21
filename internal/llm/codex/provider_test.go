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

func TestPromptSeparatesContextAndUserText(t *testing.T) {
	c := New("codex", "", time.Minute, "", "")
	r := llm.Request{Config: chat.Config{Context: "Interview Go", Instructions: "Explain briefly"}, ConfigRevision: 3, Turn: chat.Message{Text: "What is a mutex?"}}
	p := c.instructions(r) + c.prompt(r)
	for _, s := range []string{"Interview Go", "Explain briefly", "What is a mutex?"} {
		if !strings.Contains(p, s) {
			t.Fatalf("missing %q", s)
		}
	}
	if strings.Contains(p, "Ответ начни с полного кода") {
		t.Fatal("coding-first prompt")
	}
}

func TestGenerateRejectsMissingImageBeforeLaunch(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "codex-must-not-run"), "", time.Second, t.TempDir(), t.TempDir())
	r := llm.Request{Turn: chat.Message{Text: "task"}, ConfigRevision: 1, Images: []llm.Image{{Path: filepath.Join(t.TempDir(), "missing.png")}}}
	if _, err := c.Generate(context.Background(), r, nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing image error, got %v", err)
	}
}

func TestApproachPromptHasIndependentContextAndNoCode(t *testing.T) {
	var r llm.Request
	if err := json.Unmarshal([]byte(`{"Purpose":"approach","ContextText":"Earlier constraint: sorted array","Turn":{"text":"and with duplicates?"},"Config":{"response_language":"Русский"}}`), &r); err != nil {
		t.Fatal(err)
	}
	p := New("codex", "", time.Minute, "", "").prompt(r)
	for _, want := range []string{"sorted array", "and with duplicates?", "2–4", "без кода"} {
		if !strings.Contains(p, want) {
			t.Fatalf("approach prompt missing %q: %s", want, p)
		}
	}
}

const testThreadID = "01234567-89ab-4cde-8f01-23456789abcd"

func TestValidThreadID(t *testing.T) {
	for _, tt := range []struct {
		id    string
		valid bool
	}{
		{testThreadID, true},
		{"", false},
		{"--last", false},
		{"01234567-89ab-4cde-8f01-23456789abcg", false},
		{"not-a-uuid", false},
	} {
		if got := validThreadID(tt.id); got != tt.valid {
			t.Errorf("validThreadID(%q) = %t", tt.id, got)
		}
	}
}

func TestUnsupportedStateIsRejectedBeforeLaunch(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "codex-must-not-run"), "", time.Second, t.TempDir(), t.TempDir())
	for _, state := range []string{
		`{"version":1,"session_id":"` + testThreadID + `"}`,
		`{"version":2,"thread_id":"` + testThreadID + `"}`,
		`{"version":2,"transport":"app_server","thread_id":"bad"}`,
		`{"version":3}`,
	} {
		r := llm.Request{Turn: chat.Message{Text: "q"}, ConfigRevision: 1, State: json.RawMessage(state)}
		if _, err := c.Generate(context.Background(), r, nil); err == nil || !strings.Contains(err.Error(), "не поддерживается") {
			t.Fatalf("%s: %v", state, err)
		}
	}
}
