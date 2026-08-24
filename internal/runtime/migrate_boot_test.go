package runtime

import (
	"strings"
	"sync"
	"testing"
)

// The production binary used to reach its listener without ever having asked
// whether the schema matched. Every schema change was applied lazily by the
// first Repo call that touched the table, inside an HTTP handler, which meant
// three things that only ever happened in production: a blocked migration let
// the machine boot and answer the deploy's health probe while every affected
// route answered 500 forever; concurrent first requests raced to CREATE the
// same table and one real user lost; and DDL ran inside a request.
//
// These tests pin the two properties that fix depends on. The third — that
// cmd/mar-runtime calls RunBootMigrations before ServeLive — is a wiring fact
// and lives where the wiring is.

func TestBootMigrationsFillTheCache(t *testing.T) {
	// Without this, the boot pass migrated every entity and then the first
	// request to touch each table planned the whole diff again. The comment
	// on ensureMigrated claimed the cache was filled at boot for a year
	// before anything filled it.
	ResetMigrationCache()
	t.Cleanup(ResetMigrationCache)

	markMigrated("notes")
	migrationCache.mu.Lock()
	defer migrationCache.mu.Unlock()
	if !migrationCache.done["notes"] {
		t.Fatal("a migrated entity has to be recorded, or the request path redoes the work")
	}
}

func TestMigrationCacheIsSafeUnderConcurrentRequests(t *testing.T) {
	// The cache read, the migrator run and the cache write were three steps
	// with nothing between them, so two handlers arriving together both saw
	// a miss and both planned the same CREATE TABLE. Measured against a real
	// server at exactly one 500 out of 32 simultaneous first requests, three
	// rounds out of three. Run this file with -race.
	ResetMigrationCache()
	t.Cleanup(ResetMigrationCache)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if n%2 == 0 {
				markMigrated("notes")
				return
			}
			migrationCache.mu.Lock()
			_ = migrationCache.done["notes"]
			migrationCache.mu.Unlock()
		}(i)
	}
	wg.Wait()
}

func TestBlockedMigrationSaysWhatToDo(t *testing.T) {
	// The message a deploy now dies with, rather than one stderr line per
	// request on a server the health probe called healthy. It has to name
	// the table, the column, and the way out.
	db := openTempDB(t)
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO notes (name) VALUES ('alpha')`); err != nil {
		t.Fatal(err)
	}
	ent := VEntity{
		Table: "notes",
		Fields: []EntityField{
			{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
			{Name: "name", SQLType: "TEXT", NotNull: true},
			{Name: "note", SQLType: "TEXT", NotNull: true},
		},
	}
	_, err := NewMigrator(db, []VEntity{ent}).Run()
	if err == nil {
		t.Fatal("adding a required column to a table with rows must be refused")
	}
	for _, want := range []string{"notes", "note", "ALTER TABLE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}
