package bundle

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

func BenchmarkPackagedIcon(b *testing.B) {
	tile := icons.Brand()
	custom := icons.Resize(tile, 256)
	for _, item := range []struct {
		name   string
		encode func(icons.Artwork) ([]byte, error)
		art    image.Image
	}{
		{"ICNS/Tile", icns, tile},
		{"ICNS/Custom", icns, custom},
		{"ICO/Tile", ico, tile},
		{"ICO/Custom", ico, custom},
	} {
		b.Run(item.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := item.encode(icons.Artwork{Image: item.art}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestWindowsIconContainsPNGFrames(t *testing.T) {
	data, err := ico(icons.Artwork{Image: icons.Brand()})
	if err != nil {
		t.Fatal(err)
	}
	sizes := []int{16, 32, 48, 128, 256}
	if len(data) < 6+16*len(sizes) || binary.LittleEndian.Uint16(data[2:]) != 1 || int(binary.LittleEndian.Uint16(data[4:])) != len(sizes) {
		t.Fatal("invalid ICO header")
	}
	next := 6 + 16*len(sizes)
	for i, size := range sizes {
		entry := data[6+16*i:]
		if entry[0] != byte(size) || entry[1] != byte(size) {
			t.Fatalf("incorrect dimension in %dpx ICO entry", size)
		}
		length := int(binary.LittleEndian.Uint32(entry[8:]))
		offset := int(binary.LittleEndian.Uint32(entry[12:]))
		if offset != next || length <= 0 || length > len(data)-offset {
			t.Fatalf("invalid %dpx ICO payload location", size)
		}
		next = offset + length
		checkPNGFrame(t, data[offset:next], size)
	}
	if next != len(data) {
		t.Fatal("ICO has bytes past its last frame")
	}
}

func TestMacIconContainsPNGFrames(t *testing.T) {
	data, err := icns(icons.Artwork{Image: icons.Brand()})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8 || string(data[:4]) != "icns" || int(binary.BigEndian.Uint32(data[4:])) != len(data) {
		t.Fatal("invalid ICNS header")
	}
	next := 8
	for _, frame := range []struct {
		name string
		size int
	}{{"icp4", 16}, {"icp5", 32}, {"icp6", 64}, {"ic07", 128}, {"ic08", 256}} {
		remaining := data[next:]
		if len(remaining) < 8 || string(remaining[:4]) != frame.name {
			t.Fatalf("missing ICNS frame %s", frame.name)
		}
		length := int(binary.BigEndian.Uint32(remaining[4:]))
		if length <= 8 || length > len(remaining) {
			t.Fatalf("invalid %dpx ICNS payload length", frame.size)
		}
		checkPNGFrame(t, remaining[8:length], frame.size)
		next += length
	}
	if next != len(data) {
		t.Fatal("ICNS has bytes past its last frame")
	}
}

func checkPNGFrame(t *testing.T, data []byte, size int) {
	t.Helper()
	picture, err := png.Decode(bytes.NewReader(data))
	if err != nil || picture.Bounds().Size() != image.Pt(size, size) {
		t.Fatalf("invalid %dpx PNG frame: %v", size, err)
	}
}

func TestCustomArtworkUsesTheSavedPNGInEachPackageFormat(t *testing.T) {
	store := t.TempDir()
	asset := icons.Resize(icons.Brand(), 256)
	asset.SetNRGBA(15, 30, color.NRGBA{R: 96, G: 42, B: 147, A: 90})
	key, art, err := icons.NewCustomStore(store).Put(asset)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(store, key+".png"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	resources := []iconResource{{"AppIcon.icns", icns}, {"AppIcon.ico", ico}, {"AppIcon.png", icons.Artwork.PNG}}
	_, err = writeIcons(dir, "", func() icons.Artwork { return art }, resources)
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range resources {
		data, err := os.ReadFile(filepath.Join(dir, resource.name))
		// The last frame of each platform icon is the original 256px PNG.
		if err != nil || !bytes.HasSuffix(data, want) {
			t.Fatalf("%s did not use the stored image bytes: %v", resource.name, err)
		}
	}
}

func TestWriteIconsReusesLegacyArtwork(t *testing.T) {
	old, next := t.TempDir(), t.TempDir()
	const name = "AppIcon.png"
	data, err := icons.PNG(icons.Brand())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeConfig(filepath.Join(old, ConfigName), profile.App{}, iconMetadata{IconKind: icons.WebsiteArtwork}); err != nil {
		t.Fatal(err)
	}
	encode := func(icons.Artwork) ([]byte, error) {
		t.Fatal("an intact legacy icon was encoded again")
		return nil, nil
	}
	resources := []iconResource{{name, encode}}
	meta, err := writeIcons(next, old, nil, resources)
	if err != nil || meta.IconKind != icons.WebsiteArtwork || meta.IconDigest == "" {
		t.Fatalf("saving the legacy icon produced %+v (%v)", meta, err)
	}
	if saved, err := os.ReadFile(filepath.Join(next, name)); err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("saving changed the legacy file: %v", err)
	}
}

func TestWriteIconsRestoresDamagedFormatFromItsInstalledSibling(t *testing.T) {
	resources := []iconResource{{"AppIcon.ico", ico}, {"AppIcon.png", icons.Artwork.PNG}}
	for i, broken := range resources {
		t.Run(broken.name, func(t *testing.T) {
			old, next := t.TempDir(), t.TempDir()
			ink := color.NRGBA{R: 22, G: 148, B: 100, A: 255}
			picture := image.NewNRGBA(image.Rect(0, 0, 1, 1))
			picture.SetNRGBA(0, 0, ink)
			art := icons.Artwork{Image: picture, Kind: icons.WebsiteArtwork}
			meta, err := writeIcons(old, "", func() icons.Artwork { return art }, resources)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeConfig(filepath.Join(old, ConfigName), profile.App{}, meta); err != nil {
				t.Fatal(err)
			}
			if meta.IconDigest == "" || readArtwork(old, resources).validatedDigest() != meta.IconDigest {
				t.Fatal("the captured icon files did not match the installed digest")
			}
			asset := filepath.Join(old, broken.name)
			data, err := os.ReadFile(asset)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(asset, data[:9], 0o644); err != nil {
				t.Fatal(err)
			}
			if readArtwork(old, resources).validatedDigest() != "" {
				t.Fatal("the installed digest accepted a damaged file")
			}
			sibling := resources[1-i].name
			original, err := os.ReadFile(filepath.Join(old, sibling))
			if err != nil {
				t.Fatal(err)
			}

			fallback := func() icons.Artwork {
				t.Fatal("called the fallback despite an installed image being usable")
				return icons.Artwork{}
			}
			meta, err = writeIcons(next, old, fallback, resources)
			if err != nil || meta.IconKind != icons.WebsiteArtwork {
				t.Fatalf("repair lost the source of the artwork: %+v, %v", meta, err)
			}
			if meta.IconDigest == "" || readIconFiles(next, resources).validatedDigest() != meta.IconDigest {
				t.Fatal("the repaired files did not match their digest")
			}
			if data, err := os.ReadFile(filepath.Join(next, sibling)); err != nil || !bytes.Equal(data, original) {
				t.Fatalf("repair rewrote the usable artwork: %v", err)
			}
			repaired := icons.ReadInstalledIcon(filepath.Join(next, broken.name))
			if repaired == nil || color.NRGBAModel.Convert(repaired.At(repaired.Bounds().Dx()/2, repaired.Bounds().Dy()/2)) != ink {
				t.Fatal("repair did not use the sibling artwork")
			}
		})
	}
}
