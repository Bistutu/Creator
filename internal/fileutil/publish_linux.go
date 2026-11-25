package fileutil

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// PublishNew gives a completed file an unused name on the same filesystem.
// The caller owns the source directory and removes it afterwards.
func PublishNew(source, destination string) error {
	err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return os.Link(source, destination)
	}
	return &os.LinkError{Op: "publish", Old: source, New: destination, Err: err}
}
