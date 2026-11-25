//go:build creator_e2e && !mygo_noinspector

package main

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/settings"
)

func init() {
	if os.Getenv("MYGO_AUTOMATION") == "" {
		return
	}
	gate := os.Getenv("WEBCRATE_TEST_PREFERENCES_GATE")
	if path := os.Getenv("WEBCRATE_TEST_ICON_PICKER"); path != "" {
		openAppIconDialog = func(mygo.OpenDialogOptions) ([]string, error) { return []string{path}, nil }
	}
	if gate != "" {
		persistPreferences = func(path string, prefs settings.Preferences) error {
			if err := waitForFixtureGate(gate); err != nil {
				return err
			}
			return settings.Save(path, prefs)
		}
	}
	listGate := os.Getenv("WEBCRATE_TEST_LIST_GATE")
	findGate, findID := os.Getenv("WEBCRATE_TEST_FIND_GATE"), os.Getenv("WEBCRATE_TEST_FIND_ID")
	if listGate != "" || findGate != "" {
		openCreatorProfiles = func(path string) library.Profiles {
			return gatedProfileStore{Store: profile.NewStore(path), listGate: listGate, findGate: findGate, findID: findID}
		}
	}
	cacheGate, cacheAddress := os.Getenv("WEBCRATE_TEST_CACHE_GATE"), os.Getenv("WEBCRATE_TEST_CACHE_ADDRESS")
	if cacheGate != "" && cacheAddress != "" {
		open := openCreatorResolver
		openCreatorResolver = func(path string) sitePreviewProvider {
			return gatedWebsiteCache{sitePreviewProvider: open(path), gate: cacheGate, address: cacheAddress}
		}
	}
	if dropPath := os.Getenv("WEBCRATE_TEST_LINK_DROP"); dropPath != "" {
		listenForFixtureLinkDrop(dropPath)
	}
	// Exercise AppKit's menu shortcut inside the isolated fixture without
	// posting keystrokes to the user's session or requesting event access.
	keys := make(chan os.Signal, 1)
	signal.Notify(keys, syscall.SIGUSR1, syscall.SIGUSR2)
	go func() {
		for key := range keys {
			if key == syscall.SIGUSR1 {
				mygo.RunOnMain(func() {
					withCommandKeyEvent(",", 0x2B, func(app, event objc.ID) {
						app.Send(objc.RegisterName("postEvent:atStart:"), event, false)
					})
				})
			} else {
				mygo.RunOnMain(func() {
					withCommandKeyEvent("q", 0x0C, func(app, event objc.ID) {
						sel := objc.RegisterName
						if app.Send(sel("mainMenu")).Send(sel("performKeyEquivalent:"), event) != 0 && gate != "" {
							_ = os.WriteFile(gate+".quit", nil, 0o600)
						}
					})
				})
			}
		}
	}()
}

type gatedProfileStore struct {
	*profile.Store
	listGate, findGate, findID string
}

func (s gatedProfileStore) List() ([]profile.App, error) {
	if s.listGate != "" {
		if err := waitForFixtureGate(s.listGate); err != nil {
			return nil, err
		}
	}
	return s.Store.List()
}

func (s gatedProfileStore) Find(id string) (profile.App, error) {
	if s.findGate != "" && id == s.findID {
		if err := enterFixtureGate(s.findGate); err != nil {
			return profile.App{}, err
		}
	}
	return s.Store.Find(id)
}

type gatedWebsiteCache struct {
	sitePreviewProvider
	gate, address string
}

func (s gatedWebsiteCache) CachedName(address string) string {
	if address == s.address {
		if err := enterFixtureGate(s.gate); err != nil {
			return ""
		}
	}
	return s.sitePreviewProvider.CachedName(address)
}

func enterFixtureGate(path string) error {
	if err := os.WriteFile(path+".waiting", nil, 0o600); err != nil {
		return err
	}
	return waitForFixtureGate(path)
}

func waitForFixtureGate(path string) error {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(15 * time.Millisecond) {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
	}
	return errors.New("fixture did not release " + path)
}

func withCommandKeyEvent(key string, keyCode uint16, action func(app, event objc.ID)) {
	sel := objc.RegisterName
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(sel("new"))
	defer pool.Send(sel("drain"))
	const keyDown, command = 10, 1 << 20
	app := objc.ID(objc.GetClass("NSApplication")).Send(sel("sharedApplication"))
	window := app.Send(sel("keyWindow"))
	windowNumber := int(window.Send(sel("windowNumber")))
	character := objc.ID(objc.GetClass("NSString")).Send(sel("stringWithUTF8String:"), key)
	event := objc.ID(objc.GetClass("NSEvent")).Send(
		sel("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:"),
		uint(keyDown), struct{ X, Y float64 }{}, uint(command), float64(0), windowNumber, objc.ID(0), character, character, false, keyCode,
	)
	action(app, event)
}
