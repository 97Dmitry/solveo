// Package config parses application launch options without starting the GUI.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type Options struct {
	Binary  string
	Timeout time.Duration
	Prompt  string
	// TestPanic crashes the process after startup to verify crash logging on Windows.
	TestPanic bool
}

func Parse(args []string) (Options, error) {
	var out Options
	f := flag.NewFlagSet("solveo", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&out.Binary, "codex", "codex", "Codex CLI executable")
	f.DurationVar(&out.Timeout, "timeout", 10*time.Minute, "LLM request timeout")
	promptFile := f.String("prompt-file", "", "UTF-8 instruction file")
	f.Bool("keep", false, "deprecated; sent messages and screenshots are archived")
	f.BoolVar(&out.TestPanic, "diagnostics-test-panic", false, "crash after startup to verify the crash file")
	if err := f.Parse(args); err != nil {
		return out, err
	}
	if f.NArg() != 0 {
		return out, errors.New("лишние аргументы командной строки")
	}
	if strings.TrimSpace(out.Binary) == "" {
		return out, errors.New("не указан исполняемый файл Codex")
	}
	if out.Timeout <= 0 {
		out.Timeout = 10 * time.Minute
	}
	if *promptFile != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			return out, fmt.Errorf("не удалось прочитать файл инструкции: %w", err)
		}
		if strings.TrimSpace(string(b)) == "" {
			return out, errors.New("файл инструкции пуст")
		}
		out.Prompt = string(b)
	}
	return out, nil
}
