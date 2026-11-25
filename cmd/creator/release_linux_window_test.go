//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestLinuxInstalledWebsiteInteractions(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("LC_ALL", "C.UTF-8")
	releaseOpening := make(chan struct{})
	fixture, online, seen := linuxWindowFixture(t, releaseOpening)
	const name = "Creator window fixture"
	window, pid := openLinuxWebsiteFixture(t, installed, state, name, fixture+"/home?source=initial")

	opening := findLinuxControl(t, pid, "正在载入…")
	if opening.Focused {
		t.Fatal("the opening hint took keyboard focus")
	}
	close(releaseOpening)
	initial := findLinuxControl(t, pid, "重新载入")
	if !initial.Focused {
		t.Fatal("the initial connection error did not focus the retry button")
	}
	linuxAccessibleControl(t, pid, "正在载入…", "disappear")
	online.Store(true)
	linuxXDo(t, "windowfocus", "--sync", window, "key", "Return")
	assertLinuxWebsiteReport(t, seen, "/home", "?source=initial")
	t.Log("the initial title bar hint cleared on failure, then keyboard retry opened the saved URL in the same window")

	online.Store(false)
	linuxXDo(t, "mousemove", "--window", window, "130", "150", "click", "1")
	retry := findLinuxControl(t, pid, "重新载入")
	online.Store(true)
	retry.click(t)
	assertLinuxWebsiteReport(t, seen, "/target", "?source=link")
	t.Log("clicking retry recovered the destination URL and preserved the website session")

	for _, mode := range []struct{ name, y string }{{"direct", "150"}, {"blank", "335"}} {
		linuxXDo(t, "mousemove", "--window", window, "130", mode.y, "click", "1")
		popup := linuxFixtureWindow(t, "Creator sign-in fixture")
		if popup == window {
			t.Fatal("sign-in replaced the main window")
		}
		assertLinuxWebsiteReport(t, seen, "/signin", "?source="+mode.name)
		linuxXDo(t, "mousemove", "--window", popup, "130", "150", "click", "1")
		assertLinuxWebsiteReport(t, seen, "callback", "?source=link")
		waitForLinuxWindowClosed(t, popup)
		t.Logf("the %s sign-in popup shared the session, notified its opener and closed itself", mode.name)
	}
	if main := linuxFixtureWindow(t, name); main != window {
		t.Fatalf("the main window changed from %s to %s", window, main)
	}
	closeLinuxTestWindow(t, window, pid)
}

func TestLinuxInstalledWebsiteNavigation(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	fixture, online, seen := linuxWindowFixture(t, nil)
	online.Store(true)
	const name = "Creator navigation fixture"
	const home = "?source=navigation#workspace"
	root := filepath.Join(state, "config", "WebCrate")
	window, pid := openLinuxWebsiteFixture(t, installed, root, name, fixture+"/home"+home)
	assertLinuxWebsiteReport(t, seen, "/home", home)
	press := func(key string) {
		t.Helper()
		linuxXDo(t, "windowfocus", "--sync", window, "key", "--clearmodifiers", key)
	}
	press("alt+Left")
	press("alt+Right")
	press("alt+Home")
	press("j")
	assertLinuxWebsiteReport(t, seen, "keyboard", home)
	linuxXDo(t, "mousemove", "--window", window, "130", "150", "click", "1")
	assertLinuxWebsiteReport(t, seen, "/target", "?source=link")
	press("alt+Left")
	assertLinuxWebsiteReport(t, seen, "/home", home)
	press("alt+Right")
	assertLinuxWebsiteReport(t, seen, "/target", "?source=link")
	press("ctrl+r")
	assertLinuxWebsiteReport(t, seen, "/target", "?source=link")
	t.Log("the native back, forward and reload shortcuts navigated while the page had the keyboard")
	press("F10")
	findLinuxControl(t, pid, "回到首页").click(t)
	assertLinuxWebsiteReport(t, seen, "/home", home)
	press("alt+Left")
	assertLinuxWebsiteReport(t, seen, "/target", "?source=link")

	online.Store(false)
	press("ctrl+r")
	findLinuxControl(t, pid, "重新载入")
	online.Store(true)
	press("ctrl+r")
	assertLinuxWebsiteReport(t, seen, "/target", "?source=link")
	press("j")
	assertLinuxWebsiteReport(t, seen, "keyboard", "?source=link")
	press("alt+Left")
	assertLinuxWebsiteReport(t, seen, "/home", home)
	press("alt+Right")
	assertLinuxWebsiteReport(t, seen, "/target", "?source=link")
	if main := linuxFixtureWindow(t, name); main != window {
		t.Fatalf("reloading changed the main window from %s to %s", window, main)
	}
	t.Log("the reload shortcut recovered the disconnected page and kept its query, cookie, history and keyboard focus")

	online.Store(false)
	press("ctrl+r")
	findLinuxControl(t, pid, "重新载入")
	online.Store(true)
	press("alt+Left")
	assertLinuxWebsiteReport(t, seen, "/home", home)
	press("j")
	assertLinuxWebsiteReport(t, seen, "keyboard", home)
	t.Log("going back from the recovery panel restored the preceding page and its keyboard input")

	online.Store(false)
	linuxXDo(t, "mousemove", "--window", window, "130", "150", "click", "1")
	findLinuxControl(t, pid, "重新载入")
	online.Store(true)
	press("alt+Home")
	assertLinuxWebsiteReport(t, seen, "/home", home)
	press("j")
	assertLinuxWebsiteReport(t, seen, "keyboard", home)
	if main := linuxFixtureWindow(t, name); main != window || linuxWindowPID(t, main) != pid {
		t.Fatal("going home replaced the WebKit window or process")
	}
	t.Log("the menu and Alt+Home returned to the saved path, query and fragment, and kept keyboard focus, history and the session")

	linuxXDo(t, "mousemove", "--window", window, "130", "150", "click", "1")
	assertLinuxWebsiteReport(t, seen, "/target", "?source=link")
	profiles := profile.NewStore(filepath.Join(root, profile.Filename))
	apps, err := profiles.List()
	if err != nil || len(apps) != 1 {
		t.Fatalf("the test app was not saved: %v (%v)", apps, err)
	}
	const savedHome = "?source=saved#review"
	edited := apps[0]
	edited.URL = fixture + "/target" + savedHome
	backend, err := bundle.NewBackend(filepath.Dir(installed), filepath.Join(root, "Runtimes"))
	if err != nil {
		t.Fatal(err)
	}
	bundles := bundle.NewLibrary(filepath.Join(root, "Generated applications"), backend)
	previous, err := bundles.Path(apps[0])
	if err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("fixture profile write failed")
	if _, err := bundles.UpdateSettings(t.Context(), apps[0], edited, nil, func() error {
		assertPackagedHome(t, filepath.Join(filepath.Dir(previous), bundle.ConfigName), edited.URL)
		press("alt+Home")
		assertLinuxWebsiteReport(t, seen, "/home", home)
		return rejected
	}); !errors.Is(err, rejected) {
		t.Fatalf("saving a rejected homepage returned %v", err)
	}
	assertPackagedHome(t, filepath.Join(filepath.Dir(previous), bundle.ConfigName), apps[0].URL)
	linuxXDo(t, "mousemove", "--window", window, "130", "150", "click", "1")
	assertLinuxWebsiteReport(t, seen, "/target", "?source=link")
	service := portableInstalledLibrary(t, installed, root)
	if saved, err := service.Save(t.Context(), edited, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}}); err != nil || saved.URL != edited.URL {
		t.Fatalf("saving the new homepage returned %+v (%v)", saved, err)
	}
	press("j")
	assertLinuxWebsiteReport(t, seen, "keyboard", "?source=link")
	press("alt+Home")
	assertLinuxWebsiteReport(t, seen, "/target", savedHome)
	press("j")
	assertLinuxWebsiteReport(t, seen, "keyboard", savedHome)
	if main := linuxFixtureWindow(t, name); main != window || linuxWindowPID(t, main) != pid {
		t.Fatal("changing the default homepage replaced the WebKit window or process")
	}
	t.Log("the active window used the new homepage on demand and kept its session and keyboard focus")
	press("ctrl+w")
	waitForLinuxTestAppClosed(t, window, pid)
}

func TestLinuxInstalledWebsiteRecovery(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	fixture, online, seen := linuxWindowFixture(t, nil)
	online.Store(true)
	const name = "Creator renderer fixture"
	window, pid := openLinuxWebsiteFixture(t, installed, state, name, fixture+"/home?source=recovery")
	runnerPID, err := strconv.Atoi(pid)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := os.FindProcess(runnerPID)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = runner.Kill()
		}
		_ = runner.Release()
	})
	assertLinuxWebsiteReport(t, seen, "/home", "?source=recovery")
	linuxXDo(t, "mousemove", "--window", window, "130", "150", "click", "1")
	assertLinuxWebsiteReport(t, seen, "/target", "?source=link")

	terminateLinuxFixtureRenderer(t, pid)
	findLinuxControl(t, pid, "重新载入").click(t)
	assertLinuxWebsiteReport(t, seen, "/target", "?source=link")
	if got := linuxFixtureWindow(t, name); got != window || linuxWindowPID(t, got) != pid {
		t.Fatal("renderer recovery replaced the installed application's window or process")
	}
	linuxXDo(t, "key", "j")
	assertLinuxWebsiteReport(t, seen, "keyboard", "?source=link")
	linuxXDo(t, "key", "alt+Left")
	assertLinuxWebsiteReport(t, seen, "/home", "?source=recovery")
	t.Log("restarting the WebKit renderer restored the URL, session, history and focus in the same native window")
	closeLinuxTestWindow(t, window, pid)
	closed = true
}

func terminateLinuxFixtureRenderer(t *testing.T, runnerPID string) {
	t.Helper()
	parents := make(map[string]string)
	for line := range strings.SplitSeq(linuxWindowCommand(t, "ps", "-e", "-o", "pid=,ppid="), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			parents[fields[0]] = fields[1]
		}
	}
	var renderers []string
	for pid := range parents {
		// Check ancestry before inspecting an executable. The guest can be
		// running other WebKit apps using the same executable.
		for ancestor := parents[pid]; ancestor != ""; ancestor = parents[ancestor] {
			if ancestor == runnerPID {
				path, err := os.Readlink(filepath.Join("/proc", pid, "exe"))
				if err == nil && filepath.Base(path) == "WebKitWebProcess" {
					renderers = append(renderers, pid)
				}
				break
			}
		}
	}
	if len(renderers) != 1 {
		t.Fatalf("want one WebKit content process descended from fixture %s, found %v", runnerPID, renderers)
	}
	pid, err := strconv.Atoi(renderers[0])
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Release()
	if err := renderer.Kill(); err != nil {
		t.Fatalf("stop the fixture renderer: %v", err)
	}
}

func openLinuxWebsiteFixture(t *testing.T, installed, state, name, address string) (window, pid string) {
	t.Helper()
	return linuxWebsiteFixtureLauncher(t, installed, state, name, address)()
}

func linuxWebsiteFixtureLauncher(t *testing.T, installed, state, name, address string) func() (window, pid string) {
	t.Helper()
	service := portableInstalledLibrary(t, installed, state)
	app, err := service.Save(t.Context(), profile.App{
		Name: name, URL: address, Width: 680, Height: 490,
	}, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Remove(app.ID) })
	executable, err := service.Path(t.Context(), app, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, icon := generatedMenu(t, app, executable)
	return func() (window, pid string) {
		t.Helper()
		launchPortableMenu(t, entry, executable, app.Name, icon)
		window = linuxFixtureWindow(t, name)
		return window, linuxWindowPID(t, window)
	}
}

type linuxWindowReport struct{ page, suffix, session string }

func assertLinuxWebsiteReport(t *testing.T, seen <-chan linuxWindowReport, page, suffix string) {
	t.Helper()
	want := linuxWindowReport{page, suffix, "signed-in"}
	select {
	case report := <-seen:
		if report != want {
			t.Fatalf("website reported %+v, want %+v", report, want)
		}
	case <-time.After(12 * time.Second):
		t.Fatalf("site did not render the page and report %+v", want)
	}
}

func linuxWindowFixture(t *testing.T, releaseOpening <-chan struct{}) (string, *atomic.Bool, <-chan linuxWindowReport) {
	t.Helper()
	var online atomic.Bool
	seen := make(chan linuxWindowReport, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/report" {
			query := r.URL.Query()
			report := linuxWindowReport{page: query.Get("page"), suffix: query.Get("suffix")}
			if cookie, err := r.Cookie("creator_fixture"); err == nil {
				report.session = cookie.Value
			}
			select {
			case seen <- report:
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if releaseOpening != nil && r.URL.Path == "/home" {
			select {
			case <-releaseOpening:
			case <-r.Context().Done():
				return
			}
		}
		if !online.Load() {
			// A disconnected connection exercises WebKit's failure callbacks.
			if connection, _, err := http.NewResponseController(w).Hijack(); err == nil {
				_ = connection.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var title, action string
		switch r.URL.Path {
		case "/home":
			http.SetCookie(w, &http.Cookie{Name: "creator_fixture", Value: "signed-in", Path: "/", HttpOnly: true})
			title = "Creator website fixture"
			action = `<a href="/target?source=link">Continue</a>`
		case "/target":
			title = "Creator destination fixture"
			action = `<button class="sign-in" onclick="openLogin(false)">Sign in</button>
<button class="sign-in" onclick="openLogin(true)">Open blank, then sign in</button>
<script>
let login;
function openLogin(blank) {
    const address = '/signin?source=' + (blank ? 'blank' : 'direct');
    login = window.open(blank ? 'about:blank' : address);
    if (blank && login) login.location = address;
}
addEventListener('message', event => {
    if (event.origin === location.origin && event.source === login && event.data === 'signed-in') {
        report('callback');
    }
});
</script>`
		case "/signin":
			title = "Creator sign-in fixture"
			action = `<button onclick="window.opener?.postMessage('signed-in', location.origin); window.close()">Complete sign-in</button>`
		default:
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `<!doctype html><title>%s</title><style>
body { margin: 0; } a, button { display: block; width: 95vw; height: 80vh; }
.sign-in { height: 45vh; }
</style>%s<script>
function report(page) {
    fetch('/report?' + new URLSearchParams({page, suffix: location.search + location.hash}));
}
window.addEventListener('keydown', event => {
    if (event.key === 'j') report('keyboard');
});
window.addEventListener('pageshow', () => requestAnimationFrame(() => requestAnimationFrame(() => {
    report(window.webkit?.messageHandlers?.mygo ? 'native-bridge' : location.pathname);
})));
</script>`, title, action)
	}))
	t.Cleanup(server.Close)
	return server.URL, &online, seen
}

func linuxFixtureWindow(t *testing.T, name string) string {
	t.Helper()
	ids := strings.Fields(linuxXDo(t, "search", "--sync", "--onlyvisible", "--name", "^"+regexp.QuoteMeta(name)+"$"))
	if len(ids) != 1 {
		t.Fatalf("want one visible %q window, got %v", name, ids)
	}
	return ids[0]
}

func linuxXDo(t *testing.T, args ...string) string {
	t.Helper()
	return linuxWindowCommand(t, "xdotool", args...)
}

func linuxWindowPID(t *testing.T, window string) string {
	t.Helper()
	return strings.TrimSpace(linuxXDo(t, "getwindowpid", window))
}

func waitForLinuxWindowClosed(t *testing.T, window string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		if err := exec.CommandContext(ctx, "xdotool", "getwindowname", window).Run(); err != nil && ctx.Err() == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("window %s remained open after it was closed", window)
}

func closeLinuxTestWindow(t *testing.T, window, pid string) {
	t.Helper()
	findLinuxControl(t, pid, "Close").click(t)
	waitForLinuxTestAppClosed(t, window, pid)
}

func waitForLinuxTestAppClosed(t *testing.T, window, pid string) {
	t.Helper()
	waitForLinuxWindowClosed(t, window)
	link := filepath.Join("/proc", pid, "exe")
	for deadline := time.Now().Add(6 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Readlink(link); errors.Is(err, os.ErrNotExist) {
			return
		}
	}
	t.Fatalf("process %s remained running after its window was closed", pid)
}

type linuxControl struct {
	X, Y, Width, Height int
	Focused             bool
	Text                string
	Placeholder         string
	Description         string
}

func (c linuxControl) click(t *testing.T) {
	t.Helper()
	linuxXDo(t, "mousemove", fmt.Sprint(c.X), fmt.Sprint(c.Y), "click", "1")
}

func findLinuxControl(t *testing.T, pid, label string) linuxControl {
	t.Helper()
	return linuxAccessibleControl(t, pid, label, "find")
}

func linuxAccessibleControl(t *testing.T, pid, label, action string) linuxControl {
	t.Helper()
	// Resolve the control in the current process's accessibility tree so
	// clicks use its real bounds or activate its built-in action.
	const wait = `import json, sys, time
import pyatspi
pid, name, action = int(sys.argv[1]), sys.argv[2], sys.argv[3]
text_roles = (pyatspi.ROLE_ENTRY, pyatspi.ROLE_TEXT)
def matches(node):
    if name and node.name != name:
        return False
    if not name and node.getRole() not in text_roles:
        return False
    state = node.getState()
    if not state.contains(pyatspi.STATE_SHOWING) or not state.contains(pyatspi.STATE_ENABLED):
        return False
    return bool(name) or state.contains(pyatspi.STATE_FOCUSED)

deadline = time.monotonic() + 10
app = None
while time.monotonic() < deadline:
    try:
        app = next((a for a in pyatspi.Registry.getDesktop(0) if a.get_process_id() == pid), None)
        control = pyatspi.findDescendant(app, matches) if app is not None else None
        if action == 'disappear':
            if app is not None and control is None:
                print('{}')
                break
            time.sleep(0.06)
            continue
        if control is not None:
            x, y, w, h = control.queryComponent().getExtents(pyatspi.DESKTOP_COORDS)
            if w > 0 and h > 0:
                text, placeholder = '', ''
                if control.getRole() in text_roles:
                    text = control.queryText().getText(0, -1)
                    attributes = dict(item.split(':', 1) for item in control.getAttributes() if ':' in item)
                    placeholder = attributes.get('placeholder-text', '')
                bounds = dict(X=x+w//2, Y=y+h//2, Width=w, Height=h, Text=text, Placeholder=placeholder,
                    Description=control.description,
                    Focused=control.getState().contains(pyatspi.STATE_FOCUSED))
                if action == 'activate' and not control.queryAction().doAction(0):
                    raise SystemExit(f'could not activate {name!r} for process {pid}')
                print(json.dumps(bounds))
                break
    except (RuntimeError, LookupError):
        pass # A transient object can disappear during a transition.
    time.sleep(0.06)
else:
    if action == 'disappear':
        raise SystemExit(f'{name!r} did not disappear from process {pid}')
    controls = pyatspi.findAllDescendants(app, lambda node: node.name and node.getState().contains(pyatspi.STATE_SHOWING)) if app is not None else []
    labels = ', '.join(repr(node.name) for node in controls[:40])
    wanted = repr(name) if name else 'focused text entry'
    raise SystemExit(f'no visible enabled control {wanted} for process {pid}. Visible: {labels}')`
	output := linuxWindowCommand(t, "/usr/bin/python3", "-c", wait, pid, label, action)
	var control linuxControl
	if err := json.Unmarshal([]byte(output), &control); err != nil {
		t.Fatalf("invalid accessibility data for %q: %v (%s)", label, err, output)
	}
	return control
}

func linuxWindowCommand(t *testing.T, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		detail := out
		if exitError, ok := err.(*exec.ExitError); ok {
			detail = append(detail, exitError.Stderr...)
		}
		t.Fatalf("%s: %v (%s)", filepath.Base(name), err, detail)
	}
	return string(out)
}
