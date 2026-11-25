//go:build unix

package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReadRegularRejectsFIFOWithoutWaitingForWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := ReadRegular(path, 1024)
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("pipe was accepted as a regular file: %v", err)
		}
	case <-time.After(time.Second):
		// Unblock an older reader before reporting the regression.
		if writer, err := os.OpenFile(path, os.O_RDWR|unix.O_NONBLOCK, 0); err == nil {
			_ = writer.Close()
		}
		t.Fatal("opening a pipe waited for a writer")
	}
}
