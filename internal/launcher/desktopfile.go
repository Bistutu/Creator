package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/runnerconfig"
)

type desktopEntries struct{ directory string }

var errForeignDesktopEntry = errors.New("application menu file belongs to another app")

func (e desktopEntries) Register(app profile.App, executable string) error {
	path, err := e.path(app.ID)
	if err != nil {
		return err
	}
	existing, err := readDesktopEntry(path, app.ID)
	if err != nil {
		return err
	}
	data := desktopEntry(app, executable)
	if bytes.Equal(existing, data) {
		return nil
	}
	return replace(path, data, 0o644)
}

func (e desktopEntries) Remove(id string) error {
	path, err := e.path(id)
	if err != nil {
		return err
	}
	data, err := readDesktopEntry(path, id)
	if errors.Is(err, errForeignDesktopEntry) {
		return nil
	}
	if err != nil || data == nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (e desktopEntries) path(id string) (string, error) {
	if !profile.ValidID(id) {
		return "", errors.New("invalid application ID")
	}
	return filepath.Join(e.directory, profile.LinuxDesktopID(id)+".desktop"), nil
}

func readDesktopEntry(path, id string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", errForeignDesktopEntry, path)
	}
	data, err := fileutil.ReadRegular(path, 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, []byte("[Desktop Entry]\nX-WebCrate-ID="+id+"\n")) {
		return nil, fmt.Errorf("%w: %s", errForeignDesktopEntry, path)
	}
	return data, nil
}

var desktopEscapes = strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
var execEscapes = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", `\$`, "%", "%%")

func desktopEntry(app profile.App, executable string) []byte {
	// Exec is a quoted command line read from a desktop-entry string.
	// The string escaping is decoded before its command line is split.
	exec := `"` + execEscapes.Replace(executable) + `"`
	return fmt.Appendf(nil, "[Desktop Entry]\nX-WebCrate-ID=%s\nType=Application\nName=%s\nExec=%s\nIcon=%s\nStartupWMClass=%s\nTerminal=false\nCategories=Network;\n",
		app.ID, desktopEscapes.Replace(app.Name), desktopEscapes.Replace(exec),
		desktopEscapes.Replace(filepath.Join(filepath.Dir(executable), runnerconfig.WindowIconFilename)), profile.LinuxDesktopID(app.ID))
}
