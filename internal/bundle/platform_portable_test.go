//go:build windows || linux

package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestCanceledPortableOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (portableBackend{}).Open(ctx, filepath.Join(t.TempDir(), "Missing")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled launch returned %v", err)
	}
}

func BenchmarkPortableSaveWindowSize(b *testing.B) {
	benchmarkPortableSaveSettings(b, false)
}

func BenchmarkPortableSaveHomeAndDimensions(b *testing.B) {
	benchmarkPortableSaveSettings(b, true)
}

func benchmarkPortableSaveSettings(b *testing.B, changeHome bool) {
	for _, mode := range []string{"SharedRunner", "CopiedRunner"} {
		b.Run(mode, func(b *testing.B) {
			bundles, service := releaseFixture(b)
			if mode == "CopiedRunner" {
				bundles.backend.(portableBackend).runner.link = func(string, string) error { return errors.ErrUnsupported }
			}
			icon := library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}}
			const home = "https://example.org/workbench?from=creator"
			app, err := service.Save(b.Context(), profile.App{Name: "Settings fixture", URL: home}, icon)
			if err != nil {
				b.Fatal(err)
			}
			icon.KeepInstalled = true
			b.ReportAllocs()
			for b.Loop() {
				app.Width = 2140 - app.Width
				if changeHome {
					if app.URL == home {
						app.URL = home + "#review"
					} else {
						app.URL = home
					}
				}
				app, err = service.Save(b.Context(), app, icon)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestPortableCreatesAndManagesStandaloneApplicationOffline(t *testing.T) {
	bundles, service := releaseFixture(t)
	t.Setenv("PATH", t.TempDir())
	art := icons.Brand()
	app, err := service.Save(t.Context(), profile.App{Name: "A & B: Desk", URL: "https://example.com/app", Width: 960, Height: 640},
		library.Icon{Artwork: icons.Artwork{Image: art}, Custom: true})
	if err != nil {
		t.Fatal(err)
	}
	path, err := bundles.Path(app)
	if err != nil {
		t.Fatal(err)
	}
	name := "A & B- Desk"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if filepath.Base(path) != name {
		t.Fatalf("installed name = %q, want %q", filepath.Base(path), name)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || (runtime.GOOS == "linux" && info.Mode().Perm()&0o111 == 0) {
		t.Fatalf("invalid standalone executable: %v", info)
	}
	dir := filepath.Dir(path)
	iconFiles := []string{"AppIcon.png"}
	if runtime.GOOS == "windows" {
		iconFiles = append(iconFiles, "AppIcon.ico")
	}
	installedIcons := make(map[string][]byte, len(iconFiles))
	for _, name := range iconFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		installedIcons[name] = data
	}
	if stamp, err := os.ReadFile(filepath.Join(dir, runtimeMarker)); err != nil || len(stamp) != 64 {
		t.Fatalf("runtime version was not recorded: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	want := RunnerConfig{ID: app.ID, Name: app.Name, URL: app.URL, Width: 960, Height: 640}
	var config RunnerConfig
	if err := json.Unmarshal(data, &config); err != nil || config != want {
		t.Fatalf("standalone config = %+v, want %+v (%v)", config, want, err)
	}
	icon, err := os.Open(filepath.Join(dir, "AppIcon.png"))
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(icon)
	_ = icon.Close()
	if err != nil || image.Bounds() != art.Bounds() || image.At(20, 64) != art.At(20, 64) {
		t.Fatalf("invalid icon: %v", err)
	}
	if runtime.GOOS == "windows" {
		if data, err := os.ReadFile(filepath.Join(dir, "AppIcon.ico")); err != nil || len(data) < 6 || data[2] != 1 {
			t.Fatalf("missing Windows icon: %v", err)
		}
	}
	second, err := service.Save(t.Context(), profile.App{Name: "Second Desk", URL: app.URL}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := bundles.Path(second)
	if err != nil {
		t.Fatal(err)
	}
	shared := bundles.backend.(portableBackend).runner.shared
	if _, err := os.Stat(shared); err == nil {
		if !shareFile(t, shared, path) || !shareFile(t, shared, secondPath) {
			t.Fatal("portable apps did not use the shared executable")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	app.Name = "Desk revised"
	edited, err := service.Save(t.Context(), app, library.Icon{KeepInstalled: true})
	if err != nil || edited.ID != app.ID {
		t.Fatalf("rename = %+v, %v", edited, err)
	}
	renamed, err := bundles.Path(edited)
	if err != nil || renamed == path || filepath.Dir(renamed) != dir {
		t.Fatalf("renamed executable = %q, %v", renamed, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old executable should be gone: %v", err)
	}
	for name, original := range installedIcons {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(data, original) {
			t.Fatalf("renaming changed %s: %v", name, err)
		}
	}
	if err := service.Remove(app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed app directory should be gone: %v", err)
	}
	if _, err := os.Stat(secondPath); err != nil {
		t.Fatalf("removing one app affected another app: %v", err)
	}
}

func TestPortableStandaloneLoadsInSystemWebView(t *testing.T) {
	isolatePortableWebView(t)
	checkStandaloneSessions(t, func(path string) string { return path })
}

func isolatePortableWebView(t *testing.T) {
	t.Helper()
	if os.Getenv("WEBCRATE_GUI_SMOKE") != "1" {
		t.Skip("requires a desktop session and system WebView")
	}
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", t.TempDir())
	} else {
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
}

func TestPortableLaunchesDuringAnotherRuntimeRepair(t *testing.T) {
	isolatePortableWebView(t)
	bundles, service := releaseFixture(t)
	website, seen := standaloneWebsite(t)
	save := func(name string) (profile.App, string) {
		t.Helper()
		app, err := service.Save(t.Context(), profile.App{Name: name, URL: website}, library.Icon{})
		if err != nil {
			t.Fatal(err)
		}
		path, err := bundles.Path(app)
		if err != nil {
			t.Fatal(err)
		}
		return app, path
	}
	healthy, healthyPath := save("Available app")
	needsRepair, repairPath := save("App needing repair")
	requireFixtureHardLinks(t, healthyPath)
	marker := filepath.Join(filepath.Dir(repairPath), runtimeMarker)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	runner := bundles.backend.(portableBackend).runner
	waiting, resume := pauseSharedRuntimeLink(t, runner)
	var tasks sync.WaitGroup
	defer func() {
		resume()
		tasks.Wait()
	}()
	repaired := make(chan error, 1)
	tasks.Go(func() {
		_, err := service.Path(t.Context(), needsRepair, nil)
		repaired <- err
	})
	select {
	case <-waiting:
	case <-time.After(3 * time.Second):
		t.Fatal("the installed app did not reach runtime repair")
	}
	type resolvedPath struct {
		path string
		err  error
	}
	ready := make(chan resolvedPath, 1)
	tasks.Go(func() {
		path, err := service.Path(t.Context(), healthy, nil)
		ready <- resolvedPath{path, err}
	})
	select {
	case got := <-ready:
		if got.err != nil || got.path != healthyPath {
			t.Fatalf("resolving the healthy app got %q: %v", got.path, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resolving the healthy app waited for runtime repair")
	}
	checkWebsite(t, healthyPath, seen, "new", func() {
		if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the paused repair had already installed its marker: %v", err)
		}
	})
	t.Log("The healthy app loaded its page in the system WebView while the other app's runtime repair was paused.")
	resume()
	if err := <-repaired; err != nil {
		t.Fatal(err)
	}
	assertFile(t, marker, runner.digest)
	if !shareFile(t, healthyPath, repairPath) {
		t.Fatal("the repaired installation did not reuse the verified runtime")
	}
}

func TestPortableRepairsMissingExecutableWithInstalledArtwork(t *testing.T) {
	bundles, service := releaseFixture(t)
	art := icons.Brand()
	saved, err := service.Save(t.Context(), profile.App{Name: "Repair fixture", URL: "https://example.org"}, library.Icon{Artwork: icons.Artwork{Image: art}})
	if err != nil {
		t.Fatal(err)
	}
	path, err := bundles.Path(saved)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeIcon := service.InstalledArtwork(saved).Image
	if beforeIcon == nil {
		t.Fatal("the fixture has no installed artwork")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := bundles.Path(saved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete installation was reported as ready: %v", err)
	}
	if icon := service.InstalledArtwork(saved).Image; icon == nil || icon.Bounds() != beforeIcon.Bounds() || icon.At(20, 64) != beforeIcon.At(20, 64) {
		t.Fatal("the installed artwork could not be read without the executable")
	}
	if repaired, err := service.Path(t.Context(), saved, nil); err != nil || repaired != path {
		t.Fatalf("repaired path = %q, %v", repaired, err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, before) {
		t.Fatalf("repair installed a different executable: %v", err)
	}
	wantPNG, err := icons.PNG(art)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(path), portableIconPNG)); err != nil || !bytes.Equal(data, wantPNG) {
		t.Fatalf("repair changed the original app icon: %v", err)
	}
	if apps, err := service.List(); err != nil || len(apps) != 1 || apps[0] != saved {
		t.Fatalf("repair changed the saved profile: %+v, %v", apps, err)
	}
}

func TestWindowsRepairsMissingIconFormatUsingInstalledArtwork(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows installs both PNG and ICO resources")
	}
	for _, missing := range []string{portableIconPNG, portableIconICO} {
		t.Run(missing, func(t *testing.T) {
			bundles, service := releaseFixture(t)
			want := color.NRGBA{R: 22, G: 148, B: 100, A: 255}
			picture := image.NewNRGBA(image.Rect(0, 0, 1, 1))
			picture.SetNRGBA(0, 0, want)
			artwork := icons.Artwork{Image: picture, Kind: icons.WebsiteArtwork}
			app, err := service.Save(t.Context(), profile.App{Name: "Notes", URL: "https://example.org"}, library.Icon{Artwork: artwork})
			if err != nil {
				t.Fatal(err)
			}
			path, err := bundles.Path(app)
			if err != nil {
				t.Fatal(err)
			}
			asset := filepath.Join(filepath.Dir(path), missing)
			if err := os.Remove(asset); err != nil {
				t.Fatal(err)
			}
			if got, err := service.Path(t.Context(), app, nil); err != nil || got != path {
				t.Fatalf("repair changed the installed path: %q, %v", got, err)
			}
			verify := func() {
				t.Helper()
				saved := icons.ReadInstalledIcon(asset)
				if saved == nil || color.NRGBAModel.Convert(saved.At(saved.Bounds().Dx()/2, saved.Bounds().Dy()/2)) != want {
					t.Fatal("repair did not use the remaining installed artwork")
				}
				if installed := service.InstalledArtwork(app); installed.Kind != icons.WebsiteArtwork {
					t.Fatalf("icon format repair lost the source: %q", installed.Kind)
				}
			}
			verify()
			if err := os.Truncate(asset, 9); err != nil {
				t.Fatal(err)
			}
			if saved, err := service.Save(t.Context(), app, library.Icon{KeepInstalled: true}); err != nil || saved != app {
				t.Fatalf("repair of damaged artwork changed the saved settings: %+v, %v", saved, err)
			}
			verify()
		})
	}
}

func TestApplicationNamesKeepPortableResourcesIntact(t *testing.T) {
	bundles, service := releaseFixture(t)
	icon := icons.Brand()
	data, err := icons.PNG(icon)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ConfigName, portableIconPNG, "APPICON.PNG"} {
		t.Run(name, func(t *testing.T) {
			app, err := service.Save(t.Context(), profile.App{Name: name, URL: "https://example.org"}, library.Icon{Artwork: icons.Artwork{Image: icon}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Remove(app.ID) })
			path, err := bundles.Path(app)
			if err != nil {
				t.Fatal(err)
			}
			want := "Web " + name
			if runtime.GOOS == "windows" {
				want = name + ".exe"
			}
			if filepath.Base(path) != want {
				t.Fatalf("executable name = %q, want %q", filepath.Base(path), want)
			}
			dir := filepath.Dir(path)
			file, err := os.ReadFile(filepath.Join(dir, ConfigName))
			var config RunnerConfig
			if err != nil || json.Unmarshal(file, &config) != nil || config.Name != name || config.URL != app.URL {
				t.Fatalf("app name damaged the configuration: %+v (%v)", config, err)
			}
			if installed, err := os.ReadFile(filepath.Join(dir, portableIconPNG)); err != nil || !bytes.Equal(installed, data) {
				t.Fatalf("app name damaged the icon: %v", err)
			}
		})
	}
}
