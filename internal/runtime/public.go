package runtime

import (
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

const appUIBootstrapPlaceholder = "__MAR_BOOTSTRAP_JSON__"

// SetPublicFiles attaches embedded public static files to the runtime.
func (r *Runtime) SetPublicFiles(publicFS fs.FS) {
	r.publicFS = publicFS
}

// SetAppUIFiles attaches embedded App UI static files to the runtime.
func (r *Runtime) SetAppUIFiles(appUIFS fs.FS) {
	r.appUIFS = appUIFS
}

func (r *Runtime) serveAppUIAsset(w http.ResponseWriter, req *http.Request, requestPath string) (bool, error) {
	if r.appUIFS == nil {
		return false, nil
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return false, nil
	}
	assetPath, ok := publicAssetPathForRequest(requestPath, "/_mar")
	if !ok {
		return false, nil
	}
	if assetPath == "" || assetPath == "index.html" {
		return r.serveAppUIBootstrapHTML(w, req)
	}
	return servePublicFile(w, req, r.appUIFS, assetPath)
}

func (r *Runtime) serveAppUIBootstrapHTML(w http.ResponseWriter, req *http.Request) (bool, error) {
	data, err := fs.ReadFile(r.appUIFS, "index.html")
	if err != nil {
		return false, nil
	}

	bootstrapPayload := map[string]any{
		"schema":  r.schemaPayload("app_ui_bootstrap"),
		"version": r.publicVersionPayload(),
	}
	bootstrapJSON, err := json.Marshal(bootstrapPayload)
	if err != nil {
		return false, err
	}

	html := strings.ReplaceAll(string(data), appUIBootstrapPlaceholder, escapeBootstrapJSONForHTML(string(bootstrapJSON)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if req.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return true, nil
	}
	_, err = w.Write([]byte(html))
	return true, err
}

func escapeBootstrapJSONForHTML(value string) string {
	replacer := strings.NewReplacer(
		"<", "\\u003c",
		">", "\\u003e",
		"&", "\\u0026",
	)
	return replacer.Replace(value)
}

func (r *Runtime) servePublicAsset(w http.ResponseWriter, req *http.Request, requestPath string) (bool, error) {
	if r.App.Public == nil || r.publicFS == nil {
		return false, nil
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return false, nil
	}

	assetPath, ok := publicAssetPathForRequest(requestPath, normalizeMount(r.App.Public.Mount))
	if !ok {
		return false, nil
	}

	if served, err := servePublicFile(w, req, r.publicFS, assetPath); served {
		return true, err
	}

	// SPA fallback only applies to route-like paths (no file extension).
	if r.App.Public.SPAFallback != "" && path.Ext(assetPath) == "" {
		if served, err := servePublicFile(w, req, r.publicFS, r.App.Public.SPAFallback); served {
			return true, err
		}
	}

	return false, nil
}

func publicAssetPathForRequest(requestPath, mount string) (string, bool) {
	if mount == "/" {
		return strings.TrimPrefix(requestPath, "/"), true
	}
	if requestPath == mount {
		return "", true
	}
	prefix := mount + "/"
	if !strings.HasPrefix(requestPath, prefix) {
		return "", false
	}
	return strings.TrimPrefix(requestPath, prefix), true
}

func normalizeMount(mount string) string {
	value := strings.TrimSpace(mount)
	if value == "" || value == "/" {
		return "/"
	}
	value = strings.TrimSuffix(value, "/")
	if value == "" {
		return "/"
	}
	return value
}

func servePublicFile(w http.ResponseWriter, req *http.Request, publicFS fs.FS, requested string) (bool, error) {
	candidates := candidatePublicFiles(requested)
	for _, candidate := range candidates {
		if !isSafePublicRelativePath(candidate) {
			continue
		}

		data, err := fs.ReadFile(publicFS, candidate)
		if err != nil {
			continue
		}
		contentType := mime.TypeByExtension(path.Ext(candidate))
		if contentType == "" {
			contentType = http.DetectContentType(data)
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if req.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return true, nil
		}
		_, err = w.Write(data)
		return true, err
	}
	return false, nil
}

func candidatePublicFiles(requested string) []string {
	cleaned := path.Clean("/" + strings.TrimSpace(requested))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		cleaned = ""
	}

	candidates := make([]string, 0, 3)
	if cleaned == "" {
		return []string{"index.html"}
	}
	candidates = append(candidates, cleaned)
	candidates = append(candidates, path.Join(cleaned, "index.html"))
	return candidates
}

func isSafePublicRelativePath(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	if strings.HasPrefix(value, "/") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
