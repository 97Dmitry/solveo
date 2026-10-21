//go:build !windows

package codex

import "os/exec"

func configureSubprocess(cmd *exec.Cmd) {}

// localDisk accepts every path; network detection is only needed on Windows.
func localDisk(string) error { return nil }
