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

// A union that carries a payload IS checked, arguments and all.
//
// This test asserted the opposite when the guard was written, and the reason
// given — no single wire form worth asserting about — was wrong: every
// constructor has a known arity and known argument types. It was the widest
// hole in the guard. An undeclared tag, or the right tag with the wrong
// payload, reached a `case` the checker had proved total, and a payload that
// violated its own type got written to the database through it.
func TestUnionWithPayloadIsChecked(t *testing.T) {
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
	var to runtime.WireShape
	for _, f := range svc.InputShape.Fields {
		if f.Name == "to" {
			to = f.Shape
		}
	}
	if to.Kind != runtime.WireCtor {
		t.Fatalf("a payload-carrying union is checkable, got %s", to.Kind)
	}
	if strings.Join(to.Tags, ",") != "Everyone,Person" {
		t.Fatalf("tags = %v, want [Everyone Person]", to.Tags)
	}
	if len(to.Args) != 2 || len(to.Args[0]) != 0 || len(to.Args[1]) != 1 ||
		to.Args[1][0].Kind != runtime.WireString {
		t.Fatalf("argument shapes = %v, want [[] [String]]", to.Args)
	}

	rec := func(v runtime.Value) runtime.Value {
		return runtime.VRecord{
			Fields: map[string]runtime.Value{"to": v},
			Order:  []string{"to"},
		}
	}
	ok := []runtime.Value{
		runtime.VCtor{Tag: "Everyone"},
		runtime.VCtor{Tag: "Person", Args: []runtime.Value{runtime.VString{V: "ana"}}},
	}
	for _, good := range ok {
		if err := runtime.CheckWire(rec(good), *svc.InputShape); err != nil {
			t.Fatalf("a well-formed constructor must pass: %v", err)
		}
	}
	bad := map[string]runtime.Value{
		"an undeclared tag":      runtime.VCtor{Tag: "Nobody"},
		"the wrong arity":        runtime.VCtor{Tag: "Person"},
		"the wrong payload type": runtime.VCtor{Tag: "Person", Args: []runtime.Value{runtime.VInt{V: 1}}},
		"a payload on a nullary": runtime.VCtor{Tag: "Everyone", Args: []runtime.Value{runtime.VInt{V: 1}}},
	}
	for what, v := range bad {
		if err := runtime.CheckWire(rec(v), *svc.InputShape); err == nil {
			t.Errorf("%s must be refused", what)
		}
	}
}

// A tuple is checkable too: its arity is part of its type. There was no shape
// kind for one at all, which made a tuple in a request a guaranteed
// `tupleFirst: not a 2-tuple` and a tuple AS the request type something no
// client could ever call successfully.
func TestTupleIsChecked(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	src := `module Main exposing (place)

place : Service ( Int, String ) String
place = Service.declare POST "/api/place"
`
	if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	rEnv, _, _, err := LoadIntoEnvWithModulesAndHook(entry, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	v, _ := rEnv.Lookup("Main.place")
	svc := v.(runtime.VService)
	if svc.InputShape.Kind != runtime.WireTuple {
		t.Fatalf("shape kind = %s, want Tuple", svc.InputShape.Kind)
	}
	good := runtime.VTuple{Members: []runtime.Value{runtime.VInt{V: 1}, runtime.VString{V: "a"}}}
	if err := runtime.CheckWire(good, *svc.InputShape); err != nil {
		t.Fatalf("a well-formed tuple must pass: %v", err)
	}
	// A tuple travels as a JSON array and comes back from the decoder as a
	// LIST, because an array is all the wire has. So the well-formed list is
	// the ordinary case, not an error — this assertion said the opposite when
	// it was written, and would have made a tuple request uncallable in a
	// second way.
	asList := runtime.VList{Elements: []runtime.Value{runtime.VInt{V: 1}, runtime.VString{V: "a"}}}
	if err := runtime.CheckWire(asList, *svc.InputShape); err != nil {
		t.Fatalf("the wire form of a tuple is an array: %v", err)
	}
	if _, ok := runtime.CoerceWire(asList, *svc.InputShape).(runtime.VTuple); !ok {
		t.Fatal("accepting the array means producing the tuple the handler was checked against")
	}

	bad := map[string]runtime.Value{
		"the wrong arity":       runtime.VTuple{Members: []runtime.Value{runtime.VInt{V: 1}}},
		"the wrong member":      runtime.VTuple{Members: []runtime.Value{runtime.VInt{V: 1}, runtime.VInt{V: 2}}},
		"a short list":          runtime.VList{Elements: []runtime.Value{runtime.VInt{V: 1}}},
		"a list of wrong types": runtime.VList{Elements: []runtime.Value{runtime.VString{V: "x"}, runtime.VString{V: "a"}}},
		"an object":             runtime.VRecord{Fields: map[string]runtime.Value{"a": runtime.VInt{V: 1}}, Order: []string{"a"}},
	}
	for what, v := range bad {
		if err := runtime.CheckWire(v, *svc.InputShape); err == nil {
			t.Errorf("%s must be refused", what)
		}
	}
}

// The depth cap is gone, so an ordinary record keeps being checked however
// deep it is. Seven levels sailed through and met `+: unsupported types`.
func TestDeepRecordIsStillChecked(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	src := `module Main exposing (deep)

deep : Service { a : { b : { c : { d : { e : { f : { g : { h : Int } } } } } } } } Int
deep = Service.declare POST "/api/deep"
`
	if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	rEnv, _, _, err := LoadIntoEnvWithModulesAndHook(entry, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	v, _ := rEnv.Lookup("Main.deep")
	svc := v.(runtime.VService)
	shape := *svc.InputShape
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		if shape.Kind != runtime.WireRecord || len(shape.Fields) != 1 || shape.Fields[0].Name != name {
			t.Fatalf("expected a record naming %q, got %s", name, shape.Kind)
		}
		shape = shape.Fields[0].Shape
	}
	if shape.Kind != runtime.WireRecord || shape.Fields[0].Shape.Kind != runtime.WireInt {
		t.Fatalf("the innermost field is an Int, got %s", shape.Fields[0].Shape.Kind)
	}
}

// A recursive type closes its own cycle and gives up THERE, which is what the
// depth cap was standing in for. Everything above the cycle stays checked.
func TestRecursiveTypeNamesItsCycle(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	src := `module Main exposing (post)

type Comment
    = Comment String (List Comment)

post : Service { top : Comment } String
post = Service.declare POST "/api/post"
`
	if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	rEnv, _, _, err := LoadIntoEnvWithModulesAndHook(entry, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	v, _ := rEnv.Lookup("Main.post")
	svc := v.(runtime.VService)
	top := svc.InputShape.Fields[0].Shape
	if top.Kind != runtime.WireCtor || len(top.Args) != 1 || len(top.Args[0]) != 2 {
		t.Fatalf("expected a one-constructor union taking two values, got %+v", top)
	}
	if top.Args[0][0].Kind != runtime.WireString {
		t.Fatalf("the first argument is a String, got %s", top.Args[0][0].Kind)
	}
	// The recursive position NAMES the cycle rather than giving up. It used
	// to be WireAny, which checked nothing from there down: measured on a
	// running service, a wrong type at the first level answered 422 naming
	// the position and the same mistake one level deeper was waved through,
	// reaching the handler and stopping only at the write guard — as a 500,
	// blaming the server for a malformed request.
	inner := top.Args[0][1]
	if inner.Kind != runtime.WireList || inner.Elem == nil {
		t.Fatalf("the recursive position is a list, got %+v", inner)
	}
	if inner.Elem.Kind != runtime.WireRef || inner.Elem.Name != top.Name {
		t.Fatalf("the recursive position points back at %q, got %+v", top.Name, inner.Elem)
	}

	comment := func(label runtime.Value, kids ...runtime.Value) runtime.Value {
		return runtime.VCtor{Tag: "Comment", Args: []runtime.Value{label, runtime.VList{Elements: kids}}}
	}
	wrap := func(top runtime.Value) runtime.VRecord {
		return runtime.VRecord{Fields: map[string]runtime.Value{"top": top}, Order: []string{"top"}}
	}

	if err := runtime.CheckWire(wrap(comment(runtime.VString{V: "ok"})), *svc.InputShape); err != nil {
		t.Fatalf("a well-formed comment: %v", err)
	}
	if err := runtime.CheckWire(wrap(comment(runtime.VInt{V: 1})), *svc.InputShape); err == nil {
		t.Fatal("the non-recursive argument is checked")
	}
	// The one the old shape could not see: wrong, one level down.
	deep := wrap(comment(runtime.VString{V: "ok"}, comment(runtime.VInt{V: 1})))
	err = runtime.CheckWire(deep, *svc.InputShape)
	if err == nil {
		t.Fatal("a wrong value below the cycle is still a wrong value")
	}
	if !strings.Contains(err.Error(), "should be a string") {
		t.Errorf("the message must name what was expected, got: %v", err)
	}
	// And two levels down, to show it follows the cycle rather than
	// unrolling it once.
	deeper := wrap(comment(runtime.VString{V: "a"}, comment(runtime.VString{V: "b"}, comment(runtime.VInt{V: 9}))))
	if err := runtime.CheckWire(deeper, *svc.InputShape); err == nil {
		t.Fatal("the cycle is followed, not unrolled once")
	}
}

// A union inside another instantiation of ITSELF is not a cycle: the inner
// Result of `Result String (Result String Int)` is checked against its own
// arms, so `Ok (Ok 5)` passes and `Ok (Ok "x")` does not. Checked against
// the outer's arms instead, `5` would have to be an `Ok` or an `Err`; and in
// `Result Decimal (Result Int String)`, coercion would turn the Int of
// `Ok (Err 3)` into the outer's Decimal.
func TestAUnionInsideItselfIsNotACycle(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	src := `module Main exposing (nested, args)


nested : Service { v : Result String (Result String Int) } String
nested = Service.declare POST "/api/nested"


args : Service { v : Result Decimal (Result Int String) } String
args = Service.declare POST "/api/args"
`
	if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	rEnv, _, _, err := LoadIntoEnvWithModulesAndHook(entry, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	shapeOf := func(name string) runtime.WireShape {
		v, _ := rEnv.Lookup("Main." + name)
		return *v.(runtime.VService).InputShape
	}
	ok := func(v runtime.Value) runtime.Value { return runtime.VCtor{Tag: "Ok", Args: []runtime.Value{v}} }
	errv := func(v runtime.Value) runtime.Value { return runtime.VCtor{Tag: "Err", Args: []runtime.Value{v}} }
	wrap := func(v runtime.Value) runtime.VRecord {
		return runtime.VRecord{Fields: map[string]runtime.Value{"v": v}, Order: []string{"v"}}
	}

	nested := shapeOf("nested")
	if inner := nested.Fields[0].Shape.Args[0][0]; inner.Kind != runtime.WireCtor {
		t.Fatalf("the inner Result is a union of its own, not a cycle: got %+v", inner)
	}
	if err := runtime.CheckWire(wrap(ok(ok(runtime.VInt{V: 5}))), nested); err != nil {
		t.Fatalf("Ok (Ok 5) is a value of the type: %v", err)
	}
	if err := runtime.CheckWire(wrap(ok(ok(runtime.VString{V: "x"}))), nested); err == nil {
		t.Fatal("Ok (Ok \"x\") is not: the inner Ok carries an Int")
	}

	args := shapeOf("args")
	in := wrap(ok(errv(runtime.VInt{V: 3})))
	if err := runtime.CheckWire(in, args); err != nil {
		t.Fatalf("Ok (Err 3) is a value of the type: %v", err)
	}
	got := runtime.CoerceWire(in, args).(runtime.VRecord).Fields["v"]
	inner := got.(runtime.VCtor).Args[0].(runtime.VCtor).Args[0]
	if _, isInt := inner.(runtime.VInt); !isInt {
		t.Fatalf("the inner Err carries an Int, and arrived as %T", inner)
	}
}
