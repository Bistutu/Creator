package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLinuxInstalledWebsiteFullscreen(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	startTestWindowManager(t)
	type report struct {
		Status                    string
		Draft                     string
		Width, Height             int
		ScreenWidth, ScreenHeight int
	}
	seen := make(chan report, 8)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /report", func(w http.ResponseWriter, r *http.Request) {
		var update report
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case seen <- update:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><title>Fullscreen player</title>
<body style="margin:0">
<div id="player" style="width:300px;height:180px;background:#16171c"></div>
<input id="draft" aria-label="Draft">
<script>
const player = document.querySelector('#player');
const draft = document.querySelector('#draft');
const report = (status) => fetch('/report', {method: 'POST', body: JSON.stringify({
    status, draft: draft.value, width: innerWidth, height: innerHeight,
    screenWidth: screen.width, screenHeight: screen.height,
})});
addEventListener('pageshow', () => { draft.focus(); report('ready'); });
addEventListener('keydown', event => {
    if (event.key === 'F2') player.requestFullscreen().then(() => report('entered'), error => report(error.toString()));
    if (event.key === 'F3') document.exitFullscreen().then(() => report('left'), error => report(error.toString()));
    if (event.key === 'F4') report(document.fullscreenElement === player ? 'fullscreen' : 'normal');
});
</script>`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	read := func(status string) report {
		t.Helper()
		select {
		case got := <-seen:
			if got.Status != status {
				t.Fatalf("player reports %+v, want status %q", got, status)
			}
			return got
		case <-time.After(6 * time.Second):
			t.Fatalf("player did not report %q", status)
			return report{}
		}
	}
	const name = "Creator fullscreen fixture"
	const note = "unsaved notes"
	open := linuxWebsiteFixtureLauncher(t, installed, state, name, server.URL)
	window, pid := open()
	before := read("ready")
	geometry := linuxXDo(t, "getwindowgeometry", "--shell", window)
	press := func(key string) {
		t.Helper()
		linuxXDo(t, "windowfocus", "--sync", window, "key", "--clearmodifiers", key)
	}
	fullscreen := func(want bool) {
		t.Helper()
		waitForXProperty(t, "_NET_WM_STATE_FULLSCREEN", want, "-id", window, "_NET_WM_STATE")
	}
	linuxXDo(t, "windowfocus", "--sync", window, "type", "--clearmodifiers", note)
	enter := func() {
		t.Helper()
		press("F2")
		read("entered")
		fullscreen(true)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(40 * time.Millisecond) {
			press("F4")
			got := read("fullscreen")
			if got.Width == got.ScreenWidth && got.Height == got.ScreenHeight && got.Draft == note {
				return
			}
		}
		t.Fatal("the player did not fill the display or retain its draft")
	}
	checkRestored := func() {
		t.Helper()
		fullscreen(false)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(40 * time.Millisecond) {
			press("F4")
			got := read("normal")
			if got.Width == before.Width && got.Height == before.Height && got.Draft == note &&
				linuxXDo(t, "getwindowgeometry", "--shell", window) == geometry {
				return
			}
		}
		t.Fatal("leaving fullscreen did not restore the previous window, page layout and draft")
	}
	enter()
	press("Escape")
	checkRestored()
	enter()
	press("F3")
	read("left")
	checkRestored()
	t.Log("the webpage filled the display, and Escape and document.exitFullscreen both restored the window and draft")
	enter()
	press("ctrl+w")
	waitForLinuxTestAppClosed(t, window, pid)
	window, pid = open()
	if after := read("ready"); after.Width != before.Width || after.Height != before.Height {
		t.Fatalf("reopening after quitting a fullscreen player restored the wrong viewport: %+v, wanted %+v", after, before)
	}
	fullscreen(false)
	press("ctrl+w")
	waitForLinuxTestAppClosed(t, window, pid)
	t.Log("quitting a fullscreen player kept the app's regular window on the next launch")
}

func startTestWindowManager(t *testing.T) {
	t.Helper()
	waitForXWindowManager(t, false)
	wm := exec.CommandContext(t.Context(), "openbox", "--sm-disable")
	wm.Cancel = func() error { return wm.Process.Signal(syscall.SIGTERM) }
	wm.WaitDelay = 2 * time.Second
	wm.Stderr = os.Stderr
	if err := wm.Start(); err != nil {
		t.Fatalf("start Openbox in the isolated Xvfb session: %v", err)
	}
	t.Cleanup(func() { _ = wm.Wait() })
	waitForXWindowManager(t, true)
}

func waitForXWindowManager(t *testing.T, want bool) {
	t.Helper()
	const property = "_NET_SUPPORTING_WM_CHECK"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		root, err := exec.CommandContext(ctx, "xprop", "-root", property).Output()
		if err == nil {
			// The root can still point at a previous manager's destroyed window.
			// A live manager publishes the same property on its own window.
			active := false
			if _, owner, ok := strings.Cut(string(root), "window id # "); ok {
				child, err := exec.CommandContext(ctx, "xprop", "-id", strings.TrimSpace(owner), property).Output()
				active = err == nil && string(child) == string(root)
			}
			if active == want {
				return
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("the isolated Xvfb display did not reach window manager present=%t", want)
}

func waitForXProperty(t *testing.T, value string, want bool, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var result string
	for ctx.Err() == nil {
		output, err := exec.CommandContext(ctx, "xprop", args...).CombinedOutput()
		result = string(output)
		if err != nil && len(output) == 0 {
			result = err.Error()
		}
		if err == nil && strings.Contains(result, value) == want {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("xprop %v did not reach %q present=%t: %s", args, value, want, result)
}
