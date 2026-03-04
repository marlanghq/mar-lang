package runtime

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// SetPublicFiles attaches embedded public static files to the runtime.
func (r *Runtime) SetPublicFiles(publicFS fs.FS) {
	r.publicFS = publicFS
}

// SetAdminFiles attaches embedded admin static files to the runtime.
func (r *Runtime) SetAdminFiles(adminFS fs.FS) {
	r.adminFS = adminFS
}

func (r *Runtime) serveAdminAsset(w http.ResponseWriter, req *http.Request, requestPath string) (bool, error) {
	if r.adminFS == nil {
		return false, nil
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return false, nil
	}
	assetPath, ok := publicAssetPathForRequest(requestPath, "/_belm/admin")
	if !ok {
		return false, nil
	}
	return servePublicFile(w, req, r.adminFS, assetPath)
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
