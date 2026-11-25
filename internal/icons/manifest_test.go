package icons

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestParseManifestKeepsNameWithInvalidIconList(t *testing.T) {
	at := &url.URL{Scheme: "https", Host: "example.com"}
	raw := []byte(`{"name":"项目工作台","short_name":"工作台","icons":false}`)
	manifest := parseManifest(raw, at, func(*url.URL) bool { return true })
	if manifest.name != "项目工作台" || len(manifest.icons) != 0 {
		t.Fatalf("manifest with a malformed icon list = %+v", manifest)
	}
}

func TestInspectUsesManifestNameAndValidIcons(t *testing.T) {
	const name = "项目 工作台"
	ink := color.NRGBA{R: 36, G: 80, B: 176, A: 255}
	data := artworkPNG(t, 192, ink)
	waitForArtwork := make(chan struct{})
	releaseArtwork := sync.OnceFunc(func() { close(waitForArtwork) })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<head><title>Current document</title><link rel="manifest" href="/manifest"></head>`)
	})
	mux.HandleFunc("GET /manifest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/assets/site.webmanifest", http.StatusFound)
	})
	mux.HandleFunc("GET /assets/site.webmanifest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"name":42,"short_name":"  项目\t工作台  ","icons":[true,null,
			{"src":124,"sizes":"512x512"},
			{"src":"/ignored.png","sizes":"512x512","purpose":"company"},
			{"src":"/ignored.png","sizes":"512x512","purpose":"monochrome"},
			{"src":"mark.png","sizes":192,"purpose":["any"]}]}`)
	})
	mux.HandleFunc("GET /ignored.png", func(w http.ResponseWriter, r *http.Request) {
		t.Error("requested an icon with an unsuitable purpose")
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /assets/mark.png", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-waitForArtwork:
			_, _ = w.Write(data)
		case <-r.Context().Done():
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	defer releaseArtwork()
	address := server.URL + "/app?mode=work"
	cache := t.TempDir()
	resolver := NewResolver(cache, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	type lookupResult struct {
		icon image.Image
		err  error
	}
	names, results := make(chan string, 2), make(chan lookupResult, 1)
	go func() {
		img, err := resolver.Inspect(ctx, address, func(value string) { names <- value })
		results <- lookupResult{img, err}
	}()
	select {
	case actual := <-names:
		if actual != name {
			t.Fatalf("manifest name = %q, want %q", actual, name)
		}
	case result := <-results:
		t.Fatalf("manifest lookup completed before reporting the name: %v", result.err)
	case <-ctx.Done():
		t.Fatal("the manifest name waited for the image")
	}
	releaseArtwork()
	select {
	case result := <-results:
		if result.err != nil || result.icon == nil || color.NRGBAModel.Convert(result.icon.At(64, 64)) != ink {
			t.Fatalf("manifest artwork was unavailable: %v", result.err)
		}
	case <-ctx.Done():
		t.Fatal("the released manifest artwork did not arrive")
	}
	server.Close()
	var cachedName string
	img, err := NewResolver(cache, nil).Inspect(t.Context(), address+"#notes", func(value string) { cachedName = value })
	if err != nil || img == nil || color.NRGBAModel.Convert(img.At(64, 64)) != ink || cachedName != name {
		t.Fatalf("offline manifest lookup = %q, %v", cachedName, err)
	}
}

func TestResolveSecondManifestIconWhilePreferredIsSlow(t *testing.T) {
	cancelled := make(chan struct{})
	address, ink := manifestArtworkSite(t, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(cancelled)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 850*time.Millisecond)
	defer cancel()
	img, err := NewResolver("", nil).Resolve(ctx, address)
	if err != nil || img == nil || color.NRGBAModel.Convert(img.At(64, 64)) != ink {
		t.Fatalf("the second manifest icon was unavailable: %v", err)
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("the first icon request continued after resolving the alternate")
	}
}

func TestResolveUsesReadyPreferredManifestIcon(t *testing.T) {
	ink := color.NRGBA{R: 86, G: 65, B: 174, A: 255}
	data := artworkPNG(t, 192, ink)
	address, _ := manifestArtworkSite(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	})
	resolver := NewResolver("", nil)
	img, err := resolver.Resolve(t.Context(), address)
	if err != nil || img == nil || color.NRGBAModel.Convert(img.At(64, 64)) != ink {
		t.Fatalf("the preferred manifest artwork was unavailable: %v", err)
	}
}

func TestInspectUsesManifestWhilePageArtworkWaits(t *testing.T) {
	for _, pageIcons := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d page icons", pageIcons), func(t *testing.T) {
			stopped := make(chan string, 8)
			var requests atomic.Int32
			address, ink := slowPageArtworkSite(t, pageIcons, func(_ http.ResponseWriter, req *http.Request) {
				if requests.Add(1) > int32(pageIcons) {
					t.Error("requested a page icon again through the manifest")
				}
				<-req.Context().Done()
				stopped <- req.URL.Path
			})
			ctx, cancel := context.WithTimeout(t.Context(), 850*time.Millisecond)
			defer cancel()
			var name string
			img, err := NewResolver("", nil).Inspect(ctx, address, func(value string) { name = value })
			if err != nil || img == nil || color.NRGBAModel.Convert(img.At(64, 64)) != ink || name != "Project Desk" {
				t.Fatalf("manifest preview while page icons were stalled = %q, %v", name, err)
			}
			seen := make(map[string]bool, pageIcons)
			for range pageIcons {
				select {
				case path := <-stopped:
					if seen[path] {
						t.Fatalf("the shared icon URL %s was downloaded twice", path)
					}
					seen[path] = true
				case <-ctx.Done():
					t.Fatal("the page icon request continued after the manifest artwork arrived")
				}
			}
		})
	}
}

func TestResolveKeepsAvailablePageArtworkWhileManifestWaits(t *testing.T) {
	ink := color.NRGBA{R: 21, G: 137, B: 94, A: 255}
	data := artworkPNG(t, 192, ink)
	releaseArtwork, manifestStopped := make(chan struct{}), make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<head><link rel="icon" href="/mark.png"><link rel="manifest" href="/site.webmanifest"></head>`)
	})
	mux.HandleFunc("GET /mark.png", func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-releaseArtwork:
			_, _ = w.Write(data)
		case <-req.Context().Done():
		}
	})
	mux.HandleFunc("GET /site.webmanifest", func(_ http.ResponseWriter, req *http.Request) {
		close(releaseArtwork)
		<-req.Context().Done()
		close(manifestStopped)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 850*time.Millisecond)
	defer cancel()
	img, err := NewResolver("", nil).Resolve(ctx, server.URL+"/app")
	if err != nil || img == nil || color.NRGBAModel.Convert(img.At(64, 64)) != ink {
		t.Fatalf("the page artwork that became ready was unavailable: %v", err)
	}
	select {
	case <-manifestStopped:
	case <-ctx.Done():
		t.Fatal("the pending manifest download continued after resolving the page artwork")
	}
}

func TestManifestLookupReturnsWhileCanceledRequestsUnwind(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		at := &url.URL{Scheme: "https", Host: "example.com", Path: "/site.webmanifest"}
		icon := &url.URL{Scheme: "https", Host: "example.com", Path: "/page.png"}
		done := make(chan string, 2)
		release := make(chan struct{})
		defer close(release)
		client := &http.Client{Transport: artworkRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == at.Path {
				cancel()
			}
			<-req.Context().Done()
			done <- req.URL.Path
			// A transport may still be cleaning up when the caller returns.
			<-release
			return nil, req.Context().Err()
		})}
		probes := artworkProbes{ctx: ctx, client: client, resolver: new(Resolver), seen: make(map[string]bool)}
		probes.start(candidate{target: icon})
		synctest.Wait()
		site, picture := probes.readManifest(at, func(*url.URL) bool { return true })
		if picture != nil || site.name != "" || len(site.icons) != 0 {
			t.Fatalf("canceled manifest lookup returned artwork or metadata: %+v", site)
		}
		stopped := make(map[string]bool, 2)
		for range 2 {
			stopped[<-done] = true
		}
		if !stopped[at.Path] || !stopped[icon.Path] {
			t.Fatalf("cancellation reached only %v", stopped)
		}
	})
}

func BenchmarkInspectManifestWithSlowPageArtwork(b *testing.B) {
	address, _ := slowPageArtworkSite(b, 1, func(w http.ResponseWriter, r *http.Request) {
		timer := time.NewTimer(800 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
		case <-timer.C:
			http.NotFound(w, r)
		}
	})
	resolver := NewResolver("", nil)
	for b.Loop() {
		if _, err := resolver.Inspect(b.Context(), address, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func slowPageArtworkSite(t testing.TB, pageIcons int, slow http.HandlerFunc) (string, color.NRGBA) {
	t.Helper()
	ink := color.NRGBA{R: 46, G: 123, B: 183, A: 255}
	data := artworkPNG(t, 192, ink)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<head>")
		for i := range pageIcons {
			fmt.Fprintf(w, `<link rel="icon" sizes="512x512" href="/slow/%d">`, i)
		}
		fmt.Fprint(w, `<link rel="manifest" href="/site.webmanifest"></head>`)
	})
	mux.HandleFunc("GET /site.webmanifest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"name":"Project Desk","icons":[{"src":"/slow/0","sizes":"512x512"},
			{"src":"/mark.png","sizes":"192x192"}]}`)
	})
	mux.HandleFunc("GET /slow/{icon}", slow)
	mux.HandleFunc("GET /mark.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL + "/app?mode=work#notes", ink
}

func TestResolveKeepsBestFallbackAcrossManifest(t *testing.T) {
	ink := color.NRGBA{R: 86, G: 65, B: 174, A: 255}
	best, tiny := artworkPNG(t, 72, ink), artworkPNG(t, 16, color.NRGBA{R: 220, A: 255})
	var fetched atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<head><link rel="icon" sizes="512x512" href="/best.png"><link rel="manifest" href="/manifest"></head>`)
	})
	mux.HandleFunc("GET /manifest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"icons":[{"src":"/best.png","sizes":"512x512"},
			{"src":"/tiny.png","sizes":"512x512"},{"src":"/tiny2.png","sizes":"256x256"},
			{"src":"/tiny3.png","sizes":"16x16"}]}`)
	})
	mux.HandleFunc("GET /best.png", func(w http.ResponseWriter, _ *http.Request) {
		fetched.Add(1)
		_, _ = w.Write(best)
	})
	for _, path := range []string{"/tiny.png", "/tiny2.png", "/tiny3.png"} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tiny) })
	}
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)
	img, err := NewResolver("", nil).Resolve(t.Context(), site.URL+"/app")
	if err != nil || img == nil || color.NRGBAModel.Convert(img.At(64, 64)) != ink {
		t.Fatalf("the largest decoded fallback was not preserved: %v", err)
	}
	if got := fetched.Load(); got != 1 {
		t.Fatalf("the duplicate manifest URL triggered %d downloads of the same page icon", got)
	}
}

func BenchmarkResolveSecondManifestIcon(b *testing.B) {
	address, _ := manifestArtworkSite(b, func(w http.ResponseWriter, r *http.Request) {
		timer := time.NewTimer(800 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
		case <-timer.C:
			http.NotFound(w, r)
		}
	})
	resolver := NewResolver("", nil)
	for b.Loop() {
		if _, err := resolver.Resolve(b.Context(), address); err != nil {
			b.Fatal(err)
		}
	}
}

func manifestArtworkSite(t testing.TB, preferred http.HandlerFunc) (string, color.NRGBA) {
	t.Helper()
	ink := color.NRGBA{R: 46, G: 123, B: 183, A: 255}
	data := artworkPNG(t, 192, ink)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<head><link rel="manifest" href="/site.webmanifest"></head>`)
	})
	mux.HandleFunc("GET /site.webmanifest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"name":"Project Desk","icons":[
			{"src":"/preferred.png","sizes":"512x512"},
			{"src":"/alternate.png","sizes":"192x192"}]}`)
	})
	mux.HandleFunc("GET /preferred.png", preferred)
	mux.HandleFunc("GET /alternate.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL + "/app", ink
}
