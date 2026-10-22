package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInterviewConfigRoundtripAndLegacy(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{"detail":"brief","context":"legacy"}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.Position != "" || c.Stack != "" || c.InterviewFormat != "" || c.Experience != "" {
		t.Fatal("legacy acquired facts")
	}
	c.Position, c.Stack, c.InterviewFormat, c.Experience = "Backend", "Go", "System design", "Built a billing service"
	doc := New()
	doc.Config = c
	copy := Clone(doc)
	if copy.Config != c {
		t.Fatal("clone lost preparation")
	}
}

func TestValidateConfigRejectsInvalidInterviewText(t *testing.T) {
	for _, value := range []string{strings.Repeat("a", 1<<20+1), "x\x00y", string([]byte{0xff})} {
		for _, field := range []string{"position", "stack", "interview_format", "experience"} {
			cfg := New().Config
			switch field {
			case "position":
				cfg.Position = value
			case "stack":
				cfg.Stack = value
			case "interview_format":
				cfg.InterviewFormat = value
			case "experience":
				cfg.Experience = value
			}
			if ValidateConfig(cfg) == nil {
				t.Fatalf("accepted invalid %s", field)
			}
		}
	}
	if err := ValidateConfig(New().Config); err != nil {
		t.Fatal(err)
	}
}
