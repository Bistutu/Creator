package webwindow

import (
	"time"

	"github.com/egoist/mygo/ui"
)

const openingDelay = 200 * time.Millisecond
const openingLabel = "正在载入…"

var openingMotion = ui.ElementTransition{
	Duration: 120 * time.Millisecond,
	Enter:    &ui.Motion{},
}

// OpeningStatus shows a hint only during the first navigation. Its timer
// returns to the UI thread before touching the window or changing state.
type OpeningStatus struct {
	timer   *time.Timer
	visible bool
}

func (s *OpeningStatus) Start(dispatch func(func()), redraw func()) {
	s.timer = time.AfterFunc(openingDelay, func() {
		dispatch(func() {
			if s.timer == nil {
				return
			}
			s.timer = nil
			s.visible = true
			redraw()
		})
	})
}

// Finish also cancels an update that is already queued on the UI thread.
func (s *OpeningStatus) Finish() bool {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	visible := s.visible
	s.visible = false
	return visible
}

func (s *OpeningStatus) TitleBar(c *ui.Context) {
	bar := c.TitleBar()
	ui.Row(c).Height(max(32, bar.Height, bar.Bottom)).Padding(0, bar.Right+14, 0, bar.Left+14).
		AlignItems(ui.Center).Justify(ui.End).DragWindow().Children(func() {
		if s.visible {
			label := ui.Text(c.Key("opening-status"), openingLabel).Role(ui.RoleStatus).
				FontSize(12).TextColor(c.Theme().TextMuted)
			if !c.Preferences().ReduceMotion {
				label.Transition(openingMotion)
			}
		}
	})
}
