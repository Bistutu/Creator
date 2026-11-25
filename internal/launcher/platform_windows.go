package launcher

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/sys/windows"
)

type windowsEntries struct {
	mu             sync.Mutex
	current, prior menuDirectory
	verified       map[string]verifiedShortcut
}

type verifiedShortcut struct {
	path, executable string
	executableInfo   os.FileInfo
	digest           [sha256.Size]byte
}

var errShortcutClaimed = errors.New("Start menu entry changed during registration")

const (
	shellCreate = 0x00000002 // SHCNE_CREATE
	shellDelete = 0x00000004 // SHCNE_DELETE
	shellUpdate = 0x00002000 // SHCNE_UPDATEITEM
)

func ForUser() (library.Launcher, error) {
	programs, err := windows.KnownFolderPath(windows.FOLDERID_Programs, windows.KF_FLAG_DONT_VERIFY)
	if err != nil {
		return nil, err
	}
	return newWindowsEntries(filepath.Join(programs, "Creator"), filepath.Join(programs, "WebCrate")), nil
}

func newWindowsEntries(current, prior string) *windowsEntries {
	return &windowsEntries{
		current: menuDirectory{path: current}, prior: menuDirectory{path: prior},
		verified: make(map[string]verifiedShortcut),
	}
}

func (e *windowsEntries) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.current.close()
	e.prior.close()
	clear(e.verified)
}

func (e *windowsEntries) Register(app profile.App, executable string) error {
	if !profile.ValidID(app.ID) {
		return errors.New("invalid application ID")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return withCOM(func() error {
		base := filepath.Base(executable)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		icon := filepath.Join(filepath.Dir(executable), "AppIcon.ico")
		for attempt := 0; ; attempt++ {
			entries, err := e.current.entries()
			if err != nil {
				return err
			}
			name := entries.filename(stem, app.ID, func(link string) bool {
				return strings.EqualFold(filepath.Base(shortcutTarget(link)), base)
			})
			target := filepath.Join(e.current.path, name)
			if err := e.publish(target, executable, icon, app.ID); err != nil {
				if attempt == 0 && errors.Is(err, errShortcutClaimed) {
					e.current.close()
					continue
				}
				return err
			}
			return errors.Join(e.current.removeOwned(entries, app.ID, target), e.removePrior(app.ID))
		}
	})
}

func (e *windowsEntries) Remove(id string) error {
	if !profile.ValidID(id) {
		return errors.New("invalid application ID")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.verified, id)
	return withCOM(func() error {
		entries, err := e.current.entries()
		if err != nil {
			return err
		}
		return errors.Join(e.current.removeOwned(entries, id, ""), e.removePrior(id))
	})
}

func (e *windowsEntries) removePrior(id string) error {
	entries, err := e.prior.entries()
	if err != nil {
		return err
	}
	return e.prior.removeOwned(entries, id, "")
}

func (e *windowsEntries) publish(target, executable, icon, id string) error {
	linkInfo, err := os.Lstat(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var existing []byte
	var digest [sha256.Size]byte
	executableInfo := fileInfoFromHandle(executable)
	iconCurrent := false
	if linkInfo != nil {
		if !linkInfo.Mode().IsRegular() {
			return fmt.Errorf("%w: %s", errShortcutClaimed, target)
		}
		existing, err = os.ReadFile(target)
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", errShortcutClaimed, target)
		}
		if err != nil {
			return err
		}
		iconInfo, iconErr := os.Stat(icon)
		iconCurrent = iconErr == nil && !iconInfo.ModTime().After(linkInfo.ModTime())
		digest = sha256.Sum256(existing)
		if known := e.verified[id]; iconCurrent && known.matches(target, executable, executableInfo, digest) {
			return nil
		}
		if shortcutOwner(target) != id {
			return fmt.Errorf("%w: %s", errShortcutClaimed, target)
		}
	}

	if err := os.MkdirAll(e.current.path, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(e.current.path, ".webcrate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	stagedLink := filepath.Join(stage, "application.lnk")
	if err := saveShortcut(stagedLink, executable, icon, shortcutMarker+id, profile.WindowsAppUserModelID(id)); err != nil {
		return err
	}
	data, err := os.ReadFile(stagedLink)
	if err != nil {
		return err
	}
	if existing == nil {
		if err := moveNewShortcut(stagedLink, target); err != nil {
			return err
		}
		e.current.close()
		notifyShell(shellCreate, target)
	} else if !bytes.Equal(existing, data) || !iconCurrent {
		current, err := os.ReadFile(target)
		if errors.Is(err, os.ErrNotExist) || (err == nil && !bytes.Equal(existing, current)) {
			return fmt.Errorf("%w: %s", errShortcutClaimed, target)
		}
		if err != nil {
			return err
		}
		if err := os.Rename(stagedLink, target); err != nil {
			return err
		}
		e.current.close()
		notifyShell(shellUpdate, target)
		notifyShell(shellUpdate, icon)
	}
	e.verified[id] = verifiedShortcut{path: target, executable: executable, executableInfo: executableInfo, digest: sha256.Sum256(data)}
	return nil
}

func (v verifiedShortcut) matches(path, executable string, info os.FileInfo, digest [sha256.Size]byte) bool {
	return v.path == path && v.executable == executable && v.digest == digest &&
		v.executableInfo != nil && info != nil && os.SameFile(v.executableInfo, info) &&
		v.executableInfo.Size() == info.Size() && v.executableInfo.ModTime().Equal(info.ModTime())
}

// Capture the file ID while the file is open. os.Stat defers reading it on Windows.
func fileInfoFromHandle(path string) os.FileInfo {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	return info
}

func moveNewShortcut(staged, target string) error {
	err := fileutil.PublishNew(staged, target)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
		return fmt.Errorf("%w: %s", errShortcutClaimed, target)
	}
	return err
}

func (d *menuDirectory) removeOwned(entries startMenuIndex, id, keep string) error {
	var failures error
	for _, name := range entries.byOwner[id] {
		entry := entries.byName[name]
		if strings.EqualFold(entry.path, keep) {
			continue
		}
		info, err := os.Lstat(entry.path)
		if err != nil || !info.Mode().IsRegular() || shortcutOwner(entry.path) != id {
			continue
		}
		if err := os.Remove(entry.path); err == nil {
			d.close()
			notifyShell(shellDelete, entry.path)
		} else if !errors.Is(err, os.ErrNotExist) {
			failures = errors.Join(failures, err)
		}
	}
	return failures
}

var shellChange = windows.NewLazySystemDLL("shell32.dll").NewProc("SHChangeNotify")

func notifyShell(event uintptr, path string) {
	value, err := windows.UTF16PtrFromString(path)
	if err == nil {
		shellChange.Call(event, 0x0005, uintptr(unsafe.Pointer(value)), 0) // SHCNF_PATHW
	}
}
