package jsserve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReservedPublicPath reports whether a public/ relative path collides with
// Mar's reserved namespace, returning a short human reason (or "" when the
// path is fine). Two kinds of collision:
//
//   - generated bundle files (index.html / runtime.js / program.json /
//     _headers): a public/ copy would silently overwrite the real one in a
//     built dist/ and break the app.
//   - server route prefixes (_mar / _auth / api / services): the runtime
//     owns these paths (mounted in ServeLive + mountAuthHandlers +
//     mountAdminHandlers), so an asset there is shadowed by the route and
//     never served.
//
// This lives next to the route handlers it mirrors. `mar build`
// (scaffold.copyPublicDir) and `mar dev` (ValidatePublicDir) both call it so
// the two agree on what's allowed in public/ — keep it in sync with the
// prefixes registered in this package when adding a top-level route.
func ReservedPublicPath(rel string) string {
	slash := filepath.ToSlash(rel)
	switch slash {
	case "index.html", "runtime.js", "program.json", "_headers":
		return "generated bundle file"
	}
	first := slash
	if i := strings.IndexByte(slash, '/'); i >= 0 {
		first = slash[:i]
	}
	switch first {
	case "_mar", "_auth", "api", "services":
		return "reserved route prefix /" + first + "/"
	}
	return ""
}

// ValidatePublicDir walks dir and returns an error for the first file whose
// path collides with Mar's reserved namespace (see ReservedPublicPath). A
// missing dir (or "") is fine — most projects have none. Dotfiles are skipped
// to match copyPublicDir / serveStaticOrShell, which never ship or serve them.
//
// Called at `mar dev` startup so a colliding asset fails fast with the same
// error `mar build` gives, instead of being silently shadowed in dev and only
// rejected at build time (a dev↔build parity gap).
func ValidatePublicDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil // no public/ folder — nothing to validate
	}
	return filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		if strings.HasPrefix(fi.Name(), ".") {
			return nil // dotfile: never served or shipped
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if reason := ReservedPublicPath(rel); reason != "" {
			return fmt.Errorf("public/%s conflicts with Mar's %s; rename it",
				filepath.ToSlash(rel), reason)
		}
		return nil
	})
}
