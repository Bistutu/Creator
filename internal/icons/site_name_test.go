package icons

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeclaredWebsiteNameUsesValidHeadMetadata(t *testing.T) {
	for _, test := range []struct{ title, html, want string }{
		{"application wins", `<meta property="og:site_name" content="Broad site"><meta content="项目 &amp; 文档" name="APPLICATION-NAME">`, "项目 & 文档"},
		{"site name", "<meta property=\"OG:SITE_NAME\" content=\"  Project\n\tDesk \" />", "Project Desk"},
		{"first valid application", `<meta name="application-name" content=" "><meta name="application-name" content="Real app"><meta name="application-name" content="Other language">`, "Real app"},
		{"reject directional control", `<meta name="application-name" content="Test&#x202E;"><meta property="og:site_name" content="Site">`, "Site"},
		{"reject control", `<meta name="application-name" content="Bad&#x1B;name">`, ""},
		{"reject oversized name", `<meta name="application-name" content="` + strings.Repeat("站", 49) + `">`, ""},
		{"ignore invalid UTF-8", "<meta name=\"application-name\" content=\"\xff\">", ""},
		{"ignore page title and body", `<title>My article</title></head><body><meta name="application-name" content="Unsafe to use from body">`, ""},
		{"element starts implicit body", `<title>My article</title><main><meta name="application-name" content="From body">`, ""},
		{"text starts implicit body", `<title>My article</title>Article text<meta name="application-name" content="From body">`, ""},
		{"skip metadata in nested templates", `<template><div><template><meta name="application-name" content="Template app"></template></div></template><meta name="application-name" content="Actual app">`, "Actual app"},
		{"UTF-8 signature and metadata text", "\ufeff \n<title>My &amp; site</title><style>body { color: red; }</style><noscript><meta name=\"application-name\" content=\"Scripting disabled\"></noscript><meta name=\"application-name\" content=\"Actual app\">", "Actual app"},
	} {
		t.Run(test.title, func(t *testing.T) {
			address, _ := url.Parse("https://example.com/desk")
			head := parseHead(strings.NewReader(test.html), address, func(*url.URL) bool { return true }, nil)
			if got := head.name(); got != test.want {
				t.Fatalf("declared name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestInspectReportsWebsiteNameBeforeArtworkAndUsesItOffline(t *testing.T) {
	ink := color.NRGBA{R: 28, G: 112, B: 188, A: 255}
	art := artworkPNG(t, 192, ink)
	head := make(chan struct{})
	releaseHead := sync.OnceFunc(func() { close(head) })
	artwork := make(chan struct{})
	releaseArtwork := sync.OnceFunc(func() { close(artwork) })
	const title = "我的工作台"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, `<head><meta name="application-name" content="%s"><link rel="icon" href="/mark.png">`, title)
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		select {
		case <-head:
			fmt.Fprint(w, "</head>")
		case <-req.Context().Done():
		}
	})
	mux.HandleFunc("GET /mark.png", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-artwork:
			_, _ = w.Write(art)
		case <-r.Context().Done():
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	defer releaseHead()
	defer releaseArtwork()
	cache := t.TempDir()
	address := server.URL + "/app?view=board"
	type inspection struct {
		picture image.Image
		err     error
	}
	names := make(chan string, 2)
	done := make(chan inspection, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	go func() {
		picture, err := NewResolver(cache, nil).Inspect(ctx, address, func(name string) { names <- name })
		done <- inspection{picture, err}
	}()
	select {
	case name := <-names:
		if name != title {
			t.Fatalf("reported name = %q, want %q", name, title)
		}
	case result := <-done:
		t.Fatalf("artwork completed before the declared name: %v", result.err)
	case <-ctx.Done():
		t.Fatal("the declared name waited for the rest of the document head")
	}
	releaseHead()
	releaseArtwork()
	select {
	case result := <-done:
		if result.err != nil || result.picture == nil || color.NRGBAModel.Convert(result.picture.At(64, 64)) != ink {
			t.Fatalf("website artwork was unavailable after release: %v", result.err)
		}
	case <-ctx.Done():
		t.Fatal("the released icon did not arrive")
	}
	select {
	case duplicate := <-names:
		t.Fatalf("reported the same name again after detecting the icon: %q", duplicate)
	default:
	}
	server.Close()
	reader := NewResolver(cache, nil)
	var cached string
	if picture, err := reader.Inspect(t.Context(), address+"#review", func(name string) { cached = name }); err != nil || picture == nil || cached != title {
		t.Fatalf("offline result = %q, %v", cached, err)
	}
	if name := reader.CachedName(server.URL + "/app?view=other"); name != "" {
		t.Fatalf("a different page inherited %q", name)
	}
}

func TestInspectKeepsTheNameWhenArtworkIsMissing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<head><meta property="og:site_name" content="Reading Desk"></head>`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	address := server.URL + "/app"
	cache := t.TempDir()
	resolver := NewResolver(cache, nil)
	var name string
	if icon, err := resolver.Inspect(t.Context(), address, func(found string) { name = found }); icon != nil || !errors.Is(err, errImage) || name != "Reading Desk" {
		t.Fatalf("name without image = %q, %v, %v", name, icon, err)
	}
	if name := NewResolver(cache, nil).CachedName(address); name != "Reading Desk" {
		t.Fatalf("cached name = %q", name)
	}
	if err := os.WriteFile(resolver.cachePath(address, ".name"), []byte(strings.Repeat("x", 193)), 0o600); err != nil {
		t.Fatal(err)
	}
	if name := resolver.CachedName(address); name != "" {
		t.Fatalf("oversized cache entry = %q", name)
	}
}
