//go:build unix

package fileutil

import (
	"os"
	"syscall"
)

func openForRead(path string) (*os.File, error) {
	// A file can be replaced with a pipe before open. Avoid waiting for a
	// writer so OpenRegular can reject the opened descriptor immediately.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
}
