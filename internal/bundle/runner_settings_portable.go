//go:build windows || linux

package bundle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Bistutu/Creator/internal/profile"
)

const (
	settingsBackupName = ".window-size-backup" // Recover transactions left by earlier releases.
	nextConfigName     = ".next-config"
	nextRevisionName   = ".next-revision"
)

func (b portableBackend) updateRunnerSettings(ctx context.Context, path string, committed, app profile.App, save func() error) (bool, error) {
	if _, err := requiredFile(path, true); err != nil {
		return false, ignoreMissingSettings(err)
	}
	dir := filepath.Dir(path)
	stamp := revision(committed)
	oldConfig, parsed, err := readRunnerSettings(dir, committed, stamp)
	if err != nil {
		return false, ignoreMissingSettings(err)
	}
	files := readIconFiles(dir, portableIcons())
	files.meta = parsed.iconMetadata
	digest := files.validatedDigest()
	if digest == "" {
		return false, nil
	}
	backup := filepath.Join(dir, settingsBackupName)
	if err := os.Mkdir(backup, 0o700); err != nil {
		return true, err
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = os.RemoveAll(backup)
		}
	}()
	if err := os.WriteFile(filepath.Join(backup, ConfigName), oldConfig, 0o600); err != nil {
		return true, err
	}
	if err := os.WriteFile(filepath.Join(backup, revisionName), stamp, 0o600); err != nil {
		return true, err
	}
	parsed.IconDigest = digest
	if err := writeConfig(filepath.Join(backup, nextConfigName), app, parsed.iconMetadata); err != nil {
		return true, err
	}
	if err := os.WriteFile(filepath.Join(backup, nextRevisionName), revision(app), 0o600); err != nil {
		return true, err
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	// The old config and revision stay available until the profile commits.
	prepared = true
	if err := replaceRunnerSettings(dir, backup); err != nil {
		return true, errors.Join(err, b.recoverRunnerSettings(dir, committed))
	}
	if err := save(); err != nil {
		return true, errors.Join(err, b.recoverRunnerSettings(dir, committed))
	}
	_ = os.RemoveAll(backup)
	return true, nil
}

func (portableBackend) recoverRunnerSettings(dir string, committed profile.App) error {
	backup := filepath.Join(dir, settingsBackupName)
	info, err := os.Lstat(backup)
	if err != nil {
		return ignoreMissingSettings(err)
	}
	if !info.IsDir() {
		_ = os.Remove(backup)
		return nil
	}
	stamp := revision(committed)
	if _, _, err := readRunnerSettings(dir, committed, stamp); err == nil {
		_ = os.RemoveAll(backup)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	config, _, err := readRunnerSettings(backup, committed, stamp)
	if err != nil {
		// If neither copy matches, the normal bundle repair can rebuild
		// from the current profile while preserving any usable artwork.
		return ignoreMissingSettings(err)
	}
	stage, err := os.MkdirTemp(backup, ".restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, nextConfigName), config, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, nextRevisionName), stamp, 0o600); err != nil {
		return err
	}
	if err := replaceRunnerSettings(dir, stage); err != nil {
		return err
	}
	_ = os.RemoveAll(backup)
	return nil
}

func (portableBackend) hasSavedRunnerSettings(dir string, stamp []byte) bool {
	backup := filepath.Join(dir, settingsBackupName)
	info, err := os.Lstat(backup)
	return err == nil && info.IsDir() && settingsRevisionMatches(backup, stamp) == nil
}

func replaceRunnerSettings(dir, staged string) error {
	if err := os.Rename(filepath.Join(staged, nextConfigName), filepath.Join(dir, ConfigName)); err != nil {
		return err
	}
	return os.Rename(filepath.Join(staged, nextRevisionName), filepath.Join(dir, revisionName))
}

func readRunnerSettings(dir string, app profile.App, stamp []byte) ([]byte, installedConfig, error) {
	if err := settingsRevisionMatches(dir, stamp); err != nil {
		return nil, installedConfig{}, err
	}
	data, err := readBundleResource(filepath.Join(dir, ConfigName), maxConfigSize)
	if err != nil {
		return nil, installedConfig{}, err
	}
	parsed, err := decodeConfig(data)
	if err != nil || parsed.RunnerConfig != configForApp(app) {
		return nil, installedConfig{}, os.ErrNotExist
	}
	return data, parsed, nil
}

func settingsRevisionMatches(dir string, stamp []byte) error {
	data, err := readBundleResource(filepath.Join(dir, revisionName), revisionSize)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, stamp) {
		return os.ErrNotExist
	}
	return nil
}

func ignoreMissingSettings(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
