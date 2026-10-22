package attachments

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
)

// Read only the bounded JPEG header and TIFF IFD0 Orientation SHORT. Ignore
// absent/malformed optional metadata; image.Decode validates the JPEG itself.
func jpegOrientation(data []byte) uint16 {
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	for offset := 2; offset < len(data); {
		if data[offset] != 0xff {
			break
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			break
		}
		marker := data[offset]
		offset++
		if marker == 0xda || marker == 0xd9 {
			break
		} // stop before entropy-coded image data
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd8 {
			continue
		}
		if len(data)-offset < 2 {
			break
		}
		size := int(binary.BigEndian.Uint16(data[offset:]))
		if size < 2 || size > len(data)-offset {
			break
		}
		payload := data[offset+2 : offset+size]
		if marker == 0xe1 && bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
			return tiffOrientation(payload[6:])
		}
		offset += size
	}
	return 1
}
func tiffOrientation(data []byte) uint16 {
	if len(data) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(data[2:4]) != 42 {
		return 1
	}
	offset := uint64(order.Uint32(data[4:8]))
	if offset < 8 || offset+2 > uint64(len(data)) {
		return 1
	}
	count := uint64(order.Uint16(data[offset : offset+2]))
	offset += 2
	if count > (uint64(len(data))-offset)/12 {
		return 1
	}
	for i := uint64(0); i < count; i++ {
		entry := data[offset+i*12 : offset+(i+1)*12]
		if order.Uint16(entry[:2]) == 0x112 && order.Uint16(entry[2:4]) == 3 && order.Uint32(entry[4:8]) == 1 {
			value := order.Uint16(entry[8:10])
			if value >= 1 && value <= 8 {
				return value
			}
			return 1
		}
	}
	return 1
}

// Apply orientation as a read-only view. PNG encoding already visits every
// pixel; a view avoids another full-size bitmap allocation for large JPEGs.
type orientedImage struct {
	image.Image
	orientation uint16
}

func (im orientedImage) Bounds() image.Rectangle {
	b := im.Image.Bounds()
	if im.orientation >= 5 {
		return image.Rect(0, 0, b.Dy(), b.Dx())
	}
	return image.Rect(0, 0, b.Dx(), b.Dy())
}
func (im orientedImage) At(x, y int) color.Color {
	b := im.Image.Bounds()
	w, h := b.Dx(), b.Dy()
	switch im.orientation {
	case 2:
		x = w - 1 - x
	case 3:
		x, y = w-1-x, h-1-y
	case 4:
		y = h - 1 - y
	case 5:
		x, y = y, x
	case 6:
		x, y = y, h-1-x
	case 7:
		x, y = w-1-y, h-1-x
	case 8:
		x, y = w-1-y, x
	}
	return im.Image.At(b.Min.X+x, b.Min.Y+y)
}
