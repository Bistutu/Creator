package webwindow

import (
	"bytes"
	"testing"
	"testing/synctest"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestOpeningStatusTimer(t *testing.T) {
	for _, outcome := range []string{"fast", "queued", "slow"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var status OpeningStatus
				updates := make(chan func(), 1)
				redraws := 0
				status.Start(func(update func()) { updates <- update }, func() { redraws++ })
				time.Sleep(openingDelay - time.Millisecond)
				synctest.Wait()
				if len(updates) != 0 || status.visible {
					t.Fatal("the opening hint appeared before its delay")
				}
				if outcome == "fast" && status.Finish() {
					t.Fatal("finishing before the delay requested a redraw")
				}
				time.Sleep(time.Millisecond)
				synctest.Wait()
				if outcome == "fast" {
					if len(updates) != 0 || redraws != 0 {
						t.Fatal("a fast navigation scheduled a status update")
					}
					return
				}
				if len(updates) != 1 {
					t.Fatal("a pending navigation did not queue exactly one update")
				}
				if outcome == "queued" && status.Finish() {
					t.Fatal("cancelling a queued update requested a redraw")
				}
				(<-updates)()
				wantVisible := outcome == "slow"
				if status.visible != wantVisible || (redraws == 1) != wantVisible {
					t.Fatal("the queued update did not respect the navigation state")
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if len(updates) != 0 || status.Finish() != wantVisible || status.visible || status.Finish() {
					t.Fatal("the hint did not clear after exactly one update")
				}
			})
		})
	}
}

func TestOpeningStatusKeepsViewportAndRespectsReduceMotion(t *testing.T) {
	var status OpeningStatus
	view := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			status.TitleBar(c)
			ui.Button(c, "网站内容").Grow(1).AutoFocus()
		})
	}, 640, 480)
	titlePixels := func() []byte {
		frame := view.Image()
		return frame.Pix[:32*frame.Stride]
	}
	baseline := bytes.Clone(titlePixels())
	content, ok := view.AccessByRole(ui.RoleButton, "网站内容")
	if !ok || content.Bounds.Y != 32 {
		t.Fatalf("could not locate website content below title bar: %+v", content.Bounds)
	}
	viewport := content.Bounds
	checkViewport := func() {
		t.Helper()
		if got, ok := view.AccessByRole(ui.RoleButton, "网站内容"); !ok || got.Bounds != viewport || !got.Focused {
			t.Fatalf("opening hint moved the web viewport or its focus: %+v, want %+v", got.Bounds, viewport)
		}
	}

	status.visible = true
	view.Frame()
	hint, ok := view.AccessByRole(ui.RoleStatus, openingLabel)
	label := hint.Bounds
	if !ok || hint.Focusable || label.Y+label.H > viewport.Y || label.X+label.W > 626 {
		t.Fatalf("the opening hint should be contained and passive: %+v", hint)
	}
	view.Advance(60 * time.Millisecond)
	middle := bytes.Clone(titlePixels())
	view.Advance(80 * time.Millisecond)
	settled := bytes.Clone(titlePixels())
	if bytes.Equal(middle, baseline) || bytes.Equal(middle, settled) {
		t.Fatal("the title bar didn't render an intermediate fade frame")
	}
	checkViewport()
	status.Finish()
	view.Frame()
	if view.HasText(openingLabel) || !bytes.Equal(titlePixels(), baseline) {
		t.Fatal("finishing navigation left a hint in the title bar")
	}
	checkViewport()
	view.SetPreferences(ui.Preferences{ReduceMotion: true})
	quiet := bytes.Clone(titlePixels())
	status.visible = true
	view.Frame()
	instant := bytes.Clone(titlePixels())
	view.Advance(160 * time.Millisecond)
	if bytes.Equal(quiet, instant) || !bytes.Equal(titlePixels(), instant) {
		t.Fatal("the opening hint animated with Reduce Motion enabled")
	}
	checkViewport()
}
