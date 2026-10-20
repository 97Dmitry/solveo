// Package codex runs Codex CLI in read-only, non-interactive mode.
package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/llm"
)

const DefaultTimeout = 10 * time.Minute
const ConnectionTimeout = time.Minute

type Client struct {
	Binary    string
	Timeout   time.Duration
	Prompt    string
	Model     string
	Reasoning string
	Progress  func(string)
}

func (c Client) binary() string {
	if c.Binary == "" {
		return "codex"
	}
	return c.Binary
}

func (c Client) arguments(imagePaths []string, outputPath string) ([]string, error) {
	args := []string{"exec"}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	if c.Reasoning != "" {
		if !llm.ValidReasoning(c.Reasoning) {
			return nil, fmt.Errorf("недопустимый уровень рассуждения: %q", c.Reasoning)
		}
		args = append(args, "-c", "model_reasoning_effort="+c.Reasoning)
	}
	// Arguments stay separate OS arguments and are never interpolated into a shell command.
	args = append(args,
		"--ignore-user-config",
		"--skip-git-repo-check",
		"--ephemeral",
		"--sandbox", "read-only",
	)
	for _, path := range imagePaths {
		args = append(args, "--image", path)
	}
	return append(args, "--output-last-message", outputPath, "-"), nil
}

// CheckConnection makes a small authenticated request using the same CLI,
// model and reasoning options as chat requests. A non-empty reply proves
// that this path worked at the time of the check; it does not validate answers.
func (c Client) CheckConnection(ctx context.Context) (string, error) {
	return c.run(ctx, nil, "Ответь одним словом: OK.", ConnectionTimeout)
}

func (c Client) run(ctx context.Context, images []string, prompt string, timeout time.Duration) (string, error) {
	workdir, err := os.MkdirTemp("", "solveo-codex-*")
	if err != nil {
		return "", fmt.Errorf("временная папка Codex: %w", err)
	}
	defer os.RemoveAll(workdir)
	output := filepath.Join(workdir, "answer.md")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args, err := c.arguments(images, output)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, c.binary(), args...)
	// A .cmd launcher may leave a child holding stdout/stderr open after the
	// launcher exits. Do not let that keep cancellation or app exit waiting forever.
	cmd.WaitDelay = 2 * time.Second
	configureSubprocess(cmd)
	cmd.Dir = workdir // Keep Codex away from the caller's project directory.
	cmd.Stdin = strings.NewReader(prompt)
	if c.Progress != nil {
		cmd.Stdout = progressWriter{c.Progress}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &progressAndError{progress: c.Progress, errorLog: &stderr}
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("Codex не ответил за %s", timeout)
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		details := strings.TrimSpace(stderr.String())
		if len(details) > 1200 {
			details = details[len(details)-1200:]
		}
		return "", fmt.Errorf("ошибка Codex: %w; %s", err, details)
	}

	answer, err := os.ReadFile(output)
	if err != nil {
		return "", fmt.Errorf("Codex не вернул итоговый ответ: %w; stderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	if strings.TrimSpace(string(answer)) == "" {
		return "", errors.New("Codex вернул пустой ответ")
	}
	return string(answer), nil
}

type progressWriter struct{ report func(string) }

func (w progressWriter) Write(p []byte) (int, error) {
	w.report(string(p))
	return len(p), nil
}

type progressAndError struct {
	progress func(string)
	errorLog *bytes.Buffer
}

func (w *progressAndError) Write(p []byte) (int, error) {
	// Error details are only needed at the end; avoid retaining an unbounded log.
	const maxErrorLog = 4096
	data := append(append([]byte(nil), w.errorLog.Bytes()...), p...)
	if len(data) > maxErrorLog {
		data = data[len(data)-maxErrorLog:]
	}
	w.errorLog.Reset()
	w.errorLog.Write(data)
	if w.progress != nil {
		w.progress(string(p))
	}
	return len(p), nil
}
