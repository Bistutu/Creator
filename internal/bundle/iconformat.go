package bundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/Bistutu/Creator/internal/icons"
)

type iconResource struct {
	name   string
	encode func(icons.Artwork) ([]byte, error)
}

type artworkFiles struct {
	data [][]byte
	meta iconMetadata
}

func readArtwork(dir string, resources []iconResource) artworkFiles {
	files := readIconFiles(dir, resources)
	if dir == "" {
		return files
	}
	config, _ := readConfig(filepath.Join(dir, ConfigName))
	files.meta = config.iconMetadata
	return files
}

func readIconFiles(dir string, resources []iconResource) artworkFiles {
	files := artworkFiles{data: make([][]byte, len(resources))}
	if dir != "" {
		for i, resource := range resources {
			files.data[i] = icons.ReadInstalledIconData(filepath.Join(dir, resource.name))
		}
	}
	return files
}

func (files artworkFiles) decode() icons.Artwork {
	for _, data := range files.data {
		if picture := icons.DecodeInstalledIcon(data); picture != nil {
			return icons.Artwork{Image: picture, Kind: files.meta.IconKind}
		}
	}
	return icons.Artwork{}
}

// The digest covers the exact bytes of every icon, in platform resource order.
func (files artworkFiles) digest() string {
	hash := sha256.New()
	var length [8]byte
	for _, data := range files.data {
		if len(data) == 0 {
			return ""
		}
		binary.BigEndian.PutUint64(length[:], uint64(len(data)))
		hash.Write(length[:])
		hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// Old bundles and changed files need pixel validation before they can be reused.
func (files artworkFiles) validatedDigest() string {
	digest := files.digest()
	if digest == "" || digest == files.meta.IconDigest {
		return digest
	}
	for _, data := range files.data {
		if icons.DecodeInstalledIcon(data) == nil {
			return ""
		}
	}
	return digest
}

// Preserve usable files verbatim. If one of several formats is damaged,
// restore it from an installed sibling before using the local fallback.
func writeIcons(dir, previous string, fallback func() icons.Artwork, resources []iconResource) (iconMetadata, error) {
	files := readArtwork(previous, resources)
	if digest := files.digest(); digest == "" || digest != files.meta.IconDigest {
		if err := files.prepare(fallback, resources); err != nil {
			return iconMetadata{}, err
		}
		files.meta.IconDigest = files.digest()
	}
	for i, resource := range resources {
		if err := os.WriteFile(filepath.Join(dir, resource.name), files.data[i], 0o644); err != nil {
			return iconMetadata{}, err
		}
	}
	return files.meta, nil
}

func (files *artworkFiles) prepare(fallback func() icons.Artwork, resources []iconResource) error {
	var art icons.Artwork
	for i, data := range files.data {
		picture := icons.DecodeInstalledIcon(data)
		if picture == nil {
			files.data[i] = nil
		} else if art.Image == nil {
			art = icons.Artwork{Image: picture, Kind: files.meta.IconKind}
		}
	}
	if art.Image == nil && fallback != nil {
		art = fallback()
	}
	if art.Image == nil {
		return errors.New("没有可用的应用图标")
	}
	for i, resource := range resources {
		if files.data[i] == nil {
			data, err := resource.encode(art)
			if err != nil {
				return err
			}
			files.data[i] = data
		}
	}
	files.meta.IconKind = art.Kind
	return nil
}

func checkIconFiles(dir string, resources []iconResource) error {
	for _, resource := range resources {
		info, err := requiredFile(filepath.Join(dir, resource.name), false)
		if err != nil {
			return err
		}
		if info.Size() > icons.MaxInstalledIconBytes {
			return os.ErrNotExist
		}
	}
	return nil
}

func icns(art icons.Artwork) ([]byte, error) {
	var file bytes.Buffer
	file.WriteString("icns\x00\x00\x00\x00")
	for _, entry := range []struct {
		name string
		size int
	}{{"icp4", 16}, {"icp5", 32}, {"icp6", 64}, {"ic07", 128}, {"ic08", 256}} {
		start := file.Len()
		file.WriteString(entry.name)
		file.WriteString("\x00\x00\x00\x00")
		if err := art.WritePNGAt(&file, entry.size); err != nil {
			return nil, err
		}
		binary.BigEndian.PutUint32(file.Bytes()[start+4:], uint32(file.Len()-start))
	}
	binary.BigEndian.PutUint32(file.Bytes()[4:], uint32(file.Len()))
	return file.Bytes(), nil
}

// Windows Vista and later accept PNG images inside ICO files.
func ico(art icons.Artwork) ([]byte, error) {
	sizes := [...]int{16, 32, 48, 128, 256}
	var header [6 + 16*len(sizes)]byte
	binary.LittleEndian.PutUint16(header[2:], 1)
	binary.LittleEndian.PutUint16(header[4:], uint16(len(sizes)))
	var file bytes.Buffer
	file.Write(header[:])
	for i, size := range sizes {
		start := file.Len()
		if err := art.WritePNGAt(&file, size); err != nil {
			return nil, err
		}
		entry := file.Bytes()[6+16*i:]
		entry[0], entry[1] = byte(size), byte(size) // 0 represents 256 pixels.
		binary.LittleEndian.PutUint16(entry[4:], 1)
		binary.LittleEndian.PutUint16(entry[6:], 32)
		binary.LittleEndian.PutUint32(entry[8:], uint32(file.Len()-start))
		binary.LittleEndian.PutUint32(entry[12:], uint32(start))
	}
	return file.Bytes(), nil
}
