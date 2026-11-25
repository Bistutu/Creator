package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestLinuxInstalledWebsitePlacement(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	startTestWindowManager(t)
	service := portableInstalledLibrary(t, installed, state)
	createWith := service
	if previous := os.Getenv("WEBCRATE_PREVIOUS_RELEASE_APP"); previous != "" {
		checkPortableReleaseFile(t, previous)
		createWith = portableInstalledLibrary(t, previous, state)
	}
	address, rendered := installedWebsiteFixture(t)
	app, err := createWith.Save(t.Context(), profile.App{
		Name: "Creator saved window fixture", URL: address, Width: 680, Height: 490,
	}, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Remove(app.ID) })
	executable, err := createWith.Path(t.Context(), app, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, icon := generatedMenu(t, app, executable)
	open := func(session string) string {
		t.Helper()
		launchPortableMenu(t, entry, executable, app.Name, icon)
		checkLinuxInstalledReport(t, rendered, session)
		return linuxFixtureWindow(t, app.Name)
	}
	close := func(window string) {
		t.Helper()
		pid := linuxWindowPID(t, window)
		linuxXDo(t, "windowfocus", "--sync", window, "key", "--clearmodifiers", "ctrl+w")
		waitForLinuxTestAppClosed(t, window, pid)
	}
	window := open("new")
	initial := linuxWindowBounds(t, window)
	linuxXDo(t, "windowmove", "--sync", window, "79", "144")
	moved := linuxWindowBounds(t, window)
	if moved.x == initial.x || moved.y == initial.y {
		t.Fatalf("the window manager did not move the window: %+v → %+v", initial, moved)
	}
	close(window)

	app.Width, app.Height = 800, 570
	updated, err := service.Save(t.Context(), app, library.Icon{KeepInstalled: true})
	if err != nil || updated.WindowRevision != app.WindowRevision+1 {
		t.Fatalf("saving new dimensions: %+v, %v", updated, err)
	}
	if path, err := service.Path(t.Context(), updated, nil); err != nil || path != executable {
		t.Fatalf("saving dimensions changed the installed executable path: %q, %v", path, err)
	}
	window = open("remembered")
	resized := linuxWindowBounds(t, window)
	want := linuxWindowRect{moved.x, moved.y, updated.Width, updated.Height}
	if resized != want {
		t.Fatalf("reopening with new dimensions changed window placement or size: got %+v, want %+v", resized, want)
	}
	t.Logf("Creator's new dimensions kept the window at (%d, %d) and retained the website session", resized.x, resized.y)

	linuxXDo(t, "windowsize", "--sync", window, "720", "520")
	linuxXDo(t, "windowmove", "--sync", window, "114", "128")
	manual := linuxWindowBounds(t, window)
	if manual == resized || manual.width != 720 || manual.height != 520 {
		t.Fatalf("the window manager did not resize the window: %+v → %+v", resized, manual)
	}
	close(window)
	window = open("remembered")
	if restored := linuxWindowBounds(t, window); restored != manual {
		t.Fatalf("reopening without a settings change lost the manual frame: got %+v, want %+v", restored, manual)
	}
	close(window)
	t.Log("reopening with no settings change kept the manually moved and resized frame")
}

type linuxWindowRect struct{ x, y, width, height int }

func linuxWindowBounds(t *testing.T, window string) linuxWindowRect {
	t.Helper()
	data := linuxXDo(t, "getwindowgeometry", "--shell", window)
	var id int
	var bounds linuxWindowRect
	if _, err := fmt.Fscanf(strings.NewReader(data), "WINDOW=%d\nX=%d\nY=%d\nWIDTH=%d\nHEIGHT=%d",
		&id, &bounds.x, &bounds.y, &bounds.width, &bounds.height); err != nil {
		t.Fatalf("invalid X11 window geometry: %v (%s)", err, data)
	}
	return bounds
}
