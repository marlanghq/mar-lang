# Backlog

Things known to be missing, half-done, or worth revisiting. Not bugs — bugs go through GitHub issues. This file is for "we'd ship X today, but here's what we'd need before going further."

Add an entry by appending to the relevant section. Move done entries to the bottom of their section as `~~strikethrough~~` instead of deleting, so the history stays grep-able.

---

## Auth — SMTP / email delivery

**Source files**: `internal/auth/mailer.go`, `internal/jsserve/auth.go`, `internal/project/manifest.go`.

The SMTP path works end-to-end via Go's `net/smtp` for vanilla STARTTLS providers (port 587). It is wired through `Auth.config` and configured via the `mail` block in `mar.json`. Caveats below.

### TLS-on-connect (port 465) not exercised

`smtp.SendMail` in the stdlib does opportunistic STARTTLS when the server announces it. Implicit TLS on connect — the model SendGrid/Postmark expose on port 465 — is not wired. May or may not work depending on provider; never tested.

**What we'd need**: a parallel code path that opens a `tls.Dial` then hands the connection to `smtp.NewClient`, gated on a `mail.smtpTLS` config flag.

### No retry / no backoff on transient SMTP errors

A single failed `smtp.SendMail` becomes a 500 on `/_auth/request-code`. Most SMTP providers are reliable enough that this is rare, but a single-region hiccup will surface to the user as "send failed" instead of being absorbed silently.

**What we'd need**: a small retry loop (2–3 attempts, exponential) inside `auth.Send`, or a job queue if we want to decouple email from the request lifecycle entirely. The latter is heavier — adds a queue, worker, and persistence — but stops auth requests from being held by SMTP latency.

### Rate limiting is partial — only `/_auth/request-code` is gated; everything else is open

Current state (`internal/auth/ratelimit.go`, `internal/jsserve/auth.go`):

- `/_auth/request-code`: 3 hits/hour per email + 20 hits/hour per IP (in-memory `Limiter`).
- `/_auth/verify-code`: per-code attempts counter in `_mar_auth_codes.attempts`; 5 strikes locks the code.
- `/_auth/logout`, `/_auth/whoami`: no limit.
- Every user `Service.*` and `Endpoint.*` route: no limit.

The auth limiter design is fine for what it does; the gap is **everything else**. A bot hitting `/api/notes.list` 10k times/sec hurts the DB, not SMTP. We need a uniform layer.

See the dedicated spec below.

---

## Cross-cutting — rate limiting for all HTTP routes

**Problem**: today only `/_auth/request-code` is rate-limited. Every other framework route (`/_auth/logout`, `/_auth/whoami`) and **every** user route (Service.call dispatch, Endpoint handlers) accepts unlimited traffic. This invites:

- DB exhaustion (each Service.call hits SQLite; modernc/sqlite serializes writes, so a flood blocks legit traffic).
- CPU burn (auth `bcrypt`-style hash on `/_auth/verify-code`; mar lambda evaluation per Service call; JSON serialisation).
- Goroutine pile-up (one per request; Go scales but isn't free).
- Outbound quota burn for any user code that triggers external IO (Http.post to a third-party API from inside a Service handler).

A determined attacker doesn't need cleverness — `xargs -P 200 curl ...` against any endpoint is enough.

### Goals

1. **Default-on protection**. A fresh `mar dev` / `mar build` deploys with sane limits without the user opting in. Users who hit the limit accidentally during dev see a clear error, not a silent drop.
2. **Per-route tunability**. `/_auth/request-code` deserves an aggressive limit (already 3/h/email); a read-mostly `notes.list` Service.call can take 10× more before anyone notices.
3. **Multiple dimensions**. Per-IP catches anonymous floods. Per-authenticated-user catches credential-abuse flooding from one logged-in account. Global per-route catches both at once.
4. **Cheap teardown**. Rejected requests don't run handler code, don't touch DB, don't allocate beyond the limiter check.
5. **Surfaces in HTTP correctly**. 429 Too Many Requests + `Retry-After` header (seconds) + JSON body `{"error":"rate_limited","retryAfter":N}`. Frontend gets a typed `Result.Err "rate_limited"` from `Service.call`, can show a polite message.
6. **Single-process now, distributed later**. Today: in-memory. Don't paint into a corner — the limiter interface should accept a different backend (Redis-style counter) without callers caring.

### Non-goals (for v1)

- **Distributed rate limiting**. Multi-instance deployments need a shared store (Redis, libsql, etc.). Out of scope; we'll do per-process buckets and document the gotcha.
- **Adaptive / ML-driven limits**. Just thresholds.
- **Per-user-tier overrides** ("Pro accounts get 10× quota"). User can build that on top later by setting limits per-route from mar.json.
- **WAF-grade defenses** (slowloris, Slowpost, etc.). The Go HTTP server has its own timeouts; we lean on those.

### Design

#### 1. Algorithm: **token bucket**, not sliding window.

The current `auth.Limiter` is sliding-window — exact, but `O(hits-in-window)` memory per key. Token bucket gives the same fairness with `O(1)` per key (just a float for the current bucket level + a timestamp for last refill). At 100k unique IPs per hour with the existing limiter, we'd hold ~2M `time.Time` values; with token bucket, ~1.6MB total.

```go
type bucket struct {
    tokens   float64
    last     time.Time
}

// Allow: refill tokens by elapsed * rate, cap at burst, deduct 1.
// Returns (allowed, retryAfter).
```

`rate` is tokens/second; `burst` is the max bucket size. A "3 per hour" limit is `rate=3/3600, burst=3` — the user can spend all 3 instantly, then has to wait 20 minutes for one to refill.

Keep `auth.Limiter` (sliding window) as-is for `request-code` because the policy is "exactly N per hour, no burst" which is what we already promise users. The **new** general-purpose limiter is token-bucket; the auth one stays its own type.

#### 2. Public type: `ratelimit.Limiter` in a new package `internal/ratelimit`.

```go
package ratelimit

type Policy struct {
    Rate  float64       // tokens per second
    Burst int           // max bucket size
}

type Limiter struct { /* unexported */ }

// New returns an in-memory token-bucket limiter for `policy`.
// Call Allow(key) once per request; key is whatever the caller
// chose to bucket on (IP, user ID, "route:notes.list:ip:1.2.3.4").
func New(policy Policy) *Limiter

// Allow returns (true, 0) if the request fits, (false, retryAfter) if not.
func (l *Limiter) Allow(key string) (bool, time.Duration)

// Stop releases the eviction goroutine. Required for tests; in
// production the limiter lives for process lifetime.
func (l *Limiter) Stop()
```

Internals:
- `map[string]*bucket` guarded by `sync.RWMutex`. Per-key sub-mutex if contention shows up — start simple.
- Eviction goroutine: every `5 * burst / rate` (i.e. five-bucket-fill-times), drop entries with `tokens >= burst` and `last > 1h ago`. Without this the map grows forever.
- Eviction timer is the same shape as the auth sweeper — `context.Context` + `done` channel, `Stop` is synchronous.

#### 3. HTTP middleware in `internal/jsserve/middleware.go`.

```go
type RouteRule struct {
    PerIP   *Policy   // nil = no per-IP limit
    PerUser *Policy   // nil = no per-user limit (also nil-no-op for unauth requests)
    Global  *Policy   // nil = no global per-route limit
}

func RateLimitMiddleware(rule RouteRule) func(http.Handler) http.Handler
```

The middleware:
1. Reads the IP via `clientIP(r)` (already exists).
2. Reads the user ID from the session cookie if present (cheap — same lookup `handleWhoami` does).
3. For each non-nil policy, checks the corresponding limiter with a route-prefixed key (`"ip:1.2.3.4"`, `"user:42"`, `"global:notes.list"`).
4. On reject: writes 429 + `Retry-After` + JSON body, returns. Doesn't call next.
5. On accept: calls next.

The three checks happen in order: per-IP first (cheapest, no DB), then per-user (one cookie parse), then global. Short-circuits on first reject so a flood from one IP doesn't waste cycles checking the per-user bucket.

#### 4. Defaults — `internal/jsserve/server.go`.

```go
var (
    defaultRouteRule = RouteRule{
        PerIP:  &Policy{Rate: 10, Burst: 30},  // 10/sec sustained, 30 burst
        Global: &Policy{Rate: 200, Burst: 500}, // app-wide cap per route
    }
    authRequestCodeRule = RouteRule{
        PerIP: &Policy{Rate: 0.005, Burst: 5}, // ~18/hour, burst 5
        // per-email check stays in handler (it's input-derived, not header-derived)
    }
)
```

`mountAuthHandlers` wraps `/_auth/whoami` and `/_auth/logout` in `RateLimitMiddleware(defaultRouteRule)`. `/_auth/request-code` gets `authRequestCodeRule` AND keeps the existing `emailLimiter` inside the handler (we can't get the email until we read the body). `/_auth/verify-code` gets `defaultRouteRule` plus the per-code attempts counter that's already there.

User Service routes (mounted at `/api/<module>.<service>`) get `defaultRouteRule` automatically by wrapping the dispatcher with the middleware once.

#### 5. Configuration via `mar.json`.

```json
{
  "rateLimits": {
    "default":  { "perIP": { "rate": 10, "burst": 30 } },
    "perRoute": {
      "notes.list":  { "perIP": { "rate": 30, "burst": 50 } },
      "notes.create":{ "perIP": { "rate": 1,  "burst": 3  } },
      "auth.requestCode": { "perIP": { "rate": 0.005, "burst": 5 } }
    }
  }
}
```

`server.go` reads this on boot; missing `perRoute` entries fall back to `default`; missing `default` falls back to the hard-coded one above.

`internal/project/manifest.go` validates: `rate > 0`, `burst >= 1`, route names match an existing endpoint (compile error if not — same channel as unknown field).

#### 6. Error surface to mar code.

Frontend `Service.call` already returns `Result String b`. A 429 maps to:

```mar
Err "rate_limited"
```

The framework renderer recognizes that string and could add a UX nudge ("you're going too fast — try again in N seconds"), but that's optional polish. The minimum is: user code can pattern-match `Err "rate_limited"` and render whatever it wants. The `retryAfter` value goes through too — extend the Service error to a small record `{ code: String, retryAfter: Int }` if needed (breaking change for one Service builtin signature; manageable).

### Phasing

1. **Phase 1**: ship `ratelimit.Limiter` + middleware + hard-coded defaults wrapped around `/api/*` and `/_auth/*`. No mar.json knob yet. Tests cover: token refill, burst, eviction, two limiters racing on the same key.
2. **Phase 2**: add `mar.json` config + manifest validation + per-route override. Tests cover: missing default, malformed policy, unknown route name.
3. **Phase 3**: pluggable backend interface (`type Backend interface { Take(key string, n int) (allowed bool, retryAfter time.Duration) }`), with the in-memory implementation + a stub Redis one for distributed deployments. Document the dev/prod difference.

### What this doesn't solve (and we accept)

- **Distributed deployments** until phase 3 lands. Until then, behind a load balancer with N replicas, the effective limit is N×configured (each replica counts independently).
- **Header spoofing**. `clientIP` reads `X-Forwarded-For`; behind a hostile proxy this is forgeable. Document that the limiter trusts the deployment's proxy chain to be honest, and that unknown chains should set the header to the actual peer IP.
- **Bot-vs-human distinction**. We can't tell. A user with bad latency hitting refresh hard looks the same as a script. The 30-burst default exists exactly so the legit-but-jumpy case has slack.

### Plain-text body only, with no template story

`auth.DefaultBody` produces a fixed string. `Auth.config { email = { body = \code ttl -> ... } }` lets the user replace the entire body, but there's no template engine, no localization hook, no support for non-text content types.

**What we'd need**: a way for user code to localize the subject + body by `Accept-Language` (or by a user.locale field), plus a heading/footer split so the framework's "this is transactional" framing stays consistent without forcing the user to re-implement it.

### `From` validation is permissive

The `mail.from` field is passed straight through to the SMTP envelope. We don't verify it matches a domain authorized by the SMTP credentials, so a misconfigured deploy will silently bounce or land in spam without the user knowing why.

**What we'd need**: at boot, parse the `From` address and warn loudly if its domain doesn't match anything obvious (e.g. SendGrid API key vs. unrelated From domain). Soft warning at first — bouncing in front of a deploy because of a heuristic is too aggressive.

### Test coverage gap

Tests run with `SMTPConfig{}` (stdout sink), which validates the dev path but never exercises real SMTP wire format, auth, or error mapping. A regression in `buildWireMessage` or in how `smtp.SendMail` errors propagate would not be caught.

**What we'd need**: a test that uses an in-process SMTP server (e.g. `mhog`-style or a tiny custom listener) and asserts the wire payload + From/To/Subject. Not hard, just hasn't been written.

---

## Auth — session / code expiration

**Source files**: `internal/auth/schema.go`, `internal/auth/sweeper.go`, `internal/jsserve/auth.go`.

### ~~`SweepExpired` exists but nothing schedules it~~ — DONE

`auth.StartSweeper(ctx, db, interval)` now runs in a background goroutine started by `mountAuthHandlers` (24h cadence). Initial sweep fires before the first tick so processes restarted after long downtime reclaim space immediately. Tests cover the initial sweep, periodic ticks, and synchronous teardown.

---
