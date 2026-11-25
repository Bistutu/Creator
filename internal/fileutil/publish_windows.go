package fileutil

import (
	"os"

	"golang.org/x/sys/windows"
)

func PublishNew(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(from, to, 0); err != nil {
		return &os.LinkError{Op: "publish", Old: source, New: destination, Err: err}
	}
	return nil
}
