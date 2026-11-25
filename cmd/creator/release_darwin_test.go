package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/launcher"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo/ui"
)

func TestMacCreatedAppKeepsInlineArtwork(t *testing.T) {
	backend, err := bundle.NewBackend(projectPath("resources", "darwin-"+runtime.GOARCH), "")
	if err != nil {
		t.Skipf("prepare the release helper with `go run ./cmd/package -prepare`: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
		svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128"><rect width="128" height="128" fill="#15895e"/></svg>`
		fmt.Fprintf(w, `<head><link rel="icon" sizes="any" href="data:image/svg+xml,%s"><link rel="manifest" href="/app.webmanifest"></head>`, url.PathEscape(svg))
	})
	provider := httptest.NewServer(mux)
	t.Cleanup(provider.Close)
	root := t.TempDir()
	bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), backend)
	custom := icons.NewCustomStore(filepath.Join(root, "CustomIcons"))
	service := library.New(profile.NewStore(filepath.Join(root, "apps.json")), bundles, library.Options{CustomIcons: custom})
	newView := func(cache string) (*desktopApp, *ui.Tester) {
		d := newDesktopApp(service, icons.NewResolver(cache, rasterizeSVG), custom, nil)
		d.work = func(f func()) { f() }
		d.loadLibrary()
		tt := ui.NewTesterWith(d.view, windowWidth, windowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
		tt.SetTitleBar(ui.TitleBar{Height: titleHeight, Left: 66, Bottom: 28})
		return d, tt
	}
	cache := filepath.Join(root, "Icons")
	d, tt := newView(cache)
	var work []func()
	click(t, tt, "网址或应用名称")
	address := provider.URL + "/app?view=board#today"
	d.work = func(f func()) { work = append(work, f) }
	pasteAddress(tt, address)
	tt.Key(0, ui.KeyEnter)
	click(t, tt, "应用名称")
	const name = "Creator 内嵌图标验收"
	typeReplace(tt, name)
	click(t, tt, "创建应用")
	if len(work) != 2 {
		t.Fatalf("expected a website lookup and a local save, got %d tasks", len(work))
	}
	lookup, create := work[0], work[1]
	work = nil
	create()
	if len(d.apps) != 1 || d.apps[0].URL != address || d.apps[0].CustomIcon != "" {
		t.Fatalf("incorrect installed profile: %+v", d.apps)
	}
	app := d.apps[0]
	path, err := bundles.Path(app)
	if err != nil {
		t.Fatal(err)
	}
	ink := color.NRGBA{R: 21, G: 137, B: 94, A: 255}
	initial, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if icon := service.InstalledArtwork(app).Image; icon == nil || color.NRGBAModel.Convert(icon.At(icon.Bounds().Dx()/2, icon.Bounds().Dy()/2)) == ink {
		t.Fatal("the initial bundle should have temporary artwork")
	}
	const renamed = "Creator 研究图标验收"
	tt.Frame()
	click(t, tt, name+" 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "应用名称")
	typeReplace(tt, renamed)
	click(t, tt, "窗口尺寸")
	click(t, tt, "窗口宽度（像素）")
	typeReplace(tt, "950")
	click(t, tt, "保存")
	if len(work) != 1 {
		t.Fatalf("editing the app queued %d tasks while its website icon was pending", len(work))
	}
	save := work[0]
	work = nil
	save()
	app = d.apps[0]
	path, err = bundles.Path(app)
	if err != nil || app.Name != renamed || app.Width != 950 {
		t.Fatalf("the pending icon blocked the settings update: %+v, %v", app, err)
	}
	lookup()
	if len(work) != 1 {
		t.Fatalf("the inline SVG queued %d installed icon updates", len(work))
	}
	work[0]()
	if updated, err := os.Stat(path); err != nil || !os.SameFile(initial, updated) {
		t.Fatalf("editing and updating the icon replaced the installed app directory: %v", err)
	}
	checkInstalledBundle(t, path, renamed, "com.webcrate.apps."+app.ID, ink)
	if current, err := service.List(); err != nil || len(current) != 1 || current[0] != app {
		t.Fatalf("the icon update changed the saved settings: %+v, %v", current, err)
	}
	assertArtworkColor(t, d.appArt.get(app.ID), ink)
	provider.Close()
	t.Run("creates offline with cached artwork", func(t *testing.T) {
		d, tt := newView(cache)
		var work []func()
		d.work = func(f func()) { work = append(work, f) }
		click(t, tt, "网址或应用名称")
		tt.Type(address)
		tt.Key(0, ui.KeyEnter)
		click(t, tt, "应用名称")
		const offlineName = "Cached Artwork"
		typeReplace(tt, offlineName)
		if d.draft.icon == nil || d.draft.icon.kind != icons.PlaceholderArtwork || len(work) != 2 {
			t.Fatal("opening or editing the form loaded the cached artwork before its worker ran")
		}
		click(t, tt, "创建应用")
		if len(work) != 3 {
			t.Fatalf("creating with cached artwork queued %d tasks", len(work))
		}
		work[2]()
		if len(d.apps) != 2 {
			t.Fatalf("the offline app was not installed: %+v", d.apps)
		}
		for _, app := range d.apps {
			if app.Name == offlineName {
				path, err := bundles.Path(app)
				if err != nil {
					t.Fatal(err)
				}
				checkInstalledBundle(t, path, offlineName, "com.webcrate.apps."+app.ID, ink)
				assertArtworkColor(t, d.appArt.get(app.ID), ink)
				work[0]()
				work[1]()
				if len(work) != 3 {
					t.Fatalf("the signed app still needed a repackaging task for its cached icon: %d tasks", len(work))
				}
				return
			}
		}
		t.Fatal("the offline app was missing from the library")
	})
	d, tt = newView("")
	click(t, tt, "应用库")
	tt.Frame()
	assertArtworkColor(t, d.appArt.get(app.ID), ink)
}

func BenchmarkMacCachedWebsiteCreation(b *testing.B) {
	backend, err := bundle.NewBackend(projectPath("resources", "darwin-"+runtime.GOARCH), "")
	if err != nil {
		b.Skipf("prepare the release helper with go run ./cmd/package -prepare: %v", err)
	}
	pngIcon, err := icons.PNG(icons.Brand())
	if err != nil {
		b.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<head><link rel="icon" href="/app.png"></head>`)
	})
	mux.HandleFunc("GET /app.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngIcon)
	})
	site := httptest.NewServer(mux)
	b.Cleanup(site.Close)
	address := site.URL + "/app"
	resolver := icons.NewResolver(b.TempDir(), rasterizeSVG)
	if _, err := resolver.Resolve(b.Context(), address); err != nil {
		b.Fatal(err)
	}
	site.Close()
	draft := profile.App{Name: "Project Desk", URL: address}

	for _, name := range []string{"Create", "Duplicate"} {
		b.Run(name, func(b *testing.B) {
			root := b.TempDir()
			bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), backend)
			service := library.New(profile.NewStore(filepath.Join(root, "apps.json")), bundles, library.Options{})
			d := newDesktopApp(service, resolver, nil, nil)
			var work []func()
			d.work = func(f func()) { work = append(work, f) }
			submit := func() int {
				d.edit(draft)
				d.save()
				if len(work) != 2 {
					b.Fatalf("submitting queued %d tasks", len(work))
				}
				work[1]() // Submit before the pending preview is processed.
				work[0]()
				for i := 2; i < len(work); i++ {
					work[i]()
				}
				updates := len(work) - 2
				clear(work)
				work = work[:0]
				if d.draft.open || len(d.apps) != 1 {
					b.Fatalf("the app was not saved: %+v", d.apps)
				}
				return updates
			}
			if name == "Duplicate" {
				submit()
			}
			var updates int
			b.ReportAllocs()
			for b.Loop() {
				updates += submit()
				if name == "Create" {
					b.StopTimer()
					id := d.apps[0].ID
					if err := service.Remove(id); err != nil {
						b.Fatal(err)
					}
					d.apps = nil
					d.appArt.remove(id)
					b.StartTimer()
				}
			}
			b.ReportMetric(float64(updates)/float64(b.N), "icon-updates/op")
		})
	}
}

func TestMacRestoresCachedWebsiteIconAfterRestart(t *testing.T) {
	backend, err := bundle.NewBackend(projectPath("resources", "darwin-"+runtime.GOARCH), "")
	if err != nil {
		t.Skipf("prepare the release helper with `go run ./cmd/package -prepare`: %v", err)
	}
	root := t.TempDir()
	bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), backend)
	profiles := filepath.Join(root, "apps.json")
	newService := func() *library.Service {
		return library.New(profile.NewStore(profiles), bundles, library.Options{})
	}
	expected := color.NRGBA{R: 28, G: 147, B: 99, A: 255}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128"><rect width="128" height="128" fill="#1c9363"/></svg>`
		fmt.Fprintf(w, `<head><link rel="icon" sizes="any" href="data:image/svg+xml,%s"></head>`, url.PathEscape(svg))
	}))
	t.Cleanup(site.Close)
	app, err := newService().Save(t.Context(), profile.App{Name: "Cached Notes", URL: site.URL}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	if installed := newService().InstalledArtwork(app); installed.Kind != icons.PlaceholderArtwork {
		t.Fatalf("the first installation did not record its placeholder: %q", installed.Kind)
	}
	path, err := bundles.Path(app)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, "Icons")
	if _, err := icons.NewResolver(cache, rasterizeSVG).Resolve(t.Context(), app.URL); err != nil {
		t.Fatal(err)
	}
	site.Close()
	// The icon lookup wrote its cache before the old process could install it.
	newView := func(resolver sitePreviewProvider) (*desktopApp, *ui.Tester) {
		d := newDesktopApp(newService(), resolver, nil, nil)
		d.work = func(f func()) { f() }
		d.loadLibrary()
		tt := ui.NewTesterWith(d.view, windowWidth, windowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
		return d, tt
	}
	d, tt := newView(icons.NewResolver(cache, rasterizeSVG))
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	click(t, tt, "应用库")
	if len(work) != 1 {
		t.Fatalf("opening the library queued %d artwork reads", len(work))
	}
	work[0]()
	tt.Frame()
	if len(work) != 2 || d.appArt.get(app.ID).kind != icons.PlaceholderArtwork {
		t.Fatalf("the library did not paint the installed icon before queuing recovery: %d tasks", len(work))
	}
	click(t, tt, app.Name+" 的选项")
	click(t, tt, "编辑应用")
	assertArtworkColor(t, d.draft.icon, expected)
	if len(work) != 2 {
		t.Fatalf("opening settings queued another icon request: %d tasks", len(work))
	}
	click(t, tt, "取消")
	work[1]()
	tt.Frame()
	assertArtworkColor(t, d.appArt.get(app.ID), expected)
	checkInstalledBundle(t, path, app.Name, "com.webcrate.apps."+app.ID, expected)
	if updated, err := os.Stat(path); err != nil || !os.SameFile(before, updated) {
		t.Fatalf("restoring artwork replaced the installed application directory: %v", err)
	}
	if err := os.RemoveAll(cache); err != nil {
		t.Fatal(err)
	}
	offline := &cachedLibraryIcons{testingIcons: testingIcons{img: icons.Brand()}}
	d, tt = newView(offline)
	click(t, tt, "应用库")
	click(t, tt, app.Name+" 的选项")
	click(t, tt, "编辑应用")
	assertArtworkColor(t, d.draft.icon, expected)
	click(t, tt, "窗口尺寸")
	click(t, tt, "窗口宽度（像素）")
	typeReplace(tt, "950")
	click(t, tt, "保存")
	if len(offline.reads) != 0 || len(offline.lookups) != 0 {
		t.Fatalf("editing read the cache or restarted discovery for installed artwork: %v, %v", offline.reads, offline.lookups)
	}
	current := d.apps[0]
	if current.Width != 950 || newService().InstalledArtwork(current).Kind != icons.WebsiteArtwork {
		t.Fatalf("changing the width lost the installed icon kind: %+v", current)
	}
	checkInstalledBundle(t, path, app.Name, "com.webcrate.apps."+app.ID, expected)
}

func TestMacInstalledCreatorWorkflow(t *testing.T) {
	installed := os.Getenv("WEBCRATE_RELEASE_APP")
	if installed == "" {
		t.Skip("set WEBCRATE_RELEASE_APP to the installed Creator.app from the release disk image")
	}
	backend := installedMacBackend(t, installed)
	previous := backend
	previousBundle := installed
	if legacy := os.Getenv("WEBCRATE_PREVIOUS_RELEASE_APP"); legacy != "" {
		previous = installedMacBackend(t, legacy)
		previousBundle = legacy
	}
	wantRuntime := macRuntimeDigest(t, installed)
	if macRuntimeDigest(t, previousBundle) == wantRuntime {
		// CI can exercise the replacement even when it has only one release.
		// Re-sign a copy of the fixture helper to give it distinct bytes.
		resources := t.TempDir()
		runner := filepath.Join(resources, "WebCrateRunner")
		macCommand(t, "/usr/bin/ditto", filepath.Join(previousBundle, "Contents", "Resources", "WebCrateRunner"), runner)
		macCommand(t, "/usr/bin/codesign", "--force", "--sign", "-", "--timestamp=none",
			"--identifier", "com.webcrate.acceptance.runner."+profile.NewID(), runner)
		var err error
		previous, err = bundle.NewBackend(resources, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	manager := isolatedMacCreator(t, installed)
	entries, err := launcher.ForUser()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := macRegisteredAppRoot(t, home)
	display := installedMacTestDisplay(t)
	display.prepareCreator(t, root, windowWidth, windowHeight)
	waitForWebKit := macWebKitLaunchGate(t)
	if display != nil {
		t.Logf("Checking installed Creator and its standalone app on %s", display.name)
	}
	t.Setenv("PATH", t.TempDir()) // The installed Creator carries the runner and writes apps offline.

	appKit, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(appKit)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	workspace := objc.ID(objc.GetClass("NSWorkspace")).Send(objc.RegisterName("sharedWorkspace"))
	appClass := objc.ID(objc.GetClass("NSRunningApplication"))

	fixture, seen := installedWebsiteFixture(t)
	state := filepath.Join(root, "Library", "Application Support", "WebCrate")
	appRoot := filepath.Join(root, "Applications", "WebCrate")
	store := profile.NewStore(filepath.Join(state, "apps.json"))
	custom := icons.NewCustomStore(filepath.Join(state, "CustomIcons"))
	bundles := bundle.NewLibrary(appRoot, previous)
	service := library.New(store, bundles, library.Options{Launcher: entries, CustomIcons: custom})
	iconColor := color.NRGBA{R: 71, G: 132, B: 211, A: 255}
	icon := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	draw.Draw(icon, icon.Bounds(), image.NewUniform(iconColor), image.Point{}, draw.Src)
	newView := func() (*desktopApp, *ui.Tester) {
		d := newDesktopApp(service, &testingIcons{}, custom, nil)
		d.chooseIcon = func() (image.Image, error) { return icon, nil }
		d.work = func(f func()) { f() }
		d.loadLibrary()
		tt := ui.NewTesterWith(d.view, windowWidth, windowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
		tt.SetTitleBar(ui.TitleBar{Height: titleHeight, Left: 66, Bottom: 28})
		return d, tt
	}
	d, tt := newView()
	fill := func(label, value string) {
		t.Helper()
		click(t, tt, label)
		typeReplace(tt, value)
	}
	size := func(width, height string) {
		t.Helper()
		click(t, tt, "窗口尺寸")
		fill("窗口宽度（像素）", width)
		fill("窗口高度（像素）", height)
	}
	const name, renamed = "Creator 测试工作台", "Creator 研究工作台"
	fill("网址或应用名称", fixture)
	click(t, tt, "使用网址 "+profile.Host(fixture))
	fill("应用名称", name)
	click(t, tt, "更换图标")
	size("680", "490")
	click(t, tt, "创建应用")
	if len(d.apps) != 1 {
		t.Fatalf("application creation did not appear in the library: %v", tt.Texts())
	}
	app := d.apps[0]
	id := "com.webcrate.apps." + app.ID
	trackMacTestDomain(t, home, id)
	identifier := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), id)
	registeredPath := func() string {
		url := workspace.Send(objc.RegisterName("URLForApplicationWithBundleIdentifier:"), identifier)
		return objc.Send[string](url.Send(objc.RegisterName("path")), objc.RegisterName("UTF8String"))
	}
	runningApps := func() objc.ID {
		return appClass.Send(objc.RegisterName("runningApplicationsWithBundleIdentifier:"), identifier)
	}
	running := func() objc.ID { return runningApps().Send(objc.RegisterName("firstObject")) }
	isFixture := func(application objc.ID) bool {
		url := application.Send(objc.RegisterName("bundleURL"))
		path := objc.Send[string](url.Send(objc.RegisterName("path")), objc.RegisterName("UTF8String"))
		return strings.HasPrefix(path, root+string(filepath.Separator))
	}
	opened := make(map[int32]objc.ID)
	defer func() {
		// Keep the original application objects even if a failed upgrade
		// makes Launch Services lose their bundle URLs.
		for _, application := range opened {
			if !objc.Send[bool](application, objc.RegisterName("isTerminated")) {
				application.Send(objc.RegisterName("forceTerminate"))
			}
			application.Send(objc.RegisterName("release"))
		}
		list := runningApps()
		for i, count := uint(0), objc.Send[uint](list, objc.RegisterName("count")); i < count; i++ {
			application := list.Send(objc.RegisterName("objectAtIndex:"), i)
			if isFixture(application) {
				application.Send(objc.RegisterName("forceTerminate"))
			}
		}
		if os.Getenv("WEBCRATE_MAC_TEST_SCRATCH") == "" {
			_ = exec.Command("/usr/bin/defaults", "delete", id).Run()
			_ = os.RemoveAll(filepath.Join(home, "Library", "WebKit", id))
		}
	}()
	assertInstalled := func(app profile.App, wantName string) string {
		t.Helper()
		path, err := bundles.Path(app)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(path) != wantName+".app" || filepath.Dir(path) != filepath.Join(appRoot, app.ID) {
			t.Fatalf("installed application path = %q", path)
		}
		for deadline := time.Now().Add(3 * time.Second); registeredPath() != path; time.Sleep(30 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("macOS application entry = %q, want %q", registeredPath(), path)
			}
		}
		checkInstalledBundle(t, path, wantName, id, iconColor)
		return path
	}
	first := assertInstalled(app, name)
	storedPNG, err := os.ReadFile(filepath.Join(state, "CustomIcons", app.CustomIcon+".png"))
	if err != nil {
		t.Fatal(err)
	}
	installedIcon, err := os.ReadFile(filepath.Join(first, "Contents", "Resources", "AppIcon.icns"))
	if err != nil || !bytes.HasSuffix(installedIcon, storedPNG) {
		t.Fatalf("the signed bundle did not use the stored 256px PNG: %v", err)
	}
	// Use the earlier on-disk layout as well as the earlier packaged runner.
	if err := os.Rename(filepath.Join(first, "Contents", "Resources", ".revision"), filepath.Join(filepath.Dir(first), ".revision")); err != nil {
		t.Fatal(err)
	}
	oldInfo := filepath.Join(first, "Contents", "Info.plist")
	for _, key := range []string{"NSCameraUsageDescription", "NSMicrophoneUsageDescription"} {
		macCommand(t, "/usr/bin/plutil", "-remove", key, oldInfo)
	}
	marker := filepath.Join(first, "Contents", "Resources", ".runtime")
	stamped, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	oldHash, _, _ := bytes.Cut(stamped, []byte("."))
	if err := os.WriteFile(marker, oldHash, 0o644); err != nil {
		t.Fatal(err)
	}
	macCommand(t, "/usr/bin/codesign", "--force", "--sign", "-", "--timestamp=none", first)

	openFromLibrary := func(label string) {
		t.Helper()
		click(t, tt, "打开 "+label)
		if tt.HasText("无法打开「" + label + "」") {
			t.Fatal("opening the installed application from Creator failed")
		}
	}
	openFromSystem := func(environment ...string) {
		t.Helper()
		waitForWebKit(0)
		openClosedMacApp(t, id, environment...)
	}
	checkOpen := func(wantName, wantState string, width, height int) int32 {
		t.Helper()
		select {
		case report := <-seen:
			if report != (installedWebsiteReport{State: wantState, URL: fixture, Width: width, Height: height}) {
				t.Fatalf("website at %q reports %+v, want session %s and viewport %d×%d", fixture, report, wantState, width, height)
			}
			t.Logf("%s opened the saved URL at %d×%d with %s session", wantName, report.Width, report.Height, report.State)
		case <-time.After(15 * time.Second):
			t.Fatalf("%s did not load the saved URL %s", wantName, fixture)
		}
		application := running()
		if !isFixture(application) || objc.Send[string](application.Send(objc.RegisterName("localizedName")), objc.RegisterName("UTF8String")) != wantName {
			t.Fatalf("macOS did not launch the independent %s application", wantName)
		}
		pid := objc.Send[int32](application, objc.RegisterName("processIdentifier"))
		if opened[pid] == 0 {
			opened[pid] = application.Send(objc.RegisterName("retain"))
		}
		display.verifyWindow(t, int(pid))
		// Wait while the app is open so the immediate quit-and-reopen still
		// exercises the Launch Services handoff.
		waitForWebKit(2 * time.Second)
		return pid
	}
	checkSameProcess := func(pid int32) {
		t.Helper()
		if count, active := objc.Send[uint](runningApps(), objc.RegisterName("count")), objc.Send[int32](running(), objc.RegisterName("processIdentifier")); count != 1 || active != pid {
			t.Fatalf("reopening %q found %d processes (first %d), want only %d", registeredPath(), count, active, pid)
		}
		display.verifyWindow(t, int(pid))
		select {
		case report := <-seen:
			t.Fatalf("reopening the running application loaded the site again: %+v", report)
		case <-time.After(300 * time.Millisecond):
		}
	}
	closeApp := func(pid int32) {
		t.Helper()
		application := opened[pid]
		if application == 0 {
			t.Fatalf("no isolated application to close for process %d", pid)
		}
		asked := false
		for deadline := time.Now().Add(6 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			// The application can disappear from AppKit's list before its
			// process exits. Wait for both before opening the same bundle.
			if errors.Is(syscall.Kill(int(pid), 0), syscall.ESRCH) && running() == 0 {
				return
			}
			// The loopback page can report before Launch Services finishes starting the app.
			if !asked && objc.Send[bool](application, objc.RegisterName("isFinishedLaunching")) {
				asked = objc.Send[bool](application, objc.RegisterName("terminate"))
			}
		}
		t.Fatalf("standalone application %d did not quit (terminate accepted: %t)", pid, asked)
	}
	display.prepareStandalone(t, id, app)
	if !tt.Focused("打开 " + name) {
		t.Fatal("the new app did not have library focus")
	}
	waitForWebKit(0)
	tt.Key(0, ui.KeyEnter)
	pid := checkOpen(name, "new", 680, 490)
	openFromLibrary(name)
	checkSameProcess(pid)
	for range 3 {
		closeApp(pid)
		openFromLibrary(name)
		pid = checkOpen(name, "remembered", 680, 490)
	}
	t.Log("Creator reopened the app immediately after each quit and restored its website session")

	// Keep the last reopened process for the running upgrade scenario.
	currentRoot := filepath.Join(root, "Applications", "Creator")
	current := filepath.Join(currentRoot, app.ID, filepath.Base(first))
	updatedAt := func(path string) bool {
		digest, err := os.ReadFile(filepath.Join(path, "Contents", "Resources", ".runtime"))
		runnerHash, metadataVersion, _ := strings.Cut(string(digest), ".")
		return err == nil && runnerHash == wantRuntime && metadataVersion != "" && registeredPath() == path
	}
	if updatedAt(first) {
		t.Fatal("the fixture already contains the current runtime before upgrade")
	}
	beforeUpgrade, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	waitForWebKit(0)
	restoreWithInstalledMacCreator(t, manager, root, display, "refresh the running application at its existing system entry", func() bool { return updatedAt(first) })
	assertRemovedPath(t, current)
	if after, err := os.Stat(first); err != nil || !os.SameFile(beforeUpgrade, after) {
		t.Fatalf("runtime upgrade replaced the directory registered with Launch Services: %v", err)
	}
	if saved, err := store.Find(app.ID); err != nil || saved != app {
		t.Fatalf("the installed Creator changed the saved profile: %+v, %v", saved, err)
	}
	t.Run("open while the original app is running", func(t *testing.T) {
		t.Setenv("HOME", root)
		currentLibrary, err := bundle.NewDesktopLibrary(state, backend)
		if err != nil {
			t.Fatal(err)
		}
		bundles = currentLibrary
		service = library.New(store, bundles, library.Options{Launcher: entries, CustomIcons: custom})
		d.library = service
		click(t, tt, "打开 "+name)
		if tt.HasText("无法打开「" + name + "」") {
			t.Fatal("the updated library could not open the running app")
		}
	})
	assertRemovedPath(t, current)
	checkSameProcess(pid)
	macCommand(t, "/usr/bin/open", "-b", id)
	checkSameProcess(pid)
	closeApp(pid)
	runnerPath := filepath.Join(first, "Contents", "MacOS", "WebCrateRunner")
	runnerInfo, err := os.Stat(runnerPath)
	if err != nil {
		t.Fatal(err)
	}
	truncatedSize := runnerInfo.Size() / 2
	if err := os.Truncate(runnerPath, truncatedSize); err != nil {
		t.Fatal(err)
	}
	if !updatedAt(first) {
		t.Fatal("the incomplete executable should still have the current version marker and system entry")
	}
	waitForWebKit(0)
	restoreWithInstalledMacCreator(t, manager, root, display, "repair the incomplete executable at its existing system entry", func() bool {
		info, err := os.Stat(runnerPath)
		return err == nil && info.Size() > truncatedSize && updatedAt(first)
	})
	if after, err := os.Stat(first); err != nil || !os.SameFile(beforeUpgrade, after) {
		t.Fatalf("runtime repair replaced the registered application directory: %v", err)
	}
	assertInstalled(app, name)
	var legacyFrame string
	if display != nil {
		legacyFrame = display.prepareLegacyStandalone(t, id, app)
	}
	macCommand(t, "/usr/bin/defaults", "write", id, "WebsitePageZoomPercent", "-int", "125")
	openFromSystem()
	closeApp(checkOpen(name, "remembered", 544, 392))
	if legacyFrame != "" {
		saved := macCommand(t, "/usr/bin/defaults", "read", id, "WebsiteWindowFrame")
		if !strings.Contains(saved, strconv.Quote(legacyFrame)) {
			t.Fatalf("the standalone application saved %s after restoring legacy frame %s", saved, legacyFrame)
		}
		t.Log("the upgraded application restored the AppKit autosave position and saved it in the current format")
	}
	macCommand(t, "/usr/bin/defaults", "delete", id, "WebsitePageZoomPercent")
	t.Log("the installed Creator updated a running application without reloading its website and kept its zoomed viewport and session when reopened")
	t.Log("the installed Creator repaired a truncated runtime with a current marker, keeping the signed app, entry, icon and website session")
	assertInstalled(app, name)
	assertRemovedPath(t, current)
	configFile := filepath.Join(first, "Contents", "Resources", bundle.ConfigName)
	configBytes, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	damagedConfig := configBytes[:len(configBytes)-1]
	if err := os.WriteFile(configFile, damagedConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	// Seal the damaged test fixture so Launch Services can exercise the
	// runner's settings recovery even when it validates bundle resources.
	macCommand(t, "/usr/bin/codesign", "--force", "--sign", "-", "--timestamp=none", first)
	macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", first)
	// Launch Services does not take the fixture library's location from the
	// Go test process. Supply it to this launch without changing the system.
	openFromSystem("HOME=" + root)
	closeApp(checkOpen(name, "remembered", 680, 490))
	if got, err := os.ReadFile(configFile); err != nil || !bytes.Equal(got, damagedConfig) {
		t.Fatalf("direct launch changed the signed configuration: %v", err)
	}
	macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", first)
	t.Log("The signed launcher opened the original system entry with its committed settings and recovered the website session.")
	waitForWebKit(0)
	restoreWithInstalledMacCreator(t, manager, root, display, "repair the incomplete website config", func() bool {
		data, err := os.ReadFile(configFile)
		return err == nil && bytes.Equal(data, configBytes) && registeredPath() == first
	})
	assertRemovedPath(t, current)
	if saved, err := store.Find(app.ID); err != nil || saved != app {
		t.Fatalf("repair changed the saved profile: %+v, %v", saved, err)
	}
	assertInstalled(app, name)
	openFromSystem()
	pid = checkOpen(name, "remembered", 680, 490)
	t.Log("restarting Creator repaired the incomplete website config and the registered application kept its icon and session")

	d, tt = newView()
	click(t, tt, "应用库")
	before, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	click(t, tt, name+" 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "保存")
	if len(d.apps) != 1 || d.apps[0] != app || d.draft.open {
		t.Fatalf("unchanged settings did not return to the existing app: %v", tt.Texts())
	}
	if after, err := os.Stat(first); err != nil || !os.SameFile(before, after) {
		t.Fatalf("unchanged settings rebuilt the installed app: %v", err)
	}

	iconFile := filepath.Join(first, "Contents", "Resources", "AppIcon.icns")
	originalIcon, err := os.ReadFile(iconFile)
	if err != nil {
		t.Fatal(err)
	}
	damagedIcon := bytes.Clone(originalIcon)
	damagedIcon[0] ^= 1
	if err := os.WriteFile(iconFile, damagedIcon, 0o644); err != nil {
		t.Fatal(err)
	}
	click(t, tt, name+" 的选项")
	click(t, tt, "编辑应用")
	size("720", "510")
	started := time.Now()
	click(t, tt, "保存")
	resizeTime := time.Since(started)
	if len(d.apps) != 1 || d.apps[0].ID != app.ID || d.apps[0].Width != 720 || d.apps[0].Height != 510 ||
		!tt.HasText("重新打开应用后使用新尺寸") {
		t.Fatalf("resizing the running application did not save its settings: %v", tt.Texts())
	}
	assertInstalled(d.apps[0], name)
	if restored, err := os.ReadFile(iconFile); err != nil || !bytes.Equal(restored, originalIcon) {
		t.Fatalf("saving settings did not restore the custom icon: %v", err)
	}
	openFromLibrary(name)
	checkSameProcess(pid)
	macCommand(t, "/usr/bin/open", "-g", "-b", id)
	checkSameProcess(pid)
	if after, err := os.Stat(first); err != nil || !os.SameFile(before, after) {
		t.Fatalf("resizing replaced the installed directory addressed by the system entry: %v", err)
	}
	closeApp(pid)
	openFromSystem()
	pid = checkOpen(name, "remembered", 720, 510)
	t.Logf("saved and repaired the custom icon in %s, kept the installed directory and window, and used the new size and same session on relaunch", resizeTime.Round(time.Millisecond))

	click(t, tt, name+" 的选项")
	click(t, tt, "编辑应用")
	fill("应用名称", renamed)
	size("740", "530")
	iconColor = color.NRGBA{R: 194, G: 85, B: 121, A: 255}
	replacement := image.NewNRGBA(icon.Bounds())
	draw.Draw(replacement, replacement.Bounds(), image.NewUniform(iconColor), image.Point{}, draw.Src)
	d.chooseIcon = func() (image.Image, error) { return replacement, nil }
	click(t, tt, "更换图标")
	click(t, tt, "保存")
	if len(d.apps) != 1 || d.apps[0].ID != app.ID || !tt.HasText("重新打开应用后使用新尺寸") {
		t.Fatalf("editing did not preserve the installed app: %v", tt.Texts())
	}
	updated := assertInstalled(d.apps[0], renamed)
	assertRemovedPath(t, first)
	if after, err := os.Stat(updated); err != nil || !os.SameFile(before, after) {
		t.Fatalf("renaming replaced the installed directory addressed by the system entry: %v", err)
	}
	openFromLibrary(renamed)
	checkSameProcess(pid)
	macCommand(t, "/usr/bin/open", "-g", "-b", id)
	checkSameProcess(pid)
	closeApp(pid)
	openFromSystem()
	closeApp(checkOpen(renamed, "remembered", 740, 530))
	t.Log("renaming and changing the app icon kept the existing directory and open website, and macOS used the new name and icon on the same app")
	click(t, tt, renamed+" 的选项")
	click(t, tt, "移除应用")
	click(t, tt, "移除")
	assertRemovedPath(t, updated)
	out, err := exec.CommandContext(t.Context(), "/usr/bin/open", "-g", "-b", id).CombinedOutput()
	var launchError *exec.ExitError
	if !errors.As(err, &launchError) {
		t.Fatalf("expected macOS to reject opening the removed application: %v (%s)", err, out)
	}
	apps, err := store.List()
	if err != nil || len(apps) != 0 || !tt.HasText("你的应用会出现在这里") {
		t.Fatalf("removed app still appears in the library: %v, %v", apps, err)
	}

	address, ink, _, _, _ := streamingWebsiteIcon(t, false)
	const automatic = "Creator 自动图标验收"
	d.resolver = icons.NewResolver(filepath.Join(state, "Icons"), rasterizeSVG)
	click(t, tt, "创建第一个应用")
	fill("网址或应用名称", address)
	tt.Key(0, ui.KeyEnter)
	fill("应用名称", automatic)
	click(t, tt, "创建应用")
	if len(d.apps) != 1 || d.apps[0].CustomIcon != "" || d.apps[0].URL != address {
		t.Fatalf("automatic icon creation saved the wrong profile: %+v", d.apps)
	}
	autoApp := d.apps[0]
	t.Cleanup(func() { _ = service.Remove(autoApp.ID) })
	autoPath, err := bundles.Path(autoApp)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(autoPath) != filepath.Join(currentRoot, autoApp.ID) {
		t.Fatalf("new app was installed outside the Creator folder: %q", autoPath)
	}
	checkInstalledBundle(t, autoPath, automatic, "com.webcrate.apps."+autoApp.ID, ink)
	t.Log("the signed bundle used the declared website icon while the page body was still streaming")
	if err := os.RemoveAll(filepath.Join(state, "Icons")); err != nil {
		t.Fatal(err)
	}
	d, tt = newView()
	click(t, tt, "应用库")
	tt.Frame()
	assertArtworkColor(t, d.appArt.get(autoApp.ID), ink)
	click(t, tt, automatic+" 的选项")
	click(t, tt, "编辑应用")
	assertArtworkColor(t, d.draft.icon, ink)
	size("800", "570")
	click(t, tt, "保存")
	if len(d.apps) != 1 || d.apps[0].ID != autoApp.ID || d.apps[0].Width != 800 || d.apps[0].Height != 570 {
		t.Fatalf("changing the size after restart lost the installed app: %+v", d.apps)
	}
	checkInstalledBundle(t, autoPath, automatic, "com.webcrate.apps."+autoApp.ID, ink)
	assertArtworkColor(t, d.appArt.get(autoApp.ID), ink)
	t.Log("with the website icon cache removed, the reopened library, settings and signed app used the same icon")
}

func installedMacBackend(t *testing.T, installed string) bundle.Backend {
	t.Helper()
	macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", installed)
	if macCommand(t, "/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-",
		filepath.Join(installed, "Contents", "Info.plist")) != "com.webcrate.desktop" {
		t.Fatalf("%q is not the Creator manager", installed)
	}
	backend, err := bundle.NewBackend(filepath.Join(installed, "Contents", "Resources"), "")
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func macRuntimeDigest(t *testing.T, installed string) string {
	t.Helper()
	runner, err := os.ReadFile(filepath.Join(installed, "Contents", "Resources", "WebCrateRunner"))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(runner))
}

func isolatedMacCreator(t *testing.T, installed string) string {
	t.Helper()
	// Exercise the shipped executable and resources under a separate Launch
	// Services identity so the fixture cannot address the user's Creator.
	path := filepath.Join(t.TempDir(), "Creator Acceptance.app")
	macCommand(t, "/usr/bin/ditto", installed, path)
	identifier := "com.webcrate.acceptance." + profile.NewID()
	macCommand(t, "/usr/bin/plutil", "-replace", "CFBundleIdentifier", "-string", identifier,
		filepath.Join(path, "Contents", "Info.plist"))
	macCommand(t, "/usr/bin/codesign", "--force", "--sign", "-", "--timestamp=none", path)
	macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", path)
	return path
}

func restoreWithInstalledMacCreator(t *testing.T, installed, fixtureDirectory string, display *macTestDisplay, action string, ready func() bool) {
	t.Helper()
	withIsolatedMacCreator(t, installed, fixtureDirectory, nil, func(ctx context.Context, pid int, exited <-chan error, output *bytes.Buffer) {
		poll := time.NewTicker(30 * time.Millisecond)
		defer poll.Stop()
		for !ready() {
			select {
			case err := <-exited:
				t.Fatalf("installed Creator exited during startup: %v (%s)", err, output.String())
			case <-ctx.Done():
				<-exited
				t.Fatalf("installed Creator did not %s: %s", action, output.String())
			case <-poll.C:
			}
		}
		display.verifyWindow(t, pid)
	})
}

func withIsolatedMacCreator(t *testing.T, installed, fixtureDirectory string, environment []string,
	check func(context.Context, int, <-chan error, *bytes.Buffer)) {
	t.Helper()
	if id := macCommand(t, "/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-",
		filepath.Join(installed, "Contents", "Info.plist")); !strings.HasPrefix(id, "com.webcrate.acceptance.") {
		t.Fatalf("acceptance must run a Creator fixture with its own application identity, got %q", id)
	}
	// Use a short socket path so MyGo's single-instance lock stays isolated
	// from an installed Creator that the user may already have open.
	socketDir := macTestSocketDir(t, "creator-acceptance-")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(installed, "Contents", "MacOS", "Creator"))
	cmd.Env = append(os.Environ(), "HOME="+fixtureDirectory, "CFFIXED_USER_HOME="+fixtureDirectory, "XDG_RUNTIME_DIR="+socketDir)
	cmd.Env = append(cmd.Env, environment...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
		close(exited)
	}()
	defer func() {
		cancel()
		<-exited
	}()
	check(ctx, cmd.Process.Pid, exited, &output)
	// MyGo treats SIGTERM as a normal quit. Let the fixture finish writing
	// window state and any Go coverage counters before canceling the command.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("quit the isolated Creator: %v", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the isolated Creator did not quit cleanly: %v (%s)", err, output.String())
		}
	case <-time.After(4 * time.Second):
		t.Fatal("the isolated Creator did not finish quitting")
	}
}

func macTestSocketDir(t *testing.T, prefix string) string {
	t.Helper()
	base := os.Getenv("WEBCRATE_MAC_TEST_SCRATCH")
	if base == "" {
		base = "/tmp"
	}
	directory, err := os.MkdirTemp(base, prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func macRegisteredAppRoot(t *testing.T, home string) string {
	t.Helper()
	base := os.Getenv("WEBCRATE_MAC_TEST_APPLICATIONS")
	guarded := base != ""
	if !guarded {
		base = filepath.Join(home, "Applications")
		if err := os.MkdirAll(base, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	directory, err := os.MkdirTemp(base, "Creator Acceptance ")
	if err != nil {
		t.Fatal(err)
	}
	if !guarded {
		t.Cleanup(func() { _ = os.RemoveAll(directory) })
	}
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func trackMacTestDomain(t *testing.T, home, id string) {
	t.Helper()
	scratch := os.Getenv("WEBCRATE_MAC_TEST_SCRATCH")
	if scratch == "" {
		return
	}
	// Launch Services gives the independently opened app the real user's home.
	// Claim its fresh test domain before the first launch can create data there.
	for _, path := range []string{filepath.Join(home, "Library", "WebKit", id), filepath.Join(home, "Library", "Preferences", id+".plist")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the fixture cannot claim the data path %s: %v", path, err)
		}
	}
	domains := filepath.Join(scratch, "mac-test-domains")
	if err := os.MkdirAll(domains, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domains, id), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func macCommand(t *testing.T, program string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), program, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v (%s)", program, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func openClosedMacApp(t *testing.T, id string, environment ...string) {
	t.Helper()
	args := []string{"-b", id}
	for _, variable := range environment {
		args = append(args, "--env", variable)
	}
	const retries = 2
	for attempt := 0; ; attempt++ {
		out, err := exec.CommandContext(t.Context(), "/usr/bin/open", args...).CombinedOutput()
		if err == nil {
			return
		}
		// Launch Services can still reference the closed process after PID
		// exit. Report its transient rejection and use the same entry again.
		if attempt == retries || (!bytes.Contains(out, []byte("error -600.")) && !bytes.Contains(out, []byte("error -609."))) {
			t.Fatalf("open registered app %s: %v (%s)", id, err, out)
		}
		t.Logf("Launch Services has not released the previous process, retrying the system entry: %s", bytes.TrimSpace(out))
		time.Sleep(100 * time.Millisecond)
	}
}

func checkInstalledBundle(t *testing.T, path, name, id string, want color.NRGBA) {
	t.Helper()
	macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", path)
	contents := filepath.Join(path, "Contents")
	readKey := func(key string) string {
		return macCommand(t, "/usr/bin/plutil", "-extract", key, "raw", "-o", "-", filepath.Join(contents, "Info.plist"))
	}
	if readKey("CFBundleDisplayName") != name || readKey("CFBundleIdentifier") != id || readKey("CFBundleIconFile") != "AppIcon" {
		t.Fatalf("unexpected system name, identity or icon in %s", path)
	}
	for _, key := range []string{"NSCameraUsageDescription", "NSMicrophoneUsageDescription"} {
		if readKey(key) == "" {
			t.Fatalf("missing native usage description for %s in %s", key, path)
		}
	}
	icon := filepath.Join(t.TempDir(), "icon.png")
	macCommand(t, "/usr/bin/sips", "-s", "format", "png", filepath.Join(contents, "Resources", "AppIcon.icns"), "--out", icon)
	file, err := os.Open(icon)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	bitmap, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := color.NRGBAModel.Convert(bitmap.At(bitmap.Bounds().Dx()/2, bitmap.Bounds().Dy()/2)); got != want {
		t.Fatalf("Finder icon color = %v, want %v", got, want)
	}
}
