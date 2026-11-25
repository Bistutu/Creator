package icons

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"

	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/profile"
)

const (
	customIconSize = 256
	maxImportSize  = 8 << 20
)

var errFileTooLarge = errors.New("图片文件须在 8 MB 以内")

// ImportFile prepares a square icon, retaining the image's orientation,
// proportions, and transparency. The selected file is read only for this operation.
func ImportFile(path string, svg func([]byte) (image.Image, error)) (image.Image, error) {
	data, err := fileutil.ReadRegular(path, maxImportSize)
	if errors.Is(err, fileutil.ErrTooLarge) {
		return nil, errFileTooLarge
	}
	if err != nil {
		return nil, errors.New("无法读取所选图片")
	}
	img, err := decodeIcon(data, svg, 4096)
	if errors.Is(err, errImageDimensions) {
		return nil, err
	}
	if err != nil || img == nil || img.Bounds().Empty() {
		return nil, errors.New("图片无法使用，请选择 PNG、JPEG、WebP、GIF、ICO 或 SVG")
	}
	result := image.NewNRGBA(image.Rect(0, 0, customIconSize, customIconSize))
	drawCentered(result, img, 1)
	return orientSquare(result, tiffOrientation(iconExif(data))), nil
}

// CustomStore keeps copies separate from the site favicon cache. A profile
// refers to a copy by its content hash so multiple apps can reuse the image.
type CustomStore struct{ dir string }

func NewCustomStore(dir string) *CustomStore { return &CustomStore{dir: dir} }

func (s *CustomStore) Get(key string) image.Image {
	if s == nil || !profile.ValidIconKey(key) {
		return nil
	}
	data, err := fileutil.ReadRegular(filepath.Join(s.dir, key+".png"), maxIcon)
	if err != nil {
		return nil
	}
	img, _ := decodeRaster(data, customIconSize)
	return img
}

// Put saves a copy and returns the same encoded PNG for the installed app.
func (s *CustomStore) Put(img image.Image) (string, Artwork, error) {
	if img == nil || img.Bounds().Empty() || img.Bounds().Dx() > customIconSize || img.Bounds().Dy() > customIconSize {
		return "", Artwork{}, errors.New("图标尺寸无效")
	}
	data, err := PNG(img)
	if err != nil {
		return "", Artwork{}, err
	}
	key := fmt.Sprintf("%x", sha256.Sum256(data))
	path := filepath.Join(s.dir, key+".png")
	if previous, err := fileutil.ReadRegular(path, maxIcon); err != nil || !bytes.Equal(previous, data) {
		if err := writeImageFile(path, data); err != nil {
			return "", Artwork{}, err
		}
	}
	return key, Artwork{Image: img, encodedPNG: &data}, nil
}

func (s *CustomStore) Remove(key string) error {
	if !profile.ValidIconKey(key) {
		return errors.New("图标标识无效")
	}
	err := os.Remove(filepath.Join(s.dir, key+".png"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func writeImageFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "icon-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
