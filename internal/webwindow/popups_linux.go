package webwindow

import (
	"sync"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
)

// BindRelatedPopups is called on the GTK thread after MyGo installs its native
// web view. The caller disables MyGo's independent windows when this succeeds.
func BindRelatedPopups(owner *mygo.Window, downloads *Downloads) bool {
	window := owner.NativeHandle()
	if window == 0 {
		return false
	}
	gtk := linuxPopups()
	if gtk == nil {
		return false
	}
	var web uintptr
	gtk.foreach(window, gtk.visit, &web)
	if web == 0 {
		return false
	}
	// A related WebKit view shares the opener's user content manager. The
	// website runner uses native load callbacks and has no Go bindings.
	// Remove the bridge so a popup cannot send messages as the opener.
	manager := gtk.userContentManager(web)
	gtk.unregisterMessageHandler(manager, "mygo")
	gtk.removeScripts(manager)
	gtk.connect(web, "create", gtk.create, window, 0, 0)
	gtk.downloads.bindMain(web, window, owner, downloads)
	if gtk.media != nil {
		gtk.media.bind(web, window, true)
	}
	return true
}

type gtkPopups struct {
	connect                            func(object uintptr, signal string, callback, data, destroy uintptr, flags uint32) uintptr
	foreach                            func(container, callback uintptr, found *uintptr)
	instanceIsA                        func(widget, kind uintptr) bool
	windowNew                          func(kind int32) uintptr
	windowSize                         func(window uintptr, width, height int32)
	windowSetTitle                     func(window uintptr, title string)
	windowTitle                        func(window uintptr) string
	windowSetParent                    func(window, parent uintptr)
	windowDestroyWithParent            func(window uintptr, enabled bool)
	windowIcon                         func(window uintptr) uintptr
	windowSetIcon                      func(window, icon uintptr)
	add                                func(container, widget uintptr)
	show                               func(widget uintptr)
	present                            func(window uintptr)
	destroy                            func(widget uintptr)
	newRelatedView                     func(web uintptr) uintptr
	webTitle                           func(web uintptr) string
	request                            func(action uintptr) uintptr
	requestURI                         func(request uintptr) string
	userContentManager                 func(web uintptr) uintptr
	unregisterMessageHandler           func(manager uintptr, name string)
	removeScripts                      func(manager uintptr)
	visit, create, ready, title, close uintptr
	media                              *gtkMedia
	downloads                          *gtkPopupDownloads
}

var linuxPopups = sync.OnceValue(loadGTKPopups)

func loadGTKPopups() *gtkPopups {
	const flags = purego.RTLD_LAZY | purego.RTLD_LOCAL
	var webkit uintptr
	for _, library := range []string{"libwebkit2gtk-4.1.so.0", "libwebkit2gtk-4.0.so.37"} {
		if handle, err := purego.Dlopen(library, flags); err == nil {
			webkit = handle
			break
		}
	}
	if webkit == 0 {
		return nil
	}
	gtk, err := purego.Dlopen("libgtk-3.so.0", flags)
	if err != nil {
		_ = purego.Dlclose(webkit)
		return nil
	}
	var g gtkPopups
	var webType, containerType func() uintptr
	purego.RegisterLibFunc(&webType, webkit, "webkit_web_view_get_type")
	purego.RegisterLibFunc(&containerType, gtk, "gtk_container_get_type")
	purego.RegisterLibFunc(&g.connect, gtk, "g_signal_connect_data")
	purego.RegisterLibFunc(&g.foreach, gtk, "gtk_container_foreach")
	purego.RegisterLibFunc(&g.instanceIsA, gtk, "g_type_check_instance_is_a")
	purego.RegisterLibFunc(&g.windowNew, gtk, "gtk_window_new")
	purego.RegisterLibFunc(&g.windowSize, gtk, "gtk_window_set_default_size")
	purego.RegisterLibFunc(&g.windowSetTitle, gtk, "gtk_window_set_title")
	purego.RegisterLibFunc(&g.windowTitle, gtk, "gtk_window_get_title")
	purego.RegisterLibFunc(&g.windowSetParent, gtk, "gtk_window_set_transient_for")
	purego.RegisterLibFunc(&g.windowDestroyWithParent, gtk, "gtk_window_set_destroy_with_parent")
	purego.RegisterLibFunc(&g.windowIcon, gtk, "gtk_window_get_icon")
	purego.RegisterLibFunc(&g.windowSetIcon, gtk, "gtk_window_set_icon")
	purego.RegisterLibFunc(&g.add, gtk, "gtk_container_add")
	purego.RegisterLibFunc(&g.show, gtk, "gtk_widget_show_all")
	purego.RegisterLibFunc(&g.present, gtk, "gtk_window_present")
	purego.RegisterLibFunc(&g.destroy, gtk, "gtk_widget_destroy")
	purego.RegisterLibFunc(&g.newRelatedView, webkit, "webkit_web_view_new_with_related_view")
	purego.RegisterLibFunc(&g.webTitle, webkit, "webkit_web_view_get_title")
	purego.RegisterLibFunc(&g.request, webkit, "webkit_navigation_action_get_request")
	purego.RegisterLibFunc(&g.requestURI, webkit, "webkit_uri_request_get_uri")
	purego.RegisterLibFunc(&g.userContentManager, webkit, "webkit_web_view_get_user_content_manager")
	purego.RegisterLibFunc(&g.unregisterMessageHandler, webkit, "webkit_user_content_manager_unregister_script_message_handler")
	purego.RegisterLibFunc(&g.removeScripts, webkit, "webkit_user_content_manager_remove_all_scripts")
	web, container := webType(), containerType()
	g.media = newGTKMedia(&g, gtk, webkit, web)
	g.downloads = newGTKPopupDownloads(&g, gtk, webkit)
	g.visit = purego.NewCallback(func(widget uintptr, found *uintptr) {
		if *found != 0 {
			return
		}
		if g.instanceIsA(widget, web) {
			*found = widget
		} else if g.instanceIsA(widget, container) {
			g.foreach(widget, g.visit, found)
		}
	})
	g.create = purego.NewCallback(g.open)
	g.ready = purego.NewCallback(func(_, window uintptr) {
		g.show(window)
		g.present(window)
	})
	g.close = purego.NewCallback(func(_, window uintptr) { g.destroy(window) })
	g.title = purego.NewCallback(func(view, _, window uintptr) {
		if title := g.webTitle(view); title != "" {
			g.windowSetTitle(window, title)
		}
	})
	return &g
}

func (g *gtkPopups) open(opener, action, parent uintptr) uintptr {
	if !PopupAddress(g.requestURI(g.request(action))) {
		return 0
	}
	// WebKit supplies the navigation and preserves the opener relationship.
	view := g.newRelatedView(opener)
	if view == 0 {
		return 0
	}
	window := g.windowNew(0) // GTK_WINDOW_TOPLEVEL
	g.windowSize(window, 880, 650)
	g.windowSetTitle(window, g.windowTitle(parent))
	g.windowSetParent(window, parent)
	g.windowDestroyWithParent(window, true)
	g.windowSetIcon(window, g.windowIcon(parent))
	g.add(window, view)
	g.connect(view, "ready-to-show", g.ready, window, 0, 0)
	g.connect(view, "close", g.close, window, 0, 0)
	g.connect(view, "notify::title", g.title, window, 0, 0)
	g.connect(view, "create", g.create, window, 0, 0)
	g.downloads.bindPopup(view, window, opener)
	if g.media != nil {
		g.media.bind(view, window, false)
	}
	return view
}
