//go:build windows

package windows

import (
	"errors"
	"syscall"
	"unsafe"
)

// Win32 COLORREF is 0x00BBGGRR. Neutral greys avoid RGB/BGR confusion.
const (
	colorWindow = 0x1E1E1E
	colorField  = 0x262626
	colorButton = 0x303030
	colorPress  = 0x434343
	colorText   = 0xF0F0F0
	colorMuted  = 0xB0B0B0
	colorEdge   = 0x505050
	lightWindow = 0xF7F7F7
	lightField  = 0xFFFFFF
	lightButton = 0xEEEEEE
	lightPress  = 0xD5D5D5
	lightText   = 0x202020
	lightMuted  = 0x777777
	lightEdge   = 0xCCCCCC

	transparent  = 1
	opaque       = 2
	odtButton    = 4
	odsSelected  = 0x0001
	odsDisabled  = 0x0004
	odsFocus     = 0x0010
	dtCenter     = 0x0001
	dtVCenter    = 0x0004
	dtSingleLine = 0x0020
	dtNoPrefix   = 0x0800
)

var (
	dwmapi                    = syscall.NewLazyDLL("dwmapi.dll")
	ntdll                     = syscall.NewLazyDLL("ntdll.dll")
	uxtheme                   = syscall.NewLazyDLL("uxtheme.dll")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procSetWindowTheme        = uxtheme.NewProc("SetWindowTheme")
	procCreateSolidBrush      = gdi32.NewProc("CreateSolidBrush")
	procSetTextColor          = gdi32.NewProc("SetTextColor")
	procSetBkColor            = gdi32.NewProc("SetBkColor")
	procSetBkMode             = gdi32.NewProc("SetBkMode")
	procFillRect              = user32.NewProc("FillRect")
	procFrameRect             = user32.NewProc("FrameRect")
	procDrawTextW             = user32.NewProc("DrawTextW")
	procDrawFocusRect         = user32.NewProc("DrawFocusRect")
	procSelectObject          = gdi32.NewProc("SelectObject")
	procCreateFontW           = gdi32.NewProc("CreateFontW")
	procRoundRect             = gdi32.NewProc("RoundRect")
	procCreatePen             = gdi32.NewProc("CreatePen")
	procMoveMemory            = ntdll.NewProc("RtlMoveMemory")

	uiFontHandle         uintptr
	backgroundBrush      uintptr
	fieldBrush           uintptr
	buttonBrush          uintptr
	pressedBrush         uintptr
	edgeBrush            uintptr
	edgePen              uintptr
	lightEdgePen         uintptr
	lightBackgroundBrush uintptr
	lightFieldBrush      uintptr
	lightButtonBrush     uintptr
	lightPressedBrush    uintptr
	lightEdgeBrush       uintptr
)

type drawItem struct {
	controlType uint32
	controlID   uint32
	itemID      uint32
	itemAction  uint32
	itemState   uint32
	hwnd        uintptr
	hdc         uintptr
	area        rect
	itemData    uintptr
}

// uiFont returns a shared, application-owned control font.
func uiFont() uintptr {
	if uiFontHandle == 0 {
		family := utf16("Segoe UI")
		uiFontHandle, _, _ = procCreateFontW.Call(
			^uintptr(17), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0,
			uintptr(unsafe.Pointer(family)),
		)
		if uiFontHandle == 0 {
			uiFontHandle, _, _ = procGetStockObject.Call(defaultGUIFont)
		}
	}
	return uiFontHandle
}

func darkBackgroundBrush() uintptr {
	if backgroundBrush == 0 {
		backgroundBrush, _, _ = procCreateSolidBrush.Call(colorWindow)
		fieldBrush, _, _ = procCreateSolidBrush.Call(colorField)
		buttonBrush, _, _ = procCreateSolidBrush.Call(colorButton)
		pressedBrush, _, _ = procCreateSolidBrush.Call(colorPress)
		edgeBrush, _, _ = procCreateSolidBrush.Call(colorEdge)
		edgePen, _, _ = procCreatePen.Call(0, 1, colorEdge)
		lightEdgePen, _, _ = procCreatePen.Call(0, 1, lightEdge)
		lightBackgroundBrush, _, _ = procCreateSolidBrush.Call(lightWindow)
		lightFieldBrush, _, _ = procCreateSolidBrush.Call(lightField)
		lightButtonBrush, _, _ = procCreateSolidBrush.Call(lightButton)
		lightPressedBrush, _, _ = procCreateSolidBrush.Call(lightPress)
		lightEdgeBrush, _, _ = procCreateSolidBrush.Call(lightEdge)
	}
	return backgroundBrush
}

// initializeTheme runs on the native host thread before creating any windows.
// A null GDI brush makes fills silently fail, including owner-drawn buttons.
func initializeTheme() error {
	darkBackgroundBrush()
	for _, handle := range []uintptr{backgroundBrush, fieldBrush, buttonBrush, pressedBrush, edgeBrush, edgePen, lightEdgePen, lightBackgroundBrush, lightFieldBrush, lightButtonBrush, lightPressedBrush, lightEdgeBrush, uiFont()} {
		if handle == 0 {
			return errors.New("initialize native theme resources failed")
		}
	}
	return nil
}

func setTitleBarTheme(hwnd uintptr, dark bool) {
	var enabled int32
	if dark {
		enabled = 1
	}
	// Unsupported Windows versions leave the normal system title bar in place.
	procDwmSetWindowAttribute.Call(hwnd, 20, uintptr(unsafe.Pointer(&enabled)), unsafe.Sizeof(enabled))
}

func setControlTheme(hwnd uintptr, dark bool) {
	// Native combo boxes and scrollbars use this theme when Windows supports it.
	if dark {
		name := utf16("DarkMode_Explorer")
		procSetWindowTheme.Call(hwnd, uintptr(unsafe.Pointer(name)), 0)
	} else {
		procSetWindowTheme.Call(hwnd, 0, 0)
	}
}

func controlColor(msg uint32, hdc, control, logEdit uintptr, dark bool) uintptr {
	text, field, button := uintptr(lightText), lightFieldBrush, lightButtonBrush
	fieldColor, buttonColor := uintptr(lightField), uintptr(lightButton)
	background := lightBackgroundBrush
	if dark {
		text, field, button = colorText, fieldBrush, buttonBrush
		fieldColor, buttonColor = colorField, colorButton
		background = backgroundBrush
	}
	switch msg {
	case wmCtlColorEdit, wmCtlColorListBox:
		procSetTextColor.Call(hdc, text)
		procSetBkColor.Call(hdc, fieldColor)
		return field
	case wmCtlColorStatic:
		procSetTextColor.Call(hdc, text)
		if logEdit != 0 && control == logEdit {
			// A read-only EDIT sends WM_CTLCOLORSTATIC. Its scrolled text
			// needs an opaque background so old glyphs are erased.
			procSetBkMode.Call(hdc, opaque)
			procSetBkColor.Call(hdc, fieldColor)
			return field
		}
		procSetBkMode.Call(hdc, transparent)
		return background
	case wmCtlColorBtn:
		procSetTextColor.Call(hdc, text)
		procSetBkColor.Call(hdc, buttonColor)
		return button
	}
	return 0
}

func paintBackground(hwnd, hdc uintptr, dark bool) {
	var area rect
	if ok, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&area))); ok == 0 {
		return
	}
	brush := lightBackgroundBrush
	if dark {
		brush = backgroundBrush
	}
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&area)), brush)
}

func drawThemeButton(data uintptr, dark bool) bool {
	if data == 0 {
		return false
	}
	var item drawItem
	procMoveMemory.Call(uintptr(unsafe.Pointer(&item)), data, unsafe.Sizeof(item))
	d := &item
	if d.controlType != odtButton {
		return false
	}
	brush := lightButtonBrush
	textColor, muted := uintptr(lightText), uintptr(lightMuted)
	if dark {
		brush = buttonBrush
		textColor, muted = colorText, colorMuted
	}
	if d.itemState&odsSelected != 0 {
		brush = lightPressedBrush
		if dark {
			brush = pressedBrush
		}
	}
	// Select a real GDI pen for the outline: a BRUSH cannot be selected as a PEN.
	pen := lightEdgePen
	if dark {
		pen = edgePen
	}
	// WS_CLIPCHILDREN keeps the parent from painting behind this control.
	// Clear the entire button rectangle before drawing its rounded outline.
	background := lightBackgroundBrush
	if dark {
		background = backgroundBrush
	}
	procFillRect.Call(d.hdc, uintptr(unsafe.Pointer(&d.area)), background)
	// Draw slightly inset rounded controls instead of default square Win32 buttons.
	box := d.area
	box.left++
	box.top++
	box.right--
	box.bottom--
	oldBrush, _, _ := procSelectObject.Call(d.hdc, brush)
	oldPen, _, _ := procSelectObject.Call(d.hdc, pen)
	procRoundRect.Call(d.hdc, uintptr(box.left), uintptr(box.top), uintptr(box.right), uintptr(box.bottom), 12, 12)
	procSelectObject.Call(d.hdc, oldPen)
	procSelectObject.Call(d.hdc, oldBrush)
	length, _, _ := procGetWindowTextLengthW.Call(d.hwnd)
	buf := make([]uint16, length+1)
	procGetWindowTextW.Call(d.hwnd, uintptr(unsafe.Pointer(&buf[0])), length+1)
	if d.itemState&odsDisabled != 0 {
		textColor = muted
	}
	procSetTextColor.Call(d.hdc, textColor)
	procSetBkMode.Call(d.hdc, transparent)
	old, _, _ := procSelectObject.Call(d.hdc, uiFont())
	procDrawTextW.Call(d.hdc, uintptr(unsafe.Pointer(&buf[0])), length,
		uintptr(unsafe.Pointer(&d.area)), dtCenter|dtVCenter|dtSingleLine|dtNoPrefix)
	procSelectObject.Call(d.hdc, old)
	if d.itemState&odsFocus != 0 {
		focus := d.area
		focus.left += 3
		focus.top += 3
		focus.right -= 3
		focus.bottom -= 3
		procDrawFocusRect.Call(d.hdc, uintptr(unsafe.Pointer(&focus)))
	}
	return true
}
