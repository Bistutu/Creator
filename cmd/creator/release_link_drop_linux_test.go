package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/profile"
)

func TestLinuxInstalledCreatorReceivesWebsiteLink(t *testing.T) {
	installed, managerEntry, _ := portableInstalledFixture(t)
	if os.Getenv("DISPLAY") == "" || os.Getenv("WAYLAND_DISPLAY") != "" {
		t.Skip("run the link drop with Xvfb and its own D-Bus session")
	}
	t.Setenv("GTK_A11Y", "1")
	t.Setenv("NO_AT_BRIDGE", "0")
	const address = "https://www.xiaohongshu.com/explore/topic?from=creator#notes"
	launchPortableMenu(t, managerEntry, installed, appName, "")
	manager := linuxFixtureWindow(t, appName)
	pid := linuxWindowPID(t, manager)
	linuxXDo(t, "windowmove", manager, "500", "56")
	findLinuxControl(t, pid, "应用库").click(t)
	target := findLinuxControl(t, pid, "创建第一个应用")
	dragLinuxFixtureLink(t, pid, address, target)
	search := findLinuxControl(t, pid, "网址或应用名称")
	if search.Text != address || !search.Focused {
		t.Fatalf("drop left the search in state %+v for %q", search, address)
	}
	findLinuxControl(t, pid, "使用网址 小红书").click(t)
	if got := findLinuxControl(t, pid, "应用网址").Text; got != address {
		t.Fatalf("the editor received %q from the drop, want %q", got, address)
	}
	findLinuxControl(t, pid, "创建应用").click(t)
	findLinuxControl(t, pid, "打开 小红书")
	store := profile.NewStore(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WebCrate", profile.Filename))
	if apps, err := store.List(); err != nil || len(apps) != 1 || apps[0].URL != address {
		t.Fatalf("installed Creator saved %+v from the dragged link (%v)", apps, err)
	}
	closeLinuxTestWindow(t, manager, pid)
	t.Log("Dragged a GTK URI list across native windows in Xvfb and created the application with its complete address.")
}

func dragLinuxFixtureLink(t *testing.T, pid, value string, target linuxControl) {
	t.Helper()
	// A separate GTK process owns the drag data just like a browser.
	const source = `import ctypes as c
import sys

gtk = c.CDLL('libgtk-3.so.0')
gobj = c.CDLL('libgobject-2.0.so.0')
ptr, string, num = c.c_void_p, c.c_char_p, c.c_int
def bind(lib, name, out, *args):
    fn = getattr(lib, name)
    fn.argtypes, fn.restype = args, out
    return fn

class Target(c.Structure):
    _fields_ = [('name', string), ('flags', c.c_uint), ('info', c.c_uint)]

if not bind(gtk, 'gtk_init_check', num, ptr, ptr)(None, None):
    raise SystemExit('could not open the Xvfb drag source')
window = bind(gtk, 'gtk_window_new', ptr, num)(0)
bind(gtk, 'gtk_window_set_title', None, ptr, string)(window, b'Creator link drag fixture')
bind(gtk, 'gtk_window_set_default_size', None, ptr, num, num)(window, 160, 52)
bind(gtk, 'gtk_window_move', None, ptr, num, num)(window, 48, 174)
button = bind(gtk, 'gtk_button_new_with_label', ptr, string)(b'Test website link')
bind(gtk, 'gtk_container_add', None, ptr, ptr)(window, button)
target = Target(b'text/uri-list', 0, 0)
set_uris = bind(gtk, 'gtk_selection_data_set_uris', num, ptr, c.POINTER(string))
uris = (string * 2)(sys.argv[1].encode(), None)
@c.CFUNCTYPE(None, ptr, ptr, ptr, c.c_uint, c.c_uint, ptr)
def provide(widget, context, selection, info, timestamp, data):
    set_uris(selection, uris)
bind(gobj, 'g_signal_connect_data', c.c_ulong, ptr, string, ptr, ptr, ptr, num)(
    button, b'drag-data-get', provide, None, None, 0)
bind(gtk, 'gtk_drag_source_set', None, ptr, num, c.POINTER(Target), num, num)(
    button, 1 << 8, c.byref(target), 1, 1 << 1)
bind(gtk, 'gtk_widget_show_all', None, ptr)(window)
gtk.gtk_main()`
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	command := exec.CommandContext(ctx, "/usr/bin/python3", "-c", source, value)
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = command.Wait()
	}()
	window := linuxFixtureWindow(t, "Creator link drag fixture")
	defer linuxXDo(t, "mouseup", "1")
	linuxXDo(t, "mousemove", "--window", window, "48", "26", "mousedown", "1", "mousemove", "--window", window, "80", "26")
	linuxXDo(t, "sleep", "0.12", "mousemove", strconv.Itoa(target.X), strconv.Itoa(target.Y), "sleep", "0.14", "mouseup", "1")
	// The source must continue serving its URI until the receiver has read it.
	findLinuxControl(t, pid, "网址或应用名称")
}
