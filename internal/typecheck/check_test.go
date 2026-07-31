package typecheck

import (
	"strings"
	"testing"

	"mar/internal/parser"
)

func checkSource(t *testing.T, src string) (*CheckResult, error) {
	t.Helper()
	resetVarIDsForTesting()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return CheckModule(mod)
}

func TestCheckSingleValueDecl(t *testing.T) {
	src := `module M exposing (..)
foo = 42
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pretty(res.ValueTypes["foo"]); got != "number" {
		t.Fatalf("want number, got %s", got)
	}
}

func TestCheckSeveralValueDecls(t *testing.T) {
	src := `module M exposing (..)
greeting = "hello"
count = 1 + 2
flag = True
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["greeting"].String(); got != "String" {
		t.Fatalf("greeting: %s", got)
	}
	if got := Pretty(res.ValueTypes["count"]); got != "number" {
		t.Fatalf("count: %s", got)
	}
	if got := res.ValueTypes["flag"].String(); got != "Bool" {
		t.Fatalf("flag: %s", got)
	}
}

func TestCheckCustomTypeAndCtor(t *testing.T) {
	src := `module M exposing (..)
type Status = Active | Inactive
foo = Active
bar = Inactive
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["foo"].String(); got != "M.Status" {
		t.Fatalf("foo: want Status, got %s", got)
	}
	if got := res.ValueTypes["bar"].String(); got != "M.Status" {
		t.Fatalf("bar: want Status, got %s", got)
	}
}

func TestCheckCustomTypeWithPayload(t *testing.T) {
	src := `module M exposing (..)
type UserId = UserId Int
mkId = UserId 42
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["mkId"].String(); got != "M.UserId" {
		t.Fatalf("mkId: want UserId, got %s", got)
	}
}

func TestCheckGenericCustomType(t *testing.T) {
	src := `module M exposing (..)
type Box a = Box a
fooInt = Box 1
fooStr = Box "x"
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pretty(res.ValueTypes["fooInt"]); got != "Box number" {
		t.Fatalf("fooInt: %s", got)
	}
	// `.String()` is the raw identity, which since ADR 0027 carries the
	// module. `Pretty` is the display form and drops it.
	if got := res.ValueTypes["fooStr"].String(); got != "M.Box String" {
		t.Fatalf("fooStr: %s", got)
	}
}

func TestCheckAnnotation(t *testing.T) {
	src := `module M exposing (..)
foo : Int
foo = 42
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["foo"].String(); got != "Int" {
		t.Fatalf("foo: %s", got)
	}
}

func TestCheckAnnotationMismatch(t *testing.T) {
	src := `module M exposing (..)
foo : String
foo = 42
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected annotation mismatch error")
	}
	if !strings.Contains(err.Error(), "Int") || !strings.Contains(err.Error(), "String") {
		t.Fatalf("expected mismatch between Int and String, got: %v", err)
	}
}

func TestCheckFunctionDecl(t *testing.T) {
	src := `module M exposing (..)
add x y = x + y
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["add"].String()
	// `+` is number-constrained (Int | Decimal), so an unannotated add
	// generalizes over number instead of pinning to Int.
	if !strings.Contains(got, "number") {
		t.Fatalf("add: want a number-constrained scheme, got %s", got)
	}
}

func TestCheckRecursiveFunction(t *testing.T) {
	src := `module M exposing (..)
fact n =
    if n == 0 then 1 else n * fact (n - 1)
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := Pretty(res.ValueTypes["fact"])
	if got != "number -> number" {
		t.Fatalf("fact: want number -> number, got %s", got)
	}
}

func TestCheckMutualRecursion(t *testing.T) {
	src := `module M exposing (..)
even n = if n == 0 then True else odd (n - 1)
odd n = if n == 0 then False else even (n - 1)
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pretty(res.ValueTypes["even"]); got != "number -> Bool" {
		t.Fatalf("even: %s", got)
	}
	if got := Pretty(res.ValueTypes["odd"]); got != "number -> Bool" {
		t.Fatalf("odd: %s", got)
	}
}

func TestCheckCustomTypeUsedInRecord(t *testing.T) {
	src := `module M exposing (..)
type UserId = UserId Int
mkUser = { id = UserId 1, name = "Alice" }
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["mkUser"].String()
	if !strings.Contains(got, "UserId") || !strings.Contains(got, "String") {
		t.Fatalf("mkUser: %s", got)
	}
}

func TestCheckCaseOnCustomType(t *testing.T) {
	src := `module M exposing (..)
type Color = Red | Green | Blue
toInt c =
    case c of
        Red -> 1
        Green -> 2
        Blue -> 3
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pretty(res.ValueTypes["toInt"]); got != "Color -> number" {
		t.Fatalf("toInt: %s", got)
	}
}

// --- Type-alias param substitution ---
//
// These tests pin the contract that `type alias Foo a = ...` resolves
// the param `a` to whatever argument the caller supplies, in every
// position the body references it. Before paramIDForName was given
// a real implementation, parametric aliases inlined their bodies
// with the param-TVars left UNsubstituted: `Box Int` resolved to
// `{ value : t7 }` for some fresh TVar, not `{ value : Int }`. Most
// uses hid the bug because inference would unify the stray TVar with
// the surrounding context — but using the same alias at two different
// types (TestCheckAliasUsedAtTwoTypes) exposed it instantly.

func TestCheckAliasNonParametric(t *testing.T) {
	// Sanity check: zero-param aliases still resolve cleanly.
	src := `module M exposing (..)
type alias UserName = String
hello : UserName
hello = "Alice"
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["hello"].String(); got != "String" {
		t.Fatalf("hello: want String, got %s", got)
	}
}

func TestCheckAliasOneParam(t *testing.T) {
	src := `module M exposing (..)
type alias Box a = { value : a }
boxed : Box Int
boxed = { value = 42 }
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["boxed"].String()
	// The alias resolves to its body with `a` substituted by Int —
	// the printed type should mention Int (not a stray tN tvar).
	if !strings.Contains(got, "value : Int") {
		t.Fatalf("boxed: want a record with `value : Int`, got %s", got)
	}
}

func TestCheckAliasUsedAtTwoTypes(t *testing.T) {
	// The smoking-gun case for the old bug: with substitution stubbed,
	// both uses shared the same body TVar. The first annotation would
	// pin that TVar to Int, then the SECOND annotation forced the
	// same TVar to also equal String — type checking failed with a
	// spurious mismatch on a program that should compile cleanly.
	src := `module M exposing (..)
type alias Wrapper a = a
asInt : Wrapper Int
asInt = 1
asString : Wrapper String
asString = "x"
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatalf("expected clean check, got: %v", err)
	}
	if got := res.ValueTypes["asInt"].String(); got != "Int" {
		t.Fatalf("asInt: want Int, got %s", got)
	}
	if got := res.ValueTypes["asString"].String(); got != "String" {
		t.Fatalf("asString: want String, got %s", got)
	}
}

func TestCheckAliasTwoParams(t *testing.T) {
	// Multi-param aliases route each param to its own positional
	// argument. `Pair Int String` must resolve to `(Int, String)`,
	// not `(Int, Int)` or `(String, String)` — easy to flip if the
	// ParamIDs slice isn't index-aligned with Params.
	src := `module M exposing (..)
type alias Pair a b = (a, b)
sample : Pair Int String
sample = (1, "x")
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["sample"].String()
	if !strings.Contains(got, "Int") || !strings.Contains(got, "String") {
		t.Fatalf("sample: want a tuple with Int and String, got %s", got)
	}
	// Order matters: param `a` should hit the first tuple position,
	// not the second.
	intIdx := strings.Index(got, "Int")
	strIdx := strings.Index(got, "String")
	if intIdx > strIdx {
		t.Fatalf("sample: tuple positions look swapped (Int=%d, String=%d in %q)", intIdx, strIdx, got)
	}
}

// --- Stdlib expansion: List ---
//
// These are smoke tests for the new List.* functions added in the
// stdlib expansion. They don't exercise runtime behavior (that's
// covered by the runtime package's own tests via LoadModule); they
// just make sure the typeschemes registered in env.go unify the
// right shapes so user code can call them.

func TestCheckListTake(t *testing.T) {
	src := `module M exposing (..)
xs = List.take 3 [1, 2, 3, 4, 5]
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pretty(res.ValueTypes["xs"]); got != "List number" {
		t.Fatalf("xs: %s", got)
	}
}

func TestCheckListMember(t *testing.T) {
	src := `module M exposing (..)
found = List.member 3 [1, 2, 3]
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["found"].String(); got != "Bool" {
		t.Fatalf("found: %s", got)
	}
}

func TestCheckListIndexedMap(t *testing.T) {
	src := `module M exposing (..)
labeled = List.indexedMap (\i s -> String.fromInt i ++ ": " ++ s) ["a", "b"]
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["labeled"].String(); got != "List String" {
		t.Fatalf("labeled: %s", got)
	}
}

func TestCheckListPartition(t *testing.T) {
	src := `module M exposing (..)
split = List.partition (\n -> n > 2) [1, 2, 3, 4]
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := Pretty(res.ValueTypes["split"])
	if !strings.Contains(got, "List number") {
		t.Fatalf("split: %s", got)
	}
}

func TestCheckListSortBy(t *testing.T) {
	src := `module M exposing (..)
byLen = List.sortBy String.length ["aaa", "b", "cc"]
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["byLen"].String(); got != "List String" {
		t.Fatalf("byLen: %s", got)
	}
}

func TestCheckListSortWithOrder(t *testing.T) {
	// Comparator returns Order (LT / EQ / GT) — not Int. Mar refuses
	// to overload integer values as comparison results.
	src := `module M exposing (..)
desc = \a b -> if a > b then LT else if a < b then GT else EQ
sorted = List.sortWith desc [3, 1, 4, 1, 5, 9, 2, 6]
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pretty(res.ValueTypes["sorted"]); got != "List number" {
		t.Fatalf("sorted: %s", got)
	}
	if got := Pretty(res.ValueTypes["desc"]); got != "number -> number -> Order" {
		t.Fatalf("desc: %s", got)
	}
}

func TestCheckSortWithIntRejected(t *testing.T) {
	// A comparator that returns Int (old -1/0/1 style) MUST be a type
	// error. This is the test that fails loudly if someone tries to
	// "make it work" by quietly accepting Int again.
	src := `module M exposing (..)
broken = List.sortWith (\a b -> a - b) [3, 1, 2]
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected type error for Int comparator, got clean check")
	}
	if !strings.Contains(err.Error(), "Order") && !strings.Contains(err.Error(), "Int") {
		t.Fatalf("expected mention of Order/Int mismatch, got: %v", err)
	}
}

func TestCheckDictBasic(t *testing.T) {
	src := `module M exposing (..)
prices = Dict.fromList [("apple", 1), ("pear", 2)]
n = Dict.get "apple" prices
size = Dict.size prices
ks = Dict.keys prices
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pretty(res.ValueTypes["prices"]); got != "Dict String number" {
		t.Fatalf("prices: %s", got)
	}
	if got := Pretty(res.ValueTypes["n"]); got != "Maybe number" {
		t.Fatalf("n: %s", got)
	}
	if got := res.ValueTypes["size"].String(); got != "Int" {
		t.Fatalf("size: %s", got)
	}
	if got := res.ValueTypes["ks"].String(); got != "List String" {
		t.Fatalf("ks: %s", got)
	}
}

func TestCheckDictMapAndFoldl(t *testing.T) {
	src := `module M exposing (..)
counts = Dict.fromList [("a", 1), ("b", 2)]
doubled = Dict.map (\k v -> v * 2) counts
total = Dict.foldl (\k v acc -> acc + v) 0 counts
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pretty(res.ValueTypes["doubled"]); got != "Dict String number" {
		t.Fatalf("doubled: %s", got)
	}
	if got := Pretty(res.ValueTypes["total"]); got != "number" {
		t.Fatalf("total: %s", got)
	}
}

func TestCheckSetBasic(t *testing.T) {
	src := `module M exposing (..)
s = Set.fromList [3, 1, 2, 1]
yes = Set.member 2 s
size = Set.size s
both = Set.union s (Set.singleton 4)
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pretty(res.ValueTypes["s"]); got != "Set number" {
		t.Fatalf("s: %s", got)
	}
	if got := res.ValueTypes["yes"].String(); got != "Bool" {
		t.Fatalf("yes: %s", got)
	}
	if got := res.ValueTypes["size"].String(); got != "Int" {
		t.Fatalf("size: %s", got)
	}
	if got := Pretty(res.ValueTypes["both"]); got != "Set number" {
		t.Fatalf("both: %s", got)
	}
}

// --- Dict / Set comparable-key constraint ---
//
// These tests prove the Comparable Kind on Dict / Set keys is
// enforced at compile time. Each "rejected" case used to compile
// silently and explode at runtime with "comparison: unsupported
// types" — now it dies at typecheck with a clear message naming the
// offending shape and the allowed types.

func TestCheckDictRecordKeyRejected(t *testing.T) {
	src := `module M exposing (..)
broken = Dict.fromList [({ name = "bob" }, 1), ({ name = "alice" }, 2)]
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected type error for record key, got clean check")
	}
	if !strings.Contains(err.Error(), "comparable") {
		t.Fatalf("expected mention of 'comparable' in error, got: %v", err)
	}
}

func TestCheckDictBoolKeyRejected(t *testing.T) {
	// Bool isn't comparable — there's no useful total order over
	// True/False that Mar's compareValues recognizes, so the runtime
	// would blow up; typecheck catches it here instead.
	src := `module M exposing (..)
broken = Dict.fromList [(True, "yes"), (False, "no")]
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected type error for Bool key, got clean check")
	}
	if !strings.Contains(err.Error(), "comparable") {
		t.Fatalf("expected mention of 'comparable' in error, got: %v", err)
	}
}

func TestCheckDictInsertRecordKeyRejected(t *testing.T) {
	// Even if the dict starts polymorphic, the first `insert` with a
	// non-comparable key pins it. We expect rejection at THAT line,
	// not later.
	src := `module M exposing (..)
broken = Dict.insert { id = 1 } "v" Dict.empty
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected type error, got clean check")
	}
	if !strings.Contains(err.Error(), "comparable") {
		t.Fatalf("expected mention of 'comparable', got: %v", err)
	}
}

func TestCheckSetRecordKeyRejected(t *testing.T) {
	src := `module M exposing (..)
broken = Set.fromList [{ id = 1 }, { id = 2 }]
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected type error for record key, got clean check")
	}
	if !strings.Contains(err.Error(), "comparable") {
		t.Fatalf("expected mention of 'comparable' in error, got: %v", err)
	}
}

func TestCheckDictAcceptsComparableKeys(t *testing.T) {
	// Sanity: Int, String, Char keys all still pass through.
	src := `module M exposing (..)
byInt = Dict.fromList [(1, "a"), (2, "b")]
byString = Dict.fromList [("a", 1), ("b", 2)]
byChar = Dict.fromList [('a', 1), ('b', 2)]
`
	_, err := checkSource(t, src)
	if err != nil {
		t.Fatalf("expected clean check, got: %v", err)
	}
}

// --- Ordering operators are Comparable-only ---
//
// `<`, `>`, `<=`, `>=` accept only Int / String / Char.
// Equality (`==`, `/=`) stays polymorphic because it's structural.
// These tests document the constraint and guard against regression.

func TestCheckOrderingAcceptsComparable(t *testing.T) {
	src := `module M exposing (..)
intLt = 1 < 2
strLt = "abc" < "abd"
charLt = 'a' < 'z'
combinedGte = "x" >= "y"
`
	_, err := checkSource(t, src)
	if err != nil {
		t.Fatalf("expected clean check, got: %v", err)
	}
}

func TestCheckOrderingRejectsRecord(t *testing.T) {
	src := `module M exposing (..)
broken = { a = 1 } < { a = 2 }
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for record comparison, got clean check")
	}
	if !strings.Contains(err.Error(), "comparable") {
		t.Fatalf("expected 'comparable' in error, got: %v", err)
	}
}

func TestCheckOrderingRejectsBool(t *testing.T) {
	src := `module M exposing (..)
broken = True < False
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for Bool comparison, got clean check")
	}
	if !strings.Contains(err.Error(), "comparable") {
		t.Fatalf("expected 'comparable' in error, got: %v", err)
	}
}

func TestCheckOrderingRejectsList(t *testing.T) {
	// List of comparable elements is NOT itself comparable in the
	// strict semantics — runtime's compareValues doesn't recurse.
	src := `module M exposing (..)
broken = [1, 2] < [1, 3]
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for list comparison, got clean check")
	}
	if !strings.Contains(err.Error(), "comparable") {
		t.Fatalf("expected 'comparable' in error, got: %v", err)
	}
}

func TestCheckOrderingRejectsTuple(t *testing.T) {
	src := `module M exposing (..)
broken = (1, 2) < (3, 4)
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for tuple comparison, got clean check")
	}
	if !strings.Contains(err.Error(), "comparable") {
		t.Fatalf("expected 'comparable' in error, got: %v", err)
	}
}

func TestCheckEqualityStaysPolymorphic(t *testing.T) {
	// `==` and `/=` are structural — they work on records, tuples,
	// lists, custom types. Sanity check that we didn't accidentally
	// constrain them too.
	src := `module M exposing (..)
recEq = { a = 1 } == { a = 1 }
tupEq = (1, "x") == (1, "x")
listEq = [True, False] == [True, False]
maybeEq = Just 1 == Just 1
`
	_, err := checkSource(t, src)
	if err != nil {
		t.Fatalf("expected clean check for structural equality, got: %v", err)
	}
}

func TestCheckDictKeyConstraintPropagates(t *testing.T) {
	// A user function that takes a Dict and the user later calls it
	// with a record-keyed dict should fail. The constraint on the
	// k position propagates from Dict.empty's signature through the
	// user's function.
	src := `module M exposing (..)
sizeOf d = Dict.size d
broken = sizeOf (Dict.fromList [({ x = 1 }, "v")])
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected propagated constraint error, got clean check")
	}
	if !strings.Contains(err.Error(), "comparable") {
		t.Fatalf("expected propagated 'comparable' error, got: %v", err)
	}
}

// ---------------------------------------------------------------------
// Record-pattern destructuring
// ---------------------------------------------------------------------
//
// `{ field1, field2 }` as a pattern binds each named field as a local.
// Partial-match semantics: the matched record may have extra fields the
// pattern doesn't list. Works in `case` branches, `let` bindings, and
// function arguments. No renaming syntax (Elm-style — punning only).

func TestRecordPatternInCase(t *testing.T) {
	src := `module M exposing (..)
fullName p =
    case p of
        { first, last } -> first ++ " " ++ last
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["fullName"].String()
	// Should infer fullName : { first : String, last : String | <row> } -> String
	if !strings.Contains(got, "first : String") || !strings.Contains(got, "last : String") {
		t.Fatalf("fullName: expected record-arg type with first/last fields, got %s", got)
	}
	if !strings.HasSuffix(got, "-> String") {
		t.Fatalf("fullName: expected -> String return, got %s", got)
	}
}

func TestRecordPatternNestedInCtor(t *testing.T) {
	src := `module M exposing (..)
emailOf m =
    case m of
        Just { email } -> email
        Nothing -> ""
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["emailOf"].String()
	if !strings.Contains(got, "email : String") {
		t.Fatalf("emailOf: expected record with email field, got %s", got)
	}
}

func TestRecordPatternInLet(t *testing.T) {
	src := `module M exposing (..)
greet p =
    let { name } = p in
    "Hi, " ++ name
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["greet"].String()
	if !strings.Contains(got, "name : String") {
		t.Fatalf("greet: expected record-arg with name field, got %s", got)
	}
}

func TestRecordPatternInFunctionArg(t *testing.T) {
	// Lambda arg position takes a pattern, so `\{ x } -> x` should work.
	// At module-level the equivalent is `f { x } = x` if the parser
	// accepts it; lambda form is the conservative version.
	src := `module M exposing (..)
firstField = \r -> case r of { x } -> x
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["firstField"].String()
	if !strings.Contains(got, "x :") {
		t.Fatalf("firstField: expected record-arg with x field, got %s", got)
	}
}

func TestRecordPatternPartialMatchIgnoresExtraFields(t *testing.T) {
	// The pattern `{ name }` should match a record that has extra
	// fields beyond `name` — Elm-style partial match via row poly.
	src := `module M exposing (..)
person = { name = "Alice", age = 30, city = "Boston" }
just_name =
    case person of
        { name } -> name
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["just_name"].String(); got != "String" {
		t.Fatalf("just_name: want String, got %s", got)
	}
}

func TestRecordPatternMultipleFields(t *testing.T) {
	src := `module M exposing (..)
combine p =
    case p of
        { a, b, c } -> a + b + c
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["combine"].String()
	// All three fields flow through `+`, so they share ONE
	// number-constrained variable (Int | Decimal).
	for _, fld := range []string{"a : number", "b : number", "c : number"} {
		if !strings.Contains(got, fld) {
			t.Errorf("combine: missing %q in type %s", fld, got)
		}
	}
}

func TestRecordPatternDuplicateFieldRejected(t *testing.T) {
	src := `module M exposing (..)
broken p =
    case p of
        { x, x } -> x
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for duplicate field in record pattern")
	}
	if !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("expected 'more than once' in error, got: %v", err)
	}
}

func TestRecordPatternLetDestructuresFields(t *testing.T) {
	// The bound names should be usable as their inferred field types,
	// not just as the parent record. Concrete types from the RHS.
	src := `module M exposing (..)
result =
    let
        person = { name = "Alice", age = 30 }
        { name, age } = person
    in
    name
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["result"].String(); got != "String" {
		t.Fatalf("result: want String (from name field), got %s", got)
	}
}

// `import UI exposing (..)` must bring the whole UI vocabulary into the
// bare namespace. This used to parse without error and then silently
// bind NOTHING, so the first bare name died with "unknown identifier" —
// the worst kind of failure. Pins the wildcard against the builtin UI
// module (user modules go through the same ExportsOf path).
func TestImportExposingAllBindsModuleExports(t *testing.T) {
	src := `module M exposing (..)

import UI exposing (..)

v = vstack [ width fill ] [ text [] "hi" ]
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatalf("exposing (..) should bind UI's exports bare; got: %v", err)
	}
	if got := res.ValueTypes["v"].String(); !strings.Contains(got, "View") {
		t.Fatalf("v should be a View, got %s", got)
	}
}

// A type declaration that uses a variable it never declared must be
// rejected, not silently accepted with a fresh var. The silent FreshVar
// this used to produce hid typos and the lowercase-variable vs
// uppercase-concrete-type confusion; Elm rejects it ("unbound type
// variable") and so does Mar.
func TestUnboundTypeVarInCustomTypeRejected(t *testing.T) {
	src := `module M exposing (..)
type Outcome
    = SignedIn user
    | WrongCode
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("custom type with undeclared variable should be rejected")
	}
	if !strings.Contains(err.Error(), "unbound type variable `user`") {
		t.Fatalf("error should name the unbound variable, got: %v", err)
	}
}

func TestUnboundTypeVarInAliasRejected(t *testing.T) {
	src := `module M exposing (..)
type alias Box = { value : thing }
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("alias with undeclared variable should be rejected")
	}
	if !strings.Contains(err.Error(), "unbound type variable `thing`") {
		t.Fatalf("error should name the unbound variable, got: %v", err)
	}
}

// The flip side: DECLARED variables keep working in declarations, and
// annotations keep their implicit forall (free vars there are
// pre-collected into scope, never "unbound").
func TestDeclaredTypeVarsAndAnnotationForallStillWork(t *testing.T) {
	src := `module M exposing (..)

type Outcome user
    = SignedIn user
    | WrongCode

type alias Box a = { value : a }

identity : a -> a
identity x = x

name : { r | name : String } -> String
name rec = rec.name
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("declared params and annotation foralls should still check, got: %v", err)
	}
}

// Qualified constructor patterns and expressions (Shared.Created x) were a
// "not yet supported" gate; they now resolve through the dotted binding the
// loader registers per module export. The bare Name keeps driving runtime
// tag matching and exhaustiveness, so the qualifier is resolution-only.
// qualifyCustoms keys a module's custom types the way the project pipeline
// does since ADR 0027: `Shared.Outcome`, not `Outcome`. A type is identified
// by its module, so CheckModuleWith takes canonical keys.
func qualifyCustoms(module string, cts map[string]CustomType) map[string]CustomType {
	out := make(map[string]CustomType, len(cts))
	for name, ct := range cts {
		out[module+"."+name] = ct
	}
	return out
}

func TestQualifiedConstructorPatternAndExpr(t *testing.T) {
	resetVarIDsForTesting()
	shared, err := parser.Parse(`module Shared exposing (Outcome(..))
type Outcome = Created String | Rejected
`)
	if err != nil {
		t.Fatalf("parse shared: %v", err)
	}
	sharedRes, err := CheckModule(shared)
	if err != nil {
		t.Fatalf("check shared: %v", err)
	}
	env := BaseEnv()
	for name, ty := range sharedRes.ValueTypes {
		env = env.Bind("Shared."+name, ty)
	}
	main, err := parser.Parse(`module Main exposing (..)
describe : Shared.Outcome -> String
describe o =
    case o of
        Shared.Created name -> name
        Shared.Rejected -> "rejected"

made : Shared.Outcome
made = Shared.Created "x"
`)
	if err != nil {
		t.Fatalf("parse main: %v", err)
	}
	if _, err := CheckModuleWith(main, env, nil, qualifyCustoms("Shared", sharedRes.CustomTypes)); err != nil {
		t.Fatalf("qualified ctor pattern + expression should check, got: %v", err)
	}
}

func TestQualifiedConstructorExhaustivenessStillEnforced(t *testing.T) {
	resetVarIDsForTesting()
	shared, _ := parser.Parse(`module Shared exposing (Outcome(..))
type Outcome = Created String | Rejected
`)
	sharedRes, err := CheckModule(shared)
	if err != nil {
		t.Fatalf("check shared: %v", err)
	}
	env := BaseEnv()
	for name, ty := range sharedRes.ValueTypes {
		env = env.Bind("Shared."+name, ty)
	}
	main, _ := parser.Parse(`module Main exposing (..)
describe : Shared.Outcome -> String
describe o =
    case o of
        Shared.Created name -> name
`)
	_, err = CheckModuleWith(main, env, nil, qualifyCustoms("Shared", sharedRes.CustomTypes))
	// The message names the value nobody matched. It used to list the missing
	// constructor; the usefulness algorithm builds a witness instead, which
	// says the same thing for a nullary constructor and much more for a
	// nested one (`Just _`, `x :: _ :: _`).
	if err == nil || !strings.Contains(err.Error(), "no branch matches Rejected") {
		t.Fatalf("qualified patterns must still participate in exhaustiveness, got: %v", err)
	}
}
