//go:build windows

package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000

// GUI processes have no console. Avoid opening a transient console window
// when launching codex.exe or the npm-installed codex.cmd launcher.
func configureSubprocess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	cmd.Cancel = func() error {
		// A .cmd launcher starts Codex as a child. Killing only cmd.exe can
		// leave that child running and still sending the screenshot.
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		taskkill := "taskkill.exe"
		if root := os.Getenv("SystemRoot"); root != "" {
			taskkill = filepath.Join(root, "System32", taskkill)
		}
		kill := exec.CommandContext(ctx, taskkill, "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		if err := kill.Run(); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
