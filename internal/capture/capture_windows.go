//go:build windows

// Package capture implements an explicit screenshot of the primary Windows display.
package capture

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	gdi32                   = syscall.NewLazyDLL("gdi32.dll")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procSetProcessDPIAware  = user32.NewProc("SetProcessDPIAware")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC  = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC            = gdi32.NewProc("DeleteDC")
	procCreateCompatibleBmp = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject        = gdi32.NewProc("SelectObject")
	procDeleteObject        = gdi32.NewProc("DeleteObject")
	procBitBlt              = gdi32.NewProc("BitBlt")
	procGetDIBits           = gdi32.NewProc("GetDIBits")
)

const (
	smCXScreen = 0
	smCYScreen = 1
	srccopy    = 0x00CC0020
	captureBlt = 0x40000000
	biRGB      = 0
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Color  [1]uint32
}

func winError(operation string, err error) error {
	if err != nil && err != syscall.Errno(0) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s failed", operation)
}

// SavePrimary captures the primary display and saves a PNG at dst.
func SavePrimary(dst string) error { return SaveRegion(dst, image.Rectangle{}) }

// SaveRegion captures a physical-pixel rectangle of the primary display.
// An empty rectangle means the full primary display.
func SaveRegion(dst string, region image.Rectangle) error {
	// Avoid mismatches between logical screen dimensions and physical GDI pixels.
	// A failure here can mean DPI awareness was already set by the host.
	procSetProcessDPIAware.Call()

	ww, _, _ := procGetSystemMetrics.Call(smCXScreen)
	hh, _, _ := procGetSystemMetrics.Call(smCYScreen)
	screenW, screenH := int(int32(ww)), int(int32(hh))
	w, h := screenW, screenH
	if w <= 0 || h <= 0 || w > 32768 || h > 32768 || int64(w)*int64(h) > 100_000_000 {
		return fmt.Errorf("invalid primary display dimensions: %dx%d", w, h)
	}

	if region.Empty() {
		region = image.Rect(0, 0, w, h)
	}
	if err := ValidateRegion(region, image.Rect(0, 0, screenW, screenH)); err != nil {
		return err
	}
	w, h = region.Dx(), region.Dy()

	screenDC, _, callErr := procGetDC.Call(0)
	if screenDC == 0 {
		return winError("GetDC", callErr)
	}
	defer procReleaseDC.Call(0, screenDC)

	memoryDC, _, callErr := procCreateCompatibleDC.Call(screenDC)
	if memoryDC == 0 {
		return winError("CreateCompatibleDC", callErr)
	}
	defer procDeleteDC.Call(memoryDC)

	bitmap, _, callErr := procCreateCompatibleBmp.Call(screenDC, uintptr(w), uintptr(h))
	if bitmap == 0 {
		return winError("CreateCompatibleBitmap", callErr)
	}
	defer procDeleteObject.Call(bitmap)

	oldBitmap, _, callErr := procSelectObject.Call(memoryDC, bitmap)
	if oldBitmap == 0 || oldBitmap == ^uintptr(0) {
		return winError("SelectObject", callErr)
	}

	copied, _, callErr := procBitBlt.Call(
		memoryDC, 0, 0, uintptr(w), uintptr(h), screenDC, uintptr(region.Min.X), uintptr(region.Min.Y),
		srccopy|captureBlt,
	)
	// GetDIBits requires the bitmap NOT to be selected into a device context.
	procSelectObject.Call(memoryDC, oldBitmap)
	if copied == 0 {
		return winError("BitBlt", callErr)
	}

	bmi := bitmapInfo{Header: bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(w),
		Height:      -int32(h), // Top-down pixel rows.
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}}

	bgra := make([]byte, w*h*4)
	lines, _, callErr := procGetDIBits.Call(
		screenDC, bitmap, 0, uintptr(h),
		uintptr(unsafe.Pointer(&bgra[0])),
		uintptr(unsafe.Pointer(&bmi)), 0,
	)
	runtime.KeepAlive(bgra)
	runtime.KeepAlive(bmi)
	if int(lines) != h {
		return winError("GetDIBits", callErr)
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(bgra); i += 4 {
		img.Pix[i] = bgra[i+2]   // R
		img.Pix[i+1] = bgra[i+1] // G
		img.Pix[i+2] = bgra[i]   // B
		img.Pix[i+3] = 255       // A
	}

	file, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create PNG: %w", err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		os.Remove(dst)
		return fmt.Errorf("encode PNG: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("close PNG: %w", err)
	}
	return nil
}
