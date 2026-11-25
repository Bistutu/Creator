package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestReplaceProfilesWaitsForReader(t *testing.T) {
	dir := t.TempDir()
	current, next := filepath.Join(dir, Filename), filepath.Join(dir, "next.json")
	for path, data := range map[string]string{current: "before", next: "after"} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := os.Open(current)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := os.Rename(next, current); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("replacing the held snapshot returned %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- replaceProfiles(next, current) }()
	select {
	case err := <-done:
		t.Fatalf("replacement finished while the snapshot was still open: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(current); err != nil || string(data) != "after" {
		t.Fatalf("committed snapshot = %q (%v)", data, err)
	}
}
