package runtime

import (
	"strings"
	"testing"
)

// Every identifier Mar puts into SQL is quoted. A field name in Mar is an
// ordinary identifier; in SQL, some of those are reserved words.
//
// Measured before this: an entity with a column named `order` type-checked,
// passed `mar check`, and took the app down at boot with
//
//	migration create_table on rows failed: SQL logic error: near "order": syntax error (1)
//
// which names SQLite's parser and not the field the developer wrote. A table
// named `order` did the same. Quoting removes the failure rather than
// reporting it, and it is what the migration path already did — the query and
// DDL paths were the ones that did not, which is the same shape as every other
// "the check is live here and absent there" in this work.
//
// A keyword LIST would have been the wrong fix: SQLite accepts many of its
// keywords as identifiers depending on position (`rows` works, `order` does
// not), so a list refuses names that are actually fine.
func TestEverySQLIdentifierIsQuoted(t *testing.T) {
	reserved := VEntity{
		Table: "order", // a reserved word, and a perfectly good Mar name
		Fields: []EntityField{
			{Name: "id", SQLType: "INTEGER", Serial: true, NotNull: true},
			{Name: "group", SQLType: "TEXT", NotNull: true},
			{Name: "select", SQLType: "TEXT", NotNull: true, AcceptedCtors: []string{"A", "B"}},
		},
	}

	t.Run("CREATE TABLE", func(t *testing.T) {
		sql := buildCreateTableSQL(reserved)
		for _, want := range []string{`"order"`, `"id"`, `"group"`, `CHECK("select" IN`} {
			if !strings.Contains(sql, want) {
				t.Errorf("missing %s in:\n  %s", want, sql)
			}
		}
		// And no bare reserved word survives where an identifier goes.
		for _, bare := range []string{" order ", "(id ", ", group ", "CHECK(select "} {
			if strings.Contains(sql, bare) {
				t.Errorf("unquoted identifier %q in:\n  %s", bare, sql)
			}
		}
	})

	t.Run("SELECT", func(t *testing.T) {
		sql := buildSelectSQL(reserved, "")
		for _, want := range []string{`"id", "group", "select"`, `FROM "order"`} {
			if !strings.Contains(sql, want) {
				t.Errorf("missing %s in:\n  %s", want, sql)
			}
		}
	})

	t.Run("WHERE", func(t *testing.T) {
		where, params, err := buildWhereClause(reserved, VRecord{
			Fields: map[string]Value{"group": VString{V: "x"}},
			Order:  []string{"group"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if where != `WHERE "group" = ?` {
			t.Errorf("got %q", where)
		}
		// The VALUE never enters the SQL: that is what keeps a filter from
		// being able to say anything about the query.
		if len(params) != 1 || params[0] != "x" {
			t.Errorf("the value belongs in the parameters, got %v", params)
		}
	})

	t.Run("INSERT columns", func(t *testing.T) {
		cols, _, err := buildInsertColsParams(reserved, VRecord{
			Fields: map[string]Value{"group": VString{V: "x"}},
			Order:  []string{"group"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != 1 || cols[0] != `"group"` {
			t.Errorf("got %v", cols)
		}
	})

	// An identifier carrying a double quote cannot break out: quoteIdent
	// doubles it, which is SQL's own escape.
	t.Run("a quote inside the name", func(t *testing.T) {
		if got := quoteIdent(`a"b`); got != `"a""b"` {
			t.Errorf("got %s", got)
		}
	})
}
