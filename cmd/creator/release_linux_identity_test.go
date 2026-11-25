//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/sys/unix"
)

func TestLinuxInstalledWebsiteOpensWithNonregularIcon(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	service := portableInstalledLibrary(t, installed, state)
	address, rendered := installedWebsiteFixture(t)
	app, err := service.Save(t.Context(), profile.App{
		Name: "Creator icon recovery", URL: address, Width: 680, Height: 490,
	}, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Remove(app.ID) })
	executable, err := service.Path(t.Context(), app, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, icon := generatedMenu(t, app, executable)
	open := func(state string) {
		t.Helper()
		launchPortableMenu(t, entry, executable, app.Name, icon)
		report := checkLinuxInstalledReport(t, rendered, state)
		if report.URL != address {
			t.Fatalf("website loaded %q, want %q", report.URL, address)
		}
		window := assertLinuxDesktopWindow(t, app, entry, executable)
		closeLinuxTestWindow(t, window, linuxWindowPID(t, window))
	}
	open("new")
	if err := os.Remove(icon); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(icon, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Log("reopening the installed desktop entry with its icon replaced by a pipe")
	open("remembered")
}

func TestLinuxInstalledWebsiteSettingsUnavailable(t *testing.T) {
	installed, _, _ := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	root, err := profile.UserDirectory()
	if err != nil {
		t.Fatal(err)
	}
	service := portableInstalledLibrary(t, installed, root)
	address, reports := installedWebsiteFixture(t)
	app, err := service.Save(t.Context(), profile.App{Name: "Creator missing settings", URL: address},
		library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Remove(app.ID) })
	executable, err := service.Path(t.Context(), app, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(executable), bundle.ConfigName)); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(root, profile.Filename)
	if err := os.Rename(profilePath, profilePath+".saved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(profilePath+".saved", profilePath) })
	entry, _ := generatedMenu(t, app, executable)
	process := startLinuxDesktopEntry(t, entry)
	stopTestProcess(t, process, (*os.Process).Kill)
	pid := strconv.Itoa(process)
	const title = "无法读取应用设置"
	window := strings.TrimSpace(linuxXDo(t, "search", "--sync", "--all", "--onlyvisible", "--pid", pid, "--name", "^"+title+"$"))
	assertLinuxRunner(t, pid, executable)
	findLinuxControl(t, pid, "请在 Creator 应用库中检查此应用。")
	findLinuxControl(t, pid, "关闭").click(t)
	waitForLinuxTestAppClosed(t, window, pid)
	select {
	case report := <-reports:
		t.Fatalf("the runner opened a page without its settings: %+v", report)
	default:
	}
}

func TestLinuxInstalledDesktopIdentity(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	service := portableInstalledLibrary(t, installed, state)
	address, rendered := installedWebsiteFixture(t)
	const name = "Creator web workspace"
	create := func(fragment string) profile.App {
		t.Helper()
		app, err := service.Save(t.Context(), profile.App{
			Name: name, URL: address + fragment, Width: 680, Height: 490,
		}, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = service.Remove(app.ID) })
		return app
	}
	open := func(app profile.App, session string) (string, string) {
		t.Helper()
		path, err := service.Path(t.Context(), app, nil)
		if err != nil {
			t.Fatal(err)
		}
		entry, icon := generatedMenu(t, app, path)
		launchPortableMenu(t, entry, path, app.Name, icon)
		checkLinuxInstalledReport(t, rendered, session)
		return entry, path
	}
	first, second := create("#work"), create("#personal")
	firstEntry, firstPath := open(first, "new")
	secondEntry, secondPath := open(second, "new")
	firstWindow := assertLinuxDesktopWindow(t, first, firstEntry, firstPath)
	secondWindow := assertLinuxDesktopWindow(t, second, secondEntry, secondPath)
	t.Logf("same-named apps use separate desktop entries and windows %s and %s, each with a fresh session", firstWindow, secondWindow)
	firstPID := linuxWindowPID(t, firstWindow)

	first.Name = "Creator renamed workspace"
	updated, err := service.Save(t.Context(), first, library.Icon{KeepInstalled: true})
	if err != nil {
		t.Fatal(err)
	}
	newExecutable, err := service.Path(t.Context(), updated, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	reopen := exec.CommandContext(ctx, newExecutable)
	reopen.WaitDelay = time.Second
	if out, err := reopen.CombinedOutput(); err != nil {
		t.Fatalf("the renamed launcher did not hand off to the running app: %v (%s)", err, out)
	}
	if got := linuxWindowPID(t, firstWindow); got != firstPID {
		t.Fatalf("renaming changed the running window's process: %s → %s", firstPID, got)
	}
	select {
	case report := <-rendered:
		t.Fatalf("reopening the renamed app reloaded its website: %+v", report)
	case <-time.After(250 * time.Millisecond):
	}
	t.Log("the renamed launcher reopened the existing window without loading the website again")
	linuxXDo(t, "windowraise", firstWindow)
	closeLinuxTestWindow(t, firstWindow, firstPID)

	updatedEntry, updatedPath := open(updated, "remembered")
	if updatedEntry != firstEntry || updatedPath == firstPath || updated.ID != first.ID {
		t.Fatalf("editing the name did not preserve the existing desktop identity: %s → %s", firstEntry, updatedEntry)
	}
	updatedWindow := assertLinuxDesktopWindow(t, updated, updatedEntry, updatedPath)
	if assertLinuxDesktopWindow(t, second, secondEntry, secondPath) != secondWindow {
		t.Fatal("renaming the first app changed the other app's window")
	}
	t.Log("the renamed app kept its desktop identity and session, and the other window stayed open")
	closeLinuxTestWindow(t, updatedWindow, linuxWindowPID(t, updatedWindow))
	linuxXDo(t, "windowraise", secondWindow)
	closeLinuxTestWindow(t, secondWindow, linuxWindowPID(t, secondWindow))
}

func TestLinuxInstalledWaylandIdentity(t *testing.T) {
	if os.Getenv("WEBCRATE_WAYLAND_SMOKE") != "1" {
		t.Skip("set WEBCRATE_WAYLAND_SMOKE=1 to run the installed app in the headless Cage compositor")
	}
	installed, _, state := portableInstalledFixture(t)
	service := portableInstalledLibrary(t, installed, state)
	address, rendered := installedWebsiteFixture(t)
	app, err := service.Save(t.Context(), profile.App{
		Name: "Creator Wayland workspace", URL: address, Width: 680, Height: 490,
	}, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
	if err != nil {
		t.Fatal(err)
	}
	path, err := service.Path(t.Context(), app, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := generatedMenu(t, app, path)
	class := strings.TrimSuffix(filepath.Base(entry), ".desktop")
	trace, err := os.Create(filepath.Join(state, "wayland.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = trace.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	command := exec.CommandContext(ctx, "cage", "--", path)
	command.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+t.TempDir(), "DISPLAY=", "WAYLAND_DISPLAY=",
		"WLR_BACKENDS=headless", "WLR_RENDERER=pixman", "GDK_BACKEND=wayland", "WAYLAND_DEBUG=client")
	command.Stdout, command.Stderr = trace, trace
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 3 * time.Second
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = command.Wait()
	}()
	select {
	case report := <-rendered:
		if report.State != "new" || report.Width < 500 || report.Height < 350 {
			t.Fatalf("Wayland window did not display the website correctly: %+v", report)
		}
	case <-time.After(15 * time.Second):
		log, _ := os.ReadFile(trace.Name())
		t.Fatalf("the Wayland window did not render its website:\n%s", log)
	}
	log, err := os.ReadFile(trace.Name())
	if err != nil {
		t.Fatal(err)
	}
	var published []string
	for line := range strings.SplitSeq(string(log), "\n") {
		if !strings.Contains(line, "xdg_toplevel") || !strings.Contains(line, ".set_app_id(") {
			continue
		}
		if strings.Contains(line, ".set_app_id("+strconv.Quote(class)+")") {
			t.Logf("Wayland application ID matches %s: %s", filepath.Base(entry), line)
			return
		}
		published = append(published, line)
	}
	t.Fatalf("the Wayland toplevel did not identify itself as %q: %v", class, published)
}

func assertLinuxDesktopWindow(t *testing.T, app profile.App, entry, executable string) string {
	t.Helper()
	class := strings.TrimSuffix(filepath.Base(entry), ".desktop")
	search := func(name string) ([]byte, error) {
		return exec.CommandContext(t.Context(), "xdotool", "search", "--onlyvisible", "--classname", "^"+regexp.QuoteMeta(name)+"$").CombinedOutput()
	}
	output, err := search(class)
	windows := strings.Fields(string(output))
	if err != nil || len(windows) != 1 {
		old, _ := search(app.Name)
		t.Fatalf("want one visible window with desktop identity %q, got %v (%v); windows with display name as class: %v",
			class, windows, err, strings.Fields(string(old)))
	}
	data, err := os.ReadFile(entry)
	if err != nil || !strings.Contains(string(data), "\nStartupWMClass="+class+"\n") {
		t.Fatalf("desktop entry %s does not identify its window as %q: %v", entry, class, err)
	}
	window := windows[0]
	assertLinuxRunner(t, linuxWindowPID(t, window), executable)
	return window
}
