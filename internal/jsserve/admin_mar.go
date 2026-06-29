package jsserve

import (
	_ "embed"
	"fmt"
	"io"
	"net/http"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"mar/internal/admin"
	"mar/internal/ast"
	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
	"slices"
)

// This file wires the runtime bodies for the Mar.Admin.* builtins (the type
// surface + injection point live in internal/runtime/admin.go). The native
// admin SPA and the Mar-native admin panel read the EXACT same introspection
// helpers — listTables, dbFileSizes, requestLogger, the request counters — so
// this is just a second projection of that data, shaped as Mar Values instead
// of JSON. A Page.adminProtected program renders it; the SPA hits the JSON
// handlers. One source of truth, two front-ends.
//
// registerAdminMarServices is called from noteServerBooted: the bodies are
// closures that read live server state when the Mar effect is performed (at
// request time), so registering the funcs early is safe.
func registerAdminMarServices() {
	runtime.RegisterAdminServices(&runtime.AdminServices{
		ServerInfo:     marServerInfo,
		DBStats:        marDBStats,
		RecentRequests: marRecentRequests,
		ListEntities:   marListEntities,
		ListEntityRows: marListEntityRows,
		ListBackups:    marListBackups,
	})
}

// -- Mar Value constructors (tiny shims to keep the bodies readable) --

func mvStr(s string) runtime.Value { return runtime.VString{V: s} }
func mvInt(n int64) runtime.Value  { return runtime.VInt{V: n} }
func mvList(xs []runtime.Value) runtime.Value {
	if xs == nil {
		xs = []runtime.Value{}
	}
	return runtime.VList{Elements: xs}
}
func mvRec(order []string, fields map[string]runtime.Value) runtime.Value {
	return runtime.VRecord{Order: order, Fields: fields}
}

// -- Bodies (shapes MUST match the schemes in typecheck/env.go) --

func marServerInfo() (runtime.Value, error) {
	return mvRec(
		[]string{"marVersion", "goVersion", "buildTarget", "bootedAtMs", "requestsTotal", "requestsInFlight"},
		map[string]runtime.Value{
			"marVersion":       mvStr(defaultStr(MarVersion(), "dev")),
			"goVersion":        mvStr(goruntime.Version()),
			"buildTarget":      mvStr(hostTarget()),
			"bootedAtMs":       mvInt(atomic.LoadInt64(&bootStartedAtMs)),
			"requestsTotal":    mvInt(atomic.LoadInt64(&requestsTotal)),
			"requestsInFlight": mvInt(atomic.LoadInt64(&requestsInFlight)),
		}), nil
}

func marDBStats() (runtime.Value, error) {
	order := []string{"dbSizeBytes", "walSizeBytes", "entities", "frameworkTables"}
	db, err := adminDB()
	if err != nil {
		return mvRec(order, map[string]runtime.Value{
			"dbSizeBytes":     mvInt(0),
			"walSizeBytes":    mvInt(0),
			"entities":        mvList(nil),
			"frameworkTables": mvList(nil),
		}), nil
	}
	dbSize, walSize := dbFileSizes()

	// Same two-bucket split the SPA uses: the operator's own entities vs
	// framework-managed tables (the reserved _mar_ prefix). sqlite_* internal
	// tables are dropped entirely — never useful, just noise.
	var business, framework []runtime.Value
	for _, t := range listTables(db) {
		if strings.HasPrefix(t, "sqlite_") {
			continue
		}
		var n int64
		if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, t)).Scan(&n); err != nil {
			continue
		}
		row := mvRec([]string{"name", "rowCount"}, map[string]runtime.Value{
			"name":     mvStr(t),
			"rowCount": mvInt(n),
		})
		if strings.HasPrefix(t, "_mar_") {
			framework = append(framework, row)
		} else {
			business = append(business, row)
		}
	}
	return mvRec(order, map[string]runtime.Value{
		"dbSizeBytes":     mvInt(dbSize),
		"walSizeBytes":    mvInt(walSize),
		"entities":        mvList(business),
		"frameworkTables": mvList(framework),
	}), nil
}

func marRecentRequests() (runtime.Value, error) {
	if requestLogger == nil {
		return mvList(nil), nil
	}
	logs := requestLogger.Snapshot()
	items := make([]runtime.Value, 0, len(logs))
	for _, e := range logs {
		items = append(items, mvRec(
			[]string{"atMs", "method", "path", "status", "durationMs", "userEmail"},
			map[string]runtime.Value{
				"atMs":       mvInt(e.AtMs),
				"method":     mvStr(e.Method),
				"path":       mvStr(e.Path),
				"status":     mvInt(int64(e.Status)),
				"durationMs": mvInt(e.DurationMs),
				"userEmail":  mvStr(e.UserEmail),
			}))
	}
	return mvList(items), nil
}

func marListEntities() (runtime.Value, error) {
	db, err := adminDB()
	if err != nil {
		return mvList(nil), nil
	}
	var out []runtime.Value
	for _, t := range listTables(db) {
		if strings.HasPrefix(t, "sqlite_") {
			continue
		}
		cols, err := tableColumns(db, t)
		if err != nil {
			continue
		}
		colVals := make([]runtime.Value, len(cols))
		for i, c := range cols {
			colVals[i] = mvStr(c)
		}
		out = append(out, mvRec([]string{"name", "columns"}, map[string]runtime.Value{
			"name":    mvStr(t),
			"columns": mvList(colVals),
		}))
	}
	return mvList(out), nil
}

// marListEntityRows browses one table. v1: stringified cells in a Dict so a
// single shape covers every column; capped (no cursor — that's a v2 refinement
// matching the SPA's pagination).
func marListEntityRows(entity string) (runtime.Value, error) {
	db, err := adminDB()
	if err != nil {
		return mvList(nil), nil
	}
	// Whitelist against the live schema — the entity name can't pivot
	// arbitrary SQL (same defense as the JSON handler).
	if !slices.Contains(listTables(db), entity) {
		return nil, fmt.Errorf("unknown entity %q", entity)
	}
	columns, err := tableColumns(db, entity)
	if err != nil {
		return nil, err
	}

	const limit = 100
	query := fmt.Sprintf(`SELECT %s FROM "%s" LIMIT %d`, quotedColumns(columns), entity, limit)
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// VDict.Pairs MUST stay sorted ascending by key — build them in
	// sorted-column order so the invariant holds by construction.
	sortedCols := append([]string(nil), columns...)
	sort.Strings(sortedCols)

	var out []runtime.Value
	for rows.Next() {
		dest := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		byCol := make(map[string]any, len(columns))
		for i, c := range columns {
			byCol[c] = dest[i]
		}
		pairs := make([]runtime.VDictPair, 0, len(sortedCols))
		for _, c := range sortedCols {
			pairs = append(pairs, runtime.VDictPair{Key: mvStr(c), Value: mvStr(stringifyCell(byCol[c]))})
		}
		out = append(out, runtime.VDict{Pairs: pairs})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return mvList(out), nil
}

// stringifyCell renders a raw SQL cell as a string for the v1 Dict-based row
// browser. SQLite hands back []byte / int64 / float64 / nil via Scan(&any).
func stringifyCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(x)
	}
}

// marListBackups projects the database backup catalog (the same data the
// legacy SPA's /_mar/admin/api/database-backups returns) as Mar Values. The
// panel renders each entry with a plain <a href> to the existing download
// endpoint — the download itself never round-trips through Mar.Admin.*.
func marListBackups() (runtime.Value, error) {
	dbPath := runtime.CurrentDBPath()
	if dbPath == "" {
		return mvList(nil), nil
	}
	entries, err := admin.ListCatalog(admin.CatalogDir(dbPath))
	if err != nil {
		return nil, err
	}
	out := make([]runtime.Value, 0, len(entries))
	for _, e := range entries {
		out = append(out, mvRec([]string{"id", "sizeBytes", "createdAtMs"}, map[string]runtime.Value{
			"id":          mvStr(e.ID),
			"sizeBytes":   mvInt(e.SizeBytes),
			"createdAtMs": mvInt(e.CreatedAt.UnixMilli()),
		}))
	}
	return mvList(out), nil
}

// -- HTTP transport for the Mar-native panel --
//
// The panel's Mar.Admin.* effects fetch these endpoints; each runs the
// corresponding body above and returns the Mar Value in the FRONTEND wire
// format (runtime.EncodeValueJSON ↔ jsToMar), not the plain JSON the legacy
// SPA endpoints emit — that's what lets a typed Dict survive the round-trip.
// Gated by the admin session cookie, same as every other /_mar/admin/api/*
// route. mountAdminHandlers wires them under /_mar/admin/api/mar/.

func writeMarValue(w http.ResponseWriter, produce func() (runtime.Value, error)) {
	v, err := produce()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "introspect")
		return
	}
	body, err := runtime.EncodeValueJSON(v)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "encode")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

func handleAdminMarServerInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}
	writeMarValue(w, marServerInfo)
}

func handleAdminMarDBStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}
	writeMarValue(w, marDBStats)
}

func handleAdminMarRecentRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}
	writeMarValue(w, marRecentRequests)
}

func handleAdminMarEntities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}
	writeMarValue(w, marListEntities)
}

func handleAdminMarEntityRows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}
	entity := r.URL.Query().Get("entity")
	if entity == "" {
		writeAuthError(w, http.StatusBadRequest, "missing_entity")
		return
	}
	writeMarValue(w, func() (runtime.Value, error) { return marListEntityRows(entity) })
}

func handleAdminMarBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := gateAdminSession(w, r); !ok {
		return
	}
	writeMarValue(w, marListBackups)
}

// -- The Mar-native panel program: compiled at boot, served at /_mar/admin --

//go:embed admin_panel.mar
var adminPanelSource string

var (
	adminPanelOnce sync.Once
	adminPanelJSON []byte
	adminPanelErr  error
)

// adminPanelProgram parses, typechecks and serializes the embedded panel into
// the frontend program.json the runtime mounts. Compiled once and cached — it's
// framework-owned source that never changes at runtime. A typecheck failure
// here is a framework bug (the panel is shipped, not user-authored), surfaced
// the first time /_mar/admin/program.json is hit.
func adminPanelProgram() ([]byte, error) {
	adminPanelOnce.Do(func() {
		mod, err := parser.Parse(adminPanelSource)
		if err != nil {
			adminPanelErr = fmt.Errorf("admin panel parse: %w", err)
			return
		}
		if _, err := typecheck.CheckModule(mod); err != nil {
			adminPanelErr = fmt.Errorf("admin panel typecheck: %w", err)
			return
		}
		// Mirror apphost.PickFrontMods: the frontend runtime mounts a
		// synthetic, NAMELESS `__entry = appFrontend [<pages>]` module and
		// uses "__entry" as the program entry. Nameless matters — a named
		// module's decls (our Admin.Panel.main) bind into a private frame,
		// so marRun's shared-env entry lookup can't see them. The nameless
		// module's decl binds into the shared env where the lookup runs.
		entryMod := &ast.Module{
			Decls: []ast.Decl{
				&ast.ValueDecl{
					Name: "__entry",
					Body: &ast.EApp{
						Fn: &ast.EVar{Name: "appFrontend"},
						Arg: &ast.EList{Elements: []ast.Expr{
							&ast.EQualified{Module: []string{"Admin", "Panel"}, Name: "page"},
							&ast.EQualified{Module: []string{"Admin", "Panel"}, Name: "tablePage"},
							&ast.EQualified{Module: []string{"Admin", "Panel"}, Name: "loginPage"},
						}},
					},
				},
			},
		}
		adminPanelJSON, adminPanelErr = makeProgramJSON([]*ast.Module{mod, entryMod}, "__entry", false, false)
	})
	return adminPanelJSON, adminPanelErr
}

// handleAdminMarShell serves the HTML shell for the Mar-native panel. It reuses
// the standard app shell (same CSS + boot path), with the program URL pointed
// at the panel's own program.json. No HTTP auth gate (mirrors handleAdminPage)
// — the panel's data calls are gated by the admin cookie server-side.
func handleAdminMarShell(w http.ResponseWriter, r *http.Request) {
	html := strings.ReplaceAll(
		fmt.Sprintf(pageHTML, "Mar Admin"),
		"/_mar/program.json",
		"/_mar/admin/program.json",
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, html)
}

func handleAdminMarProgram(w http.ResponseWriter, r *http.Request) {
	body, err := adminPanelProgram()
	if err != nil {
		http.Error(w, "admin panel: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
