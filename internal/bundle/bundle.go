package bundle

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

// Backend writes an offline application using the runtime shipped in Creator.
// A nonempty previous path identifies the committed application's artwork.
// Read the fallback only if the installed artwork cannot be used.
type Backend interface {
	Filename(profile.App) string
	Build(ctx context.Context, path string, app profile.App, fallback func() icons.Artwork, previous string) error
	Open(context.Context, string) error
}

type Library struct {
	root       string
	priorRoots []string
	backend    Backend
}

func NewLibrary(root string, backend Backend) *Library {
	return &Library{root: root, backend: backend}
}

// NewDesktopLibrary puts macOS bundles where Finder and Launch Services look
// for a user's applications. Profiles and browser sessions keep their own storage.
func NewDesktopLibrary(config string, backend Backend) (*Library, error) {
	root := filepath.Join(config, "Applications")
	if runtime.GOOS != "darwin" {
		return NewLibrary(root, backend), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	applications := filepath.Join(home, "Applications")
	return &Library{
		root: filepath.Join(applications, "Creator"), backend: backend,
		priorRoots: []string{filepath.Join(applications, "WebCrate"), root},
	}, nil
}

// Install stages the replacement and keeps the committed app until save succeeds.
// A zero committed profile means this is a new application. Calls go through
// the service, which serializes mutations for the same application.
func (l *Library) Install(ctx context.Context, committed, app profile.App, icon icons.Artwork, save func() error) (string, error) {
	return l.install(ctx, committed, app, func() icons.Artwork { return icon }, save, false)
}

// UpdateSettings keeps the icon from the committed application when available.
func (l *Library) UpdateSettings(ctx context.Context, committed, app profile.App, fallback func() icons.Artwork, save func() error) (string, error) {
	return l.install(ctx, committed, app, fallback, save, true)
}

func (l *Library) install(ctx context.Context, committed, app profile.App, fallback func() icons.Artwork, save func() error, keepIcon bool) (string, error) {
	if !profile.ValidID(app.ID) || (committed.ID != "" && committed.ID != app.ID) {
		return "", errors.New("应用 ID 无效")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	location, _, err := l.installation(committed)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(location.root, 0o700); err != nil {
		return "", err
	}
	dir, backup := location.paths(app.ID)
	hasBackup, err := l.recoverBackup(dir, backup, committed)
	if err != nil {
		return "", err
	}
	if backend, ok := l.backend.(runnerSettingsBackend); ok && keepIcon && save != nil && onlyRunnerSettingsChanged(committed, app) {
		path := filepath.Join(dir, l.backend.Filename(committed))
		if handled, err := backend.updateRunnerSettings(ctx, path, committed, app, save); err != nil {
			return "", err
		} else if handled {
			return path, nil
		}
	}
	var previousName, previous string
	if committed.ID != "" {
		previousName = l.backend.Filename(committed)
	}
	if keepIcon && committed.ID != "" {
		previous, err = l.pathWithRevision(dir, previousName, revision(committed))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	stage, err := os.MkdirTemp(location.root, installStagePrefix+app.ID+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	name := l.backend.Filename(app)
	if err := l.backend.Build(ctx, filepath.Join(stage, name), app, fallback, previous); err != nil {
		return "", err
	}
	if _, signed := l.backend.(contentsBackend); !signed {
		if err := os.WriteFile(filepath.Join(stage, revisionName), revision(app), 0o600); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if hasBackup {
		if err := os.RemoveAll(backup); err != nil {
			return "", err
		}
	}
	if handled, err := l.installContents(dir, stage, backup, previousName, name, save); err != nil {
		return "", err
	} else if !handled {
		if err := installDirectory(dir, stage, backup, save); err != nil {
			return "", err
		}
	}
	_ = os.RemoveAll(backup)
	if err := l.removeCopies(app.ID, location.root); err != nil {
		log.Printf("remove previous app bundle %s: %v", app.ID, err)
	}
	return filepath.Join(dir, name), nil
}

func installDirectory(dir, stage, backup string, save func() error) error {
	if err := moveIfPresent(dir, backup); err != nil {
		return err
	}
	if err := os.Rename(stage, dir); err != nil {
		return errors.Join(err, restoreBackup(dir, backup))
	}
	if save != nil {
		if err := save(); err != nil {
			if removeErr := os.RemoveAll(dir); removeErr != nil {
				return errors.Join(err, removeErr)
			}
			return errors.Join(err, restoreBackup(dir, backup))
		}
	}
	return nil
}

func (l *Library) Path(app profile.App) (string, error) {
	if !profile.ValidID(app.ID) {
		return "", errors.New("应用 ID 无效")
	}
	location, current, err := l.installation(app)
	if err != nil {
		return "", err
	}
	dir, backup := location.paths(app.ID)
	if current != "" {
		// The version was already checked while locating this installation.
		if _, err := os.Lstat(backup); errors.Is(err, os.ErrNotExist) {
			return l.checkPathFiles(current, app)
		} else if err != nil {
			return "", err
		}
	}
	hasBackup, err := l.recoverBackup(dir, backup, app)
	if err != nil {
		return "", err
	}
	path, err := l.checkInstalledPath(dir, app)
	if err == nil && hasBackup {
		// A busy backup must not prevent the committed app from opening.
		_ = os.RemoveAll(backup)
	}
	return path, err
}

// Keep older installs at their original path so existing desktop shortcuts
// continue to open them. A matching revision also locates a damaged install
// or an interrupted replacement in its original directory. Return the
// verified active path when it carries that revision.
func (l *Library) installation(app profile.App) (*Library, string, error) {
	if app.ID == "" || len(l.priorRoots) == 0 {
		return l, "", nil
	}
	name, stamp := l.backend.Filename(app), revision(app)
	var fallback *Library
	for _, candidate := range append([]string{l.root}, l.priorRoots...) {
		location := NewLibrary(candidate, l.backend)
		dir, backup := location.paths(app.ID)
		for _, path := range []string{dir, backup} {
			if matched, err := l.pathWithRevision(path, name, stamp); err == nil {
				if path == dir {
					return location, matched, nil
				}
				return location, "", nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, "", err
			}
			if fallback == nil {
				if _, err := os.Lstat(path); err == nil {
					fallback = location
				} else if !errors.Is(err, os.ErrNotExist) {
					return nil, "", err
				}
			}
		}
	}
	if fallback != nil {
		return fallback, "", nil
	}
	return l, "", nil
}

func (l *Library) Open(ctx context.Context, path string) error { return l.backend.Open(ctx, path) }

type artworkBackend interface{ installedArtwork(string) artworkFiles }

// ReadInstalledArtwork captures the encoded artwork of the saved revision.
// The returned decoder needs no open files and can run after the app changes.
// A committed backup may supply the artwork after an interrupted replacement.
func (l *Library) ReadInstalledArtwork(app profile.App) func() icons.Artwork {
	reader, ok := l.backend.(artworkBackend)
	if !ok || !profile.ValidID(app.ID) {
		return func() icons.Artwork { return icons.Artwork{} }
	}
	var candidates []artworkFiles
	name, stamp := l.backend.Filename(app), revision(app)
	for _, root := range append([]string{l.root}, l.priorRoots...) {
		for _, dir := range []string{app.ID, ".old-" + app.ID} {
			folder := filepath.Join(root, dir)
			path, err := l.pathWithRevision(folder, name, stamp)
			if err == nil {
				candidates = append(candidates, reader.installedArtwork(path))
			} else if backend, ok := l.backend.(runnerSettingsBackend); ok && backend.hasSavedRunnerSettings(folder, stamp) {
				// A runner settings update keeps the same icon, including if it
				// stopped between replacing the configuration and revision.
				candidates = append(candidates, reader.installedArtwork(filepath.Join(folder, name)))
			}
		}
	}
	return func() icons.Artwork {
		for _, candidate := range candidates {
			if artwork := candidate.decode(); artwork.Image != nil {
				return artwork
			}
		}
		return icons.Artwork{}
	}
}

// HasUsableArtwork checks the icon resources when the user explicitly saves
// settings. Routine launches only need to check that the files are present.
func (l *Library) HasUsableArtwork(path string) bool {
	reader, ok := l.backend.(artworkBackend)
	return !ok || reader.installedArtwork(path).validatedDigest() != ""
}

func (l *Library) RefreshRuntime(ctx context.Context, path string) error {
	if backend, ok := l.backend.(interface {
		refreshRuntime(context.Context, string) error
	}); ok {
		return backend.refreshRuntime(ctx, path)
	}
	return nil
}

func (l *Library) Remove(app profile.App, removeProfile func() error) error {
	if !profile.ValidID(app.ID) {
		return errors.New("应用 ID 无效")
	}
	location, _, err := l.installation(app)
	if err != nil {
		return err
	}
	dir, backup := location.paths(app.ID)
	hasBackup, err := l.recoverBackup(dir, backup, app)
	if err != nil {
		return err
	}
	if hasBackup {
		if err := os.RemoveAll(backup); err != nil {
			return err
		}
	}
	mark := filepath.Join(location.root, pendingRemovalPrefix+app.ID)
	moved := false
	if err := os.Rename(dir, backup); err == nil {
		moved = true
		if err := markRemoval(mark); err != nil {
			return errors.Join(err, restoreBackup(dir, backup))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := removeProfile(); err != nil {
		restoreErr := restoreBackup(dir, backup)
		if moved && restoreErr == nil {
			_ = dropRemovalMarker(mark)
		}
		return errors.Join(err, restoreErr)
	}
	// The profile commit is complete. Keep the marker if the filesystem still
	// has a package open so the next launch can finish this removal.
	if err := l.finishRemoval(app.ID, location.root); err != nil {
		log.Printf("clean removed application %s: %v", app.ID, err)
	}
	return nil
}

func (l *Library) removeCopies(id, keep string) error {
	var failures error
	for _, root := range append([]string{l.root}, l.priorRoots...) {
		if root == keep {
			continue
		}
		dir, backup := NewLibrary(root, l.backend).paths(id)
		failures = errors.Join(failures, os.RemoveAll(dir), os.RemoveAll(backup))
	}
	return failures
}

func (l *Library) paths(id string) (dir, backup string) {
	return filepath.Join(l.root, id), filepath.Join(l.root, ".old-"+id)
}

func fileLabel(name string, reserved ...string) string {
	name = strings.Trim(strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, name), ". ")
	if name == "" {
		return "Web app"
	}
	stem, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return "Web " + name
	}
	for _, file := range reserved {
		if strings.EqualFold(name, file) {
			return "Web " + name
		}
	}
	return name
}

func revision(app profile.App) []byte {
	// Rebuild on first use when the saved settings, or the bundle format,
	// differ from the installed copy.
	format := 1
	if runtime.GOOS == "windows" {
		format = 2 // Start menu entries need an ICO resource.
	}
	key := fmt.Sprintf("%d\n%s\n%s\n%d\n%d\n%s", format, app.Name, app.URL, app.Width, app.Height, app.UpdatedAt.UTC())
	if app.WindowRevision != 0 {
		key += fmt.Sprintf("\nwindow=%d", app.WindowRevision)
	}
	if app.CustomIcon != "" {
		key += "\n" + app.CustomIcon
	}
	return fmt.Appendf(nil, "%x", sha256.Sum256([]byte(key)))
}

func readRuntime(dir, name string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("读取随附的应用运行程序：%w", err)
	}
	return data, nil
}
