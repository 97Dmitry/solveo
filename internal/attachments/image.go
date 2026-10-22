// Package attachments validates incoming images before they enter the queue.
package attachments

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	_ "image/jpeg" // register the supported import decoder
	"image/png"
	"io"
	"os"
)

const (
	MaxBytes     = 32 << 20
	MaxPixels    = 40_000_000
	MaxDimension = 32768
)

func sizeError() error {
	return errors.New("изображение превышает лимит 32 МиБ")
}

// ReadFile reads only a bounded regular source. Archive callers resolve its path
// through Archive.AttachmentPath first; incoming external paths are never accepted.
func ReadFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("изображение должно быть обычным файлом")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("изображение должно быть обычным файлом")
	}
	if info.Size() > MaxBytes {
		return nil, sizeError()
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if len(b) > MaxBytes {
		return nil, sizeError()
	}
	return b, err
}

// ValidatePNGFile applies import limits to a freshly captured PNG without re-encoding.
func ValidatePNGFile(path string) error {
	data, err := ReadFile(path)
	if err != nil {
		return err
	}
	_, format, err := decode(data)
	if err != nil {
		return err
	}
	if format != "png" {
		return errors.New("снимок экрана должен быть PNG")
	}
	return nil
}

// Normalize accepts PNG/JPEG by content and produces a verified, bounded PNG.
func Normalize(data []byte) ([]byte, error) {
	decoded, format, err := decode(data)
	if err != nil {
		return nil, err
	}
	if format == "png" {
		return data, nil
	}
	if orientation := jpegOrientation(data); orientation != 1 {
		decoded = orientedImage{Image: decoded, orientation: orientation}
	}
	out := &boundedBuffer{limit: MaxBytes}
	if err := png.Encode(out, decoded); err != nil {
		return nil, fmt.Errorf("не удалось подготовить PNG: %w", err)
	}
	return out.Bytes(), nil
}
func decode(data []byte) (image.Image, string, error) {
	if len(data) == 0 {
		return nil, "", errors.New("изображение пустое")
	}
	if len(data) > MaxBytes {
		return nil, "", sizeError()
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return nil, "", errors.New("нужен исправный файл PNG или JPEG")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxDimension || cfg.Height > MaxDimension || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, "", errors.New("изображение превышает 40 Мп или 32768 пикселей по стороне")
	}
	if format == "png" {
		if err := validateChunks(data); err != nil {
			return nil, "", err
		}
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", errors.New("изображение повреждено или не полностью прочитано")
	}
	return decoded, format, nil
}

// Decode does not inspect every ancillary chunk or bytes after IEND. Verify the
// entire PNG envelope as well, and reject animation instead of dropping frames.
func validateChunks(data []byte) error {
	for offset := 8; offset < len(data); {
		if len(data)-offset < 12 {
			break
		}
		n := int64(binary.BigEndian.Uint32(data[offset : offset+4]))
		if n > int64(len(data)-offset-12) {
			break
		}
		end := offset + 12 + int(n)
		kind := string(data[offset+4 : offset+8])
		if kind == "acTL" {
			return errors.New("анимированные PNG не поддерживаются")
		}
		if crc32.ChecksumIEEE(data[offset+4:end-4]) != binary.BigEndian.Uint32(data[end-4:end]) {
			break
		}
		if kind == "IEND" {
			if n == 0 && end == len(data) {
				return nil
			}
			break
		}
		offset = end
	}
	return errors.New("изображение PNG повреждено или содержит лишние данные")
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, sizeError()
	}
	return b.Buffer.Write(p)
}
