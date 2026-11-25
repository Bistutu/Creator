package icons

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/net/html"
)

const (
	maxHead               = 384 << 10
	maxIcon               = 1 << 20
	maxIconCandidates     = 4
	preferredIconLead     = 160 * time.Millisecond
	artworkRequestTimeout = 1150 * time.Millisecond
)

type Resolver struct {
	cacheDir string
	svg      func([]byte) (image.Image, error)
	public   *http.Transport
	private  *http.Transport
}

func NewResolver(cacheDir string, svg func([]byte) (image.Image, error)) *Resolver {
	public := &net.Dialer{Timeout: artworkRequestTimeout, Control: checkPublicSocket}
	private := &net.Dialer{Timeout: 900 * time.Millisecond}
	transport := func(dialer *net.Dialer) *http.Transport {
		return &http.Transport{
			DialContext: dialer.DialContext, TLSHandshakeTimeout: time.Second, ForceAttemptHTTP2: true,
			MaxIdleConns: 8, IdleConnTimeout: 25 * time.Second,
		}
	}
	return &Resolver{
		cacheDir: cacheDir, svg: svg, public: transport(public), private: transport(private),
	}
}

func (r *Resolver) Cached(raw string) image.Image {
	if r.cacheDir == "" {
		return nil
	}
	data, err := fileutil.ReadRegular(r.cachePath(raw, ".png"), maxIcon)
	if err != nil {
		return nil
	}
	img, _ := decodeRaster(data, 2048)
	return img
}

func (r *Resolver) cachePath(raw, ext string) string {
	return filepath.Join(r.cacheDir, fmt.Sprintf("%x%s", sha256.Sum256([]byte(profile.ArtworkAddress(raw))), ext))
}

func (r *Resolver) store(raw string, icon image.Image) {
	if r.cacheDir == "" {
		return
	}
	data, err := PNG(icon)
	if err != nil {
		return
	}
	_ = writeImageFile(r.cachePath(raw, ".png"), data)
}

// Resolve probes the chosen website only when asked. The caller can keep
// creating an app with its fallback icon while this request is in flight.
func (r *Resolver) Resolve(ctx context.Context, raw string) (image.Image, error) {
	return r.Inspect(ctx, raw, nil)
}

// Inspect reports a declared name as soon as it is available, including when
// the site's artwork takes longer or is unavailable.
func (r *Resolver) Inspect(ctx context.Context, raw string, onName func(string)) (image.Image, error) {
	start, err := profile.ParseAddress(raw)
	if err != nil {
		return nil, err
	}
	key := start.String()
	var reported string
	reportName := func(discovered string) {
		if discovered == "" || discovered == reported || ctx.Err() != nil {
			return
		}
		reported = discovered
		if onName != nil {
			onName(discovered)
		}
	}
	cachedName := r.CachedName(key)
	reportName(cachedName)
	if icon := r.Cached(key); icon != nil {
		return icon, nil
	}
	lookup, cancel := context.WithTimeout(ctx, 3200*time.Millisecond)
	defer cancel()
	client, allow := r.client(start)
	head, base := discoverHead(lookup, client, start, allow, reportName)
	rememberName := func(name string) {
		reportName(name)
		if name != "" && name != cachedName && r.cacheDir != "" && ctx.Err() == nil {
			_ = writeImageFile(r.cachePath(key, ".name"), []byte(name))
		}
	}
	headName := head.name()
	rememberName(headName)
	candidates, manifest := head.icons, head.manifest
	seen := make(map[string]bool)
	probe := func(c candidate) image.Image {
		if lookup.Err() != nil || (c.target != nil && !allow(c.target)) {
			return nil
		}
		key := c.key()
		if seen[key] {
			return nil
		}
		seen[key] = true
		return r.decodeArtwork(artworkBytes(lookup, client, c))
	}
	finish := func(img image.Image) (image.Image, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cancel()
		icon := SiteTile(img)
		r.store(key, icon)
		return icon, nil
	}
	probes := artworkProbes{ctx: lookup, client: client, resolver: r, seen: seen}
	rankCandidates(candidates)
	preferred := preferredArtworkCandidates(candidates, seen)
	if len(preferred) == 2 || (manifest != nil && len(preferred) == 1) {
		if img := probes.startPreferred(preferred); img != nil {
			return finish(img)
		}
	}
	if manifest != nil {
		if img := probes.waitLead(); img != nil {
			return finish(img)
		}
		site, img := probes.readManifest(manifest, allow)
		if img != nil {
			return finish(img)
		}
		if headName == "" {
			rememberName(site.name)
		}
		candidates = append(candidates, site.icons...)
		rankCandidates(candidates)
		preferred := preferredArtworkCandidates(candidates, seen)
		if len(preferred) == 2 || (probes.pending != 0 && len(preferred) == 1) {
			if img := probes.startPreferred(preferred); img != nil {
				return finish(img)
			}
		}
	}
	if img := probes.wait(nil); img != nil {
		return finish(img)
	}
	small := probes.small
	for _, c := range candidates {
		if len(seen) >= maxIconCandidates || lookup.Err() != nil {
			break
		}
		if img := probe(c); img != nil && artworkSide(img) >= artworkSide(small) {
			return finish(img)
		}
	}
	if small != nil {
		return finish(small)
	}
	if img := probe(candidate{target: base.ResolveReference(&url.URL{Path: "/favicon.ico"})}); img != nil {
		return finish(img)
	}
	if err := lookup.Err(); err != nil {
		return nil, err
	}
	return nil, errImage
}

func rankCandidates(candidates []candidate) {
	if len(candidates) > 1 {
		sort.SliceStable(candidates, func(i, j int) bool { return max(candidates[i].size, 32) > max(candidates[j].size, 32) })
	}
}

func preferredArtworkCandidates(candidates []candidate, seen map[string]bool) []candidate {
	var preferred []candidate
	var firstKey string
	for _, c := range candidates {
		if c.size > 0 && c.size < Size && c.inline == "" {
			continue
		}
		key := c.key()
		if seen[key] || key == firstKey {
			continue
		}
		preferred = append(preferred, c)
		if len(preferred) == 2 {
			break
		}
		firstKey = key
	}
	return preferred
}

func artworkSide(img image.Image) int {
	if img == nil {
		return 0
	}
	return min(img.Bounds().Dx(), img.Bounds().Dy())
}

func artworkBytes(ctx context.Context, client *http.Client, c candidate) []byte {
	if c.target == nil {
		return inlineArtwork(c.inline)
	}
	data, _, _ := fetch(ctx, client, c.target, maxIcon)
	return data
}

func (r *Resolver) decodeArtwork(data []byte) image.Image {
	if len(data) == 0 {
		return nil
	}
	img, err := decodeIcon(data, r.svg, 2048)
	if err != nil || img == nil || img.Bounds().Empty() {
		return nil
	}
	return img
}

func (r *Resolver) client(start *url.URL) (*http.Client, func(*url.URL) bool) {
	transport := r.public
	local := localName(start.Hostname())
	if local {
		transport = r.private
	}
	allow := func(u *url.URL) bool {
		if u.User != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return false
		}
		if start.Scheme == "https" && u.Scheme != "https" {
			return false
		}
		if local {
			return strings.EqualFold(start.Host, u.Host)
		}
		return !localName(u.Hostname())
	}
	return &http.Client{
		Transport: transport,
		Timeout:   artworkRequestTimeout,
		CheckRedirect: func(req *http.Request, previous []*http.Request) error {
			if len(previous) >= 5 || !allow(req.URL) || (previous[len(previous)-1].URL.Scheme == "https" && req.URL.Scheme != "https") {
				return http.ErrUseLastResponse
			}
			// Don't disclose the original app URL to a redirected icon host.
			req.Header.Del("Referer")
			return nil
		},
	}, allow
}

func localName(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if addr, err := netip.ParseAddr(host); err == nil {
		return !publicIP(addr)
	}
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || !strings.Contains(host, ".")
}

var (
	carrierNAT       = netip.MustParsePrefix("100.64.0.0/10")
	errPrivateSocket = errors.New("站点图标不能转到本机或内网")
)

func publicIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsGlobalUnicast() && !addr.IsPrivate() && !carrierNAT.Contains(addr)
}

// The dialer calls this with the resolved endpoint before connecting. Keep
// address selection and IPv4/IPv6 fallback in net.Dialer.
func checkPublicSocket(_ string, address string, _ syscall.RawConn) error {
	endpoint, err := netip.ParseAddrPort(address)
	if err != nil || !publicIP(endpoint.Addr()) {
		return errPrivateSocket
	}
	return nil
}

func getResponse(ctx context.Context, client *http.Client, source *url.URL, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.String(), nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return res, nil
}

func discoverHead(ctx context.Context, client *http.Client, source *url.URL, allowed func(*url.URL) bool, reportName func(string)) (pageHead, *url.URL) {
	res, err := getResponse(ctx, client, source, "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")
	if err != nil {
		return pageHead{}, source
	}
	defer res.Body.Close()
	// The head can finish while the rest of the page is still streaming.
	at := res.Request.URL
	return parseHead(io.LimitReader(res.Body, maxHead), at, allowed, reportName), at
}

func fetch(ctx context.Context, client *http.Client, source *url.URL, limit int64) ([]byte, *url.URL, error) {
	res, err := getResponse(ctx, client, source, "")
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > limit {
		return nil, nil, errImage
	}
	return data, res.Request.URL, nil
}

type candidate struct {
	target *url.URL
	inline string
	size   int
}

func (c candidate) key() string {
	if c.target != nil {
		return c.target.String()
	}
	return c.inline
}

func iconCandidate(base *url.URL, href, sizes string, allowed func(*url.URL) bool) (candidate, bool) {
	c := candidate{size: iconSize(sizes)}
	if len(href) >= len("data:") && strings.EqualFold(href[:len("data:")], "data:") {
		if len(href) > maxInlineAddress {
			return candidate{}, false
		}
		c.inline, _, _ = strings.Cut(href, "#")
		return c, len(c.inline) > len("data:")
	}
	c.target = resolveURL(base, href, allowed)
	return c, c.target != nil
}

func resolveURL(base *url.URL, raw string, allowed func(*url.URL) bool) *url.URL {
	if raw == "" || len(raw) > 2048 {
		return nil
	}
	u, err := base.Parse(raw)
	if err != nil || !allowed(u) {
		return nil
	}
	u.Fragment, u.RawFragment = "", ""
	return u
}

type pageHead struct {
	icons             []candidate
	manifest          *url.URL
	application, site string
}

func (head pageHead) name() string {
	if head.application != "" {
		return head.application
	}
	return head.site
}

func parseHead(page io.Reader, at *url.URL, allowed func(*url.URL) bool, reportName func(string)) pageHead {
	type link struct {
		href, sizes    string
		icon, manifest bool
	}
	var base *url.URL
	var links []link
	var head pageHead
	var templateDepth int
	var inHeadText bool
	tokens := html.NewTokenizer(page)
scan:
	for first := true; len(links) < 32; first = false {
		kind := tokens.Next()
		switch kind {
		case html.ErrorToken:
			break scan
		case html.TextToken:
			if templateDepth == 0 && !inHeadText {
				value := string(tokens.Text())
				if first {
					value = strings.TrimPrefix(value, "\ufeff")
				}
				if strings.Trim(value, " \t\r\n\f") != "" {
					break scan
				}
			}
			continue
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
		default:
			continue
		}
		name, _ := tokens.TagName()
		tag := string(name)
		if kind == html.EndTagToken {
			inHeadText = false
			if templateDepth > 0 {
				if tag == "template" {
					templateDepth--
				}
				continue
			}
			switch tag {
			case "head", "body", "html", "br":
				break scan
			}
			continue
		}
		if tag == "template" {
			templateDepth++
		}
		if templateDepth > 0 {
			continue
		}
		switch tag {
		case "html", "head", "basefont", "bgsound":
			continue
		case "title", "noscript", "noframes", "style", "script":
			inHeadText = true
			continue
		case "meta":
			// A valid application-name will take precedence over later metadata.
			if name := head.readName(tokens); name != "" && reportName != nil {
				reportName(name)
			}
			continue
		case "base", "link":
		default:
			// HTML can close the head implicitly at the first body element.
			break scan
		}
		var l link
		for {
			key, value, more := tokens.TagAttr()
			switch string(key) {
			case "href":
				l.href = strings.TrimSpace(string(value))
			case "rel":
				for _, rel := range strings.Fields(strings.ToLower(string(value))) {
					switch rel {
					case "apple-touch-icon", "apple-touch-icon-precomposed", "icon":
						l.icon = true
					case "manifest":
						l.manifest = true
					}
				}
			case "sizes":
				l.sizes = string(value)
			}
			if !more {
				break
			}
		}
		if tag == "base" {
			if base == nil {
				base = resolveURL(at, l.href, allowed)
			}
		} else if l.href != "" && (l.icon || l.manifest) {
			links = append(links, l)
		}
	}
	if base == nil {
		base = at
	}
	for _, l := range links {
		if l.icon {
			if c, ok := iconCandidate(base, l.href, l.sizes, allowed); ok {
				head.icons = append(head.icons, c)
			}
		}
		if l.manifest && head.manifest == nil {
			head.manifest = resolveURL(base, l.href, allowed)
		}
	}
	return head
}

func iconSize(sizes string) int {
	pixels := 0
	for _, size := range strings.Fields(strings.ToLower(sizes)) {
		if size == "any" {
			return 512
		}
		w, h, ok := strings.Cut(size, "x")
		if !ok {
			continue
		}
		width, _ := strconv.Atoi(w)
		height, _ := strconv.Atoi(h)
		pixels = max(pixels, min(width, height, 512))
	}
	return pixels
}
