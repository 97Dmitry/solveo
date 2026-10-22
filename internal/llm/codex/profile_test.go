package codex

import (
	"strings"
	"testing"

	"github.com/97Dmitry/solveo/internal/chat"
	"github.com/97Dmitry/solveo/internal/llm"
)

func TestInterviewFieldsInBothRequestInstructions(t *testing.T) {
	c := &Client{}
	cfg := chat.New().Config
	cfg.Position, cfg.Stack, cfg.InterviewFormat, cfg.Experience = "Backend", "Go SQL", "Design", "Built billing"
	for _, purpose := range []string{"", chat.AnswerApproach, chat.AnswerSolution} {
		text := c.instructions(llm.Request{Config: cfg, Purpose: purpose, GlobalInstructions: "GLOBAL"})
		for _, want := range []string{`"position":"Backend"`, `"stack":"Go SQL"`, `"interview_format":"Design"`, `"experience":"Built billing"`, "GLOBAL", "Не выдумывай"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %s", purpose, want)
			}
		}
	}
	cfg.Experience = ""
	if strings.Contains(c.instructions(llm.Request{Config: cfg}), "Built billing") {
		t.Fatal("invented experience")
	}
}
