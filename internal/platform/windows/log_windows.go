//go:build windows

package windows

import (
	"fmt"
	"github.com/97Dmitry/solveo/internal/platform"
	"unsafe"
)

func setText(hwnd uintptr, text string) {
	p := utf16(text)
	procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(p)))
}
func (w *Window) setStatus(text string) {
	w.onEvent(platform.Event{Kind: "error", Err: fmt.Errorf("%s", text)})
}
func (h *Host) ShowImages(images []platform.ViewImage, index int) error {
	paths := make([]string, len(images))
	for i, v := range images {
		paths[i] = v.Path
	}
	return h.post(func() { h.window.showImages(paths, index) })
}
func (h *Host) CloseImages() error {
	return h.call(func() {
		w := h.window
		if w.viewer != nil && w.viewer.hwnd != 0 {
			procDestroyWindow.Call(w.viewer.hwnd)
		}
	})
}
func (h *Host) RebindImages(remap map[string]string) error {
	return h.call(func() {
		v := h.window.viewer
		if v == nil {
			return
		}
		for i, p := range v.paths {
			if to, ok := remap[p]; ok {
				v.paths[i] = to
			}
		}
	})
}
func (h *Host) ShowLog() error {
	return h.post(func() {
		w := h.window
		if w.logHwnd == 0 {
			if e := w.createLog(); e != nil {
				w.setStatus(e.Error())
				return
			}
		}
		procShowWindow.Call(w.logHwnd, swShow)
		procSetForegroundWindow.Call(w.logHwnd)
	})
}
func (h *Host) SetLog(text string) error {
	return h.post(func() {
		w := h.window
		w.logText = text
		if w.logEdit != 0 {
			setText(w.logEdit, text)
		}
	})
}
func (h *Host) SetTheme(dark bool) error {
	return h.post(func() {
		w := h.window
		w.dark = dark
		for _, hwnd := range []uintptr{w.logHwnd} {
			if hwnd != 0 {
				setTitleBarTheme(hwnd, dark)
				procRedrawWindow.Call(hwnd, 0, 0, 0x185)
			}
		}
		if w.viewer != nil && w.viewer.hwnd != 0 {
			setTitleBarTheme(w.viewer.hwnd, dark)
			w.viewer.refresh()
		}
	})
}
func (w *Window) createLog() error {
	instance, _, _ := procGetModuleHandleW.Call(0)
	class, title := utf16("SolveoNativeHost"), utf16("Solveo — журнал")
	hwnd, _, e := procCreateWindowExW.Call(wsExToolWindow|0x8, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), wsOverlappedWindow|wsClipChildren, cwUseDefault, cwUseDefault, 780, 420, w.hwnd, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("create log: %w", e)
	}
	w.logHwnd = hwnd
	if e := EnsureExcluded(hwnd); e != nil {
		procDestroyWindow.Call(hwnd)
		w.logHwnd = 0
		return e
	}
	success := false
	defer func() {
		if !success {
			procDestroyWindow.Call(hwnd)
			w.logHwnd = 0
		}
	}()
	cls := utf16("EDIT")
	edit, _, e := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), 0, wsChild|wsVisible|wsVScroll|wsBorder|esMultiline|esAutoVScroll|esReadOnly, 12, 12, 720, 320, hwnd, 0, instance, 0)
	if edit == 0 {
		return fmt.Errorf("create log text: %w", e)
	}
	w.logEdit = edit
	procSendMessageW.Call(edit, wmSetFont, uiFont(), 1)
	procSendMessageW.Call(edit, emSetLimitText, 128<<10, 0)
	setText(edit, w.logText)
	cls = utf16("BUTTON")
	label := utf16("Отменить запрос")
	button, _, e := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(label)), wsChild|wsVisible|bsOwnerDraw, 12, 340, 160, 30, hwnd, btnCancel, instance, 0)
	if button == 0 {
		return fmt.Errorf("create log cancel: %w", e)
	}
	w.cancelButton = button
	procSendMessageW.Call(button, wmSetFont, uiFont(), 1)
	setTitleBarTheme(hwnd, w.dark)
	success = true
	return nil
}
func (w *Window) logProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case wmClose:
		procShowWindow.Call(hwnd, swHide)
		return 0
	case wmDestroy:
		w.logHwnd = 0
		w.logEdit = 0
		w.cancelButton = 0
		return 0
	case wmSize:
		var r rect
		procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		procMoveWindow.Call(w.logEdit, 12, 12, uintptr(max(20, r.right-24)), uintptr(max(20, r.bottom-66)), 0)
		procMoveWindow.Call(w.cancelButton, 12, uintptr(max(12, r.bottom-42)), 160, 30, 0)
		procRedrawWindow.Call(hwnd, 0, 0, 0x185)
		return 0
	case wmCommand:
		if wp&0xffff == btnCancel {
			w.emit("cancel")
		}
		return 0
	case wmEraseBkgnd:
		paintBackground(hwnd, wp, w.dark)
		return 1
	case wmDrawItem:
		if drawThemeButton(lp, w.dark) {
			return 1
		}
	case wmCtlColorEdit, wmCtlColorStatic, wmCtlColorBtn:
		return controlColor(msg, wp, lp, w.logEdit, w.dark)
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
	return ret
}
