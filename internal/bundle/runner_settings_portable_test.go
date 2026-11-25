//go:build windows || linux

package bundle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestPortableRunnerSettingsRollsBackFailedProfileSave(t *testing.T) {
	bundles, old, next, path := runnerSettingsFixture(t)
	next.Width, next.Height, next.WindowRevision = old.Width, old.Height, old.WindowRevision
	dir := filepath.Dir(path)
	identity := func() os.FileInfo {
		t.Helper()
		opened, err := os.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer opened.Close()
		info, err := opened.Stat()
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	before := identity()
	configPath := filepath.Join(dir, ConfigName)
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	original, err := decodeConfig(config)
	if err != nil || original.IconDigest == "" {
		t.Fatalf("the original configuration did not record its artwork: %+v (%v)", original, err)
	}
	brokenStore := errors.New("profile write failed")
	_, err = bundles.UpdateSettings(t.Context(), old, next, nil, func() error {
		if err := checkConfig(configPath, next); err != nil {
			t.Fatalf("callback ran before the new configuration was installed: %v", err)
		}
		return brokenStore
	})
	if !errors.Is(err, brokenStore) {
		t.Fatalf("saving the homepage returned %v", err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(got, config) {
		t.Fatalf("the failed save changed the original configuration: %q (%v)", got, err)
	}
	assertFile(t, filepath.Join(dir, revisionName), string(revision(old)))
	assertRunnerSettingsDirectory(t, bundles, old, path)

	if updated, err := bundles.UpdateSettings(t.Context(), old, next, nil, func() error { return nil }); err != nil || updated != path {
		t.Fatalf("retry returned %q, %v", updated, err)
	}
	assertRunnerSettingsDirectory(t, bundles, next, path)
	if saved, err := readConfig(configPath); err != nil || saved.iconMetadata != original.iconMetadata {
		t.Fatalf("a homepage edit changed the installed artwork metadata: %+v (%v)", saved, err)
	}
	if !os.SameFile(before, identity()) {
		t.Fatal("changing only the homepage replaced the application directory")
	}
}

func TestPortableRunnerSettingsRecoversInterruptedSave(t *testing.T) {
	for _, point := range []string{"Prepared", "ConfigReplaced", "RevisionReplaced", "ProfileCommitted"} {
		t.Run(point, func(t *testing.T) {
			bundles, old, next, path := runnerSettingsFixture(t)
			dir := filepath.Dir(path)
			// Use the on-disk name from the resize-only releases.
			backup := filepath.Join(dir, ".window-size-backup")
			if err := os.Mkdir(backup, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{ConfigName, revisionName} {
				data, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(backup, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if point != "Prepared" {
				if err := writeConfig(filepath.Join(dir, ConfigName), next, iconMetadata{IconKind: icons.WebsiteArtwork}); err != nil {
					t.Fatal(err)
				}
			}
			if point == "RevisionReplaced" || point == "ProfileCommitted" {
				if err := os.WriteFile(filepath.Join(dir, revisionName), revision(next), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			committed := old
			if point == "ProfileCommitted" {
				committed = next
			}
			// The library may render its icon before the startup repair
			// runs. The same icon still belongs to the committed profile.
			if art := bundles.ReadInstalledArtwork(committed)(); art.Image == nil || art.Kind != icons.WebsiteArtwork {
				t.Fatalf("interrupted save lost the original artwork: %+v", art)
			}
			assertRunnerSettingsDirectory(t, bundles, committed, path)
			if err := checkConfig(filepath.Join(dir, ConfigName), committed); err != nil {
				t.Fatalf("recovery installed the wrong settings: %v", err)
			}
			assertFile(t, filepath.Join(dir, revisionName), string(revision(committed)))
		})
	}
}

func TestPortableRunnerSettingsRepairsMissingResources(t *testing.T) {
	for _, missing := range []string{ConfigName, portableIconPNG} {
		t.Run(missing, func(t *testing.T) {
			bundles, old, next, path := runnerSettingsFixture(t)
			resource := filepath.Join(filepath.Dir(path), missing)
			if err := os.Remove(resource); err != nil {
				t.Fatal(err)
			}
			art := icons.Artwork{Image: icons.Brand(), Kind: icons.WebsiteArtwork}
			updated, err := bundles.UpdateSettings(t.Context(), old, next, func() icons.Artwork { return art }, func() error { return nil })
			if err != nil || updated != path {
				t.Fatalf("saving settings with a missing %s returned %q, %v", missing, updated, err)
			}
			assertRunnerSettingsDirectory(t, bundles, next, path)
			if !bundles.HasUsableArtwork(path) {
				t.Fatal("saving the settings did not repair the icon")
			}
		})
	}
}

func runnerSettingsFixture(t *testing.T) (*Library, profile.App, profile.App, string) {
	t.Helper()
	bundles, _ := releaseFixture(t)
	old := profile.App{ID: profile.NewID(), Name: "Workbench", URL: "https://example.org/workbench?from=creator#today", Width: 1200, Height: 820}
	art := icons.Artwork{Image: icons.Brand(), Kind: icons.WebsiteArtwork}
	path, err := bundles.Install(t.Context(), profile.App{}, old, art, nil)
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.URL = "https://example.org/review?from=library#overview"
	next.Width, next.Height = 960, 710
	next.WindowRevision, next.UpdatedAt = 1, time.Now().UTC()
	return bundles, old, next, path
}

func assertRunnerSettingsDirectory(t *testing.T, bundles *Library, app profile.App, path string) {
	t.Helper()
	if got, err := bundles.Path(app); err != nil || got != path {
		t.Fatalf("the committed app path is %q (%v), want %q", got, err, path)
	}
	pending, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".window-size-*"))
	if err != nil || len(pending) != 0 {
		t.Fatalf("the save left staging files %v (%v)", pending, err)
	}
}
