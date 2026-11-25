//go:build !windows

package profile

import "os"

func replaceProfiles(staged, path string) error {
	return os.Rename(staged, path)
}
