package windows

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
)

type imageView struct {
	width, height int
	scale         float64
	panX, panY    float64
}

// Keep an open viewer attached to durable copies after its queue is sent.
func remapImagePaths(paths, sources, archived []string) []string {
	remapped := append([]string(nil), paths...)
	if len(sources) != len(archived) {
		return remapped
	}
	for i, path := range remapped {
		for j, source := range sources {
			if path == source {
				remapped[i] = archived[j]
				break
			}
		}
	}
	return remapped
}

func fitImageScale(width, height, areaW, areaH int) float64 {
	if width <= 0 || height <= 0 || areaW <= 0 || areaH <= 0 {
		return 1
	}
	return min(1, float64(areaW)/float64(width), float64(areaH)/float64(height))
}

// layout centers small images and clamps dragging to the enlarged image's edges.
func (v *imageView) layout(areaW, areaH int) (x, y, width, height int) {
	width = max(1, int(math.Round(float64(v.width)*v.scale)))
	height = max(1, int(math.Round(float64(v.height)*v.scale)))
	limitX, limitY := float64(max(0, width-areaW))/2, float64(max(0, height-areaH))/2
	v.panX = max(-limitX, min(limitX, v.panX))
	v.panY = max(-limitY, min(limitY, v.panY))
	x = int(math.Round(float64(areaW-width)/2 + v.panX))
	y = int(math.Round(float64(areaH-height)/2 + v.panY))
	return
}

// loadScreenshot returns top-down, opaque BGRA pixels for a 32-bit BI_RGB DIB.
func loadScreenshot(path string) ([]byte, int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = file.Close() }() // read-only
	config, err := png.DecodeConfig(file)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("read PNG: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 32768 || config.Height > 32768 || int64(config.Width)*int64(config.Height) > 100_000_000 {
		return nil, 0, 0, fmt.Errorf("invalid screenshot dimensions: %dx%d", config.Width, config.Height)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, 0, 0, err
	}
	decoded, err := png.Decode(file)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode PNG: %w", err)
	}
	rgba := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	draw.Draw(rgba, rgba.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), decoded, decoded.Bounds().Min, draw.Over)
	for i := 0; i < len(rgba.Pix); i += 4 {
		rgba.Pix[i], rgba.Pix[i+2] = rgba.Pix[i+2], rgba.Pix[i]
	}
	return rgba.Pix, config.Width, config.Height, nil
}
