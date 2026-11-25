package icons

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
)

type siteManifest struct {
	name  string
	icons []candidate
}

func parseManifest(data []byte, at *url.URL, allowed func(*url.URL) bool) siteManifest {
	var document struct {
		Name      json.RawMessage `json:"name"`
		ShortName json.RawMessage `json:"short_name"`
		Icons     json.RawMessage `json:"icons"`
	}
	if json.Unmarshal(data, &document) != nil {
		return siteManifest{}
	}
	result := siteManifest{name: normalizeSiteName(manifestString(document.Name))}
	if result.name == "" {
		result.name = normalizeSiteName(manifestString(document.ShortName))
	}
	// An invalid member or icon entry must leave other entries usable.
	var entries []json.RawMessage
	if json.Unmarshal(document.Icons, &entries) != nil {
		return result
	}
	for _, entry := range entries[:min(len(entries), 24)] {
		var icon struct{ Src, Sizes, Purpose json.RawMessage }
		if json.Unmarshal(entry, &icon) != nil {
			continue
		}
		purpose := manifestString(icon.Purpose)
		purposes := strings.Fields(purpose)
		if purpose != "" && !slices.Contains(purposes, "any") && !slices.Contains(purposes, "maskable") {
			continue
		}
		src, sizes := strings.TrimSpace(manifestString(icon.Src)), manifestString(icon.Sizes)
		if c, ok := iconCandidate(at, src, sizes, allowed); ok {
			result.icons = append(result.icons, c)
		}
	}
	return result
}

func manifestString(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var value string
	_ = json.Unmarshal(data, &value)
	return value
}
