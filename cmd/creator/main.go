package main

import (
	"context"
	"image"
	"image/color"
	"log"
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/catalog"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/launcher"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/settings"
)

const appName = "Creator"

var openCreatorProfiles = func(path string) library.Profiles { return profile.NewStore(path) }

var openCreatorResolver = func(path string) sitePreviewProvider { return icons.NewResolver(path, rasterizeSVG) }

func main() {
	mygo.App.SetName(appName)
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	root, err := profile.UserDirectory()
	if err != nil {
		log.Fatal(err)
	}
	resources, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		log.Fatal(err)
	}
	runtimeDir := filepath.Join(root, "Runtimes")
	backend, err := bundle.NewBackend(resources, runtimeDir)
	if err != nil && !mygo.App.IsPackaged() {
		backend, err = bundle.NewBackend(filepath.Join(resources, runtime.GOOS+"-"+runtime.GOARCH), runtimeDir)
	}
	if err != nil {
		log.Fatal(err)
	}
	sites, err := catalog.Load()
	if err != nil {
		log.Fatal(err)
	}
	bundles, err := bundle.NewDesktopLibrary(root, backend)
	if err != nil {
		log.Fatal(err)
	}
	entries, err := launcher.ForUser()
	if err != nil {
		log.Printf("desktop app list: %v", err)
	}
	customIcons := icons.NewCustomStore(filepath.Join(root, "CustomIcons"))
	service := library.New(openCreatorProfiles(filepath.Join(root, profile.Filename)), bundles, library.Options{
		Launcher: entries, CustomIcons: customIcons,
	})
	resolver := openCreatorResolver(filepath.Join(root, "Icons"))
	desktop := newDesktopApp(service, resolver, customIcons, sites)
	preferencesPath := filepath.Join(root, "preferences.json")
	preferences, preferencesErr := settings.Load(preferencesPath)
	if preferencesErr != nil {
		log.Printf("读取 Creator 设置: %v", preferencesErr)
	}
	desktop.preferences = preferences
	if preferences.StartPage == settings.LibraryPage {
		desktop.page = 1
	}
	windowDirectory, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Printf("读取 Creator 窗口位置: %v", err)
	}
	mygo.App.SetWindowStateStore(settings.WindowStateFile{Directory: windowDirectory})
	mygo.Theme.SetSource(mygo.ThemeSource(preferences.Appearance))
	mygo.App.WhenReady(func() {
		window := mygo.NewWindow(mygo.WindowOptions{
			Title:                appName,
			Width:                windowWidth,
			Height:               windowHeight,
			MinWidth:             minWindowWidth,
			MinHeight:            minWindowHeight,
			UseContentSize:       true,
			TitleBarStyle:        mygo.TitleBarHidden,
			TitleBarHeight:       titleHeight,
			TrafficLightPosition: &mygo.Point{X: 13, Y: 14},
			AutoHideMenuBar:      true,
			BackgroundColor:      "light-dark(" + lightBackground + ", " + darkBackground + ")",
			StateKey:             "portrait",
			Content:              ui.View(desktop.view),
		})
		showCreator := func() {
			if !showExistingWindow(mygo.FocusedWindow()) {
				showExistingWindow(window)
			}
		}
		mygo.App.OnSecondInstance(func([]string, string) { showCreator() })
		mygo.App.OnActivate(func(bool) { showCreator() })
		desktop.dispatch = window.Update
		desktop.reveal = mygo.Shell.ShowItemInFolder
		desktop.chooseIcon = func() (image.Image, error) { return chooseAppIcon(window) }
		desktop.installMenu(window, preferencesPath, preferencesErr != nil)
		desktop.afterLibraryLoad = func(apps []profile.App) {
			err := service.RestoreLaunchers(context.Background(), apps, func(app profile.App) icons.Artwork {
				if site, ok := catalog.ByURL(sites, app.URL); ok {
					if picture, err := site.Icon(); err == nil {
						return icons.Artwork{Image: picture, Kind: icons.WebsiteArtwork}
					}
				}
				return icons.Artwork{Image: resolver.Cached(app.URL), Kind: icons.WebsiteArtwork}
			})
			if err != nil {
				log.Printf("restore desktop apps: %v", err)
			}
		}
		desktop.loadLibrary()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func rasterizeSVG(data []byte) (image.Image, error) {
	svg, err := ui.ParseSVG(data)
	if err != nil {
		return nil, err
	}
	return svg.Image(2*icons.Size, 2*icons.Size, color.Black), nil
}
