package profile

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func replaceProfiles(staged, path string) error {
	// A standalone app can hold a short-lived read handle while resolving its
	// homepage. Let it close before replacing the committed snapshot.
	for delay := 5 * time.Millisecond; ; delay *= 2 {
		err := os.Rename(staged, path)
		if delay > 40*time.Millisecond ||
			!(errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)) {
			return err
		}
		time.Sleep(delay)
	}
}
