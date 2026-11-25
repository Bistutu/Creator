package runnerconfig

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestReadWindowIcon(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, WindowIconFilename)
	if data := ReadWindowIcon(dir); data != nil {
		t.Fatal("a missing window icon should use the desktop default")
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if data := ReadWindowIcon(dir); data != nil {
		t.Fatal("a directory was read as a window icon")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	encode := func(width, height int) []byte {
		t.Helper()
		var output bytes.Buffer
		if err := png.Encode(&output, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	for _, example := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"website artwork", encode(128, 128), true},
		{"custom artwork", encode(256, 256), true},
		{"legacy dimensions", encode(maxWindowIconSide, 2), true},
		{"invalid PNG header", []byte("not a PNG"), false},
		{"wide PNG", encode(maxWindowIconSide+1, 2), false},
		{"tall PNG", encode(2, maxWindowIconSide+1), false},
	} {
		t.Run(example.name, func(t *testing.T) {
			if err := os.WriteFile(path, example.data, 0o600); err != nil {
				t.Fatal(err)
			}
			got := ReadWindowIcon(dir)
			if example.valid && !bytes.Equal(got, example.data) {
				t.Fatal("the stored window icon changed")
			}
			if !example.valid && got != nil {
				t.Fatal("an invalid window icon was passed to the toolkit")
			}
		})
	}
	if err := os.WriteFile(path, encode(128, 128), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, maxWindowIconBytes+1); err != nil {
		t.Fatal(err)
	}
	if data := ReadWindowIcon(dir); data != nil {
		t.Fatal("an oversized file with a valid PNG header was read as a window icon")
	}
}
