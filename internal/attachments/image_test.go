package attachments

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func chunk(kind string, data []byte) []byte {
	b := make([]byte, len(data)+12)
	binary.BigEndian.PutUint32(b, uint32(len(data)))
	copy(b[4:], kind)
	copy(b[8:], data)
	binary.BigEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[4:len(b)-4]))
	return b
}
func TestNormalizePNGAndJPEG(t *testing.T) {
	p := testPNG(t)
	var j bytes.Buffer
	if err := jpeg.Encode(&j, image.NewRGBA(image.Rect(0, 0, 2, 3)), nil); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{p, j.Bytes()} {
		out, err := Normalize(data)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(out))
		if err != nil || cfg.Width != 2 || cfg.Height != 3 {
			t.Fatal(cfg, err)
		}
	}
}
func TestNormalizeRejectsInvalidAndAnimated(t *testing.T) {
	p := testPNG(t)
	animated := append(append(append([]byte{}, p[:33]...), chunk("acTL", make([]byte, 8))...), p[33:]...)
	badCRC := bytes.Clone(p)
	badCRC[len(badCRC)-1] ^= 1
	for name, data := range map[string][]byte{"empty": nil, "unknown": []byte("not an image"), "truncated": p[:len(p)-12], "bad CRC": badCRC, "trailing": append(bytes.Clone(p), 1), "APNG": animated, "oversized": make([]byte, MaxBytes+1)} {
		t.Run(name, func(t *testing.T) {
			if _, err := Normalize(data); err == nil {
				t.Fatal("accepted invalid image")
			}
		})
	}
}
func TestNormalizeRejectsDimensionsBeforeDecode(t *testing.T) {
	for _, size := range [][2]uint32{{32769, 1}, {10000, 4001}, {0, 3}} {
		p := testPNG(t)
		binary.BigEndian.PutUint32(p[16:20], size[0])
		binary.BigEndian.PutUint32(p[20:24], size[1])
		binary.BigEndian.PutUint32(p[29:33], crc32.ChecksumIEEE(p[12:29]))
		if _, err := Normalize(p); err == nil {
			t.Fatal("accepted dimensions", size)
		}
	}
}
func TestValidatePNGFileAndBoundedOutput(t *testing.T) {
	p := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(p, testPNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePNGFile(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("png"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePNGFile(p); err == nil {
		t.Fatal("accepted fake capture")
	}
	w := &boundedBuffer{limit: 2}
	if _, err := w.Write([]byte("123")); err == nil || w.Len() != 0 {
		t.Fatal("output not bounded")
	}
}

// A bounded APP1 with a single IFD0 SHORT Orientation entry.
func orientedJPEG(t *testing.T, orientation uint16, bigEndian bool) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 3, 2))
	colors := []color.RGBA{{240, 10, 20, 255}, {10, 240, 20, 255}, {10, 20, 240, 255}, {200, 220, 10, 255}, {10, 220, 220, 255}, {220, 10, 220, 255}}
	for i, c := range colors {
		im.SetRGBA(i%3, i/3, c)
	}
	if err := jpeg.Encode(&b, im, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	tiff := make([]byte, 26)
	var order binary.ByteOrder = binary.LittleEndian
	copy(tiff, "II")
	if bigEndian {
		order = binary.BigEndian
		copy(tiff, "MM")
	}
	order.PutUint16(tiff[2:], 42)
	order.PutUint32(tiff[4:], 8)
	order.PutUint16(tiff[8:], 1)
	order.PutUint16(tiff[10:], 0x112)
	order.PutUint16(tiff[12:], 3)
	order.PutUint32(tiff[14:], 1)
	order.PutUint16(tiff[18:], orientation)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(len(payload)+2))
	out := append([]byte{}, b.Bytes()[:2]...)
	out = append(out, segment...)
	out = append(out, payload...)
	out = append(out, b.Bytes()[2:]...)
	return out
}
func TestNormalizePreservesJPEGDisplayOrientation(t *testing.T) {
	// Each row lists source pixel indices at destination row-major positions.
	expected := [][]int{{0, 1, 2, 3, 4, 5}, {2, 1, 0, 5, 4, 3}, {5, 4, 3, 2, 1, 0}, {3, 4, 5, 0, 1, 2}, {0, 3, 1, 4, 2, 5}, {3, 0, 4, 1, 5, 2}, {5, 2, 4, 1, 3, 0}, {2, 5, 1, 4, 0, 3}}
	for orientation := 1; orientation <= 8; orientation++ {
		for _, bigEndian := range []bool{false, true} {
			data := orientedJPEG(t, uint16(orientation), bigEndian)
			original, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			normalized, err := Normalize(data)
			if err != nil {
				t.Fatal(err)
			}
			got, err := png.Decode(bytes.NewReader(normalized))
			if err != nil {
				t.Fatal(err)
			}
			width, height := 3, 2
			if orientation >= 5 {
				width, height = 2, 3
			}
			if got.Bounds().Dx() != width || got.Bounds().Dy() != height {
				t.Fatalf("orientation %d: bounds %v", orientation, got.Bounds())
			}
			for i, source := range expected[orientation-1] {
				want := color.NRGBAModel.Convert(original.At(source%3, source/3))
				actual := color.NRGBAModel.Convert(got.At(i%width, i/width))
				if want != actual {
					t.Fatalf("orientation %d pixel %d: got %v want %v", orientation, i, actual, want)
				}
			}
		}
	}
}

func TestNormalizeIgnoresMalformedOptionalJPEGOrientation(t *testing.T) {
	for _, mutate := range []func([]byte){
		func(b []byte) { binary.LittleEndian.PutUint32(b[16:20], 0xfffffff0) },
		func(b []byte) { binary.LittleEndian.PutUint16(b[20:22], 0xffff) },
		func(b []byte) { binary.LittleEndian.PutUint16(b[24:26], 4) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[26:30], 0xffffffff) },
		func(b []byte) { binary.LittleEndian.PutUint16(b[30:32], 9) },
	} {
		data := orientedJPEG(t, 6, false)
		mutate(data)
		got, err := Normalize(data)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(got))
		if err != nil || cfg.Width != 3 || cfg.Height != 2 {
			t.Fatal(cfg, err)
		}
	}
}
