package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Bistutu/Creator/internal/icons"
)

const tileSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M3 3h18v18H3z"/></svg>`

func TestGenerateColorsCatalogMarksAndWritesBrand(t *testing.T) {
	root, tiles := testCatalog(t, `[{"mark":"one","ink":"124578"},{"mark":"two","ink":"ABCDEF","local":true}]`)
	local := filepath.Join(root, "internal", "catalog", "marks", "two.svg")
	writeFixture(t, local, []byte(strings.Replace(tileSVG, "<svg ", "<svg fill=\"currentColor\" ", 1)))
	data := iconArchive(t,
		archiveFile{"package/one.svg", "ignored"},
		archiveFile{"package/icons/one", "ignored"},
		archiveFile{"package/icons/one.svg", tileSVG},
		archiveFile{"package/icons/unused.svg", "ignored"},
		archiveFile{"package/icons/two.svg", "the local mark takes precedence"},
	)
	archive := filepath.Join(root, "icons.tgz")
	writeFixture(t, archive, data)

	if err := generate(root, archive, fmt.Sprintf("%x", sha256.Sum256(data))); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(tiles); err != nil || len(entries) != 2 {
		t.Fatalf("catalog contains %d tiles: %v", len(entries), err)
	}
	for _, mark := range []struct {
		name string
		ink  color.NRGBA
	}{
		{"one.rgba.zlib", color.NRGBA{R: 0x12, G: 0x45, B: 0x78, A: 255}},
		{"two.rgba.zlib", color.NRGBA{R: 0xab, G: 0xcd, B: 0xef, A: 255}},
	} {
		data, err := os.ReadFile(filepath.Join(tiles, mark.name))
		if err != nil {
			t.Fatal(err)
		}
		reader, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		pixels, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil || len(pixels) != 4*icons.Size*icons.Size {
			t.Fatalf("catalog entry %s has %d bytes: %v", mark.name, len(pixels), err)
		}
		tile := image.NRGBA{Pix: pixels, Stride: 4 * icons.Size, Rect: image.Rect(0, 0, icons.Size, icons.Size)}
		if center, corner := tile.NRGBAAt(icons.Size/2, icons.Size/2), tile.NRGBAAt(0, 0); center != mark.ink || corner.A != 0 {
			t.Fatalf("%s center = %v, corner = %v", mark.name, center, corner)
		}
	}
	for _, icon := range []struct {
		path string
		size int
	}{
		{path: filepath.Join(root, "resources", "icon.png"), size: 512},
		{path: filepath.Join(root, "website", "assets", "icon.png"), size: 96},
	} {
		data, err := os.ReadFile(icon.path)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != icon.size || img.Bounds().Dy() != icon.size {
			t.Fatalf("%s dimensions = %v", icon.path, img.Bounds())
		}
	}
}

func TestGenerateKeepsExistingIconsWhenInputIsInvalid(t *testing.T) {
	data := iconArchive(t, archiveFile{"package/icons/one.svg", tileSVG})
	for _, test := range []struct {
		name, message string
		data          []byte
		wrongChecksum bool
	}{
		{name: "wrong checksum", message: "wrong checksum", data: data, wrongChecksum: true},
		{name: "archive missing marks", message: "source icons not found: [two zero]", data: data},
		{name: "malformed mark", message: "parse icon two", data: iconArchive(t,
			archiveFile{"package/icons/one.svg", tileSVG},
			archiveFile{"package/icons/two.svg", "<svg><path"},
		)},
		{name: "broken archive", message: "gzip", data: []byte("archive is corrupt")},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, tiles := testCatalog(t, `[{"mark":"one","ink":"124578"},{"mark":"zero","ink":"ABCDEF"},{"mark":"two","ink":"ABCDEF"}]`)
			packed := filepath.Join(tiles, "one.rgba.zlib")
			brand := filepath.Join(root, "resources", "icon.png")
			webBrand := filepath.Join(root, "website", "assets", "icon.png")
			writeFixture(t, packed, []byte("existing artwork"))
			writeFixture(t, brand, []byte("existing brand"))
			writeFixture(t, webBrand, []byte("existing website brand"))
			archive := filepath.Join(root, "icons.tgz")
			writeFixture(t, archive, test.data)
			checksum := fmt.Sprintf("%x", sha256.Sum256(test.data))
			if test.wrongChecksum {
				checksum = simpleIconsSHA256
			}
			err := generate(root, archive, checksum)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v, want %q", err, test.message)
			}
			for path, want := range map[string]string{
				packed:   "existing artwork",
				brand:    "existing brand",
				webBrand: "existing website brand",
			} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != want {
					t.Fatalf("existing %s = %q, %v", path, data, err)
				}
			}
		})
	}
}

func TestRenderCatalogRejectsInvalidInk(t *testing.T) {
	for _, ink := range []string{"1234", "red"} {
		manifest := fmt.Appendf(nil, `[{"mark":"one","ink":%q}]`, ink)
		_, err := renderCatalog(nil, manifest, fstest.MapFS{})
		if err == nil || !strings.Contains(err.Error(), "invalid icon ink for one") {
			t.Fatalf("ink %q: %v", ink, err)
		}
	}
}

func TestRenderCatalogRejectsMissingOrNestedLocalMarks(t *testing.T) {
	for _, test := range []struct{ mark, message string }{
		{"missing", "read local icon missing"},
		{"../outside", "invalid icon name"},
	} {
		manifest := fmt.Appendf(nil, `[{"mark":%q,"ink":"123456","local":true}]`, test.mark)
		_, err := renderCatalog(nil, manifest, fstest.MapFS{})
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("local mark %q: %v", test.mark, err)
		}
	}
}

type archiveFile struct{ name, content string }

func iconArchive(t *testing.T, files ...archiveFile) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for _, file := range files {
		if err := tw.WriteHeader(&tar.Header{Name: file.name, Mode: 0o644, Size: int64(len(file.content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(file.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func testCatalog(t *testing.T, manifest string) (root, tiles string) {
	t.Helper()
	root = t.TempDir()
	catalog := filepath.Join(root, "internal", "catalog")
	writeFixture(t, filepath.Join(catalog, "presets.json"), []byte(manifest))
	return root, filepath.Join(catalog, "tiles")
}

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
