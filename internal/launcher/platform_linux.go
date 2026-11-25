package launcher

import (
	"os"
	"path/filepath"

	"github.com/Bistutu/Creator/internal/library"
)

func ForUser() (library.Launcher, error) {
	data := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(data) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		data = filepath.Join(home, ".local", "share")
	}
	return desktopEntries{directory: filepath.Join(data, "applications")}, nil
}
