package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/profile"
)

func TestLinuxLaunchesFromApplicationMenu(t *testing.T) {
	gio, err := exec.LookPath("gio")
	if err != nil {
		if os.Getenv("CI") == "true" {
			t.Fatal(err)
		}
		t.Skip("requires GLib's gio")
	}
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	menu, err := ForUser()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "示例 with \"quotes\" %$`\\")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'launched' > \"$0.ready\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := profile.App{ID: profile.NewID(), Name: "示例 App"}
	if err := menu.Register(app, executable); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(data, "applications", "com.webcrate."+app.ID+".desktop")
	if tool, err := exec.LookPath("desktop-file-validate"); err == nil {
		if output, err := exec.Command(tool, entry).CombinedOutput(); err != nil {
			t.Fatalf("desktop entry validation: %v (%s)", err, output)
		}
	}
	if output, err := exec.Command(gio, "launch", entry).CombinedOutput(); err != nil {
		t.Fatalf("GLib could not launch the entry: %v (%s)", err, output)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(executable + ".ready"); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	t.Fatal("the launcher did not execute its target")
}
