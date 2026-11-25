package icons

import (
	"bytes"
	"encoding/binary"
	"image"
)

const exifHeader = "Exif\x00\x00"

func iconExif(data []byte) []byte {
	switch {
	case bytes.HasPrefix(data, []byte("\xff\xd8")):
		return jpegExif(data[2:])
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return pngExif(data[8:])
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return webpExif(data[4:])
	}
	return nil
}

func jpegExif(data []byte) []byte {
	for len(data) >= 2 && data[0] == 0xff {
		marker := data[1]
		if marker == 0xff { // Fill byte before a marker.
			data = data[1:]
			continue
		}
		data = data[2:]
		if marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) { // Standalone marker.
			continue
		}
		if marker == 0xda || marker == 0xd9 || len(data) < 2 { // Image scan or end of image.
			break
		}
		size := int(binary.BigEndian.Uint16(data))
		if size < 2 || size > len(data) {
			break
		}
		if marker == 0xe1 && bytes.HasPrefix(data[2:size], []byte(exifHeader)) {
			return data[2+len(exifHeader) : size]
		}
		data = data[size:]
	}
	return nil
}

func pngExif(data []byte) []byte {
	for len(data) >= 12 {
		size := uint64(binary.BigEndian.Uint32(data))
		if size > uint64(len(data)-12) {
			break
		}
		switch string(data[4:8]) {
		case "eXIf":
			// The PNG decoder already verified this chunk's checksum.
			return data[8 : 8+size]
		case "IEND":
			return nil
		}
		data = data[12+size:]
	}
	return nil
}

func webpExif(data []byte) []byte {
	size := uint64(binary.LittleEndian.Uint32(data))
	if size < 4 || size > uint64(len(data)-4) {
		return nil
	}
	data = data[8 : 4+size]
	for len(data) >= 8 {
		size := uint64(binary.LittleEndian.Uint32(data[4:]))
		padded := size + size%2
		if padded > uint64(len(data)-8) {
			break
		}
		if string(data[:4]) == "EXIF" {
			return bytes.TrimPrefix(data[8:8+size], []byte(exifHeader))
		}
		data = data[8+padded:]
	}
	return nil
}

func tiffOrientation(data []byte) uint16 {
	if len(data) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	// Read only IFD0. A thumbnail's directory may report a different orientation.
	offset := uint64(order.Uint32(data[4:]))
	if order.Uint16(data[2:]) != 42 || offset < 8 || offset > uint64(len(data)-2) {
		return 0
	}
	count := int(order.Uint16(data[offset:]))
	entries := data[offset+2:]
	if len(entries) < 4 || count > (len(entries)-4)/12 {
		return 0
	}
	for range count {
		if order.Uint16(entries) == 0x0112 && order.Uint16(entries[2:]) == 3 && order.Uint32(entries[4:]) == 1 {
			orientation := order.Uint16(entries[8:])
			if orientation >= 1 && orientation <= 8 {
				return orientation
			}
		}
		entries = entries[12:]
	}
	return 0
}

// Apply the transform after scaling, so rotating a large photo only needs
// another icon-sized buffer. The steps map output pixels back to source pixels.
func orientSquare(img *image.NRGBA, orientation uint16) *image.NRGBA {
	if orientation <= 1 {
		return img
	}
	size := img.Bounds().Dx()
	last := size - 1
	origin, right, down := image.Pt(0, 0), image.Pt(1, 0), image.Pt(0, 1)
	switch orientation {
	case 2: // Mirror left to right.
		origin.X, right.X = last, -1
	case 3: // Rotate 180 degrees.
		origin, right.X, down.Y = image.Pt(last, last), -1, -1
	case 4: // Mirror top to bottom.
		origin.Y, down.Y = last, -1
	case 5: // Transpose across the top-left diagonal.
		right, down = image.Pt(0, 1), image.Pt(1, 0)
	case 6: // Rotate clockwise.
		origin.Y, right, down = last, image.Pt(0, -1), image.Pt(1, 0)
	case 7: // Transpose across the top-right diagonal.
		origin, right, down = image.Pt(last, last), image.Pt(0, -1), image.Pt(-1, 0)
	case 8: // Rotate counterclockwise.
		origin.X, right, down = last, image.Pt(0, 1), image.Pt(-1, 0)
	}
	result := image.NewNRGBA(img.Bounds())
	for y := range size {
		source := origin
		for x := range size {
			result.SetNRGBA(x, y, img.NRGBAAt(source.X, source.Y))
			source = source.Add(right)
		}
		origin = origin.Add(down)
	}
	return result
}
