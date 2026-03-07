package sqlitecli

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	Path    string
	sqlDB   *sql.DB
	openErr error

	hookMu  sync.RWMutex
	onQuery func(QueryEvent)
}

type Result struct {
	Changes       int64
	LastInsertRow int64
}

type Statement struct {
	Query string
	Args  []any
}

type QueryEvent struct {
	SQL        string
	DurationMs float64
	RowCount   int
	Error      string
}

// Config controls SQLite PRAGMA settings used on database open.
type Config struct {
	JournalMode       string
	Synchronous       string
	ForeignKeys       bool
	BusyTimeoutMs     int
	WALAutoCheckpoint int
	JournalSizeLimitB int64
	MmapSizeB         int64
	CacheSizeKB       int
}

const (
	defaultJournalMode       = "wal"
	defaultSynchronous       = "normal"
	defaultForeignKeys       = true
	defaultBusyTimeoutMs     = 5000
	defaultWALAutoCheckpoint = 1000
	defaultJournalSizeLimitB = int64(64 * 1024 * 1024)
	defaultMmapSizeB         = int64(128 * 1024 * 1024)
	defaultCacheSizeKB       = 2000
)

// DefaultConfig returns Mar's default SQLite settings focused on local performance.
func DefaultConfig() Config {
	return Config{
		JournalMode:       defaultJournalMode,
		Synchronous:       defaultSynchronous,
		ForeignKeys:       defaultForeignKeys,
		BusyTimeoutMs:     defaultBusyTimeoutMs,
		WALAutoCheckpoint: defaultWALAutoCheckpoint,
		JournalSizeLimitB: defaultJournalSizeLimitB,
		MmapSizeB:         defaultMmapSizeB,
		CacheSizeKB:       defaultCacheSizeKB,
	}
}

// Open creates a SQLite connection with default driver behavior.
func Open(path string) *DB {
	sqlDB, err := sql.Open("sqlite", path)
	db := &DB{
		Path:    path,
		sqlDB:   sqlDB,
		openErr: err,
	}
	if err != nil {
		return db
	}

	// Keep a single connection to preserve SQLite transactional behavior and
	// avoid surprising lock interactions in this runtime.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	// Ensure the database is reachable early, so failures are clearer.
	if pingErr := sqlDB.Ping(); pingErr != nil {
		db.openErr = pingErr
	}
	return db
}

// OpenWithConfig creates a SQLite connection and applies PRAGMA settings from cfg.
func OpenWithConfig(path string, cfg Config) *DB {
	db := Open(path)
	if db.openErr != nil {
		return db
	}
	normalizedCfg, cfgErr := normalizeConfig(cfg)
	if cfgErr != nil {
		db.openErr = cfgErr
		return db
	}
	if applyErr := applyPragmas(db.sqlDB, normalizedCfg); applyErr != nil {
		db.openErr = applyErr
		return db
	}
	return db
}

func normalizeConfig(cfg Config) (Config, error) {
	cfg.JournalMode = strings.ToLower(strings.TrimSpace(cfg.JournalMode))
	switch cfg.JournalMode {
	case "wal", "delete", "truncate", "persist", "memory", "off":
	default:
		return Config{}, fmt.Errorf("invalid sqlite journal mode %q", cfg.JournalMode)
	}

	cfg.Synchronous = strings.ToLower(strings.TrimSpace(cfg.Synchronous))
	switch cfg.Synchronous {
	case "off", "normal", "full", "extra":
	default:
		return Config{}, fmt.Errorf("invalid sqlite synchronous mode %q", cfg.Synchronous)
	}

	if cfg.BusyTimeoutMs < 0 || cfg.BusyTimeoutMs > 600000 {
		return Config{}, fmt.Errorf("invalid sqlite busy timeout %dms", cfg.BusyTimeoutMs)
	}
	if cfg.WALAutoCheckpoint < 0 || cfg.WALAutoCheckpoint > 1000000 {
		return Config{}, fmt.Errorf("invalid sqlite wal_autocheckpoint %d", cfg.WALAutoCheckpoint)
	}
	if cfg.JournalSizeLimitB < -1 {
		return Config{}, fmt.Errorf("invalid sqlite journal_size_limit %d", cfg.JournalSizeLimitB)
	}
	if cfg.MmapSizeB < 0 {
		return Config{}, fmt.Errorf("invalid sqlite mmap_size %d", cfg.MmapSizeB)
	}
	if cfg.CacheSizeKB < 0 || cfg.CacheSizeKB > 1048576 {
		return Config{}, fmt.Errorf("invalid sqlite cache_size_kb %d", cfg.CacheSizeKB)
	}
	return cfg, nil
}

func applyPragmas(db *sql.DB, cfg Config) error {
	statements := []string{
		fmt.Sprintf("PRAGMA journal_mode = %s", strings.ToUpper(cfg.JournalMode)),
		fmt.Sprintf("PRAGMA synchronous = %s", strings.ToUpper(cfg.Synchronous)),
		fmt.Sprintf("PRAGMA foreign_keys = %s", boolToOnOff(cfg.ForeignKeys)),
		fmt.Sprintf("PRAGMA busy_timeout = %d", cfg.BusyTimeoutMs),
		fmt.Sprintf("PRAGMA wal_autocheckpoint = %d", cfg.WALAutoCheckpoint),
		fmt.Sprintf("PRAGMA journal_size_limit = %d", cfg.JournalSizeLimitB),
		fmt.Sprintf("PRAGMA mmap_size = %d", cfg.MmapSizeB),
		fmt.Sprintf("PRAGMA cache_size = -%d", cfg.CacheSizeKB),
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to apply sqlite setting (%s): %w", stmt, err)
		}
	}
	return nil
}

func boolToOnOff(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
}

// Close releases the underlying SQLite connection.
func (db *DB) Close() error {
	if db == nil || db.sqlDB == nil {
		return nil
	}
	return db.sqlDB.Close()
}

// SetQueryHook registers a callback invoked after each executed query summary.
func (db *DB) SetQueryHook(hook func(QueryEvent)) {
	db.hookMu.Lock()
	defer db.hookMu.Unlock()
	db.onQuery = hook
}

// Exec runs a statement and returns SQLite change metadata for it.
func (db *DB) Exec(query string, args ...any) (Result, error) {
	logSQL := interpolateSQLForLog(query, args)
	if err := db.ensureOpen(); err != nil {
		db.emitQueryEvent(QueryEvent{
			SQL:        logSQL,
			DurationMs: 0,
			RowCount:   0,
			Error:      err.Error(),
		})
		return Result{}, err
	}

	startedAt := time.Now()
	res, err := db.sqlDB.Exec(query, args...)
	if err != nil {
		db.emitQueryEvent(QueryEvent{
			SQL:        logSQL,
			DurationMs: elapsedMs(startedAt),
			RowCount:   0,
			Error:      err.Error(),
		})
		return Result{}, err
	}

	changes, _ := res.RowsAffected()
	lastInsertRow, _ := res.LastInsertId()
	db.emitQueryEvent(QueryEvent{
		SQL:        logSQL,
		DurationMs: elapsedMs(startedAt),
		RowCount:   0,
		Error:      "",
	})
	return Result{
		Changes:       changes,
		LastInsertRow: lastInsertRow,
	}, nil
}

// QueryRows runs a query and returns all rows as column-name maps.
func (db *DB) QueryRows(query string, args ...any) ([]map[string]any, error) {
	logSQL := interpolateSQLForLog(query, args)
	if err := db.ensureOpen(); err != nil {
		db.emitQueryEvent(QueryEvent{
			SQL:        logSQL,
			DurationMs: 0,
			RowCount:   0,
			Error:      err.Error(),
		})
		return nil, err
	}

	startedAt := time.Now()
	rows, err := db.sqlDB.Query(query, args...)
	if err != nil {
		db.emitQueryEvent(QueryEvent{
			SQL:        logSQL,
			DurationMs: elapsedMs(startedAt),
			RowCount:   0,
			Error:      err.Error(),
		})
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		db.emitQueryEvent(QueryEvent{
			SQL:        logSQL,
			DurationMs: elapsedMs(startedAt),
			RowCount:   0,
			Error:      err.Error(),
		})
		return nil, err
	}

	result := make([]map[string]any, 0, 16)
	for rows.Next() {
		rawValues := make([]any, len(columns))
		scanTargets := make([]any, len(columns))
		for i := range rawValues {
			scanTargets[i] = &rawValues[i]
		}

		if err := rows.Scan(scanTargets...); err != nil {
			db.emitQueryEvent(QueryEvent{
				SQL:        logSQL,
				DurationMs: elapsedMs(startedAt),
				RowCount:   len(result),
				Error:      err.Error(),
			})
			return nil, err
		}

		record := make(map[string]any, len(columns))
		for i, col := range columns {
			record[col] = normalizeColumnValue(rawValues[i])
		}
		result = append(result, record)
	}

	if err := rows.Err(); err != nil {
		db.emitQueryEvent(QueryEvent{
			SQL:        logSQL,
			DurationMs: elapsedMs(startedAt),
			RowCount:   len(result),
			Error:      err.Error(),
		})
		return nil, err
	}

	db.emitQueryEvent(QueryEvent{
		SQL:        logSQL,
		DurationMs: elapsedMs(startedAt),
		RowCount:   len(result),
		Error:      "",
	})
	return result, nil
}

// QueryRow runs a query and returns the first row, if any.
func (db *DB) QueryRow(query string, args ...any) (map[string]any, bool, error) {
	rows, err := db.QueryRows(query, args...)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	return rows[0], true, nil
}

// ExecTx executes statements in a single transaction and rolls back on failure.
func (db *DB) ExecTx(statements []Statement) error {
	logSQL := txStatementSummary(statements)
	if len(statements) == 0 {
		return nil
	}
	if err := db.ensureOpen(); err != nil {
		db.emitQueryEvent(QueryEvent{
			SQL:        logSQL,
			DurationMs: 0,
			RowCount:   0,
			Error:      err.Error(),
		})
		return err
	}

	startedAt := time.Now()
	tx, err := db.sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		db.emitQueryEvent(QueryEvent{
			SQL:        logSQL,
			DurationMs: elapsedMs(startedAt),
			RowCount:   0,
			Error:      err.Error(),
		})
		return err
	}

	for i, stmt := range statements {
		if _, err := tx.Exec(stmt.Query, stmt.Args...); err != nil {
			_ = tx.Rollback()
			db.emitQueryEvent(QueryEvent{
				SQL:        logSQL,
				DurationMs: elapsedMs(startedAt),
				RowCount:   0,
				Error:      fmt.Sprintf("statement %d: %v", i+1, err),
			})
			return fmt.Errorf("statement %d: %w", i+1, err)
		}
	}

	if err := tx.Commit(); err != nil {
		db.emitQueryEvent(QueryEvent{
			SQL:        logSQL,
			DurationMs: elapsedMs(startedAt),
			RowCount:   0,
			Error:      err.Error(),
		})
		return err
	}

	db.emitQueryEvent(QueryEvent{
		SQL:        logSQL,
		DurationMs: elapsedMs(startedAt),
		RowCount:   0,
		Error:      "",
	})
	return nil
}

func (db *DB) emitQueryEvent(event QueryEvent) {
	db.hookMu.RLock()
	hook := db.onQuery
	db.hookMu.RUnlock()
	if hook != nil {
		hook(event)
	}
}

func (db *DB) ensureOpen() error {
	if db == nil {
		return fmt.Errorf("sqlite database handle is nil")
	}
	if db.openErr != nil {
		return db.openErr
	}
	if db.sqlDB == nil {
		return fmt.Errorf("sqlite database connection is not initialized")
	}
	return nil
}

func elapsedMs(start time.Time) float64 {
	return time.Since(start).Seconds() * 1000
}

func normalizeColumnValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		return string(typed)
	case bool:
		if typed {
			return int64(1)
		}
		return int64(0)
	default:
		return typed
	}
}

func txStatementSummary(statements []Statement) string {
	if len(statements) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("BEGIN; ")
	for i, stmt := range statements {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(interpolateSQLForLog(stmt.Query, stmt.Args))
	}
	b.WriteString("; COMMIT;")
	return b.String()
}

func interpolateSQLForLog(query string, args []any) string {
	if strings.TrimSpace(query) == "" || len(args) == 0 {
		return query
	}

	var b strings.Builder
	argIndex := 0
	inSingleQuote := false
	inDoubleQuote := false
	runes := []rune(query)

	for i := 0; i < len(runes); i++ {
		ch := runes[i]

		if ch == '\'' && !inDoubleQuote {
			if inSingleQuote && i+1 < len(runes) && runes[i+1] == '\'' {
				b.WriteRune(ch)
				b.WriteRune(runes[i+1])
				i++
				continue
			}
			inSingleQuote = !inSingleQuote
			b.WriteRune(ch)
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			b.WriteRune(ch)
			continue
		}

		if ch == '?' && !inSingleQuote && !inDoubleQuote {
			j := i + 1
			for j < len(runes) && runes[j] >= '0' && runes[j] <= '9' {
				j++
			}
			if argIndex < len(args) {
				b.WriteString(formatSQLArgForLog(args[argIndex]))
				argIndex++
			} else {
				b.WriteRune('?')
				if j > i+1 {
					b.WriteString(string(runes[i+1 : j]))
				}
			}
			i = j - 1
			continue
		}

		b.WriteRune(ch)
	}

	return b.String()
}

func formatSQLArgForLog(arg any) string {
	switch typed := arg.(type) {
	case nil:
		return "NULL"
	case string:
		return quoteSQLString(typed)
	case []byte:
		return "x'" + hex.EncodeToString(typed) + "'"
	case bool:
		if typed {
			return "1"
		}
		return "0"
	case time.Time:
		return quoteSQLString(typed.Format(time.RFC3339Nano))
	case fmt.Stringer:
		return quoteSQLString(typed.String())
	}

	value := reflect.ValueOf(arg)
	if !value.IsValid() {
		return "NULL"
	}

	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(value.Uint(), 10)
	case reflect.Float32:
		return strconv.FormatFloat(value.Float(), 'f', -1, 32)
	case reflect.Float64:
		f := value.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return quoteSQLString(fmt.Sprintf("%v", f))
		}
		return strconv.FormatFloat(f, 'f', -1, 64)
	case reflect.String:
		return quoteSQLString(value.String())
	case reflect.Bool:
		if value.Bool() {
			return "1"
		}
		return "0"
	default:
		return quoteSQLString(fmt.Sprintf("%v", arg))
	}
}

func quoteSQLString(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}
