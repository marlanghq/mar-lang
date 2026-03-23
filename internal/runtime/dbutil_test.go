package runtime

import "mar/internal/sqlitecli"

func queryRow(db *sqlitecli.DB, query string, args ...any) (map[string]any, bool, error) {
	return db.QueryRow(query, args...)
}
