package webwindow

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Bistutu/Creator/internal/fileutil"
)

const (
	actualSize  = 100
	maxZoomSize = 64
)

// Use the same steps as the AppKit runner.
var zoomLevels = [...]int{25, 33, 50, 67, 75, 80, 90, 100, 110, 125, 150, 175, 200, 250, 300, 400, 500}

// PageZoom stores the main website window's scale alongside its other settings.
type PageZoom struct {
	path    string
	percent int
}

// LoadPageZoom reads the preference from an already-created, app-specific directory.
// An empty directory path keeps the preference in memory for the current launch.
func LoadPageZoom(directory string) *PageZoom {
	zoom := &PageZoom{percent: actualSize}
	if directory == "" {
		return zoom
	}
	zoom.path = filepath.Join(directory, "page-zoom")
	data, err := fileutil.ReadRegular(zoom.path, maxZoomSize)
	if err == nil {
		percent, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && slices.Contains(zoomLevels[:], percent) {
			zoom.percent = percent
		}
	}
	return zoom
}

func (zoom *PageZoom) Factor() float64 { return float64(zoom.percent) / 100 }

// A step of zero restores the actual size.
func (zoom *PageZoom) change(step int, setFactor func(float64)) error {
	next := actualSize
	if step != 0 {
		index := slices.Index(zoomLevels[:], zoom.percent)
		next = zoomLevels[min(max(0, index+step), len(zoomLevels)-1)]
	}
	if next == zoom.percent {
		return nil
	}
	zoom.percent = next
	setFactor(zoom.Factor())
	if zoom.path == "" {
		return nil
	}
	return fileutil.WriteAtomic(zoom.path, []byte(strconv.Itoa(zoom.percent)+"\n"))
}
