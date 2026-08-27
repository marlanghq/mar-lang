package runtime

import (
	"fmt"
	"regexp"
	"strings"
)

// An `Entity.enum` column's constructor set is frozen at CREATE TABLE time and
// never revisited, because the migrator compares the SQLite STORAGE type and a
// TEXT column with any constructor set is just TEXT. So changing the type in
// Mar source emitted no migration step at all, and the CHECK constraint went on
// enforcing a set the program no longer declares. Both directions bite:
//
//   - a constructor ADDED is refused by the database on write, quoting a set
//     the source does not contain
//   - a constructor REMOVED leaves rows holding a tag the type no longer has,
//     and the read path refuses them, so the table becomes unreadable
//
// This does not need the schema catalog the other declared-type changes need.
// The old set is recorded, exactly, in the CHECK text the migrator itself
// wrote: `PRAGMA table_info` merely does not return it. sqlite_master does.

// checkInPattern matches the CHECK the migrator emits in columnDefSQL:
// `CHECK(<column> IN ('A', 'B'))`. Anchored on the column name so a table with
// several enum columns yields the right one.
var checkInPattern = regexp.MustCompile(`(?i)CHECK\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)\s+IN\s*\(([^)]*)\)\s*\)`)

// liveEnumSet reads the constructor set a column's CHECK currently enforces.
// Returns ok=false when the column has no CHECK, which is what a column that
// was never an enum looks like.
func (m *Migrator) liveEnumSet(table, column string) ([]string, bool, error) {
	var ddl string
	err := m.db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
	).Scan(&ddl)
	if err != nil {
		return nil, false, err
	}
	for _, match := range checkInPattern.FindAllStringSubmatch(ddl, -1) {
		if !strings.EqualFold(match[1], column) {
			continue
		}
		var tags []string
		for _, raw := range strings.Split(match[2], ",") {
			raw = strings.TrimSpace(raw)
			if len(raw) < 2 || raw[0] != '\'' || raw[len(raw)-1] != '\'' {
				continue
			}
			tags = append(tags, strings.ReplaceAll(raw[1:len(raw)-1], "''", "'"))
		}
		return tags, true, nil
	}
	return nil, false, nil
}

// planEnumSets compares each enum column's declared constructors against the
// set its CHECK enforces, and says what to do about the difference.
//
// The rule is the one the whole migrator follows: do to stored data what the
// language does to a value of the old type written into a column of the new
// one. Widening accepts everything already there, so it applies. Narrowing
// applies when nothing in the table holds a departing tag, and blocks when
// something does — which surviving constructor those rows should say is a fact
// about the app, not about the schema.
//
// A RENAME arrives as one addition and one removal, and the order matters: the
// widening goes out FIRST and applies, so the operator's own
// `UPDATE t SET c = 'New' WHERE c = 'Old'` is not rejected by a constraint
// quoting a set their source no longer contains. The next boot then narrows.
func (m *Migrator) planEnumSets(ent VEntity, hasRows bool) ([]MigrationStep, error) {
	var steps []MigrationStep
	var blocked *MigrationStep

	widened := ent
	widenedFields := append([]EntityField(nil), ent.Fields...)
	needsWidening := false

	for i, f := range ent.Fields {
		if f.AcceptedCtors == nil {
			continue
		}
		live, ok, err := m.liveEnumSet(ent.Table, f.Name)
		if err != nil {
			return nil, err
		}
		if !ok || sameTagSet(live, f.AcceptedCtors) {
			continue
		}
		removed := missingFrom(live, f.AcceptedCtors)
		added := missingFrom(f.AcceptedCtors, live)

		if len(removed) > 0 {
			held, err := m.tagsStillHeld(ent.Table, f.Name, removed)
			if err != nil {
				return nil, err
			}
			if len(held) > 0 {
				if blocked == nil {
					blocked = &MigrationStep{
						Kind:  StepBlocked,
						Table: ent.Table,
						Error: enumTagStillHeldError(ent.Table, f.Name, held, f.AcceptedCtors),
					}
				}
				// A rename is an addition and a removal at once. Widen
				// first so the operator's UPDATE is not refused by the
				// old constraint, then let the block stop the boot.
				if len(added) > 0 {
					widenedFields[i].AcceptedCtors = union(live, f.AcceptedCtors)
					needsWidening = true
				}
				continue
			}
		}
		// Nothing departing is still held: the declared set is reachable
		// in one rebuild, whether it widened, narrowed, or both.
		widenedFields[i].AcceptedCtors = f.AcceptedCtors
		needsWidening = true
		_ = hasRows
	}

	if needsWidening {
		widened.Fields = widenedFields
		rebuild, err := m.buildRebuildSteps(widened)
		if err != nil {
			return nil, err
		}
		steps = append(steps, rebuild...)
	}
	if blocked != nil {
		steps = append(steps, *blocked)
	}
	return steps, nil
}

// buildRebuildSteps rewrites a table to a new definition, keeping every row.
// SQLite cannot alter a CHECK in place, so this is the copy-and-swap it
// documents, run as ONE transaction by applyStep: a failure part-way leaves
// the original table exactly as it was.
func (m *Migrator) buildRebuildSteps(ent VEntity) ([]MigrationStep, error) {
	tmp := ent.Table + "_mar_rebuild"
	cols := make([]string, len(ent.Fields))
	defs := make([]string, len(ent.Fields))
	for i, f := range ent.Fields {
		cols[i] = quoteIdent(f.Name)
		defs[i] = columnDefSQL(f)
	}
	list := strings.Join(cols, ", ")

	stmts := []string{
		"CREATE TABLE " + quoteIdent(tmp) + " (" + strings.Join(defs, ", ") + ")",
		"INSERT INTO " + quoteIdent(tmp) + " (" + list + ") SELECT " + list + " FROM " + quoteIdent(ent.Table),
		"DROP TABLE " + quoteIdent(ent.Table),
		"ALTER TABLE " + quoteIdent(tmp) + " RENAME TO " + quoteIdent(ent.Table),
	}
	// The indexes went with the old table; the declaration is the record of
	// which ones should exist.
	for _, idxCols := range ent.UniqueIndexes {
		name := uniqueIndexName(ent.Table, idxCols)
		stmts = append(stmts, buildCreateUniqueIndexSQL(ent.Table, name, idxCols))
	}
	return []MigrationStep{{
		Kind:        StepRecreateEmpty,
		Table:       ent.Table,
		SQLs:        stmts,
		Description: fmt.Sprintf("rewrite %s to match its declared constructor sets (keeping every row)", ent.Table),
	}}, nil
}

// tagsStillHeld returns which of the departing tags any row still holds.
func (m *Migrator) tagsStillHeld(table, column string, departing []string) ([]string, error) {
	var held []string
	for _, tag := range departing {
		var n int
		err := m.db.QueryRow(
			"SELECT count(*) FROM "+quoteIdent(table)+" WHERE "+quoteIdent(column)+" = ?", tag,
		).Scan(&n)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			held = append(held, fmt.Sprintf("%s (%d row%s)", tag, n, plural(n, "")))
		}
	}
	return held, nil
}

func enumTagStillHeldError(table, column string, held, declared []string) error {
	return &BlockedMigrationError{
		Summary: fmt.Sprintf(
			"migration blocked for entity `%s.%s`: rows still hold a constructor the type no longer declares: %s",
			table, column, strings.Join(held, ", ")),
		Hint: "Reading one of those rows fails, because the value is not one the type has.\n" +
			"Which of " + strings.Join(quoteEach(declared), ", ") + " they should say is a fact about\n" +
			"the app, so it is not something a migration can choose.\n\n" +
			"    UPDATE " + quoteIdent(table) + " SET " + quoteIdent(column) + " = '<value>' WHERE " +
			quoteIdent(column) + " NOT IN (" + quotedList(declared) + ");\n\n" +
			"Fill in a constructor and restart.",
	}
}

func quotedList(tags []string) string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = "'" + strings.ReplaceAll(t, "'", "''") + "'"
	}
	return strings.Join(out, ", ")
}

func sameTagSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		seen[x] = true
	}
	for _, y := range b {
		if !seen[y] {
			return false
		}
	}
	return true
}

// missingFrom returns the members of `from` that `to` does not contain.
func missingFrom(from, to []string) []string {
	has := map[string]bool{}
	for _, x := range to {
		has[x] = true
	}
	var out []string
	for _, x := range from {
		if !has[x] {
			out = append(out, x)
		}
	}
	return out
}

// union keeps the declared order and appends whatever the live set still has,
// which is what makes the transitional CHECK accept both spellings during a
// rename.
func union(live, declared []string) []string {
	out := append([]string(nil), declared...)
	has := map[string]bool{}
	for _, x := range declared {
		has[x] = true
	}
	for _, x := range live {
		if !has[x] {
			out = append(out, x)
		}
	}
	return out
}
