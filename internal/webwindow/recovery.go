package webwindow

import (
	"net/url"

	"github.com/egoist/mygo/ui"
	"github.com/Bistutu/Creator/internal/profile"
)

// Recovery holds the attempted address while a window displays a load failure.
// The native window and its existing web view stay alive during a retry.
type Recovery struct {
	requested     string
	committed     string
	failed        string
	retrying      bool
	reloadCurrent bool
}

func (r *Recovery) Requested(address string) {
	if websiteAddress(address) {
		r.requested = address
	}
}

func (r *Recovery) Committed(address string) {
	if websiteAddress(address) {
		r.committed, r.requested = address, address
	}
}

func (r *Recovery) Failed(address string) {
	// Some WebView2 failures still report the previously committed page.
	if !websiteAddress(address) || address == r.committed {
		address = r.requested
	}
	r.failed, r.retrying = address, false
	r.reloadCurrent = false
}

func (r *Recovery) RendererTerminated(address string) {
	r.Failed(address)
	// Reload the existing history item only when it is the failed address.
	// The renderer can also stop before its first navigation commits.
	r.reloadCurrent = r.Visible() && r.failed == address && address == r.committed
}

func (r *Recovery) Finished() bool {
	// WebKitGTK can finish a failed load as well. Failed has already reset
	// retrying in that case, so keep the panel showing.
	if !r.retrying {
		return false
	}
	r.failed, r.retrying, r.reloadCurrent = "", false, false
	return true
}

func (r *Recovery) Visible() bool { return r.failed != "" }

// Retry starts the failed navigation again. It returns whether a retry was
// attempted so the caller can update the original window.
func (r *Recovery) Retry(load func(string) error, reload func()) bool {
	if !r.beginNavigation() {
		return false
	}
	r.requested = r.failed
	if r.reloadCurrent {
		reload()
	} else if err := load(r.failed); err != nil {
		r.retrying = false
	}
	return true
}

func (r *Recovery) beginNavigation() bool {
	if !r.Visible() || r.retrying {
		return false
	}
	r.retrying = true
	return true
}

func (r *Recovery) View(c *ui.Context, load func(string) error, reload func()) {
	recoveryPanel(c, func() {
		ui.Text(c, "无法载入网站").FontSize(19).FontWeight(600)
		ui.Text(c, profile.Host(r.failed)).MaxWidthPercent(100).SingleLine().Ellipsis("…").FontSize(13).TextColor(c.Theme().TextMuted)
		label := "重新载入"
		if r.retrying {
			label = "正在载入…"
		}
		if ui.PrimaryButton(c, label).AutoFocus().Disabled(r.retrying).Clicked() && r.Retry(load, reload) {
			c.Invalidate()
		}
	})
}

// RuntimeFailureView uses the native surface when no website view could start.
func RuntimeFailureView(c *ui.Context, detail string, open func()) {
	recoveryPanel(c, func() {
		ui.Text(c, "无法启动网页组件").FontSize(19).FontWeight(600)
		ui.Text(c, detail).MaxWidthPercent(100).TextAlign(ui.Center).FontSize(13).TextColor(c.Theme().TextMuted)
		if ui.PrimaryButton(c, "在浏览器中打开").AutoFocus().Clicked() {
			open()
		}
	})
}

func recoveryPanel(c *ui.Context, content func()) {
	ui.Column(c).Grow(1).WidthPercent(100).Padding(32).AlignItems(ui.Center).Justify(ui.Center).Gap(9).Children(content)
}

func websiteAddress(address string) bool {
	u, err := url.Parse(address)
	return err == nil && u.Hostname() != "" && u.User == nil && (u.Scheme == "http" || u.Scheme == "https")
}

// WebView2ConnectionError reports the connectivity and redirect statuses from
// COREWEBVIEW2_WEB_ERROR_STATUS. Keep the WebView's certificate, sign-in and
// HTTP response pages available.
func WebView2ConnectionError(code int) bool {
	switch code {
	case 6, 7, 9, 10, 11, 12, 13, 15:
		return true
	default:
		return false
	}
}
