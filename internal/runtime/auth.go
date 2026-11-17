package runtime

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// VAuth is the runtime registration produced by `Auth.config`. It
// carries everything the framework needs to operate the email-code
// flow: which user entity holds the records, which field is the
// email, what the signup hook returns, and the session duration.
//
// `Auth.config` is normally bound at the top level of `Main.mar`. The
// project loader stamps the registration into a process-wide singleton
// (RegisteredAuth) so the dispatcher and the framework HTTP handlers
// can find it without an explicit thread-through.
type VAuth struct {
	Entity   VEntity
	Identify Value // user -> String

	// EmailSubject is the Subject: header for the sign-in code email.
	// Required in Auth.config: gives each app a chance to brand it
	// ("Sign in to Notes" vs the generic default).
	//
	// The From: header is NOT in this struct. It lives in mar.json's
	// `mail.from`: the single source of truth for the address
	// verified with the SMTP provider. Setting it in Mar source too
	// would invite typos that silently shadow the manifest value
	// and break delivery at the provider ("550 domain not verified").
	EmailSubject string

	// EmailBody is an optional `String -> Int -> String` function
	// (code, ttlMinutes → body) provided in `Auth.config { email.body }`.
	// nil means the framework's default body is used. Applied via
	// Eval.apply at request-code time; failures fall back to the
	// default so a typo in the user's body fn doesn't take down auth.
	EmailBody       Value
	Signup          Value // String -> userExceptId
	SessionDuration int64 // seconds; <=0 means use the framework default

	// Role is the optional getter `\user -> role` declared in
	// Auth.config. nil when the app doesn't model roles (then
	// Auth.requireRole is a misconfiguration). The dispatcher invokes
	// this on the loaded User to extract the role for comparison
	// against Auth.requireRole's argument.
	Role Value

	// SignInPath is the URL Page.protected redirects to when there's
	// no session. Extracted from the `signInPage : Page` field in
	// Auth.config so the source of truth is the Page itself:
	// renaming the path on Frontend.SignIn.page propagates here
	// automatically. Empty when the app has no Page.protected.
	SignInPath string
}

func (VAuth) isValue()        {}
func (VAuth) Display() string { return "<auth>" }

var (
	regMu   sync.RWMutex
	regAuth *VAuth
)

// RegisterAuth captures the most recent Auth.config result so the
// HTTP dispatcher can find it. The runtime sets this when evaluating
// the user's Main module; subsequent reads are concurrent-safe.
func RegisterAuth(a VAuth) error {
	regMu.Lock()
	defer regMu.Unlock()
	if regAuth != nil {
		if what := authDiffers(*regAuth, a); what != "" {
			return fmt.Errorf(
				"this program evaluates `Auth.config` twice, and the two disagree about %s.\n"+
					"  Only one can be the app's authentication: the last one evaluated wins, "+
					"and the other is simply gone — sign-in, the protected routes and the "+
					"identity column all follow the winner.\n"+
					"  Keep one `Auth.config`",
				what)
		}
	}
	regAuth = &a
	return nil
}

// authDiffers names the first thing two configurations disagree about, and is
// empty when they agree. Two identical registrations leave nothing to choose
// between, so they are not a conflict — only a difference is.
//
// The function-valued fields are compared on whether they are present rather
// than on identity: two closures are never equal in Go, and a program that
// declares the same auth twice would then be refused for saying the same thing
// twice.
func authDiffers(a, b VAuth) string {
	switch {
	case a.Entity.Table != b.Entity.Table:
		return fmt.Sprintf("which entity holds the users (`%s` and `%s`)", a.Entity.Table, b.Entity.Table)
	case a.SignInPath != b.SignInPath:
		return fmt.Sprintf("where signed-out visitors go (`%s` and `%s`)", a.SignInPath, b.SignInPath)
	case a.EmailSubject != b.EmailSubject:
		return fmt.Sprintf("the sign-in email subject (%q and %q)", a.EmailSubject, b.EmailSubject)
	case a.SessionDuration != b.SessionDuration:
		return fmt.Sprintf("how long a session lasts (%d and %d seconds)", a.SessionDuration, b.SessionDuration)
	case (a.Role == nil) != (b.Role == nil):
		return "whether the app models roles"
	case (a.Signup == nil) != (b.Signup == nil):
		return "whether there is a signup hook"
	}
	colA, _ := ResolveIdentityColumn(a)
	colB, _ := ResolveIdentityColumn(b)
	if colA != colB {
		return fmt.Sprintf("which column identifies a user (`%s` and `%s`)", colA, colB)
	}
	return ""
}

// CurrentAuth returns the registered Auth, if any. Used by the HTTP
// dispatcher to know whether to mount /_auth/* and to satisfy
// Auth.protected services.
func CurrentAuth() *VAuth {
	regMu.RLock()
	defer regMu.RUnlock()
	return regAuth
}

// ResetAuth clears the registered Auth so a re-evaluation starts from the
// source rather than from what a previous one left behind.
//
// This registry was the one global ResetForReload did not clear, and the
// omission is visible: loading a program WITHOUT `Auth.config` after one that
// had it leaves the old config live, so `mar dev` goes on mounting `/_auth/*`
// and protecting routes with a configuration the source no longer contains.
// Deleting `Auth.config` and saving did nothing until the process restarted.
//
// Clearing is safe because there is exactly one way in: evaluating
// `Auth.config`, which a reload re-runs. A program that has auth gets it back
// on the same pass that cleared it.
func ResetAuth() {
	regMu.Lock()
	regAuth = nil
	regMu.Unlock()
}

// ResetAuthForTesting is the old spelling, kept for the tests that call it.
func ResetAuthForTesting() { ResetAuth() }

// authBuiltins registers the language-level surface for authentication.
//
//	Auth.config  : { entity, identify, email, signup, sessionDuration } -> Auth user
//	Auth.protect : Service req resp -> (req -> user -> Task resp) -> ExposedService
//
// The four user-facing client effects (Auth.requestCode / verifyCode /
// logout / me) are JS-runtime-only: they're built-ins on the browser
// side that hit the framework HTTP endpoints directly.
func authBuiltins() map[string]Value {
	return map[string]Value{
		"authConfig":  nativeFn(1, makeAuthConfig),
		"authProtect": nativeFn(2, makeAuthProtect),

		// PROPOSAL stubs (see docs/authorization-proposal.md). No-op
		// pass-throughs that return the ExposedService unchanged.
		"authRequireRole":  nativeFn(2, makeAuthRequireRole),
		"authAuthorize":    nativeFn(3, makeAuthAuthorize),
		"authRequireOwner": nativeFn(3, makeAuthRequireOwner),

		// Browser-only Effects. On the Go side they error out: the
		// JS runtime overrides them with fetch-based implementations
		// (see runtime.js). Mirrors the Service.call / Http.get pattern.
		"authRequestCode": nativeFn(2, browserOnlyEffect("Auth.requestCode")),
		"authVerifyCode":  nativeFn(2, browserOnlyEffect("Auth.verifyCode")),
		"authLogout":      nativeFn(1, browserOnlyEffect("Auth.logout")),
		"authMe":          nativeFn(1, browserOnlyEffect("Auth.me")),
	}
}

func browserOnlyEffect(name string) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		return VEffect{
			Tag: name,
			Run: func() (Value, error) {
				return nil, fmt.Errorf("%s is only available in the browser runtime", name)
			},
		}, nil
	}
}

// ApplyEmailBody invokes a user-supplied `email.body` function,
// which has type `String -> Int -> String` (code, ttlMinutes →
// body). Returns the rendered body or an error if the function
// rejects (wrong type returned, runtime fault, etc.). Used by the
// auth dispatcher when sending the request-code email.
func ApplyEmailBody(fn Value, code string, ttlMinutes int) (string, error) {
	step1, err := Apply(fn, VString{V: code})
	if err != nil {
		return "", err
	}
	step2, err := Apply(step1, VInt{V: int64(ttlMinutes)})
	if err != nil {
		return "", err
	}
	out, ok := step2.(VString)
	if !ok {
		return "", fmt.Errorf("email.body: expected String result (got %T)", step2)
	}
	return out.V, nil
}

func makeAuthConfig(args []Value) (Value, error) {
	rec, ok := args[0].(VRecord)
	if !ok {
		return nil, fmt.Errorf("Auth.config: expected record (got %T)", args[0])
	}
	entity, ok := rec.Fields["entity"].(VEntity)
	if !ok {
		return nil, fmt.Errorf("Auth.config: `entity` must be an Entity (got %T)", rec.Fields["entity"])
	}
	identify, ok := rec.Fields["identify"]
	if !ok {
		return nil, fmt.Errorf("Auth.config: missing `identify`")
	}
	emailRec, ok := rec.Fields["email"].(VRecord)
	if !ok {
		return nil, fmt.Errorf("Auth.config: `email` must be a record")
	}
	// `email.from` is intentionally NOT accepted here: it duplicates
	// mar.json's `mail.from` (the address verified with the SMTP
	// provider). Reject explicitly so a stale paste doesn't silently
	// take over and trip "550 domain not verified" at the provider.
	if _, ok := emailRec.Fields["from"]; ok {
		return nil, fmt.Errorf(
			"Auth.config: email.from is not accepted — set %q in %s instead",
			"mail.from", "mar.json")
	}
	subject, _ := emailRec.Fields["subject"].(VString)
	if subject.V == "" {
		return nil, fmt.Errorf("Auth.config: email.subject is required")
	}
	// Optional `email.body : String -> Int -> String`, given the
	// generated code and TTL in minutes, produces the email body.
	// When omitted, the framework's auth.DefaultBody fills in a
	// transactional default. Useful for branding ("Welcome to App!
	// Your code is …"), localized copy, or simply tweaking tone.
	emailBody := emailRec.Fields["body"]
	signup, ok := rec.Fields["signup"]
	if !ok {
		return nil, fmt.Errorf("Auth.config: missing `signup`")
	}
	// `sessionDuration` must be a Duration (e.g. `Time.days 30`).
	// A bare Int is rejected: ambiguous units ("is 86400 seconds
	// or minutes?") are the kind of subtle bug worth surfacing
	// loudly. The compiler's type signature for Auth.config also
	// expects Duration here, so this is just the runtime guard.
	durationSecs := int64(0)
	if d, ok := rec.Fields["sessionDuration"].(VDuration); ok {
		durationSecs = int64(d.Seconds)
	}
	// `role` is optional; only required when the app uses
	// Auth.requireRole. We don't validate the field's type here: the
	// dispatcher applies it as a function and surfaces failures as 500.
	role := rec.Fields["role"]
	// `signInPage` is a Page reference (typically Frontend.SignIn.page)
	// that Page.protected redirects to when the user isn't logged in.
	// Optional: backend-only apps don't have pages at all. When the
	// app declares any Page.protected, the bundle bootstrap errors
	// loudly if signInPage was missing.
	signInPath := ""
	if pageVal, ok := rec.Fields["signInPage"]; ok {
		page, ok := pageVal.(VPage)
		if !ok {
			// `describeValueKind`, not `%T`. `signInPage` is OPTIONAL, so it
			// cannot be typed — a field in a record type is required — which
			// makes this one of the few boot messages a developer can
			// actually reach. Answering it with `runtime.VInt` names a Go
			// type they have never heard of.
			return nil, fmt.Errorf(
				"`Auth.config`'s `signInPage` is %s, and it has to be a Page.\n"+
					"  It is where a signed-out visitor is sent, so it names one of the app's own "+
					"pages.\n"+
					"  Pass the page value itself, as in `signInPage = Frontend.SignIn.page`",
				describeValueKind(pageVal))
		}
		signInPath = page.Path
	}
	// The record type is OPEN — its tail has to carry the optional fields —
	// so a name outside this set is accepted by the checker and then read by
	// nobody. `rol = \\u -> u.role` leaves the app with no role getter at
	// all, and the only thing that notices is a service using
	// `Auth.requireRole`, if there is one.
	//
	// Symmetric with the page record, and for the same reason: an open
	// record cannot tell a typo from an optional field, so the boot does.
	if err := rejectUnknownAuthFields(rec); err != nil {
		return nil, err
	}

	cfg := VAuth{
		Entity:          entity,
		Identify:        identify,
		EmailSubject:    subject.V,
		EmailBody:       emailBody,
		Signup:          signup,
		SessionDuration: durationSecs,
		Role:            role,
		SignInPath:      signInPath,
	}
	if err := RegisterAuth(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func makeAuthProtect(args []Value) (Value, error) {
	contract, ok := args[0].(VService)
	if !ok {
		return nil, fmt.Errorf("Auth.protect: expected Service contract (got %T)", args[0])
	}
	handler := args[1]
	contract.Handler = handler
	contract.RequiresUser = true
	return VExposedService{Service: contract}, nil
}

// makeAuthRequireRole / makeAuthAuthorize / makeAuthRequireOwner are
// the authorization decorators from docs/authorization-proposal.md.
// Each attaches policy state to the wrapped ExposedService; the
// dispatcher (ExposedServiceToRoute) reads that state and runs the
// gates before invoking the user's handler.
//
// Decorators are pure: they don't run the gates themselves, they only
// record what the dispatcher should do. This keeps the wiring code in
// `services = [...]` static and inspectable.

// Auth.requireRole : role -> ExposedService -> ExposedService
//
//	args = [role, exposed]
func makeAuthRequireRole(args []Value) (Value, error) {
	exposed, ok := args[1].(VExposedService)
	if !ok {
		return nil, fmt.Errorf("Auth.requireRole: expected ExposedService (got %T)", args[1])
	}
	exposed.Service.RequireRole = args[0]
	// A role gate is meaningless without an authenticated user, so an
	// authorization decorator always implies authentication. Without this,
	// `Auth.requireRole role (Service.implement c h)` (i.e. decorating a raw
	// service instead of an `Auth.protect`ed one) leaves RequiresUser=false,
	// the dispatcher skips every gate, and the "protected" service serves
	// public. See docs/security-audit-2026-07-15.md #1.
	exposed.Service.RequiresUser = true
	return exposed, nil
}

// Auth.authorize : (loader) -> (policy) -> ExposedService -> ExposedService
//
//	args = [loader, policy, exposed]
func makeAuthAuthorize(args []Value) (Value, error) {
	exposed, ok := args[2].(VExposedService)
	if !ok {
		return nil, fmt.Errorf("Auth.authorize: expected ExposedService (got %T)", args[2])
	}
	exposed.Service.LoadResource = args[0]
	exposed.Service.Policy = args[1]
	// A policy gate implies an authenticated user (see #1 above).
	exposed.Service.RequiresUser = true
	return exposed, nil
}

// Auth.requireOwner : (loader) -> (selector) -> ExposedService -> ExposedService
//
//	args = [loader, selector, exposed]
//
// Sugar for the common ABAC case "user owns this resource". Desugars
// to Auth.authorize with a synthesized policy that compares
// `selector(resource)` against `user.id`.
func makeAuthRequireOwner(args []Value) (Value, error) {
	exposed, ok := args[2].(VExposedService)
	if !ok {
		return nil, fmt.Errorf("Auth.requireOwner: expected ExposedService (got %T)", args[2])
	}
	loader := args[0]
	selector := args[1]
	// Synthesized policy: \input user resource -> selector(resource) == user.id
	// We build a curried 3-arg native function so it composes the same
	// way a user-written policy would.
	policy := nativeFn(3, func(pargs []Value) (Value, error) {
		// pargs = [input, user, resource]
		ownerID, err := Apply(selector, pargs[2])
		if err != nil {
			return nil, fmt.Errorf("Auth.requireOwner: selector failed: %w", err)
		}
		// The key field's NAME, not the literal "id". `Entity.serial` can be
		// called anything, `idColumnName` is what the rest of the framework
		// asks, and this asked for "id" — so every request to an ownership-
		// gated service answered 500 on an app whose users table has a `pk`.
		//
		// Read here rather than when the policy was built: top-level values
		// evaluate in file order, so `Auth.config` may not have run yet at
		// that point. By the time a request arrives, `main` has finished.
		key := "id"
		if cfg := CurrentAuth(); cfg != nil {
			key = idColumnName(cfg.Entity)
		}
		userID, err := projectField(pargs[1], key)
		if err != nil {
			return nil, fmt.Errorf("Auth.requireOwner: the signed-in user has no `%s` field: %w", key, err)
		}
		return VBool{V: equalValues(ownerID, userID)}, nil
	})
	exposed.Service.LoadResource = loader
	exposed.Service.Policy = policy
	// An ownership gate implies an authenticated user (see #1 above).
	exposed.Service.RequiresUser = true
	return exposed, nil
}

// AuthDB returns the project's SQLite handle if it's been opened, so
// the auth HTTP handlers can read/write `_mar_auth_*` rows. Mirrors
// `getDB()` but exported (the lazy-open is shared with Repo).
func AuthDB() (*sql.DB, error) {
	return getDB()
}

// EnsureUser looks up a user by email; if missing, runs the signup
// hook from VAuth and creates the row via Repo.create. Returns the
// user's `id` (the Serial column).
//
// Public so jsserve.handleRequestCode can call it without poking at
// runtime internals.
func EnsureUser(cfg VAuth, email string) (int64, error) {
	id, err := LookupUserID(cfg, email)
	if err == nil {
		return id, nil
	}
	// Run the user-supplied signup hook.
	v, err := Apply(cfg.Signup, VString{V: email})
	if err != nil {
		return 0, fmt.Errorf("signup hook: %w", err)
	}
	rec, ok := v.(VRecord)
	if !ok {
		return 0, fmt.Errorf("signup hook must return a record (got %T)", v)
	}
	// Auto-fill TIMESTAMP columns with the current server time.
	// Signup hooks are synchronous (no Effect access), so they can't
	// call Time.now themselves: the framework patches in `now` for
	// any timestamp field the hook didn't already provide a valid
	// VTime for. A common pattern is `createdAt = 0` in the hook
	// record as a sentinel meaning "fill this in for me"; we treat
	// any non-VTime value (including missing) as that signal.
	rec = fillTimestampsForSignup(cfg.Entity, rec)
	// Pipe through Repo.create. We can re-use the same code path users do.
	created, err := repoCreateInner(cfg.Entity, rec)
	if err != nil {
		return 0, fmt.Errorf("Repo.create: %w", err)
	}
	idVal, err := projectField(created, idColumnName(cfg.Entity))
	if err != nil {
		return 0, err
	}
	idInt, ok := idVal.(VInt)
	if !ok {
		return 0, fmt.Errorf("user id is not an Int")
	}
	return idInt.V, nil
}

// fillTimestampsForSignup overlays VTime{now} onto any TIMESTAMP
// column whose value in the signup record isn't already a VTime.
// Returns a copy with the Fields map cloned so the caller's record
// isn't mutated. Field order is preserved; if the entity declares a
// timestamp column the hook omitted, the column name is appended.
func fillTimestampsForSignup(entity VEntity, rec VRecord) VRecord {
	now := VTime{Millis: time.Now().UnixMilli()}
	out := VRecord{
		Fields: make(map[string]Value, len(rec.Fields)+1),
		Order:  append([]string(nil), rec.Order...),
	}
	for k, v := range rec.Fields {
		out.Fields[k] = v
	}
	for _, field := range entity.Fields {
		if field.SQLType != "TIMESTAMP" {
			continue
		}
		if _, isTime := out.Fields[field.Name].(VTime); isTime {
			continue
		}
		out.Fields[field.Name] = now
		// Keep Order in sync: append only if the hook didn't already
		// list the field (e.g. as a non-Time sentinel).
		present := false
		for _, n := range out.Order {
			if n == field.Name {
				present = true
				break
			}
		}
		if !present {
			out.Order = append(out.Order, field.Name)
		}
	}
	return out
}

// LookupUserID returns the id of the user whose `identify` projection
// equals the given email, or an error if no such row.
func LookupUserID(cfg VAuth, email string) (int64, error) {
	// The same door every Repo.* call goes through: a cache hit once the
	// boot pass has run, the real migrator otherwise. This used to issue its
	// own `CREATE TABLE IF NOT EXISTS` on every sign-in, built by a second
	// DDL writer that only this call site used.
	db, err := ensureMigrated(cfg.Entity)
	if err != nil {
		return 0, err
	}
	emailCol, err := identifyColumn(cfg)
	if err != nil {
		return 0, err
	}
	idCol := idColumnName(cfg.Entity)
	row := db.QueryRow(
		"SELECT "+quoteIdent(idCol)+" FROM "+quoteIdent(cfg.Entity.Table)+" WHERE "+quoteIdent(emailCol)+" = ? LIMIT 1",
		email,
	)
	var id int64
	if err := row.Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// LoadUserJSON returns the user record for `id` in the wire format, as
// /_auth/whoami and /_auth/verify-code send it to the client, who write these
// bytes as they are.
//
// It is the wire encoder (appendValue), the one every Service response goes
// through, so every column type the wire knows (a Decimal, a Time with its
// milliseconds) reaches the sign-in answer in the same form.
func LoadUserJSON(cfg VAuth, id int64) (json.RawMessage, error) {
	v, err := loadUserValue(cfg, id)
	if err != nil {
		return nil, err
	}
	if _, ok := v.(VRecord); !ok {
		return nil, fmt.Errorf("user row is not a record")
	}
	buf, err := appendValue(make([]byte, 0, 256), v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(buf), nil
}

// LoadUserValue returns the user row as a runtime VRecord, used by the
// dispatcher when injecting `__user` into protected service requests.
func LoadUserValue(cfg VAuth, id int64) (Value, error) {
	return loadUserValue(cfg, id)
}

func loadUserValue(cfg VAuth, id int64) (Value, error) {
	// Reuse Repo.findById's machinery via Apply with VInt id.
	eff, err := repoFindByID([]Value{cfg.Entity, VInt{V: id}})
	if err != nil {
		return nil, err
	}
	veff, ok := eff.(VEffect)
	if !ok {
		return nil, fmt.Errorf("findById didn't return Effect")
	}
	out, err := veff.Run()
	if err != nil {
		return nil, err
	}
	ctor, ok := out.(VCtor)
	if !ok || ctor.Tag != "Just" || len(ctor.Args) != 1 {
		return nil, fmt.Errorf("user not found")
	}
	return ctor.Args[0], nil
}

// repoCreateInner is repo.go's repoCreate logic invoked synchronously
// (it normally returns a deferred Effect). Mirrors that path so the
// auth signup flow runs through identical SQL/decode logic.
func repoCreateInner(entity VEntity, input VRecord) (Value, error) {
	eff, err := repoCreate([]Value{entity, input})
	if err != nil {
		return nil, err
	}
	veff, ok := eff.(VEffect)
	if !ok {
		return nil, fmt.Errorf("repoCreate didn't return Effect")
	}
	return veff.Run()
}

// projectField pulls a single field by name from a VRecord.
func projectField(v Value, name string) (Value, error) {
	rec, ok := v.(VRecord)
	if !ok {
		return nil, fmt.Errorf("cannot project field %q from %T", name, v)
	}
	val, ok := rec.Fields[name]
	if !ok {
		return nil, fmt.Errorf("missing field %q", name)
	}
	return val, nil
}

// idColumnName returns the name of the entity's serial primary key
// column. Auth requires entities to have one.
func idColumnName(e VEntity) string {
	for _, f := range e.Fields {
		if f.Serial {
			return f.Name
		}
	}
	return "id" // fallback; entities without serial PK will fail elsewhere
}

// identifyColumn returns the column that holds a user's identity: the one the
// sign-in flow queries, and the one the migrator makes UNIQUE.
//
// `Auth.config` takes an `identify` projection precisely to say which field
// that is, and until now the value was stored and never read. The column was
// GUESSED instead — a field named `email`, else the first TEXT NOT NULL column
// in declaration order — so an entity like
//
//	{ name : String, login : String }
//
// with `identify = \u -> u.login` was queried on `name`, and had a UNIQUE
// index created on `name` that the app never asked for. Sign-in looked up the
// wrong column, and two users could not share a name.
//
// The projection now decides. It is applied to a record whose every field
// holds its own name, so `\u -> u.login` comes back as the string "login".
// That is the technique the original comment described and did not do.
//
// The guess stays as a fallback for a projection that is not a plain field
// read (`\u -> String.toLower u.email` answers with no field name at all).
// CheckAuthIdentity refuses that at boot rather than leaving it to be
// discovered by a user who cannot sign in.
func identifyColumn(cfg VAuth) (string, error) {
	if col, ok := projectedIdentityColumn(cfg); ok {
		return col, nil
	}
	// Common case: an `email` column.
	for _, f := range cfg.Entity.Fields {
		if f.Name == "email" {
			return "email", nil
		}
	}
	// Fall back to the first TEXT NOT NULL column.
	for _, f := range cfg.Entity.Fields {
		if f.SQLType == "TEXT" && f.NotNull {
			return f.Name, nil
		}
	}
	return "", fmt.Errorf("auth: cannot determine email column on entity %q", cfg.Entity.Table)
}

// projectedIdentityColumn reads the field name `identify` picks.
//
// Applying a projection to a record of field-name strings is safe to do at
// boot: `identify : user -> String` is a pure read in every shape the language
// can express, and a projection that does something else simply fails to name
// a field, which the caller treats as "not resolved".
func projectedIdentityColumn(cfg VAuth) (string, bool) {
	if cfg.Identify == nil {
		return "", false
	}
	return projectedField(cfg.Identify, cfg.Entity)
}

// projectedField reads the field name a projection picks, by applying it to a
// record whose every field holds its own name. Shared by `identify` and by the
// role getter: both are `user -> something`, and both are only useful when
// they are a plain field read.
func projectedField(fn Value, entity VEntity) (string, bool) {
	if fn == nil || len(entity.Fields) == 0 {
		return "", false
	}
	named := VRecord{Fields: map[string]Value{}}
	for _, f := range entity.Fields {
		named.Fields[f.Name] = VString{V: f.Name}
		named.Order = append(named.Order, f.Name)
	}
	out, err := Apply(fn, named)
	if err != nil {
		return "", false
	}
	s, ok := out.(VString)
	if !ok {
		return "", false
	}
	if _, isField := named.Fields[s.V]; !isField {
		return "", false
	}
	return s.V, true
}

// ResolveIdentityColumn returns the identity column and whether `identify`
// is what decided it. A false second result means the framework guessed,
// which is the state CheckAuthIdentity refuses at boot.
func ResolveIdentityColumn(cfg VAuth) (string, bool) {
	col, ok := projectedIdentityColumn(cfg)
	if ok {
		return col, true
	}
	col, err := identifyColumn(cfg)
	if err != nil {
		return "", false
	}
	return col, false
}

// authConfigFields is every field `Auth.config` reads, required and optional.
var authConfigFields = map[string]bool{
	"entity": true, "identify": true, "signup": true, "email": true,
	"sessionDuration": true, "role": true, "signInPage": true,
}

func rejectUnknownAuthFields(rec VRecord) error {
	unknown := make([]string, 0, len(rec.Fields))
	for f := range rec.Fields {
		if !authConfigFields[f] {
			unknown = append(unknown, f)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	known := make([]string, 0, len(authConfigFields))
	for k := range authConfigFields {
		known = append(known, "`"+k+"`")
	}
	sort.Strings(known)
	return fmt.Errorf(
		"`Auth.config`: `%s` is not one of its fields, so it is being ignored.\n"+
			"  It reads %s.\n"+
			"  Check the spelling, or remove it",
		unknown[0], strings.Join(known, ", "))
}

// CheckAuthEmailBody exercises the optional `email.body` once, at boot.
//
// It is OPTIONAL, so it cannot be a field of `Auth.config`'s record type: a
// field in a record type is required, and most apps do not write one. So its
// type is unchecked — measured, `body = \\code -> code` (one argument where
// two belong) passes `mar check`.
//
// At request time ApplyEmailBody fails and the framework falls back to the
// default body, deliberately, so a typo does not take auth down. The cost is
// that the app's branded email silently becomes the generic one, forever,
// with nobody told. Applying it here turns that into a refusal the operator
// sees before anyone signs in.
//
// Safe to call: `String -> Int -> String` is pure by contract, and a sample
// code exercises the same path a real one does.
func CheckAuthEmailBody(cfg *VAuth) error {
	if cfg == nil || cfg.EmailBody == nil {
		return nil
	}
	if _, err := ApplyEmailBody(cfg.EmailBody, "000000", 15); err != nil {
		// The error is deliberately NOT passed through. Applying two
		// arguments to a one-argument function raises the runtime's
		// internal-error text — "a checked program cannot do this, so this
		// is a bug in Mar rather than in your code. Please report it" —
		// which is exactly backwards here: the mistake is in the app, and
		// the reason a checked program CAN do this is that the field is
		// optional and so cannot be typed.
		return fmt.Errorf(
			"`Auth.config`'s `email.body` is not a function of a code and a number of minutes.\n" +
				"  It is called with the sign-in code and the minutes it stays valid, and has to " +
				"answer the body as a String. When it does not, the framework sends its own body " +
				"instead — so the app's own email would never go out, and nothing would say so.\n" +
				"  Write it as `\\code minutes -> ...`")
	}
	return nil
}

// CheckAuthRoles reports a service that demands a role the getter can never
// answer.
//
// The two halves are written in different places with nothing tying them
// together, and the type system cannot: `Auth.requireRole` receives no
// reference to the `Auth.config` whose getter it will be compared against.
// The lint cannot either — by the time it runs, the unification that pinned
// the getter's result has been generalized away.
//
// The boot can, with the trick `identify` uses: apply the getter to a record
// whose every field holds its own name, and the field it reads comes back as
// a string. The entity then says what that column holds, and the demanded
// value either fits or it does not.
//
// It matters because the dispatcher compares with equalValues and answers 403
// on a mismatch — which for two different types is EVERY request, for
// everyone, with no error anywhere. A route dead in silence.
func CheckAuthRoles(cfg *VAuth, services []VExposedService) error {
	if cfg == nil || cfg.Role == nil {
		return nil
	}
	column, ok := projectedField(cfg.Role, cfg.Entity)
	if !ok {
		return nil // not a plain field read: nothing certain to compare
	}
	var field *EntityField
	for i := range cfg.Entity.Fields {
		if cfg.Entity.Fields[i].Name == column {
			field = &cfg.Entity.Fields[i]
			break
		}
	}
	if field == nil {
		return nil
	}
	for _, es := range services {
		want := es.Service.RequireRole
		if want == nil {
			continue
		}
		// marValueToSQLForField, not storableAs: a role column is usually an
		// `Entity.enum`, and the enum arm lives above the plain-kind check.
		// Calling the inner one refused `requireRole Admin` on two shipped
		// examples — the floor test caught it before anyone else would have.
		if _, err := marValueToSQLForField(field, want); err != nil {
			return fmt.Errorf(
				"the service on `%s %s` requires a role of a kind `%s` never holds: it asks for %s.\n"+
					"  The two are compared for equality, so they never match: every request to it "+
					"is refused, for everyone, with no error anywhere.\n"+
					"  Ask for a value of the same kind the column holds",
				es.Service.Verb, es.Service.Path, column, describeValueKind(want))
		}
	}
	return nil
}

// CheckAuthIdentity reports an `Auth.config` whose `identify` does not name a
// column, which is decided by the program alone and so belongs at boot.
//
// Without it the framework silently falls back to a guess, and the failure
// surfaces as a visitor who can never sign in — or as a UNIQUE index on a
// column nobody meant to be unique.
func CheckAuthIdentity(cfg *VAuth) error {
	if cfg == nil || cfg.Identify == nil {
		return nil
	}
	if _, ok := projectedIdentityColumn(*cfg); ok {
		return nil
	}
	names := make([]string, 0, len(cfg.Entity.Fields))
	for _, f := range cfg.Entity.Fields {
		names = append(names, "`"+f.Name+"`")
	}
	return fmt.Errorf(
		"`Auth.config`'s `identify` does not name a field of `%s`.\n"+
			"  Sign-in looks a visitor up by one column and the framework cannot tell "+
			"which, so it would guess — and the guess also decides which column is made "+
			"UNIQUE in the database.\n"+
			"  Write it as a plain field read, one of: %s",
		cfg.Entity.Table, strings.Join(names, ", "))
}
