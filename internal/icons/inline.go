package icons

import (
	"encoding/base64"
	"mime"
	"net/url"
	"strings"
)

const maxInlineAddress = 64 << 10

// inlineArtwork decodes a data URL without involving the network client.
func inlineArtwork(address string) []byte {
	_, contents, _ := strings.Cut(address, ":")
	header, payload, ok := strings.Cut(contents, ",")
	if !ok || len(header) > 256 {
		return nil
	}
	encoded := strings.HasSuffix(strings.ToLower(header), ";base64")
	if encoded {
		header = header[:len(header)-len(";base64")]
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return nil
	}
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml", "image/vnd.microsoft.icon", "image/x-icon":
	default:
		return nil
	}
	payload, err = url.PathUnescape(payload)
	if err != nil {
		return nil
	}
	if !encoded {
		return []byte(payload)
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil
	}
	return data
}
