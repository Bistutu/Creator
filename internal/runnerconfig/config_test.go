package runnerconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestReadApplicationConfig(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing config = %v", err)
	}
	file := filepath.Join(dir, Filename)
	if err := os.Mkdir(file, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, fileutil.ErrNotRegular) {
		t.Fatalf("configuration directory = %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Repeat(" ", MaxSize+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, fileutil.ErrTooLarge) {
		t.Fatalf("oversized configuration = %v", err)
	}
	for _, invalid := range []string{
		`{`,
		`{"id":"missing","url":"https://example.com"}`,
		`{"id":"app-0123456789abcdef","url":"file:///example"}`,
		`{"id":"app-0123456789abcdef","url":"https://example.com","width":-1}`,
	} {
		if err := os.WriteFile(file, []byte(invalid), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Errorf("accepted invalid config %q", invalid)
		}
	}
	valid := `{"id":"app-0123456789abcdef","url":"https://www.example.com/app"}`
	if err := os.WriteFile(file, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := Load(dir)
	if err != nil || app.Name != "example.com" || app.Width != profile.DefaultWidth || app.Height != profile.DefaultHeight || app.URL != "https://www.example.com/app" {
		t.Fatalf("config defaults = %+v, %v", app, err)
	}
	if app.WindowRevision != 0 {
		t.Fatal("legacy config did not retain its window revision")
	}
	resized := `{"id":"app-0123456789abcdef","url":"https://www.example.com/app","width":960,"height":680,"windowRevision":2,"iconKind":"website"}`
	if err := os.WriteFile(file, []byte(resized), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err = Load(dir)
	if err != nil || app.Width != 960 || app.Height != 680 || app.WindowRevision != 2 {
		t.Fatalf("updated config = %+v, %v", app, err)
	}
}

func TestReadInstalledSettingsFromLibrary(t *testing.T) {
	state := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", state)
	case "darwin":
		t.Setenv("HOME", state)
	default:
		t.Setenv("XDG_CONFIG_HOME", state)
	}
	root, err := profile.UserDirectory()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := profile.Normalize(profile.App{
		ID: "app-0123456789abcdef", Name: "项目工作台", URL: "https://example.org/report?from=creator%20link#latest",
		Width: 936, Height: 648, WindowRevision: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	packaged := saved
	packaged.URL = "https://example.org/packaged"
	packaged.Width, packaged.WindowRevision = 860, 1
	other := packaged
	other.ID = "app-23456789abcdef01"
	store := profile.NewStore(filepath.Join(root, profile.Filename))
	for _, app := range []profile.App{other, saved} {
		if err := store.Put(app); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(state, "Applications", saved.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, Filename)
	encode := func(app profile.App) []byte {
		t.Helper()
		data, err := json.Marshal(app)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, tc := range []struct {
		name string
		data []byte
		want profile.App
	}{
		{"missing", nil, saved},
		{"damaged", []byte("{"), saved},
		{"different app", encode(other), saved},
		{"valid packaged settings", encode(packaged), packaged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.data != nil {
				if err := os.WriteFile(file, tc.data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := Load(dir); err != nil || got != tc.want {
				t.Fatalf("startup settings = %+v (%v), want %+v", got, err, tc.want)
			}
			actual, err := os.ReadFile(file)
			if tc.data == nil {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("the launcher wrote a config while reading fallback settings: %v", err)
				}
			} else if err != nil || string(actual) != string(tc.data) {
				t.Fatalf("reading startup settings changed the file: %v", err)
			}
		})
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, profile.ErrNotFound) {
		t.Fatalf("missing the app's own saved profile = %v", err)
	}
}
