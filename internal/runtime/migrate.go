// Schema migrator — see docs/migrations.md for the full spec.
//
// The migrator runs on every server boot (and every `mar dev`
// hot-reload), takes the list of registered entities, and brings the
// live SQLite schema in line with what the code declares. Forward-only,
// safe-by-default, blocks unsafe changes with copy-pasteable manual
// SQL hints.
//
// Three rough phases per Run():
//
//  1. Lock the database for the duration. SQLite serializes writers
//     anyway via BEGIN IMMEDIATE; we layer on top with a small retry
//     loop so concurrent boots (zero-downtime deploys) don't fail
//     loudly when the lock is briefly held.
//  2. Ensure the audit table (_mar_schema_migrations) exists.
//  3. For each entity: read the live schema with PRAGMA table_info,
//     diff against the declaration, apply safe changes, block unsafe
//     ones with helpful errors. Every applied statement gets recorded
//     in the audit table.
//
// The same diff machinery powers `mar migrate plan` (read-only) and
// the boot-time apply path. Plan walks the diff and returns
// MigrationStep values; Run iterates the steps and executes.

package runtime

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// stderr is the destination for migrator log lines. Tests override
// via SetMigrateStderr to capture output without leaking to the
// real stderr.
var migrateStderr io.Writer = os.Stderr

func stderr() io.Writer { return migrateStderr }

// SetMigrateStderr redirects migrator log lines to the given writer.
// Test-only helper. Pass nil to restore os.Stderr.
func SetMigrateStderr(w io.Writer) {
	if w == nil {
		migrateStderr = os.Stderr
		return
	}
	migrateStderr = w
}

// MigrationStepKind classifies what a planned migration would do.
// Used both for execution dispatch and for human-readable output.
type MigrationStepKind int

const (
	StepCreateTable MigrationStepKind = iota
	StepAddColumn
	StepRecreateEmpty // drop+recreate when nullability changed and table is empty
	StepNoteOrphanTable
	StepBlocked // unsafe — has a non-nil Error
)

// MigrationStep is one item in the diff plan.
type MigrationStep struct {
	Kind MigrationStepKind

	// Table is always set; identifies which entity table this affects.
	Table string

	// Column is set for AddColumn steps; identifies the new column.
	Column string

	// SQL is the statement that would (or did) execute. Empty for
	// blocked or note-only steps.
	SQL string

	// Description is a one-line human summary used in plan/status output
	// and in the audit table's `migration_kind` column. Stable —
	// downstream code grepping the audit log can rely on the format.
	Description string

	// Error is set when Kind == StepBlocked. The error message is the
	// long-form, copy-pasteable hint.
	Error error
}

// migrationKind returns the audit-table key for the step. Stable
// across releases so log greps remain valid.
func (s MigrationStep) migrationKind() string {
	switch s.Kind {
	case StepCreateTable:
		return "create_table"
	case StepAddColumn:
		return "add_column_" + s.Column
	case StepRecreateEmpty:
		return "recreate_empty_table"
	case StepNoteOrphanTable:
		return "orphan_table"
	}
	return "unknown"
}

// Migrator applies pending schema migrations to a SQLite database.
//
// Constructed with a connection + the entities to migrate; Run() is
// the apply path, Plan() is the read-only diff. Both are idempotent.
type Migrator struct {
	db       *sql.DB
	entities []VEntity
}

// NewMigrator builds a migrator over the given entities. Order matters
// only for deterministic output — actual execution is independent
// per-table.
func NewMigrator(db *sql.DB, entities []VEntity) *Migrator {
	return &Migrator{db: db, entities: entities}
}

// RunBootMigrations is the top-level entry point invoked by `mar dev`
// after `main` has run (registering all entities) and before the
// HTTP listener accepts traffic. Plus on each hot-reload.
//
// Returns silently when:
//   - no DB is configured (dbPath empty) — the project doesn't use Repo;
//   - no entities are registered — same situation, just from the
//     other side of the lookup;
//   - everything is up-to-date (no changes to apply).
//
// On changes, prints a one-line summary plus per-step lines for
// applied migrations and per-orphan-table notes. On block, returns
// the formatted error so the caller can `log.Fatalf` (boot path) or
// surface in the dev banner (hot-reload path).
func RunBootMigrations() error {
	entities := RegisteredEntities()
	if len(entities) == 0 {
		return nil
	}
	if currentDBPath() == "" {
		// No DB configured but entities exist — that's a user bug.
		// Surface it loud rather than letting Repo.* fail later
		// with the cryptic "no database configured" message.
		return fmt.Errorf(
			"%d entity declaration(s) found but no database configured (set `database.path` in mar.json)",
			len(entities))
	}
	db, err := getDB()
	if err != nil {
		return err
	}
	m := NewMigrator(db, entities)
	summary, err := m.Run()
	if err != nil {
		return err
	}
	if summary.HasChanges() || len(summary.Notes) > 0 {
		printRunSummary(summary)
	}
	return nil
}

// printRunSummary writes the boot-time migration log lines. Format is
// stable so tooling can grep:
//
//	[migrate] applied N change(s) in <duration>
//	[migrate] +column notes.archived (TEXT)
//	[migrate] table comments has no entity declaring it; ...
func printRunSummary(s RunSummary) {
	if len(s.Applied) > 0 {
		fmt.Fprintf(stderr(), "[migrate] applied %d change(s) in %s\n",
			len(s.Applied), s.Elapsed.Round(time.Millisecond))
		for _, step := range s.Applied {
			fmt.Fprintf(stderr(), "[migrate]   %s\n", step.Description)
		}
	}
	for _, note := range s.Notes {
		fmt.Fprintf(stderr(), "[migrate] %s\n", note)
	}
}

// Run applies every pending migration. Returns a summary of applied
// steps so the caller can format the boot-time log line.
//
// Wraps the diff loop in a SQLite-busy retry: 3 attempts at 100ms /
// 500ms / 2s. The retry only triggers on the initial lock attempt —
// once we're past the audit-table bootstrap, individual ALTER
// statements that hit BUSY would already be unusual and surface
// directly as errors.
func (m *Migrator) Run() (RunSummary, error) {
	if err := m.acquireWithRetry(); err != nil {
		return RunSummary{}, err
	}

	if err := m.ensureMigrationMetaTable(); err != nil {
		return RunSummary{}, fmt.Errorf("create _mar_schema_migrations: %w", err)
	}

	plan, err := m.planLocked()
	if err != nil {
		return RunSummary{}, err
	}

	start := time.Now()
	var applied []MigrationStep
	var notes []string
	for _, step := range plan {
		switch step.Kind {
		case StepBlocked:
			return RunSummary{}, step.Error
		case StepNoteOrphanTable:
			notes = append(notes, step.Description)
		default:
			if _, err := m.db.Exec(step.SQL); err != nil {
				return RunSummary{}, fmt.Errorf("migration %s on %s failed: %w",
					step.migrationKind(), step.Table, err)
			}
			if err := m.recordMigration(step); err != nil {
				return RunSummary{}, fmt.Errorf("audit migration %s on %s: %w",
					step.migrationKind(), step.Table, err)
			}
			applied = append(applied, step)
		}
	}
	return RunSummary{
		Applied: applied,
		Notes:   notes,
		Elapsed: time.Since(start),
	}, nil
}

// Plan computes the full diff without applying it. Returns even
// blocked steps so callers (mar migrate plan) can show the user what
// WOULD fail. The plan is stable: re-running Plan against an
// unchanged DB produces the same result.
func (m *Migrator) Plan() ([]MigrationStep, error) {
	if err := m.acquireWithRetry(); err != nil {
		return nil, err
	}
	if err := m.ensureMigrationMetaTable(); err != nil {
		return nil, fmt.Errorf("create _mar_schema_migrations: %w", err)
	}
	return m.planLocked()
}

// RunSummary is the result of an apply operation. Used by the boot
// path to format `[migrate] applied N changes in <duration>`.
type RunSummary struct {
	Applied []MigrationStep
	Notes   []string // orphan-table warnings, etc.
	Elapsed time.Duration
}

// HasChanges reports whether the run actually applied anything. The
// boot path uses this to silence the "no changes" log line on
// hot-reload.
func (s RunSummary) HasChanges() bool {
	return len(s.Applied) > 0
}

// ---------- Locking + retry ----------

// acquireWithRetry pings the DB up to three times, waiting on busy.
// Cheap call (SELECT 1); the real lock is taken inside each statement
// that mutates schema. The point here is to fail fast with a useful
// error if the file is unreadable, vs. fail loudly later.
//
// SQLite's busy backoff happens inside individual statements anyway;
// this layer only adds the human-friendly retry log lines so the
// operator sees `[migrate] database is locked; retrying (1/3)`
// instead of a bare error.
func (m *Migrator) acquireWithRetry() error {
	const attempts = 3
	delays := []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}
	dbPath := currentDBPath()
	for i := 0; i < attempts; i++ {
		err := m.db.Ping()
		if err == nil {
			// Probe a write lock with a no-op transaction. If
			// another process is holding the writer lock, we'll
			// see it here and can retry instead of failing
			// inside the audit-table bootstrap.
			tx, err := m.db.Begin()
			if err == nil {
				_ = tx.Rollback()
				return nil
			}
			if !isSqliteBusy(err) {
				return err
			}
		} else if !isSqliteBusy(err) {
			return err
		}
		fmt.Fprintf(stderr(), "[migrate] database %s is locked; retrying (%d/%d)\n",
			displayPath(dbPath), i+1, attempts)
		time.Sleep(delays[i])
	}
	return errors.New(formatLockedFinal(dbPath))
}

// formatLockedFinal builds the multi-line "FATAL: locked after retries"
// message documented in docs/migrations.md.
func formatLockedFinal(dbPath string) string {
	return fmt.Sprintf(`[migrate] FATAL: %s locked after 3 retries (~6500ms total)

  Most likely causes:
    - A sqlite3 CLI session is holding the lock (check open terminals)
    - A backup tool or external script is reading the file
    - Filesystem issue (NFS, disk full, sync tool)
    - Another mar instance has a slow-running migration (uncommon
      for ALTER COLUMN; possible for CREATE INDEX on large tables)

  Investigate with: lsof %s`, displayPath(dbPath), displayPath(dbPath))
}

func isSqliteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// modernc/sqlite returns "SQLITE_BUSY: database is locked (5)" or
	// similar; match by substring.
	return strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "database is locked")
}

// ---------- Audit ----------

func (m *Migrator) ensureMigrationMetaTable() error {
	_, err := m.db.Exec(`
		CREATE TABLE IF NOT EXISTS _mar_schema_migrations (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			table_name      TEXT    NOT NULL,
			migration_kind  TEXT    NOT NULL,
			sql_text        TEXT    NOT NULL,
			applied_at      INTEGER NOT NULL
		)
	`)
	return err
}

func (m *Migrator) recordMigration(step MigrationStep) error {
	_, err := m.db.Exec(
		`INSERT INTO _mar_schema_migrations (table_name, migration_kind, sql_text, applied_at) VALUES (?, ?, ?, ?)`,
		step.Table, step.migrationKind(), step.SQL, time.Now().UnixMilli(),
	)
	return err
}

// ---------- Diff ----------

// planLocked builds the full diff. Caller must already hold the
// migrator lock (acquireWithRetry). Read-only.
func (m *Migrator) planLocked() ([]MigrationStep, error) {
	var plan []MigrationStep

	// Track which tables we know about so we can detect orphan
	// tables (in DB but no entity) afterward.
	declared := map[string]bool{}

	for _, ent := range m.entities {
		declared[ent.Table] = true
		steps, err := m.planEntity(ent)
		if err != nil {
			return nil, err
		}
		plan = append(plan, steps...)
	}

	// Orphan-table warnings: tables in the live DB that no entity
	// declares. We deliberately ignore framework tables (_mar_*) since
	// they're managed elsewhere.
	live, err := m.liveTableNames()
	if err != nil {
		return nil, err
	}
	for _, t := range live {
		if strings.HasPrefix(t, "_mar_") || strings.HasPrefix(t, "sqlite_") {
			continue
		}
		if declared[t] {
			continue
		}
		plan = append(plan, MigrationStep{
			Kind:        StepNoteOrphanTable,
			Table:       t,
			Description: fmt.Sprintf(`table %q has no entity declaring it; keeping data intact. If no longer needed, drop with: DROP TABLE %s;`, t, t),
		})
	}
	return plan, nil
}

// planEntity computes the steps needed to align one entity's table
// with its declaration.
func (m *Migrator) planEntity(ent VEntity) ([]MigrationStep, error) {
	exists, err := m.tableExists(ent.Table)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []MigrationStep{{
			Kind:        StepCreateTable,
			Table:       ent.Table,
			SQL:         buildCreateTableSQLNew(ent),
			Description: fmt.Sprintf("create table %s", ent.Table),
		}}, nil
	}

	// Existing table — read live schema and diff column-by-column.
	live, err := m.readTableInfo(ent.Table)
	if err != nil {
		return nil, err
	}
	liveByName := map[string]tableInfoRow{}
	for _, r := range live {
		liveByName[r.Name] = r
	}

	var steps []MigrationStep
	hasRows, _ := m.tableHasRows(ent.Table)

	for _, f := range ent.Fields {
		row, found := liveByName[f.Name]
		if !found {
			step, err := planAddColumn(ent, f, hasRows)
			if err != nil {
				return nil, err
			}
			steps = append(steps, step)
			continue
		}
		// Column present — assert compatible.
		if blocked := assertCompatibleColumn(ent, f, row, hasRows); blocked != nil {
			if blocked.Kind == StepRecreateEmpty {
				steps = append(steps, *blocked)
				continue
			}
			steps = append(steps, *blocked)
			return steps, nil // stop on first hard block — clear error path
		}
	}
	return steps, nil
}

// planAddColumn decides whether adding a missing column is safe.
//
// SAFE cases:
//   - Column is optional (nullable) → ALTER TABLE ADD COLUMN.
//   - Column is NOT NULL but table is empty → same; SQLite accepts
//     NOT NULL on empty tables.
//
// BLOCKED cases:
//   - NOT NULL on a non-empty table without a default → user must
//     either make it optional or (when Entity.default lands in v1)
//     attach a default. The error message points at both fixes.
//   - Serial column (auto-incrementing PK) — SQLite doesn't support
//     adding AUTOINCREMENT via ALTER TABLE.
func planAddColumn(ent VEntity, f EntityField, hasRows bool) (MigrationStep, error) {
	if f.Serial {
		return MigrationStep{
			Kind:        StepBlocked,
			Table:       ent.Table,
			Column:      f.Name,
			Description: fmt.Sprintf("blocked: cannot add auto-increment primary key to existing table %s", ent.Table),
			Error: fmt.Errorf(`migration blocked for entity %s: cannot add auto-increment primary key %q to existing table %s.

SQLite doesn't allow adding AUTOINCREMENT via ALTER TABLE. To migrate manually:

    BEGIN TRANSACTION;
    ALTER TABLE %s RENAME TO %s_old;
    -- Recreate %s with the new schema (let the app boot create it).
    -- Then copy the data back, omitting %s so the new id is auto-generated:
    INSERT INTO %s (<other columns>) SELECT <other columns> FROM %s_old;
    DROP TABLE %s_old;
    COMMIT;`,
				ent.Table, f.Name, ent.Table,
				ent.Table, ent.Table, ent.Table, f.Name,
				ent.Table, ent.Table, ent.Table),
		}, nil
	}

	if f.NotNull && hasRows {
		return MigrationStep{
			Kind:        StepBlocked,
			Table:       ent.Table,
			Column:      f.Name,
			Description: fmt.Sprintf("blocked: cannot add NOT NULL column %q to non-empty table %s", f.Name, ent.Table),
			Error: fmt.Errorf(`migration blocked for entity %s: cannot add required column %q (%s) to non-empty table %s.

Options:
  - Make the column optional in the entity declaration:
      %s : ... -- without Entity.notNull
  - Backfill the data manually first:
      ALTER TABLE %s ADD COLUMN %s %s;
      UPDATE %s SET %s = <value>;
      -- then on next boot the migrator will assert NOT NULL and re-add the constraint.

Note: a future Entity.default builtin will let you skip this manual step
by attaching a default value the migrator can use to backfill.`,
				ent.Table, f.Name, f.SQLType, ent.Table,
				f.Name, ent.Table, f.Name, sqlTypeForDDL(f.SQLType),
				ent.Table, f.Name),
		}, nil
	}

	return MigrationStep{
		Kind:        StepAddColumn,
		Table:       ent.Table,
		Column:      f.Name,
		SQL:         fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", ent.Table, columnDefSQL(f)),
		Description: fmt.Sprintf("add column %s.%s (%s)", ent.Table, f.Name, f.SQLType),
	}, nil
}

// assertCompatibleColumn checks that a live column matches the
// declared field. Returns a Blocked step on incompatibility, or nil
// when the column is fine.
//
// The Empty + Nullability-Change special case returns a
// StepRecreateEmpty: the lispy version handled this by dropping the
// (data-less) table and rebuilding from scratch, since no rows are
// at risk. We mirror that.
func assertCompatibleColumn(ent VEntity, f EntityField, live tableInfoRow, hasRows bool) *MigrationStep {
	expectedType := strings.ToUpper(sqlTypeForDDL(f.SQLType))
	liveType := strings.ToUpper(strings.TrimSpace(live.Type))
	if liveType != "" && expectedType != "" && liveType != expectedType {
		return &MigrationStep{
			Kind:        StepBlocked,
			Table:       ent.Table,
			Column:      f.Name,
			Description: fmt.Sprintf("blocked: type changed for %s.%s", ent.Table, f.Name),
			Error: fmt.Errorf(`migration blocked for entity %s.%s: column type changed from %s to %s in table %s.

SQLite cannot change column types in place. To migrate manually:

    BEGIN TRANSACTION;
    ALTER TABLE %s RENAME TO %s_old;
    -- Boot the app to create %s with the new schema.
    -- Then copy + cast the data back:
    INSERT INTO %s (<columns>) SELECT <columns, casting %s as needed> FROM %s_old;
    DROP TABLE %s_old;
    COMMIT;`,
				ent.Table, f.Name, liveType, expectedType, ent.Table,
				ent.Table, ent.Table, ent.Table,
				ent.Table, f.Name, ent.Table, ent.Table),
		}
	}

	livePrimary := live.PK > 0
	if livePrimary != f.Serial {
		return &MigrationStep{
			Kind:        StepBlocked,
			Table:       ent.Table,
			Column:      f.Name,
			Description: fmt.Sprintf("blocked: primary-key shape changed for %s.%s", ent.Table, f.Name),
			Error: fmt.Errorf(`migration blocked for entity %s.%s: primary-key shape changed in table %s. Mar does not auto-migrate primary-key changes; rename + rebuild the table manually.`,
				ent.Table, f.Name, ent.Table),
		}
	}

	if !f.Serial {
		liveNotNull := live.NotNull == 1
		expectedNotNull := f.NotNull
		if liveNotNull != expectedNotNull {
			if !hasRows {
				// Empty table: drop+recreate is safe and zero-data-loss.
				return &MigrationStep{
					Kind:        StepRecreateEmpty,
					Table:       ent.Table,
					SQL:         buildRecreateEmptySQL(ent),
					Description: fmt.Sprintf("recreate empty table %s (nullability change)", ent.Table),
				}
			}
			return &MigrationStep{
				Kind:        StepBlocked,
				Table:       ent.Table,
				Column:      f.Name,
				Description: fmt.Sprintf("blocked: nullability changed for %s.%s", ent.Table, f.Name),
				Error: fmt.Errorf(`migration blocked for entity %s.%s: nullability changed from %s to %s in table %s.

SQLite cannot change NOT NULL constraints in place when data exists. To migrate manually:

    BEGIN TRANSACTION;
    ALTER TABLE %s RENAME TO %s_old;
    -- Boot the app to create %s with the new schema.
    INSERT INTO %s (<columns>) SELECT <columns> FROM %s_old;
    DROP TABLE %s_old;
    COMMIT;`,
					ent.Table, f.Name, nullabilityLabel(liveNotNull), nullabilityLabel(expectedNotNull), ent.Table,
					ent.Table, ent.Table, ent.Table, ent.Table, ent.Table, ent.Table),
			}
		}
	}
	return nil
}

func nullabilityLabel(notNull bool) string {
	if notNull {
		return "required"
	}
	return "optional"
}

// ---------- Helpers ----------

type tableInfoRow struct {
	Name    string
	Type    string
	NotNull int
	PK      int
}

func (m *Migrator) tableExists(name string) (bool, error) {
	var found string
	err := m.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=? LIMIT 1`,
		name,
	).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (m *Migrator) tableHasRows(name string) (bool, error) {
	var x int
	err := m.db.QueryRow("SELECT 1 FROM " + quoteIdent(name) + " LIMIT 1").Scan(&x)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (m *Migrator) readTableInfo(name string) ([]tableInfoRow, error) {
	rows, err := m.db.Query("PRAGMA table_info(" + quoteIdent(name) + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tableInfoRow
	for rows.Next() {
		var (
			cid       int
			colName   string
			colType   string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		out = append(out, tableInfoRow{
			Name:    colName,
			Type:    colType,
			NotNull: notNull,
			PK:      pk,
		})
	}
	return out, rows.Err()
}

func (m *Migrator) liveTableNames() ([]string, error) {
	rows, err := m.db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// columnDefSQL builds the per-column DDL fragment used inside CREATE
// TABLE and ALTER TABLE ADD COLUMN. Mirrors the existing
// buildCreateTableSQL formatting in entity.go but kept separate so
// the migrator can produce ALTER fragments without rebuilding the
// whole table SQL.
func columnDefSQL(f EntityField) string {
	parts := []string{f.Name, sqlTypeForDDL(f.SQLType)}
	if f.Serial {
		parts = append(parts, "PRIMARY KEY AUTOINCREMENT")
	} else if f.NotNull {
		parts = append(parts, "NOT NULL")
	}
	if f.AcceptedCtors != nil {
		quoted := make([]string, len(f.AcceptedCtors))
		for i, t := range f.AcceptedCtors {
			quoted[i] = "'" + strings.ReplaceAll(t, "'", "''") + "'"
		}
		parts = append(parts, "CHECK("+f.Name+" IN ("+strings.Join(quoted, ", ")+"))")
	}
	return strings.Join(parts, " ")
}

// buildCreateTableSQLNew emits a non-IF-NOT-EXISTS CREATE TABLE for
// the migrator. The IF-NOT-EXISTS variant exists in entity.go for
// backward compat with the legacy lazy ensureMigrated path; the
// migrator only emits CREATE for tables it has confirmed don't
// exist.
func buildCreateTableSQLNew(e VEntity) string {
	cols := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		cols[i] = columnDefSQL(f)
	}
	return "CREATE TABLE " + quoteIdent(e.Table) + " (" + strings.Join(cols, ", ") + ")"
}

// buildRecreateEmptySQL drops the (empty) table and recreates it
// with the current declared schema. Wrapped in a transaction so a
// failure mid-recreate doesn't leave the DB without the table.
func buildRecreateEmptySQL(e VEntity) string {
	cols := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		cols[i] = columnDefSQL(f)
	}
	return "BEGIN; DROP TABLE " + quoteIdent(e.Table) + "; CREATE TABLE " +
		quoteIdent(e.Table) + " (" + strings.Join(cols, ", ") + "); COMMIT"
}

// sqlTypeForDDL returns the storage type to write into the CREATE /
// ALTER statement. TIMESTAMP is conceptual — stored as INTEGER (Unix
// ms) so SQLite can compare/sort numerically.
func sqlTypeForDDL(marType string) string {
	if strings.EqualFold(marType, "TIMESTAMP") {
		return "INTEGER"
	}
	return marType
}

func quoteIdent(name string) string {
	// SQLite identifier quoting: double-quote with internal "" escape.
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
