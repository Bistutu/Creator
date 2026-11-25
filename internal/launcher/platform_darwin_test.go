package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestMacLaunchServicesFindsCreatedApp(t *testing.T) {
	if os.Getenv("WEBCRATE_GUI_SMOKE") != "1" {
		t.Skip("requires a macOS user session")
	}
	backend, err := bundle.NewBackend(filepath.Join("..", "..", "resources", "darwin-"+runtime.GOARCH), "")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := ForUser()
	if err != nil {
		t.Fatal(err)
	}
	app := profile.App{Name: "Creator 入口测试", URL: "https://example.com", Width: 640, Height: 480}
	applications := os.Getenv("WEBCRATE_MAC_TEST_APPLICATIONS")
	if applications == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		applications = filepath.Join(home, "Applications")
		if err := os.MkdirAll(applications, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.MkdirTemp(applications, "Creator Launcher Test ")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	bundles := bundle.NewLibrary(root, backend)
	store := profile.NewStore(filepath.Join(t.TempDir(), "apps.json"))
	service := library.New(store, bundles, library.Options{Launcher: entries})
	art := icons.Brand()
	creator := library.New(store, bundles, library.Options{})
	app, err = creator.Save(t.Context(), app, library.Icon{Artwork: icons.Artwork{Image: art}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RestoreLaunchers(t.Context(), []profile.App{app}, nil); err != nil {
		t.Fatal(err)
	}
	appKit, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(appKit)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	workspace := objc.ID(objc.GetClass("NSWorkspace")).Send(objc.RegisterName("sharedWorkspace"))
	assertPath := func(app profile.App, path string) {
		t.Helper()
		wantedInfo, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		identifier := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), "com.webcrate.apps."+app.ID)
		lookup := objc.RegisterName("URLForApplicationWithBundleIdentifier:")
		var registered string
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if url := workspace.Send(lookup, identifier); url != 0 {
				pathValue := url.Send(objc.RegisterName("path"))
				registered = objc.Send[string](pathValue, objc.RegisterName("UTF8String"))
				if info, err := os.Stat(registered); err == nil && os.SameFile(wantedInfo, info) {
					return
				}
			}
		}
		t.Fatalf("Launch Services selected %q, want %q", registered, path)
	}
	first := filepath.Join(root, app.ID, backend.Filename(app))
	assertPath(app, first)
	other, err := creator.Save(t.Context(), profile.App{Name: "Creator 待恢复", URL: "https://example.org"},
		library.Icon{Artwork: icons.Artwork{Image: art}})
	if err != nil {
		t.Fatal(err)
	}
	held := heldMacRegistration{appID: other.ID, started: make(chan struct{}), resume: make(chan struct{})}
	service = library.New(store, bundles, library.Options{Launcher: held})
	release := sync.OnceFunc(func() { close(held.resume) })
	var tasks sync.WaitGroup
	defer func() {
		release()
		tasks.Wait()
	}()
	restored := make(chan error, 1)
	tasks.Go(func() { restored <- service.RestoreLaunchers(t.Context(), []profile.App{other}, nil) })
	select {
	case <-held.started:
	case <-time.After(3 * time.Second):
		t.Fatal("repair did not reach registration")
	}
	draft := app
	draft.Name = "Creator 新名称"
	type result struct {
		app profile.App
		err error
	}
	edited := make(chan result, 1)
	start := time.Now()
	tasks.Go(func() {
		saved, err := service.Save(t.Context(), draft, library.Icon{KeepInstalled: true})
		edited <- result{saved, err}
	})
	select {
	case saved := <-edited:
		if saved.err != nil {
			t.Fatal(saved.err)
		}
		app = saved.app
	case <-time.After(3 * time.Second):
		t.Fatal("renaming and registering the edited bundle waited for another app")
	}
	updated := filepath.Join(root, app.ID, backend.Filename(app))
	assertPath(app, updated)
	t.Logf("Launch Services located the renamed app in %s while another registration waited", time.Since(start).Round(time.Millisecond))
	release()
	if err := <-restored; err != nil {
		t.Fatal(err)
	}
	assertPath(other, filepath.Join(root, other.ID, backend.Filename(other)))
	if _, err := os.Stat(first); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old bundle still at %q: %v", first, err)
	}
	if err := service.Remove(app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(updated); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed bundle still at %q: %v", updated, err)
	}
}

type heldMacRegistration struct {
	macEntries
	appID           string
	started, resume chan struct{}
}

func (e heldMacRegistration) RegisterIfNeeded(app profile.App, path string) error {
	if app.ID == e.appID {
		close(e.started)
		<-e.resume
	}
	return e.macEntries.RegisterIfNeeded(app, path)
}

// The baseline exposes only Register, as before checked registration was
// available to the library. Both cases use the same installed bundles.
type forceMacRegistration struct{ library.Launcher }

func BenchmarkMacRestoreLaunchers(b *testing.B) {
	applications := os.Getenv("WEBCRATE_MAC_TEST_APPLICATIONS")
	if applications == "" {
		b.Skip("run under the DeskPad test supervisor")
	}
	root, err := os.MkdirTemp(applications, "Creator Entry Benchmark ")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(root) })
	backend, err := bundle.NewBackend(filepath.Join("..", "..", "resources", "darwin-"+runtime.GOARCH), "")
	if err != nil {
		b.Fatal(err)
	}
	entries := macEntries{}
	bundles := bundle.NewLibrary(root, backend)
	store := profile.NewStore(filepath.Join(b.TempDir(), "apps.json"))
	creator := library.New(store, bundles, library.Options{Launcher: entries})
	art := library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}}
	for i := range 24 {
		app := profile.App{Name: fmt.Sprintf("Entry %d", i), URL: "https://example.com"}
		if _, err := creator.Save(b.Context(), app, art); err != nil {
			b.Fatal(err)
		}
	}
	apps, err := creator.List()
	if err != nil || len(apps) != 24 {
		b.Fatalf("load 24 installed apps: got %d (%v)", len(apps), err)
	}
	for _, test := range []struct {
		name    string
		entries library.Launcher
	}{
		{"Forced", forceMacRegistration{entries}}, {"Current", entries},
	} {
		b.Run(test.name, func(b *testing.B) {
			service := library.New(store, bundles, library.Options{Launcher: test.entries})
			b.ReportAllocs()
			for b.Loop() {
				if err := service.RestoreLaunchers(b.Context(), apps, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
