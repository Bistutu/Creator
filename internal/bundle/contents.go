package bundle

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	revisionName   = ".revision"
	revisionSize   = sha256.Size * 2
	contentsBackup = ".contents-backup"
)

// A signed package can replace its payload without replacing the directory
// addressed by Finder and Launch Services. The payload includes its revision.
type contentsBackend interface {
	contentsPath(string) string
	revisionPath(string) string
	swapContents(string, string) error
}

func (l *Library) readRevision(dir, path string) ([]byte, error) {
	if backend, ok := l.backend.(contentsBackend); ok {
		data, err := readBundleResource(backend.revisionPath(path), revisionSize)
		if !errors.Is(err, os.ErrNotExist) {
			return data, err
		}
	}
	return readBundleResource(filepath.Join(dir, revisionName), revisionSize)
}

func (l *Library) installContents(dir, stage, backup, previous, name string, save func() error) (bool, error) {
	backend, ok := l.backend.(contentsBackend)
	if !ok || previous == "" {
		return false, nil
	}
	path := filepath.Join(dir, previous)
	if exists, err := hasContents(backend, path); !exists || err != nil {
		return false, err
	}
	staged := filepath.Join(stage, name)
	if save == nil && previous == name {
		return true, backend.swapContents(path, staged)
	}
	// Publish the rollback directory before changing the installed app. Its
	// original basename and revision let recovery find the last saved package.
	if err := renameBundle(staged, filepath.Join(stage, previous)); err != nil {
		return true, err
	}
	legacy, err := readBundleResource(filepath.Join(dir, revisionName), revisionSize)
	if err == nil {
		if err := os.WriteFile(filepath.Join(stage, revisionName), legacy, 0o600); err != nil {
			return true, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return true, err
	}
	if err := os.WriteFile(filepath.Join(stage, contentsBackup), []byte(name), 0o600); err != nil {
		return true, err
	}
	if err := os.Rename(stage, backup); err != nil {
		return true, err
	}
	old := filepath.Join(backup, previous)
	if err := backend.swapContents(path, old); err != nil {
		return true, errors.Join(err, os.RemoveAll(backup))
	}
	updated := filepath.Join(dir, name)
	if err := renameBundle(path, updated); err != nil {
		return true, revertContents(backend, path, old, backup, err)
	}
	if save != nil {
		if err := save(); err != nil {
			if renameErr := renameBundle(updated, path); renameErr != nil {
				return true, errors.Join(err, renameErr)
			}
			return true, revertContents(backend, path, old, backup, err)
		}
	}
	return true, nil
}

func revertContents(backend contentsBackend, path, old, backup string, cause error) error {
	if err := backend.swapContents(path, old); err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(cause, os.RemoveAll(backup))
}

func (l *Library) recoverContents(dir, backup, committed string) (bool, error) {
	backend, ok := l.backend.(contentsBackend)
	if !ok {
		return false, nil
	}
	proposed, err := os.ReadFile(filepath.Join(backup, contentsBackup))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	name := string(proposed)
	if name == "." || name == ".." || name != filepath.Base(name) || filepath.Ext(name) != filepath.Ext(committed) {
		return true, errors.New("应用备份的名称无效")
	}
	path := filepath.Join(dir, committed)
	updated := filepath.Join(dir, name)
	if exists, err := hasContents(backend, updated); err != nil {
		return true, err
	} else if exists && path != updated {
		if err := renameBundle(updated, path); err != nil {
			return true, err
		}
	}
	if exists, err := hasContents(backend, path); !exists || err != nil {
		return false, err
	}
	return true, backend.swapContents(path, filepath.Join(backup, committed))
}

func hasContents(backend contentsBackend, path string) (bool, error) {
	for _, dir := range []string{path, backend.contentsPath(path)} {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func renameBundle(from, to string) error {
	if from == to {
		return nil
	}
	if target, err := os.Lstat(to); err == nil {
		origin, err := os.Lstat(from)
		if err != nil {
			return err
		}
		if !os.SameFile(origin, target) {
			return fmt.Errorf("已有应用文件 %q：%w", filepath.Base(to), os.ErrExist)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(from, to)
}
