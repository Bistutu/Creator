// Command assets generates the manager icon and the offline catalog.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"io"
	"io/fs"
	"log"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/Bistutu/Creator/internal/icons"
)

const simpleIconsSHA256 = "9a0aabd7b23c313aa75ab0bb05ac36d8638b2a56797040d0bb56eab9c2a53904"

func main() {
	archive := flag.String("simple-icons", "", "simple-icons-15.21.0.tgz")
	flag.Parse()
	if err := generate(".", *archive, simpleIconsSHA256); err != nil {
		log.Fatal(err)
	}
}

func generate(root, archive, checksum string) error {
	if archive == "" {
		return errors.New("supply -simple-icons with the pinned npm archive")
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != checksum {
		return errors.New("Simple Icons archive has the wrong checksum")
	}
	catalog := filepath.Join(root, "internal", "catalog")
	sitesJSON, err := os.ReadFile(filepath.Join(catalog, "presets.json"))
	if err != nil {
		return err
	}
	marks, err := renderCatalog(data, sitesJSON, os.DirFS(filepath.Join(catalog, "marks")))
	if err != nil {
		return err
	}
	assets := filepath.Join(catalog, "tiles")
	resources := filepath.Join(root, "resources")
	website := filepath.Join(root, "website", "assets")
	for _, dir := range []string{assets, resources, website} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	for _, mark := range slices.Sorted(maps.Keys(marks)) {
		if err := os.WriteFile(filepath.Join(assets, mark+".rgba.zlib"), marks[mark], 0o644); err != nil {
			return err
		}
	}
	brand := icons.Brand()
	for _, target := range []struct {
		dir  string
		size int
	}{{resources, 512}, {website, 96}} {
		png, err := icons.PNG(icons.Resize(brand, target.size))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(target.dir, "icon.png"), png, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func renderCatalog(data, sitesJSON []byte, local fs.FS) (map[string][]byte, error) {
	var sites []struct {
		Mark, Ink string
		Local     bool
	}
	if err := json.Unmarshal(sitesJSON, &sites); err != nil {
		return nil, err
	}
	pending := make(map[string]color.NRGBA, len(sites))
	rendered := make(map[string][]byte, len(sites))
	for _, site := range sites {
		if site.Mark == "" || strings.ContainsAny(site.Mark, "/\\") {
			return nil, fmt.Errorf("invalid icon name %q", site.Mark)
		}
		rgb, err := hex.DecodeString(site.Ink)
		if err != nil || len(rgb) != 3 {
			return nil, fmt.Errorf("invalid icon ink for %s", site.Mark)
		}
		ink := color.NRGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}
		if !site.Local {
			pending[site.Mark] = ink
			continue
		}
		data, err := fs.ReadFile(local, site.Mark+".svg")
		if err != nil {
			return nil, fmt.Errorf("read local icon %s: %w", site.Mark, err)
		}
		tile, err := renderMark(site.Mark, data, ink)
		if err != nil {
			return nil, err
		}
		rendered[site.Mark] = tile
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		entry, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if entry.Typeflag != tar.TypeReg || path.Dir(entry.Name) != "package/icons" || path.Ext(entry.Name) != ".svg" {
			continue
		}
		mark := strings.TrimSuffix(path.Base(entry.Name), ".svg")
		ink, ok := pending[mark]
		if !ok {
			continue
		}
		svgBytes, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		svgBytes = bytes.ReplaceAll(svgBytes, []byte("<path "), []byte("<path fill=\"currentColor\" "))
		tile, err := renderMark(mark, svgBytes, ink)
		if err != nil {
			return nil, err
		}
		rendered[mark] = tile
		delete(pending, mark)
	}
	if len(pending) != 0 {
		return nil, fmt.Errorf("source icons not found: %v", slices.Sorted(maps.Keys(pending)))
	}
	return rendered, nil
}

func renderMark(mark string, source []byte, ink color.NRGBA) ([]byte, error) {
	svg, err := ui.ParseSVG(source)
	if err != nil {
		return nil, fmt.Errorf("parse icon %s: %w", mark, err)
	}
	tile := icons.SiteTile(svg.Image(96, 96, ink))
	var data bytes.Buffer
	writer := zlib.NewWriter(&data)
	if _, err := writer.Write(tile.Pix); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}
