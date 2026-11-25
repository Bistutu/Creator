package launcher

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type propertyKey struct {
	format windows.GUID
	id     uint32
}

var (
	iidPropertyStore  = windows.GUID{Data1: 0x886d8eeb, Data2: 0x8cf2, Data3: 0x4446, Data4: [8]byte{0x8d, 0x02, 0xcd, 0xba, 0x1d, 0xbd, 0xcf, 0x99}}
	appUserModelIDKey = propertyKey{
		format: windows.GUID{Data1: 0x9f4c2855, Data2: 0x9f79, Data3: 0x4b39, Data4: [8]byte{0xa8, 0xd0, 0xe1, 0xd4, 0x2d, 0xe1, 0xd5, 0xf3}},
		id:     5,
	}
)

type propertyStore struct{ v *propertyStoreMethods }

type propertyStoreMethods struct {
	query, addRef, release            uintptr
	getCount, getAt, get, set, commit uintptr
}

const wideStringVariant = 31 // VT_LPWSTR

// PROPVARIANT with its LPWSTR member active. Reserve the full union, whose
// largest members include a count and a pointer.
type stringVariant struct {
	kind uint16
	_    [3]uint16
	text *uint16
	_    uintptr
}

func (link *shellLink) withProperties(use func(*propertyStore) error) error {
	var store *propertyStore
	result, _, _ := syscall.SyscallN(link.v.query, uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(&iidPropertyStore)), uintptr(unsafe.Pointer(&store)))
	if err := hresult("open shortcut property store", result); err != nil {
		return err
	}
	defer syscall.SyscallN(store.v.release, uintptr(unsafe.Pointer(store)))
	return use(store)
}

func (link *shellLink) setAppUserModelID(id string) error {
	text, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return err
	}
	return link.withProperties(func(store *propertyStore) error {
		value := stringVariant{kind: wideStringVariant, text: text}
		result, _, _ := syscall.SyscallN(store.v.set, uintptr(unsafe.Pointer(store)),
			uintptr(unsafe.Pointer(&appUserModelIDKey)), uintptr(unsafe.Pointer(&value)))
		runtime.KeepAlive(text)
		if err := hresult("set shortcut AppUserModelID", result); err != nil {
			return err
		}
		result, _, _ = syscall.SyscallN(store.v.commit, uintptr(unsafe.Pointer(store)))
		return hresult("commit shortcut properties", result)
	})
}
