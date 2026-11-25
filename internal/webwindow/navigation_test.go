package webwindow

import (
	"errors"
	"testing"
)

type homePageProbe struct {
	address string
	loading bool
	loads   []string
	err     error
}

func (p *homePageProbe) URL() string     { return p.address }
func (p *homePageProbe) IsLoading() bool { return p.loading }
func (p *homePageProbe) LoadURL(address string) error {
	p.loads = append(p.loads, address)
	return p.err
}

func TestNavigateHomeUsesSavedAddress(t *testing.T) {
	const saved = "https://example.com/workspace?source=creator#draft"
	for _, tc := range []struct {
		name, saved, current string
		loading, wantLoad    bool
	}{
		{name: "already home", saved: saved, current: saved},
		{name: "browser normalizes root and default port", saved: "https://example.com:443#draft", current: "https://example.com/#draft"},
		{name: "pending navigation", saved: saved, current: saved, loading: true, wantLoad: true},
		{name: "different path", saved: saved, current: "https://example.com/", wantLoad: true},
		{name: "different query", saved: saved, current: "https://example.com/workspace?source=other#draft", wantLoad: true},
		{name: "different fragment", saved: saved, current: "https://example.com/workspace?source=creator#other", wantLoad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := homePageProbe{address: tc.current, loading: tc.loading}
			redraw, err := navigateHome(&page, tc.saved, nil)
			if err != nil || redraw {
				t.Fatalf("go home without a recovery panel: redraw=%t, %v", redraw, err)
			}
			if tc.wantLoad && (len(page.loads) != 1 || page.loads[0] != tc.saved) {
				t.Fatalf("loaded %q, want the saved address %q", page.loads, tc.saved)
			}
			if !tc.wantLoad && len(page.loads) != 0 {
				t.Fatalf("going home reloaded the current document: %q", page.loads)
			}
		})
	}
}

func TestNavigateHomeFromRecovery(t *testing.T) {
	const home = "https://example.com/workspace?source=creator#draft"
	const broken = "https://example.com/unavailable"
	var recovery Recovery
	recovery.Committed(home)
	recovery.Requested(broken)
	recovery.Failed(broken)
	page := homePageProbe{address: home}
	// Home can supersede an earlier retry of a different address.
	recovery.Retry(page.LoadURL, func() { t.Fatal("retried by reloading home") })
	redraw, err := navigateHome(&page, home, &recovery)
	if err != nil || !redraw || !recovery.retrying || recovery.requested != home || len(page.loads) != 2 || page.loads[1] != home {
		t.Fatalf("go home during recovery: redraw=%t, err=%v, state=%+v, loads=%q", redraw, err, recovery, page.loads)
	}
	if !recovery.Finished() || recovery.Visible() {
		t.Fatal("the recovery panel hid the successfully loaded home page")
	}

	page.address = broken
	page.err = errors.New("page is unavailable")
	redraw, err = navigateHome(&page, home, &recovery)
	if !errors.Is(err, page.err) || !redraw || recovery.failed != home || recovery.retrying {
		t.Fatalf("failed home load: redraw=%t, err=%v, state=%+v", redraw, err, recovery)
	}
	page.err = nil
	if !recovery.Retry(page.LoadURL, func() { t.Fatal("reloaded the secondary address") }) || page.loads[len(page.loads)-1] != home {
		t.Fatalf("recovery did not retry the saved home address: %q", page.loads)
	}
}

func TestVisibleWebsiteAddressPreservesContext(t *testing.T) {
	const home = "https://example.org/workspace?from=creator#top"
	const current = "https://example.org/notes/2026?view=board%20layout&mode=edit#week"
	const failed = "https://example.org/notes/other?tag=%2F#today"
	for _, test := range []struct {
		name, current, home string
		recovery            *Recovery
		want                string
	}{
		{name: "loaded document", current: current, home: home, want: current},
		{name: "failed destination", current: current, home: home, recovery: &Recovery{failed: failed}, want: failed},
		{name: "first load", current: "about:blank", home: home, want: home},
		{name: "invalid failure", current: current, home: home, recovery: &Recovery{failed: "file:///tmp/report"}, want: current},
		{name: "credentials in URL", current: "https://user:pass@example.org/notes", home: home, want: home},
		{name: "no website", current: "data:text/plain,example", home: "file:///tmp/report"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := visibleWebsiteAddress(test.current, test.home, test.recovery); got != test.want {
				t.Fatalf("visible website address = %q, want %q", got, test.want)
			}
		})
	}
}
