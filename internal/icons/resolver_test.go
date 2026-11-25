package icons

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestResolveManifestAndReuseIconOffline(t *testing.T) {
	mark := image.NewNRGBA(image.Rect(0, 0, 14, 14))
	for y := range 14 {
		for x := range 14 {
			mark.SetNRGBA(x, y, color.NRGBA{R: 224, G: 15, A: 255})
		}
	}
	imageBytes, err := PNG(mark)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, req *http.Request) { http.Redirect(w, req, "/app/page", http.StatusFound) })
	mux.HandleFunc("/app/page", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Cookie") != "" {
			t.Errorf("forwarded a cookie: %q", req.Header.Get("Cookie"))
		}
		fmt.Fprint(w, `<html><head><base href="/assets/"><link rel="shortcut icon" sizes="16x16" href="small.ico"><link rel="manifest" href="app.webmanifest"></head></html>`)
	})
	mux.HandleFunc("/assets/app.webmanifest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"icons":[{"src":"large.png","sizes":"180x180","purpose":"any"}]}`)
	})
	mux.HandleFunc("/assets/small.ico", func(http.ResponseWriter, *http.Request) { t.Error("lower resolution icon fetched first") })
	mux.HandleFunc("/assets/large.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(imageBytes) })
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	cache := t.TempDir()
	address := s.URL + "/start"
	icon, err := NewResolver(cache, nil).Resolve(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	if pixel := color.NRGBAModel.Convert(icon.At(64, 64)).(color.NRGBA); pixel.R != 224 || pixel.G != 15 || pixel.A != 255 {
		t.Fatalf("unexpected artwork center: %+v", pixel)
	}
	s.Close()
	resolver := NewResolver(cache, nil)
	cached, err := resolver.Resolve(context.Background(), address+"#anchor")
	if err != nil || cached.At(64, 64) != icon.At(64, 64) {
		t.Fatalf("couldn't use the same icon offline: %v", err)
	}
	if err := os.WriteFile(resolver.cachePath(address, ".png"), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if resolver.Cached(address) != nil {
		t.Fatal("invalid cache entry was used")
	}
}

func TestResolveIconFromDocumentHead(t *testing.T) {
	ink := color.NRGBA{R: 21, G: 137, B: 94, A: 255}
	mark := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	mark.SetNRGBA(0, 0, ink)
	data, err := PNG(mark)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name   string
		head   string
		stream bool
	}{
		{name: "streaming body", stream: true},
		{name: "many preloads", head: strings.Repeat(`<link rel="modulepreload" href="/assets/chunk.js">`, 48)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /workbench", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprintf(w, `<!doctype html><head>%s<link rel="icon" href="/workbench.png"></head><body>`, fixture.head)
				if fixture.stream {
					if err := http.NewResponseController(w).Flush(); err != nil {
						return
					}
					// The server has delivered the icon link. Keep the body
					// open until the resolver is done with this request.
					select {
					case <-r.Context().Done():
					case <-t.Context().Done():
					}
				}
			})
			mux.HandleFunc("GET /workbench.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) })
			site := httptest.NewServer(mux)
			t.Cleanup(site.Close)
			icon, err := NewResolver("", nil).Resolve(t.Context(), site.URL+"/workbench")
			if err != nil || icon == nil {
				t.Fatalf("the resolver missed the declared icon: %v", err)
			}
			if got := color.NRGBAModel.Convert(icon.At(64, 64)); got != ink {
				t.Fatalf("the resolved icon center is %v, wanted %v", got, ink)
			}
		})
	}
}

func TestResolveIconWithImplicitBodyWhileResponseStreams(t *testing.T) {
	site, ink := implicitBodyArtwork(t)
	ctx, cancel := context.WithTimeout(t.Context(), 650*time.Millisecond)
	defer cancel()
	picture, err := NewResolver("", nil).Resolve(ctx, site.URL+"/workspace")
	if err != nil || picture == nil || color.NRGBAModel.Convert(picture.At(64, 64)) != ink {
		t.Fatalf("the icon should load while the implicit body is still streaming: %v", err)
	}
}

func BenchmarkResolveIconWithImplicitBody(b *testing.B) {
	site, ink := implicitBodyArtwork(b)
	resolver := NewResolver("", nil)
	b.ReportAllocs()
	for b.Loop() {
		picture, err := resolver.Resolve(b.Context(), site.URL+"/workspace")
		if err != nil || picture == nil || color.NRGBAModel.Convert(picture.At(64, 64)) != ink {
			b.Fatalf("could not read the declared artwork: %v", err)
		}
	}
}

func implicitBodyArtwork(t testing.TB) (*httptest.Server, color.NRGBA) {
	t.Helper()
	ink := color.NRGBA{R: 25, G: 142, B: 95, A: 255}
	picture := artworkPNG(t, 192, ink)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /workspace", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Workspace</title><link rel="icon" href="/mark.png"><main>`)
		if err := http.NewResponseController(w).Flush(); err == nil {
			<-r.Context().Done()
		}
	})
	mux.HandleFunc("GET /mark.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(picture) })
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)
	return site, ink
}

func TestResolveReusesHTTPSConnectionForStreamingHeadAndIcon(t *testing.T) {
	ink := color.NRGBA{R: 21, G: 137, B: 94, A: 255}
	data := artworkPNG(t, 192, ink)
	var connections atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, `<head><link rel="icon" href="/mark.png"></head><body>`)
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		<-req.Context().Done()
	})
	mux.HandleFunc("GET /mark.png", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	})
	site := httptest.NewUnstartedServer(mux)
	site.EnableHTTP2 = true
	site.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	site.StartTLS()
	t.Cleanup(site.Close)
	resolver := NewResolver("", nil)
	resolver.private.TLSClientConfig = site.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	icon, err := resolver.Resolve(t.Context(), site.URL+"/app")
	if err != nil || icon == nil || color.NRGBAModel.Convert(icon.At(64, 64)) != ink {
		t.Fatalf("website artwork = %v", err)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("the HTML head and icon used %d TLS connections, want 1", got)
	}
}

func TestResolveDeclaredArtworkBeforeSlowManifest(t *testing.T) {
	ink := color.NRGBA{R: 21, G: 137, B: 94, A: 255}
	data := artworkPNG(t, 192, ink)
	for _, fixture := range []struct {
		name, head string
	}{
		{"declared size", `<link rel="icon" sizes="192x192" href="/mark.png">`},
		{"no declared size", `<link rel="icon" href="/mark.png">`},
		{"small icon before the unlabelled icon", `<link rel="icon" sizes="16x16" href="/tiny.png"><link rel="icon" href="/mark.png">`},
		{"two full-size icons", `<link rel="icon" sizes="512x512" href="/mark.png"><link rel="icon" sizes="192x192" href="/alternate.png">`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `<head>%s<link rel="manifest" href="/slow.webmanifest"></head>`, fixture.head)
			})
			mux.HandleFunc("GET /mark.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) })
			mux.HandleFunc("GET /tiny.png", func(w http.ResponseWriter, r *http.Request) {
				t.Error("requested the small icon before trying the unlabelled one")
				http.NotFound(w, r)
			})
			mux.HandleFunc("GET /alternate.png", func(w http.ResponseWriter, r *http.Request) {
				t.Error("requested the alternate despite a usable preferred icon")
				http.NotFound(w, r)
			})
			mux.HandleFunc("GET /slow.webmanifest", func(_ http.ResponseWriter, r *http.Request) {
				t.Error("requested the manifest despite a usable icon in the page head")
				<-r.Context().Done()
			})
			site := httptest.NewServer(mux)
			t.Cleanup(site.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer cancel()
			icon, err := NewResolver("", nil).Resolve(ctx, site.URL+"/app")
			if err != nil {
				t.Fatal(err)
			}
			if got := color.NRGBAModel.Convert(icon.At(64, 64)); got != ink {
				t.Fatalf("icon center = %v, want %v", got, ink)
			}
		})
	}
}

func TestResolveAfterMisleadingDeclaredArtwork(t *testing.T) {
	smallInk := color.NRGBA{R: 224, G: 15, A: 255}
	largeInk := color.NRGBA{R: 21, G: 137, B: 94, A: 255}
	small := artworkPNG(t, 16, smallInk)
	large := artworkPNG(t, 192, largeInk)
	for _, fixture := range []struct {
		name           string
		direct, listed bool
		noDeclaredSize bool
		want           color.NRGBA
	}{
		{name: "missing page icon", listed: true, want: largeInk},
		{name: "undersized page icon", direct: true, listed: true, want: largeInk},
		{name: "unlabelled small icon", direct: true, listed: true, noDeclaredSize: true, want: largeInk},
		{name: "keep small icon as fallback", direct: true, want: smallInk},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
				size := ` sizes="512x512"`
				if fixture.noDeclaredSize {
					size = ""
				}
				fmt.Fprintf(w, `<head><link rel="icon"%s href="/direct.png"><link rel="manifest" href="/app.webmanifest"></head>`, size)
			})
			mux.HandleFunc("GET /direct.png", func(w http.ResponseWriter, r *http.Request) {
				if !fixture.direct {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write(small)
			})
			mux.HandleFunc("GET /app.webmanifest", func(w http.ResponseWriter, r *http.Request) {
				if !fixture.listed {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, `{"icons":[{"src":"/listed.png","sizes":"192x192"}]}`)
			})
			mux.HandleFunc("GET /listed.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(large) })
			site := httptest.NewServer(mux)
			t.Cleanup(site.Close)
			icon, err := NewResolver("", nil).Resolve(t.Context(), site.URL+"/app")
			if err != nil {
				t.Fatal(err)
			}
			if got := color.NRGBAModel.Convert(icon.At(64, 64)); got != fixture.want {
				t.Fatalf("icon center = %v, want %v", got, fixture.want)
			}
		})
	}
}

func TestResolveSecondPageIconWhilePreferredIsSlow(t *testing.T) {
	for _, manifest := range []bool{false, true} {
		t.Run(fmt.Sprintf("manifest=%t", manifest), func(t *testing.T) {
			stopped := make(chan struct{})
			address, ink := alternativeArtworkSite(t, manifest, func(_ http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
				close(stopped)
			})
			ctx, cancel := context.WithTimeout(t.Context(), 850*time.Millisecond)
			defer cancel()
			resolver := NewResolver(t.TempDir(), nil)
			icon, err := resolver.Resolve(ctx, address)
			if err != nil {
				t.Fatal(err)
			}
			if got := color.NRGBAModel.Convert(icon.At(64, 64)); got != ink {
				t.Fatalf("icon center = %v, want %v", got, ink)
			}
			select {
			case <-stopped:
			case <-ctx.Done():
				t.Fatal("the preferred icon request was left running")
			}
			if cached := resolver.Cached(address); cached == nil || color.NRGBAModel.Convert(cached.At(64, 64)) != ink {
				t.Fatal("the alternate artwork was not cached")
			}
		})
	}
}

func TestProbePageArtworkKeepsAnAvailableImage(t *testing.T) {
	for _, test := range []struct {
		name                            string
		preferred, alternate, full, low int
	}{
		{"ready preferred", 192, 0, 192, 0},
		{"alternate times out", 32, 0, 0, 32},
		{"alternate is a better fallback", 32, 64, 0, 64},
	} {
		t.Run(test.name, func(t *testing.T) {
			ink := color.NRGBA{R: 21, G: 137, B: 94, A: 255}
			data := artworkPNG(t, test.preferred, ink)
			var alternateData []byte
			if test.alternate > 0 {
				alternateData = artworkPNG(t, test.alternate, ink)
			}
			first := candidate{inline: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)}
			alternate := candidate{target: &url.URL{Scheme: "https", Host: "example.com", Path: "/alternate.png"}}
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				defer close(release)
				client := &http.Client{Transport: artworkRoundTripper(func(r *http.Request) (*http.Response, error) {
					if test.preferred >= Size {
						t.Error("requested an alternate after receiving a full-size image")
					}
					if alternateData != nil {
						return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(alternateData)), Request: r}, nil
					}
					<-r.Context().Done()
					<-release
					return nil, r.Context().Err()
				})}
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				probes := artworkProbes{ctx: ctx, client: client, resolver: new(Resolver), seen: make(map[string]bool)}
				large := probes.startPreferred([]candidate{first, alternate})
				if large == nil {
					large = probes.wait(nil)
				}
				if gotLarge, gotSmall := artworkSide(large), artworkSide(probes.small); gotLarge != test.full || gotSmall != test.low {
					t.Fatalf("preferred and fallback sizes = %d, %d; want %d, %d", gotLarge, gotSmall, test.full, test.low)
				}
			})
		})
	}
}

func TestProbePageArtworkDoesNotStartAlternateAfterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first := candidate{target: &url.URL{Scheme: "https", Host: "example.com", Path: "/preferred.png"}}
		alternate := candidate{target: &url.URL{Scheme: "https", Host: "example.com", Path: "/alternate.png"}}
		client := &http.Client{Transport: artworkRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == alternate.target.Path {
				t.Error("started a second request after the lookup was cancelled")
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		ctx, cancel := context.WithTimeout(t.Context(), preferredIconLead/2)
		defer cancel()
		probes := artworkProbes{ctx: ctx, client: client, resolver: new(Resolver), seen: make(map[string]bool)}
		preferred := probes.startPreferred([]candidate{first, alternate})
		if preferred != nil || probes.wait(nil) != nil || probes.small != nil || probes.seen[alternate.key()] {
			t.Fatal("a cancelled request returned artwork")
		}
	})
}

func TestResolveDoesNotCacheArtworkAfterCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<head><link rel="icon" href="data:image/svg+xml,%3Csvg%2F%3E"></head>`)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resolver := NewResolver(t.TempDir(), func([]byte) (image.Image, error) {
		cancel()
		return image.NewNRGBA(image.Rect(0, 0, Size, Size)), nil
	})
	if icon, err := resolver.Resolve(ctx, server.URL); icon != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("resolve after cancellation = %v, %v", icon, err)
	}
	if resolver.Cached(server.URL) != nil {
		t.Fatal("a cancelled lookup populated the cache")
	}
}

func TestResolveCapsDistinctCandidates(t *testing.T) {
	mux := http.NewServeMux()
	requests := make(chan string, maxIconCandidates+3)
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<head><link rel="icon" sizes="32x32" href="/icon0.png">`)
		for i := range maxIconCandidates + 1 {
			fmt.Fprintf(w, `<link rel="icon" sizes="32x32" href="/icon%d.png">`, i)
		}
		fmt.Fprint(w, `</head>`)
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		http.NotFound(w, r)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	if icon, err := NewResolver("", nil).Resolve(t.Context(), server.URL+"/app"); icon != nil || !errors.Is(err, errImage) {
		t.Fatalf("resolve missing icons = %v, %v", icon, err)
	}
	close(requests)
	var got, want []string
	for path := range requests {
		got = append(got, path)
	}
	for i := range maxIconCandidates {
		want = append(want, fmt.Sprintf("/icon%d.png", i))
	}
	want = append(want, "/favicon.ico")
	if !slices.Equal(got, want) {
		t.Fatalf("checked %v, want %v", got, want)
	}
}

type artworkRoundTripper func(*http.Request) (*http.Response, error)

func (f artworkRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func BenchmarkResolveSecondPageIcon(b *testing.B) {
	address, _ := alternativeArtworkSite(b, false, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(800 * time.Millisecond):
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

func alternativeArtworkSite(t testing.TB, manifest bool, preferred http.HandlerFunc) (string, color.NRGBA) {
	t.Helper()
	ink := color.NRGBA{R: 21, G: 137, B: 94, A: 255}
	icon := artworkPNG(t, 192, ink)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<head><link rel="icon" sizes="512x512" href="/preferred.png"><link rel="icon" sizes="192x192" href="/alternate.png">`)
		if manifest {
			fmt.Fprint(w, `<link rel="manifest" href="/app.webmanifest">`)
		}
		fmt.Fprint(w, `</head>`)
	})
	mux.HandleFunc("GET /preferred.png", preferred)
	mux.HandleFunc("GET /alternate.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(icon) })
	mux.HandleFunc("GET /app.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		t.Error("requested the manifest despite an available full-size icon in the page")
		http.NotFound(w, r)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL + "/app", ink
}

func TestResolveInlineWebsiteArtwork(t *testing.T) {
	ink := color.NRGBA{R: 21, G: 137, B: 94, A: 255}
	data := artworkPNG(t, 128, ink)
	pngURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	svg := `<svg viewBox="0 0 128 128"><rect width="128" height="128" fill="#15895e"/></svg>`
	for _, fixture := range []struct {
		name, address string
		manifest      bool
	}{
		{name: "base64 PNG", address: pngURL},
		{name: "percent-encoded SVG", address: "data:image/svg+xml;charset=utf-8," + url.PathEscape(svg)},
		{name: "manifest icon", address: pngURL, manifest: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
				if fixture.manifest {
					fmt.Fprint(w, `<head><link rel="manifest" href="/app.webmanifest"></head>`)
					return
				}
				fmt.Fprintf(w, `<head><link rel="icon" href="%s"></head>`, fixture.address)
			})
			mux.HandleFunc("GET /app.webmanifest", func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"icons":[{"sizes":"128x128","src":%q}]}`, fixture.address)
			})
			mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
				t.Error("requested a favicon despite an inline icon")
				http.NotFound(w, r)
			})
			site := httptest.NewServer(mux)
			t.Cleanup(site.Close)
			cache := t.TempDir()
			resolver := NewResolver(cache, func(artwork []byte) (image.Image, error) {
				if string(artwork) != svg {
					t.Fatalf("decoded SVG = %q, want %q", artwork, svg)
				}
				return decodeRaster(data, 2048)
			})
			address := site.URL + "/app"
			icon, err := resolver.Resolve(t.Context(), address)
			if err != nil {
				t.Fatal(err)
			}
			if got := color.NRGBAModel.Convert(icon.At(64, 64)); got != ink {
				t.Fatalf("icon center = %v, want %v", got, ink)
			}
			site.Close()
			cached, err := NewResolver(cache, nil).Resolve(t.Context(), address)
			if err != nil || cached.At(64, 64) != icon.At(64, 64) {
				t.Fatalf("inline artwork was not reused offline: %v", err)
			}
		})
	}
}

func TestResolveInvalidInlineArtwork(t *testing.T) {
	ink := color.NRGBA{R: 21, G: 137, B: 94, A: 255}
	data := artworkPNG(t, 128, ink)
	other := base64.StdEncoding.EncodeToString(artworkPNG(t, 128, color.NRGBA{R: 222, A: 255}))
	for _, fixture := range []struct{ name, address string }{
		{"oversized address", "data:image/png;base64," + strings.Repeat("\n", maxInlineAddress) + other},
		{"missing payload delimiter", "data:image/png"},
		{"invalid media parameter", "data:image/png;charset;base64," + other},
		{"malformed base64", "data:image/png;base64,broken!"},
		{"unaccepted media type", "data:text/plain;base64," + other},
		{"bad escape", "data:image/svg+xml,%zz"},
		{"invalid image", "data:image/png,not an image"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `<head><link rel="icon" href="%s"></head>`, fixture.address)
			})
			mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) })
			site := httptest.NewServer(mux)
			t.Cleanup(site.Close)
			icon, err := NewResolver("", nil).Resolve(t.Context(), site.URL+"/app")
			if err != nil {
				t.Fatal(err)
			}
			if got := color.NRGBAModel.Convert(icon.At(64, 64)); got != ink {
				t.Fatalf("fallback icon center = %v, want %v", got, ink)
			}
		})
	}
}

func artworkPNG(t testing.TB, side int, ink color.NRGBA) []byte {
	t.Helper()
	mark := image.NewNRGBA(image.Rect(0, 0, side, side))
	draw.Draw(mark, mark.Bounds(), image.NewUniform(ink), image.Point{}, draw.Src)
	data, err := PNG(mark)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestResolveFallbackICOAndCancel(t *testing.T) {
	data, err := PNG(Brand())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<head><link rel="icon" href="/too-big"></head>`)
	})
	mux.HandleFunc("/too-big", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, maxIcon+1)) })
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(icoFrame(128, 128, data)) })
	s := httptest.NewServer(mux)
	defer s.Close()
	r := NewResolver("", nil)
	if icon, err := r.Resolve(context.Background(), s.URL+"/app"); err != nil || icon == nil {
		t.Fatalf("ICO fallback = %v, %v", icon, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Resolve(ctx, s.URL+"/app"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel resolve = %v", err)
	}
	if _, err := r.Resolve(context.Background(), "file:///bad"); err == nil {
		t.Fatal("invalid URL accepted")
	}
}

func TestIconURLPolicyAndDiscovery(t *testing.T) {
	public, _ := url.Parse("https://example.com/app")
	private, _ := url.Parse("http://localhost:3000/app")
	r := NewResolver(t.TempDir(), nil)
	client, allow := r.client(public)
	_, allowLocal := r.client(private)
	entries := map[string]bool{
		"https://assets.example.com/a.png": true, "https://203.0.113.2/a.png": true,
		"http://assets.example.com/a.png": false, "https://user:pass@example.com/icon": false,
		"https://127.0.0.1/icon": false, "https://[::ffff:10.0.0.2]/icon": false,
		"https://foo.local/icon": false, "https://100.64.5.6/a.png": false, "data:image/png;base64,foo": false,
	}
	for raw, want := range entries {
		u, err := url.Parse(raw)
		if err != nil || allow(u) != want {
			t.Errorf("network rule for %q = %v, want %v", raw, allow(u), want)
		}
	}
	if !allowLocal(private) || allowLocal(public) {
		t.Fatal("local requests may only visit the selected local origin")
	}
	previous, _ := http.NewRequest(http.MethodGet, public.String(), nil)
	next, _ := http.NewRequest(http.MethodGet, "https://assets.example.com/icon", nil)
	next.Header.Set("Referer", previous.URL.String())
	if err := client.CheckRedirect(next, []*http.Request{previous}); err != nil || next.Header.Get("Referer") != "" {
		t.Fatalf("redirect leaked Referer: %v", err)
	}
	if err := client.CheckRedirect(next, []*http.Request{previous, previous, previous, previous, previous}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("unbounded redirect: %v", err)
	}
	for _, endpoint := range []string{"127.0.0.1:80", "localhost:80"} {
		if conn, err := r.public.DialContext(t.Context(), "tcp", endpoint); conn != nil || !errors.Is(err, errPrivateSocket) {
			t.Fatalf("public dial to %s: %v, %v", endpoint, conn, err)
		}
	}
	for _, endpoint := range []string{"[::ffff:10.0.0.2]:80", "example.org:443", "100.64.5.6:443"} {
		if err := checkPublicSocket("tcp", endpoint, nil); !errors.Is(err, errPrivateSocket) {
			t.Fatalf("the socket guard allowed %s: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"93.184.215.14:443", "[2001:db8::1]:443"} {
		if err := checkPublicSocket("tcp", endpoint, nil); err != nil {
			t.Fatalf("the socket guard rejected %s: %v", endpoint, err)
		}
	}
	if !publicIP(netip.MustParseAddr("2001:db8::1")) || publicIP(netip.MustParseAddr("fe80::1")) {
		t.Fatal("invalid IPv6 classification")
	}
	if head := parseHead(strings.NewReader(`<head><meta charset="utf-8"><base href="file:///tmp"><link rel="icon" href="/top" sizes="16x16 any"><link rel="shortcut icon" href="https://127.0.0.1/bad"></head><body><link rel="icon" href="/ignored"></body>`), public, allow, nil); len(head.icons) != 1 || head.icons[0].target.Path != "/top" || head.icons[0].size != 512 || head.manifest != nil {
		t.Fatalf("discovery links = %v, manifest = %v", head.icons, head.manifest)
	}
	if links := parseManifest([]byte(`{"icons":[{"src":"/bad","purpose":"monochrome"},{"src":"/retina","sizes":"broken 192X192"}]}`), public, allow).icons; len(links) != 1 || links[0].size != 192 {
		t.Fatalf("manifest links = %v", links)
	}
	if links := parseManifest([]byte(`invalid`), public, allow).icons; len(links) != 0 {
		t.Fatalf("malformed manifest = %v", links)
	}
}

func TestIconDecodersAndFallback(t *testing.T) {
	for _, value := range []string{" example", "中文", ""} {
		tile := Fallback(value, "invalid color")
		if tile.Bounds().Dx() != Size || tile.NRGBAAt(0, 0).A != 0 || tile.NRGBAAt(64, 64).A != 255 {
			t.Fatalf("invalid fallback for %q", value)
		}
	}
	dib := make([]byte, 48)
	le := binary.LittleEndian
	le.PutUint32(dib, 40)
	le.PutUint32(dib[4:], 1)
	le.PutUint32(dib[8:], 2)
	le.PutUint16(dib[12:], 1)
	le.PutUint16(dib[14:], 32)
	copy(dib[40:44], []byte{24, 53, 89, 80})
	img, err := decodeICO(icoFrame(1, 1, dib))
	if err != nil || img.At(0, 0) != (color.NRGBA{R: 89, G: 53, B: 24, A: 80}) {
		t.Fatalf("32 bit ICO = %v, %v", img, err)
	}
	dib[43], dib[44] = 0, 0x80
	if img := decodeDIB(dib, 1, 1); img.NRGBAAt(0, 0).A != 0 {
		t.Fatal("ICO mask was not applied")
	}
	if _, err := decodeICO(append([]byte{0, 0, 1, 0, 5, 0}, 0)); err == nil {
		t.Fatal("truncated icon accepted")
	}
	if _, err := decodeICO(icoFrame(16, 16, []byte("bad"))); err == nil {
		t.Fatal("invalid frame accepted")
	}
	if _, err := decodeRaster([]byte("bad"), 2048); err == nil {
		t.Fatal("invalid raster accepted")
	}
	r := NewResolver(filepath.Join(t.TempDir(), "cache"), func(svg []byte) (image.Image, error) {
		if !bytes.Contains(svg, []byte("viewBox")) {
			return nil, errImage
		}
		return Brand(), nil
	})
	if _, err := decodeIcon([]byte(`<svg viewBox="0 0 24 24"/>`), r.svg, 2048); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeIcon([]byte(`<?xml version="1.0"?><svg/>`), r.svg, 2048); !errors.Is(err, errImage) {
		t.Fatalf("SVG decoder error = %v", err)
	}
	if x, _, err := fetch(context.Background(), http.DefaultClient, &url.URL{Scheme: "nope", Host: "example.com"}, 1); x != nil || err == nil || !strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("transport failure = %v, %v", x, err)
	}
}

func icoFrame(width, height byte, payload []byte) []byte {
	le := binary.LittleEndian
	data := make([]byte, 22+len(payload))
	data[2], data[4], data[6], data[7] = 1, 1, width, height
	le.PutUint32(data[14:], uint32(len(payload)))
	le.PutUint32(data[18:], 22)
	copy(data[22:], payload)
	return data
}
