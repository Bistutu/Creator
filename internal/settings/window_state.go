package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/Bistutu/Creator/internal/fileutil"
)

const (
	WindowStateFilename = "window-state.json"
	// Leave room for the old runner's map of window size revisions.
	maxWindowStateSize = 1 << 20
)

// WindowStateFile preserves MyGo's on-disk format with bounded reads on the UI thread.
// An empty directory leaves window state in memory.
type WindowStateFile struct {
	Directory string
}

func (file WindowStateFile) LoadWindowStates() (map[string]mygo.WindowState, error) {
	if file.Directory == "" {
		return nil, nil
	}
	data, err := fileutil.ReadRegular(filepath.Join(file.Directory, WindowStateFilename), maxWindowStateSize)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var states map[string]mygo.WindowState
	if err := json.Unmarshal(data, &states); err != nil {
		return nil, err
	}
	return states, nil
}

func (file WindowStateFile) SaveWindowStates(states map[string]mygo.WindowState) error {
	if file.Directory == "" {
		return nil
	}
	data, err := json.Marshal(states)
	if err != nil {
		return err
	}
	if len(data) > maxWindowStateSize {
		return fileutil.ErrTooLarge
	}
	return fileutil.WriteAtomic(filepath.Join(file.Directory, WindowStateFilename), data)
}
