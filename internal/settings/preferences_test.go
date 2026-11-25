package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreferencesRoundTripAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WebCrate", "preferences.json")
	assertPreferences(t, path, Default())
	custom := Preferences{Appearance: Dark, StartPage: LibraryPage, Width: 960, Height: 680}
	if err := Save(path, custom); err != nil {
		t.Fatal(err)
	}
	assertPreferences(t, path, custom)

	invalid := []Preferences{
		{Appearance: "sepia", StartPage: LibraryPage, Width: 960, Height: 680},
		{Appearance: Dark, StartPage: "other", Width: 960, Height: 680},
		{Appearance: Dark, StartPage: LibraryPage, Width: 639, Height: 680},
		{Appearance: Dark, StartPage: LibraryPage, Width: 960, Height: 1801},
	}
	for _, prefs := range invalid {
		if err := Save(path, prefs); err == nil {
			t.Fatalf("accepted invalid settings: %+v", prefs)
		}
		assertPreferences(t, path, custom)
	}
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}
	assertPreferences(t, path, Default())
}

func TestPreferencesLoadWithMissingOrDamagedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"appearance":"light","unrecognized":true}`)
	prefs := Default()
	prefs.Appearance = Light
	assertPreferences(t, path, prefs)

	for _, data := range []string{`{"appearance":`, `{"startPage":"other"}`, `{"windowWidth":0}`} {
		write(data)
		got, err := Load(path)
		if err == nil || got != Default() {
			t.Fatalf("read %s as %+v with error %v; want defaults and diagnostic", data, got, err)
		}
	}
	if err := Save(path, prefs); err != nil {
		t.Fatal(err)
	}
	assertPreferences(t, path, prefs)
}

func TestPreferencesFailedReplaceCleansStagingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Default()); err == nil {
		t.Fatal("replacing a directory succeeded")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "preferences.json" || !entries[0].IsDir() {
		t.Fatalf("unexpected entries after failed save: %v (%v)", entries, err)
	}
}

func assertPreferences(t *testing.T, path string, want Preferences) {
	t.Helper()
	got, err := Load(path)
	if err != nil || got != want {
		t.Fatalf("read %+v (%v), want %+v", got, err, want)
	}
}
