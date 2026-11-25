package webwindow

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/Bistutu/Creator/internal/settings"
)

// WindowGeometryStore saves the website's normal bounds. A new size in Creator
// takes precedence over manual resizing and keeps the last window's position.
type WindowGeometryStore struct {
	Directory string
	Revision  uint64
	Width     int
	Height    int
}

func (store WindowGeometryStore) Key() string {
	if store.Revision == 0 {
		return "main"
	}
	return "main-" + strconv.FormatUint(store.Revision, 10)
}

func (store WindowGeometryStore) LoadWindowStates() (map[string]mygo.WindowState, error) {
	states, err := (settings.WindowStateFile{Directory: store.Directory}).LoadWindowStates()
	if err != nil || states == nil {
		return nil, err
	}
	var bounds mygo.WindowState
	var revision uint64
	for key, state := range states {
		saved, valid := geometryRevision(key)
		if !valid || saved > store.Revision || (bounds.Width > 0 && saved <= revision) || state.Width <= 0 || state.Height <= 0 {
			continue
		}
		bounds, revision = state, saved
	}
	if bounds.Width <= 0 {
		return map[string]mygo.WindowState{}, nil
	}
	if revision != store.Revision {
		bounds.Width, bounds.Height = store.Width, store.Height
		bounds.Maximized = false
		// MyGo uses the saved pixel rectangle to find the Windows monitor.
		// The new size above is in DIPs and is scaled for that monitor.
	}
	bounds.FullScreen = false
	return map[string]mygo.WindowState{store.Key(): bounds}, nil
}

func (store WindowGeometryStore) SaveWindowStates(states map[string]mygo.WindowState) error {
	key := store.Key()
	saved := make(map[string]mygo.WindowState, 1)
	if state, ok := states[key]; ok {
		state.FullScreen = false
		saved[key] = state
	}
	return (settings.WindowStateFile{Directory: store.Directory}).SaveWindowStates(saved)
}

func geometryRevision(key string) (uint64, bool) {
	if key == "main" {
		return 0, true
	}
	suffix, ok := strings.CutPrefix(key, "main-")
	if !ok || suffix == "" || suffix[0] == '0' {
		return 0, false
	}
	revision, err := strconv.ParseUint(suffix, 10, 64)
	return revision, err == nil
}
