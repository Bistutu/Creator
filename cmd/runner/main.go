//go:build windows || linux

// The Windows and Linux website runtime is built once when Creator ships.
package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/runnerconfig"
	"github.com/Bistutu/Creator/internal/webwindow"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	dir := filepath.Dir(exe)
	app, settingsErr := runnerconfig.Load(dir)
	if settingsErr != nil {
		log.Printf("website app settings: %v", settingsErr)
		app.ID = filepath.Base(dir)
	}
	if profile.ValidID(app.ID) {
		// Keep the data directory and browser session used by older releases.
		mygo.App.SetName("WebCrate " + app.ID)
		if !mygo.App.RequestSingleInstanceLock() {
			return
		}
		if err := webwindow.SetWindowIdentity(app.ID); err != nil {
			log.Printf("desktop window identity: %v", err)
		}
	}
	if settingsErr == nil {
		registerWebsite(app, dir)
	} else {
		mygo.App.WhenReady(func() {
			_, _ = mygo.Dialog.Message(mygo.MessageOptions{
				Type: mygo.MessageWarning, Title: "无法读取应用设置", Message: "请在 Creator 应用库中检查此应用。", Buttons: []string{"关闭"},
			})
			mygo.App.Quit()
		})
	}
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func registerWebsite(app profile.App, dir string) {
	dataDirectory, err := mygo.App.Path(mygo.PathUserData)
	geometry := webwindow.WindowGeometryStore{
		Directory: dataDirectory, Revision: app.WindowRevision, Width: app.Width, Height: app.Height,
	}
	if err != nil {
		log.Printf("website app settings: %v", err)
	}
	mygo.App.SetWindowStateStore(geometry)
	zoom := webwindow.LoadPageZoom(dataDirectory)
	icon := runnerconfig.ReadWindowIcon(dir)
	webwindow.UseSystemDownloadsFolder()
	var downloads webwindow.Downloads
	mygo.App.OnQuit(downloads.Close)
	mygo.App.OnWindowCreated(func(window *mygo.Window) {
		if len(icon) > 0 {
			_ = window.SetIcon(icon)
		}
		if page := window.Page(); page != nil {
			downloads.Bind(window, page)
			configurePopups(window, page, app.Name, &downloads)
		}
	})
	var window *mygo.Window
	createWindow := func() {
		// Creating a WebView can process events. Later launch requests can
		// wait for this window to appear.
		window = nil
		window = openWebsite(app, zoom, geometry.Key(), &downloads)
	}
	mygo.App.WhenReady(createWindow)
	mygo.App.OnSecondInstance(func([]string, string) {
		if window == nil {
			return
		}
		if window.IsDestroyed() {
			createWindow()
			return
		}
		if window.IsMinimized() {
			window.Restore()
		}
		window.Show()
	})
}

func openWebsite(app profile.App, zoom *webwindow.PageZoom, stateKey string, downloads *webwindow.Downloads) *mygo.Window {
	readHome := func() string { return profile.SavedHome(app.ID) }
	pageOptions := mygo.PageOptions{Session: mygo.NewSession(app.ID), DevTools: mygo.DevToolsDisabled, ZoomFactor: zoom.Factor()}
	base := mygo.WindowOptions{
		Title: app.Name, Width: app.Width, Height: app.Height,
		MinWidth: 640, MinHeight: 480, StateKey: stateKey, AutoHideMenuBar: true,
	}
	options := base
	options.TitleBarStyle = mygo.TitleBarHidden
	options.Hidden = true
	var opening webwindow.OpeningStatus
	var recovery webwindow.Recovery
	var runtimeFailure string
	recovery.Requested(app.URL)
	var view *mygo.WebView
	var window *mygo.Window
	options.Content = ui.View(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			// The native buttons and drag area sit outside the web view.
			// Fixed-position headers on the site keep their own input area.
			if window == nil || !window.IsFullScreen() {
				opening.TitleBar(c)
			}
			if runtimeFailure != "" {
				webwindow.RuntimeFailureView(c, runtimeFailure, func() {
					go webwindow.OpenWebsiteInBrowser(window, app.URL)
				})
			} else if recovery.Visible() {
				page := view.Page()
				recovery.View(c, page.LoadURL, page.Reload)
			} else if view != nil {
				ui.WebView(c, view).Grow(1).MinHeight(0)
			}
		})
	})
	window = mygo.NewWindow(options)
	var err error
	view, err = window.NewWebView(mygo.WebViewOptions{Page: pageOptions})
	if err != nil {
		log.Printf("native website view: %v", err)
		if mygo.WebViewAvailable() {
			// A standalone window can still work when embedding is unavailable.
			base.Page, base.URL = pageOptions, app.URL
			if fallback := tryWebsiteWindow(base); fallback != nil {
				if page := fallback.Page(); page != nil {
					webwindow.InstallNavigationMenu(fallback, page, app.URL, readHome, nil, zoom)
				}
				window.Destroy()
				return fallback
			}
			runtimeFailure = "请重新打开应用，或在浏览器中继续。"
		} else if runtime.GOOS == "windows" {
			runtimeFailure = "请检查系统的 Microsoft Edge WebView2 Runtime，然后重新打开应用。"
		} else {
			runtimeFailure = "请检查系统的 WebKitGTK，然后重新打开应用。"
		}
		window.SetMenu(mygo.NewMenu([]*mygo.MenuItem{{Label: "显示", Submenu: []*mygo.MenuItem{
			{Label: "关闭窗口", Accelerator: "Ctrl+W", Click: func(*mygo.MenuItem, *mygo.Window) { window.Close() }},
		}}}))
		window.Invalidate()
		window.Show()
		return window
	}
	page := view.Page()
	clearOpening := func() {
		if opening.Finish() {
			window.Invalidate()
		}
	}
	window.OnClosed(func() { opening.Finish() })
	page.OnWillDownload(func(*mygo.DownloadEvent) { clearOpening() })
	downloads.Bind(window, page)
	configurePopups(window, page, app.Name, downloads)
	webwindow.InstallNavigationMenu(window, page, app.URL, readHome, &recovery, zoom)
	clearFailure := func() {
		if recovery.Finished() {
			window.Invalidate()
			view.Focus()
		}
	}
	page.OnWillNavigate(func(event *mygo.NavigateEvent) { recovery.Requested(event.URL) })
	page.OnDidNavigate(recovery.Committed)
	page.OnDidFinishLoad(func() {
		clearOpening()
		clearFailure()
	})
	page.OnDidFailLoad(func(failure *mygo.LoadError) {
		clearOpening()
		if runtime.GOOS == "windows" && !webwindow.WebView2ConnectionError(failure.Code) {
			clearFailure()
			return
		}
		recovery.Failed(failure.URL)
		window.Invalidate()
	})
	page.OnRenderProcessGone(func(reason string) {
		if reason != "unresponsive" {
			clearOpening()
		}
		// WebKitGTK reports termination of the view's own content process.
		// WebView2's callback also reports subframe and browser-process failures.
		if runtime.GOOS == "linux" {
			recovery.RendererTerminated(page.URL())
			window.Invalidate()
		}
	})
	opening.Start(mygo.RunOnMain, window.Invalidate)
	if err := page.LoadURL(app.URL); err != nil {
		opening.Finish()
		log.Printf("open website: %v", err)
		recovery.Failed(app.URL)
	}
	window.Invalidate()
	window.Show()
	if !recovery.Visible() {
		view.Focus()
	}
	return window
}

func tryWebsiteWindow(options mygo.WindowOptions) *mygo.Window {
	defer func() {
		if failure := recover(); failure != nil {
			// MyGo reports native window and session creation errors via panic.
			message, ok := failure.(string)
			if !ok || !strings.HasPrefix(message, "mygo: cannot create window: ") {
				panic(failure)
			}
			log.Print(message)
		}
	}()
	return mygo.NewWindow(options)
}

func configurePopups(window *mygo.Window, page *mygo.Page, name string, downloads *webwindow.Downloads) {
	if webwindow.BindRelatedPopups(window, downloads) {
		// The Linux adapter handles the native WebKit create signal.
		page.SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions { return nil })
		return
	}
	page.SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
		if !webwindow.PopupAddress(req.URL) {
			return nil
		}
		// MyGo manages related popup web views directly, including the
		// relationship with the opener on WebView2 for sign-in flows.
		return &mygo.WindowOptions{Title: name, Width: 880, Height: 650, Page: mygo.PageOptions{DevTools: mygo.DevToolsDisabled}}
	})
}
