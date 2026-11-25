package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	windowWidth       = 448
	windowHeight      = 640
	minWindowWidth    = 388
	minWindowHeight   = 502
	titleHeight       = 34
	brandRowHeight    = 34
	pagePadding       = 17
	lightBackground   = "#faf9f6"
	darkBackground    = "#1b1c1f"
	cardRadius        = 10
	dialogRadius      = 15
	fieldRadius       = 9
	buttonRadius      = 7
	dimensionDuration = 140 * time.Millisecond
)

var (
	settleEase    = ui.CubicBezier(0.2, 0.68, 0.25, 1)
	controlMotion = ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond, Ease: settleEase}
	pageMotion    = ui.ElementTransition{
		Duration: 140 * time.Millisecond, Ease: settleEase,
		Enter: &ui.Motion{Y: 2, Opacity: 0.72}, Exit: &ui.Motion{Opacity: 0}, ExitDuration: 90 * time.Millisecond,
	}
	dialogMotion = ui.ElementTransition{
		Colors: true, Duration: 155 * time.Millisecond, Ease: settleEase,
		Enter: &ui.Motion{}, Exit: &ui.Motion{}, ExitDuration: 100 * time.Millisecond,
	}
	lightTheme = crateTheme(false)
	darkTheme  = crateTheme(true)
)

func applyCreatorTheme(c *ui.Context) {
	if c.Theme().Dark {
		c.SetTheme(darkTheme)
	} else {
		c.SetTheme(lightTheme)
	}
	if c.Preferences().ReduceMotion {
		c.DisableAnimations()
	}
}

func crateTheme(dark bool) *ui.Theme {
	t := ui.LightTheme()
	t.Background = ui.Hex(lightBackground)
	t.Surface = ui.Hex("#efeeea")
	t.SurfaceHover = ui.Hex("#eae9e5")
	t.SurfacePressed = ui.Hex("#e5e9f7")
	t.Border = ui.Hex("#e0dfdb")
	t.Text = ui.Hex("#272727")
	t.TextMuted = ui.Hex("#626168")
	t.Accent = ui.Hex("#3d54ae")
	t.AccentHover = ui.Hex("#324898")
	t.AccentPressed = ui.Hex("#283d83")
	if dark {
		t = ui.DarkTheme()
		t.Background = ui.Hex(darkBackground)
		t.Surface = ui.Hex("#25262a")
		t.SurfaceHover = ui.Hex("#2a2c31")
		t.SurfacePressed = ui.Hex("#31364a")
		t.Border = ui.Hex("#36373c")
		t.Text = ui.Hex("#eeedef")
		t.TextMuted = ui.Hex("#aba9b2")
		t.Accent = ui.Hex("#b3c2ff")
		t.AccentHover = ui.Hex("#cad5ff")
		t.AccentPressed = ui.Hex("#9eaff5")
		t.AccentText = ui.Hex("#1c2548")
	}
	t.InactiveAccent = t.Accent
	t.Radius = buttonRadius
	t.Spacing = 4
	return t
}
