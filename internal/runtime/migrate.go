// Schema migrator: see docs/migrations.md for the full spec.
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
	"os"
	"strings"
	"time"

	"mar/internal/clio"
)

// BlockedMigrationError is the structured error type the migrator
// returns for unsafe schema changes. It splits the message into a
// one-line `Summary` and a multi-line `Hint` so the CLI can render
// them as the standard `Error:` (red) + `Hint:` (yellow) blocks
// (see cmd/mar/color.go: fprintError / fprintHint).
//
// The Error() method falls back to the bundled `Summary\n\nHint`
// shape so callers that just stringify the error still get a
// readable message: useful for tests, logging, and the SSE
// compile-error channel.
type BlockedMigrationError struct {
	Summary string // one-line headline (e.g. "migration blocked for entity tasks: ...")
	Hint    string // multi-line body: root cause + remediation SQL + closing note
}

func (e *BlockedMigrationError) Error() string {
	if e.Hint == "" {
		return e.Summary
	}
	return e.Summary + "\n\n" + e.Hint
}

// MigrationStepKind classifies what a planned migration would do.
// Used both for execution dispatch and for human-readable output.
type MigrationStepKind int

const (
	StepCreateTable MigrationStepKind = iota
	StepAddColumn
	StepRecreateEmpty // drop+recreate when nullability changed and table is empty
	StepNoteOrphanTable
	StepBlocked // unsafe, has a non-nil Error
	// StepCreateUniqueIndex: a `CREATE UNIQUE INDEX IF NOT EXISTS`
	// for a unique constraint declared via `Entity.unique`. Emitted
	// after the table itself is present (so column references in the
	// index resolve). Idempotent: re-running the migrator against a
	// DB that already has the index is a no-op.
	StepCreateUniqueIndex
	// StepRestateColumn: the stored numbers stay where they are and are
	// multiplied so they keep meaning the same amount, after a change the
	// storage type cannot show — today, a Decimal scale. See
	// migrate_declared.go.
	StepRestateColumn
)

// MigrationStep is one item in the diff plan.
type MigrationStep struct {
	Kind MigrationStepKind

	// Table is always set; identifies which entity table this affects.
	Table string

	// Column is set for AddColumn steps; identifies the new column.
	Column string

	// SQL is the statement that would (or did) execute. Empty for
	// blocked or note-only steps, and empty when a step carries SQLs
	// instead.
	SQL string

	// SQLs is a step that takes more than one statement, run as one
	// transaction. It exists because the obvious spelling does not work:
	// handing "BEGIN; ...; COMMIT" to a single db.Exec does NOT roll back
	// on the driver we ship — a rebuild whose CREATE failed left the DROP
	// committed and the table simply gone, with later writes going through
	// as if nothing had happened. A migration is the one place where half
	// of something is worse than none of it.
	SQLs []string

	// Description is a one-line human summary used in plan/status output
	// and in the audit table's `migration_kind` column. Stable:
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
	case StepCreateUniqueIndex:
		// Column carries the underscore-joined column list (set by
		// planUniqueIndexes); using it as the audit suffix means
		// audit log greps can distinguish indexes on the same table.
		return "create_unique_index_" + s.Column
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
// only for deterministic output: actual execution is independent
// per-table.
func NewMigrator(db *sql.DB, entities []VEntity) *Migrator {
	return &Migrator{db: db, entities: ensureAuthIdentityUnique(entities)}
}

// ensureAuthIdentityUnique makes the login identity column (e.g. `email`)
// on the Auth entity UNIQUE at the database level, even if the app didn't
// declare `Entity.unique [...]` on it. Two concurrent request-code calls
// for a new email would otherwise create two user rows (EnsureUser does a
// non-atomic lookup-then-insert). The unique index closes that race in the
// DB: the second insert fails instead of duplicating.
// See docs/security-audit-2026-07-15.md #11.
func ensureAuthIdentityUnique(entities []VEntity) []VEntity {
	a := CurrentAuth()
	if a == nil {
		return entities
	}
	col, err := identifyColumn(*a)
	if err != nil {
		return entities // can't determine the column; leave the schema as-is
	}
	for i := range entities {
		if entities[i].Table != a.Entity.Table {
			continue
		}
		for _, idx := range entities[i].UniqueIndexes {
			if len(idx) == 1 && idx[0] == col {
				return entities // already covered by an explicit Entity.unique
			}
		}
		entities[i].UniqueIndexes = append(entities[i].UniqueIndexes, []string{col})
		return entities
	}
	return entities
}

// RunBootMigrations is the top-level entry point invoked by `mar dev`
// after `main` has run (registering all entities) and before the
// HTTP listener accepts traffic. Plus on each hot-reload.
//
// Returns silently when:
//   - no DB is configured (dbPath empty): the project doesn't use Repo;
//   - no entities are registered: same situation, just from the
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
		// No DB configured but entities exist: that's a user bug.
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
	// Tell the request path it has nothing left to do. Without this the boot
	// pass migrated every entity and then the first request to touch each
	// table planned the whole diff again — the cache the comment on
	// ensureMigrated described was never actually filled by anyone.
	for _, e := range entities {
		markMigrated(e.Table)
	}
	if summary.HasChanges() || len(summary.Notes) > 0 {
		printRunSummary(summary)
	}
	return nil
}

// printRunSummary writes the boot-time migration log.
//
// Two shapes, same as the dev banner (internal/jsserve/banner.go):
//
//   - Non-TTY (CI / piped logs): the grep-stable `[migrate] ...` lines,
//     no colors or blanks, so tooling can match them.
//
//   - TTY (interactive `mar dev`): the house style from
//     docs/cli-style.md §1/§3: a leading blank to separate from prior
//     output, a bold "Migrations" header with a dim summary, and dim,
//     indented detail lines.
func printRunSummary(s RunSummary) {
	if !migrateStderrTTY() {
		if len(s.Applied) > 0 {
			fmt.Fprintf(os.Stderr, "[migrate] applied %d change(s) in %s\n",
				len(s.Applied), s.Elapsed.Round(time.Millisecond))
			for _, step := range s.Applied {
				fmt.Fprintf(os.Stderr, "[migrate]   %s\n", step.Description)
			}
		}
		for _, note := range s.Notes {
			fmt.Fprintf(os.Stderr, "[migrate] %s\n", note)
		}
		return
	}

	dim := migrateWrapANSI("\x1b[38;5;245m")
	bold := migrateWrapANSI("\x1b[1m")

	// Leading blank: separates the block from preceding boot output,
	// skipped if the previous block already ended with one.
	if clio.WantLeadingBlank() {
		fmt.Fprintln(os.Stderr)
	}
	if len(s.Applied) > 0 {
		fmt.Fprintf(os.Stderr, "  %s  %s\n",
			bold("Migrations"),
			dim(fmt.Sprintf("applied %s in %s",
				pluralizeChanges(len(s.Applied)),
				s.Elapsed.Round(time.Millisecond))))
		for _, step := range s.Applied {
			fmt.Fprintf(os.Stderr, "    %s %s\n", dim("•"), step.Description)
		}
	}
	for _, note := range s.Notes {
		fmt.Fprintf(os.Stderr, "  %s\n", dim(note))
	}
	// Tight ending: let the next block (the dev banner) emit its own
	// leading blank rather than us trailing one here.
	clio.ClearTrailingBlank()
}

// pluralizeChanges renders "1 change" / "N changes".
func pluralizeChanges(n int) string {
	if n == 1 {
		return "1 change"
	}
	return fmt.Sprintf("%d changes", n)
}

// migrateStderrTTY reports whether stderr is an interactive terminal
// (so colors are safe). Honors NO_COLOR. Mirrors the stdout check in
// internal/jsserve/banner.go.
func migrateStderrTTY() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stderr.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

// migrateWrapANSI wraps a string in the given SGR prefix + reset.
func migrateWrapANSI(prefix string) func(string) string {
	return func(s string) string { return prefix + s + "\x1b[0m" }
}

// Run applies every pending migration. Returns a summary of applied
// steps so the caller can format the boot-time log line.
//
// Wraps the diff loop in a SQLite-busy retry: 3 attempts at 100ms /
// 500ms / 2s. The retry only triggers on the initial lock attempt:
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
			if err := m.applyStep(step); err != nil {
				return RunSummary{}, err
			}
			applied = append(applied, step)
		}
	}
	// Only once every step has applied. The catalog says what the numbers
	// in each column MEAN, so it has to describe the table as it now is —
	// writing it earlier would record a claim the data had not caught up
	// with. A blocked plan returns above and writes nothing.
	for _, ent := range m.entities {
		if err := m.rememberDeclaredTypes(ent); err != nil {
			return RunSummary{}, fmt.Errorf("record declared types for %s: %w", ent.Table, err)
		}
	}

	return RunSummary{
		Applied: applied,
		Notes:   notes,
		Elapsed: time.Since(start),
	}, nil
}

// applyStep runs one step and records it, as one transaction.
//
// The audit row goes inside the transaction with the DDL it describes, not
// after it: a crash between the two would leave the ledger claiming a
// migration that did not happen, and the next boot would trust it.
func (m *Migrator) applyStep(step MigrationStep) error {
	stmts := step.SQLs
	if len(stmts) == 0 {
		stmts = []string{step.SQL}
	}
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("migration %s on %s: begin: %w",
			step.migrationKind(), step.Table, err)
	}
	for _, stmt := range stmts {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := tx.Exec(stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s on %s failed: %w",
				step.migrationKind(), step.Table, err)
		}
	}
	if err := m.recordMigrationTx(tx, step); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("audit migration %s on %s: %w",
			step.migrationKind(), step.Table, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %s on %s: commit: %w",
			step.migrationKind(), step.Table, err)
	}
	return nil
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
		fmt.Fprintf(os.Stderr, "[migrate] database %s is locked; retrying (%d/%d)\n",
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
	// The declared-type catalog is part of the same bookkeeping: both
	// answer "what has already happened to this database".
	if err := m.ensureDeclaredTypeCatalog(); err != nil {
		return err
	}
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
		step.Table, step.migrationKind(), step.recordedSQL(), time.Now().UnixMilli(),
	)
	return err
}

// recordMigrationTx is the same row, written inside the transaction that
// carries the step's own statements. See applyStep.
func (m *Migrator) recordMigrationTx(tx *sql.Tx, step MigrationStep) error {
	_, err := tx.Exec(
		`INSERT INTO _mar_schema_migrations (table_name, migration_kind, sql_text, applied_at) VALUES (?, ?, ?, ?)`,
		step.Table, step.migrationKind(), step.recordedSQL(), time.Now().UnixMilli(),
	)
	return err
}

// recordedSQL is what the audit row shows: one statement as itself, several
// joined the way an operator would paste them back.
func (s MigrationStep) recordedSQL() string {
	if len(s.SQLs) > 0 {
		return strings.Join(s.SQLs, "; ")
	}
	return s.SQL
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
		steps := []MigrationStep{{
			Kind:        StepCreateTable,
			Table:       ent.Table,
			SQL:         buildCreateTableSQLNew(ent),
			Description: fmt.Sprintf("create table %s", ent.Table),
		}}
		// Indexes follow the CREATE TABLE so the column references
		// resolve. SQLite would error otherwise.
		steps = append(steps, planUniqueIndexes(ent)...)
		return steps, nil
	}

	// Existing table: read live schema and diff column-by-column.
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
			step, err := planAddColumn(ent, f, hasRows, liveByName)
			if err != nil {
				return nil, err
			}
			steps = append(steps, step)
			continue
		}
		// Column present: assert compatible.
		if blocked := assertCompatibleColumn(ent, f, row, hasRows, liveByName); blocked != nil {
			if blocked.Kind == StepRecreateEmpty {
				steps = append(steps, *blocked)
				continue
			}
			steps = append(steps, *blocked)
			return steps, nil // stop on first hard block: clear error path
		}
	}

	// The constructor set of an enum column, which the storage-type diff
	// cannot see: a TEXT column with any set is just TEXT. The old set is
	// recorded exactly in the CHECK the migrator itself wrote.
	enumSteps, err := m.planEnumSets(ent, hasRows)
	if err != nil {
		return nil, err
	}
	steps = append(steps, enumSteps...)

	// Columns the live table has and the declaration does not.
	//
	// planEntity walked the DECLARED fields and never looked at what was
	// left over, so removing a field from an entity emitted nothing at all.
	// That is fine when the leftover column can hold a row on its own, and
	// fatal when it cannot: Repo.create names only declared columns, so
	// SQLite has to supply a value for an undeclared NOT NULL column with
	// no default, and cannot. Every create then failed inside a request
	// with a constraint error naming a column the source no longer
	// mentions, on an app that booted clean.
	orphanSteps, err := m.planOrphanColumns(ent, live)
	if err != nil {
		return nil, err
	}
	steps = append(steps, orphanSteps...)

	// Changes the storage type ERASES: a Decimal scale, and Int against
	// Time. INTEGER covers all of them, so nothing in the database says
	// which one the numbers in a column are. See migrate_declared.go for
	// what that cost, measured.
	declaredSteps, err := m.planDeclaredTypeChanges(ent)
	if err != nil {
		return nil, err
	}
	steps = append(steps, declaredSteps...)

	// Unique indexes: emit unconditionally as IF NOT EXISTS. The
	// migrator doesn't track per-index drift: an index that already
	// matches is a no-op; an index that doesn't exist yet gets
	// created. A removed Entity.unique leaves the old index in the
	// DB (orphan): documented as a v1 limitation; we'd add an
	// "index drift" step later if it becomes a real problem.
	steps = append(steps, planUniqueIndexes(ent)...)
	return steps, nil
}

// planOrphanColumns decides what to do about columns the live table has and
// the entity no longer declares.
//
// Keeping them is the existing policy for orphan TABLES, and it is right for
// the same reason: real apps accumulate legacy fields, and the migrator does
// not delete data it was not asked to delete. So a leftover column that can
// hold a row on its own is a note.
//
// A leftover column that is NOT NULL with no default cannot hold a row on its
// own, and that is a different situation: the app is already broken, it just
// does not know yet. Blocking at boot is strictly better than what happens
// today, which is a listener that opens and then fails every write with a
// SQLite constraint error from inside a request.
//
// It is a block rather than an automatic DROP COLUMN because dropping a
// column deletes what it holds for every row, forever — the only
// irreversible thing the migrator could do. The hint copies first.
func (m *Migrator) planOrphanColumns(ent VEntity, live []tableInfoRow) ([]MigrationStep, error) {
	declared := map[string]bool{}
	for _, f := range ent.Fields {
		declared[f.Name] = true
	}
	var steps []MigrationStep
	for _, row := range live {
		if declared[row.Name] {
			continue
		}
		if row.NotNull == 0 || row.Default != nil {
			steps = append(steps, MigrationStep{
				Kind:  StepNoteOrphanTable,
				Table: ent.Table,
				Description: fmt.Sprintf(
					"column %s.%s has no field declaring it; keeping the data. If no longer needed, drop with: ALTER TABLE %s DROP COLUMN %s;",
					ent.Table, row.Name, quoteIdent(ent.Table), quoteIdent(row.Name)),
			})
			continue
		}
		indexes, err := m.indexesOnColumn(ent.Table, row.Name)
		if err != nil {
			return nil, err
		}
		steps = append(steps, MigrationStep{
			Kind:  StepBlocked,
			Table: ent.Table,
			Error: orphanRequiredColumnError(ent.Table, row.Name, indexes),
		})
		return steps, nil
	}
	return steps, nil
}

// orphanRequiredColumnError explains a column that no longer has a field and
// cannot be left alone. The script copies the data to a side table before it
// drops anything, so following it verbatim never loses the column's contents.
func orphanRequiredColumnError(table, column string, indexes []string) error {
	keep := table + "_" + column + "_kept"
	nl := "\n"
	var script strings.Builder
	for _, idx := range indexes {
		// DROP COLUMN refuses while an index still mentions the column.
		script.WriteString("    DROP INDEX " + quoteIdent(idx) + ";" + nl)
	}
	script.WriteString("    CREATE TABLE " + quoteIdent(keep) + " AS SELECT rowid, " +
		quoteIdent(column) + " FROM " + quoteIdent(table) + ";" + nl)
	script.WriteString("    ALTER TABLE " + quoteIdent(table) + " DROP COLUMN " + quoteIdent(column) + ";" + nl)
	return &BlockedMigrationError{
		Summary: fmt.Sprintf(
			"migration blocked for entity `%s`: the table still has a required column `%s` that no field declares",
			table, column),
		Hint: "Every `Repo.create` names only the declared columns, so the database has to " +
			"supply a value for `" + column + "` and it has none." + nl + nl +
			script.String() + nl +
			"Run that and restart. The first line keeps the old values in `" + keep +
			"`, which you can drop once the app is up and the data looks right.",
	}
}

// indexesOnColumn lists the indexes that mention a column, because DROP
// COLUMN refuses while one still does.
func (m *Migrator) indexesOnColumn(table, column string) ([]string, error) {
	rows, err := m.db.Query(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name NOT LIKE 'sqlite_%'`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var hits []string
	for _, name := range names {
		info, err := m.db.Query(`PRAGMA index_info(` + quoteIdent(name) + `)`)
		if err != nil {
			return nil, err
		}
		for info.Next() {
			var seqno, cid int
			var col any
			if err := info.Scan(&seqno, &cid, &col); err != nil {
				info.Close()
				return nil, err
			}
			if got, ok := col.(string); ok && got == column {
				hits = append(hits, name)
				break
			}
		}
		info.Close()
	}
	return hits, nil
}

// planUniqueIndexes turns the entity's UniqueIndexes slice into a
// sequence of StepCreateUniqueIndex steps. Empty if the entity has
// no unique constraints. Index names are stable across boots
// (<table>_uq_<col1>_<col2>...) so re-running the migrator emits the
// same SQL the existing index already covers.
func planUniqueIndexes(ent VEntity) []MigrationStep {
	if len(ent.UniqueIndexes) == 0 {
		return nil
	}
	steps := make([]MigrationStep, 0, len(ent.UniqueIndexes))
	for _, cols := range ent.UniqueIndexes {
		name := uniqueIndexName(ent.Table, cols)
		steps = append(steps, MigrationStep{
			Kind:        StepCreateUniqueIndex,
			Table:       ent.Table,
			Column:      strings.Join(cols, "_"), // used by migrationKind for audit suffix
			SQL:         buildCreateUniqueIndexSQL(ent.Table, name, cols),
			Description: fmt.Sprintf("create unique index %s on %s(%s)", name, ent.Table, strings.Join(cols, ", ")),
		})
	}
	return steps
}

// uniqueIndexName builds the deterministic name we use for a
// declared unique index. Format: `<table>_uq_<col1>_<col2>...`.
// Stable across boots so `CREATE UNIQUE INDEX IF NOT EXISTS` is
// idempotent: a re-run sees the same name and no-ops.
func uniqueIndexName(table string, cols []string) string {
	return table + "_uq_" + strings.Join(cols, "_")
}

// buildCreateUniqueIndexSQL emits the `CREATE UNIQUE INDEX IF NOT
// EXISTS` statement. Both the index name and the column references
// go through quoteIdent: column names are mar identifiers (alnum)
// but quoting costs nothing and survives any future relaxation of
// the allowed character set.
func buildCreateUniqueIndexSQL(table, name string, cols []string) string {
	quotedCols := make([]string, len(cols))
	for i, c := range cols {
		quotedCols[i] = quoteIdent(c)
	}
	return "CREATE UNIQUE INDEX IF NOT EXISTS " + quoteIdent(name) +
		" ON " + quoteIdent(table) + " (" + strings.Join(quotedCols, ", ") + ")"
}

// planAddColumn decides whether adding a missing column is safe.
//
// SAFE cases:
//   - Column is optional (nullable) → ALTER TABLE ADD COLUMN.
//   - Column is NOT NULL but table is empty → same; SQLite accepts
//     NOT NULL on empty tables.
//
// BLOCKED cases:
//   - NOT NULL on a non-empty table → SQLite would take it with a
//     constant DEFAULT, but what the existing rows should say is a
//     decision only the operator can make (entity columns carry no
//     default), so we stop and hand them the one-line ALTER to run.
//   - Serial column (auto-incrementing PK): SQLite doesn't support
//     adding AUTOINCREMENT via ALTER TABLE.
//
// liveByName is the live schema, used to tell which columns carry over
// in the rebuild and which ones are being added.
func planAddColumn(ent VEntity, f EntityField, hasRows bool, liveByName map[string]tableInfoRow) (MigrationStep, error) {
	if f.Serial {
		return MigrationStep{
			Kind:        StepBlocked,
			Table:       ent.Table,
			Column:      f.Name,
			Description: fmt.Sprintf("blocked: cannot add auto-increment primary key to existing table %s", ent.Table),
			Error: &BlockedMigrationError{
				// Backticks mark identifiers here for the same reason
				// they do in the hints below: the runtime doesn't know
				// about TTY state, so it marks the words and the CLI
				// decides the color. Anything that is not a table or a
				// column name stays unmarked, so cyan keeps meaning
				// "this is a name in your schema".
				Summary: fmt.Sprintf(
					"migration blocked for entity `%s`: cannot add auto-increment primary key `%s` to existing table `%s`.",
					ent.Table, f.Name, ent.Table),
				Hint: fmt.Sprintf("SQLite cannot add a primary key to an existing table, so `%s` has to be rebuilt.\n\n%s\n\n"+
					"Run it and restart. The copies get new ids.\n%s",
					ent.Table, buildRebuildSQL(ent, liveByName, nil), backupLine(ent)),
			},
		}, nil
	}

	if f.NotNull && hasRows {
		return MigrationStep{
			Kind:        StepBlocked,
			Table:       ent.Table,
			Column:      f.Name,
			Description: fmt.Sprintf("blocked: cannot add NOT NULL column %q to non-empty table %s", f.Name, ent.Table),
			Error: &BlockedMigrationError{
				Summary: fmt.Sprintf(
					"migration blocked for entity `%s`: cannot add required column `%s` (%s) to non-empty table `%s`.",
					ent.Table, f.Name, f.SQLType, ent.Table),
				// Backticks mark identifiers; the CLI colorizer turns
				// them cyan in the prose AND finds the same tokens
				// in the SQL block to highlight there too. Don't
				// rephrase without checking colorizeHint stays in
				// sync: bare `tasks` / `position` in either spot
				// is what makes the two halves visually match, and
				// `<value>` verbatim is what makes the placeholder
				// stand out yellow.
				Hint: fmt.Sprintf(`The rows already in `+"`"+`%s`+"`"+` need a value for `+"`"+`%s`+"`"+`, and entity columns carry no default.

%s

Fill in a constant and restart, or drop `+"`"+`Entity.notNull`+"`"+` to make the column optional.`,
					ent.Table, f.Name, buildAddColumnSQL(ent, liveByName)),
			},
		}, nil
	}

	return MigrationStep{
		Kind:        StepAddColumn,
		Table:       ent.Table,
		Column:      f.Name,
		SQL:         fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quoteIdent(ent.Table), columnDefSQL(f)),
		Description: fmt.Sprintf("add column %s.%s (%s)", ent.Table, f.Name, f.SQLType),
	}, nil
}

// buildAddColumnSQL writes the ALTER that unblocks a required column
// on a populated table: one line per column the entity declares and
// the table doesn't have yet.
//
// A DEFAULT is the whole trick. SQLite refuses `ADD COLUMN ... NOT
// NULL` on its own ("Cannot add a NOT NULL column with default value
// NULL") but accepts it with a constant default, which it records in
// the schema and hands back for the rows that predate the column. No
// table copy, no rewrite of existing rows: it is a schema-only change
// whatever the table's size. The default must be a literal, since
// SQLite also refuses a non-constant one.
//
// Every missing required column is listed, not just the one that
// tripped the plan, so following the hint once doesn't land on the
// same block for the next column.
//
// Identifiers go in unquoted. Safe, because entity and field names are
// Mar identifiers, and it lets the CLI colorizer match them against
// the backtick spans in the prose above. Indented 4 spaces so
// colorizeHint reads the block as code.
func buildAddColumnSQL(ent VEntity, liveByName map[string]tableInfoRow) string {
	var lines []string
	for _, f := range ent.Fields {
		if _, live := liveByName[f.Name]; live || f.Serial || !f.NotNull {
			continue
		}
		lines = append(lines, "    ALTER TABLE "+ent.Table+" ADD COLUMN "+
			columnDefSQL(f)+" DEFAULT <value>;")
	}
	return strings.Join(lines, "\n")
}

// buildRebuildSQL writes the rename-and-copy that a change SQLite
// cannot make in place needs: a new column type, a new primary key, a
// NOT NULL added or dropped.
//
// IT NEVER DROPS THE ORIGINAL. The old rows stay under `<table>_old`
// and the operator removes that table themselves once the app boots
// and the data looks right. This is not politeness, it is the whole
// point: `sqlite3` does NOT stop at the first failed statement unless
// you pass -bail, so a script that ends in DROP TABLE will run that
// DROP even after the copy failed, and take the data with it. The
// version this function emits cannot lose a row no matter which line
// fails.
//
// `expr` overrides the SELECT expression for a column (a CAST when the
// type changed, a COALESCE when a NULL has to become a value);
// anything not overridden is copied across by name. A serial column
// that does not exist yet is left out of the copy so SQLite assigns
// it; one that already exists is copied so the ids survive.
//
// Identifiers go in unquoted so the CLI colorizer can match them
// against the backtick spans in the prose. Indented 4 spaces to read
// as code, 8 for continuations.
func buildRebuildSQL(ent VEntity, liveByName map[string]tableInfoRow, expr map[string]string) string {
	defs := make([]string, 0, len(ent.Fields))
	names := make([]string, 0, len(ent.Fields))
	values := make([]string, 0, len(ent.Fields))
	for _, f := range ent.Fields {
		defs = append(defs, "        "+columnDefSQL(f))
		_, live := liveByName[f.Name]
		if f.Serial && !live {
			continue
		}
		names = append(names, f.Name)
		switch {
		case expr[f.Name] != "":
			values = append(values, expr[f.Name])
		case live:
			values = append(values, f.Name)
		default:
			values = append(values, "<value>")
		}
	}
	old := ent.Table + "_old"
	return "    ALTER TABLE " + ent.Table + " RENAME TO " + old + ";\n" +
		"    CREATE TABLE " + ent.Table + " (\n" +
		strings.Join(defs, ",\n") + "\n" +
		"    );\n" +
		"    INSERT INTO " + ent.Table + " (" + strings.Join(names, ", ") + ")\n" +
		"        SELECT " + strings.Join(values, ", ") + " FROM " + old + ";"
}

// assertCompatibleColumn checks that a live column matches the
// declared field. Returns a Blocked step on incompatibility, or nil
// when the column is fine.
//
// The Empty + Nullability-Change special case returns a
// StepRecreateEmpty: when the table has zero rows AND a nullability
// constraint flipped (add/remove NOT NULL), we drop and rebuild from
// scratch since no rows are at risk. The dev-time UX matters here:
// most projects edit entity fields before any production data
// exists, and a hard "incompatible change" error would feel hostile.
func assertCompatibleColumn(ent VEntity, f EntityField, live tableInfoRow, hasRows bool, liveByName map[string]tableInfoRow) *MigrationStep {
	expectedType := strings.ToUpper(sqlTypeForDDL(f.SQLType))
	liveType := strings.ToUpper(strings.TrimSpace(live.Type))
	if liveType != "" && expectedType != "" && liveType != expectedType {
		return &MigrationStep{
			Kind:        StepBlocked,
			Table:       ent.Table,
			Column:      f.Name,
			Description: fmt.Sprintf("blocked: type changed for %s.%s", ent.Table, f.Name),
			Error: &BlockedMigrationError{
				Summary: fmt.Sprintf(
					"migration blocked for entity `%s.%s`: column type changed from %s to %s in table `%s`.",
					ent.Table, f.Name, liveType, expectedType, ent.Table),
				Hint: fmt.Sprintf("SQLite cannot change a column's type in place, so `%s` has to be rebuilt.\n\n%s\n\n"+
					"Adjust the cast if you need to, run it and restart.\n%s",
					ent.Table,
					buildRebuildSQL(ent, liveByName, map[string]string{
						f.Name: "CAST(" + f.Name + " AS " + expectedType + ")",
					}),
					backupLine(ent)),
			},
		}
	}

	livePrimary := live.PK > 0
	if livePrimary != f.Serial {
		return &MigrationStep{
			Kind:        StepBlocked,
			Table:       ent.Table,
			Column:      f.Name,
			Description: fmt.Sprintf("blocked: primary-key shape changed for %s.%s", ent.Table, f.Name),
			Error: &BlockedMigrationError{
				Summary: fmt.Sprintf(
					"migration blocked for entity `%s.%s`: primary-key shape changed in table `%s`.",
					ent.Table, f.Name, ent.Table),
				Hint: "Mar does not auto-migrate primary-key changes; rename + rebuild the table manually.",
			},
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
					SQLs:        buildRecreateEmptySQL(ent),
					Description: fmt.Sprintf("recreate empty table %s (nullability change)", ent.Table),
				}
			}
			return &MigrationStep{
				Kind:        StepBlocked,
				Table:       ent.Table,
				Column:      f.Name,
				Description: fmt.Sprintf("blocked: nullability changed for %s.%s", ent.Table, f.Name),
				Error: &BlockedMigrationError{
					Summary: fmt.Sprintf(
						"migration blocked for entity `%s.%s`: nullability changed from %s to %s in table `%s`.",
						ent.Table, f.Name, nullabilityLabel(liveNotNull), nullabilityLabel(expectedNotNull), ent.Table),
					Hint: nullabilityHint(ent, f, expectedNotNull, liveByName),
				},
			}
		}
	}
	return nil
}

// nullabilityHint covers both directions of the same rebuild. Going
// required is the one that needs a human: a row already holding NULL
// has to become something, so the copy wraps the column in a COALESCE
// with a placeholder. Going optional just copies.
func nullabilityHint(ent VEntity, f EntityField, expectedNotNull bool, liveByName map[string]tableInfoRow) string {
	expr := map[string]string{}
	action := "Run it and restart."
	if expectedNotNull {
		expr[f.Name] = "COALESCE(" + f.Name + ", <value>)"
		action = "Fill in a constant for the rows holding NULL, run it and restart."
	}
	return fmt.Sprintf("SQLite cannot add or drop NOT NULL in place, so `%s` has to be rebuilt.\n\n%s\n\n%s\n%s",
		ent.Table, buildRebuildSQL(ent, liveByName, expr), action, backupLine(ent))
}

// backupLine is the sentence every rebuild hint ends on. It is the
// counterpart to buildRebuildSQL never dropping anything: the script
// leaves a copy behind, so the hint has to say where it is and that
// removing it is the operator's call.
func backupLine(ent VEntity) string {
	return fmt.Sprintf("`%s_old` keeps the original rows until you drop it.", ent.Table)
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

	// Default is the column's DEFAULT expression as SQLite reports it, or
	// nil when it has none. PRAGMA has always returned it and readTableInfo
	// has always scanned it into a discarded variable — it is what decides
	// whether an undeclared NOT NULL column merely sits there or breaks
	// every insert.
	Default any
}

func (m *Migrator) tableExists(name string) (bool, error) {
	var found string
	err := m.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=? LIMIT 1`,
		name,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
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
	if errors.Is(err, sql.ErrNoRows) {
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
			Default: dfltValue,
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
	parts := []string{quoteIdent(f.Name), sqlTypeForDDL(f.SQLType)}
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

// buildCreateTableSQLNew emits a plain `CREATE TABLE` (no IF NOT
// EXISTS) for the migrator. The migrator only emits CREATE for
// tables it has just confirmed don't exist, so the guard would be
// noise, and worse, would mask a real bug if drift were to make
// the table already exist. entity.go has an IF-NOT-EXISTS variant
// used elsewhere for one-shot static-table bootstrapping.
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
func buildRecreateEmptySQL(e VEntity) []string {
	cols := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		cols[i] = columnDefSQL(f)
	}
	// Two statements, run as one transaction by applyStep. This used to be
	// one string reading "BEGIN; DROP TABLE ...; CREATE TABLE ...; COMMIT"
	// handed to a single Exec, which does not roll back on the driver we
	// ship: a CREATE that failed left the DROP committed and the table gone.
	return []string{
		"DROP TABLE " + quoteIdent(e.Table),
		"CREATE TABLE " + quoteIdent(e.Table) + " (" + strings.Join(cols, ", ") + ")",
	}
}

// sqlTypeForDDL returns the storage type to write into the CREATE /
// ALTER statement. TIMESTAMP and DECIMAL are conceptual: both stored
// as INTEGER (Unix ms / scaled coefficient) so SQLite can compare and
// sort numerically.
func sqlTypeForDDL(marType string) string {
	if strings.EqualFold(marType, "TIMESTAMP") || strings.EqualFold(marType, "DECIMAL") {
		return "INTEGER"
	}
	return marType
}

func quoteIdent(name string) string {
	// SQLite identifier quoting: double-quote with internal "" escape.
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
