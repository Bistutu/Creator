package webwindow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestMain(m *testing.M) {
	if address := os.Getenv("WEBCRATE_MEDIA_FIXTURE"); address != "" {
		if err := runMediaFixture(address, os.Getenv("WEBCRATE_MEDIA_ACTION")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

func TestGtkMediaPermissions(t *testing.T) {
	if os.Getenv("WEBCRATE_LINUX_MEDIA_SMOKE") != "1" {
		t.Skip("set WEBCRATE_LINUX_MEDIA_SMOKE=1 in an isolated GTK session")
	}
	t.Setenv("no_proxy", "127.0.0.1,::1,localhost")
	t.Setenv("NO_PROXY", "127.0.0.1,::1,localhost")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/next" {
			fmt.Fprint(w, "<!doctype html><title>Next page</title><script>window.mediaResult = 'navigated';</script>")
			return
		}
		fmt.Fprint(w, `<!doctype html><title>Capture fixture</title><script>
			const video = new URL(location.href).searchParams.get('action') === 'allow';
			(async () => {
				if (!navigator.mediaDevices) {
					window.mediaResult = 'MediaDevices unavailable (secure=' + isSecureContext + ')';
					return;
				}
				const stream = await navigator.mediaDevices.getUserMedia({audio: true, video});
				const tracks = stream.getTracks();
				window.mediaResult = tracks.map(track => track.kind + ':' + track.readyState).sort().join(',');
				tracks.forEach(track => track.stop());
			})().catch(error => { window.mediaResult = error.name; });
		</script>`)
	}))
	t.Cleanup(server.Close)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ action, want string }{
		{"deny", "NotAllowedError"},
		{"allow", "audio:live,video:live"},
		{"navigate", "navigated"},
	} {
		t.Run(test.action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 14*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^$")
			command.WaitDelay = time.Second
			data := t.TempDir()
			command.Env = append(os.Environ(),
				"XDG_DATA_HOME="+filepath.Join(data, "data"),
				"XDG_CACHE_HOME="+filepath.Join(data, "cache"),
				"XDG_CONFIG_HOME="+filepath.Join(data, "config"),
				"WEBCRATE_MEDIA_FIXTURE="+server.URL, "WEBCRATE_MEDIA_ACTION="+test.action,
			)
			output, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "media result: "+test.want+"\n") {
				t.Fatalf("GTK permission %s = %v\n%s", test.action, err, output)
			}
			t.Log(strings.TrimSpace(string(output)))
		})
	}
}

func runMediaFixture(address, action string) error {
	outcome := make(chan error, 1)
	finish := func(err error) {
		outcome <- err
		mygo.App.Quit()
	}
	mygo.App.SetName("Creator media fixture")
	mygo.App.WhenReady(func() {
		var view *mygo.WebView
		window := mygo.NewWindow(mygo.WindowOptions{Title: "Creator media fixture", Width: 680, Height: 480, Hidden: true,
			Content: ui.View(func(c *ui.Context) {
				if view != nil {
					ui.WebView(c, view).Fill()
				}
			}),
		})
		var err error
		view, err = window.NewWebView(mygo.WebViewOptions{})
		if err != nil {
			finish(err)
			return
		}
		if !BindRelatedPopups(window, &Downloads{}) || linuxPopups().media == nil {
			finish(errors.New("GTK permission adapter was not bound"))
			return
		}
		media := linuxPopups().media
		var widget uintptr
		media.gtk.foreach(window.NativeHandle(), media.gtk.visit, &widget)
		const flags = purego.RTLD_LAZY | purego.RTLD_LOCAL
		webkit, err := purego.Dlopen("libwebkit2gtk-4.1.so.0", flags)
		if err != nil {
			finish(err)
			return
		}
		gtk, err := purego.Dlopen("libgtk-3.so.0", flags)
		if err != nil {
			finish(err)
			return
		}
		var mockDevices func(uintptr, bool)
		var dialogButton func(uintptr, int32) uintptr
		var click func(uintptr)
		purego.RegisterLibFunc(&mockDevices, webkit, "webkit_settings_set_enable_mock_capture_devices")
		purego.RegisterLibFunc(&dialogButton, gtk, "gtk_dialog_get_widget_for_response")
		purego.RegisterLibFunc(&click, gtk, "gtk_button_clicked")
		mockDevices(media.settings(widget), true)
		page := view.Page()
		destination := address + "/next"
		var destinationLoaded atomic.Bool
		if action == "navigate" {
			page.OnDidFinishLoad(func() { destinationLoaded.Store(page.URL() == destination) })
		}
		if err := page.LoadURL(address + "/?action=" + action); err != nil {
			finish(err)
			return
		}
		window.Invalidate()
		window.Show()
		view.Focus()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
			defer cancel()
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			resultScript := "window.mediaResult || ''"
			if action == "navigate" {
				// The old page can receive a denial while WebKit is still
				// committing the new document. Read the result from /next.
				resultScript = "location.pathname === '/next' ? (window.mediaResult || '') : ''"
			}
			responded := false
			var evalErr error
			for {
				select {
				case <-ctx.Done():
					finish(fmt.Errorf("timed out waiting for GTK media request or result (responded=%v, URL=%q, destination loaded=%v, eval=%v)",
						responded, page.URL(), destinationLoaded.Load(), evalErr))
					return
				case <-tick.C:
				}
				if !responded {
					var loadErr error
					mygo.RunOnMain(func() {
						dialog := media.pending.dialog
						if dialog == 0 {
							return
						}
						fmt.Println("native permission dialog:", media.gtk.windowTitle(dialog))
						if action == "navigate" {
							loadErr = page.LoadURL(destination)
						} else {
							response := int32(gtkResponseDeny)
							if action == "allow" {
								response = gtkResponseAllow
							}
							click(dialogButton(dialog, response))
						}
						responded = true
					})
					if loadErr != nil {
						finish(loadErr)
						return
					}
				}
				if action == "navigate" && !destinationLoaded.Load() {
					continue
				}
				evalContext, cancelEval := context.WithTimeout(ctx, time.Second)
				var result any
				result, evalErr = page.EvalContext(evalContext, resultScript)
				cancelEval()
				if result != nil && result != "" {
					if !responded {
						finish(fmt.Errorf("site returned %v without a native permission dialog", result))
						return
					}
					var pending bool
					mygo.RunOnMain(func() { pending = media.pending.dialog != 0 })
					if pending {
						finish(errors.New("permission dialog survived after the request ended"))
						return
					}
					fmt.Println("media result:", result)
					finish(nil)
					return
				}
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		return err
	}
	select {
	case err := <-outcome:
		return err
	default:
		return errors.New("permission fixture exited before reporting a result")
	}
}
