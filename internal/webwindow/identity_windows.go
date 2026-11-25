package webwindow

import (
	"fmt"
	"unsafe"

	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/sys/windows"
)

var setProcessAppID = windows.NewLazySystemDLL("shell32.dll").NewProc("SetCurrentProcessExplicitAppUserModelID")

// SetWindowIdentity associates all windows of the website process with its
// own Start menu link. Call it before creating the first window.
func SetWindowIdentity(id string) error {
	value, err := windows.UTF16PtrFromString(profile.WindowsAppUserModelID(id))
	if err != nil {
		return err
	}
	if err := setProcessAppID.Find(); err != nil {
		return err
	}
	result, _, _ := setProcessAppID.Call(uintptr(unsafe.Pointer(value)))
	if int32(result) < 0 {
		return fmt.Errorf("SetCurrentProcessExplicitAppUserModelID: HRESULT 0x%08x", uint32(result))
	}
	return nil
}
