package main

import (
	"context"
	"errors"
	"image"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/Bistutu/Creator/internal/catalog"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

type iconLookup struct {
	url      string
	name     string
	cancel   context.CancelFunc
	result   *artwork
	finished bool
	saved    map[string]profile.App
}

func (d *desktopApp) lookupFor(address string) *iconLookup {
	if lookup := d.activeLookups[profile.ArtworkAddress(address)]; lookup != nil {
		return lookup
	}
	return &iconLookup{url: address}
}

func (d *desktopApp) clearDraftLookup() {
	lookup := d.draft.lookup
	d.draft.lookup = nil
	d.cancelUnusedLookup(lookup)
}

func (d *desktopApp) clearAddressLookup() {
	d.stopAddressCacheRead()
	lookup := d.address.lookup
	d.address.lookup = nil
	d.cancelUnusedLookup(lookup)
}

// A lookup can serve the address card, the editor and several installed apps.
func (d *desktopApp) cancelUnusedLookup(lookup *iconLookup) {
	if lookup == nil || len(lookup.saved) != 0 || lookup == d.address.lookup || lookup == d.draft.lookup {
		return
	}
	key := profile.ArtworkAddress(lookup.url)
	if d.activeLookups[key] == lookup {
		delete(d.activeLookups, key)
		lookup.cancel()
	}
}

func (d *desktopApp) cancelSavedLookup(id string) {
	if lookup := d.savedLookups[id]; lookup != nil {
		delete(d.savedLookups, id)
		delete(lookup.saved, id)
		d.cancelUnusedLookup(lookup)
	}
}

func (d *desktopApp) keepSavedLookup(app profile.App, lookup *iconLookup, chosen *artwork) {
	previous := d.savedLookups[app.ID]
	if previous != nil && !app.UsesAutomaticIcon(previous.url) {
		d.cancelSavedLookup(app.ID)
		previous = nil
	}
	if lookup == nil || lookup == previous || !app.UsesAutomaticIcon(lookup.url) {
		return
	}
	if lookup.finished && (lookup.result == nil || lookup.result == chosen) {
		return
	}
	d.cancelSavedLookup(app.ID)
	if lookup.saved == nil {
		lookup.saved = make(map[string]profile.App)
	}
	lookup.saved[app.ID] = app
	d.savedLookups[app.ID] = lookup
	if lookup.finished {
		d.updateIcons(lookup, []profile.App{app})
	}
}

func (d *desktopApp) previewIcon(delay time.Duration) {
	e := &d.draft
	app := e.app
	address, err := profile.ParseAddress(app.URL)
	if err == nil {
		app.URL = address.String()
	}
	artworkAddress := profile.ArtworkAddress(app.URL)
	if e.websiteKey != artworkAddress {
		e.websiteKey, e.websiteName = artworkAddress, ""
	}
	preset, known := catalog.ByURL(d.presets, app.URL)
	if err == nil && app.ID == "" {
		if known {
			e.websiteName = preset.Name
		} else if d.address.cacheKey == artworkAddress && d.address.websiteName != "" {
			e.websiteName = d.address.websiteName
		}
	}
	if e.lookup != nil && (err != nil || profile.ArtworkAddress(e.lookup.url) != artworkAddress) {
		d.clearDraftLookup()
	}
	if err == nil && app.ID == "" && e.lookup == nil && d.address.cacheKey == artworkAddress {
		e.lookup = d.address.lookup
	}
	if e.custom {
		if err == nil && app.ID == "" && e.websiteName == "" && strings.TrimSpace(app.Name) == "" {
			d.lookupDraftWebsite(app.URL, delay)
		}
		return
	}
	if previous := d.savedLookups[app.ID]; err == nil && e.lookup == nil && previous != nil && app.UsesAutomaticIcon(previous.url) {
		e.lookup = previous
	}
	if e.lookup != nil && e.lookup.result != nil {
		e.icon = e.lookup.result
		return
	}
	if e.keepsInstalledArt(app) {
		d.appArt.show(app.ID)
		var requests []profile.App
		e.icon = d.visibleArtwork(e.original, &requests)
		if d.appArt.get(app.ID) == nil {
			if e.icon == nil {
				e.icon = placeholderArtwork(app)
			}
			d.loadVisibleArtwork(requests)
			return
		}
	} else if err == nil && known {
		e.icon = d.presetIcon(preset.Mark)
	} else if app.ID == "" && d.address.cacheKey != "" && d.address.cacheKey == artworkAddress &&
		d.address.icon.kind == icons.WebsiteArtwork {
		e.icon = d.address.icon
	} else {
		e.icon = placeholderArtwork(app)
	}
	if err != nil || e.icon.kind == icons.WebsiteArtwork || app.CustomIcon != "" {
		return
	}
	d.lookupDraftWebsite(app.URL, delay)
}

func (d *desktopApp) lookupDraftWebsite(address string, delay time.Duration) {
	if d.draft.lookup == nil {
		lookup := d.lookupFor(address)
		d.draft.lookup = lookup
		if d.page == 0 && d.draft.app.ID == "" && d.address.cacheKey == profile.ArtworkAddress(address) {
			d.address.lookup = lookup
		}
		d.startIconLookup(lookup, delay)
	}
}

func (d *desktopApp) startIconLookup(request *iconLookup, delay time.Duration) {
	if request.cancel != nil {
		if request.name != "" {
			d.applyWebsiteName(request, request.name)
		}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	request.cancel = cancel
	address := request.url
	key := profile.ArtworkAddress(address)
	d.activeLookups[key] = request
	d.work(func() {
		defer cancel()
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		img, err := d.resolver.Inspect(ctx, address, func(name string) {
			d.dispatch(func() {
				if d.activeLookups[key] == request {
					d.applyWebsiteName(request, name)
				}
			})
		})
		var result *artwork
		if err == nil && img != nil && ctx.Err() == nil {
			result = siteArt(img)
		}
		d.dispatch(func() {
			if d.activeLookups[key] != request {
				return
			}
			delete(d.activeLookups, key)
			request.result, request.finished = result, true
			onCard := d.address.lookup == request
			inEditor := d.draft.open && d.draft.lookup == request
			if onCard {
				d.address.cacheChecked = true
			}
			if result != nil && (onCard || inEditor || len(request.saved) != 0) && d.address.cacheKey == key {
				d.address.icon, d.address.cacheChecked = result, true
			}
			if inEditor && !d.draft.custom && result != nil {
				d.draft.icon = result
			}
			if result == nil {
				for id := range request.saved {
					d.cancelSavedLookup(id)
				}
			} else if len(request.saved) != 0 {
				d.updateIcons(request, slices.Collect(maps.Values(request.saved)))
			}
		})
	})
}

func (d *desktopApp) applyWebsiteName(request *iconLookup, name string) {
	request.name = name
	key := profile.ArtworkAddress(request.url)
	e := &d.draft
	inEditor := e.open && e.lookup == request && e.websiteKey == key
	if (d.address.lookup == request || inEditor) && d.address.cacheKey == key {
		d.address.websiteName = name
	}
	if inEditor && e.app.ID == "" {
		e.websiteName = name
	}
}

func (d *desktopApp) updateIcons(lookup *iconLookup, apps []profile.App) {
	updated := lookup.result
	d.work(func() {
		for _, saved := range apps {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			applied, err := d.library.RefreshIcon(ctx, saved, updated.img)
			cancel()
			if err != nil {
				log.Printf("refresh icon %s: %v", saved.ID, err)
			}
			d.dispatch(func() {
				if d.savedLookups[saved.ID] != lookup {
					return
				}
				d.cancelSavedLookup(saved.ID)
				if !applied {
					return
				}
				for _, app := range d.apps {
					if app.ID == saved.ID && app.UsesAutomaticIcon(saved.URL) {
						d.appArt.set(app.ID, updated)
						break
					}
				}
			})
		}
	})
}

var openAppIconDialog = mygo.Dialog.Open

func chooseAppIcon(parent *mygo.Window) (image.Image, error) {
	paths, err := openAppIconDialog(mygo.OpenDialogOptions{
		Parent: parent, Title: "选择应用图标", ButtonLabel: "使用图片",
		Filters: []mygo.FileFilter{{
			Name: "图片", Extensions: []string{"png", "jpg", "jpeg", "webp", "gif", "ico", "svg"},
		}},
	})
	if err != nil {
		return nil, errors.New("无法打开图片选择窗口")
	}
	if len(paths) == 0 {
		return nil, nil
	}
	return icons.ImportFile(paths[0], rasterizeSVG)
}

func (d *desktopApp) iconButton(c *ui.Context) {
	e := &d.draft
	t := c.Theme()
	disabled := e.busy || e.picking || d.chooseIcon == nil
	button := ui.ButtonBase(c).Label("更换图标").Width(44).Height(44).
		AlignItems(ui.Center).Justify(ui.Center).Radius(cardRadius).Background(ui.Transparent).
		Disabled(disabled).Transition(controlMotion)
	if button.Hovered() {
		button.Background(t.SurfaceHover)
	}
	button.Children(func() {
		icon := showIcon(c, e.icon, 42).AccessHidden(true)
		if !disabled {
			icon.Tooltip("更换图标")
		}
		ui.Box(c).Absolute().Right(0).Bottom(0).Width(16).Height(16).Radius(5).
			Background(t.Surface).Border(1, t.Background).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Text(c, "✎").FontSize(12).FontWeight(600).TextColor(t.Text)
		})
	})
	if button.Clicked() {
		d.selectIcon()
	}
}

func (d *desktopApp) selectIcon() {
	e := &d.draft
	e.picking = true
	d.work(func() {
		picked, err := d.chooseIcon()
		d.dispatch(func() {
			e.picking = false
			if !e.open {
				return
			}
			if err != nil {
				e.showIssue(formIssue{message: err.Error()})
				return
			}
			if picked == nil {
				return
			}
			if e.app.ID != "" {
				d.clearDraftLookup()
			}
			e.icon, e.custom, e.app.CustomIcon = art(picked), true, ""
			e.issue.fieldChanged(noField)
		})
	})
}
