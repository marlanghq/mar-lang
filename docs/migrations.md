# Schema Migrations, Spec

**Status**: proposal, not implemented.
**Replaces**: today's lazy `ensureMigrated` in `internal/runtime/repo.go`,
which only runs `CREATE TABLE IF NOT EXISTS` and silently no-ops on
schema drift (a real production hazard).

This spec adapts the migration approach from the lispy version of mar
(circa commit `c83eff7`, "Better migration system") to the current
ML-flavored mar's `Entity.define` API.

---

## What's broken today

`internal/runtime/repo.go::ensureMigrated`:

```go
func ensureMigrated(entity VEntity) (*sql.DB, error) {
    if migrationCache.done[entity.Table] { return db, nil }
    db.Exec(buildCreateTableSQL(entity))   // CREATE TABLE IF NOT EXISTS ...
    migrationCache.done[entity.Table] = true
    return db, nil
}
```

This is naive in three dangerous ways:

1. **`CREATE TABLE IF NOT EXISTS` silently does nothing** when the
   table already exists, even if its schema doesn't match what the
   code expects. So `entity.define "notes" { ..., archived = Entity.bool ... }`
   on a table that already has all the previous fields but no
   `archived` column → user code reads `note.archived` → crash, or
   worse, default-zero values that the user code interprets as real.
2. **Lazy on first use**, not eager at boot. Schema bugs surface as
   runtime errors mid-request instead of failing fast at startup.
3. **No history table**. No way to ask "what changed in this DB?"
   for an audit trail or rollback investigation.

The lispy version solved all three. This spec brings that solution
forward.

---

## Goals

1. **Forward-only, automatic, safe-by-default**: every `mar dev` and
   every `mar build` deployment applies pending safe migrations on
   boot. No separate `mar migrate` step in the common case.
2. **Block unsafe changes with helpful errors** that include the
   suggested manual SQL, so the user has an obvious next step.
3. **Audit trail**: a `_mar_schema_migrations` history table records
   every applied change with timestamp + the SQL that ran.
4. **Idempotent**: running migrations on an already-migrated DB is
   a no-op (modulo the meta-table check).
5. **Drift-detection in dev**: when the DB has a column that the
   declared entity doesn't mention, warn but don't drop. Real apps
   accumulate legacy fields.
6. **Read-only inspection commands** for the operator: `mar migrate
   plan` shows what the next boot WILL apply, without touching the
   DB; `mar migrate status` shows the full history. These are
   inspection tools, not control levers, boot always runs the same
   single auto-mode.

Non-goals:
- Down migrations (rollback by replaying inverse). SQLite makes this
  very hard; mar takes the Rails-7 stance: forward-only + backups.
- Data migrations (transforming rows). Out of scope; user writes a
  one-off `Task` if needed. Schema only.
- Multi-tenant / multi-DB orchestration. Single SQLite per app for now.

---

## Mechanism

### Schema = Entities

Source of truth is the `VEntity` produced by `Entity.define`. Already
carries every fact the migrator needs:

- Table name
- Field name, type, primary, auto, optional/notNull
- Defaults (today partially supported, needs a small extension to
  carry the parsed default value, see "Required changes" below)
- Foreign keys (when relations land, the lispy version had
  `belongs-to`; current mar has it implicitly via `Entity.int` plus
  user convention; needs a first-class `Entity.belongsTo` builtin
  before relation migrations can be modeled)

### Run at boot, before any handler

A new `runtime.Migrator` runs once during server bootstrap, before
the HTTP listener accepts traffic:

```go
// In internal/jsserve/server.go, just before mux registration:
if err := runtime.Migrate(routes, signInUser); err != nil {
    log.Fatalf("[mar] migration failed: %v", err)
}
```

The migrator collects every `VEntity` that's referenced by any
`ExposedService` in the program (via the entity field captured by
`Auth.config` and any `Repo.*` callsite). Plus the framework's
internal tables: `_mar_auth_codes`, `_mar_auth_sessions`,
`_mar_schema_migrations` itself.

### Per-entity diff

For each entity `E`:

1. **Doesn't exist**: emit `CREATE TABLE` and record.
2. **Exists**: read live schema via `PRAGMA table_info(E.table)` and
   `PRAGMA foreign_key_list(E.table)`. Compare field-by-field:
   - Field declared, not in DB:
     - **Optional or has default**: emit `ALTER TABLE ADD COLUMN` and
       record.
     - **Required, no default, table empty**: emit `ALTER TABLE ADD COLUMN`
       (with `NOT NULL` is fine, SQLite accepts it on empty table).
     - **Required, no default, table has rows**: BLOCK with
       error message that suggests adding a default or making the
       field optional.
     - **Primary or auto**: BLOCK, these can't be retrofitted.
     - **Foreign key**: BLOCK with the manual-migration SQL pattern
       (CREATE TABLE _new + INSERT SELECT + DROP + RENAME). SQLite
       can't `ALTER TABLE ADD CONSTRAINT FOREIGN KEY`; you have to
       rebuild.
   - Field in DB, declared too: assert compatible:
     - Type matches (`INTEGER`/`TEXT`/`REAL` per `typeToSQLite`).
     - Primary-key shape matches.
     - Nullability matches **OR** table is empty (then drop+recreate).
     - Mismatch in any → BLOCK.
   - Field in DB, not declared: WARN to stderr (`extra column %q;
     keeping unchanged`). Don't drop.

### Internal tables (auth)

The lispy version had `migrateStaticTable` for `mar_auth_codes` /
`mar_auth_sessions`. Same idea but the current schema lives in
`internal/auth/schema.go` (already creates the tables). Refactor:
move the column declarations into `Migrator` so they participate in
the same diff/audit machinery as user entities. This removes the
duplication between `auth.EnsureSchema` and the new `Migrator`.

### Audit table

```sql
CREATE TABLE IF NOT EXISTS _mar_schema_migrations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    table_name      TEXT    NOT NULL,
    migration_kind  TEXT    NOT NULL,    -- create_table | add_column_<n> | recreate_empty | create_index_<n>
    sql_text        TEXT    NOT NULL,
    applied_at      INTEGER NOT NULL     -- unix millis
);
```

Single underscore prefix follows the existing `_mar_*` framework
table convention (auth tables already use it).

### Error format

Blocked migrations produce an error message engineered to be
copy-pasteable into a fix. Example for the FK case (lispy had this
exact format):

```
migration blocked for entity Note: table "notes" already exists, and
relation "author" requires a foreign key notes.authorId -> users.id

SQLite cannot add this foreign key with ALTER TABLE, so Mar does not
migrate it automatically.

Hint:
  Migrate the table manually, then restart the app.
  Suggested Manual Migration SQL:

    BEGIN TRANSACTION;

    CREATE TABLE notes_new (
      id        INTEGER PRIMARY KEY AUTOINCREMENT,
      body      TEXT NOT NULL,
      authorId  INTEGER NOT NULL REFERENCES users(id)
    );

    INSERT INTO notes_new (id, body, authorId)
    SELECT id, body, /* replace NULL with a valid users.id */ NULL AS authorId
    FROM notes;

    DROP TABLE notes;
    ALTER TABLE notes_new RENAME TO notes;

    COMMIT;
```

Same shape for "required field added to non-empty table", error
explains the situation and suggests `Entity.text Entity.notNull
(Entity.default "")` or `Entity.text` (without notNull) as fixes.

---

## Required changes to the framework

### 1. Entity.default (extend existing)

```mar
Entity.text Entity.notNull (Entity.default "untitled")
Entity.int  Entity.notNull (Entity.default 0)
Entity.bool Entity.notNull (Entity.default False)
```

The runtime carries the default through to:
- `CREATE TABLE` SQL (`column TEXT NOT NULL DEFAULT 'untitled'`)
- `ALTER TABLE ADD COLUMN` SQL (same shape)
- `Repo.create` shape: defaults make the field optional in the input
  record, the runtime fills it server-side.

Today the entity field carries `Optional`, `Primary`, `Auto`,
`NotNull`. Add `Default Value` (using the existing runtime `Value`
type, supports String / Int / Bool / Float).

### 2. Entity.belongsTo (NEW)

For relation migrations to be possible, the framework needs to model
foreign keys explicitly. Today users encode them via
`Entity.int Entity.notNull` and a naming convention (`authorId` →
`Note.author`), fine for queries via `findBy`, but the migrator
can't see the FK.

```mar
notes : Entity Note
notes =
    Entity.define "notes"
        { id     = Entity.serial
        , body   = Entity.text Entity.notNull
        , author = Entity.belongsTo Backend.Users.users Entity.notNull
        }
```

Generates `authorId INTEGER NOT NULL REFERENCES users(id)`.
At query time, `Repo.create { body = "...", author = userId }`
accepts the int as the FK.

This is a separate feature, but spec it together because the
migration story for FKs depends on it.

### 3. Migrator (NEW)

```go
// internal/runtime/migrate.go (new file)
package runtime

type Migrator struct {
    db       *sql.DB
    entities []VEntity
}

func NewMigrator(db *sql.DB, entities []VEntity) *Migrator
func (m *Migrator) Run() error                       // auto-mode: apply safe, block unsafe
func (m *Migrator) Plan() ([]MigrationStep, error)   // read-only diff for inspection commands
```

Single mode. The Plan API is for read-only CLI commands; `Run`
wraps `Plan` plus actual execution.

### 4. Boot integration

`internal/jsserve/server.go::ServeLive` calls `Migrator.Run()` after
collecting entities from the program but before binding the listener.
Failure → `log.Fatalf` with the formatted error, no listener
opens, no traffic served against a broken schema.

### 5. CLI commands (new, inspection only)

```
mar migrate status [path]      # show history from _mar_schema_migrations
mar migrate plan [path]        # show what next boot WILL apply or block; read-only
```

Both commands open the DB read-only and report. Useful in CI
(`mar migrate plan` against a staging DB clone, fail the pipeline
if anything would be blocked) and in operator workflows ("what
changed last deploy?" via `status`).

There's no `mar migrate apply`, application happens at boot, not
as a separate step.

---

## Production deploy story

### Boot

```sh
mar build --target backend
./myapp serve
# Log: [migrate] applied 2 changes: add_column_archived (notes), create_table (sessions_v2)
# Log: [server] listening on :3000
```

If a migration is blocked (e.g. type change), the boot fails fast
with the formatted error and the listener never opens. Existing
running instances keep serving until the operator fixes the entity
declaration or runs the suggested manual SQL.

### CI guardrail

In CI, before promoting a build to production:

```sh
# Against a clone of the production DB:
./myapp migrate plan
# exit 0 if everything would apply cleanly
# exit 1 if any change would be blocked, with the same error the
# real boot would produce
```

Pipeline fails on exit 1 → bad migration never reaches production.
Same diff machine, same error format, just read-only.

### Operator review

```sh
./myapp migrate status
# Lists every applied migration from _mar_schema_migrations:
# 2026-04-09 17:45:27  notes  create_table         CREATE TABLE notes (...)
# 2026-04-09 18:01:13  notes  add_column_archived  ALTER TABLE notes ADD COLUMN archived ...
# ...
```

Useful for incident review ("what changed last deploy?") and for
debugging schema drift after a manual SQL session.

---

## Edge cases worth being explicit about

### Empty table + nullability change → drop + recreate

If the user changes `name : String` (notNull) to `name : Maybe String`
(optional) and the table has data, the migrator blocks. But if the
table is empty (often the case in dev), the migrator drops the table
and recreates with the new schema. Loses zero rows.

This was already in the lispy version; keep it.

### Primary key changes → blocked with no automatic fix

Changing a primary key forces a full table rebuild (CREATE NEW +
COPY + DROP + RENAME), too dangerous to automate. The error
includes the manual SQL pattern, same as the FK case.

### Renaming columns: NOT supported

Mar can't tell the difference between "renamed `body` to `content`"
and "removed `body`, added `content`". So the migrator treats the
old column as orphan (warn, keep) and the new column as a new field
(apply if safe). If the user wants real rename, they do it manually
and Mar accepts the result on next boot.

This is the same trade-off Rails / Phoenix make.

### Index migrations

Today the framework creates one unique index automatically (the auth
email lookup, see `internal/auth/schema.go`). The new migrator
generalizes this:

- Entity field marked `Entity.unique` → unique index named
  `idx_<table>_<field>_unique`.
- Entity-level `Entity.uniqueOn ["a", "b"]` for composite uniques
  (mirrors the lispy `(unique ((handle)))` form).
- Indexes are created idempotently (CREATE UNIQUE INDEX IF NOT
  EXISTS) and recorded in the audit table.

Adding a unique index that would violate existing data → migrator
catches the SQLite error, blocks, and points at the duplicate rows.

### Foreign-key cascading

`Entity.belongsTo` accepts a cascade hint:

```mar
author = Entity.belongsTo Backend.Users.users Entity.cascadeOnDelete
```

Maps to `REFERENCES users(id) ON DELETE CASCADE`. Default is
`NO ACTION`. This is a feature of `Entity.belongsTo`, not the
migrator per se, but spec it here for completeness.

---

## Testing plan

1. **Fresh install**: empty DB → boot → all entities created →
   audit table has N `create_table` rows.
2. **No-op restart**: boot again → migrator runs → no new audit
   rows → boot succeeds in <50ms.
3. **Add nullable field**: edit entity, restart → `add_column_*`
   row appears, table has new column, existing rows have NULL.
4. **Add required field with default**: edit entity, restart →
   `add_column_*`, existing rows backfilled with default.
5. **Add required field without default to non-empty table**:
   edit entity, restart → migrator blocks with helpful error
   pointing at "add a default or make optional"; existing data
   untouched.
6. **Type change**: edit entity (Int → String), restart → blocked
   with the type-changed error; existing data untouched.
7. **Add belongsTo to existing table**: edit entity, restart →
   blocked with the suggested manual SQL.
8. **Drift on extra column**: manually `ALTER TABLE` to add a
   column, restart → boot succeeds, warning logged.
9. **Crash mid-migration**: simulate by killing the process during
   `Migrator.Run()`; restart should pick up where left off (each
   statement is its own transaction; idempotent by design).
10. **History audit**: `SELECT * FROM _mar_schema_migrations
    ORDER BY id` returns every applied step in order.
11. **CI plan command**: `mar migrate plan` against a DB with a
    blocked-migration scenario → exit 1, same error format as boot.

---

## Implementation outline (estimated effort)

| Step | Files touched | Effort |
|---|---|---|
| Add `Entity.default` shape | `runtime/entity.go`, `typecheck/env.go`, `runtime/view.go` (new builders), 3 examples | 0.5d |
| Add `Entity.belongsTo` (and `Entity.unique`/`Entity.uniqueOn`) | same as above + `runtime/repo.go` for joins/decoding | 1d |
| Write `internal/runtime/migrate.go` (port from lispy) | new file ~400 lines, audit table, diff machinery | 1d |
| Wire `Migrator.Run()` into boot | `internal/jsserve/server.go`, `cmd/mar/main.go` | 0.25d |
| Refactor auth schema into the migrator | `internal/auth/schema.go`, `internal/auth/sweeper.go` | 0.5d |
| `mar migrate status / plan` commands (read-only) | `cmd/mar/main.go` + small lib | 0.25d |
| Tests: 11 scenarios above | new `migrate_test.go` | 1d |
| Docs (README + this spec → /docs) | `docs/migrations.md`, `BACKLOG.md` cleanup | 0.25d |
| **Total** | | **~4.75 days** |

The largest single chunk is `Entity.belongsTo` (which is its own
feature anyway) plus the test plan. Without `belongsTo`, the
migrator can ship without FK support, relation migrations would
just be in the "blocked, here's manual SQL" bucket forever, which is
acceptable for v1.

A v0 that lands today's app's needs (notes-auth-multipage, team-notes)
without `belongsTo` is realistically **~2 days**.

### v0 deferrals (called out)

These are in the spec above for completeness but **deferred** in
v0 to keep scope tight:

- **`Entity.default`**: without defaults, adding a NOT NULL
  column to a non-empty table is blocked with an error pointing
  at "make optional or add a default" as the fix. Once
  `Entity.default` lands, the migrator auto-applies the ALTER
  with the default value backfilled.
- **`Entity.belongsTo`**: without it, foreign keys are still
  encoded as `<rel>Id : Int`. The migrator can't see the FK so
  it never tries to add one. Once `Entity.belongsTo` lands, the
  block-with-manual-SQL path activates for FK changes.
- **`Entity.unique` / `Entity.uniqueOn`**: only the auth-email
  unique index exists today (created by `auth.Migrate`). Once
  unique declarations land, the migrator generalizes the same
  pattern to user entities.

All three are isolated extensions; adding any of them later is
backward compatible and doesn't reshape the migrator's API.

---

## Resolved design decisions

### Hot-reload behavior

The migrator runs on every `mar dev` hot-reload. To keep the dev
loop quiet:

- Silence the no-op case (no log line when nothing changed).
- Verbose, line-per-change when migrations actually apply
  (`[migrate] +column notes.archived`).
- If a migration fails mid-reload, log the error and keep the
  previous version of the program running, same policy as
  typecheck failures during hot-reload.

### Concurrent boot

When two mar processes touch the same SQLite file (typical during
zero-downtime deploys), SQLite's `BEGIN IMMEDIATE` serializes the
writers, no schema corruption is possible. The migrator handles
the resulting `SQLITE_BUSY` with three retries at exponential
backoff (100ms, 500ms, 2s), then fails loudly:

Retry log line (no-op when the lock clears quickly):

```
[migrate] database notes.db is locked; retrying (1/3)
```

Final failure (after 3 retries, ~6.5s total):

```
[migrate] FATAL: notes.db locked after 3 retries (6500ms total)

  Most likely causes:
    - A sqlite3 CLI session is holding the lock (check open terminals)
    - A backup tool or external script is reading the file
    - Filesystem issue (NFS, disk full, sync tool)
    - Another mar instance has a slow-running migration (uncommon
      for ALTER COLUMN; possible for CREATE INDEX on large tables)

  Investigate with: lsof notes.db
  exit 1
```

The retry message is intentionally neutral about the cause,
SQLite doesn't tell us who holds the lock. The final message
ranks possible causes by likelihood for a 6.5-second hold (a
parallel mar instance bootstrapping should have finished by then;
if it hasn't, the migration is genuinely slow).

### Long-running migrations

For v1, no progress reporting. SQLite `ALTER TABLE ADD COLUMN` is
metadata-only (O(1)), fast even on huge tables. `CREATE INDEX` is
O(n) but rare and the user controls when it happens.

What we do: log total elapsed time on the boot summary
(`[migrate] applied 2 changes in 47ms`). Operator sees the
duration and knows whether it's healthy.

What we don't do (yet): per-step progress reporting,
background-application, online-rebuild patterns. Document that
migrations should stay schema-only; data migrations are the user's
job, run out-of-band.

### iOS

iOS bundles are thin HTTP clients to the backend. No on-device
database. The migrator does not run there and Entity declarations
that reach iOS evaluation (via `Auth.config { entity = ... }`) are
just opaque values, the iOS runtime never opens the schema.

### Removing an entity from user code

A table in the live DB that no entity declares any longer is left
intact (no automatic drop). The migrator logs a hint pointing at
the cleanup command:

```
[migrate] table "comments" has no entity declaring it; keeping data intact.
          If no longer needed, drop with: DROP TABLE comments;
```

Same shape applies to extra columns within an entity's table.

### Upgrading from the legacy `ensureMigrated`

Existing deployments that ran with the old `CREATE TABLE IF NOT
EXISTS` path will encounter the new migrator on first boot of the
upgraded binary. Behavior:

- `_mar_schema_migrations` doesn't exist → migrator creates it.
- For each entity, the migrator reads the live schema and compares
  against the declared one. If they match (the common case), no
  migrations are applied; the audit table stays empty for that
  entity.
- If they DON'T match (latent drift that the old `IF NOT EXISTS`
  silently masked), the migrator surfaces it now, as a safe
  auto-fix or as a blocking error with a manual-SQL hint.

The latter case is feature, not bug: the new migrator is detecting
real schema drift that was silently corrupting writes before. The
release notes will document the upgrade and recommend running
`mar migrate plan` against a staging clone before deploying.

### `mar migrate plan` output format

Plain text only for v1. Same human-readable format the boot logs
use. JSON output is a documented possibility for a later release
when tooling needs it; we wait for a real consumer to ask before
shipping.
