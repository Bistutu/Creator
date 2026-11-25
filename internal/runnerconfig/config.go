// Package runnerconfig defines the website settings shared by Creator and its launchers.
package runnerconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/profile"
)

const (
	Filename = "website.json"
	MaxSize  = 16 << 10
)

type Config struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	WindowRevision uint64 `json:"windowRevision,omitempty"`
}

// Load uses packaged settings first. An installed app with missing or invalid
// settings can use the library record for its directory's stable identity.
func Load(dir string) (profile.App, error) {
	app, err := loadPackaged(dir)
	id := filepath.Base(dir)
	if err == nil && id == app.ID {
		return app, nil
	}
	if !profile.ValidID(id) {
		return app, err
	}
	if err == nil {
		err = errors.New("website settings belong to a different installed application")
	}
	saved, savedErr := profile.SavedApp(id)
	if savedErr == nil {
		app, savedErr = profile.Normalize(saved)
		if savedErr == nil {
			return app, nil
		}
	}
	return profile.App{}, errors.Join(err, fmt.Errorf("saved website settings: %w", savedErr))
}

func loadPackaged(dir string) (profile.App, error) {
	data, err := fileutil.ReadRegular(filepath.Join(dir, Filename), MaxSize)
	if err != nil {
		return profile.App{}, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return profile.App{}, err
	}
	if !profile.ValidID(config.ID) {
		return profile.App{}, errors.New("invalid website application ID")
	}
	return profile.Normalize(profile.App{
		ID: config.ID, Name: config.Name, URL: config.URL, Width: config.Width, Height: config.Height,
		WindowRevision: config.WindowRevision,
	})
}
