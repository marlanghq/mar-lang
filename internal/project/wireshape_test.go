package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/runtime"
)

// End to end: a service declared with a record request must arrive at the
// dispatcher carrying the shape of that record, or the 422 never happens.
func TestServiceCarriesItsDeclaredShape(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	src := `module Main exposing (addNap)

addNap : Service { title : String, minutes : Int } String
addNap = Service.declare POST "/api/naps"
`
	if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	rEnv, _, err := LoadIntoEnvForRuntime(entry, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	v, ok := rEnv.Lookup("Main.addNap")
	if !ok {
		t.Fatal("addNap not bound")
	}
	svc, ok := v.(runtime.VService)
	if !ok {
		t.Fatalf("expected a service, got %T", v)
	}
	if svc.InputShape == nil {
		t.Fatal("the service reached the runtime without its request shape")
	}
	if svc.InputShape.Kind != runtime.WireRecord {
		t.Fatalf("shape kind = %s, want Record", svc.InputShape.Kind)
	}
	got := map[string]runtime.WireKind{}
	for _, f := range svc.InputShape.Fields {
		got[f.Name] = f.Shape.Kind
	}
	if got["title"] != runtime.WireString || got["minutes"] != runtime.WireInt {
		t.Fatalf("fields = %v, want title:String minutes:Int", got)
	}

	// And the shape has to actually reject the thing it exists for.
	bad := runtime.VRecord{
		Fields: map[string]runtime.Value{
			"title":   runtime.VString{V: "nap"},
			"minutes": runtime.VString{V: "soon"},
		},
		Order: []string{"title", "minutes"},
	}
	err = runtime.CheckWire(bad, *svc.InputShape)
	if err == nil {
		t.Fatal("a String where Int is declared should be rejected")
	}
	if !strings.Contains(err.Error(), "minutes") {
		t.Errorf("the error should name the field, got: %v", err)
	}
}
