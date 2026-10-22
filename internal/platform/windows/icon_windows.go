//go:build windows

package windows

import (
	_ "embed"
	"fmt"
	"unsafe"
)

//go:embed trayicon.png
var trayIconPNG []byte

var (
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon              = user32.NewProc("DestroyIcon")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
)

// loadTrayIcon creates an owned HICON from the embedded transparent PNG.
// Windows supports PNG icon resources directly; no temporary file is needed.
func loadTrayIcon() (uintptr, error) {
	// CreateIconFromResourceEx requires DWORD-aligned resource bytes.
	resource := make([]uint32, (len(trayIconPNG)+3)/4)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&resource[0])), len(trayIconPNG)), trayIconPNG)
	width, _, _ := procGetSystemMetrics.Call(49)  // SM_CXSMICON
	height, _, _ := procGetSystemMetrics.Call(50) // SM_CYSMICON
	icon, _, err := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&resource[0])), uintptr(len(trayIconPNG)),
		1, 0x00030000, width, height, 0,
	)
	if icon == 0 {
		return 0, fmt.Errorf("create tray icon: %w", err)
	}
	return icon, nil
}

// destroyTrayIcon runs after Shell_NotifyIcon(NIM_DELETE), on the host thread.
func (w *Window) destroyTrayIcon() {
	if w.icon != 0 {
		procDestroyIcon.Call(w.icon)
		w.icon = 0
	}
}
