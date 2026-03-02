package runtime

import (
	"fmt"
	"strings"

	"belm/internal/model"
)

type tableInfoRow struct {
	Name    string
	Type    string
	NotNull int
	PK      int
}

func (r *Runtime) runMigrations() error {
	if err := r.ensureMigrationMetaTable(); err != nil {
		return err
	}
	for i := range r.App.Entities {
		entity := &r.App.Entities[i]
		if err := r.migrateEntityTable(entity); err != nil {
			return err
		}
	}
	if r.authEnabled() {
		if err := r.migrateStaticTable("belm_auth_codes", []staticColumn{
			{Name: "id", Type: "INTEGER", Primary: true, Auto: true},
			{Name: "email", Type: "TEXT", NotNull: true},
			{Name: "user_id", Type: "INTEGER", NotNull: true},
			{Name: "code", Type: "TEXT", NotNull: true},
			{Name: "expires_at", Type: "INTEGER", NotNull: true},
			{Name: "used", Type: "INTEGER", NotNull: true, DefaultSQL: "0"},
			{Name: "created_at", Type: "INTEGER", NotNull: true},
		}); err != nil {
			return err
		}
		if err := r.migrateStaticTable("belm_sessions", []staticColumn{
			{Name: "token", Type: "TEXT", Primary: true},
			{Name: "user_id", Type: "INTEGER", NotNull: true},
			{Name: "email", Type: "TEXT", NotNull: true},
			{Name: "expires_at", Type: "INTEGER", NotNull: true},
			{Name: "revoked", Type: "INTEGER", NotNull: true, DefaultSQL: "0"},
			{Name: "created_at", Type: "INTEGER", NotNull: true},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) ensureMigrationMetaTable() error {
	_, err := r.DB.Exec(`
		CREATE TABLE IF NOT EXISTS belm_schema_migrations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			table_name TEXT NOT NULL,
			migration_kind TEXT NOT NULL,
			sql_text TEXT NOT NULL,
			applied_at INTEGER NOT NULL
		);
	`)
	return err
}

func (r *Runtime) recordMigration(tableName, kind, sqlText string) error {
	_, err := r.DB.Exec(
		`INSERT INTO belm_schema_migrations (table_name, migration_kind, sql_text, applied_at) VALUES (?, ?, ?, CAST((julianday('now') - 2440587.5)*86400000 AS INTEGER))`,
		tableName,
		kind,
		sqlText,
	)
	return err
}

func (r *Runtime) tableExists(tableName string) (bool, error) {
	_, ok, err := r.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ? LIMIT 1`, tableName)
	if err != nil {
		return false, err
	}
	return ok, nil
}

func (r *Runtime) readTableInfo(tableName string) ([]tableInfoRow, error) {
	quoted, err := quoteIdentifier(tableName)
	if err != nil {
		return nil, err
	}
	rows, err := r.DB.QueryRows("PRAGMA table_info(" + quoted + ")")
	if err != nil {
		return nil, err
	}

	info := make([]tableInfoRow, 0, 16)
	for _, item := range rows {
		row := tableInfoRow{
			Name:    fmt.Sprintf("%v", item["name"]),
			Type:    fmt.Sprintf("%v", item["type"]),
			NotNull: int(sqliteToInt(item["notnull"])),
			PK:      int(sqliteToInt(item["pk"])),
		}
		info = append(info, row)
	}
	return info, nil
}

func (r *Runtime) migrateEntityTable(entity *model.Entity) error {
	exists, err := r.tableExists(entity.Table)
	if err != nil {
		return err
	}
	if !exists {
		cols := make([]string, 0, len(entity.Fields))
		for _, field := range entity.Fields {
			cols = append(cols, entityColumnDefinition(&field))
		}
		table, _ := quoteIdentifier(entity.Table)
		sqlText := fmt.Sprintf("CREATE TABLE %s (%s);", table, strings.Join(cols, ", "))
		if _, err := r.DB.Exec(sqlText); err != nil {
			return err
		}
		return r.recordMigration(entity.Table, "create_table", sqlText)
	}

	existing, err := r.readTableInfo(entity.Table)
	if err != nil {
		return err
	}
	existingByName := map[string]tableInfoRow{}
	for _, row := range existing {
		existingByName[row.Name] = row
	}
	expected := map[string]bool{}

	for _, field := range entity.Fields {
		expected[field.Name] = true
		row, ok := existingByName[field.Name]
		if !ok {
			if field.Primary || field.Auto {
				return fmt.Errorf("migration blocked for entity %s: cannot auto-add primary/auto field %q to existing table %s", entity.Name, field.Name, entity.Table)
			}
			if !field.Optional {
				return fmt.Errorf("migration blocked for entity %s: cannot auto-add required field %q to existing table %s", entity.Name, field.Name, entity.Table)
			}
			table, _ := quoteIdentifier(entity.Table)
			sqlText := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", table, entityColumnDefinition(&field))
			if _, err := r.DB.Exec(sqlText); err != nil {
				return err
			}
			if err := r.recordMigration(entity.Table, "add_column_"+field.Name, sqlText); err != nil {
				return err
			}
			continue
		}
		if err := assertCompatibleColumn(entity.Name, entity.Table, &field, row); err != nil {
			return err
		}
	}

	for _, row := range existing {
		if !expected[row.Name] {
			fmt.Printf("[BelmMigrate] Table %s has extra column %q not present in entity %s; keeping unchanged.\n", entity.Table, row.Name, entity.Name)
		}
	}
	return nil
}

func sqliteToInt(v any) int64 {
	n, _ := toInt64(v)
	return n
}

type staticColumn struct {
	Name       string
	Type       string
	Primary    bool
	Auto       bool
	NotNull    bool
	DefaultSQL string
}

func (r *Runtime) migrateStaticTable(tableName string, columns []staticColumn) error {
	exists, err := r.tableExists(tableName)
	if err != nil {
		return err
	}
	if !exists {
		defs := make([]string, 0, len(columns))
		for _, col := range columns {
			defs = append(defs, staticColumnDefinition(col))
		}
		table, _ := quoteIdentifier(tableName)
		sqlText := fmt.Sprintf("CREATE TABLE %s (%s);", table, strings.Join(defs, ", "))
		if _, err := r.DB.Exec(sqlText); err != nil {
			return err
		}
		return r.recordMigration(tableName, "create_table", sqlText)
	}

	existing, err := r.readTableInfo(tableName)
	if err != nil {
		return err
	}
	byName := map[string]tableInfoRow{}
	for _, row := range existing {
		byName[row.Name] = row
	}
	for _, col := range columns {
		row, ok := byName[col.Name]
		if !ok {
			if col.Primary || (col.NotNull && col.DefaultSQL == "") {
				return fmt.Errorf("migration blocked for internal table %s: cannot auto-add strict column %q", tableName, col.Name)
			}
			table, _ := quoteIdentifier(tableName)
			sqlText := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", table, staticColumnDefinition(col))
			if _, err := r.DB.Exec(sqlText); err != nil {
				return err
			}
			if err := r.recordMigration(tableName, "add_column_"+col.Name, sqlText); err != nil {
				return err
			}
			continue
		}
		expectedField := model.Field{Name: col.Name, Type: staticTypeToBelm(col.Type), Primary: col.Primary, Optional: !col.NotNull}
		if err := assertCompatibleColumn(tableName, tableName, &expectedField, row); err != nil {
			return err
		}
	}
	return nil
}

func staticTypeToBelm(sqlType string) string {
	switch strings.ToUpper(strings.TrimSpace(sqlType)) {
	case "INTEGER":
		return "Int"
	case "REAL":
		return "Float"
	case "TEXT":
		return "String"
	default:
		return "String"
	}
}

func assertCompatibleColumn(entityName, tableName string, field *model.Field, existing tableInfoRow) error {
	existingType := strings.ToUpper(strings.TrimSpace(existing.Type))
	expectedType := strings.ToUpper(typeToSQLite(field.Type))
	if existingType != "" && expectedType != "" && existingType != expectedType {
		return fmt.Errorf("migration blocked for %s.%s: type changed from %s to %s in table %s", entityName, field.Name, existingType, expectedType, tableName)
	}
	existingPK := existing.PK > 0
	if existingPK != field.Primary {
		return fmt.Errorf("migration blocked for %s.%s: primary key shape changed in table %s", entityName, field.Name, tableName)
	}
	if !field.Primary {
		existingNotNull := existing.NotNull == 1
		expectedNotNull := !field.Optional
		if existingNotNull != expectedNotNull {
			return fmt.Errorf("migration blocked for %s.%s: nullability changed in table %s", entityName, field.Name, tableName)
		}
	}
	return nil
}

func entityColumnDefinition(field *model.Field) string {
	name, _ := quoteIdentifier(field.Name)
	parts := []string{name, typeToSQLite(field.Type)}
	if field.Primary {
		parts = append(parts, "PRIMARY KEY")
	}
	if field.Auto {
		parts = append(parts, "AUTOINCREMENT")
	}
	if !field.Optional && !field.Primary {
		parts = append(parts, "NOT NULL")
	}
	return strings.Join(parts, " ")
}

func staticColumnDefinition(column staticColumn) string {
	name, _ := quoteIdentifier(column.Name)
	parts := []string{name, strings.ToUpper(strings.TrimSpace(column.Type))}
	if column.Primary {
		parts = append(parts, "PRIMARY KEY")
	}
	if column.Auto {
		parts = append(parts, "AUTOINCREMENT")
	}
	if column.NotNull && !column.Primary {
		parts = append(parts, "NOT NULL")
	}
	if column.DefaultSQL != "" {
		parts = append(parts, "DEFAULT "+column.DefaultSQL)
	}
	return strings.Join(parts, " ")
}
