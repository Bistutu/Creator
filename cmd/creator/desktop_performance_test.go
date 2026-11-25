package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/catalog"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

type cachedLibraryIcons struct {
	testingIcons
	reads []string
}

func (i *cachedLibraryIcons) Cached(address string) image.Image {
	i.reads = append(i.reads, address)
	return i.img
}

type libraryListProbe struct {
	appLibrary
	list func() ([]profile.App, error)
}

func (p libraryListProbe) List() ([]profile.App, error) { return p.list() }

func TestNativeFirstWindowAcceptsAPastedAddress(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	const address = "https://www.xiaohongshu.com/explore/topic?source=creator#notes"
	pasteAddress(tt, address)
	if d.query != address || !tt.Focused("网址或应用名称") || !tt.HasText("使用网址 小红书") {
		t.Fatalf("pasting into the new window did not reach the URL input: %q (%v)", d.query, tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if !tt.Focused("应用名称") || d.draft.app.URL != address {
		t.Fatalf("Enter did not open the form for the pasted URL: %+v", d.draft.app)
	}
	tt.Key(0, ui.KeyEnter)
	if !tt.Focused("打开 小红书") || len(d.apps) != 1 || d.apps[0].URL != address {
		t.Fatalf("Enter did not create and reveal the saved address: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if filepath.Base(backend.opened) != "小红书.fixture" {
		t.Fatalf("Enter did not open the installed website: %q", backend.opened)
	}
}

func TestNativeLibraryStartupFocus(t *testing.T) {
	for _, test := range []struct {
		name                    string
		populated, retry, moved bool
	}{
		{name: "Empty"},
		{name: "Populated", populated: true},
		{name: "Retry", populated: true, retry: true},
		{name: "Toolbar", populated: true, moved: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var apps []profile.App
			if test.populated {
				_, apps = savedLibrary(t, 1, "")
			}
			fail := test.retry
			d := newDesktopApp(libraryListProbe{list: func() ([]profile.App, error) {
				if fail {
					fail = false
					return nil, errors.New("profile read requires a retry")
				}
				return apps, nil
			}}, &testingIcons{}, nil, nil)
			d.page = 1
			var work []func()
			d.work = func(task func()) { work = append(work, task) }
			d.loadLibrary()
			tt := ui.NewTesterWith(d.view, minWindowWidth, minWindowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
			if !tt.HasText("正在读取应用…") {
				t.Fatal("the library did not wait for the initial read")
			}
			if test.moved {
				tt.Key(0, ui.KeyTab)
				if !tt.Focused(creatorMenuLabel) {
					t.Fatal("Tab did not reach the toolbar while loading")
				}
			}
			work[0]()
			tt.Frame()
			if test.retry {
				if !tt.Focused("重试") {
					t.Fatal("the failed startup did not focus Retry")
				}
				tt.Key(0, ui.KeyEnter)
				if len(work) != 2 {
					t.Fatal("Enter did not restart the library read")
				}
				work[1]()
				tt.Frame()
			}
			switch {
			case test.moved:
				if !tt.Focused(creatorMenuLabel) {
					t.Fatal("the library read stole focus from the toolbar")
				}
			case test.populated:
				if !tt.Focused("搜索应用库") {
					t.Fatal("the loaded library did not focus its search")
				}
			case tt.Focused("创建第一个应用"):
				tt.Key(0, ui.KeyEnter)
				if !tt.Focused("网址或应用名称") {
					t.Fatal("Enter did not move from the empty library into creation")
				}
			default:
				t.Fatal("the empty library did not focus its creation action")
			}
		})
	}
}

func TestNativeStartupAllowsCreationWhileLibraryLoads(t *testing.T) {
	d, tt, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	service := d.library
	old, err := service.Save(t.Context(), profile.App{Name: "Reading Desk", URL: "https://example.org/reading"}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	d.library = libraryListProbe{appLibrary: service, list: func() ([]profile.App, error) {
		reads++
		return service.List()
	}}
	var work, callbacks []func()
	d.work = func(f func()) { work = append(work, f) }
	d.dispatch = func(f func()) { callbacks = append(callbacks, f) }
	var restored []profile.App
	d.afterLibraryLoad = func(apps []profile.App) { restored = apps }
	d.loadLibrary()
	d.loadLibrary()
	if reads != 0 || len(work) != 1 || !tt.HasText("选择 DeepSeek") {
		t.Fatalf("startup read %d libraries on the foreground and queued %d tasks", reads, len(work))
	}
	click(t, tt, "应用库")
	if !tt.HasText("正在读取应用…") || tt.HasText("创建第一个应用") {
		t.Fatalf("the pending library appeared empty: %v", tt.Texts())
	}
	click(t, tt, "创建")
	click(t, tt, "选择 DeepSeek")
	click(t, tt, "创建应用")
	if len(work) != 2 {
		t.Fatalf("creation during startup queued %d tasks", len(work))
	}
	work[0]() // Read the old snapshot before saving and deliver it afterward.
	work[1]()
	if len(callbacks) != 2 {
		t.Fatalf("the startup read and save produced %d callbacks", len(callbacks))
	}
	callbacks[1]()
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0].Name != "DeepSeek" || !tt.HasText("打开 DeepSeek") {
		t.Fatalf("the saved app did not appear during startup: %+v", d.apps)
	}
	saved := d.apps[0]
	picture := d.appArt.get(saved.ID)
	callbacks[0]()
	if len(work) != 3 || !tt.HasText("打开 DeepSeek") {
		t.Fatalf("the outdated snapshot did not queue a fresh read: %d tasks, %+v", len(work), d.apps)
	}
	work[2]()
	callbacks[2]()
	if len(work) != 4 || restored != nil {
		t.Fatalf("restoring system entries was not queued after the fresh read: %d tasks", len(work))
	}
	work[3]()
	if len(restored) != 2 || restored[0] != saved || restored[1] != old {
		t.Fatalf("restoring system entries used stale profiles: %+v", restored)
	}
	restored[0] = profile.App{} // The worker must have its own snapshot.
	tt.Frame()
	if !tt.HasText("打开 Reading Desk") || !tt.HasText("打开 DeepSeek") || len(d.apps) != 2 || d.apps[0] != saved ||
		d.apps[1] != old || picture == nil || d.appArt.get(saved.ID) != picture || reads != 2 {
		t.Fatalf("the library lost the saved app or its existing profile: %+v, %d reads", d.apps, reads)
	}
}

func TestNativeLibraryReadCanBeRetried(t *testing.T) {
	d, tt, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	service := d.library
	reads := 0
	d.library = libraryListProbe{appLibrary: service, list: func() ([]profile.App, error) {
		reads++
		if reads == 1 {
			return nil, errors.New("reading profile fixture")
		}
		return service.List()
	}}
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	restored := false
	d.afterLibraryLoad = func([]profile.App) { restored = true }
	d.loadLibrary()
	click(t, tt, "应用库")
	if len(work) != 1 {
		t.Fatalf("startup queued %d reads", len(work))
	}
	work[0]()
	tt.Frame()
	if !tt.HasText("无法读取应用库") || tt.HasText("创建第一个应用") {
		t.Fatalf("the library read error was not shown: %v", tt.Texts())
	}
	click(t, tt, "重试")
	if len(work) != 2 || !tt.HasText("正在读取应用…") {
		t.Fatalf("retry did not start another read: %d tasks, %v", len(work), tt.Texts())
	}
	work[1]()
	tt.Frame()
	if !tt.HasText("创建第一个应用") || tt.HasText("无法读取应用库") || reads != 2 || len(work) != 3 || restored {
		t.Fatalf("retry did not show the loaded library: %v, %d reads", tt.Texts(), reads)
	}
	work[2]()
	if !restored {
		t.Fatal("retry did not restore the system entries after the library loaded")
	}
}

func TestNativeDuplicateIsReachableBeforeLibraryLoads(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	var work, results []func()
	d.work = func(task func()) { work = append(work, task) }
	d.dispatch = func(result func()) { results = append(results, result) }
	d.loadLibrary()
	if len(work) != 1 {
		t.Fatalf("startup queued %d tasks", len(work))
	}
	work[0]() // The first snapshot is empty. Keep its delivery pending.
	work = work[:0]
	old, err := d.library.Save(t.Context(), profile.App{Name: "DeepSeek", URL: "https://chat.deepseek.com"}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	click(t, tt, "选择 DeepSeek")
	click(t, tt, "创建应用")
	if len(work) != 1 {
		t.Fatalf("submitting the duplicate queued %d tasks", len(work))
	}
	work[0]()
	work = work[:0]
	if len(results) != 2 {
		t.Fatalf("the two tasks returned %d updates", len(results))
	}
	stale := results[0]
	results[1]()
	results = results[:0]
	tt.Frame()
	if !tt.Focused("打开 DeepSeek") || len(d.apps) != 1 || d.apps[0] != old || backend.builds != 1 {
		t.Fatalf("the conflict did not present the existing app while loading: %+v, %d installs", d.apps, backend.builds)
	}
	stale()
	if !d.loadingLibrary {
		t.Fatal("the outdated initial snapshot did not queue a fresh read")
	}
	for _, task := range work {
		task()
	}
	for _, result := range results {
		result()
	}
	tt.Frame()
	if d.loadingLibrary || len(d.apps) != 1 || d.apps[0] != old || !tt.Focused("打开 DeepSeek") {
		t.Fatalf("the fresh snapshot changed the focused app: %+v", d.apps)
	}
}

func TestNativeCreationUsesCachedArtworkBeforePreview(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	resolver := &cachedLibraryIcons{testingIcons: testingIcons{img: icons.Brand()}}
	d.resolver = resolver
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	const address = "https://example.org/project"
	click(t, tt, "网址或应用名称")
	tt.Type(address)
	click(t, tt, "使用网址 example.org")
	click(t, tt, "应用名称")
	typeReplace(tt, "Project desk")
	if len(resolver.reads) != 0 || len(resolver.lookups) != 0 || len(work) != 2 {
		t.Fatalf("the editor performed artwork I/O while opening or typing: %v cached reads, %v lookups, %d tasks",
			resolver.reads, resolver.lookups, len(work))
	}
	if !d.draft.open || d.draft.icon == nil || d.draft.icon.kind != icons.PlaceholderArtwork {
		t.Fatal("the editor did not show temporary artwork while its worker was pending")
	}
	click(t, tt, "创建应用")
	if len(work) != 3 {
		t.Fatalf("creating with pending artwork queued %d tasks", len(work))
	}
	work[2]()
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0].Name != "Project desk" || backend.builds != 1 || backend.lastIcon != resolver.img {
		t.Fatalf("creation did not install the cached artwork on the first save: %+v, %d builds", d.apps, backend.builds)
	}
	app := d.apps[0]
	work[0]()
	work[1]()
	if len(work) != 3 || len(resolver.lookups) != 0 || len(resolver.reads) != 1 || resolver.reads[0] != address {
		t.Fatalf("the cached creation repeated the artwork work: %v reads, %v lookups, %d tasks", resolver.reads, resolver.lookups, len(work))
	}
	if picture := d.appArt.get(app.ID); picture == nil || picture.img != resolver.img {
		t.Fatal("the saved app did not display its installed artwork in the library")
	}

	work = nil
	click(t, tt, "创建")
	click(t, tt, "网址或应用名称")
	tt.Type(address)
	click(t, tt, "使用网址 example.org")
	click(t, tt, "应用名称")
	typeReplace(tt, "Project desk")
	click(t, tt, "创建应用")
	if len(work) != 3 {
		t.Fatalf("duplicate creation queued %d tasks", len(work))
	}
	work[2]()
	work[0]()
	work[1]()
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0] != app || backend.builds != 1 || len(work) != 3 || len(resolver.reads) != 1 || len(resolver.lookups) != 0 {
		t.Fatalf("duplicate creation repeated work: %+v, %d builds, %v reads, %v lookups, %d tasks", d.apps, backend.builds, resolver.reads, resolver.lookups, len(work))
	}
}

func TestNativeAddressCardReusesCachedArtwork(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	firstIcon, secondIcon := icons.Brand(), icons.Fallback("Other project", "")
	resolver := &cachedLibraryIcons{testingIcons: testingIcons{img: firstIcon, cachedName: "Project board"}}
	d.resolver = resolver
	var work, updates []func()
	d.work = func(f func()) { work = append(work, f) }
	d.dispatch = func(f func()) { updates = append(updates, f) }

	const first = "https://example.org/projects?view=board"
	const second = "https://example.org/projects?view=calendar"
	click(t, tt, "网址或应用名称")
	tt.Type(first)
	if len(work) != 1 || len(resolver.reads) != 0 || d.draft.open || !tt.HasText("使用网址 example.org") {
		t.Fatalf("the visible card did not queue one background cache read: %d tasks, %v reads", len(work), resolver.reads)
	}
	work[0]()
	typeReplace(tt, second)
	if len(work) != 2 || len(updates) != 1 || len(resolver.lookups) != 0 {
		t.Fatalf("the current URL waited for the old cache callback: %d tasks, %d updates", len(work), len(updates))
	}
	updates[0]()
	tt.Frame()
	if d.address.icon == nil || d.address.icon.img == firstIcon || d.address.websiteName != "" || len(work) != 2 {
		t.Fatal("the earlier callback replaced the current preview or queued a duplicate read")
	}
	resolver.img = secondIcon
	resolver.cachedName = "Calendar desk"
	work[1]()
	updates[1]()
	tt.Frame()
	seen := d.address.icon
	if seen == nil || seen.img != secondIcon || len(resolver.reads) != 2 || resolver.reads[0] != first || resolver.reads[1] != second {
		t.Fatalf("the card did not display the cached artwork for its current URL: %v", resolver.reads)
	}
	const selected = second + "#today"
	typeReplace(tt, selected)
	tt.Frame()
	if d.address.icon != seen || len(work) != 2 || len(resolver.lookups) != 0 {
		t.Fatal("changing the fragment reloaded the same icon or contacted the website")
	}
	click(t, tt, "使用网址 Calendar desk")
	field, _ := tt.Access("应用名称")
	if d.draft.app.URL != selected || d.draft.icon != seen || field.Placeholder != "Calendar desk" || len(work) != 2 {
		t.Fatal("the editor did not reuse the preview shown on the selected card")
	}
	click(t, tt, "创建应用")
	if len(work) != 3 {
		t.Fatalf("creating with the icon ready queued %d tasks", len(work))
	}
	work[2]()
	updates[2]()
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0].URL != selected || d.apps[0].Name != "Calendar desk" || backend.builds != 1 || backend.lastIcon != secondIcon ||
		len(resolver.reads) != 2 || len(resolver.lookups) != 0 {
		t.Fatalf("creation repeated the artwork work or lost the selected URL: %+v, %v reads, %v lookups", d.apps, resolver.reads, resolver.lookups)
	}
}

func TestNativeAddressCardSkipsObsoleteCacheReads(t *testing.T) {
	d, tt, _, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	probe := &cachedLibraryIcons{testingIcons: *resolver}
	d.resolver = probe
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	click(t, tt, "网址或应用名称")
	for _, address := range []string{
		"https://example.org/board", "https://example.org/calendar", "https://example.org/archive",
	} {
		typeReplace(tt, address)
	}
	click(t, tt, "应用库")
	for _, read := range work {
		read()
	}
	if len(probe.reads) != 0 || len(probe.lookups) != 0 {
		t.Fatalf("hidden address cards still read artwork: %v, %v", probe.reads, probe.lookups)
	}
	work = nil
	click(t, tt, "创建")
	if len(work) != 1 {
		t.Fatalf("returning to the address queued %d cache reads, want one", len(work))
	}
	work[0]()
	tt.Frame()
	if len(probe.reads) != 1 || probe.reads[0] != "https://example.org/archive" || d.address.icon.img != probe.img {
		t.Fatalf("the visible address did not use its cached artwork: %v", probe.reads)
	}
}

type addressCacheProbe struct {
	testingIcons
	nameFor  func(string) string
	imageFor func(string) image.Image
}

func (p *addressCacheProbe) CachedName(address string) string  { return p.nameFor(address) }
func (p *addressCacheProbe) Cached(address string) image.Image { return p.imageFor(address) }

func TestAddressCacheReadsKeepOnlyTheLatestQueuedAddress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const first = "https://example.org/board"
		const second = "https://example.org/notes"
		const third = "https://example.org/archive"
		const latest = "https://example.org/calendar"
		holdFirst, holdSecond := make(chan struct{}), make(chan struct{})
		releaseFirst := sync.OnceFunc(func() { close(holdFirst) })
		defer releaseFirst()
		defer close(holdSecond)
		names, updates := make(chan string, 4), make(chan func(), 4)
		images := make(chan string, 4)
		picture := icons.Brand()
		resolver := &addressCacheProbe{
			nameFor: func(address string) string {
				names <- address
				if address == second {
					<-holdSecond
				}
				return "Calendar desk"
			},
			imageFor: func(address string) image.Image {
				images <- address
				if address == first {
					<-holdFirst
				}
				return picture
			},
		}
		d := newDesktopApp(nil, resolver, nil, nil)
		d.dispatch = func(f func()) { updates <- f }
		for _, address := range []string{first, second, third, latest} {
			d.query = address
			d.previewAddress()
			d.loadAddressArtwork(false)
			synctest.Wait()
		}
		if len(names) != 2 || <-names != first || <-names != second || len(images) != 1 || <-images != first {
			t.Fatal("the two held cache operations did not bound concurrent work")
		}
		releaseFirst()
		synctest.Wait()
		if len(names) != 1 || <-names != latest || len(images) != 1 || <-images != latest || len(updates) != 1 {
			t.Fatal("a superseded address used the newly available cache slot")
		}
		(<-updates)()
		if d.address.websiteName != "Calendar desk" || d.address.icon.img != picture {
			t.Fatal("the available cache result did not reach the current card")
		}
	})
}

func pasteAddress(tt *ui.Tester, address string) {
	modifier := ui.Ctrl
	if runtime.GOOS == "darwin" {
		modifier = ui.Cmd
	}
	tt.Key(modifier, ui.KeyA)
	tt.SetClipboard(address)
	tt.Key(modifier, ui.KeyV)
}

func TestNativePastedURLLoadsWebsiteArtwork(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	const siteName = "Work Board"
	resolver.name = siteName
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	const address = "https://example.org/workspace?view=board"
	click(t, tt, "网址或应用名称")
	pasteAddress(tt, address)
	if d.draft.open || !tt.HasText("使用网址 example.org") || len(work) != 1 || len(resolver.lookups) != 0 {
		t.Fatalf("the pasted address did not paint immediately and queue one artwork request: %+v, %d tasks", d.address, len(work))
	}
	work[0]()
	tt.Frame()
	seen := d.address.icon
	if seen.img != resolver.img || !tt.HasText("使用网址 "+siteName) || len(resolver.lookups) != 1 || resolver.lookups[0] != address || d.draft.open {
		t.Fatalf("the website artwork did not reach the card before selection: %v", resolver.lookups)
	}
	shot(t, tt, "pasted-fresh-website")
	const selected = address + "#today"
	typeReplace(tt, selected)
	click(t, tt, "使用网址 "+siteName)
	field, _ := tt.Access("应用名称")
	if d.draft.app.URL != selected || d.draft.icon != seen || field.Value != "" || field.Placeholder != siteName || len(work) != 1 {
		t.Fatal("the form did not reuse the artwork or lost the URL fragment")
	}
	click(t, tt, "创建应用")
	if len(work) != 2 {
		t.Fatalf("creating with the icon ready queued %d tasks", len(work))
	}
	work[1]()
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0].URL != selected || d.apps[0].Name != siteName || backend.builds != 1 || backend.lastIcon != resolver.img {
		t.Fatal("the installed app did not use the selected URL and preview artwork")
	}
}

func TestNativeCancelCreationKeepsWebsiteArtworkOnTheCard(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	const address = "https://example.org/workspace?view=board#today"
	const name = "Project Board"
	resolver.name = name
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	click(t, tt, "网址或应用名称")
	pasteAddress(tt, address)
	click(t, tt, "使用网址 example.org")
	if !d.draft.open || len(work) != 1 || d.draft.lookup == nil {
		t.Fatal("the form did not use the pending artwork lookup")
	}
	tt.Key(0, ui.KeyEscape)
	if d.draft.open || !tt.Focused("使用网址 example.org") {
		t.Fatal("Escape did not close the form and focus the original URL card")
	}
	work[0]()
	tt.Frame()
	if !tt.Focused("使用网址 "+name) || d.address.icon.img != resolver.img {
		t.Fatalf("the returning card did not receive the pending artwork and name: %v", resolver.lookups)
	}
	tt.Key(0, ui.KeyEnter)
	field, _ := tt.Access("应用名称")
	if !d.draft.open || d.draft.app.URL != address || d.draft.icon != d.address.icon ||
		field.Value != "" || field.Placeholder != name || len(work) != 1 {
		t.Fatal("reopening the form repeated the artwork lookup or lost the selected address")
	}
	click(t, tt, "创建应用")
	if len(work) != 2 {
		t.Fatalf("the reopened form queued %d tasks, want one lookup and one save", len(work))
	}
	work[1]()
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0].URL != address || d.apps[0].Name != name ||
		backend.lastIcon != resolver.img || len(resolver.lookups) != 1 {
		t.Fatal("the installed app did not use the original lookup")
	}
}

func TestNativeCancelCreationStopsArtworkForChangedAddress(t *testing.T) {
	d, tt, _, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	const address = "https://example.org/workspace?view=board"
	click(t, tt, "网址或应用名称")
	pasteAddress(tt, address)
	click(t, tt, "使用网址 example.org")
	click(t, tt, "应用网址")
	pasteAddress(tt, "https://example.org/workspace?view=calendar")
	if len(work) != 2 {
		t.Fatalf("editing the form queued %d tasks, want one per URL", len(work))
	}
	click(t, tt, "取消")
	work[1]()
	work[0]()
	tt.Frame()
	if d.draft.open || d.address.app.URL != address || d.address.icon.img != resolver.img ||
		len(resolver.lookups) != 1 || resolver.lookups[0] != address {
		t.Fatalf("the returning card did not finish just its original lookup: %+v, %v", d.address, resolver.lookups)
	}
}

func TestNativeClearSearchCancelsPastedArtwork(t *testing.T) {
	d, tt, _, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	const search = "网址或应用名称"
	click(t, tt, "AI")
	click(t, tt, search)
	pasteAddress(tt, "https://example.org/workspace?view=board#notes")
	if len(work) != 1 || d.address.lookup == nil {
		t.Fatal("pasting the address did not start an artwork request")
	}
	click(t, tt, "清除搜索")
	work[0]()
	tt.Frame()
	if len(resolver.lookups) != 0 || d.address.lookup != nil || d.query != "" || tt.HasText("使用网址 example.org") {
		t.Fatal("clearing the address left its artwork request or card active")
	}
	if !tt.Focused(search) || !tt.HasText("添加") || !tt.HasText("选择 DeepSeek") || tt.HasText("选择 GitHub") {
		t.Fatal("clearing an address lost input focus or the selected category")
	}
}

func TestNativePastedURLSharesPendingArtworkWithCreation(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	resolver.name = "Arrives after creation"
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	const address = "https://example.org/workspace"
	click(t, tt, "网址或应用名称")
	pasteAddress(tt, address)
	const selected = address + "#today"
	typeReplace(tt, selected)
	tt.Key(0, ui.KeyEnter)
	click(t, tt, "创建应用")
	if len(work) != 2 {
		t.Fatalf("selecting and creating queued %d tasks, want one lookup and one save", len(work))
	}
	work[1]()
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0].URL != selected || backend.builds != 1 || len(resolver.lookups) != 0 {
		t.Fatal("the foreground save did not finish while website artwork was pending")
	}
	work[0]()
	if len(work) != 3 || len(resolver.lookups) != 1 || resolver.lookups[0] != address {
		t.Fatalf("the pasted address's request did not carry on after installation: %v, %d tasks", resolver.lookups, len(work))
	}
	work[2]()
	tt.Frame()
	if picture := d.appArt.get(d.apps[0].ID); picture == nil || picture.img != resolver.img || backend.lastIcon != resolver.img {
		t.Fatal("the background lookup did not update the installed and visible artwork")
	}
	if d.apps[0].Name != "example.org" {
		t.Fatal("a late site name changed the installed application")
	}
}

func TestNativePendingWebsiteArtworkIsSharedAcrossApps(t *testing.T) {
	for _, test := range []struct {
		name        string
		removeOne   bool
		unavailable bool
	}{
		{name: "update both installations"},
		{name: "remove one while fetching", removeOne: true},
		{name: "retry after a missing icon", unavailable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, tt, backend, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
			var work []func()
			d.work = func(f func()) { work = append(work, f) }
			const address = "https://example.org/workspace?view=board"
			create := func(name, fragment string) {
				t.Helper()
				click(t, tt, "网址或应用名称")
				pasteAddress(tt, address+fragment)
				tt.Key(0, ui.KeyEnter)
				click(t, tt, "应用名称")
				typeReplace(tt, name)
				click(t, tt, "创建应用")
			}
			runLast := func() {
				t.Helper()
				n := len(work) - 1
				if n < 0 {
					t.Fatal("no work was queued")
				}
				task := work[n]
				work = work[:n]
				task()
				tt.Frame()
			}
			create("Design Desk", "#design")
			if len(work) != 2 {
				t.Fatalf("first creation queued %d jobs, want one lookup and one save", len(work))
			}
			runLast()
			click(t, tt, "创建")
			create("Review Desk", "#review")
			if len(work) != 2 {
				t.Fatalf("second creation queued a duplicate lookup: %d pending jobs, want two", len(work))
			}
			runLast()
			if len(d.apps) != 2 || d.apps[0].ID == d.apps[1].ID || d.apps[0].URL != address+"#review" || d.apps[1].URL != address+"#design" || backend.builds != 2 {
				t.Fatalf("the foreground saves lost an identity or URL: %+v, %d builds", d.apps, backend.builds)
			}
			if test.removeOne {
				click(t, tt, "Design Desk 的选项")
				click(t, tt, "移除应用")
				click(t, tt, "移除")
				runLast()
				if len(d.apps) != 1 || d.apps[0].Name != "Review Desk" {
					t.Fatalf("the wrong installation was removed: %+v", d.apps)
				}
			}
			mark := resolver.img
			if test.unavailable {
				resolver.img = nil
			}
			runLast() // The one network operation both applications subscribed to.
			if test.unavailable {
				if len(d.savedLookups) != 0 || len(d.activeLookups) != 0 || len(work) != 0 || backend.builds != 2 {
					t.Fatal("the missing icon left an update or unnecessary rebuild pending")
				}
				resolver.img = mark
				click(t, tt, "创建")
				click(t, tt, "网址或应用名称")
				pasteAddress(tt, address+"#retry")
				runLast()
				if len(resolver.lookups) != 2 || d.address.icon.img != mark {
					t.Fatalf("pasting again did not retry the failed lookup: %v", resolver.lookups)
				}
				return
			}
			if len(work) != 1 || len(resolver.lookups) != 1 || resolver.lookups[0] != address+"#design" {
				t.Fatalf("resolving the shared lookup produced %d jobs and requests %v", len(work), resolver.lookups)
			}
			runLast()
			for _, app := range d.apps {
				if picture := d.appArt.get(app.ID); picture == nil || picture.img != resolver.img {
					t.Fatalf("installation %q did not receive the shared icon", app.Name)
				}
			}
			if backend.builds != 2+len(d.apps) || len(d.savedLookups) != 0 || len(work) != 0 {
				t.Fatalf("expected one icon update per remaining app, got %d builds, %d subscriptions and %d tasks", backend.builds, len(d.savedLookups), len(work))
			}
		})
	}
}

func TestNativeWebsiteNamePreservesManualText(t *testing.T) {
	d, tt, _, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	resolver.name = "Declared Workbench"
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	click(t, tt, "网址或应用名称")
	pasteAddress(tt, "https://example.org/board")
	click(t, tt, "使用网址 example.org")
	click(t, tt, "应用名称")
	typeReplace(tt, "My board")
	if len(work) != 1 {
		t.Fatalf("expected one website lookup, got %d tasks", len(work))
	}
	work[0]()
	tt.Frame()
	if field, _ := tt.Access("应用名称"); field.Placeholder != resolver.name || field.Value != "My board" {
		t.Fatalf("the name field after the lookup is %+v", field)
	}
	click(t, tt, "创建应用")
	if len(work) != 2 {
		t.Fatalf("expected one save after the lookup, got %d tasks", len(work))
	}
	work[1]()
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0].Name != "My board" {
		t.Fatalf("creation replaced the chosen name: %+v", d.apps)
	}
}

func TestNativeWebsiteNameIgnoresResultsForAnOldAddress(t *testing.T) {
	d, tt, _, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	resolver.name = "Old Workbench"
	var work, updates []func()
	d.work = func(f func()) { work = append(work, f) }
	d.dispatch = func(f func()) { updates = append(updates, f) }
	click(t, tt, "网址或应用名称")
	pasteAddress(tt, "https://example.org/old")
	click(t, tt, "使用网址 example.org")
	work[0]()
	click(t, tt, "应用网址")
	pasteAddress(tt, "https://example.org/new")
	for _, update := range updates {
		update()
	}
	tt.Frame()
	if field, _ := tt.Access("应用名称"); field.Placeholder != "使用网站域名" || field.Value != "" {
		t.Fatalf("the edited address inherited the old name: %+v", field)
	}
}

func TestNativePastedArtworkIgnoresEarlierAddress(t *testing.T) {
	d, tt, _, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	var work, updates []func()
	d.work = func(f func()) { work = append(work, f) }
	d.dispatch = func(f func()) { updates = append(updates, f) }
	click(t, tt, "网址或应用名称")
	pasteAddress(tt, "https://example.org/project?view=board")
	if len(work) != 1 {
		t.Fatalf("paste queued %d artwork tasks", len(work))
	}
	work[0]()
	const current = "https://example.org/project?view=calendar"
	pasteAddress(tt, current)
	if len(work) != 2 || len(updates) != 1 {
		t.Fatalf("unexpected work after replacing the URL: %d tasks, %d updates", len(work), len(updates))
	}
	updates[0]()
	tt.Frame()
	if d.address.icon.img == resolver.img {
		t.Fatal("the old URL supplied artwork to a different card on the same host")
	}
	resolver.img = icons.Fallback("Calendar", "")
	work[1]()
	updates[1]()
	tt.Frame()
	if d.address.icon.img != resolver.img || len(resolver.lookups) != 2 || resolver.lookups[1] != current {
		t.Fatalf("the current URL did not supply its own artwork: %v", resolver.lookups)
	}
}

func TestNativePastedArtworkReplacesPendingCacheRead(t *testing.T) {
	d, tt, _, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	probe := &cachedLibraryIcons{testingIcons: *resolver}
	d.resolver = probe
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	click(t, tt, "网址或应用名称")
	const address = "https://example.org/workspace"
	tt.Type(address)
	if len(work) != 1 {
		t.Fatalf("typing queued %d tasks, want a cache read", len(work))
	}
	pasteAddress(tt, address)
	if len(work) != 2 {
		t.Fatalf("pasting queued %d tasks, want one replacement", len(work)-1)
	}
	work[0]()
	work[1]()
	tt.Frame()
	if len(probe.reads) != 0 || len(probe.lookups) != 1 || probe.lookups[0] != address || d.address.icon.img != probe.img {
		t.Fatalf("pasting did not replace the queued cache read with a single lookup: %v reads, %v lookups", probe.reads, probe.lookups)
	}
}

func TestNativeLargeLibraryCanOpenEditAndRemoveWhileArtworkLoads(t *testing.T) {
	path, apps := savedLibrary(t, 120, "")
	backend := &testingBundle{}
	bundles := bundle.NewLibrary(t.TempDir(), backend)
	store := profile.NewStore(path)
	service := library.New(store, bundles, library.Options{})
	resolver := &cachedLibraryIcons{testingIcons: testingIcons{img: icons.Brand()}}
	opened, removed := apps[0], apps[1]
	var openPath string
	for _, app := range []profile.App{opened, removed} {
		installed, err := bundles.Install(t.Context(), app, app, icons.Artwork{Image: resolver.img}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if app.ID == opened.ID {
			openPath = installed
		}
	}
	d := newDesktopApp(service, resolver, nil, nil)
	d.work = func(f func()) { f() }
	d.loadLibrary()
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	tt := ui.NewTesterWith(d.view, windowWidth, windowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	if len(d.apps) != len(apps) || len(resolver.reads) != 0 || len(work) != 0 {
		t.Fatal("the creation page loaded the saved application's artwork")
	}
	runLast := func() {
		t.Helper()
		if len(work) == 0 {
			t.Fatal("expected a queued action")
		}
		last := len(work) - 1
		task := work[last]
		work = work[:last]
		task()
		tt.Frame()
	}
	search := func(name string) {
		t.Helper()
		click(t, tt, "搜索应用库")
		typeReplace(tt, name)
	}
	click(t, tt, "应用库")
	tt.Frame()
	if pending := len(d.pendingArt); pending == 0 || pending >= len(apps) || len(resolver.reads) != 0 {
		t.Fatalf("artwork loading is not limited to the displayed rows: %d queued, %d read", pending, len(resolver.reads))
	}
	queued := len(work)
	tt.Frame()
	if len(work) != queued {
		t.Fatal("redrawing the same rows queued duplicate artwork reads")
	}
	search(opened.Name)
	click(t, tt, "打开 "+opened.Name)
	runLast()
	if backend.opened != openPath || len(resolver.reads) != 0 {
		t.Fatalf("opening an installed app waited for artwork: %q, %v", backend.opened, resolver.reads)
	}
	click(t, tt, opened.Name+" 的选项")
	click(t, tt, "编辑应用")
	if !d.draft.open || len(resolver.reads) != 0 || len(resolver.lookups) != 0 {
		t.Fatal("opening settings waited for pending artwork")
	}
	if d.draft.icon == nil || d.draft.icon.kind != icons.PlaceholderArtwork {
		t.Fatal("settings did not show temporary artwork while its icon was pending")
	}
	click(t, tt, "保存")
	runLast()
	if backend.builds != 2 || d.draft.open || len(resolver.reads) != 0 {
		t.Fatal("saving unchanged settings while artwork was pending caused a rebuild or read")
	}
	search(opened.Name)
	click(t, tt, opened.Name+" 的选项")
	click(t, tt, "编辑应用")
	runLast()
	if d.draft.icon == nil || d.draft.icon.img != resolver.img || len(resolver.reads) != 1 || len(resolver.lookups) != 0 {
		t.Fatalf("settings did not pick up the saved artwork: %v, %v", resolver.reads, resolver.lookups)
	}
	keep := d.draft.icon
	click(t, tt, "保存")
	runLast()
	if backend.builds != 2 || d.draft.open || d.appArt.get(opened.ID) != keep {
		t.Fatal("opening and saving the editor rebuilt the app or lost its picture")
	}
	search(removed.Name)
	if _, pending := d.pendingArt[removed.ID]; !pending {
		t.Fatal("the test did not exercise an in-flight artwork read")
	}
	click(t, tt, removed.Name+" 的选项")
	click(t, tt, "移除应用")
	click(t, tt, "移除")
	runLast()
	for _, task := range work {
		task()
	}
	tt.Frame()
	if len(d.apps) != len(apps)-1 || d.appArt.get(removed.ID) != nil || !tt.HasText("没有匹配的应用") {
		t.Fatal("a delayed artwork read brought back a removed row")
	}
	if d.appArt.get(opened.ID) != keep || len(resolver.reads) >= len(apps) {
		t.Fatal("artwork loading replaced the active picture or scanned the full library")
	}
}

type delayedLaunchBackend struct {
	*testingBundle
	open func(context.Context, string) error
}

func (b delayedLaunchBackend) Open(ctx context.Context, path string) error {
	return b.open(ctx, path)
}

func TestNativeLibraryShowsAvailableArtworkWhileAnotherAppStarts(t *testing.T) {
	path, apps := savedLibrary(t, 2, "")
	slow, available := apps[1], apps[0]
	started, unblock := make(chan struct{}), make(chan struct{})
	backend := delayedLaunchBackend{testingBundle: &testingBundle{}, open: func(ctx context.Context, path string) error {
		if filepath.Base(path) != slow.Name+".fixture" {
			return errors.New("the fixture opened the wrong app")
		}
		close(started)
		select {
		case <-unblock:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	resolver := &cachedLibraryIcons{testingIcons: testingIcons{img: icons.Brand()}}
	bundles := bundle.NewLibrary(t.TempDir(), backend)
	service := library.New(profile.NewStore(path), bundles, library.Options{})
	if _, err := bundles.Install(t.Context(), slow, slow, icons.Artwork{Image: resolver.img}, nil); err != nil {
		t.Fatal(err)
	}
	desktop := newDesktopApp(service, resolver, nil, nil)
	desktop.work = func(f func()) { f() }
	desktop.loadLibrary()

	var tasks sync.WaitGroup
	defer tasks.Wait()
	release := sync.OnceFunc(func() { close(unblock) })
	defer release()
	launched := make(chan error, 1)
	tasks.Go(func() { launched <- service.Open(t.Context(), slow, nil) })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("the first application did not reach its launch step")
	}

	var jobs []func()
	callbacks := make(chan func(), 4)
	desktop.work = func(f func()) { jobs = append(jobs, f) }
	desktop.dispatch = func(f func()) { callbacks <- f }
	tt := ui.NewTesterWith(desktop.view, minWindowWidth, minWindowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	click(t, tt, "应用库")
	tt.Frame()
	if len(desktop.pendingArt) != 2 || len(jobs) != 1 {
		t.Fatalf("expected both visible rows in one artwork batch, got %d rows and %d jobs", len(desktop.pendingArt), len(jobs))
	}
	completeJob := func(stage string) {
		t.Helper()
		if len(jobs) == 0 {
			t.Fatalf("%s did not queue a job", stage)
		}
		job := jobs[0]
		jobs = jobs[1:]
		tasks.Go(job)
		select {
		case callback := <-callbacks:
			callback()
			tt.Frame()
		case <-time.After(time.Second):
			t.Fatalf("%s did not update the library while the first app's launch was held", stage)
		}
	}
	reading := time.Now()
	completeJob("the available icon")
	if desktop.appArt.get(available.ID) == nil || desktop.appArt.get(slow.ID) != nil || len(resolver.reads) != 1 || resolver.reads[0] != available.URL {
		t.Fatalf("the library did not show only the available icon: %v", resolver.reads)
	}
	t.Logf("the available icon was displayed in %s while the first launch was still held", time.Since(reading).Round(time.Millisecond))
	click(t, tt, "搜索应用库")
	typeReplace(tt, available.Name)
	if tt.HasText("打开 " + slow.Name) {
		t.Fatal("the busy row is still visible through the filter")
	}
	if len(jobs) != 1 {
		t.Fatalf("the held row left %d waiting artwork jobs", len(jobs))
	}
	next := jobs[0]
	jobs = jobs[1:]
	stopped := make(chan struct{})
	tasks.Go(func() {
		next()
		close(stopped)
	})
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("the hidden row kept waiting for its artwork during the held launch")
	}
	select {
	case <-callbacks:
		t.Fatal("the hidden row requested a redraw")
	default:
	}
	release()
	if err := <-launched; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 || len(resolver.reads) != 1 {
		t.Fatal("finishing the launch read artwork for a filtered out row")
	}
	typeReplace(tt, "")
	tt.Frame()
	completeJob("the deferred icon")
	if desktop.appArt.get(slow.ID) == nil || len(resolver.reads) != 2 || resolver.reads[1] != slow.URL || len(desktop.pendingArt) != 0 {
		t.Fatalf("the deferred icon did not load exactly once: %v", resolver.reads)
	}
}

type libraryArtworkProbe struct {
	appLibrary
	reads map[string]int
	read  func(profile.App, <-chan struct{}) (icons.Artwork, <-chan struct{})
}

func (p libraryArtworkProbe) TryInstalledArtwork(app profile.App, stopped <-chan struct{}) (icons.Artwork, <-chan struct{}) {
	p.reads[app.ID]++
	return p.read(app, stopped)
}

func TestNativeLibrarySkipsArtworkForEarlierFilters(t *testing.T) {
	path, apps := savedLibrary(t, 120, "")
	service := library.New(profile.NewStore(path), nil, library.Options{})
	probe := galleryArtwork(service)
	desktop := newDesktopApp(probe, &testingIcons{}, nil, nil)
	desktop.work = func(f func()) { f() }
	desktop.loadLibrary()
	var work []func()
	desktop.work = func(f func()) { work = append(work, f) }
	tt := ui.NewTesterWith(desktop.view, windowWidth, windowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	click(t, tt, "应用库")
	tt.Frame()
	click(t, tt, "搜索应用库")
	const filterCount = 24
	for _, app := range apps[:filterCount] {
		typeReplace(tt, app.Name)
		tt.Frame()
	}
	active := apps[filterCount-1]
	if !tt.HasText("打开 "+active.Name) || len(work) <= filterCount || len(probe.reads) != 0 {
		t.Fatalf("the fixture did not queue the earlier filters before reading: %d jobs, %d reads", len(work), len(probe.reads))
	}
	for _, read := range work {
		read()
	}
	tt.Frame()
	t.Logf("%d queued batches, %d installed icons read", len(work), len(probe.reads))
	if len(probe.reads) != 1 || probe.reads[active.ID] != 1 || desktop.appArt.get(active.ID) == nil {
		t.Fatalf("the final filter read artwork for %d rows before showing the current icon", len(probe.reads))
	}
}

func galleryArtwork(service appLibrary) libraryArtworkProbe {
	return libraryArtworkProbe{
		appLibrary: service,
		reads:      make(map[string]int),
		read: func(profile.App, <-chan struct{}) (icons.Artwork, <-chan struct{}) {
			return icons.Artwork{Image: libraryFixtureIcon(), Kind: icons.WebsiteArtwork}, nil
		},
	}
}

func libraryFixtureIcon() *image.NRGBA {
	picture := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	ink := color.NRGBA{R: 69, G: 126, B: 225, A: 255}
	draw.Draw(picture, picture.Bounds(), image.NewUniform(ink), image.Point{}, draw.Src)
	picture.SetNRGBA(255, 255, color.NRGBA{R: 255, A: 255})
	return picture
}

func TestNativeLibraryReleasesOffscreenArtwork(t *testing.T) {
	const count = 240
	path, apps := savedLibrary(t, count, "")
	first, last := apps[count-1], apps[0]
	backend := &testingBundle{}
	bundles := bundle.NewLibrary(t.TempDir(), backend)
	if _, err := bundles.Install(t.Context(), first, first, icons.Artwork{Image: libraryFixtureIcon()}, nil); err != nil {
		t.Fatal(err)
	}
	service := library.New(profile.NewStore(path), bundles, library.Options{})
	probe := galleryArtwork(service)
	desktop := newDesktopApp(probe, &testingIcons{}, nil, nil)
	desktop.work = func(f func()) { f() }
	desktop.loadLibrary()
	tt := ui.NewTesterWith(desktop.view, windowWidth, windowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	click(t, tt, "应用库")
	tt.Frame()
	if desktop.appArt.get(first.ID) == nil || probe.reads[first.ID] != 1 {
		t.Fatal("the first viewport did not load its initial artwork")
	}
	for step := 0; step < count && desktop.appArt.get(last.ID) == nil; step++ {
		tt.Scroll(windowWidth/2, windowHeight-40, 0, 350)
		tt.Advance(180 * time.Millisecond)
		tt.Frame()
	}
	if desktop.appArt.get(last.ID) == nil || len(probe.reads) < count-5 {
		t.Fatalf("scrolling reached %d of %d application icons", len(probe.reads), count)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("scrolling through %d icons retained %d cached images and %.1f MiB of additional Go heap",
		len(probe.reads), len(desktop.appArt.entries), float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/(1<<20))
	if kept := len(desktop.appArt.entries); kept > 32 || desktop.appArt.get(first.ID) != nil {
		t.Fatalf("the old viewports still hold artwork after scrolling: %d icons", kept)
	}

	click(t, tt, "搜索应用库")
	typeReplace(tt, first.Name)
	tt.Frame()
	loaded := desktop.appArt.get(first.ID)
	if loaded == nil || probe.reads[first.ID] != 2 || loaded.img.Bounds().Size() != image.Pt(256, 256) {
		t.Fatalf("returning to the first row did not reload its original-size icon once: %d reads", probe.reads[first.ID])
	}
	click(t, tt, first.Name+" 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "窗口尺寸")
	click(t, tt, "窗口宽度（像素）")
	typeReplace(tt, "920")
	click(t, tt, "保存")
	if desktop.draft.open || backend.builds != 2 || backend.lastIcon != loaded.img {
		t.Fatal("editing a reloaded app did not save with its original artwork")
	}
}

func TestNativeWideLibraryKeepsVisibleArtwork(t *testing.T) {
	const width, height = 1580, 1230
	path, apps := savedLibrary(t, 120, "")
	service := library.New(profile.NewStore(path), nil, library.Options{})
	probe := galleryArtwork(service)
	desktop := newDesktopApp(probe, &testingIcons{}, nil, nil)
	desktop.work = func(f func()) { f() }
	desktop.loadLibrary()
	desktop.page = 1
	tt := ui.NewTesterWith(desktop.view, width, height, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	tt.Frame()
	total := desktop.appArt.bytes
	if total <= recentArtworkBudget {
		t.Fatalf("the wide viewport only loaded %d bytes of artwork", total)
	}
	for range 5 {
		tt.Frame()
	}
	visible := 0
	for _, app := range apps {
		row, found := tt.Find("打开 " + app.Name)
		if !found || row.Y >= height || row.Y+row.H <= titleHeight+70 {
			continue
		}
		visible++
		if desktop.appArt.get(app.ID) == nil || probe.reads[app.ID] != 1 {
			t.Fatalf("the wide viewport did not keep %q after %d reads", app.Name, probe.reads[app.ID])
		}
	}
	if visible < 40 {
		t.Fatalf("the wide viewport only rendered %d cards", visible)
	}
	click(t, tt, "创建")
	tt.Frame()
	if retained := desktop.appArt.bytes; retained > recentArtworkBudget || len(desktop.appArt.entries) == 0 {
		t.Fatalf("leaving the library retained %d bytes of cached artwork", retained)
	}
}

func TestNativeLibraryOpensAnotherAppWhileTheFirstIsStarting(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, windowWidth, windowHeight)
	click(t, tt, "选择 DeepSeek")
	click(t, tt, "创建应用")
	click(t, tt, "创建")
	click(t, tt, "选择 GitHub")
	click(t, tt, "创建应用")

	var work []func()
	d.work = func(task func()) { work = append(work, task) }
	click(t, tt, "打开 DeepSeek")
	if len(work) != 1 {
		t.Fatalf("first open submitted %d tasks", len(work))
	}
	first, found := tt.Find("打开 DeepSeek")
	if !found {
		t.Fatal("the pending app disappeared from the library")
	}
	tt.ClickAt(first.X+first.W/2, first.Y+first.H/2)
	if len(work) != 1 {
		t.Fatal("clicking the pending app submitted another launch")
	}
	click(t, tt, "打开 GitHub")
	if len(work) != 2 {
		t.Fatal("the first launch blocked the second row")
	}
	if !tt.HasText("正在打开…") {
		t.Fatal("the rows did not show their pending state")
	}
	work[1]()
	tt.Frame()
	if filepath.Base(backend.opened) != "GitHub.fixture" || !tt.HasText("正在打开…") || !tt.HasText("github.com") {
		t.Fatalf("finishing GitHub changed the wrong row: %q %v", backend.opened, tt.Texts())
	}
	shot(t, tt, "library-opening")
	click(t, tt, "GitHub 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "应用名称")
	typeReplace(tt, "GitHub Desk")
	click(t, tt, "保存")
	if len(work) != 3 {
		t.Fatal("saving the other app while DeepSeek starts was not submitted")
	}
	work[2]()
	tt.Frame()
	if !tt.HasText("GitHub Desk") || !tt.HasText("正在打开…") {
		t.Fatalf("saving the other app changed the pending row: %v", tt.Texts())
	}
	work[0]()
	tt.Frame()
	if filepath.Base(backend.opened) != "DeepSeek.fixture" || tt.HasText("正在打开…") || !tt.HasText("chat.deepseek.com") {
		t.Fatalf("finishing DeepSeek did not restore its address: %q %v", backend.opened, tt.Texts())
	}
}

func TestNativePendingLaunchKeepsKeyboardNavigation(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "选择 GitHub")
	click(t, tt, "创建应用")
	click(t, tt, "创建")
	click(t, tt, "选择 DeepSeek")
	click(t, tt, "创建应用")
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	const first, second = "打开 DeepSeek", "打开 GitHub"
	if !tt.Focused(first) {
		t.Fatalf("creation did not focus %q", first)
	}
	tt.Key(0, ui.KeyEnter)
	if len(work) != 1 || !tt.Focused(first) || !tt.HasText("正在打开…") {
		t.Fatalf("starting the app lost keyboard focus or status: %v", tt.Texts())
	}
	if control, ok := tt.AccessByRole(ui.RoleButton, first); !ok || !control.Disabled || !control.Focusable || control.Description != "正在打开… https://chat.deepseek.com" {
		t.Fatalf("the busy row did not report its unavailable action and readable status: %+v", control)
	}
	tt.Key(0, ui.KeyEnter)
	if len(work) != 1 {
		t.Fatal("pressing Enter again submitted another launch")
	}
	tt.Key(0, ui.KeyDown)
	tt.Advance(30 * time.Millisecond)
	if !tt.Focused(second) {
		t.Fatal("Down could not leave the pending row")
	}
	work[0]()
	tt.Frame()
	if !tt.Focused(second) || filepath.Base(backend.opened) != "DeepSeek.fixture" {
		t.Fatal("finishing the first launch reset the user's new focus")
	}
	tt.Key(0, ui.KeyEnter)
	if len(work) != 2 || !tt.Focused(second) {
		t.Fatal("the second row lost focus while launching")
	}
	work[1]()
	tt.Frame()
	if !tt.Focused(second) || filepath.Base(backend.opened) != "GitHub.fixture" || tt.HasText("正在打开…") {
		t.Fatal("finishing the launch did not keep the focused row usable")
	}
}

func TestNativeCreationRevealsAnExistingAppInALargeLibrary(t *testing.T) {
	for _, test := range []struct {
		name         string
		width        int
		reduceMotion bool
	}{
		{"compact", minWindowWidth, false},
		{"wide", 740, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, apps := savedLibrary(t, 120, "")
			d, tt, backend, _ := nativeFixture(t, test.width, minWindowHeight)
			store := profile.NewStore(path)
			backendRoot := t.TempDir()
			bundles := bundle.NewLibrary(backendRoot, backend)
			target := apps[33]
			target.Name, target.URL = "DeepSeek", "https://chat.deepseek.com"
			if err := store.Put(target); err != nil {
				t.Fatal(err)
			}
			installed, err := bundles.Install(t.Context(), target, target, icons.Artwork{Image: icons.Brand()}, nil)
			if err != nil {
				t.Fatal(err)
			}
			service := library.New(store, bundles, library.Options{})
			probe := libraryArtworkProbe{appLibrary: service, reads: make(map[string]int), read: service.TryInstalledArtwork}
			d.library = probe
			d.loadLibrary()
			tt.SetPreferences(ui.Preferences{ReduceMotion: test.reduceMotion})
			click(t, tt, "应用库")
			tt.Advance(180 * time.Millisecond)
			if tt.HasText("打开 DeepSeek") {
				t.Fatal("the fixture should be outside the initial library viewport")
			}
			t.Logf("initial viewport read %d icons", len(probe.reads))
			click(t, tt, "搜索应用库")
			tt.Type("Workspace 119")
			click(t, tt, "创建")
			tt.Advance(180 * time.Millisecond)
			click(t, tt, "选择 DeepSeek")
			tt.Advance(180 * time.Millisecond)
			click(t, tt, "应用网址")
			typeReplace(tt, "https://chat.deepseek.com:443/")
			previousReads := len(probe.reads)
			click(t, tt, "创建应用")
			tt.Advance(180 * time.Millisecond)
			const label = "打开 DeepSeek"
			row, found := tt.Find(label)
			search, _ := tt.Find("搜索应用库")
			if !found || row.Y < search.Y+search.H || row.Y+row.H > minWindowHeight-62 || !tt.Focused(label) {
				t.Fatalf("the existing app was not brought into view and focused: %v, search %v, focused=%v", row, search, tt.Focused(label))
			}
			if got, err := store.List(); err != nil || len(got) != len(apps) || got[33] != target || backend.opened != "" || backend.builds != 1 {
				t.Fatalf("duplicate creation changed the library or opened an app: %d records, %d installs, open=%q, error %v", len(got), backend.builds, backend.opened, err)
			}
			if d.appsQuery != "" || d.appArt.get(target.ID) == nil {
				t.Fatal("revealing the app left an unrelated filter or lost its artwork")
			}
			if len(probe.reads) > 32 {
				t.Fatalf("revealing one app read artwork for %d of %d profiles (%d already read before saving)", len(probe.reads), len(apps), previousReads)
			}
			t.Logf("the jump read %d additional icons (%d total for %d profiles)", len(probe.reads)-previousReads, len(probe.reads), len(apps))
			shot(t, tt, "library-existing-app-"+test.name)
			tt.Key(0, ui.KeyEnter)
			if backend.opened != installed || backend.builds != 1 {
				t.Fatalf("Enter opened %q with %d builds, want existing app at %q", backend.opened, backend.builds, installed)
			}
		})
	}
}

func TestNativeLibrarySearchStartsAtFirstMatch(t *testing.T) {
	path, apps := savedLibrary(t, 120, "")
	service := library.New(profile.NewStore(path), bundle.NewLibrary(t.TempDir(), &testingBundle{}), library.Options{})
	d := newDesktopApp(service, &testingIcons{}, nil, nil)
	d.work = func(f func()) { f() }
	d.loadLibrary()
	tt := ui.NewTesterWith(d.view, minWindowWidth, minWindowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	tt.SetTitleBar(ui.TitleBar{Height: titleHeight, Left: 66, Bottom: 28})
	const search = "搜索应用库"
	click(t, tt, "应用库")
	first := "打开 " + apps[len(apps)-1].Name
	top, ok := tt.Find(first)
	if !ok {
		t.Fatal("the library did not start at its newest app")
	}
	scrollAway := func(label string) {
		t.Helper()
		tt.Scroll(minWindowWidth/2, minWindowHeight-40, 0, 1500)
		tt.Advance(180 * time.Millisecond)
		if row, visible := tt.Find(label); visible && row.Y >= top.Y {
			t.Fatalf("the library did not scroll away from %q: %v", label, row)
		}
	}
	expectTop := func(label string) {
		t.Helper()
		if row, visible := tt.Find(label); !visible || row.Y != top.Y || !tt.Focused(search) {
			t.Fatalf("the search left %q at %v, want y=%g with input focus", label, row, top.Y)
		}
	}
	scrollAway(first)
	click(t, tt, search)
	tt.Type("Workspace 0")
	const match = "打开 Workspace 099"
	expectTop(match)
	scrollAway(match)
	click(t, tt, "清除搜索")
	expectTop(first)
	if seventh, visible := tt.Find("打开 Workspace 113"); !visible || seventh.H != top.H || seventh.Y+seventh.H > minWindowHeight {
		t.Fatalf("seven complete library rows did not fit the minimum window: %+v", seventh)
	}
}

func TestNativeLibrarySearchOpensWithKeyboard(t *testing.T) {
	path, apps := savedLibrary(t, 20, "")
	backend := &testingBundle{}
	bundles := bundle.NewLibrary(t.TempDir(), backend)
	service := library.New(profile.NewStore(path), bundles, library.Options{})
	d := newDesktopApp(service, &testingIcons{}, nil, nil)
	d.work = func(f func()) { f() }
	d.loadLibrary()
	d.busyApps[apps[19].ID] = "正在打开…"
	tt := ui.NewTesterWith(d.view, minWindowWidth, minWindowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	const search = "搜索应用库"
	click(t, tt, "应用库")
	click(t, tt, search)
	tt.Type("Workspace 01")
	keyFocus := func(key ui.Key, label string) {
		t.Helper()
		tt.Key(0, key)
		tt.Advance(30 * time.Millisecond)
		if !tt.Focused(label) {
			t.Fatalf("key %v did not focus %q: %v", key, label, tt.Texts())
		}
	}
	keyFocus(ui.KeyEnter, search) // A multi-result search does not select a site implicitly.
	if backend.opened != "" {
		t.Fatal("Enter opened an ambiguous search result")
	}
	keyFocus(ui.KeyDown, "打开 Workspace 018")
	keyFocus(ui.KeyUp, search)
	for i := 18; i >= 10; i-- {
		keyFocus(ui.KeyDown, "打开 "+apps[i].Name)
	}
	last, ok := tt.Find("打开 Workspace 010")
	if !ok || last.Y+last.H > minWindowHeight {
		t.Fatalf("the keyboard did not fully reveal the last row: %+v", last)
	}
	keyFocus(ui.KeyDown, "打开 Workspace 010")
	keyFocus(ui.KeyUp, "打开 Workspace 011")
	keyFocus(ui.KeyEscape, search)
	for _, query := range []string{"no matches", "Workspace 019"} {
		typeReplace(tt, query)
		keyFocus(ui.KeyDown, search)
		keyFocus(ui.KeyEnter, search)
		if backend.opened != "" {
			t.Fatalf("the search %q opened an unavailable app", query)
		}
	}
	typeReplace(tt, "site-012.example.org Workspace")
	if _, found := tt.Find("打开 Workspace 013"); !tt.HasText("打开 Workspace 012") || found {
		t.Fatalf("combining the domain and name did not identify a single app: %v", tt.Texts())
	}
	keyFocus(ui.KeyEnter, search)
	if !strings.Contains(backend.opened, string(filepath.Separator)+apps[12].ID+string(filepath.Separator)) {
		t.Fatalf("Enter did not open the unique result: %q", backend.opened)
	}
	typeReplace(tt, "Workspace 013")
	keyFocus(ui.KeyDown, "打开 Workspace 013")
	tt.Key(0, ui.KeyEnter)
	if !strings.Contains(backend.opened, string(filepath.Separator)+apps[13].ID+string(filepath.Separator)) {
		t.Fatalf("Enter did not open the focused row: %q", backend.opened)
	}
}

func TestNativeEditingKeepsTheLibraryScroll(t *testing.T) {
	path, apps := savedLibrary(t, 120, "")
	backend := &testingBundle{}
	bundles := bundle.NewLibrary(t.TempDir(), backend)
	service := library.New(profile.NewStore(path), bundles, library.Options{})
	d := newDesktopApp(service, &testingIcons{}, nil, nil)
	d.work = func(f func()) { f() }
	d.loadLibrary()
	tt := ui.NewTesterWith(d.view, minWindowWidth, minWindowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	click(t, tt, "应用库")
	tt.Scroll(minWindowWidth/2, minWindowHeight-40, 0, 1470)
	tt.Advance(180 * time.Millisecond)
	search, _ := tt.Find("搜索应用库")
	var target profile.App
	var y float32
	for _, app := range apps[:len(apps)-12] {
		if row, ok := tt.Find("打开 " + app.Name); ok && row.Y >= search.Y+search.H && row.Y+row.H < minWindowHeight/2 {
			target, y = app, row.Y
			break
		}
	}
	if target.ID == "" {
		t.Fatal("scrolling did not reach an older app at the top of the viewport")
	}
	if _, err := bundles.Install(t.Context(), target, target, icons.Artwork{Image: icons.Brand()}, nil); err != nil {
		t.Fatal(err)
	}
	firstItem := &d.items[0]
	click(t, tt, target.Name+" 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "保存")
	label := "打开 " + target.Name
	row, visible := tt.Find(label)
	if !visible || row.Y != y || !tt.Focused(label) || &d.items[0] != firstItem || backend.builds != 1 {
		t.Fatalf("returning to the unchanged app moved the list or did extra work: y=%v (was %v), focus=%t, builds=%d",
			row.Y, y, tt.Focused(label), backend.builds)
	}
}

func savedLibrary(t testing.TB, count int, iconKey string) (string, []profile.App) {
	t.Helper()
	path := filepath.Join(t.TempDir(), profile.Filename)
	return path, writeLibraryFixture(t, path, count, iconKey)
}

func writeLibraryFixture(t testing.TB, path string, count int, iconKey string) []profile.App {
	t.Helper()
	apps := make([]profile.App, count)
	for i := range apps {
		date := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)
		apps[i] = profile.App{
			ID: profile.NewID(), Name: fmt.Sprintf("Workspace %03d", i), URL: fmt.Sprintf("https://site-%03d.example.org/board", i),
			Width: 880, Height: 640, Category: "效率", Color: profile.DefaultColor, CustomIcon: iconKey,
			CreatedAt: date, UpdatedAt: date,
		}
	}
	data, err := json.Marshal(apps)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return apps
}

func BenchmarkCreatorFirstFrame(b *testing.B) {
	for _, page := range []struct {
		name, firstControl string
		library            bool
	}{
		{name: "create", firstControl: "选择 DeepSeek"},
		{name: "library", firstControl: "创建第一个应用", library: true},
	} {
		b.Run(page.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sites, err := catalog.Load()
				if err != nil {
					b.Fatal(err)
				}
				desktop := newDesktopApp(nil, nil, nil, sites)
				if page.library {
					desktop.page = 1
				}
				tester := ui.NewTesterWith(desktop.view, windowWidth, windowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
				if _, ok := tester.Find(page.firstControl); !ok {
					b.Fatalf("the initial view lacks %q", page.firstControl)
				}
			}
		})
	}
}

func BenchmarkDesktopLibraryLoad(b *testing.B) {
	custom := icons.NewCustomStore(b.TempDir())
	key, _, err := custom.Put(icons.Resize(icons.Brand(), 256))
	if err != nil {
		b.Fatal(err)
	}
	path, apps := savedLibrary(b, 120, key)
	desktop := newDesktopApp(nil, &testingIcons{}, custom, nil)
	desktop.work = func(f func()) { f() }
	b.ReportAllocs()
	for b.Loop() {
		desktop.library = library.New(profile.NewStore(path), nil, library.Options{CustomIcons: custom})
		desktop.loadLibrary()
		if len(desktop.apps) != len(apps) {
			b.Fatal("saved library did not load")
		}
	}
}

func BenchmarkDesktopLibraryItems(b *testing.B) {
	path, _ := savedLibrary(b, 120, "")
	service := library.New(profile.NewStore(path), nil, library.Options{})
	desktop := newDesktopApp(service, &testingIcons{}, nil, nil)
	desktop.work = func(f func()) { f() }
	desktop.loadLibrary()
	desktop.appsQuery = "site-01"
	b.ReportAllocs()
	for b.Loop() {
		if count := len(desktop.libraryItems()); count != 10 {
			b.Fatalf("found %d matching apps", count)
		}
	}
}

func BenchmarkDesktopLibrarySearchChange(b *testing.B) {
	path, _ := savedLibrary(b, 1200, "")
	service := library.New(profile.NewStore(path), nil, library.Options{})
	desktop := newDesktopApp(service, &testingIcons{}, nil, nil)
	desktop.work = func(f func()) { f() }
	desktop.loadLibrary()
	for _, test := range []struct {
		name    string
		queries [2]string
	}{
		{"Phrase", [2]string{"Workspace 1024", "Workspace 1124"}},
		{"Combined", [2]string{"site-1024.example.org Workspace", "site-1124.example.org Workspace"}},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			query := 0
			for b.Loop() {
				desktop.appsQuery = test.queries[query]
				if count := len(desktop.libraryItems()); count != 1 {
					b.Fatalf("found %d matches for %q", count, desktop.appsQuery)
				}
				query = 1 - query
			}
		})
	}
}

func BenchmarkStoredApplicationLookup(b *testing.B) {
	path, apps := savedLibrary(b, 120, "")
	store := profile.NewStore(path)
	wanted := apps[len(apps)-1]
	b.ReportAllocs()
	for b.Loop() {
		found, err := store.Find(wanted.ID)
		if err != nil || found != wanted {
			b.Fatalf("looking up application: %+v, %v", found, err)
		}
	}
}
