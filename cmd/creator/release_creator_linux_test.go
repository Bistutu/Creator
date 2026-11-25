package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/settings"
	"golang.org/x/sys/unix"
)

func TestLinuxInstalledCreatorUsesManifestArtwork(t *testing.T) {
	installed, menu, _ := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	address, ink, rendered, manifestWhileWaiting := manifestWebsiteFixture(t)
	root := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WebCrate")
	launchPortableMenu(t, menu, installed, appName, "")
	manager := linuxFixtureWindow(t, appName)
	pid := linuxWindowPID(t, manager)
	pasteLinuxFixtureText(t, pid, "网址或应用名称", address)
	card := findLinuxControl(t, pid, "使用网址 "+fixtureManifestName)
	assertLinuxIconPaint(t, card.X-card.Width/2+10+16, card.Y, ink)
	assertManifestReadWhilePageIconWaited(t, manifestWhileWaiting)
	card.click(t)
	if got := findLinuxControl(t, pid, "应用网址").Text; got != address {
		t.Fatalf("the manifest card opened %q, want %q", got, address)
	}
	icon := findLinuxControl(t, pid, "更换图标")
	assertLinuxIconPaint(t, icon.X, icon.Y, ink)
	findLinuxControl(t, pid, "创建应用").click(t)
	findLinuxControl(t, pid, "打开 "+fixtureManifestName)
	apps, err := profile.NewStore(filepath.Join(root, profile.Filename)).List()
	if err != nil || len(apps) != 1 || apps[0].Name != fixtureManifestName || apps[0].URL != address {
		t.Fatalf("manifest creation saved the wrong profile: %+v (%v)", apps, err)
	}
	executable := filepath.Join(root, "Applications", apps[0].ID, fixtureManifestName)
	entry, iconFile := generatedMenu(t, apps[0], executable)
	assertLinuxGeneratedIcon(t, iconFile, ink)
	closeLinuxTestWindow(t, manager, pid)
	launchPortableMenu(t, entry, executable, fixtureManifestName, iconFile)
	created := linuxFixtureWindow(t, fixtureManifestName)
	select {
	case loaded := <-rendered:
		if loaded != address {
			t.Fatalf("the application opened %q, want %q", loaded, address)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("the generated application did not open the saved website")
	}
	closeLinuxTestWindow(t, created, linuxWindowPID(t, created))
	t.Log("The pasted URL used the manifest's name and alternate icon through creation and opened the full URL from its installed desktop entry.")
}

func TestLinuxInstalledCreatorReturnsPendingWebsiteArtwork(t *testing.T) {
	installed, menu, _ := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	address, ink, requested, finish := pendingWebsiteArtwork(t)
	const label = "使用网址 " + fixtureWebsiteName
	launchPortableMenu(t, menu, installed, appName, "")
	manager := linuxFixtureWindow(t, appName)
	pid := linuxWindowPID(t, manager)
	pasteLinuxFixtureText(t, pid, "网址或应用名称", address)
	findLinuxControl(t, pid, label).click(t)
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("the pasted website did not request its icon")
	}
	findLinuxControl(t, pid, "取消").click(t)
	card := findLinuxControl(t, pid, label)
	if !card.Focused {
		t.Fatal("canceling did not return focus to the website card")
	}
	finish()
	assertLinuxIconPaint(t, card.X-card.Width/2+10+16, card.Y, ink)
	linuxXDo(t, "key", "--clearmodifiers", "Return")
	if got := findLinuxControl(t, pid, "应用网址").Text; got != address {
		t.Fatalf("the reopened card used %q, want %q", got, address)
	}
	icon := findLinuxControl(t, pid, "更换图标")
	assertLinuxIconPaint(t, icon.X, icon.Y, ink)
	findLinuxControl(t, pid, "创建应用").click(t)
	findLinuxControl(t, pid, "打开 "+fixtureWebsiteName)
	root := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WebCrate")
	apps, err := profile.NewStore(filepath.Join(root, profile.Filename)).List()
	if err != nil || len(apps) != 1 || apps[0].Name != fixtureWebsiteName || apps[0].URL != address {
		t.Fatalf("the reopened form saved %+v (%v)", apps, err)
	}
	executable := filepath.Join(root, "Applications", apps[0].ID, fixtureWebsiteName)
	_, iconFile := generatedMenu(t, apps[0], executable)
	assertLinuxGeneratedIcon(t, iconFile, ink)
	closeLinuxTestWindow(t, manager, pid)
	t.Log("Cancel returned to the same pasted URL, and the artwork from the original request reached an installed desktop entry.")
}

func TestLinuxInstalledCreatorSharesPendingWebsiteArtwork(t *testing.T) {
	installed, menu, _ := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	first, ink, requested, finish := pendingWebsiteArtwork(t)
	second := profile.ArtworkAddress(first) + "#review"
	const design, review = "Design Desk", "Review Desk"
	launchPortableMenu(t, menu, installed, appName, "")
	manager := linuxFixtureWindow(t, appName)
	pid := linuxWindowPID(t, manager)
	openDraft := func(address string) {
		t.Helper()
		findLinuxControl(t, pid, "网址或应用名称").click(t)
		linuxXDo(t, "type", "--clearmodifiers", "--delay", "1", address)
		linuxXDo(t, "key", "--clearmodifiers", "Return")
		findLinuxControl(t, pid, "创建应用")
	}
	saveDraft := func(name string) {
		t.Helper()
		linuxXDo(t, "type", "--clearmodifiers", "--delay", "1", name)
		linuxXDo(t, "key", "--clearmodifiers", "Return")
		findLinuxControl(t, pid, "打开 "+name)
	}
	openDraft(first)
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("the first draft did not request the website icon")
	}
	saveDraft(design)
	findLinuxControl(t, pid, "创建").click(t)
	openDraft(second)
	saveDraft(review)
	root := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WebCrate")
	apps, err := profile.NewStore(filepath.Join(root, profile.Filename)).List()
	if err != nil || len(apps) != 2 || apps[0].ID == apps[1].ID {
		t.Fatalf("expected two independent profiles while the website icon was pending: %+v (%v)", apps, err)
	}
	finish()
	addresses := map[string]string{design: first, review: second}
	for _, app := range apps {
		if addresses[app.Name] != app.URL {
			t.Fatalf("the %q profile lost its own URL: %s", app.Name, app.URL)
		}
		row := findLinuxControl(t, pid, "打开 "+app.Name)
		if row.Description != app.URL || row.Height != 44 {
			t.Fatalf("the compact library row did not expose its complete address: %+v", row)
		}
		assertLinuxIconPaint(t, row.X-row.Width/2+9+16, row.Y, ink)
		executable := filepath.Join(root, "Applications", app.ID, app.Name)
		_, iconFile := generatedMenu(t, app, executable)
		assertLinuxGeneratedIcon(t, iconFile, ink)
	}
	closeLinuxTestWindow(t, manager, pid)
	t.Log("Both GTK library rows and installed desktop entries received the single pending icon, with independent saved URLs.")
}

func TestLinuxInstalledCreatorCustomIconUsesWebsiteName(t *testing.T) {
	installed, menu, state := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	address, _, rendered, manifestWhileWaiting := manifestWebsiteFixture(t)
	root := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WebCrate")
	launchPortableMenu(t, menu, installed, appName, "")
	manager := linuxFixtureWindow(t, appName)
	pid := linuxWindowPID(t, manager)
	findLinuxControl(t, pid, "添加").click(t)
	chooseLinuxCreatorIcon(t, pid, orientedIconFixture(t, state))
	pasteLinuxFixtureText(t, pid, "应用网址", address)
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(45 * time.Millisecond) {
		field := findLinuxControl(t, pid, "应用名称")
		if field.Text != "" || time.Now().After(deadline) {
			t.Fatalf("the custom-icon draft's name field was %+v", field)
		}
		if field.Placeholder == fixtureManifestName {
			break
		}
	}
	assertManifestReadWhilePageIconWaited(t, manifestWhileWaiting)
	findLinuxControl(t, pid, "创建应用").click(t)
	findLinuxControl(t, pid, "打开 "+fixtureManifestName)
	apps, err := profile.NewStore(filepath.Join(root, profile.Filename)).List()
	if err != nil || len(apps) != 1 || apps[0].Name != fixtureManifestName || apps[0].URL != address || !profile.ValidIconKey(apps[0].CustomIcon) {
		t.Fatalf("creation with the suggested name and chosen image saved %+v (%v)", apps, err)
	}
	executable := filepath.Join(root, "Applications", apps[0].ID, fixtureManifestName)
	entry, icon := generatedMenu(t, apps[0], executable)
	assertOrientedIcon(t, icons.ReadInstalledIcon(icon))
	closeLinuxTestWindow(t, manager, pid)
	launchPortableMenu(t, entry, executable, fixtureManifestName, icon)
	window := linuxFixtureWindow(t, fixtureManifestName)
	select {
	case loaded := <-rendered:
		if loaded != address {
			t.Fatalf("the generated application opened %q, want %q", loaded, address)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("the generated application did not load its website")
	}
	closeLinuxTestWindow(t, window, linuxWindowPID(t, window))
	t.Log("The GTK file picker, website name, installed desktop icon and full page address worked in the same creation flow.")
}

func chooseLinuxCreatorIcon(t *testing.T, pid, path string) {
	t.Helper()
	findLinuxControl(t, pid, "更换图标").click(t)
	picker := linuxFixtureWindow(t, "选择应用图标")
	linuxXDo(t, "windowfocus", "--sync", picker, "key", "--clearmodifiers", "ctrl+l")
	findLinuxControl(t, pid, "") // The native file chooser focuses its location entry.
	linuxXDo(t, "key", "--clearmodifiers", "ctrl+a")
	linuxXDo(t, "type", "--clearmodifiers", "--delay", "1", path)
	if got := findLinuxControl(t, pid, "").Text; got != path {
		t.Fatalf("file chooser contains %q, want %q", got, path)
	}
	findLinuxControl(t, pid, "使用图片").click(t)
	findLinuxControl(t, pid, "恢复网站图标")
}

func TestLinuxInstalledCreatorReopensFromMenu(t *testing.T) {
	installed, entry, _ := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	startTestWindowManager(t)
	launchPortableMenu(t, entry, installed, appName, "")
	window := linuxFixtureWindow(t, appName)
	pid := linuxWindowPID(t, window)
	const name, draft = "应用名称", "Unsaved workspace"
	findLinuxControl(t, pid, "选择 DeepSeek").click(t)
	findLinuxControl(t, pid, name).click(t)
	linuxXDo(t, "key", "--clearmodifiers", "ctrl+a")
	linuxXDo(t, "type", "--clearmodifiers", draft)
	if got := findLinuxControl(t, pid, name).Text; got != draft {
		t.Fatalf("typing the creation draft left %q", got)
	}

	linuxXDo(t, "windowminimize", "--sync", window)
	waitForXProperty(t, "_NET_WM_STATE_HIDDEN", true, "-id", window, "_NET_WM_STATE")
	reopened, err := os.FindProcess(startLinuxDesktopEntry(t, entry))
	if err != nil {
		t.Fatal(err)
	}
	timeout := time.AfterFunc(5*time.Second, func() { _ = reopened.Kill() })
	result, err := reopened.Wait()
	timeout.Stop()
	if err != nil || !result.Success() {
		t.Fatalf("the second launch did not hand off to Creator: %v (%v)", result, err)
	}
	waitForXProperty(t, "_NET_WM_STATE_HIDDEN", false, "-id", window, "_NET_WM_STATE")
	id, err := strconv.ParseUint(window, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	waitForXProperty(t, "window id # 0x"+strconv.FormatUint(id, 16), true, "-root", "_NET_ACTIVE_WINDOW")
	if got := linuxFixtureWindow(t, appName); got != window || linuxWindowPID(t, got) != pid {
		t.Fatalf("reopening Creator replaced the original window %s (PID %s) with %s", window, pid, got)
	}
	if got := findLinuxControl(t, pid, name).Text; got != draft {
		t.Fatalf("the reopened window's creation draft was %q, want %q", got, draft)
	}
	closeLinuxTestWindow(t, window, pid)
	t.Log("Launching the desktop entry again restored and focused the original window with its unsaved draft.")
}

func TestLinuxInstalledCreatorPreferences(t *testing.T) {
	installed, menuEntry, _ := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("LC_ALL", "C.UTF-8")
	open := func() (window, pid string) {
		t.Helper()
		launchPortableMenu(t, menuEntry, installed, appName, "")
		window = linuxFixtureWindow(t, appName)
		return window, linuxWindowPID(t, window)
	}
	manager, pid := open()
	click := func(label string) { findLinuxControl(t, pid, label).click(t) }
	click(creatorMenuLabel)
	click(settingsItem)
	panel := linuxFixtureWindow(t, settingsTitle)
	linuxXDo(t, "windowfocus", "--sync", panel, "key", "--clearmodifiers", "Escape")
	waitForLinuxWindowClosed(t, panel)
	click("选择 DeepSeek")
	findLinuxControl(t, pid, "应用名称")
	linuxXDo(t, "windowfocus", "--sync", manager, "key", "--clearmodifiers", "ctrl+comma")
	panel = linuxFixtureWindow(t, settingsTitle)
	if linuxWindowPID(t, panel) != pid {
		t.Fatal("Settings opened in a different process")
	}
	field := "默认宽度（像素）"
	if got := findLinuxControl(t, pid, field).Text; got != "1200" {
		t.Fatalf("Settings reported an unexpected first width of %q", got)
	}
	click("深色")
	click("应用库")
	click(field)
	linuxXDo(t, "key", "--clearmodifiers", "ctrl+a")
	linuxXDo(t, "type", "--clearmodifiers", "960")
	click("保存")
	waitForLinuxWindowClosed(t, panel)
	want := settings.Preferences{Appearance: settings.Dark, StartPage: settings.LibraryPage, Width: 960, Height: 820}
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WebCrate", "preferences.json")
	if got, err := settings.Load(path); err != nil || got != want {
		t.Fatalf("installed Creator saved %+v (%v), want %+v", got, err, want)
	}
	if got := findLinuxControl(t, pid, "应用名称").Text; got != "DeepSeek" {
		t.Fatalf("Settings left a draft named %q", got)
	}
	click("取消")
	linuxXDo(t, "windowfocus", "--sync", manager, "key", "--clearmodifiers", "F10")
	click(aboutTitle)
	about := linuxFixtureWindow(t, aboutTitle)
	version := creatorVersion()
	findLinuxControl(t, pid, "版本 "+version)
	click(licenseTitle)
	if got := findLinuxControl(t, pid, licenseBody).Text; got != creatorLicense {
		t.Fatalf("the installed About window reported a different license (%d bytes)", len(got))
	}
	click("返回")
	findLinuxControl(t, pid, "版本 "+version)
	click("复制版本号")
	findLinuxControl(t, pid, "已复制")
	linuxXDo(t, "windowfocus", "--sync", about, "key", "--clearmodifiers", "ctrl+w")
	waitForLinuxWindowClosed(t, about)
	linuxXDo(t, "windowfocus", "--sync", manager, "key", "--clearmodifiers", "ctrl+comma")
	panel = linuxFixtureWindow(t, settingsTitle)
	click(field)
	linuxXDo(t, "key", "--clearmodifiers", "ctrl+a", "ctrl+v")
	if got := findLinuxControl(t, pid, field).Text; got != version {
		t.Fatalf("the system clipboard contains %q, want %q", got, version)
	}
	linuxXDo(t, "windowfocus", "--sync", panel, "key", "--clearmodifiers", "Escape")
	waitForLinuxWindowClosed(t, panel)
	closeLinuxTestWindow(t, manager, pid)

	manager, pid = open()
	if !findLinuxControl(t, pid, "创建第一个应用").Focused {
		t.Fatal("starting in an empty library did not focus the first creation action")
	}
	linuxXDo(t, "windowfocus", "--sync", manager, "key", "--clearmodifiers", "Return")
	if !findLinuxControl(t, pid, "网址或应用名称").Focused {
		t.Fatal("starting the first application did not focus the URL field")
	}
	click("选择 DeepSeek")
	if got := findLinuxControl(t, pid, "应用网址").Text; got != "https://chat.deepseek.com" {
		t.Fatalf("the first recommendation opened %q after restoring the library", got)
	}
	click("窗口尺寸")
	if got := findLinuxControl(t, pid, "窗口宽度（像素）").Text; got != "960" {
		t.Fatalf("the first recommendation used width %q after restoring the library, want 960", got)
	}
	click("取消")
	linuxXDo(t, "windowfocus", "--sync", manager, "key", "--clearmodifiers", "ctrl+comma")
	panel = linuxFixtureWindow(t, settingsTitle)
	if got := findLinuxControl(t, pid, field).Text; got != "960" {
		t.Fatalf("after restart the default width is %q, want 960", got)
	}
	click("恢复默认")
	click("保存")
	waitForLinuxWindowClosed(t, panel)
	closeLinuxTestWindow(t, manager, pid)
	t.Log("Ctrl+comma opened the installed GTK Settings. About copied the version, Escape discarded edits, and the first recommendation used the saved default size after starting in the library.")
}

func TestLinuxInstalledCreatorRecoversNonregularProfiles(t *testing.T) {
	installed, menu, _ := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("LC_ALL", "C.UTF-8")
	root := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WebCrate")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "apps.json")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	launchPortableMenu(t, menu, installed, appName, "")
	manager := linuxFixtureWindow(t, appName)
	pid := linuxWindowPID(t, manager)
	control := func(label string) linuxControl {
		t.Helper()
		return findLinuxControl(t, pid, label)
	}
	click := func(label string) {
		t.Helper()
		control(label).click(t)
	}
	click("应用库")
	control("无法读取应用库")
	click("创建")
	click("选择 DeepSeek")
	click("创建应用")
	control("无法保存应用，请重试")
	const address = "https://chat.deepseek.com"
	if url, name := control("应用网址").Text, control("应用名称").Text; url != address || name != "DeepSeek" {
		t.Fatalf("the failed save left a draft with %q and %q", url, name)
	}
	replacement := path + ".fixture"
	if err := os.WriteFile(replacement, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	click("创建应用")
	control("打开 DeepSeek")
	click("重试")
	linuxAccessibleControl(t, pid, "无法读取应用库", "disappear")
	control("打开 DeepSeek")
	apps, err := profile.NewStore(path).List()
	if err != nil || len(apps) != 1 || apps[0].URL != address || apps[0].Name != "DeepSeek" {
		t.Fatalf("retrying the retained draft saved %+v (%v)", apps, err)
	}
	executable := filepath.Join(root, "Applications", apps[0].ID, apps[0].Name)
	entry, icon := generatedMenu(t, apps[0], executable)
	for _, path := range []string{entry, icon, executable} {
		checkPortableReleaseFile(t, path)
	}
	closeLinuxTestWindow(t, manager, pid)
	t.Log("The installed Creator reported the invalid profile file, then accepted the same draft and registered its desktop entry after recovery.")
}

func TestLinuxInstalledCreatorKeyboard(t *testing.T) {
	installed, menu, _ := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("LC_ALL", "C.UTF-8")
	launchPortableMenu(t, menu, installed, appName, "")
	manager := linuxFixtureWindow(t, appName)
	pid := linuxWindowPID(t, manager)
	linuxXDo(t, "windowsize", "--sync", manager, strconv.Itoa(minWindowWidth), strconv.Itoa(minWindowHeight))
	press := func(key string) {
		t.Helper()
		linuxXDo(t, "windowfocus", "--sync", manager, "key", "--clearmodifiers", key)
	}
	expectFocus := func(label string) {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
			if findLinuxControl(t, pid, label).Focused {
				return
			}
		}
		t.Fatalf("the installed keyboard workflow did not focus %q", label)
	}
	const input = "网址或应用名称"
	const address = "https://www.xiaohongshu.com/explore/topic?source=creator#notes"
	expectFocus(input)
	pasteLinuxFixtureText(t, pid, input, address)
	press("Return")
	if got := findLinuxControl(t, pid, "应用网址").Text; got != address {
		t.Fatalf("the first paste opened %q, want %q", got, address)
	}
	press("Escape")
	expectFocus(input)
	t.Log("The first window accepted a real clipboard paste and opened its creation form using only the keyboard.")
	findLinuxControl(t, pid, "AI").click(t)
	pasteLinuxFixtureText(t, pid, input, "写作")
	press("Return")
	expectFocus(input)
	press("Down")
	expectFocus("选择 ChatGPT")
	press("Right")
	expectFocus("选择 Gemini")
	press("Right")
	expectFocus("选择 Claude")
	press("Return")
	if got := findLinuxControl(t, pid, "应用网址").Text; got != "https://claude.ai" {
		t.Fatalf("the keyboard opened %q instead of Claude", got)
	}
	const dimensions, width = "窗口尺寸", "窗口宽度（像素）"
	size, create := findLinuxControl(t, pid, dimensions), findLinuxControl(t, pid, "创建应用")
	if size.X >= create.X || size.Y < create.Y-6 || size.Y > create.Y+6 {
		t.Fatalf("the installed editor misaligned its footer: size=%+v, create=%+v", size, create)
	}
	expectFocus("应用名称")
	press("Tab")
	expectFocus(dimensions)
	press("space")
	expectFocus(width)
	linuxXDo(t, "key", "--clearmodifiers", "ctrl+a")
	linuxXDo(t, "type", "--clearmodifiers", "914")
	press("Tab")
	expectFocus("窗口高度（像素）")
	press("Tab")
	expectFocus(dimensions)
	press("space")
	expectFocus(dimensions)
	press("space")
	expectFocus(width)
	if got := findLinuxControl(t, pid, width).Text; got != "914" {
		t.Fatalf("reopening dimensions restored width %q instead of the draft", got)
	}
	press("Escape")
	expectFocus("选择 Claude")
	press("Escape")
	expectFocus(input)
	if got := findLinuxControl(t, pid, input).Text; got != "写作" {
		t.Fatalf("the original search became %q", got)
	}

	findLinuxControl(t, pid, "全部").click(t)
	pasteLinuxFixtureText(t, pid, input, address)
	press("Down")
	expectFocus("使用网址 小红书")
	press("Return")
	if got := findLinuxControl(t, pid, "应用网址").Text; got != address {
		t.Fatalf("opening the focused card changed the full address to %q", got)
	}
	press("Escape")
	expectFocus("使用网址 小红书")
	press("Down")
	expectFocus("选择 GitHub")
	press("End")
	expectFocus("选择 Hacker News")
	press("Return")
	if got := findLinuxControl(t, pid, "应用网址").Text; got != "https://news.ycombinator.com" {
		t.Fatalf("the end of the scrollable grid opened %q", got)
	}
	press("Escape")
	expectFocus("选择 Hacker News")
	press("Escape")
	expectFocus(input)
	press("Tab")
	expectFocus("清除搜索")
	press("Return")
	expectFocus(input)
	findLinuxControl(t, pid, "添加")
	if got := findLinuxControl(t, pid, input).Text; got != "" {
		t.Fatalf("clearing the search left %q in the input", got)
	}
	findLinuxControl(t, pid, "选择 DeepSeek").click(t)
	findLinuxControl(t, pid, dimensions).click(t)
	const height = "窗口高度（像素）"
	pasteLinuxFixtureText(t, pid, width, "915")
	pasteLinuxFixtureText(t, pid, height, "479")
	findLinuxControl(t, pid, dimensions).click(t)
	findLinuxControl(t, pid, "创建应用").click(t)
	findLinuxControl(t, pid, profile.ErrWindowHeight.Error())
	expectFocus(height)
	if got := findLinuxControl(t, pid, height).Text; got != "479" {
		t.Fatalf("the revealed height was %q, want the original invalid value", got)
	}
	pasteLinuxFixtureText(t, pid, height, "704")
	press("Return")
	expectFocus("打开 DeepSeek")
	root := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WebCrate")
	apps, err := profile.NewStore(filepath.Join(root, profile.Filename)).List()
	if err != nil || len(apps) != 1 || apps[0].Width != 915 || apps[0].Height != 704 {
		t.Fatalf("correcting the height and pressing Return saved %+v (%v)", apps, err)
	}
	closeLinuxTestWindow(t, manager, pid)
	t.Log("The installed workflow returned to the first row after clearing the URL, then revealed an invalid dimension and saved its correction with Return.")
}

func TestLinuxInstalledCreatorLibrarySearch(t *testing.T) {
	installed, menu, _ := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("LC_ALL", "C.UTF-8")
	root := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WebCrate")
	writeLibraryFixture(t, filepath.Join(root, profile.Filename), 120, "")
	launchPortableMenu(t, menu, installed, appName, "")
	manager := linuxFixtureWindow(t, appName)
	pid := linuxWindowPID(t, manager)
	linuxXDo(t, "windowsize", "--sync", manager, strconv.Itoa(minWindowWidth), strconv.Itoa(minWindowHeight))
	findLinuxControl(t, pid, "应用库").click(t)
	const search, latest, match = "搜索应用库", "打开 Workspace 119", "打开 Workspace 099"
	top := findLinuxControl(t, pid, latest)
	scrollAway := func(label string) {
		t.Helper()
		row := findLinuxControl(t, pid, label)
		linuxXDo(t, "mousemove", strconv.Itoa(row.X), strconv.Itoa(row.Y), "click", "--repeat", "16", "5")
		linuxAccessibleControl(t, pid, label, "disappear")
	}
	expectTop := func(label string) {
		t.Helper()
		row := findLinuxControl(t, pid, label)
		if row.Y != top.Y || !findLinuxControl(t, pid, search).Focused {
			t.Fatalf("the library left %q at y=%d, want y=%d with search focus", label, row.Y, top.Y)
		}
	}
	scrollAway(latest)
	pasteLinuxFixtureText(t, pid, search, "Workspace 0")
	expectTop(match)
	keys := func(key, label string) {
		t.Helper()
		linuxXDo(t, "key", "--clearmodifiers", key)
		if !findLinuxControl(t, pid, label).Focused {
			t.Fatalf("%s did not focus %q", key, label)
		}
	}
	keys("Return", search)
	for i := 99; i >= 90; i-- {
		keys("Down", fmt.Sprintf("打开 Workspace %03d", i))
	}
	keys("Up", "打开 Workspace 091")
	keys("Escape", search)
	if value := findLinuxControl(t, pid, search).Text; value != "Workspace 0" {
		t.Fatalf("returning to the input changed the filter to %q", value)
	}
	keys("Down", match)
	keys("Up", search)
	expectTop(match)
	scrollAway(match)
	findLinuxControl(t, pid, "清除搜索").click(t)
	expectTop(latest)
	closeLinuxTestWindow(t, manager, pid)
}

func TestLinuxInstalledCreatorUI(t *testing.T) {
	installed, managerEntry, state := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("GTK_USE_PORTAL", "0")
	t.Setenv("LC_ALL", "C.UTF-8")

	root := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WebCrate")
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	t.Cleanup(func() {
		if t.Failed() {
			apps, err := store.List()
			t.Logf("saved profiles at failure: %+v (%v)", apps, err)
		}
	})
	site, rendered := installedWebsiteFixture(t)
	site += "#today"
	iconPath, iconColor := orientedIconFixture(t, state), orientedFixtureBlue
	const name, renamed = "Creator Preview & 50%", "Creator Review"
	const originalWidth, originalHeight = 680, 490
	const resizedWidth, resizedHeight = 730, 520
	const updatedWidth, updatedHeight = 800, 570
	var manager, pid string
	startManager := func() {
		t.Helper()
		launchPortableMenu(t, managerEntry, installed, appName, "")
		manager = linuxFixtureWindow(t, appName)
		pid = linuxWindowPID(t, manager)
	}
	control := func(label string) linuxControl {
		t.Helper()
		return findLinuxControl(t, pid, label)
	}
	click := func(label string) {
		t.Helper()
		control(label).click(t)
	}
	fill := func(label, value string) {
		t.Helper()
		click(label)
		linuxXDo(t, "key", "--clearmodifiers", "ctrl+a")
		linuxXDo(t, "type", "--clearmodifiers", "--delay", "1", value)
		if got := control(label).Text; got != value {
			t.Fatalf("%q contains %q, want %q", label, got, value)
		}
	}
	beginCreate := func(address, title string) {
		t.Helper()
		fill("网址或应用名称", address)
		click("使用网址 " + profile.Host(address))
		if got := control("应用网址").Text; got != address {
			t.Fatalf("the editor received %q, want %q", got, address)
		}
		fill("应用名称", title)
	}
	setSize := func(width, height int) {
		t.Helper()
		click("窗口尺寸")
		fill("窗口宽度（像素）", strconv.Itoa(width))
		fill("窗口高度（像素）", strconv.Itoa(height))
	}
	savedApp := func(title, address string, width, height int) profile.App {
		t.Helper()
		control("打开 " + title)
		apps, err := store.List()
		if err != nil || len(apps) != 1 {
			t.Fatalf("the installed Creator saved %d applications: %v", len(apps), err)
		}
		app := apps[0]
		if app.URL != address || app.Name != title || app.Width != width || app.Height != height || app.CustomIcon == "" {
			t.Fatalf("saved profile does not match the submitted form: %+v", app)
		}
		return app
	}

	startManager()
	linuxXDo(t, "windowsize", "--sync", manager, strconv.Itoa(minWindowWidth), strconv.Itoa(minWindowHeight))
	for _, preset := range []struct{ category, name, url, query string }{
		{"AI", "DeepSeek", "https://chat.deepseek.com", "推理 DeepSeek"},
		{"社交", "小红书", "https://www.xiaohongshu.com/explore", "攻略 小红书"},
	} {
		click(preset.category)
		pasteLinuxFixtureText(t, pid, "网址或应用名称", preset.query)
		control("选择 " + preset.name)
		linuxXDo(t, "windowfocus", "--sync", manager, "key", "--clearmodifiers", "Return")
		if title, address := control("应用名称").Text, control("应用网址").Text; title != preset.name || address != preset.url {
			t.Fatalf("preset %q filled %q and %q", preset.name, title, address)
		}
		click("取消")
		if value := control("网址或应用名称").Text; value != preset.query {
			t.Fatalf("canceling a combined search left the value %q", value)
		}
	}
	beginCreate(site, name)
	chooseLinuxCreatorIcon(t, pid, iconPath)
	setSize(originalWidth, originalHeight)
	create := control("创建应用")
	createdAt := time.Now()
	create.click(t)
	app := savedApp(name, site, originalWidth, originalHeight)
	if !control("打开 " + name).Focused {
		t.Fatal("the installed Creator did not focus the new app")
	}
	appDir := filepath.Join(root, "Applications", app.ID)
	executable := filepath.Join(appDir, name)
	entry, icon := generatedMenu(t, app, executable)
	createdIcon := assertLinuxGeneratedIcon(t, icon, iconColor)
	picture, err := png.Decode(bytes.NewReader(createdIcon))
	if err != nil {
		t.Fatal(err)
	}
	assertOrientedIcon(t, picture)
	t.Logf("native create saved the app and its selected icon in %s", time.Since(createdAt).Round(time.Millisecond))
	storedPNG, err := os.ReadFile(filepath.Join(root, "CustomIcons", app.CustomIcon+".png"))
	if err != nil || !bytes.Equal(storedPNG, createdIcon) {
		t.Fatalf("the system menu icon did not use the stored PNG bytes: %v", err)
	}
	click(name + " 的选项")
	click("复制网址")
	control("已复制网址")
	click("搜索应用库")
	linuxXDo(t, "key", "--clearmodifiers", "ctrl+v")
	if got := control("搜索应用库").Text; got != site {
		t.Fatalf("copying the library address put %q on the system clipboard, want %q", got, site)
	}
	linuxXDo(t, "key", "--clearmodifiers", "ctrl+a", "BackSpace")
	t.Log("The library menu copied the address with its port, path, query and fragment through the GTK clipboard.")

	click("创建")
	beginCreate(paddedFixturePort(t, site), name)
	click("创建应用")
	if duplicate := savedApp(name, site, originalWidth, originalHeight); duplicate != app {
		t.Fatalf("creating the same site and name changed the original application: %+v", duplicate)
	}
	if !control("打开 " + name).Focused {
		t.Fatal("a repeated creation did not focus the installed app")
	}
	entryBytes, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	pasteLinuxFixtureText(t, pid, "搜索应用库", "#today Preview")
	linuxXDo(t, "windowfocus", "--sync", manager, "key", "--clearmodifiers", "Return")
	window := linuxFixtureWindow(t, name)
	appPID := linuxWindowPID(t, window)
	assertLinuxRunner(t, appPID, executable)
	first := checkLinuxInstalledReport(t, rendered, "new")
	if first.URL != site || first.Width != originalWidth || first.Height < 350 || first.Height > originalHeight {
		t.Fatalf("first website window does not match the installed size: %+v", first)
	}
	if repaired, err := os.ReadFile(entry); err != nil || !bytes.Equal(repaired, entryBytes) {
		t.Fatalf("opening from the library did not restore the original system entry: %v", err)
	}
	dirBeforeResize, err := os.Stat(appDir)
	if err != nil {
		t.Fatal(err)
	}
	linuxXDo(t, "windowraise", manager, "windowfocus", "--sync", manager)
	click("清除搜索")
	click(name + " 的选项")
	click("编辑应用")
	updatedHome := profile.ArtworkAddress(site) + "#review"
	fill("应用网址", updatedHome)
	setSize(resizedWidth, resizedHeight)
	startedResize := time.Now()
	click("保存")
	control("首页已保存，新尺寸下次启动时生效")
	resized := savedApp(name, updatedHome, resizedWidth, resizedHeight)
	if resized.ID != app.ID || resized.WindowRevision != app.WindowRevision+1 {
		t.Fatalf("editing the running app changed its identity or did not advance its revision: %+v", resized)
	}
	dirAfterResize, err := os.Stat(appDir)
	if err != nil || !os.SameFile(dirBeforeResize, dirAfterResize) {
		t.Fatalf("saving in the installed Creator replaced the application directory: %v", err)
	}
	if linuxWindowPID(t, window) != appPID {
		t.Fatal("the website window changed processes during the resize")
	}
	assertLinuxRunner(t, appPID, executable)
	t.Logf("the installed Creator saved the homepage and dimensions in %s while the original website window stayed open", time.Since(startedResize).Round(time.Millisecond))
	app = resized
	linuxXDo(t, "windowraise", window, "windowfocus", "--sync", window)
	closeLinuxTestWindow(t, window, appPID)
	linuxXDo(t, "windowraise", manager, "windowfocus", "--sync", manager)
	t.Log("Return opened the only app matching the combined name and URL search at its complete saved URL and window size")

	if err := os.Truncate(icon, 9); err != nil {
		t.Fatal(err)
	}
	click(name + " 的选项")
	click("编辑应用")
	click("保存")
	if repaired := savedApp(name, updatedHome, resizedWidth, resizedHeight); repaired != app {
		t.Fatalf("repairing the damaged icon changed the saved settings: %+v", repaired)
	}
	if repaired := assertLinuxGeneratedIcon(t, icon, iconColor); !bytes.Equal(repaired, createdIcon) {
		t.Fatal("the settings save did not restore the selected icon")
	}
	t.Log("saving unchanged settings in the installed Creator restored the original system icon")

	click(name + " 的选项")
	click("编辑应用")
	fill("应用名称", renamed)
	setSize(updatedWidth, updatedHeight)
	click("保存")
	updated := savedApp(renamed, updatedHome, updatedWidth, updatedHeight)
	if updated.ID != app.ID || updated.CustomIcon != app.CustomIcon || updated.WindowRevision != app.WindowRevision+1 {
		t.Fatalf("editing changed the installed identity, icon or size revision: %+v", updated)
	}
	assertRemovedPath(t, executable)
	newExecutable := filepath.Join(appDir, renamed)
	if updatedIcon := assertLinuxGeneratedIcon(t, icon, iconColor); !bytes.Equal(updatedIcon, createdIcon) {
		t.Fatal("editing the name and size changed the installed icon")
	}

	closeLinuxTestWindow(t, manager, pid)
	launchPortableMenu(t, entry, newExecutable, renamed, icon)
	window = linuxFixtureWindow(t, renamed)
	second := checkLinuxInstalledReport(t, rendered, "remembered")
	if second.URL != updatedHome || second.Width != updatedWidth || second.Height-first.Height != updatedHeight-originalHeight {
		t.Fatalf("reopened website window did not follow the new size: %+v → %+v", first, second)
	}
	closeLinuxTestWindow(t, window, linuxWindowPID(t, window))
	t.Log("the renamed app opened from the system menu while Creator was closed and kept its icon and session")

	marker := filepath.Join(appDir, ".runtime")
	digest, err := os.ReadFile(filepath.Join(filepath.Dir(installed), "WebCrateRunner.sha256"))
	if err != nil || len(digest) != 64 {
		t.Fatalf("the installed Creator is missing its runner checksum: %v", err)
	}
	if got, err := fileutil.ReadRegular(marker, 64); err != nil || !bytes.Equal(got, digest) {
		t.Fatalf("the generated runner marker differs from the installed checksum: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(marker, 0o600); err != nil {
		t.Fatal(err)
	}
	verifyRecovery := stageInterruptedPackages(t, filepath.Dir(appDir), updated.ID)
	startManager()
	click("应用库")
	savedApp(renamed, updatedHome, updatedWidth, updatedHeight)
	verifyRecovery()
	for deadline := time.Now().Add(4 * time.Second); ; time.Sleep(25 * time.Millisecond) {
		if got, err := fileutil.ReadRegular(marker, 64); err == nil && bytes.Equal(got, digest) {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("startup did not restore the runtime marker: %q (%v)", got, err)
		}
	}
	t.Log("startup restored the original application and reaped the interrupted build and completed removal")
	t.Log("startup replaced the invalid version marker with the installed runtime checksum")
	click(renamed + " 的选项")
	click("移除应用")
	click("取消")
	savedApp(renamed, updatedHome, updatedWidth, updatedHeight)
	entryBefore, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	executableBefore, err := os.Stat(newExecutable)
	if err != nil {
		t.Fatal(err)
	}
	menuDirectory := filepath.Dir(entry)
	menuInfo, err := os.Stat(menuDirectory)
	if err != nil {
		t.Fatal(err)
	}
	// The isolated system entry can still be read, but cannot be unlinked.
	if err := os.Chmod(menuDirectory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(menuDirectory, menuInfo.Mode().Perm()) })
	click(renamed + " 的选项")
	click("移除应用")
	click("移除")
	control("无法移除「" + renamed + "」，请重试")
	if app := savedApp(renamed, updatedHome, updatedWidth, updatedHeight); app != updated {
		t.Fatalf("a failed removal changed the committed profile: %+v", app)
	}
	if current, err := os.ReadFile(entry); err != nil || !bytes.Equal(current, entryBefore) {
		t.Fatalf("the failed removal changed the system entry: %v", err)
	}
	if current, err := os.Stat(newExecutable); err != nil || !os.SameFile(current, executableBefore) {
		t.Fatalf("the failed removal changed the installed executable: %v", err)
	}
	launchPortableMenu(t, entry, newExecutable, renamed, icon)
	window = linuxFixtureWindow(t, renamed)
	checkLinuxInstalledReport(t, rendered, "remembered")
	closeLinuxTestWindow(t, window, linuxWindowPID(t, window))
	if err := os.Chmod(menuDirectory, menuInfo.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	t.Log("a read-only desktop entry directory kept the app installed and launchable with its remembered session until a retry was possible")

	click(renamed + " 的选项")
	click("移除应用")
	click("移除")
	control("创建第一个应用")
	assertRemovedPath(t, entry)
	assertRemovedPath(t, appDir)
	if apps, err := store.List(); err != nil || len(apps) != 0 {
		t.Fatalf("the removed app still appears in the saved library: %v (%v)", apps, err)
	}
	t.Log("removing the app from the reopened Creator removed the standalone files and system menu entry")

	address, expected, showBody, renderedIconSite, stopSite := streamingWebsiteIcon(t, true)
	address += "?view=board#today"
	const automatic = fixtureWebsiteName
	const label = "使用网址 " + automatic
	click("创建第一个应用")
	started := time.Now()
	pasteLinuxFixtureText(t, pid, "网址或应用名称", address)
	card := control(label)
	assertLinuxIconPaint(t, card.X-card.Width/2+10+16, card.Y, expected)
	t.Logf("the installed URL card painted its website icon %s after pasting, while the body was still held", time.Since(started).Round(time.Millisecond))
	card.click(t)
	if got := control("应用网址").Text; got != address {
		t.Fatalf("the pasted card opened %q, want %q", got, address)
	}
	iconButton := control("更换图标")
	assertLinuxIconPaint(t, iconButton.X, iconButton.Y, expected)
	click("创建应用")
	control("打开 " + automatic)
	apps, err := store.List()
	if err != nil || len(apps) != 1 || apps[0].Name != automatic || apps[0].URL != address || apps[0].CustomIcon != "" {
		t.Fatalf("automatic icon creation saved the wrong profile: %+v (%v)", apps, err)
	}
	executable = filepath.Join(root, "Applications", apps[0].ID, automatic)
	entry, icon = generatedMenu(t, apps[0], executable)
	for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
		if _, got, err := linuxGeneratedIcon(icon); err == nil && got == expected {
			break
		}
	}
	assertLinuxGeneratedIcon(t, icon, expected)
	t.Log("the site's declared name and alternate artwork were used in the installed application before it released the webpage body")
	showBody()
	closeLinuxTestWindow(t, manager, pid)
	launchPortableMenu(t, entry, executable, automatic, icon)
	window = linuxFixtureWindow(t, automatic)
	select {
	case <-renderedIconSite:
	case <-time.After(10 * time.Second):
		t.Fatal("the app with an automatic icon did not render its website from the system entry")
	}
	closeLinuxTestWindow(t, window, linuxWindowPID(t, window))
	stopSite()

	startManager()
	const offlineName = "Offline website artwork"
	offlineAddress := profile.ArtworkAddress(address) + "#offline"
	click("创建")
	fill("网址或应用名称", offlineAddress)
	card = control(label)
	assertLinuxIconPaint(t, card.X-card.Width/2+10+16, card.Y, expected)
	card.click(t)
	if got := control("应用网址").Text; got != offlineAddress {
		t.Fatalf("the cached card opened %q, want %q", got, offlineAddress)
	}
	iconButton = control("更换图标")
	assertLinuxIconPaint(t, iconButton.X, iconButton.Y, expected)
	fill("应用名称", offlineName)
	click("创建应用")
	control("打开 " + offlineName)
	offlineApps, err := store.List()
	if err != nil || len(offlineApps) != 2 {
		t.Fatalf("the offline creation did not add a profile: %+v (%v)", offlineApps, err)
	}
	offlineIndex := slices.IndexFunc(offlineApps, func(app profile.App) bool { return app.Name == offlineName })
	if offlineIndex < 0 || offlineApps[offlineIndex].URL != offlineAddress || offlineApps[offlineIndex].CustomIcon != "" {
		t.Fatalf("the offline creation saved the wrong profile: %+v", offlineApps)
	}
	offline := offlineApps[offlineIndex]
	offlineDir := filepath.Join(root, "Applications", offline.ID)
	offlineEntry, offlineIcon := generatedMenu(t, offline, filepath.Join(offlineDir, offlineName))
	assertLinuxGeneratedIcon(t, offlineIcon, expected)
	click(offlineName + " 的选项")
	click("移除应用")
	click("移除")
	control("打开 " + automatic)
	assertRemovedPath(t, offlineDir)
	assertRemovedPath(t, offlineEntry)
	closeLinuxTestWindow(t, manager, pid)
	t.Log("with the site offline, Creator painted the cached icon on the URL card and in the editor, then installed it as the system menu icon")

	// Model an older install that saved the cache before it could apply it.
	backend, err := bundle.NewBackend(filepath.Dir(installed), filepath.Join(root, "Runtimes"))
	if err != nil {
		t.Fatal(err)
	}
	bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), backend)
	automaticApp := apps[0]
	placeholder := icons.Artwork{Image: icons.Fallback("Earlier name", automaticApp.Color)}
	if _, err := bundles.Install(t.Context(), automaticApp, automaticApp, placeholder, nil); err != nil {
		t.Fatal(err)
	}
	if _, got, err := linuxGeneratedIcon(icon); err != nil || got == expected {
		t.Fatalf("the older fixture did not keep its generated icon: %v, %v", got, err)
	}
	startManager()
	click("应用库")
	row := control("打开 " + automatic)
	assertLinuxIconPaint(t, row.X-row.Width/2+9+16, row.Y, expected)
	assertLinuxGeneratedIcon(t, icon, expected)
	if installed := bundles.ReadInstalledArtwork(automaticApp)(); installed.Kind != icons.WebsiteArtwork {
		t.Fatalf("the applied icon did not record its source: %q", installed.Kind)
	}
	closeLinuxTestWindow(t, manager, pid)
	t.Log("the restarted Creator applied its cached website icon to a legacy placeholder without changing the app entry")

	if err := os.RemoveAll(filepath.Join(root, "Icons")); err != nil {
		t.Fatal(err)
	}
	startManager()
	linuxXDo(t, "windowraise", manager, "windowfocus", "--sync", manager)
	click("应用库")
	row = control("打开 " + automatic)
	assertLinuxIconPaint(t, row.X-row.Width/2+9+16, row.Y, expected)
	before := assertLinuxGeneratedIcon(t, icon, expected)
	dirBeforeEdit, err := os.Stat(filepath.Dir(executable))
	if err != nil {
		t.Fatal(err)
	}
	libraryQuery := strings.ToLower(automatic)
	fill("搜索应用库", libraryQuery)
	click(automatic + " 的选项")
	click("编辑应用")
	homepage := profile.ArtworkAddress(address) + "#review"
	fill("应用网址", homepage)
	iconButton = control("更换图标")
	assertLinuxIconPaint(t, iconButton.X, iconButton.Y, expected)
	setSize(updatedWidth, updatedHeight)
	click("保存")
	if !control("打开 "+automatic).Focused || control("搜索应用库").Text != libraryQuery {
		t.Fatal("saving the homepage lost the library filter or the edited row's focus")
	}
	click("清除搜索")
	if search := control("搜索应用库"); search.Text != "" || !search.Focused {
		t.Fatalf("clearing the installed library search left %+v", search)
	}
	if saved, err := store.Find(automaticApp.ID); err != nil || saved.URL != homepage || saved.Width != updatedWidth || saved.Height != updatedHeight {
		t.Fatalf("offline editing saved %+v (%v)", saved, err)
	}
	assertPackagedHome(t, filepath.Join(filepath.Dir(executable), bundle.ConfigName), homepage)
	if current, err := os.Stat(filepath.Dir(executable)); err != nil || !os.SameFile(dirBeforeEdit, current) {
		t.Fatalf("changing only the home fragment and dimensions replaced the installed application: %v", err)
	}
	if after := assertLinuxGeneratedIcon(t, icon, expected); !bytes.Equal(before, after) {
		t.Fatal("offline editing changed the PNG used by the system menu")
	}
	closeLinuxTestWindow(t, manager, pid)
	t.Log("after restart and cache removal, Creator kept the icon bytes, library filter and focus while saving a new home fragment and dimensions")
}

func pasteLinuxFixtureText(t *testing.T, pid, label, value string) {
	t.Helper()
	// GTK serves clipboard requests until the isolated Xvfb paste has completed.
	const owner = `import ctypes as c
import sys
gtk = c.CDLL('libgtk-3.so.0')
gdk = c.CDLL('libgdk-3.so.0')
gtk.gtk_init_check.argtypes = [c.c_void_p, c.c_void_p]
gdk.gdk_atom_intern_static_string.argtypes = [c.c_char_p]
gdk.gdk_atom_intern_static_string.restype = c.c_void_p
gtk.gtk_clipboard_get.argtypes = [c.c_void_p]
gtk.gtk_clipboard_get.restype = c.c_void_p
gtk.gtk_clipboard_set_text.argtypes = [c.c_void_p, c.c_char_p, c.c_int]
if not gtk.gtk_init_check(None, None):
    raise SystemExit('could not open the test clipboard')
clipboard = gtk.gtk_clipboard_get(gdk.gdk_atom_intern_static_string(b'CLIPBOARD'))
gtk.gtk_clipboard_set_text(clipboard, sys.argv[1].encode(), -1)
print('ready', flush=True)
gtk.gtk_main()`
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/python3", "-c", owner, value)
	command.Stderr = os.Stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = command.Wait()
	}()
	if status, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || status != "ready\n" {
		t.Fatalf("the Linux clipboard fixture did not start: %q (%v)", status, err)
	}
	if field := findLinuxControl(t, pid, label); !field.Focused {
		field.click(t)
	}
	linuxXDo(t, "key", "--clearmodifiers", "ctrl+a", "ctrl+v")
	if got := findLinuxControl(t, pid, label).Text; got != value {
		t.Fatalf("the clipboard produced %q in %s, want %q", got, label, value)
	}
}

func assertLinuxGeneratedIcon(t *testing.T, path string, want color.NRGBA) []byte {
	t.Helper()
	data, got, err := linuxGeneratedIcon(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("installed icon center = %v, want %v", got, want)
	}
	return data
}

func linuxGeneratedIcon(path string) ([]byte, color.NRGBA, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, color.NRGBA{}, err
	}
	picture, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, color.NRGBA{}, err
	}
	center := picture.Bounds().Min.Add(picture.Bounds().Size().Div(2))
	return data, color.NRGBAModel.Convert(picture.At(center.X, center.Y)).(color.NRGBA), nil
}

func assertLinuxRunner(t *testing.T, pid, executable string) {
	t.Helper()
	link := filepath.Join("/proc", pid, "exe")
	if actual, err := os.Readlink(link); err != nil || actual != executable {
		t.Fatalf("library started %q, want installed app %q (%v)", actual, executable, err)
	}
	processID, err := strconv.Atoi(pid)
	if err != nil || processID <= 1 {
		t.Fatalf("invalid installed application pid %q: %v", pid, err)
	}
	t.Cleanup(func() {
		// Creator started this child. Only clean it up if the fixture's own
		// executable is still running with the observed PID.
		if actual, _ := os.Readlink(link); actual == executable || actual == executable+" (deleted)" {
			if app, err := os.FindProcess(processID); err == nil {
				_ = app.Kill()
			}
		}
	})
}

func checkLinuxInstalledReport(t *testing.T, reports <-chan installedWebsiteReport, want string) installedWebsiteReport {
	t.Helper()
	select {
	case report := <-reports:
		if report.State != want {
			t.Fatalf("standalone app reports %+v, want %q site state", report, want)
		}
		return report
	case <-time.After(15 * time.Second):
		t.Fatal("the standalone app did not load its saved URL")
		return installedWebsiteReport{}
	}
}
