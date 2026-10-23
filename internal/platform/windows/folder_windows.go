//go:build windows

package windows

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	ole32                = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procShellExecuteW    = shell32.NewProc("ShellExecuteW")
	coinitApartment      = uintptr(0x2)
	coinitDisableOLE1DDE = uintptr(0x4)
)

// OpenFolder shows a directory in Explorer. ShellExecute may block on shell
// extensions, so it runs on its own COM-initialized thread instead of the
// host message loop. Explorer is an external window: it is not excluded from
// screen capture.
func (h *Host) OpenFolder(path string) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hr, _, _ := procCoInitializeEx.Call(0, coinitApartment|coinitDisableOLE1DDE)
		if int32(hr) >= 0 {
			defer procCoUninitialize.Call()
		}
		verb, file := utf16("open"), utf16(path)
		// ShellExecuteW returns a value greater than 32 on success.
		r, _, err := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, swShowNormal)
		if r <= 32 {
			done <- fmt.Errorf("не удалось открыть папку: %w", err)
			return
		}
		done <- nil
	}()
	return <-done
}
