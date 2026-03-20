package marversion

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var embeddedVersion string

func Version() string {
	version := strings.TrimSpace(embeddedVersion)
	if version == "" {
		return "unknown"
	}
	return version
}
