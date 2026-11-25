package icons

import (
	"encoding/binary"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestReadInstalledIconFormats(t *testing.T) {
	red := color.NRGBA{R: 211, G: 71, B: 83, A: 255}
	green := color.NRGBA{R: 28, G: 151, B: 103, A: 255}
	pngPixel := func(ink color.NRGBA) []byte {
		t.Helper()
		img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		img.SetNRGBA(0, 0, ink)
		data, err := PNG(img)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	frame := func(tag string, data []byte) []byte {
		return append(binary.BigEndian.AppendUint32([]byte(tag), uint32(8+len(data))), data...)
	}
	icns := func(frames ...[]byte) []byte {
		data := append([]byte("icns"), 0, 0, 0, 0)
		for _, frame := range frames {
			data = append(data, frame...)
		}
		binary.BigEndian.PutUint32(data[4:], uint32(len(data)))
		return data
	}
	redPNG, greenPNG := pngPixel(red), pngPixel(green)
	preferred, alternate := frame("ic07", greenPNG), frame("ic08", redPNG)
	for _, fixture := range []struct {
		name string
		data []byte
		ink  color.NRGBA
	}{
		{"AppIcon.png", greenPNG, green},
		{"AppIcon.ico", icoFrame(1, 1, greenPNG), green},
		{"AppIcon.icns", icns(frame("info", []byte("metadata")), alternate, preferred), green},
		{"alternate.icns", icns(frame("ic07", greenPNG[:8]), alternate), red},
	} {
		path := filepath.Join(t.TempDir(), fixture.name)
		if err := os.WriteFile(path, fixture.data, 0600); err != nil {
			t.Fatal(err)
		}
		icon := ReadInstalledIcon(path)
		if icon == nil || color.NRGBAModel.Convert(icon.At(0, 0)) != fixture.ink {
			t.Fatalf("%s did not decode its installed artwork", fixture.name)
		}
	}
	for _, data := range [][]byte{
		[]byte("icns"), icns(preferred[:7]), icns(append([]byte("ic07"), 0, 0, 0, 7)),
		icns(frame("ic07", []byte("not a PNG"))), []byte("not an image"),
	} {
		path := filepath.Join(t.TempDir(), "damaged.icns")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if ReadInstalledIcon(path) != nil {
			t.Fatalf("decoded a damaged %d-byte icon", len(data))
		}
	}
	if ReadInstalledIcon(filepath.Join(t.TempDir(), "missing.png")) != nil {
		t.Fatal("a missing icon was reported as installed")
	}
}

func TestMatchesLegacyPlaceholderAtInstalledSizes(t *testing.T) {
	for _, size := range []int{Size, 2 * Size} {
		for _, name := range []string{"Work Notes", "工作台"} {
			const background = "#8564b7"
			picture := Resize(Fallback(name, background), size)
			if !MatchesPlaceholder(picture, background) || MatchesPlaceholder(picture, "#356f97") {
				t.Fatalf("%dpx artwork for %q did not match its original background", size, name)
			}
		}
		if MatchesPlaceholder(Resize(SiteTile(Brand()), size), "") {
			t.Fatalf("%dpx website art was mistaken for a placeholder", size)
		}
	}
	if MatchesPlaceholder(image.NewNRGBA(image.Rect(0, 0, Size, 2*Size)), "") {
		t.Fatal("an unrelated image size was accepted")
	}
}
