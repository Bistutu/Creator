package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

type menuDirectory struct {
	path   string
	watch  windows.Handle
	folder os.FileInfo
	index  startMenuIndex
}

// Read the menu once while the local directory is unchanged. Watch only its
// files so staging a shortcut in a temporary subdirectory keeps the index warm.
func (d *menuDirectory) entries() (startMenuIndex, error) {
	if d.path == "" {
		return startMenuIndex{}, nil
	}
	if d.watch != 0 {
		state, err := windows.WaitForSingleObject(d.watch, 0)
		if err == nil && state == uint32(windows.WAIT_TIMEOUT) {
			folder, err := os.Lstat(d.path)
			if err == nil && os.SameFile(d.folder, folder) {
				return d.index, nil
			}
		}
		d.close()
	}
	watch, folder := watchMenuDirectory(d.path)
	index, readable, err := scanStartMenu(d.path)
	if watch != 0 {
		if err != nil || !readable {
			_ = windows.FindCloseChangeNotification(watch)
		} else {
			d.watch, d.folder, d.index = watch, folder, index
		}
	}
	return index, err
}

func (d *menuDirectory) close() {
	if d.watch != 0 {
		_ = windows.FindCloseChangeNotification(d.watch)
	}
	d.watch, d.folder, d.index = 0, nil, startMenuIndex{}
}

func watchMenuDirectory(path string) (windows.Handle, os.FileInfo) {
	dir, err := os.Open(path)
	if err != nil {
		return 0, nil
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil || !info.IsDir() {
		return 0, nil
	}
	// Directory change notifications may be unavailable on network filesystems.
	var resolved [windows.MAX_PATH + 1]uint16
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(dir.Fd()), &resolved[0], uint32(len(resolved)), 0)
	if err != nil || n >= uint32(len(resolved)) {
		return 0, nil
	}
	volume := filepath.VolumeName(strings.TrimPrefix(windows.UTF16ToString(resolved[:n]), `\\?\`))
	if len(volume) != 2 || volume[1] != ':' {
		return 0, nil
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil || windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return 0, nil
	}
	const changes = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_ATTRIBUTES |
		windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE | windows.FILE_NOTIFY_CHANGE_SECURITY
	watch, err := windows.FindFirstChangeNotification(path, false, changes)
	if err != nil {
		return 0, nil
	}
	return watch, info
}

func scanStartMenu(directory string) (startMenuIndex, bool, error) {
	files, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return startMenuIndex{}, false, nil
	}
	if err != nil {
		return startMenuIndex{}, false, err
	}
	entries := make(map[string]startMenuEntry, len(files))
	readable := true
	for _, entry := range files {
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".lnk") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		var owner string
		if entry.Type().IsRegular() {
			owner, err = readShortcutOwner(path)
			readable = readable && err == nil
		}
		entries[name] = startMenuEntry{path: path, owner: owner}
	}
	return newStartMenuIndex(entries), readable, nil
}
