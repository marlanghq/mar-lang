# Admin Panel

A specification for a built-in admin panel that ships with the `mar` framework — written in Mar, served at `/_mar/admin`, but **invisible to the user's project code**. Updates to the panel arrive automatically with `mar` upgrades. Every Mar project gets it for free.

This document is a design proposal for the **MVP**, not an API reference. It establishes how the panel is hosted, authenticated, kept in sync with the project's deployment, and what features it ships with in v1.

> **Status.** Specification only. None of the `_mar_admin_*` symbols, the `admins` field in `mar.json`, the `mar admin` CLI, or the embedded `.mar` panel sources exist yet. Everything they rely on (the Mar interpreter, the `UI` vocabulary, schema introspection, request logging, the `_auth/*` HTTP handlers, the `mar.json` loader) is already in the framework today.

## 1. Goals & non-goals

### Goals

- **Zero opt-in.** Every Mar project has the panel. No scaffolding step, no `mar admin init`, no entry in `App.fullstack`. It just exists at `/_mar/admin`.
- **Invisible to user code.** Nothing in `Frontend/`, `Backend/`, or `Shared/` mentions admin. The panel's Mar source files live inside the framework binary. The user's only contact surface is `mar.json` (an `admins: [...]` list) and `mar admin add/remove/list` CLI.
- **Updates with `mar`.** Upgrade the framework → next deploy ships the new panel. No code-migration step in the user's project, ever.
- **Same UI vocabulary.** The panel is built from `navigationStack` / `list` / `section` / `text` / `button` — same primitives user pages use, same web renderer, no parallel framework for the admin surface.
- **Admin auth is fully separate.** The panel doesn't know about the project's `User`, `Auth.config`, or `role` field. Logging into the app and logging into the admin panel are different flows with different sessions.
- **Production-safe by default.** No "first visitor claims it" bootstrap. No env-var hack. Admins are declared in `mar.json`, version-controlled, deploy-driven.

### Non-goals (for v1)

- **Generic CRUD.** Browse rows, yes; edit them, no. Edit forms in v2 once read paths are stable.
- **Action runner.** Lispy's "execute any registered Action" panel — punted to v2; needs an explicit allowlist mechanism we don't want to fake.
- **Charts / time-series.** Counts and tables only.
- **Multi-tenant scoping.** Admin sees the whole DB.
- **Audit log of admin actions.** v2. v1 admin reads, so there's nothing to audit yet.

## 2. Mental model

```
┌────────────────────────────────────────────────────────────┐
│  user project                                              │
│    Main.mar / Backend/* / Frontend/* / Shared/*            │  ← only business code
│    mar.json                                                │  ← + admins: ["..."]
└──────────────────────┬─────────────────────────────────────┘
                       │  App.fullstack  (no admin opt-in)
┌──────────────────────▼─────────────────────────────────────┐
│  mar runtime (framework)                                   │
│                                                            │
│   on boot                                                  │
│     1. ensure _mar_admin_* tables exist                    │
│     2. sync _mar_admins ↔ mar.json["admins"]               │
│     3. load embedded panel sources (embed.FS)              │
│     4. interpret + register them as routes under /_mar/admin │
│                                                            │
│   at request time                                          │
│     /_mar/admin/auth/*             → admin-only auth flow  │
│     /_mar/admin/api/*              → embedded services     │
│     /_mar/admin/*                  → embedded page         │
│                                                            │
│   reuses                                                   │
│     - same mar.db (_mar_* prefix)                          │
│     - same SMTP from mar.json["mail"]                      │
│     - same Mar interpreter                                 │
└────────────────────────────────────────────────────────────┘
```

The boundary that matters: **the user's project does not import or reference any admin symbol**. The framework binary contains everything.

### 2.1 Why embed Mar source instead of compiling it in

The runtime is already an interpreter — it parses + typechecks + interprets `.mar` files as part of the normal `mar dev` / `mar build` flow. Embedding the panel's `.mar` source via `embed.FS` and interpreting them at framework boot is:

- **Zero extra build machinery.** No new artifact format, no codegen step in the framework's own build.
- **Editable in the framework repo like any other Mar code.** A change to `internal/admin/mar/Page.mar` ships with the next `mar` release; framework devs edit it the same way they edit example projects.
- **Already-tested code path.** Same parser, same typechecker, same interpreter the user code goes through. Zero parallel implementation.

Cost: the panel adds ~tens of milliseconds to framework boot time (parse + typecheck of ~6 small files). Acceptable.

### 2.2 Why the panel is not a user-side scaffold

The earlier draft of this spec proposed `mar admin init` writing files into the user's project. That was wrong:

- **Updates to the panel would have required user-side migration.** Each `mar` release would risk drifting from older copies still living in user projects.
- **It mixed user code and framework code in the same namespace.** Confusing — every user project would have a `Frontend/Admin.mar` they didn't really write.
- **It made the panel feel like a feature with optional installation,** instead of a baseline capability of the runtime.

Framework-owned and embedded fixes all three.

## 3. Authentication: separate from user auth

The admin panel has its **own** passwordless email-code flow. It does not reuse `Auth.config`. It does not require the project's `User` entity to have a `role` field. The two systems share only the SMTP transport.

### 3.1 Why fully separate

Three reasons it's worth the small redundancy:

1. **The admin user might not be an app user.** A developer running ops on a SaaS doesn't necessarily have a customer account, and forcing them to create one to "promote to admin" couples auth to admin in a way that complicates real usage.
2. **The role-on-user model leaks framework concerns into business code.** The user shouldn't have to model "Admin" as part of their domain just because the framework needs it.
3. **Independent rotation.** Revoking admin access shouldn't touch the user's app session and vice versa. Different cookie names (`mar_admin_session` vs `mar_session`), different DB tables, different lifetimes.

### 3.2 Tables (framework-managed)

Created automatically on first boot. Live in the same `mar.db` as the user's data, prefixed with `_mar_admin_*` so they're easy to spot and never collide with user entity names:

```
_mar_admins                  -- email + createdAt + (optional) lastLoginAt
_mar_admin_codes             -- ephemeral 6-digit codes (hashed), TTL minutes
_mar_admin_sessions          -- session token (hashed) + email + expiresAt
```

The user never queries these — they're internal. Migrations for these tables are part of the framework, not the user's migration history.

**Two parallel migration histories.** The `mar.db` ends up with two migration tables:

```
_mar_framework_migrations    -- managed by the framework binary itself
                                (add columns to _mar_admin_*, etc as mar evolves)
mar_migrations               -- managed by the user's project
                                (the existing user-side migration system)
```

Framework migrations apply on every boot, before user migrations and before the `_mar_admins` sync. They're embedded in the `mar` binary alongside the panel's `.mar` sources, so upgrading `mar` ships any required schema evolution automatically — same property as the panel itself. Naming is `_mar_framework_*` to fit the reserved prefix and avoid collision with anything user-side.

### 3.2a Cookie signing — reuses the framework session secret

Admin session cookies are HMAC-signed with the **same `auth.sessionSecret`** the user-auth uses. No separate admin secret. Different cookie names (`mar_session` vs `mar_admin_session`) and different DB tables ensure the two session systems are isolated at the storage layer; the shared signing key just means there's one secret to manage, rotate, and protect — same security model.

Implications:

- **`auth.sessionSecret` is the framework's session secret, not "the user-auth secret".** Whether the project uses `Auth.config`, the admin panel, or both, the same key signs all session cookies the framework issues.
- **Required when either feature is in use.** The build-time production check (which today fires when `Auth.config` is registered) extends to also fire when `admins` is non-empty: `auth.sessionSecret = "env:VAR"` must be present in `mar.json` for production builds.
- **Dev fallback unchanged.** `ResolveSessionSecret` already auto-generates and persists to `.mar/dev-secrets.json` when no explicit secret is configured — that path works for admin sessions too with no extra wiring.
- **Rotation is shared.** Rotating `auth.sessionSecret` invalidates all sessions for both user-auth and admin-auth simultaneously. This is the right default — if you're rotating, you're either reacting to a leak (revoke everything) or planning a controlled rotation (schedule the cutover for both).

**Why not a separate secret?** Two extra concerns for marginal gain: another env var to set in production, another piece of state to forget to rotate. A leak of either secret is a "rotate the framework session key" event anyway; coupling them is honest about that.

### 3.3 Login flow

Identical idiom to the app's email-code flow, just on isolated endpoints:

```
1. GET  /_mar/admin                       → page renders, asks for email
2. POST /_mar/admin/auth/request-code     → if email ∈ _mar_admins, generates code
                                              + INSERT _mar_admin_codes
                                              + sends email via shared SMTP (or terminal in dev)
3. POST /_mar/admin/auth/verify-code      → constant-time compare,
                                              creates _mar_admin_sessions row,
                                              Set-Cookie: mar_admin_session=...
4. subsequent requests → middleware reads opaque session ID from cookie,
                          looks up _mar_admin_sessions row by ID,
                          gates /_mar/admin/api/* on row presence + non-expired
5. POST /_mar/admin/auth/logout           → DELETE _mar_admin_sessions row, clear cookie
```

A few intentional details:

- **No email enumeration.** If the email isn't in `_mar_admins`, request-code returns the same 200 OK as the success path. The attacker can't probe who's admin by error message.
- **Rate-limited request-code.** Same per-IP limiter the app's `/_auth/request-code` uses (already implemented).
- **Sessions short by default.** 12 hours, vs the app's 30 days. Admins typically open the panel, do what they need, and close it — there's no value in a long-lived session, and the security cost of a stolen cookie is much higher than for a normal user. 12h covers a single working day without forcing re-auth in the middle of an incident; anything longer leaks risk.
- **DB-per-request session validation, not cookie-only.** The cookie carries an opaque session ID; the row lives in `_mar_admin_sessions`. Every authenticated request looks up that row before the handler runs. This means deleting the row (sync, logout, manual `DELETE`) revokes the session **immediately, at the next request**, without any cache invalidation or cross-machine signaling. It's the property that makes admin removal in §4.1a actually immediate.

### 3.4 No "promote app user to admin" UI

By construction, there is no link between an app user and an admin. Promoting `me@example.com` to admin is editing `mar.json`'s `admins` list. The user doesn't need to exist in `users` first; the admin doesn't need to be a customer.

If you want the same person in both worlds, they'll sign in twice — once on the app, once on the panel. That's fine.

## 4. Configuration: the `admins` field in `mar.json`

The single place where a project declares its admins:

```json
{
  "name": "my-app",
  "admins": [
    "me@example.com",
    "ops@example.com"
  ],
  "auth": { "sessionSecret": "env:SESSION" },
  "mail": {
    "from": "noreply@example.com",
    "smtpHost": "smtp.resend.com",
    "smtpUsername": "resend",
    "smtpPassword": "env:RESEND_API_KEY"
  }
}
```

Properties of this design:

- **Version-controlled.** Admins are part of the project's git history, reviewable in PRs.
- **Declarative.** The list is the source of truth; the DB table mirrors it.
- **Deploy-driven.** Adding/removing an admin requires a deploy. This is a feature, not a bug — admin changes get the same review + rollback machinery as any other change.
- **No env-var sprawl.** Production secrets stay in `env:VAR` references; admins are not secrets and live as plain JSON.

### 4.1 Boot-time sync

When the runtime starts:

```
mar.json.admins         -- declarative list (e.g. ["a@x.com", "b@x.com"])
_mar_admins             -- DB table (current state)

reconciliation (per email removed from mar.json.admins):
  - DELETE FROM _mar_admins        WHERE email = ?
  - DELETE FROM _mar_admin_codes   WHERE email = ?    -- pending codes invalidated
  - DELETE FROM _mar_admin_sessions WHERE email = ?   -- active sessions revoked

(per email added):
  - INSERT INTO _mar_admins (email, createdAt) VALUES (?, NOW)

(unchanged emails: no-op.)
```

Idempotent and cheap. Runs on every boot, not just deploys.

### 4.1a Revocation is immediate

Removing an admin from `mar.json` and deploying revokes that admin's access **at the next request after the sync runs** — which on a single-machine Fly app means "as soon as the new machine is up." There's no cookie-trust window, no in-memory cache to invalidate, no cross-machine coordination problem.

The reason is §3.3's "DB-per-request session validation": the cookie is just a pointer; the source of truth is the row in `_mar_admin_sessions`. The boot-time sync deletes that row. Any subsequent request from that admin's cookie hits the middleware, finds no row, returns 401.

**On single-machine Fly (typical):**

```
t=0    edit mar.json (remove ops@example.com), commit
t=1    mar fly deploy
t=2    fly stops old machine                    ← server is down, no requests served
t=3    fly boots new machine, runs boot-sync:
         DELETE FROM _mar_admin_codes    WHERE email = 'ops@example.com'
         DELETE FROM _mar_admin_sessions WHERE email = 'ops@example.com'
         DELETE FROM _mar_admins         WHERE email = 'ops@example.com'
t=4    new machine accepts requests; ops's old cookie → 401
```

Window during which `ops@example.com` retains access: **zero**. There's a downtime window (`t=2` → `t=4`) where neither admin nor user can reach anything, but that's a deploy property, not a security property.

**On multi-machine deploys** (rolling update, multiple replicas of the same app on a shared volume):

```
t=0    edit mar.json, commit, mar fly deploy
t=1    fly boots new machine #2 alongside old machine #1
t=2    new machine #2 runs sync → DELETE FROM _mar_admin_sessions WHERE email = 'ops@example.com'
       (machine #1 still up, still serving requests)
t=3    ops's request lands on machine #1 → middleware queries _mar_admin_sessions
       → row was deleted by #2's sync → 401
t=4    fly stops machine #1
```

Window during which `ops@example.com` retains access: **bounded by the time between the first sync (`t=2`) and the next request from that admin**. In practice ~zero, because sync happens in milliseconds and any subsequent admin request hits the now-shared post-sync DB state. Both machines see the same `_mar_admin_sessions` because the volume is shared.

**Edge case: in-flight request at the moment of sync.** A request that started before the DELETE and is mid-handler when the DELETE runs continues to completion (Go's `http.Handler` doesn't abort on DB row changes). The next request from that admin hits 401. The window is one request, typically milliseconds.

Conclusion: in practice, removing an admin via `mar fly deploy` revokes their access immediately for any meaningful definition of "immediately." The previous draft of this section described an "eventual consistency" window that doesn't actually exist once session validation is DB-per-request rather than cookie-trust.

Dev (`mar dev`) is identical — file watcher triggers the same sync, same DELETE, same instant revocation on the next request.

### 4.2 What if `admins` is missing or empty?

If the field is absent or `[]`:

- Boot proceeds normally; `_mar_admin_*` tables still get created.
- `/_mar/admin` still serves the login page.
- Any login attempt fails (no email is in the empty `_mar_admins`), returns the same generic "code sent if recognized" response.
- Net effect: panel exists but is inaccessible. Safe default for projects that haven't set up admins yet.

But "safe default + silent" is a footgun: developers who don't know the panel exists never benefit from it. We emit a discovery warning so the feature surfaces itself:

**On `mar dev` startup** (once per session, alongside the other startup hints):

```
hint: no admins configured — the admin panel at /_mar/admin is locked.
      run `mar admin add YOUR_EMAIL` to enable it.
```

**On `mar build` for a production target** (alongside the existing auth/mail check):

```
warn: building for production with no admins configured.
      the admin panel at /_mar/admin will be inaccessible.
      run `mar admin add YOUR_EMAIL` if you want admin access in production.
      (this is a warning, not an error — pass --no-admin-warning to silence it)
```

**On runtime boot** (production mode, once at startup, in stderr):

```
mar: admin panel locked (no admins in mar.json) — /_mar/admin will reject all logins.
```

Three layers because each catches a different audience: `mar dev` for the developer first encountering the framework, `mar build` for someone shipping to prod for the first time, runtime boot for someone inheriting an existing project. None of them block — admin is opt-in by design — but all three make the feature impossible to miss.

### 4.3 Production validation

The build-time production-config check (which today exists for `Auth.config` use → `auth.sessionSecret` + `mail` block required) gets one extension: **a non-empty `admins` list also triggers the `auth.sessionSecret` requirement**, because admin session cookies use the same HMAC key (see §3.2a). It does *not* trigger the `mail` requirement on its own — admin codes can fall back to terminal output in dev, and a project that ships to prod with admins but no SMTP will get a runtime warning rather than a build-time error (since admin login simply won't work, but the rest of the app can).

What the check does NOT enforce:

- **Non-empty `admins` itself.** Admin is opt-in — many projects (single dev, internal tools, CLI-only apps) won't bother. Empty list = panel exists but is inaccessible (see §4.2 for the discovery warnings).
- **`mail` when only `admins` is set.** Belongs in a runtime check, not build (so the project can still ship and operate fine without admin login working).

## 5. CLI: `mar admin`

Three subcommands, all operating on `mar.json` from the project root:

### `mar admin add EMAIL`

```
$ mar admin add me@example.com
mar admin add: me@example.com added to admins

  → mar.json updated
  → next deploy will sync this to _mar_admins on production

In development, the admin panel auth code prints to the terminal (no SMTP needed).
In production, codes are sent via the SMTP configured in mar.json["mail"].

The dev panel URL is http://localhost:3000/_mar/admin (or whatever port `mar dev` printed).
```

Mechanics:
- Reads `mar.json`, parses, **appends** the email to the `admins` array (deduplicated against existing entries, preserves the existing order — same surgical edit pattern `mar fly init` uses for `deploy/fly/fly.toml`). The CLI doesn't sort the list because the user may have intentional ordering; we just don't add duplicates.
- Validates email shape (basic `<local>@<domain>` regex; no DNS lookup).
- If the project's running `mar dev` is up, the file watcher picks up the change and the dev server's `_mar_admins` is re-synced live.

### `mar admin remove EMAIL`

```
$ mar admin remove ops@example.com
mar admin remove: ops@example.com removed from admins

  → mar.json updated
  → next deploy will sync this to _mar_admins on production
  → existing admin sessions for ops@example.com will be revoked at next boot
```

Mirrors `add`. If the email isn't in the list, prints a friendly "not in admins list" without error.

### `mar admin list`

```
$ mar admin list
admins (from mar.json):
  me@example.com
  ops@example.com
```

Just reads `mar.json` and prints the list. Source-of-truth in, source-of-truth out — no DB query, no last-login enrichment, nothing that could disagree with what's committed.

For production state (who actually logged in, when), use `mar fly admin list` — see §6.1.

### What `mar admin` does NOT do

- **No `mar admin sync`.** Sync is automatic on every boot. There's nothing to "manually trigger."
- **No `mar admin login`.** Logging in is browser-only, by design.
- **No `mar admin invite`.** The CLI doesn't email anyone. `add` only authorizes; the email is sent by the runtime when the user clicks "send code" on the panel.

## 6. Operating in production (Fly.io and others)

The flow for the typical case:

```
$ mar admin add me@example.com           # edits mar.json locally
$ git add mar.json && git commit -m "admin: add me"
$ mar fly deploy                         # ships new binary; on boot, _mar_admins is synced
$ open https://my-app.fly.dev/_mar/admin
  → enter email, get code via SMTP, sign in
```

Same flow for any host. The panel is accessible at `/_mar/admin` on whatever domain the app is served from.

### 6.1 `mar fly admin list` — production runtime inspection

There is exactly one way to add or remove an admin: edit `mar.json` and deploy. No SSH escape hatch, no runtime mutation, no out-of-band override. The only production-side command is read-only inspection.

`mar fly admin list` is a thin wrapper around `fly ssh console -C "mar-runtime admin list"` so the user never has to remember the SSH incantation. Same DX as `mar fly logs` / `mar fly status`.

```
$ mar fly admin list
admins (from mar.json on disk in prod):
  me@example.com
  ops@example.com
admins (from runtime _mar_admins, post-sync):
  me@example.com
  ops@example.com
last login:
  me@example.com   17 minutes ago
  ops@example.com  never
```

Shows both sides of the sync (config + DB) plus last-login data — exactly the bits that don't exist locally and that you actually need when debugging "wait, why can't this person log in?". Read-only.

> **About `mar-runtime`.** The production binary that runs inside the deployed container (compiled by `mar build`, packaged by `mar fly deploy`) — same binary that serves HTTP for the app. The proposal here is that it also exposes a single `admin list` CLI subcommand operating on the local `mar.db`, used by `mar fly admin list` over SSH. The local `mar` CLI never opens the production DB; only `mar-runtime` does, from inside the container.

### 6.2 Why no runtime add/remove

Considered briefly and rejected: the whole design rests on **`mar.json` is the source of truth, deploy applies it**. Adding a second path that mutates production state out-of-band creates two ways to do the same thing, with the runtime path always being the lossy/ephemeral one. Rather than ship that footgun and rely on warning messages to contain it, we keep the surface to one operation: edit `mar.json` + deploy. New admin = new deploy. If that's slow, the answer is faster deploys, not a side channel.

### 6.3 SMTP and the dev/prod split

Reusing the app's SMTP from `mar.json["mail"]` means:

- **Dev**: no SMTP configured → admin panel codes print to the terminal alongside auth codes (same `MailSink.Stdout` path the framework already uses).
- **Prod**: SMTP configured → codes sent via that transport. If `mail` block is missing in prod, the existing build-time check already fails the build with a clear error before deploy completes.

Net: admin panel works in dev with zero config, works in prod with the same SMTP config the app already needs. No new admin-specific configuration surface.

## 7. The page (in Mar, embedded in the framework)

Lives in `internal/admin/mar/`:

```
internal/admin/mar/
  Page.mar          -- the protected page (init/update/view)
  Auth.mar          -- email entry + code verification screens
  Services.mar      -- the six service contracts
  Types.mar         -- shared types (ServerInfo, DbStats, RequestLog, ...)
```

These are real `.mar` files. The framework loads them via `//go:embed` at startup, runs the same parser + typechecker the user's code goes through, and registers the resulting routes under `/_mar/admin/*`.

### 7.1 Page shape

Single page with sub-sections, each rendered as a `section` of the outer `list`:

```elm
module Mar.Admin.Page exposing (page)


import UI exposing
    ( navigationStack, navigationTitle, trailing
    , list, section, header
    , hstack, vstack, text, button
    )
import Mar.Admin.Services as Services
import Mar.Admin.Types as Types


type alias Model =
    { server   : RemoteData Types.ServerInfo
    , db       : RemoteData Types.DbStats
    , requests : RemoteData (List Types.RequestLog)
    , browsing : Maybe Browsing       -- present when an entity row is clicked
    }


type alias Browsing =
    { entity : String                 -- "users", "notes", ...
    , rows   : RemoteData (Page (Dict String Json))
    , cursor : Maybe Int
    }


type Msg
    = ServerLoaded   (Result String Types.ServerInfo)
    | DbLoaded       (Result String Types.DbStats)
    | RequestsLoaded (Result String (List Types.RequestLog))
    | EntityClicked  String                                          -- name from the dbStats list
    | RowsLoaded     String (Result String (Page (Dict String Json))) -- name + result, so a stale response can't clobber a newer entity selection
    | BackToOverview
    | RefreshClicked
    | LogoutClicked
    | LoggedOut      (Result String ())


view : AdminSession -> Model -> View Msg
view session model =
    navigationStack
        [ navigationTitle ("Admin — " ++ session.email)
        , leading  (button [] LogoutClicked "Sign out")
        , trailing (button [] RefreshClicked "Refresh")
        ]
        [ list
            (case model.browsing of
                Nothing ->
                    [ serverSection   model.server
                    , dbSection       model.db
                    , requestsSection model.requests
                    ]

                Just b ->
                    [ entityBrowser b ]
            )
        ]


page : Page
page =
    Page.adminProtected
        { path   = "/_mar/admin"
        , title  = "Admin"
        , init   = init
        , update = update
        , view   = view
        }
```

`Page.adminProtected` is a new framework primitive — same shape as `Page.protected` but gated by the admin session cookie instead of the user session cookie. It threads an `AdminSession` (containing the admin email + login time) into init/update/view.

### 7.2 Services

Five service contracts. All gated by the framework's admin-auth middleware:

| Service                       | Purpose                                                |
|-------------------------------|--------------------------------------------------------|
| `Mar.Admin.serverInfo`        | Mar version, Go version, build target, uptime, request counters |
| `Mar.Admin.dbStats`           | mar.db on-disk + WAL size, per-entity row count        |
| `Mar.Admin.recentRequests`    | everything currently in the in-memory buffer (capped at `adminPanel.recentRequestsSize`, default 200) |
| `Mar.Admin.listEntities`      | schema introspection — entity names + columns          |
| `Mar.Admin.listEntityRows`    | paginated row browser for any entity — `Dict String Json` per row, includes `users` as just-another-entity |

**No special-case service for users.** The panel browses the project's user table the same way it browses any other entity — via `listEntityRows`. This keeps the framework's admin surface uniform: one row-listing primitive, one column-introspection primitive, and the page handles them generically. v2 entity-edit forms get the same treatment.

**Implementation lives Go-side** in `internal/admin/services/*.go` (the Mar contracts in `Services.mar` are interpreted, but the bodies are Go primitives the runtime exposes — request log access, schema introspection, etc, can't be implemented in user-level Mar without leaking internals).

### 7.3 Why Page.adminProtected, not Auth.protect-with-role

`Auth.protect` is wired to the user-auth middleware that reads `mar_session` and looks up rows in the project's `users` entity. Reusing it would force the panel to know about the user's User type. Cleaner to add a parallel:

```
Page.protected         → user-auth, redirects to Auth.config.signInPage
Page.adminProtected    → admin-auth, redirects to /_mar/admin/sign-in
```

Different middleware, different cookie, different redirect target. Composes cleanly with the rest of the framework because the only things that change are the auth check and the redirect.

## 8. MVP feature list

What v1 actually shows, top-to-bottom on `/_mar/admin`:

1. **Server**
   - Mar version, Go version, build target
   - Booted at, uptime
   - Requests total / in-flight

2. **Database** — three sub-sections so the eye lands quickly:
   - **Database**: on-disk size of `mar.db` + WAL only.
   - **Tables**: user-defined entities (the business model). Click one → page swaps into row-browser view (paginated, columns from `PRAGMA table_info`, cells from a `SELECT * LIMIT N` projection).
   - **Framework tables**: `_mar_*`-prefixed (auth + admin + schema migrations). Same row browser, separate header so framework noise doesn't drown out the operator's own model.
   - All read-only — no edit, no row creation, no row deletion. v2 territory.
   - **No special-casing of `users`.** If `Auth.config` is in use, `users` shows up under Tables like everything else. If it isn't, `users` simply isn't there. No conditional rendering.

3. **Database backups** (catalog of automatic snapshots — see §13)
   - Newest-first list of `<timestamp>.tar.gz` entries from `<dir-of-mar.db>/backups/`.
   - Each row: timestamp, size, [Restore] [Download] buttons.
   - **Restore** does an atomic-swap-and-restart: validates `schemaFingerprint` matches the live DB (refuses 409 on mismatch), renames live `mar.db` → `mar.db.bak-<TS>`, moves the bundle's `mar.db` into place, exits the process so Fly auto-restart picks up the new state. Operator sees a banner "Server restarting…" and the panel polls `/api/whoami` until the server is back up, then auto-reloads.
   - **Download** streams the .tar.gz with `Content-Disposition: attachment` for cold-storage archival.

4. **Recent requests** (everything in the buffer — up to `adminPanel.recentRequestsSize`, default 200, manual refresh)
   - Time, method, path, status, duration ms, user email
   - Color-coded status (2xx green / 4xx yellow / 5xx red)
   - **Excludes the panel's own polling** (`/_mar/admin/*`) and the SSE reload channel (`/_mar/reload`). Same exclusion applies to the `requestsTotal` / `requestsInFlight` counters.

What v1 deliberately leaves out:
- Auth tools (impersonate, force-logout)
- Action runner
- Entity row detail / edit / create / delete (browsing only — read-only `Dict String Json` view)
- Inspector (request/response bodies)
- Time-series graphs
- Auto-refresh
- Search / filter inside an entity browser (just paginated cursor scan in v1)

## 9. Authorization recap

The full security boundary, top to bottom:

| Concern                          | What enforces it                                                  |
|----------------------------------|-------------------------------------------------------------------|
| Anyone hits `/_mar/admin`         | Login form renders. No data fetched. (Hiding the URL is not the gate.) |
| Email submitted to request-code  | If email ∉ `_mar_admins`, no code is sent. Generic 200 OK either way. |
| Code submitted                   | Constant-time compare against hashed code. Wrong code → fail without leaking which part was wrong. |
| Cookie present on subsequent req | Middleware loads admin session, gates `/_mar/admin/api/*`. Missing/expired cookie → 401. |
| `_mar_admins` table tampered with directly | Next boot's sync overwrites it from `mar.json`. Persistence requires editing config. |
| `mar.json` modified by attacker  | Out of scope — same trust level as modifying any source file. Git review is the control. |

## 10. Open questions

**Q1. How big should the in-memory request log be, and should it be configurable?**

To power `recentRequests` without writing every HTTP hit to disk, the runtime keeps a fixed-size circular buffer in memory — when it's full, each new request overwrites the oldest. Bigger buffer = more history visible in the panel but more RAM. Smaller = leaner but you lose requests faster on busy apps.

**Decision: configurable, with a documented range and hard rejection on out-of-range.**

```json
{ "adminPanel": { "recentRequestsSize": 200 } }
```

| Property      | Value                                                    |
|---------------|----------------------------------------------------------|
| Default       | `200`                                                     |
| Allowed range | `10` … `5000`                                            |
| Out of range  | Boot fails with `invalid mar.json: adminPanel.recentRequestsSize must be between 10 and 5000 (got N)` |
| Wrong type    | Boot fails with the standard manifest type-mismatch error |
| Missing       | Default applied silently                                  |

The range mirrors lispy's old `request_logs.go` (10/5000) — proven not-stupid. Lispy clamped silently when out of range; we reject on boot instead. Silent clamping creates "I set 99999 but the panel only shows 5000, why?" debugging mysteries; rejection forces the user to fix the config and removes the surprise.

This sets the template for every other knob the framework adds — see §11 below.

**Q2. Does the admin panel survive `mar dev`'s hot reload?**
The embedded sources are static (built into the binary), so reloads of user code don't touch them. An admin session survives a user-code reload because the session is in `_mar_admin_sessions`, not in memory. ✓

**Q3. What if the runtime crashes on boot before the panel is reachable?**

If boot itself fails (e.g. SMTP verify rejects the credentials, manifest validation rejects a knob, DB migration fails), the HTTP server never starts and `/_mar/admin` returns connection refused. By design — there's no "fallback panel" we can serve when the runtime didn't manage to boot, and there's no runtime-mutation path that could "fix" it from outside.

The recovery loop is the same as for any other boot failure: read the crash diagnostic via `mar fly logs` (the framework's existing `FriendlyError` path surfaces Title / Stage / Hints there), fix `mar.json` or whatever caused the crash, redeploy. The admin panel is just one more thing that's down until boot succeeds, not a special case.

**Q4. Should the Mar interpreter cache the parsed embedded sources?**
Yes — parse once on framework init, stash the AST, route requests against it. Avoids reparsing per request. Same lifecycle as user pages once they're loaded.

**Q5. What happens if the user adds an entity literally named `_mar_admins`?**
We reserve the `_mar_*` prefix for framework tables. `Entity.define "_mar_..."` should fail with a clear error pointing at this rule. (Implementation note: easy `strings.HasPrefix` check in `Entity.define`.)

**Q6. Multiple Mar apps on the same machine sharing a DB?**
Out of scope — each project has its own `mar.db`. If anyone tries to point two projects at the same DB, the admin panel is the least of their concerns.

## 11. Configuration discipline (project-wide)

Q1 introduced a pattern that should apply to **every** knob the framework adds going forward, not just the admin panel's. Lispy was strict about this and the result was a config surface that caught typos and bad values before they became confusing runtime behavior. This spec adopts the same discipline, with one strengthening: **validate as early as possible** — preferably compile time, falling back to boot time only when truly necessary.

### 11.1 The five rules

For every configurable value the framework reads from `mar.json`:

1. **Documented type** — the field has a known type (`Int`, `String`, `Bool`, `[String]`, etc).
2. **Documented default** — what happens if the field is absent.
3. **Documented allowed range** (for numerics) or **allowed values** (for strings/enums).
4. **Hard rejection on invalid** — fail with a friendly error naming the field and the violation, not silent fallback. **At the earliest phase that can prove invalidity** (see 11.2).
5. **Silent default on missing** — absence is fine and gets the documented default.

### 11.2 Two-phase validation: compile time first, boot time only if needed

Most config errors don't need a running runtime to detect — they're structural. Those should be caught by `mar build` / `mar dev` startup, before any binary ships to production.

The framework already has the split via `LoadManifestStructure` (no env resolution, suitable at build time) vs `LoadManifest` (full resolution, used at boot). The validator runs in both phases, but the **rules** that apply at each phase differ:

| Phase             | Runs in                  | What it can check                                                         |
|-------------------|--------------------------|---------------------------------------------------------------------------|
| **Compile time**  | `mar build`, `mar dev`   | Type correctness, range/enum membership for literal values, unknown fields, structural shape, cross-field consistency on literals (e.g. `auth` requires `mail`). |
| **Boot time**     | `mar-runtime` startup    | Same checks re-run on env-resolved values (e.g. `auth.sessionSecret = "env:SECRET"` → real secret only known at boot), plus connectivity (SMTP) and DB-state checks. |

The split lets the strict-typed knobs (numbers, enums, ranges) fail at compile time — the moment the user types a bad value into `mar.json`, `mar dev` rejects it. Production never sees that mistake. Boot-time validation only catches the residue: env-resolved strings, transport connectivity.

**Concrete examples of phase assignment**:

| Knob                                | Phase that catches it                                                |
|-------------------------------------|----------------------------------------------------------------------|
| `adminPanel.recentRequestsSize: 99999` | Compile time (literal int, range check on parse)                  |
| `adminPanel.recentRequestSize: 200` (typo) | Compile time (unknown field detection)                       |
| `system.sqlite.journalMode: "weal"`     | Compile time (enum membership)                                   |
| `auth.sessionSecret: "env:SECRET"`      | Compile time validates `env:` syntax; **boot time** validates the resolved secret is non-empty/long-enough. |
| `mail.smtpHost: "smtp.foo.com"`         | Compile time validates the string is non-empty; **boot time** verifies the connection works (already implemented). |
| `admins: ["not-an-email"]`              | Compile time (regex shape check)                                 |

### 11.3 Why hard rejection instead of silent clamping

| Scenario                          | Silent clamp                         | Hard rejection                      |
|-----------------------------------|--------------------------------------|--------------------------------------|
| User sets `recentRequestsSize: 99999` | Quietly becomes 5000. Panel works. User wonders why history is shorter than expected. | `mar dev` refuses to start: "must be between 10 and 5000 (got 99999)". User fixes config, ships, done. |
| User typoes `recentRequestSize` (missing `s`) | Field unused, default applied. User thinks their value is in effect. | `mar dev` refuses to start: "unknown field: adminPanel.recentRequestSize. Did you mean recentRequestsSize?" |
| User sets enum `journalMode: "weal"` (typo) | SQLite open might silently fall back, or fail with cryptic SQLite error | `mar build` refuses: "must be one of wal, delete, truncate, persist, memory, off (got weal)" |

Silent clamping prioritizes "the app boots no matter what" over "the user gets the behavior they configured." For dev productivity, the second is more valuable: bad config caught at compile time is a 5-second fix; bad config that boots but misbehaves is hours of confused debugging.

### 11.4 Reference values inherited from lispy

(Where lispy got it right; we revisit if usage data suggests otherwise.)

| Knob                               | Default | Range / values                         | Phase   |
|------------------------------------|---------|----------------------------------------|---------|
| `adminPanel.recentRequestsSize`    | 200     | `10` … `5000`                          | compile |
| `database.autoBackup.enabled`      | `true`  | `true | false`                         | compile |
| `database.autoBackup.intervalHours`| 6       | `1` … `168` (1h…1 week)                | compile |
| `database.autoBackup.retentionCount` | 28    | `2` … `100`                            | compile |
| `ios.serverUrl`                    | —       | https:// URL (or http://localhost for QA) | compile |
| `adminPanel.sessionDuration`       | 12 h    | `1 minute` … `7 days`                  | compile |
| `system.sqlite.busyTimeoutMs`      | 5000    | `0` … `600000`                         | compile |
| `system.sqlite.walAutoCheckpoint`  | 1000    | `0` … `1000000`                        | compile |
| `system.sqlite.cacheSizeKB`        | 2000    | `0` … `1048576` (1 GiB)                | compile |
| `system.sqlite.journalMode`        | `wal`   | `wal | delete | truncate | persist | memory | off` | compile |
| `system.sqlite.synchronous`        | `normal`| `off | normal | full | extra`          | compile |
| `auth.sessionDuration`             | 30 days | `1 minute` … `365 days`                | compile |
| `auth.sessionSecret` (resolved)    | —       | non-empty string, length ≥ 32 (required when `Auth.config` OR `admins` is in use, in production) | boot    |
| `mail.smtpHost` (connectivity)     | —       | reachable + STARTTLS + AUTH succeed    | boot    |

(The non-admin entries are aspirational — listed to show the pattern; their actual landing belongs to other specs. The admin-panel entries are this doc's commitment.)

### 11.5 Implementation note

A single `manifest.Validate(*Manifest, ValidationPhase) error` function gates all of this, called:

1. From `mar build` after `LoadManifestStructure`, with `Phase: CompileTime`.
2. From `mar dev` startup, same as above.
3. From `mar-runtime` after `LoadManifest`, with `Phase: BootTime` — re-runs every check (compile-time rules are still checked because `mar-runtime` might be running on a binary built outside the official flow), plus boot-only checks.

The `Phase` enum lets each rule declare when it applies. Tests assert each field's accept/reject cases at each phase. New knobs get added by extending the validator + the table in §11.4.

Unknown-field detection requires the manifest parser to expose unknown JSON keys (Go's `json.Decoder.DisallowUnknownFields()` does this for free) — same compile-time signal that catches typos.

## 12. Database backups (auto-backup + restore)

### 12.1 Why this is part of the admin panel

Backups are an operational concern, but the operator's mental model
of them lives next to the database itself: "show me the live state,
let me click to roll back". A separate `mar fly snapshots` tool would
fragment the workflow. The panel's Database section gets a sibling
**Database backups** section, and the same auth gate covers both.

The CLI (`mar fly database backup` / `backups` / `download`) is the
complement for ops scripting. **Restore is panel-only** — the
schema-mismatch refusal needs an interactive UI to surface clearly,
and the post-swap "wait for restart" loop is friendlier in the
browser than tied to a CLI session.

### 12.2 Catalog layout

Backups live alongside `mar.db` on the same Fly volume:

```
/data/
  mar.db
  mar.db-wal
  mar.db-shm
  backups/
    2026-05-08-060000.tar.gz   ← auto, every 6h
    2026-05-08-120000.tar.gz   ← auto
    2026-05-08-143022.tar.gz   ← ad-hoc via `mar fly database backup`
    ...
```

Each tarball is the same shape (whether automatic or ad-hoc):

```
metadata.json   timestamp, mar version, build target, app name,
                env:VAR refs declared in mar.json, schemaFingerprint
mar.json        deployed config (env refs intact, no values)
mar.db          consistent snapshot via `VACUUM INTO`
```

`schemaFingerprint` is a sha256 of the `sqlite_master` rows (sorted,
whitespace-normalized) — same fingerprint at backup time and at
restore time means the schemas are byte-identical.

### 12.3 Auto-backup scheduler

Boot of `mar dev` and `mar-runtime` calls `admin.MaybeStartAutoBackup`,
which spawns a goroutine if `database.autoBackup.enabled != false`
in `mar.json`. The goroutine:

1. On first wake, checks the catalog. If a backup younger than
   `intervalHours` exists, defers the first tick proportionally —
   prevents hot-reload churn from generating redundant backups.
2. Each tick: `VACUUM INTO /tmp/mar-snap-stage-*/mar.db` →
   stages mar.json + metadata.json → tar+gzip → moves to
   `<catalog>/<timestamp>.tar.gz` → prunes oldest entries to keep
   `retentionCount`.
3. Snapshot errors are logged but never abort the loop.

`VACUUM INTO` runs as a long read transaction. In WAL mode (which
the framework forces via DSN pragmas), this **does not block writers**
— writes go to `mar.db-wal` while the snapshot is in progress and
get checkpointed back after. Some IO contention is possible during
the snapshot window (~seconds for typical DBs), but no requests are
blocked.

### 12.4 Restore flow

Triggered from the panel's **Restore** button on any catalog entry:

1. **Pre-flight schema check**. Bundle's `schemaFingerprint` is
   compared with the live DB's. Mismatch → 409, no swap, no `.bak`,
   error banner explaining "this backup was taken against a different
   schema (likely a migration ran since)". Match → proceed.

2. **Atomic swap**. Rename `mar.db` → `mar.db.bak-<timestamp>`,
   rename WAL/SHM sidecars to `.bak-restoring` siblings, move the
   bundle's mar.db into place. All three operations are atomic on
   the same filesystem.

3. **Process exit**. `os.Exit(2)` after a 1.5s grace (so the HTTP
   response lands first). Fly's `restart_policy = on-failure` brings
   the machine back up; the new process opens the restored DB.

4. **Client-side polling**. The panel's JS polls `/api/whoami` every
   1.5s for up to 60s; first 200 response triggers
   `window.location.reload()`. The user sees a banner during the
   restart wait, then a fresh panel load.

If anything goes wrong post-swap (the new DB has data the binary
doesn't expect, machine fails to come up), the `.bak` file is
preserved on the volume. Manual recovery: `fly ssh console -C "mv
/data/mar.db.bak-<TS> /data/mar.db && fly machine restart"`.

### 12.5 Schema mismatch policy

We deliberately picked the strict version: **identical schemas only**.
A backup taken before a migration cannot be restored after that
migration. The error message is explicit:

> Schema mismatch — this backup was taken against a different schema
> (likely a migration ran since). Restore manually by deploying the
> matching binary first.

Why no migration replay? Migrations are forward-only and their
backfills assume preconditions that may not hold for arbitrary older
data. Trying to migrate forward during restore is a bug factory —
the bundle's data might violate constraints the migration's backfill
didn't anticipate, leaving the DB half-applied. Refusing strictly
is honest about the limit and pushes the operator toward the
correct manual recovery (deploy old binary → restore → redeploy).

### 12.6 What deliberately ISN'T here

- **Multi-machine catalogs.** Each Fly volume has its own catalog;
  scaling horizontally fragments them. Single-machine apps work
  fully today; horizontal scaling needs a separate design (object
  storage? designated leader machine?). Documented as a known limit.
- **Bundle versioning.** `metadata.json` doesn't carry a `version`
  field yet. Adding one is cheap; not done because nothing has
  forced the format to change yet.
- **Streaming replication** (à la Litestream). Auto-backup gives
  RPO of `intervalHours`; Litestream gives RPO of seconds. Out of
  scope for v1; we document Fly Volume Snapshots as the stopgap
  for finer-grained recovery.
- **`mar fly restore <bundle.tar.gz>`** uploading a local file.
  Restore is from-catalog only, by design — uploads invite "I sent
  you a file, restore it" workflows that don't carry the schema
  context the panel can validate.

See `docs/backup-smoke-test.md` for the manual production-smoke
checklist.

## 13. Why this shape

Three forces converged on this design:

1. **Framework-owned, embedded code**: the only way to make the panel update with `mar` upgrades without involving the user. Source-embedding rather than codegen because the runtime is already a working interpreter — no new artifact pipeline needed.
2. **Separate auth track**: keeps the user's `User` entity clean, lets admin sessions rotate independently, and removes the "you can't have admin without first having a user" footgun.
3. **`mar.json admins: [...]` as the *only* source of truth**: makes admin membership deploy-driven, version-controlled, reviewable. New admin = new deploy, period. Every alternative (env var, claim-on-first-visit, admin-promotes-admin UI, runtime-mutation escape hatch) fails one of: production safety, audit, "first admin" bootstrap, or "two ways to do the same thing." Keeping a single path means every admin who exists in production is also in git, every change is reviewable, and there's no gap between "what's committed" and "what's running."

The result: a panel that's there from day one of any Mar project, never gets edited by the user, never gets out of date relative to the framework, and gates itself with infrastructure orthogonal to the app's own auth. The user's only contact surfaces are `mar.json` and three CLI commands. Everything else lives in the framework.
