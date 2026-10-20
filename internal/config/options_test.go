package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseOptions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(p, []byte("custom instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		args    []string
		binary  string
		timeout time.Duration
		prompt  string
	}{
		{"defaults", nil, "codex", 10 * time.Minute, ""},
		{"custom", []string{"-codex", "codex.cmd", "-timeout", "3m", "-prompt-file", p}, "codex.cmd", 3 * time.Minute, "custom instructions"},
		{"fallback", []string{"-timeout", "0", "-keep"}, "codex", 10 * time.Minute, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if got.Binary != tt.binary || got.Timeout != tt.timeout || got.Prompt != tt.prompt {
				t.Fatalf("unexpected options: %+v", got)
			}
		})
	}
}
func TestParseRejectsEmptyPrompt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(p, []byte("  "), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]string{"-prompt-file", p}); err == nil {
		t.Fatal("empty prompt accepted")
	}
}
