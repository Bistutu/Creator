//go:build !linux && !windows

package fileutil

import "os"

func PublishNew(source, destination string) error {
	return os.Link(source, destination)
}
