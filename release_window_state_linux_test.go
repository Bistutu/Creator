package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/sys/unix"
)

func TestLinuxInstalledCreatorRecoversWindowState(t *testing.T) {
	installed, menu, _ := portableInstalledFixture(t)
	startTestWindowManager(t)
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), appName, "window-state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	open := func() (string, string) {
		t.Helper()
		launchPortableMenu(t, menu, installed, appName, "")
		window := linuxFixtureWindow(t, appName)
		return window, linuxWindowPID(t, window)
	}
	started := time.Now()
	window, pid := open()
	t.Logf("Creator opened from its installed entry in %s with an unreadable window-state file", time.Since(started).Round(time.Millisecond))
	initial := linuxWindowBounds(t, window)
	if initial.width != windowWidth || initial.height < windowHeight {
		t.Fatalf("Creator did not use its default window size: %+v", initial)
	}
	linuxXDo(t, "windowsize", "--sync", window, "420", "550")
	linuxXDo(t, "windowmove", "--sync", window, "112", "128")
	moved := linuxWindowBounds(t, window)
	closeLinuxWindowWithKey(t, window, pid, "alt+F4")
	checkLinuxStoredFrame(t, path, "portrait", moved)
	window, pid = open()
	if got := linuxWindowBounds(t, window); got != moved {
		t.Fatalf("Creator changed the recovered window bounds: %+v, want %+v", got, moved)
	}
	closeLinuxWindowWithKey(t, window, pid, "alt+F4")
}

func TestLinuxInstalledWebsiteRecoversWindowState(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	startTestWindowManager(t)
	service := portableInstalledLibrary(t, installed, state)
	address, seen := installedWebsiteFixture(t)
	app, err := service.Save(t.Context(), profile.App{
		Name: "Creator window settings recovery", URL: address, Width: 680, Height: 490,
	}, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Remove(app.ID) })
	executable, err := service.Path(t.Context(), app, nil)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WebCrate "+app.ID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	geometry, zoom := filepath.Join(directory, "window-state.json"), filepath.Join(directory, "page-zoom")
	for _, path := range []string{geometry, zoom} {
		if err := unix.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entry, icon := generatedMenu(t, app, executable)
	open := func(session string) (string, string, installedWebsiteReport) {
		t.Helper()
		launchPortableMenu(t, entry, executable, app.Name, icon)
		report := checkLinuxInstalledReport(t, seen, session)
		if report.URL != address {
			t.Fatalf("website loaded %q, want %q", report.URL, address)
		}
		window := linuxFixtureWindow(t, app.Name)
		return window, linuxWindowPID(t, window), report
	}
	started := time.Now()
	window, pid, first := open("new")
	t.Logf("The installed website rendered in %s with unreadable geometry and zoom files", time.Since(started).Round(time.Millisecond))
	initial := linuxWindowBounds(t, window)
	if initial.width != app.Width || initial.height != app.Height || first.Width != initial.width {
		t.Fatalf("the website did not use the saved size and actual page scale: bounds %+v, page %+v", initial, first)
	}
	linuxXDo(t, "windowmove", "--sync", window, "91", "130")
	moved := linuxWindowBounds(t, window)
	linuxXDo(t, "windowfocus", "--sync", window, "key", "--clearmodifiers", "ctrl+equal")
	closeLinuxWindowWithKey(t, window, pid, "ctrl+w")
	checkLinuxStoredFrame(t, geometry, "main", moved)
	if data, err := fileutil.ReadRegular(zoom, 64); err != nil || string(data) != "110\n" {
		t.Fatalf("zoom was not saved after recovery: %q (%v)", data, err)
	}
	window, pid, reopened := open("remembered")
	if got := linuxWindowBounds(t, window); got != moved || math.Abs(float64(reopened.Width)-float64(first.Width)/1.10) >= 2 {
		t.Fatalf("reopening lost the recovered frame or zoom: bounds %+v, page %+v", got, reopened)
	}
	closeLinuxWindowWithKey(t, window, pid, "ctrl+w")
}

func closeLinuxWindowWithKey(t *testing.T, window, pid, key string) {
	t.Helper()
	linuxXDo(t, "windowfocus", "--sync", window, "key", "--clearmodifiers", key)
	waitForLinuxTestAppClosed(t, window, pid)
}

func checkLinuxStoredFrame(t *testing.T, path, key string, bounds linuxWindowRect) {
	t.Helper()
	data, err := fileutil.ReadRegular(path, 4096)
	if err != nil {
		t.Fatal(err)
	}
	var states map[string]mygo.WindowState
	if err := json.Unmarshal(data, &states); err != nil {
		t.Fatal(err)
	}
	if state := states[key]; state.Width != bounds.width || state.Height != bounds.height {
		t.Fatalf("saved window state = %+v, want size %d×%d", state, bounds.width, bounds.height)
	}
}
