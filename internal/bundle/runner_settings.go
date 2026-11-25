package bundle

import (
	"context"

	"github.com/Bistutu/Creator/internal/profile"
)

// Portable backends can update the homepage and dimensions in place. Signed
// macOS apps continue through the package installer.
type runnerSettingsBackend interface {
	updateRunnerSettings(ctx context.Context, path string, committed, app profile.App, save func() error) (bool, error)
	recoverRunnerSettings(dir string, committed profile.App) error
	hasSavedRunnerSettings(dir string, stamp []byte) bool
}

func onlyRunnerSettingsChanged(committed, app profile.App) bool {
	if committed.ID == "" || (committed.URL == app.URL && committed.Width == app.Width && committed.Height == app.Height) {
		return false
	}
	committed.URL = app.URL
	committed.Width, committed.Height = app.Width, app.Height
	committed.WindowRevision, committed.UpdatedAt = app.WindowRevision, app.UpdatedAt
	return committed == app
}
