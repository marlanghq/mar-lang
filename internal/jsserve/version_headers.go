package jsserve

import "net/http"

// withVersionHeaders wraps a handler so every response carries two
// identity headers:
//
//	X-Mar-Runtime: <version>   - the mar version that built this server
//	X-Mar-Program: <hash>      - sha256 prefix of the current program.json
//
// Clients (web runtime + iOS template) read these on every response
// to detect when the deployed code has changed under them. The split
// is deliberate:
//
//   - Different X-Mar-Runtime  → wire format / builtin set may have
//     changed. Client warns the user;
//     native apps require a rebuild,
//     web users can refresh.
//   - Different X-Mar-Program  → same runtime, but the user's app
//     code (services, pages, schemas) was
//     redeployed. Client prompts user to
//     apply the new version.
//
// Both headers are stamped eagerly (before next.ServeHTTP) because
// the inner handler may call Write directly — once bytes go out, the
// header map is frozen.
//
// The values come from process-wide state: marVersion from
// SetAdminBuildInfo (set once at boot), programHash from LiveProgram
// (recomputed on each Update / live-reload swap). Empty values are
// omitted — a "dev" build without the version stamp shouldn't be
// advertising a misleading "1.0.0" or similar.
func withVersionHeaders(lp *LiveProgram, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := MarVersion(); v != "" {
			w.Header().Set("X-Mar-Runtime", v)
		}
		if h := lp.ProgramHash(); h != "" {
			w.Header().Set("X-Mar-Program", h)
		}
		next.ServeHTTP(w, r)
	})
}
