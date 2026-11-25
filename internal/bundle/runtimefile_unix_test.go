//go:build unix

package bundle

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRuntimeRefreshRepairsAPipeMarker(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "runner")
	cache := filepath.Join(root, "Runtimes")
	writeRuntimeFixture(t, source, []byte("previous runtime"))
	app := runtimeDestination(t, root, "installed")
	if err := openRuntimeFixture(t, source, cache).install(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(filepath.Dir(app), runtimeMarker)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(marker, 0o600); err != nil {
		t.Fatal(err)
	}
	writeRuntimeFixture(t, source, []byte("updated runtime"))
	runner := openRuntimeFixture(t, source, cache)
	result := make(chan error, 1)
	go func() { result <- runner.refresh(t.Context(), app) }()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		// Release the old blocking read when reproducing the regression.
		if file, err := os.OpenFile(marker, os.O_RDWR|unix.O_NONBLOCK, 0); err == nil {
			_ = file.Close()
		}
		t.Fatal("checking the installed runtime waited for a pipe writer")
	}
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the runtime marker is still invalid: %v (%v)", info, err)
	}
	assertFile(t, marker, runner.digest)
	assertFile(t, app, "updated runtime")
}
