//go:build windows

package windows

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	wmAppTray       = 0x8002
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	nimAdd          = 0
	nimDelete       = 2
	nifMessage      = 1
	nifIcon         = 2
	nifTip          = 4
	trayID          = 1
	menuShow        = 3001
	menuExit        = 3002
	menuLog         = 3003
	menuTheme       = 3004
	menuPin         = 3005
	mfString        = 0
	tpmRightButton  = 2
)

var (
	shell32                = syscall.NewLazyDLL("shell32.dll")
	procShellNotifyIconW   = shell32.NewProc("Shell_NotifyIconW")
	procLoadIconW          = user32.NewProc("LoadIconW")
	procRegisterWindowMsgW = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu    = user32.NewProc("CreatePopupMenu")
	procAppendMenuW        = user32.NewProc("AppendMenuW")
	procTrackPopupMenu     = user32.NewProc("TrackPopupMenu")
	procDestroyMenu        = user32.NewProc("DestroyMenu")
	procGetCursorPos       = user32.NewProc("GetCursorPos")
)

// Win32 NOTIFYICONDATAW layout. Shell_NotifyIcon requires a supported cbSize.
type notifyIconData struct {
	size            uint32
	hwnd            uintptr
	id              uint32
	flags           uint32
	callbackMessage uint32
	icon            uintptr
	tip             [128]uint16
	state           uint32
	stateMask       uint32
	info            [256]uint16
	version         uint32
	infoTitle       [64]uint16
	infoFlags       uint32
	guid            [16]byte
	balloonIcon     uintptr
}

func (w *Window) trayData() notifyIconData {
	data := notifyIconData{
		size:            uint32(unsafe.Sizeof(notifyIconData{})),
		hwnd:            w.hwnd,
		id:              trayID,
		flags:           nifMessage | nifIcon | nifTip,
		callbackMessage: wmAppTray,
	}
	data.icon = w.icon
	copy(data.tip[:], syscall.StringToUTF16("Solveo — двойной щелчок: открыть"))
	return data
}

func (w *Window) addTrayIcon() error {
	if w.taskbarCreated == 0 {
		w.taskbarCreated = uint32(registerTaskbarCreated())
	}
	data := w.trayData()
	if data.icon == 0 {
		return fmt.Errorf("application icon is unavailable")
	}
	if ok, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&data))); ok == 0 {
		return fmt.Errorf("Shell_NotifyIconW(NIM_ADD): %w", err)
	}
	w.trayAdded = true
	return nil
}

func registerTaskbarCreated() uintptr {
	name := utf16("TaskbarCreated")
	id, _, _ := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(name)))
	return id
}

func (w *Window) removeTrayIcon() {
	if !w.trayAdded {
		return
	}
	data := w.trayData()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
	w.trayAdded = false
}

func (w *Window) showWindow()   { w.emit("show") }
func (w *Window) toggleWindow() { w.emit("toggle") }
func (w *Window) trayMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	show := utf16("Открыть")
	log := utf16("Показать лог")
	themeLabel := "Светлая тема"
	if !w.dark {
		themeLabel = "Тёмная тема"
	}
	theme := utf16(themeLabel)
	exit := utf16("Выход")
	pin := utf16("Поверх всех окон")
	procAppendMenuW.Call(menu, mfString, menuShow, uintptr(unsafe.Pointer(show)))
	procAppendMenuW.Call(menu, mfString, menuLog, uintptr(unsafe.Pointer(log)))
	procAppendMenuW.Call(menu, mfString, menuTheme, uintptr(unsafe.Pointer(theme)))
	procAppendMenuW.Call(menu, mfString, menuPin, uintptr(unsafe.Pointer(pin)))
	procAppendMenuW.Call(menu, mfString, menuExit, uintptr(unsafe.Pointer(exit)))
	var pos point
	if ok, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pos))); ok == 0 {
		return
	}
	procSetForegroundWindow.Call(w.hwnd)
	procTrackPopupMenu.Call(menu, tpmRightButton, uintptr(pos.x), uintptr(pos.y), 0, w.hwnd, 0)
	procPostMessageW.Call(w.hwnd, 0, 0, 0)
}
