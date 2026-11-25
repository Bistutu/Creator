package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/bundle"
	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

var (
	orientedFixtureBlue = color.NRGBA{R: 38, G: 118, B: 174, A: 255}
	orientedFixtureRed  = color.NRGBA{R: 220, G: 59, B: 47, A: 255}
)

func projectPath(parts ...string) string {
	// Go runs package tests from cmd/creator. The compiled test binary also
	// supports running from the repository root.
	if _, err := os.Stat("mygo.json"); err == nil {
		return filepath.Join(parts...)
	}
	return filepath.Join("..", "..", filepath.Join(parts...))
}

func orientedIconFixture(t *testing.T, dir string) string {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, 128, 64))
	draw.Draw(picture, picture.Bounds(), image.NewUniform(orientedFixtureBlue), image.Point{}, draw.Src)
	draw.Draw(picture, image.Rect(0, 0, 64, 20), image.NewUniform(orientedFixtureRed), image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	// A little-endian IFD with one SHORT entry, Orientation (0x0112) = 6.
	const exif = "II\x2a\x00\x08\x00\x00\x00\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00\x06\x00\x00\x00\x00\x00\x00\x00"
	const endIHDR = 33
	raw := encoded.Bytes()
	icon := binary.BigEndian.AppendUint32(bytes.Clone(raw[:endIHDR]), uint32(len(exif)))
	chunk := []byte("eXIf" + exif)
	icon = append(icon, chunk...)
	icon = binary.BigEndian.AppendUint32(icon, crc32.ChecksumIEEE(chunk))
	icon = append(icon, raw[endIHDR:]...)
	path := filepath.Join(dir, "chosen-icon.png")
	if err := os.WriteFile(path, icon, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertOrientedIcon(t *testing.T, picture image.Image) {
	t.Helper()
	if picture == nil {
		t.Fatal("the oriented fixture icon is missing")
	}
	for _, sample := range []struct {
		x, y int
		want color.NRGBA
	}{
		{168, 48, orientedFixtureRed}, {96, 48, orientedFixtureBlue},
		{128, 128, orientedFixtureBlue}, {32, 128, color.NRGBA{}},
	} {
		bounds := picture.Bounds()
		x, y := bounds.Min.X+sample.x*bounds.Dx()/256, bounds.Min.Y+sample.y*bounds.Dy()/256
		if got := color.NRGBAModel.Convert(picture.At(x, y)); got != sample.want {
			t.Errorf("rotated icon pixel at (%d, %d) = %v, want %v", x, y, got, sample.want)
		}
	}
}

func assertPackagedHome(t *testing.T, configuration, want string) {
	t.Helper()
	data, err := os.ReadFile(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var config bundle.RunnerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.URL != want {
		t.Fatalf("packaged home address = %q, want %q", config.URL, want)
	}
}

func assertRemovedPath(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old application still present at %s: %v", path, err)
	}
}

func stageInterruptedPackages(t *testing.T, installs, keepID string) func() {
	t.Helper()
	active := filepath.Join(installs, keepID)
	before, err := os.Stat(active)
	if err != nil {
		t.Fatal(err)
	}
	rollback := filepath.Join(installs, ".old-"+keepID)
	if err := os.Rename(active, rollback); err != nil {
		t.Fatal(err)
	}
	finishedID := profile.NewID()
	finished := filepath.Join(installs, ".old-"+finishedID)
	if err := os.Mkdir(finished, 0o700); err != nil {
		t.Fatal(err)
	}
	stage, err := os.MkdirTemp(installs, ".new-"+keepID+"-")
	if err != nil {
		t.Fatal(err)
	}
	keepMark, finishedMark := filepath.Join(installs, ".remove-"+keepID), filepath.Join(installs, ".remove-"+finishedID)
	for _, mark := range []string{keepMark, finishedMark} {
		if err := os.WriteFile(mark, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{stage, finished} {
		if err := os.WriteFile(filepath.Join(dir, "website.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		t.Helper()
		paths := []string{rollback, keepMark, finished, finishedMark, stage}
		for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(25 * time.Millisecond) {
			left := 0
			for _, path := range paths {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					left++
				}
			}
			if left == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("startup left %d of %d interrupted-operation files", left, len(paths))
			}
		}
		if current, err := os.Stat(active); err != nil || !os.SameFile(before, current) {
			t.Fatalf("startup did not restore the original installation: %v", err)
		}
	}
}

func assertArtworkColor(t *testing.T, picture *artwork, want color.NRGBA) {
	t.Helper()
	if picture == nil || picture.img == nil {
		t.Fatal("the application has no displayed artwork")
	}
	center := picture.img.Bounds().Min.Add(picture.img.Bounds().Size().Div(2))
	if got := color.NRGBAModel.Convert(picture.img.At(center.X, center.Y)); got != want {
		t.Fatalf("displayed artwork = %v, installed artwork = %v", got, want)
	}
}

type installedWebsiteReport struct {
	State, URL    string
	Width, Height int
}

func installedWebsiteFixture(t *testing.T) (string, <-chan installedWebsiteReport) {
	t.Helper()
	seen := make(chan installedWebsiteReport, 1)
	mux := http.NewServeMux()
	page := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "from=creator" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><title>Creator acceptance</title><h1>Workbench</h1><script>
const report = (state) => fetch('/ready', {method: 'POST', body: JSON.stringify({
  state, url: location.href, width: innerWidth, height: innerHeight,
})});
const opening = indexedDB.open('creator-acceptance', 1);
opening.onupgradeneeded = () => opening.result.createObjectStore('settings');
opening.onerror = () => report(String(opening.error));
opening.onsuccess = () => {
  const db = opening.result;
  const transaction = db.transaction('settings', 'readwrite', {durability: 'strict'});
  const settings = transaction.objectStore('settings');
  const before = settings.get('status');
  before.onsuccess = () => settings.put('remembered', 'status');
  transaction.onabort = () => report(String(transaction.error));
  transaction.oncomplete = () => {
    db.close();
    report(before.result || 'new');
  };
};
</script>`)
	}
	mux.HandleFunc("GET /workbench", page)
	mux.HandleFunc("GET /review", page)
	mux.HandleFunc("POST /ready", func(w http.ResponseWriter, r *http.Request) {
		var report installedWebsiteReport
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&report); err != nil {
			http.Error(w, "invalid website report", http.StatusBadRequest)
			return
		}
		select {
		case seen <- report:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL + "/workbench?from=creator", seen
}

func paddedFixturePort(t *testing.T, address string) string {
	t.Helper()
	u, err := url.Parse(address)
	if err != nil || u.Port() == "" {
		t.Fatalf("expected a fixture address with a port, got %q (%v)", address, err)
	}
	u.Host = net.JoinHostPort(u.Hostname(), "0"+u.Port())
	return u.String()
}

const fixtureManifestName = "项目工作台"

func manifestWebsiteFixture(t *testing.T) (address string, ink color.NRGBA, rendered <-chan string, manifestWhileWaiting <-chan struct{}) {
	t.Helper()
	ink = color.NRGBA{R: 54, G: 101, B: 189, A: 255}
	mark := image.NewNRGBA(image.Rect(0, 0, 192, 192))
	draw.Draw(mark, mark.Bounds(), image.NewUniform(ink), image.Point{}, draw.Src)
	data, err := icons.PNG(mark)
	if err != nil {
		t.Fatal(err)
	}
	pages := make(chan string, 2)
	overlap := make(chan struct{}, 1)
	var pageIconPending atomic.Bool
	var pageIconRequests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /board", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><head><title>Current board</title>
<link rel="icon" href="/assets/slow.png" sizes="512x512">
<link rel="manifest" href="/assets/site.webmanifest"></head><body>
<script>fetch('/rendered?' + new URLSearchParams({address: location.href}))</script></body>`)
	})
	mux.HandleFunc("GET /assets/site.webmanifest", func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.UserAgent(), "Go-http-client/") && pageIconPending.Load() {
			select {
			case overlap <- struct{}{}:
			default:
			}
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		_, _ = io.WriteString(w, `{"name":"`+fixtureManifestName+`","short_name":"工作台","start_url":"/welcome",
			"icons":[{"src":"slow.png","sizes":"512x512"},123,{"src":"mark.png","sizes":"192x192","purpose":"maskable any"}]}`)
	})
	mux.HandleFunc("GET /assets/slow.png", func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasPrefix(req.UserAgent(), "Go-http-client/") {
			http.NotFound(w, req)
			return
		}
		if pageIconRequests.Add(1) != 1 {
			t.Error("Creator requested the same icon from the page and manifest")
		}
		pageIconPending.Store(true)
		defer pageIconPending.Store(false)
		<-req.Context().Done()
	})
	mux.HandleFunc("GET /assets/mark.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /rendered", func(w http.ResponseWriter, r *http.Request) {
		select {
		case pages <- r.URL.Query().Get("address"):
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)
	return site.URL + "/board?mode=work#notes", ink, pages, overlap
}

func assertManifestReadWhilePageIconWaited(t *testing.T, manifestWhileWaiting <-chan struct{}) {
	t.Helper()
	select {
	case <-manifestWhileWaiting:
	default:
		t.Fatal("Creator waited for the page icon request before reading the manifest")
	}
}

const fixtureWebsiteName = "Creator Workbench"

func pendingWebsiteArtwork(t *testing.T) (string, color.NRGBA, <-chan struct{}, func()) {
	t.Helper()
	ink := color.NRGBA{R: 45, G: 110, B: 198, A: 255}
	mark := image.NewNRGBA(image.Rect(0, 0, 192, 192))
	draw.Draw(mark, mark.Bounds(), image.NewUniform(ink), image.Point{}, draw.Src)
	data, err := icons.PNG(mark)
	if err != nil {
		t.Fatal(err)
	}
	requested, ready := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(ready) })
	var pageRequests, iconRequests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /workbench", func(w http.ResponseWriter, _ *http.Request) {
		if pageRequests.Add(1) > 1 {
			t.Error("the same website preview was requested more than once")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><head><meta name="application-name" content="`+fixtureWebsiteName+`">
<link rel="icon" href="/mark.png" sizes="192x192"></head><body>Workbench</body>`)
	})
	mux.HandleFunc("GET /mark.png", func(w http.ResponseWriter, req *http.Request) {
		if iconRequests.Add(1) != 1 {
			t.Error("the same website icon was requested more than once")
			return
		}
		close(requested)
		select {
		case <-ready:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(data)
		case <-req.Context().Done():
		}
	})
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)
	t.Cleanup(finish)
	return site.URL + "/workbench?view=board#today", ink, requested, finish
}

func streamingWebsiteIcon(t *testing.T, slowPreferred bool) (address string, ink color.NRGBA, release func(), rendered <-chan struct{}, stop func()) {
	t.Helper()
	ink = color.NRGBA{R: 25, G: 142, B: 95, A: 255}
	side := 1
	links := `<link rel="icon" href="/artwork.png">`
	if slowPreferred {
		side = 192
		links = `<link rel="icon" sizes="512x512" href="/slow.png"><link rel="icon" sizes="192x192" href="/artwork.png">` +
			`<link rel="manifest" href="/app.webmanifest">`
	}
	mark := image.NewNRGBA(image.Rect(0, 0, side, side))
	draw.Draw(mark, mark.Bounds(), image.NewUniform(ink), image.Point{}, draw.Src)
	data, err := icons.PNG(mark)
	if err != nil {
		t.Fatal(err)
	}
	body := make(chan struct{})
	showBody := sync.OnceFunc(func() { close(body) })
	seen := make(chan struct{}, 1)
	head := `<!doctype html><title>Creator streaming icon</title><meta name="application-name" content="` + fixtureWebsiteName + `">` +
		strings.Repeat(`<link rel="modulepreload" href="/module.js">`, 48) +
		links + `<main>`
	mux := http.NewServeMux()
	if slowPreferred {
		mux.HandleFunc("GET /slow.png", func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		mux.HandleFunc("GET /app.webmanifest", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.UserAgent(), "Go-http-client/") {
				t.Error("Creator loaded a manifest while the page offered a full-size icon")
			}
			http.NotFound(w, r)
		})
	}
	mux.HandleFunc("GET /workbench", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, head)
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		select {
		case <-body:
			_, _ = io.WriteString(w, `<h1>Workbench</h1><script>fetch('/rendered')</script></main>`)
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("GET /artwork.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /module.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = io.WriteString(w, "export {}")
	})
	mux.HandleFunc("GET /rendered", func(w http.ResponseWriter, _ *http.Request) {
		select {
		case seen <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	t.Cleanup(showBody)
	return server.URL + "/workbench", ink, showBody, seen, server.Close
}
