package sttdocker

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps docker.exe from flashing a console window from the GUI process.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
