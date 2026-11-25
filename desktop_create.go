package main

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/Bistutu/Creator/internal/catalog"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

type addressPreview struct {
	query        string
	app          profile.App
	title        string
	websiteName  string
	icon         *artwork
	cacheKey     string
	cacheChecked bool
	lookup       *iconLookup
}

type addressCacheRead struct {
	cancel context.CancelFunc
}

const addressCardKey = "website"

var websiteDropOptions = transfer.DropOptions{Formats: []transfer.Format{transfer.URIList}, Operations: transfer.Copy}

func (d *desktopApp) receiveWebsiteLink(data transfer.Data) {
	urls, err := data.URLs()
	if err != nil || len(urls) != 1 || !strings.Contains(urls[0], "://") {
		return
	}
	if _, err := profile.ParseAddress(urls[0]); err != nil {
		return
	}
	d.page = 0
	d.query = urls[0]
	d.receivedAddress = d.query
	d.searchError = ""
	d.focusCreateInput = true
	d.focusCreateResult = false
}

func (d *desktopApp) previewAddress() addressPreview {
	query := strings.TrimSpace(d.query)
	if d.address.query == query {
		return d.address
	}
	previous := d.address
	preview := addressPreview{query: query}
	address, err := profile.ParseAddress(query)
	if err != nil {
		d.clearAddressLookup()
		d.address = preview
		return preview
	}
	preview.app = profile.App{URL: address.String()}
	preview.title = profile.Host(preview.app.URL)
	if preset, ok := catalog.ByURL(d.presets, preview.app.URL); ok {
		preview.app.Name, preview.app.Category = preset.Name, preset.Category
		if strings.EqualFold(query, preset.Host()) {
			preview.app = preset.App()
		}
		preview.title, preview.icon = preset.Name, d.presetIcon(preset.Mark)
	} else {
		preview.cacheKey = profile.ArtworkAddress(preview.app.URL)
		if previous.cacheKey == preview.cacheKey {
			preview.icon, preview.lookup = previous.icon, previous.lookup
			preview.websiteName, preview.cacheChecked = previous.websiteName, previous.cacheChecked
		} else if previous.title == preview.title && previous.icon != nil && previous.icon.kind == icons.PlaceholderArtwork {
			preview.icon = previous.icon
		}
	}
	if preview.icon == nil {
		preview.icon = placeholderArtwork(preview.app)
	}
	if previous.cacheKey != preview.cacheKey {
		d.clearAddressLookup()
	}
	d.address = preview
	return preview
}

func (d *desktopApp) loadAddressArtwork(received bool) {
	key := d.address.cacheKey
	if key == "" || d.draft.open {
		return
	}
	lookup := d.address.lookup
	if (lookup == nil || lookup.finished) && (d.activeLookups[key] != nil || received && d.address.icon.kind == icons.PlaceholderArtwork) {
		d.stopAddressCacheRead()
		d.address.lookup = d.lookupFor(d.address.app.URL)
		d.startIconLookup(d.address.lookup, 0)
		return
	}
	if lookup != nil || d.address.cacheChecked || d.addressRead != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	read := &addressCacheRead{cancel: cancel}
	d.addressRead = read
	d.work(func() {
		// Let a new address read while an old disk call unwinds, with a
		// fixed limit on reads already inside a filesystem operation.
		select {
		case d.addressReadSlots <- struct{}{}:
			defer func() { <-d.addressReadSlots }()
		case <-ctx.Done():
			return
		}
		if ctx.Err() != nil {
			return
		}
		name := d.resolver.CachedName(key)
		if ctx.Err() != nil {
			return
		}
		cached := d.resolver.Cached(key)
		if ctx.Err() != nil {
			return
		}
		var picture *artwork
		if cached != nil {
			picture = siteArt(cached)
		}
		d.dispatch(func() {
			if d.addressRead != read {
				return
			}
			d.stopAddressCacheRead()
			if !d.address.cacheChecked {
				d.address.cacheChecked = true
				d.address.websiteName = name
				if picture != nil {
					d.address.icon = picture
				}
			}
		})
	})
}

func (d *desktopApp) stopAddressCacheRead() {
	if read := d.addressRead; read != nil {
		d.addressRead = nil
		read.cancel()
	}
}

func (d *desktopApp) createPage(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Gap(9).Children(func() {
		search, cleared := searchBox(c, &d.query, "网址或应用名称", "粘贴网址或搜索站点", func() {
			d.edit(profile.App{})
		})
		d.focusInitialControl(search)
		if d.focusCreateInput {
			search.Focus().SetTextSelection(0, utf8.RuneCountInString(d.query))
			d.focusCreateInput = false
		}
		search.OnPaste(func(text string) bool {
			d.receivedAddress = strings.TrimSpace(text)
			return false
		}).OnTextKey(func(key ui.TextKey) bool {
			if key.Key != ui.KeyDown || key.Mods != 0 || d.draft.open {
				return false
			}
			d.focusCreateResult = true
			return true
		})
		if search.Changed() || cleared {
			d.searchError = ""
		}
		if search.Submitted() {
			d.submitSearch()
		}
		focusFirst := d.focusCreateResult
		d.focusCreateResult = false
		var returnToCard string
		if !d.draft.open {
			returnToCard, d.draft.returnToCard = d.draft.returnToCard, ""
		}
		if d.searchError != "" {
			ui.Text(c, d.searchError).FontSize(12).TextColor(t.Danger)
		}
		preview := d.previewAddress()
		// Load the site's artwork after a whole address is pasted or dropped.
		received := d.receivedAddress == preview.query
		d.receivedAddress = ""
		query := d.query
		prior := search
		if preview.app.URL != "" {
			name := preview.websiteName
			if name == "" {
				name = preview.title
			}
			button := card(c.Key(addressCardKey), "使用网址 "+name).Height(52).WidthPercent(100).Padding(0, 10).
				Justify(ui.Start).Description(preview.app.URL)
			if !button.Hovered() && !button.Focused() {
				button.Background(t.Surface)
			}
			button.Children(func() {
				showIcon(c, preview.icon, 32)
				cardTitle(c, name, preview.app.URL)
				ui.Text(c, "›").FontSize(19).TextColor(t.TextMuted)
			})
			if button.Clicked() {
				d.edit(preview.app)
				d.draft.returnToCard = addressCardKey
			}
			if !d.draft.open {
				up := button.Shortcut(0, ui.KeyUp)
				down := button.Shortcut(0, ui.KeyDown)
				escape := button.Shortcut(0, ui.KeyEscape)
				switch {
				case focusFirst || returnToCard == addressCardKey:
					button.Focus()
					focusFirst = false
				case up || escape:
					search.Focus()
				case down:
					focusFirst = true
				}
			}
			prior = button
			d.loadAddressArtwork(received)
			query = ""
		}
		ui.Row(c).Gap(5).FocusGroup(ui.Horizontal).Children(func() {
			for i, label := range categories {
				button := ui.Button(c.Key(label), label).FontSize(12).Height(25).Padding(0, 9).Radius(buttonRadius).
					Background(ui.Transparent).Border(0, ui.Transparent).TextColor(t.TextMuted).Transition(controlMotion)
				if i == d.category {
					button.Background(t.SurfacePressed).TextColor(t.Accent).Selected(true)
				} else if button.Hovered() {
					button.Background(t.Surface).TextColor(t.Text)
				}
				if button.Clicked() {
					d.category = i
					d.searchError = ""
				}
			}
		})
		category := d.selectedCategory()
		sites := catalog.Filter(d.presets, category, query)
		if len(sites) == 0 {
			if d.searchError == "" {
				empty(c, "没有匹配的网站", "")
			}
			return
		}
		d.recommendationGrid(c.Key(category), sites, search, prior, focusFirst, returnToCard)
	})
}

func (d *desktopApp) recommendationGrid(c *ui.Context, sites []catalog.Preset, search, prior ui.Element, focusFirst bool, returnToCard string) {
	width, _ := c.Size()
	const gap = 5
	columns := max(1, int((width-2*pagePadding+gap)/(76+gap)))
	scroll := ui.Scroll(c).Grow(1)
	type scrollPosition struct {
		query string
		view  ui.ScrollState
	}
	position := ui.Local(scroll, "position", func() scrollPosition { return scrollPosition{} })
	// The URL preview can change while the recommendation set stays the same.
	if position.query != d.query {
		position.query, position.view.Y = d.query, 0
	}
	scroll.TrackScroll(&position.view).Children(func() {
		cards := make([]ui.Element, len(sites))
		focusAt := -1
		if focusFirst {
			focusAt = 0
		}
		// Tab enters the grid in one step. The layout's column count also
		// determines which card is above or below the focused card.
		grid := ui.Grid(c).Columns(columns).Gap(gap).FocusGroup(ui.Horizontal | ui.Vertical)
		grid.Children(func() {
			for i, site := range sites {
				button := card(c.Key(site.Mark), "选择 "+site.Name).Column().Height(75).Gap(5).
					Padding(4, 2).Justify(ui.Center).Description(site.URL)
				cards[i] = button
				if site.Mark == returnToCard {
					focusAt = i
				}
				button.Children(func() {
					showIcon(c, d.presetIcon(site.Mark), 46)
					ui.Text(c, site.Name).WidthPercent(100).TextAlign(ui.Center).FontSize(11).SingleLine().Ellipsis("…").
						Tooltip(site.Name + " · " + site.Host())
				})
				if button.Clicked() {
					d.edit(site.App())
					d.draft.returnToCard = site.Mark
				}
			}
		})
		if !d.draft.open {
			focusRecommendations(grid, cards, columns, search, prior, focusAt)
		}
	})
}

func focusRecommendations(grid ui.Element, cards []ui.Element, columns int, search, prior ui.Element, focusAt int) {
	left := grid.Shortcut(0, ui.KeyLeft)
	right := grid.Shortcut(0, ui.KeyRight)
	up := grid.Shortcut(0, ui.KeyUp)
	down := grid.Shortcut(0, ui.KeyDown)
	escape := grid.Shortcut(0, ui.KeyEscape)
	if focusAt >= 0 {
		cards[focusAt].Focus()
		return
	}
	if escape {
		search.Focus()
		return
	}
	if !left && !right && !up && !down {
		return
	}
	for i, button := range cards {
		if !button.Focused() {
			continue
		}
		switch {
		case left && i%columns > 0:
			cards[i-1].Focus()
		case right && i%columns < columns-1 && i+1 < len(cards):
			cards[i+1].Focus()
		case up && i < columns:
			prior.Focus()
		case up:
			cards[i-columns].Focus()
		case down && i/columns < (len(cards)-1)/columns:
			cards[min(i+columns, len(cards)-1)].Focus()
		}
		return
	}
}

// submitSearch opens an unambiguous result.
func (d *desktopApp) submitSearch() {
	d.searchError = ""
	query := strings.TrimSpace(d.query)
	if query == "" {
		d.edit(profile.App{})
		return
	}
	if preview := d.previewAddress(); preview.app.URL != "" {
		d.edit(preview.app)
		return
	}
	matches := catalog.Filter(d.presets, d.selectedCategory(), query)
	for _, preset := range matches {
		if strings.EqualFold(query, preset.Name) {
			d.edit(preset.App())
			return
		}
	}
	if len(matches) == 1 {
		d.edit(matches[0].App())
	} else if len(matches) == 0 {
		d.searchError = "请输入有效的网址"
	}
}

func (d *desktopApp) selectedCategory() string {
	if d.category == 0 {
		return ""
	}
	return categories[d.category]
}
