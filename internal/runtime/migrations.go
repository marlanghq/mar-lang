package runtime

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"mar/internal/model"
)

type tableInfoRow struct {
	Name    string
	Type    string
	NotNull int
	PK      int
}

type foreignKeyInfoRow struct {
	FromColumn string
	ToTable    string
	ToColumn   string
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
	if err := r.migrateStaticTable("mar_auth_codes", []staticColumn{
		{Name: "id", Type: "INTEGER", Primary: true, Auto: true},
		{Name: "email", Type: "TEXT", NotNull: true},
		{Name: "user_id", Type: "INTEGER", NotNull: true},
		{Name: "code", Type: "TEXT", NotNull: true},
		{Name: "grant_role", Type: "TEXT"},
		{Name: "expires_at", Type: "INTEGER", NotNull: true},
		{Name: "used", Type: "INTEGER", NotNull: true, DefaultSQL: "0"},
		{Name: "created_at", Type: "INTEGER", NotNull: true},
	}); err != nil {
		return err
	}
	if err := r.migrateStaticTable("mar_sessions", []staticColumn{
		{Name: "token", Type: "TEXT", Primary: true},
		{Name: "user_id", Type: "INTEGER", NotNull: true},
		{Name: "email", Type: "TEXT", NotNull: true},
		{Name: "expires_at", Type: "INTEGER", NotNull: true},
		{Name: "revoked", Type: "INTEGER", NotNull: true, DefaultSQL: "0"},
		{Name: "created_at", Type: "INTEGER", NotNull: true},
	}); err != nil {
		return err
	}

	cfg := r.authConfig()
	if err := r.migrateUniqueNoCaseIndex(
		authEmailUniqueIndexName(r.authUser.Table, cfg.EmailField),
		r.authUser.Table,
		cfg.EmailField,
	); err != nil {
		return err
	}
	return nil
}

// ensureMigrationMetaTable creates the migration history table when missing.
func (r *Runtime) ensureMigrationMetaTable() error {
	_, err := r.DB.Exec(`
		CREATE TABLE IF NOT EXISTS mar_schema_migrations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			table_name TEXT NOT NULL,
			migration_kind TEXT NOT NULL,
			sql_text TEXT NOT NULL,
			applied_at INTEGER NOT NULL
		);
	`)
	return err
}

// recordMigration appends an applied migration statement to history.
func (r *Runtime) recordMigration(tableName, kind, sqlText string) error {
	_, err := r.DB.Exec(
		`INSERT INTO mar_schema_migrations (table_name, migration_kind, sql_text, applied_at) VALUES (?, ?, ?, CAST((julianday('now') - 2440587.5)*86400000 AS INTEGER))`,
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

func (r *Runtime) indexExists(indexName string) (bool, error) {
	_, ok, err := r.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ? LIMIT 1`, indexName)
	if err != nil {
		return false, err
	}
	return ok, nil
}

func (r *Runtime) tableHasRows(tableName string) (bool, error) {
	quoted, err := quoteIdentifier(tableName)
	if err != nil {
		return false, err
	}
	_, ok, err := r.DB.QueryRow("SELECT 1 FROM " + quoted + " LIMIT 1")
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

func (r *Runtime) readForeignKeyInfo(tableName string) ([]foreignKeyInfoRow, error) {
	quoted, err := quoteIdentifier(tableName)
	if err != nil {
		return nil, err
	}
	rows, err := r.DB.QueryRows("PRAGMA foreign_key_list(" + quoted + ")")
	if err != nil {
		return nil, err
	}

	info := make([]foreignKeyInfoRow, 0, len(rows))
	for _, item := range rows {
		info = append(info, foreignKeyInfoRow{
			FromColumn: fmt.Sprintf("%v", item["from"]),
			ToTable:    fmt.Sprintf("%v", item["table"]),
			ToColumn:   fmt.Sprintf("%v", item["to"]),
		})
	}
	return info, nil
}

// migrateEntityTable applies safe forward-only schema updates for an app entity table.
func (r *Runtime) migrateEntityTable(entity *model.Entity) error {
	exists, err := r.tableExists(entity.Table)
	if err != nil {
		return err
	}
	if !exists {
		cols := make([]string, 0, len(entity.Fields))
		for _, field := range entity.Fields {
			cols = append(cols, r.entityColumnDefinition(entity, &field))
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
	existingFKs, err := r.readForeignKeyInfo(entity.Table)
	if err != nil {
		return err
	}
	existingByName := map[string]tableInfoRow{}
	for _, row := range existing {
		existingByName[row.Name] = row
	}
	expected := map[string]bool{}

	for _, field := range entity.Fields {
		storageName := model.FieldStorageName(&field)
		expected[storageName] = true
		row, ok := existingByName[storageName]
		if field.RelationEntity != "" {
			if !ok {
				return r.blockRelationMigration(entity, &field, false)
			}
			if !r.hasExpectedForeignKey(existingFKs, entity, &field) {
				return r.blockRelationMigration(entity, &field, true)
			}
		}
		if !ok {
			if field.Primary || field.Auto {
				return fmt.Errorf("migration blocked for entity %s: cannot auto-add primary/auto field %q to existing table %s", entity.Name, storageName, entity.Table)
			}
			if !field.Optional && field.Default == nil {
				hasRows, err := r.tableHasRows(entity.Table)
				if err != nil {
					return err
				}
				if hasRows {
					return fmt.Errorf("migration blocked for entity %s: cannot auto-add required field %q (%s) to existing table %s", entity.Name, storageName, field.Type, entity.Table)
				}
			}
			table, _ := quoteIdentifier(entity.Table)
			sqlText := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", table, r.entityColumnDefinition(entity, &field))
			if _, err := r.DB.Exec(sqlText); err != nil {
				return err
			}
			if err := r.recordMigration(entity.Table, "add_column_"+storageName, sqlText); err != nil {
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
			fmt.Printf("[MarMigrate] Table %s has extra column %q not present in entity %s; keeping unchanged.\n", entity.Table, row.Name, entity.Name)
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

// migrateStaticTable applies safe migrations for Mar internal auth tables.
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
		expectedField := model.Field{Name: col.Name, Type: staticTypeToMar(col.Type), Primary: col.Primary, Optional: !col.NotNull}
		if err := assertCompatibleColumn(tableName, tableName, &expectedField, row); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) migrateUniqueNoCaseIndex(indexName, tableName, fieldName string) error {
	exists, err := r.indexExists(indexName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	index, err := quoteIdentifier(indexName)
	if err != nil {
		return err
	}
	table, err := quoteIdentifier(tableName)
	if err != nil {
		return err
	}
	field, err := quoteIdentifier(fieldName)
	if err != nil {
		return err
	}
	sqlText := fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s(%s COLLATE NOCASE);", index, table, field)
	if _, err := r.DB.Exec(sqlText); err != nil {
		return fmt.Errorf("migration blocked for %s.%s: duplicate values prevent unique index creation in table %s", tableName, fieldName, tableName)
	}
	return r.recordMigration(tableName, "create_index_"+indexName, sqlText)
}

func authEmailUniqueIndexName(tableName, emailField string) string {
	sanitize := func(value string) string {
		builder := strings.Builder{}
		for _, ch := range strings.TrimSpace(value) {
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
				builder.WriteRune(ch)
			} else {
				builder.WriteRune('_')
			}
		}
		out := strings.Trim(builder.String(), "_")
		if out == "" {
			return "x"
		}
		return strings.ToLower(out)
	}
	return fmt.Sprintf("idx_%s_%s_unique", sanitize(tableName), sanitize(emailField))
}

func staticTypeToMar(sqlType string) string {
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

// assertCompatibleColumn blocks startup when the live schema drifts in a non-safe direction.
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

func (r *Runtime) entityColumnDefinition(entity *model.Entity, field *model.Field) string {
	name, _ := quoteIdentifier(model.FieldStorageName(field))
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
	if defaultSQL, ok := fieldDefaultSQL(field); ok {
		parts = append(parts, "DEFAULT "+defaultSQL)
	}
	if field.RelationEntity != "" {
		target := r.relationTargetEntity(field)
		if target != nil {
			targetTable, _ := quoteIdentifier(target.Table)
			targetPrimary, _ := quoteIdentifier(model.FieldStorageName(primaryField(target)))
			parts = append(parts, fmt.Sprintf("REFERENCES %s(%s)", targetTable, targetPrimary))
		}
	}
	return strings.Join(parts, " ")
}

func (r *Runtime) relationTargetEntity(field *model.Field) *model.Entity {
	if r == nil || r.App == nil || field == nil || field.RelationEntity == "" {
		return nil
	}
	for i := range r.App.Entities {
		if r.App.Entities[i].Name == field.RelationEntity {
			return &r.App.Entities[i]
		}
	}
	return nil
}

func (r *Runtime) hasExpectedForeignKey(existing []foreignKeyInfoRow, entity *model.Entity, field *model.Field) bool {
	target := r.relationTargetEntity(field)
	if entity == nil || field == nil || target == nil {
		return false
	}
	expectedFrom := model.FieldStorageName(field)
	expectedTable := target.Table
	expectedTo := model.FieldStorageName(primaryField(target))
	for _, fk := range existing {
		if strings.EqualFold(fk.FromColumn, expectedFrom) &&
			strings.EqualFold(fk.ToTable, expectedTable) &&
			strings.EqualFold(fk.ToColumn, expectedTo) {
			return true
		}
	}
	return false
}

func (r *Runtime) blockRelationMigration(entity *model.Entity, field *model.Field, relationColumnExists bool) error {
	target := r.relationTargetEntity(field)
	if entity == nil || field == nil || target == nil {
		return fmt.Errorf("migration blocked for entity %s: relation %q could not be resolved", entity.Name, field.Name)
	}

	sourceTable := entity.Table
	sourceColumn := model.FieldStorageName(field)
	targetTable := target.Table
	targetColumn := model.FieldStorageName(primaryField(target))

	var b strings.Builder
	fmt.Fprintf(&b, "migration blocked for entity %s: table %q already exists, and relation %q requires a foreign key %s.%s -> %s.%s\n\n",
		entity.Name,
		sourceTable,
		field.Name,
		sourceTable,
		sourceColumn,
		targetTable,
		targetColumn,
	)
	fmt.Fprintf(&b, "SQLite cannot add this foreign key with ALTER TABLE, so Mar does not migrate it automatically.\n\n")
	fmt.Fprintf(&b, "Hint:\n")
	fmt.Fprintf(&b, "  Migrate the table manually, then restart the app.\n")
	fmt.Fprintf(&b, "  Suggested Manual Migration SQL:\n")
	for _, line := range strings.Split(r.manualRelationMigrationSQL(entity, field, target, relationColumnExists), "\n") {
		if strings.TrimSpace(line) == "" {
			fmt.Fprintf(&b, "\n")
		} else {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

func (r *Runtime) manualRelationMigrationSQL(entity *model.Entity, relationField *model.Field, target *model.Entity, relationColumnExists bool) string {
	newTableName := entity.Table + "_new"

	columnDefs := make([]string, 0, len(entity.Fields))
	insertColumns := make([]string, 0, len(entity.Fields))
	selectColumns := make([]string, 0, len(entity.Fields))

	for _, field := range entity.Fields {
		columnDefs = append(columnDefs, "  "+r.entityColumnDefinition(entity, &field))
		storageName := model.FieldStorageName(&field)
		insertColumns = append(insertColumns, storageName)

		if field.RelationEntity != "" && field.Name == relationField.Name {
			if relationColumnExists {
				selectColumns = append(selectColumns, storageName)
			} else if field.Optional {
				selectColumns = append(selectColumns, "NULL AS "+storageName)
			} else {
				selectColumns = append(
					selectColumns,
					"/* replace NULL with a valid "+target.Table+"."+model.FieldStorageName(primaryField(target))+" value */ NULL AS "+storageName,
				)
			}
		} else {
			selectColumns = append(selectColumns, storageName)
		}
	}

	return strings.Join(
		[]string{
			"CREATE TABLE " + newTableName + " (",
			strings.Join(columnDefs, ",\n"),
			");",
			"",
			"INSERT INTO " + newTableName + " (" + strings.Join(insertColumns, ", ") + ")",
			"SELECT " + strings.Join(selectColumns, ", "),
			"FROM " + entity.Table + ";",
			"",
			"DROP TABLE " + entity.Table + ";",
			"ALTER TABLE " + newTableName + " RENAME TO " + entity.Table + ";",
		},
		"\n",
	)
}

func fieldDefaultSQL(field *model.Field) (string, bool) {
	if field.Default == nil {
		return "", false
	}
	switch field.Type {
	case "String":
		text, ok := field.Default.(string)
		if !ok {
			return "", false
		}
		return "'" + strings.ReplaceAll(text, "'", "''") + "'", true
	case "Bool":
		boolean, ok := field.Default.(bool)
		if !ok {
			return "", false
		}
		if boolean {
			return "1", true
		}
		return "0", true
	case "Int", "Posix":
		number, ok := toInt64(field.Default)
		if !ok {
			return "", false
		}
		return strconv.FormatInt(number, 10), true
	case "Float":
		number, ok := toFloat64(field.Default)
		if !ok {
			return "", false
		}
		return strconv.FormatFloat(number, 'f', -1, 64), true
	default:
		return "", false
	}
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
