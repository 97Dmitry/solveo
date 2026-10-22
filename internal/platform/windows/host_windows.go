//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"image"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/97Dmitry/solveo/internal/platform"
)

const wmHostCommand = 0x8008

// Window is accessed exclusively from the locked native message-loop thread.
type Window struct {
	viewer                         *imageViewer
	logHwnd, logEdit, cancelButton uintptr
	logText                        string
	hwnd, icon                     uintptr
	trayAdded                      bool
	dark                           bool
	taskbarCreated                 uint32
	onEvent                        func(platform.Event)
	commands                       chan func()
}
type Host struct {
	mu     sync.Mutex
	window *Window
	done   chan struct{}
}

func (w *Window) emit(kind string) {
	if w.onEvent != nil {
		w.onEvent(platform.Event{Kind: kind})
	}
}
func (h *Host) Start(ctx context.Context, onEvent func(platform.Event)) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.window != nil {
		return errors.New("native host already started")
	}
	ready := make(chan error, 1)
	h.done = make(chan struct{})
	w := &Window{onEvent: onEvent, commands: make(chan func(), 128)}
	h.window = w
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(h.done)
		current = w
		err := w.create()
		ready <- err
		if err != nil {
			current = nil
			return
		}
		defer func() {
			w.removeTrayIcon()
			w.destroyTrayIcon()
			for i := 2001; i <= 2005; i++ {
				procUnregisterHotKey.Call(w.hwnd, uintptr(i))
			}
			current = nil
		}()
		go func() {
			select {
			case <-ctx.Done():
				_ = h.Close()
			case <-h.done:
			}
		}()
		var m message
		for {
			v, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(v) <= 0 {
				break
			}
			if m.msg == wmKeyDown && m.wparam == selectionEsc && w.viewer != nil && w.viewer.ownsControl(m.hwnd) {
				procDestroyWindow.Call(w.viewer.hwnd)
				continue
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	return <-ready
}
func (w *Window) create() error {
	if err := initializeTheme(); err != nil {
		return err
	}
	instance, _, _ := procGetModuleHandleW.Call(0)
	class := utf16("SolveoNativeHost")
	wc := wndClassEx{size: uint32(unsafe.Sizeof(wndClassEx{})), wndProc: windowCallback, instance: instance, className: class}
	if ok, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ok == 0 {
		return fmt.Errorf("register host: %w", err)
	}
	hwnd, _, err := procCreateWindowExW.Call(wsExToolWindow, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("create host: %w", err)
	}
	w.hwnd = hwnd
	success := false
	defer func() {
		if !success {
			w.removeTrayIcon()
			w.destroyTrayIcon()
			for i := 2001; i <= 2005; i++ {
				procUnregisterHotKey.Call(hwnd, uintptr(i))
			}
			procDestroyWindow.Call(hwnd)
		}
	}()
	if err := EnsureExcluded(hwnd); err != nil {
		return err
	}
	w.icon, err = loadTrayIcon()
	if err != nil {
		return err
	}
	if err := w.addTrayIcon(); err != nil {
		return err
	}
	for i, key := range []uintptr{keyS, keyO, keyA, keyEnter, keyF} {
		if ok, _, err := procRegisterHotKey.Call(hwnd, uintptr(2001+i), modCtrl|modAlt|modNoRepeat, key); ok == 0 {
			return fmt.Errorf("register hotkey %d: %w", i, err)
		}
	}
	success = true
	return nil
}
func (h *Host) post(f func()) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.window == nil {
		return errors.New("native host unavailable")
	}
	select {
	case <-h.done:
		return errors.New("native host stopped")
	default:
	}
	select {
	case h.window.commands <- f:
	default:
		return errors.New("native command queue full")
	}
	if ok, _, err := procPostMessageW.Call(h.window.hwnd, wmHostCommand, 0, 0); ok == 0 {
		return fmt.Errorf("post native command: %w", err)
	}
	return nil
}
func (h *Host) BeginSelection(id string) error {
	return h.post(func() {
		w := h.window
		err := w.startRegionSelection(func(r image.Rectangle, ok bool) {
			e := platform.Event{Kind: "selection", OperationID: id, Region: r}
			if !ok {
				e.Kind = "selection-cancelled"
			}
			w.onEvent(e)
		})
		if err != nil {
			w.onEvent(platform.Event{Kind: "selection", OperationID: id, Err: err})
		}
	})
}

// call is for commands that must finish before the caller removes source files.
// It must never be called by the native window procedure.
func (h *Host) call(f func()) error {
	ack := make(chan struct{})
	if err := h.post(func() { defer close(ack); f() }); err != nil {
		return err
	}
	select {
	case <-ack:
		return nil
	case <-h.done:
		return errors.New("native host stopped before command acknowledgement")
	case <-time.After(3 * time.Second):
		return errors.New("native command acknowledgement timed out")
	}
}
func (h *Host) Close() error {
	err := h.post(func() {
		if activeSelection != nil {
			activeSelection.complete(image.Rectangle{}, false)
		}
		procDestroyWindow.Call(h.window.hwnd)
	})
	if err != nil {
		select {
		case <-h.done:
			return nil
		default:
			return err
		}
	}
	select {
	case <-h.done:
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("native host shutdown timed out")
	}
}

func windowProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	w := current
	if w != nil {
		if w.viewer != nil && w.viewer.hwnd == hwnd {
			return w.viewer.windowProc(msg, wp, lp)
		}
		if w.logHwnd != 0 && w.logHwnd == hwnd {
			return w.logProc(hwnd, msg, wp, lp)
		}
		if w.taskbarCreated != 0 && msg == w.taskbarCreated {
			w.trayAdded = false
			if err := w.addTrayIcon(); err != nil {
				w.onEvent(platform.Event{Kind: "fatal", Err: err})
			}
			return 0
		}
		switch msg {
		case wmHostCommand:
			for {
				select {
				case f := <-w.commands:
					f()
				default:
					return 0
				}
			}
		case wmHotkey:
			switch wp {
			case 2001:
				w.emit("quick-region")
			case 2002:
				w.emit("toggle")
			case 2003:
				w.emit("queue-region")
			case 2004:
				w.emit("submit")
			case 2005:
				w.emit("quick-full")
			}
			return 0
		case wmAppTray:
			if uint32(lp) == wmLButtonDblClk {
				w.emit("show")
			}
			if uint32(lp) == wmRButtonUp {
				w.trayMenu()
			}
			return 0
		case wmCommand:
			switch wp & 0xffff {
			case menuShow:
				w.emit("show")
			case menuExit:
				w.emit("exit")
			case menuLog:
				w.emit("log")
			case menuTheme:
				w.emit("theme")
			case menuPin:
				w.emit("pin")
			}
			return 0
		case wmDestroy:
			if w.viewer != nil && w.viewer.hwnd != 0 {
				procDestroyWindow.Call(w.viewer.hwnd)
			}
			if w.logHwnd != 0 {
				procDestroyWindow.Call(w.logHwnd)
			}
			procPostQuitMessage.Call(0)
			return 0
		}
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
	return ret
}
