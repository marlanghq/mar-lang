package apphost

import (
	"strings"
	"testing"

	"mar/internal/runtime"
)

// A service contract that is not a top-level binding never had its declared
// request type reduced to a shape: the stamping happens where the loader can
// see a value and its declaration together, which is at a named declaration.
// So an inline `Service.declare` mounted a route whose body was never
// validated against anything, and the handler was handed a value its own type
// forbids.
//
// Measured before this check existed: one unauthenticated POST of
// {"body":42,"weight":"pesado"} to an entity-backed inline service answered
// 500 AND wrote the row, putting text in a column declared Int, after which
// every read of that table fails. The app had booted clean.
//
// Pages have refused exactly this since they were written, in the function ten
// lines above ExtractServices. Services went through the sibling and got no
// such check, and that asymmetry is the whole bug.

func exposed(shape *runtime.WireShape) runtime.VRecord {
	svc := runtime.VService{Verb: "POST", Path: "/api/notes", InputShape: shape}
	return runtime.VRecord{
		Fields: map[string]runtime.Value{
			"services": runtime.VList{Elements: []runtime.Value{
				runtime.VExposedService{Service: svc},
			}},
		},
		Order: []string{"services"},
	}
}

func TestInlineServiceIsRefusedAtBoot(t *testing.T) {
	_, err := ExtractServices(exposed(nil), "App.backend")
	if err == nil {
		t.Fatal("a contract with no request shape mounts a route that checks nothing")
	}
	for _, want := range []string{"POST", "/api/notes", "top-level binding"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got:\n%s", want, err)
		}
	}
}

func TestTopLevelServiceStillMounts(t *testing.T) {
	// The floor: every service in every example is a top-level binding and
	// carries a shape, so this check cannot refuse a program that works.
	shape := runtime.WireShape{Kind: runtime.WireRecord}
	routes, err := ExtractServices(exposed(&shape), "App.backend")
	if err != nil {
		t.Fatalf("a stamped contract must mount: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected one route, got %d", len(routes))
	}
}

// A GET or DELETE contract whose request is not a record is refused at boot
// too, which covers a verb the checker cannot read off the declaration (a
// verb passed through a variable or a pipe): here only the verb and the
// stamped shape count.
func TestABodilessServiceThatTakesNoRecordIsRefusedAtBoot(t *testing.T) {
	mount := func(verb string, kind runtime.WireKind) error {
		shape := runtime.WireShape{Kind: kind}
		svc := runtime.VService{Verb: verb, Path: "/api/notes", InputShape: &shape}
		_, err := ExtractServices(runtime.VRecord{
			Fields: map[string]runtime.Value{"services": runtime.VList{Elements: []runtime.Value{
				runtime.VExposedService{Service: svc},
			}}},
			Order: []string{"services"},
		}, "App.backend")
		return err
	}
	for _, verb := range []string{"GET", "DELETE"} {
		for _, kind := range []runtime.WireKind{runtime.WireInt, runtime.WireMaybe, runtime.WireList, runtime.WireCtor} {
			err := mount(verb, kind)
			if err == nil {
				t.Errorf("%s with a %s request mounted; no client can send it", verb, kind)
				continue
			}
			for _, want := range []string{verb, "/api/notes", "not a record"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the message must mention %q, got:\n%s", want, err)
				}
			}
		}
		for _, kind := range []runtime.WireKind{runtime.WireRecord, runtime.WireUnit} {
			if err := mount(verb, kind); err != nil {
				t.Errorf("%s with a %s request is an ordinary contract: %v", verb, kind, err)
			}
		}
	}
	if err := mount("POST", runtime.WireInt); err != nil {
		t.Errorf("a POST carries any value in its body: %v", err)
	}
}
