package launcher

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bistutu/Creator/internal/profile"
)

func TestDesktopEntryTracksRenamesAndRemoval(t *testing.T) {
	menu := desktopEntries{directory: filepath.Join(t.TempDir(), "applications")}
	app := profile.App{ID: profile.NewID(), Name: "示例 & 搜索"}
	path, err := menu.path(app.ID)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exec := filepath.Join(dir, "Example \"site\" $5 %")
	if err := menu.Register(app, exec); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	windowIdentity := []byte("\nStartupWMClass=com.webcrate." + app.ID + "\n")
	for _, expected := range []string{"Name=" + app.Name, "Type=Application", "Icon=" + desktopEscapes.Replace(filepath.Join(dir, "AppIcon.png")), "Terminal=false"} {
		if !strings.Contains(string(entry), "\n"+expected+"\n") {
			t.Errorf("missing %q in %q", expected, entry)
		}
	}
	if !bytes.Contains(entry, []byte(`Example \\"site\\" \\$5 %%`)) {
		t.Errorf("Exec field has incorrect quoting: %q", entry)
	}
	if !bytes.Contains(entry, windowIdentity) {
		t.Errorf("desktop entry does not use its own ID as the window class: %q", entry)
	}
	if err := menu.Register(app, exec); err != nil {
		t.Fatal(err)
	}
	app.Name = "Renamed"
	newTarget := filepath.Join(dir, "Renamed")
	if err := menu.Register(app, newTarget); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(updated, []byte("Name=Renamed\n")) || !bytes.Contains(updated, []byte(`Exec="`+desktopEscapes.Replace(execEscapes.Replace(newTarget))+`"`)) {
		t.Fatalf("invalid updated app-menu entry: %q %v", updated, err)
	}
	if !bytes.Contains(updated, windowIdentity) {
		t.Errorf("renaming changed the window class: %q", updated)
	}
	for range 2 {
		if err := menu.Remove(app.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("application menu entry still exists: %v", err)
	}
}

func TestDesktopEntryPreservesForeignFile(t *testing.T) {
	menu := desktopEntries{directory: t.TempDir()}
	app := profile.App{ID: profile.NewID(), Name: "Example"}
	path, err := menu.path(app.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreign := []byte("[Desktop Entry]\nName=Other app\n")
	if err := os.WriteFile(path, foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := menu.Register(app, filepath.Join(t.TempDir(), "Example")); err == nil {
		t.Fatal("registration overwrote an unmanaged file")
	}
	if err := menu.Remove(app.ID); err != nil {
		t.Fatalf("an unmanaged file prevented removal: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, foreign) {
		t.Fatalf("unmanaged file was changed: %q %v", data, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := menu.Register(app, filepath.Join(t.TempDir(), "Example")); err == nil {
		t.Fatal("registration replaced an unmanaged directory")
	}
	if err := menu.Remove(app.ID); err != nil {
		t.Fatalf("an unmanaged directory prevented removal: %v", err)
	}
	if directory, err := os.Stat(path); err != nil || !directory.IsDir() {
		t.Fatalf("unmanaged directory was changed: %v", err)
	}
	if err := menu.Remove("../other"); err == nil {
		t.Fatal("invalid ID accepted")
	}
}
