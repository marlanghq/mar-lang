// Embedded admin panel assets — see WebFS below.
//
// Pragmatic v1: the panel is hand-written vanilla HTML+JS rather
// than Mar source interpreted at framework boot. The user-visible
// effect is the same — invisible to user code, ships with `mar`,
// styled to echo the Mar UI vocabulary — but the implementation is
// faster to land. Once the runtime supports multi-source compilation
// (interpreting framework-supplied .mar alongside user code), this
// directory swaps over to embedded Mar sources without touching the
// user-facing surface.

package admin

import (
	"embed"
	"io/fs"
)

//go:embed web/index.html web/admin.css web/admin.js
var webFS embed.FS

// WebFS exposes the embedded admin panel assets as a sub-FS rooted
// at the `web/` subdirectory, so callers can serve files by their
// short paths (`index.html`, `admin.js`, etc).
func WebFS() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		// embed.FS is statically known to contain `web/` — only a
		// build-time refactor could break this. Panic surfaces the
		// regression at boot rather than silently 500'ing on the
		// admin route.
		panic("admin: embed missing web/: " + err.Error())
	}
	return sub
}
