package icons

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportFileKeepsProportionsAndCopiesIcon(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "wide picture.png")
	picture := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	ink := color.NRGBA{R: 42, G: 139, B: 170, A: 255}
	draw.Draw(picture, picture.Bounds(), image.NewUniform(ink), image.Point{}, draw.Src)
	saveImportFixture(t, source, picture)
	icon, err := ImportFile(source, nil)
	if err != nil || icon.Bounds() != image.Rect(0, 0, 256, 256) {
		t.Fatalf("import icon: %v", err)
	}
	if _, _, _, alpha := icon.At(128, 45).RGBA(); alpha != 0 {
		t.Fatal("rectangular picture was stretched instead of centered")
	}
	if icon.At(128, 128) != ink {
		t.Fatalf("import changed the image color: %v", icon.At(128, 128))
	}
	storeDir := filepath.Join(root, "CustomIcons")
	store := NewCustomStore(storeDir)
	key, prepared, err := store.Put(icon)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(storeDir, key+".png"))
	if err != nil || prepared.encodedPNG == nil || !bytes.Equal(*prepared.encodedPNG, data) {
		t.Fatalf("the package and store did not receive the same encoded PNG: %v", err)
	}
	writes := 0
	err = prepared.WritePNGAt(pngTestWriter(func(frame []byte) (int, error) {
		writes++
		if len(frame) != len(data) || &frame[0] != &(*prepared.encodedPNG)[0] {
			t.Fatal("the frame writer received different bytes from the store")
		}
		return len(frame), nil
	}), 256)
	if err != nil || writes != 1 {
		t.Fatalf("a 256px system icon frame did not reuse the stored PNG: %v", err)
	}
	if same, _, err := store.Put(icon); same != key || err != nil {
		t.Fatalf("same image received a different reference: %q, %v", same, err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	reopened := NewCustomStore(storeDir)
	if persisted := reopened.Get(key); persisted == nil || color.NRGBAModel.Convert(persisted.At(128, 128)) != ink {
		t.Fatal("the stored copy depends on the selected picture")
	}
	if reopened.Get("../wide picture") != nil || reopened.Get(strings.Repeat("z", 64)) != nil {
		t.Fatal("an invalid reference was accepted")
	}
	if err := reopened.Remove(key); err != nil || reopened.Get(key) != nil {
		t.Fatalf("removed picture remains readable: %v", err)
	}
	if err := reopened.Remove(key); err != nil {
		t.Fatalf("repeating removal should succeed: %v", err)
	}
}

type pngTestWriter func([]byte) (int, error)

func (w pngTestWriter) Write(data []byte) (int, error) { return w(data) }

func TestImportRejectsUnreadableAndOversizedFiles(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad.png")
	if err := os.WriteFile(bad, []byte("not a picture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, bad, filepath.Join(root, "missing.png")} {
		if img, err := ImportFile(path, nil); img != nil || err == nil {
			t.Fatalf("invalid image %q: %v, %v", path, img, err)
		}
	}
	wide := filepath.Join(root, "wide.png")
	saveImportFixture(t, wide, image.NewNRGBA(image.Rect(0, 0, 4097, 1)))
	if _, err := ImportFile(wide, nil); !errors.Is(err, errImageDimensions) {
		t.Fatalf("oversized dimensions: %v", err)
	}
	if err := os.Truncate(bad, maxImportSize+1); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportFile(bad, nil); !errors.Is(err, errFileTooLarge) {
		t.Fatalf("oversized file: %v", err)
	}
}

func saveImportFixture(t *testing.T, path string, picture image.Image) {
	t.Helper()
	data, err := PNG(picture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
