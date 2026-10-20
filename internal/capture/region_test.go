package capture

import (
	"image"
	"testing"
)

func TestValidateRegion(t *testing.T) {
	screen := image.Rect(0, 0, 1920, 1080)
	for _, tt := range []struct {
		name   string
		region image.Rectangle
		valid  bool
	}{
		{"full screen", screen, true},
		{"selection", image.Rect(100, 200, 301, 406), true},
		{"one pixel", image.Rect(1919, 1079, 1920, 1080), true},
		{"empty", image.Rect(1, 1, 1, 5), false},
		{"negative", image.Rect(-1, 10, 50, 50), false},
		{"past width", image.Rect(100, 200, 1921, 500), false},
		{"past height", image.Rect(100, 200, 500, 1081), false},
		{"inverted", image.Rectangle{Min: image.Pt(10, 10), Max: image.Pt(5, 5)}, false},
		{"oversized", image.Rect(0, 0, 32000, 32000), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bounds := screen
			if tt.name == "oversized" {
				bounds = tt.region
			}
			got := ValidateRegion(tt.region, bounds)
			if (got == nil) != tt.valid {
				t.Fatalf("ValidateRegion(%v, %v) = %v, valid=%v", tt.region, bounds, got, tt.valid)
			}
		})
	}
}
