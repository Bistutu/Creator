package webwindow

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"github.com/Bistutu/Creator/internal/settings"
)

func TestWindowGeometryStorePreservesBoundsWithoutFullscreen(t *testing.T) {
	store := WindowGeometryStore{Directory: t.TempDir(), Revision: 2, Width: 960, Height: 680}
	if states, err := store.LoadWindowStates(); err != nil || states != nil {
		t.Fatalf("a new app should open without saved geometry: %v, %v", states, err)
	}
	file := filepath.Join(store.Directory, settings.WindowStateFilename)
	legacy := []byte(`{"main-2":{"x":40,"y":52,"width":800,"height":600,"maximized":true,"fullScreen":true}}`)
	if err := os.WriteFile(file, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	want := mygo.WindowState{X: 40, Y: 52, Width: 800, Height: 600, Maximized: true}
	states, err := store.LoadWindowStates()
	if err != nil || states["main-2"] != want {
		t.Fatalf("legacy state = %v, %v, want %+v", states, err, want)
	}
	want.PixelWidth, want.PixelHeight = 1600, 1200
	active := want
	active.FullScreen = true
	states["main-2"] = active
	if err := store.SaveWindowStates(states); err != nil || states["main-2"] != active {
		t.Fatalf("saving window geometry changed the active state: %v, %v", states, err)
	}
	if data, err := os.ReadFile(file); err != nil || bytes.Contains(data, []byte(`"fullScreen"`)) {
		t.Fatalf("the stored state should omit fullscreen: %s, %v", data, err)
	}
	if saved, err := store.LoadWindowStates(); err != nil || saved["main-2"] != want {
		t.Fatalf("saved state = %v, %v, want %+v", saved, err, want)
	}
}

func TestWindowGeometryStoreAppliesNewSizeAtLastPosition(t *testing.T) {
	store := WindowGeometryStore{Directory: t.TempDir(), Revision: 12, Width: 900, Height: 650}
	file := filepath.Join(store.Directory, settings.WindowStateFilename)
	previous := mygo.WindowState{
		X: -800, Y: 52, Width: 800, Height: 600, Maximized: true, FullScreen: true,
		PixelX: -1600, PixelY: 104, PixelWidth: 1600, PixelHeight: 1200,
	}
	legacy := map[string]mygo.WindowState{
		"main":     {X: 10, Width: 680, Height: 490},
		"main-9":   {X: 20, Width: 690, Height: 510},
		"main-11":  previous,
		"main-12":  {X: 30, Width: 800},
		"main-13":  {X: 50, Width: 900, Height: 650},
		"main-12a": {X: 60, Width: 900, Height: 650},
		"main-012": {X: 70, Width: 900, Height: 650},
		"popup":    {X: 80, Width: 880, Height: 650},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	want := previous
	want.Width, want.Height = 900, 650
	want.Maximized, want.FullScreen = false, false
	states, err := store.LoadWindowStates()
	if err != nil || !maps.Equal(states, map[string]mygo.WindowState{"main-12": want}) {
		t.Fatalf("resized geometry = %+v, %v, want %+v", states, err, want)
	}

	manual := mygo.WindowState{X: 130, Y: 150, Width: 770, Height: 530}
	legacy["main-12"] = manual
	if err := store.SaveWindowStates(legacy); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]mygo.WindowState
	if err := json.Unmarshal(data, &saved); err != nil || !maps.Equal(saved, map[string]mygo.WindowState{"main-12": manual}) {
		t.Fatalf("saving the current frame kept obsolete revisions: %s (%v)", data, err)
	}
	if loaded, err := store.LoadWindowStates(); err != nil || !maps.Equal(loaded, saved) {
		t.Fatalf("restoring the same revision lost the manual geometry: %+v, %v", loaded, err)
	}
}

func TestWindowGeometryStoreReadsUnversionedBounds(t *testing.T) {
	store := WindowGeometryStore{Directory: t.TempDir(), Width: 690, Height: 510}
	before := mygo.WindowState{X: 180, Y: 100, Width: 720, Height: 520}
	if err := store.SaveWindowStates(map[string]mygo.WindowState{"main": before}); err != nil {
		t.Fatal(err)
	}
	if states, err := store.LoadWindowStates(); err != nil || states["main"] != before {
		t.Fatalf("the original configuration lost its manual frame: %+v, %v", states, err)
	}
	store.Revision = 1
	before.Width, before.Height = 690, 510
	if states, err := store.LoadWindowStates(); err != nil || states["main-1"] != before {
		t.Fatalf("first resize lost the original placement: %+v, %v", states, err)
	}
	if err := store.SaveWindowStates(map[string]mygo.WindowState{"main-1": before}); err != nil {
		t.Fatal(err)
	}
	store.Revision = 0
	if states, err := store.LoadWindowStates(); err != nil || len(states) != 0 {
		t.Fatalf("an older configuration applied a newer frame: %+v, %v", states, err)
	}
}

func TestWindowGeometryStoreRecoversFromInvalidSettings(t *testing.T) {
	store := WindowGeometryStore{Directory: t.TempDir()}
	file := filepath.Join(store.Directory, settings.WindowStateFilename)
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if states, err := store.LoadWindowStates(); err == nil || states != nil {
		t.Fatalf("unreadable window-state file should return an error: %v, %v", states, err)
	}
	if err := store.SaveWindowStates(nil); err == nil {
		t.Fatal("replacing a directory as a file should fail")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if states, err := store.LoadWindowStates(); err == nil || states != nil {
		t.Fatalf("invalid geometry should return an error: %v, %v", states, err)
	}
	if err := store.SaveWindowStates(map[string]mygo.WindowState{}); err != nil {
		t.Fatal(err)
	}
	if states, err := store.LoadWindowStates(); err != nil || states == nil || len(states) != 0 {
		t.Fatalf("saving should recover the file: %v, %v", states, err)
	}
}
