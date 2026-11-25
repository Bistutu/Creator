package settings

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/Bistutu/Creator/internal/fileutil"
)

func TestWindowStateFilePreservesMyGoFrames(t *testing.T) {
	file := WindowStateFile{Directory: t.TempDir()}
	if states, err := file.LoadWindowStates(); err != nil || states != nil {
		t.Fatalf("new window store returned %v, %v", states, err)
	}
	legacy := `{
  "portrait": {"x": -500, "y": 110, "width": 430, "height": 595, "maximized": true,
               "fullScreen": true, "pixelX": -1000, "pixelY": 220, "pixelWidth": 860, "pixelHeight": 1190},
  "preview": {"x": 60, "width": 500, "height": 400}
}`
	path := filepath.Join(file.Directory, WindowStateFilename)
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	want := map[string]mygo.WindowState{
		"portrait": {X: -500, Y: 110, Width: 430, Height: 595, Maximized: true, FullScreen: true,
			PixelX: -1000, PixelY: 220, PixelWidth: 860, PixelHeight: 1190},
		"preview": {X: 60, Width: 500, Height: 400},
	}
	states, err := file.LoadWindowStates()
	if err != nil || !maps.Equal(states, want) {
		t.Fatalf("legacy frames = %+v, %v, want %+v", states, err, want)
	}
	states["portrait"] = mygo.WindowState{X: 80, Y: 120, Width: 448, Height: 640}
	if err := file.SaveWindowStates(states); err != nil {
		t.Fatal(err)
	}
	if got, err := file.LoadWindowStates(); err != nil || !maps.Equal(got, states) {
		t.Fatalf("saved frames = %+v, %v, want %+v", got, err, states)
	}
}

func TestWindowStateFileBoundsReadsAndSaves(t *testing.T) {
	file := WindowStateFile{Directory: t.TempDir()}
	path := filepath.Join(file.Directory, WindowStateFilename)
	data := []byte(`{"portrait":{"width":900,"height":620}}` + strings.Repeat(" ", maxWindowStateSize))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if states, err := file.LoadWindowStates(); !errors.Is(err, fileutil.ErrTooLarge) || states != nil {
		t.Fatalf("oversized state returned %+v, %v", states, err)
	}
	want := map[string]mygo.WindowState{"portrait": {Width: 448, Height: 640}}
	if err := file.SaveWindowStates(want); err != nil {
		t.Fatal(err)
	}
	large := map[string]mygo.WindowState{strings.Repeat("x", maxWindowStateSize): {Width: 900, Height: 620}}
	if err := file.SaveWindowStates(large); !errors.Is(err, fileutil.ErrTooLarge) {
		t.Fatalf("oversized save returned %v", err)
	}
	if states, err := file.LoadWindowStates(); err != nil || !maps.Equal(states, want) {
		t.Fatalf("the bounded save lost the committed state: %+v, %v", states, err)
	}
}

func TestWindowStateFileWithoutDirectoryLeavesStorageUnused(t *testing.T) {
	file := WindowStateFile{}
	if err := file.SaveWindowStates(map[string]mygo.WindowState{"portrait": {Width: 448, Height: 640}}); err != nil {
		t.Fatal(err)
	}
	if states, err := file.LoadWindowStates(); err != nil || states != nil {
		t.Fatalf("window store without a directory returned %+v, %v", states, err)
	}
}
