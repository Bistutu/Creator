package catalog

import (
	"bytes"
	"compress/zlib"
	"embed"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"io/fs"
	"strings"
	"sync"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/search"
)

//go:embed presets.json
var metadata []byte

//go:embed tiles/*.rgba.zlib
var artwork embed.FS

var tileReaders sync.Pool

type Preset struct {
	Name       string
	URL        string
	Category   string
	Mark       string
	Keywords   string
	host       string
	searchText string
	icon       func() (image.Image, error)
}

func (p Preset) App() profile.App {
	return profile.App{Name: p.Name, URL: p.URL, Category: p.Category}
}

// Icon decodes the embedded artwork on demand and shares it with callers.
func (p Preset) Icon() (image.Image, error) { return p.icon() }

func (p Preset) Host() string { return p.host }

func Load() ([]Preset, error) {
	return loadCatalog(metadata, artwork)
}

func loadCatalog(data []byte, assets fs.FS) ([]Preset, error) {
	var sites []Preset
	if err := json.Unmarshal(data, &sites); err != nil {
		return nil, err
	}
	for i, site := range sites {
		sites[i].host = profile.Host(site.URL)
		sites[i].searchText = strings.ToLower(site.Name + " " + site.URL + " " + site.Keywords)
		sites[i].icon = sync.OnceValues(func() (image.Image, error) {
			picture, err := readTile(assets, site.Mark)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", site.Name, err)
			}
			return picture, nil
		})
	}
	return sites, nil
}

// Each resource contains the straight RGBA pixels of one square site tile.
func readTile(assets fs.FS, mark string) (*image.NRGBA, error) {
	compressed, err := fs.ReadFile(assets, "tiles/"+mark+".rgba.zlib")
	if err != nil {
		return nil, err
	}
	input := bytes.NewReader(compressed)
	reader, err := resetTileReader(input)
	if err != nil {
		return nil, err
	}
	defer func() {
		if reader.Close() == nil {
			input.Reset(nil)
			tileReaders.Put(reader)
		}
	}()
	picture := image.NewNRGBA(image.Rect(0, 0, icons.Size, icons.Size))
	if _, err := io.ReadFull(reader, picture.Pix); err != nil {
		return nil, err
	}
	// Consume EOF to verify the checksum after reading the exact pixel count.
	var extra [1]byte
	if _, err := io.ReadFull(reader, extra[:]); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("tile contains more than %d bytes", len(picture.Pix))
		}
		return nil, err
	}
	return picture, nil
}

func resetTileReader(input *bytes.Reader) (io.ReadCloser, error) {
	reader, _ := tileReaders.Get().(io.ReadCloser)
	if reader == nil {
		return zlib.NewReader(input)
	}
	return reader, reader.(zlib.Resetter).Reset(input, nil)
}

func ByURL(sites []Preset, raw string) (Preset, bool) {
	host := profile.Host(raw)
	for _, site := range sites {
		if strings.EqualFold(host, site.host) {
			return site, true
		}
	}
	return Preset{}, false
}

func Filter(sites []Preset, category, query string) []Preset {
	query = strings.TrimSpace(query)
	if category == "" && query == "" {
		return sites
	}
	return search.Filter(sites, query, func(site Preset) string {
		if category == "" || site.Category == category {
			return site.searchText
		}
		return ""
	})
}
