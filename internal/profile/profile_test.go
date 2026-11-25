package profile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWindowsAppUserModelID(t *testing.T) {
	const first = "app-0011223344556677"
	const second = "app-0011223344556688"
	if got, want := WindowsAppUserModelID(first), "WebCrate.Creator.App0011223344556677"; got != want || got == WindowsAppUserModelID(second) {
		t.Fatalf("AppUserModelID for %s is %q, want distinct ID %q", first, got, want)
	}
}

func TestParseAddress(t *testing.T) {
	for input, want := range map[string]string{
		"  example.com/docs  ":                                                "https://example.com/docs",
		"https://EXAMPLE.COM:443/work?query=1#start":                          "https://example.com:443/work?query=1#start",
		"example.com/session?return=https://login.example.org/continue#draft": "https://example.com/session?return=https://login.example.org/continue#draft",
		"example.com/archive/https://old.example.org/":                        "https://example.com/archive/https://old.example.org/",
		"[::1]:8443/board#https://docs.example.org/":                          "https://[::1]:8443/board#https://docs.example.org/",
		"https://例子.中国":                                                       "https://xn--fsqu00a.xn--fiqs8s",
		"http://localhost:3000/test":                                          "http://localhost:3000/test",
		"http://[::1]:8080/":                                                  "http://[::1]:8080/",
		"http://nas/board":                                                    "http://nas/board",
	} {
		t.Run(input, func(t *testing.T) {
			u, err := ParseAddress(input)
			if err != nil || u.String() != want {
				t.Fatalf("ParseAddress(%q) = %v, %v; want %s", input, u, err, want)
			}
		})
	}
	for _, value := range []string{"", "quick search", "quick", "file:///etc/hosts", "https://name:password@example.com", "https://example.com:0", "https://example.com:", "https://example.com:65536", "https://..com", "-site.com", "https://[bad::host]/", "http://[fe80::1%25en0]", "https://foo\\bar.com", "https://example.com/" + strings.Repeat("x", 2048)} {
		if _, err := ParseAddress(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestAddressSummary(t *testing.T) {
	for _, test := range []struct {
		address, host, summary string
	}{
		{"https://www.example.com/?view=board#today", "example.com", "example.com"},
		{"http://localhost:3000/workbench", "localhost", "localhost:3000/workbench"},
		{"http://[::1]:8080/path", "::1", "[::1]:8080/path"},
		{"http://[::1]/path", "::1", "[::1]/path"},
		{"https://www.xn--fsqu00a.xn--fiqs8s/work%2Ftoday", "例子.中国", "例子.中国/work%2Ftoday"},
		{"%/", "%/", "%/"},
	} {
		if host, summary := AddressSummary(test.address); host != test.host || summary != test.summary || host != Host(test.address) {
			t.Errorf("AddressSummary(%q) = %q, %q, want %q, %q", test.address, host, summary, test.host, test.summary)
		}
	}
}

func TestAutomaticArtworkMatchesTheHTTPResource(t *testing.T) {
	const base = "https://example.org/notes%23draft?view=all"
	app := App{URL: base + "#first"}
	if got := ArtworkAddress(app.URL); got != base {
		t.Fatalf("the artwork address lost the encoded path or query: %q", got)
	}
	for _, address := range []string{base, base + "#review", base + "#"} {
		if !app.UsesAutomaticIcon(address) {
			t.Errorf("the same HTTP resource did not reuse its artwork: %q", address)
		}
	}
	for _, address := range []string{
		"https://example.org/notes%23draft?view=recent#first",
		"https://example.org/notes#draft?view=all",
		"https://example.org/review?view=all#first",
	} {
		if app.UsesAutomaticIcon(address) {
			t.Errorf("accepted artwork fetched for a different HTTP resource: %q", address)
		}
	}
	app.CustomIcon = strings.Repeat("a", 64)
	if app.UsesAutomaticIcon(base) {
		t.Fatal("a website result could replace a chosen local icon")
	}
}

func TestNormalize(t *testing.T) {
	app, err := Normalize(App{URL: "www.example.com"})
	if err != nil || app.Name != "example.com" || app.URL != "https://www.example.com" || app.Category != "其他" || app.Color != DefaultColor || app.Width != DefaultWidth || app.Height != DefaultHeight {
		t.Fatalf("unexpected defaults: %+v, %v", app, err)
	}
	for _, bad := range []App{{URL: "search"}, {URL: "example.com", Width: 500}, {URL: "example.com", Height: 1801}, {URL: "example.com", Name: strings.Repeat("国", 49)}, {URL: "example.com", Name: "bad\nname"}} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if got := Host("https://www.xn--fsqu00a.xn--fiqs8s/"); got != "例子.中国" {
		t.Fatalf("IDN display = %q", got)
	}
	if got := Host("%/"); got != "%/" {
		t.Fatalf("unparsed host = %q", got)
	}
	if !ValidColor("#CED4FF") || ValidColor("#12zyac") || ValidColor("1234567") {
		t.Fatal("unexpected color validation")
	}
	if id := NewID(); !ValidID(id) || id == NewID() {
		t.Fatalf("invalid or repeated ID %q", id)
	}
	if ValidID("app-000000000000000G") || ValidID("app-000000000000000F") || ValidID("../anything") {
		t.Fatal("invalid ID accepted")
	}
}

func TestStoreLegacyAndConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apps.json")
	s := NewStore(path)
	if empty, err := s.List(); err != nil || len(empty) != 0 {
		t.Fatalf("new library: %v, %v", empty, err)
	}
	const legacyID = "app-0000000000000000"
	legacy := `[{"id":"app-0000000000000000","name":"旧应用","url":"https://example.com","category":"开发","width":800,"height":600,"glyph":"旧","light":true,"createdAt":"2025-01-01T01:02:03Z","updatedAt":"2025-01-01T01:02:03Z"}]`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := s.Find(legacyID)
	if err != nil || old.Name != "旧应用" || old.Glyph != "旧" || !old.Light || old.CreatedAt.Year() != 2025 {
		t.Fatalf("legacy app: %+v, %v", old, err)
	}
	old.Name = "改名"
	if err := s.Put(old); err != nil {
		t.Fatal(err)
	}
	if changed, _ := s.Find(legacyID); changed.Name != old.Name || changed.Glyph != old.Glyph {
		t.Fatalf("updated entry: %+v", changed)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := s.Put(App{ID: NewID(), URL: "https://example.com"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if apps, err := s.List(); err != nil || len(apps) != 9 {
		t.Fatalf("persisted %d entries, err %v", len(apps), err)
	}
	if err := s.Delete(legacyID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Find(legacyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("find removed profile: %v", err)
	}
	if err := s.Delete(legacyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete removed profile: %v", err)
	}
	if err := s.Put(App{ID: "invalid"}); err == nil {
		t.Fatal("store accepted invalid ID")
	}
}

func TestStoreKeepsSnapshotsIndependentAndReadsReplacements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apps.json")
	store := NewStore(path)
	app := App{ID: NewID(), Name: "Before", URL: "https://example.org"}
	if err := store.Put(app); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.List()
	if err != nil || len(snapshot) != 1 {
		t.Fatalf("reading initial snapshot: %v, %v", snapshot, err)
	}
	snapshot[0].Name = "Local change"
	if current, err := store.Find(app.ID); err != nil || current != app {
		t.Fatalf("caller changed the store's decoded data: %+v, %v", current, err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	app.Name = "Edited"
	if err := NewStore(path).Put(app); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if current, err := store.Find(app.ID); err != nil || current != app {
		t.Fatalf("equal-size replacement was not read: %+v, %v", current, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	app.Name = "Latest"
	updated := strings.Replace(string(data), `"Edited"`, `"Latest"`, 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if current, err := store.Find(app.ID); err != nil || current != app {
		t.Fatalf("in-place edit with the original length and timestamp was not read: %+v, %v", current, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Find(app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed library still has a cached profile: %v", err)
	}
}

func TestStoreKeepsFirstMatchingRecordAcrossWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apps.json")
	first := App{ID: NewID(), Name: "First", URL: "https://example.org/first"}
	following := App{ID: NewID(), Name: "Following", URL: "https://example.org/following"}
	duplicate := App{ID: first.ID, Name: "Old duplicate", URL: "https://example.org/older"}
	data, err := json.Marshal([]App{first, following, duplicate})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	if app, err := store.Find(first.ID); err != nil || app != first {
		t.Fatalf("the first of two matching profiles was not loaded: %+v, %v", app, err)
	}
	first.Name = "Updated first"
	if err := store.Put(first); err != nil {
		t.Fatal(err)
	}
	if apps, err := store.List(); err != nil || !slices.Equal(apps, []App{first, following, duplicate}) {
		t.Fatalf("updating the first match changed another profile or the order: %+v, %v", apps, err)
	}
	if err := store.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	if app, err := store.Find(first.ID); err != nil || app != duplicate {
		t.Fatalf("the remaining profile was not found after deletion: %+v, %v", app, err)
	}
	if app, err := store.Find(following.ID); err != nil || app != following {
		t.Fatalf("the index did not follow the record moved by deletion: %+v, %v", app, err)
	}
}

func TestStoreCachesTheCommittedRepresentation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apps.json")
	store := NewStore(path)
	app := App{
		ID: NewID(), Name: "Example\xff", URL: "https://example.org",
		CreatedAt: time.Now(), UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("Office", 8*60*60)),
	}
	if err := store.Put(app); err != nil {
		t.Fatal(err)
	}
	onDisk, err := NewStore(path).Find(app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk == app || !slices.Equal(store.lastProfiles, []App{onDisk}) {
		t.Fatalf("the cached snapshot differs from the file: %+v, on disk %+v", store.lastProfiles, onDisk)
	}
	if cached, err := store.Find(app.ID); err != nil || cached != onDisk {
		t.Fatalf("reading the cached record: %+v (%v), want %+v", cached, err, onDisk)
	}

	app.UpdatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Put(app); err == nil {
		t.Fatal("saved an unencodable record")
	}
	if cached, err := store.Find(app.ID); err != nil || cached != onDisk {
		t.Fatalf("the failed write changed the committed record: %+v (%v)", cached, err)
	}
}

func TestStoreDoesNotServeCachedProfilesAfterReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apps.json")
	store := NewStore(path)
	wanted := App{ID: NewID(), Name: "Workspace", URL: "https://example.org/"}
	if err := store.Put(wanted); err != nil {
		t.Fatal(err)
	}
	if app, err := store.Find(wanted.ID); err != nil || app != wanted {
		t.Fatalf("seed profile: %+v, %v", app, err)
	}
	backup := path + ".backup"
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if app, err := store.Find(wanted.ID); err == nil || app.ID != "" {
		t.Fatalf("a directory at the profile path exposed a stale profile: %+v, %v", app, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if app, err := store.Find(wanted.ID); err == nil || app.ID != "" {
		t.Fatalf("an empty profile file exposed a stale profile: %+v, %v", app, err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	if app, err := store.Find(wanted.ID); err != nil || app != wanted {
		t.Fatalf("restored profile: %+v, %v", app, err)
	}
}

func TestStorePreservesUnreadableData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apps.json")
	const bad = `[{` // an interrupted write from an older installation
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	if _, err := s.List(); err == nil {
		t.Fatal("read succeeded with a malformed profile")
	}
	if _, err := s.Find(NewID()); err == nil {
		t.Fatal("find succeeded with a malformed profile")
	}
	if err := s.Put(App{ID: NewID()}); err == nil {
		t.Fatal("write succeeded with a malformed profile")
	}
	if err := s.Delete(NewID()); err == nil {
		t.Fatal("delete succeeded with a malformed profile")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != bad {
		t.Fatalf("old data changed: %q, %v", data, err)
	}
}
