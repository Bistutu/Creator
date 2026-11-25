package webwindow

import (
	"errors"
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestRecoveryRetriesFailedDestination(t *testing.T) {
	const previous = "https://example.com/home"
	const destination = "https://example.net/reports/2026?source=link%20retry#latest"
	var recovery Recovery
	var addresses []string
	var loadErr error
	view := ui.NewTesterWith(func(c *ui.Context) {
		if recovery.Visible() {
			recovery.View(c, func(address string) error {
				addresses = append(addresses, address)
				return loadErr
			}, func() { t.Error("retried a transport error by reloading the previously committed page") })
		} else {
			ui.Text(c, "网站内容")
		}
	}, 640, 448, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	checkText := func(label string, want bool) {
		t.Helper()
		if _, found := view.Find(label); found != want {
			t.Fatalf("visible text %q: got %t, want %t (%v)", label, found, want, view.Texts())
		}
	}
	retry := func(wantCount int) {
		t.Helper()
		if err := view.Click("重新载入"); err != nil {
			t.Fatal(err)
		}
		if len(addresses) != wantCount || addresses[wantCount-1] != destination {
			t.Fatalf("retry %d loaded %q, want %q", wantCount, addresses, destination)
		}
	}

	recovery.Requested(previous)
	recovery.Committed(previous)
	view.Frame()
	checkText("网站内容", true)
	checkText("重新载入", false)

	recovery.Requested(destination)
	recovery.Failed(previous)
	view.Frame()
	checkText("网站内容", false)
	checkText("无法载入网站", true)
	checkText("example.net", true)
	checkText(destination, false)
	retry(1)
	checkText("正在载入…", true)
	loading, _ := view.Find("正在载入…")
	view.ClickAt(loading.X+loading.W/2, loading.Y+loading.H/2)
	if len(addresses) != 1 {
		t.Fatalf("clicking while loading retried again: %v", addresses)
	}

	recovery.Failed("")
	if recovery.Finished() {
		t.Fatal("a finish event after failure cleared the recovery panel")
	}
	view.Frame()
	loadErr = errors.New("view unavailable")
	retry(2)
	checkText("正在载入…", false)
	checkText("重新载入", true)
	loadErr = nil
	retry(3)
	recovery.Committed(destination)
	if !recovery.Finished() || recovery.Visible() {
		t.Fatal("successful retry did not restore the website")
	}
	view.Frame()
	checkText("网站内容", true)
	checkText("无法载入网站", false)
}

func TestRecoveryReloadsOnlyTheCommittedPageAfterRendererTermination(t *testing.T) {
	const page = "https://example.org/reports?range=week"
	const next = "https://example.org/reports?range=month"
	for _, tc := range []struct {
		name, committed, requested, current, load string
	}{
		{name: "current page", committed: page, requested: page, current: page},
		{name: "initial load", requested: page, current: page, load: page},
		{name: "pending next page", committed: page, requested: next, current: page, load: next},
		{name: "unavailable address", committed: page, requested: page, load: page},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var recovery Recovery
			recovery.Committed(tc.committed)
			recovery.Requested(tc.requested)
			recovery.RendererTerminated(tc.current)
			var requested string
			var reloaded bool
			if !recovery.Retry(func(address string) error {
				requested = address
				return nil
			}, func() { reloaded = true }) {
				t.Fatal("renderer termination left no retry action")
			}
			if requested != tc.load || reloaded != (tc.load == "") {
				t.Fatalf("retry requested %q, reloaded current page %t", requested, reloaded)
			}
		})
	}
}

func TestRuntimeFailureOffersBrowserOnlyOnRequest(t *testing.T) {
	const detail = "请检查系统的 Microsoft Edge WebView2 Runtime，然后重新打开应用。"
	var opened int
	view := ui.NewTesterWith(func(c *ui.Context) {
		RuntimeFailureView(c, detail, func() { opened++ })
	}, 640, 448, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	for _, label := range []string{"无法启动网页组件", detail, "在浏览器中打开"} {
		if _, found := view.Find(label); !found {
			t.Fatalf("runtime recovery is missing %q (%v)", label, view.Texts())
		}
	}
	if opened != 0 {
		t.Fatal("the browser opened while displaying the panel")
	}
	if err := view.Click("在浏览器中打开"); err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("the button opened the browser %d times", opened)
	}
}

func TestRecoveryUsesValidWebsiteAddress(t *testing.T) {
	const original = "http://example.com:8080/first"
	const redirected = "https://example.org/final?q=1"
	for _, address := range []string{"", "https:///empty", "https://[bad", "file:///tmp/local", "https://user:password@example.com", "about:blank"} {
		t.Run(address, func(t *testing.T) {
			var recovery Recovery
			recovery.Requested(address)
			recovery.Failed(address)
			if recovery.Visible() {
				t.Fatalf("invalid site entered recovery: %q", recovery.failed)
			}
			recovery.Requested(original)
			recovery.Requested(address)
			recovery.Failed(address)
			if recovery.failed != original {
				t.Fatalf("invalid site replaced the requested address: %q", recovery.failed)
			}
			recovery.Failed(redirected)
			if recovery.failed != redirected {
				t.Fatalf("valid failed redirect was ignored: %q", recovery.failed)
			}
		})
	}
}

func TestWebView2ConnectionErrors(t *testing.T) {
	for status := range 20 {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			want := status == 6 || status == 7 || (status >= 9 && status <= 13) || status == 15
			if WebView2ConnectionError(status) != want {
				t.Errorf("connection error status %d: want %t", status, want)
			}
		})
	}
}
