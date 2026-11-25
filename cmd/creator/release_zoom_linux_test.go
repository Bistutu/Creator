//go:build linux

package main

import (
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestLinuxInstalledWebsiteZoom(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	type report struct {
		app   string
		width int
	}
	seen := make(chan report, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/report" {
			width, err := strconv.Atoi(r.URL.Query().Get("width"))
			if err != nil || width <= 0 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			select {
			case seen <- report{r.URL.Query().Get("app"), width}:
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><title>Creator viewport test</title>
<style>body { margin: 0; min-height: 100vh; }</style>
<body tabindex="-1">Creator viewport test</body>
<script>
const report = () => requestAnimationFrame(() => requestAnimationFrame(() => {
    fetch('/report?' + new URLSearchParams({app: location.pathname, width: innerWidth}));
}));
window.addEventListener('pageshow', () => { document.body.focus(); report(); });
window.addEventListener('keydown', event => { if (event.key === 'j') report(); });
</script>`)
	}))
	t.Cleanup(server.Close)

	readWidth := func(app string) int {
		t.Helper()
		select {
		case got := <-seen:
			if got.app != app {
				t.Fatalf("the wrong app reported its webpage width: %+v, wanted %s", got, app)
			}
			return got.width
		case <-time.After(6 * time.Second):
			t.Fatalf("%s did not report its webpage width", app)
			return 0
		}
	}
	press := func(window, key string) {
		t.Helper()
		linuxXDo(t, "windowfocus", "--sync", window, "key", "--clearmodifiers", key)
	}
	openFirst := linuxWebsiteFixtureLauncher(t, installed, state, "Creator zoom fixture", server.URL+"/first")
	first, pid := openFirst()
	width := readWidth("/first")
	geometry := linuxXDo(t, "getwindowgeometry", "--shell", first)
	wantWidth := func(got int, factor float64) bool {
		return math.Abs(float64(got)-float64(width)/factor) < 2
	}
	for _, step := range []struct {
		key    string
		factor float64
	}{{"ctrl+equal", 1.10}, {"ctrl+equal", 1.25}, {"ctrl+minus", 1.10}, {"ctrl+0", 1}, {"ctrl+minus", 0.90}, {"ctrl+equal", 1}, {"ctrl+equal", 1.10}} {
		press(first, step.key)
		// Give the renderer another frame if the keyboard report preceded the reflow.
		var got int
		for attempt := 0; attempt < 4; attempt++ {
			press(first, "j")
			got = readWidth("/first")
			if wantWidth(got, step.factor) {
				break
			}
		}
		if !wantWidth(got, step.factor) {
			t.Fatalf("%s left a %dpx viewport, expected %.0fpx at %.0f%% zoom", step.key, got, float64(width)/step.factor, step.factor*100)
		}
	}
	if got := linuxXDo(t, "getwindowgeometry", "--shell", first); got != geometry {
		t.Fatalf("zoom changed the outer window: %s, wanted %s", got, geometry)
	}
	press(first, "ctrl+w")
	waitForLinuxTestAppClosed(t, first, pid)

	second, secondPID := openLinuxWebsiteFixture(t, installed, state, "Creator separate zoom fixture", server.URL+"/second")
	if got := readWidth("/second"); got != width {
		t.Fatalf("a separate app inherited the zoom: viewport %dpx, wanted %dpx", got, width)
	}
	press(second, "ctrl+w")
	waitForLinuxTestAppClosed(t, second, secondPID)

	first, pid = openFirst()
	if got := readWidth("/first"); !wantWidth(got, 1.10) {
		t.Fatalf("reopening the app lost its 110%% zoom: viewport %dpx, wanted %.0fpx", got, float64(width)/1.10)
	}
	press(first, "ctrl+w")
	waitForLinuxTestAppClosed(t, first, pid)
	t.Log("native keyboard zoom changed the webpage layout, kept the window size and survived reopening only the same app")
}
