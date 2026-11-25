package webwindow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageZoomChangesStayWithTheApp(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	zoom := LoadPageZoom(first)
	if got := zoom.Factor(); got != 1 {
		t.Fatalf("a new app opens at zoom %g", got)
	}
	var applied float64
	for _, step := range []struct {
		change int
		want   float64
	}{{1, 1.10}, {1, 1.25}, {-1, 1.10}, {0, 1}, {-1, 0.90}} {
		if err := zoom.change(step.change, func(f float64) { applied = f }); err != nil {
			t.Fatal(err)
		}
		if got := LoadPageZoom(first).Factor(); got != step.want || applied != step.want {
			t.Fatalf("zoom after step %d = %g, saved zoom = %g, want %g", step.change, applied, got, step.want)
		}
	}
	if got := LoadPageZoom(second).Factor(); got != 1 {
		t.Fatalf("another app inherited zoom %g", got)
	}
}

func TestPageZoomLimitsAndInvalidPreferences(t *testing.T) {
	dir := t.TempDir()
	for _, saved := range []string{"not a number", "0", "111", "10000000", "125" + strings.Repeat(" ", maxZoomSize)} {
		if err := os.WriteFile(filepath.Join(dir, "page-zoom"), []byte(saved), 0600); err != nil {
			t.Fatal(err)
		}
		if got := LoadPageZoom(dir).Factor(); got != 1 {
			t.Fatalf("invalid preference %q gave zoom %g", saved, got)
		}
	}
	zoom := LoadPageZoom("")
	for _, limit := range []struct {
		step int
		want float64
	}{{1, 5}, {-1, 0.25}} {
		changes := 0
		for range len(zoomLevels) + 1 {
			if err := zoom.change(limit.step, func(float64) { changes++ }); err != nil {
				t.Fatal(err)
			}
		}
		if zoom.Factor() != limit.want || changes >= len(zoomLevels) {
			t.Fatalf("expected zoom to stop at %g, got %g after %d updates", limit.want, zoom.Factor(), changes)
		}
	}
}

func TestPageZoomStillWorksWhenSavingFails(t *testing.T) {
	dir := t.TempDir()
	zoom := LoadPageZoom(dir)
	if err := os.Mkdir(zoom.path, 0700); err != nil {
		t.Fatal(err)
	}
	var applied float64
	if err := zoom.change(1, func(f float64) { applied = f }); err == nil || applied != 1.10 {
		t.Fatalf("could not change the visible zoom after a settings failure: %g, %v", applied, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("failed to clean up the unsaved preference: %v, %v", entries, err)
	}
	zoom = LoadPageZoom(filepath.Join(dir, "unavailable"))
	if err := zoom.change(-1, func(f float64) { applied = f }); err == nil || applied != 0.90 {
		t.Fatalf("expected live zoom to work with an unavailable preferences directory: %g, %v", applied, err)
	}
}
