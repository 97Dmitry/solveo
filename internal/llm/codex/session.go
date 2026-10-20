package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/97Dmitry/solveo/internal/llm"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AnalyzeImagesSession starts a persistent Codex CLI session for subsequent
// text-only questions. Unlike one-shot AnalyzeImages, it must not use --ephemeral.
func (c Client) AnalyzeImagesSession(ctx context.Context, imagePaths []string) (answer, threadID string, err error) {
	if len(imagePaths) == 0 {
		return "", "", errors.New("no screenshots to analyze")
	}
	images := make([]string, len(imagePaths))
	for i, path := range imagePaths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", "", err
		}
		if _, err := os.Stat(absolute); err != nil {
			return "", "", fmt.Errorf("image %d not found: %w", i+1, err)
		}
		images[i] = absolute
	}
	prompt := c.Prompt
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultPrompt
	}
	if c.Language != "" {
		prompt += "\nЯзык программирования для решения и кода: " + c.Language + "."
	}
	return c.runSession(ctx, images, prompt, "")
}

// FollowUp sends only the user's text, without image flags, to the specified
// conversation. A missing/wrong session ID must never silently start a new chat.
func (c Client) FollowUp(ctx context.Context, threadID, question string) (string, error) {
	if !validThreadID(threadID) {
		return "", errors.New("invalid Codex session ID")
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return "", errors.New("empty follow-up question")
	}
	answer, returnedID, err := c.runSession(ctx, nil, question, threadID)
	if err != nil {
		return "", err
	}
	if returnedID != threadID {
		return "", fmt.Errorf("Codex resumed a different session (%s); answer discarded", returnedID)
	}
	return answer, nil
}

func validThreadID(id string) bool {
	// Codex emits a UUID in the thread.started JSON event.
	if len(id) != 36 {
		return false
	}
	for i, ch := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if ch != '-' {
				return false
			}
			continue
		}
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

func (c Client) sessionArguments(images []string, output, resumeID string) ([]string, error) {
	args, err := c.arguments(images, output)
	if err != nil {
		return nil, err
	}
	// Keep the existing sandbox, no-plugins and model flags; only sessions
	// intentionally omit --ephemeral. JSON supplies the exact thread ID.
	filtered := make([]string, 0, len(args)+3)
	for _, arg := range args {
		if arg != "--ephemeral" {
			filtered = append(filtered, arg)
		}
	}
	args = append(filtered[:1], append([]string{"--json"}, filtered[1:]...)...)
	if resumeID != "" {
		// The final '-' instructs both exec and exec resume to read stdin.
		args = append(args[:len(args)-1], "resume", resumeID, "-")
	}
	return args, nil
}

// sessionEvents consumes NDJSON output in chunks. It captures only the thread
// ID and short progress markers, never retains the complete model transcript.
type sessionEvents struct {
	pending   []byte
	threadID  string
	completed bool
	progress  func(string)
}

func (w *sessionEvents) Write(p []byte) (int, error) {
	n := len(p)
	w.pending = append(w.pending, p...)
	for {
		end := bytes.IndexByte(w.pending, '\n')
		if end < 0 {
			break
		}
		w.handle(w.pending[:end])
		w.pending = w.pending[end+1:]
	}
	// Avoid unbounded memory if a CLI version sends a huge event on one line.
	if len(w.pending) > 1<<20 {
		w.pending = nil
	}
	return n, nil
}

func (w *sessionEvents) handle(line []byte) {
	var event struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Message  string `json:"message"`
	}
	if json.Unmarshal(line, &event) != nil {
		return
	}
	if event.Type == "turn.completed" {
		w.completed = true
	}
	if event.Type == "thread.started" && validThreadID(event.ThreadID) {
		w.threadID = event.ThreadID
	}
	if w.progress != nil {
		switch event.Type {
		case "thread.started", "turn.started", "turn.completed", "turn.failed":
			w.progress("Codex: " + event.Type + "\n")
		case "error":
			if event.Message != "" {
				w.progress("Codex: " + event.Message + "\n")
			}
		}
	}
}

func (c Client) runSession(ctx context.Context, images []string, prompt, resumeID string) (string, string, error) {
	workdir, err := os.MkdirTemp("", "solveo-cli-*")
	if err != nil {
		return "", "", fmt.Errorf("temporary Codex directory: %w", err)
	}
	defer os.RemoveAll(workdir)
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	output := filepath.Join(workdir, "answer.md")
	args, err := c.sessionArguments(images, output, resumeID)
	if err != nil {
		return "", "", err
	}
	cmd := exec.CommandContext(ctx, c.binary(), args...)
	cmd.WaitDelay = 2 * time.Second
	configureSubprocess(cmd)
	cmd.Dir = workdir
	cmd.Stdin = strings.NewReader(prompt)
	events := &sessionEvents{progress: c.Progress}
	cmd.Stdout = events
	var stderr bytes.Buffer
	cmd.Stderr = &progressAndError{progress: c.Progress, errorLog: &stderr}

	if err := cmd.Start(); err != nil {
		return "", "", fmt.Errorf("start Codex: %w", err)
	}
	runErr := cmd.Wait()
	if len(events.pending) != 0 {
		events.handle(events.pending)
		events.pending = nil
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return "", events.threadID, fmt.Errorf("%w: %w", llm.ErrOutcomeUnknown, ctx.Err())
		}
		details := strings.TrimSpace(stderr.String())
		if len(details) > 1200 {
			details = details[len(details)-1200:]
		}
		return "", events.threadID, fmt.Errorf("%w: Codex failed: %w; %s", llm.ErrOutcomeUnknown, runErr, details)
	}

	// Some versions can output one final JSON event without a newline.
	if len(events.pending) != 0 {
		events.handle(events.pending)
	}
	if events.threadID == "" {
		return "", "", fmt.Errorf("%w: Codex returned no session ID (update Codex CLI to a version with --json)", llm.ErrOutcomeUnknown)
	}
	if !events.completed {
		return "", events.threadID, fmt.Errorf("%w: Codex returned no final turn event", llm.ErrOutcomeUnknown)
	}
	answer, err := os.ReadFile(output)
	if err != nil {
		return "", events.threadID, fmt.Errorf("%w: Codex returned no final message: %w; stderr: %s", llm.ErrOutcomeUnknown, err, strings.TrimSpace(stderr.String()))
	}
	if strings.TrimSpace(string(answer)) == "" {
		return "", events.threadID, fmt.Errorf("%w: Codex returned an empty answer", llm.ErrOutcomeUnknown)
	}
	return string(answer), events.threadID, nil
}
