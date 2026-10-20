package capture

import (
	"fmt"
	"image"
)

// ValidateRegion rejects empty, off-screen, or oversized capture rectangles.
// Coordinates are physical pixels relative to the primary display.
func ValidateRegion(region, bounds image.Rectangle) error {
	if region.Empty() {
		return fmt.Errorf("empty screen region")
	}
	if !region.In(bounds) {
		return fmt.Errorf("screen region %v is outside primary display %v", region, bounds)
	}
	w, h := region.Dx(), region.Dy()
	if w > 32768 || h > 32768 || int64(w)*int64(h) > 100_000_000 {
		return fmt.Errorf("screen region too large: %dx%d", w, h)
	}
	return nil
}
