//go:build windows

package windows

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	wdaExcludeFromCapture = 0x00000011
	wsExToolWindow        = 0x00000080
	wsOverlappedWindow    = 0x00CF0000
	wsChild               = 0x40000000
	wsVisible             = 0x10000000
	wsClipChildren        = 0x02000000
	wsVScroll             = 0x00200000
	wsBorder              = 0x00800000
	esMultiline           = 0x0004
	esAutoVScroll         = 0x0040
	esAutoHScroll         = 0x0080
	esReadOnly            = 0x0800
	bsOwnerDraw           = 0x0000000B
	cbsDropDownList       = 0x0003
	cbsDropDown           = 0x0002
	cbAddString           = 0x0143
	cbGetCurSel           = 0x0147
	cbSetCurSel           = 0x014E
	cbLimitText           = 0x0141
	lbAddString           = 0x0180
	lbDeleteString        = 0x0182
	lbGetCurSel           = 0x0188
	lbResetContent        = 0x0184
	lbsNotify             = 0x0001
	swHide                = 0
	cwUseDefault          = 0x80000000
	swShowNormal          = 1
	swShow                = 5
	swRestore             = 9
	wmDestroy             = 0x0002
	wmSize                = 0x0005
	wmClose               = 0x0010
	wmCommand             = 0x0111
	wmDrawItem            = 0x002B
	wmEraseBkgnd          = 0x0014
	wmGetMinMaxInfo       = 0x0024
	wmCtlColorEdit        = 0x0133
	wmCtlColorListBox     = 0x0134
	wmCtlColorBtn         = 0x0135
	wmCtlColorStatic      = 0x0138
	wmSetFont             = 0x0030
	emSetCueBanner        = 0x1501
	wmHotkey              = 0x0312
	wmKeyDown             = 0x0100
	wmTimer               = 0x0113
	emSetLimitText        = 0x00C5
	wmAppResult           = 0x8001
	wmAppLog              = 0x8003
	btnCapture            = 1001
	btnAdd                = 1002
	btnSend               = 1003
	btnRemove             = 1004
	btnClear              = 1005
	btnLog                = 1006
	btnCancel             = 1007
	btnCheck              = 1008
	btnTheme              = 1009
	btnFollowUp           = 1010
	btnNewChat            = 1011
	btnZoomIn             = 1012
	btnZoomOut            = 1013
	btnCopyCode           = 1014
	btnShots              = 1015
	btnSettings           = 1016
	btnHistory            = 1017
	btnChatImages         = 1018
	btnViewShot           = 1019
	listQueue             = 1020
	listHistory           = 1021
	listChatImages        = 1022
	btnOpenChat           = 1023
	btnViewChatImage      = 1024
	btnDiscardChat        = 1025
	// Global hotkey IDs are hotkeyBase + index in hotkeys.Actions.
	hotkeyBase     = 2001
	modNoRepeat    = 0x4000
	defaultGUIFont = 17
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	gdi32                        = syscall.NewLazyDLL("gdi32.dll")
	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procShowWindow               = user32.NewProc("ShowWindow")
	procEnableWindow             = user32.NewProc("EnableWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procSendMessageW             = user32.NewProc("SendMessageW")
	procSetWindowTextW           = user32.NewProc("SetWindowTextW")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procMoveWindow               = user32.NewProc("MoveWindow")
	procRedrawWindow             = user32.NewProc("RedrawWindow")
	procGetClientRect            = user32.NewProc("GetClientRect")
	procRegisterHotKey           = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey         = user32.NewProc("UnregisterHotKey")
	procSetWindowDisplayAffinity = user32.NewProc("SetWindowDisplayAffinity")
	procGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
	procGetStockObject           = gdi32.NewProc("GetStockObject")

	// Windows calls this function pointer. Retain it for the program's lifetime.
	windowCallback = syscall.NewCallback(windowProc)
	current        *Window
)

type point struct{ x, y int32 }
type message struct {
	hwnd    uintptr
	msg     uint32
	wparam  uintptr
	lparam  uintptr
	time    uint32
	pt      point
	private uint32
}
type rect struct{ left, top, right, bottom int32 }
type minMaxInfo struct {
	reserved, maxSize, maxPosition, minTrackSize, maxTrackSize point
}
type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	classExtra int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSmall  uintptr
}

func utf16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func EnsureExcluded(hwnd uintptr) error {
	if ok, _, err := procSetWindowDisplayAffinity.Call(hwnd, wdaExcludeFromCapture); ok == 0 {
		return fmt.Errorf("exclude window: %w", err)
	}
	var affinity uint32
	if ok, _, err := user32.NewProc("GetWindowDisplayAffinity").Call(hwnd, uintptr(unsafe.Pointer(&affinity))); ok == 0 {
		return fmt.Errorf("verify capture affinity: %w", err)
	}
	if affinity != wdaExcludeFromCapture {
		return fmt.Errorf("unexpected capture affinity: %x", affinity)
	}
	return nil
}
func FindMainWindow(class string, pid uint32) (uintptr, error) {
	name := utf16(class)
	previous := uintptr(0)
	for {
		hwnd, _, _ := user32.NewProc("FindWindowExW").Call(0, previous, uintptr(unsafe.Pointer(name)), 0)
		if hwnd == 0 {
			break
		}
		var owner uint32
		user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner == pid {
			return hwnd, nil
		}
		previous = hwnd
	}
	return 0, fmt.Errorf("main window was not found")
}

func limitMainWindowSize(data uintptr) {
	if data == 0 {
		return
	}
	var info minMaxInfo
	procMoveMemory.Call(uintptr(unsafe.Pointer(&info)), data, unsafe.Sizeof(info))
	info.minTrackSize = point{x: 520, y: 400}
	procMoveMemory.Call(data, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
}

const (
	gwlStyle        = -16
	gwlExStyle      = -20
	wsExAppWindow   = 0x00040000
	wsMinimizeBox   = 0x00020000
	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
)

// TrayOnly removes a still hidden main window from the taskbar and Alt+Tab:
// Windows lists unowned windows unless they are tool windows. A tool window
// minimized has no taskbar button to restore it, so its minimize box is
// removed; the window hides to the tray on close. Call before the first show.
func TrayOnly(hwnd uintptr) error {
	get := user32.NewProc("GetWindowLongPtrW")
	set := user32.NewProc("SetWindowLongPtrW")
	exIndex, styleIndex := gwlExStyle, gwlStyle
	ex, _, _ := get.Call(hwnd, uintptr(exIndex))
	style, _, _ := get.Call(hwnd, uintptr(styleIndex))
	set.Call(hwnd, uintptr(exIndex), (ex&^wsExAppWindow)|wsExToolWindow)
	set.Call(hwnd, uintptr(styleIndex), style&^wsMinimizeBox)
	// Apply the frame change without moving, showing or activating the window.
	user32.NewProc("SetWindowPos").Call(hwnd, 0, 0, 0, 0, 0, swpNoSize|swpNoMove|swpNoZOrder|swpNoActivate|swpFrameChanged)
	ex, _, _ = get.Call(hwnd, uintptr(exIndex))
	if ex&wsExToolWindow == 0 || ex&wsExAppWindow != 0 {
		return fmt.Errorf("main window still has a taskbar button: style %#x", ex)
	}
	return nil
}
