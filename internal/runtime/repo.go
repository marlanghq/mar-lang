package runtime

import (
	"database/sql"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"
)

// Repo is the high-level data-access API. Each operation takes an Entity
// (carrying schema metadata) and produces SQL that's executed on the
// runtime-managed DB handle. Decoding maps SQL rows into mar Values
// shaped per the entity's row type.
//
// Public surface (deliberately small):
//
//	Repo.all        : Entity a -> Task (List a)
//	Repo.findById   : Entity a -> Int -> Task (Maybe a)
//	Repo.findBy     : Entity a -> r -> Task (List a)
//	Repo.create     : Entity a -> r -> Task a
//	Repo.update     : Entity a -> Int -> r -> Task (Maybe a)
//	Repo.deleteById : Entity a -> Int -> Task ()
//
// Filters / payloads (`r`) are records whose fields are a subset of the
// entity's columns with matching value types. Cross-checking happens at
// runtime per call (mar HM doesn't carry the constraint).

func repoBuiltins() map[string]Value {
	return map[string]Value{
		"repoAll":        nativeFn(1, repoAll),
		"repoFindByID":   nativeFn(2, repoFindByID),
		"repoFindBy":     nativeFn(2, repoFindBy),
		"repoCreate":     nativeFn(2, repoCreate),
		"repoUpdate":     nativeFn(3, repoUpdate),
		"repoDeleteByID": nativeFn(2, repoDeleteByID),
	}
}

// ---------- Repo.all ----------

func repoAll(args []Value) (Value, error) {
	entity, ok := args[0].(VEntity)
	if !ok {
		return nil, fmt.Errorf("Repo.all: expected Entity (got %T)", args[0])
	}
	return VEffect{
		Tag: "repoAll",
		Run: func() (Value, error) {
			db, err := ensureMigrated(entity)
			if err != nil {
				return nil, err
			}
			return runSelectMany(db, entity, "", nil)
		},
	}, nil
}

// ---------- Repo.findById ----------

func repoFindByID(args []Value) (Value, error) {
	entity, ok := args[0].(VEntity)
	if !ok {
		return nil, fmt.Errorf("Repo.findById: expected Entity (got %T)", args[0])
	}
	idVal, ok := args[1].(VInt)
	if !ok {
		return nil, fmt.Errorf("Repo.findById: expected Int id (got %T)", args[1])
	}
	return VEffect{
		Tag: "repoFindByID",
		Run: func() (Value, error) {
			pk := entity.primaryKey()
			if pk == nil {
				return nil, fmt.Errorf("Repo.findById: entity %s has no Entity.serial primary key", entity.Table)
			}
			db, err := ensureMigrated(entity)
			if err != nil {
				return nil, err
			}
			return runSelectOne(db, entity,
				"WHERE "+quoteIdent(pk.Name)+" = ?",
				[]any{idVal.V})
		},
	}, nil
}

// ---------- Repo.findBy ----------

func repoFindBy(args []Value) (Value, error) {
	entity, ok := args[0].(VEntity)
	if !ok {
		return nil, fmt.Errorf("Repo.findBy: expected Entity (got %T)", args[0])
	}
	filter, ok := args[1].(VRecord)
	if !ok {
		return nil, fmt.Errorf("Repo.findBy: expected a filter record, got %s", marKindOf(args[1]))
	}
	return VEffect{
		Tag: "repoFindBy",
		Run: func() (Value, error) {
			db, err := ensureMigrated(entity)
			if err != nil {
				return nil, err
			}
			where, params, err := buildWhereClause(entity, filter)
			if err != nil {
				return nil, err
			}
			return runSelectMany(db, entity, where, params)
		},
	}, nil
}

// ---------- Repo.create ----------

func repoCreate(args []Value) (Value, error) {
	entity, ok := args[0].(VEntity)
	if !ok {
		return nil, fmt.Errorf("Repo.create: expected Entity (got %T)", args[0])
	}
	input, ok := args[1].(VRecord)
	if !ok {
		return nil, fmt.Errorf("Repo.create: expected an input record, got %s", marKindOf(args[1]))
	}
	return VEffect{
		Tag: "repoCreate",
		Run: func() (Value, error) {
			db, err := ensureMigrated(entity)
			if err != nil {
				return nil, err
			}
			pk := entity.primaryKey()
			if pk == nil {
				return nil, fmt.Errorf("Repo.create: entity %s has no Entity.serial primary key", entity.Table)
			}
			cols, params, err := buildInsertColsParams(entity, input)
			if err != nil {
				return nil, err
			}
			placeholders := strings.Repeat("?, ", len(cols))
			placeholders = strings.TrimSuffix(placeholders, ", ")
			// One statement: insert and hand back the row as stored (id
			// included). It was an INSERT and then a SELECT of that id: two
			// transactions on the single connection, with a gap between them
			// in which another request could change the row.
			sqlText := "INSERT INTO " + quoteIdent(entity.Table) + " (" + strings.Join(cols, ", ") + ") VALUES (" + placeholders + ")" +
				" RETURNING " + selectColumns(entity)
			rec, found, err, decodeErr := runWriteReturning(db, entity, sqlText, params)
			if err != nil {
				return nil, fmt.Errorf("Repo.create: %w", err)
			}
			if decodeErr != nil {
				return nil, decodeErr
			}
			if !found {
				return nil, fmt.Errorf("Repo.create: insert produced no row")
			}
			return rec, nil
		},
	}, nil
}

// ---------- Repo.update ----------

func repoUpdate(args []Value) (Value, error) {
	entity, ok := args[0].(VEntity)
	if !ok {
		return nil, fmt.Errorf("Repo.update: expected Entity (got %T)", args[0])
	}
	idVal, ok := args[1].(VInt)
	if !ok {
		return nil, fmt.Errorf("Repo.update: expected Int id (got %T)", args[1])
	}
	patch, ok := args[2].(VRecord)
	if !ok {
		return nil, fmt.Errorf("Repo.update: expected a patch record, got %s", marKindOf(args[2]))
	}
	return VEffect{
		Tag: "repoUpdate",
		Run: func() (Value, error) {
			pk := entity.primaryKey()
			if pk == nil {
				return nil, fmt.Errorf("Repo.update: entity %s has no Entity.serial primary key", entity.Table)
			}
			db, err := ensureMigrated(entity)
			if err != nil {
				return nil, err
			}
			if len(patch.Order) == 0 {
				// Empty patch: just return the current row. Avoids generating
				// `UPDATE ... SET WHERE id = ?` which is invalid SQL.
				return runSelectOne(db, entity, "WHERE "+quoteIdent(pk.Name)+" = ?", []any{idVal.V})
			}
			sets := make([]string, 0, len(patch.Order))
			params := make([]any, 0, len(patch.Order)+1)
			for _, fname := range patch.Order {
				field := entity.findField(fname)
				if field == nil {
					return nil, fmt.Errorf("Repo.update: %s has no column %q", entity.Table, fname)
				}
				if field.Serial {
					return nil, fmt.Errorf("Repo.update: cannot update primary key %q", fname)
				}
				sets = append(sets, quoteIdent(fname)+" = ?")
				p, err := marValueToSQLForField(field, patch.Fields[fname])
				if err != nil {
					return nil, fmt.Errorf("Repo.update: field %q: %w", fname, err)
				}
				params = append(params, p)
			}
			params = append(params, idVal.V)
			sqlText := "UPDATE " + quoteIdent(entity.Table) + " SET " + strings.Join(sets, ", ") + " WHERE " + quoteIdent(pk.Name) + " = ?" +
				" RETURNING " + selectColumns(entity)
			rec, found, err, decodeErr := runWriteReturning(db, entity, sqlText, params)
			if err != nil {
				return nil, fmt.Errorf("Repo.update: %w", err)
			}
			if decodeErr != nil {
				return nil, decodeErr
			}
			if !found {
				return VCtor{Tag: "Nothing"}, nil
			}
			return VCtor{Tag: "Just", Args: []Value{rec}}, nil
		},
	}, nil
}

// ---------- Repo.deleteById ----------

func repoDeleteByID(args []Value) (Value, error) {
	entity, ok := args[0].(VEntity)
	if !ok {
		return nil, fmt.Errorf("Repo.deleteById: expected Entity (got %T)", args[0])
	}
	idVal, ok := args[1].(VInt)
	if !ok {
		return nil, fmt.Errorf("Repo.deleteById: expected Int id (got %T)", args[1])
	}
	return VEffect{
		Tag: "repoDeleteByID",
		Run: func() (Value, error) {
			pk := entity.primaryKey()
			if pk == nil {
				return nil, fmt.Errorf("Repo.deleteById: entity %s has no Entity.serial primary key", entity.Table)
			}
			db, err := ensureMigrated(entity)
			if err != nil {
				return nil, err
			}
			_, err = db.Exec("DELETE FROM "+quoteIdent(entity.Table)+" WHERE "+quoteIdent(pk.Name)+" = ?", idVal.V)
			if err != nil {
				return nil, fmt.Errorf("Repo.deleteById: %w", err)
			}
			return VUnit{}, nil
		},
	}, nil
}

// ---------- shared helpers ----------

// migrationCache prevents repeated migration work per process. Keyed by
// the entity's table name. Entity schemas don't change at runtime within a
// process: hot-reload swaps the entire registry and resets the cache via
// ResetMigrationCache.
//
// The mutex is not decoration. Requests are concurrent, and the cache read
// and the migrator run and the cache write used to be three separate steps
// with nothing between them, so two handlers arriving together both saw a
// cache miss and both planned the same CREATE TABLE. One of them lost:
// measured at exactly one 500 out of 32 simultaneous first requests, three
// rounds out of three, on every schema change. Holding the lock ACROSS the
// migrator run, not merely around the map, is what makes that impossible.
var migrationCache = struct {
	mu   sync.Mutex
	done map[string]bool
}{done: map[string]bool{}}

// ResetMigrationCache clears the per-process migration cache. Called by
// the boot path on hot-reload (so re-edited entities re-migrate) and
// by tests for isolation.
func ResetMigrationCache() {
	migrationCache.mu.Lock()
	defer migrationCache.mu.Unlock()
	migrationCache.done = map[string]bool{}
}

// markMigrated records that an entity's schema is current, so the request
// path can stop asking. RunBootMigrations calls it for every entity it
// migrated: without that, the boot pass did all the work and then the first
// request did it again, diff and all.
func markMigrated(table string) {
	migrationCache.mu.Lock()
	defer migrationCache.mu.Unlock()
	migrationCache.done[table] = true
}

// ensureMigrated opens the DB (lazy) and runs the migrator over this entity
// if it hasn't been migrated in this process yet.
//
// The sentence that used to be here — "in normal operation RunBootMigrations
// runs once at boot over every registered entity, fills the cache, and Repo.*
// calls hit the cache hot-path" — was wrong twice. Boot migrations did not
// run at all in a deployed binary, so this WAS the production migration path;
// and RunBootMigrations did not fill the cache, so even under `mar dev` the
// first request re-planned everything the boot pass had just done. Both are
// now true, which leaves this as the fallback it was always described as:
//
//  1. Tests that exercise Repo.* directly without going through `mar dev` /
//     `mar build`: boot migrations don't run in that path.
//  2. Defensive: a Repo call against an entity that wasn't in the boot-time
//     registry.
//
// Goes through the proper migrator (not a bare `CREATE TABLE IF NOT EXISTS`),
// so schema drift is detected here too. The lock is held across the run:
// see migrationCache.
func ensureMigrated(entity VEntity) (*sql.DB, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	migrationCache.mu.Lock()
	defer migrationCache.mu.Unlock()
	if migrationCache.done[entity.Table] {
		return db, nil
	}
	m := NewMigrator(db, []VEntity{entity})
	if _, err := m.Run(); err != nil {
		return nil, fmt.Errorf("migration of %s failed: %w", entity.Table, err)
	}
	migrationCache.done[entity.Table] = true
	return db, nil
}

// runSelectMany emits `SELECT col1, col2, ... FROM table [whereClause]` and
// decodes every row into a record matching the entity's row shape.
func runSelectMany(db *sql.DB, entity VEntity, whereClause string, params []any) (Value, error) {
	rows, err := db.Query(buildSelectSQL(entity, whereClause), params...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()
	var out []Value
	for rows.Next() {
		rec, err := scanRow(rows, entity)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return VList{Elements: out}, nil
}

// runSelectOne emits the same SELECT but expects at most one row. Returns
// `Just record` on hit, `Nothing` on no rows.
func runSelectOne(db *sql.DB, entity VEntity, whereClause string, params []any) (Value, error) {
	rows, err := db.Query(buildSelectSQL(entity, whereClause)+" LIMIT 1", params...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		// rows.Next() returns false for end-of-rows AND for iteration
		// errors. Distinguish them so a transient DB failure doesn't
		// masquerade as a successful "not found".
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("query iteration failed: %w", err)
		}
		return VCtor{Tag: "Nothing"}, nil
	}
	rec, err := scanRow(rows, entity)
	if err != nil {
		return nil, err
	}
	return VCtor{Tag: "Just", Args: []Value{rec}}, nil
}

// runWriteReturning runs an INSERT or UPDATE that ends in `RETURNING <the
// entity's columns>` and decodes the one row it affects. found is false when
// it affected none. The write is committed when the statement finishes, which
// is at Close: its error is the write's error.
//
// Two errors, because they were always worded differently: a failed write is
// reported under the caller's name, and a row that was written but does not
// decode is reported as the stored value it is.
func runWriteReturning(db *sql.DB, entity VEntity, sqlText string, params []any) (rec Value, found bool, writeErr, decodeErr error) {
	rows, err := db.Query(sqlText, params...)
	if err != nil {
		return nil, false, err, nil
	}
	if !rows.Next() {
		err := rows.Err()
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
		return nil, false, err, nil
	}
	row, scanErr := scanRow(rows, entity)
	// Step to the end before closing so the statement completes, not aborts.
	for rows.Next() {
	}
	err = rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, false, err, nil
	}
	if scanErr != nil {
		return nil, false, nil, scanErr
	}
	return row, true, nil, nil
}

// selectColumns is the entity's columns, quoted, in declaration order: the
// order scanRow decodes.
func selectColumns(entity VEntity) string {
	cols := make([]string, len(entity.Fields))
	for i, f := range entity.Fields {
		cols[i] = quoteIdent(f.Name)
	}
	return strings.Join(cols, ", ")
}

// buildSelectSQL composes `SELECT a, b FROM table WHERE ...`. Columns are
// listed explicitly (instead of `SELECT *`) so the order matches the row
// shape we expect to decode into.
func buildSelectSQL(entity VEntity, whereClause string) string {
	sql := "SELECT " + selectColumns(entity) + " FROM " + quoteIdent(entity.Table)
	if whereClause != "" {
		sql += " " + whereClause
	}
	return sql
}

// buildWhereClause turns a filter record into `WHERE col1 = ? AND col2 = ?`
// plus the parameter slice. Empty filter → no WHERE clause.
func buildWhereClause(entity VEntity, filter VRecord) (string, []any, error) {
	if len(filter.Order) == 0 {
		return "", nil, nil
	}
	conds := make([]string, 0, len(filter.Order))
	params := make([]any, 0, len(filter.Order))
	for _, fname := range filter.Order {
		field := entity.findField(fname)
		if field == nil {
			return "", nil, fmt.Errorf("Repo.findBy: %s has no column %q", entity.Table, fname)
		}
		conds = append(conds, quoteIdent(fname)+" = ?")
		p, err := marValueToSQLForField(field, filter.Fields[fname])
		if err != nil {
			return "", nil, fmt.Errorf("Repo.findBy: field %q: %w", fname, err)
		}
		params = append(params, p)
	}
	return "WHERE " + strings.Join(conds, " AND "), params, nil
}

// buildInsertColsParams turns an input record into the (cols, params) pair
// used by INSERT. Validates that each field is a real column on the
// entity (rejects typo'd field names early with a clear error).
func buildInsertColsParams(entity VEntity, input VRecord) ([]string, []any, error) {
	cols := make([]string, 0, len(input.Order))
	params := make([]any, 0, len(input.Order))
	for _, fname := range input.Order {
		field := entity.findField(fname)
		if field == nil {
			return nil, nil, fmt.Errorf("Repo.create: %s has no column %q", entity.Table, fname)
		}
		if field.Serial {
			// Skip serial PK in inserts; SQLite assigns it.
			continue
		}
		cols = append(cols, quoteIdent(fname))
		p, err := marValueToSQLForField(field, input.Fields[fname])
		if err != nil {
			return nil, nil, fmt.Errorf("Repo.create: field %q: %w", fname, err)
		}
		params = append(params, p)
	}
	return cols, params, nil
}

// marValueToSQLForField encodes a runtime Value for storage. When
// `field.AcceptedCtors != nil`, a VCtor value is encoded as its tag
// (after validating membership in the accepted set). For other column
// kinds the field is informational; the value's runtime type drives
// the encoding.
func marValueToSQLForField(field *EntityField, v Value) (any, error) {
	if ctor, ok := v.(VCtor); ok && ctor.Tag == "Just" && len(ctor.Args) == 1 {
		return marValueToSQLForField(field, ctor.Args[0])
	}
	if field != nil && field.AcceptedCtors != nil {
		ctor, ok := v.(VCtor)
		if !ok {
			return nil, fmt.Errorf("enum column %q: expected a constructor (got %T)", field.Name, v)
		}
		if len(ctor.Args) != 0 {
			return nil, fmt.Errorf("enum column %q: constructor %q takes args; only zero-arg ctors are supported", field.Name, ctor.Tag)
		}
		for _, accepted := range field.AcceptedCtors {
			if accepted == ctor.Tag {
				return ctor.Tag, nil
			}
		}
		return nil, fmt.Errorf("enum column %q: %q is not in accepted set %v", field.Name, ctor.Tag, field.AcceptedCtors)
	}
	if field != nil && field.SQLType == "TIMESTAMP" {
		t, ok := v.(VTime)
		if !ok {
			return nil, fmt.Errorf("timestamp column %q: expected Time (got %T)", field.Name, v)
		}
		return t.Millis, nil
	}
	if field != nil && field.SQLType == "DATE" {
		dt, ok := v.(VDate)
		if !ok {
			return nil, fmt.Errorf("date column %q: expected Date (got %T)", field.Name, v)
		}
		return dt.Days, nil
	}
	if field != nil && field.SQLType == "DECIMAL" {
		d, ok := v.(VDecimal)
		if !ok {
			return nil, fmt.Errorf("decimal column %q: expected Decimal (got %T)", field.Name, v)
		}
		coef, err := decimalCoefficientAtScale(d, field.DecimalScale)
		if err != nil {
			return nil, fmt.Errorf("decimal column %q: %w", field.Name, err)
		}
		return coef, nil
	}
	if field == nil {
		// Every caller resolves the column and refuses by name when the
		// entity has none, so this cannot happen from any path that
		// exists. It used to fall back to transcribing the value, with a
		// comment about "a filter built without a column" — a caller that
		// was never written. A fallback nobody reaches is a hole waiting
		// for the first caller who forgets, so it says so instead.
		return nil, fmt.Errorf("internal: a value was written with no column to check it against (got %T)", v)
	}
	return storableAs(*field, v)
}

// scanRow reads one row from rows and produces a record value shaped per
// the entity's fields. Each column is decoded according to its declared
// SQLType: INTEGER → VInt, TEXT → VString, BOOLEAN → VBool.
func scanRow(rows *sql.Rows, entity VEntity) (VRecord, error) {
	vals := make([]any, len(entity.Fields))
	ptrs := make([]any, len(entity.Fields))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return VRecord{}, fmt.Errorf("scan: %w", err)
	}
	fields := make(map[string]Value, len(entity.Fields))
	order := make([]string, len(entity.Fields))
	for i, f := range entity.Fields {
		order[i] = f.Name
		v, err := decodeColumn(entity.Table, f, vals[i])
		if err != nil {
			return VRecord{}, err
		}
		fields[f.Name] = v
	}
	return VRecord{Fields: fields, Order: order}, nil
}

// decodeColumn maps a raw SQL value into a runtime Value typed per the
// entity's column declaration. NULL on a NOT NULL column is treated as
// the zero value (defensive: schema should prevent this in practice).
//
// A row is the third thing a Mar program receives that the type system did
// not produce. A request body is checked against its declared type and a
// service response is checked against its declared type; a row was not, and
// SQLite is dynamically typed, so what a column HOLDS and what it was
// DECLARED as are two different facts. This function knew the declaration
// and used it only to pick a branch: anything the branch did not recognise
// fell through to goValueToScalar, which reads whatever the database handed
// back. Declared-type-first, database-type-as-silent-fallback — so a TEXT
// column holding an integer arrived as an Int where the type said String.
//
// Every branch now either produces the declared type or fails naming the
// table, the column and what was actually stored. The failure surfaces as a
// failed read, which is the honest outcome: the alternative is a value that
// violates its own type, and downstream that is a `case` with no matching
// branch on a program the checker proved total.
//
// It can also fail because SQLite's INTEGER is 64 bits wide and Mar's Int is
// 53, so a row can hold a number the language cannot represent. That is not a
// bad request from anyone: it is our own storage disagreeing with the
// language, whether from data written before the bound existed, a restore
// from another tool, or an external 64-bit id, so it raises rather than
// rounding.
//
// A TIMESTAMP is bounded by the range of a Time instead, a DATE by the range
// of a Date, and a DECIMAL decodes to an arbitrary-precision coefficient,
// which is not an Int.
func decodeColumn(table string, f EntityField, raw any) (Value, error) {
	if raw == nil {
		// All declared columns are NOT NULL today, so NULL shouldn't
		// happen if migrations were applied correctly. Return the
		// zero value rather than crash.
		switch f.SQLType {
		case "INTEGER":
			return VInt{V: 0}, nil
		case "BOOLEAN":
			return VBool{V: false}, nil
		default:
			return VString{V: ""}, nil
		}
	}
	switch f.SQLType {
	case "INTEGER":
		n, err := storedInt(table, f, raw)
		if err != nil {
			return nil, err
		}
		if !inIntRange(n) {
			return nil, intOutOfRange("the database", n)
		}
		return VInt{V: n}, nil

	case "TIMESTAMP":
		// Stored as INTEGER under the hood; rehydrate to VTime so
		// the user's record has `createdAt : Time`, not Int.
		n, err := storedInt(table, f, raw)
		if err != nil {
			return nil, err
		}
		return timeOf(fmt.Sprintf("%d milliseconds from the database", n), n)

	case "DATE":
		// Stored as an INTEGER count of days from 1970-01-01; rehydrate to
		// a Date so `due : Date` reads back as a Date.
		n, err := storedInt(table, f, raw)
		if err != nil {
			return nil, err
		}
		return dateOf(fmt.Sprintf("%d days from the database", n), n)

	case "DECIMAL":
		// Stored as an INTEGER coefficient at the column's fixed
		// scale; rehydrate to a Decimal so `amount : Decimal` reads
		// back as 12.34, never a bare 1234.
		n, err := storedInt(table, f, raw)
		if err != nil {
			return nil, err
		}
		return VDecimal{Coef: big.NewInt(n), Scale: f.DecimalScale}, nil

	case "BOOLEAN":
		switch x := raw.(type) {
		case bool:
			return VBool{V: x}, nil
		case int64:
			return VBool{V: x != 0}, nil
		}
		return nil, storedWrong(table, f, raw, "true or false")

	case "TEXT":
		var str string
		switch x := raw.(type) {
		case string:
			str = x
		case []byte:
			str = string(x)
		default:
			return nil, storedWrong(table, f, raw, "text")
		}
		if f.AcceptedCtors == nil {
			return VString{V: str}, nil
		}
		// Enum column: rehydrate the ctor from its tag. The comment
		// here used to say membership was enforced, and the code
		// below it returned the same thing whether or not the tag was
		// in the set — so a value the type no longer declares came
		// back AS that type. The old rationale was that a
		// pattern-match could then detect it, which is not something
		// Mar can do: every `case` is total over the declared
		// constructors (ADR 0022), so an undeclared tag matches no
		// branch at all and the read surfaced as
		// "no case branch matched" somewhere downstream.
		for _, accepted := range f.AcceptedCtors {
			if accepted == str {
				return VCtor{Tag: str}, nil
			}
		}
		return nil, fmt.Errorf(
			"%s.%s holds %q, which is not one of %s — a row predating a change to the type, "+
				"or written by something that bypassed the CHECK constraint",
			table, f.Name, str, strings.Join(quoteEach(f.AcceptedCtors), ", "))
	}
	return nil, storedWrong(table, f, raw, "a value of its declared type")
}

// storedInt reads a column stored as an integer. SQLite is dynamically typed,
// so an INTEGER column can hold a REAL: 3.7 used to become 3 silently, on a
// column whose declared type says Int. An integral float is the same number
// and is accepted; anything else is not that number and is refused.
func storedInt(table string, f EntityField, raw any) (int64, error) {
	switch x := raw.(type) {
	case int64:
		return x, nil
	case float64:
		if x != math.Trunc(x) || math.IsNaN(x) {
			return 0, fmt.Errorf("%s.%s holds %v, which is not a whole number", table, f.Name, x)
		}
		// Integral is not the same as representable. Converting a float
		// outside int64's range is implementation-defined in Go, so 1e300
		// came out as some arbitrary number and kept going — a saturated
		// value reads as real data, which is the failure mode this whole
		// function exists to remove. The bound is written as a comparison
		// against 2^63 because float64(math.MaxInt64) rounds UP to exactly
		// that, so `x <= float64(math.MaxInt64)` would let 2^63 through.
		const twoTo63 = 9223372036854775808.0
		if math.IsInf(x, 0) || x >= twoTo63 || x < -twoTo63 {
			return 0, fmt.Errorf("%s.%s holds %v, which is too large to store as a whole number",
				table, f.Name, x)
		}
		return int64(x), nil
	}
	return 0, storedWrong(table, f, raw, "a whole number")
}

// storedWrong says what the column was declared to hold and what it actually
// holds, in the vocabulary of whoever wrote the entity rather than Go's.
func storedWrong(table string, f EntityField, raw any, want string) error {
	return fmt.Errorf("%s.%s should hold %s, but the row holds %s",
		table, f.Name, want, describeStored(raw))
}

func describeStored(raw any) string {
	switch x := raw.(type) {
	case nil:
		return "nothing"
	case int64:
		return fmt.Sprintf("the whole number %d", x)
	case float64:
		return fmt.Sprintf("the number %v", x)
	case bool:
		return fmt.Sprintf("%v", x)
	case string:
		return fmt.Sprintf("the text %q", x)
	case []byte:
		return fmt.Sprintf("the text %q", string(x))
	}
	return fmt.Sprintf("a %T", raw)
}

// storableAs is the write path's half of decodeColumn: a value is written as
// the column was DECLARED, or it is refused.
//
// The read path was made to refuse a stored value that does not match its
// column; the write path went on transcribing whatever it was handed,
// switching on the VALUE and never on the column. SQLite has no opinion about
// it — affinity converts, or stores the other type as-is — so an Int written
// into a TEXT column comes back and fails on the READ, at a point with no
// connection to the write that caused it.
//
// The two paths have to agree, or the read guard is a way of blaming later
// code for what an earlier write did.
func storableAs(f EntityField, v Value) (any, error) {
	switch f.SQLType {
	case "INTEGER":
		if n, ok := v.(VInt); ok {
			return n.V, nil
		}
		return nil, writeWrong(f, v, "a whole number")

	case "TEXT":
		if s, ok := v.(VString); ok {
			return s.V, nil
		}
		return nil, writeWrong(f, v, "text")

	case "BOOLEAN":
		if b, ok := v.(VBool); ok {
			return b.V, nil
		}
		return nil, writeWrong(f, v, "true or false")
	}
	// TIMESTAMP, DATE, DECIMAL and enum columns are handled above, before this is
	// reached; anything else is a column kind this function has not been
	// taught, and guessing is what it exists to stop.
	return nil, writeWrong(f, v, "a value of its declared type")
}

func writeWrong(f EntityField, v Value, want string) error {
	return fmt.Errorf(
		"column %q holds %s, and the value being written is %s.\n"+
			"  It would be stored as it is and refused on the way back out, at a point "+
			"with nothing to connect it to this write",
		f.Name, want, describeValueKind(v))
}

// describeValueKind names a value the way the language does, not the way Go
// does: a `%T` reading `runtime.VCtor` tells a developer nothing.
func describeValueKind(v Value) string {
	switch x := v.(type) {
	case VInt:
		return fmt.Sprintf("the whole number %d", x.V)
	case VString:
		return fmt.Sprintf("the text %q", x.V)
	case VBool:
		return fmt.Sprintf("%t", x.V)
	case VTime:
		return "a Time"
	case VDate:
		return "a Date"
	case VDecimal:
		return "a Decimal"
	case VCtor:
		return fmt.Sprintf("the constructor `%s`", x.Tag)
	case VList:
		return "a list"
	case VRecord:
		return "a record"
	case VUnit:
		return "()"
	}
	return fmt.Sprintf("%T", v)
}

// ColumnIsCheckedBothWays reports whether a column's declared kind has an arm
// in BOTH `decodeColumn` (the read) and `storableAs` / the enum and time and
// decimal branches above it (the write).
//
// Exported for the census in internal/apphost, which walks every entity in
// every example. A kind neither switch has falls through to an error, so
// nothing is silently transcribed — but the failure surfaces only for a
// program that uses that kind, which is why the census exists.
func ColumnIsCheckedBothWays(f EntityField) bool {
	switch f.SQLType {
	case "INTEGER", "TEXT", "BOOLEAN", "TIMESTAMP", "DATE", "DECIMAL":
		return true
	}
	return false
}
