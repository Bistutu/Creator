package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/Bistutu/Creator/internal/settings"
)

//go:embed testdata/windows_creator_preferences.ps1
var windowsPreferencesCheck string

func TestWindowsInstalledCreatorPreferences(t *testing.T) {
	installed, menuEntry, state := portableInstalledFixture(t)
	path := filepath.Join(state, "WebCrate", "preferences.json")
	assertSaved := func(want settings.Preferences) {
		t.Helper()
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
		if got, err := settings.Load(path); err != nil || got != want {
			t.Fatalf("installed Creator saved %+v (%v), want %+v", got, err, want)
		}
	}
	encoded := windowsEncodedCommand(windowsPreferencesCheck)
	for _, restarted := range []bool{false, true} {
		checkWindowsCreatorPreferences(t, installed, menuEntry, encoded, restarted)
		want := settings.Default()
		if !restarted {
			want.Appearance, want.StartPage, want.Width = settings.Dark, settings.LibraryPage, 960
		}
		assertSaved(want)
	}
}

func windowsEncodedCommand(source string) string {
	// CreateProcess preserves the UTF-16 script through PowerShell's encoded command.
	script := make([]byte, 0, len(source)*2)
	for _, unit := range utf16.Encode([]rune(source)) {
		script = binary.LittleEndian.AppendUint16(script, unit)
	}
	return base64.StdEncoding.EncodeToString(script)
}

func checkWindowsCreatorPreferences(t *testing.T, installed, menuEntry, encoded string, restarted bool) {
	t.Helper()
	pid, stop := launchWindowsMenu(t, menuEntry, installed, appName, "")
	defer stop()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-EncodedCommand", encoded)
	command.Env = append(os.Environ(),
		"CREATOR_TEST_PID="+strconv.Itoa(pid), "CREATOR_TEST_RESTARTED="+strconv.FormatBool(restarted),
		"CREATOR_TEST_ENTRY="+menuEntry,
		"CREATOR_TEST_VERSION="+creatorVersion(), "CREATOR_TEST_LICENSE="+creatorLicense,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installed Creator preferences (restarted=%t): %v\n%s", restarted, err, output)
	}
	t.Logf("%s", output)
}
