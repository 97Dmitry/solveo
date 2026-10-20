package codex

import (
	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
	"strings"
	"testing"
	"time"
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
