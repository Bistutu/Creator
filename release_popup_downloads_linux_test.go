package main

import (
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

func TestLinuxInstalledWebsitePopupDownloads(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	directory := linuxFixtureDownloads(t, state)
	const name = "Creator export fixture"
	const popupName = "Website export fixture"
	const draftText = "Preserved draft"
	const report, existing, contents = "report.csv", "Local draft\n", "Full export\n"
	const finalName = "long export.csv"
	const failedName = "incomplete export.csv"
	const blobName, blobContents = "客户数据.csv", "账号,状态\nCreator,就绪\n"
	finalContents := strings.Repeat("The export continues after its window closes.\n", 52)
	if err := os.WriteFile(filepath.Join(directory, report), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	mainReady, popupReady := make(chan struct{}, 1), make(chan struct{}, 1)
	requested := make(chan struct{}, 1)
	type heldExport struct {
		name              string
		started, complete chan struct{}
	}
	exports := map[string]heldExport{
		"complete": {finalName, make(chan struct{}, 1), make(chan struct{})},
		"fail":     {failedName, make(chan struct{}, 1), make(chan struct{})},
	}
	stop := make(chan struct{})
	defer close(stop)
	closed := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "poll", Value: "signed-in", Path: "/", HttpOnly: true})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><title>Creator export fixture</title>
<style>
input, button {position:absolute; left:20px}
input {top:20px; width:260px; height:32px}
button {top:72px; width:420px; height:240px}
</style>
<input id="draft" aria-label="Unsaved draft">
<button onclick="window.open('/exports')">Open export window</button>
<script>
requestAnimationFrame(() => fetch('/ready/main', {method:'POST'}));
addEventListener('message', event => {
  if (event.origin === location.origin && event.data === 'closed') {
    fetch('/closed', {method:'POST', body:document.getElementById('draft').value});
  }
});
</script>`)
	})
	mux.HandleFunc("GET /exports", func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("poll"); err != nil || cookie.Value != "signed-in" {
			http.Error(w, "export requires the original session", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><title>Website export fixture</title>
<style>a, button {position:absolute;left:20px;width:280px;height:50px;line-height:50px}</style>
<a style="top:55px" href="/attachment">Download report</a>
<a style="top:155px" href="/interruption">Disconnect transport</a>
<a style="top:255px" href="/attachment?retry">Retry report</a>
<a style="top:355px" href="/held/complete">Start large export</a>
<a style="top:355px;left:340px" href="/held/fail">Start interrupted export</a>
<button style="top:455px" onclick="window.opener.postMessage('closed', location.origin); window.close()">Close export window</button>
<button style="top:555px" onclick="downloadBlob()">Export local CSV</button>
<script>
function downloadBlob() {
  const link = document.createElement('a');
  link.download = '客户数据.csv';
  link.href = URL.createObjectURL(new Blob(['账号,状态\nCreator,就绪\n'], {type:'text/csv'}));
  link.click();
}
requestAnimationFrame(() => fetch('/ready/popup', {method:'POST'}));
</script>`)
	})
	mux.HandleFunc("POST /ready/{window}", func(w http.ResponseWriter, r *http.Request) {
		ready := mainReady
		if r.PathValue("window") == "popup" {
			ready = popupReady
		}
		select {
		case ready <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /closed", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 512))
		if err != nil {
			http.Error(w, "invalid draft", http.StatusBadRequest)
			return
		}
		select {
		case closed <- string(data):
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
	})
	attachment := func(w http.ResponseWriter, name string) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	}
	mux.HandleFunc("GET /attachment", func(w http.ResponseWriter, _ *http.Request) {
		attachment(w, report)
		_, _ = io.WriteString(w, contents)
		select {
		case requested <- struct{}{}:
		default:
		}
	})
	mux.HandleFunc("GET /interruption", func(w http.ResponseWriter, _ *http.Request) {
		attachment(w, report)
		w.Header().Set("Content-Length", "5000")
		_, _ = io.WriteString(w, strings.Repeat("This transfer was interrupted.\n", 24))
		_ = http.NewResponseController(w).Flush()
	})
	mux.HandleFunc("GET /held/{outcome}", func(w http.ResponseWriter, r *http.Request) {
		outcome := r.PathValue("outcome")
		export, ok := exports[outcome]
		if !ok {
			http.NotFound(w, r)
			return
		}
		attachment(w, export.name)
		w.Header().Set("Content-Length", strconv.Itoa(len(finalContents)))
		split := len(finalContents) / 2
		_, _ = io.WriteString(w, finalContents[:split])
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		select {
		case export.started <- struct{}{}:
		default:
		}
		select {
		case <-export.complete:
			if outcome == "complete" {
				_, _ = io.WriteString(w, finalContents[split:])
			}
		case <-r.Context().Done():
		case <-stop:
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	window, pid := openLinuxWebsiteFixture(t, installed, state, name, server.URL)
	select {
	case <-mainReady:
	case <-time.After(8 * time.Second):
		t.Fatal("the original website did not load")
	}
	click := func(target, x, y string) {
		t.Helper()
		linuxXDo(t, "windowfocus", "--sync", target, "mousemove", "--window", target, x, y, "click", "1")
	}
	click(window, "130", "92")
	linuxXDo(t, "type", "--clearmodifiers", draftText)
	openPopup := func() string {
		t.Helper()
		click(window, "130", "160")
		popup := linuxFixtureWindow(t, popupName)
		select {
		case <-popupReady:
		case <-time.After(8 * time.Second):
			t.Fatal("the export window did not load with the original website session")
		}
		if popup == window || linuxWindowPID(t, popup) != pid {
			t.Fatal("the export did not open as a related native window")
		}
		return popup
	}
	popup := openPopup()

	published := map[string]string{report: existing}
	waitForLinuxDownloads(t, directory, published, 0)
	click(popup, "130", "80")
	select {
	case <-requested:
	case <-time.After(8 * time.Second):
		t.Fatal("the popup's attachment was not requested")
	}
	published["report (2).csv"] = contents
	waitForLinuxDownloads(t, directory, published, 0)
	t.Log("the related export saved a new copy and preserved the file already in Downloads")

	checkWarning := func(owner string) {
		t.Helper()
		warning := linuxFixtureWindow(t, "下载未完成")
		parent, err := strconv.ParseUint(owner, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		transient := strings.TrimSpace(linuxWindowCommand(t, "xprop", "-id", warning, "WM_TRANSIENT_FOR"))
		if !strings.HasSuffix(transient, "0x"+strconv.FormatUint(parent, 16)) {
			t.Fatalf("the error should belong to window %s, got %q", owner, transient)
		}
		linuxXDo(t, "windowfocus", "--sync", warning, "key", "--clearmodifiers", "Return")
		waitForLinuxWindowClosed(t, warning)
		waitForLinuxDownloads(t, directory, published, 0)
	}
	click(popup, "130", "180")
	checkWarning(popup)
	click(popup, "130", "280")
	published["report (3).csv"] = contents
	waitForLinuxDownloads(t, directory, published, 0)
	t.Log("the native interruption warning belonged to the related window, and it could retry the download")

	click(popup, "130", "580")
	published[blobName] = blobContents
	waitForLinuxDownloads(t, directory, published, 0)
	t.Log("the related window exported a Blob with its complete UTF-8 contents and name")

	finishAfterClose := func(popup, x, outcome string) {
		t.Helper()
		export := exports[outcome]
		click(popup, x, "380")
		select {
		case <-export.started:
		case <-time.After(8 * time.Second):
			t.Fatalf("the %s export did not start", outcome)
		}
		waitForLinuxDownloads(t, directory, published, 1)
		click(popup, "130", "480")
		waitForLinuxWindowClosed(t, popup)
		close(export.complete)
		select {
		case draft := <-closed:
			if draft != draftText {
				t.Fatalf("the original window reported a different draft: %q", draft)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("the related window did not notify its opener")
		}
	}
	finishAfterClose(popup, "130", "complete")
	published[finalName] = finalContents
	waitForLinuxDownloads(t, directory, published, 0)
	t.Log("the in-progress export completed after the popup closed, with the same draft visible in the original window")
	finishAfterClose(openPopup(), "450", "fail")
	checkWarning(window)
	t.Log("an export that failed after the popup closed showed its warning in the live opener")
	linuxXDo(t, "windowfocus", "--sync", window, "key", "--clearmodifiers", "ctrl+w")
	waitForLinuxTestAppClosed(t, window, pid)
}
