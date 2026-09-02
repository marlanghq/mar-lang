package jsserve

import (
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// admin.go says it plainly: "Each func returns a Mar Value whose shape MUST
// match the corresponding type scheme in typecheck/env.go". Nothing checked
// it. These are hand-built records -- mvRec with a list of field names and a
// map -- and the type they have to match lives in another package, so a field
// renamed on one side and not the other is a panel that renders nothing, found
// by a person.
//
// It reads like a cross-runtime promise and is not one: there is no second
// implementation. It is a value the runtime BUILDS against a type the checker
// declares, which is a wire boundary pointing the other way, and the wire
// checker already knows how to answer it.
func TestEveryAdminBodyMatchesItsDeclaredType(t *testing.T) {
	dir := t.TempDir()
	runtime.ResetForReload()
	runtime.SetDBPath(filepath.Join(dir, "admin_shape.db"))

	// A table, so the list-shaped answers are not all empty. An empty list
	// satisfies every list shape, so checking one proves nothing about its
	// elements -- which is the same vacuity this suite has been finding all
	// day, and it would be in the check rather than in the code.
	db, err := runtime.OpenDB()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO items (name) VALUES ('a')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	bodies := map[string]func() (runtime.Value, error){
		"Mar.Admin.serverInfo":     marServerInfo,
		"Mar.Admin.dbStats":        marDBStats,
		"Mar.Admin.recentRequests": marRecentRequests,
		"Mar.Admin.listEntities":   marListEntities,
		"Mar.Admin.listBackups":    marListBackups,
	}
	syms := typecheck.AllQualifiedSymbols()
	tags := typecheck.CtorsFromEnv(typecheck.BaseEnv())

	var nonEmptyLists int
	for name, body := range bodies {
		t.Run(strings.TrimPrefix(name, "Mar.Admin."), func(t *testing.T) {
			ty, ok := syms[name]
			if !ok {
				t.Fatalf("%s has no declared type", name)
			}
			want := adminResultShape(t, ty, tags)
			got, err := body()
			if err != nil {
				t.Fatalf("body: %v", err)
			}
			if l, isList := got.(runtime.VList); isList && len(l.Elements) > 0 {
				nonEmptyLists++
			}
			if err := runtime.CheckWire(got, want); err != nil {
				t.Errorf("the body's value does not match its declared type: %v\n  value: %s",
					err, got.Display())
			}
		})
	}
	if nonEmptyLists == 0 {
		t.Error("every list-shaped answer came back empty, so nothing was checked about " +
			"their elements; the table this test creates is not reaching the bodies")
	}
}

// The declared type is `AdminSession -> (Result String X -> msg) -> Cmd msg`,
// and X is the shape the body has to produce.
func adminResultShape(t *testing.T, ty typecheck.Type, tags typecheck.CtorLookup) ast.WireShape {
	t.Helper()
	arrow, ok := typecheck.StripForall(ty).(typecheck.TArrow)
	if !ok {
		t.Fatalf("not a function: %s", typecheck.Pretty(ty))
	}
	cb, ok := typecheck.StripForall(arrow.To).(typecheck.TArrow)
	if !ok {
		t.Fatalf("no callback: %s", typecheck.Pretty(ty))
	}
	inner, ok := typecheck.StripForall(cb.From).(typecheck.TArrow)
	if !ok {
		t.Fatalf("callback is not a function: %s", typecheck.Pretty(cb.From))
	}
	res, ok := typecheck.StripForall(inner.From).(typecheck.TCon)
	if !ok || res.Name != "Result" || len(res.Args) != 2 {
		t.Fatalf("not a Result: %s", typecheck.Pretty(inner.From))
	}
	return typecheck.WireShapeOf(res.Args[1], tags)
}
