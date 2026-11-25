package bundle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestRemovalCanRetryAfterAMarkerCollision(t *testing.T) {
	library := NewLibrary(t.TempDir(), &textBackend{})
	app := profile.App{ID: profile.NewID(), Name: "Example", URL: "https://example.com"}
	path, err := library.Install(t.Context(), profile.App{}, app, icons.Artwork{Image: icons.Brand()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(library.root, pendingRemovalPrefix+app.ID)
	foreign := []byte("foreign file")
	if err := os.WriteFile(marker, foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	commitCalled := false
	commit := func() error {
		commitCalled = true
		return nil
	}
	if err := library.Remove(app, commit); err == nil || commitCalled {
		t.Fatalf("removing with a conflicting marker committed %t and returned %v", commitCalled, err)
	}
	if current, err := os.Stat(path); err != nil || !os.SameFile(original, current) {
		t.Fatalf("the marker collision changed the installed bundle: %v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || !bytes.Equal(data, foreign) {
		t.Fatalf("the marker collision overwrote the unrelated file: %v", err)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := library.Remove(app, commit); err != nil || !commitCalled {
		t.Fatalf("removing with an existing valid marker committed %t and returned %v", commitCalled, err)
	}
	if entries, err := os.ReadDir(library.root); err != nil || len(entries) != 0 {
		t.Fatalf("removal left %v (%v)", entries, err)
	}
}

func TestReclaimSkipsSymlinkedStages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated privileges on Windows")
	}
	root, other := t.TempDir(), t.TempDir()
	outside := filepath.Join(other, "keep")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	library := NewLibrary(root, nil)
	stage, err := os.MkdirTemp(root, installStagePrefix+profile.NewID()+"-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(stage, "archive")); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, installStagePrefix+profile.NewID()+"-100")
	if err := os.Symlink(other, foreign); err != nil {
		t.Fatal(err)
	}
	inspect := func(_ string, _ bool, clean func() error) error { return clean() }
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := library.ReclaimInterrupted(ctx, func(_ string, _ bool, clean func() error) error {
		cancel()
		return clean()
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceling cleanup returned %v", err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("cancellation changed the work directory: %v", err)
	}
	if err := library.ReclaimInterrupted(t.Context(), inspect); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(foreign) {
		t.Fatalf("cleanup changed unexpected files: %v (%v)", entries, err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
		t.Fatalf("cleanup followed the symlink into another directory: %v", err)
	}
}

func TestReclaimRetriesWhenBackupCannotBeDeleted(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions")
	}
	root := t.TempDir()
	prior := filepath.Join(t.TempDir(), "Old installs")
	library := &Library{root: root, priorRoots: []string{prior}}
	id := profile.NewID()
	_, backup := library.paths(id)
	marker := filepath.Join(root, pendingRemovalPrefix+id)
	if err := os.Mkdir(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(prior, id)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "website.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := markRemoval(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backup, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(backup, 0o700) })
	reclaim := func() error {
		return library.ReclaimInterrupted(t.Context(), func(next string, removal bool, clean func() error) error {
			if next != id || !removal {
				t.Fatalf("unexpected removal marker %s (%t)", next, removal)
			}
			return clean()
		})
	}
	if err := reclaim(); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("removing the read-only backup returned %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the failed cleanup discarded its marker: %v", err)
	}
	if err := os.Chmod(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := reclaim(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{marker, backup, legacy} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cleanup left %s: %v", path, err)
		}
	}
}

func BenchmarkReclaimCleanInstallRoot(b *testing.B) {
	root := b.TempDir()
	for range 1200 {
		if err := os.Mkdir(filepath.Join(root, profile.NewID()), 0o700); err != nil {
			b.Fatal(err)
		}
	}
	library := NewLibrary(root, nil)
	inspect := func(id string, _ bool, _ func() error) error {
		b.Fatalf("an installed application became a cleanup candidate: %s", id)
		return nil
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := library.ReclaimInterrupted(b.Context(), inspect); err != nil {
			b.Fatal(err)
		}
	}
}
