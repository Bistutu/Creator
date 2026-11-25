//go:build linux || windows

package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/launcher"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/egoist/mygo/ui"
)

func TestPortableInstalledCreatorWorkflow(t *testing.T) {
	installed, managerEntry, _ := portableInstalledFixture(t)
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(config, "WebCrate")
	newBundles := func(creator string) *bundle.Library {
		t.Helper()
		backend, err := bundle.NewBackend(filepath.Dir(creator), filepath.Join(root, "Runtimes"))
		if err != nil {
			t.Fatalf("Creator did not install its offline runner: %v", err)
		}
		bundles, err := bundle.NewDesktopLibrary(root, backend)
		if err != nil {
			t.Fatal(err)
		}
		return bundles
	}
	currentBundles := newBundles(installed)
	bundles := currentBundles
	if previous := os.Getenv("WEBCRATE_PREVIOUS_RELEASE_APP"); previous != "" {
		checkPortableReleaseFile(t, previous)
		bundles = newBundles(previous)
	}
	closeManager := launchPortableMenu(t, managerEntry, installed, appName, "")
	closeManager()
	t.Log("Creator opened from its installed system menu entry")
	entries, err := launcher.ForUser()
	if err != nil {
		t.Fatal(err)
	}
	fixture, seen := installedWebsiteFixture(t)
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	custom := icons.NewCustomStore(filepath.Join(root, "CustomIcons"))
	options := library.Options{Launcher: entries, CustomIcons: custom}
	service := library.New(store, bundles, options)
	icon := icons.Brand()
	wantPNG, err := icons.PNG(icon)
	if err != nil {
		t.Fatal(err)
	}
	d := newDesktopApp(service, &testingIcons{}, custom, nil)
	d.chooseIcon = func() (image.Image, error) { return icon, nil }
	d.work = func(f func()) { f() }
	d.loadLibrary()
	tt := ui.NewTesterWith(d.view, windowWidth, windowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	fill := func(label, value string) {
		t.Helper()
		click(t, tt, label)
		typeReplace(tt, value)
	}
	resize := func(width, height string) {
		t.Helper()
		click(t, tt, "窗口尺寸")
		fill("窗口宽度（像素）", width)
		fill("窗口高度（像素）", height)
	}
	name, renamed := portableFixtureNames()
	fill("网址或应用名称", fixture)
	tt.Key(0, ui.KeyEnter)
	fill("应用名称", name)
	click(t, tt, "更换图标")
	resize("680", "490")
	click(t, tt, "创建应用")
	if len(d.apps) != 1 {
		t.Fatalf("application creation did not appear in the library: %v", tt.Texts())
	}
	app := d.apps[0]
	t.Cleanup(func() { _ = service.Remove(app.ID) })
	running := func(saved profile.App, wantState string) (installedWebsiteReport, string, string, func()) {
		t.Helper()
		executable, err := bundles.Path(saved)
		if err != nil {
			t.Fatal(err)
		}
		iconPNG := filepath.Join(filepath.Dir(executable), "AppIcon.png")
		if data, err := os.ReadFile(iconPNG); err != nil || !bytes.Equal(data, wantPNG) {
			t.Fatalf("installed icon at %s does not match the selected image: %v", iconPNG, err)
		}
		entry, menuIcon := generatedMenu(t, saved, executable)
		stop := launchPortableMenu(t, entry, executable, saved.Name, menuIcon)
		select {
		case report := <-seen:
			if report.URL != saved.URL || report.State != wantState || report.Width < 500 || report.Height < 350 {
				t.Fatalf("the website reported %+v, want %s with a %s session and its own visible window", report, saved.URL, wantState)
			}
			if wantState == "new" {
				if runtime.GOOS == "linux" && bundles != currentBundles {
					verifyPortableRuntimeUpgrade(t, installed, managerEntry, entry, executable)
					t.Log("the installed Creator replaced the runner while its previous process remained open")
				}
				ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
				defer cancel()
				repeat := exec.CommandContext(ctx, executable)
				repeat.WaitDelay = time.Second
				if out, err := repeat.CombinedOutput(); err != nil {
					t.Fatalf("opening the installed app again did not reuse its window: %v (%s)", err, out)
				}
				select {
				case duplicate := <-seen:
					t.Fatalf("opening the installed app again reloaded the website: %+v", duplicate)
				default:
				}
			}
			t.Logf("%s opened from its system menu at %d×%d with %s session", saved.Name, report.Width, report.Height, report.State)
			return report, entry, executable, stop
		case <-time.After(20 * time.Second):
			t.Fatalf("%s did not load the saved URL %s", saved.Name, saved.URL)
			return installedWebsiteReport{}, "", "", nil
		}
	}
	open := func(saved profile.App, wantState string) (installedWebsiteReport, string, string) {
		t.Helper()
		report, entry, executable, stop := running(saved, wantState)
		stop()
		return report, entry, executable
	}
	initial, oldEntry, oldExecutable := open(app, "new")
	if bundles != currentBundles {
		if runtime.GOOS == "windows" {
			verifyPortableRuntimeUpgrade(t, installed, managerEntry, oldEntry, oldExecutable)
		}
		if saved, err := store.Find(app.ID); err != nil || saved != app {
			t.Fatalf("the installed Creator changed the saved profile: %+v, %v", saved, err)
		}
		bundles = currentBundles
		service = library.New(store, bundles, options)
		d.library = service
		t.Log("the installed Creator refreshed the previously generated runner and restored the original system menu entry")
	}
	reopened, runningEntry, runningExecutable, stopRunning := running(app, "remembered")
	if reopened.Width != initial.Width || reopened.Height != initial.Height {
		t.Fatalf("reopening changed the website viewport: %+v → %+v", initial, reopened)
	}
	if err := custom.Remove(app.CustomIcon); err != nil {
		t.Fatal(err)
	}
	d = newDesktopApp(service, &testingIcons{}, custom, nil)
	d.work = func(f func()) { f() }
	d.loadLibrary()
	d.page = 1
	tt = ui.NewTesterWith(d.view, windowWidth, windowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	tt.Frame()
	center := icon.Bounds().Size().Div(2).Add(icon.Bounds().Min)
	wantColor := color.NRGBAModel.Convert(icon.At(center.X, center.Y)).(color.NRGBA)
	assertArtworkColor(t, d.appArt.get(app.ID), wantColor)
	paths := []string{filepath.Dir(runningExecutable), runningExecutable,
		filepath.Join(filepath.Dir(runningExecutable), "AppIcon.png"), runningEntry}
	fileIdentity := func(path string) os.FileInfo {
		t.Helper()
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	originalFiles := make([]os.FileInfo, len(paths))
	for i, path := range paths {
		originalFiles[i] = fileIdentity(path)
	}
	click(t, tt, name+" 的选项")
	click(t, tt, "编辑应用")
	updatedHome := strings.Replace(fixture, "/workbench?", "/review?", 1) + "#overview"
	fill("应用网址", updatedHome)
	resize("720", "530")
	click(t, tt, "保存")
	if len(d.apps) != 1 || !tt.HasText("首页已保存，新尺寸下次启动时生效") {
		t.Fatalf("the new settings were not confirmed: %v", tt.Texts())
	}
	edited := d.apps[0]
	if edited.ID != app.ID || edited.URL != updatedHome || edited.Width != 720 || edited.Height != 530 || edited.WindowRevision != app.WindowRevision+1 {
		t.Fatalf("saving the homepage and dimensions while the website was running left %+v: %v", edited, tt.Texts())
	}
	for i, path := range paths {
		if !os.SameFile(originalFiles[i], fileIdentity(path)) {
			t.Fatalf("saving the homepage and dimensions replaced %s", filepath.Base(path))
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	repeat := exec.CommandContext(ctx, runningExecutable)
	repeat.WaitDelay = time.Second
	if out, err := repeat.CombinedOutput(); err != nil {
		t.Fatalf("reopening while the app was running: %v (%s)", err, out)
	}
	select {
	case duplicate := <-seen:
		t.Fatalf("saving the settings reloaded the running website: %+v", duplicate)
	default:
	}
	stopRunning()
	afterResize, _, _ := open(edited, "remembered")
	if afterResize.Width <= reopened.Width+10 || afterResize.Height <= reopened.Height+10 {
		t.Fatalf("relaunch did not apply the new dimensions: %+v → %+v", reopened, afterResize)
	}
	t.Log("saving the homepage and dimensions preserved the live window, installed directory, runner, icon and menu entry; on relaunch both settings and the session were available")

	click(t, tt, name+" 的选项")
	click(t, tt, "编辑应用")
	assertArtworkColor(t, d.draft.icon, wantColor)
	fill("应用名称", renamed)
	resize("780", "560")
	click(t, tt, "保存")
	if len(d.apps) != 1 || d.apps[0].ID != app.ID || !tt.HasText("重新打开应用后使用新尺寸") {
		t.Fatalf("editing did not keep the installed app: %v", tt.Texts())
	}
	updated, entry, executable := open(d.apps[0], "remembered")
	assertArtworkColor(t, d.appArt.get(app.ID), wantColor)
	t.Log("the installed icon was used in the library and settings after the original custom icon was unavailable")
	if updated.Width < initial.Width+45 || updated.Height < initial.Height+25 {
		t.Fatalf("website viewport did not follow the new settings: %+v → %+v", initial, updated)
	}
	zoomFile := filepath.Join(config, "WebCrate "+app.ID, "page-zoom")
	if err := os.WriteFile(zoomFile, []byte("110\n"), 0600); err != nil {
		t.Fatal(err)
	}
	zoomed, _, _ := open(d.apps[0], "remembered")
	if math.Abs(float64(zoomed.Width)-float64(updated.Width)/1.10) >= 2 ||
		math.Abs(float64(zoomed.Height)-float64(updated.Height)/1.10) >= 2 {
		t.Fatalf("the installed runtime lost the saved 110%% zoom: %+v → %+v", updated, zoomed)
	}
	configFile := filepath.Join(filepath.Dir(executable), bundle.ConfigName)
	wantConfig, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(configFile); err != nil {
		t.Fatal(err)
	}
	_, menuIcon := generatedMenu(t, d.apps[0], executable)
	stopRecovered := launchPortableMenu(t, entry, executable, renamed, menuIcon)
	select {
	case report := <-seen:
		if report != zoomed {
			t.Fatalf("opening the existing entry with missing settings changed the website: %+v → %+v", zoomed, report)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the installed app did not open its committed homepage")
	}
	stopRecovered()
	if _, err := os.Stat(configFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("opening directly changed the package before Creator restored it: %v", err)
	}
	t.Log("The original system entry opened the saved homepage with its window size, zoom and session while the packaged settings were missing.")
	withInstalledPortableCreator(t, managerEntry, installed, "repair the missing website config", func() bool {
		got, err := os.ReadFile(configFile)
		return err == nil && bytes.Equal(got, wantConfig)
	})
	before, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(filepath.Dir(executable), ".runtime")
	marker, err := os.ReadFile(markerPath)
	if err != nil || len(marker) != 64 {
		t.Fatalf("the fixture should have the current version marker: %q (%v)", marker, err)
	}
	if err := os.Truncate(executable, before.Size()/2); err != nil {
		t.Fatal(err)
	}
	withInstalledPortableCreator(t, managerEntry, installed, "repair the incomplete executable at its existing system entry", func() bool {
		info, err := os.Stat(executable)
		return err == nil && info.Size() == before.Size()
	})
	if got, err := os.ReadFile(markerPath); err != nil || !bytes.Equal(got, marker) {
		t.Fatalf("runtime repair changed the current version marker: %q (%v)", got, err)
	}
	if saved, err := store.Find(app.ID); err != nil || saved != d.apps[0] {
		t.Fatalf("repair changed the saved profile: %+v, %v", saved, err)
	}
	if repaired, repairedEntry, repairedExecutable := open(d.apps[0], "remembered"); repaired != zoomed || repairedEntry != entry || repairedExecutable != executable {
		t.Fatalf("repair changed the installed application: %+v, %q, %q", repaired, repairedEntry, repairedExecutable)
	}
	t.Log("restarting Creator repaired the missing config and truncated executable with its current version marker, keeping the installed icon, system entry, zoom and session")
	assertRemovedPath(t, oldExecutable)
	if oldEntry != entry {
		assertRemovedPath(t, oldEntry)
	}
	click(t, tt, renamed+" 的选项")
	click(t, tt, "移除应用")
	click(t, tt, "移除")
	assertRemovedPath(t, entry)
	assertRemovedPath(t, executable)
	remaining, err := store.List()
	if err != nil || len(remaining) != 0 || !tt.HasText("你的应用会出现在这里") {
		t.Fatalf("removed app still appears in the library: %v, %v", remaining, err)
	}
}

func verifyPortableRuntimeUpgrade(t *testing.T, creator, managerEntry, entry, executable string) {
	t.Helper()
	runner := "WebCrateRunner"
	if runtime.GOOS == "windows" {
		runner += ".exe"
	}
	want, err := os.ReadFile(filepath.Join(filepath.Dir(creator), runner))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	withInstalledPortableCreator(t, managerEntry, creator, "restore the menu entry and upgrade the existing executable", func() bool {
		_, entryErr := os.Stat(entry)
		if entryErr != nil {
			return false
		}
		got, err := os.ReadFile(executable)
		return err == nil && bytes.Equal(got, want)
	})
}

func withInstalledPortableCreator(t *testing.T, entry, creator, action string, ready func() bool) {
	t.Helper()
	stop := launchPortableMenu(t, entry, creator, appName, "")
	defer stop()
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if ready() {
			return
		}
	}
	t.Fatalf("the installed Creator did not %s", action)
}

func portableInstalledLibrary(t *testing.T, installed, state string) *library.Service {
	t.Helper()
	backend, err := bundle.NewBackend(filepath.Dir(installed), filepath.Join(state, "Runtimes"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := launcher.ForUser()
	if err != nil {
		t.Fatal(err)
	}
	bundles := bundle.NewLibrary(filepath.Join(state, "Generated applications"), backend)
	store := profile.NewStore(filepath.Join(state, "apps.json"))
	return library.New(store, bundles, library.Options{Launcher: entries})
}

func portableInstalledFixture(t *testing.T) (installed, managerEntry, state string) {
	t.Helper()
	installed, managerEntry = os.Getenv("WEBCRATE_RELEASE_APP"), os.Getenv("WEBCRATE_RELEASE_MENU")
	if installed == "" && managerEntry == "" {
		t.Skip("set WEBCRATE_RELEASE_APP and WEBCRATE_RELEASE_MENU to the installed Creator and its system menu entry")
	}
	checkPortableReleaseFile(t, installed)
	checkPortableReleaseFile(t, managerEntry)
	if runtime.GOOS == "windows" {
		// WebView2 adds browser, profile and IndexedDB segments to this
		// path. Keep the temporary base short enough for its file APIs.
		var err error
		state, err = os.MkdirTemp("", "creator-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			end := time.Now().Add(8 * time.Second)
			for {
				err := os.RemoveAll(state)
				if err == nil {
					return
				}
				if time.Now().After(end) {
					t.Errorf("removing the isolated application data: %v", err)
					return
				}
				time.Sleep(150 * time.Millisecond)
			}
		})
		t.Setenv("APPDATA", state)
		t.Setenv("LOCALAPPDATA", filepath.Join(state, "local"))
	} else {
		state = t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(state, "config"))
		t.Setenv("XDG_CACHE_HOME", filepath.Join(state, "cache"))
		t.Setenv("XDG_DATA_HOME", filepath.Join(state, "data"))
		// The fixtures serve all website content on the local loopback.
		t.Setenv("no_proxy", "127.0.0.1,::1,localhost")
		t.Setenv("NO_PROXY", "127.0.0.1,::1,localhost")
	}
	return installed, managerEntry, state
}

func checkPortableReleaseFile(t *testing.T, file string) {
	t.Helper()
	if !filepath.IsAbs(file) {
		t.Fatalf("expected the absolute path of an installed file, got %q", file)
	}
	if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("invalid installed file %s: %v", file, err)
	}
}

func stopTestProcess(t *testing.T, pid int, terminate func(*os.Process) error) func() {
	t.Helper()
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		if err := terminate(process); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("stopping test application: %v", err)
			_ = process.Release()
			return
		}
		if _, err := process.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("waiting for test application to stop: %v", err)
		}
	}
	t.Cleanup(stop)
	return stop
}
