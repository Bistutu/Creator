package webwindow

import (
	"log"
	"net/url"
	"strings"

	"github.com/egoist/mygo"
)

// InstallNavigationMenu binds the visible website to the native window's
// menu and accelerators. With AutoHideMenuBar, the website keeps its space.
func InstallNavigationMenu(window *mygo.Window, page *mygo.Page, homeURL string, readHome func() string, recovery *Recovery, zoom *PageZoom) {
	home := homeCommand{address: homeURL, read: readHome, dispatch: mygo.RunOnMain}
	window.OnClosed(home.cancel)
	item := func(label, accelerator string, action func()) *mygo.MenuItem {
		return &mygo.MenuItem{Label: label, Accelerator: accelerator, Click: func(*mygo.MenuItem, *mygo.Window) { action() }}
	}
	move := func(canGo func() bool, goTo func()) func() {
		return func() {
			if !canGo() {
				return
			}
			home.cancel()
			if recovery != nil && recovery.beginNavigation() {
				window.Invalidate()
			}
			goTo()
		}
	}
	goHome := func() {
		previous := page.URL()
		home.open(func(address string) {
			if window.IsDestroyed() || page.URL() != previous {
				return
			}
			redraw, err := navigateHome(page, address, recovery)
			if err != nil {
				log.Printf("open home page: %v", err)
			}
			if redraw {
				window.Invalidate()
			}
		})
	}
	reload := func() {
		home.cancel()
		if recovery != nil && recovery.Visible() {
			if recovery.Retry(page.LoadURL, page.Reload) {
				window.Invalidate()
			}
			return
		}
		page.Reload()
	}
	zoomItem := func(label, accelerator string, step int) *mygo.MenuItem {
		return item(label, accelerator, func() {
			if err := zoom.change(step, page.SetZoomFactor); err != nil {
				log.Printf("save page zoom: %v", err)
			}
		})
	}
	address := func() string { return visibleWebsiteAddress(page.URL(), home.address, recovery) }
	window.SetMenu(mygo.NewMenu([]*mygo.MenuItem{{Label: "显示", Submenu: []*mygo.MenuItem{
		item("后退", "Alt+Left", move(page.CanGoBack, page.GoBack)),
		item("前进", "Alt+Right", move(page.CanGoForward, page.GoForward)),
		item("回到首页", "Alt+Home", goHome),
		item("重新载入", "CmdOrCtrl+R", reload),
		mygo.Separator(),
		zoomItem("放大网页", "CmdOrCtrl+=", 1),
		zoomItem("缩小网页", "CmdOrCtrl+-", -1),
		zoomItem("实际大小", "CmdOrCtrl+0", 0),
		mygo.Separator(),
		item("复制网页链接", "", func() {
			if url := address(); url != "" {
				mygo.Clipboard.WriteText(url)
			}
		}),
		item("在浏览器中打开", "", func() {
			if url := address(); url != "" {
				go OpenWebsiteInBrowser(window, url)
			}
		}),
		mygo.Separator(),
		{Label: "关闭窗口", Role: mygo.RoleClose},
	}}}))
}

// The recovery panel represents the failed destination even when the native
// browser still reports the previous document as its current URL.
func visibleWebsiteAddress(current, home string, recovery *Recovery) string {
	if recovery != nil && websiteAddress(recovery.failed) {
		return recovery.failed
	}
	if websiteAddress(current) {
		return current
	}
	if websiteAddress(home) {
		return home
	}
	return ""
}

// OpenWebsiteInBrowser sends an explicit request to the system browser.
func OpenWebsiteInBrowser(window *mygo.Window, address string) {
	if err := mygo.Shell.OpenExternal(address); err != nil && !window.IsDestroyed() {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{
			Parent: window, Type: mygo.MessageWarning, Title: "无法打开默认浏览器",
			Message: "请检查系统的默认浏览器设置。", Buttons: []string{"确定"},
		})
	}
}

type homePage interface {
	URL() string
	IsLoading() bool
	LoadURL(string) error
}

func navigateHome(page homePage, address string, recovery *Recovery) (redraw bool, err error) {
	if (recovery == nil || !recovery.Visible()) && !page.IsLoading() && atHome(page.URL(), address) {
		return false, nil
	}
	if recovery != nil {
		recovery.Requested(address)
		recovery.beginNavigation()
	}
	err = page.LoadURL(address)
	if err != nil && recovery != nil {
		recovery.Failed(address)
	}
	return recovery != nil && recovery.Visible(), err
}

func atHome(current, saved string) bool {
	if current == saved {
		return true
	}
	// WebView URLs can include the root slash and omit the default port.
	home, err := url.Parse(saved)
	if err != nil {
		return false
	}
	if home.Path == "" {
		home.Path = "/"
	}
	if port := home.Port(); (home.Scheme == "https" && port == "443") || (home.Scheme == "http" && port == "80") {
		home.Host = strings.TrimSuffix(home.Host, ":"+port)
	}
	return current == home.String()
}
