//go:build unix

package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/fileutil"
	"golang.org/x/sys/unix"
)

func TestStoreRejectsAPipeWithNoWriter(t *testing.T) {
	for _, checkCache := range []bool{false, true} {
		name := "FirstRead"
		if checkCache {
			name = "AfterReadingAnApp"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "apps.json")
			store := NewStore(path)
			original := App{ID: NewID(), Name: "Example", URL: "https://example.org"}
			if err := store.Put(original); err != nil {
				t.Fatal(err)
			}
			if checkCache {
				if _, err := store.List(); err != nil {
					t.Fatal(err)
				}
			} else {
				store = NewStore(path)
			}
			backup := path + ".saved"
			if err := os.Rename(path, backup); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				_, err := store.List()
				result <- err
			}()
			select {
			case err := <-result:
				if !errors.Is(err, fileutil.ErrNotRegular) {
					t.Fatalf("reading the pipe returned %v", err)
				}
			case <-time.After(time.Second):
				// Release the blocking read when demonstrating the regression on an older build.
				if writer, err := os.OpenFile(path, os.O_RDWR|unix.O_NONBLOCK, 0); err == nil {
					_ = writer.Close()
				}
				t.Fatal("reading the app list waited for a pipe writer")
			}
			if err := store.Put(App{ID: NewID(), URL: "https://example.com"}); !errors.Is(err, fileutil.ErrNotRegular) {
				t.Fatalf("trying to save through a pipe returned %v", err)
			}
			if err := os.Rename(backup, path); err != nil {
				t.Fatal(err)
			}
			if recovered, err := store.Find(original.ID); err != nil || recovered != original {
				t.Fatalf("restoring the ordinary profile file loaded %+v (%v)", recovered, err)
			}
		})
	}
}
