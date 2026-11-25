package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/sys/windows"
)

func portableFixtureNames() (string, string) {
	suffix := profile.NewID()[4:12]
	return "Creator 验收 & 50% " + suffix, "Creator 工作台 " + suffix
}

func generatedMenu(t *testing.T, _ profile.App, executable string) (entry, icon string) {
	t.Helper()
	programs, err := windows.KnownFolderPath(windows.FOLDERID_Programs, windows.KF_FLAG_DONT_VERIFY)
	if err != nil {
		t.Fatal(err)
	}
	stem := strings.TrimSuffix(filepath.Base(executable), filepath.Ext(executable))
	return filepath.Join(programs, "Creator", stem+".lnk"), filepath.Join(filepath.Dir(executable), "AppIcon.ico")
}

func launchPortableMenu(t *testing.T, entry, executable, name, icon string) func() {
	t.Helper()
	_, stop := launchWindowsMenu(t, entry, executable, name, icon)
	return stop
}

func launchWindowsMenu(t *testing.T, entry, executable, name, icon string) (int, func()) {
	t.Helper()
	if info, err := os.Stat(entry); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("invalid Start menu shortcut %s: %v", entry, err)
	}
	if icon != "" {
		contents, err := os.ReadFile(icon)
		if err != nil || len(contents) < 6 || contents[2] != 1 {
			t.Fatalf("invalid installed ICO %s: %v", icon, err)
		}
	}
	const script = `
$ErrorActionPreference = 'Stop'
$link = (New-Object -ComObject WScript.Shell).CreateShortcut($env:CREATOR_TEST_ENTRY)
if ($link.TargetPath -ine $env:CREATOR_TEST_EXE) { throw "Unexpected shortcut target: $($link.TargetPath)" }
if ($env:CREATOR_TEST_ICON) {
  $location = $link.IconLocation
  if ($location -notmatch ',\s*0$' -or ($location -replace ',\s*0$', '') -ine $env:CREATOR_TEST_ICON) {
    throw "Unexpected shortcut icon: $location"
  }
}
$process = Start-Process -FilePath $env:CREATOR_TEST_ENTRY -PassThru
if ($null -eq $process -or $process.Path -ine $env:CREATOR_TEST_EXE) { throw 'The shortcut did not start the installed application' }
try {
  $deadline = [DateTime]::UtcNow.AddSeconds(12)
  do {
    $process.Refresh()
    if ($process.HasExited) { throw "Application exited early: $($process.ExitCode)" }
    if ($process.MainWindowHandle -ne [IntPtr]::Zero -and $process.MainWindowTitle -ceq $env:CREATOR_TEST_NAME) {
      [Console]::Write($process.Id)
      exit 0
    }
    Start-Sleep -Milliseconds 100
  } while ([DateTime]::UtcNow -lt $deadline)
  throw "Application did not display the expected window: $env:CREATOR_TEST_NAME"
} catch {
  if (!$process.HasExited) { $process.Kill(); $process.WaitForExit() }
  throw
}`
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "CREATOR_TEST_ENTRY="+entry, "CREATOR_TEST_EXE="+executable,
		"CREATOR_TEST_NAME="+name, "CREATOR_TEST_ICON="+icon)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("opening Start menu shortcut %s: %v (%s)", entry, err, output)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || pid <= 0 {
		t.Fatalf("invalid application process %q: %v", output, err)
	}
	return pid, stopTestProcess(t, pid)
}
