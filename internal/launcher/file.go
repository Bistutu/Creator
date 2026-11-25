package launcher

import (
	"errors"
	"os"
	"path/filepath"
)

// replace writes the complete entry on the destination volume before it becomes visible.
func replace(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".webcrate-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	_, err = f.Write(data)
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
