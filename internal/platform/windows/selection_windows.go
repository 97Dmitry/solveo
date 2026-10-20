//go:build windows

package windows

import (
	"fmt"
	"image"
	"syscall"
	"unsafe"
)

// The selection window is a top-level, capture-excluded layered Win32 window.
// A color-keyed hole reveals the selected area while the rest is dimmed.
// Mouse capture keeps mouse-up routed here even when the pointer is in that hole.
const (
	selectionWsPopup      = 0x80000000
	selectionWsExTopmost  = 0x00000008
	selectionWsExLayered  = 0x00080000
	selectionLwaColorKey  = 0x00000001
	selectionLwaAlpha     = 0x00000002
	selectionWmPaint      = 0x000F
	selectionWmSetCursor  = 0x0020
	selectionWmErase      = 0x0014
	selectionWmKeyDown    = 0x0100
	selectionWmLDown      = 0x0201
	selectionWmLUp        = 0x0202
	selectionWmMouseMove  = 0x0200
	selectionWmRDown      = 0x0204
	selectionWmCaptureEnd = 0x0215
	selectionEsc          = 0x1B
)

var (
	selectionGetMetrics      = user32.NewProc("GetSystemMetrics")
	selectionDPIAware        = user32.NewProc("SetProcessDPIAware")
	selectionSetLayered      = user32.NewProc("SetLayeredWindowAttributes")
	selectionSetFocus        = user32.NewProc("SetFocus")
	selectionSetCapture      = user32.NewProc("SetCapture")
	selectionReleaseCapture  = user32.NewProc("ReleaseCapture")
	selectionInvalidateRect  = user32.NewProc("InvalidateRect")
	selectionBeginPaint      = user32.NewProc("BeginPaint")
	selectionEndPaint        = user32.NewProc("EndPaint")
	selectionLoadCursor      = user32.NewProc("LoadCursorW")
	selectionGetCursorInfo   = user32.NewProc("GetCursorInfo")
	selectionSetCursor       = user32.NewProc("SetCursor")
	selectionDeleteObject    = gdi32.NewProc("DeleteObject")
	selectionProcCallback    = syscall.NewCallback(selectionWindowProc)
	selectionClassRegistered bool
	activeSelection          *regionSelector
)

// Matches the Windows CURSORINFO structure on both 32- and 64-bit Windows.
type selectionCursorInfo struct {
	size     uint32
	flags    uint32
	hCursor  uintptr
	position point
}

type selectionPaint struct {
	hdc       uintptr
	erase     int32
	area      rect
	restore   int32
	incUpdate int32
	reserved  [32]byte
}

type regionSelector struct {
	hwnd          uintptr
	width         int32
	height        int32
	anchor        point
	cursor        point
	dragging      bool
	closing       bool
	displayCursor uintptr
	dimBrush      uintptr
	holeBrush     uintptr
	edgeBrush     uintptr
	onComplete    func(image.Rectangle, bool)
}

func primarySelectionBounds() (image.Rectangle, error) {
	ww, _, _ := selectionGetMetrics.Call(0) // SM_CXSCREEN
	hh, _, _ := selectionGetMetrics.Call(1) // SM_CYSCREEN
	w, h := int(int32(ww)), int(int32(hh))
	if w <= 0 || h <= 0 || w > 32768 || h > 32768 || int64(w)*int64(h) > 100_000_000 {
		return image.Rectangle{}, fmt.Errorf("invalid primary display dimensions: %dx%d", w, h)
	}
	return image.Rect(0, 0, w, h), nil
}

func (w *Window) startRegionSelection(done func(image.Rectangle, bool)) error {
	if activeSelection != nil {
		return fmt.Errorf("region selection is already active")
	}
	bounds, err := primarySelectionBounds()
	if err != nil {
		return err
	}
	instance, _, _ := procGetModuleHandleW.Call(0)
	className := utf16("SolveoRegionSelector")
	if !selectionClassRegistered {
		wc := wndClassEx{
			size:      uint32(unsafe.Sizeof(wndClassEx{})),
			wndProc:   selectionProcCallback,
			instance:  instance,
			className: className,
		}
		if ok, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ok == 0 {
			return fmt.Errorf("register selection window: %w", callErr)
		}
		selectionClassRegistered = true
	}
	s := &regionSelector{
		width:      int32(bounds.Dx()),
		height:     int32(bounds.Dy()),
		onComplete: done,
	}
	s.dimBrush, _, _ = procCreateSolidBrush.Call(0x00242424)
	s.holeBrush, _, _ = procCreateSolidBrush.Call(0x00000000)
	s.edgeBrush, _, _ = procCreateSolidBrush.Call(0x00FFFFFF)
	if s.dimBrush == 0 || s.holeBrush == 0 || s.edgeBrush == 0 {
		s.releaseBrushes()
		return fmt.Errorf("create selection brushes")
	}
	// Preserve the currently visible pointer shape while the user drags a region.
	// The overlay has no class cursor: WM_SETCURSOR reapplies this handle instead
	// of switching to IDC_CROSS (which can be visible to screen-share viewers).
	var cursorInfo selectionCursorInfo
	cursorInfo.size = uint32(unsafe.Sizeof(cursorInfo))
	if ok, _, _ := selectionGetCursorInfo.Call(uintptr(unsafe.Pointer(&cursorInfo))); ok != 0 {
		if cursorInfo.flags&1 != 0 { // CURSOR_SHOWING
			s.displayCursor = cursorInfo.hCursor
		}
	} else {
		// Only fall back to the normal arrow if reading the cursor fails.
		s.displayCursor, _, _ = selectionLoadCursor.Call(0, 32512) // IDC_ARROW
	}

	activeSelection = s
	hwnd, _, callErr := procCreateWindowExW.Call(
		selectionWsExTopmost|selectionWsExLayered|wsExToolWindow,
		uintptr(unsafe.Pointer(className)), 0, selectionWsPopup,
		0, 0, uintptr(s.width), uintptr(s.height), 0, 0, instance, 0,
	)
	if hwnd == 0 {
		activeSelection = nil
		s.releaseBrushes()
		return fmt.Errorf("create selection window: %w", callErr)
	}
	s.hwnd = hwnd
	// Fail closed: never display this window if exclusion is unsupported.
	if ok, _, callErr := procSetWindowDisplayAffinity.Call(hwnd, wdaExcludeFromCapture); ok == 0 {
		s.complete(image.Rectangle{}, false)
		return fmt.Errorf("exclude selection window from capture: %w", callErr)
	}
	// Black is fully transparent; dim gray is partially opaque.
	if ok, _, callErr := selectionSetLayered.Call(
		hwnd, 0, 145, selectionLwaColorKey|selectionLwaAlpha,
	); ok == 0 {
		s.complete(image.Rectangle{}, false)
		return fmt.Errorf("configure selection transparency: %w", callErr)
	}
	procShowWindow.Call(hwnd, swShow)
	procSetForegroundWindow.Call(hwnd)
	selectionSetFocus.Call(hwnd)
	return nil
}

func (s *regionSelector) releaseBrushes() {
	for _, brush := range []uintptr{s.dimBrush, s.holeBrush, s.edgeBrush} {
		if brush != 0 {
			selectionDeleteObject.Call(brush)
		}
	}
	s.dimBrush, s.holeBrush, s.edgeBrush = 0, 0, 0
}

func selectionPoint(lparam uintptr) point {
	return point{
		x: int32(int16(uint16(lparam))),
		y: int32(int16(uint16(lparam >> 16))),
	}
}

func (s *regionSelector) selectedRect() image.Rectangle {
	x1, x2 := min(s.anchor.x, s.cursor.x), max(s.anchor.x, s.cursor.x)
	y1, y2 := min(s.anchor.y, s.cursor.y), max(s.anchor.y, s.cursor.y)
	return image.Rect(int(x1), int(y1), int(x2), int(y2)).
		Intersect(image.Rect(0, 0, int(s.width), int(s.height)))
}

func (s *regionSelector) paint() {
	var ps selectionPaint
	dc, _, _ := selectionBeginPaint.Call(s.hwnd, uintptr(unsafe.Pointer(&ps)))
	if dc == 0 {
		return
	}
	defer selectionEndPaint.Call(s.hwnd, uintptr(unsafe.Pointer(&ps)))
	full := rect{right: s.width, bottom: s.height}
	procFillRect.Call(dc, uintptr(unsafe.Pointer(&full)), s.dimBrush)
	if !s.dragging {
		return
	}
	area := s.selectedRect()
	if area.Empty() {
		return
	}
	r := rect{left: int32(area.Min.X), top: int32(area.Min.Y), right: int32(area.Max.X), bottom: int32(area.Max.Y)}
	procFillRect.Call(dc, uintptr(unsafe.Pointer(&r)), s.holeBrush)
	procFrameRect.Call(dc, uintptr(unsafe.Pointer(&r)), s.edgeBrush)
}

func (s *regionSelector) complete(region image.Rectangle, ok bool) {
	if s.closing {
		return
	}
	s.closing = true
	if s.dragging {
		selectionReleaseCapture.Call()
	}
	procShowWindow.Call(s.hwnd, swHide)
	procDestroyWindow.Call(s.hwnd)
	if activeSelection == s {
		activeSelection = nil
	}
	s.releaseBrushes()
	s.onComplete(region, ok)
}

func selectionWindowProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	s := activeSelection
	if s != nil && s.hwnd == hwnd {
		switch msg {
		case selectionWmSetCursor:
			selectionSetCursor.Call(s.displayCursor)
			return 1
		case selectionWmLDown:
			s.anchor, s.cursor = selectionPoint(lparam), selectionPoint(lparam)
			s.dragging = true
			selectionSetCapture.Call(hwnd)
			return 0
		case selectionWmMouseMove:
			if s.dragging {
				s.cursor = selectionPoint(lparam)
				selectionInvalidateRect.Call(hwnd, 0, 0)
			}
			return 0
		case selectionWmLUp:
			if s.dragging {
				s.cursor = selectionPoint(lparam)
				region := s.selectedRect()
				s.complete(region, !region.Empty())
			}
			return 0
		case selectionWmKeyDown:
			if wparam == selectionEsc {
				s.complete(image.Rectangle{}, false)
				return 0
			}
		case selectionWmRDown, wmClose, selectionWmCaptureEnd:
			s.complete(image.Rectangle{}, false)
			return 0
		case selectionWmPaint:
			s.paint()
			return 0
		case selectionWmErase:
			return 1
		case wmDestroy:
			// Also handle external window destruction (e.g., session shutdown).
			if !s.closing {
				s.closing = true
				activeSelection = nil
				s.releaseBrushes()
				s.onComplete(image.Rectangle{}, false)
			}
			return 0
		}
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return ret
}
