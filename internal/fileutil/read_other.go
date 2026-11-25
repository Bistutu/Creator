//go:build !unix

package fileutil

import "os"

func openForRead(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	return os.Open(path)
}
