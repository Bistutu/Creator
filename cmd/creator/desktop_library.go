package main

import (
	"log"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/search"
)

type libraryItem struct {
	app     profile.App
	title   string
	host    string
	address string
	match   string
}

type libraryFocus struct {
	id     string
	center bool
}

func (d *desktopApp) moveLibraryFocus(items []libraryItem, start, step int) bool {
	for i := start; i >= 0 && i < len(items); i += step {
		id := items[i].app.ID
		if _, busy := d.busyApps[id]; !busy {
			d.focusLibraryApp = libraryFocus{id: id}
			return true
		}
	}
	return false
}

func (d *desktopApp) libraryRowKeys(c *ui.Context, button, search ui.Element, items []libraryItem, index int) {
	if d.draft.open || d.removeOpen {
		return
	}
	// The button may receive focus after this row is built. Register its
	// shortcuts before then so the next key already has a destination.
	switch {
	case button.Shortcut(0, ui.KeyEscape):
		search.Focus()
	case button.Shortcut(0, ui.KeyUp):
		if d.moveLibraryFocus(items, index-1, -1) {
			c.AnimationFrame()
		} else {
			search.Focus()
		}
	case button.Shortcut(0, ui.KeyDown):
		if d.moveLibraryFocus(items, index+1, 1) {
			c.AnimationFrame()
		}
	}
}

// focusSavedApp keeps the current filter when the saved app still matches it.
func (d *desktopApp) focusSavedApp(id string, center bool) {
	if d.appsQuery != "" && !slices.ContainsFunc(d.libraryItems(), func(item libraryItem) bool { return item.app.ID == id }) {
		d.appsQuery = ""
	}
	d.focusLibraryApp = libraryFocus{id: id, center: center}
}

func (d *desktopApp) loadLibrary() {
	if d.loadingLibrary {
		return
	}
	d.loadingLibrary, d.libraryLoadFailed = true, false
	revision := d.libraryRevision
	restore := d.afterLibraryLoad
	d.work(func() {
		apps, err := d.library.List()
		var items []libraryItem
		var snapshot []profile.App
		if err != nil {
			log.Printf("无法读取应用库: %v", err)
		} else {
			items = indexLibrary(apps)
			if restore != nil {
				snapshot = slices.Clone(apps)
			}
		}
		d.dispatch(func() {
			d.loadingLibrary = false
			// A save or removal may have reached the window after the read.
			if revision != d.libraryRevision {
				d.loadLibrary()
				return
			}
			if err != nil {
				d.libraryLoadFailed = true
				return
			}
			d.apps, d.items, d.filtered = apps, items, nil
			if restore != nil {
				d.afterLibraryLoad = nil
				d.work(func() { restore(snapshot) })
			}
		})
	})
}

func (d *desktopApp) libraryItems() []libraryItem {
	if d.items == nil {
		d.items = indexLibrary(d.apps)
	}
	query := strings.ToLower(strings.TrimSpace(d.appsQuery))
	if query == "" {
		return d.items
	}
	if d.filtered != nil && d.filterQuery == query {
		return d.filtered
	}
	d.filterQuery = query
	d.filtered = search.Filter(d.items, query, func(item libraryItem) string { return item.match })
	return d.filtered
}

func indexLibrary(apps []profile.App) []libraryItem {
	type label struct{ name, host string }
	type addressGroup struct {
		firstURL  string
		ambiguous bool
	}
	used := make(map[label]int, len(apps))
	addresses := make(map[string]addressGroup, len(apps))
	items := make([]libraryItem, len(apps))
	for i, app := range apps {
		host, address := profile.AddressSummary(app.URL)
		items[i] = libraryItem{app: app, title: app.Name, host: host, address: address}
		used[label{strings.ToLower(app.Name), host}] = 0
		if group, seen := addresses[address]; !seen {
			addresses[address] = addressGroup{firstURL: app.URL}
		} else if group.firstURL != app.URL && !group.ambiguous {
			group.ambiguous = true
			addresses[address] = group
		}
	}
	// Give the oldest app the original label. Reserve all literal names so
	// an existing app named e.g. "DeepSeek (2)" keeps its own label.
	for i := len(items) - 1; i >= 0; i-- {
		item := &items[i]
		// Keep the compact label unless it would make two destinations look the same.
		if addresses[item.address].ambiguous {
			item.address = item.app.URL
		}
		key := label{strings.ToLower(item.title), item.host}
		if used[key] == 0 {
			used[key] = 1
		} else {
			for copyNumber := used[key] + 1; ; copyNumber++ {
				suffix := " (" + strconv.Itoa(copyNumber) + ")"
				copyKey := label{key.name + suffix, key.host}
				if _, taken := used[copyKey]; !taken {
					item.title += suffix
					used[key], used[copyKey] = copyNumber, 1
					break
				}
			}
		}
		item.match = strings.ToLower(item.title + " " + item.app.URL)
	}
	return items
}

func (d *desktopApp) visibleArtwork(app profile.App, requests *[]profile.App) *artwork {
	if icon := d.appArt.get(app.ID); icon != nil {
		return icon
	}
	if _, pending := d.pendingArt[app.ID]; requests != nil && !pending {
		*requests = append(*requests, app)
	}
	if app.CustomIcon == "" {
		return d.presetArtwork(app.URL)
	}
	return nil
}

type loadedArtwork struct {
	installed   *artwork
	replacement *artwork
}

type artworkRead struct {
	app     profile.App
	picture loadedArtwork
	busy    <-chan struct{}
	stopped chan struct{}
}

func (d *desktopApp) savedArtwork(app profile.App, stopped <-chan struct{}) (loadedArtwork, <-chan struct{}) {
	installed, busy := d.library.TryInstalledArtwork(app, stopped)
	select {
	case <-stopped:
		return loadedArtwork{}, nil
	default:
	}
	if busy != nil {
		return loadedArtwork{}, busy
	}
	if installed.Image == nil {
		return loadedArtwork{installed: d.localArt(app)}, nil
	}
	if app.CustomIcon != "" {
		return loadedArtwork{installed: displayArtwork(installed)}, nil
	}
	if installed.Kind != icons.WebsiteArtwork && installed.Kind != icons.PlaceholderArtwork {
		// Preserve older installed artwork. Only upgrade the generated
		// placeholder when it can be identified from the pixels.
		installed.Kind = icons.WebsiteArtwork
		if icons.MatchesPlaceholder(installed.Image, app.Color) {
			installed.Kind = icons.PlaceholderArtwork
		}
	}
	loaded := loadedArtwork{installed: displayArtwork(installed)}
	if installed.Kind == icons.PlaceholderArtwork {
		if preset := d.presetArtwork(app.URL); preset != nil {
			loaded.replacement = preset
		} else if picture := d.resolver.Cached(app.URL); picture != nil {
			loaded.replacement = siteArt(picture)
		}
	}
	return loaded, nil
}

func (d *desktopApp) loadVisibleArtwork(requests []profile.App) {
	// A grid can realize more rows during layout, after this batch was
	// collected. Mark only the reads that will actually run as pending.
	queue := make([]artworkRead, 0, len(requests))
	for _, app := range requests {
		if _, pending := d.pendingArt[app.ID]; !pending {
			queue = append(queue, artworkRead{app: app, stopped: make(chan struct{})})
			d.pendingArt[app.ID] = &queue[len(queue)-1]
		}
	}
	if len(queue) == 0 {
		return
	}
	d.work(func() {
		var needed bool
		for i := range queue {
			read := &queue[i]
			select {
			case <-read.stopped:
				continue
			default:
			}
			needed = true
			read.picture, read.busy = d.savedArtwork(read.app, read.stopped)
		}
		if !needed {
			return
		}
		d.dispatch(func() {
			for i := range queue {
				d.applyArtworkRead(&queue[i])
			}
		})
	})
}

func (d *desktopApp) applyArtworkRead(read *artworkRead) {
	app := read.app
	if d.pendingArt[app.ID] != read {
		return
	}
	if read.busy != nil {
		d.work(func() {
			select {
			case <-read.stopped:
				return
			case <-read.busy:
			}
			d.dispatch(func() {
				if d.pendingArt[app.ID] == read {
					// The next frame only queues a read if this row or
					// its editor still needs the artwork.
					d.stopArtworkRead(app.ID)
					d.refreshDraftArtwork(app)
				}
			})
		})
		return
	}
	d.stopArtworkRead(app.ID)
	if d.appArt.get(app.ID) != nil {
		return
	}
	picture := read.picture
	d.appArt.set(app.ID, picture.installed)
	if picture.replacement != nil && d.savedLookups[app.ID] == nil {
		lookup := &iconLookup{url: app.URL, result: picture.replacement, finished: true}
		d.keepSavedLookup(app, lookup, picture.installed)
	}
	d.refreshDraftArtwork(app)
}

func (d *desktopApp) stopArtworkRead(id string) {
	if read := d.pendingArt[id]; read != nil {
		delete(d.pendingArt, id)
		close(read.stopped)
	}
}

func (d *desktopApp) stopHiddenArtworkReads() {
	for id := range d.pendingArt {
		if _, visible := d.appArt.visible[id]; !visible {
			d.stopArtworkRead(id)
		}
	}
}

func (d *desktopApp) refreshDraftArtwork(app profile.App) {
	draft := &d.draft
	if draft.open && !draft.busy && draft.original == app && draft.keepsInstalledArt(draft.app) {
		d.previewIcon(0)
	}
}
