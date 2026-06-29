package runtime

import (
	"database/sql"
	"fmt"
	"strings"
)

// Repo is the high-level data-access API. Each operation takes an Entity
// (carrying schema metadata) and produces SQL that's executed on the
// runtime-managed DB handle. Decoding maps SQL rows into mar Values
// shaped per the entity's row type.
//
// Public surface (deliberately small):
//
//	Repo.all        : Entity a -> Effect String (List a)
//	Repo.findById   : Entity a -> Int -> Effect String (Maybe a)
//	Repo.findBy     : Entity a -> r -> Effect String (List a)
//	Repo.create     : Entity a -> r -> Effect String a
//	Repo.update     : Entity a -> Int -> r -> Effect String (Maybe a)
//	Repo.deleteById : Entity a -> Int -> Effect String ()
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
				"WHERE "+pk.Name+" = ?",
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
		return nil, fmt.Errorf("Repo.findBy: expected filter record (got %T)", args[1])
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
		return nil, fmt.Errorf("Repo.create: expected input record (got %T)", args[1])
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
			sqlText := "INSERT INTO " + entity.Table + " (" + strings.Join(cols, ", ") + ") VALUES (" + placeholders + ")"
			res, err := db.Exec(sqlText, params...)
			if err != nil {
				return nil, fmt.Errorf("Repo.create: %w", err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return nil, fmt.Errorf("Repo.create: %w", err)
			}
			// Read back the row so the caller gets the full record (including
			// the just-assigned id). This is the simplest cross-DB path
			// without RETURNING; fine for SQLite.
			result, err := runSelectOne(db, entity,
				"WHERE "+pk.Name+" = ?",
				[]any{id})
			if err != nil {
				return nil, err
			}
			ctor, ok := result.(VCtor)
			if !ok || ctor.Tag != "Just" || len(ctor.Args) != 1 {
				return nil, fmt.Errorf("Repo.create: insert produced no row")
			}
			return ctor.Args[0], nil
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
		return nil, fmt.Errorf("Repo.update: expected patch record (got %T)", args[2])
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
				return runSelectOne(db, entity, "WHERE "+pk.Name+" = ?", []any{idVal.V})
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
				sets = append(sets, fname+" = ?")
				p, err := marValueToSQLForField(field, patch.Fields[fname])
				if err != nil {
					return nil, fmt.Errorf("Repo.update: field %q: %w", fname, err)
				}
				params = append(params, p)
			}
			params = append(params, idVal.V)
			sqlText := "UPDATE " + entity.Table + " SET " + strings.Join(sets, ", ") + " WHERE " + pk.Name + " = ?"
			res, err := db.Exec(sqlText, params...)
			if err != nil {
				return nil, fmt.Errorf("Repo.update: %w", err)
			}
			affected, err := res.RowsAffected()
			if err != nil {
				return nil, fmt.Errorf("Repo.update: %w", err)
			}
			if affected == 0 {
				return VCtor{Tag: "Nothing"}, nil
			}
			return runSelectOne(db, entity, "WHERE "+pk.Name+" = ?", []any{idVal.V})
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
			_, err = db.Exec("DELETE FROM "+entity.Table+" WHERE "+pk.Name+" = ?", idVal.V)
			if err != nil {
				return nil, fmt.Errorf("Repo.deleteById: %w", err)
			}
			return VUnit{}, nil
		},
	}, nil
}

// ---------- shared helpers ----------

// migrationCache prevents repeated migration work per process. Keyed by
// the entity's table name. Safe because entity schemas don't change at
// runtime within a process — hot-reload swaps the entire registry and
// resets the cache via ResetMigrationCache.
var migrationCache = struct {
	done map[string]bool
}{done: map[string]bool{}}

// ResetMigrationCache clears the per-process migration cache. Called by
// the boot path on hot-reload (so re-edited entities re-migrate) and
// by tests for isolation.
func ResetMigrationCache() {
	migrationCache.done = map[string]bool{}
}

// ensureMigrated opens the DB (lazy) and runs the migrator over this
// entity if it hasn't been migrated in this process yet.
//
// In normal operation, RunBootMigrations runs once at boot over every
// registered entity, fills the cache, and Repo.* calls hit the cache
// hot-path. This per-entity fallback exists for two cases:
//
//  1. Tests that exercise Repo.* directly without going through `mar
//     dev` / `mar build` — boot migrations don't run in that path.
//  2. Defensive: a future Repo call against an entity that wasn't in
//     the boot-time registry (shouldn't happen, but the safety net is
//     worth the trivial cost).
//
// Goes through the proper migrator (not a bare `CREATE TABLE IF
// NOT EXISTS`), so schema drift is detected here too.
func ensureMigrated(entity VEntity) (*sql.DB, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
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

// buildSelectSQL composes `SELECT a, b FROM table WHERE ...`. Columns are
// listed explicitly (instead of `SELECT *`) so the order matches the row
// shape we expect to decode into.
func buildSelectSQL(entity VEntity, whereClause string) string {
	cols := make([]string, len(entity.Fields))
	for i, f := range entity.Fields {
		cols[i] = f.Name
	}
	sql := "SELECT " + strings.Join(cols, ", ") + " FROM " + entity.Table
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
		conds = append(conds, fname+" = ?")
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
		cols = append(cols, fname)
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
	switch x := v.(type) {
	case VInt:
		return x.V, nil
	case VString:
		return x.V, nil
	case VBool:
		return x.V, nil
	case VFloat:
		return x.V, nil
	case VTime:
		// Caller didn't pass a field (e.g. filter without column
		// info). Fall back to milliseconds — same wire format.
		return x.Millis, nil
	}
	return nil, fmt.Errorf("unsupported value type %T", v)
}

// scanRow reads one row from rows and produces a record value shaped per
// the entity's fields. Each column is decoded according to its declared
// SQLType — INTEGER → VInt, TEXT → VString, BOOLEAN → VBool.
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
		fields[f.Name] = decodeColumn(f, vals[i])
	}
	return VRecord{Fields: fields, Order: order}, nil
}

// decodeColumn maps a raw SQL value into a runtime Value typed per the
// entity's column declaration. NULL on a NOT NULL column is treated as
// the zero value (defensive — schema should prevent this in practice).
func decodeColumn(f EntityField, raw any) Value {
	if raw == nil {
		// All declared columns are NOT NULL today, so NULL shouldn't
		// happen if migrations were applied correctly. Return the
		// zero value rather than crash.
		switch f.SQLType {
		case "INTEGER":
			return VInt{V: 0}
		case "BOOLEAN":
			return VBool{V: false}
		default:
			return VString{V: ""}
		}
	}
	switch f.SQLType {
	case "INTEGER":
		switch x := raw.(type) {
		case int64:
			return VInt{V: x}
		case float64:
			return VInt{V: int64(x)}
		}
	case "TIMESTAMP":
		// Stored as INTEGER under the hood; rehydrate to VTime so
		// the user's record has `createdAt : Time`, not Int.
		switch x := raw.(type) {
		case int64:
			return VTime{Millis: x}
		case float64:
			return VTime{Millis: int64(x)}
		}
	case "BOOLEAN":
		switch x := raw.(type) {
		case bool:
			return VBool{V: x}
		case int64:
			return VBool{V: x != 0}
		}
	case "TEXT":
		var s string
		switch x := raw.(type) {
		case string:
			s = x
		case []byte:
			s = string(x)
		default:
			return goValueToScalar(raw)
		}
		// Enum column: rehydrate the ctor from its tag. Membership in
		// AcceptedCtors is enforced; an unknown value (e.g. a row
		// written by something that bypassed the CHECK constraint)
		// surfaces as a zero-arg ctor with the raw tag so it's at
		// least visible in the model rather than silently coerced.
		if f.AcceptedCtors != nil {
			for _, accepted := range f.AcceptedCtors {
				if accepted == s {
					return VCtor{Tag: s}
				}
			}
			// Unknown value: still return as a ctor so pattern-matches
			// can detect it. Caller can choose how to handle.
			return VCtor{Tag: s}
		}
		return VString{V: s}
	}
	// Fallback: best-effort scalar conversion.
	return goValueToScalar(raw)
}
