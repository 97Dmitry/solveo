//go:build windows

package windows

import (
	"runtime"
	"testing"
	"unsafe"
)

// Run on Windows: verifies actual GDI fills, not just palette constants.
func TestThemeBackgroundAndOwnerDrawButtonHaveVisibleFill(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initializeTheme(); err != nil {
		t.Fatal(err)
	}
	cls, text := utf16("BUTTON"), utf16("Вписать")
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(text)), 0, 0, 0, 180, 80, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatal(err)
	}
	defer procDestroyWindow.Call(hwnd)
	dc, _, _ := gdi32.NewProc("CreateCompatibleDC").Call(0)
	if dc == 0 {
		t.Fatal("CreateCompatibleDC")
	}
	defer gdi32.NewProc("DeleteDC").Call(dc)
	screen, _, _ := user32.NewProc("GetDC").Call(0)
	if screen == 0 {
		t.Fatal("GetDC")
	}
	bitmap, _, _ := gdi32.NewProc("CreateCompatibleBitmap").Call(screen, 180, 80)
	user32.NewProc("ReleaseDC").Call(0, screen)
	if bitmap == 0 {
		t.Fatal("CreateCompatibleBitmap")
	}
	defer gdi32.NewProc("DeleteObject").Call(bitmap)
	old, _, _ := procSelectObject.Call(dc, bitmap)
	defer procSelectObject.Call(dc, old)
	for _, dark := range []bool{false, true} {
		paintBackground(hwnd, dc, dark)
		want := uintptr(lightWindow)
		if dark {
			want = colorWindow
		}
		got, _, _ := gdi32.NewProc("GetPixel").Call(dc, 2, 2)
		if got != want {
			t.Fatalf("background dark=%v: got %#x want %#x", dark, got, want)
		}
		item := drawItem{controlType: odtButton, hwnd: hwnd, hdc: dc, area: rect{left: 5, top: 5, right: 160, bottom: 55}}
		if !drawThemeButton(uintptr(unsafe.Pointer(&item)), dark) {
			t.Fatal("button not drawn")
		}
		want = lightButton
		if dark {
			want = colorButton
		}
		got, _, _ = gdi32.NewProc("GetPixel").Call(dc, 20, 15)
		if got != want {
			t.Fatalf("button dark=%v: got %#x want %#x", dark, got, want)
		}
	}
}
