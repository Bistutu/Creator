package icons

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"sort"

	_ "golang.org/x/image/webp"
)

var (
	errImage           = errors.New("网站未提供可用图标")
	errImageDimensions = errors.New("图片宽度和高度须在 4096 像素以内")
)

func decodeIcon(data []byte, svg func([]byte) (image.Image, error), maxSide int) (image.Image, error) {
	if bytes.HasPrefix(data, []byte{0, 0, 1, 0}) {
		return decodeICO(data)
	}
	if svg != nil && len(data) <= 96<<10 {
		header := bytes.TrimSpace(data[:min(len(data), 512)])
		if bytes.HasPrefix(header, []byte("<svg")) || (bytes.HasPrefix(header, []byte("<?xml")) && bytes.Contains(header, []byte("<svg"))) {
			return svg(data)
		}
	}
	return decodeRaster(data, maxSide)
}

func decodeRaster(data []byte, maxSide int) (image.Image, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 {
		return nil, errImage
	}
	if config.Width > maxSide || config.Height > maxSide {
		return nil, errImageDimensions
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func decodeICO(data []byte) (image.Image, error) {
	if len(data) < 6 || !bytes.Equal(data[:4], []byte{0, 0, 1, 0}) {
		return nil, errImage
	}
	le := binary.LittleEndian
	count := int(le.Uint16(data[4:6]))
	if count < 1 || count > 64 || len(data) < 6+16*count {
		return nil, errImage
	}
	type frame struct {
		width, height int
		data          []byte
	}
	frames := make([]frame, 0, count)
	for i := range count {
		entry := data[6+i*16:]
		w, h := int(entry[0]), int(entry[1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		size, offset := uint64(le.Uint32(entry[8:])), uint64(le.Uint32(entry[12:]))
		if offset >= uint64(6+16*count) && offset+size <= uint64(len(data)) && size != 0 {
			frames = append(frames, frame{w, h, data[offset : offset+size]})
		}
	}
	sort.SliceStable(frames, func(i, j int) bool {
		return frames[i].width*frames[i].height > frames[j].width*frames[j].height
	})
	for _, f := range frames {
		if bytes.HasPrefix(f.data, []byte("\x89PNG\r\n\x1a\n")) {
			if img, err := decodeRaster(f.data, 2048); err == nil {
				return img, nil
			}
		} else if img := decodeDIB(f.data, f.width, f.height); img != nil {
			return img, nil
		}
	}
	return nil, errImage
}

func decodeDIB(data []byte, width, height int) *image.NRGBA {
	le := binary.LittleEndian
	if len(data) < 40 || le.Uint32(data) != 40 || le.Uint32(data[4:]) != uint32(width) ||
		le.Uint32(data[8:]) != uint32(height*2) || le.Uint16(data[12:]) != 1 || le.Uint32(data[16:]) != 0 {
		return nil
	}
	depth := int(le.Uint16(data[14:]))
	var palette []byte
	if depth == 1 || depth == 4 || depth == 8 {
		colors := le.Uint32(data[32:])
		if colors == 0 {
			colors = 1 << depth
		}
		if colors > 1<<depth || len(data) < 40+int(colors)*4 {
			return nil
		}
		palette = data[40 : 40+int(colors)*4]
	} else if depth != 24 && depth != 32 {
		return nil
	}
	colorStride := (width*depth + 31) / 32 * 4
	maskStride := (width + 31) / 32 * 4
	pixels := data[40+len(palette):]
	if len(pixels) < height*colorStride {
		return nil
	}
	mask := pixels[height*colorStride:]
	hasMask := len(mask) >= maskStride*height
	var usesAlpha bool
	if depth == 32 {
		for i := 3; i < height*colorStride; i += 4 {
			if pixels[i] != 0 {
				usesAlpha = true
				break
			}
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		row := height - 1 - y
		for x := range width {
			var rgb []byte
			alpha := uint8(255)
			if depth >= 24 {
				offset := row*colorStride + x*(depth/8)
				rgb = pixels[offset:]
				if usesAlpha {
					alpha = rgb[3]
				}
			} else {
				index := int(pixels[row*colorStride+x*depth/8]>>(8-depth-(x*depth)%8)) & (1<<depth - 1)
				if index*4 >= len(palette) {
					return nil
				}
				rgb = palette[index*4:]
			}
			if !usesAlpha && hasMask && mask[row*maskStride+x/8]&(0x80>>uint(x%8)) != 0 {
				alpha = 0
			}
			img.SetNRGBA(x, y, color.NRGBA{R: rgb[2], G: rgb[1], B: rgb[0], A: alpha})
		}
	}
	return img
}
