package main

import (
	"fmt"
	"image/color"
	"strconv"
	"testing"
)

// Sample the real Xvfb framebuffer used by the installed Creator window.
func assertLinuxIconPaint(t *testing.T, x, y int, want color.NRGBA) {
	t.Helper()
	const sample = `import ctypes as c
import sys, time

x, y, wanted = int(sys.argv[1]), int(sys.argv[2]), int(sys.argv[3], 16)
x11 = c.CDLL('libX11.so.6')
x11.XOpenDisplay.argtypes = [c.c_char_p]
x11.XOpenDisplay.restype = c.c_void_p
x11.XDefaultRootWindow.argtypes = [c.c_void_p]
x11.XDefaultRootWindow.restype = c.c_ulong
x11.XGetImage.argtypes = [c.c_void_p, c.c_ulong, c.c_int, c.c_int, c.c_uint, c.c_uint, c.c_ulong, c.c_int]
x11.XGetImage.restype = c.c_void_p
x11.XGetPixel.argtypes = [c.c_void_p, c.c_int, c.c_int]
x11.XGetPixel.restype = c.c_ulong
x11.XDestroyImage.argtypes = [c.c_void_p]
x11.XCloseDisplay.argtypes = [c.c_void_p]

display = x11.XOpenDisplay(None)
if not display:
    raise SystemExit('could not open the test display')
root = x11.XDefaultRootWindow(display)
deadline = time.monotonic() + 4
observed = None
try:
    while time.monotonic() < deadline:
        image = x11.XGetImage(display, root, x, y, 1, 1, c.c_ulong(-1).value, 2)
        if image:
            observed = x11.XGetPixel(image, 0, 0) & 0xFFFFFF
            x11.XDestroyImage(image)
            if observed == wanted:
                break
        time.sleep(0.05)
    else:
        raise SystemExit(f'pixel ({x}, {y}) was {observed!r}, wanted 0x{wanted:06x}')
finally:
    x11.XCloseDisplay(display)`
	linuxWindowCommand(t, "/usr/bin/python3", "-c", sample, strconv.Itoa(x), strconv.Itoa(y), fmt.Sprintf("%02x%02x%02x", want.R, want.G, want.B))
}
