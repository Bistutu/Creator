package bundle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/profile"
)

// An interrupted save can leave the replacement at dir while the saved
// profile still describes the backup. Restore the matching bundle before
// the next operation. The bool reports a backup left beside the active path.
func (l *Library) recoverBackup(dir, backup string, committed profile.App) (bool, error) {
	if backend, ok := l.backend.(runnerSettingsBackend); ok {
		if err := backend.recoverRunnerSettings(dir, committed); err != nil {
			return false, err
		}
	}
	if _, err := os.Lstat(backup); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(backup, dir); err != nil {
			return false, err
		}
		if committed.ID != "" {
			_ = dropRemovalMarker(filepath.Join(filepath.Dir(dir), pendingRemovalPrefix+committed.ID))
		}
		return false, nil
	} else if err != nil {
		return false, err
	}
	if committed.ID == "" {
		return true, nil
	}
	if _, err := l.checkInstalledPath(dir, committed); !errors.Is(err, os.ErrNotExist) {
		return true, err
	}
	if _, err := l.checkInstalledPath(backup, committed); errors.Is(err, os.ErrNotExist) {
		// Keep both copies until a replacement is ready.
		return true, nil
	} else if err != nil {
		return true, err
	}
	if handled, err := l.recoverContents(dir, backup, l.backend.Filename(committed)); handled || err != nil {
		return true, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return true, err
	}
	return false, os.Rename(backup, dir)
}

// Match the committed settings even when an executable or another resource
// has gone missing. Repair can still use the remaining artwork.
func (l *Library) pathWithRevision(dir, name string, stamp []byte) (string, error) {
	path := filepath.Join(dir, name)
	data, err := l.readRevision(dir, path)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(data, stamp) {
		return "", os.ErrNotExist
	}
	return path, nil
}

func (l *Library) checkInstalledPath(dir string, app profile.App) (string, error) {
	path, err := l.pathWithRevision(dir, l.backend.Filename(app), revision(app))
	if err != nil {
		return "", err
	}
	return l.checkPathFiles(path, app)
}

func (l *Library) checkPathFiles(path string, app profile.App) (string, error) {
	var err error
	if backend, ok := l.backend.(interface {
		checkFiles(string, profile.App) error
	}); ok {
		err = backend.checkFiles(path, app)
	} else {
		_, err = os.Stat(path)
	}
	if err != nil {
		return "", err
	}
	return path, nil
}

func requiredFile(path string, executable bool) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 ||
		(executable && runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return nil, os.ErrNotExist
	}
	return info, nil
}

func readBundleResource(path string, limit int64) ([]byte, error) {
	data, err := fileutil.ReadRegular(path, limit)
	if errors.Is(err, fileutil.ErrNotRegular) || errors.Is(err, fileutil.ErrTooLarge) {
		return nil, os.ErrNotExist
	}
	return data, err
}

func moveIfPresent(source, destination string) error {
	err := os.Rename(source, destination)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func restoreBackup(dir, backup string) error {
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return moveIfPresent(backup, dir)
}
