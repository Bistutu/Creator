package runnerconfig

import (
	"bytes"
	"image/png"
	"path/filepath"

	"github.com/Bistutu/Creator/internal/fileutil"
)

const (
	WindowIconFilename = "AppIcon.png"
	maxWindowIconBytes = 1 << 20
	maxWindowIconSide  = 1024
)

// ReadWindowIcon returns the optional artwork for a portable app's window.
// Check the dimensions before handing the PNG to the window toolkit to decode.
func ReadWindowIcon(dir string) []byte {
	data, err := fileutil.ReadRegular(filepath.Join(dir, WindowIconFilename), maxWindowIconBytes)
	if err != nil {
		return nil
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width > maxWindowIconSide || config.Height > maxWindowIconSide {
		return nil
	}
	return data
}
