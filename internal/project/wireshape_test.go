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

// The test that was missing, and the reason it was missing is the bug.
//
// The shape guard existed, was correct, and ran on the deployed load path —
// and the loader `mar dev`, `mar build` and the iOS bundler use passed a
// literal nil where the checked types belonged, so every service reached the
// dispatcher with no shape at all. Same sources, same request, 422 in
// production and 500 locally, for a month.
//
// One test drove one loader. So this one drives both and demands they agree,
// which is the only property that actually matters here.
func TestBothLoadersStampTheSameShape(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	src := `module Main exposing (addNap)

addNap : Service { title : String, minutes : Int } String
addNap = Service.declare POST "/api/naps"
`
	if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	shapeFrom := func(rEnv *runtime.Env) *runtime.WireShape {
		t.Helper()
		v, ok := rEnv.Lookup("Main.addNap")
		if !ok {
			t.Fatal("addNap not bound")
		}
		svc, ok := v.(runtime.VService)
		if !ok {
			t.Fatalf("expected a service, got %T", v)
		}
		return svc.InputShape
	}

	deployed, _, err := LoadIntoEnvForRuntime(entry, nil)
	if err != nil {
		t.Fatalf("deployed load: %v", err)
	}
	dev, _, _, err := LoadIntoEnvWithModulesAndHook(entry, nil)
	if err != nil {
		t.Fatalf("dev load: %v", err)
	}

	deployedShape, devShape := shapeFrom(deployed), shapeFrom(dev)
	if deployedShape == nil {
		t.Fatal("the deployed loader stamped no shape")
	}
	if devShape == nil {
		t.Fatal("the dev loader stamped no shape: `mar dev` would 500 where production 422s")
	}

	// Same body, same verdict, on both.
	bad := runtime.VRecord{
		Fields: map[string]runtime.Value{
			"title":   runtime.VString{V: "nap"},
			"minutes": runtime.VString{V: "soon"},
		},
		Order: []string{"title", "minutes"},
	}
	deployedErr := runtime.CheckWire(bad, *deployedShape)
	devErr := runtime.CheckWire(bad, *devShape)
	if deployedErr == nil || devErr == nil {
		t.Fatalf("both loaders must reject this body: deployed=%v dev=%v", deployedErr, devErr)
	}
	if deployedErr.Error() != devErr.Error() {
		t.Fatalf("the two loaders disagree:\n  deployed: %v\n  dev:      %v", deployedErr, devErr)
	}
}

// A union whose constructors all carry nothing has a finite set of legal wire
// values, so it is checkable. It used to reduce to WireAny, and a tag outside
// the set reached a `case` the checker had proved total.
func TestNullaryUnionIsCheckable(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	src := `module Main exposing (setStatus)

type Status
    = Draft
    | Sent

setStatus : Service { status : Status } String
setStatus = Service.declare POST "/api/status"
`
	if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	rEnv, _, _, err := LoadIntoEnvWithModulesAndHook(entry, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	v, _ := rEnv.Lookup("Main.setStatus")
	svc := v.(runtime.VService)
	if svc.InputShape == nil {
		t.Fatal("no shape stamped")
	}
	var status runtime.WireShape
	for _, f := range svc.InputShape.Fields {
		if f.Name == "status" {
			status = f.Shape
		}
	}
	if status.Kind != runtime.WireCtor {
		t.Fatalf("status shape = %s, want Ctor", status.Kind)
	}
	if strings.Join(status.Tags, ",") != "Draft,Sent" {
		t.Fatalf("tags = %v, want [Draft Sent]", status.Tags)
	}

	rec := func(tag string) runtime.Value {
		return runtime.VRecord{
			Fields: map[string]runtime.Value{"status": runtime.VCtor{Tag: tag}},
			Order:  []string{"status"},
		}
	}
	if err := runtime.CheckWire(rec("Sent"), *svc.InputShape); err != nil {
		t.Fatalf("a declared tag must pass: %v", err)
	}
	err = runtime.CheckWire(rec("Purple"), *svc.InputShape)
	if err == nil {
		t.Fatal("a tag the type does not declare must be refused")
	}
	if !strings.Contains(err.Error(), "Draft") || !strings.Contains(err.Error(), "Purple") {
		t.Fatalf("the message must name the legal set and what arrived, got: %v", err)
	}
}

// A union that carries a payload keeps the escape hatch: there is no single
// wire form worth asserting about, and a wrong 422 is worse than the 500.
func TestUnionWithPayloadStaysAny(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	src := `module Main exposing (send)

type Target
    = Everyone
    | Person String

send : Service { to : Target } String
send = Service.declare POST "/api/send"
`
	if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	rEnv, _, _, err := LoadIntoEnvWithModulesAndHook(entry, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	v, _ := rEnv.Lookup("Main.send")
	svc := v.(runtime.VService)
	for _, f := range svc.InputShape.Fields {
		if f.Name == "to" && f.Shape.Kind != runtime.WireAny {
			t.Fatalf("a payload-carrying union must stay Any, got %s", f.Shape.Kind)
		}
	}
}
