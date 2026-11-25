package fileutil

import (
	"errors"
	"os"
	"path/filepath"
)

// WriteAtomic replaces path with new data from a temporary sibling.
func WriteAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".setting-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	// Root.Rename can also replace a file when a Windows reader shares deletion.
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Rename(filepath.Base(file.Name()), filepath.Base(path))
}
