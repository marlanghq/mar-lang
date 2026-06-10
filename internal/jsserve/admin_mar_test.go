package jsserve

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"mar/internal/runtime"
)

// The Mar.Admin.* runtime bodies read the SAME introspection helpers as the
// JSON handlers. These tests pin the Mar Value shapes, so a Page.adminProtected
// program that destructures them keeps compiling against real server output.

func TestMarServerInfo_Shape(t *testing.T) {
	v, err := marServerInfo()
	if err != nil {
		t.Fatalf("marServerInfo: %v", err)
	}
	rec, ok := v.(runtime.VRecord)
	if !ok {
		t.Fatalf("want VRecord, got %T", v)
	}
	for _, f := range []string{"marVersion", "goVersion", "buildTarget", "bootedAtMs", "requestsTotal", "requestsInFlight"} {
		if _, ok := rec.Fields[f]; !ok {
			t.Errorf("serverInfo missing field %q", f)
		}
	}
}

func TestMarListEntitiesAndRows(t *testing.T) {
	_, cleanup := adminTestServer(t, []string{"a@x.com"})
	defer cleanup()

	db, _ := runtime.OpenDB()
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes (id, body) VALUES (1, 'hello')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// listEntities → includes `notes` with its 2 columns.
	ents, err := marListEntities()
	if err != nil {
		t.Fatalf("marListEntities: %v", err)
	}
	entList, ok := ents.(runtime.VList)
	if !ok {
		t.Fatalf("listEntities: want VList, got %T", ents)
	}
	var found bool
	for _, e := range entList.Elements {
		rec, ok := e.(runtime.VRecord)
		if !ok {
			t.Fatalf("entity: want VRecord, got %T", e)
		}
		if name, _ := rec.Fields["name"].(runtime.VString); name.V == "notes" {
			found = true
			cols, ok := rec.Fields["columns"].(runtime.VList)
			if !ok || len(cols.Elements) != 2 {
				t.Errorf("notes columns: want a 2-element VList, got %#v", rec.Fields["columns"])
			}
		}
	}
	if !found {
		t.Error("listEntities should include `notes`")
	}

	// listEntityRows("notes") → one Dict row {body:"hello", id:"1"}.
	rows, err := marListEntityRows("notes")
	if err != nil {
		t.Fatalf("marListEntityRows: %v", err)
	}
	rowList, ok := rows.(runtime.VList)
	if !ok || len(rowList.Elements) != 1 {
		t.Fatalf("listEntityRows: want a 1-element VList, got %#v", rows)
	}
	dict, ok := rowList.Elements[0].(runtime.VDict)
	if !ok {
		t.Fatalf("row: want VDict, got %T", rowList.Elements[0])
	}
	got := map[string]string{}
	for _, p := range dict.Pairs {
		got[p.Key.(runtime.VString).V] = p.Value.(runtime.VString).V
	}
	if got["id"] != "1" || got["body"] != "hello" {
		t.Errorf("row cells: got %v, want id=1 body=hello", got)
	}

	// The entity name is whitelisted against the live schema — an unknown
	// table can't pivot arbitrary SQL.
	if _, err := marListEntityRows("no_such_table"); err == nil {
		t.Error("listEntityRows should reject an unknown entity")
	}
}

// The /_mar/admin/api/mar/* transport serializes bodies with
// runtime.EncodeValueJSON — the same wire format jsToMar decodes. Pin that
// serverInfo round-trips to valid JSON carrying every field.
func TestMarServerInfo_WireJSON(t *testing.T) {
	v, err := marServerInfo()
	if err != nil {
		t.Fatalf("marServerInfo: %v", err)
	}
	body, err := runtime.EncodeValueJSON(v)
	if err != nil {
		t.Fatalf("EncodeValueJSON: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("wire JSON should be valid: %v\n%s", err, body)
	}
	for _, k := range []string{"marVersion", "goVersion", "buildTarget", "bootedAtMs", "requestsTotal", "requestsInFlight"} {
		if _, ok := decoded[k]; !ok {
			t.Errorf("wire JSON missing field %q", k)
		}
	}
}

// Rows are Dicts (dynamic columns), so they MUST encode with the __dict marker
// — that's the only shape jsToMar rebuilds back into a Mar Dict. A plain JSON
// object would silently decode as a record and break the panel.
func TestMarListEntityRows_WireDictMarker(t *testing.T) {
	_, cleanup := adminTestServer(t, []string{"a@x.com"})
	defer cleanup()

	db, _ := runtime.OpenDB()
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes (id, body) VALUES (1, 'hi')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	v, err := marListEntityRows("notes")
	if err != nil {
		t.Fatalf("marListEntityRows: %v", err)
	}
	body, err := runtime.EncodeValueJSON(v)
	if err != nil {
		t.Fatalf("EncodeValueJSON: %v", err)
	}
	if !strings.Contains(body, "__dict") {
		t.Errorf("entity rows should encode dicts with the __dict marker, got: %s", body)
	}
}

// The embedded Mar admin panel must parse, typecheck (against the real BaseEnv
// — Page.adminProtected, the Mar.Admin.* toMsg signatures, the UI vocabulary)
// and serialize to a mountable program.json. This is the proof that the whole
// capability is usable from actual Mar code, and it guards the panel source
// from drifting out of sync with the type schemes.
func TestAdminMarPanelCompiles(t *testing.T) {
	body, err := adminPanelProgram()
	if err != nil {
		t.Fatalf("embedded admin panel should compile: %v", err)
	}
	var prog map[string]any
	if err := json.Unmarshal(body, &prog); err != nil {
		t.Fatalf("panel program.json should be valid JSON: %v", err)
	}
	if prog["entry"] != "__entry" {
		t.Errorf("panel entry should be \"__entry\" (the synthetic frontend entry), got %v", prog["entry"])
	}
	// The bundle must be the named Admin.Panel module PLUS a synthetic
	// nameless module (carrying `__entry`). Nameless is the whole point: the
	// frontend runtime resolves the program entry from the shared env, and
	// only a nameless module binds its decls there — a named module's decls
	// land in a private frame the lookup can't see ("entry not found: main").
	mods, ok := prog["modules"].([]any)
	if !ok || len(mods) < 2 {
		t.Fatalf("panel program should carry the panel + synthetic entry module, got %v", prog["modules"])
	}
	names := make([]string, 0, len(mods))
	for _, m := range mods {
		mm, _ := m.(map[string]any)
		parts := []string{}
		if arr, ok := mm["name"].([]any); ok {
			for _, p := range arr {
				if s, ok := p.(string); ok {
					parts = append(parts, s)
				}
			}
		}
		names = append(names, strings.Join(parts, "."))
	}
	foundPanel, foundEntry := false, false
	for _, n := range names {
		if n == "Admin.Panel" {
			foundPanel = true
		}
		if n == "" {
			foundEntry = true
		}
	}
	if !foundPanel || !foundEntry {
		t.Fatalf("bundle should be [Admin.Panel module, synthetic nameless __entry module]; got module names %v", names)
	}
}

// End-to-end serving: the shell HTML loads runtime.js and points the program
// promise at the panel's own program.json; that route returns the compiled,
// mountable program.
func TestAdminMarPanelServed(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"a@x.com"})
	defer cleanup()
	client := server.Client()

	resp, err := client.Get(server.URL + "/_mar/admin")
	if err != nil {
		t.Fatalf("GET shell: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("shell status: %d", resp.StatusCode)
	}
	shell, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(shell), "/_mar/admin/program.json") {
		t.Error("shell should point the program promise at the panel program.json")
	}
	if !strings.Contains(string(shell), "/_mar/runtime.js") {
		t.Error("shell should load the shared runtime.js")
	}

	resp2, err := client.Get(server.URL + "/_mar/admin/program.json")
	if err != nil {
		t.Fatalf("GET program.json: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("program.json status: %d", resp2.StatusCode)
	}
	var prog map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&prog); err != nil {
		t.Fatalf("served program.json should be valid: %v", err)
	}
	if prog["entry"] != "__entry" {
		t.Errorf("served panel entry should be \"__entry\", got %v", prog["entry"])
	}
}

// TestMarDBStats_SplitsFrameworkFromBusiness pins the two-bucket split:
// user tables land under `entities`, reserved _mar_* tables under
// `frameworkTables`. The split logic lives only in marDBStats now (the
// retired SPA's /api/db-stats handler used to carry a parallel copy).
func TestMarDBStats_SplitsFrameworkFromBusiness(t *testing.T) {
	_, cleanup := adminTestServer(t, []string{"a@x.com", "b@x.com"})
	defer cleanup()

	db, _ := runtime.OpenDB()
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	v, err := marDBStats()
	if err != nil {
		t.Fatalf("marDBStats: %v", err)
	}
	rec, ok := v.(runtime.VRecord)
	if !ok {
		t.Fatalf("want VRecord, got %T", v)
	}

	tableNames := func(field string) []string {
		list, _ := rec.Fields[field].(runtime.VList)
		var out []string
		for _, e := range list.Elements {
			if r, ok := e.(runtime.VRecord); ok {
				if n, ok := r.Fields["name"].(runtime.VString); ok {
					out = append(out, n.V)
				}
			}
		}
		return out
	}

	business := tableNames("entities")
	foundNotes := false
	for _, n := range business {
		if n == "notes" {
			foundNotes = true
		}
		if strings.HasPrefix(n, "_mar_") {
			t.Errorf("entities should not contain framework table %q", n)
		}
	}
	if !foundNotes {
		t.Errorf("entities should include user table `notes`; got %v", business)
	}

	framework := tableNames("frameworkTables")
	foundAdmins := false
	for _, n := range framework {
		if n == "_mar_admins" {
			foundAdmins = true
		}
		if !strings.HasPrefix(n, "_mar_") {
			t.Errorf("frameworkTables should only contain _mar_* tables; found %q", n)
		}
	}
	if !foundAdmins {
		t.Errorf("frameworkTables should include _mar_admins; got %v", framework)
	}
}
