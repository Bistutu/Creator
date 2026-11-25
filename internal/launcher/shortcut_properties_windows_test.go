package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/sys/windows"
)

func TestWindowsWebsiteShortcutIdentity(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "Creator")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	menu := newWindowsEntries(directory, "")
	t.Cleanup(menu.close)
	first := profile.App{ID: "app-0011223344556677", Name: "Dashboard"}
	second := profile.App{ID: "app-0011223344556688", Name: first.Name}
	newTarget := func(app profile.App) string {
		t.Helper()
		dir := filepath.Join(root, app.ID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		executable := filepath.Join(dir, app.Name+".exe")
		for _, path := range []string{executable, filepath.Join(dir, "AppIcon.ico")} {
			if err := os.WriteFile(path, []byte(app.ID), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return executable
	}
	firstTarget, secondTarget := newTarget(first), newTarget(second)
	firstPath := filepath.Join(directory, first.Name+".lnk")
	if err := withCOM(func() error {
		return saveShortcut(firstPath, firstTarget, filepath.Join(filepath.Dir(firstTarget), "AppIcon.ico"), shortcutMarker+first.ID, "")
	}); err != nil {
		t.Fatal(err)
	}
	assertShortcutIdentity(t, firstPath, "")
	if err := menu.Register(first, firstTarget); err != nil {
		t.Fatal(err)
	}
	const firstID = "WebCrate.Creator.App0011223344556677"
	const secondID = "WebCrate.Creator.App0011223344556688"
	assertShortcutIdentity(t, firstPath, firstID)
	assertNativeLink(t, firstPath, first.ID, firstTarget)
	if err := menu.Register(second, secondTarget); err != nil {
		t.Fatal(err)
	}
	secondPath := filepath.Join(directory, second.Name+" (2).lnk")
	assertShortcutIdentity(t, secondPath, secondID)
	assertNativeLink(t, secondPath, second.ID, secondTarget)

	menu.close() // Force a fresh lookup, as after restarting Creator.
	first.Name = "工作台"
	renamedTarget := newTarget(first)
	if err := menu.Register(first, renamedTarget); err != nil {
		t.Fatal(err)
	}
	renamedPath := filepath.Join(directory, first.Name+".lnk")
	assertShortcutIdentity(t, renamedPath, firstID)
	assertNativeLink(t, renamedPath, first.ID, renamedTarget)
	assertShortcutIdentity(t, secondPath, secondID)
	if _, err := os.Stat(firstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the previous shortcut survived the rename: %v", err)
	}
}

func assertShortcutIdentity(t *testing.T, path, want string) {
	t.Helper()
	clearVariant := ole32.NewProc("PropVariantClear")
	err := withCOM(func() error {
		return withSavedShortcut(path, func(link *shellLink) error {
			return link.withProperties(func(store *propertyStore) error {
				var value stringVariant
				result, _, _ := syscall.SyscallN(store.v.get, uintptr(unsafe.Pointer(store)),
					uintptr(unsafe.Pointer(&appUserModelIDKey)), uintptr(unsafe.Pointer(&value)))
				defer func() { clearVariant.Call(uintptr(unsafe.Pointer(&value))) }()
				if err := hresult("read shortcut AppUserModelID", result); err != nil {
					return err
				}
				if want == "" && value.kind == 0 { // VT_EMPTY
					return nil
				}
				if value.kind != wideStringVariant || value.text == nil {
					return fmt.Errorf("AppUserModelID variant type %d", value.kind)
				}
				if got := windows.UTF16PtrToString(value.text); got != want {
					return fmt.Errorf("AppUserModelID %q, want %q", got, want)
				}
				return nil
			})
		})
	})
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
