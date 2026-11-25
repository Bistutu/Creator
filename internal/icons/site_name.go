package icons

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/profile"
	"golang.org/x/net/html"
)

func (r *Resolver) CachedName(raw string) string {
	if r.cacheDir == "" {
		return ""
	}
	data, err := fileutil.ReadRegular(r.cachePath(raw, ".name"), profile.MaxNameLength*utf8.UTFMax)
	if err != nil {
		return ""
	}
	return normalizeSiteName(string(data))
}

func (head *pageHead) readName(tokens *html.Tokenizer) string {
	if head.application != "" {
		return ""
	}
	var application, site bool
	var content []byte
	for {
		key, value, more := tokens.TagAttr()
		switch string(key) {
		case "name":
			application = strings.EqualFold(string(value), "application-name")
		case "property":
			site = strings.EqualFold(string(value), "og:site_name")
		case "content":
			content = value
		}
		if !more {
			break
		}
	}
	if application {
		head.application = normalizeSiteName(string(content))
		return head.application
	} else if site && head.site == "" {
		head.site = normalizeSiteName(string(content))
	}
	return ""
}

func normalizeSiteName(raw string) string {
	if !utf8.ValidString(raw) || strings.ContainsFunc(raw, func(r rune) bool { return unicode.Is(unicode.Bidi_Control, r) }) {
		return ""
	}
	name := strings.Join(strings.Fields(raw), " ")
	if !profile.ValidName(name) {
		return ""
	}
	return name
}
