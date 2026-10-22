//go:build !windows

package sttdocker

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestExecRunnerStreamsLinesAndReportsStderr(t *testing.T) {
	var lines []string
	out, err := ExecRunner{Binary: "sh"}.Run(context.Background(), strings.NewReader("in\n"), func(l string) { lines = append(lines, l) },
		"-c", "cat; echo out; echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
	if out != "" {
		t.Fatalf("failed command returned output %q", out)
	}
	if strings.Join(lines, ",") == "" || !strings.Contains(strings.Join(lines, ","), "out") {
		t.Fatalf("lines %v", lines)
	}
	out, err = ExecRunner{Binary: "sh"}.Run(context.Background(), nil, nil, "-c", "echo ok")
	if err != nil || out != "ok" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestExecRunnerMissingBinary(t *testing.T) {
	_, err := ExecRunner{Binary: "solveo-no-such-docker"}.Run(context.Background(), nil, nil, "version")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
