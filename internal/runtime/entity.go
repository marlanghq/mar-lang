package runtime

import (
	"fmt"
	"strings"
)

// VEntity is the runtime representation of an Entity declaration.
//
// It holds the table name, ordered fields with their column types, primary
// key, and constraints. The runtime uses this both to derive migrations
// (CREATE/ALTER TABLE) and to translate Db.list/Db.find/etc into SQL.
type VEntity struct {
	Table       string
	Fields      []EntityField
	PrimaryKey  string
	Uniques     [][]string
	ForeignKeys []EntityFK
}

type EntityField struct {
	Name     string
	SQLType  string // "INTEGER", "TEXT", "REAL", "DATETIME"
	NotNull  bool
	Default  string // SQL default expression, "" if none
	AutoIncr bool   // PRIMARY KEY AUTOINCREMENT (SQLite-specific)
}

type EntityFK struct {
	Field     string
	RefTable  string
	RefColumn string
}

func (VEntity) isValue() {}
func (e VEntity) Display() string {
	return fmt.Sprintf("<entity:%s>", e.Table)
}

// VColType is a typed column kind used by Entity.field. Values are produced
// by Entity.int / Entity.text / Entity.real / Entity.blob / Entity.dateTime
// rather than written as raw SQL strings — typo'd type names are caught at
// compile time instead of leaking into a CREATE TABLE.
type VColType struct {
	SQL string
}

func (VColType) isValue() {}
func (c VColType) Display() string {
	return "<coltype:" + c.SQL + ">"
}

// entityBuiltins exposes a builder API in mar:
//
//	Entity.create     : String -> Entity                       -- new empty entity, just a table name
//	Entity.field      : String -> ColType -> Entity -> Entity  -- name, column kind
//	Entity.int        : ColType                                -- INTEGER
//	Entity.text       : ColType                                -- TEXT
//	Entity.real       : ColType                                -- REAL
//	Entity.blob       : ColType                                -- BLOB
//	Entity.dateTime   : ColType                                -- DATETIME
//	Entity.primaryKey : String -> Entity -> Entity             -- mark a field as primary key (autoinc)
//	Entity.unique     : List String -> Entity -> Entity
//	Entity.notNull    : String -> Entity -> Entity
//
// (Higher-level: future. For MVP, this builder is enough to express schemas.)
func entityBuiltins() map[string]Value {
	return map[string]Value{
		"entityCreate": nativeFn(1, func(args []Value) (Value, error) {
			name, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Entity.create: expected String name")
			}
			return VEntity{Table: name.V}, nil
		}),
		"entityField": nativeFn(3, func(args []Value) (Value, error) {
			name, ok1 := args[0].(VString)
			ct, ok2 := args[1].(VColType)
			ent, ok3 := args[2].(VEntity)
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("Entity.field: expected String, ColType, Entity")
			}
			ent.Fields = append(ent.Fields, EntityField{Name: name.V, SQLType: ct.SQL})
			return ent, nil
		}),

		// ColType constants — typed alternative to raw SQL strings.
		"entityInt":      VColType{SQL: "INTEGER"},
		"entityText":     VColType{SQL: "TEXT"},
		"entityReal":     VColType{SQL: "REAL"},
		"entityBlob":     VColType{SQL: "BLOB"},
		"entityDateTime": VColType{SQL: "DATETIME"},
		"entityPrimaryKey": nativeFn(2, func(args []Value) (Value, error) {
			name, ok1 := args[0].(VString)
			ent, ok2 := args[1].(VEntity)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Entity.primaryKey: expected String, Entity")
			}
			ent.PrimaryKey = name.V
			// Mark the field as autoincr if it exists.
			for i, f := range ent.Fields {
				if f.Name == name.V {
					ent.Fields[i].AutoIncr = true
					ent.Fields[i].NotNull = true
				}
			}
			return ent, nil
		}),
		"entityNotNull": nativeFn(2, func(args []Value) (Value, error) {
			name, ok1 := args[0].(VString)
			ent, ok2 := args[1].(VEntity)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Entity.notNull: expected String, Entity")
			}
			for i, f := range ent.Fields {
				if f.Name == name.V {
					ent.Fields[i].NotNull = true
				}
			}
			return ent, nil
		}),
		"entityUnique": nativeFn(2, func(args []Value) (Value, error) {
			cols, ok1 := args[0].(VList)
			ent, ok2 := args[1].(VEntity)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Entity.unique: expected List String, Entity")
			}
			names := make([]string, len(cols.Elements))
			for i, c := range cols.Elements {
				s, ok := c.(VString)
				if !ok {
					return nil, fmt.Errorf("Entity.unique: list element not String")
				}
				names[i] = s.V
			}
			ent.Uniques = append(ent.Uniques, names)
			return ent, nil
		}),
		"entityForeignKey": nativeFn(4, func(args []Value) (Value, error) {
			field, ok1 := args[0].(VString)
			refTable, ok2 := args[1].(VString)
			refCol, ok3 := args[2].(VString)
			ent, ok4 := args[3].(VEntity)
			if !ok1 || !ok2 || !ok3 || !ok4 {
				return nil, fmt.Errorf("Entity.foreignKey: expected String, String, String, Entity")
			}
			ent.ForeignKeys = append(ent.ForeignKeys, EntityFK{
				Field: field.V, RefTable: refTable.V, RefColumn: refCol.V,
			})
			return ent, nil
		}),

		// Auto-migrate: given a Db connection and a list of entities, create
		// missing tables. Returns Effect String ().
		//
		// MVP: only CREATE TABLE IF NOT EXISTS (no schema diff yet).
		"entityMigrate": nativeFn(2, func(args []Value) (Value, error) {
			conn, ok1 := args[0].(VDb)
			ents, ok2 := args[1].(VList)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Entity.migrate: expected Db, List Entity")
			}
			return VEffect{
				Tag: "entityMigrate",
				Run: func() (Value, error) {
					for _, e := range ents.Elements {
						ent, ok := e.(VEntity)
						if !ok {
							return nil, fmt.Errorf("Entity.migrate: list element not Entity")
						}
						sqlText := buildCreateTableSQL(ent)
						if _, err := conn.Conn.DB.Exec(sqlText); err != nil {
							return nil, fmt.Errorf("entity %s: %v", ent.Table, err)
						}
					}
					return VUnit{}, nil
				},
			}, nil
		}),
	}
}

// buildCreateTableSQL generates a CREATE TABLE IF NOT EXISTS statement.
func buildCreateTableSQL(e VEntity) string {
	var sb strings.Builder
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(e.Table)
	sb.WriteString(" (")
	for i, f := range e.Fields {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(f.Name)
		sb.WriteString(" ")
		sb.WriteString(f.SQLType)
		if f.AutoIncr && f.Name == e.PrimaryKey {
			sb.WriteString(" PRIMARY KEY AUTOINCREMENT")
		} else if f.Name == e.PrimaryKey {
			sb.WriteString(" PRIMARY KEY")
		}
		if f.NotNull && !f.AutoIncr {
			sb.WriteString(" NOT NULL")
		}
		if f.Default != "" {
			sb.WriteString(" DEFAULT ")
			sb.WriteString(f.Default)
		}
	}
	for _, u := range e.Uniques {
		sb.WriteString(", UNIQUE (")
		sb.WriteString(strings.Join(u, ", "))
		sb.WriteString(")")
	}
	for _, fk := range e.ForeignKeys {
		sb.WriteString(", FOREIGN KEY (")
		sb.WriteString(fk.Field)
		sb.WriteString(") REFERENCES ")
		sb.WriteString(fk.RefTable)
		sb.WriteString("(")
		sb.WriteString(fk.RefColumn)
		sb.WriteString(")")
	}
	sb.WriteString(")")
	return sb.String()
}
