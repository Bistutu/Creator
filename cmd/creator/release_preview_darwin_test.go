package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/catalog"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/settings"
	"golang.org/x/sys/unix"
)

func TestMacCreatorCustomIconOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	path := orientedIconFixture(t, t.TempDir())
	t.Setenv("WEBCRATE_TEST_ICON_PICKER", path)
	config := filepath.Join(state, "Library", "Application Support", "WebCrate")
	store := profile.NewStore(filepath.Join(config, "apps.json"))
	var app profile.App
	var installed, iconPath string
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, _ int) {
		ui = ui.panel(appName)
		ui.press("选择 DeepSeek")
		ui.expectValue("应用网址", "https://chat.deepseek.com")
		ui.press("更换图标")
		ui.control("恢复网站图标")
		ui.checkOrientedIcon("deskpad-custom-photo.png")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		ui.press("创建应用")
		ui.control("打开 DeepSeek")
		apps, err := store.List()
		if err != nil || len(apps) != 1 || apps[0].CustomIcon == "" {
			t.Fatalf("creating with the selected picture saved %+v (%v)", apps, err)
		}
		app = apps[0]
		installed = filepath.Join(state, "Applications", "Creator", app.ID, "DeepSeek.app")
		iconPath = filepath.Join(installed, "Contents", "Resources", "AppIcon.icns")
		assertOrientedIcon(t, icons.ReadInstalledIcon(iconPath))
		assertOrientedIcon(t, icons.NewCustomStore(filepath.Join(config, "CustomIcons")).Get(app.CustomIcon))
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", installed)
	})
	original, err := os.ReadFile(iconPath)
	if err != nil {
		t.Fatal(err)
	}
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, _ int) {
		ui = ui.panel(appName)
		ui.press("应用库")
		ui.press("DeepSeek 的选项")
		ui.press("编辑应用")
		ui.checkOrientedIcon("deskpad-custom-photo-reopened.png")
		ui.press("窗口尺寸")
		ui.fill("窗口宽度（像素）", "720")
		ui.fill("窗口高度（像素）", "510")
		ui.press("保存")
		ui.control("打开 DeepSeek")
		if saved, err := store.Find(app.ID); err != nil || saved.Width != 720 || saved.Height != 510 || saved.CustomIcon != app.CustomIcon {
			t.Fatalf("editing dimensions changed the icon or missed the new dimensions: %+v (%v)", saved, err)
		}
		if after, err := os.ReadFile(iconPath); err != nil || !bytes.Equal(original, after) {
			t.Fatalf("editing dimensions rewrote the custom icon: %v", err)
		}
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", installed)
	})
	t.Log("Importing the fixture kept the rotated pixels after relaunch and a size-only save.")
}

func TestMacCreatorCustomIconUsesWebsiteNameOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	t.Setenv("WEBCRATE_TEST_ICON_PICKER", orientedIconFixture(t, t.TempDir()))
	address, _, _, manifestWhileWaiting := manifestWebsiteFixture(t)
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		ui.press("添加")
		ui.press("更换图标")
		ui.control("恢复网站图标")
		ui.fill("应用网址", address)
		for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(30 * time.Millisecond) {
			field := ui.control("应用名称")
			if field.Value != "" || time.Now().After(deadline) {
				t.Fatalf("after selecting an icon and entering the address, the name field was %+v", field)
			}
			if field.Placeholder == fixtureManifestName {
				break
			}
		}
		assertManifestReadWhilePageIconWaited(t, manifestWhileWaiting)
		ui.checkOrientedIcon("deskpad-custom-icon-with-website-name.png")
		ui.press("创建应用")
		ui.control("打开 " + fixtureManifestName)
		config := filepath.Join(state, "Library", "Application Support", "WebCrate")
		apps, err := profile.NewStore(filepath.Join(config, profile.Filename)).List()
		if err != nil || len(apps) != 1 || apps[0].Name != fixtureManifestName || apps[0].URL != address || !profile.ValidIconKey(apps[0].CustomIcon) {
			t.Fatalf("creation with the suggested name and custom icon saved %+v (%v)", apps, err)
		}
		installed := filepath.Join(state, "Applications", "Creator", apps[0].ID, fixtureManifestName+".app")
		assertOrientedIcon(t, icons.ReadInstalledIcon(filepath.Join(installed, "Contents", "Resources", "AppIcon.icns")))
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", installed)
		display.verifyWindow(t, pid)
	})
	t.Log("The draft used the site's declared name with the selected image, and the signed app preserved both.")
}

func (ui macCreatorUI) checkOrientedIcon(name string) {
	ui.t.Helper()
	width, height := ui.contentSize()
	samples := [...]struct {
		x, y float64
		want color.NRGBA
	}{
		{0.66, 0.19, orientedFixtureRed}, {0.37, 0.19, orientedFixtureBlue},
	}
	var found [len(samples)]color.NRGBA
	for deadline := time.Now().Add(1500 * time.Millisecond); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		bounds := ui.control("更换图标").Bounds
		frame, err := png.Decode(bytes.NewReader(ui.capture("")))
		if err != nil {
			ui.t.Fatal(err)
		}
		for i, sample := range samples {
			x := int((bounds.X + bounds.Width*sample.x) * float64(frame.Bounds().Dx()) / width)
			y := int((bounds.Y + bounds.Height*sample.y) * float64(frame.Bounds().Dy()) / height)
			found[i] = color.NRGBAModel.Convert(frame.At(x, y)).(color.NRGBA)
		}
		if found[0] == samples[0].want && found[1] == samples[1].want {
			ui.capture(name)
			return
		}
	}
	ui.t.Fatalf("photo preview rendered %v at the sampled points, want %v and %v", found, samples[0].want, samples[1].want)
}

func TestMacCreatorLoadsRecommendationsAfterLibraryStartupOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	preferences := settings.Default()
	preferences.StartPage = settings.LibraryPage
	path := filepath.Join(state, "Library", "Application Support", "WebCrate", "preferences.json")
	if err := settings.Save(path, preferences); err != nil {
		t.Fatal(err)
	}
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, _ int) {
		ui = ui.panel(appName)
		const search = "网址或应用名称"
		ui.expectFocus("创建第一个应用")
		ui.key("Enter")
		ui.expectFocus(search)
		ui.control("选择 小红书")
		ui.capture("deskpad-recommendations-after-library.png")
		ui.fill(search, "推理 DeepSeek")
		ui.expectFocus(search)
		ui.control("选择 DeepSeek")
		ui.capture("deskpad-recommendation-combined-search.png")
		ui.key("Enter")
		ui.expectValue("应用网址", "https://chat.deepseek.com")
		ui.checkPresetIcon(color.NRGBA{R: 77, G: 107, B: 254, A: 255}, "deskpad-deepseek-after-library.png")
		ui.press("取消")
		ui.expectFocus(search)
		ui.expectValue(search, "推理 DeepSeek")
		ui.fill(search, "攻略 小红书")
		ui.control("选择 小红书")
		ui.key("Enter")
		const recommendation = "https://www.xiaohongshu.com/explore"
		ui.expectValue("应用网址", recommendation)
		ui.checkPresetIcon(color.NRGBA{R: 243, G: 32, B: 71, A: 255}, "deskpad-rednote-after-library.png")
		ui.press("取消")
		const address = recommendation + "?return=https://example.org/saved#today"
		ui.fill(search, strings.TrimPrefix(address, "https://"))
		ui.press("使用网址 小红书")
		ui.expectValue("应用网址", address)
		ui.press("创建应用")
		ui.control("打开 小红书")
		apps, err := profile.NewStore(filepath.Join(filepath.Dir(path), "apps.json")).List()
		if err != nil || len(apps) != 1 || apps[0].URL != address || apps[0].CustomIcon != "" {
			t.Fatalf("creating a recommended app saved %+v (%v)", apps, err)
		}
		generated := filepath.Join(state, "Applications", "Creator", apps[0].ID, "小红书.app")
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", generated)
		got, gotOK := icons.ReadInstalledIcon(filepath.Join(generated, "Contents", "Resources", "AppIcon.icns")).(*image.NRGBA)
		sites, err := catalog.Load()
		if err != nil {
			t.Fatal(err)
		}
		preset, ok := catalog.ByURL(sites, address)
		if !ok {
			t.Fatal("小红书 is missing from the recommendations")
		}
		picture, err := preset.Icon()
		want, wantOK := picture.(*image.NRGBA)
		if err != nil || !gotOK || !wantOK || got.Rect != want.Rect || !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("the signed app's 128-pixel icon differs from the recommendation: %v", err)
		}
	})
	t.Logf("Creator opened DeepSeek and Xiaohongshu by combining terms. The signed app kept the nested URL and embedded Xiaohongshu artwork on %s", display.name)
}

func TestMacCreatorRetriesAppRemovalOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	config := filepath.Join(state, "Library", "Application Support", "WebCrate")
	store := profile.NewStore(filepath.Join(config, "apps.json"))
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		ui.press("选择 DeepSeek")
		ui.press("创建应用")
		ui.control("打开 DeepSeek")
		apps, err := store.List()
		if err != nil || len(apps) != 1 || apps[0].Name != "DeepSeek" {
			t.Fatalf("creating the test application saved %+v (%v)", apps, err)
		}
		app := apps[0]
		directory := filepath.Join(state, "Applications", "Creator", app.ID)
		path := filepath.Join(directory, "DeepSeek.app")
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(config, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(config, info.Mode().Perm()) })
		remove := func() {
			ui.press("DeepSeek 的选项")
			ui.press("移除应用")
			ui.press("移除")
		}
		remove()
		ui.control("无法移除「DeepSeek」，请重试")
		ui.control("打开 DeepSeek")
		if saved, err := store.Find(app.ID); err != nil || saved != app {
			t.Fatalf("the failed removal changed %+v (%v)", saved, err)
		}
		if after, err := os.Stat(path); err != nil || !os.SameFile(before, after) {
			t.Fatalf("the failed removal changed the original app bundle: %v", err)
		}
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", path)
		display.verifyWindow(t, pid)
		ui.capture("deskpad-removal-retry.png")
		if err := os.Chmod(config, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
		remove()
		ui.control("创建第一个应用")
		assertRemovedPath(t, directory)
		if apps, err := store.List(); err != nil || len(apps) != 0 {
			t.Fatalf("successful retry left %+v (%v)", apps, err)
		}
	})
	t.Logf("A read-only profile directory preserved the signed app and library entry until a successful retry on %s", display.name)
}

func TestMacCreatorRecoversInterruptedPackagesOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	config := filepath.Join(state, "Library", "Application Support", "WebCrate")
	store := profile.NewStore(filepath.Join(config, "apps.json"))
	preferences := settings.Default()
	preferences.StartPage = settings.LibraryPage
	if err := settings.Save(filepath.Join(config, "preferences.json"), preferences); err != nil {
		t.Fatal(err)
	}
	installs := filepath.Join(state, "Applications", "Creator")
	backend, err := bundle.NewBackend(filepath.Join(manager, "Contents", "Resources"), "")
	if err != nil {
		t.Fatal(err)
	}
	service := library.New(store, bundle.NewLibrary(installs, backend), library.Options{})
	app, err := service.Save(t.Context(), profile.App{Name: "Recovery desk", URL: "https://example.com"}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	verify := stageInterruptedPackages(t, installs, app.ID)
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		ui.control("打开 " + app.Name)
		verify()
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", filepath.Join(installs, app.ID, app.Name+".app"))
		if apps, err := store.List(); err != nil || len(apps) != 1 || apps[0] != app {
			t.Fatalf("startup changed the library to %+v (%v)", apps, err)
		}
		display.verifyWindow(t, pid)
	})
	t.Logf("Startup restored the original signed application and cleared the abandoned build and committed removal on %s", display.name)
}

func TestMacCreatorAddressPreviewOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	libraryPath := filepath.Join(state, "Library", "Application Support", "WebCrate", "apps.json")
	if err := os.MkdirAll(filepath.Dir(libraryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(libraryPath, 0o600); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(state, "hold-library-read")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEBCRATE_TEST_LIST_GATE", gate)
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		exerciseMacCreatorPreview(t, ui.panel(appName), libraryPath, gate)
		display.verifyWindow(t, pid)
		t.Logf("Checked Creator's live URL card, creation form and library recovery on %s", display.name)
	})
}

func TestMacCreatorNamesAppBeforeHeadCompletesOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	const name = "Streaming Workbench"
	ink := color.NRGBA{R: 25, G: 142, B: 95, A: 255}
	mark := image.NewNRGBA(image.Rect(0, 0, 192, 192))
	draw.Draw(mark, mark.Bounds(), image.NewUniform(ink), image.Point{}, draw.Src)
	picture, err := icons.PNG(mark)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan time.Time, 1)
	tail := make(chan struct{})
	release := sync.OnceFunc(func() { close(tail) })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /workbench", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, `<!doctype html><head><meta name="application-name" content="%s">`, name)
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		select {
		case started <- time.Now():
		default:
		}
		select {
		case <-tail:
			fmt.Fprint(w, `<link rel="icon" href="/mark.png" sizes="192x192"></head><body>Workbench</body>`)
		case <-req.Context().Done():
		}
	})
	mux.HandleFunc("GET /mark.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(picture) })
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)
	defer release()
	address := site.URL + "/workbench"
	store := profile.NewStore(filepath.Join(state, "Library", "Application Support", "WebCrate", profile.Filename))
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		ui.fill("网址或应用名称", address)
		ui.press("使用网址 " + profile.Host(address))
		var headSent time.Time
		select {
		case headSent = <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("Creator did not request the website preview")
		}
		for {
			field := ui.control("应用名称")
			elapsed := time.Since(headSent)
			if elapsed >= 900*time.Millisecond || field.Value != "" {
				t.Fatalf("name field after %s with the head held: %+v", elapsed, field)
			}
			if field.Placeholder == name {
				t.Logf("The draft displayed the declared name %s after the server sent it", elapsed.Round(time.Millisecond))
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		ui.press("创建应用")
		ui.control("打开 " + name)
		apps, err := store.List()
		if err != nil || len(apps) != 1 || apps[0].Name != name || apps[0].URL != address {
			t.Fatalf("creation with the remainder of the head held saved %+v (%v)", apps, err)
		}
		release()
		ui.checkIconPixel("打开 "+name, 9+16, ink, "deskpad-streaming-website-icon.png")
		app := filepath.Join(state, "Applications", "Creator", apps[0].ID, name+".app")
		icon := icons.ReadInstalledIcon(filepath.Join(app, "Contents", "Resources", "AppIcon.icns"))
		if icon == nil || color.NRGBAModel.Convert(icon.At(icon.Bounds().Dx()/2, icon.Bounds().Dy()/2)) != ink {
			t.Fatal("the installed application did not receive the website icon")
		}
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", app)
		display.verifyWindow(t, pid)
		t.Log("Creating while the head was held used the declared name, then the signed app received the website icon")
	})
}

func TestMacCreatorUsesSecondPageIconOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	address, ink, _, _, _ := streamingWebsiteIcon(t, true)
	checkMacCreatorWebsiteArtwork(t, manager, display, address, fixtureWebsiteName, ink, "alternate-website")
}

func TestMacCreatorReturnsPendingWebsiteArtworkOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	address, ink, requested, finish := pendingWebsiteArtwork(t)
	const label = "使用网址 " + fixtureWebsiteName
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		ui.fill("网址或应用名称", address)
		ui.press("使用网址 " + profile.Host(address))
		select {
		case <-requested:
		case <-time.After(time.Second):
			t.Fatal("the draft did not request the icon")
		}
		ui.key("Escape")
		ui.expectCardFocus(label, address)
		finish()
		ui.checkIconPixel(label, 10+16, ink, "deskpad-returned-website-artwork.png")
		ui.key("Enter")
		ui.expectValue("应用网址", address)
		ui.checkIconPixel("更换图标", 22, ink, "")
		ui.press("创建应用")
		ui.expectFocus("打开 " + fixtureWebsiteName)
		store := profile.NewStore(filepath.Join(state, "Library", "Application Support", "WebCrate", profile.Filename))
		apps, err := store.List()
		if err != nil || len(apps) != 1 || apps[0].Name != fixtureWebsiteName || apps[0].URL != address {
			t.Fatalf("reopening the form saved %+v (%v)", apps, err)
		}
		app := filepath.Join(state, "Applications", "Creator", apps[0].ID, fixtureWebsiteName+".app")
		icon := icons.ReadInstalledIcon(filepath.Join(app, "Contents", "Resources", "AppIcon.icns"))
		if icon == nil || color.NRGBAModel.Convert(icon.At(icon.Bounds().Dx()/2, icon.Bounds().Dy()/2)) != ink {
			t.Fatal("the signed application did not use the artwork from the reopened form")
		}
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", app)
		display.verifyWindow(t, pid)
		t.Log("Escape returned to the original URL card, which received the pending icon and used it in the signed application.")
	})
}

func TestMacCreatorSharesPendingWebsiteArtworkOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	first, ink, requested, finish := pendingWebsiteArtwork(t)
	second := profile.ArtworkAddress(first) + "#review"
	const design, review = "Design Desk", "Review Desk"
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		ui.fill("网址或应用名称", first)
		ui.press("使用网址 " + profile.Host(first))
		select {
		case <-requested:
		case <-time.After(time.Second):
			t.Fatal("the first draft did not request the website icon")
		}
		ui.fill("应用名称", design)
		ui.press("创建应用")
		ui.expectFocus("打开 " + design)
		ui.press("创建")
		ui.fill("网址或应用名称", second)
		ui.press("使用网址 " + fixtureWebsiteName)
		ui.expectValue("应用网址", second)
		if field := ui.control("应用名称"); field.Placeholder != fixtureWebsiteName || field.Value != "" {
			t.Fatalf("the second draft did not reuse the reported website name: %+v", field)
		}
		ui.fill("应用名称", review)
		ui.press("创建应用")
		ui.expectFocus("打开 " + review)
		root := filepath.Join(state, "Library", "Application Support", "WebCrate")
		apps, err := profile.NewStore(filepath.Join(root, profile.Filename)).List()
		if err != nil || len(apps) != 2 || apps[0].ID == apps[1].ID {
			t.Fatalf("expected two independent profiles while the website icon was pending: %+v (%v)", apps, err)
		}
		finish()
		ui.checkIconPixel("打开 "+design, 9+16, ink, "")
		ui.checkIconPixel("打开 "+review, 9+16, ink, "deskpad-shared-website-artwork.png")
		addresses := map[string]string{design: first, review: second}
		for _, app := range apps {
			if addresses[app.Name] != app.URL {
				t.Fatalf("the %q profile lost its own URL: %s", app.Name, app.URL)
			}
			if row := ui.control("打开 " + app.Name); row.Description != app.URL || row.Bounds.Height != 44 {
				t.Fatalf("the compact library row did not expose its complete address: %+v", row)
			}
			path := filepath.Join(state, "Applications", "Creator", app.ID, app.Name+".app")
			if icon := icons.ReadInstalledIcon(filepath.Join(path, "Contents", "Resources", "AppIcon.icns")); icon == nil ||
				color.NRGBAModel.Convert(icon.At(icon.Bounds().Dx()/2, icon.Bounds().Dy()/2)) != ink {
				t.Fatalf("the signed application %q did not receive the shared icon", app.Name)
			}
			macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", path)
		}
		display.verifyWindow(t, pid)
		t.Log("Both signed apps were created before the artwork arrived, then received the same icon from one website request.")
	})
}

func TestMacCreatorUsesManifestArtworkOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	address, ink, _, manifestWhileWaiting := manifestWebsiteFixture(t)
	checkMacCreatorWebsiteArtwork(t, manager, display, address, fixtureManifestName, ink, "manifest-website")
	assertManifestReadWhilePageIconWaited(t, manifestWhileWaiting)
}

func checkMacCreatorWebsiteArtwork(t *testing.T, manager string, display *macTestDisplay, address, name string, ink color.NRGBA, screenshot string) {
	t.Helper()
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	label := "使用网址 " + profile.Host(address)
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		ui.fill("网址或应用名称", address)
		ui.press(label)
		ui.checkIconPixel("更换图标", 22, ink, "deskpad-"+screenshot+"-draft.png")
		if field := ui.control("应用名称"); field.Placeholder != name || field.Value != "" {
			t.Fatalf("the new application name field is %+v", field)
		}
		ui.press("取消")
		label = "使用网址 " + name
		ui.expectCardFocus(label, address)
		ui.checkIconPixel(label, 10+16, ink, "deskpad-"+screenshot+"-card.png")
		ui.press(label)
		ui.expectValue("应用网址", address)
		ui.press("创建应用")
		ui.expectFocus("打开 " + name)
		ui.checkIconPixel("打开 "+name, 9+16, ink, "deskpad-"+screenshot+"-library.png")
		store := profile.NewStore(filepath.Join(state, "Library", "Application Support", "WebCrate", profile.Filename))
		apps, err := store.List()
		if err != nil || len(apps) != 1 || apps[0].Name != name || apps[0].URL != address || apps[0].CustomIcon != "" {
			t.Fatalf("creation saved %+v (%v)", apps, err)
		}
		app := filepath.Join(state, "Applications", "Creator", apps[0].ID, name+".app")
		icon := icons.ReadInstalledIcon(filepath.Join(app, "Contents", "Resources", "AppIcon.icns"))
		if icon == nil || color.NRGBAModel.Convert(icon.At(icon.Bounds().Dx()/2, icon.Bounds().Dy()/2)) != ink {
			t.Fatal("the application package did not have the alternate icon")
		}
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", app)
		display.verifyWindow(t, pid)
		t.Log("The draft, card, library and signed application used the declared name and the second site icon")
	})
}

func TestMacCreatorCachedURLArtworkOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	config := filepath.Join(state, "Library", "Application Support", "WebCrate")
	cache := filepath.Join(config, "Icons")
	address, ink, _, _, stop := streamingWebsiteIcon(t, false)
	earlier := address + "?view=notes"
	address += "?view=board"
	if _, err := icons.NewResolver(cache, rasterizeSVG).Resolve(t.Context(), address); err != nil {
		t.Fatal(err)
	}
	stop()
	first, homepage := address+"#today", address+"#review"
	const name = "Offline Workbench"
	store := profile.NewStore(filepath.Join(config, "apps.json"))
	gate := filepath.Join(state, "hold-earlier-cache-read")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		ui.fill("网址或应用名称", earlier)
		expectMacFixtureWaiting(t, gate)
		ui.fill("网址或应用名称", first)
		label := "使用网址 " + fixtureWebsiteName
		ui.focus("网址或应用名称")
		ui.key("Down")
		ui.expectCardFocus(label, first)
		ui.checkIconPixel(label, 10+16, ink, "deskpad-cached-url-card.png")
		if err := os.Remove(gate); err != nil {
			t.Fatal(err)
		}
		ui.press(label)
		ui.expectValue("应用网址", first)
		ui.checkIconPixel("更换图标", 22, ink, "deskpad-cached-url-draft.png")
		if field := ui.control("应用名称"); field.Placeholder != fixtureWebsiteName || field.Value != "" {
			t.Fatalf("the offline name field is %+v", field)
		}
		ui.fill("应用名称", name)
		ui.press("创建应用")
		ui.expectFocus("打开 " + name)
		apps, err := store.List()
		if err != nil || len(apps) != 1 || apps[0].URL != first || apps[0].CustomIcon != "" {
			t.Fatalf("offline creation saved %+v (%v)", apps, err)
		}
		installed := filepath.Join(state, "Applications", "Creator", apps[0].ID, name+".app")
		resources := filepath.Join(installed, "Contents", "Resources")
		icon := filepath.Join(resources, "AppIcon.icns")
		before, err := os.ReadFile(icon)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(cache); err != nil {
			t.Fatal(err)
		}
		ui.fill("搜索应用库", "Workbench")
		ui.press(name + " 的选项")
		ui.press("编辑应用")
		ui.fill("应用网址", homepage)
		ui.checkIconPixel("更换图标", 22, ink, "deskpad-offline-home-edit.png")
		ui.press("保存")
		ui.control("已保存首页")
		ui.expectFocus("打开 " + name)
		ui.expectValue("搜索应用库", "Workbench")
		ui.checkIconPixel("打开 "+name, 9+16, ink, "deskpad-offline-home-saved.png")
		if saved, err := store.Find(apps[0].ID); err != nil || saved.URL != homepage || saved.CustomIcon != "" {
			t.Fatalf("editing the home address saved %+v (%v)", saved, err)
		}
		assertPackagedHome(t, filepath.Join(resources, bundle.ConfigName), homepage)
		if after, err := os.ReadFile(icon); err != nil || !bytes.Equal(before, after) {
			t.Fatalf("changing the fragment rewrote the installed icon: %v", err)
		}
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", installed)
		display.verifyWindow(t, pid)
		t.Log("The offline card displayed the current name and icon while the previous read was held. Creation and the homepage edit kept that artwork.")
	}, "WEBCRATE_TEST_CACHE_GATE="+gate, "WEBCRATE_TEST_CACHE_ADDRESS="+earlier)
}

func TestMacCreatorLibraryArtworkDuringRepairOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	libraryPath := filepath.Join(state, "Library", "Application Support", "WebCrate", "apps.json")
	store := profile.NewStore(libraryPath)
	backend, err := bundle.NewBackend(filepath.Join(manager, "Contents", "Resources"), "")
	if err != nil {
		t.Fatal(err)
	}
	bundles := bundle.NewLibrary(filepath.Join(state, "Applications", "Creator"), backend)
	service := library.New(store, bundles, library.Options{})
	const available, restoring = "阅读工作台", "研究工作台"
	availableInk := color.NRGBA{R: 79, G: 114, B: 226, A: 255}
	restoringInk := color.NRGBA{R: 219, G: 112, B: 82, A: 255}
	install := func(name, address string, ink color.NRGBA) string {
		t.Helper()
		picture := image.NewNRGBA(image.Rect(0, 0, 256, 256))
		draw.Draw(picture, picture.Bounds(), image.NewUniform(ink), image.Point{}, draw.Src)
		app, err := service.Save(t.Context(), profile.App{Name: name, URL: address}, library.Icon{
			Artwork: icons.Artwork{Image: picture, Kind: icons.WebsiteArtwork},
		})
		if err != nil {
			t.Fatal(err)
		}
		path, err := bundles.Path(app)
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	install(available, "https://example.org/reading", availableInk)
	repair := install(restoring, "https://example.com/research", restoringInk)
	if err := os.Remove(filepath.Join(repair, "Contents", "MacOS", "WebCrateRunner")); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(state, "hold-repair")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEBCRATE_TEST_FIND_GATE", gate)
	t.Setenv("WEBCRATE_TEST_FIND_ID", filepath.Base(filepath.Dir(repair)))
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		// Wait until repair owns the newest application's package.
		expectMacFixtureWaiting(t, gate)
		ui.press("应用库")
		ui.control("打开 " + restoring)
		ui.checkIconPixel("打开 "+available, 9+16, availableInk, "deskpad-library-icon-while-restoring.png")
		if err := os.Remove(gate); err != nil {
			t.Fatal(err)
		}
		ui.checkIconPixel("打开 "+restoring, 9+16, restoringInk, "deskpad-library-icons-ready.png")
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", repair)
		display.verifyWindow(t, pid)
	})
	t.Log("The other installed icon appeared during startup repair, and the held app's icon appeared in the same window after release.")
}

func expectMacFixtureWaiting(t *testing.T, gate string) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); ; time.Sleep(15 * time.Millisecond) {
		if _, err := os.Stat(gate + ".waiting"); err == nil {
			return
		} else if !os.IsNotExist(err) || time.Now().After(deadline) {
			t.Fatalf("the fixture did not reach %s: %v", filepath.Base(gate), err)
		}
	}
}

func TestMacCreatorReloadsAnEvictedIconOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	root := filepath.Join(state, "Library", "Application Support", "WebCrate")
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	backend, err := bundle.NewBackend(filepath.Join(manager, "Contents", "Resources"), "")
	if err != nil {
		t.Fatal(err)
	}
	bundles := bundle.NewLibrary(filepath.Join(state, "Applications", "Creator"), backend)
	service := library.New(store, bundles, library.Options{})
	app, err := service.Save(t.Context(), profile.App{Name: "图标工作台", URL: "https://example.org/library"}, library.Icon{
		Artwork: icons.Artwork{Image: icons.Brand(), Kind: icons.WebsiteArtwork},
	})
	if err != nil {
		t.Fatal(err)
	}
	path, err := bundles.Path(app)
	if err != nil {
		t.Fatal(err)
	}
	iconPath := filepath.Join(path, "Contents", "Resources", "AppIcon.icns")
	original := color.NRGBA{R: 62, G: 107, B: 221, A: 255}
	replacement := color.NRGBA{R: 203, G: 96, B: 91, A: 255}
	writeLargeMacFixtureIcon(t, path, iconPath, original)
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		row := "打开 " + app.Name
		ui.press("应用库")
		ui.checkIconPixel(row, 9+16, original, "")
		ui.press("创建")
		ui.control("网址或应用名称")
		expected := writeLargeMacFixtureIcon(t, path, iconPath, replacement)
		ui.press("应用库")
		ui.checkIconPixel(row, 9+16, replacement, "deskpad-library-icon-reloaded.png")
		ui.press(app.Name + " 的选项")
		ui.press("编辑应用")
		ui.checkIconPixel("更换图标", 22, replacement, "deskpad-reloaded-icon-editor.png")
		ui.press("窗口尺寸")
		ui.fill("窗口宽度（像素）", "930")
		ui.press("保存")
		ui.control("重新打开应用后使用新尺寸")
		saved, err := store.Find(app.ID)
		if err != nil || saved.Width != 930 || saved.WindowRevision != app.WindowRevision+1 {
			t.Fatalf("the resized app was not saved: %+v (%v)", saved, err)
		}
		if actual, err := os.ReadFile(iconPath); err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("resizing changed the original ICNS bytes: %v", err)
		}
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", path)
		display.verifyWindow(t, pid)
	})
	t.Log("The visible large icon stayed live, reloaded from disk on return to the library, and kept its original bytes after a size edit.")
}

func writeLargeMacFixtureIcon(t *testing.T, appPath, iconPath string, ink color.NRGBA) []byte {
	t.Helper()
	// A single legacy 1024-pixel frame exceeds the recent-image budget.
	picture := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	draw.Draw(picture, picture.Bounds(), image.NewUniform(ink), image.Point{}, draw.Src)
	png, err := icons.PNG(picture)
	if err != nil {
		t.Fatal(err)
	}
	data := binary.BigEndian.AppendUint32([]byte("icns"), uint32(16+len(png)))
	data = append(data, "ic10"...)
	data = binary.BigEndian.AppendUint32(data, uint32(8+len(png)))
	data = append(data, png...)
	if err := os.WriteFile(iconPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	macCommand(t, "/usr/bin/codesign", "--force", "--sign", "-", "--timestamp=none", appPath)
	return data
}

func TestMacCreatorKeyboardOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		const search = "网址或应用名称"
		const address = "https://www.xiaohongshu.com/explore/topic?source=creator#notes"
		ui.expectFocus(search)
		ui.request("input", map[string]any{"events": []map[string]string{{"type": "text", "text": address}}}, nil)
		ui.expectValue(search, address)
		ui.capture("deskpad-startup-url-focus.png")
		ui.key("Enter")
		ui.expectValue("应用网址", address)
		ui.key("Escape")
		ui.expectFocus(search)
		ui.press("AI")
		ui.focus(search)
		ui.fill(search, "写作")
		ui.key("Down")
		ui.expectFocus("选择 ChatGPT")
		ui.key("Right")
		ui.expectFocus("选择 Gemini")
		ui.key("Right")
		ui.expectCardFocus("选择 Claude", "https://claude.ai")
		ui.capture("deskpad-keyboard-choice.png")
		ui.key("Enter")
		ui.expectValue("应用网址", "https://claude.ai")
		ui.key("Escape")
		ui.expectFocus("选择 Claude")
		ui.key("Escape")
		ui.expectFocus(search)
		ui.expectValue(search, "写作")
		ui.press("全部")
		ui.fill(search, "")
		first := ui.control("选择 DeepSeek")
		ui.focus(search)
		ui.key("Down")
		ui.expectFocus("选择 GitHub")
		ui.key("End")
		ui.expectFocus("选择 Hacker News")
		ui.key("Enter")
		ui.expectValue("应用网址", "https://news.ycombinator.com")
		ui.key("Escape")
		ui.expectFocus("选择 Hacker News")
		ui.key("Escape")
		ui.expectFocus(search)
		ui.fill(search, address)
		if card := ui.control(first.Label); card.Bounds.Height != first.Bounds.Height {
			t.Fatalf("entering an address after scrolling left the first recommendation clipped: %+v", card.Bounds)
		}
		ui.key("Down")
		ui.expectCardFocus("使用网址 小红书", address)
		ui.key("Down")
		ui.expectFocus("选择 GitHub")
		ui.key("End")
		ui.expectFocus("选择 Hacker News")
		ui.key("Escape")
		ui.expectFocus(search)
		ui.key("Tab")
		ui.expectFocus("清除搜索")
		ui.key("Enter")
		ui.expectFocus(search)
		ui.expectValue(search, "")
		if card := ui.control(first.Label); card.Bounds != first.Bounds || card.Bounds.Height == 0 {
			t.Fatalf("clearing the URL left the first row at %+v, want %+v", card.Bounds, first.Bounds)
		}
		ui.capture("deskpad-search-cleared.png")
		display.verifyWindow(t, pid)
		t.Logf("Keyboard selection, focus restoration and returning to the first row after clearing the URL worked on %s", display.name)
	})
}

func TestMacCreatorLibrarySearchStartsAtFirstMatchOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	path := filepath.Join(state, "Library", "Application Support", "WebCrate", profile.Filename)
	writeLibraryFixture(t, path, 120, "")
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		ui.press("应用库")
		const search, latest, match = "搜索应用库", "打开 Workspace 119", "打开 Workspace 099"
		first := ui.control(latest)
		scrollAway := func(label string) {
			t.Helper()
			row := ui.control(label).Bounds
			events := []map[string]any{{
				"type": "wheel", "x": row.X + row.Width/2, "y": row.Y + row.Height/2, "dx": 0, "dy": 1500, "precise": true,
			}}
			ui.request("input", map[string]any{"events": events}, nil)
			ui.request("idle", map[string]any{"timeout": 1400}, nil)
			var tree struct{ Nodes []macControl }
			ui.request("tree", nil, &tree)
			for _, item := range tree.Nodes {
				if item.Label == label && item.Bounds.Height > 0 {
					t.Fatalf("the library did not scroll away from %q: %+v", label, item.Bounds)
				}
			}
		}
		expectTop := func(label string) {
			t.Helper()
			ui.expectFocus(search)
			if row := ui.control(label); row.Bounds != first.Bounds || row.Bounds.Height == 0 {
				t.Fatalf("the library left %q at %+v, want %+v", label, row.Bounds, first.Bounds)
			}
		}
		scrollAway(latest)
		ui.focus(search)
		ui.fill(search, "Workspace 0")
		expectTop(match)
		ui.key("Enter")
		ui.expectFocus(search)
		for i := 99; i >= 90; i-- {
			ui.key("Down")
			ui.expectFocus(fmt.Sprintf("打开 Workspace %03d", i))
		}
		ui.key("Up")
		ui.expectFocus("打开 Workspace 091")
		ui.key("Escape")
		ui.expectFocus(search)
		ui.expectValue(search, "Workspace 0")
		ui.key("Down")
		ui.expectFocus(match)
		ui.key("Up")
		expectTop(match)
		ui.capture("deskpad-library-first-match.png")
		scrollAway(match)
		ui.press("清除搜索")
		ui.expectValue(search, "")
		expectTop(latest)
		if seventh := ui.control("打开 Workspace 113"); seventh.Bounds.Height != first.Bounds.Height {
			t.Fatalf("the seventh library row was clipped: %+v", seventh.Bounds)
		}
		display.verifyWindow(t, pid)
	})
}

func TestMacCreatorRevealsInstalledAppOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	state := macRegisteredAppRoot(t, home)
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	site, seen := installedWebsiteFixture(t)
	site += "#today"
	store := profile.NewStore(filepath.Join(state, "Library", "Application Support", "WebCrate", "apps.json"))
	const name = "Creator 工作台"
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		create := func(address string) {
			ui.fill("网址或应用名称", address)
			ui.press("使用网址 " + profile.Host(address))
			ui.fill("应用名称", name)
			ui.press("创建应用")
			ui.expectFocus("打开 " + name)
		}
		create(site)
		original, err := store.List()
		if err != nil || len(original) != 1 || original[0].URL != site {
			t.Fatalf("first creation saved %+v (%v), want %q", original, err, site)
		}
		ui.press("创建")
		create(paddedFixturePort(t, site))
		if saved, err := store.List(); err != nil || len(saved) != 1 || saved[0] != original[0] {
			t.Fatalf("repeated creation changed the profile: %+v (%v)", saved, err)
		}
		ui.expectCardFocus("打开 "+name, site)
		ui.capture("deskpad-library-existing-app.png")
		display.verifyWindow(t, pid)
		const search = "搜索应用库"
		ui.focus(search)
		ui.fill(search, "#today 工作台")
		ui.expectFocus(search)
		openMacCreatorLibrarySelection(t, ui, display, home, original[0], seen)
		ui.press(name + " 的选项")
		ui.control("复制网址")
		ui.key("Escape")
	})
}

func openMacCreatorLibrarySelection(t *testing.T, ui macCreatorUI, display *macTestDisplay, home string, app profile.App, seen <-chan installedWebsiteReport) {
	t.Helper()
	id := "com.webcrate.apps." + app.ID
	trackMacTestDomain(t, home, id)
	if os.Getenv("WEBCRATE_MAC_TEST_SCRATCH") == "" {
		defer func() {
			_ = os.RemoveAll(filepath.Join(home, "Library", "WebKit", id))
			_ = os.Remove(filepath.Join(home, "Library", "Preferences", id+".plist"))
		}()
	}
	display.prepareStandalone(t, id, app)
	started := time.Now()
	ui.key("Enter")
	select {
	case report := <-seen:
		if want := (installedWebsiteReport{State: "new", URL: app.URL, Width: app.Width, Height: app.Height}); report != want {
			t.Fatalf("the standalone page reported %+v, want %+v", report, want)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Enter did not load the new app's saved page")
	}
	ready := time.Since(started).Round(time.Millisecond)
	appKit, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(appKit)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	identifier := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), id)
	active := objc.ID(objc.GetClass("NSRunningApplication")).Send(objc.RegisterName("runningApplicationsWithBundleIdentifier:"), identifier)
	if objc.Send[uint](active, objc.RegisterName("count")) != 1 {
		t.Fatal("the filtered library search did not launch one independent application")
	}
	application := active.Send(objc.RegisterName("firstObject"))
	defer application.Send(objc.RegisterName("terminate"))
	display.verifyWindow(t, int(objc.Send[int32](application, objc.RegisterName("processIdentifier"))))
	t.Logf("Creation and resubmission focused the same entry. Enter in the filtered search opened its saved page in %s on %s", ready, display.name)
}

func automatedMacCreator(t *testing.T) (string, *macTestDisplay) {
	t.Helper()
	installed, executable := os.Getenv("WEBCRATE_RELEASE_APP"), os.Getenv("WEBCRATE_AUTOMATED_CREATOR")
	if installed == "" || executable == "" {
		t.Skip("set WEBCRATE_RELEASE_APP and WEBCRATE_AUTOMATED_CREATOR to the release bundle and automation-enabled test binary")
	}
	display := installedMacTestDisplay(t)
	if display == nil {
		t.Fatal("set WEBCRATE_MAC_TEST_DISPLAY to the DeskPad display before launching Creator")
	}
	installedMacBackend(t, installed)
	manager := isolatedMacCreator(t, installed)
	macCommand(t, "/usr/bin/ditto", executable, filepath.Join(manager, "Contents", "MacOS", "Creator"))
	macCommand(t, "/usr/bin/codesign", "--force", "--sign", "-", "--timestamp=none", manager)
	macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", manager)
	return manager, display
}

func withAutomatedMacCreator(t *testing.T, manager, state string, display *macTestDisplay, check func(macCreatorUI, int), environment ...string) {
	t.Helper()
	socket := filepath.Join(macTestSocketDir(t, "creator-ui-"), "ui.sock")
	withIsolatedMacCreator(t, manager, state, append(environment, "MYGO_AUTOMATION="+socket),
		func(ctx context.Context, pid int, exited <-chan error, output *bytes.Buffer) {
			display.verifyWindow(t, pid)
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, "unix", socket)
			if err != nil {
				select {
				case failure := <-exited:
					t.Fatalf("the Creator fixture exited: %v (%s)", failure, output.String())
				default:
					t.Fatalf("open the Creator fixture's automation socket: %v", err)
				}
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
				t.Fatal(err)
			}
			check(macCreatorUI{t: t, send: json.NewEncoder(conn), read: json.NewDecoder(conn)}, pid)
		})
}

type macCreatorUI struct {
	t      *testing.T
	send   *json.Encoder
	read   *json.Decoder
	window int
}

func (ui macCreatorUI) request(method string, params map[string]any, target any) {
	ui.t.Helper()
	if ui.window != 0 && method != "windows" {
		if params == nil {
			params = make(map[string]any)
		}
		params["window"] = ui.window
	}
	if err := ui.send.Encode(map[string]any{"id": 1, "method": method, "params": params}); err != nil {
		ui.t.Fatalf("send %s to Creator: %v", method, err)
	}
	var reply struct {
		Result json.RawMessage
		Error  *struct{ Code, Message string }
	}
	if err := ui.read.Decode(&reply); err != nil {
		ui.t.Fatalf("read %s from Creator: %v", method, err)
	}
	if reply.Error != nil {
		ui.t.Fatalf("Creator %s: %s (%s)", method, reply.Error.Message, reply.Error.Code)
	}
	if target != nil {
		if err := json.Unmarshal(reply.Result, target); err != nil {
			ui.t.Fatalf("decode %s from Creator: %v", method, err)
		}
	}
}

type macControl struct {
	ID, Label, Value, Role, Description string
	Placeholder                         string
	Focused                             bool
	Selection                           *[2]int
	Bounds                              struct{ X, Y, Width, Height float64 }
}

func (ui macCreatorUI) control(label string) macControl {
	ui.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ui.request("idle", map[string]any{"timeout": 1400}, nil)
		var tree struct {
			Focus string
			Nodes []macControl
		}
		ui.request("tree", nil, &tree)
		for _, element := range tree.Nodes {
			if element.Label == label {
				element.Focused = tree.Focus == element.ID
				return element
			}
		}
		if time.Now().After(deadline) {
			ui.t.Fatalf("the live Creator window %d is missing %q", ui.window, label)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (ui macCreatorUI) press(label string) {
	ui.t.Helper()
	ui.request("act", map[string]any{"id": ui.control(label).ID, "action": "press"}, nil)
}

func (ui macCreatorUI) fill(label, value string) {
	ui.t.Helper()
	ui.request("act", map[string]any{"id": ui.control(label).ID, "action": "setValue", "text": value}, nil)
}

func (ui macCreatorUI) focus(label string) {
	ui.t.Helper()
	ui.request("act", map[string]any{"id": ui.control(label).ID, "action": "focus"}, nil)
}

func (ui macCreatorUI) key(key string) {
	ui.t.Helper()
	ui.request("input", map[string]any{"events": []map[string]string{{"type": "key", "key": key}}}, nil)
}

func (ui macCreatorUI) expectFocus(label string) {
	ui.t.Helper()
	for deadline := time.Now().Add(1500 * time.Millisecond); time.Now().Before(deadline); {
		if ui.control(label).Focused {
			return
		}
	}
	ui.t.Fatalf("the live Creator window did not focus %q", label)
}

func (ui macCreatorUI) expectCardFocus(label, address string) {
	ui.t.Helper()
	ui.expectFocus(label)
	if got := ui.control(label).Description; got != address {
		ui.t.Fatalf("the accessibility description for %q is %q, want %q", label, got, address)
	}
	var tree struct{ Nodes []macControl }
	ui.request("tree", nil, &tree)
	for _, node := range tree.Nodes {
		if node.Role == "tooltip" {
			ui.t.Fatalf("keyboard focus on %q left a tooltip over the window: %+v", label, node)
		}
	}
}

func (ui macCreatorUI) capture(name string) []byte {
	ui.t.Helper()
	var frame struct{ PNG []byte }
	ui.request("capture", nil, &frame)
	if len(frame.PNG) == 0 {
		ui.t.Fatal("Creator returned an empty screenshot")
	}
	if directory := os.Getenv("WEBCRATE_SCREENSHOTS"); directory != "" && name != "" {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			ui.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), frame.PNG, 0o600); err != nil {
			ui.t.Fatal(err)
		}
	}
	return frame.PNG
}

func (ui macCreatorUI) contentSize() (float64, float64) {
	ui.t.Helper()
	for _, window := range ui.windows() {
		if bounds := window.ContentBounds; window.ID == ui.window && bounds.Width > 0 && bounds.Height > 0 {
			return bounds.Width, bounds.Height
		}
	}
	ui.t.Fatal("the Creator window returned empty content bounds")
	return 0, 0
}

func (ui macCreatorUI) checkIconPixel(label string, inset float64, want color.NRGBA, name string) {
	ui.t.Helper()
	width, height := ui.contentSize()
	var got color.NRGBA
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		bounds := ui.control(label).Bounds
		frame, err := png.Decode(bytes.NewReader(ui.capture("")))
		if err != nil {
			ui.t.Fatal(err)
		}
		x := int((bounds.X + inset) * float64(frame.Bounds().Dx()) / width)
		y := int((bounds.Y + bounds.Height/2) * float64(frame.Bounds().Dy()) / height)
		got = color.NRGBAModel.Convert(frame.At(x, y)).(color.NRGBA)
		if got == want {
			if os.Getenv("WEBCRATE_SCREENSHOTS") != "" {
				ui.capture(name)
			}
			return
		}
	}
	ui.t.Fatalf("the icon in %q was %v, wanted %v", label, got, want)
}

func (ui macCreatorUI) checkPresetIcon(ink color.NRGBA, name string) {
	ui.t.Helper()
	width, height := ui.contentSize()
	bounds := ui.control("更换图标").Bounds
	frame, err := png.Decode(bytes.NewReader(ui.capture(name)))
	if err != nil {
		ui.t.Fatal(err)
	}
	sx, sy := float64(frame.Bounds().Dx())/width, float64(frame.Bounds().Dy())/height
	// Allow for resampling at the mark's edges and leave out the edit badge.
	area := image.Rect(int((bounds.X+4)*sx), int((bounds.Y+4)*sy),
		int((bounds.X+bounds.Width-4)*sx), int((bounds.Y+bounds.Height-16)*sy)).Intersect(frame.Bounds())
	near := func(a, b uint8) bool { return max(a, b)-min(a, b) <= 10 }
	colored, tile := 0, 0
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(frame.At(x, y)).(color.NRGBA)
			if near(pixel.R, ink.R) && near(pixel.G, ink.G) && near(pixel.B, ink.B) {
				colored++
			}
			if near(pixel.R, 246) && near(pixel.G, 247) && near(pixel.B, 251) {
				tile++
			}
		}
	}
	pixels := area.Dx() * area.Dy()
	if pixels == 0 || colored*40 < pixels || tile*5 < pixels {
		ui.t.Fatalf("the preset icon in %s had %d ink pixels and %d light tile pixels in %v", name, colored, tile, area)
	}
}

func exerciseMacCreatorPreview(t *testing.T, ui macCreatorUI, libraryPath, gate string) {
	t.Helper()
	ui.press("应用库")
	ui.control("正在读取应用…")
	ui.press("创建")
	const address = "https://www.xiaohongshu.com/explore?view=notes#latest"
	ui.fill("网址或应用名称", address)
	ui.control("使用网址 小红书")
	ui.control("清除搜索")
	ui.control("选择 DeepSeek")
	if os.Getenv("WEBCRATE_SCREENSHOTS") != "" {
		ui.capture("deskpad-url-preview.png")
	}
	ui.press("清除搜索")
	ui.expectValue("网址或应用名称", "")
	ui.expectFocus("网址或应用名称")
	ui.control("添加")
	ui.fill("网址或应用名称", address)
	ui.press("使用网址 小红书")
	ui.expectValue("应用网址", address)
	ui.expectValue("应用名称", "小红书")
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	ui.press("创建应用")
	ui.control("无法保存应用，请重试")
	ui.expectValue("应用网址", address)
	ui.expectValue("应用名称", "小红书")
	ui.press("取消")
	ui.press("应用库")
	ui.control("无法读取应用库")
	if os.Getenv("WEBCRATE_SCREENSHOTS") != "" {
		ui.capture("deskpad-library-retry.png")
	}
	replacement := libraryPath + ".fixture"
	if err := os.WriteFile(replacement, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, libraryPath); err != nil {
		t.Fatal(err)
	}
	ui.press("重试")
	ui.press("创建第一个应用")
	ui.expectFocus("网址或应用名称")
	ui.press("使用网址 小红书")
	ui.expectValue("应用网址", address)
	ui.press("创建应用")
	ui.control("打开 小红书")
	ui.fill("搜索应用库", "no matching fixture")
	ui.control("没有匹配的应用")
	ui.press("清除搜索")
	ui.expectValue("搜索应用库", "")
	ui.expectFocus("搜索应用库")
	ui.control("打开 小红书")
	apps, err := profile.NewStore(libraryPath).List()
	if err != nil || len(apps) != 1 || apps[0].URL != address || apps[0].Name != "小红书" {
		t.Fatalf("after retry the Creator saved %+v (%v)", apps, err)
	}
	t.Log("The window and creation form worked during a delayed library read. A nonregular profile file rejected creation with the draft intact, and retrying the repaired library made creation work.")
}
