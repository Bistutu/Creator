package bundle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestCheckConfigUsesCommittedSettings(t *testing.T) {
	app := profile.App{ID: profile.NewID(), Name: "工作台", URL: "https://example.com/?one=1&two=2", Width: 800, Height: 620}
	path := filepath.Join(t.TempDir(), ConfigName)
	if err := writeConfig(path, app, iconMetadata{}); err != nil {
		t.Fatal(err)
	}
	if err := checkConfig(path, app); err != nil {
		t.Fatalf("configuration before window revisions should still work: %v", err)
	}
	app.WindowRevision = 1
	if err := checkConfig(path, app); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale configuration was accepted: %v", err)
	}
	want := iconMetadata{IconKind: icons.WebsiteArtwork, IconDigest: "digest"}
	if err := writeConfig(path, app, want); err != nil {
		t.Fatal(err)
	}
	if err := checkConfig(path, app); err != nil {
		t.Fatalf("current configuration was rejected: %v", err)
	}
	if config, err := readConfig(path); err != nil || config.iconMetadata != want {
		t.Fatalf("the installed icon metadata was lost: %+v, %v", config, err)
	}
	for _, malformed := range [][]byte{nil, []byte(`{"url":`), bytes.Repeat([]byte(" "), (16<<10)+1)} {
		if err := os.WriteFile(path, malformed, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := checkConfig(path, app); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("configuration with %d invalid bytes was accepted: %v", len(malformed), err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := checkConfig(path, app); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing configuration was accepted: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := checkConfig(path, app); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a directory in place of the configuration was accepted: %v", err)
	}
}
