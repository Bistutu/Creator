package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestWindowsInstalledWebsiteRuntimeUnavailable(t *testing.T) {
	installed, _, state := portableInstalledFixture(t)
	t.Setenv("LOCALAPPDATA", filepath.Join(state, "local"))
	address, rendered := installedWebsiteFixture(t)
	address += "#workspace"
	service := portableInstalledLibrary(t, installed, state)
	app, err := service.Save(t.Context(), profile.App{Name: "Creator runtime recovery " + profile.NewID()[4:12], URL: address},
		library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Remove(app.ID) })
	executable, err := service.Path(t.Context(), app, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, icon := generatedMenu(t, app, executable)
	checkHealthy := func(session string) {
		t.Helper()
		stop := launchPortableMenu(t, entry, executable, app.Name, icon)
		defer stop()
		select {
		case report := <-rendered:
			if report.State != session || report.URL != address {
				t.Fatalf("the website reported %+v, want %s with the %s session", report, address, session)
			}
		case <-time.After(12 * time.Second):
			t.Fatal("the installed application did not render the website")
		}
	}
	checkHealthy("new")

	// Select an empty fixed-runtime folder for the test launch only.
	originalRuntime := os.Getenv("WEBVIEW2_BROWSER_EXECUTABLE_FOLDER")
	t.Setenv("WEBVIEW2_BROWSER_EXECUTABLE_FOLDER", t.TempDir())
	pid, stop := launchWindowsMenu(t, entry, executable, app.Name, icon)
	defer stop()
	ctx, cancel := context.WithTimeout(t.Context(), 24*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-EncodedCommand", windowsEncodedCommand(windowsRuntimeRecoveryCheck))
	command.Env = append(os.Environ(), "CREATOR_TEST_PID="+strconv.Itoa(pid))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("checking the runtime recovery window: %v (%s)", err, output)
	}
	stop()
	select {
	case report := <-rendered:
		t.Fatalf("the application opened the website without user input: %+v", report)
	default:
	}

	t.Setenv("WEBVIEW2_BROWSER_EXECUTABLE_FOLDER", originalRuntime)
	checkHealthy("remembered")
	t.Log("The Start menu entry displayed WebView2 recovery and then reopened the website with its existing session.")
}

const windowsRuntimeRecoveryCheck = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes
$process = Get-Process -Id ([int]$env:CREATOR_TEST_PID)
$window = [System.Windows.Automation.AutomationElement]::FromHandle($process.MainWindowHandle)
function Find-Control([string]$name) {
    $condition = [System.Windows.Automation.PropertyCondition]::new(
        [System.Windows.Automation.AutomationElement]::NameProperty, $name)
    $deadline = [DateTime]::UtcNow.AddSeconds(4)
    do {
        $control = $window.FindFirst([System.Windows.Automation.TreeScope]::Descendants, $condition)
        if ($control -and !$control.Current.IsOffscreen) { return $control }
        Start-Sleep -Milliseconds 75
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Missing visible recovery control: $name"
}
[void](Find-Control '无法启动网页组件')
[void](Find-Control '请检查系统的 Microsoft Edge WebView2 Runtime，然后重新打开应用。')
$browser = Find-Control '在浏览器中打开'
if (!$browser.Current.IsEnabled -or $browser.Current.ControlType -ne [System.Windows.Automation.ControlType]::Button) {
    throw 'The browser action is not available as a button'
}
if (!$process.CloseMainWindow() -or !$process.WaitForExit(6000)) { throw 'The recovery window did not close' }
`
