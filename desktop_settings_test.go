package main

import (
	"errors"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/settings"
)

func TestCreatorMenuCommands(t *testing.T) {
	var settingsOpened, aboutOpened bool
	menu := creatorMenu(func() { settingsOpened = true }, func() { aboutOpened = true })
	for _, item := range menu.Items()[0].Submenu {
		switch item.Label {
		case settingsItem:
			if item.Accelerator != "CmdOrCtrl+," || item.Type != mygo.MenuItemNormal {
				t.Fatalf("unexpected Settings menu item: %+v", item)
			}
			item.Click(item, nil)
		case aboutTitle:
			item.Click(item, nil)
		}
	}
	if !settingsOpened || !aboutOpened {
		t.Fatal("the application menu did not invoke both commands")
	}
}

func TestNativeCreatorBrandMenu(t *testing.T) {
	d, tt, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	var opened []string
	d.showSettings = func() { opened = append(opened, settingsTitle) }
	d.showAbout = func() { opened = append(opened, aboutTitle) }
	click(t, tt, creatorMenuLabel)
	if _, ok := tt.AccessByRole(ui.RoleMenuItem, settingsItem); !ok {
		t.Fatal("the Creator menu did not expose Settings")
	}
	tt.Key(0, ui.KeyEscape)
	if !tt.Focused(creatorMenuLabel) || len(opened) != 0 {
		t.Fatal("dismissing the menu moved focus or ran a command")
	}
	tt.Key(0, ui.KeySpace)
	tt.Key(0, ui.KeyEnter)
	if len(opened) != 1 || opened[0] != settingsTitle {
		t.Fatalf("choosing Settings with the keyboard ran %v", opened)
	}
	click(t, tt, creatorMenuLabel)
	click(t, tt, aboutTitle)
	if len(opened) != 2 || opened[1] != aboutTitle {
		t.Fatalf("choosing About ran %v", opened)
	}
}

func TestNativePreferencesEditor(t *testing.T) {
	editor := newPreferencesEditor(settings.Default())
	lastSaved := settings.Default()
	var attempts, closed int
	var finishFirst func(error)
	editor.close = func() { closed++ }
	editor.apply = func(prefs settings.Preferences, finish func(error)) {
		attempts++
		if attempts == 1 {
			finishFirst = finish
			return
		}
		lastSaved = prefs
		finish(nil)
	}
	tt := ui.NewTesterWith(editor.view, settingsWidth, settingsHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	fill := func(label, text string) {
		t.Helper()
		click(t, tt, label)
		typeReplace(tt, text)
	}

	click(t, tt, "深色")
	click(t, tt, "应用库")
	fill("默认宽度（像素）", "宽")
	click(t, tt, "保存")
	if !tt.HasText("请输入有效的窗口尺寸") || attempts != 0 {
		t.Fatal("invalid text was saved or had no visible error")
	}
	if width, _ := tt.Access("默认宽度（像素）"); !tt.Focused("默认宽度（像素）") || !width.Invalid || len(width.ErrorMessage) != 1 {
		t.Fatalf("settings did not focus the invalid width or describe its error: %+v", width)
	}
	fill("默认宽度（像素）", "639")
	click(t, tt, "保存")
	err := profile.ValidateWindowSize(639, profile.DefaultHeight)
	if !tt.HasText(err.Error()) || attempts != 0 {
		t.Fatal("unsupported width was saved or had no visible error")
	}

	fill("默认宽度（像素）", "960")
	fill("默认高度（像素）", "1801")
	click(t, tt, "保存")
	if height, _ := tt.Access("默认高度（像素）"); !tt.Focused("默认高度（像素）") || !height.Invalid || len(height.ErrorMessage) != 1 || attempts != 0 {
		t.Fatalf("settings did not keep the out-of-range height ready to correct: %+v", height)
	}
	fill("默认高度（像素）", "680")
	tt.Key(0, ui.KeyEnter)
	if finishFirst == nil || attempts != 1 || closed != 0 {
		t.Fatal("Enter did not start one write and keep the editor open")
	}
	if _, ok := tt.AccessByRole(ui.RoleStatus, "正在保存…"); !ok {
		t.Fatal("the pending write has no accessible status")
	}
	for _, name := range []string{"默认宽度（像素）", "默认高度（像素）", "恢复默认", "取消"} {
		if control, ok := tt.Access(name); !ok || !control.Disabled {
			t.Fatalf("%q is not disabled during the write", name)
		}
	}
	tt.Key(0, ui.KeyEnter)
	editor.save() // An action queued before the controls were disabled.
	if attempts != 1 {
		t.Fatal("a pending write allowed a second submission")
	}
	finishFirst(errors.New("fixture could not replace settings"))
	tt.Frame()
	if !tt.HasText("无法保存设置，请重试") || lastSaved != settings.Default() || closed != 0 {
		t.Fatal("a write failure changed saved preferences or had no visible error")
	}
	click(t, tt, "保存")
	want := settings.Preferences{Appearance: settings.Dark, StartPage: settings.LibraryPage, Width: 960, Height: 680}
	if lastSaved != want || attempts != 2 || closed != 1 {
		t.Fatalf("retry saved %+v after %d attempts, want %+v", lastSaved, attempts, want)
	}

	click(t, tt, "恢复默认")
	if attempts != 2 || lastSaved != want {
		t.Fatal("reset wrote settings before confirmation")
	}
	click(t, tt, "保存")
	if lastSaved != settings.Default() || attempts != 3 || closed != 2 {
		t.Fatalf("reset saved %+v after %d attempts", lastSaved, attempts)
	}
	fill("默认宽度（像素）", "960")
	click(t, tt, "取消")
	if closed != 3 || attempts != 3 || lastSaved != settings.Default() {
		t.Fatal("Cancel committed the draft instead of closing the settings window")
	}
}

func TestDefaultWindowDimensionsApplyToNewDrafts(t *testing.T) {
	d, tt, _, _ := nativeFixture(t, windowWidth, windowHeight)
	click(t, tt, "选择 DeepSeek")
	original := d.draft
	d.preferences.Width, d.preferences.Height = 960, 680
	if d.draft != original {
		t.Fatal("changing preferences edited the active draft")
	}

	click(t, tt, "取消")
	click(t, tt, "选择 小红书")
	if draft := d.draft; draft.app.Width != 960 || draft.app.Height != 680 || draft.width != "960" || draft.height != "680" {
		t.Fatalf("new preset draft used %sx%s", draft.width, draft.height)
	}
	saved := profile.App{ID: profile.NewID(), Name: "DeepSeek", URL: "https://chat.deepseek.com", Width: 800, Height: 560}
	d.edit(saved)
	if d.draft.width != "800" || d.draft.height != "560" {
		t.Fatal("editing an existing application applied the new defaults")
	}
}

func TestNativeAboutCopiesVersion(t *testing.T) {
	const version = "0.0.0-test"
	d := newDesktopApp(nil, nil, nil, nil)
	tt := ui.NewTester(func(c *ui.Context) { d.aboutView(c, version) }, aboutWidth, aboutHeight)
	tt.SetClipboard("previous contents")
	tt.FailClipboard(errors.New("clipboard busy"))
	click(t, tt, "复制版本号")
	if !tt.HasText("重试复制") || tt.Clipboard() != "previous contents" {
		t.Fatal("About did not offer to retry a failed copy")
	}
	tt.FailClipboard(nil)
	click(t, tt, "重试复制")
	if got := tt.Clipboard(); got != version || !tt.HasText("已复制") {
		t.Fatalf("About copied %q and showed %v, want %q and a confirmation", got, tt.Texts(), version)
	}
}

func TestNativeAboutShowsLicense(t *testing.T) {
	const version = "0.0.0-test"
	d := newDesktopApp(nil, nil, nil, nil)
	tt := ui.NewTester(func(c *ui.Context) { d.aboutView(c, version) }, aboutWidth, aboutHeight)
	tt.Key(0, ui.KeyTab)
	if !tt.Focused(licenseTitle) {
		t.Fatal("the license button is missing from About's tab order")
	}
	tt.Key(0, ui.KeyEnter)
	if !tt.Focused("返回") {
		t.Fatal("opening the license did not focus Back")
	}
	field, ok := tt.Access(licenseBody)
	if !ok || !field.ReadOnly || field.Value != creatorLicense {
		t.Fatalf("About should show the bundled license in a read-only field, got %+v", field)
	}
	tt.Key(0, ui.KeyTab)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("edited license")
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != creatorLicense {
		t.Fatal("the license body could not be selected and copied unchanged")
	}
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Key(0, ui.KeyEnter)
	if !tt.Focused(licenseTitle) || !tt.HasText("版本 "+version) {
		t.Fatal("returning from the license did not restore About and its focus")
	}
}
