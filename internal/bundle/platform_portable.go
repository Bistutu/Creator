//go:build windows || linux

package bundle

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/runnerconfig"
)

type portableBackend struct{ runner *runtimeFile }

const (
	portableIconPNG = runnerconfig.WindowIconFilename
	portableIconICO = "AppIcon.ico"
)

func portableIcons() []iconResource {
	if runtime.GOOS == "windows" {
		return []iconResource{{portableIconICO, ico}, {portableIconPNG, icons.Artwork.PNG}}
	}
	return []iconResource{{portableIconPNG, icons.Artwork.PNG}}
}

func NewBackend(resources, runtimeDir string) (Backend, error) {
	name := "WebCrateRunner"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	runner, err := newRuntimeFile(filepath.Join(resources, name), runtimeDir)
	if err != nil {
		return nil, err
	}
	return portableBackend{runner: runner}, nil
}

func (portableBackend) Filename(app profile.App) string {
	if runtime.GOOS == "windows" {
		return fileLabel(app.Name) + ".exe"
	}
	return fileLabel(app.Name, ConfigName, portableIconPNG)
}

func (portableBackend) checkFiles(path string, app profile.App) error {
	if _, err := requiredFile(path, true); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := checkIconFiles(dir, portableIcons()); err != nil {
		return err
	}
	return checkConfig(filepath.Join(dir, ConfigName), app)
}

func (portableBackend) installedArtwork(path string) artworkFiles {
	return readArtwork(filepath.Dir(path), portableIcons())
}

func (b portableBackend) Build(ctx context.Context, path string, app profile.App, fallback func() icons.Artwork, previous string) error {
	dir := filepath.Dir(path)
	var oldResources string
	if previous != "" {
		oldResources = filepath.Dir(previous)
	}
	icon, err := writeIcons(dir, oldResources, fallback, portableIcons())
	if err != nil {
		return err
	}
	if err := writeConfig(filepath.Join(dir, ConfigName), app, icon); err != nil {
		return err
	}
	return b.runner.install(ctx, path)
}

func (b portableBackend) refreshRuntime(ctx context.Context, path string) error {
	return b.runner.refresh(ctx, path)
}

func (portableBackend) Open(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.Command(path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
