//go:build darwin || windows || linux

package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

func releaseFixture(t testing.TB) (*Library, *library.Service) {
	t.Helper()
	root := t.TempDir()
	backend, err := NewBackend(filepath.Join("..", "..", "resources", runtime.GOOS+"-"+runtime.GOARCH), filepath.Join(root, "Runtimes"))
	if err != nil {
		if os.Getenv("WEBCRATE_GUI_SMOKE") == "1" {
			t.Fatalf("prepare the release helper with `go run ./cmd/package -prepare`: %v", err)
		}
		t.Skipf("prepare the release helper with `go run ./cmd/package -prepare`: %v", err)
	}
	bundles := NewLibrary(filepath.Join(root, "Applications"), backend)
	return bundles, library.New(profile.NewStore(filepath.Join(root, "apps.json")), bundles,
		library.Options{CustomIcons: icons.NewCustomStore(filepath.Join(root, "CustomIcons"))})
}

// IndexedDB's strict durability lets the fixture assert persistence even on
// platforms where stopping a test process does not flush localStorage.
const websiteFixture = `<title>WebCrate smoke fixture</title><h1>Ready</h1><script>
const report = (state) => fetch('/ready', {method: 'POST', body: JSON.stringify({
  state, width: window.innerWidth, height: window.innerHeight,
})});
const opening = indexedDB.open('webcrate-smoke', 1);
opening.onupgradeneeded = () => opening.result.createObjectStore('settings');
opening.onerror = () => report(String(opening.error));
opening.onsuccess = () => {
  const db = opening.result;
  const transaction = db.transaction('settings', 'readwrite', {durability: 'strict'});
  const settings = transaction.objectStore('settings');
  const before = settings.get('fixture');
  before.onsuccess = () => settings.put('remembered', 'fixture');
  transaction.onabort = () => report(String(transaction.error));
  transaction.oncomplete = () => {
    db.close();
    report(before.result || 'new');
  };
};
</script>`

type websiteReport struct {
	State  string `json:"state"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func standaloneWebsite(t *testing.T) (string, <-chan websiteReport) {
	t.Helper()
	if runtime.GOOS == "linux" {
		t.Setenv("no_proxy", "127.0.0.1,::1,localhost")
		t.Setenv("NO_PROXY", "127.0.0.1,::1,localhost")
	}
	seen := make(chan websiteReport, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, websiteFixture)
	})
	mux.HandleFunc("POST /ready", func(w http.ResponseWriter, req *http.Request) {
		var report websiteReport
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 512)).Decode(&report); err != nil {
			http.Error(w, "invalid fixture response", http.StatusBadRequest)
			return
		}
		select {
		case seen <- report:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL, seen
}

func TestMyGoStandaloneReusesRunningInstance(t *testing.T) {
	if os.Getenv("WEBCRATE_GUI_SMOKE") != "1" {
		t.Skip("requires a desktop session and system WebView")
	}
	root := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", root)
	case "linux":
		t.Setenv("XDG_CACHE_HOME", root)
		t.Setenv("XDG_CONFIG_HOME", root)
	case "darwin":
		out, err := exec.CommandContext(t.Context(), "/usr/bin/sw_vers", "-productVersion").Output()
		if err != nil {
			t.Fatal(err)
		}
		major, _, _ := strings.Cut(strings.TrimSpace(string(out)), ".")
		version, err := strconv.Atoi(major)
		if err != nil {
			t.Fatal(err)
		}
		if version < 14 {
			t.Skip("MyGo's named WebKit stores require macOS 14 for persistence across launches")
		}
		t.Setenv("CFFIXED_USER_HOME", root)
	}
	website, seen := standaloneWebsite(t)
	icon, err := icons.PNG(icons.Brand())
	if err != nil {
		t.Fatal(err)
	}
	makeApp := func(folder string) string {
		t.Helper()
		dir := filepath.Join(root, folder)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		app := profile.App{ID: profile.NewID(), Name: "Creator instance test", URL: website, Width: 680, Height: 490}
		if err := writeConfig(filepath.Join(dir, ConfigName), app, iconMetadata{}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "AppIcon.png"), icon, 0o644); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "darwin" {
			t.Cleanup(func() {
				config, _ := os.UserConfigDir()
				if config != "" {
					_ = os.RemoveAll(filepath.Join(config, "WebCrate "+app.ID))
				}
			})
		}
		if runtime.GOOS == "windows" {
			return filepath.Join(dir, "website.exe")
		}
		return filepath.Join(dir, "website")
	}
	a, b := makeApp("A"), makeApp("B")
	// Compile the portable runner entry point by filename so that the same
	// main can be exercised with a real WebView on a macOS development host.
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", a, "../../cmd/runner/main.go").CombinedOutput(); err != nil {
		t.Fatalf("build MyGo website runner: %v (%s)", err, out)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	checkWebsite(t, a, seen, "new", func() {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		repeat := exec.CommandContext(ctx, a)
		repeat.WaitDelay = time.Second
		if out, err := repeat.CombinedOutput(); err != nil {
			t.Fatalf("second launch did not hand off to the running app: %v (%s)", err, out)
		}
		select {
		case report := <-seen:
			t.Fatalf("launching an open application loaded the site again: %+v", report)
		default:
		}
		checkWebsite(t, b, seen, "new", nil)
	})
	checkWebsite(t, a, seen, "remembered", nil)
}

func checkStandaloneSessions(t *testing.T, executable func(string) string) {
	t.Helper()
	bundles, service := releaseFixture(t)
	website, seen := standaloneWebsite(t)
	makeApp := func(name string) (profile.App, string) {
		t.Helper()
		app, err := service.Save(t.Context(), profile.App{Name: name, URL: website, Width: 640, Height: 480}, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
		if err != nil {
			t.Fatal(err)
		}
		path, err := bundles.Path(app)
		if err != nil {
			t.Fatal(err)
		}
		return app, executable(path)
	}
	app, a := makeApp("WebCrate smoke A")
	_, b := makeApp("WebCrate smoke B")
	// The generated apps run from their own folders after the manager's
	// shared name is removed.
	for _, source := range []string{"WebCrateRunner", "WebCrateRunner.exe"} {
		shared := filepath.Join(filepath.Dir(bundles.root), "Runtimes", source)
		if err := os.Remove(shared); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	seedLegacyWindow(t, app.ID)
	legacy := checkWebsite(t, a, seen, "new", nil)
	defaultWindow := checkWebsite(t, b, seen, "new", nil)
	if runtime.GOOS == "darwin" {
		// A standalone app built before runtime updates had no digest. Keep
		// its WebKit data store when the manager replaces its signed bundle.
		path, err := bundles.Path(app)
		if err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(path, "Contents", "Resources", runtimeMarker)
		if err := os.Remove(marker); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Path(t.Context(), app, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("runtime was not updated: %v", err)
		}
	}
	checkWebsite(t, a, seen, "remembered", nil)
	if legacy.Width <= defaultWindow.Width+20 || legacy.Height <= defaultWindow.Height+20 {
		t.Fatalf("existing window placement was not restored: got %+v, default %+v", legacy, defaultWindow)
	}

	resize := func(width, height int) websiteReport {
		t.Helper()
		app.Width, app.Height = width, height
		saved, err := service.Save(t.Context(), app, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
		if err != nil {
			t.Fatal(err)
		}
		app = saved
		return checkWebsite(t, a, seen, "remembered", nil)
	}
	larger := resize(840, 620)
	if larger.Width <= legacy.Width+20 || larger.Height <= legacy.Height+20 {
		t.Fatalf("window did not open with the new dimensions: got %+v, previously %+v", larger, legacy)
	}
	reverted := resize(640, 480)
	if reverted.Width != defaultWindow.Width || reverted.Height != defaultWindow.Height {
		t.Fatalf("returning to the original dimensions restored the old layout: got %+v, want %+v", reverted, defaultWindow)
	}
	t.Logf("website viewport after reopening: saved layout %d×%d, edited %d×%d, reverted %d×%d",
		legacy.Width, legacy.Height, larger.Width, larger.Height, reverted.Width, reverted.Height)
}

func seedLegacyWindow(t *testing.T, id string) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		domain, key := "com.webcrate.apps."+id, "NSWindow Frame Website-640-480"
		out, err := exec.Command("/usr/bin/defaults", "write", domain, key, "-string", "100 100 720 570 0 0 1280 800 ").CombinedOutput()
		if err != nil {
			t.Fatalf("save native test window placement: %v (%s)", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("/usr/bin/defaults", "delete", domain, key).Run() })
		return
	}
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(config, "WebCrate "+id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"main":{"x":100,"y":100,"width":720,"height":570}}`)
	if err := os.WriteFile(filepath.Join(dir, "window-state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func checkWebsite(t *testing.T, path string, seen <-chan websiteReport, want string, whileOpen func()) websiteReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path)
	cmd.WaitDelay = time.Second
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var exitErr error
	go func() {
		exitErr = cmd.Wait()
		close(done)
	}()
	defer func() { _ = cmd.Process.Kill(); <-done }()
	select {
	case got := <-seen:
		if got.State != want || got.Width <= 0 || got.Height <= 0 {
			t.Fatalf("website state and dimensions = %+v, want %q and a visible window", got, want)
		}
		if whileOpen != nil {
			whileOpen()
			select {
			case <-done:
				t.Fatalf("open application exited: %v (%s)", exitErr, &output)
			default:
			}
		}
		return got
	case <-done:
		t.Fatalf("native website runner exited: %v (%s)", exitErr, &output)
	case <-ctx.Done():
		<-done
		t.Fatalf("native website runner did not execute the fixture: %s", &output)
	}
	return websiteReport{}
}
