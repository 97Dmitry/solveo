//go:build !windows

package qwen

import "os/exec"

func hideWindow(*exec.Cmd) {}
