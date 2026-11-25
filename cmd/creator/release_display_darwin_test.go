package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/profile"
	"github.com/ebitengine/purego"
)

type macTestDisplay struct {
	name, probe string
}

func macWebKitLaunchGate(t *testing.T) func(time.Duration) {
	t.Helper()
	configured := os.Getenv("WEBCRATE_MAC_TEST_START_LOAD")
	if configured == "" {
		return func(time.Duration) {}
	}
	var getloadavg func(*float64, int32) int32
	purego.RegisterLibFunc(&getloadavg, purego.RTLD_DEFAULT, "getloadavg")
	cores := runtime.NumCPU()
	limit, err := strconv.ParseFloat(configured, 64)
	if err != nil || !(limit >= 1 && limit <= float64(cores)) {
		t.Fatalf("invalid DeskPad test launch load limit %q", configured)
	}
	var peak float64
	t.Cleanup(func() {
		t.Logf("Peak sampled host load around WebKit launches was %.1f across %d logical CPUs (launch limit %.1f)", peak, cores, limit)
	})
	return func(dwell time.Duration) {
		t.Helper()
		time.Sleep(dwell)
		deadline := time.Now().Add(25 * time.Second)
		for {
			var load float64
			if getloadavg(&load, 1) != 1 {
				t.Fatal("macOS did not report host load before the next WebKit launch")
			}
			peak = max(peak, load)
			if load < limit {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("Host load %.1f stayed at or above %.1f before the next WebKit launch", load, limit)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func installedMacTestDisplay(t *testing.T) *macTestDisplay {
	t.Helper()
	name := os.Getenv("WEBCRATE_MAC_TEST_DISPLAY")
	if name == "" {
		return nil
	}
	probe := os.Getenv("WEBCRATE_MAC_TEST_DISPLAY_TOOL")
	if probe == "" {
		probe = filepath.Join(t.TempDir(), "mac-test-display")
		macCommand(t, "/usr/bin/xcrun", "clang", "-fobjc-arc", "-framework", "AppKit", "-framework", "CoreGraphics",
			projectPath("tools", "mac_test_display.m"), "-o", probe)
	}
	return &macTestDisplay{name: name, probe: probe}
}

func (d *macTestDisplay) prepareCreator(t *testing.T, home string, width, height int) {
	t.Helper()
	if d == nil {
		return
	}
	state := macCommand(t, d.probe, "creator", d.name, strconv.Itoa(width), strconv.Itoa(height))
	dir := filepath.Join(home, "Library", "Application Support", appName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "window-state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (d *macTestDisplay) prepareStandalone(t *testing.T, identifier string, app profile.App) {
	t.Helper()
	if d == nil {
		return
	}
	frame := macCommand(t, d.probe, "website", d.name, strconv.Itoa(app.Width), strconv.Itoa(app.Height))
	macCommand(t, "/usr/bin/defaults", "write", identifier, "WebsiteWindowFrame", "-dict", "frame", strconv.Quote(frame),
		"revision", strconv.Quote(strconv.FormatUint(app.WindowRevision, 10)))
}

func (d *macTestDisplay) prepareLegacyStandalone(t *testing.T, identifier string, app profile.App) string {
	t.Helper()
	frames := macCommand(t, d.probe, "website-legacy", d.name, strconv.Itoa(app.Width), strconv.Itoa(app.Height))
	legacy, expected, ok := strings.Cut(frames, "\n")
	if !ok || legacy == "" || expected == "" {
		t.Fatalf("could not encode an AppKit autosaved frame: %q", frames)
	}
	key := "NSWindow Frame Website-" + strconv.Itoa(app.Width) + "-" + strconv.Itoa(app.Height)
	if app.WindowRevision != 0 {
		key = "NSWindow Frame Website-settings-" + strconv.FormatUint(app.WindowRevision, 10)
	}
	macCommand(t, "/usr/bin/defaults", "delete", identifier, "WebsiteWindowFrame")
	macCommand(t, "/usr/bin/defaults", "write", identifier, key, "-string", strings.TrimSpace(legacy))
	return expected
}

func (d *macTestDisplay) verifyWindow(t *testing.T, pid int) {
	t.Helper()
	if d == nil {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	for {
		out, err := exec.CommandContext(ctx, d.probe, "verify", d.name, strconv.Itoa(pid)).CombinedOutput()
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("process %d did not show a window on %s", pid, d.name)
		}
		var failure *exec.ExitError
		if !errors.As(err, &failure) || failure.ExitCode() != 2 {
			t.Fatalf("could not verify window %d on %s: %v (%s)", pid, d.name, err, out)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("process %d did not show a window on %s", pid, d.name)
		case <-poll.C:
		}
	}
}
