package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestLinuxInstalledWebsiteAddressActions(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	launched := linuxBrowserProbe(t)
	var reachable atomic.Bool
	reports := make(chan string, 8)
	const home = "/start?from=creator#top"
	const later = "/later?from=app%20link#notes"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/report" {
			select {
			case reports <- r.URL.Query().Get("value"):
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/later" && !reachable.Load() {
			if socket, _, err := http.NewResponseController(w).Hijack(); err == nil {
				_ = socket.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><title>Address actions</title>
<style>body { margin: 0 } input, a { position: absolute; left: 24px; width: 260px; height: 40px }
input { top: 12px } a { top: 100px }</style>
<input id="draft" aria-label="Draft">
<a href="/later?from=app%20link#notes">Open another page</a>
<script>
function report(value) { fetch('/report?' + new URLSearchParams({value})); }
const draft = document.querySelector('#draft');
draft.addEventListener('paste', event => {
    event.preventDefault();
    report(draft.value + '\n' + event.clipboardData.getData('text/plain'));
});
addEventListener('pageshow', () => report(location.pathname + location.search + location.hash));
</script>`)
	}))
	t.Cleanup(server.Close)
	window, pid := openLinuxWebsiteFixture(t, installed, state, "Creator address actions fixture", server.URL+home)
	press := func(key string) {
		t.Helper()
		linuxXDo(t, "windowfocus", "--sync", window, "key", "--clearmodifiers", key)
	}
	menu := func(label string) {
		t.Helper()
		press("F10")
		findLinuxControl(t, pid, label).click(t)
	}
	expectPage := func(want string) {
		t.Helper()
		select {
		case got := <-reports:
			if got != want {
				t.Fatalf("the website reported %q, want %q", got, want)
			}
		case <-time.After(6 * time.Second):
			t.Fatalf("the website did not report %q", want)
		}
	}
	focusDraft := func() {
		t.Helper()
		findLinuxControl(t, pid, "Draft").click(t)
	}
	writeDraft := func() {
		t.Helper()
		focusDraft()
		linuxXDo(t, "type", "--clearmodifiers", "unsaved draft")
	}
	paste := func(want string) {
		t.Helper()
		focusDraft()
		press("ctrl+v")
		expectPage("unsaved draft\n" + want)
	}

	expectPage(home)
	writeDraft()
	menu("复制网页链接")
	menu("在浏览器中打开")
	expectLinuxBrowserAddress(t, launched, server.URL+home)
	paste(server.URL + home)

	findLinuxControl(t, pid, "Open another page").click(t)
	findLinuxControl(t, pid, "重新载入")
	menu("复制网页链接")
	menu("在浏览器中打开")
	expectLinuxBrowserAddress(t, launched, server.URL+later)
	reachable.Store(true)
	findLinuxControl(t, pid, "重新载入").click(t)
	expectPage(later)
	writeDraft()
	paste(server.URL + later)
	if current := linuxFixtureWindow(t, "Creator address actions fixture"); current != window {
		t.Fatalf("opening an address changed the embedded window from %s to %s", window, current)
	}
	press("ctrl+w")
	waitForLinuxTestAppClosed(t, window, pid)
	t.Log("The visible page and the failed destination preserved their full URLs through the clipboard and the default browser.")
}

func TestLinuxInstalledWebsiteRuntimeUnavailable(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	launched := linuxBrowserProbe(t)
	address, rendered := installedWebsiteFixture(t)
	address += "#workspace"
	service := portableInstalledLibrary(t, installed, state)
	app, err := service.Save(t.Context(), profile.App{Name: "Creator runtime recovery", URL: address},
		library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Remove(app.ID) })
	executable, err := service.Path(t.Context(), app, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, icon := generatedMenu(t, app, executable)
	checkHealthy := func(session string) {
		t.Helper()
		launchPortableMenu(t, entry, executable, app.Name, icon)
		if report := checkLinuxInstalledReport(t, rendered, session); report.URL != address {
			t.Fatalf("the recovered application loaded %q, want %q", report.URL, address)
		}
		window := assertLinuxDesktopWindow(t, app, entry, executable)
		closeLinuxTestWindow(t, window, linuxWindowPID(t, window))
	}
	checkHealthy("new")

	// Make both WebKit sonames fail to load only for this launch.
	originalLibraries := os.Getenv("LD_LIBRARY_PATH")
	libraries := t.TempDir()
	for _, name := range []string{"libwebkit2gtk-4.1.so.0", "libwebkit2gtk-4.0.so.37"} {
		if err := os.WriteFile(filepath.Join(libraries, name), []byte("unavailable"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LD_LIBRARY_PATH", libraries)
	launchPortableMenu(t, entry, executable, app.Name, icon)
	window := assertLinuxDesktopWindow(t, app, entry, executable)
	pid := linuxWindowPID(t, window)
	findLinuxControl(t, pid, "无法启动网页组件")
	findLinuxControl(t, pid, "请检查系统的 WebKitGTK，然后重新打开应用。")
	if _, err := os.Stat(launched); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the default browser opened without user input: %v", err)
	}
	findLinuxControl(t, pid, "在浏览器中打开").click(t)
	expectLinuxBrowserAddress(t, launched, address)
	linuxXDo(t, "windowfocus", "--sync", window, "key", "--clearmodifiers", "ctrl+w")
	waitForLinuxTestAppClosed(t, window, pid)

	t.Setenv("LD_LIBRARY_PATH", originalLibraries)
	checkHealthy("remembered")
	t.Log("The installed application explained the WebKitGTK error, opened the saved URL only on request, and recovered its session from the original desktop entry.")
}

func expectLinuxBrowserAddress(t *testing.T, path, want string) {
	t.Helper()
	var last []byte
	for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		last, _ = os.ReadFile(path)
		if strings.HasSuffix("\n"+string(last), "\n"+want+"\n") {
			return
		}
	}
	t.Fatalf("the system browser received %q, want %q", last, want)
}

func linuxBrowserProbe(t *testing.T) string {
	t.Helper()
	config := os.Getenv("XDG_CONFIG_HOME")
	applications := filepath.Join(os.Getenv("XDG_DATA_HOME"), "applications")
	for _, path := range []string{config, applications} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "record-browser")
	const handler = "org.creator.BrowserProbe.desktop"
	desktop := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Creator browser fixture\nExec=/bin/sh %s %%u\nMimeType=x-scheme-handler/http;x-scheme-handler/https;\n", script)
	for path, content := range map[string]string{
		filepath.Join(config, "mimeapps.list"): "[Default Applications]\nx-scheme-handler/http=" + handler + "\nx-scheme-handler/https=" + handler + "\n",
		filepath.Join(applications, handler):   desktop,
		script:                                 "printf '%s\\n' \"$1\" >> \"$(dirname -- \"$0\")/browser-addresses\"\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const checkDefault = `import sys
from gi.repository import Gio
app = Gio.AppInfo.get_default_for_uri_scheme(sys.argv[1])
if app is not None:
    print(app.get_id())`
	for _, scheme := range []string{"http", "https"} {
		actual := strings.TrimSpace(linuxWindowCommand(t, "/usr/bin/python3", "-c", checkDefault, scheme))
		if actual != handler {
			t.Fatalf("the default browser for %s is outside the isolated fixture: %q", scheme, actual)
		}
	}
	return filepath.Join(dir, "browser-addresses")
}
