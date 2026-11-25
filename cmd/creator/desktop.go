package main

import (
	"context"
	"errors"
	"image"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/Bistutu/Creator/internal/catalog"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/settings"
)

var categories = []string{"全部", "AI", "效率", "社交", "影音", "阅读", "开发"}

const noticeToastID = "creator.notice"

type appLibrary interface {
	List() ([]profile.App, error)
	Save(context.Context, profile.App, library.Icon) (profile.App, error)
	SaveWithSuggestedName(context.Context, profile.App, library.Icon, string) (profile.App, error)
	RefreshIcon(context.Context, profile.App, image.Image) (bool, error)
	TryInstalledArtwork(profile.App, <-chan struct{}) (icons.Artwork, <-chan struct{})
	Path(context.Context, profile.App, library.IconSource) (string, error)
	Open(context.Context, profile.App, library.IconSource) error
	Remove(string) error
}

type sitePreviewProvider interface {
	Cached(string) image.Image
	CachedName(string) string
	Inspect(context.Context, string, func(string)) (image.Image, error)
}

type artwork struct {
	img    image.Image
	bitmap *ui.Bitmap
	kind   icons.ArtworkKind
}

func art(img image.Image) *artwork { return displayArtwork(icons.Artwork{Image: img}) }

func siteArt(img image.Image) *artwork {
	return displayArtwork(icons.Artwork{Image: img, Kind: icons.WebsiteArtwork})
}

func displayArtwork(picture icons.Artwork) *artwork {
	return &artwork{img: picture.Image, bitmap: ui.NewBitmap(picture.Image), kind: picture.Kind}
}

func (a *artwork) stored() icons.Artwork {
	if a == nil {
		return icons.Artwork{}
	}
	return icons.Artwork{Image: a.img, Kind: a.kind}
}

type editor struct {
	open         bool
	sizeOpen     bool
	focusField   formField
	busy         bool
	picking      bool
	custom       bool
	returnToCard string
	original     profile.App
	app          profile.App
	width        string
	height       string
	issue        formIssue
	icon         *artwork
	lookup       *iconLookup
	websiteKey   string
	websiteName  string
}

type desktopApp struct {
	library       appLibrary
	resolver      sitePreviewProvider
	customIcons   *icons.CustomStore
	chooseIcon    func() (image.Image, error)
	dispatch      func(func())
	work          func(func())
	reveal        func(string)
	brand         *artwork
	presets       []catalog.Preset
	presetArt     map[string]func() *artwork
	appArt        artworkCache
	pendingArt    map[string]*artworkRead
	activeLookups map[string]*iconLookup
	savedLookups  map[string]*iconLookup
	preferences   settings.Preferences
	showSettings  func()
	showAbout     func()

	page               int
	initialFocus       bool
	category           int
	query              string
	focusCreateInput   bool
	focusCreateResult  bool
	receivedAddress    string
	address            addressPreview
	addressRead        *addressCacheRead
	addressReadSlots   chan struct{}
	appsQuery          string
	focusLibraryResult bool
	focusLibraryApp    libraryFocus
	apps               []profile.App
	items              []libraryItem
	filtered           []libraryItem
	filterQuery        string
	loadingLibrary     bool
	libraryLoadFailed  bool
	libraryRevision    uint64
	afterLibraryLoad   func([]profile.App)
	busyApps           map[string]string
	notice             string
	noticeOpen         bool
	searchError        string
	draft              editor
	removeOpen         bool
	removeApp          profile.App
}

func newDesktopApp(library appLibrary, resolver sitePreviewProvider, customIcons *icons.CustomStore, presets []catalog.Preset) *desktopApp {
	d := &desktopApp{
		library: library, resolver: resolver, customIcons: customIcons, presets: presets,
		brand: art(icons.Brand()), presetArt: make(map[string]func() *artwork, len(presets)), appArt: newArtworkCache(),
		pendingArt: make(map[string]*artworkRead), busyApps: make(map[string]string),
		activeLookups: make(map[string]*iconLookup), savedLookups: make(map[string]*iconLookup),
		preferences: settings.Default(), initialFocus: true,
		dispatch: func(f func()) { f() }, work: func(f func()) { go f() },
		addressReadSlots: make(chan struct{}, 2),
	}
	for _, preset := range presets {
		d.presetArt[preset.Mark] = sync.OnceValue(func() *artwork {
			picture, err := preset.Icon()
			if err != nil {
				log.Printf("读取推荐图标: %v", err)
				return placeholderArtwork(preset.App())
			}
			return siteArt(picture)
		})
	}
	return d
}

func (d *desktopApp) view(c *ui.Context) {
	applyCreatorTheme(c)
	d.appArt.beginFrame()
	if d.draft.open && d.draft.original.ID != "" {
		d.appArt.show(d.draft.original.ID)
	}
	root := ui.Column(c).Fill()
	if !d.draft.open && !d.removeOpen {
		if drop, ok := ui.DropData(root, websiteDropOptions); ok {
			d.receiveWebsiteLink(drop.Data)
		}
	}
	root.Children(func() {
		d.header(c)
		ui.Box(c).Grow(1).Clip().Children(func() {
			ui.Column(c.Key(d.page)).Absolute().Fill().Padding(4, pagePadding, 12).Transition(pageMotion).Children(func() {
				if d.page == 0 {
					d.focusLibraryResult = false
					d.focusLibraryApp = libraryFocus{}
					d.createPage(c)
				} else {
					d.clearAddressLookup()
					d.libraryPage(c)
				}
			})
		})
	})
	d.appArt.trim()
	if d.draft.open || d.removeOpen {
		// Feedback from the library can overlap the actions at the bottom
		// of a compact dialog. Dismiss it when the dialog takes focus.
		if d.noticeOpen {
			c.CloseToast(noticeToastID)
		}
	} else if d.notice != "" {
		d.noticeOpen = true
		c.AddToast(ui.Toast{ID: noticeToastID, Title: d.notice, OnClose: func() { d.noticeOpen = false }})
		d.notice = ""
	}
	d.editorDialog(c)
	d.confirmRemove(c)
	d.stopHiddenArtworkReads()
}

func (d *desktopApp) header(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	ui.Box(c).Height(max(8, bar.Height, bar.Bottom+4)).DragWindow()
	header := ui.Row(c).Height(brandRowHeight).Padding(0, pagePadding).
		AlignItems(ui.Center).Justify(ui.SpaceBetween)
	header.Children(func() {
		d.brandMenu(c)
		ui.Segmented(c, &d.page, "创建", "应用库").Label("页面").Height(26).Background(t.Surface).Border(0, ui.Transparent)
	})
	if d.initialFocus && header.FocusWithin() {
		// The user can use the header while the initial library read is pending.
		d.initialFocus = false
	}
}

func (d *desktopApp) focusInitialControl(control ui.Element) {
	if d.initialFocus {
		d.initialFocus = false
		control.Focus()
	}
}

func (d *desktopApp) libraryPage(c *ui.Context) {
	t := c.Theme()
	panel := ui.Column(c).Grow(1).Gap(8)
	grid := ui.Local(panel, "library-grid", func() ui.GridState { return ui.GridState{} })
	panel.Children(func() {
		var search ui.Element
		if len(d.apps) > 0 {
			var cleared bool
			search, cleared = searchBox(c, &d.appsQuery, "搜索应用库", "搜索应用", nil)
			d.focusInitialControl(search)
			search.OnTextKey(func(key ui.TextKey) bool {
				if key.Key != ui.KeyDown || key.Mods != 0 || d.draft.open || d.removeOpen {
					return false
				}
				d.focusLibraryResult = true
				return true
			})
			if search.Changed() || cleared {
				grid.ScrollTo(0, ui.Start)
			}
		}
		if d.libraryLoadFailed {
			status := ui.Row(c.Key("read-error")).AlignItems(ui.Center).Gap(8)
			if len(d.apps) == 0 {
				status.Grow(1).Justify(ui.Center)
			}
			status.Children(func() {
				ui.Text(c, "无法读取应用库").FontSize(13).TextColor(t.TextMuted)
				retry := ui.Button(c, "重试")
				d.focusInitialControl(retry)
				if retry.Clicked() {
					d.initialFocus = retry.Focused()
					d.loadLibrary()
				}
			})
		}
		items := d.libraryItems()
		if d.focusLibraryResult {
			d.focusLibraryResult = false
			d.moveLibraryFocus(items, 0, 1)
		}
		if len(items) == 1 && !d.draft.open && !d.removeOpen && search.Submitted() && strings.TrimSpace(d.appsQuery) != "" {
			d.open(items[0].app, false)
		}
		target := -1
		if d.focusLibraryApp.id != "" {
			target = slices.IndexFunc(items, func(item libraryItem) bool { return item.app.ID == d.focusLibraryApp.id })
			if target == -1 {
				d.focusLibraryApp = libraryFocus{}
			}
		}
		if len(items) == 0 {
			if d.loadingLibrary {
				ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Text(c, "正在读取应用…").FontSize(13).TextColor(t.TextMuted).Role(ui.RoleStatus)
				})
				return
			}
			if d.libraryLoadFailed {
				return
			}
			if len(d.apps) > 0 {
				empty(c, "没有匹配的应用", "")
				return
			}
			ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(12).Children(func() {
				showIcon(c, d.brand, 42)
				ui.Text(c, "你的应用会出现在这里").FontSize(15).FontWeight(600)
				create := ui.PrimaryButton(c, "创建第一个应用")
				d.focusInitialControl(create)
				if create.Clicked() {
					d.page = 0
					d.focusCreateInput = true
				}
			})
			return
		}
		const rowHeight = 44
		viewport := grid.Bounds(c)
		var artworkRequests []profile.App
		var focus ui.Element
		ui.GridView(c, grid, len(items), 342, rowHeight, func(i int) {
			item := items[i]
			app := item.app
			d.appArt.show(app.ID)
			status, busy := d.busyApps[app.ID]
			description := app.URL
			if busy {
				description = status + " " + app.URL
			} else {
				status = item.address
			}
			row := ui.Row(c.Key(app.ID)).Grow(1).AlignItems(ui.Center).Padding(0, 4, 0, 0).Radius(cardRadius).Background(t.Surface)
			var requests *[]profile.App
			bounds := row.LayoutBounds()
			if viewport.H == 0 || bounds.H == 0 {
				// Newly realized rows have coordinates after the first layout.
				c.AnimationFrame()
			} else if bounds.Y < viewport.Y+viewport.H && bounds.Y+bounds.H > viewport.Y {
				requests = &artworkRequests
			}
			row.Children(func() {
				button := card(c.Key("open"), "打开 "+item.title).Height(rowHeight).Grow(1).MinWidth(0).Padding(0, 9).
					Justify(ui.Start).Description(description).AccessDisabled(busy)
				if i == target && !busy {
					focus = button
				}
				button.Children(func() {
					if icon := d.visibleArtwork(app, requests); icon != nil {
						showIcon(c, icon, 32)
					} else {
						ui.Box(c).Width(32).Height(32).Radius(7).Background(t.SurfacePressed)
					}
					cardTitle(c, item.title, status)
				})
				if button.Clicked() && !busy {
					d.open(app, false)
				}
				d.libraryRowKeys(c, button, search, items, i)
				options := ui.Button(c.Key("options"), "⋯").Label(item.title+" 的选项").
					Width(38).Height(38).Padding(0).FontSize(18).Radius(buttonRadius).Background(ui.Transparent).
					Border(0, ui.Transparent).TextColor(t.TextMuted).Transition(controlMotion).Disabled(busy)
				if options.Hovered() {
					options.Background(t.SurfaceHover).TextColor(t.Text)
				}
				options.PopoverMenuButton(func(menu *ui.Menu) {
					if menu.Item("编辑应用").Chosen() {
						d.edit(app)
					}
					if menu.Item("复制网址").Chosen() {
						c.WriteClipboardThen(app.URL, func(err error) {
							if err != nil {
								d.fail("无法复制网址，请重试", err)
								return
							}
							d.notice = "已复制网址"
						})
					}
					if menu.Item("在文件管理器中显示").Chosen() {
						d.open(app, true)
					}
					menu.Separator()
					if menu.Item("移除应用").Chosen() {
						d.removeApp, d.removeOpen = app, true
					}
				})
			})
		}).Gap(0).Grow(1)
		if target >= 0 {
			if d.focusLibraryApp.center {
				grid.ScrollTo(target, ui.Center)
			} else {
				grid.ScrollIntoView(target)
			}
			if focus.Valid() {
				focus.Focus(ui.FocusOptions{PreventScroll: true})
				d.focusLibraryApp = libraryFocus{}
			} else if _, busy := d.busyApps[d.focusLibraryApp.id]; busy {
				d.focusLibraryApp = libraryFocus{}
			} else {
				// The scroll builds an offscreen row during layout. Give it
				// keyboard focus when that row participates in the next view.
				c.AnimationFrame()
			}
		}
		d.loadVisibleArtwork(artworkRequests)
	})
}

func (d *desktopApp) editorDialog(c *ui.Context) {
	e := &d.draft
	if !e.open {
		if !e.busy {
			d.clearDraftLookup()
		}
		return
	}
	t := c.Theme()
	open := e.open
	var fields [heightField + 1]ui.Element
	ui.DialogBase(c, &open, func(back, panel ui.Element) {
		styleDialog(c, back, panel, 350)
		title := "新建应用"
		if e.app.ID != "" {
			title = "编辑应用"
		}
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			d.iconButton(c)
			ui.Text(c, title).FontSize(18).FontWeight(600).Grow(1)
			if e.custom || e.app.CustomIcon != "" {
				reset := ui.Button(c, "恢复网站图标").Height(28).FontSize(12).Padding(0, 7).
					Background(ui.Transparent).Border(0, ui.Transparent).TextColor(t.Accent).Disabled(e.busy || e.picking)
				if reset.Clicked() {
					e.custom, e.app.CustomIcon = false, ""
					e.issue.fieldChanged(noField)
					d.previewIcon(0)
				}
			}
		})
		ui.Column(c).Gap(6).Children(func() {
			caption := "网址"
			if e.app.ID != "" {
				caption = "首页网址"
			}
			label(c, caption)
			field := textField(c, &e.app.URL, "应用网址", "example.com", e.busy)
			fields[addressField] = field
			if field.Changed() {
				e.issue.fieldChanged(addressField)
				d.previewIcon(240 * time.Millisecond)
			}
			if field.Submitted() {
				d.save()
			}
			if e.issue.field == addressField {
				e.issue.draw(c, field)
			}
		})
		ui.Column(c).Gap(6).Children(func() {
			label(c, "名称")
			placeholder := e.websiteName
			if placeholder == "" {
				placeholder = "使用网站域名"
			}
			field := textField(c, &e.app.Name, "应用名称", placeholder, e.busy)
			fields[nameField] = field
			if field.Changed() {
				e.issue.fieldChanged(nameField)
				d.previewIcon(0)
			}
			if field.Submitted() {
				d.save()
			}
			if e.issue.field == nameField {
				e.issue.draw(c, field)
			}
		})
		target := float32(0)
		if e.sizeOpen {
			target = 1
		}
		reveal := panel.AnimateWith("dimensions", target, dimensionDuration, settleEase)
		ui.Column(c.Key("editor-footer")).Children(func() {
			width, height := d.windowDimensions(c, reveal)
			if e.sizeOpen && reveal > 0 {
				fields[widthField], fields[heightField] = width, height
			}
			if e.issue.field == noField {
				e.issue.draw(c, ui.Element{}).Margin(0, 0, 10)
			}
			d.editorActions(c, reveal)
		})
	})
	// Let the overlay record its opener before moving focus into the form.
	if field := fields[e.focusField]; field.Valid() {
		field.Focus()
		e.focusField = noField
	}
	if !open && !e.busy && !e.picking {
		d.dismissEditor()
	}
}

func (d *desktopApp) dismissEditor() {
	d.clearDraftLookup()
	d.draft.open = false
}

func (d *desktopApp) windowDimensions(c *ui.Context, reveal float32) (width, height ui.Element) {
	e := &d.draft
	if !e.sizeOpen && reveal == 0 {
		return width, height
	}
	clip := ui.Column(c.Key("dimensions")).ClipY()
	clip.Children(func() {
		// Keep the fields at their natural height while the footer reflows.
		content := ui.Column(c).Shrink(0).Padding(0, 0, 10).Gap(6).
			IgnoreInput(!e.sizeOpen).AccessHidden(!e.sizeOpen)
		if reveal < 1 {
			clip.Height(reveal * content.LayoutBounds().H)
		}
		content.Children(func() {
			ui.Row(c).Gap(10).Children(func() {
				ui.Column(c).Grow(1).Gap(6).Children(func() {
					label(c, "窗口宽度")
					width = d.dimensionField(c, &e.width, "窗口宽度（像素）", "1200", widthField)
				})
				ui.Column(c).Grow(1).Gap(6).Children(func() {
					label(c, "窗口高度")
					height = d.dimensionField(c, &e.height, "窗口高度（像素）", "820", heightField)
				})
			})
			switch e.issue.field {
			case widthField:
				e.issue.draw(c, width)
			case heightField:
				e.issue.draw(c, height)
			}
		})
	})
	return width, height
}

func (d *desktopApp) dimensionField(c *ui.Context, value *string, label, placeholder string, kind formField) ui.Element {
	field := textField(c, value, label, placeholder, d.draft.busy)
	if field.Changed() {
		d.draft.issue.fieldChanged(kind)
	}
	if field.Submitted() {
		d.save()
	}
	return field
}

func (d *desktopApp) editorActions(c *ui.Context, reveal float32) {
	e := &d.draft
	disabled := e.busy || e.picking
	ui.Row(c.Key("editor-actions")).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
		if dimensionDisclosure(c, e.sizeOpen, reveal, disabled).Clicked() {
			e.sizeOpen = !e.sizeOpen
			e.focusField = noField
			if e.sizeOpen {
				e.focusField = widthField
			}
			c.AnimationFrame()
		}
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			if ui.Button(c, "取消").Disabled(disabled).Clicked() {
				d.dismissEditor()
			}
			cta := "创建应用"
			if e.app.ID != "" {
				cta = "保存"
			}
			if e.busy {
				cta = "正在保存…"
			}
			if ui.PrimaryButton(c, cta).MinWidth(104).Disabled(disabled).Clicked() {
				d.save()
			}
		})
	})
}

func dimensionDisclosure(c *ui.Context, open bool, reveal float32, disabled bool) ui.Element {
	const title = "窗口尺寸"
	t := c.Theme()
	button := ui.ButtonBase(c).Role(ui.RoleDisclosure).Label(title).Expanded(open).
		Height(30).Padding(0, 5).Gap(5).AlignItems(ui.Center).Radius(buttonRadius).
		Background(ui.Transparent).FocusRingColor(t.Accent).Transition(controlMotion).Disabled(disabled)
	if button.Hovered() && !disabled {
		button.Background(t.SurfaceHover)
	}
	button.Children(func() {
		ui.Box(c).Size(12, 12).Rotate(90 * reveal).Draw(func(p *ui.Painter, r ui.Rect) {
			x, y := r.X+r.W/2, r.Y+r.H/2
			var path ui.Path
			path.MoveTo(x-1.5, y-3).LineTo(x+1.5, y).LineTo(x-1.5, y+3)
			p.StrokePath(&path, 1.4, t.TextMuted)
		})
		ui.Text(c, title).FontSize(12).TextColor(t.TextMuted)
	})
	return button
}

func (d *desktopApp) confirmRemove(c *ui.Context) {
	t := c.Theme()
	ui.DialogBase(c, &d.removeOpen, func(back, panel ui.Element) {
		styleDialog(c, back, panel, 310)
		ui.Text(c, "移除应用？").FontSize(17).FontWeight(600)
		ui.Text(c, d.removeApp.Name).FontSize(14).TextColor(t.TextMuted).Wrap()
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c, "取消").Clicked() {
				d.removeOpen = false
			}
			if ui.Button(c, "移除").TextColor(t.Danger).Clicked() {
				d.remove()
			}
		})
	})
}

func styleDialog(c *ui.Context, back, panel ui.Element, width float32) {
	theme := c.Theme()
	window, _ := c.Size()
	back.Background(ui.RGBA(0, 0, 0, 0.30)).Transition(dialogMotion)
	panel.Width(min(width, window-28)).Padding(17).Gap(10).Radius(dialogRadius).Background(theme.Background).Border(1, theme.Border).
		Shadow(0, 8, 28, 0, ui.RGBA(0, 0, 0, 0.22))
}

func card(c *ui.Context, accessibleName string) ui.Element {
	t := c.Theme()
	button := ui.ButtonBase(c).Label(accessibleName).Radius(cardRadius).AlignItems(ui.Center).Gap(9).
		Background(ui.Transparent).Transition(controlMotion).FocusRingOffset(-2).FocusRingColor(t.Accent)
	if button.Hovered() {
		button.Background(t.SurfaceHover)
	} else if button.Focused() {
		button.Background(t.SurfacePressed)
	}
	return button
}

func cardTitle(c *ui.Context, name, origin string) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
		ui.Text(c, name).WidthPercent(100).FontSize(13).FontWeight(600).SingleLine().Ellipsis("…")
		ui.Text(c, origin).WidthPercent(100).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine().TruncateMiddle()
	})
}

func searchBox(c *ui.Context, value *string, name, placeholder string, add func()) (field ui.Element, cleared bool) {
	t := c.Theme()
	ui.Row(c).WidthPercent(100).Height(37).AlignItems(ui.Center).
		Radius(fieldRadius).Background(t.Surface).Children(func() {
		field = ui.TextInput(c, value).Search().Label(name).Placeholder(placeholder).Grow(1).MinWidth(0).
			Height(37).Padding(0, 11).Radius(fieldRadius).Background(ui.Transparent).Border(0, ui.Transparent)
		if *value != "" {
			clear := ui.Button(c.Key("clear"), "×").Label("清除搜索").Width(28).Height(28).FontSize(17).Padding(0).
				Radius(buttonRadius).Margin(0, 4, 0, 0).Background(ui.Transparent).Border(0, ui.Transparent).
				TextColor(t.TextMuted).Transition(controlMotion)
			if clear.Hovered() {
				clear.Background(t.SurfacePressed).TextColor(t.Text)
			}
			if clear.Clicked() {
				*value, cleared = "", true
				field.Focus()
			}
		} else if add != nil {
			button := ui.Button(c.Key("add"), "添加").Height(28).MinWidth(50).FontSize(12).FontWeight(600).
				Margin(0, 4, 0, 0).Background(t.SurfacePressed).Border(0, ui.Transparent).TextColor(t.Accent).Transition(controlMotion)
			if button.Clicked() {
				add()
			}
		}
	})
	return field, cleared
}

func label(c *ui.Context, value string) {
	ui.Text(c, value).FontSize(12).Bold().TextColor(c.Theme().TextMuted)
}

func textField(c *ui.Context, value *string, label, placeholder string, disabled bool) ui.Element {
	return ui.TextInput(c, value).Label(label).Placeholder(placeholder).Height(36).Padding(0, 10).
		Radius(fieldRadius).Background(c.Theme().Surface).Border(0, ui.Transparent).Disabled(disabled)
}

func showIcon(c *ui.Context, icon *artwork, size float32) ui.Element {
	if icon == nil {
		return ui.Element{}
	}
	return ui.Image(c, icon.bitmap).Width(size).Height(size)
}

func empty(c *ui.Context, heading, caption string) {
	ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(7).Children(func() {
		ui.Text(c, heading).FontSize(16).Bold()
		if caption != "" {
			ui.Text(c, caption).FontSize(13).TextColor(c.Theme().TextMuted)
		}
	})
}

func (d *desktopApp) edit(app profile.App) {
	if d.draft.busy || d.draft.picking {
		return
	}
	d.stopAddressCacheRead()
	d.clearDraftLookup()
	width, height := profile.DefaultWidth, profile.DefaultHeight
	if app.ID == "" {
		width, height = d.preferences.Width, d.preferences.Height
	}
	if app.Width == 0 {
		app.Width = width
	}
	if app.Height == 0 {
		app.Height = height
	}
	focus := nameField
	if app.URL == "" {
		focus = addressField
	}
	d.draft = editor{open: true, focusField: focus, original: app, app: app, width: strconv.Itoa(app.Width), height: strconv.Itoa(app.Height)}
	d.previewIcon(0)
}

func (d *desktopApp) presetArtwork(address string) *artwork {
	if preset, ok := catalog.ByURL(d.presets, address); ok {
		return d.presetIcon(preset.Mark)
	}
	return nil
}

func (d *desktopApp) presetIcon(mark string) *artwork { return d.presetArt[mark]() }

func (d *desktopApp) localArt(app profile.App) *artwork {
	if custom := d.customIcons.Get(app.CustomIcon); custom != nil {
		return art(custom)
	}
	if preset := d.presetArtwork(app.URL); preset != nil {
		return preset
	}
	if image := d.resolver.Cached(app.URL); image != nil {
		return siteArt(image)
	}
	return placeholderArtwork(app)
}

func placeholderArtwork(app profile.App) *artwork {
	name := app.Name
	if strings.TrimSpace(name) == "" {
		name = profile.Host(app.URL)
	}
	return displayArtwork(icons.Artwork{Image: icons.Fallback(name, app.Color), Kind: icons.PlaceholderArtwork})
}

func (e *editor) keepsInstalledArt(app profile.App) bool {
	return e.original.ID != "" && !e.custom && app.CustomIcon == e.original.CustomIcon &&
		(app.CustomIcon != "" || (app.UsesAutomaticIcon(e.original.URL) && app.Color == e.original.Color))
}

func (e *editor) showIssue(issue formIssue) {
	e.issue, e.focusField = issue, issue.field
	if issue.field == widthField || issue.field == heightField {
		e.sizeOpen = true
	}
}

func (d *desktopApp) save() {
	e := &d.draft
	if e.busy || e.picking {
		return
	}
	app := e.app
	width, height, issue := readWindowSize(e.width, e.height)
	if issue.message != "" {
		e.showIssue(issue)
		return
	}
	app.Width, app.Height = width, height
	useDefaultName := strings.TrimSpace(app.Name) == ""
	app, err := profile.Normalize(app)
	if err != nil {
		f := noField
		switch err {
		case profile.ErrAddress:
			f = addressField
		case profile.ErrName:
			f = nameField
		}
		e.showIssue(formIssue{f, err.Error()})
		return
	}
	if useDefaultName {
		app.Name = ""
	}
	suggestedName := e.websiteName
	e.busy, e.issue = true, formIssue{}
	chosen, lookup := e.icon, e.lookup
	changedHome := app.URL != e.original.URL
	keepIcon := e.keepsInstalledArt(app) && (lookup == nil || lookup.result == nil || d.savedLookups[app.ID] == lookup)
	if keepIcon {
		if current := d.appArt.get(app.ID); current != nil {
			chosen = current
		}
	}
	icon := library.Icon{Artwork: chosen.stored(), Custom: e.custom, KeepInstalled: keepIcon}
	readCached := !keepIcon && !e.custom && app.CustomIcon == "" && (chosen == nil || chosen.kind == icons.PlaceholderArtwork)
	d.work(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var cached image.Image
		if readCached {
			icon.CachedArtwork = func(app profile.App) icons.Artwork {
				cached = d.resolver.Cached(app.URL)
				return icons.Artwork{Image: cached, Kind: icons.WebsiteArtwork}
			}
		}
		saved, err := d.library.SaveWithSuggestedName(ctx, app, icon, suggestedName)
		if err == nil && cached != nil {
			chosen = siteArt(cached)
		}
		d.dispatch(func() {
			e.busy = false
			var duplicate *library.AlreadyExistsError
			if errors.As(err, &duplicate) {
				if app.ID != "" {
					e.showIssue(formIssue{nameField, duplicate.Error() + "，请更改名称"})
					return
				}
				d.clearAddressLookup()
				d.clearDraftLookup()
				d.draft = editor{}
				d.page, d.query, d.appsQuery = 1, "", ""
				existing := duplicate.App
				if i := slices.IndexFunc(d.apps, func(app profile.App) bool { return app.ID == existing.ID }); i < 0 || d.apps[i] != existing {
					d.record(existing, nil)
				}
				d.focusSavedApp(existing.ID, true)
				d.notice = "「" + duplicate.App.Name + "」已在应用库中"
				return
			}
			if err != nil {
				log.Printf("save %s: %v", app.ID, err)
				e.open = true
				e.showIssue(formIssue{message: "无法保存应用，请重试"})
				return
			}
			d.clearAddressLookup()
			if cached != nil || icon.Custom {
				d.clearDraftLookup()
				lookup = nil
			}
			d.draft = editor{}
			d.page = 1
			if keepIcon {
				chosen = d.appArt.get(saved.ID)
			}
			d.record(saved, chosen)
			created := app.ID == ""
			if created {
				d.query, d.appsQuery = "", ""
				d.notice = "已创建「" + saved.Name + "」"
			} else if !saved.UpdatedAt.Equal(app.UpdatedAt) {
				changedSize := saved.WindowRevision != app.WindowRevision
				switch {
				case changedHome && changedSize:
					d.notice = "首页已保存，新尺寸下次启动时生效"
				case changedHome:
					d.notice = "已保存首页"
				case changedSize:
					d.notice = "重新打开应用后使用新尺寸"
				default:
					d.notice = "已保存「" + saved.Name + "」"
				}
			}
			d.focusSavedApp(saved.ID, created)
			d.keepSavedLookup(saved, lookup, chosen)
		})
	})
}

func (d *desktopApp) record(app profile.App, icon *artwork) {
	d.appArt.set(app.ID, icon)
	d.stopArtworkRead(app.ID)
	existing := slices.IndexFunc(d.apps, func(item profile.App) bool { return item.ID == app.ID })
	if existing >= 0 && d.apps[existing] == app {
		return
	}
	d.libraryRevision++
	d.items, d.filtered = nil, nil
	if existing >= 0 {
		d.apps[existing] = app
		return
	}
	index := slices.IndexFunc(d.apps, func(other profile.App) bool { return other.CreatedAt.Before(app.CreatedAt) })
	if index < 0 {
		index = len(d.apps)
	}
	d.apps = slices.Insert(d.apps, index, app)
}

func (d *desktopApp) open(app profile.App, reveal bool) {
	if _, busy := d.busyApps[app.ID]; busy {
		return
	}
	preview := d.appArt.get(app.ID)
	icon := func(saved profile.App) icons.Artwork {
		if preview != nil && saved == app {
			return preview.stored()
		}
		return d.localArt(saved).stored()
	}
	status := "正在打开…"
	if reveal {
		status = "正在定位…"
	}
	d.busyApps[app.ID] = status
	d.work(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var path string
		var err error
		if reveal {
			path, err = d.library.Path(ctx, app, icon)
		} else {
			err = d.library.Open(ctx, app, icon)
		}
		d.dispatch(func() {
			delete(d.busyApps, app.ID)
			if err != nil {
				d.fail("无法打开「"+app.Name+"」", err)
				return
			}
			if reveal && d.reveal != nil {
				d.reveal(path)
			}
		})
	})
}

func (d *desktopApp) remove() {
	app := d.removeApp
	d.removeOpen = false
	if _, busy := d.busyApps[app.ID]; busy {
		return
	}
	d.busyApps[app.ID] = "正在移除…"
	d.work(func() {
		err := d.library.Remove(app.ID)
		d.dispatch(func() {
			delete(d.busyApps, app.ID)
			if err != nil {
				d.fail("无法移除「"+app.Name+"」，请重试", err)
				return
			}
			d.libraryRevision++
			d.apps = slices.DeleteFunc(d.apps, func(item profile.App) bool { return item.ID == app.ID })
			d.items, d.filtered = nil, nil
			d.appArt.remove(app.ID)
			d.stopArtworkRead(app.ID)
			d.cancelSavedLookup(app.ID)
			d.notice = "已移除「" + app.Name + "」"
		})
	})
}

func (d *desktopApp) fail(message string, err error) {
	log.Printf("%s: %v", message, err)
	d.notice = message
}
