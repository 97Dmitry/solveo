//go:build !windows

package sttdocker

import "os/exec"

func hideWindow(*exec.Cmd) {}
