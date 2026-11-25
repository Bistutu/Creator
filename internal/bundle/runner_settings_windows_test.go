package bundle

import (
	"errors"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPortableSettingsWithOpenLauncher(t *testing.T) {
	bundles, old, next, path := runnerSettingsFixture(t)
	defer lockAgainstReplacement(t, path)()
	got, err := bundles.UpdateSettings(t.Context(), old, next, nil, func() error { return nil })
	if err != nil || got != path {
		t.Fatalf("saving settings with the executable open returned %q, %v", got, err)
	}
	assertRunnerSettingsDirectory(t, bundles, next, path)
}

func TestWindowsPortableSettingsCanRetryLockedConfig(t *testing.T) {
	bundles, old, next, path := runnerSettingsFixture(t)
	unlock := lockAgainstReplacement(t, filepath.Join(filepath.Dir(path), ConfigName))
	defer unlock()
	saved := false
	save := func() error {
		saved = true
		return nil
	}
	_, err := bundles.UpdateSettings(t.Context(), old, next, nil, save)
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("saving the locked config returned %v", err)
	}
	if saved {
		t.Fatal("the profile committed while the configuration was still locked")
	}
	assertRunnerSettingsDirectory(t, bundles, old, path)
	unlock()
	if got, err := bundles.UpdateSettings(t.Context(), old, next, nil, save); err != nil || got != path || !saved {
		t.Fatalf("retry returned %q, %v, committed=%t", got, err, saved)
	}
	assertRunnerSettingsDirectory(t, bundles, next, path)
}

func lockAgainstReplacement(t *testing.T, path string) func() {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	return func() {
		if handle != windows.InvalidHandle {
			if err := windows.CloseHandle(handle); err != nil {
				t.Error(err)
			}
			handle = windows.InvalidHandle
		}
	}
}
