package runtime

import (
	goruntime "runtime"
	"strings"
)

// VersionInfo carries build metadata injected by the generated Belm app binary.
type VersionInfo struct {
	BelmVersion   string
	BelmCommit    string
	BelmBuildTime string
	AppBuildTime  string
	ManifestHash  string
}

// SetVersionInfo updates runtime metadata exposed by version endpoints.
func (r *Runtime) SetVersionInfo(info VersionInfo) {
	if r == nil {
		return
	}
	r.versionInfo = info
}

func (r *Runtime) publicVersionPayload() map[string]any {
	return map[string]any{
		"app": map[string]any{
			"name":         r.App.AppName,
			"buildTime":    strings.TrimSpace(r.versionInfo.AppBuildTime),
			"manifestHash": strings.TrimSpace(r.versionInfo.ManifestHash),
		},
	}
}

func (r *Runtime) adminVersionPayload() map[string]any {
	return map[string]any{
		"app": map[string]any{
			"name":         r.App.AppName,
			"buildTime":    strings.TrimSpace(r.versionInfo.AppBuildTime),
			"manifestHash": strings.TrimSpace(r.versionInfo.ManifestHash),
		},
		"belm": map[string]any{
			"version":   strings.TrimSpace(r.versionInfo.BelmVersion),
			"commit":    strings.TrimSpace(r.versionInfo.BelmCommit),
			"buildTime": strings.TrimSpace(r.versionInfo.BelmBuildTime),
		},
		"runtime": map[string]any{
			"goVersion": goruntime.Version(),
			"platform":  goruntime.GOOS + "/" + goruntime.GOARCH,
		},
	}
}
