package runtime

import (
	"fmt"
	"strings"
)

// The migrator diffs the SQLite STORAGE type, and INTEGER covers `Int`,
// `Time`, `Date` and `Decimal` at any scale. So a change that stays inside a
// storage class is invisible to it, and it reports the schema up to date — correctly by its own
// standard, wrongly by the program's.
//
// The enum half of this was closable without help: the old constructor set is
// written in the CHECK that sqlite_master already stores. The rest is not.
// INTEGER is INTEGER; nothing in the database says whether the number in it
// was a count, a millisecond, or a coefficient at scale 2.
//
// Measured, on a running app with one row of R$ 12,34 stored as the
// coefficient 1234:
//
//	Entity.decimal 2 -> Entity.decimal 3
//	boot             says nothing, no migration is planned
//	the stored row   is untouched: still 1234
//	the read         answers 1.234
//
// A one-character edit turned R$ 12,34 into R$ 1,234 on every row, silently.
// That is what this catalog exists to stop.

// declaredTypeCatalog is the table that remembers what a column was DECLARED
// as, which is the one thing SQLite cannot be asked.
const declaredTypeCatalog = "_mar_column_types"

func (m *Migrator) ensureDeclaredTypeCatalog() error {
	_, err := m.db.Exec(`
		CREATE TABLE IF NOT EXISTS ` + declaredTypeCatalog + ` (
			table_name   TEXT    NOT NULL,
			column_name  TEXT    NOT NULL,
			declared     TEXT    NOT NULL,
			scale        INTEGER NOT NULL,
			PRIMARY KEY (table_name, column_name)
		)
	`)
	return err
}

// declaredType is how a column's Mar type is written down: the kind, plus the
// scale that only Decimal uses.
type declaredType struct {
	Kind  string
	Scale int
}

// withArticle keeps the sentence readable: "an Int", "a Time".
func withArticle(kind string) string {
	if kind == "" {
		return kind
	}
	switch kind[0] {
	case 'A', 'E', 'I', 'O', 'U', 'a', 'e', 'i', 'o', 'u':
		return "an " + kind
	}
	return "a " + kind
}

func (d declaredType) String() string {
	if d.Kind == "Decimal" {
		return fmt.Sprintf("Decimal at scale %d", d.Scale)
	}
	return d.Kind
}

// declaredTypeOf reads a field's Mar type off its column definition. It is the
// inverse of the mapping Entity.* applies, and it is deliberately coarse:
// only the distinctions the storage type ERASES need recording.
func declaredTypeOf(f EntityField) declaredType {
	switch f.SQLType {
	case "DECIMAL":
		return declaredType{Kind: "Decimal", Scale: f.DecimalScale}
	case "TIMESTAMP":
		return declaredType{Kind: "Time"}
	case "DATE":
		return declaredType{Kind: "Date"}
	case "INTEGER":
		return declaredType{Kind: "Int"}
	case "BOOLEAN":
		return declaredType{Kind: "Bool"}
	case "TEXT":
		if f.AcceptedCtors != nil {
			// The constructor set lives in the CHECK, which planEnumSets
			// reads; the catalog only needs to know it is an enum so a
			// change to or from plain text is visible.
			return declaredType{Kind: "enum"}
		}
		return declaredType{Kind: "String"}
	}
	return declaredType{Kind: f.SQLType}
}

// recordedTypes reads what the catalog holds for one table.
func (m *Migrator) recordedTypes(table string) (map[string]declaredType, error) {
	out := map[string]declaredType{}
	rows, err := m.db.Query(
		`SELECT column_name, declared, scale FROM `+declaredTypeCatalog+` WHERE table_name = ?`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, kind string
		var scale int
		if err := rows.Scan(&name, &kind, &scale); err != nil {
			return nil, err
		}
		out[name] = declaredType{Kind: kind, Scale: scale}
	}
	return out, rows.Err()
}

// rememberDeclaredTypes writes the current declaration down. Called after a
// table reaches the shape the program declares, so the catalog always
// describes what the data in it means.
func (m *Migrator) rememberDeclaredTypes(ent VEntity) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range ent.Fields {
		d := declaredTypeOf(f)
		if _, err := tx.Exec(
			`INSERT INTO `+declaredTypeCatalog+` (table_name, column_name, declared, scale)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT (table_name, column_name) DO UPDATE SET declared = excluded.declared, scale = excluded.scale`,
			ent.Table, f.Name, d.Kind, d.Scale,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// planDeclaredTypeChanges compares the catalog with the declaration and says
// what to do about each difference.
//
// A column the catalog has never seen is ADOPTED, not guessed about: the
// database has been working, so what the source says today is what the data
// in it means. The catalog can only protect changes made after it exists, and
// pretending otherwise would mean inventing an old type for every column in
// every database that predates this table.
func (m *Migrator) planDeclaredTypeChanges(ent VEntity) ([]MigrationStep, error) {
	recorded, err := m.recordedTypes(ent.Table)
	if err != nil {
		return nil, err
	}
	var steps []MigrationStep
	for _, f := range ent.Fields {
		was, known := recorded[f.Name]
		if !known {
			continue // adopted; rememberDeclaredTypes writes it down
		}
		now := declaredTypeOf(f)
		if was == now {
			continue
		}
		step, err := m.planOneTypeChange(ent, f, was, now)
		if err != nil {
			return nil, err
		}
		if step != nil {
			steps = append(steps, *step)
		}
	}
	return steps, nil
}

func (m *Migrator) planOneTypeChange(ent VEntity, f EntityField, was, now declaredType) (*MigrationStep, error) {
	// A Decimal that only changed scale has a mechanical answer: the stored
	// coefficient is the same number at a different scale, so multiplying by
	// the difference is exact.
	if was.Kind == "Decimal" && now.Kind == "Decimal" {
		return m.planScaleChange(ent, f, was.Scale, now.Scale)
	}
	// A Date and the Time at its midnight UTC are the same day, so a column
	// can move between the two when no row loses anything (Date.toTime and
	// Date.fromTime).
	if was.Kind == "Time" && now.Kind == "Date" {
		return m.planTimeToDate(ent, f)
	}
	if was.Kind == "Date" && now.Kind == "Time" {
		return &MigrationStep{
			Kind:  StepRestateColumn,
			Table: ent.Table,
			SQLs: []string{
				fmt.Sprintf("UPDATE %s SET %s = %s * %d", quoteIdent(ent.Table), quoteIdent(f.Name), quoteIdent(f.Name), msPerDay),
			},
			Description: fmt.Sprintf(
				"restate %s.%s from Date to Time (every day becomes its midnight UTC)",
				ent.Table, f.Name),
		}, nil
	}
	// Everything else means the numbers in the column stand for something
	// different than they did. What a count should become in milliseconds is
	// a fact about the app, not about the schema.
	return &MigrationStep{
		Kind:  StepBlocked,
		Table: ent.Table,
		Error: &BlockedMigrationError{
			Summary: fmt.Sprintf(
				"migration blocked for entity `%s.%s`: the column was declared as %s and is now %s",
				ent.Table, f.Name, was, now),
			Hint: "The database stores both the same way, so nothing about the stored rows changed —\n" +
				"only what they are read as. There is no conversion that is right in general:\n" +
				"what " + withArticle(was.Kind) + " should become as " + withArticle(now.Kind) +
				" is a fact about the app.\n\n" +
				"Put the column back to " + was.String() + ", or migrate the data yourself and then\n" +
				"update " + declaredTypeCatalog + " to match.",
		},
	}, nil
}

// planScaleChange turns a Decimal scale change into the multiplication that
// keeps every stored value meaning the same number.
//
// Widening (scale 2 -> 3) multiplies and is always exact. Narrowing divides,
// which is only exact when every row divides evenly — and ADR 0004 says the
// language never rounds without being asked, so a row that would lose a digit
// blocks instead.
func (m *Migrator) planScaleChange(ent VEntity, f EntityField, from, to int) (*MigrationStep, error) {
	col := quoteIdent(f.Name)
	table := quoteIdent(ent.Table)
	if to > from {
		factor := scaleFactor(to - from)
		return &MigrationStep{
			Kind:  StepRestateColumn,
			Table: ent.Table,
			SQLs: []string{
				fmt.Sprintf("UPDATE %s SET %s = %s * %d", table, col, col, factor),
			},
			Description: fmt.Sprintf(
				"restate %s.%s from scale %d to scale %d (every value keeps its amount)",
				ent.Table, f.Name, from, to),
		}, nil
	}

	factor := scaleFactor(from - to)
	var lossy int
	err := m.db.QueryRow(
		fmt.Sprintf("SELECT count(*) FROM %s WHERE %s %% %d <> 0", table, col, factor),
	).Scan(&lossy)
	if err != nil && !isNoSuchTable(err) {
		return nil, err
	}
	if lossy > 0 {
		return &MigrationStep{
			Kind:  StepBlocked,
			Table: ent.Table,
			Error: &BlockedMigrationError{
				Summary: fmt.Sprintf(
					"migration blocked for entity `%s.%s`: scale %d holds %s that scale %d cannot",
					ent.Table, f.Name, from, plural(lossy, "value"), to),
				Hint: "Narrowing the scale drops a digit from those rows, and the language does not\n" +
					"round without being asked (ADR 0004).\n\n" +
					"Round them yourself, then restart:\n\n" +
					fmt.Sprintf("    UPDATE %s SET %s = round(%s / %d.0) * %d WHERE %s %% %d <> 0;\n\n",
						table, col, col, factor, factor, col, factor) +
					"Or keep the column at scale " + fmt.Sprint(from) + ".",
			},
		}, nil
	}
	return &MigrationStep{
		Kind:  StepRestateColumn,
		Table: ent.Table,
		SQLs: []string{
			fmt.Sprintf("UPDATE %s SET %s = %s / %d", table, col, col, factor),
		},
		Description: fmt.Sprintf(
			"restate %s.%s from scale %d to scale %d (every value keeps its amount)",
			ent.Table, f.Name, from, to),
	}, nil
}

// planTimeToDate turns a Time column into a Date column when every Time in it
// is a midnight UTC, the Time Date.toTime gives for a day. A Time with a time
// of day blocks: which day it stands for depends on where its person was,
// and the database does not know that.
func (m *Migrator) planTimeToDate(ent VEntity, f EntityField) (*MigrationStep, error) {
	col := quoteIdent(f.Name)
	table := quoteIdent(ent.Table)
	var timed int
	err := m.db.QueryRow(
		fmt.Sprintf("SELECT count(*) FROM %s WHERE %s %% %d <> 0", table, col, msPerDay),
	).Scan(&timed)
	if err != nil && !isNoSuchTable(err) {
		return nil, err
	}
	if timed > 0 {
		verb := "have"
		if timed == 1 {
			verb = "has"
		}
		return &MigrationStep{
			Kind:  StepBlocked,
			Table: ent.Table,
			Error: &BlockedMigrationError{
				Summary: fmt.Sprintf(
					"migration blocked for entity `%s.%s`: the column was declared as Time and is now Date, and %s %s a time of day",
					ent.Table, f.Name, plural(timed, "row"), verb),
				Hint: "A Date is a day with no time of day. A Time at midnight UTC becomes its day,\n" +
					"but which day a later Time stands for depends on where its person was.\n\n" +
					"If the day in UTC is the right one, move those rows to midnight and restart:\n\n" +
					fmt.Sprintf("    UPDATE %s SET %s = %s - (%s %% %d + %d) %% %d WHERE %s %% %d <> 0;\n\n",
						table, col, col, col, msPerDay, msPerDay, msPerDay, col, msPerDay) +
					"Or keep the column as a Time.",
			},
		}, nil
	}
	return &MigrationStep{
		Kind:  StepRestateColumn,
		Table: ent.Table,
		SQLs: []string{
			fmt.Sprintf("UPDATE %s SET %s = %s / %d", table, col, col, msPerDay),
		},
		Description: fmt.Sprintf(
			"restate %s.%s from Time to Date (every midnight UTC becomes its day)",
			ent.Table, f.Name),
	}, nil
}

// scaleFactor is 10^n as an int64: a Decimal scale difference is small, and
// the SQL below needs a plain number.
func scaleFactor(n int) int64 {
	out := int64(1)
	for i := 0; i < n; i++ {
		out *= 10
	}
	return out
}

func isNoSuchTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}
