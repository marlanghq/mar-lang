// Admin panel server-side infrastructure — request instrumentation
// (counters + the recent-requests ring buffer), boot metadata, and the
// SQLite introspection helpers (listTables, tableColumns, dbFileSizes,
// …). These feed the Mar panel's Mar.Admin.* bodies (admin_mar.go),
// which project the same data as Mar Values for the frontend.
//
// (The hand-written SPA that used to consume plain-JSON variants of
// these — /_mar/admin/api/{server-info,db-stats,…} — was retired; the
// Mar-native panel reads /_mar/admin/api/mar/* instead.)

package jsserve

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"time"

	"mar/internal/admin"
	"mar/internal/runtime"
)

// Boot metadata — set by the CLI at ServeLive call time.
var (
	bootStartedAtMs int64
	marVersion      string
)

// SetAdminBuildInfo plumbs the mar version stamp into the framework
// so the admin panel can show it. Called once from the CLI before
// ServeLive. Empty values are tolerated and rendered as "dev" by the
// introspection body.
//
// Build target is intentionally NOT taken as a parameter — it's
// always derived from runtime.GOOS/GOARCH at request time (see
// hostTarget). If we ever want to differentiate the target the
// binary was BUILT for vs the host it's running on (cross-compiled
// edge cases), add a parameter here.
func SetAdminBuildInfo(version string) {
	authMu.Lock()
	defer authMu.Unlock()
	marVersion = version
}

// MarVersion returns the build-time version string set by
// SetAdminBuildInfo. Empty when the CLI didn't pass one (test paths,
// rare). Used by the X-Mar-Runtime header middleware so each response
// advertises the runtime that's serving it — clients compare against
// their own embedded version to decide whether to apply OTA updates.
func MarVersion() string {
	authMu.Lock()
	defer authMu.Unlock()
	return marVersion
}

// noteServerBooted is called from ServeLive at the moment the
// listener is about to accept traffic. Idempotent if called
// multiple times (test hot-reload paths).
func noteServerBooted() {
	atomic.StoreInt64(&bootStartedAtMs, time.Now().UnixMilli())
	// Wire the Mar.Admin.* runtime bodies now that server state (DB, request
	// counters, log buffer) is live. Idempotent — just re-registers the same
	// closures on hot reload.
	registerAdminMarServices()
}

// Request counters — atomic so middleware can read without locking.
var (
	requestsTotal    int64
	requestsInFlight int64
)

// requestLogger holds the in-memory ring buffer powering the admin
// panel's "recent requests" view. Initialized at ServeLive boot
// using the cap from manifest.adminPanel.recentRequestsSize.
var requestLogger *admin.RequestLogger

// SetAdminRequestBufferSize installs (or replaces) the request log
// ring buffer at the configured size. Called from the CLI after
// reading manifest.adminPanel.recentRequestsSize. Cap=0 disables
// recording (for tests / specialized deployments).
func SetAdminRequestBufferSize(cap int) {
	if cap <= 0 {
		requestLogger = nil
		return
	}
	requestLogger = admin.NewRequestLogger(cap)
}

// adminRecord records a request log entry. Called by the
// instrumenting middleware. No-op when the logger isn't set.
func adminRecord(entry admin.RequestLog) {
	if requestLogger == nil {
		return
	}
	requestLogger.Record(entry)
}

// adminInstrument wraps a handler with the request log middleware.
// Captures method/path/status/duration on every request. The user
// email field is best-effort — we sniff the user-auth cookie's
// email when present, but skip the heavier admin-cookie lookup.
//
// Skipped paths (no recording, no counter bump):
//
//   - /_mar/reload    — SSE channel; long-lived connections would
//     dominate the buffer and noise out the panel.
//   - /_mar/admin/*   — the admin panel polling itself (the dashboard
//     fetches server-info / db-stats / recent-requests on each
//     refresh, plus the program/shell). Including these makes the
//     "recent requests" view show the panel watching itself,
//     drowning out the actual app traffic the operator wants to see.
//
// Skipping from BOTH the ring buffer AND the counters keeps "requests
// total" honest about real application traffic.
func adminInstrument(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isFrameworkInternalPath(r.URL.Path) {
			h.ServeHTTP(w, r)
			return
		}
		atomic.AddInt64(&requestsTotal, 1)
		atomic.AddInt64(&requestsInFlight, 1)
		defer atomic.AddInt64(&requestsInFlight, -1)

		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: 200}
		h.ServeHTTP(recorder, r)

		adminRecord(admin.RequestLog{
			AtMs:       started.UnixMilli(),
			Method:     r.Method,
			Path:       r.URL.Path,
			Status:     recorder.status,
			DurationMs: time.Since(started).Milliseconds(),
			UserEmail:  bestEffortUserEmail(r),
		})
	})
}

// isFrameworkInternalPath reports whether `path` belongs to the
// framework's own surface (admin panel + SSE reload channel) rather
// than the user's application. These paths are excluded from the
// request log so the panel's "recent requests" section reflects
// what the app is doing, not what the panel itself is doing.
//
// Note: /_auth/* is intentionally NOT excluded — sign-ins are
// real application traffic the operator does want to see.
func isFrameworkInternalPath(path string) bool {
	if path == "/_mar/reload" {
		return true
	}
	if path == "/_mar/admin" || strings.HasPrefix(path, "/_mar/admin/") {
		return true
	}
	return false
}

// statusRecorder is a tiny http.ResponseWriter that captures the
// status code so middleware can log it. Default 200 if the handler
// never explicitly calls WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// bestEffortUserEmail looks for a logged-in user-auth session and
// returns the email if found. Best-effort — never blocks, never
// fails the request, returns "" when the user is anonymous or
// when anything goes wrong.
func bestEffortUserEmail(r *http.Request) string {
	tok := extractSessionToken(r)
	if tok == "" {
		return ""
	}
	secret := AuthSecret()
	if secret == "" {
		return ""
	}
	db, err := dbHandle()
	if err != nil {
		return ""
	}
	uid, ok := sessionUserID(db, secret, tok)
	if !ok {
		return ""
	}
	cfg := runtime.CurrentAuth()
	if cfg == nil {
		return ""
	}
	js, err := runtime.LoadUserJSON(*cfg, uid)
	if err != nil || js == nil {
		return ""
	}
	if email, ok := js["email"].(string); ok {
		return email
	}
	return ""
}

// -- Introspection helpers (shared with the Mar.Admin.* bodies) --

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func hostTarget() string {
	return goruntime.GOOS + "-" + goruntime.GOARCH
}

func dbFileSizes() (db int64, wal int64) {
	path := runtime.CurrentDBPath()
	if path == "" {
		return 0, 0
	}
	return statSize(path), statSize(path + "-wal")
}

func statSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// listTables returns the set of user + framework tables in the
// SQLite database. Skips SQLite-internal tables (sqlite_*).
//
// On error (open or iteration) we return nil rather than propagating
// — callers use the result as an allowlist for the entity browser,
// and a nil allowlist correctly rejects every entity-name request
// (slices.Contains(nil, x) is false). Iteration errors are still
// checked so a partial result doesn't masquerade as the full list.
func listTables(db *sql.DB) []string {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		return nil
	}
	return out
}

// tableColumns returns the column names for `name` in declared
// order. Uses PRAGMA table_info, available in any SQLite version.
func tableColumns(db *sql.DB, name string) ([]string, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info("%s")`, name))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		// PRAGMA table_info returns: cid, name, type, notnull, dflt_value, pk
		var (
			cid     int
			cname   string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, cname)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

func quotedColumns(cols []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = `"` + c + `"`
	}
	return strings.Join(parts, ", ")
}
