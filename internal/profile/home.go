package profile

import (
	"os"
	"path/filepath"
)

const Filename = "apps.json"

func UserDirectory() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "WebCrate"), nil
}

// SavedApp reads the committed profile for one installed application.
func SavedApp(id string) (App, error) {
	if !ValidID(id) {
		return App{}, ErrNotFound
	}
	root, err := UserDirectory()
	if err != nil {
		return App{}, err
	}
	return NewStore(filepath.Join(root, Filename)).Find(id)
}

// SavedHome reads Creator's committed address for a running app. A standalone
// app can still use its last known address when the library is unavailable.
func SavedHome(id string) string {
	app, err := SavedApp(id)
	if err != nil {
		return ""
	}
	address, err := ParseAddress(app.URL)
	if err != nil {
		return ""
	}
	return address.String()
}
