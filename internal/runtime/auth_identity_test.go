package runtime

import (
	"strings"
	"testing"
)

// `identify` was stored by Auth.config and read by nothing. The column that
// sign-in queries — and that the migrator makes UNIQUE — was guessed: a field
// named `email`, else the first TEXT NOT NULL column in declaration order.
// These pin the declaration as the answer.

func userEntity(fields ...string) VEntity {
	e := VEntity{Table: "users"}
	for _, name := range fields {
		e.Fields = append(e.Fields, EntityField{Name: name, SQLType: "TEXT", NotNull: true})
	}
	return e
}

// projects models `\u -> u.<field>`: the only shape `identify` is meant to be.
func projects(field string) Value {
	return VFn{
		Arity: 1,
		Native: func(args []Value) (Value, error) {
			rec, ok := args[0].(VRecord)
			if !ok {
				return nil, nil
			}
			return rec.Fields[field], nil
		},
	}
}

func TestIdentifyDecidesTheColumnTheGuessWouldGetWrong(t *testing.T) {
	// The guess takes the first TEXT NOT NULL column, which is `name`.
	// Sign-in then looked visitors up by their name, and the migrator put a
	// UNIQUE index on `name` that the app never asked for.
	cfg := VAuth{Entity: userEntity("name", "login"), Identify: projects("login")}

	col, fromIdentify := ResolveIdentityColumn(cfg)
	if !fromIdentify {
		t.Fatal("a plain field read is exactly what identify is for")
	}
	if col != "login" {
		t.Fatalf("the program says `login`, got %q", col)
	}
}

func TestIdentifyAgreesWithTheGuessInTheOrdinaryCase(t *testing.T) {
	// Every example in the repository has this shape, and the answer must
	// not move.
	cfg := VAuth{Entity: userEntity("email", "name"), Identify: projects("email")}
	if col, _ := ResolveIdentityColumn(cfg); col != "email" {
		t.Fatalf("got %q", col)
	}
}

func TestIdentifyOutranksAFieldNamedEmail(t *testing.T) {
	// An entity with both: the old code stopped at the name `email` without
	// ever consulting the projection.
	cfg := VAuth{Entity: userEntity("email", "workEmail"), Identify: projects("workEmail")}
	col, fromIdentify := ResolveIdentityColumn(cfg)
	if !fromIdentify || col != "workEmail" {
		t.Fatalf("the declaration decides, got %q (from identify: %v)", col, fromIdentify)
	}
}

func TestAProjectionThatNamesNoFieldIsRefusedAtBoot(t *testing.T) {
	// `\u -> String.toLower u.email` answers with no field name, so the
	// framework would fall back to guessing. That is not something to
	// discover as a visitor who cannot sign in.
	lowered := VFn{
		Arity:  1,
		Native: func([]Value) (Value, error) { return VString{V: "not-a-field"}, nil },
	}
	cfg := VAuth{Entity: userEntity("email", "name"), Identify: lowered}

	if _, fromIdentify := ResolveIdentityColumn(cfg); fromIdentify {
		t.Fatal("nothing on the entity is called `not-a-field`")
	}
	err := CheckAuthIdentity(&cfg)
	if err == nil {
		t.Fatal("a column the framework has to guess is a boot-time fact")
	}
	for _, want := range []string{"identify", "`email`", "`name`"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestNoAuthAndNoIdentifyAreNotRefused(t *testing.T) {
	if err := CheckAuthIdentity(nil); err != nil {
		t.Fatalf("an app with no auth: %v", err)
	}
	if err := CheckAuthIdentity(&VAuth{Entity: userEntity("email")}); err != nil {
		t.Fatalf("nothing to check when identify is absent: %v", err)
	}
}

func TestTheUniqueIndexFollowsTheDeclaredColumn(t *testing.T) {
	// The guess did not only decide what sign-in queries: it decided which
	// column the migrator makes UNIQUE, so two users could not share a name.
	t.Cleanup(ResetAuth)
	ent := userEntity("name", "login")
	RegisterAuth(VAuth{Entity: ent, Identify: projects("login")})

	out := ensureAuthIdentityUnique([]VEntity{ent})
	var got []string
	for _, idx := range out[0].UniqueIndexes {
		got = append(got, strings.Join(idx, "+"))
	}
	if len(got) != 1 || got[0] != "login" {
		t.Fatalf("UNIQUE belongs on the identity column, got %v", got)
	}
}

// Two `Auth.config` values in one program: the last one evaluated became the
// app's authentication and the other vanished, with nothing said. Which one
// wins is decided by file order.
func TestTwoDisagreeingAuthConfigsRefuseTheBoot(t *testing.T) {
	t.Cleanup(ResetAuth)
	if err := RegisterAuth(VAuth{Entity: userEntity("email"), SignInPath: "/sign-in"}); err != nil {
		t.Fatalf("the first one is the app's auth: %v", err)
	}
	err := RegisterAuth(VAuth{Entity: userEntity("email"), SignInPath: "/login"})
	if err == nil {
		t.Fatal("two configurations, one of which silently disappears")
	}
	if !strings.Contains(err.Error(), "/login") || !strings.Contains(err.Error(), "/sign-in") {
		t.Errorf("the message must name both, got: %v", err)
	}
}

func TestTheSameAuthDeclaredTwiceIsNotAConflict(t *testing.T) {
	// Refusing this would refuse a program for saying the same thing twice,
	// and two closures are never equal in Go — so presence, not identity, is
	// what the function-valued fields are compared on.
	t.Cleanup(ResetAuth)
	mk := func() VAuth {
		return VAuth{Entity: userEntity("email"), Identify: projects("email"), SignInPath: "/sign-in"}
	}
	if err := RegisterAuth(mk()); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAuth(mk()); err != nil {
		t.Fatalf("nothing to choose between: %v", err)
	}
}

func TestDisagreementOnTheIdentityColumnIsCaught(t *testing.T) {
	// Same entity, same paths: the difference is which field signs people in.
	t.Cleanup(ResetAuth)
	ent := userEntity("email", "workEmail")
	if err := RegisterAuth(VAuth{Entity: ent, Identify: projects("email")}); err != nil {
		t.Fatal(err)
	}
	err := RegisterAuth(VAuth{Entity: ent, Identify: projects("workEmail")})
	if err == nil {
		t.Fatal("the two look people up by different columns")
	}
	if !strings.Contains(err.Error(), "workEmail") {
		t.Errorf("the message must name the columns, got: %v", err)
	}
}

// `Auth.requireRole`'s argument and `Auth.config`'s `role` getter are two
// halves of one comparison, written in different places. The type system
// cannot link them — `requireRole` receives no reference to the config — and
// neither can the shape lint: by the time it runs, the unification that
// pinned the getter's result has been generalized away.
//
// The boot can, with the trick `identify` uses. It matters because the
// dispatcher compares with equalValues and answers 403 on a mismatch, which
// for two different types is EVERY request, for everyone, with no error
// anywhere. Measured before this: `Auth.requireRole 42` against a String role
// checked clean, booted clean, and left the route dead in silence.

func roleEntity(kind string, ctors []string) VEntity {
	f := EntityField{Name: "role", SQLType: kind, NotNull: true, AcceptedCtors: ctors}
	return VEntity{Table: "users", Fields: []EntityField{
		{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
		{Name: "email", SQLType: "TEXT", NotNull: true},
		f,
	}}
}

func gated(want Value) VExposedService {
	es := svcFor("POST", "/api/ban")
	es.Service.RequireRole = want
	return es
}

func svcFor(verb, path string) VExposedService {
	return VExposedService{Service: VService{Verb: verb, Path: path}}
}

func TestARoleTheColumnCanNeverHoldIsRefused(t *testing.T) {
	cfg := &VAuth{Entity: roleEntity("TEXT", nil), Role: projects("role")}
	err := CheckAuthRoles(cfg, []VExposedService{gated(VInt{V: 42})})
	if err == nil {
		t.Fatal("a whole number is never the text a role column holds")
	}
	for _, want := range []string{"/api/ban", "role", "42"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestARoleTheColumnHoldsIsAccepted(t *testing.T) {
	cfg := &VAuth{Entity: roleEntity("TEXT", nil), Role: projects("role")}
	if err := CheckAuthRoles(cfg, []VExposedService{gated(VString{V: "admin"})}); err != nil {
		t.Fatalf("this is the ordinary shape: %v", err)
	}
}

func TestAnEnumRoleTakesItsConstructor(t *testing.T) {
	// The shape two shipped examples use. Calling the plain-kind check
	// instead of the enum-aware one refused both, and the floor test caught
	// it — the enum arm lives above the kind check.
	cfg := &VAuth{Entity: roleEntity("TEXT", []string{"Admin", "Member"}), Role: projects("role")}
	if err := CheckAuthRoles(cfg, []VExposedService{gated(VCtor{Tag: "Admin"})}); err != nil {
		t.Fatalf("`Admin` is exactly what this column holds: %v", err)
	}
	if err := CheckAuthRoles(cfg, []VExposedService{gated(VCtor{Tag: "Owner"})}); err == nil {
		t.Fatal("`Owner` is not in the accepted set")
	}
}

func TestNoRoleGetterMeansNothingToCompare(t *testing.T) {
	if err := CheckAuthRoles(&VAuth{Entity: roleEntity("TEXT", nil)}, []VExposedService{gated(VInt{V: 1})}); err != nil {
		t.Fatalf("an app that models no roles: %v", err)
	}
	if err := CheckAuthRoles(nil, nil); err != nil {
		t.Fatalf("an app with no auth: %v", err)
	}
}

func TestAGetterThatIsNotAFieldReadSaysNothing(t *testing.T) {
	// `\u -> String.toUpper u.role` names no field, so there is no column to
	// compare against. Refusing on a guess would be worse than staying quiet.
	odd := VFn{Arity: 1, Native: func([]Value) (Value, error) { return VString{V: "not-a-field"}, nil }}
	cfg := &VAuth{Entity: roleEntity("TEXT", nil), Role: odd}
	if err := CheckAuthRoles(cfg, []VExposedService{gated(VInt{V: 42})}); err != nil {
		t.Fatalf("nothing certain to compare: %v", err)
	}
}

// `email.body` is OPTIONAL, so it cannot be a field of `Auth.config`'s record
// type — a field in a record type is required, and most apps write none. Its
// type is therefore unchecked: measured, `body = \code -> code` (one argument
// where two belong) passes `mar check`.
//
// At request time the framework falls back to its own body, deliberately, so
// a typo does not take auth down. The cost is that the app's branded email
// silently becomes the generic one, forever, with nobody told.

func TestAnEmailBodyOfTheWrongShapeStopsTheBoot(t *testing.T) {
	// One argument where two belong.
	oneArg := VFn{Arity: 1, Native: func(a []Value) (Value, error) { return a[0], nil }}
	err := CheckAuthEmailBody(&VAuth{EmailBody: oneArg})
	if err == nil {
		t.Fatal("this body can never render, and the app's email would never go out")
	}
	if !strings.Contains(err.Error(), "code and a number of minutes") {
		t.Errorf("the message must say what it is called with, got: %v", err)
	}
	// The runtime's internal-error text says "this is a bug in Mar rather
	// than in your code. Please report it", which is exactly backwards here.
	if strings.Contains(err.Error(), "bug in Mar") {
		t.Error("the mistake is in the app; the message must not send them to report a Mar bug")
	}
}

func TestAWellShapedEmailBodyIsAccepted(t *testing.T) {
	twoArgs := VFn{Arity: 2, Native: func([]Value) (Value, error) { return VString{V: "hi"}, nil }}
	if err := CheckAuthEmailBody(&VAuth{EmailBody: twoArgs}); err != nil {
		t.Fatalf("this is the documented shape: %v", err)
	}
}

func TestNoEmailBodyIsTheOrdinaryCase(t *testing.T) {
	// Most apps write none, and the framework's own body is the point.
	if err := CheckAuthEmailBody(&VAuth{}); err != nil {
		t.Fatalf("an absent body is not a problem: %v", err)
	}
	if err := CheckAuthEmailBody(nil); err != nil {
		t.Fatalf("an app with no auth: %v", err)
	}
}

// `signInPage` is the third optional field, and optional means untyped: a
// field in a record type is required. So `signInPage = 42` passes `mar check`
// and reaches the boot — one of the few boot messages a developer can
// actually arrive at, which is why it should not answer with a Go type name.
func TestTheSignInPageMessageNamesTheValueTheWayTheLanguageDoes(t *testing.T) {
	t.Cleanup(ResetAuth)
	_, err := makeAuthConfig([]Value{VRecord{
		Fields: map[string]Value{
			"entity":          VEntity{Table: "users", Fields: []EntityField{{Name: "id", SQLType: "INTEGER", Serial: true}}},
			"identify":        projects("id"),
			"signup":          VFn{Arity: 1, Native: func([]Value) (Value, error) { return VRecord{}, nil }},
			"email":           VRecord{Fields: map[string]Value{"subject": VString{V: "s"}}, Order: []string{"subject"}},
			"sessionDuration": VDuration{Seconds: 60},
			"signInPage":      VInt{V: 42},
		},
		Order: []string{"entity", "identify", "signup", "email", "sessionDuration", "signInPage"},
	}})
	if err == nil {
		t.Fatal("42 is not a page")
	}
	if strings.Contains(err.Error(), "VInt") || strings.Contains(err.Error(), "runtime.") {
		t.Errorf("the message names a Go type the developer has never heard of: %v", err)
	}
	if !strings.Contains(err.Error(), "whole number 42") {
		t.Errorf("it should name the value the way the language does, got: %v", err)
	}
}

// `Auth.config`'s record type is OPEN — its tail carries the optional fields
// — so a name outside the set it reads is accepted by the checker and then
// read by nobody. Symmetric with the page record, and for the same reason: an
// open record cannot tell a typo from an optional field.
//
// Measured: `rol = \u -> u.email` passes `mar check` and leaves the app with
// no role getter, which only a service using `Auth.requireRole` would ever
// notice — and only by refusing every request to itself.
func TestAFieldAuthConfigDoesNotReadIsRefused(t *testing.T) {
	t.Cleanup(ResetAuth)
	base := map[string]Value{
		"entity":          VEntity{Table: "users", Fields: []EntityField{{Name: "id", SQLType: "INTEGER", Serial: true}}},
		"identify":        projects("id"),
		"signup":          VFn{Arity: 1, Native: func([]Value) (Value, error) { return VRecord{}, nil }},
		"email":           VRecord{Fields: map[string]Value{"subject": VString{V: "s"}}, Order: []string{"subject"}},
		"sessionDuration": VDuration{Seconds: 60},
	}
	order := []string{"entity", "identify", "signup", "email", "sessionDuration"}

	if _, err := makeAuthConfig([]Value{VRecord{Fields: base, Order: order}}); err != nil {
		t.Fatalf("the required set alone is what most apps write: %v", err)
	}

	withTypo := map[string]Value{"rol": projects("id")}
	for k, v := range base {
		withTypo[k] = v
	}
	_, err := makeAuthConfig([]Value{VRecord{Fields: withTypo, Order: append(order, "rol")}})
	if err == nil {
		t.Fatal("`rol` is read by nobody, and the app silently has no roles")
	}
	for _, want := range []string{"rol", "role"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestTheOptionalFieldsAreNotRejectedAsUnknown(t *testing.T) {
	// The whole point of the tail: `role`, `signInPage` and `email.body` are
	// legitimate and most apps write none of them.
	t.Cleanup(ResetAuth)
	fields := map[string]Value{
		"entity":          VEntity{Table: "users", Fields: []EntityField{{Name: "id", SQLType: "INTEGER", Serial: true}}},
		"identify":        projects("id"),
		"signup":          VFn{Arity: 1, Native: func([]Value) (Value, error) { return VRecord{}, nil }},
		"email":           VRecord{Fields: map[string]Value{"subject": VString{V: "s"}}, Order: []string{"subject"}},
		"sessionDuration": VDuration{Seconds: 60},
		"role":            projects("id"),
	}
	order := []string{"entity", "identify", "signup", "email", "sessionDuration", "role"}
	if _, err := makeAuthConfig([]Value{VRecord{Fields: fields, Order: order}}); err != nil {
		t.Fatalf("`role` is one of its fields: %v", err)
	}
}
