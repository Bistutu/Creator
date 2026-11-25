package webwindow

import (
	"github.com/ebitengine/purego"
	"github.com/Bistutu/Creator/internal/profile"
)

const (
	gtkResponseAllow = -3 // GTK_RESPONSE_ACCEPT
	gtkResponseDeny  = -6 // GTK_RESPONSE_CANCEL
)

// gtkMedia keeps a pending request on the GTK thread until its native dialog
// closes. The request belongs to one view and ends when that view navigates.
type gtkMedia struct {
	gtk *gtkPopups

	signalID   uint32
	find       func(instance uintptr, mask, signal, detail uint32, closure, fn, data uintptr) uintptr
	disconnect func(instance, handler uintptr)
	ref        func(request uintptr) uintptr
	unref      func(request uintptr)
	mediaType  uintptr
	isCamera   func(request uintptr) bool
	isMic      func(request uintptr) bool
	isDisplay  func(request uintptr) bool
	allow      func(request uintptr)
	deny       func(request uintptr)
	settings   func(view uintptr) uintptr
	enable     func(settings uintptr, enabled bool)
	uri        func(view uintptr) string

	visible         func(widget uintptr) bool
	createDialog    func() uintptr
	modal           func(window uintptr, modal bool)
	area            func(dialog uintptr) uintptr
	border          func(container uintptr, width uint32)
	label           func(text string) uintptr
	wrap            func(label uintptr, wrap bool)
	button          func(dialog uintptr, text string, response int32) uintptr
	defaultResponse func(dialog uintptr, response int32)

	request, response, destroyDialog, loadChanged, destroyView uintptr
	pending                                                    gtkMediaRequest
}

type gtkMediaRequest struct {
	view, dialog, request uintptr
}

func newGTKMedia(gtk *gtkPopups, libGTK, webkit, webType uintptr) *gtkMedia {
	const displaySymbol = "webkit_user_media_permission_is_for_display_device"
	if _, err := purego.Dlsym(webkit, displaySymbol); err != nil {
		return nil // Older engines cannot distinguish camera and screen capture.
	}
	m := gtkMedia{gtk: gtk}
	var lookup func(name string, receiver uintptr) uint32
	var mediaType func() uintptr
	purego.RegisterLibFunc(&lookup, libGTK, "g_signal_lookup")
	m.signalID = lookup("permission-request", webType)
	if m.signalID == 0 {
		return nil
	}
	purego.RegisterLibFunc(&m.find, libGTK, "g_signal_handler_find")
	purego.RegisterLibFunc(&m.disconnect, libGTK, "g_signal_handler_disconnect")
	purego.RegisterLibFunc(&m.ref, libGTK, "g_object_ref")
	purego.RegisterLibFunc(&m.unref, libGTK, "g_object_unref")
	purego.RegisterLibFunc(&mediaType, webkit, "webkit_user_media_permission_request_get_type")
	m.mediaType = mediaType()
	purego.RegisterLibFunc(&m.isCamera, webkit, "webkit_user_media_permission_is_for_video_device")
	purego.RegisterLibFunc(&m.isMic, webkit, "webkit_user_media_permission_is_for_audio_device")
	purego.RegisterLibFunc(&m.isDisplay, webkit, displaySymbol)
	purego.RegisterLibFunc(&m.allow, webkit, "webkit_permission_request_allow")
	purego.RegisterLibFunc(&m.deny, webkit, "webkit_permission_request_deny")
	purego.RegisterLibFunc(&m.settings, webkit, "webkit_web_view_get_settings")
	purego.RegisterLibFunc(&m.enable, webkit, "webkit_settings_set_enable_media_stream")
	purego.RegisterLibFunc(&m.uri, webkit, "webkit_web_view_get_uri")
	purego.RegisterLibFunc(&m.visible, libGTK, "gtk_widget_get_visible")
	purego.RegisterLibFunc(&m.createDialog, libGTK, "gtk_dialog_new")
	purego.RegisterLibFunc(&m.modal, libGTK, "gtk_window_set_modal")
	purego.RegisterLibFunc(&m.area, libGTK, "gtk_dialog_get_content_area")
	purego.RegisterLibFunc(&m.border, libGTK, "gtk_container_set_border_width")
	purego.RegisterLibFunc(&m.label, libGTK, "gtk_label_new")
	purego.RegisterLibFunc(&m.wrap, libGTK, "gtk_label_set_line_wrap")
	purego.RegisterLibFunc(&m.button, libGTK, "gtk_dialog_add_button")
	purego.RegisterLibFunc(&m.defaultResponse, libGTK, "gtk_dialog_set_default_response")
	m.request = purego.NewCallback(m.requestPermission)
	m.response = purego.NewCallback(func(dialog uintptr, response int32, _ uintptr) {
		m.finish(dialog, response == gtkResponseAllow)
		gtk.destroy(dialog)
	})
	m.destroyDialog = purego.NewCallback(func(dialog, _ uintptr) { m.finish(dialog, false) })
	m.loadChanged = purego.NewCallback(func(view uintptr, event int32, _ uintptr) {
		if event == 0 { // WEBKIT_LOAD_STARTED
			m.dismiss(view)
		}
	})
	m.destroyView = purego.NewCallback(func(view, _ uintptr) { m.dismiss(view) })
	return &m
}

func (m *gtkMedia) bind(view, parent uintptr, fromMyGo bool) {
	var previous uintptr
	if fromMyGo {
		// This view has MyGo's synchronous permission-request handler.
		// The standalone website uses a dialog and completes the request
		// asynchronously. Other permissions keep WebKit's default policy.
		const matchSignalID = 1 // G_SIGNAL_MATCH_ID
		previous = m.find(view, matchSignalID, m.signalID, 0, 0, 0, 0)
	}
	if m.gtk.connect(view, "permission-request", m.request, parent, 0, 0) == 0 {
		return
	}
	if previous != 0 {
		m.disconnect(view, previous)
	}
	m.gtk.connect(view, "load-changed", m.loadChanged, 0, 0, 0)
	m.gtk.connect(view, "destroy", m.destroyView, 0, 0, 0)
	m.enable(m.settings(view), true)
}

func (m *gtkMedia) requestPermission(view, request, parent uintptr) bool {
	if !m.gtk.instanceIsA(request, m.mediaType) {
		return false
	}
	camera, mic := m.isCamera(request), m.isMic(request)
	if m.isDisplay(request) || (!camera && !mic) || m.pending.dialog != 0 || !m.visible(parent) {
		m.deny(request)
		return true
	}
	device := "麦克风"
	if camera && mic {
		device = "摄像头和麦克风"
	} else if camera {
		device = "摄像头"
	}
	dialog := m.createDialog()
	m.pending = gtkMediaRequest{view: view, dialog: dialog, request: m.ref(request)}
	m.gtk.windowSetParent(dialog, parent)
	m.gtk.windowDestroyWithParent(dialog, true)
	m.modal(dialog, true)
	m.gtk.windowSetTitle(dialog, device+"权限")
	text := "允许此窗口内的网页使用" + device + "？"
	// WebKitGTK supplies device types here. The top-level page is provided
	// for context because a delegated frame can be the actual requester.
	if host := profile.Host(m.uri(view)); host != "" {
		text = host + "\n\n" + text
	}
	area, label := m.area(dialog), m.label(text)
	m.border(area, 18)
	m.wrap(label, true)
	m.gtk.add(area, label)
	m.button(dialog, "拒绝", gtkResponseDeny)
	m.button(dialog, "允许", gtkResponseAllow)
	m.defaultResponse(dialog, gtkResponseDeny)
	m.gtk.connect(dialog, "response", m.response, 0, 0, 0)
	m.gtk.connect(dialog, "destroy", m.destroyDialog, 0, 0, 0)
	m.gtk.show(dialog)
	m.gtk.present(dialog)
	return true
}

func (m *gtkMedia) dismiss(view uintptr) {
	if m.pending.view == view && m.pending.dialog != 0 {
		m.gtk.destroy(m.pending.dialog)
	}
}

func (m *gtkMedia) finish(dialog uintptr, allow bool) {
	if dialog != m.pending.dialog || m.pending.request == 0 {
		return
	}
	request := m.pending.request
	m.pending = gtkMediaRequest{}
	if allow {
		m.allow(request)
	} else {
		m.deny(request)
	}
	m.unref(request)
}
