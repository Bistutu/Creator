package icons

import (
	"bytes"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"strings"
	"sync"
	"unicode"

	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

const Size = 128

// Reuse PNG compression buffers when writing several sizes of one icon.
type encoderPool struct{ buffers sync.Pool }

func (p *encoderPool) Get() *png.EncoderBuffer {
	buffer, _ := p.buffers.Get().(*png.EncoderBuffer)
	return buffer
}

func (p *encoderPool) Put(buffer *png.EncoderBuffer) { p.buffers.Put(buffer) }

var pngBuffers encoderPool

// Reuse the resampling weights and scratch buffers for packaged icons.
// The set of source and output sizes is fixed even when users import artwork.
var packagedScalers = func() map[[2]int]func() draw.Scaler {
	scalers := make(map[[2]int]func() draw.Scaler, 12)
	for _, source := range []int{Size, customIconSize} {
		for _, target := range []int{16, 32, 48, 64, Size, customIconSize} {
			scalers[[2]int{source, target}] = sync.OnceValue(func() draw.Scaler {
				return draw.CatmullRom.NewScaler(target, target, source, source)
			})
		}
	}
	return scalers
}()

var monogramFont = sync.OnceValue(func() *opentype.Font {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		panic(err)
	}
	return f
})

func PNG(img image.Image) ([]byte, error) {
	var data bytes.Buffer
	err := encodePNG(&data, img)
	return data.Bytes(), err
}

func encodePNG(w io.Writer, img image.Image) error {
	encoder := png.Encoder{CompressionLevel: png.BestSpeed, BufferPool: &pngBuffers}
	return encoder.Encode(w, img)
}

func Resize(img image.Image, size int) *image.NRGBA {
	result := image.NewNRGBA(image.Rect(0, 0, size, size))
	source := img.Bounds()
	var scaler draw.Scaler = draw.CatmullRom
	if source.Dx() == source.Dy() {
		if packaged := packagedScalers[[2]int{source.Dx(), size}]; packaged != nil {
			scaler = packaged()
		}
	}
	scaler.Scale(result, result.Bounds(), img, source, draw.Src, nil)
	return result
}

// Tile gives a website icon a consistent shape while retaining its proportions.
func Tile(mark image.Image, fill color.NRGBA, proportion float64) *image.NRGBA {
	result := roundedBackground(fill, fill)
	drawCentered(result, mark, proportion)
	return result
}

func drawCentered(dst *image.NRGBA, mark image.Image, proportion float64) {
	b := mark.Bounds()
	if b.Empty() {
		return
	}
	size := dst.Bounds().Dx()
	scale := proportion * float64(size) / float64(max(b.Dx(), b.Dy()))
	w, h := max(1, int(math.Round(float64(b.Dx())*scale))), max(1, int(math.Round(float64(b.Dy())*scale)))
	x, y := (size-w)/2, (size-h)/2
	draw.CatmullRom.Scale(dst, image.Rect(x, y, x+w, y+h), mark, b, draw.Over, nil)
}

func SiteTile(mark image.Image) *image.NRGBA {
	return Tile(mark, color.NRGBA{R: 246, G: 247, B: 251, A: 255}, 0.70)
}

func Fallback(name, hexColor string) *image.NRGBA {
	result := fallbackBackground(hexColor)
	for _, char := range strings.ToUpper(name) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			if glyph, err := monogramFont().GlyphIndex(nil, char); err == nil && glyph != 0 && char < 0x0250 {
				face, _ := opentype.NewFace(monogramFont(), &opentype.FaceOptions{Size: 67, DPI: 72})
				defer face.Close()
				text := string(char)
				bounds, _ := font.BoundString(face, text)
				ink := &font.Drawer{
					Dst: result, Src: image.White, Face: face,
					Dot: fixed.Point26_6{X: fixed.I(Size/2) - (bounds.Min.X+bounds.Max.X)/2, Y: fixed.I(Size/2) - (bounds.Min.Y+bounds.Max.Y)/2},
				}
				ink.DrawString(text)
				return result
			}
			break
		}
	}
	crateMark(result)
	return result
}

func fallbackBackground(hexColor string) *image.NRGBA {
	if !profile.ValidColor(hexColor) {
		hexColor = profile.DefaultColor
	}
	components, _ := hex.DecodeString(hexColor[1:])
	base := color.NRGBA{R: components[0], G: components[1], B: components[2], A: 255}
	shade := func(value uint8) uint8 { return uint8(uint16(value) * 3 / 4) }
	return roundedBackground(base, color.NRGBA{R: shade(base.R), G: shade(base.G), B: shade(base.B), A: 255})
}

// MatchesPlaceholder recognizes the background on older installed icons. The
// top and bottom margins have no letters and survive a later app rename.
func MatchesPlaceholder(picture image.Image, hexColor string) bool {
	bounds := picture.Bounds()
	size := bounds.Dx()
	if size != bounds.Dy() || (size != Size && size != Size*2) {
		return false
	}
	expected := fallbackBackground(hexColor)
	if size != Size {
		expected = Resize(expected, size)
	}
	for _, y := range []int{size / 32, size - 1 - size/32} {
		for x := range size {
			r, g, b, a := picture.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			wantR, wantG, wantB, wantA := expected.At(x, y).RGBA()
			if r != wantR || g != wantG || b != wantB || a != wantA {
				return false
			}
		}
	}
	return true
}

func Brand() *image.NRGBA {
	result := roundedBackground(color.NRGBA{R: 112, G: 145, B: 255, A: 255}, color.NRGBA{R: 60, G: 85, B: 216, A: 255})
	crateMark(result)
	return result
}

func crateMark(dst *image.NRGBA) {
	// Three faces form the Creator mark. The open pane faces forward.
	polygon(dst, color.NRGBA{R: 218, G: 230, B: 255, A: 255}, [][2]float32{{64, 26}, {103, 47}, {64, 69}, {25, 47}})
	polygon(dst, color.NRGBA{R: 136, G: 174, B: 255, A: 255}, [][2]float32{{25, 53}, {61, 73}, {61, 105}, {25, 85}})
	polygon(dst, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, [][2]float32{{67, 73}, {103, 53}, {103, 85}, {67, 105}})
	polygon(dst, color.NRGBA{R: 71, G: 100, B: 225, A: 255}, [][2]float32{{76, 77}, {94, 67}, {94, 79}, {76, 89}})
}

func polygon(dst *image.NRGBA, fill color.NRGBA, points [][2]float32) {
	left, top := points[0][0], points[0][1]
	right, bottom := left, top
	for _, p := range points[1:] {
		left, top = min(left, p[0]), min(top, p[1])
		right, bottom = max(right, p[0]), max(bottom, p[1])
	}
	bounds := image.Rect(int(math.Floor(float64(left))), int(math.Floor(float64(top))),
		int(math.Ceil(float64(right))), int(math.Ceil(float64(bottom)))).Intersect(dst.Bounds())
	x, y := float32(bounds.Min.X), float32(bounds.Min.Y)
	path := vector.NewRasterizer(bounds.Dx(), bounds.Dy())
	path.MoveTo(points[0][0]-x, points[0][1]-y)
	for _, p := range points[1:] {
		path.LineTo(p[0]-x, p[1]-y)
	}
	path.ClosePath()
	path.Draw(dst, bounds, image.NewUniform(fill), image.Point{})
}

func roundedBackground(top, bottom color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, Size, Size))
	const radius = 29.0
	for y := range Size {
		rowMix := float64(y) / (Size - 1)
		fill := color.NRGBA{
			R: uint8(float64(top.R)*(1-rowMix) + float64(bottom.R)*rowMix),
			G: uint8(float64(top.G)*(1-rowMix) + float64(bottom.G)*rowMix),
			B: uint8(float64(top.B)*(1-rowMix) + float64(bottom.B)*rowMix),
		}
		for x := range Size {
			dx := max(radius-float64(x)-0.5, float64(x)+0.5-(Size-radius), 0)
			dy := max(radius-float64(y)-0.5, float64(y)+0.5-(Size-radius), 0)
			fill.A = uint8(255 * min(max(radius+0.5-math.Hypot(dx, dy), 0), 1))
			img.SetNRGBA(x, y, fill)
		}
	}
	return img
}
