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
