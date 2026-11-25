package main

import (
	"log"
	"runtime"
	"slices"
	"strconv"

	"github.com/Bistutu/Creator"
	"github.com/Bistutu/Creator/internal/settings"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	creatorMenuLabel = appName + " 菜单"
	settingsTitle    = "设置"
	settingsItem     = "设置…"
	settingsWidth    = 382
	settingsHeight   = 300
	aboutTitle       = "关于 " + appName
	aboutWidth       = 318
	aboutHeight      = 221
	licenseTitle     = "MIT 许可证"
	licenseBody      = "MIT 许可证正文"
)

var creatorLicense = creator.License()
var persistPreferences = settings.Save

func creatorVersion() string {
	if version := mygo.App.Version(); version != "" {
		return version
	}
	return creator.Version()
}

func creatorMenu(showSettings, showAbout func()) *mygo.Menu {
	settingsCommand := &mygo.MenuItem{Label: settingsItem, Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) {
		showSettings()
	}}
	aboutCommand := &mygo.MenuItem{Label: aboutTitle, Click: func(*mygo.MenuItem, *mygo.Window) {
		showAbout()
	}}
	if runtime.GOOS == "darwin" {
		return mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
				aboutCommand, mygo.Separator(), settingsCommand, mygo.Separator(),
				{Role: mygo.RoleServices}, mygo.Separator(),
				{Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(), {Role: mygo.RoleQuit},
			}},
			{Role: mygo.RoleFileMenu}, {Role: mygo.RoleEditMenu}, {Role: mygo.RoleViewMenu}, {Role: mygo.RoleWindowMenu},
		})
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: appName, Submenu: []*mygo.MenuItem{
			settingsCommand, aboutCommand, mygo.Separator(),
			{Label: "关闭窗口", Accelerator: "Ctrl+W", Click: func(_ *mygo.MenuItem, window *mygo.Window) {
				if window != nil {
					window.Close()
				}
			}},
			{Role: mygo.RoleQuit},
		}},
		{Role: mygo.RoleEditMenu},
	})
}

func (d *desktopApp) brandMenu(c *ui.Context) {
	t := c.Theme()
	button := card(c.Key("creator-menu"), creatorMenuLabel).Height(30).Padding(0, 5, 0, 0).Gap(7).Radius(buttonRadius)
	button.Children(func() {
		showIcon(c, d.brand, 23)
		ui.Text(c, appName).FontSize(17).FontWeight(600).TextColor(t.Text)
		ui.Box(c).Size(9, 9).Draw(func(p *ui.Painter, r ui.Rect) {
			x, y := r.X+r.W/2, r.Y+r.H/2
			var path ui.Path
			path.MoveTo(x-3, y-1.5).LineTo(x, y+1.5).LineTo(x+3, y-1.5)
			p.StrokePath(&path, 1.3, t.TextMuted)
		})
	})
	button.PopoverMenuButton(func(menu *ui.Menu) {
		if menu.Item(settingsItem).Shortcut(ui.Cmd, ui.KeyComma).Chosen() && d.showSettings != nil {
			d.showSettings()
		}
		if menu.Item(aboutTitle).Chosen() && d.showAbout != nil {
			d.showAbout()
		}
	})
}

func (d *desktopApp) installMenu(parent *mygo.Window, path string, repairPreferences bool) {
	var preferences, about *mygo.Window
	var editor *preferencesEditor
	keepOpenWhileSaving := func(event *mygo.Preventable) {
		if editor != nil && editor.saving {
			event.PreventDefault()
		}
	}
	parent.OnClose(func(event *mygo.CloseEvent) { keepOpenWhileSaving(&event.Preventable) })
	mygo.App.OnBeforeQuit(func(event *mygo.QuitEvent) { keepOpenWhileSaving(&event.Preventable) })
	d.showSettings = func() {
		if showExistingWindow(preferences) {
			return
		}
		editor = newPreferencesEditor(d.preferences)
		editor.close = func() { preferences.Close() }
		editor.apply = func(prefs settings.Preferences, finish func(error)) {
			current := d.preferences
			if prefs == current && !repairPreferences {
				finish(nil)
				return
			}
			window := preferences
			d.work(func() {
				err := persistPreferences(path, prefs)
				window.Update(func() {
					if err == nil {
						repairPreferences = false
						d.preferences = prefs
						if prefs.Appearance != current.Appearance {
							mygo.Theme.SetSource(mygo.ThemeSource(prefs.Appearance))
						}
					}
					finish(err)
				})
			})
		}
		preferences = openCreatorPanel(parent, settingsTitle, settingsWidth, settingsHeight, editor.view)
		preferences.OnClose(func(event *mygo.CloseEvent) { keepOpenWhileSaving(&event.Preventable) })
	}
	d.showAbout = func() {
		if showExistingWindow(about) {
			return
		}
		version := creatorVersion()
		about = openCreatorPanel(parent, aboutTitle, aboutWidth, aboutHeight, func(c *ui.Context) { d.aboutView(c, version) })
	}
	mygo.App.SetMenu(creatorMenu(d.showSettings, d.showAbout))
}

func showExistingWindow(window *mygo.Window) bool {
	if window == nil || window.IsDestroyed() {
		return false
	}
	if window.IsMinimized() {
		window.Restore()
	}
	window.Show()
	return true
}

func openCreatorPanel(parent *mygo.Window, title string, width, height int, view func(*ui.Context)) *mygo.Window {
	if parent.IsMinimized() {
		parent.Restore()
	}
	frame := parent.Bounds()
	var panel *mygo.Window
	panel = mygo.NewWindow(mygo.WindowOptions{
		Title: title, Parent: parent, Width: width, Height: height, UseContentSize: true,
		X: frame.X + (frame.Width-width)/2, Y: frame.Y + max(0, (frame.Height-height)/3),
		DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		AutoHideMenuBar: true, SkipTaskbar: true,
		BackgroundColor: "light-dark(" + lightBackground + ", " + darkBackground + ")",
		Content: ui.View(func(c *ui.Context) {
			c.OnWindowShortcut(0, ui.KeyEscape, func() { panel.Close() })
			view(c)
		}),
	})
	return panel
}

var appearances = [...]settings.Appearance{settings.System, settings.Light, settings.Dark}
var startPages = [...]settings.StartPage{settings.CreatePage, settings.LibraryPage}

type preferencesEditor struct {
	appearance, startup int
	width, height       string
	issue               formIssue
	focusField          formField
	saving              bool
	apply               func(settings.Preferences, func(error))
	close               func()
}

func newPreferencesEditor(prefs settings.Preferences) *preferencesEditor {
	editor := &preferencesEditor{}
	editor.fill(prefs)
	return editor
}

func (e *preferencesEditor) fill(prefs settings.Preferences) {
	e.appearance = slices.Index(appearances[:], prefs.Appearance)
	e.startup = slices.Index(startPages[:], prefs.StartPage)
	e.width, e.height = strconv.Itoa(prefs.Width), strconv.Itoa(prefs.Height)
	e.issue, e.focusField = formIssue{}, noField
}

func (e *preferencesEditor) view(c *ui.Context) {
	applyCreatorTheme(c)
	t := c.Theme()
	var fields [heightField + 1]ui.Element
	ui.Column(c).Fill().Padding(16, 17).Gap(11).Children(func() {
		ui.Column(c).Padding(10, 12).Gap(9).Radius(cardRadius).Background(t.Surface).Children(func() {
			ui.Row(c).Height(30).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, "外观").FontSize(13).FontWeight(600)
				ui.Segmented(c, &e.appearance, "跟随系统", "浅色", "深色").Label("外观").Height(28).
					Background(t.Background).Border(0, ui.Transparent).Disabled(e.saving)
			})
			ui.Box(c).Height(1).Background(t.Border)
			ui.Row(c).Height(30).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, "启动时显示").FontSize(13).FontWeight(600)
				ui.Segmented(c, &e.startup, "创建", "应用库").Label("启动时显示").Height(28).
					Background(t.Background).Border(0, ui.Transparent).Disabled(e.saving)
			})
		})
		ui.Column(c).Padding(10, 12).Gap(7).Radius(cardRadius).Background(t.Surface).Children(func() {
			ui.Text(c, "新应用默认尺寸").FontSize(13).FontWeight(600)
			ui.Row(c).Gap(10).Children(func() {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					label(c, "宽度（像素）")
					field := textField(c, &e.width, "默认宽度（像素）", "1200", e.saving).Background(t.Background)
					fields[widthField] = field
					e.sizeField(field, widthField)
				})
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					label(c, "高度（像素）")
					field := textField(c, &e.height, "默认高度（像素）", "820", e.saving).Background(t.Background)
					fields[heightField] = field
					e.sizeField(field, heightField)
				})
			})
			if e.issue.field != noField {
				e.issue.draw(c, fields[e.issue.field])
			}
		})
		ui.Column(c).Grow(1).Justify(ui.End).Gap(7).Children(func() {
			if e.issue.field == noField {
				e.issue.draw(c, ui.Element{})
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				if ui.Button(c, "恢复默认").Background(ui.Transparent).Border(0, ui.Transparent).TextColor(t.TextMuted).Disabled(e.saving).Clicked() {
					e.fill(settings.Default())
				}
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Button(c, "取消").MinWidth(60).Disabled(e.saving).OnClick(e.close)
					if e.saving {
						ui.Text(c, "正在保存…").Width(78).TextAlign(ui.Center).FontSize(12).TextColor(t.TextMuted).Role(ui.RoleStatus)
					} else {
						ui.PrimaryButton(c, "保存").MinWidth(78).OnClick(e.save)
					}
				})
			})
		})
	})
	if field := fields[e.focusField]; field.Valid() {
		field.Focus()
		e.focusField = noField
	}
}

func (e *preferencesEditor) sizeField(field ui.Element, kind formField) {
	if field.Changed() {
		e.issue.fieldChanged(kind)
	}
	if field.Submitted() {
		e.save()
	}
}

func (e *preferencesEditor) save() {
	if e.saving {
		return
	}
	width, height, issue := readWindowSize(e.width, e.height)
	if issue.message != "" {
		e.issue, e.focusField = issue, issue.field
		return
	}
	prefs := settings.Preferences{Appearance: appearances[e.appearance], StartPage: startPages[e.startup], Width: width, Height: height}
	e.saving, e.issue = true, formIssue{}
	e.apply(prefs, func(err error) {
		e.saving = false
		if err != nil {
			log.Printf("保存 Creator 设置: %v", err)
			e.issue = formIssue{message: "无法保存设置，请重试"}
			return
		}
		e.close()
	})
}

type aboutPanelState struct {
	showLicense, moveFocus bool
	copyLabel              string
}

func (d *desktopApp) aboutView(c *ui.Context, version string) {
	applyCreatorTheme(c)
	t := c.Theme()
	panel := ui.Column(c).Fill().Padding(15).Gap(8)
	state := ui.Local(panel, "about", func() aboutPanelState { return aboutPanelState{copyLabel: "复制版本号"} })
	if state.showLicense {
		panel.Children(func() {
			ui.Row(c.Key("license-heading")).Gap(10).AlignItems(ui.Center).Children(func() {
				back := ui.Button(c, "返回").Height(26).OnClick(func() {
					state.showLicense, state.moveFocus = false, true
				})
				if state.moveFocus {
					back.Focus()
				}
				ui.Text(c, licenseTitle).FontSize(13).FontWeight(600)
			})
			text := creatorLicense
			body := ui.TextArea(c.Key("license-text"), &text).Label(licenseBody).ReadOnly(true).Grow(1).
				Padding(8, 10).FontSize(12).Background(t.Surface).Border(0, ui.Transparent)
			if state.moveFocus {
				body.SetTextSelection(0, 0)
				state.moveFocus = false
			}
		})
		return
	}
	panel.AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		showIcon(c, d.brand, 56)
		ui.Text(c, appName).FontSize(21).FontWeight(600)
		ui.Text(c, "版本 "+version).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, "把常用网站变成独立的桌面应用").FontSize(13)
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			license := ui.Button(c.Key("license"), licenseTitle).Height(26).FontSize(12).
				Background(t.Surface).Border(0, ui.Transparent).Transition(controlMotion).OnClick(func() {
				state.showLicense, state.moveFocus = true, true
			})
			if state.moveFocus {
				license.Focus()
				state.moveFocus = false
			}
			if ui.Button(c.Key("copy-version"), state.copyLabel).Height(26).MinWidth(88).FontSize(12).
				Background(t.Surface).Border(0, ui.Transparent).Transition(controlMotion).Clicked() {
				c.WriteClipboardThen(version, func(err error) {
					if err != nil {
						state.copyLabel = "重试复制"
					} else {
						state.copyLabel = "已复制"
					}
				})
			}
		})
	})
}
