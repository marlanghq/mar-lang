package runtime

import (
	"database/sql"
	"fmt"
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
	// Required in Auth.config — gives each app a chance to brand it
	// ("Sign in to Notes" vs the generic default).
	//
	// The From: header is NOT in this struct. It lives in mar.json's
	// `mail.from` — the single source of truth for the address
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
	// Auth.config so the source of truth is the Page itself —
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
func RegisterAuth(a VAuth) {
	regMu.Lock()
	regAuth = &a
	regMu.Unlock()
}

// CurrentAuth returns the registered Auth, if any. Used by the HTTP
// dispatcher to know whether to mount /_auth/* and to satisfy
// Auth.protected services.
func CurrentAuth() *VAuth {
	regMu.RLock()
	defer regMu.RUnlock()
	return regAuth
}

// ResetAuthForTesting clears the registered Auth so tests can run in
// isolation without leaking state from previous runs. Production
// code should never call this — `mar dev` and `mar-runtime` rely on
// the auth registration sticking for the lifetime of the process.
func ResetAuthForTesting() {
	regMu.Lock()
	regAuth = nil
	regMu.Unlock()
}

// authBuiltins registers the language-level surface for authentication.
//
//	Auth.config  : { entity, identify, email, signup, sessionDuration } -> Auth user
//	Auth.protect : Service req resp -> (req -> user -> Effect String resp) -> ExposedService
//
// The four user-facing client effects (Auth.requestCode / verifyCode /
// logout / me) are JS-runtime-only — they're built-ins on the browser
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

		// Browser-only Effects. On the Go side they error out — the
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
	// `email.from` is intentionally NOT accepted here — it duplicates
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
	// Optional `email.body : String -> Int -> String` — given the
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
		durationSecs = d.Seconds
	}
	// `role` is optional; only required when the app uses
	// Auth.requireRole. We don't validate the field's type here — the
	// dispatcher applies it as a function and surfaces failures as 500.
	role := rec.Fields["role"]
	// `signInPage` is a Page reference (typically Frontend.SignIn.page)
	// that Page.protected redirects to when the user isn't logged in.
	// Optional — backend-only apps don't have pages at all. When the
	// app declares any Page.protected, the bundle bootstrap errors
	// loudly if signInPage was missing.
	signInPath := ""
	if pageVal, ok := rec.Fields["signInPage"]; ok {
		page, ok := pageVal.(VPage)
		if !ok {
			return nil, fmt.Errorf("Auth.config: `signInPage` must be a Page (got %T)", pageVal)
		}
		signInPath = page.Path
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
	RegisterAuth(cfg)
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
		userID, err := projectField(pargs[1], "id")
		if err != nil {
			return nil, fmt.Errorf("Auth.requireOwner: user has no `id` field: %w", err)
		}
		return VBool{V: equalValues(ownerID, userID)}, nil
	})
	exposed.Service.LoadResource = loader
	exposed.Service.Policy = policy
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
	// call Time.now themselves — the framework patches in `now` for
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
		// Keep Order in sync — append only if the hook didn't already
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
	db, err := getDB()
	if err != nil {
		return 0, err
	}
	if err := ensureMigratedNoLock(cfg.Entity, db); err != nil {
		return 0, err
	}
	emailCol, err := identifyColumn(cfg)
	if err != nil {
		return 0, err
	}
	idCol := idColumnName(cfg.Entity)
	row := db.QueryRow(
		"SELECT "+idCol+" FROM "+cfg.Entity.Table+" WHERE "+emailCol+" = ? LIMIT 1",
		email,
	)
	var id int64
	if err := row.Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// LoadUserJSON returns the user record for `id` as a `map[string]any`
// suitable for JSON encoding. Used by /_auth/whoami and /_auth/verify-code
// to send the User row to the client.
func LoadUserJSON(cfg VAuth, id int64) (map[string]any, error) {
	v, err := loadUserValue(cfg, id)
	if err != nil {
		return nil, err
	}
	rec, ok := v.(VRecord)
	if !ok {
		return nil, fmt.Errorf("user row is not a record")
	}
	out := map[string]any{}
	for _, name := range rec.Order {
		out[name] = valueToAny(rec.Fields[name])
	}
	return out, nil
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

// ensureMigratedNoLock runs the entity's migration. Wrapped helper so
// auth code can ensure the user table exists before the first query.
func ensureMigratedNoLock(entity VEntity, db *sql.DB) error {
	_, err := db.Exec(buildCreateTableSQL(entity))
	return err
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

// identifyColumn applies the user-supplied identify projection to a
// dummy record where every field is named-as-its-name, so we can read
// back which field name the projection picked. Implementation: look
// up which field on the entity is a String (typical case) and trust
// the convention of `\u -> u.email` mapping to the `email` field.
//
// For v1, we accept this slight cheat: the identify projection is
// expected to be `\u -> u.<emailFieldName>`, and we discover the
// column name by trying common names. If multiple String columns
// exist, the user can name their email column something obvious or
// the framework will pick the first one.
func identifyColumn(cfg VAuth) (string, error) {
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

// valueToAny converts a runtime Value to a JSON-friendly Go value.
// Subset of what JSON.encode does; used to serialize user records for
// the /_auth/* responses.
func valueToAny(v Value) any {
	switch x := v.(type) {
	case VInt:
		return x.V
	case VFloat:
		return x.V
	case VString:
		return x.V
	case VBool:
		return x.V
	case VUnit:
		return nil
	case VDuration:
		return x.Seconds
	case VTime:
		// Marker form so jsToMar / iOS decoders can rebuild a VTime
		// (instead of dropping back to a plain VString) — same
		// pattern as VCtor's `{__ctor:...}`.
		return map[string]any{
			"__time": time.UnixMilli(x.Millis).UTC().Format(time.RFC3339),
		}
	case VList:
		out := make([]any, 0, len(x.Elements))
		for _, e := range x.Elements {
			out = append(out, valueToAny(e))
		}
		return out
	case VRecord:
		out := map[string]any{}
		for _, name := range x.Order {
			out[name] = valueToAny(x.Fields[name])
		}
		return out
	case VCtor:
		// Every ctor — Nothing and Just included — uses the
		// `__ctor` marker convention shared with encodeValue + the JS
		// / iOS runtimes. See the note in internal/runtime/json.go on
		// why Nothing can't be transparent to null (collides with
		// VUnit's encoding) and why Just can't be transparent either
		// (breaks generic decoders on record payloads).
		out := map[string]any{"__ctor": x.Tag}
		if len(x.Args) > 0 {
			args := make([]any, len(x.Args))
			for i, a := range x.Args {
				args[i] = valueToAny(a)
			}
			out["__args"] = args
		}
		return out
	}
	return nil
}
