package launcher

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

type macEntries struct{}

func ForUser() (library.Launcher, error) {
	return macEntries{}, nil
}

type launchServices struct {
	url      func(uintptr, *byte, int, uint8) uintptr
	release  func(uintptr)
	register func(uintptr, uint8) int32
}

var loadLaunchServices = sync.OnceValues(func() (launchServices, error) {
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		return launchServices{}, err
	}
	core, err := purego.Dlopen("/System/Library/Frameworks/CoreServices.framework/CoreServices", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		_ = purego.Dlclose(cf)
		return launchServices{}, err
	}
	var api launchServices
	purego.RegisterLibFunc(&api.url, cf, "CFURLCreateFromFileSystemRepresentation")
	purego.RegisterLibFunc(&api.release, cf, "CFRelease")
	purego.RegisterLibFunc(&api.register, core, "LSRegisterURL")
	return api, nil
})

func (macEntries) Register(_ profile.App, path string) error {
	return registerMacApplication(path, 1)
}

// RegisterIfNeeded lets Launch Services reuse its record for a current bundle.
func (macEntries) RegisterIfNeeded(_ profile.App, path string) error {
	return registerMacApplication(path, 0)
}

func registerMacApplication(path string, update uint8) error {
	api, err := loadLaunchServices()
	if err != nil {
		return err
	}
	file := []byte(path)
	if len(file) == 0 {
		return errors.New("missing application path")
	}
	url := api.url(0, &file[0], len(file), 1)
	if url == 0 {
		return fmt.Errorf("could not make an application URL for %s", path)
	}
	defer api.release(url)
	if status := api.register(url, update); status != 0 {
		return fmt.Errorf("macOS application registration failed (%d)", status)
	}
	return nil
}

// Launch Services drops the old path when it notices the bundle was removed.
func (macEntries) Remove(string) error { return nil }
