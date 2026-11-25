package webwindow

import (
	"github.com/ebitengine/purego"
	"github.com/Bistutu/Creator/internal/profile"
)

// SetWindowIdentity gives GTK the desktop entry's ID before initialization.
// GTK uses it for the X11 window class and the Wayland application ID.
func SetWindowIdentity(id string) error {
	glib, err := purego.Dlopen("libglib-2.0.so.0", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		return err
	}
	symbol, err := purego.Dlsym(glib, "g_set_prgname")
	if err != nil {
		_ = purego.Dlclose(glib)
		return err
	}
	var setName func(string)
	purego.RegisterFunc(&setName, symbol)
	setName(profile.LinuxDesktopID(id))
	// Keep GLib loaded so the program name survives until GTK initializes.
	return nil
}
