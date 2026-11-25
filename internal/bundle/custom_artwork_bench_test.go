//go:build darwin || windows || linux

package bundle

import (
	"image"
	"math/rand/v2"
	"testing"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

func BenchmarkCustomArtworkApplication(b *testing.B) {
	picture := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	colors := rand.New(rand.NewPCG(28, 431))
	for i := 0; i < len(picture.Pix); i += 4 {
		pixel := colors.Uint32()
		picture.Pix[i], picture.Pix[i+1], picture.Pix[i+2], picture.Pix[i+3] = byte(pixel), byte(pixel>>8), byte(pixel>>16), 255
	}
	icon := library.Icon{Artwork: icons.Artwork{Image: picture}, Custom: true}
	draft := profile.App{Name: "Artwork fixture", URL: "https://example.org", Width: 1020, Height: 760}
	b.Run("Create", func(b *testing.B) {
		_, service := releaseFixture(b)
		b.ReportAllocs()
		for b.Loop() {
			app, err := service.Save(b.Context(), draft, icon)
			if err != nil {
				b.Fatal(err)
			}
			b.StopTimer()
			if err := service.Remove(app.ID); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
		}
	})
	b.Run("Resize", func(b *testing.B) {
		_, service := releaseFixture(b)
		app, err := service.Save(b.Context(), draft, icon)
		if err != nil {
			b.Fatal(err)
		}
		keep := library.Icon{KeepInstalled: true, Artwork: icon.Artwork}
		b.ReportAllocs()
		for b.Loop() {
			app.Width = 2100 - app.Width
			app, err = service.Save(b.Context(), app, keep)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
