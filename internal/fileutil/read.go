package fileutil

import (
	"bytes"
	"errors"
	"io"
	"os"
)

var (
	ErrNotRegular = errors.New("not a regular file")
	ErrTooLarge   = errors.New("file exceeds the size limit")
)

// OpenRegular opens a regular file for reading. The caller must close it.
func OpenRegular(path string) (*os.File, os.FileInfo, error) {
	file, err := openForRead(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, ErrNotRegular
	}
	return file, info, nil
}

// ReadRegular reads a bounded regular file and closes it before returning.
func ReadRegular(path string, limit int64) ([]byte, error) {
	file, info, err := OpenRegular(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info.Size() > limit {
		return nil, ErrTooLarge
	}
	var data bytes.Buffer
	data.Grow(int(info.Size()) + bytes.MinRead)
	if _, err := data.ReadFrom(io.LimitReader(file, limit+1)); err != nil {
		return nil, err
	}
	if int64(data.Len()) > limit {
		return nil, ErrTooLarge
	}
	return data.Bytes(), nil
}
