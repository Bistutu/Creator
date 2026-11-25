package creator

import (
	_ "embed"
	"encoding/json"
)

//go:embed mygo.json
var manifest []byte

//go:embed LICENSE
var license string

// Version returns the version declared in the MyGo project manifest.
func Version() string {
	var project struct{ Version string }
	_ = json.Unmarshal(manifest, &project)
	return project.Version
}

// License returns the project's bundled license.
func License() string { return license }
