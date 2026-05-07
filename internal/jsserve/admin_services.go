// Admin panel services — JSON endpoints under /_mar/admin/api/*
// consumed by the embedded SPA. Each handler reads from the
// framework's runtime state (entity registry, request log buffer,
// boot metadata) and serializes a small JSON shape.
//
// All endpoints are gated by requireAdminSession (Phase 3).

package jsserve

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	goruntime "runtime"
	"runtime/debug"
	"strconv"
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
	buildTarget     string
)

// SetAdminBuildInfo plumbs build-time stamps into the framework so
// /_mar/admin/api/server-info can show them. Called once from the
// CLI before ServeLive. Empty values are tolerated and rendered as
// "—" by the SPA.
func SetAdminBuildInfo(version, target string) {
	authMu.Lock()
	defer authMu.Unlock()
	marVersion = version
	buildTarget = target
}

// noteServerBooted is called from ServeLive at the moment the
// listener is about to accept traffic. Idempotent if called
// multiple times (test hot-reload paths).
func noteServerBooted() {
	atomic.StoreInt64(&bootStartedAtMs, time.Now().UnixMilli())
}

// Request counters — atomic so middleware can read without locking.
var (
	requestsTotal    int64
	requestsInFlight int64
)

// requestLogger holds the in-memory ring buffer powering
// /_mar/admin/api/recent-requests. Initialized at ServeLive boot
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
//                       dominate the buffer and noise out the panel.
//   - /_mar/admin/*   — the admin panel polling itself (whoami every
//                       boot, server-info / db-stats / recent-requests
//                       on each refresh, static assets). Including
//                       these makes the "recent requests" view show
//                       the panel watching itself, drowning out the
//                       actual app traffic the operator wants to see.
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
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
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
	uid, ok := sessionUserID(db, secret, c.Value)
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

// -- Service handlers --

func handleAdminServerInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}
	bootedMs := atomic.LoadInt64(&bootStartedAtMs)
	resp := map[string]any{
		"marVersion":       defaultStr(marVersion, "dev"),
		"goVersion":        goruntime.Version(),
		"buildTarget":      defaultStr(buildTarget, hostTarget()),
		"bootedAtMs":       bootedMs,
		"requestsTotal":    atomic.LoadInt64(&requestsTotal),
		"requestsInFlight": atomic.LoadInt64(&requestsInFlight),
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleAdminDBStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}
	db, err := adminDB()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"dbSizeBytes":  0,
			"walSizeBytes": 0,
			"entities":     []any{},
		})
		return
	}

	dbSize, walSize := dbFileSizes()

	// Two buckets: user-defined entities (the business model) and
	// framework-managed tables (auth, admin, schema migrations —
	// everything under the reserved _mar_ prefix). The panel
	// renders them as separate sub-groups so framework noise doesn't
	// drown out the operator's own tables.
	type stat struct {
		Name     string `json:"name"`
		RowCount int    `json:"rowCount"`
	}
	tables := listTables(db)
	business := make([]stat, 0, len(tables))
	framework := make([]stat, 0)
	for _, t := range tables {
		// Skip SQLite's internal tables (sqlite_*) entirely —
		// never useful in the admin browser, just noise.
		if strings.HasPrefix(t, "sqlite_") {
			continue
		}
		var n int
		// Quoting protects against unusual entity names; we already
		// validated names came from the schema so injection isn't a
		// concern, but quoting is the principled form.
		if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, t)).Scan(&n); err != nil {
			continue
		}
		row := stat{Name: t, RowCount: n}
		if strings.HasPrefix(t, "_mar_") {
			framework = append(framework, row)
		} else {
			business = append(business, row)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dbSizeBytes":     dbSize,
		"walSizeBytes":    walSize,
		"entities":        business,
		"frameworkTables": framework,
	})
}

func handleAdminRecentRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}
	if requestLogger == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": requestLogger.Snapshot(),
	})
}

func handleAdminEntityRows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}
	entityName := r.URL.Query().Get("entity")
	if entityName == "" {
		writeAuthError(w, http.StatusBadRequest, "missing_entity")
		return
	}
	db, err := adminDB()
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "no_database")
		return
	}
	// Whitelist against the live schema so an attacker can't pivot
	// arbitrary SQL through the entity parameter.
	allowed := listTables(db)
	if !contains(allowed, entityName) {
		writeAuthError(w, http.StatusNotFound, "unknown_entity")
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	cursor := r.URL.Query().Get("cursor")

	columns, err := tableColumns(db, entityName)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "introspect")
		return
	}

	// Cursor-based pagination by primary id. We assume entities have
	// an integer-keyed `id` column — the convention every Mar entity
	// uses (Entity.serial). Tables without an `id` column fall back
	// to LIMIT/OFFSET; for v1 read-only browsing of framework tables
	// this is acceptable.
	hasID := contains(columns, "id")

	var rows *sql.Rows
	if hasID {
		// Quoting columns + table name to support entity names that
		// happen to be SQL keywords or contain special characters.
		query := fmt.Sprintf(`SELECT %s FROM "%s"`, quotedColumns(columns), entityName)
		if cursor != "" {
			query += ` WHERE id > ?`
			query += ` ORDER BY id ASC LIMIT ?`
			rows, err = db.Query(query, cursor, limit+1)
		} else {
			query += ` ORDER BY id ASC LIMIT ?`
			rows, err = db.Query(query, limit+1)
		}
	} else {
		query := fmt.Sprintf(`SELECT %s FROM "%s" LIMIT ?`,
			quotedColumns(columns), entityName)
		rows, err = db.Query(query, limit+1)
	}
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "query")
		return
	}
	defer rows.Close()

	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		dest := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			break
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			row[col] = jsonable(dest[i])
		}
		items = append(items, row)
	}

	// Pagination: we asked for limit+1 to detect "more". If we got
	// limit+1 rows, drop the last and return its id as nextCursor.
	var nextCursor any
	if len(items) > limit {
		last := items[limit-1] // ← the last item we'll return
		items = items[:limit]
		if hasID {
			nextCursor = last["id"]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"columns":    columns,
		"items":      items,
		"nextCursor": nextCursor,
	})
}

// -- Small helpers --

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
			continue
		}
		out = append(out, name)
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
	return cols, nil
}

func quotedColumns(cols []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = `"` + c + `"`
	}
	return strings.Join(parts, ", ")
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// jsonable converts a SQL driver scan target value to something
// json.Marshal can serialize. Bytes become strings (text columns
// arrive as []byte sometimes), nil stays nil.
func jsonable(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case nil:
		return nil
	default:
		return x
	}
}

// hostBuildInfo is invoked from main package init to surface mar
// version + commit if available via debug.ReadBuildInfo (works for
// `go install`-style builds without ldflags).
func hostBuildInfo() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}
