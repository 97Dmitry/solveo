package codex

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const testThreadID = "01234567-89ab-4cde-8f01-23456789abcd"

func TestSessionArguments(t *testing.T) {
	client := Client{Model: "gpt-6-sol", Reasoning: "high"}
	initial, err := client.sessionArguments([]string{"screen.png"}, "answer.md", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"exec", "--json", "--model", "gpt-6-sol", "-c", "model_reasoning_effort=high",
		"--ignore-user-config", "--skip-git-repo-check", "--sandbox", "read-only",
		"--image", "screen.png", "--output-last-message", "answer.md", "-",
	}
	if !reflect.DeepEqual(initial, want) {
		t.Fatalf("initial session args = %#v, want %#v", initial, want)
	}
	resumed, err := client.sessionArguments(nil, "answer.md", testThreadID)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{
		"exec", "--json", "--model", "gpt-6-sol", "-c", "model_reasoning_effort=high",
		"--ignore-user-config", "--skip-git-repo-check", "--sandbox", "read-only",
		"--output-last-message", "answer.md", "resume", testThreadID, "-",
	}
	if !reflect.DeepEqual(resumed, want) {
		t.Fatalf("resumed session args = %#v, want %#v", resumed, want)
	}
}

func TestSessionEventsReadAcrossWrites(t *testing.T) {
	var log strings.Builder
	parser := &sessionEvents{progress: func(s string) { log.WriteString(s) }}
	data := fmt.Appendf(
		nil,
		"{\"type\":\"thread.started\",\"thread_id\":%q}\n{\"type\":\"turn.completed\"}\n",
		testThreadID,
	)
	for _, chunk := range [][]byte{data[:12], data[12:53], data[53:]} {
		if _, err := parser.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if parser.threadID != testThreadID {
		t.Fatalf("session ID = %q", parser.threadID)
	}
	if !strings.Contains(log.String(), "turn.completed") {
		t.Fatalf("progress missing: %q", log.String())
	}
}

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
