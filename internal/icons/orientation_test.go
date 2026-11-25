package icons

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestImportFileHonorsExifOrientation(t *testing.T) {
	picture := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	for _, block := range []struct {
		area image.Rectangle
		ink  color.NRGBA
	}{
		{image.Rect(0, 0, 32, 16), color.NRGBA{R: 232, G: 59, B: 41, A: 255}},
		{image.Rect(32, 0, 64, 16), color.NRGBA{R: 34, G: 173, B: 100, A: 255}},
		{image.Rect(0, 16, 32, 32), color.NRGBA{R: 38, G: 118, B: 174, A: 255}},
		{image.Rect(32, 16, 64, 32), color.NRGBA{R: 244, G: 187, B: 26, A: 112}},
	} {
		draw.Draw(picture, block.area, image.NewUniform(block.ink), image.Point{}, draw.Src)
	}
	var jpg, pngImage bytes.Buffer
	if err := jpeg.Encode(&jpg, picture, &jpeg.Options{Quality: 96}); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&pngImage, picture); err != nil {
		t.Fatal(err)
	}
	for _, format := range []struct {
		name     string
		data     []byte
		withExif func([]byte, []byte) []byte
	}{
		{"jpeg", jpg.Bytes(), jpegWithExif},
		{"png", pngImage.Bytes(), pngWithExif},
	} {
		t.Run(format.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "photo."+format.name)
			base := importPhotoFixture(t, path, format.data)
			for i, swatches := range [][4]int{
				{0, 1, 2, 3}, {1, 0, 3, 2}, {3, 2, 1, 0}, {2, 3, 0, 1},
				{0, 2, 1, 3}, {2, 0, 3, 1}, {3, 1, 2, 0}, {1, 3, 0, 2},
			} {
				var order binary.ByteOrder = binary.LittleEndian
				if i%2 == 0 {
					order = binary.BigEndian
				}
				orientation := i + 1
				icon := importPhotoFixture(t, path, format.withExif(format.data, exifFixture(order, uint16(orientation))))
				source := [...]image.Point{{64, 96}, {192, 96}, {64, 160}, {192, 160}}
				dest, padding := source, image.Pt(128, 32)
				if orientation >= 5 {
					dest = [...]image.Point{{96, 64}, {160, 64}, {96, 192}, {160, 192}}
					padding = image.Pt(32, 128)
				}
				for slot, pos := range dest {
					original := source[swatches[slot]]
					if got, want := icon.At(pos.X, pos.Y), base.At(original.X, original.Y); got != want {
						t.Errorf("orientation %d at %v = %v, want %v", orientation, pos, got, want)
					}
				}
				if _, _, _, alpha := icon.At(padding.X, padding.Y).RGBA(); alpha != 0 {
					t.Errorf("orientation %d moved the image over the transparent padding", orientation)
				}
			}
		})
	}
}

func TestImportFileReadsWebPExif(t *testing.T) {
	// A lossless 64×32 single-color image. EXIF follows a padded odd-length chunk.
	imageChunk := "VP8L\x0c\x00\x00\x00\x2f\x3f\xc0\x07\x00\xa8\x5d\x4d\xea\xda\xff\x00"
	for _, header := range []string{"", "Exif\x00\x00"} {
		payload := append([]byte(header), exifFixture(binary.LittleEndian, 6)...)
		body := []byte("WEBPVP8X\x0a\x00\x00\x00\x08\x00\x00\x00\x3f\x00\x00\x1f\x00\x00" + imageChunk + "JUNK\x01\x00\x00\x00\x00\x00EXIF")
		body = binary.LittleEndian.AppendUint32(body, uint32(len(payload)))
		body = append(body, payload...)
		file := binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body)))
		icon := importPhotoFixture(t, filepath.Join(t.TempDir(), "photo.webp"), append(file, body...))
		if got := icon.At(128, 32); got != (color.NRGBA{R: 38, G: 118, B: 174, A: 255}) {
			t.Errorf("oriented WebP with EXIF prefix %q has %v at the top, want blue", header, got)
		}
		if _, _, _, alpha := icon.At(32, 128).RGBA(); alpha != 0 {
			t.Errorf("oriented WebP with EXIF prefix %q lost the side padding", header)
		}
	}
}

func TestImportFileIgnoresDamagedExif(t *testing.T) {
	picture := image.NewNRGBA(image.Rect(0, 0, 128, 64))
	draw.Draw(picture, picture.Bounds(), image.NewUniform(color.NRGBA{A: 255, R: 255}), image.Point{}, draw.Src)
	var raw bytes.Buffer
	if err := png.Encode(&raw, picture); err != nil {
		t.Fatal(err)
	}
	valid := exifFixture(binary.LittleEndian, 6)
	malformed := [][]byte{valid[:5], valid[:17], exifFixture(binary.BigEndian, 9)}
	for _, change := range []struct {
		index int
		value byte
	}{
		{0, 'M'}, {2, 23}, {4, 0}, {7, 255}, {9, 255}, {12, 4}, {14, 2},
	} {
		data := bytes.Clone(valid)
		data[change.index] = change.value
		malformed = append(malformed, data)
	}
	path := filepath.Join(t.TempDir(), "photo.png")
	for i, exif := range malformed {
		icon := importPhotoFixture(t, path, pngWithExif(raw.Bytes(), exif))
		if got := icon.At(32, 128); got != (color.NRGBA{A: 255, R: 255}) {
			t.Errorf("damaged EXIF case %d changed the image: %v", i, got)
		}
	}
}

func TestIconExifStopsAtContainerBounds(t *testing.T) {
	exif := exifFixture(binary.LittleEndian, 6)
	const pngHeader = "\x89PNG\r\n\x1a\n"
	const jpegHeader = "\xff\xd8"
	const fakeIEND = "\x00\x00\x00\x00IEND\x00\x00\x00\x00"
	for _, test := range []struct{ name, raw string }{
		{"non-Exif format", "GIF89a"},
		{"short JPEG marker", jpegHeader + "\xff\xe1\x00"},
		{"short JPEG segment", jpegHeader + "\xff\xe1\x00\x26Exif\x00\x00"},
		{"zero JPEG length", jpegHeader + "\xff\xe1\x00\x00"},
		{"start of JPEG scan", jpegHeader + "\xff\xda" + string(jpegWithExif([]byte(jpegHeader), exif)[2:])},
		{"short PNG chunk", pngHeader + "\xff\xff\xff\xffeXIf\x00\x00\x00\x00"},
		{"after PNG end", pngHeader + fakeIEND + "\x00\x00\x00\x1aeXIf" + string(exif) + "\x00\x00\x00\x00"},
		{"short WebP container", "RIFF\x10\x00\x00\x00WEBP"},
		{"invalid WebP length", "RIFF\x03\x00\x00\x00WEBP"},
		{"missing WebP padding", "RIFF\x0d\x00\x00\x00WEBPEXIF\x01\x00\x00\x00I"},
		{"WebP without EXIF", "RIFF\x04\x00\x00\x00WEBP"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if payload := iconExif([]byte(test.raw)); payload != nil {
				t.Errorf("malformed container returned %x", payload)
			}
		})
	}
	// Fill, standalone, and unrelated application markers precede APP1.
	jpegPrefix := jpegHeader + "\xff\xff\x01\xff\xd0\xff\xe0\x00\x04JF"
	jpeg := append([]byte(jpegPrefix), jpegWithExif([]byte(jpegHeader), exif)[2:]...)
	if !bytes.Equal(iconExif(jpeg), exif) {
		t.Error("JPEG marker traversal missed APP1")
	}
}

func importPhotoFixture(t *testing.T, path string, data []byte) image.Image {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	icon, err := ImportFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return icon
}

func exifFixture(order binary.ByteOrder, orientation uint16) []byte {
	data := make([]byte, 26)
	if order == binary.BigEndian {
		copy(data, "MM")
	} else {
		copy(data, "II")
	}
	order.PutUint16(data[2:], 42)
	order.PutUint32(data[4:], 8)
	order.PutUint16(data[8:], 1)
	order.PutUint16(data[10:], 0x0112)
	order.PutUint16(data[12:], 3)
	order.PutUint32(data[14:], 1)
	order.PutUint16(data[18:], orientation)
	return data
}

func jpegWithExif(jpg, exif []byte) []byte {
	const header = "Exif\x00\x00"
	data := binary.BigEndian.AppendUint16([]byte("\xff\xd8\xff\xe1"), uint16(2+len(header)+len(exif)))
	data = append(data, header...)
	data = append(data, exif...)
	return append(data, jpg[2:]...)
}

func pngWithExif(pngImage, exif []byte) []byte {
	const endIHDR = 33
	data := binary.BigEndian.AppendUint32(bytes.Clone(pngImage[:endIHDR]), uint32(len(exif)))
	start := len(data)
	data = append(data, "eXIf"...)
	data = append(data, exif...)
	data = binary.BigEndian.AppendUint32(data, crc32.ChecksumIEEE(data[start:]))
	return append(data, pngImage[endIHDR:]...)
}
