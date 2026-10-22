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

func TestFollowupPromptQuotesSelectedAnswer(t *testing.T) {
	c := New("codex", "", time.Minute, "", "")
	r := llm.Request{Turn: chat.Message{Text: "Короче", ReplyToID: "old"}, History: []chat.Message{
		{ID: "old", Role: chat.RoleAssistant, Status: chat.StatusComplete, Text: "selected\n```go\ncode\n```"},
		{ID: "new", Role: chat.RoleAssistant, Status: chat.StatusComplete, Text: "latest"},
	}}
	var payload struct {
		Question   string `json:"question"`
		Referenced *struct {
			MessageID string `json:"message_id"`
			Text      string `json:"text"`
		} `json:"referenced_answer"`
	}
	if err := json.NewDecoder(strings.NewReader(c.prompt(r))).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Question != "Короче" || payload.Referenced == nil || payload.Referenced.MessageID != "old" || payload.Referenced.Text != r.History[0].Text {
		t.Fatalf("%s", c.prompt(r))
	}
}

func TestGenerateRejectsUnresolvedFollowupBeforeLaunch(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "codex-must-not-run"), "", time.Second, t.TempDir(), t.TempDir())
	_, err := c.Generate(context.Background(), llm.Request{Turn: chat.Message{Text: "shorter", ReplyToID: "missing"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "выбранный ответ") {
		t.Fatalf("%v", err)
	}
}

func TestParallelSolutionFollowupDoesNotForceBriefFullSolution(t *testing.T) {
	c := New("codex", "", time.Minute, "", "")
	r := llm.Request{Purpose: chat.AnswerSolution, Turn: chat.Message{Text: "Раскрой ответ подробнее.", ReplyToID: "old"}, History: []chat.Message{{ID: "old", Role: chat.RoleAssistant, Status: chat.StatusComplete, Text: "selected"}}}
	p := c.prompt(r)
	if strings.Contains(p, "краткое пояснение") || !strings.Contains(p, "подробность") {
		t.Fatalf("followup format conflicts with requested detail: %s", p)
	}
}

func TestOrdinaryPromptUsesMainSolutionFormat(t *testing.T) {
	c := New("codex", "", time.Minute, "", "")
	for _, question := range []string{"Найди медиану двух отсортированных массивов за O(log(min(m,n)))", ""} {
		t.Run(question, func(t *testing.T) {
			request := llm.Request{Turn: chat.Message{Text: question}, Config: chat.New().Config}
			ordinary := c.prompt(request)
			request.Purpose = chat.AnswerSolution
			if ordinary != c.prompt(request) {
				t.Fatalf("ordinary request lost main solution instructions: %s", ordinary)
			}
			if !strings.Contains(ordinary, "код, если задача требует кода") || !strings.Contains(ordinary, "Для теории ответь прямо без ненужного кода") {
				t.Fatalf("missing task-dependent answer format: %s", ordinary)
			}
		})
	}
}
