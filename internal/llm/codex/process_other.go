//go:build !windows

package codex

import "os/exec"

func configureSubprocess(cmd *exec.Cmd) {}
