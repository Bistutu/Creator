package bundle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Bistutu/Creator/internal/profile"
)

const (
	installStagePrefix   = ".new-"
	pendingRemovalPrefix = ".remove-"
)

// ReclaimInterrupted visits abandoned builds and staged removals. The
// coordinator runs each callback while the app is idle and confirms that a
// removal was committed before allowing its backup to be deleted.
func (l *Library) ReclaimInterrupted(ctx context.Context, run func(id string, removal bool, reclaim func() error) error) error {
	var failures error
	for _, directory := range append([]string{l.root}, l.priorRoots...) {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		if err := l.reclaimDirectory(ctx, directory, run); err != nil {
			failures = errors.Join(failures, err)
		}
	}
	return failures
}

func (l *Library) reclaimDirectory(ctx context.Context, directory string, run func(string, bool, func() error) error) error {
	root, err := os.OpenRoot(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	listing, err := root.Open(".")
	if err != nil {
		return err
	}
	defer listing.Close()
	var failures error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		names, readErr := listing.Readdirnames(64)
		for _, name := range names {
			id, removal := interruptedPackage(name)
			if id == "" {
				continue
			}
			err := run(id, removal, func() error {
				if err := ctx.Err(); err != nil {
					return err
				}
				info, err := root.Lstat(name)
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				if err != nil {
					return err
				}
				if removal && isRemovalMarker(info) {
					return l.finishRemoval(id, directory)
				}
				if !removal && info.IsDir() {
					return root.RemoveAll(name)
				}
				return nil
			})
			if err != nil {
				failures = errors.Join(failures, err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return failures
		}
		if readErr != nil {
			return errors.Join(failures, readErr)
		}
	}
}

func interruptedPackage(name string) (id string, removal bool) {
	if suffix, ok := strings.CutPrefix(name, installStagePrefix); ok {
		separator := strings.LastIndexByte(suffix, '-')
		if separator > 0 && profile.ValidID(suffix[:separator]) {
			if _, err := strconv.ParseUint(suffix[separator+1:], 10, 64); err == nil {
				return suffix[:separator], false
			}
		}
	}
	if suffix, ok := strings.CutPrefix(name, pendingRemovalPrefix); ok && profile.ValidID(suffix) {
		return suffix, true
	}
	return "", false
}

// Stage a removal using the existing rollback directory and a marker beside
// it. The marker survives a successful profile commit and a failed cleanup.
func markRemoval(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !isRemovalMarker(info) {
			return fmt.Errorf("unexpected removal marker: %s", path)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return errors.Join(err, os.Remove(path))
	}
	return nil
}

func (l *Library) finishRemoval(id, directory string) error {
	if err := l.removeCopies(id, directory); err != nil {
		return err
	}
	_, backup := NewLibrary(directory, l.backend).paths(id)
	if err := os.RemoveAll(backup); err != nil {
		return err
	}
	return dropRemovalMarker(filepath.Join(directory, pendingRemovalPrefix+id))
}

func dropRemovalMarker(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !isRemovalMarker(info) {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func isRemovalMarker(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Size() == 0
}
