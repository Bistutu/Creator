package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/sys/unix"
)

const darwinRunner = "WebCrateRunner"
const darwinBundlePrefix = "com.webcrate.apps."
const darwinBundleMetadataVersion = 1
const macIconName = "AppIcon.icns"

var macIcons = []iconResource{{macIconName, icns}}

type darwinBackend struct {
	runtime       []byte
	runtimeDigest []byte
	sign          func(context.Context, string) error
}

func NewBackend(resources, _ string) (Backend, error) {
	runtime, err := readRuntime(resources, darwinRunner)
	if err != nil {
		return nil, err
	}
	digest := fmt.Appendf(nil, "%x.%d", sha256.Sum256(runtime), darwinBundleMetadataVersion)
	return darwinBackend{runtime: runtime, runtimeDigest: digest, sign: signBundle}, nil
}

func (darwinBackend) Filename(app profile.App) string { return fileLabel(app.Name) + ".app" }

func (darwinBackend) contentsPath(path string) string { return filepath.Join(path, "Contents") }

func (b darwinBackend) revisionPath(path string) string {
	return filepath.Join(b.contentsPath(path), "Resources", revisionName)
}

func (b darwinBackend) swapContents(first, second string) error {
	return unix.RenamexNp(b.contentsPath(first), b.contentsPath(second), unix.RENAME_SWAP)
}

func (b darwinBackend) checkFiles(path string, app profile.App) error {
	contents := b.contentsPath(path)
	if _, err := requiredFile(filepath.Join(contents, "MacOS", darwinRunner), true); err != nil {
		return err
	}
	resources := filepath.Join(contents, "Resources")
	if _, err := requiredFile(filepath.Join(contents, "Info.plist"), false); err != nil {
		return err
	}
	if err := checkIconFiles(resources, macIcons); err != nil {
		return err
	}
	return checkConfig(filepath.Join(resources, ConfigName), app)
}

func (b darwinBackend) installedArtwork(path string) artworkFiles {
	return readArtwork(filepath.Join(b.contentsPath(path), "Resources"), macIcons)
}

func (b darwinBackend) Build(ctx context.Context, path string, app profile.App, fallback func() icons.Artwork, previous string) error {
	contents := b.contentsPath(path)
	resources := filepath.Join(contents, "Resources")
	if err := b.writeRunner(contents); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), infoPlist(app), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(b.revisionPath(path), revision(app), 0o644); err != nil {
		return err
	}
	var oldResources string
	if previous != "" {
		oldResources = filepath.Join(b.contentsPath(previous), "Resources")
	}
	icon, err := writeIcons(resources, oldResources, fallback, macIcons)
	if err != nil {
		return err
	}
	if err := writeConfig(filepath.Join(resources, ConfigName), app, icon); err != nil {
		return err
	}
	return b.sign(ctx, path)
}

func (b darwinBackend) writeRunner(contents string) error {
	bin, resources := filepath.Join(contents, "MacOS"), filepath.Join(contents, "Resources")
	for _, dir := range []string{bin, resources} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(bin, darwinRunner), b.runtime, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(resources, runtimeMarker), b.runtimeDigest, 0o644)
}

func (b darwinBackend) refreshRuntime(ctx context.Context, path string) error {
	contents := b.contentsPath(path)
	digest, err := readBundleResource(filepath.Join(contents, "Resources", runtimeMarker), 256)
	current := err == nil && bytes.Equal(digest, b.runtimeDigest)
	if current && completeMacExecutable(filepath.Join(contents, "MacOS", darwinRunner)) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(path), ".runner-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	next := filepath.Join(stage, filepath.Base(path))
	nextContents := b.contentsPath(next)
	if err := b.writeRunner(nextContents); err != nil {
		return err
	}
	// Keep the installed settings, icon and bundle identity when refreshing
	// the runner and its required metadata.
	for _, resource := range []struct {
		name  string
		limit int64
	}{
		{"Info.plist", 1 << 20},
		{filepath.Join("Resources", ConfigName), maxConfigSize},
		{filepath.Join("Resources", macIconName), icons.MaxInstalledIconBytes},
		{filepath.Join("Resources", revisionName), revisionSize},
	} {
		data, err := fileutil.ReadRegular(filepath.Join(contents, resource.name), resource.limit)
		if filepath.Base(resource.name) == revisionName && errors.Is(err, os.ErrNotExist) {
			// Old packages keep their revision beside the .app directory.
			continue
		}
		if err != nil {
			return err
		}
		destination := filepath.Join(nextContents, resource.name)
		if err := os.WriteFile(destination, data, 0o644); err != nil {
			return err
		}
		if resource.name == "Info.plist" && !current {
			if err := addMediaDescriptions(ctx, destination, data); err != nil {
				return err
			}
		}
	}
	if err := b.sign(ctx, next); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Publish the signed contents as a unit while keeping the installed
	// .app directory's identity. Launch Services can then still address the
	// running instance registered against that directory.
	return b.swapContents(next, path)
}

func signBundle(ctx context.Context, path string) error {
	output, err := exec.CommandContext(ctx, "/usr/bin/codesign", "--force", "--sign", "-", "--timestamp=none", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("macOS 无法签署应用：%w\n%s", err, output)
	}
	return nil
}
