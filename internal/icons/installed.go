package icons

import (
	"bytes"
	"encoding/binary"
	"image"
	"path/filepath"
	"slices"

	"github.com/Bistutu/Creator/internal/fileutil"
)

const MaxInstalledIconBytes = maxIcon

// ReadInstalledIcon reads the artwork already shipped in a generated app.
// Creator writes PNG, PNG-backed ICO, and PNG-backed ICNS resources.
func ReadInstalledIcon(path string) image.Image {
	return DecodeInstalledIcon(ReadInstalledIconData(path))
}

// ReadInstalledIconData reads the encoded resource without decoding its pixels.
// A file must match its extension before it can be used in an application.
func ReadInstalledIconData(path string) []byte {
	data, err := fileutil.ReadRegular(path, MaxInstalledIconBytes)
	if err != nil {
		return nil
	}
	var signature string
	switch filepath.Ext(path) {
	case ".png":
		signature = "\x89PNG\r\n\x1a\n"
	case ".ico":
		signature = "\x00\x00\x01\x00"
	case ".icns":
		signature = "icns"
	default:
		return nil
	}
	if !bytes.HasPrefix(data, []byte(signature)) {
		return nil
	}
	return data
}

func DecodeInstalledIcon(data []byte) image.Image {
	if len(data) == 0 {
		return nil
	}
	if bytes.HasPrefix(data, []byte("icns")) {
		return decodeICNS(data)
	}
	icon, _ := decodeIcon(data, nil, 1024)
	return icon
}

func decodeICNS(data []byte) image.Image {
	const headerSize = 8
	if len(data) < headerSize || int(binary.BigEndian.Uint32(data[4:headerSize])) != len(data) {
		return nil
	}
	// The 128 px frame matches Creator's site artwork and is sufficient for
	// a library row even on high-density displays.
	priority := [...]string{"ic07", "ic08", "icp6", "icp5", "icp4", "ic09", "ic10"}
	var frames [len(priority)][]byte
	for data = data[headerSize:]; len(data) > 0; {
		if len(data) < headerSize {
			return nil
		}
		size := int(binary.BigEndian.Uint32(data[4:headerSize]))
		if size < headerSize || size > len(data) {
			return nil
		}
		if index := slices.Index(priority[:], string(data[:4])); index >= 0 {
			payload := data[headerSize:size]
			if bytes.HasPrefix(payload, []byte("\x89PNG\r\n\x1a\n")) {
				frames[index] = payload
			}
		}
		data = data[size:]
	}
	for _, frame := range frames {
		if len(frame) > 0 {
			if icon, err := decodeRaster(frame, 1024); err == nil {
				return icon
			}
		}
	}
	return nil
}
