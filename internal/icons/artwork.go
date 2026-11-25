package icons

import (
	"image"
	"io"
)

type ArtworkKind string

const (
	PlaceholderArtwork ArtworkKind = "placeholder"
	WebsiteArtwork     ArtworkKind = "website"
)

// Artwork describes the pixels and how Creator chose them. An empty kind
// covers custom artwork and bundles written before the kind was recorded.
type Artwork struct {
	Image image.Image
	Kind  ArtworkKind
	// CustomStore keeps the original PNG available to the package writer.
	// The image and encoded bytes are read-only after that snapshot is made.
	encodedPNG *[]byte
}

func (a Artwork) PNG() ([]byte, error) {
	if a.encodedPNG != nil {
		return *a.encodedPNG, nil
	}
	return PNG(a.Image)
}

// WritePNGAt writes a frame directly to the system icon being assembled.
func (a Artwork) WritePNGAt(w io.Writer, size int) error {
	if img, ok := a.Image.(*image.NRGBA); ok && img.Bounds().Size() == image.Pt(size, size) {
		if a.encodedPNG != nil {
			_, err := w.Write(*a.encodedPNG)
			return err
		}
		return encodePNG(w, img)
	}
	return encodePNG(w, Resize(a.Image, size))
}
