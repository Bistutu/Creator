package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/Bistutu/Creator/internal/profile"
)

func portableFixtureNames() (string, string) {
	// Both names must be usable without replacing files in the application.
	return "website.json", "AppIcon.png"
}

func generatedMenu(t *testing.T, app profile.App, executable string) (entry, icon string) {
	t.Helper()
	return filepath.Join(os.Getenv("XDG_DATA_HOME"), "applications", "com.webcrate."+app.ID+".desktop"),
		filepath.Join(filepath.Dir(executable), "AppIcon.png")
}

func launchPortableMenu(t *testing.T, entry, executable, name, icon string) func() {
	t.Helper()
	data, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	lines := "\n" + string(data)
	if !strings.Contains(lines, "\nName="+name+"\n") || (icon != "" && !strings.Contains(lines, "\nIcon="+icon+"\n")) {
		t.Fatalf("desktop entry %s has the wrong name or icon", entry)
	}
	if out, err := exec.CommandContext(t.Context(), "desktop-file-validate", entry).CombinedOutput(); err != nil {
		t.Fatalf("desktop entry validation: %v (%s)", err, out)
	}
	pid := startLinuxDesktopEntry(t, entry)
	stop := stopTestProcess(t, pid)
	expected, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	tool, err := exec.LookPath("xdotool")
	if err != nil {
		t.Fatal(err)
	}
	processLink := fmt.Sprintf("/proc/%d/exe", pid)
	var actual string
	var readErr error
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		// GIO may publish the PID of gio-launch-desktop just before it
		// execs the requested application in the same process.
		actual, readErr = os.Readlink(processLink)
		if readErr == nil && actual == expected && exec.CommandContext(t.Context(), tool,
			"search", "--all", "--onlyvisible", "--pid", strconv.Itoa(pid),
			"--name", "^"+regexp.QuoteMeta(name)+"$").Run() == nil {
			return stop
		}
	}
	if readErr != nil || actual != expected {
		t.Fatalf("desktop entry started %q, want %q (%v)", actual, expected, readErr)
	}
	t.Fatalf("process %d did not show the %q window", pid, name)
	return stop
}

func startLinuxDesktopEntry(t *testing.T, entry string) int {
	t.Helper()
	gio, err := purego.Dlopen("libgio-2.0.so.0", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = purego.Dlclose(gio) })
	var newInfo func(string) uintptr
	var newContext func() uintptr
	var setenv func(uintptr, string, string)
	var unref func(uintptr)
	var launch func(uintptr, uintptr, uintptr, uint32, uintptr, uintptr, uintptr, uintptr, uintptr) int32
	purego.RegisterLibFunc(&newInfo, gio, "g_desktop_app_info_new_from_filename")
	purego.RegisterLibFunc(&newContext, gio, "g_app_launch_context_new")
	purego.RegisterLibFunc(&setenv, gio, "g_app_launch_context_setenv")
	purego.RegisterLibFunc(&unref, gio, "g_object_unref")
	purego.RegisterLibFunc(&launch, gio, "g_desktop_app_info_launch_uris_as_manager")
	info := newInfo(entry)
	if info == 0 {
		t.Fatalf("GIO could not resolve %s", entry)
	}
	defer unref(info)
	context := newContext()
	defer unref(context)
	for _, variable := range []string{
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR", "no_proxy", "NO_PROXY",
		"GTK_A11Y", "NO_AT_BRIDGE", "GTK_USE_PORTAL", "LC_ALL", "GOCOVERDIR",
	} {
		if value, ok := os.LookupEnv(variable); ok {
			setenv(context, variable, value)
		}
	}
	var pid int
	published := purego.NewCallback(func(_ uintptr, child int32, _ uintptr) { pid = int(child) })
	const spawnDoNotReapChild = 2
	if launch(info, 0, context, spawnDoNotReapChild, 0, 0, published, 0, 0) == 0 || pid <= 0 {
		t.Fatalf("GIO could not start a process from %s", entry)
	}
	return pid
}
