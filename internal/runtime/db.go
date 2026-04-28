package runtime

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// DbConn wraps a SQLite connection inside a runtime Value.
type DbConn struct {
	DB *sql.DB
}

// VDb is a runtime value holding a database connection.
type VDb struct {
	Conn *DbConn
}

func (VDb) isValue()           {}
func (VDb) Display() string    { return "<db>" }

// dbBuiltins returns runtime functions for SQLite access.
//
// Low-level API (MVP):
//
//	Db.open    : String -> Effect String Db
//	Db.exec    : Db -> String -> Effect String ()
//	Db.query   : Db -> String -> Effect String (List Record)
//	Db.queryOne: Db -> String -> Effect String (Maybe Record)
//
// Higher-level helpers (entity, list, find, etc.) are layered on top of this.
func dbBuiltins() map[string]Value {
	return map[string]Value{
		"dbOpen": nativeFn(1, func(args []Value) (Value, error) {
			path, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Db.open: expected String path")
			}
			return VEffect{
				Tag: "dbOpen",
				Run: func() (Value, error) {
					db, err := sql.Open("sqlite", path.V)
					if err != nil {
						return nil, err
					}
					if err := db.Ping(); err != nil {
						return nil, err
					}
					return VDb{Conn: &DbConn{DB: db}}, nil
				},
			}, nil
		}),

		"dbExec": nativeFn(2, func(args []Value) (Value, error) {
			conn, ok := args[0].(VDb)
			if !ok {
				return nil, fmt.Errorf("Db.exec: expected Db connection")
			}
			query, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("Db.exec: expected String query")
			}
			return VEffect{
				Tag: "dbExec",
				Run: func() (Value, error) {
					if _, err := conn.Conn.DB.Exec(query.V); err != nil {
						return nil, err
					}
					return VUnit{}, nil
				},
			}, nil
		}),

		"dbQuery": nativeFn(2, func(args []Value) (Value, error) {
			conn, ok := args[0].(VDb)
			if !ok {
				return nil, fmt.Errorf("Db.query: expected Db connection")
			}
			query, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("Db.query: expected String query")
			}
			return VEffect{
				Tag: "dbQuery",
				Run: func() (Value, error) {
					rows, err := conn.Conn.DB.Query(query.V)
					if err != nil {
						return nil, err
					}
					defer rows.Close()
					cols, err := rows.Columns()
					if err != nil {
						return nil, err
					}
					var results []Value
					for rows.Next() {
						vals := make([]any, len(cols))
						ptrs := make([]any, len(cols))
						for i := range vals {
							ptrs[i] = &vals[i]
						}
						if err := rows.Scan(ptrs...); err != nil {
							return nil, err
						}
						results = append(results, scannedRowToRecord(cols, vals))
					}
					if err := rows.Err(); err != nil {
						return nil, err
					}
					return VList{Elements: results}, nil
				},
			}, nil
		}),

		"dbQueryOne": nativeFn(2, func(args []Value) (Value, error) {
			conn, ok := args[0].(VDb)
			if !ok {
				return nil, fmt.Errorf("Db.queryOne: expected Db connection")
			}
			query, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("Db.queryOne: expected String query")
			}
			return VEffect{
				Tag: "dbQueryOne",
				Run: func() (Value, error) {
					rows, err := conn.Conn.DB.Query(query.V)
					if err != nil {
						return nil, err
					}
					defer rows.Close()
					cols, err := rows.Columns()
					if err != nil {
						return nil, err
					}
					if !rows.Next() {
						return VCtor{Tag: "Nothing"}, nil
					}
					vals := make([]any, len(cols))
					ptrs := make([]any, len(cols))
					for i := range vals {
						ptrs[i] = &vals[i]
					}
					if err := rows.Scan(ptrs...); err != nil {
						return nil, err
					}
					return VCtor{Tag: "Just", Args: []Value{scannedRowToRecord(cols, vals)}}, nil
				},
			}, nil
		}),

		// Db.execParams : Db -> String -> List String -> Effect String ()
		// Parameterized exec (use ? for placeholders). Safer than string concat.
		"dbExecParams": nativeFn(3, func(args []Value) (Value, error) {
			conn, ok1 := args[0].(VDb)
			query, ok2 := args[1].(VString)
			params, ok3 := args[2].(VList)
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("Db.execParams: expected Db, String, List String")
			}
			pVals := make([]any, len(params.Elements))
			for i, p := range params.Elements {
				if s, ok := p.(VString); ok {
					pVals[i] = s.V
				} else if n, ok := p.(VInt); ok {
					pVals[i] = n.V
				} else {
					return nil, fmt.Errorf("Db.execParams: unsupported param type %T", p)
				}
			}
			return VEffect{
				Tag: "dbExecParams",
				Run: func() (Value, error) {
					if _, err := conn.Conn.DB.Exec(query.V, pVals...); err != nil {
						return nil, err
					}
					return VUnit{}, nil
				},
			}, nil
		}),

		// Db.queryParams : Db -> String -> List String -> Effect String (List Record)
		"dbQueryParams": nativeFn(3, func(args []Value) (Value, error) {
			conn, ok1 := args[0].(VDb)
			query, ok2 := args[1].(VString)
			params, ok3 := args[2].(VList)
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("Db.queryParams: expected Db, String, List")
			}
			pVals := make([]any, len(params.Elements))
			for i, p := range params.Elements {
				if s, ok := p.(VString); ok {
					pVals[i] = s.V
				} else if n, ok := p.(VInt); ok {
					pVals[i] = n.V
				} else {
					return nil, fmt.Errorf("Db.queryParams: unsupported param type %T", p)
				}
			}
			return VEffect{
				Tag: "dbQueryParams",
				Run: func() (Value, error) {
					rows, err := conn.Conn.DB.Query(query.V, pVals...)
					if err != nil {
						return nil, err
					}
					defer rows.Close()
					cols, err := rows.Columns()
					if err != nil {
						return nil, err
					}
					var results []Value
					for rows.Next() {
						vals := make([]any, len(cols))
						ptrs := make([]any, len(cols))
						for i := range vals {
							ptrs[i] = &vals[i]
						}
						if err := rows.Scan(ptrs...); err != nil {
							return nil, err
						}
						results = append(results, scannedRowToRecord(cols, vals))
					}
					if err := rows.Err(); err != nil {
						return nil, err
					}
					return VList{Elements: results}, nil
				},
			}, nil
		}),
	}
}

// scannedRowToRecord converts a SQL row's column values into a VRecord.
//
// Type mapping:
//   - int64 -> VInt
//   - float64 -> VFloat
//   - string -> VString
//   - []byte -> VString (assumed UTF-8)
//   - bool -> VBool
//   - nil -> VCtor{Nothing} (so columns are returned as Maybe-like)
//
// All record field names are the column names as returned by SQLite.
func scannedRowToRecord(cols []string, vals []any) VRecord {
	fields := make(map[string]Value, len(cols))
	order := make([]string, len(cols))
	for i, name := range cols {
		fields[name] = goValueToMar(vals[i])
		order[i] = name
	}
	return VRecord{Fields: fields, Order: order}
}

func goValueToMar(v any) Value {
	if v == nil {
		return VCtor{Tag: "Nothing"}
	}
	switch x := v.(type) {
	case int64:
		return VCtor{Tag: "Just", Args: []Value{VInt{V: x}}}
	case float64:
		return VCtor{Tag: "Just", Args: []Value{VFloat{V: x}}}
	case bool:
		return VCtor{Tag: "Just", Args: []Value{VBool{V: x}}}
	case string:
		return VCtor{Tag: "Just", Args: []Value{VString{V: x}}}
	case []byte:
		return VCtor{Tag: "Just", Args: []Value{VString{V: string(x)}}}
	}
	return VCtor{Tag: "Just", Args: []Value{VString{V: fmt.Sprintf("%v", v)}}}
}

// Quote escapes a string for safe inclusion in a SQL literal (single quotes
// doubled). For real apps, parameterized queries are preferred.
func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

var _ = sqlQuote // exported for future use
