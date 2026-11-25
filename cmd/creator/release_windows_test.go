package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Bistutu/Creator/internal/launcher"
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
		actual, index, err := launcher.WindowsShortcutIcon(entry)
		if err != nil || index != 0 {
			t.Fatalf("invalid shortcut icon %q at index %d: %v", actual, index, err)
		}
		checkWindowsMenuPath(t, "shortcut icon", actual, icon)
	}
	const script = `
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$process = Start-Process -FilePath $env:CREATOR_TEST_ENTRY -PassThru
if ($null -eq $process) { throw 'The shortcut did not start an application' }
try {
  $deadline = [DateTime]::UtcNow.AddSeconds(12)
  do {
    $process.Refresh()
    if ($process.HasExited) { throw "Application exited early: $($process.ExitCode)" }
    if ($process.MainWindowHandle -ne [IntPtr]::Zero -and $process.MainWindowTitle -ceq $env:CREATOR_TEST_NAME) {
      [Console]::Write((@{ pid = $process.Id; executable = $process.Path; window = $process.MainWindowHandle.ToInt64() } | ConvertTo-Json -Compress))
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
	command.Env = append(os.Environ(), "CREATOR_TEST_ENTRY="+entry, "CREATOR_TEST_NAME="+name)
	output, err := command.Output()
	if err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			output = failure.Stderr
		}
		t.Fatalf("opening Start menu shortcut %s: %v (%s)", entry, err, output)
	}
	var result struct {
		PID        int
		Executable string
		Window     uintptr
	}
	if err := json.Unmarshal(output, &result); err != nil || result.PID <= 0 || result.Window == 0 {
		t.Fatalf("invalid application process %q: %v", output, err)
	}
	checkWindowsMenuPath(t, "started executable", result.Executable, executable)
	stop := stopTestProcess(t, result.PID, func(process *os.Process) error {
		return stopWindowsTestProcess(process, result.Window)
	})
	return result.PID, stop
}

func checkWindowsMenuPath(t *testing.T, label, actual, expected string) {
	t.Helper()
	want, err := filepath.EvalSymlinks(expected)
	if err != nil {
		t.Fatal(err)
	}
	got, err := filepath.EvalSymlinks(actual)
	if err != nil || !strings.EqualFold(got, want) {
		t.Fatalf("%s = %q (%v), want %q", label, actual, err, want)
	}
}

func stopWindowsTestProcess(process *os.Process, window uintptr) error {
	var graceful bool
	_ = process.WithHandle(func(handle uintptr) {
		if windowsTestProcessExited(handle, 0) {
			graceful = true
			return
		}
		user := windows.NewLazySystemDLL("user32.dll")
		var owner uint32
		user.NewProc("GetWindowThreadProcessId").Call(window, uintptr(unsafe.Pointer(&owner)))
		if owner == uint32(process.Pid) {
			const closeWindow = 0x0010 // WM_CLOSE
			user.NewProc("PostMessageW").Call(window, closeWindow, 0, 0)
			graceful = windowsTestProcessExited(handle, 4000)
		}
	})
	if graceful {
		return nil
	}
	err := process.Kill()
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return err
	}
	// Windows rejects TerminateProcess after a normal exit. Verify the
	// original process handle is signaled before accepting that result.
	var exited bool
	handleErr := process.WithHandle(func(handle uintptr) {
		exited = windowsTestProcessExited(handle, 0)
	})
	if handleErr == nil && exited {
		return nil
	}
	return err
}

func windowsTestProcessExited(handle uintptr, wait uint32) bool {
	state, err := windows.WaitForSingleObject(windows.Handle(handle), wait)
	return err == nil && state == windows.WAIT_OBJECT_0
}
