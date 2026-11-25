package catalog

import (
	"bytes"
	"compress/zlib"
	"errors"
	"image"
	"io"
	"io/fs"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestAllRecommendationsHaveOfflineArtwork(t *testing.T) {
	sites, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) < 24 {
		t.Fatalf("only %d recommendations", len(sites))
	}
	seen := map[string]bool{}
	for _, site := range sites {
		t.Run(site.Name, func(t *testing.T) {
			if seen[site.Mark] {
				t.Fatalf("duplicate mark %q", site.Mark)
			}
			seen[site.Mark] = true
			if _, err := profile.Normalize(site.App()); err != nil {
				t.Fatal(err)
			}
			icon, err := site.Icon()
			if err != nil {
				t.Fatal(err)
			}
			if b := icon.Bounds(); b.Dx() != icons.Size || b.Dy() != icons.Size {
				t.Fatalf("icon size = %v", b)
			}
			base := icon.At(icons.Size/3, 12)
			ink := 0
			for x := 24; x < 104; x++ {
				for y := 24; y < 104; y++ {
					if icon.At(x, y) != base {
						ink++
					}
				}
			}
			if ink < 250 {
				t.Fatalf("icon looks empty (%d marked pixels)", ink)
			}
		})
	}
	if site, ok := ByURL(sites, "https://www.github.com/logout"); !ok || site.Name != "GitHub" {
		t.Fatalf("alias lookup = %+v, %v", site, ok)
	}
	if _, ok := ByURL(sites, "https://github.com.example.test"); ok {
		t.Fatal("matched a different host")
	}
	if hits := Filter(sites, "影音", " B站 "); len(hits) != 1 || hits[0].Mark != "bilibili" {
		t.Fatalf("filter = %+v", hits)
	}
	if hits := Filter(sites, "AI", "youtube"); len(hits) != 0 {
		t.Fatalf("unexpected results: %+v", hits)
	}
}

func TestCatalogSearchCombinesTermsWithinTheCategory(t *testing.T) {
	sites, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		category, query string
		marks           []string
	}{
		{"AI", "编程 CLAUDE", []string{"claude"}},
		{"社交", "小红书 攻略", []string{"xiaohongshu"}},
		{"社交", "攻略　Rednote", []string{"xiaohongshu"}},
		{"AI", "攻略　Rednote", nil},
		{"AI", "编程 写作", []string{"openai", "claude"}},
	} {
		t.Run(test.category+"/"+test.query, func(t *testing.T) {
			var marks []string
			for _, site := range Filter(sites, test.category, test.query) {
				marks = append(marks, site.Mark)
			}
			if !slices.Equal(marks, test.marks) {
				t.Fatalf("filtered recommendations = %v, want %v", marks, test.marks)
			}
		})
	}
}

type countedIcons struct {
	fs.FS
	opens atomic.Int32
}

func (f *countedIcons) Open(path string) (fs.File, error) {
	f.opens.Add(1)
	return f.FS.Open(path)
}

func TestCatalogLoadsOnlyRequestedArtwork(t *testing.T) {
	valid, err := artwork.ReadFile("tiles/deepseek.rgba.zlib")
	if err != nil {
		t.Fatal(err)
	}
	assets := &countedIcons{FS: fstest.MapFS{
		"tiles/deepseek.rgba.zlib": {Data: valid},
		"tiles/broken.rgba.zlib":   {Data: []byte("invalid compressed data")},
	}}
	data := []byte(`[
		{"name":"DeepSeek","url":"https://chat.deepseek.com","category":"AI","mark":"deepseek"},
		{"name":"Broken","url":"https://example.test","category":"开发","mark":"broken"}
	]`)
	sites, err := loadCatalog(data, assets)
	if err != nil {
		t.Fatal(err)
	}
	all := Filter(sites, "", " ")
	if len(all) != 2 || all[0].Mark != "deepseek" || all[1].Mark != "broken" {
		t.Fatalf("the default category should preserve the catalog order: %v", all)
	}
	matches := Filter(sites, "AI", "deepseek")
	if len(matches) != 1 || assets.opens.Load() != 0 {
		t.Fatalf("loading and searching metadata opened %d icons for %v", assets.opens.Load(), matches)
	}
	var wg sync.WaitGroup
	var pictures [4]image.Image
	for i := range pictures {
		wg.Go(func() {
			picture, err := matches[0].Icon()
			if err != nil {
				t.Error(err)
			}
			pictures[i] = picture
		})
	}
	wg.Wait()
	for _, picture := range pictures {
		if picture == nil || picture != pictures[0] {
			t.Fatal("the artwork was not shared by simultaneous callers")
		}
	}
	if opens := assets.opens.Load(); opens != 1 {
		t.Fatalf("requesting one icon opened %d files", opens)
	}
	if _, err := sites[1].Icon(); err == nil {
		t.Fatal("decoding the damaged asset succeeded")
	}
	if picture, err := sites[0].Icon(); err != nil || picture != pictures[0] || assets.opens.Load() != 2 {
		t.Fatalf("the damaged asset affected other artwork: %v, %d reads", err, assets.opens.Load())
	}
}

func TestCatalogChecksIndividualTileChecksumAndSize(t *testing.T) {
	const tileSize = 4 * icons.Size * icons.Size
	encode := func(size int) []byte {
		t.Helper()
		var data bytes.Buffer
		writer := zlib.NewWriter(&data)
		if _, err := writer.Write(make([]byte, size)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return data.Bytes()
	}
	intact := encode(tileSize)
	badChecksum := bytes.Clone(intact)
	badChecksum[len(badChecksum)-1] ^= 1
	files := fstest.MapFS{
		"tiles/intact.rgba.zlib":       {Data: intact},
		"tiles/bad-checksum.rgba.zlib": {Data: badChecksum},
		"tiles/oversized.rgba.zlib":    {Data: encode(tileSize + 1)},
		"tiles/short.rgba.zlib":        {Data: encode(tileSize - 1)},
	}
	if _, err := readTile(files, "bad-checksum"); !errors.Is(err, zlib.ErrChecksum) {
		t.Fatalf("read tile with a bad checksum: %v", err)
	}
	if _, err := readTile(files, "oversized"); err == nil {
		t.Fatal("accepted extra bytes after the tile")
	}
	if _, err := readTile(files, "short"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read a tile with missing pixels: %v", err)
	}
	if tile, err := readTile(files, "intact"); err != nil || !bytes.Equal(tile.Pix, make([]byte, tileSize)) {
		t.Fatalf("the failed reads affected the intact tile: %v", err)
	}
}

func TestCatalogCanSearchWithoutArtwork(t *testing.T) {
	sites, err := loadCatalog(metadata, fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	matched := Filter(sites, "AI", " DEEPSEEK ")
	byURL, ok := ByURL(sites, "https://chat.deepseek.com/chat#recent")
	if len(matched) != 1 || !ok || matched[0].Mark != byURL.Mark || byURL.Host() != "chat.deepseek.com" {
		t.Fatalf("metadata search with damaged artwork matched %v and %v (%v)", matched, byURL, ok)
	}
	if _, err := byURL.Icon(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("icon error = %v, want missing artwork", err)
	}
}

func TestCatalogRejectsMalformedMetadataWithoutReadingArtwork(t *testing.T) {
	assets := &countedIcons{FS: artwork}
	if sites, err := loadCatalog([]byte("{"), assets); err == nil || sites != nil || assets.opens.Load() != 0 {
		t.Fatalf("malformed metadata loaded %d sites and opened %d icons: %v", len(sites), assets.opens.Load(), err)
	}
}
