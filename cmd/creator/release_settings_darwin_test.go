package main

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/settings"
	"golang.org/x/sys/unix"
)

func TestMacCreatorReopensFromSystemOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	home := t.TempDir()
	registered := filepath.Join(macRegisteredAppRoot(t, home), filepath.Base(manager))
	macCommand(t, "/usr/bin/ditto", manager, registered)
	runtimeDir := "XDG_RUNTIME_DIR=" + macTestSocketDir(t, "creator-reopen-")
	display.prepareCreator(t, home, windowWidth, windowHeight)
	withAutomatedMacCreator(t, registered, home, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		const field, draft = "应用名称", "Unsaved workspace"
		ui.press("选择 DeepSeek")
		ui.fill(field, draft)
		ui.request("menu", map[string]any{"path": []string{"Window", "Minimize"}}, nil)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		for {
			out, err := exec.CommandContext(ctx, display.probe, "verify", display.name, strconv.Itoa(pid)).CombinedOutput()
			var failure *exec.ExitError
			if errors.As(err, &failure) && failure.ExitCode() == 2 {
				break
			}
			if err != nil {
				t.Fatalf("the native window did not minimize on %s: %v (%s)", display.name, err, out)
			}
			time.Sleep(40 * time.Millisecond)
		}

		macCommand(t, "/usr/bin/open", "--env", "HOME="+home, "--env", "CFFIXED_USER_HOME="+home, "--env", runtimeDir, registered)
		display.verifyWindow(t, pid)
		if reopened := ui.panel(appName); reopened.window != ui.window {
			t.Fatal("Launch Services replaced the original Creator window")
		}
		ui.expectValue(field, draft)
		ui.capture("deskpad-creator-reopened-draft.png")
		t.Logf("Launch Services restored the same Creator process and its unsaved draft on %s", display.name)
	}, runtimeDir)
}

func TestMacCreatorPreferencesAndAboutOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	path := filepath.Join(state, "Library", "Application Support", "WebCrate", "preferences.json")
	light := settings.Default()
	light.Appearance = settings.Light
	if err := settings.Save(path, light); err != nil {
		t.Fatal(err)
	}
	version := macCommand(t, "/usr/bin/plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-",
		filepath.Join(manager, "Contents", "Info.plist"))
	want := settings.Preferences{Appearance: settings.Dark, StartPage: settings.LibraryPage, Width: 960, Height: 680}
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		const width = "默认宽度（像素）"
		ui.focus(creatorMenuLabel)
		ui.key("Space")
		ui.control(settingsItem)
		ui.capture("deskpad-creator-menu.png")
		ui.key("Enter")
		prefs := ui.panel(settingsTitle)
		prefs.expectValue(width, "1200")
		prefs.key("Escape")
		prefs.waitClosed()
		ui.expectFocus(creatorMenuLabel)
		ui.press("选择 DeepSeek")
		ui.fill("应用名称", "DeskPad review")
		ui.focus("应用名称")
		disclosure := ui.control("窗口尺寸")
		cancel, create := ui.control("取消"), ui.control("创建应用")
		cancelCenter := cancel.Bounds.Y + cancel.Bounds.Height/2
		createCenter := create.Bounds.Y + create.Bounds.Height/2
		sizeCenter := disclosure.Bounds.Y + disclosure.Bounds.Height/2
		if disclosure.Bounds.X+disclosure.Bounds.Width >= cancel.Bounds.X ||
			math.Abs(cancelCenter-createCenter) > 1 || math.Abs(sizeCenter-cancelCenter) > 1 {
			t.Fatalf("compact footer alignment: size=%+v, cancel=%+v, create=%+v", disclosure.Bounds, cancel.Bounds, create.Bounds)
		}
		ui.capture("deskpad-compact-editor.png")
		ui.key("Tab")
		ui.expectFocus("窗口尺寸")
		ui.key("Space")
		ui.expectFocus("窗口宽度（像素）")
		ui.fill("窗口宽度（像素）", "914")
		ui.key("Tab")
		ui.expectFocus("窗口高度（像素）")
		ui.key("Tab")
		ui.expectFocus("窗口尺寸")
		ui.key("Space")
		ui.expectFocus("窗口尺寸")
		ui.press("窗口尺寸")
		ui.expectValue("窗口宽度（像素）", "914")
		ui.focus("应用名称")
		ui.waitWindowFocus()
		if err := unix.Kill(pid, unix.SIGUSR1); err != nil {
			t.Fatal(err)
		}
		prefs = ui.panel(settingsTitle)
		prefs.waitWindowFocus()
		display.verifyWindow(t, pid)
		prefs.fill(width, "920")
		ui.menu(aboutTitle)
		about := ui.panel(aboutTitle)
		about.waitWindowFocus()
		about.control("版本 " + version)
		if err := unix.Kill(pid, unix.SIGUSR1); err != nil {
			t.Fatal(err)
		}
		prefs.waitWindowFocus()
		if reopened := ui.panel(settingsTitle); reopened.window != prefs.window {
			t.Fatal("opening Settings with Cmd+comma from About replaced the editor")
		}
		if reopened := ui.panel(aboutTitle); reopened.window != about.window {
			t.Fatal("the Settings command changed the About window")
		}
		prefs.expectValue(width, "920")
		prefs.focus(width)
		prefs.key("Escape")
		prefs.waitClosed()
		about.key("Escape")
		about.waitClosed()
		if got, err := settings.Load(path); err != nil || got != light {
			t.Fatalf("Escape changed preferences to %+v (%v), want %+v", got, err, light)
		}
		ui.expectValue("应用名称", "DeskPad review")
		ui.menu(settingsItem)
		prefs = ui.panel(settingsTitle)
		prefs.expectValue(width, "1200")
		prefs.checkAppearance("deskpad-settings-light.png", false)
		prefs.press("深色")
		prefs.press("应用库")
		prefs.fill(width, "960")
		prefs.fill("默认高度（像素）", "1801")
		prefs.press("保存")
		prefs.control(profile.ErrWindowHeight.Error())
		prefs.expectFocus("默认高度（像素）")
		prefs.checkAppearance("deskpad-settings-incorrect-height.png", false)
		if got, err := settings.Load(path); err != nil || got != light {
			t.Fatalf("invalid settings changed the file to %+v (%v)", got, err)
		}
		prefs.fill("默认高度（像素）", "680")
		// Refuse the atomic replacement once, then retry the same draft.
		backup := path + ".saved"
		if err := os.Rename(path, backup); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		prefs.press("保存")
		prefs.control("无法保存设置，请重试")
		prefs.expectValue(width, "960")
		if got, err := settings.Load(backup); err != nil || got != light {
			t.Fatalf("the failed save changed preferences to %+v (%v)", got, err)
		}
		prefs.checkAppearance("deskpad-settings-retry.png", false)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(backup, path); err != nil {
			t.Fatal(err)
		}
		prefs.press("保存")
		prefs.waitClosed()
		if got, err := settings.Load(path); err != nil || got != want {
			t.Fatalf("native settings saved %+v (%v), want %+v", got, err, want)
		}
		ui.expectValue("应用名称", "DeskPad review")
		ui.expectValue("应用网址", "https://chat.deepseek.com")
		ui.expectValue("窗口宽度（像素）", "914")
		ui.checkAppearance("deskpad-draft-dark.png", true)
		ui.press("取消")
		ui.press("选择 小红书")
		ui.press("窗口尺寸")
		ui.expectValue("窗口宽度（像素）", "960")
		ui.expectValue("窗口高度（像素）", "680")
		ui.press("取消")
		ui.focus(creatorMenuLabel)
		ui.expectFocus(creatorMenuLabel)
		ui.key("Space")
		ui.press(aboutTitle)
		about = ui.panel(aboutTitle)
		display.verifyWindow(t, pid)
		about.control("版本 " + version)
		about.control(licenseTitle)
		about.control("复制版本号")
		about.checkAppearance("deskpad-about.png", true)
		about.press(licenseTitle)
		about.expectFocus("返回")
		license := about.control(licenseBody)
		if license.Value != creatorLicense || license.Selection == nil || *license.Selection != ([2]int{}) {
			t.Fatalf("the About panel did not open the bundled license at its beginning: selection %v", license.Selection)
		}
		about.checkAppearance("deskpad-about-license.png", true)
		about.key("Enter")
		about.expectFocus(licenseTitle)
		about.control("版本 " + version)
		about.key("Escape")
		about.waitClosed()
		ui.expectFocus(creatorMenuLabel)
	})
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		ui.control("创建第一个应用")
		ui.menu(settingsItem)
		prefs := ui.panel(settingsTitle)
		display.verifyWindow(t, pid)
		prefs.expectValue("默认宽度（像素）", "960")
		prefs.expectValue("默认高度（像素）", "680")
		prefs.checkAppearance("deskpad-settings-dark.png", true)
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		prefs.press("保存")
		prefs.waitClosed()
		if after, err := os.Stat(path); err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
			t.Fatalf("saving unchanged preferences touched the file: %v (%v)", after, err)
		}
		ui.menu(settingsItem)
		prefs = ui.panel(settingsTitle)
		prefs.press("恢复默认")
		prefs.press("保存")
		prefs.waitClosed()
		if got, err := settings.Load(path); err != nil || got != settings.Default() {
			t.Fatalf("native reset saved %+v (%v)", got, err)
		}
	})
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, _ int) {
		ui.menu(settingsItem)
		prefs := ui.panel(settingsTitle)
		prefs.expectValue("默认宽度（像素）", "1200")
		prefs.press("保存")
		prefs.waitClosed()
		if got, err := settings.Load(path); err != nil || got != settings.Default() {
			t.Fatalf("saving defaults left damaged preferences: %+v (%v)", got, err)
		}
	})
	t.Logf("Opened and focused Settings from the Creator brand menu and AppKit's Cmd+comma, including from About. Verified the preserved drafts, license and restart on %s", display.name)
}

func TestMacCreatorRevealsInvalidDimensionsOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		ui = ui.panel(appName)
		const width, height = "窗口宽度（像素）", "窗口高度（像素）"
		ui.press("选择 DeepSeek")
		ui.press("窗口尺寸")
		ui.fill(width, "915")
		ui.fill(height, "479")
		ui.press("窗口尺寸")
		ui.press("创建应用")
		ui.control(profile.ErrWindowHeight.Error())
		ui.capture("deskpad-invalid-window-height.png")
		ui.expectFocus(height)
		ui.expectValue(width, "915")
		ui.expectValue(height, "479")
		ui.fill(height, "704")
		ui.key("Enter")
		ui.expectFocus("打开 DeepSeek")
		path := filepath.Join(state, "Library", "Application Support", "WebCrate", profile.Filename)
		apps, err := profile.NewStore(path).List()
		if err != nil || len(apps) != 1 || apps[0].Width != 915 || apps[0].Height != 704 {
			t.Fatalf("correcting the height and pressing Enter saved %+v (%v)", apps, err)
		}
		generated := filepath.Join(state, "Applications", "Creator", apps[0].ID, "DeepSeek.app")
		macCommand(t, "/usr/bin/codesign", "--verify", "--strict", "--deep", generated)
		display.verifyWindow(t, pid)
	})
	t.Log("The compact window revealed and focused the invalid height, then Enter created the signed app with the corrected dimensions.")
}

func TestMacCreatorSavesPreferencesWithoutBlockingOnDeskPad(t *testing.T) {
	manager, display := automatedMacCreator(t)
	state := t.TempDir()
	display.prepareCreator(t, state, minWindowWidth, minWindowHeight)
	path := filepath.Join(state, "Library", "Application Support", "WebCrate", "preferences.json")
	initial := settings.Default()
	initial.Appearance = settings.Light
	if err := settings.Save(path, initial); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(state, "hold-preferences-write")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEBCRATE_TEST_PREFERENCES_GATE", gate)
	withAutomatedMacCreator(t, manager, state, display, func(ui macCreatorUI, pid int) {
		main := ui.panel(appName)
		main.press("选择 DeepSeek")
		ui.menu(settingsItem)
		prefs := ui.panel(settingsTitle)
		const width = "默认宽度（像素）"
		prefs.fill(width, "960")
		prefs.press("保存")
		prefs.control("正在保存…")
		prefs.checkAppearance("deskpad-settings-saving.png", false)
		prefs.key("Escape")
		ui.menu(aboutTitle)
		about := ui.panel(aboutTitle)
		about.control("复制版本号")
		about.key("Escape")
		about.waitClosed()
		if err := unix.Kill(pid, unix.SIGUSR2); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(time.Second); ; time.Sleep(15 * time.Millisecond) {
			if _, err := os.Stat(gate + ".quit"); err == nil {
				break
			} else if !os.IsNotExist(err) || time.Now().After(deadline) {
				t.Fatalf("AppKit did not handle Cmd+Q while the write was held: %v", err)
			}
		}
		ui.menu(settingsItem)
		if reopened := ui.panel(settingsTitle); reopened.window != prefs.window {
			t.Fatal("a pending save allowed Settings to be closed")
		}
		prefs.control("正在保存…")
		main.expectValue("应用名称", "DeepSeek")
		if got, err := settings.Load(path); err != nil || got != initial {
			t.Fatalf("the pending write changed preferences to %+v (%v)", got, err)
		}
		if err := os.Remove(gate); err != nil {
			t.Fatal(err)
		}
		prefs.waitClosed()
		want := initial
		want.Width = 960
		if got, err := settings.Load(path); err != nil || got != want {
			t.Fatalf("finishing the write saved %+v (%v), want %+v", got, err, want)
		}
		main.expectValue("应用名称", "DeepSeek")
	})
	t.Logf("About opened during a pending Settings write; Escape and Cmd+Q preserved the draft until it was saved on %s", display.name)
}

type macCreatorWindow struct {
	ID            int
	Title         string
	Focused       bool
	Visible       bool
	ContentBounds struct{ Width, Height float64 }
}

func (ui macCreatorUI) windows() []macCreatorWindow {
	ui.t.Helper()
	var reply struct{ Windows []macCreatorWindow }
	ui.request("windows", nil, &reply)
	return reply.Windows
}

func (ui macCreatorUI) panel(title string) macCreatorUI {
	ui.t.Helper()
	var windows []macCreatorWindow
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		windows = ui.windows()
		id := 0
		for _, window := range windows {
			if window.Title == title && window.Visible {
				if id != 0 {
					ui.t.Fatalf("multiple visible windows are titled %q", title)
				}
				id = window.ID
			}
		}
		if id != 0 {
			ui.window = id
			return ui
		}
	}
	ui.t.Fatalf("no visible window is titled %q: %+v", title, windows)
	return ui
}

func (ui macCreatorUI) waitClosed() {
	ui.t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		closed := true
		for _, window := range ui.windows() {
			if window.ID == ui.window && window.Visible {
				closed = false
				break
			}
		}
		if closed {
			return
		}
	}
	ui.t.Fatalf("Creator window %d remained open after dismissal", ui.window)
}

func (ui macCreatorUI) waitWindowFocus() {
	ui.t.Helper()
	var windows []macCreatorWindow
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		windows = ui.windows()
		for _, window := range windows {
			if window.ID == ui.window && window.Focused {
				return
			}
		}
	}
	ui.t.Fatalf("Creator window %d did not receive keyboard focus: %+v", ui.window, windows)
}

func (ui macCreatorUI) menu(item string) {
	ui.t.Helper()
	ui.request("menu", map[string]any{"path": []string{appName, item}}, nil)
}

func (ui macCreatorUI) expectValue(label, want string) {
	ui.t.Helper()
	if value := ui.control(label).Value; value != want {
		ui.t.Fatalf("Creator field %q contains %q, want %q", label, value, want)
	}
}

func (ui macCreatorUI) checkAppearance(name string, dark bool) {
	ui.t.Helper()
	frame, err := png.Decode(bytes.NewReader(ui.capture(name)))
	if err != nil {
		ui.t.Fatal(err)
	}
	p := frame.Bounds().Min.Add(frame.Bounds().Size().Div(2))
	p.X = frame.Bounds().Min.X + 3 // The margin outside all cards and dialogs.
	r, g, b, _ := frame.At(p.X, p.Y).RGBA()
	if got := (r+g+b)/3 < 0x7fff; got != dark {
		ui.t.Fatalf("%s has unexpected appearance at %v, RGB #%02x%02x%02x", name, p, r>>8, g>>8, b>>8)
	}
}
