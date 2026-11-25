package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLinuxInstalledWebsiteFiles(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	t.Setenv("GTK_USE_PORTAL", "0")
	t.Setenv("LC_ALL", "C.UTF-8")
	downloads := linuxFixtureDownloads(t, state)
	const uploadName, uploadContent = "selected.txt", "Selected local file\n"
	const downloadName, downloadContent = "workbench.txt", "Exported website file\n"
	const failedName = "interrupted.txt"
	selected := filepath.Join(state, uploadName)
	existing := filepath.Join(downloads, downloadName)
	if err := os.WriteFile(selected, []byte(uploadContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("User's existing file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	type selection struct{ Name, Content string }
	reported := make(chan selection, 2)
	ready := make(chan struct{}, 1)
	interrupted := make(chan struct{}, 1)
	type transfer struct {
		name, content string
		started, send chan struct{}
	}
	transfers := map[string]transfer{
		"first":  {"report.csv", strings.Repeat("Summary from first response\n", 80), make(chan struct{}, 1), make(chan struct{})},
		"second": {"report.csv", strings.Repeat("Summary from second response\n", 80), make(chan struct{}, 1), make(chan struct{})},
		"quit":   {"unfinished.txt", strings.Repeat("This transfer should end on quit\n", 80), make(chan struct{}, 1), make(chan struct{})},
	}
	stop := make(chan struct{})
	defer close(stop)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><title>Website attachment fixture</title>
<style>input, button, a {position:absolute;left:16px;width:260px;height:44px}
input {top:30px} button {top:110px} a {top:192px;line-height:44px}</style>
<input id="local" type="file" aria-label="Attach file">
<button id="report">Confirm selection</button>
<a href="/attachment">Download export</a>
<a href="/interrupted" style="left:310px;top:192px">Interrupt transfer</a>
<a href="/retry" style="top:254px">Retry transfer</a>
<a href="/transfer/first" style="left:310px;top:254px">Download first report</a>
<a href="/transfer/second" style="top:316px">Download second report</a>
<a href="/transfer/quit" style="left:310px;top:316px">Leave download running</a>
<script>
document.querySelector('#report').onclick = async () => {
  const file = document.querySelector('#local').files[0];
  const selection = file ? {name:file.name, content:await file.text()} : {name:'', content:''};
  fetch('/selection', {method:'POST', body:JSON.stringify(selection)});
};
requestAnimationFrame(() => fetch('/ready', {method:'POST'}));
</script>`)
	})
	mux.HandleFunc("POST /ready", func(w http.ResponseWriter, _ *http.Request) {
		select {
		case ready <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /selection", func(w http.ResponseWriter, r *http.Request) {
		var result selection
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&result); err != nil {
			http.Error(w, "invalid selection", http.StatusBadRequest)
			return
		}
		select {
		case reported <- result:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /attachment", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(downloadName))
		_, _ = io.WriteString(w, downloadContent)
	})
	mux.HandleFunc("GET /interrupted", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(failedName))
		w.Header().Set("Content-Length", "4096")
		_, _ = io.WriteString(w, "Incomplete transfer")
		_ = http.NewResponseController(w).Flush()
		select {
		case interrupted <- struct{}{}:
		default:
		}
	})
	mux.HandleFunc("GET /retry", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(failedName))
		_, _ = io.WriteString(w, downloadContent)
	})
	mux.HandleFunc("GET /transfer/{name}", func(w http.ResponseWriter, r *http.Request) {
		transfer, ok := transfers[r.PathValue("name")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(transfer.name))
		w.Header().Set("Content-Length", strconv.Itoa(len(transfer.content)))
		split := len(transfer.content) / 2
		_, _ = io.WriteString(w, transfer.content[:split])
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		select {
		case transfer.started <- struct{}{}:
		default:
		}
		select {
		case <-transfer.send:
			_, _ = io.WriteString(w, transfer.content[split:])
		case <-r.Context().Done():
		case <-stop:
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	const name = "Creator file fixture"
	window, pid := openLinuxWebsiteFixture(t, installed, state, name, server.URL)
	select {
	case <-ready:
	case <-time.After(12 * time.Second):
		t.Fatal("the website did not render its upload and download controls")
	}
	clickPage := func(x, y string) {
		t.Helper()
		linuxXDo(t, "windowfocus", "--sync", window, "mousemove", "--window", window, x, y, "click", "1")
	}
	checkSelection := func(want selection) {
		t.Helper()
		clickPage("100", "174")
		select {
		case got := <-reported:
			if got != want {
				t.Fatalf("the website received %+v, want %+v", got, want)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("the website did not report its file selection")
		}
	}
	clickPage("65", "88")
	picker := linuxFixtureWindow(t, "Select File")
	linuxXDo(t, "windowfocus", "--sync", picker, "key", "--clearmodifiers", "Escape")
	waitForLinuxWindowClosed(t, picker)
	checkSelection(selection{})
	clickPage("65", "88")
	picker = linuxFixtureWindow(t, "Select File")
	linuxXDo(t, "windowfocus", "--sync", picker, "key", "--clearmodifiers", "ctrl+l", "type", "--clearmodifiers", selected)
	if got := findLinuxControl(t, pid, "").Text; got != selected {
		t.Fatalf("the native chooser contains %q, want %q", got, selected)
	}
	linuxAccessibleControl(t, pid, "Open", "activate")
	waitForLinuxWindowClosed(t, picker)
	checkSelection(selection{uploadName, uploadContent})
	t.Log("the native chooser canceled cleanly and then delivered the selected file's exact name and contents")

	published := map[string]string{downloadName: "User's existing file\n", "workbench (2).txt": downloadContent}
	clickPage("100", "252")
	waitForLinuxDownloads(t, downloads, published, 0)
	checkSelection(selection{uploadName, uploadContent})
	t.Log("the export went to the configured Downloads folder under a new name while the original page stayed open")

	clickPage("400", "252")
	select {
	case <-interrupted:
	case <-time.After(8 * time.Second):
		t.Fatal("the test site did not start the interrupted attachment")
	}
	t.Cleanup(func() {
		if t.Failed() {
			entries, err := os.ReadDir(downloads)
			t.Logf("the Downloads folder after the failed transfer contained %v (%v)", entries, err)
		}
	})
	warning := linuxFixtureWindow(t, "下载未完成")
	linuxXDo(t, "windowfocus", "--sync", warning, "key", "--clearmodifiers", "Return")
	waitForLinuxWindowClosed(t, warning)
	waitForLinuxDownloads(t, downloads, published, 0)
	checkSelection(selection{uploadName, uploadContent})
	clickPage("100", "314")
	published[failedName] = downloadContent
	waitForLinuxDownloads(t, downloads, published, 0)
	t.Log("the native warning left the page usable, and retry saved a complete file without a stale partial copy")

	begin := func(which, x, y string, pending int) {
		t.Helper()
		clickPage(x, y)
		select {
		case <-transfers[which].started:
		case <-time.After(8 * time.Second):
			t.Fatalf("the site did not start the %s attachment", which)
		}
		waitForLinuxDownloads(t, downloads, published, pending)
	}
	begin("first", "400", "314", 1)
	begin("second", "100", "376", 2)
	const external = "This file appeared while both downloads were waiting\n"
	report, err := os.OpenFile(filepath.Join(downloads, "report.csv"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("the final name was occupied by an incomplete download: %v", err)
	}
	if _, err := io.WriteString(report, external); err != nil {
		_ = report.Close()
		t.Fatal(err)
	}
	if err := report.Close(); err != nil {
		t.Fatal(err)
	}
	published["report.csv"] = external
	close(transfers["second"].send)
	published["report (2).csv"] = transfers["second"].content
	waitForLinuxDownloads(t, downloads, published, 1)
	close(transfers["first"].send)
	published["report (3).csv"] = transfers["first"].content
	waitForLinuxDownloads(t, downloads, published, 0)
	checkSelection(selection{uploadName, uploadContent})
	t.Log("finishing the two same-name attachments independently preserved the intervening file and both exact payloads")

	begin("quit", "400", "376", 1)
	linuxXDo(t, "windowfocus", "--sync", window, "key", "--clearmodifiers", "ctrl+w")
	waitForLinuxTestAppClosed(t, window, pid)
	waitForLinuxDownloads(t, downloads, published, 0)
	t.Log("quitting the installed app cleaned up the transfer that was still waiting")
}

func linuxFixtureDownloads(t *testing.T, state string) string {
	t.Helper()
	directory := filepath.Join(state, "Downloads")
	config := os.Getenv("XDG_CONFIG_HOME")
	if err := os.MkdirAll(config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "user-dirs.dirs"), []byte("XDG_DOWNLOAD_DIR="+strconv.Quote(directory)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func waitForLinuxDownloads(t *testing.T, dir string, published map[string]string, pending int) {
	t.Helper()
	var entries []os.DirEntry
	var problems []string
	for deadline := time.Now().Add(6 * time.Second); time.Now().Before(deadline); time.Sleep(40 * time.Millisecond) {
		var err error
		entries, err = os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		problems = problems[:0]
		for name, want := range published {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				problems = append(problems, err.Error())
			} else if string(data) != want {
				problems = append(problems, fmt.Sprintf("%s contents differ (%d bytes, want %d)", name, len(data), len(want)))
			}
		}
		staged := 0
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), ".creator-download-") {
				staged++
			}
		}
		if len(problems) == 0 && staged == pending && len(entries) == len(published)+pending {
			return
		}
	}
	t.Fatalf("download folder contained %v, want %d complete files and %d staged transfers. File checks: %v", entries, len(published), pending, problems)
}
