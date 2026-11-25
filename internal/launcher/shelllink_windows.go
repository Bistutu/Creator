package launcher

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/sys/windows"
)

var (
	ole32          = windows.NewLazySystemDLL("ole32.dll")
	coInitializeEx = ole32.NewProc("CoInitializeEx")
	coUninitialize = ole32.NewProc("CoUninitialize")
	coCreate       = ole32.NewProc("CoCreateInstance")
	clsidShellLink = windows.GUID{Data1: 0x00021401, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidShellLinkW  = windows.GUID{Data1: 0x000214f9, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidPersistFile = windows.GUID{Data1: 0x0000010b, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
)

type shellLink struct{ v *shellLinkMethods }
type shellLinkMethods struct {
	query, addRef, release            uintptr
	getPath, getIDList, setIDList     uintptr
	getDescription, setDescription    uintptr
	getDirectory, setDirectory        uintptr
	getArguments, setArguments        uintptr
	getHotkey, setHotkey              uintptr
	getShowCmd, setShowCmd            uintptr
	getIconLocation, setIconLocation  uintptr
	setRelativePath, resolve, setPath uintptr
}

type persistFile struct{ v *persistFileMethods }
type persistFileMethods struct {
	query, addRef, release          uintptr
	getClassID, isDirty, load, save uintptr
}

func withCOM(f func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result, _, _ := coInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED
	if int32(result) >= 0 {
		defer coUninitialize.Call()
	} else if uint32(result) != 0x80010106 { // RPC_E_CHANGED_MODE
		return hresult("CoInitializeEx", result)
	}
	return f()
}

func withShortcut(f func(*shellLink, *persistFile) error) error {
	var link *shellLink
	result, _, _ := coCreate.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, 1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&link)))
	if err := hresult("create shell link", result); err != nil {
		return err
	}
	defer syscall.SyscallN(link.v.release, uintptr(unsafe.Pointer(link)))
	var file *persistFile
	result, _, _ = syscall.SyscallN(link.v.query, uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(&iidPersistFile)), uintptr(unsafe.Pointer(&file)))
	if err := hresult("open shell link persistence", result); err != nil {
		return err
	}
	defer syscall.SyscallN(file.v.release, uintptr(unsafe.Pointer(file)))
	return f(link, file)
}

const shortcutMarker = "Creator application "

func saveShortcut(path, executable, icon, description, appUserModelID string) error {
	return withShortcut(func(link *shellLink, file *persistFile) error {
		for _, field := range []struct {
			method    uintptr
			op, value string
		}{
			{link.v.setPath, "set target", executable},
			{link.v.setDirectory, "set working directory", filepath.Dir(executable)},
			{link.v.setDescription, "set description", description},
		} {
			if err := shortcutString(field.op, field.method, unsafe.Pointer(link), field.value, nil); err != nil {
				return err
			}
		}
		index := uintptr(0)
		if err := shortcutString("set icon", link.v.setIconLocation, unsafe.Pointer(link), icon, &index); err != nil {
			return err
		}
		if appUserModelID != "" {
			if err := link.setAppUserModelID(appUserModelID); err != nil {
				return err
			}
		}
		remember := uintptr(1)
		return shortcutString("save shortcut", file.v.save, unsafe.Pointer(file), path, &remember)
	})
}

func shortcutOwner(path string) string {
	owner, _ := readShortcutOwner(path)
	return owner
}

func readShortcutOwner(path string) (string, error) {
	var owner string
	err := withSavedShortcut(path, func(link *shellLink) error {
		var text [128]uint16
		result, _, _ := syscall.SyscallN(link.v.getDescription, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
		if err := hresult("get description", result); err != nil {
			return err
		}
		description := windows.UTF16ToString(text[:])
		id, marked := strings.CutPrefix(description, shortcutMarker)
		if !marked {
			id, marked = strings.CutPrefix(description, "WebCrate application ")
		}
		if marked && profile.ValidID(id) {
			owner = id
		}
		return nil
	})
	return owner, err
}

func shortcutTarget(path string) string {
	var target [windows.MAX_PATH]uint16
	err := withSavedShortcut(path, func(link *shellLink) error {
		const rawPath = 0x4 // SLGP_RAWPATH
		result, _, _ := syscall.SyscallN(link.v.getPath, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(&target[0])), uintptr(len(target)), 0, rawPath)
		return hresult("get stored shortcut target", result)
	})
	if err != nil {
		return ""
	}
	return windows.UTF16ToString(target[:])
}

func withSavedShortcut(path string, use func(*shellLink) error) error {
	return withShortcut(func(link *shellLink, file *persistFile) error {
		read := uintptr(0)
		if err := shortcutString("load shortcut", file.v.load, unsafe.Pointer(file), path, &read); err != nil {
			return err
		}
		return use(link)
	})
}

func shortcutString(op string, method uintptr, self unsafe.Pointer, value string, extra *uintptr) error {
	text, err := windows.UTF16PtrFromString(value)
	if err != nil {
		return err
	}
	var result uintptr
	if extra == nil {
		result, _, _ = syscall.SyscallN(method, uintptr(self), uintptr(unsafe.Pointer(text)))
	} else {
		result, _, _ = syscall.SyscallN(method, uintptr(self), uintptr(unsafe.Pointer(text)), *extra)
	}
	return hresult(op, result)
}

func hresult(operation string, result uintptr) error {
	if int32(result) < 0 {
		return fmt.Errorf("%s: HRESULT 0x%08x", operation, uint32(result))
	}
	return nil
}
