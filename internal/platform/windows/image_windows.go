//go:build windows

package windows

import (
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"unsafe"
)

const (
	imagePrevious = 1100 + iota
	imageNext
	imageZoomOut
	imageZoomIn
	imageActual
	imageFit
)

var (
	procStretchDIBits     = gdi32.NewProc("StretchDIBits")
	procSetStretchBltMode = gdi32.NewProc("SetStretchBltMode")
	procSaveDC            = gdi32.NewProc("SaveDC")
	procRestoreDC         = gdi32.NewProc("RestoreDC")
	procIntersectClipRect = gdi32.NewProc("IntersectClipRect")
)

type imageBitmapHeader struct {
	size                        uint32
	width, height               int32
	planes, bitCount            uint16
	compression, sizeImage      uint32
	xPixels, yPixels            int32
	colorsUsed, colorsImportant uint32
}

type imageViewer struct {
	window   *Window
	hwnd     uintptr
	buttons  [6]uintptr
	label    uintptr
	paths    []string
	index    int
	pixels   []byte
	view     imageView
	fit      bool
	dragging bool
	anchor   point
}

func (w *Window) closeViewerFor(path string) {
	if w.viewer == nil || w.viewer.hwnd == 0 {
		return
	}
	for _, shown := range w.viewer.paths {
		if shown == path {
			procDestroyWindow.Call(w.viewer.hwnd)
			return
		}
	}
}

func (v *imageViewer) ownsControl(hwnd uintptr) bool {
	if v.hwnd == 0 {
		return false
	}
	if hwnd == v.hwnd {
		return true
	}
	for _, button := range v.buttons {
		if hwnd == button {
			return true
		}
	}
	return false
}

func (w *Window) showImages(paths []string, index int) {
	if index < 0 || index >= len(paths) {
		return
	}
	// Decode before replacing the displayed image, so a missing/corrupt file
	// leaves the previous image intact and reports the error in the main window.
	pixels, width, height, err := loadScreenshot(paths[index])
	if err != nil {
		w.setStatus("Не удалось открыть снимок: " + err.Error())
		return
	}
	if w.viewer == nil {
		w.viewer = &imageViewer{window: w}
	}
	v := w.viewer
	if v.hwnd == 0 {
		if err := v.create(); err != nil {
			w.setStatus("Не удалось открыть просмотрщик: " + err.Error())
			return
		}
	}
	v.paths, v.index, v.pixels = append([]string(nil), paths...), index, pixels
	v.view = imageView{width: width, height: height, scale: 1}
	v.fit = true
	v.resize()
	procShowWindow.Call(v.hwnd, swShow)
	procSetForegroundWindow.Call(v.hwnd)
}

func (v *imageViewer) create() error {
	instance, _, _ := procGetModuleHandleW.Call(0)
	class, title := utf16("SolveoNativeHost"), utf16("Solveo — снимок")
	hwnd, _, err := procCreateWindowExW.Call(wsExToolWindow|0x8, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow|wsClipChildren, cwUseDefault, cwUseDefault, 960, 700, v.window.hwnd, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("create image window: %w", err)
	}
	v.hwnd = hwnd
	setTitleBarTheme(hwnd, v.window.dark)
	if ok, _, err := procSetWindowDisplayAffinity.Call(hwnd, wdaExcludeFromCapture); ok == 0 {
		procDestroyWindow.Call(hwnd)
		return fmt.Errorf("SetWindowDisplayAffinity(image window): %w", err)
	}
	for i, label := range []string{"←", "→", "−", "+", "1:1", "Вписать"} {
		cls, text := utf16("BUTTON"), utf16(label)
		h, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(text)),
			wsChild|wsVisible|bsOwnerDraw, 0, 0, 80, 28, hwnd, uintptr(imagePrevious+i), instance, 0)
		if h == 0 {
			procDestroyWindow.Call(hwnd)
			return fmt.Errorf("create image button: %w", err)
		}
		v.buttons[i] = h
		procSendMessageW.Call(h, wmSetFont, uiFont(), 1)
	}
	cls, text := utf16("STATIC"), utf16("")
	v.label, _, err = procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(text)),
		wsChild|wsVisible, 0, 0, 300, 24, hwnd, 0, instance, 0)
	if v.label == 0 {
		procDestroyWindow.Call(hwnd)
		return fmt.Errorf("create image caption: %w", err)
	}
	procSendMessageW.Call(v.label, wmSetFont, uiFont(), 1)
	return nil
}

func (v *imageViewer) clientSize() (int, int) {
	var r rect
	procGetClientRect.Call(v.hwnd, uintptr(unsafe.Pointer(&r)))
	return max(1, int(r.right)-24), max(1, int(r.bottom)-90)
}

func (v *imageViewer) resize() {
	areaW, areaH := v.clientSize()
	x := 12
	for i, h := range v.buttons {
		width := 42
		if i >= 4 {
			width = 76
		}
		procMoveWindow.Call(h, uintptr(x), 12, uintptr(width), 28, 0)
		x += width + 6
	}
	procMoveWindow.Call(v.label, 12, uintptr(areaH+62), uintptr(areaW), 22, 0)
	if v.fit {
		v.view.scale = fitImageScale(v.view.width, v.view.height, areaW, areaH)
		v.view.panX, v.view.panY = 0, 0
	}
	v.view.layout(areaW, areaH)
	v.refresh()
}

func (v *imageViewer) refresh() {
	if len(v.paths) > 0 {
		setText(v.hwnd, fmt.Sprintf("Solveo — %d/%d · %s", v.index+1, len(v.paths), filepath.Base(v.paths[v.index])))
	}
	setText(v.label, fmt.Sprintf("%d×%d · %.0f%% · Колесо — масштаб, перетаскивание — перемещение", v.view.width, v.view.height, v.view.scale*100))
	for i, enabled := range []bool{v.index > 0, v.index+1 < len(v.paths)} {
		value := uintptr(0)
		if enabled {
			value = 1
		}
		procEnableWindow.Call(v.buttons[i], value)
	}
	procRedrawWindow.Call(v.hwnd, 0, 0, 0x0185)
}

func (v *imageViewer) zoom(scale float64) {
	v.fit = false
	old := v.view.scale
	v.view.scale = max(0.01, min(8, scale))
	if old > 0 {
		v.view.panX *= v.view.scale / old
		v.view.panY *= v.view.scale / old
	}
	areaW, areaH := v.clientSize()
	v.view.layout(areaW, areaH)
	v.refresh()
}

func (v *imageViewer) paint() {
	var ps selectionPaint // PAINTSTRUCT layout shared with the region selector.
	hdc, _, _ := selectionBeginPaint.Call(v.hwnd, uintptr(unsafe.Pointer(&ps)))
	defer selectionEndPaint.Call(v.hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	paintBackground(v.hwnd, hdc, v.window.dark)
	if len(v.pixels) == 0 {
		return
	}
	areaW, areaH := v.clientSize()
	x, y, width, height := v.view.layout(areaW, areaH)
	saved, _, _ := procSaveDC.Call(hdc)
	defer procRestoreDC.Call(hdc, saved)
	procIntersectClipRect.Call(hdc, 12, 50, uintptr(12+areaW), uintptr(50+areaH))
	procSetStretchBltMode.Call(hdc, 3) // COLORONCOLOR; preserves hard screenshot edges.
	header := imageBitmapHeader{size: 40, width: int32(v.view.width), height: -int32(v.view.height), planes: 1, bitCount: 32}
	procStretchDIBits.Call(hdc, uintptr(x+12), uintptr(y+50), uintptr(width), uintptr(height),
		0, 0, uintptr(v.view.width), uintptr(v.view.height), uintptr(unsafe.Pointer(&v.pixels[0])), uintptr(unsafe.Pointer(&header)), 0, 0x00CC0020)
	runtime.KeepAlive(v.pixels)
	runtime.KeepAlive(header)
}

func (v *imageViewer) windowProc(msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmCommand:
		switch int(uint16(wparam)) {
		case imagePrevious:
			v.window.showImages(v.paths, v.index-1)
		case imageNext:
			v.window.showImages(v.paths, v.index+1)
		case imageZoomIn:
			v.zoom(v.view.scale * 1.25)
		case imageZoomOut:
			v.zoom(v.view.scale / 1.25)
		case imageActual:
			v.zoom(1)
		case imageFit:
			v.fit = true
			v.resize()
		}
		return 0
	case 0x020A: // WM_MOUSEWHEEL
		v.zoom(v.view.scale * math.Pow(1.25, float64(int16(wparam>>16))/120))
		return 0
	case selectionWmLDown:
		v.dragging, v.anchor = true, selectionPoint(lparam)
		selectionSetCapture.Call(v.hwnd)
		selectionSetFocus.Call(v.hwnd)
		return 0
	case selectionWmMouseMove:
		if v.dragging {
			p := selectionPoint(lparam)
			v.view.panX += float64(p.x - v.anchor.x)
			v.view.panY += float64(p.y - v.anchor.y)
			v.anchor = p
			areaW, areaH := v.clientSize()
			v.view.layout(areaW, areaH)
			selectionInvalidateRect.Call(v.hwnd, 0, 0)
		}
		return 0
	case selectionWmLUp:
		v.dragging = false
		selectionReleaseCapture.Call()
		return 0
	case selectionWmCaptureEnd:
		v.dragging = false
		return 0
	case selectionWmPaint:
		v.paint()
		return 0
	case wmEraseBkgnd:
		return 1 // paint clears the complete canvas in one pass.
	case wmSize:
		v.resize()
		return 0
	case wmGetMinMaxInfo:
		limitMainWindowSize(lparam)
		return 0
	case wmClose:
		procDestroyWindow.Call(v.hwnd)
		return 0
	case wmDestroy:
		v.hwnd, v.label = 0, 0
		v.buttons = [6]uintptr{}
		v.pixels, v.paths = nil, nil
		v.dragging = false
		return 0
	case wmKeyDown:
		if wparam == selectionEsc {
			procDestroyWindow.Call(v.hwnd)
			return 0
		}
	}
	if brush := controlColor(msg, wparam, lparam, 0, v.window.dark); brush != 0 {
		return brush
	}
	if msg == wmDrawItem && drawThemeButton(lparam, v.window.dark) {
		return 1
	}
	ret, _, _ := procDefWindowProcW.Call(v.hwnd, uintptr(msg), wparam, lparam)
	return ret
}
