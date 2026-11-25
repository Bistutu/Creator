package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/sys/windows"
)

func TestWindowsStartMenuTracksAppLifecycle(t *testing.T) {
	t.Run("ASCII", func(t *testing.T) { checkWindowsStartMenuLifecycle(t, "Example Desk") })
	t.Run("Unicode", func(t *testing.T) { checkWindowsStartMenuLifecycle(t, "示例 Desk") })
}

func checkWindowsStartMenuLifecycle(t *testing.T, name string) {
	t.Helper()
	programs := filepath.Join(t.TempDir(), "Start Menu")
	directory, prior := filepath.Join(programs, "Creator"), filepath.Join(programs, "WebCrate")
	menu := newWindowsEntries(directory, prior)
	t.Cleanup(menu.close)
	if err := os.MkdirAll(prior, 0o755); err != nil {
		t.Fatal(err)
	}
	app := profile.App{ID: profile.NewID(), Name: name}
	sibling := app
	sibling.ID = profile.NewID()
	executable := func(name string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name+".exe")
		if err := os.WriteFile(path, []byte("shell link target"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), "AppIcon.ico"), []byte("icon target"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	first, second := executable(app.Name), executable(sibling.Name)
	old := filepath.Join(prior, app.Name+".lnk")
	unrelated := filepath.Join(prior, "Unrelated.lnk")
	if err := withCOM(func() error {
		icon := filepath.Join(filepath.Dir(first), "AppIcon.ico")
		if err := saveShortcut(old, first, icon, "WebCrate application "+app.ID, ""); err != nil {
			return err
		}
		return saveShortcut(unrelated, first, icon, "External application", "")
	}); err != nil {
		t.Fatal(err)
	}
	if err := menu.Register(app, first); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous brand shortcut still exists: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated shortcut should survive: %v", err)
	}
	if err := menu.Register(sibling, second); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(directory, app.Name+".lnk")
	b := filepath.Join(directory, sibling.Name+" (2).lnk")
	assertNativeLink(t, a, app.ID, first)
	assertNativeLink(t, b, sibling.ID, second)

	app.Name = "New Name"
	rename := executable(app.Name)
	if err := menu.Register(app, rename); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old Start menu entry: %v", err)
	}
	c := filepath.Join(directory, app.Name+".lnk")
	assertNativeLink(t, c, app.ID, rename)
	removeWhileLocked := func() error {
		path, err := windows.UTF16PtrFromString(c)
		if err != nil {
			t.Fatal(err)
		}
		link, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(link)
		return menu.Remove(app.ID)
	}
	if err := removeWhileLocked(); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("removing the locked ownership link returned %v", err)
	}
	assertNativeLink(t, c, app.ID, rename)
	if err := menu.Remove(app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed Start menu entry: %v", err)
	}
	if err := menu.Register(sibling, second); err != nil {
		t.Fatal(err)
	}
	assertNativeLink(t, b, sibling.ID, second)
	if _, err := os.Stat(a); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refresh moved the remaining shortcut to the freed path: %v", err)
	}
	withNumber := sibling
	withNumber.Name += " (2)"
	withNumberExecutable := executable(withNumber.Name)
	if err := menu.Register(withNumber, withNumberExecutable); err != nil {
		t.Fatal(err)
	}
	assertNativeLink(t, b, sibling.ID, withNumberExecutable)
	if err := menu.Register(sibling, second); err != nil {
		t.Fatal(err)
	}
	assertNativeLink(t, a, sibling.ID, second)
	if _, err := os.Stat(b); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("renaming the app incorrectly preserved digits in the name: %v", err)
	}
}

func TestWindowsStartMenuRechecksAnExternallyChangedShortcut(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "Creator")
	menu := newWindowsEntries(directory, "")
	t.Cleanup(menu.close)
	app := profile.App{ID: profile.NewID(), Name: "Workbench"}
	executable := filepath.Join(root, app.Name+".exe")
	icon := filepath.Join(root, "AppIcon.ico")
	for _, path := range []string{executable, icon} {
		if err := os.WriteFile(path, []byte(filepath.Ext(path)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(directory, app.Name+".lnk")
	register := func() {
		t.Helper()
		if err := menu.Register(app, executable); err != nil {
			t.Fatal(err)
		}
	}
	register()
	timeBefore := time.Now().Add(-15 * time.Second)
	if err := os.Chtimes(link, timeBefore, timeBefore); err != nil {
		t.Fatal(err)
	}
	register()
	refreshed := fileInfoFromHandle(link)
	if refreshed == nil || !refreshed.ModTime().After(timeBefore) {
		t.Fatal("registration did not refresh the shortcut for the newer icon")
	}
	watch, err := windows.FindFirstChangeNotification(directory, false, windows.FILE_NOTIFY_CHANGE_DIR_NAME)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.FindCloseChangeNotification(watch) })
	register()
	if result, err := windows.WaitForSingleObject(watch, 40); err != nil || result != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("an unchanged shortcut used a staging directory: status %d, %v", result, err)
	}
	stayed := fileInfoFromHandle(link)
	if stayed == nil || !os.SameFile(refreshed, stayed) || !refreshed.ModTime().Equal(stayed.ModTime()) {
		t.Fatal("registering the same app replaced its valid shortcut")
	}
	foreign := profile.NewID()
	if err := withCOM(func() error { return saveShortcut(link, executable, icon, shortcutMarker+foreign, "") }); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(link, stayed.ModTime(), stayed.ModTime()); err != nil {
		t.Fatal(err)
	}
	register()
	assertNativeLink(t, link, foreign, executable)
	appLink := filepath.Join(directory, app.Name+" (2).lnk")
	assertNativeLink(t, appLink, app.ID, executable)
	if err := menu.Remove(app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(appLink); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned shortcut still exists after removal: %v", err)
	}
	assertNativeLink(t, link, foreign, executable)
}

func TestWindowsShortcutMoveDoesNotReplaceAnExistingFile(t *testing.T) {
	root := t.TempDir()
	staged := filepath.Join(root, "new.lnk")
	destination := filepath.Join(root, "existing.lnk")
	for path, data := range map[string]string{staged: "new", destination: "existing"} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := moveNewShortcut(staged, destination); !errors.Is(err, errShortcutClaimed) {
		t.Fatalf("moving over a conflicting shortcut returned %v", err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "existing" {
		t.Fatalf("conflicting shortcut was changed: %q, %v", got, err)
	}
}

func TestWindowsMenuDirectoryReusesIndexUntilFilesChange(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "Creator")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	menu := menuDirectory{path: directory}
	t.Cleanup(menu.close)
	id := profile.NewID()
	link := filepath.Join(directory, "External.lnk")
	executable := filepath.Join(root, "Workbench.exe")
	if err := os.WriteFile(executable, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := withCOM(func() error {
		if err := saveShortcut(link, executable, filepath.Join(root, "AppIcon.ico"), "External app", ""); err != nil {
			return err
		}
		first, err := menu.entries()
		if err != nil {
			return err
		}
		if menu.watch == 0 {
			if os.Getenv("GITHUB_ACTIONS") == "true" {
				t.Fatal("the CI runner could not watch its local test directory")
			}
			t.Skip("local directory change notifications are unavailable")
		}
		folder := menu.folder
		unchanged, err := menu.entries()
		if err != nil {
			return err
		}
		if menu.folder != folder || unchanged.byName["external.lnk"] != first.byName["external.lnk"] {
			t.Fatal("an unchanged folder was rescanned")
		}
		if err := saveShortcut(link, executable, filepath.Join(root, "AppIcon.ico"), shortcutMarker+id, ""); err != nil {
			return err
		}
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			updated, err := menu.entries()
			if err != nil {
				return err
			}
			if owned := updated.byOwner[id]; len(owned) == 1 && owned[0] == "external.lnk" {
				return nil
			}
		}
		t.Fatal("the index did not reflect an externally changed owner")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func assertNativeLink(t *testing.T, path, id, executable string) {
	t.Helper()
	err := withCOM(func() error {
		if owner := shortcutOwner(path); owner != id {
			t.Errorf("shortcut ownership = %q, want %q", owner, id)
		}
		return withShortcut(func(link *shellLink, file *persistFile) error {
			read := uintptr(0)
			if err := shortcutString("load shortcut", file.v.load, unsafe.Pointer(file), path, &read); err != nil {
				return err
			}
			var target, icon [1024]uint16
			result, _, _ := syscall.SyscallN(link.v.getPath, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(&target[0])), uintptr(len(target)), 0, 0)
			if err := hresult("get target", result); err != nil {
				return err
			}
			var index int32
			result, _, _ = syscall.SyscallN(link.v.getIconLocation, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(&icon[0])), uintptr(len(icon)), uintptr(unsafe.Pointer(&index)))
			if err := hresult("get icon", result); err != nil {
				return err
			}
			if got := windows.UTF16ToString(target[:]); !strings.EqualFold(got, executable) {
				t.Errorf("shortcut target = %q, want %q", got, executable)
			}
			if got := windows.UTF16ToString(icon[:]); !strings.EqualFold(got, filepath.Join(filepath.Dir(executable), "AppIcon.ico")) || index != 0 {
				t.Errorf("shortcut icon = %q (%d)", got, index)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}
