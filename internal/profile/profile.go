package profile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

const (
	DefaultWidth  = 1200
	DefaultHeight = 820
	DefaultColor  = "#4d6cf0"
	MaxNameLength = 48
)

var (
	ErrAddress      = errors.New("请输入有效的网址")
	ErrName         = errors.New("应用名称须在 48 个字符以内，且不能包含控制字符")
	ErrWindowWidth  = errors.New("宽度须在 640–2400 像素之间")
	ErrWindowHeight = errors.New("高度须在 480–1800 像素之间")
)

// App retains the original on-disk profile format.
type App struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	URL            string    `json:"url"`
	Category       string    `json:"category"`
	Width          int       `json:"width"`
	Height         int       `json:"height"`
	WindowRevision uint64    `json:"windowRevision,omitempty"`
	Color          string    `json:"color"`
	CustomIcon     string    `json:"customIcon,omitempty"`
	Glyph          string    `json:"glyph,omitempty"`
	Light          bool      `json:"light,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// ArtworkAddress identifies the HTTP resource used to look up website artwork.
// The profile and the installed application retain the full home address.
func ArtworkAddress(address string) string {
	key, _, _ := strings.Cut(address, "#")
	return key
}

func (app App) UsesAutomaticIcon(address string) bool {
	return app.CustomIcon == "" && ArtworkAddress(app.URL) == ArtworkAddress(address)
}

func NewID() string {
	var data [8]byte
	rand.Read(data[:])
	return "app-" + hex.EncodeToString(data[:])
}

func ValidID(id string) bool {
	payload, ok := strings.CutPrefix(id, "app-")
	if !ok || len(payload) != 16 {
		return false
	}
	_, err := hex.DecodeString(payload)
	return err == nil && payload == strings.ToLower(payload)
}

// LinuxDesktopID matches the generated application entry and its window.
func LinuxDesktopID(id string) string { return "com.webcrate." + id }

// WindowsAppUserModelID matches a generated application's process and shortcut.
func WindowsAppUserModelID(id string) string {
	return "WebCrate.Creator.App" + strings.TrimPrefix(id, "app-")
}

// ParseAddress accepts an explicit HTTP(S) URL or a bare domain, IP or localhost.
func ParseAddress(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 || strings.ContainsAny(value, "\r\n\t\\") {
		return nil, ErrAddress
	}
	prefix, _, hasSchemeDelimiter := strings.Cut(value, "://")
	explicit := hasSchemeDelimiter && !strings.ContainsAny(prefix, "/?#")
	if !explicit {
		value = "https://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, ErrAddress
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, ErrAddress
	}
	if port := u.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return nil, ErrAddress
		}
	}
	host := strings.ToLower(u.Hostname())
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return nil, ErrAddress
		}
		return u, nil
	}
	if strings.HasPrefix(u.Host, "[") || (!explicit && host != "localhost" && !strings.Contains(host, ".")) {
		return nil, ErrAddress
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" || strings.Contains(ascii, ":") {
		return nil, ErrAddress
	}
	for _, label := range strings.Split(strings.TrimSuffix(ascii, "."), ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return nil, ErrAddress
		}
	}
	port := u.Port()
	u.Host = ascii
	if port != "" {
		u.Host += ":" + port
	}
	return u, nil
}

func Host(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		return value
	}
	return displayHost(u)
}

// AddressSummary returns the hostname and the address shown in the library.
// The summary includes the port and path. Query and fragment stay on the app.
func AddressSummary(value string) (host, summary string) {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		return value, value
	}
	host = displayHost(u)
	summary = host
	if port := u.Port(); port != "" {
		summary = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		summary = "[" + host + "]"
	}
	if path := u.EscapedPath(); path != "/" {
		summary += path
	}
	return host, summary
}

func displayHost(u *url.URL) string {
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if name, err := idna.Lookup.ToUnicode(host); err == nil {
		return name
	}
	return host
}

func Normalize(app App) (App, error) {
	if app.CustomIcon != "" && !ValidIconKey(app.CustomIcon) {
		return App{}, errors.New("请重新选择应用图标")
	}
	u, err := ParseAddress(app.URL)
	if err != nil {
		return App{}, err
	}
	app.URL = u.String()
	app.Name = strings.TrimSpace(app.Name)
	if app.Name == "" {
		app.Name = Host(app.URL)
		if utf8.RuneCountInString(app.Name) > MaxNameLength {
			app.Name = string([]rune(app.Name)[:MaxNameLength])
		}
	}
	if !ValidName(app.Name) {
		return App{}, ErrName
	}
	if app.Width == 0 {
		app.Width = DefaultWidth
	}
	if app.Height == 0 {
		app.Height = DefaultHeight
	}
	if err := ValidateWindowSize(app.Width, app.Height); err != nil {
		return App{}, err
	}
	switch app.Category {
	case "AI", "效率", "社交", "影音", "阅读", "开发", "其他":
	default:
		app.Category = "其他"
	}
	if !ValidColor(app.Color) {
		app.Color = DefaultColor
	}
	return app, nil
}

func ValidName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= MaxNameLength && !strings.ContainsFunc(name, unicode.IsControl)
}

func ValidateWindowSize(width, height int) error {
	if width < 640 || width > 2400 {
		return ErrWindowWidth
	}
	if height < 480 || height > 1800 {
		return ErrWindowHeight
	}
	return nil
}

func ValidColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	_, err := hex.DecodeString(value[1:])
	return err == nil
}

func ValidIconKey(key string) bool {
	if len(key) != 64 || key != strings.ToLower(key) {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}
