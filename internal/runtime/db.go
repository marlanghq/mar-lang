package runtime

import (
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

// SQLite is a runtime-managed resource: user code interacts with the
// database only through `Entity` / `Repo`, never with raw connections.
// The path is configured once (typically in mar.json's `database.path`),
// the runtime opens lazily on first Repo call and reuses the connection
// for the process lifetime.
//
// Migrations and per-call SQL generation live in repo.go; this file only
// owns the global handle and the Go-value ⇄ runtime-Value scan helpers.

var (
	dbMu      sync.Mutex
	dbPath    string
	dbHandle  *sql.DB
	dbOpenErr error
)

// currentDBPath returns the configured path under the same lock that
// SetDBPath / getDB use. Read-only access for callers (e.g. the
// migrator's lock-failure log lines) that don't need the live handle.
func currentDBPath() string {
	dbMu.Lock()
	defer dbMu.Unlock()
	return dbPath
}

// CurrentDBPath is the exported counterpart for callers outside the
// runtime package — used by `mar migrate plan/status` to decide
// whether the project has a DB configured before opening it.
func CurrentDBPath() string {
	return currentDBPath()
}

// OpenDB exposes getDB to the cmd/mar package for the migrate
// subcommand. Same lazy semantics — opens on first call, reuses the
// cached handle thereafter. Cleaner than re-implementing the open
// logic in cmd/mar.
func OpenDB() (*sql.DB, error) {
	return getDB()
}

// displayPath truncates an absolute path for log output. The migrator
// uses this when it can't say the full path won't be unwieldy. Empty
// path becomes "<unset>" so the message still parses.
func displayPath(p string) string {
	if p == "" {
		return "<unset>"
	}
	return p
}

// SetDBPath records the SQLite file the runtime should open on first use.
// Called by `mar dev` / `mar build` after reading mar.json. Empty path is
// allowed (and means "no DB available" — Repo calls will error out with a
// clear message, but the rest of the program runs fine).
func SetDBPath(path string) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if dbHandle != nil && path != dbPath {
		// Path changed mid-session (hot-reload after editing mar.json).
		// Close the old handle so the next Repo call reopens.
		_ = dbHandle.Close()
		dbHandle = nil
		dbOpenErr = nil
	}
	dbPath = path
}

// getDB returns the lazy-opened SQLite handle. First call opens the file
// (creating it if absent); subsequent calls return the cached handle.
func getDB() (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if dbHandle != nil {
		return dbHandle, nil
	}
	if dbOpenErr != nil {
		return nil, dbOpenErr
	}
	if dbPath == "" {
		dbOpenErr = fmt.Errorf("no database configured: set `database.path` in mar.json")
		return nil, dbOpenErr
	}
	// DSN tweaks for concurrency:
	//   - busy_timeout(5000): retry on SQLITE_BUSY for up to 5s
	//     before erroring. Prevents intermittent failures when
	//     parallel requests contend on the same writer.
	//   - journal_mode(wal): writers don't block readers; readers
	//     don't block writers. The default DELETE journal serializes
	//     everything and turns 3-parallel admin GETs into a coin
	//     flip on whether they all succeed.
	//
	// Both PRAGMAs are applied per-connection by modernc/sqlite when
	// passed in the DSN. WAL mode persists in the DB file, so once
	// set the first time, subsequent opens see WAL-mode storage.
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		dbOpenErr = err
		return nil, err
	}
	if err := conn.Ping(); err != nil {
		dbOpenErr = err
		_ = conn.Close()
		return nil, err
	}
	dbHandle = conn
	return dbHandle, nil
}

// goValueToScalar maps a single SQL value to a runtime Value, unwrapped
// (no Maybe). Used by Repo decoders when the column is declared NOT NULL.
//
//   - int64  → VInt
//   - float64 → VFloat
//   - string → VString
//   - []byte → VString (assumed UTF-8)
//   - bool   → VBool
//   - nil    → VString "" (defensive — should be filtered by NotNull check)
func goValueToScalar(v any) Value {
	if v == nil {
		return VString{V: ""}
	}
	switch x := v.(type) {
	case int64:
		return VInt{V: x}
	case float64:
		return VFloat{V: x}
	case bool:
		return VBool{V: x}
	case string:
		return VString{V: x}
	case []byte:
		return VString{V: string(x)}
	}
	return VString{V: fmt.Sprintf("%v", v)}
}
