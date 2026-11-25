package icons

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"testing"

	"golang.org/x/image/draw"
)

func TestResizeKeepsPackagedIconPixels(t *testing.T) {
	for _, source := range []image.Point{{X: Size, Y: Size}, {X: customIconSize, Y: customIconSize}, {X: 35, Y: 50}} {
		t.Run(fmt.Sprintf("%dx%d", source.X, source.Y), func(t *testing.T) {
			t.Parallel()
			for _, shade := range []uint8{17, 143} {
				min := image.Pt(7, 11)
				original := image.NewNRGBA(image.Rectangle{Min: min, Max: min.Add(source)})
				for y := range source.Y {
					for x := range source.X {
						original.SetNRGBA(x+min.X, y+min.Y, color.NRGBA{R: shade + uint8(x), G: uint8(y), B: 13, A: uint8(x + y)})
					}
				}
				for _, size := range []int{16, 32, 48, 64, Size, customIconSize, 150} {
					want := image.NewNRGBA(image.Rect(0, 0, size, size))
					draw.CatmullRom.Scale(want, want.Bounds(), original, original.Bounds(), draw.Src, nil)
					if got := Resize(original, size); got.Bounds() != want.Bounds() || !bytes.Equal(got.Pix, want.Pix) {
						t.Fatalf("%dpx artwork changed for shade %d", size, shade)
					}
				}
			}
		})
	}
}
