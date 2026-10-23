//go:build windows

package windows

import (
	"testing"
	"unsafe"
)

// Run on Windows: verify that the embedded PNG creates a real, correctly sized
// color icon, independently of the application resource compiled by Wails.
func TestEmbeddedTrayIconLoadsAtSystemSize(t *testing.T) {
	icon, err := loadTrayIcon()
	if err != nil {
		t.Fatal(err)
	}
	defer procDestroyIcon.Call(icon)
	var info struct {
		isIcon             uint32
		hotspotX, hotspotY uint32
		mask, color        uintptr
	}
	if ok, _, err := user32.NewProc("GetIconInfo").Call(icon, uintptr(unsafe.Pointer(&info))); ok == 0 {
		t.Fatal(err)
	}
	defer gdi32.NewProc("DeleteObject").Call(info.mask)
	defer gdi32.NewProc("DeleteObject").Call(info.color)
	if info.isIcon == 0 || info.color == 0 || info.mask == 0 {
		t.Fatalf("expected a color icon with a transparency mask: %+v", info)
	}
	var bitmap struct {
		kind, width, height, widthBytes int32
		planes, bitsPixel               uint16
		bits                            uintptr
	}
	if n, _, err := gdi32.NewProc("GetObjectW").Call(info.color, unsafe.Sizeof(bitmap), uintptr(unsafe.Pointer(&bitmap))); n == 0 {
		t.Fatal(err)
	}
	width, _, _ := procGetSystemMetrics.Call(49)
	height, _, _ := procGetSystemMetrics.Call(50)
	if bitmap.width != int32(width) || bitmap.height != int32(height) || bitmap.bitsPixel != 32 {
		t.Fatalf("got %dx%d, %d bpp; want %dx%d, 32 bpp", bitmap.width, bitmap.height, bitmap.bitsPixel, width, height)
	}
}
