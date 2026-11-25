package library_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

type artifact struct{}

func (artifact) Filename(app profile.App) string    { return app.Name + ".fixture" }
func (artifact) Open(context.Context, string) error { return nil }
func (artifact) Build(_ context.Context, path string, _ profile.App, fallback func() icons.Artwork, previous string) error {
	if previous != "" {
		data, err := os.ReadFile(previous)
		if err == nil {
			return os.WriteFile(path, data, 0o644)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	data, err := fallback().PNG()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

type launchArtifact struct {
	artifact
	open func(context.Context, string) error
}

func (b launchArtifact) Open(ctx context.Context, path string) error { return b.open(ctx, path) }

type gatedArtifact struct {
	artifact
	beforeBuild func(context.Context, profile.App) error
}

func (b gatedArtifact) Build(ctx context.Context, path string, app profile.App, fallback func() icons.Artwork, previous string) error {
	if err := b.beforeBuild(ctx, app); err != nil {
		return err
	}
	return b.artifact.Build(ctx, path, app, fallback, previous)
}

func TestBackgroundPackagingLeavesOtherApplicationsUsable(t *testing.T) {
	for _, task := range []string{"Restore", "WebsiteIcon"} {
		t.Run(task, func(t *testing.T) {
			root := t.TempDir()
			started, resume := make(chan struct{}, 2), make(chan struct{})
			var heldID string
			bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), gatedArtifact{
				beforeBuild: func(ctx context.Context, app profile.App) error {
					if app.ID != heldID {
						return nil
					}
					started <- struct{}{}
					select {
					case <-resume:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				},
			})
			store := profile.NewStore(filepath.Join(root, "apps.json"))
			service := library.New(store, bundles, library.Options{Launcher: readyLauncher{}})
			older, err := service.Save(t.Context(), profile.App{Name: "Existing desk", URL: "https://example.com"}, library.Icon{})
			if err != nil {
				t.Fatal(err)
			}
			heldID = older.ID
			if task == "Restore" {
				path, err := bundles.Path(older)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			ready := make(chan error, 1)
			var tasks sync.WaitGroup
			t.Cleanup(tasks.Wait)
			picture := icons.Brand()
			tasks.Go(func() {
				if task == "Restore" {
					ready <- service.RestoreLaunchers(t.Context(), []profile.App{older}, nil)
				} else {
					_, err := service.RefreshIcon(t.Context(), older, picture)
					ready <- err
				}
			})
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("background work did not reach the package writer")
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			type result struct {
				app profile.App
				err error
			}
			opened := make(chan result, 1)
			start := time.Now()
			tasks.Go(func() {
				app, err := service.Save(ctx, profile.App{Name: "New desk", URL: "https://example.com/work#today"}, library.Icon{})
				if err == nil {
					err = service.Open(ctx, app, nil)
				}
				opened <- result{app, err}
			})
			var other profile.App
			select {
			case created := <-opened:
				if created.err != nil {
					t.Fatal(created.err)
				}
				other = created.app
			case <-ctx.Done():
				t.Fatal("creating and opening a new app waited for another app's background package")
			}
			t.Logf("saved and opened the new app in %s with background packaging held", time.Since(start).Round(time.Millisecond))

			wait, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer stop()
			draft := older
			draft.Width += 20
			if _, err := service.Save(wait, draft, library.Icon{KeepInstalled: true}); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("editing the app whose package was still being written returned %v", err)
			}
			select {
			case <-started:
				t.Fatal("an edit started a second write to the app already being packaged")
			default:
			}
			queued, drop := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer drop()
			if applied, err := service.RefreshIcon(queued, other, picture); applied || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("a second background update started while the first was held: %t, %v", applied, err)
			}
			close(resume)
			if err := <-ready; err != nil {
				t.Fatal(err)
			}
			heldID = ""
			saved, err := service.Save(t.Context(), draft, library.Icon{KeepInstalled: true})
			if err != nil || saved.ID != older.ID || saved.Width != draft.Width {
				t.Fatalf("save after background packaging = %+v, %v", saved, err)
			}
			if _, err := bundles.Path(saved); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIndependentSettingsSaveWhileAnotherApplicationIsPackaging(t *testing.T) {
	root := t.TempDir()
	started, resume := make(chan struct{}), make(chan struct{})
	var slowID string
	bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), gatedArtifact{
		beforeBuild: func(ctx context.Context, app profile.App) error {
			if app.ID == slowID {
				close(started)
				select {
				case <-resume:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		},
	})
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	service := library.New(store, bundles, library.Options{})
	save := func(name string) profile.App {
		t.Helper()
		app, err := service.Save(t.Context(), profile.App{Name: name, URL: "https://example.org/" + name}, library.Icon{})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	slow, other := save("first"), save("second")
	slowID = slow.ID
	slow.Width += 20
	other.Height += 30
	release := sync.OnceFunc(func() { close(resume) })
	var tasks sync.WaitGroup
	t.Cleanup(func() {
		release()
		tasks.Wait()
	})
	icon := library.Icon{KeepInstalled: true}
	first := make(chan error, 1)
	tasks.Go(func() {
		_, err := service.Save(t.Context(), slow, icon)
		first <- err
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("saving the first app did not reach the package writer")
	}

	second := make(chan error, 1)
	start := time.Now()
	tasks.Go(func() {
		_, err := service.Save(t.Context(), other, icon)
		second <- err
	})
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("saving the second app waited for the first app's package")
	}
	t.Logf("saved the second app in %s with the first package held", time.Since(start).Round(time.Millisecond))
	for _, app := range []profile.App{slow, other} {
		committed, err := store.Find(app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if app.ID == slowID && committed.Width == slow.Width {
			t.Fatal("the first edit was committed before its package was ready")
		}
		if app.ID == other.ID {
			if committed.Height != other.Height || committed.WindowRevision != other.WindowRevision+1 {
				t.Fatal("the second edit completed before its profile was committed")
			}
			if _, err := bundles.Path(committed); err != nil {
				t.Fatalf("the second edit did not install the matching package: %v", err)
			}
		}
	}
	release()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestAddressChangeKeepsDuplicateChecksConsistent(t *testing.T) {
	root := t.TempDir()
	started, resume := make(chan struct{}), make(chan struct{})
	var movingID string
	bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), gatedArtifact{
		beforeBuild: func(ctx context.Context, app profile.App) error {
			if app.ID != movingID {
				return nil
			}
			close(started)
			select {
			case <-resume:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	service := library.New(store, bundles, library.Options{})
	old, err := service.Save(t.Context(), profile.App{Name: "Desk", URL: "https://example.org/current"}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	movingID = old.ID
	draft := old
	draft.URL = "https://example.org/next"
	release := sync.OnceFunc(func() { close(resume) })
	var tasks sync.WaitGroup
	t.Cleanup(func() {
		release()
		tasks.Wait()
	})
	finished := make(chan error, 1)
	tasks.Go(func() {
		_, err := service.Save(t.Context(), draft, library.Icon{})
		finished <- err
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("switching addresses did not reach the package writer")
	}
	for _, address := range []string{"https://example.org:443/current", "https://example.org:443/next"} {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		_, err := service.Save(ctx, profile.App{Name: old.Name, URL: address}, library.Icon{})
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("save at %s before the move committed returned %v", address, err)
		}
	}
	release()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	replacement, err := service.Save(t.Context(), profile.App{Name: old.Name, URL: old.URL}, library.Icon{})
	if err != nil || old.ID == replacement.ID {
		t.Fatalf("old address was not available after the move: %+v, %v", replacement, err)
	}
	var duplicate *library.AlreadyExistsError
	_, err = service.Save(t.Context(), profile.App{Name: old.Name, URL: draft.URL}, library.Icon{})
	if !errors.As(err, &duplicate) || duplicate.App.ID != old.ID {
		t.Fatalf("new address did not return the moved app: %v", err)
	}
}

type heldLauncher struct {
	*menu
	id      string
	entered chan struct{}
	resume  chan struct{}
}

func (l heldLauncher) Register(app profile.App, path string) error {
	if app.ID == l.id {
		close(l.entered)
		<-l.resume
	}
	return l.menu.Register(app, path)
}

func TestOpenStartsWhileAnotherSystemEntryIsBeingRegistered(t *testing.T) {
	root := t.TempDir()
	opened := make(chan string, 1)
	backend := launchArtifact{open: func(_ context.Context, path string) error {
		opened <- path
		return nil
	}}
	bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), backend)
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	entries := &heldLauncher{
		menu:    &menu{store: store, entries: make(map[string]string)},
		entered: make(chan struct{}), resume: make(chan struct{}),
	}
	service := library.New(store, bundles, library.Options{Launcher: entries})
	restore, err := service.Save(t.Context(), profile.App{Name: "Restore", URL: "https://example.org/restore"}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	app, err := service.Save(t.Context(), profile.App{Name: "Open now", URL: "https://example.org/now"}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	path := entries.entries[app.ID]
	if path == "" {
		t.Fatal("saving did not create the system entry")
	}
	delete(entries.entries, app.ID)
	entries.id = restore.ID
	var tasks sync.WaitGroup
	release := sync.OnceFunc(func() { close(entries.resume) })
	t.Cleanup(func() {
		release()
		tasks.Wait()
	})
	restored := make(chan error, 1)
	tasks.Go(func() { restored <- service.RestoreLaunchers(t.Context(), []profile.App{restore}, nil) })
	select {
	case <-entries.entered:
	case <-time.After(time.Second):
		t.Fatal("startup restoration did not reach system registration")
	}
	finished := make(chan error, 1)
	tasks.Go(func() { finished <- service.Open(t.Context(), app, nil) })
	select {
	case got := <-opened:
		if got != path {
			t.Fatalf("opened %q, want %q", got, path)
		}
	case <-time.After(time.Second):
		t.Fatal("opening the application waited for another system entry to register")
	}
	select {
	case err := <-finished:
		if err != nil || entries.entries[app.ID] != path {
			t.Fatalf("Open did not repair the missing entry after launch: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the open row stayed busy while another system entry was being registered")
	}
	release()
	if err := <-restored; err != nil {
		t.Fatal(err)
	}
}

func TestSlowLaunchKeepsOtherApplicationsUsable(t *testing.T) {
	root := t.TempDir()
	started, release := make(chan string, 1), make(chan struct{})
	launchError := errors.New("system could not open the application")
	backend := launchArtifact{open: func(ctx context.Context, path string) error {
		if filepath.Base(path) != "Slow.fixture" {
			return nil
		}
		started <- path
		select {
		case <-release:
			return launchError
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	service := library.New(store, bundle.NewLibrary(filepath.Join(root, "Applications"), backend), library.Options{})
	save := func(name string) profile.App {
		t.Helper()
		app, err := service.Save(t.Context(), profile.App{Name: name, URL: "https://example.org"}, library.Icon{})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	slow, other := save("Slow"), save("Other")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	launchResult, launchDone := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(launchDone)
		launchResult <- service.Open(ctx, slow, nil)
	}()
	t.Cleanup(func() { cancel(); <-launchDone })
	var openingPath string
	select {
	case openingPath = <-started:
	case <-ctx.Done():
		t.Fatal("first launch did not reach the system")
	}

	workStarted := time.Now()
	other.Name = "Updated"
	updated, err := service.Save(ctx, other, library.Icon{KeepInstalled: true})
	if err != nil || updated.ID != other.ID || updated.Name != other.Name {
		t.Fatalf("the other app could not be edited during launch: %+v, %v", updated, err)
	}
	if err := service.Open(ctx, updated, nil); err != nil {
		t.Fatalf("the other app could not be opened during launch: %v", err)
	}
	t.Logf("edited and opened the other app in %s while the first launch was pending", time.Since(workStarted).Round(time.Millisecond))

	// Protect the path that the operating system is still opening.
	slow.Name = "Renamed"
	wait, stop := context.WithTimeout(ctx, 15*time.Millisecond)
	defer stop()
	if _, err := service.Save(wait, slow, library.Icon{KeepInstalled: true}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("editing the app being opened returned %v", err)
	}
	if err := service.Open(wait, slow, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("opening with an expired request returned %v", err)
	}
	if _, err := os.Stat(openingPath); err != nil {
		t.Fatalf("launch path changed before the system confirmed it: %v", err)
	}
	close(release)
	if err := <-launchResult; !errors.Is(err, launchError) {
		t.Fatalf("launch error = %v, want %v", err, launchError)
	}
	if edited, err := service.Save(ctx, slow, library.Icon{KeepInstalled: true}); err != nil || edited.ID != slow.ID || edited.Name != slow.Name {
		t.Fatalf("edit after failed launch = %+v, %v", edited, err)
	}
	if err := service.Remove(slow.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Open(ctx, slow, nil); !errors.Is(err, profile.ErrNotFound) {
		t.Fatalf("opening a stale selection after removal returned %v", err)
	}
}

type artworkBundles struct {
	*bundle.Library
	read func(profile.App) func() icons.Artwork
}

func (b artworkBundles) ReadInstalledArtwork(app profile.App) func() icons.Artwork {
	return b.read(app)
}

func TestSlowArtworkKeepsOtherApplicationsUsable(t *testing.T) {
	root := t.TempDir()
	started, release := make(chan struct{}), make(chan struct{})
	var tasks sync.WaitGroup
	t.Cleanup(tasks.Wait)

	artwork := icons.Artwork{Image: icons.Brand(), Kind: icons.WebsiteArtwork}
	bundles := artworkBundles{
		Library: bundle.NewLibrary(filepath.Join(root, "Applications"), artifact{}),
		read: func(profile.App) func() icons.Artwork {
			close(started)
			select {
			case <-release:
			case <-t.Context().Done():
			}
			return func() icons.Artwork { return artwork }
		},
	}
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	service := library.New(store, bundles, library.Options{})
	save := func(name string) profile.App {
		t.Helper()
		app, err := service.Save(t.Context(), profile.App{Name: name, URL: "https://example.com"}, library.Icon{})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	slow, other := save("Slow artwork"), save("Other")
	picture := make(chan icons.Artwork, 1)
	tasks.Go(func() { picture <- service.InstalledArtwork(slow) })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("installed artwork read did not start")
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	edited := make(chan error, 1)
	workStarted := time.Now()
	tasks.Go(func() {
		draft := other
		draft.Width += 20
		app, err := service.Save(ctx, draft, library.Icon{KeepInstalled: true})
		if err == nil {
			err = service.Open(ctx, app, nil)
		}
		edited <- err
	})
	select {
	case err := <-edited:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("the other app's settings and launch waited for the artwork read")
	}
	t.Logf("edited and opened another app in %s with artwork still waiting", time.Since(workStarted).Round(time.Millisecond))
	if saved, err := store.Find(other.ID); err != nil || saved.Width != other.Width+20 {
		t.Fatalf("the other app's new size was not saved: %+v, %v", saved, err)
	}

	draft := slow
	draft.Width += 20
	wait, stop := context.WithTimeout(t.Context(), 15*time.Millisecond)
	defer stop()
	blocked := make(chan error, 1)
	tasks.Go(func() {
		_, err := service.Save(wait, draft, library.Icon{KeepInstalled: true})
		blocked <- err
	})
	select {
	case err := <-blocked:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("editing the app whose artwork was still being read returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the pending settings change ignored its deadline")
	}
	close(release)
	if read := <-picture; read != artwork {
		t.Fatal("the artwork read lost its result")
	}
	if saved, err := service.Save(t.Context(), draft, library.Icon{KeepInstalled: true}); err != nil || saved.Width != draft.Width {
		t.Fatalf("saving after the read completed: %+v, %v", saved, err)
	}
}

func TestSettingsCanSaveWhileInstalledArtworkDecodes(t *testing.T) {
	root := t.TempDir()
	decoding, resume := make(chan struct{}), make(chan struct{})
	var tasks sync.WaitGroup
	t.Cleanup(tasks.Wait)
	artwork := icons.Artwork{Image: icons.Brand(), Kind: icons.WebsiteArtwork}
	bundles := artworkBundles{
		Library: bundle.NewLibrary(filepath.Join(root, "Applications"), artifact{}),
		read: func(profile.App) func() icons.Artwork {
			return func() icons.Artwork {
				close(decoding)
				select {
				case <-resume:
				case <-t.Context().Done():
				}
				return artwork
			}
		},
	}
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	service := library.New(store, bundles, library.Options{})
	app, err := service.Save(t.Context(), profile.App{Name: "Desk", URL: "https://example.org"}, library.Icon{Artwork: artwork})
	if err != nil {
		t.Fatal(err)
	}
	previous, err := bundles.Path(app)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(previous)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan icons.Artwork, 1)
	tasks.Go(func() { result <- service.InstalledArtwork(app) })
	select {
	case <-decoding:
	case <-time.After(time.Second):
		t.Fatal("the preview did not reach the decoder")
	}
	cancelled := make(chan struct{})
	skipped := make(chan bool, 1)
	tasks.Go(func() {
		picture, waiting := service.TryInstalledArtwork(app, cancelled)
		skipped <- picture.Image == nil && waiting == nil
	})
	close(cancelled)
	select {
	case empty := <-skipped:
		if !empty {
			t.Fatal("cancelled artwork read returned pending work")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled artwork still queued behind the held decoder")
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	draft := app
	draft.Name, draft.Width = "Renamed", app.Width+40
	saved, err := service.Save(ctx, draft, library.Icon{KeepInstalled: true})
	if err != nil || saved.ID != app.ID || saved.Name != draft.Name || saved.Width != draft.Width {
		t.Fatalf("saving settings while decoding: %+v, %v", saved, err)
	}
	path, err := bundles.Path(saved)
	if err != nil || path == previous {
		t.Fatalf("updated path = %q, %v", path, err)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("the settings save changed the installed artwork: %v", err)
	}
	t.Logf("saved the same app in %s with its preview decoder still paused", time.Since(start).Round(time.Millisecond))
	close(resume)
	if read := <-result; read != artwork {
		t.Fatal("the preview lost the original revision's artwork")
	}
}

func TestRemoveKeepsInstalledAppWhenSystemEntryOrProfileCannotBeRemoved(t *testing.T) {
	for _, stage := range []string{"SystemEntry", "Profile"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			store := &removalProfiles{Store: profile.NewStore(filepath.Join(root, "apps.json"))}
			bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), artifact{})
			entries := &menu{store: store.Store, entries: make(map[string]string)}
			artwork := icons.NewCustomStore(filepath.Join(root, "CustomIcons"))
			service := library.New(store, bundles, library.Options{Launcher: entries, CustomIcons: artwork})
			app, err := service.Save(t.Context(), profile.App{Name: "Example", URL: "https://example.com"}, library.Icon{
				Custom: true, Artwork: icons.Artwork{Image: icons.Brand()},
			})
			if err != nil {
				t.Fatal(err)
			}
			path, err := bundles.Path(app)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			blocked := errors.New("removal is temporarily unavailable")
			if stage == "SystemEntry" {
				entries.removeError = blocked
			} else {
				store.deleteError = blocked
			}
			if err := service.Remove(app.ID); !errors.Is(err, blocked) {
				_, findErr := store.Find(app.ID)
				t.Fatalf("Remove returned %v, want %v; saved profile lookup returned %v", err, blocked, findErr)
			}
			if current, err := store.Find(app.ID); err != nil || current != app {
				t.Fatalf("failed removal changed the profile to %+v (%v)", current, err)
			}
			if after, err := os.Stat(path); err != nil || !os.SameFile(before, after) {
				t.Fatalf("failed removal changed the installed file: %v (%v)", after, err)
			}
			if entries.entries[app.ID] != path || artwork.Get(app.CustomIcon) == nil {
				t.Fatal("failed removal did not retain the system entry and custom icon")
			}
			if err := service.Open(t.Context(), app, nil); err != nil {
				t.Fatalf("opening after a failed removal: %v", err)
			}
			entries.removeError, store.deleteError = nil, nil
			if err := service.Remove(app.ID); err != nil {
				t.Fatalf("retrying removal: %v", err)
			}
			if _, err := store.Find(app.ID); !errors.Is(err, profile.ErrNotFound) {
				t.Fatalf("successful removal left a profile: %v", err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("successful removal left an installed file: %v", err)
			}
			if entries.entries[app.ID] != "" || artwork.Get(app.CustomIcon) != nil {
				t.Fatal("successful removal left the system entry or custom icon")
			}
		})
	}
}

type removalProfiles struct {
	*profile.Store
	deleteError error
}

func (p *removalProfiles) Delete(id string) error {
	if p.deleteError != nil {
		return p.deleteError
	}
	return p.Store.Delete(id)
}

func TestStartupCleansCommittedRemovalsAndAbandonedBuilds(t *testing.T) {
	root := t.TempDir()
	installs, profiles := filepath.Join(root, "Applications"), filepath.Join(root, "apps.json")
	store := profile.NewStore(profiles)
	bundles := bundle.NewLibrary(installs, artifact{})
	service := library.New(store, bundles, library.Options{Launcher: readyLauncher{}})
	var kept, removed profile.App
	for _, destination := range []struct {
		name string
		app  *profile.App
	}{{"Kept desk", &kept}, {"Removed desk", &removed}} {
		app, err := service.Save(t.Context(), profile.App{Name: destination.name, URL: "https://example.com"}, library.Icon{})
		if err != nil {
			t.Fatal(err)
		}
		*destination.app = app
	}
	if _, err := os.MkdirTemp(installs, ".new-"+kept.ID+"-"); err != nil {
		t.Fatal(err)
	}
	unmarked := ".old-" + profile.NewID()
	unknown := ".new-" + kept.ID + "-manual"
	for _, directory := range []string{unmarked, unknown} {
		if err := os.Mkdir(filepath.Join(installs, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	keptPath, err := bundles.Path(kept)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(keptPath)
	if err != nil {
		t.Fatal(err)
	}
	// Stop each removal at a different side of the profile commit.
	interruptRemove(t, bundles, kept, nil)
	interruptRemove(t, bundles, removed, func() error { return store.Delete(removed.ID) })

	restarted := library.New(profile.NewStore(profiles), bundle.NewLibrary(installs, artifact{}), library.Options{Launcher: readyLauncher{}})
	apps, err := restarted.List()
	if err != nil || len(apps) != 1 || apps[0] != kept {
		t.Fatalf("after the interrupted removals the library contains %+v (%v)", apps, err)
	}
	if err := restarted.RestoreLaunchers(t.Context(), apps, nil); err != nil {
		t.Fatal(err)
	}
	if recovered, err := os.Stat(keptPath); err != nil || !os.SameFile(before, recovered) {
		t.Fatalf("startup changed the original application after an uncommitted removal: %v", err)
	}
	entries, err := os.ReadDir(installs)
	if err != nil || len(entries) != 3 || entries[0].Name() != unknown || entries[1].Name() != unmarked || entries[2].Name() != kept.ID {
		t.Fatalf("startup left abandoned application directories: %v (%v)", entries, err)
	}
}

func TestStartupRetainsRemovalBackupUntilItCanConfirmTheCommit(t *testing.T) {
	root := t.TempDir()
	installs, profiles := filepath.Join(root, "Applications"), filepath.Join(root, "apps.json")
	store := profile.NewStore(profiles)
	bundles := bundle.NewLibrary(installs, artifact{})
	service := library.New(store, bundles, library.Options{})
	app, err := service.Save(t.Context(), profile.App{Name: "Example", URL: "https://example.com"}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	interruptRemove(t, bundles, app, func() error { return store.Delete(app.ID) })
	committed, err := os.ReadFile(profiles)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(profiles); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(installs, ".old-"+app.ID, "Example.fixture")
	for _, list := range []string{"missing", "null", "[", `[null]`, `[{"id":"invalid"}]`, string(committed)} {
		if list != "missing" {
			if err := os.WriteFile(profiles, []byte(list), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := service.RestoreLaunchers(t.Context(), nil, nil); (err != nil) != (list == "[") {
			t.Fatalf("startup with profiles %q returned %v", list, err)
		}
		_, err := os.Stat(backup)
		if list == string(committed) {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("confirmed removal retained its staged package: %v", err)
			}
		} else if err != nil {
			t.Fatalf("startup deleted a backup with profiles %q: %v", list, err)
		}
	}
}

func TestStartupReclaimSkipsBuildInProgress(t *testing.T) {
	root := t.TempDir()
	installs := filepath.Join(root, "Applications")
	started, resume := make(chan string, 1), make(chan struct{})
	bundles := bundle.NewLibrary(installs, gatedArtifact{beforeBuild: func(ctx context.Context, app profile.App) error {
		started <- app.ID
		select {
		case <-resume:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	service := library.New(store, bundles, library.Options{})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	var tasks sync.WaitGroup
	t.Cleanup(tasks.Wait)
	tasks.Go(func() {
		_, err := service.Save(ctx, profile.App{Name: "Background build", URL: "https://example.com"}, library.Icon{})
		result <- err
	})
	var id string
	select {
	case id = <-started:
	case <-ctx.Done():
		t.Fatal("the application build did not start")
	}
	if err := service.RestoreLaunchers(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	if matches, err := filepath.Glob(filepath.Join(installs, ".new-"+id+"-*")); err != nil || len(matches) != 1 {
		t.Fatalf("startup removed the working build directory: %v (%v)", matches, err)
	}
	close(resume)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(installs); err != nil || len(entries) != 1 || entries[0].Name() != id {
		t.Fatalf("the resumed build did not finish: %v (%v)", entries, err)
	}
}

func interruptRemove(t *testing.T, bundles *bundle.Library, app profile.App, commit func() error) {
	t.Helper()
	interrupted := errors.New("interrupted before deleting the staged package")
	defer func() {
		if cause := recover(); cause != interrupted {
			t.Fatalf("expected a simulated interruption, got %v", cause)
		}
	}()
	if err := bundles.Remove(app, func() error {
		if commit != nil {
			if err := commit(); err != nil {
				return err
			}
		}
		panic(interrupted)
	}); err != nil {
		t.Fatal(err)
	}
}

type menu struct {
	mu            sync.Mutex
	store         *profile.Store
	entries       map[string]string
	registrations int
	unavailable   bool
	removeError   error
}

func (m *menu) Register(app profile.App, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registrations++
	if m.unavailable {
		return errors.New("menu unavailable")
	}
	current, err := m.store.Find(app.ID)
	if err != nil || current.Name != app.Name {
		return errors.New("profile was not committed")
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	m.entries[app.ID] = path
	return nil
}

func (m *menu) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, id)
	return m.removeError
}

type registrationRecorder struct {
	menu
	updates []bool
}

func (m *registrationRecorder) Register(app profile.App, path string) error {
	m.updates = append(m.updates, true)
	return m.menu.Register(app, path)
}

func (m *registrationRecorder) RegisterIfNeeded(app profile.App, path string) error {
	m.updates = append(m.updates, false)
	return m.menu.Register(app, path)
}

func TestLibraryReusesRegistrationUntilTheApplicationChanges(t *testing.T) {
	root := t.TempDir()
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	entries := &registrationRecorder{menu: menu{store: store, entries: make(map[string]string)}}
	service := library.New(store, bundle.NewLibrary(filepath.Join(root, "Applications"), artifact{}), library.Options{Launcher: entries})
	ctx := t.Context()
	app, err := service.Save(ctx, profile.App{Name: "Example", URL: "https://example.com"}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	startup := func() {
		t.Helper()
		if err := service.RestoreLaunchers(ctx, []profile.App{app}, nil); err != nil {
			t.Fatal(err)
		}
	}
	startup()
	if err := service.Open(ctx, app, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Path(ctx, app, nil); err != nil {
		t.Fatal(err)
	}
	if saved, err := service.Save(ctx, app, library.Icon{KeepInstalled: true}); err != nil || saved != app {
		t.Fatalf("saving current settings returned %+v (%v)", saved, err)
	}
	previousPath := entries.entries[app.ID]
	if err := os.Remove(previousPath); err != nil {
		t.Fatal(err)
	}
	startup()
	if _, err := os.Stat(previousPath); err != nil {
		t.Fatal(err)
	}
	draft := app
	draft.Name = "Renamed"
	app, err = service.Save(ctx, draft, library.Icon{KeepInstalled: true})
	if err != nil || filepath.Base(entries.entries[app.ID]) != "Renamed.fixture" {
		t.Fatalf("register the renamed app: %+v (%v)", entries.entries, err)
	}
	if applied, err := service.RefreshIcon(ctx, app, icons.Brand()); err != nil || !applied {
		t.Fatalf("register refreshed artwork: applied=%t (%v)", applied, err)
	}
	want := []bool{true, false, false, false, false, true, true, true}
	if !slices.Equal(entries.updates, want) {
		t.Fatalf("forced registration calls = %v, want %v", entries.updates, want)
	}
}

func TestSaveReusesExistingIdentityAndAllowsIndependentApps(t *testing.T) {
	root := t.TempDir()
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	service := library.New(store, bundle.NewLibrary(filepath.Join(root, "Applications"), artifact{}), library.Options{})
	type result struct {
		app profile.App
		err error
	}
	results := make(chan result, 2)
	for _, draft := range []profile.App{
		{Name: "DeepSeek", URL: "https://chat.deepseek.com"},
		{Name: "deepseek", URL: "https://chat.deepseek.com:443/"},
	} {
		go func() {
			app, err := service.Save(t.Context(), draft, library.Icon{})
			results <- result{app, err}
		}()
	}
	first, second := <-results, <-results
	if first.err != nil {
		first, second = second, first
	}
	var duplicate *library.AlreadyExistsError
	if first.err != nil || !errors.As(second.err, &duplicate) || duplicate.App != first.app {
		t.Fatalf("concurrent creation did not keep one application: %+v, %+v", first, second)
	}
	entries, err := os.ReadDir(filepath.Join(root, "Applications"))
	if err != nil || len(entries) != 1 || entries[0].Name() != first.app.ID {
		t.Fatalf("duplicate creation installed another bundle: %v, %v", entries, err)
	}
	other, err := service.Save(t.Context(), profile.App{Name: "DeepSeek Work", URL: first.app.URL}, library.Icon{})
	if err != nil || other.ID == first.app.ID {
		t.Fatalf("independently named app = %+v, %v", other, err)
	}
	if _, err := service.Save(t.Context(), profile.App{Name: "DeepSeek", URL: "https://chat.deepseek.com/other"}, library.Icon{}); err != nil {
		t.Fatalf("different page on the same host: %v", err)
	}
	other.Name = first.app.Name
	if _, err := service.Save(t.Context(), other, library.Icon{}); !errors.As(err, &duplicate) {
		t.Fatalf("editing into a duplicate did not return the conflict: %v", err)
	}
	// Older versions allowed this profile. It must keep its own ID and be
	// editable without asking the user to rename or remove the other app.
	if err := store.Put(other); err != nil {
		t.Fatal(err)
	}
	other.Width += 24
	if saved, err := service.Save(t.Context(), other, library.Icon{}); err != nil || saved.ID != other.ID || saved.Width != other.Width {
		t.Fatalf("edit of an existing duplicate: %+v, %v", saved, err)
	}
}

func TestSuggestedNameKeepsAnExistingApplicationReachable(t *testing.T) {
	root := t.TempDir()
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	service := library.New(store, bundle.NewLibrary(filepath.Join(root, "Apps"), artifact{}), library.Options{})
	const address = "https://example.org/notes?view=board#today"
	draft := profile.App{URL: address}
	first, err := service.Save(t.Context(), draft, library.Icon{})
	if err != nil || first.Name != "example.org" {
		t.Fatalf("created before finding the name: %+v, %v", first, err)
	}
	assertDuplicate := func(title string, want profile.App) {
		t.Helper()
		alias := profile.App{URL: "https://EXAMPLE.org:0443/notes?view=board#today", Name: "  "}
		_, err := service.SaveWithSuggestedName(t.Context(), alias, library.Icon{
			CachedArtwork: func(profile.App) icons.Artwork {
				t.Error("an unnamed duplicate should not read cached artwork")
				return icons.Artwork{}
			},
		}, title)
		var duplicate *library.AlreadyExistsError
		if !errors.As(err, &duplicate) || duplicate.App != want {
			t.Fatalf("suggestion %q did not select %s: %v", title, want.ID, err)
		}
	}
	assertDuplicate("Board", first)
	second, err := service.SaveWithSuggestedName(t.Context(), profile.App{URL: address, Name: "Private Board"}, library.Icon{}, "Board")
	if err != nil || second.Name != "Private Board" || second.ID == first.ID {
		t.Fatalf("an explicit name did not create a separate app: %+v, %v", second, err)
	}
	assertDuplicate(second.Name, second)
	third, err := service.SaveWithSuggestedName(t.Context(), profile.App{URL: "https://example.org/notes?view=board#review"}, library.Icon{}, "Board")
	if err != nil || third.Name != "Board" || third.ID == first.ID || third.ID == second.ID {
		t.Fatalf("a different home address did not use the suggestion: %+v, %v", third, err)
	}
}

func TestSaveRecognizesEquivalentPortsAndIPv6Addresses(t *testing.T) {
	for _, test := range []struct {
		name, address, alternate string
	}{
		{"default HTTPS port", "https://example.com?view=notes#today", "https://EXAMPLE.com:0443/?view=notes#today"},
		{"default HTTP port", "http://example.com/", "http://example.com:080"},
		{"IPv6 and nondefault port", "http://[2001:db8::1]:8080/notes?view=all#today", "http://[2001:0DB8:0:0:0:0:0:1]:08080/notes?view=all#today"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store := profile.NewStore(filepath.Join(root, "apps.json"))
			installed := filepath.Join(root, "Applications")
			service := library.New(store, bundle.NewLibrary(installed, artifact{}), library.Options{})
			first, err := service.Save(t.Context(), profile.App{Name: "Desk", URL: test.address}, library.Icon{})
			if err != nil || first.URL != test.address {
				t.Fatalf("saved address %q (%v), want %q", first.URL, err, test.address)
			}
			var duplicate *library.AlreadyExistsError
			_, err = service.Save(t.Context(), profile.App{Name: "Desk", URL: test.alternate}, library.Icon{
				CachedArtwork: func(profile.App) icons.Artwork {
					t.Error("a duplicate should not read its artwork")
					return icons.Artwork{}
				},
			})
			if !errors.As(err, &duplicate) || duplicate.App != first {
				t.Fatalf("the alternate address did not identify %s: %v", first.ID, err)
			}
			entries, err := os.ReadDir(installed)
			if err != nil || len(entries) != 1 || entries[0].Name() != first.ID {
				t.Fatalf("saving the alternate address left %v installed: %v", entries, err)
			}
		})
	}
	root := t.TempDir()
	service := library.New(profile.NewStore(filepath.Join(root, "apps.json")), bundle.NewLibrary(filepath.Join(root, "Applications"), artifact{}), library.Options{})
	for _, address := range []string{
		"https://example.com/work?view=notes#today",
		"http://example.com/work?view=notes#today",
		"https://example.com:8443/work?view=notes#today",
		"https://example.com/work/?view=notes#today",
		"https://example.com/work?view=all#today",
		"https://example.com/work?view=notes#tomorrow",
	} {
		if saved, err := service.Save(t.Context(), profile.App{Name: "Desk", URL: address}, library.Icon{}); err != nil || saved.URL != address {
			t.Fatalf("distinct address %q saved as %q (%v)", address, saved.URL, err)
		}
	}
}

func TestRestoreLauncherAndKeepItCurrent(t *testing.T) {
	root := t.TempDir()
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), artifact{})
	launcher := &menu{store: store, entries: make(map[string]string), unavailable: true}
	service := library.New(store, bundles, library.Options{Launcher: launcher})
	ctx := t.Context()
	saved, err := service.Save(ctx, profile.App{Name: "Example", URL: "https://example.com"}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.entries) != 0 || launcher.registrations != 1 {
		t.Fatalf("unexpected failed entry: %+v", launcher.entries)
	}
	launcher.unavailable = false
	var artworkRequests []profile.App
	artwork := func(app profile.App) icons.Artwork {
		artworkRequests = append(artworkRequests, app)
		return icons.Artwork{Image: icons.Brand()}
	}
	if err := service.RestoreLaunchers(ctx, []profile.App{saved}, artwork); err != nil {
		t.Fatal(err)
	}
	if len(artworkRequests) != 0 {
		t.Fatalf("restoring a working bundle read its artwork: %+v", artworkRequests)
	}
	if path := launcher.entries[saved.ID]; filepath.Base(path) != "Example.fixture" {
		t.Fatalf("unexpected restored path %q", path)
	}
	draft := saved
	draft.Name = "Updated"
	updated, err := service.Save(ctx, draft, library.Icon{})
	if err != nil || updated.ID != saved.ID {
		t.Fatalf("update: %+v %v", updated, err)
	}
	if path := launcher.entries[saved.ID]; filepath.Base(path) != "Updated.fixture" || len(launcher.entries) != 1 {
		t.Fatalf("unexpected renamed entry %+v", launcher.entries)
	}
	if err := os.Remove(launcher.entries[updated.ID]); err != nil {
		t.Fatal(err)
	}
	if err := service.RestoreLaunchers(ctx, []profile.App{saved}, artwork); err != nil {
		t.Fatal(err)
	}
	if len(artworkRequests) != 1 || artworkRequests[0] != updated {
		t.Fatalf("repair used an outdated profile's icon: %+v", artworkRequests)
	}
	registrationCount := launcher.registrations
	previousSite := saved
	previousSite.URL = "https://another.example"
	if applied, err := service.RefreshIcon(ctx, previousSite, icons.Brand()); err != nil || applied || launcher.registrations != registrationCount {
		t.Fatalf("another site's icon touched the launcher: applied=%v, err=%v", applied, err)
	}
	if applied, err := service.RefreshIcon(ctx, saved, icons.Brand()); err != nil || !applied || launcher.registrations != registrationCount+1 {
		t.Fatalf("the original site's icon did not refresh the renamed app: applied=%v, err=%v", applied, err)
	}
	if current, err := store.Find(saved.ID); err != nil || current != updated || filepath.Base(launcher.entries[saved.ID]) != "Updated.fixture" {
		t.Fatalf("refresh changed the renamed app's settings or launcher: %+v, %v", current, err)
	}
	if err := service.Remove(saved.ID); err != nil || len(launcher.entries) != 0 {
		t.Fatalf("removal: %+v %v", launcher.entries, err)
	}
	if err := service.RestoreLaunchers(ctx, []profile.App{saved}, artwork); err != nil || len(launcher.entries) != 0 {
		t.Fatalf("restoring a stale snapshot brought back a removed app: %+v, %v", launcher.entries, err)
	}
	if applied, err := service.RefreshIcon(ctx, saved, icons.Brand()); err != nil || applied || len(launcher.entries) != 0 {
		t.Fatalf("a late icon update restored the removed app: applied=%v, err=%v", applied, err)
	}
}

type restoreBundles struct {
	*bundle.Library
	brokenID string
	failure  error
}

func (b restoreBundles) Path(app profile.App) (string, error) {
	if app.ID == b.brokenID {
		return "", b.failure
	}
	return b.Library.Path(app)
}

func TestRestoreLaunchersContinuesAfterAnUnavailableApp(t *testing.T) {
	root := t.TempDir()
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), artifact{})
	service := library.New(store, bundles, library.Options{})
	save := func(name string) profile.App {
		t.Helper()
		app, err := service.Save(t.Context(), profile.App{Name: name, URL: "https://example.org"}, library.Icon{})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	broken, available := save("Busy app"), save("Available app")
	apps := []profile.App{broken, available}
	if err := service.RestoreLaunchers(t.Context(), apps, nil); err != nil {
		t.Fatalf("startup without a desktop menu returned %v", err)
	}
	failure := errors.New("application directory is unavailable")
	entries := &menu{store: store, entries: make(map[string]string)}
	service = library.New(store, restoreBundles{Library: bundles, brokenID: broken.ID, failure: failure}, library.Options{Launcher: entries})
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.RestoreLaunchers(canceled, apps, nil); !errors.Is(err, context.Canceled) || entries.registrations != 0 {
		t.Fatalf("canceled startup registered %d entries and returned %v", entries.registrations, err)
	}
	if err := service.RestoreLaunchers(t.Context(), apps, nil); !errors.Is(err, failure) || len(entries.entries) != 1 ||
		filepath.Base(entries.entries[available.ID]) != "Available app.fixture" {
		t.Fatalf("a broken app prevented restoring other entries: %+v, %v", entries.entries, err)
	}
}

type failingWriteStore struct {
	*profile.Store
	fail bool
}

func (s *failingWriteStore) Put(app profile.App) error {
	if s.fail {
		return errors.New("could not write settings")
	}
	return s.Store.Put(app)
}

func TestCustomIconPersistsThroughRepairAndFailedReplacement(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "apps.json")
	bundlesPath, iconsPath := filepath.Join(root, "Applications"), filepath.Join(root, "CustomIcons")
	profiles := &failingWriteStore{Store: profile.NewStore(configPath)}
	bundles := bundle.NewLibrary(bundlesPath, artifact{})
	custom := icons.NewCustomStore(iconsPath)
	service := library.New(profiles, bundles, library.Options{CustomIcons: custom})
	ctx := t.Context()
	draft := profile.App{Name: "Workspace", URL: "https://example.com"}
	firstIcon := icons.Fallback("K", "#ec2c76")
	selection := library.Icon{Artwork: icons.Artwork{Image: firstIcon}, Custom: true}
	saved, err := service.Save(ctx, draft, selection)
	if err != nil || !profile.ValidIconKey(saved.CustomIcon) {
		t.Fatalf("save custom icon: %+v %v", saved, err)
	}
	draft.Name = "Second workspace"
	second, err := service.Save(ctx, draft, selection)
	if err != nil || second.CustomIcon != saved.CustomIcon || second.ID == saved.ID {
		t.Fatalf("reuse custom icon: %+v %v", second, err)
	}
	if err := service.Remove(second.ID); err != nil || custom.Get(saved.CustomIcon) == nil {
		t.Fatalf("shared icon removed too soon: %v", err)
	}
	path, err := bundles.Path(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	restarted := library.New(profile.NewStore(configPath), bundle.NewLibrary(bundlesPath, artifact{}),
		library.Options{CustomIcons: icons.NewCustomStore(iconsPath)})
	if _, err := restarted.Path(ctx, saved, func(profile.App) icons.Artwork { return icons.Artwork{Image: icons.Brand()} }); err != nil {
		t.Fatal(err)
	}
	if applied, err := restarted.RefreshIcon(ctx, saved, icons.Brand()); err != nil || applied {
		t.Fatalf("the website icon replaced a custom icon: applied=%v, err=%v", applied, err)
	}
	expected, err := icons.PNG(firstIcon)
	if err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, expected) {
		t.Fatalf("regenerated application did not keep the custom icon: %v", err)
	}
	profiles.fail = true
	if _, err := service.Save(ctx, saved, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}, Custom: true}); err == nil {
		t.Fatal("failed profile write was reported as a successful replacement")
	}
	if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, expected) {
		t.Fatalf("failed replacement changed the current installed icon: %v", err)
	}
	if files, err := os.ReadDir(iconsPath); err != nil || len(files) != 1 {
		t.Fatalf("failed replacement left an unattached copy: %v %v", files, err)
	}
	if err := restarted.Remove(saved.ID); err != nil || custom.Get(saved.CustomIcon) != nil {
		t.Fatalf("unused icon wasn't removed: %v", err)
	}
}

func TestSharedCustomIconSurvivesWhileAnotherSaveIsPending(t *testing.T) {
	root := t.TempDir()
	started, resume := make(chan struct{}, 2), make(chan struct{})
	bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), gatedArtifact{
		beforeBuild: func(ctx context.Context, app profile.App) error {
			if app.Name == "Original" {
				return nil
			}
			started <- struct{}{}
			select {
			case <-resume:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	custom := icons.NewCustomStore(filepath.Join(root, "CustomIcons"))
	service := library.New(store, bundles, library.Options{CustomIcons: custom})
	artwork := library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}, Custom: true}
	original, err := service.Save(t.Context(), profile.App{Name: "Original", URL: "https://example.org"}, artwork)
	if err != nil {
		t.Fatal(err)
	}
	release := sync.OnceFunc(func() { close(resume) })
	var tasks sync.WaitGroup
	t.Cleanup(func() {
		release()
		tasks.Wait()
	})
	cancelled, cancel := context.WithCancel(t.Context())
	defer cancel()
	failed := make(chan error, 1)
	tasks.Go(func() {
		_, err := service.Save(cancelled, profile.App{
			Name: "Cancelled edit", URL: "https://example.org/edit", CustomIcon: original.CustomIcon,
		}, library.Icon{})
		failed <- err
	})
	type result struct {
		app profile.App
		err error
	}
	finished := make(chan result, 1)
	tasks.Go(func() {
		app, err := service.Save(t.Context(), profile.App{Name: "New app", URL: "https://example.org/new"}, artwork)
		finished <- result{app, err}
	})
	deadline := time.After(time.Second)
	for range 2 {
		select {
		case <-started:
		case <-deadline:
			t.Fatal("both saves did not reach the package writer")
		}
	}
	if err := service.Remove(original.ID); err != nil {
		t.Fatal(err)
	}
	if custom.Get(original.CustomIcon) == nil {
		t.Fatal("removing the last saved app deleted the artwork shared by pending saves")
	}
	cancel()
	if err := <-failed; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save returned %v", err)
	}
	if custom.Get(original.CustomIcon) == nil {
		t.Fatal("cancelling one save deleted artwork used by the other pending save")
	}
	release()
	saved := <-finished
	if saved.err != nil || saved.app.CustomIcon != original.CustomIcon || custom.Get(original.CustomIcon) == nil {
		t.Fatalf("committed app lost its shared custom icon: %+v, %v", saved.app, saved.err)
	}
	if err := service.Remove(saved.app.ID); err != nil || custom.Get(original.CustomIcon) != nil {
		t.Fatalf("unused shared artwork was not cleaned up: %v", err)
	}
}

func TestWindowRevisionChangesOnlyWhenNewDimensionsAreCommitted(t *testing.T) {
	root := t.TempDir()
	store := &failingWriteStore{Store: profile.NewStore(filepath.Join(root, "apps.json"))}
	bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), artifact{})
	service := library.New(store, bundles, library.Options{})
	ctx := t.Context()
	original, err := service.Save(ctx, profile.App{Name: "Example", URL: "https://example.com", WindowRevision: 20}, library.Icon{})
	if err != nil || original.WindowRevision != 0 {
		t.Fatalf("new placement = %+v, %v", original, err)
	}
	draft := original
	draft.Width = 900
	store.fail = true
	if _, err := service.Save(ctx, draft, library.Icon{}); err == nil {
		t.Fatal("missing error for failed save")
	}
	if saved, err := store.Find(original.ID); err != nil || saved.Width != original.Width || saved.WindowRevision != 0 {
		t.Fatalf("failed save changed the stored window placement: %+v, %v", saved, err)
	}
	store.fail = false
	resized, err := service.Save(ctx, draft, library.Icon{})
	if err != nil || resized.WindowRevision != 1 || resized.Width != 900 {
		t.Fatalf("first size change = %+v, %v", resized, err)
	}
	renamed := resized
	renamed.Name, renamed.URL, renamed.WindowRevision = "New name", "https://example.com/new", 0
	edited, err := service.Save(ctx, renamed, library.Icon{})
	if err != nil || edited.WindowRevision != resized.WindowRevision {
		t.Fatalf("editing other settings reset the saved placement: %+v, %v", edited, err)
	}
	reverted, err := service.Save(ctx, original, library.Icon{})
	if err != nil || reverted.Width != original.Width || reverted.WindowRevision != resized.WindowRevision+1 {
		t.Fatalf("returning to the original dimensions reused a stale placement: %+v, %v", reverted, err)
	}
}

func TestUnchangedSaveKeepsProfileAndRepairsMissingApp(t *testing.T) {
	root := t.TempDir()
	store := &failingWriteStore{Store: profile.NewStore(filepath.Join(root, "apps.json"))}
	bundles := bundle.NewLibrary(filepath.Join(root, "Applications"), artifact{})
	service := library.New(store, bundles, library.Options{})
	icon := library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}}
	saved, err := service.Save(t.Context(), profile.App{Name: "Desk", URL: "https://example.org"}, icon)
	if err != nil {
		t.Fatal(err)
	}
	path, err := bundles.Path(saved)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	store.fail = true
	icon.KeepInstalled = true
	if unchanged, err := service.Save(t.Context(), saved, icon); err != nil || unchanged != saved {
		t.Fatalf("unchanged save attempted to write a profile: %+v %v", unchanged, err)
	}
	if after, err := os.Stat(path); err != nil || !os.SameFile(before, after) {
		t.Fatalf("unchanged save replaced the app: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if unchanged, err := service.Save(t.Context(), saved, icon); err != nil || unchanged != saved {
		t.Fatalf("repair changed the profile or failed: %+v %v", unchanged, err)
	}
	if repaired, err := bundles.Path(saved); err != nil || repaired != path {
		t.Fatalf("unchanged save did not restore the missing app: %q %v", repaired, err)
	}
}

type runtimeRefreshFixture struct {
	*bundle.Library
	failed   bool
	attempts int
}

func (r *runtimeRefreshFixture) RefreshRuntime(_ context.Context, _ string) error {
	r.attempts++
	if r.failed {
		return errors.New("executable is open")
	}
	return nil
}

func TestExistingAppCanOpenWhileRuntimeUpdateIsDeferred(t *testing.T) {
	root := t.TempDir()
	bundles := &runtimeRefreshFixture{Library: bundle.NewLibrary(filepath.Join(root, "Applications"), artifact{}), failed: true}
	store := profile.NewStore(filepath.Join(root, "apps.json"))
	service := library.New(store, bundles, library.Options{})
	ctx := t.Context()
	app, err := service.Save(ctx, profile.App{Name: "Workbench", URL: "https://example.org"}, library.Icon{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Open(ctx, app, nil); err != nil || bundles.attempts != 1 {
		t.Fatalf("the running app could not reopen during the update: %v", err)
	}
	bundles.failed = false
	if err := service.Open(ctx, app, nil); err != nil || bundles.attempts != 2 {
		t.Fatalf("the deferred runtime update did not retry: %v", err)
	}
	saved, err := store.Find(app.ID)
	if err != nil || saved.URL != app.URL || !saved.UpdatedAt.Equal(app.UpdatedAt) {
		t.Fatalf("runtime refresh altered the website settings: %v", err)
	}
}
