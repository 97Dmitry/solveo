package sttdocker

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// Runner executes one docker CLI command. line receives every stdout/stderr line while it runs.
type Runner interface {
	Run(ctx context.Context, stdin io.Reader, line func(string), args ...string) (string, error)
}

// ExecRunner runs the docker binary from PATH (Docker Desktop's docker.exe on Windows).
type ExecRunner struct{ Binary string }

func (r ExecRunner) Run(ctx context.Context, stdin io.Reader, line func(string), args ...string) (string, error) {
	binary := r.Binary
	if binary == "" {
		binary = "docker"
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	hideWindow(cmd)
	cmd.Stdin = stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var (
		mu   sync.Mutex
		out  bytes.Buffer
		tail []string
		wg   sync.WaitGroup
	)
	scan := func(rd io.Reader, keep bool) {
		defer wg.Done()
		s := bufio.NewScanner(rd)
		s.Buffer(make([]byte, 64<<10), 1<<20)
		for s.Scan() {
			text := s.Text()
			mu.Lock()
			if keep {
				out.WriteString(text + "\n")
			}
			tail = append(tail, text)
			if len(tail) > 20 {
				tail = tail[1:]
			}
			if line != nil {
				line(text)
			}
			mu.Unlock()
		}
	}
	wg.Add(2)
	go scan(stdout, true)
	go scan(stderr, false)
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(strings.Join(tail, "\n")))
	}
	return strings.TrimSpace(out.String()), nil
}
