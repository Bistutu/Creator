package webwindow

import (
	"errors"
	"net/url"
	"os"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
)

// gtkPopupDownloads bridges related WebKit views that MyGo does not own to
// the same file handling used by the main window. All signals run on GTK's thread.
type gtkPopupDownloads struct {
	gtk       *gtkPopups
	windows   map[uintptr]*gtkDownloadWindow
	transfers map[uintptr]*gtkPopupTransfer

	context        func(view uintptr) uintptr
	view           func(download uintptr) uintptr
	setDestination func(download uintptr, uri string)
	cancel         func(download uintptr)
	disconnect     func(receiver, handler uintptr)
	visible        func(window uintptr) bool
	newMessage     func(parent uintptr, flags, kind, buttons int32, format, text string) uintptr
	secondary      func(dialog uintptr, format, text string)
	addButton      func(dialog uintptr, label string, response int32) uintptr
	defaultButton  func(dialog uintptr, response int32)
	runDialog      func(dialog uintptr) int32
	ref            func(object uintptr) uintptr
	unref          func(object uintptr)

	started, destination, failed, finished, destroyed uintptr
}

type gtkDownloadWindow struct {
	window           uintptr
	root             *gtkDownloadWindow
	downloads        *Downloads
	show             func(string)
	context, handler uintptr
}

type gtkPopupTransfer struct {
	window *gtkDownloadWindow
	path   string
	err    error
}

// Only the message of the borrowed GLib GError is needed before the signal returns.
type gtkDownloadError struct {
	domain  uint32
	code    int32
	message *byte
}

func newGTKPopupDownloads(gtk *gtkPopups, libGTK, webkit uintptr) *gtkPopupDownloads {
	f := &gtkPopupDownloads{
		gtk:       gtk,
		windows:   make(map[uintptr]*gtkDownloadWindow),
		transfers: make(map[uintptr]*gtkPopupTransfer),
	}
	purego.RegisterLibFunc(&f.context, webkit, "webkit_web_view_get_context")
	purego.RegisterLibFunc(&f.view, webkit, "webkit_download_get_web_view")
	purego.RegisterLibFunc(&f.setDestination, webkit, "webkit_download_set_destination")
	purego.RegisterLibFunc(&f.cancel, webkit, "webkit_download_cancel")
	purego.RegisterLibFunc(&f.disconnect, libGTK, "g_signal_handler_disconnect")
	purego.RegisterLibFunc(&f.visible, libGTK, "gtk_widget_get_visible")
	purego.RegisterLibFunc(&f.newMessage, libGTK, "gtk_message_dialog_new")
	purego.RegisterLibFunc(&f.secondary, libGTK, "gtk_message_dialog_format_secondary_text")
	purego.RegisterLibFunc(&f.addButton, libGTK, "gtk_dialog_add_button")
	purego.RegisterLibFunc(&f.defaultButton, libGTK, "gtk_dialog_set_default_response")
	purego.RegisterLibFunc(&f.runDialog, libGTK, "gtk_dialog_run")
	purego.RegisterLibFunc(&f.ref, libGTK, "g_object_ref")
	purego.RegisterLibFunc(&f.unref, libGTK, "g_object_unref")
	f.started = purego.NewCallback(f.downloadStarted)
	f.destination = purego.NewCallback(f.chooseDestination)
	f.failed = purego.NewCallback(func(download uintptr, err *gtkDownloadError, _ uintptr) {
		if transfer := f.transfers[download]; transfer != nil {
			transfer.err = errors.New(nativeDownloadString(err.message))
		}
	})
	f.finished = purego.NewCallback(func(download, _ uintptr) {
		if transfer := f.transfers[download]; transfer != nil {
			delete(f.transfers, download)
			transfer.window.downloads.complete(transfer.path, transfer.err, transfer.window.show)
		}
	})
	f.destroyed = purego.NewCallback(func(view, _ uintptr) {
		window := f.windows[view]
		delete(f.windows, view)
		window.window = 0
		if window.handler != 0 {
			f.disconnect(window.context, window.handler)
		}
	})
	return f
}

func (f *gtkPopupDownloads) bindMain(view, window uintptr, owner *mygo.Window, downloads *Downloads) {
	if f.windows[view] != nil {
		return
	}
	source := &gtkDownloadWindow{window: window, downloads: downloads, context: f.context(view)}
	source.show = func(detail string) { showWindowDownloadWarning(owner, detail) }
	source.handler = f.gtk.connect(source.context, "download-started", f.started, view, 0, 0)
	f.bind(view, source)
}

func (f *gtkPopupDownloads) bindPopup(view, window, opener uintptr) {
	parent := f.windows[opener]
	if parent == nil {
		return
	}
	root := parent
	if parent.root != nil {
		root = parent.root
	}
	source := &gtkDownloadWindow{window: window, root: root, downloads: root.downloads}
	source.show = func(detail string) {
		if source.window != 0 && f.visible(source.window) {
			f.showWarning(source.window, detail)
		} else {
			parent.show(detail)
		}
	}
	f.bind(view, source)
}

func (f *gtkPopupDownloads) bind(view uintptr, source *gtkDownloadWindow) {
	f.windows[view] = source
	f.gtk.connect(view, "destroy", f.destroyed, 0, 0, 0)
}

func (f *gtkPopupDownloads) downloadStarted(_, download, mainView uintptr) {
	root, source := f.windows[mainView], f.windows[f.view(download)]
	if root == nil || source == nil || source.root != root {
		return // MyGo handles the opener's own downloads.
	}
	f.transfers[download] = &gtkPopupTransfer{window: source}
	f.gtk.connect(download, "decide-destination", f.destination, 0, 0, 0)
	f.gtk.connect(download, "failed", f.failed, 0, 0, 0)
	f.gtk.connect(download, "finished", f.finished, 0, 0, 0)
}

func (f *gtkPopupDownloads) chooseDestination(download uintptr, filename *byte, _ uintptr) bool {
	transfer := f.transfers[download]
	name := nativeDownloadString(filename)
	directory, err := mygo.App.Path(mygo.PathDownloads)
	if err == nil {
		err = os.MkdirAll(directory, 0o755)
	}
	owner := transfer.window
	if err == nil {
		transfer.path = owner.downloads.reserve(directory, name, owner.show)
	} else {
		owner.downloads.warn(owner.show, name, "无法写入下载目录。", err)
	}
	if transfer.path == "" {
		delete(f.transfers, download)
		f.cancel(download)
		return true
	}
	f.setDestination(download, (&url.URL{Scheme: "file", Path: transfer.path}).String())
	return true
}

func (f *gtkPopupDownloads) showWarning(parent uintptr, detail string) {
	const (
		modalWithParent = 1 | 2 // GTK_DIALOG_MODAL | GTK_DIALOG_DESTROY_WITH_PARENT
		warning         = 1     // GTK_MESSAGE_WARNING
		noButtons       = 0     // GTK_BUTTONS_NONE
		ok              = -5    // GTK_RESPONSE_OK
	)
	dialog := f.newMessage(parent, modalWithParent, warning, noButtons, "%s", downloadWarningTitle)
	// Keep the object alive if the parent destroys its dialog during gtk_dialog_run.
	f.ref(dialog)
	defer f.unref(dialog)
	f.gtk.windowSetTitle(dialog, downloadWarningTitle)
	f.secondary(dialog, "%s", detail)
	f.addButton(dialog, "确定", ok)
	f.defaultButton(dialog, ok)
	f.runDialog(dialog)
	f.gtk.destroy(dialog)
}

func nativeDownloadString(text *byte) string {
	if text == nil {
		return ""
	}
	length := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(text), length)) != 0 {
		length++
	}
	return string(unsafe.Slice(text, length))
}
