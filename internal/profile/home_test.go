package profile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSavedHomeReadsCommittedProfile(t *testing.T) {
	base := t.TempDir()
	config := base
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", base)
		config = filepath.Join(base, "Library", "Application Support")
	case "windows":
		t.Setenv("APPDATA", base)
	default:
		t.Setenv("XDG_CONFIG_HOME", base)
	}
	root, err := UserDirectory()
	if want := filepath.Join(config, "WebCrate"); err != nil || root != want {
		t.Fatalf("library location = %q (%v), want %q", root, err, want)
	}
	path := filepath.Join(root, Filename)
	store := NewStore(path)
	app := App{ID: NewID(), URL: "https://example.org/app?view=work#today"}
	if SavedHome(app.ID) != "" {
		t.Fatal("a standalone app without a library should use its existing address")
	}
	for _, address := range []string{app.URL, "https://example.org/notes?view=saved#review", "file:///tmp/notes"} {
		app.URL = address
		if err := store.Put(app); err != nil {
			t.Fatal(err)
		}
		want := address
		if address == "file:///tmp/notes" {
			want = ""
		}
		if got := SavedHome(app.ID); got != want {
			t.Fatalf("saved homepage %q resolved to %q, want %q", address, got, want)
		}
	}
	if SavedHome(NewID()) != "" || SavedHome("another app") != "" {
		t.Fatal("a different app used a saved address")
	}
	if err := os.WriteFile(path, []byte(`[{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if SavedHome(app.ID) != "" {
		t.Fatal("a corrupt profile was used as a homepage")
	}
}
