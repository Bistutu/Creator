package bundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

type textBackend struct {
	fail     error
	opened   string
	finished func()
}

func (*textBackend) Filename(app profile.App) string { return fileLabel(app.Name) + ".txt" }
func (b *textBackend) Build(_ context.Context, path string, app profile.App, _ func() icons.Artwork, _ string) error {
	if b.fail != nil {
		return b.fail
	}
	if err := os.WriteFile(path, []byte(app.URL), 0o600); err != nil {
		return err
	}
	if b.finished != nil {
		b.finished()
	}
	return nil
}
func (b *textBackend) Open(_ context.Context, path string) error { b.opened = path; return nil }

func TestInstallUpdateRollbackAndRemove(t *testing.T) {
	ctx := context.Background()
	errSave := errors.New("save profile failed")
	backend := &textBackend{}
	root := filepath.Join(t.TempDir(), "output")
	library := NewLibrary(root, backend)
	app := profile.App{ID: profile.NewID(), Name: "A & B", URL: "https://example.com/v1"}
	updated := app
	updated.Name, updated.URL, updated.UpdatedAt = "新名字", "https://example.com/v2", time.Now()
	icon := icons.Brand()
	if path, err := library.Path(app); !errors.Is(err, os.ErrNotExist) || path != "" {
		t.Fatalf("absent path = %q, %v", path, err)
	}
	if _, err := library.Install(ctx, profile.App{}, updated, icons.Artwork{Image: icon}, func() error { return errSave }); !errors.Is(err, errSave) {
		t.Fatalf("initial failure = %v", err)
	}
	path, err := library.Install(ctx, profile.App{}, app, icons.Artwork{Image: icon}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, app.URL)
	if err := library.Open(ctx, path); err != nil || backend.opened != path {
		t.Fatalf("open %q: %v", backend.opened, err)
	}
	if _, err := library.Install(ctx, app, updated, icons.Artwork{Image: icon}, func() error { return errSave }); !errors.Is(err, errSave) {
		t.Fatalf("update failure = %v", err)
	}
	assertFile(t, path, app.URL)
	backend.fail = errors.New("native packager failed")
	if _, err := library.Install(ctx, app, updated, icons.Artwork{Image: icon}, nil); !errors.Is(err, backend.fail) {
		t.Fatalf("backend failure = %v", err)
	}
	assertFile(t, path, app.URL)
	backend.fail = nil
	newPath, err := library.Install(ctx, app, updated, icons.Artwork{Image: icon}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, newPath, updated.URL)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old filename exists: %v", err)
	}
	if err := library.Remove(updated, func() error { return errSave }); !errors.Is(err, errSave) {
		t.Fatalf("remove rollback = %v", err)
	}
	assertFile(t, newPath, updated.URL)
	if err := library.Remove(updated, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact still exists: %v", err)
	}
}

func TestRestoreInterruptedMoveAndDetectOldRevision(t *testing.T) {
	library := NewLibrary(t.TempDir(), &textBackend{})
	app := profile.App{ID: profile.NewID(), Name: "Same name", URL: "https://example.com"}
	ctx := context.Background()
	path, err := library.Install(ctx, profile.App{}, app, icons.Artwork{Image: icons.Brand()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir, backup := library.paths(app.ID)
	if err := os.Rename(dir, backup); err != nil {
		t.Fatal(err)
	}
	if recovered, err := library.Path(app); recovered != path || err != nil {
		t.Fatalf("recovery = %q, %v", recovered, err)
	}
	app.URL += "/new"
	if _, err := library.Path(app); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale revision = %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := library.Install(cancelled, app, app, icons.Artwork{Image: icons.Brand()}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled install = %v", err)
	}
	assertFile(t, path, "https://example.com")
	if _, err := library.Path(profile.App{ID: "../oops"}); err == nil {
		t.Fatal("unsafe path accepted")
	}
	if err := library.Remove(profile.App{ID: "../oops"}, func() error { t.Fatal("unsafe ID used"); return nil }); err == nil {
		t.Fatal("unsafe removal accepted")
	}
}

func TestCancellationAfterBuildingKeepsTheSavedApp(t *testing.T) {
	backend := &textBackend{}
	manager := NewLibrary(t.TempDir(), backend)
	committed := profile.App{ID: profile.NewID(), Name: "Example", URL: "https://example.com/original"}
	icon := icons.Brand()
	path, err := manager.Install(t.Context(), profile.App{}, committed, icons.Artwork{Image: icon}, nil)
	if err != nil {
		t.Fatal(err)
	}
	draft := committed
	draft.URL = "https://example.com/proposed"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	backend.finished = cancel
	if _, err := manager.Install(ctx, committed, draft, icons.Artwork{Image: icon}, func() error {
		t.Fatal("cancelled operation committed preferences")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save returned %v", err)
	}
	assertFile(t, path, committed.URL)
	entries, err := os.ReadDir(manager.root)
	if err != nil || len(entries) != 1 || entries[0].Name() != committed.ID {
		t.Fatalf("cancelled operation left partial files: %v, %v", entries, err)
	}
}

func TestOpeningRecoversTheCommittedAppAfterAnInterruptedSwap(t *testing.T) {
	for _, committedUpdate := range []bool{false, true} {
		name := "before profile write"
		if committedUpdate {
			name = "after profile write"
		}
		t.Run(name, func(t *testing.T) {
			manager, old, next, before, after := interruptedSwap(t)
			committed, expected := old, before
			if committedUpdate {
				committed, expected = next, after
			}
			path, err := manager.Path(committed)
			if err != nil {
				t.Fatal(err)
			}
			assertFile(t, path, committed.URL)
			actual, err := os.Stat(path)
			if err != nil || !os.SameFile(actual, expected) {
				t.Fatalf("the saved executable was not preserved: %v", err)
			}
			_, backup := manager.paths(committed.ID)
			if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("superseded backup remains: %v", err)
			}
		})
	}
}

func TestMutationAfterInterruptedSwapPreservesTheCommittedAppOnFailure(t *testing.T) {
	storageError := errors.New("profile write failed")
	commit := func() error { return storageError }
	for _, operation := range []string{"save", "remove"} {
		t.Run(operation, func(t *testing.T) {
			manager, old, next, before, _ := interruptedSwap(t)
			var err error
			if operation == "save" {
				_, err = manager.Install(t.Context(), old, next, icons.Artwork{Image: icons.Brand()}, commit)
			} else {
				err = manager.Remove(old, commit)
			}
			if !errors.Is(err, storageError) {
				t.Fatalf("failed %s returned %v", operation, err)
			}
			dir, backup := manager.paths(old.ID)
			path := filepath.Join(dir, manager.backend.Filename(old))
			assertFile(t, path, old.URL)
			if !os.SameFile(fileInfo(t, path), before) {
				t.Fatal("failed operation replaced the committed artifact")
			}
			if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("backup was not restored: %v", err)
			}
		})
	}
}

func TestUnrecognizedRevisionsStayUntilRepairFinishes(t *testing.T) {
	manager, committed, _, _, _ := interruptedSwap(t)
	committed.URL += "/different"
	if _, err := manager.Path(committed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unrecognized revision = %v", err)
	}
	backend := manager.backend.(*textBackend)
	backend.fail = errors.New("not enough space")
	if _, err := manager.Install(t.Context(), committed, committed, icons.Artwork{Image: icons.Brand()}, nil); !errors.Is(err, backend.fail) {
		t.Fatalf("broken repair = %v", err)
	}
	dir, backup := manager.paths(committed.ID)
	for _, candidate := range []string{dir, backup} {
		if _, err := os.Stat(filepath.Join(candidate, ".revision")); err != nil {
			t.Fatalf("repair failure discarded %s: %v", candidate, err)
		}
	}
	backend.fail = nil
	path, err := manager.Install(t.Context(), committed, committed, icons.Artwork{Image: icons.Brand()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, committed.URL)
	if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful repair left the backup: %v", err)
	}
}

// Materialize both real bundle directory layouts, then stop just after the swap.
func interruptedSwap(t *testing.T) (*Library, profile.App, profile.App, os.FileInfo, os.FileInfo) {
	t.Helper()
	root := t.TempDir()
	backend := &textBackend{}
	manager := NewLibrary(filepath.Join(root, "apps"), backend)
	staging := NewLibrary(filepath.Join(root, "stage"), backend)
	old := profile.App{ID: profile.NewID(), Name: "Saved Name", URL: "https://example.com/saved"}
	next := old
	next.URL, next.UpdatedAt = "https://example.com/proposed", time.Now().UTC()
	icon := icons.Brand()
	first, err := manager.Install(t.Context(), profile.App{}, old, icons.Artwork{Image: icon}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := staging.Install(t.Context(), profile.App{}, next, icons.Artwork{Image: icon}, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, after := fileInfo(t, first), fileInfo(t, second)
	dir, backup := manager.paths(old.ID)
	if err := os.Rename(dir, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Dir(second), dir); err != nil {
		t.Fatal(err)
	}
	return manager, old, next, before, after
}

func TestPreviouslyInstalledBundleKeepsItsPathAndFileIdentity(t *testing.T) {
	backend := &textBackend{}
	root := t.TempDir()
	current := &Library{
		root: filepath.Join(root, "Applications", "Creator"),
		priorRoots: []string{
			filepath.Join(root, "Applications", "WebCrate"),
			filepath.Join(root, "Application Support", "WebCrate", "Applications"),
		},
		backend: backend,
	}
	for i, location := range []string{"Applications", "Application Support"} {
		t.Run(location, func(t *testing.T) {
			prior := NewLibrary(current.priorRoots[i], backend)
			app := profile.App{ID: profile.NewID(), Name: "Existing", URL: "https://example.com"}
			old, err := prior.Install(t.Context(), profile.App{}, app, icons.Artwork{Image: icons.Brand()}, nil)
			if err != nil {
				t.Fatal(err)
			}
			before := fileInfo(t, old)
			path, err := current.Path(app)
			if err != nil || path != old {
				t.Fatalf("existing app = %q, %v; want %q", path, err, old)
			}
			if after, err := os.Stat(path); err != nil || !os.SameFile(before, after) {
				t.Fatalf("existing file was replaced: %v", err)
			}
			if _, err := os.Stat(filepath.Join(current.root, app.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("existing bundle appeared at the new apps location: %v", err)
			}
		})
	}
}

func TestPriorInstallSurvivesFailedSave(t *testing.T) {
	backend := &textBackend{}
	root := t.TempDir()
	prior := NewLibrary(filepath.Join(root, "Application Support", "WebCrate"), backend)
	current := &Library{root: filepath.Join(root, "Applications", "Creator"), priorRoots: []string{prior.root}, backend: backend}
	app := profile.App{ID: profile.NewID(), Name: "Existing", URL: "https://example.com/a"}
	icon := icons.Brand()
	old, err := prior.Install(t.Context(), profile.App{}, app, icons.Artwork{Image: icon}, nil)
	if err != nil {
		t.Fatal(err)
	}
	updated := app
	updated.URL = "https://example.com/b"
	writeError := errors.New("failed to save profile")
	if _, err := current.Install(t.Context(), app, updated, icons.Artwork{Image: icon}, func() error { return writeError }); !errors.Is(err, writeError) {
		t.Fatalf("failed update error = %v", err)
	}
	assertFile(t, old, app.URL)
	path, err := current.Install(t.Context(), app, updated, icons.Artwork{Image: icon}, nil)
	if err != nil || path != old {
		t.Fatalf("updated app = %q, %v; want %q", path, err, old)
	}
	assertFile(t, path, updated.URL)
	if err := current.Remove(updated, func() error { return writeError }); !errors.Is(err, writeError) {
		t.Fatalf("failed removal error = %v", err)
	}
	assertFile(t, path, updated.URL)
	if err := current.Remove(updated, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed bundle still exists: %v", err)
	}
}

func TestFileLabel(t *testing.T) {
	for input, want := range map[string]string{"..": "Web app", "A/B\\C:D": "A-B-C-D", " 学习 ": "学习", "CON.txt": "Web CON.txt", "报告\n": "报告-"} {
		if got := fileLabel(input); got != want {
			t.Errorf("fileLabel(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := readRuntime(t.TempDir(), "missing"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing runtime error = %v", err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	if data, err := os.ReadFile(path); err != nil || string(data) != want {
		t.Fatalf("%s = %q, %v; want %q", path, data, err, want)
	}
}

// Get the identity while the file is open. Windows can otherwise resolve
// identity from the old path after it has moved.
func fileInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return info
}
