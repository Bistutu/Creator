package main

import (
	"context"
	"errors"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/catalog"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

type testingBundle struct {
	opened     string
	buildError error
	builds     int
	lastIcon   image.Image
}

func (b *testingBundle) Filename(app profile.App) string { return app.Name + ".fixture" }
func (b *testingBundle) Build(_ context.Context, path string, app profile.App, source func() icons.Artwork, _ string) error {
	b.builds++
	if b.buildError != nil {
		return b.buildError
	}
	icon := source()
	if icon.Image == nil {
		return errors.New("missing app icon")
	}
	b.lastIcon = icon.Image
	return os.WriteFile(path, []byte(app.URL), 0o600)
}
func (b *testingBundle) Open(_ context.Context, path string) error { b.opened = path; return nil }

type testingIcons struct {
	img              image.Image
	lookups          []string
	name, cachedName string
}

func (*testingIcons) Cached(string) image.Image  { return nil }
func (i *testingIcons) CachedName(string) string { return i.cachedName }
func (i *testingIcons) Inspect(_ context.Context, addr string, onName func(string)) (image.Image, error) {
	i.lookups = append(i.lookups, addr)
	if i.name != "" {
		onName(i.name)
	}
	return i.img, nil
}

func nativeFixture(t *testing.T, width, height int) (*desktopApp, *ui.Tester, *testingBundle, *testingIcons) {
	t.Helper()
	presets, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	backend, resolver := &testingBundle{}, &testingIcons{img: icons.Brand()}
	root := t.TempDir()
	customIcons := icons.NewCustomStore(filepath.Join(root, "CustomIcons"))
	service := library.New(profile.NewStore(filepath.Join(root, "apps.json")),
		bundle.NewLibrary(filepath.Join(root, "Applications"), backend), library.Options{CustomIcons: customIcons})
	d := newDesktopApp(service, resolver, customIcons, presets)
	d.chooseIcon = func() (image.Image, error) { return nil, nil }
	d.work = func(f func()) { f() }
	d.loadLibrary()
	tester := ui.NewTesterWith(d.view, width, height, ui.TesterOptions{Dark: true, Preferences: ui.Preferences{ReduceMotion: true}})
	tester.SetTitleBar(ui.TitleBar{Height: titleHeight, Left: 66, Bottom: 28})
	return d, tester, backend, resolver
}

func TestNativeRecommendationWorkflow(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, windowWidth, windowHeight)
	shot(t, tt, "create")
	if first, ok := tt.Find("选择 GitHub"); !ok || first.Y > 166 || first.W > 86 {
		t.Fatalf("the first recommendation starts at %v", first)
	}
	if last, ok := tt.Find("选择 Hacker News"); !ok || last.Y+last.H > windowHeight {
		t.Fatalf("the last recommendation overflows the default window: %v", last)
	}
	if name, ok := tt.Find("Hacker News"); !ok || name.H < 10 || name.Y+name.H > windowHeight-10 {
		t.Fatalf("the last recommendation's name is cropped: %v", name)
	}
	click(t, tt, "AI")
	if tt.HasText("选择 Spotify") {
		t.Fatal("category filter left music visible")
	}
	click(t, tt, "全部")
	click(t, tt, "选择 GitHub")
	if d.draft.icon != d.presetIcon("github") || len(resolver.lookups) != 0 {
		t.Fatal("preset did not get its offline icon")
	}
	shot(t, tt, "editor")
	if tt.HasText("窗口宽度") {
		t.Fatal("extra settings are shown by default")
	}
	click(t, tt, "窗口尺寸")
	if !tt.HasText("窗口宽度") {
		t.Fatal("window size is not reachable")
	}
	click(t, tt, "窗口宽度（像素）")
	typeReplace(tt, "invalid")
	click(t, tt, "创建应用")
	if backend.builds != 0 || !tt.HasText("请输入有效的窗口尺寸") {
		t.Fatal("invalid window size attempted to build the app")
	}
	click(t, tt, "窗口宽度（像素）")
	typeReplace(tt, "880")
	click(t, tt, "创建应用")
	if len(d.apps) != 1 || d.page != 1 || d.apps[0].Name != "GitHub" || d.apps[0].Width != 880 {
		t.Fatalf("created app = %+v, page=%d, width=%q error=%q (%v)", d.apps, d.page, d.draft.width, d.draft.issue.message, tt.Texts())
	}
	shot(t, tt, "library")
	click(t, tt, "打开 GitHub")
	if !strings.HasSuffix(backend.opened, "GitHub.fixture") {
		t.Fatalf("opened path %q", backend.opened)
	}
	oldID := d.apps[0].ID
	click(t, tt, "GitHub 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "应用名称")
	typeReplace(tt, "GitHub Desk")
	click(t, tt, "保存")
	if len(d.apps) != 1 || d.apps[0].Name != "GitHub Desk" || d.apps[0].ID != oldID {
		t.Fatalf("edited app = %+v (draft=%+v)", d.apps, d.draft.app)
	}
	d.reveal = func(path string) { backend.opened = path }
	click(t, tt, "GitHub Desk 的选项")
	click(t, tt, "在文件管理器中显示")
	if !strings.HasSuffix(backend.opened, "GitHub Desk.fixture") {
		t.Fatalf("revealed path %q", backend.opened)
	}
	click(t, tt, "GitHub Desk 的选项")
	click(t, tt, "移除应用")
	click(t, tt, "取消")
	if len(d.apps) != 1 {
		t.Fatal("cancel removed the app")
	}
	click(t, tt, "GitHub Desk 的选项")
	click(t, tt, "移除应用")
	click(t, tt, "移除")
	if len(d.apps) != 0 || !tt.HasText("你的应用会出现在这里") {
		t.Fatal("removed app is still in the library")
	}
	click(t, tt, "创建第一个应用")
	if d.page != 0 {
		t.Fatal("empty library did not return to creation")
	}
}

func TestNativeLibraryKeepsAppAfterFailedRemoval(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "选择 DeepSeek")
	click(t, tt, "创建应用")
	click(t, tt, "创建")
	click(t, tt, "选择 GitHub")
	click(t, tt, "创建应用")
	removal := &libraryWithRemovalError{appLibrary: d.library, err: errors.New("system entry is temporarily locked")}
	d.library = removal
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	removeDeepSeek := func() {
		t.Helper()
		click(t, tt, "DeepSeek 的选项")
		click(t, tt, "移除应用")
		click(t, tt, "移除")
	}
	removeDeepSeek()
	if len(work) != 1 || !tt.HasText("正在移除…") {
		t.Fatalf("the library did not show a pending removal: %v", tt.Texts())
	}
	for _, label := range []string{"打开 DeepSeek", "DeepSeek 的选项"} {
		button, ok := tt.Find(label)
		if !ok {
			t.Fatalf("the pending row lost %q", label)
		}
		tt.ClickAt(button.X+button.W/2, button.Y+button.H/2)
	}
	if len(work) != 1 || tt.HasText("编辑应用") {
		t.Fatal("the pending row accepted another action")
	}
	click(t, tt, "打开 GitHub")
	if len(work) != 2 {
		t.Fatal("the pending removal blocked the other app")
	}
	work[1]()
	tt.Frame()
	if filepath.Base(backend.opened) != "GitHub.fixture" || !tt.HasText("正在移除…") {
		t.Fatal("opening GitHub changed DeepSeek's pending removal")
	}
	work[0]()
	tt.Frame()
	if !tt.HasText("无法移除「DeepSeek」，请重试") || !tt.HasText("chat.deepseek.com") || tt.HasText("正在移除…") {
		t.Fatalf("failed removal did not restore the row and offer a retry: %v", tt.Texts())
	}
	click(t, tt, "打开 DeepSeek")
	if len(work) != 3 {
		t.Fatal("DeepSeek could no longer open after failure")
	}
	work[2]()
	tt.Frame()
	if filepath.Base(backend.opened) != "DeepSeek.fixture" {
		t.Fatalf("opened %q after failing to remove DeepSeek", backend.opened)
	}
	removal.err = nil
	removeDeepSeek()
	if len(work) != 4 {
		t.Fatal("retry was not submitted")
	}
	work[3]()
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0].Name != "GitHub" || !tt.HasText("已移除「DeepSeek」") {
		t.Fatalf("retry did not remove just DeepSeek: %v", tt.Texts())
	}
}

type libraryWithRemovalError struct {
	appLibrary
	err error
}

func (l *libraryWithRemovalError) Remove(id string) error {
	if l.err != nil {
		return l.err
	}
	return l.appLibrary.Remove(id)
}

func TestNativeAddressWorkflowAndRetry(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	shot(t, tt, "compact")
	click(t, tt, "网址或应用名称")
	tt.Type("之前的搜索")
	click(t, tt, "应用库")
	click(t, tt, "创建第一个应用")
	if !tt.Focused("网址或应用名称") || d.query != "之前的搜索" {
		t.Fatal("creating the first app did not return to the preserved search")
	}
	tt.Type("missing site")
	if d.query != "missing site" {
		t.Fatal("typing after creating the first app appended to the previous search")
	}
	tt.Key(0, ui.KeyEnter)
	if d.draft.open || !tt.HasText("输入有效的网址") || tt.HasText("没有匹配的网站") {
		t.Fatalf("invalid search: %v", tt.Texts())
	}
	click(t, tt, "清除搜索")
	if d.searchError != "" || d.query != "" || !tt.Focused("网址或应用名称") {
		t.Fatal("clearing the invalid address did not return to an empty input")
	}
	tt.Type("example.org/work")
	tt.Key(0, ui.KeyEnter)
	if !d.draft.open || d.draft.app.URL != "https://example.org/work" || len(resolver.lookups) != 1 || d.draft.icon.img != resolver.img {
		t.Fatalf("typed address: %+v, lookups=%v", d.draft.app, resolver.lookups)
	}
	backend.buildError = errors.New("simulated disk failure")
	click(t, tt, "创建应用")
	if !d.draft.open || len(d.apps) != 0 || !tt.HasText("无法保存应用") {
		t.Fatal("creation failure closed the form or saved the app")
	}
	backend.buildError = nil
	click(t, tt, "创建应用")
	if len(d.apps) != 1 || d.apps[0].Name != "example.org" {
		t.Fatalf("retry saved %+v", d.apps)
	}
	click(t, tt, "搜索应用库")
	tt.Type("does not match")
	if !tt.HasText("没有匹配的应用") {
		t.Fatal("library search did not filter")
	}
	click(t, tt, "清除搜索")
	if d.appsQuery != "" || !tt.Focused("搜索应用库") || !tt.HasText("打开 example.org") || tt.HasText("没有匹配的应用") {
		t.Fatal("clearing the library filter did not restore the app and input focus")
	}
}

func TestNativeLibraryAvoidsDuplicatesAndKeepsExistingCopiesReachable(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, minWindowWidth, windowHeight)
	click(t, tt, "选择 DeepSeek")
	click(t, tt, "创建应用")
	if len(d.apps) != 1 {
		t.Fatalf("first DeepSeek save left %d apps", len(d.apps))
	}
	original := d.apps[0]
	if !tt.Focused("打开 DeepSeek") || backend.opened != "" {
		t.Fatal("creating DeepSeek did not leave it focused in the library")
	}
	tt.Key(0, ui.KeyEnter)
	if filepath.Base(backend.opened) != "DeepSeek.fixture" {
		t.Fatalf("Enter opened %q after creating DeepSeek", backend.opened)
	}
	click(t, tt, "创建")
	click(t, tt, "选择 DeepSeek")
	click(t, tt, "创建应用")
	if len(d.apps) != 1 || d.apps[0] != original || backend.builds != 1 || d.page != 1 || d.draft.open {
		t.Fatalf("the repeated action created another app or stayed in the form: %+v (%d builds)", d.apps, backend.builds)
	}
	if !tt.HasText("「DeepSeek」已在应用库中") || tt.HasText("添加应用") {
		t.Fatalf("library displayed the wrong actions: %v", tt.Texts())
	}

	legacy := original
	legacy.ID = profile.NewID()
	legacy.CreatedAt = original.CreatedAt.Add(time.Second)
	d.record(legacy, d.appArt.get(original.ID))
	tt.Frame()
	search, ok := tt.Find("搜索应用库")
	menu, hasMenu := tt.Find("DeepSeek (2) 的选项")
	row, hasRow := tt.Find("打开 DeepSeek (2)")
	if !ok || search.W < minWindowWidth-2*pagePadding-2 || !hasMenu || !hasRow || menu.X+menu.W > minWindowWidth-pagePadding || row.X+row.W > menu.X {
		t.Fatalf("library items do not fit the narrow window: search=%v row=%v menu=%v", search, row, menu)
	}
	click(t, tt, "DeepSeek (2) 的选项")
	click(t, tt, "编辑应用")
	if d.draft.app.ID != legacy.ID {
		t.Fatal("legacy row menu opened the wrong app")
	}
	click(t, tt, "取消")
	click(t, tt, "搜索应用库")
	typeReplace(tt, "(2)")
	if items := d.libraryItems(); len(items) != 1 || items[0].app.ID != legacy.ID {
		t.Fatalf("search did not find the indicated copy: %+v", items)
	}
}

func TestNativeKeyboardFocusKeepsCardsUnobstructed(t *testing.T) {
	t.Run("Recommendation", func(t *testing.T) {
		_, tt, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
		click(t, tt, "网址或应用名称")
		tt.Key(0, ui.KeyDown)
		tt.Key(ui.Shift, ui.KeyTab)
		tt.Key(0, ui.KeyTab)
		tt.Frame()
		const label, hint = "选择 GitHub", "GitHub · github.com"
		if !tt.Focused(label) || tt.HasText(hint) {
			t.Fatalf("keyboard selection rendered an overlay or lost focus: %v", tt.Texts())
		}
		tt.Key(0, ui.KeyEscape)
		if !tt.Focused("网址或应用名称") {
			t.Fatal("Escape did not return focus to the search")
		}
		name, ok := tt.Find("GitHub")
		if !ok {
			t.Fatal("the first recommendation name is not visible")
		}
		tt.Move(name.X+name.W/2, name.Y+name.H/2)
		tt.Advance(500 * time.Millisecond)
		if tt.HasText(hint) {
			t.Fatal("the hover hint appeared before the pointer rested")
		}
		tt.Advance(150 * time.Millisecond)
		if !tt.HasText(hint) {
			t.Fatal("the pointer could not reveal the complete recommendation name and domain")
		}
		tt.Move(1, 1)
		if tt.HasText(hint) {
			t.Fatal("the hint stayed after the pointer left")
		}
	})

	t.Run("Library", func(t *testing.T) {
		_, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
		const address = "https://example.org:8443/desk/view?mode=work#today"
		click(t, tt, "网址或应用名称")
		tt.Type(address)
		tt.Key(0, ui.KeyDown)
		if !tt.Focused("使用网址 example.org") {
			t.Fatal("Down did not focus the URL preview")
		}
		card, ok := tt.Find("使用网址 example.org")
		if !ok {
			t.Fatal("the focused preview was not visible")
		}
		tt.Move(card.X+card.W/2, card.Y+card.H/2)
		tt.Advance(650 * time.Millisecond)
		occurrences := 0
		for _, text := range tt.Texts() {
			if text == address {
				occurrences++
			}
		}
		if occurrences != 1 {
			t.Fatalf("the focused URL card rendered the same address %d times", occurrences)
		}
		tt.Key(0, ui.KeyEnter)
		click(t, tt, "应用名称")
		typeReplace(tt, "Work desk")
		click(t, tt, "创建应用")
		row, ok := tt.Find("打开 Work desk")
		if !ok {
			t.Fatal("the focused library row was not visible")
		}
		tt.Move(row.X+row.W/2, row.Y+row.H/2)
		tt.Advance(650 * time.Millisecond)
		if !tt.Focused("打开 Work desk") || tt.HasText("Work desk · "+address) {
			t.Fatalf("the focused library entry rendered an overlay or lost focus: %v", tt.Texts())
		}
		tt.Key(0, ui.KeyEnter)
		if filepath.Base(backend.opened) != "Work desk.fixture" {
			t.Fatalf("Enter opened %q", backend.opened)
		}
	})
}

func TestNativeLibraryCopiesFullAddress(t *testing.T) {
	for _, appearance := range []struct {
		name string
		dark bool
	}{{"Light", false}, {"Dark", true}} {
		t.Run(appearance.name, func(t *testing.T) {
			d, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
			tt.SetDark(appearance.dark)
			const first = "https://example.org:8443/team/weekly?view=board#notes"
			const second = "https://example.org:8443/team/today?view=table#work"
			for _, address := range []string{first, second} {
				click(t, tt, "创建")
				click(t, tt, "网址或应用名称")
				pasteAddress(tt, address)
				tt.Key(0, ui.KeyEnter)
				click(t, tt, "应用名称")
				typeReplace(tt, "Project")
				click(t, tt, "创建应用")
			}
			if len(d.apps) != 2 || backend.builds != 2 || !tt.Focused("打开 Project (2)") {
				t.Fatalf("saving two workspace addresses left the library in the wrong state: %v", tt.Texts())
			}
			for _, summary := range []string{"example.org:8443/team/weekly", "example.org:8443/team/today"} {
				if _, ok := tt.Find(summary); !ok {
					t.Fatalf("the library does not identify %q in the compact window: %v", summary, tt.Texts())
				}
			}
			if tt.HasText("view=") {
				t.Fatal("the row showed the query in its visible address")
			}
			tt.SetClipboard("previous contents")
			tt.FailClipboard(errors.New("clipboard busy"))
			click(t, tt, "Project (2) 的选项")
			click(t, tt, "复制网址")
			if !tt.HasText("无法复制网址，请重试") || tt.Clipboard() != "previous contents" {
				t.Fatalf("the failed copy replaced the clipboard or gave no feedback: %v", tt.Texts())
			}
			tt.FailClipboard(nil)
			for _, choice := range []struct{ label, address string }{
				{"Project (2) 的选项", second},
				{"Project 的选项", first},
			} {
				click(t, tt, choice.label)
				click(t, tt, "复制网址")
				if got := tt.Clipboard(); got != choice.address || !tt.HasText("已复制网址") {
					t.Fatalf("copying from %q put %q on the clipboard: %v", choice.label, got, tt.Texts())
				}
			}
			if len(d.apps) != 2 || backend.builds != 2 || backend.opened != "" {
				t.Fatal("copying the address changed or opened an installed app")
			}
		})
	}
}

func TestNativeLibraryDistinguishesCollidingAddresses(t *testing.T) {
	d, tt, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	const base = "https://example.org:8443/team"
	const first = base + "?view=board#today"
	const second = base + "?view=board#review"
	const unique = "https://example.org:8443/inbox?view=table#today"
	for _, entry := range []struct{ address, name string }{
		{unique, "Inbox"}, {unique, "Inbox Copy"}, {first, "Project"}, {second, "Project"},
	} {
		click(t, tt, "创建")
		click(t, tt, "网址或应用名称")
		pasteAddress(tt, entry.address)
		tt.Key(0, ui.KeyEnter)
		click(t, tt, "应用名称")
		typeReplace(tt, entry.name)
		click(t, tt, "创建应用")
	}
	if len(d.apps) != 4 {
		t.Fatalf("the saved workspace entries are %+v", d.apps)
	}
	for _, label := range []string{first, second, "example.org:8443/inbox"} {
		if caption, found := tt.Find(label); !found || caption.H >= 23 {
			t.Fatalf("the compact library did not show the address %q on one line: %v", label, tt.Texts())
		}
	}
	if _, found := tt.Find(unique); found {
		t.Fatal("two installations of an identical address expanded their compact captions")
	}
	click(t, tt, "搜索应用库")
	tt.Type("#review")
	if _, other := tt.Find("打开 Project"); !tt.HasText("打开 Project (2)") || other || !tt.HasText(second) {
		t.Fatalf("filtering by the fragment did not identify the matching installation: %v", tt.Texts())
	}
}

func TestNativeFeaturedPlatformsOpenFromSearchAliases(t *testing.T) {
	for _, site := range []struct {
		name, mark, category, alias, url string
	}{
		{"DeepSeek", "deepseek", "AI", "深度求索", "https://chat.deepseek.com"},
		{"小红书", "xiaohongshu", "社交", "Rednote", "https://www.xiaohongshu.com/explore"},
	} {
		t.Run(site.name, func(t *testing.T) {
			d, tt, backend, resolver := nativeFixture(t, windowWidth, windowHeight)
			first, firstVisible := tt.Find("选择 GitHub")
			card, visible := tt.Find("选择 " + site.name)
			if !firstVisible || !visible || card.Y != first.Y || card.X+card.W > windowWidth {
				t.Fatalf("%s was outside the first row: %v", site.name, card)
			}
			icon := d.presetIcon(site.mark)
			click(t, tt, "选择 "+site.name)
			if !d.draft.open || d.draft.app.URL != site.url || d.draft.icon != icon || icon == nil {
				t.Fatalf("opening %s from the first row did not use its preset", site.name)
			}
			click(t, tt, "取消")
			click(t, tt, site.category)
			click(t, tt, "网址或应用名称")
			tt.Type(site.alias)
			if !tt.HasText("选择 " + site.name) {
				t.Fatalf("searching for %q did not show %s", site.alias, site.name)
			}
			tt.Key(0, ui.KeyEnter)
			if !d.draft.open || d.draft.app.URL != site.url || d.draft.icon != icon || len(resolver.lookups) != 0 {
				t.Fatalf("preset did not open with its embedded icon: %+v", d.draft)
			}
			click(t, tt, "创建应用")
			if len(d.apps) != 1 || d.apps[0].Name != site.name || d.apps[0].URL != site.url ||
				d.apps[0].Category != site.category || backend.lastIcon != icon.img {
				t.Fatalf("saved app = %+v", d.apps)
			}
		})
	}
}

func TestNativeScenarioSearchKeepsChoiceAndExplicitAddresses(t *testing.T) {
	d, tt, _, _ := nativeFixture(t, windowWidth, windowHeight)
	click(t, tt, "AI")
	click(t, tt, "网址或应用名称")
	tt.Type("写作")
	tt.Key(0, ui.KeyEnter)
	if d.draft.open || !tt.HasText("选择 ChatGPT") || !tt.HasText("选择 Claude") {
		t.Fatal("multiple scenario matches should remain selectable")
	}
	const combined = "Claude　编程"
	typeReplace(tt, combined)
	if !tt.HasText("选择 Claude") || tt.HasText("选择 ChatGPT") {
		t.Fatalf("the name and scenario did not narrow the results: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if !d.draft.open || d.draft.app.URL != "https://claude.ai" || d.draft.icon != d.presetIcon("claude") {
		t.Fatalf("the combined search did not open Claude with its embedded icon: %+v", d.draft.app)
	}
	tt.Key(0, ui.KeyEscape)
	if !tt.Focused("网址或应用名称") || d.query != combined {
		t.Fatal("canceling a combined search result did not restore the query and keyboard focus")
	}
	const address = "https://chat.deepseek.com/a/chat?source=creator"
	typeReplace(tt, address)
	tt.Key(0, ui.KeyEnter)
	if !d.draft.open || d.draft.app.URL != address || d.draft.app.Name != "DeepSeek" {
		t.Fatalf("typed address was not kept: %+v", d.draft.app)
	}
}

func TestNativeCreateSearchKeyboardNavigation(t *testing.T) {
	d, tt, _, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "AI")
	click(t, tt, "网址或应用名称")
	tt.Type("写作")
	tt.Key(0, ui.KeyEnter)
	if d.draft.open || !tt.Focused("网址或应用名称") || d.searchError != "" {
		t.Fatal("an ambiguous search did not let the user continue typing")
	}
	tt.Key(0, ui.KeyDown)
	if !tt.Focused("选择 ChatGPT") {
		t.Fatal("Down did not focus the first visible recommendation")
	}
	tt.Key(0, ui.KeyRight)
	if !tt.Focused("选择 Gemini") {
		t.Fatal("Right did not focus the adjacent result")
	}
	tt.Key(0, ui.KeyRight)
	if !tt.Focused("选择 Claude") {
		t.Fatal("Right did not reach Claude")
	}
	shot(t, tt, "keyboard-search-choice")
	tt.Key(0, ui.KeyEnter)
	if !d.draft.open || d.draft.app.URL != "https://claude.ai" || d.draft.icon != d.presetIcon("claude") || len(resolver.lookups) != 0 {
		t.Fatalf("Enter did not open the focused result with its local icon: %+v", d.draft.app)
	}
	tt.Key(0, ui.KeyEscape)
	if d.draft.open || !tt.Focused("选择 Claude") {
		t.Fatal("closing the form did not return focus to the result")
	}
	tt.Key(0, ui.KeyEscape)
	if !tt.Focused("网址或应用名称") || d.query != "写作" {
		t.Fatal("Escape did not return to the original search")
	}
	typeReplace(tt, "小红书")
	tt.Key(0, ui.KeyEnter)
	if d.draft.open || tt.HasText("选择 小红书") || d.searchError == "" {
		t.Fatal("Enter opened a recommendation hidden by the current category")
	}
}

func TestNativeCreateKeyboardNavigatesRowsAndAddress(t *testing.T) {
	d, tt, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "网址或应用名称")
	const address = "https://www.xiaohongshu.com/explore/topic?source=creator#notes"
	tt.Type(address)
	tt.Key(0, ui.KeyDown)
	if !tt.Focused("使用网址 小红书") {
		t.Fatal("Down did not focus the full address card")
	}
	tt.Key(0, ui.KeyEnter)
	if !d.draft.open || d.draft.app.URL != address {
		t.Fatalf("opening the focused URL card lost the address: %+v", d.draft.app)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyDown)
	if !tt.Focused("选择 GitHub") {
		t.Fatal("Down did not move from the address into the recommendations")
	}
	tt.Key(0, ui.KeyRight)
	tt.Key(0, ui.KeyDown)
	if !tt.Focused("选择 Notion") {
		t.Fatal("Down did not keep the same column in the next row")
	}
	tt.Key(0, ui.KeyUp)
	if !tt.Focused("选择 ChatGPT") {
		t.Fatal("Up did not return to the same column in the first row")
	}
	tt.Key(0, ui.KeyLeft)
	if !tt.Focused("选择 GitHub") {
		t.Fatal("Left did not move back across the row")
	}
	tt.Key(0, ui.KeyUp)
	if !tt.Focused("使用网址 小红书") {
		t.Fatal("Up at the first row did not return to the address card")
	}
	tt.Key(0, ui.KeyUp)
	if !tt.Focused("网址或应用名称") {
		t.Fatal("Up at the address card did not return to the input")
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnd)
	if last, ok := tt.Find("选择 Hacker News"); !ok || !tt.Focused("选择 Hacker News") || last.Y+last.H > minWindowHeight {
		t.Fatalf("End did not scroll the focused card into view: %v", last)
	}
	tt.SetPreferences(ui.Preferences{})
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Advance(170 * time.Millisecond)
	if !d.draft.open || d.draft.app.URL != "https://news.ycombinator.com" {
		t.Fatalf("Enter did not open the scrolled card: %+v", d.draft.app)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Advance(150 * time.Millisecond)
	if d.draft.open || !tt.Focused("选择 Hacker News") {
		t.Fatal("the animated form did not return focus to the last card")
	}
}

func TestNativeRecommendationsResetScrollOnSearchChange(t *testing.T) {
	const input, first = "网址或应用名称", "选择 DeepSeek"
	const address = "https://www.xiaohongshu.com/explore/topic?source=creator#notes"
	for _, action := range []string{"Clear", "ReplaceAddress"} {
		t.Run(action, func(t *testing.T) {
			d, tt, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
			blank, ok := tt.Find(first)
			if !ok {
				t.Fatal("the first recommendation is missing")
			}
			click(t, tt, input)
			tt.Type(address)
			withAddress, _ := tt.Find(first)
			tt.Key(0, ui.KeyDown)
			tt.Key(0, ui.KeyDown)
			tt.Key(0, ui.KeyEnd)
			if !tt.Focused("选择 Hacker News") {
				t.Fatal("the keyboard did not reach the last recommendation")
			}
			tt.Key(0, ui.KeyEnter)
			tt.Key(0, ui.KeyEscape)
			tt.Key(0, ui.KeyEscape)
			if scrolled, _ := tt.Find(first); !tt.Focused(input) || scrolled.H >= withAddress.H {
				t.Fatalf("returning to the unchanged search moved the grid from %v to %v, input focused=%t", withAddress, scrolled, tt.Focused(input))
			}
			want := blank
			if action == "Clear" {
				tt.Key(0, ui.KeyTab)
				tt.Key(0, ui.KeyEnter)
				if d.query != "" {
					t.Fatalf("clearing the field left %q", d.query)
				}
			} else {
				const next = "https://chat.deepseek.com/"
				typeReplace(tt, next)
				if d.query != next || !tt.HasText("使用网址 DeepSeek") {
					t.Fatalf("replacing the URL did not update the preview: %q", d.query)
				}
				want = withAddress
			}
			if visible, ok := tt.Find(first); !ok || visible != want || !tt.Focused(input) {
				t.Fatalf("the changed search left the first row at %v, want %v with input focus", visible, want)
			}
			click(t, tt, first)
			if d.draft.app.URL != "https://chat.deepseek.com" {
				t.Fatalf("the first row was not actionable: %+v", d.draft.app)
			}
		})
	}
}

func TestNativeCreateSearchPreservesDirectActions(t *testing.T) {
	d, tt, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "添加")
	if !d.draft.open || d.draft.app.URL != "" || !tt.Focused("应用网址") {
		t.Fatal("Add with an empty input did not open the blank form")
	}
	tt.Key(0, ui.KeyEscape)
	const search = "网址或应用名称"
	click(t, tt, search)
	tt.Type("YouTube")
	tt.Key(0, ui.KeyEnter)
	if !d.draft.open || d.draft.app.URL != "https://www.youtube.com" {
		t.Fatalf("the exact name was lost among similar matches: %+v", d.draft.app)
	}
	tt.Key(0, ui.KeyEscape)
	typeReplace(tt, "xiaohongshu.com")
	tt.Key(0, ui.KeyEnter)
	if !d.draft.open || d.draft.app.URL != "https://www.xiaohongshu.com/explore" {
		t.Fatalf("the known domain lost its preset address: %+v", d.draft.app)
	}
	tt.Key(0, ui.KeyEscape)
	typeReplace(tt, "写作")
	if tt.HasText("添加") || !tt.HasText("清除搜索") {
		t.Fatal("a filled search did not replace Add with Clear")
	}
	tt.Key(0, ui.KeyDown)
	if d.draft.open || !tt.Focused("选择 ChatGPT") {
		t.Fatal("Down with several matches did not focus the first option")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("清除搜索") {
		t.Fatal("Tab did not reach Clear from the search field")
	}
	tt.Key(0, ui.KeyEnter)
	if d.query != "" || !tt.Focused(search) || !tt.HasText("添加") {
		t.Fatal("keyboard Clear did not restore the empty search")
	}
	tt.Key(0, ui.KeyEnter)
	if !d.draft.open || d.draft.app.URL != "" || !tt.Focused("应用网址") {
		t.Fatal("Enter on the empty search did not open the blank form")
	}
}

func TestNativePastedURLShowsWebsitePreview(t *testing.T) {
	d, tt, _, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "AI")
	const search = "网址或应用名称"
	blank, _ := tt.Find(search)
	click(t, tt, search)
	const rednote = "https://www.xiaohongshu.com/explore/topic?source=creator#notes"
	tt.Type(rednote)
	if card, ok := tt.Find("使用网址 小红书"); !ok || card.H < 38 || card.X+card.W > minWindowWidth {
		t.Fatalf("the pasted site has no usable preview: %v (%v)", card, tt.Texts())
	}
	if input, ok := tt.Find(search); !ok || input.W <= blank.W || !tt.HasText("清除搜索") || tt.HasText("添加") {
		t.Fatalf("the populated search did not give the address more room: empty %v, filled %v", blank, input)
	}
	if !tt.HasText("选择 DeepSeek") {
		t.Fatal("the URL preview displaced the recommendations for the selected category")
	}
	if len(resolver.lookups) != 0 || d.draft.open {
		t.Fatal("typing a URL started loading the website")
	}
	shot(t, tt, "pasted-website")
	click(t, tt, "使用网址 小红书")
	if !d.draft.open || d.draft.app.URL != rednote || d.draft.icon != d.presetIcon("xiaohongshu") || len(resolver.lookups) != 0 {
		t.Fatalf("selecting the preview did not preserve the URL and local icon: %+v", d.draft)
	}
	click(t, tt, "取消")
	click(t, tt, "网址或应用名称")
	typeReplace(tt, "https://example.org/projects")
	provisional := d.address.icon
	const other = "https://example.org/projects?tab=notes#current"
	typeReplace(tt, other)
	if !tt.HasText("使用网址 example.org") || provisional == nil || d.address.icon != provisional || len(resolver.lookups) != 0 {
		t.Fatalf("an unlisted site had no preview or fetched early: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if !d.draft.open || d.draft.app.URL != other || d.draft.icon.img != resolver.img ||
		len(resolver.lookups) != 1 || resolver.lookups[0] != other {
		t.Fatalf("Enter opened a different site: %+v (%v)", d.draft, resolver.lookups)
	}
}

func TestNativeDroppedWebsiteUsesExistingCreation(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, windowWidth, windowHeight)
	click(t, tt, "应用库")
	const address = "https://example.org/current%20work?tab=notes#today"
	data, err := transfer.URLData(address)
	if err != nil {
		t.Fatal(err)
	}
	resolver.name = "Current Work"
	runWork := d.work
	var work []func()
	d.work = func(fn func()) { work = append(work, fn) }
	d.receiveWebsiteLink(data)
	tt.Frame()
	if d.page != 0 || d.query != address || !tt.Focused("网址或应用名称") ||
		!tt.HasText("使用网址 example.org") || len(work) != 1 || backend.builds != 0 {
		t.Fatalf("the dropped link did not focus its preview: page=%d query=%q text=%v", d.page, d.query, tt.Texts())
	}
	work[0]()
	d.work = runWork
	tt.Frame()
	if !tt.HasText("使用网址 Current Work") || d.address.icon.img != resolver.img ||
		len(resolver.lookups) != 1 || resolver.lookups[0] != address {
		t.Fatalf("the preview lost the artwork or name: %v (%v)", tt.Texts(), resolver.lookups)
	}
	tt.Key(0, ui.KeyEnter)
	if !d.draft.open || d.draft.app.URL != address || d.draft.icon.img != resolver.img || len(resolver.lookups) != 1 {
		t.Fatalf("the editor did not carry over the full address and its artwork: %+v (%v)", d.draft, resolver.lookups)
	}
	click(t, tt, "创建应用")
	if backend.builds != 1 || len(d.apps) != 1 || d.apps[0].URL != address || d.apps[0].Name != resolver.name {
		t.Fatalf("the created app did not keep the dropped website: builds=%d apps=%+v", backend.builds, d.apps)
	}
}

func TestNativeDroppedWebsiteIgnoresOtherData(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, windowWidth, windowHeight)
	click(t, tt, "网址或应用名称")
	tt.Type("DeepSeek")
	click(t, tt, "应用库")
	for _, raw := range []string{
		"file:///tmp/example.webloc",
		"example.org",
		"javascript:alert(1)",
		"https://invalid host.test/",
		"https://example.org\r\nhttps://example.com",
		"# no address\r\n",
	} {
		d.receiveWebsiteLink(transfer.New(transfer.NewItem(transfer.Bytes(transfer.URIList, []byte(raw)))))
		if d.page != 1 || d.query != "DeepSeek" || d.draft.open {
			t.Fatalf("drop %q changed the current workflow: page=%d query=%q", raw, d.page, d.query)
		}
	}
	d.receiveWebsiteLink(transfer.TextData("https://example.org"))
	if d.page != 1 || d.query != "DeepSeek" || backend.builds != 0 || len(resolver.lookups) != 0 {
		t.Fatalf("a nonlink transfer changed the library or search: page=%d query=%q", d.page, d.query)
	}
}

func TestNativeWindowSizeSaveKeepsEditorOpenUntilFinished(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, windowWidth, windowHeight)
	click(t, tt, "选择 GitHub")
	click(t, tt, "创建应用")
	original := d.apps[0]

	click(t, tt, "GitHub 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "窗口尺寸")
	click(t, tt, "窗口宽度（像素）")
	typeReplace(tt, "880")
	var pending []func()
	d.work = func(f func()) { pending = append(pending, f) }
	click(t, tt, "保存")
	tt.Key(0, ui.KeyEscape)
	tt.ClickAt(6, windowHeight-12)
	if !d.draft.open || !tt.HasText("正在保存…") || len(pending) != 1 || backend.builds != 1 {
		t.Fatalf("pending save changed after dismissal attempt: open=%t tasks=%d builds=%d", d.draft.open, len(pending), backend.builds)
	}

	pending[0]()
	tt.Frame()
	if d.draft.open || len(d.apps) != 1 || d.apps[0].ID != original.ID ||
		d.apps[0].Width != 880 || d.apps[0].WindowRevision != original.WindowRevision+1 || backend.builds != 2 {
		t.Fatalf("saved window dimensions: %+v", d.apps)
	}
	if !tt.HasText("重新打开应用后使用新尺寸") {
		t.Fatal("missing confirmation for the next open")
	}
	click(t, tt, "GitHub 的选项")
	click(t, tt, "编辑应用")
	tt.Key(0, ui.KeyEscape)
	if d.draft.open {
		t.Fatal("idle editor did not close with Escape")
	}
}

func TestNativeEditingReturnsToLibraryRow(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "选择 DeepSeek")
	click(t, tt, "应用名称")
	typeReplace(tt, "Reading Desk")
	click(t, tt, "创建应用")
	original := d.apps[0]
	click(t, tt, "创建")
	click(t, tt, "选择 GitHub")
	click(t, tt, "创建应用")
	click(t, tt, "创建")
	click(t, tt, "网址或应用名称")
	tt.Type("写作")
	click(t, tt, "应用库")
	click(t, tt, "搜索应用库")
	tt.Type("Reading Desk")
	edit := func(name string) {
		t.Helper()
		click(t, tt, name+" 的选项")
		click(t, tt, "编辑应用")
	}
	edit("Reading Desk")
	click(t, tt, "保存")
	if d.draft.open || d.query != "写作" || d.appsQuery != "Reading Desk" || tt.HasText("打开 GitHub") || !tt.Focused("打开 Reading Desk") {
		t.Fatalf("returning from a save reset the search or lost the edited row: %q, %q, %v", d.query, d.appsQuery, tt.Texts())
	}
	if backend.builds != 2 {
		t.Fatal("saving without changes rebuilt an installed app")
	}
	edit("Reading Desk")
	click(t, tt, "应用名称")
	typeReplace(tt, "笔记")
	backend.buildError = errors.New("temporary disk write failure")
	click(t, tt, "保存")
	if !tt.HasText("无法保存应用，请重试") || d.draft.app.Name != "笔记" || d.apps[1] != original || d.appsQuery != "Reading Desk" {
		t.Fatal("the failed rename lost the draft, original app or library filter")
	}
	backend.buildError = nil
	click(t, tt, "保存")
	if d.appsQuery != "" || !tt.Focused("打开 笔记") || d.query != "写作" || !tt.HasText("打开 GitHub") {
		t.Fatalf("the renamed app was hidden or did not receive keyboard focus: %q, %v", d.appsQuery, tt.Texts())
	}
	if len(d.apps) != 2 || d.apps[1].ID != original.ID || d.apps[1].Name != "笔记" {
		t.Fatalf("the rename changed the installed application identity: %+v", d.apps)
	}
	tt.Key(0, ui.KeyEnter)
	if filepath.Base(backend.opened) != "笔记.fixture" {
		t.Fatalf("Enter opened %q, want the edited app", backend.opened)
	}
}

func TestNativeNoticesGiveWayToDialogs(t *testing.T) {
	d, tt, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "选择 DeepSeek")
	click(t, tt, "创建应用")
	const created = "已创建「DeepSeek」"
	const duplicate = "「DeepSeek」已在应用库中"
	if !tt.HasText(created) {
		t.Fatal("creation had no confirmation")
	}
	d.notice = duplicate
	tt.Frame()
	if tt.HasText(created) || !tt.HasText(duplicate) {
		t.Fatal("the latest notice did not replace the previous notice")
	}
	click(t, tt, "DeepSeek 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "窗口尺寸")
	if tt.HasText(duplicate) {
		t.Fatal("the library notice remained above the editor")
	}
	click(t, tt, "窗口宽度（像素）")
	typeReplace(tt, "800")
	click(t, tt, "保存")
	if d.draft.open || d.apps[0].Width != 800 || !tt.HasText("重新打开应用后使用新尺寸") {
		t.Fatal("the compact editor did not save the new size and show its confirmation")
	}
	click(t, tt, "DeepSeek 的选项")
	click(t, tt, "移除应用")
	if tt.HasText("重新打开应用后使用新尺寸") {
		t.Fatal("the size notice remained above the removal dialog")
	}
	click(t, tt, "取消")
	if tt.HasText("重新打开应用后使用新尺寸") || len(d.apps) != 1 {
		t.Fatal("canceling removal restored a stale notice or removed the app")
	}
}

func TestEditingKeepsResolvedArtworkAndUnchangedSaveSkipsBuild(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, windowWidth, windowHeight)
	click(t, tt, "网址或应用名称")
	tt.Type("https://example.org/work")
	tt.Key(0, ui.KeyEnter)
	click(t, tt, "创建应用")
	if len(d.apps) != 1 {
		t.Fatalf("website was not created: %v", tt.Texts())
	}
	original := d.apps[0]
	picture := d.appArt.get(original.ID)
	if picture.img != resolver.img || len(resolver.lookups) != 1 {
		t.Fatal("the fetched website icon was not used")
	}
	edit := func() {
		click(t, tt, original.Name+" 的选项")
		click(t, tt, "编辑应用")
		if d.draft.icon != picture || len(resolver.lookups) != 1 {
			t.Fatal("opening an existing app fetched the same icon again")
		}
	}
	edit()
	click(t, tt, "保存")
	if d.draft.open || d.apps[0] != original || d.appArt.get(original.ID) != picture || backend.builds != 1 {
		t.Fatal("saving unchanged settings should keep the app and its icon")
	}
	edit()
	click(t, tt, "应用名称")
	typeReplace(tt, "Project Desk")
	click(t, tt, "保存")
	if d.apps[0].Name != "Project Desk" || d.appArt.get(original.ID) != picture || backend.builds != 2 || len(resolver.lookups) != 1 {
		t.Fatal("renaming an app should keep its resolved artwork")
	}
}

func TestNativeCompactEditorWindowSize(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "选择 DeepSeek")
	size, sizeOK := tt.Find("窗口尺寸")
	cancel, cancelOK := tt.Find("取消")
	create, createOK := tt.Find("创建应用")
	sizeCenter := size.Y + size.H/2
	cancelCenter := cancel.Y + cancel.H/2
	createCenter := create.Y + create.H/2
	if !sizeOK || !cancelOK || !createOK || size.X+size.W >= cancel.X || cancel.X+cancel.W >= create.X ||
		math.Abs(float64(sizeCenter-cancelCenter)) > 1 || math.Abs(float64(createCenter-cancelCenter)) > 1 {
		t.Fatalf("the compact editor does not share one footer: size=%+v, cancel=%+v, create=%+v", size, cancel, create)
	}
	if tt.HasText("窗口宽度") || !tt.Focused("应用名称") {
		t.Fatal("the editor should open with its name focused and dimensions collapsed")
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("窗口尺寸") {
		t.Fatal("Tab from the name did not reach the size disclosure")
	}
	tt.Key(0, ui.KeySpace)
	if !tt.Focused("窗口宽度（像素）") {
		t.Fatal("opening the disclosure did not focus the width field")
	}
	typeReplace(tt, "915")
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("窗口高度（像素）") {
		t.Fatal("Tab from the width did not reach the height")
	}
	typeReplace(tt, "704")
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("窗口尺寸") {
		t.Fatal("Tab from the height did not return to the disclosure")
	}
	tt.Key(0, ui.KeySpace)
	if tt.HasText("窗口宽度") || !tt.Focused("窗口尺寸") {
		t.Fatal("collapsing the dimensions lost disclosure focus")
	}
	click(t, tt, "窗口尺寸")
	if d.draft.width != "915" || d.draft.height != "704" {
		t.Fatalf("reopening dimensions reset the draft to %sx%s", d.draft.width, d.draft.height)
	}
	click(t, tt, "窗口尺寸")
	click(t, tt, "创建应用")
	if backend.builds != 1 || len(d.apps) != 1 || d.apps[0].Width != 915 || d.apps[0].Height != 704 {
		t.Fatalf("saving with dimensions collapsed lost the changes: %+v", d.apps)
	}
}

func TestNativeEditorRevealsInvalidDimensions(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	const width, height = "窗口宽度（像素）", "窗口高度（像素）"
	click(t, tt, "选择 DeepSeek")
	click(t, tt, "窗口尺寸")
	typeReplace(tt, "915")
	tt.Key(0, ui.KeyTab)
	typeReplace(tt, "479")
	click(t, tt, "窗口尺寸")
	if tt.HasText(height) {
		t.Fatal("the dimensions did not collapse before submitting")
	}
	click(t, tt, "创建应用")
	field, ok := tt.Access(height)
	if !ok || !tt.Focused(height) || !field.Invalid || field.Value != "479" || len(field.ErrorMessage) != 1 || backend.builds != 0 {
		t.Fatalf("submitting a hidden invalid height should reopen and identify that field: %+v", field)
	}
	if other, _ := tt.Access(width); other.Value != "915" || other.Invalid {
		t.Fatalf("validation changed or marked the valid width: %+v", other)
	}
	shot(t, tt, "invalid-window-height")
	typeReplace(tt, "479.5")
	tt.Key(0, ui.KeyEnter)
	if malformed, _ := tt.Access(height); !malformed.Invalid || !tt.Focused(height) || !tt.HasText("请输入有效的窗口尺寸") || backend.builds != 0 {
		t.Fatalf("a noninteger height did not remain ready to correct: %+v", malformed)
	}
	typeReplace(tt, "704")
	if repaired, _ := tt.Access(height); repaired.Invalid || len(repaired.ErrorMessage) != 0 {
		t.Fatalf("correcting the height left the issue active: %+v", repaired)
	}
	tt.Key(0, ui.KeyEnter)
	if d.draft.open || backend.builds != 1 || len(d.apps) != 1 || d.apps[0].Width != 915 || d.apps[0].Height != 704 || !tt.Focused("打开 DeepSeek") {
		t.Fatalf("Enter from the corrected height did not create the app and focus its row: %+v", d.apps)
	}
}

func TestNativeEditorFocusesTheInvalidAddressOrName(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	const address, name = "应用网址", "应用名称"
	click(t, tt, "添加")
	typeReplace(tt, "not a website")
	click(t, tt, "创建应用")
	if got, _ := tt.Access(address); !tt.Focused(address) || !got.Invalid || len(got.ErrorMessage) != 1 || backend.builds != 0 {
		t.Fatalf("invalid URL did not guide the user to the address field: %+v", got)
	}
	typeReplace(tt, "https://github.com/explore")
	click(t, tt, name)
	typeReplace(tt, strings.Repeat("a", profile.MaxNameLength+1))
	click(t, tt, "创建应用")
	if got, _ := tt.Access(name); !tt.Focused(name) || !got.Invalid || len(got.ErrorMessage) != 1 || backend.builds != 0 {
		t.Fatalf("invalid name did not guide the user to the name field: %+v", got)
	}
	typeReplace(tt, "Workspace")
	tt.Key(0, ui.KeyEnter)
	if d.draft.open || len(d.apps) != 1 || d.apps[0].Name != "Workspace" || d.apps[0].URL != "https://github.com/explore" || backend.builds != 1 {
		t.Fatalf("the corrected form saved %+v after %d builds", d.apps, backend.builds)
	}
}

func TestNativeEditorIconHintDoesNotInterruptEscape(t *testing.T) {
	_, tt, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "选择 DeepSeek")
	tt.Move(1, 1)
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Key(ui.Shift, ui.KeyTab)
	if !tt.Focused("更换图标") {
		t.Fatal("the icon was not reachable from the keyboard")
	}
	tt.Advance(680 * time.Millisecond)
	if _, ok := tt.AccessByRole(ui.RoleTooltip, "更换图标"); ok {
		t.Fatal("keyboard focus showed a tooltip over the editor")
	}
	tt.Key(0, ui.KeyEscape)
	if tt.HasText("新建应用") || !tt.Focused("选择 DeepSeek") {
		t.Fatal("one Escape did not close the editor and restore opener focus")
	}
	click(t, tt, "选择 DeepSeek")
	icon, ok := tt.Find("更换图标")
	if !ok {
		t.Fatal("the icon was not visible on reopen")
	}
	tt.Move(icon.X+icon.W/2, icon.Y+icon.H/2)
	tt.Advance(680 * time.Millisecond)
	if _, ok := tt.AccessByRole(ui.RoleTooltip, "更换图标"); !ok {
		t.Fatal("the pointer could not reveal the icon hint")
	}
}

func TestNativeCardAndDialogMotion(t *testing.T) {
	_, tt, _, _ := nativeFixture(t, windowWidth, windowHeight)
	tt.SetPreferences(ui.Preferences{})
	tt.Frame()
	card, ok := tt.Find("选择 GitHub")
	if !ok {
		t.Fatal("recommendation card is absent")
	}
	x, y := int(card.X+card.W-7), int(card.Y+card.H/2)
	rest := tt.Image().At(x, y)
	tt.Move(float32(x), float32(y))
	tt.Advance(50 * time.Millisecond)
	mid := tt.Image().At(x, y)
	tt.Advance(100 * time.Millisecond)
	hover := tt.Image().At(x, y)
	if mid == rest || mid == hover || rest == hover {
		t.Fatalf("card hover does not blend over time: %v → %v → %v", rest, mid, hover)
	}
	dialogX, dialogY := (windowWidth-350)/2+9, windowHeight/2
	backdropX := 5
	closedBackdrop := tt.Image().RGBAAt(backdropX, dialogY)
	click(t, tt, "选择 GitHub")
	before := tt.Image().At(dialogX, dialogY)
	tt.Advance(80 * time.Millisecond)
	during := tt.Image().At(dialogX, dialogY)
	tt.Advance(130 * time.Millisecond)
	settled := tt.Image().At(dialogX, dialogY)
	if before == during || during == settled || !tt.HasText("新建应用") {
		t.Fatalf("dialog does not ease in: %v → %v → %v", before, during, settled)
	}
	openBackdrop := tt.Image().RGBAAt(backdropX, dialogY)
	tt.Key(0, ui.KeyEscape)
	tt.Advance(36 * time.Millisecond)
	out := tt.Image()
	fadingPanel, fadingBackdrop := out.RGBAAt(dialogX, dialogY), out.RGBAAt(backdropX, dialogY)
	if !(openBackdrop.R < fadingBackdrop.R && fadingBackdrop.R < closedBackdrop.R) ||
		int(fadingPanel.R)-int(fadingBackdrop.R) < 4 {
		t.Fatalf("panel and backdrop should fade out together: panel=%v, backdrop %v → %v → %v", fadingPanel, openBackdrop, fadingBackdrop, closedBackdrop)
	}
	if tt.HasText("新建应用") || !tt.Focused("选择 GitHub") {
		t.Fatal("fading dialog kept its controls or opener focus")
	}
	tt.Advance(130 * time.Millisecond)
	if got := tt.Image().RGBAAt(backdropX, dialogY); got != closedBackdrop {
		t.Fatalf("completed fade left the backdrop at %v, want %v", got, closedBackdrop)
	}
	tt.Move(0, 0)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	tt.Move(float32(x), float32(y))
	if instant := tt.Image().At(x, y); instant != hover {
		t.Fatalf("reduce motion left an intermediate color: %v, want %v", instant, hover)
	}
	click(t, tt, "选择 GitHub")
	if instant := tt.Image().At(dialogX, dialogY); instant != settled {
		t.Fatalf("reduce motion left a dialog fade: %v, want %v", instant, settled)
	}
	tt.Key(0, ui.KeyEscape)
	if instant := tt.Image().RGBAAt(backdropX, dialogY); instant != closedBackdrop {
		t.Fatalf("reduce motion left a closing backdrop: %v, want %v", instant, closedBackdrop)
	}
}

func TestNativeWindowDimensionsEaseInAndOut(t *testing.T) {
	_, tt, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	tt.SetPreferences(ui.Preferences{})
	tt.Frame()
	click(t, tt, "选择 DeepSeek")
	tt.Advance(180 * time.Millisecond)
	footerY := func() float32 {
		t.Helper()
		button, ok := tt.Find("创建应用")
		if !ok {
			t.Fatal("the create button disappeared during the disclosure transition")
		}
		return button.Y
	}
	closed := footerY()
	click(t, tt, "窗口尺寸")
	tt.Advance(50 * time.Millisecond)
	opening := footerY()
	tt.Advance(110 * time.Millisecond)
	opened := footerY()
	if !(closed < opening && opening < opened) || !tt.Focused("窗口宽度（像素）") {
		t.Fatalf("dimensions did not ease open and focus the width: footer %v → %v → %v", closed, opening, opened)
	}
	click(t, tt, "窗口尺寸")
	tt.Advance(50 * time.Millisecond)
	closing := footerY()
	if !(closed < closing && closing < opened) {
		t.Fatalf("dimensions did not ease closed: footer %v → %v → %v", opened, closing, closed)
	}
	tt.Key(ui.Shift, ui.KeyTab)
	if !tt.Focused("应用名称") {
		t.Fatal("the closing dimensions still took keyboard focus")
	}
	click(t, tt, "窗口尺寸")
	tt.Advance(160 * time.Millisecond)
	if reopened := footerY(); reopened != opened || !tt.Focused("窗口宽度（像素）") {
		t.Fatalf("reversing the transition left the footer at %v and lost the width focus; want %v", reopened, opened)
	}
}

func TestCreationNeverWaitsForSiteIcon(t *testing.T) {
	d, tt, _, resolver := nativeFixture(t, windowWidth, windowHeight)
	var queue []func()
	d.work = func(f func()) { queue = append(queue, f) }
	click(t, tt, "网址或应用名称")
	tt.Type("example.org/work")
	tt.Key(0, ui.KeyEnter)
	click(t, tt, "创建应用")
	if len(queue) != 3 || len(resolver.lookups) != 0 {
		t.Fatalf("expected queued cache read, icon fetch and save, got %d tasks and %v lookups", len(queue), resolver.lookups)
	}
	queue[2]() // Creation completes while cache and website artwork are pending.
	tt.Frame()
	if len(d.apps) != 1 || d.appArt.get(d.apps[0].ID).img == resolver.img {
		t.Fatal("the local app was not created with fallback artwork while offline")
	}
	queue[0]()
	queue[1]()
	if len(queue) != 4 {
		t.Fatal("resolving the favicon did not queue the application icon update")
	}
	queue[3]()
	tt.Frame()
	if d.appArt.get(d.apps[0].ID).img != resolver.img {
		t.Fatal("the saved app did not pick up the resolved icon")
	}
}

func TestLateSiteIconSurvivesRenamingAndResizing(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, windowWidth, windowHeight)
	var work []func()
	click(t, tt, "网址或应用名称")
	tt.Type("example.org/notes?view=all#first")
	d.work = func(f func()) { work = append(work, f) }
	tt.Key(0, ui.KeyEnter)
	click(t, tt, "创建应用")
	if len(work) != 2 {
		t.Fatalf("expected one site icon lookup and one save, got %d tasks", len(work))
	}
	fetch, create := work[0], work[1]
	work = nil
	create()
	tt.Frame()
	original := d.apps[0]

	openEditor := func() {
		t.Helper()
		click(t, tt, original.Name+" 的选项")
		click(t, tt, "编辑应用")
		if len(work) != 0 {
			t.Fatalf("reopening an app with a site icon lookup pending queued %d tasks", len(work))
		}
	}
	openEditor()
	click(t, tt, "取消")
	openEditor()
	const homepage = "https://example.org/notes?view=all#review"
	click(t, tt, "应用网址")
	typeReplace(tt, homepage)
	click(t, tt, "应用名称")
	typeReplace(tt, "Project Notes")
	click(t, tt, "窗口尺寸")
	click(t, tt, "窗口宽度（像素）")
	typeReplace(tt, "950")
	click(t, tt, "保存")
	if len(work) != 1 {
		t.Fatalf("changing settings started another icon lookup: %d tasks", len(work))
	}
	save := work[0]
	work = nil
	save()
	tt.Frame()
	fetch()
	if len(work) != 1 || len(resolver.lookups) != 1 {
		t.Fatalf("expected the original lookup to update the saved app: %d tasks, %v", len(work), resolver.lookups)
	}
	work[0]()
	tt.Frame()
	if app := d.apps[0]; app.ID != original.ID || app.URL != homepage || app.Name != "Project Notes" || app.Width != 950 ||
		d.appArt.get(app.ID).img != resolver.img || backend.lastIcon != resolver.img {
		t.Fatalf("the app lost settings or artwork after the late icon arrived: %+v", app)
	}
}

func TestNativeHomeFragmentKeepsInstalledArtworkOffline(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "网址或应用名称")
	tt.Type("example.org/notes?view=all#first")
	tt.Key(0, ui.KeyEnter)
	click(t, tt, "创建应用")
	app := d.apps[0]
	installed := d.appArt.get(app.ID)
	if installed == nil || installed.img != resolver.img || len(resolver.lookups) != 1 {
		t.Fatal("expected a saved website icon and a single lookup")
	}
	resolver.img = nil // The website and its cache are both unavailable.
	click(t, tt, app.Name+" 的选项")
	click(t, tt, "编辑应用")
	const homepage = "https://example.org/notes?view=all#review"
	click(t, tt, "应用网址")
	typeReplace(tt, homepage)
	if d.draft.icon != installed || len(resolver.lookups) != 1 {
		t.Fatalf("editing the fragment replaced the installed preview or requested the site again: %v", resolver.lookups)
	}
	click(t, tt, "保存")
	if saved := d.apps[0]; saved.ID != app.ID || saved.URL != homepage || len(resolver.lookups) != 1 ||
		d.appArt.get(app.ID) != installed || backend.lastIcon != installed.img {
		t.Fatalf("saving the new homepage changed the icon or lost the fragment: %+v, %v", saved, resolver.lookups)
	}
	if !tt.HasText("已保存首页") {
		t.Fatal("missing confirmation for the new homepage")
	}
}

func TestOutdatedSiteIconCannotChangeAnEditedApp(t *testing.T) {
	d, tt, backend, _ := nativeFixture(t, windowWidth, windowHeight)
	var tasks []func()
	click(t, tt, "网址或应用名称")
	tt.Type("example.org")
	d.work = func(f func()) { tasks = append(tasks, f) }
	tt.Key(0, ui.KeyEnter)
	click(t, tt, "创建应用")
	if len(tasks) != 2 {
		t.Fatalf("expected a pending icon fetch and save, got %d tasks", len(tasks))
	}
	oldFetch := tasks[0]
	tasks[1]()
	tasks = nil
	tt.Frame()
	if len(d.apps) != 1 {
		t.Fatalf("created apps = %+v", d.apps)
	}
	app := d.apps[0]
	oldFetch()
	if len(tasks) != 1 {
		t.Fatalf("expected one pending update with the old site's icon, got %d tasks", len(tasks))
	}
	oldUpdate := tasks[0]
	tasks = nil
	click(t, tt, app.Name+" 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "应用网址")
	typeReplace(tt, "https://github.com/webcrate")
	click(t, tt, "保存")
	if len(tasks) == 0 {
		t.Fatal("no edit was queued")
	}
	tasks[len(tasks)-1]()
	tasks = nil
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0].ID != app.ID || d.apps[0].URL != "https://github.com/webcrate" {
		t.Fatalf("edited apps = %+v", d.apps)
	}
	mark, buildCount := d.presetIcon("github"), backend.builds
	if d.appArt.get(app.ID) != mark {
		t.Fatal("edited app is missing its preset icon")
	}
	oldUpdate()
	tt.Frame()
	if d.appArt.get(app.ID) != mark || backend.builds != buildCount {
		t.Fatal("the late update replaced the new site's icon")
	}
}

func TestRemovingAppCancelsPendingSiteArtwork(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, windowWidth, windowHeight)
	var tasks []func()
	click(t, tt, "网址或应用名称")
	tt.Type("example.org")
	d.work = func(f func()) { tasks = append(tasks, f) }
	tt.Key(0, ui.KeyEnter)
	click(t, tt, "创建应用")
	if len(tasks) != 2 {
		t.Fatalf("expected one icon lookup and one creation, got %d tasks", len(tasks))
	}
	lookup, create := tasks[0], tasks[1]
	tasks = nil
	create()
	tt.Frame()
	click(t, tt, "example.org 的选项")
	click(t, tt, "移除应用")
	click(t, tt, "移除")
	if len(tasks) != 1 {
		t.Fatalf("removing the application queued %d tasks", len(tasks))
	}
	remove := tasks[0]
	tasks = nil
	remove()
	lookup()
	tt.Frame()
	if len(d.apps) != 0 || len(d.savedLookups) != 0 || len(tasks) != 0 || len(resolver.lookups) != 0 || backend.builds != 1 {
		t.Fatalf("the removed application still attempted to fetch an icon: apps=%v, requests=%v, builds=%d", d.apps, resolver.lookups, backend.builds)
	}
}

func TestEditingCanSaveWebsiteIconAfterItsBackgroundUpdateFails(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, windowWidth, windowHeight)
	var tasks []func()
	click(t, tt, "网址或应用名称")
	tt.Type("example.org")
	d.work = func(f func()) { tasks = append(tasks, f) }
	tt.Key(0, ui.KeyEnter)
	click(t, tt, "创建应用")
	if len(tasks) != 2 {
		t.Fatalf("creation queued %d tasks", len(tasks))
	}
	lookup, create := tasks[0], tasks[1]
	tasks = nil
	create()
	tt.Frame()
	app := d.apps[0]
	click(t, tt, app.Name+" 的选项")
	click(t, tt, "编辑应用")
	lookup()
	if len(tasks) != 1 || d.draft.icon.img != resolver.img {
		t.Fatalf("the editor did not share the resolved preview: %d tasks", len(tasks))
	}
	background := tasks[0]
	tasks = nil
	backend.buildError = errors.New("installed files temporarily unavailable")
	background()
	if d.appArt.get(app.ID).img == resolver.img || len(d.savedLookups) != 0 {
		t.Fatal("a failed background replacement changed the installed picture or left a pending job")
	}
	backend.buildError = nil
	click(t, tt, "保存")
	if len(tasks) != 1 {
		t.Fatalf("saving the ready icon queued %d tasks", len(tasks))
	}
	tasks[0]()
	tt.Frame()
	if d.draft.open || d.appArt.get(app.ID).img != resolver.img || backend.lastIcon != resolver.img || len(resolver.lookups) != 1 {
		t.Fatal("saving did not apply the resolved website icon")
	}
}

func TestNativeCustomIconCanBeSelectedAndReset(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	click(t, tt, "选择 GitHub")
	auto := d.draft.icon
	click(t, tt, "更换图标") // Canceling the native picker keeps the original preview.
	if d.draft.icon != auto || d.draft.custom {
		t.Fatal("canceling the picker changed the icon")
	}
	source := filepath.Join(t.TempDir(), "app icon.svg")
	d.chooseIcon = func() (image.Image, error) { return icons.ImportFile(source, rasterizeSVG) }
	click(t, tt, "更换图标")
	if !tt.HasText("无法读取所选图片") || d.draft.icon != auto {
		t.Fatal("a failed selection replaced the preview")
	}
	const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40"><rect x="0" y="0" width="40" height="40" rx="7" fill="#4d8158"/></svg>`
	if err := os.WriteFile(source, []byte(svg), 0o600); err != nil {
		t.Fatal(err)
	}
	click(t, tt, "更换图标")
	if !d.draft.custom || d.draft.icon == auto || tt.HasText("无法读取所选图片") {
		t.Fatal("the new picture was not previewed")
	}
	selected := d.draft.icon.img.At(128, 128)
	shot(t, tt, "custom-icon")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	click(t, tt, "创建应用")
	if len(d.apps) != 1 || !profile.ValidIconKey(d.apps[0].CustomIcon) || backend.lastIcon.At(128, 128) != selected {
		t.Fatal("creation lost the chosen local picture")
	}
	reopened := newDesktopApp(d.library, resolver, d.customIcons, d.presets)
	reopened.work = func(f func()) { f() }
	reopened.loadLibrary()
	reopened.page = 1
	ui.NewTesterWith(reopened.view, minWindowWidth, minWindowHeight, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}}).Frame()
	app := reopened.apps[0]
	if icon := reopened.appArt.get(app.ID); icon == nil || icon.img.At(128, 128) != selected {
		t.Fatal("the app lost its custom icon on restart")
	}

	click(t, tt, "GitHub 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "应用名称")
	typeReplace(tt, "Work Hub")
	click(t, tt, "保存")
	if d.apps[0].ID != app.ID || d.apps[0].CustomIcon != app.CustomIcon || backend.lastIcon.At(128, 128) != selected {
		t.Fatal("editing the name changed the icon or app identity")
	}
	click(t, tt, "Work Hub 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "恢复网站图标")
	if d.draft.icon != auto || d.draft.app.CustomIcon != "" {
		t.Fatal("the preview did not return to the site icon")
	}
	click(t, tt, "取消")
	if d.apps[0].CustomIcon != app.CustomIcon {
		t.Fatal("canceling the editor committed the icon reset")
	}
	click(t, tt, "Work Hub 的选项")
	click(t, tt, "编辑应用")
	click(t, tt, "恢复网站图标")
	click(t, tt, "保存")
	if d.apps[0].CustomIcon != "" || backend.lastIcon != auto.img || d.customIcons.Get(app.CustomIcon) != nil {
		t.Fatal("saving the reset did not restore the automatic icon")
	}
}

func TestLateSiteIconCannotReplaceChosenImage(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, windowWidth, windowHeight)
	resolver.name = "Declared workspace"
	var tasks []func()
	custom := icons.Fallback("M", "#886047")
	d.chooseIcon = func() (image.Image, error) { return custom, nil }
	click(t, tt, "网址或应用名称")
	tt.Type("example.org")
	d.work = func(f func()) { tasks = append(tasks, f) }
	tt.Key(0, ui.KeyEnter)
	click(t, tt, "更换图标")
	if len(tasks) != 2 {
		t.Fatalf("expected a pending favicon lookup and image picker, got %d tasks", len(tasks))
	}
	tasks[1]()
	tasks[0]()
	tt.Frame()
	if field, _ := tt.Access("应用名称"); field.Value != "" || field.Placeholder != resolver.name {
		t.Fatalf("choosing an image lost the website's suggested name: %+v", field)
	}
	if d.draft.icon.img != custom || len(resolver.lookups) != 1 {
		t.Fatal("the website icon replaced the manually selected image")
	}
	click(t, tt, "创建应用")
	if len(tasks) != 3 {
		t.Fatalf("expected an app save, got %d tasks", len(tasks))
	}
	tasks[2]()
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0].Name != resolver.name || d.apps[0].CustomIcon == "" || backend.lastIcon != custom || len(tasks) != 3 {
		t.Fatal("the saved app did not use the suggested name and chosen image")
	}
}

func TestNativeCustomIconDraftUsesTheCurrentWebsiteName(t *testing.T) {
	d, tt, _, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	custom := icons.Fallback("W", "#886047")
	d.chooseIcon = func() (image.Image, error) { return custom, nil }
	click(t, tt, "添加")
	click(t, tt, "更换图标")
	var work, updates []func()
	d.work = func(f func()) { work = append(work, f) }
	d.dispatch = func(f func()) { updates = append(updates, f) }
	click(t, tt, "应用网址")
	const first = "https://example.org/first"
	pasteAddress(tt, first)
	if len(work) != 1 {
		t.Fatalf("an unnamed custom draft queued %d lookups after receiving an address", len(work))
	}
	resolver.name = "First workspace"
	work[0]()
	const current = "https://example.org/current"
	typeReplace(tt, current)
	if len(work) != 2 {
		t.Fatalf("changing the address queued %d total lookups", len(work))
	}
	for _, update := range updates {
		update()
	}
	updates = nil
	tt.Frame()
	if field, _ := tt.Access("应用名称"); field.Placeholder != "使用网站域名" || field.Value != "" {
		t.Fatalf("the draft inherited a name from an old address: %+v", field)
	}
	resolver.name = "Current workspace"
	work[1]()
	for _, update := range updates {
		update()
	}
	tt.Frame()
	if field, _ := tt.Access("应用名称"); field.Placeholder != resolver.name || field.Value != "" || d.draft.icon.img != custom {
		t.Fatalf("the current draft lost its suggested name or manually selected image: %+v", field)
	}
	typeReplace(tt, "https://chat.deepseek.com/?from=workspace")
	if field, _ := tt.Access("应用名称"); field.Placeholder != "DeepSeek" || field.Value != "" || len(work) != 2 || d.draft.icon.img != custom {
		t.Fatalf("the known site did not suggest its offline name with the chosen image: %+v", field)
	}
}

func TestNativeCreationWithCustomIconDoesNotWaitForWebsiteName(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	custom := icons.Fallback("W", "#886047")
	d.chooseIcon = func() (image.Image, error) { return custom, nil }
	resolver.name = "Late workspace"
	var work []func()
	d.work = func(f func()) { work = append(work, f) }
	click(t, tt, "网址或应用名称")
	pasteAddress(tt, "https://example.org/workspace")
	click(t, tt, "使用网址 example.org")
	click(t, tt, "更换图标")
	if len(work) != 2 {
		t.Fatalf("selecting a local image queued %d tasks, want the preview and the picker", len(work))
	}
	work[1]()
	tt.Frame()
	click(t, tt, "创建应用")
	if len(work) != 3 || backend.builds != 0 {
		t.Fatalf("creating before the lookup queued %d tasks after %d builds", len(work), backend.builds)
	}
	work[2]()
	work[0]()
	tt.Frame()
	if len(d.apps) != 1 || d.apps[0].Name != "example.org" || backend.lastIcon != custom || len(work) != 3 || len(resolver.lookups) != 0 {
		t.Fatalf("the saved custom app left work for the website preview: %+v, %v", d.apps, resolver.lookups)
	}
}

func TestNativeCreatesFromBareAddressWithURLParameter(t *testing.T) {
	d, tt, backend, resolver := nativeFixture(t, minWindowWidth, minWindowHeight)
	const bare = "www.xiaohongshu.com/explore?return=https://example.org/saved#today"
	const address = "https://" + bare
	click(t, tt, "网址或应用名称")
	pasteAddress(tt, bare)
	click(t, tt, "使用网址 小红书")
	if field, _ := tt.Access("应用网址"); field.Value != address {
		t.Fatalf("the bare address opened a draft for %q, want %q", field.Value, address)
	}
	click(t, tt, "创建应用")
	if len(d.apps) != 1 || d.apps[0].URL != address || d.apps[0].Name != "小红书" || backend.lastIcon != d.presetIcon("xiaohongshu").img || len(resolver.lookups) != 0 {
		t.Fatalf("creation did not preserve the full address and offline artwork: %+v", d.apps)
	}
}

func TestNativePortraitLayoutFollowsAppearanceAndWindowControls(t *testing.T) {
	_, tt, _, _ := nativeFixture(t, windowWidth, windowHeight)
	dark := tt.Image().At(4, windowHeight-4)
	tt.SetDark(false)
	tt.Frame()
	light := tt.Image().At(4, windowHeight-4)
	if dark == light {
		t.Fatal("content did not follow the light system appearance")
	}
	shot(t, tt, "create-light")
	brand, ok := tt.Find(appName)
	if !ok || brand.X >= 66 || brand.Y < titleHeight {
		t.Fatalf("brand is not below the macOS window controls: %v", brand)
	}
	if field, ok := tt.Find("网址或应用名称"); !ok || field.Y <= brand.Y+brand.H {
		t.Fatalf("address field is not below the brand: %v", field)
	}
	_, narrow, _, _ := nativeFixture(t, minWindowWidth, minWindowHeight)
	narrow.SetTitleBar(ui.TitleBar{Height: titleHeight, Right: 138, Bottom: titleHeight})
	if tab, ok := narrow.Find("应用库"); !ok || tab.Y < titleHeight+4 || tab.X+tab.W > minWindowWidth-pagePadding {
		t.Fatalf("tabs are not below the Windows window controls: %v", tab)
	}
	if category, ok := narrow.Find("开发"); !ok || category.X+category.W > minWindowWidth-pagePadding {
		t.Fatalf("category overflowed the narrow layout: %v", category)
	}
	click(t, narrow, "应用库")
	click(t, narrow, "创建第一个应用")
	if !narrow.HasText("选择 GitHub") {
		t.Fatal("navigation became unavailable at the minimum window size")
	}
	card, ok := narrow.Find("选择 GitHub")
	if !ok {
		t.Fatal("the first recommendation is not visible")
	}
	narrow.Scroll(card.X+card.W/2, card.Y+card.H/2, 0, 600)
	narrow.Advance(180 * time.Millisecond)
	if last, ok := narrow.Find("选择 Hacker News"); !ok || last.Y < 0 || last.Y+last.H > minWindowHeight {
		t.Fatalf("bottom of the recommendation grid is unreachable: %v", last)
	}
}

func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v\nvisible: %v", label, err, tt.Texts())
	}
}

func typeReplace(tt *ui.Tester, text string) {
	modifier := ui.Ctrl
	if runtime.GOOS == "darwin" {
		modifier = ui.Cmd
	}
	tt.Key(modifier, ui.KeyA)
	tt.Type(text)
}

func shot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("WEBCRATE_SCREENSHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
