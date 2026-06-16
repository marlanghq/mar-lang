# Authentication & Authorization

How authentication works in Mar: **passwordless email codes**. The user receives a one-time code by email, types it back, and gets a session.

This is the reference for the `Auth.*` surface. For the language-level overview of services, pages, and the error model, see [`mar.md`](./mar.md).

## 1. Goals & non-goals

### Goals

- **Passwordless by default.** Email + 6-digit code. No password storage, no password reset flow, no "forgot password" page.
- **User owns the UI.** The framework provides handlers, not pages. Users compose their own sign-in / verify / logged-in pages with the regular `Page.*` MVU pattern. Mar must not ship a default login page.
- **One configuration, two clients.** Same backend serves the browser frontend and the iOS app (the latter via Bearer tokens instead of cookies).
- **Type-safe end to end.** The `User` record type is whatever the user defines; `Auth.protect` and `Page.protected` thread that exact type through to handlers and pages.
- **Drop-in.** Adding auth to an existing project is one `Auth.config` call plus `Auth.protect` around the services that need a signed-in user.
- **Safe defaults.** Constant-time comparisons, hashed codes/tokens at rest, rate-limited request endpoints, no email enumeration through error messages, opaque session cookies (HttpOnly + SameSite + Secure when HTTPS).

### Non-goals (for v1)

- Password authentication. Code is structured to allow it later, but the v1 surface is email-code only.
- OAuth / SSO (Google, GitHub, Apple, etc.).
- WebAuthn / passkeys.
- Multi-factor (the email code IS the factor).
- Account self-service: change email, delete account, merge accounts.
- Multi-tenant scoping: the spec assumes a single user pool.
- Granular permissions / capabilities. Authorization in v1 is coarse: anonymous, authenticated, role-matching predicate.

## 2. Mental model

Auth in Mar is structured as a **library**, not a code generator. Phoenix's `mix phx.gen.auth` writes ~2k lines of code into your project that you then own and edit. Mar's `Auth.config` returns a value that captures every decision; the framework owns the wiring.

This trades flexibility for cohesion:

- you can't easily replace half of the flow with custom code
- in exchange, the typing is tight (the `Auth User` value carries the user record's row type), the surface is small, and updates to the framework don't require code migrations in user projects

The model has four parts:

1. **`Auth.config`**: declarative description of how authentication works for this app: which entity holds users, how to identify them (which field is the email), how long sessions live, how to deliver the code.
2. **Auto-managed entities**: `auth_codes` and `auth_sessions`. Created and migrated by the runtime. The user never queries them directly.
3. **Server primitives**: `Auth.protect` wraps a service's handler and injects the signed-in user; the `/_auth/*` endpoints are auto-registered; `Page.protected` gates a page.
4. **Frontend primitives**: `Auth.requestCode`, `Auth.verifyCode`, `Auth.logout`, `Auth.me`. Plain `Effect` values the user wires into their `update` like any other backend call.

The sign-in page is just a normal page. The verify page is just a normal page. They are the user's UX; the framework is what their messages talk to.

## 3. The login flow, end to end

To anchor the API design, here is the entire flow with no code, just labelled steps. Every API decision below answers a question raised by one of these steps.

```
┌──────────┐                          ┌──────────────┐                    ┌───────────┐
│  User    │                          │  Mar app     │                    │  SMTP     │
│ browser  │                          │  (server)    │                    │  server   │
└────┬─────┘                          └──────┬───────┘                    └─────┬─────┘
     │                                       │                                  │
     │ 1. POST /_auth/request-code           │                                  │
     │    { email }                          │                                  │
     ├──────────────────────────────────────▶│                                  │
     │                                       │                                  │
     │                          2. lookup or upsert user                        │
     │                          3. invalidate prior unused codes for email      │
     │                          4. generate 6-digit code                        │
     │                          5. INSERT auth_codes (hash(code), email, exp)   │
     │                                       │                                  │
     │                                       │ 6. send email                    │
     │                                       ├─────────────────────────────────▶│
     │                                       │                                  │
     │ 7. 200 OK { ok: true }                │                                  │
     │◀──────────────────────────────────────┤                                  │
     │                                       │                                  │
     │ 8. user reads email, types 6-digit code into the user-built page         │
     │                                       │                                  │
     │ 9. POST /_auth/verify-code            │                                  │
     │    { email, code }                    │                                  │
     ├──────────────────────────────────────▶│                                  │
     │                                       │                                  │
     │                         10. SELECT auth_codes WHERE email = ? AND ...   │
     │                         11. constant-time compare hash(code)             │
     │                         12. on success:                                  │
     │                              - DELETE the code                           │
     │                              - generate session token                    │
     │                              - INSERT auth_sessions(hash(token), ...)    │
     │                              - Set-Cookie or response body for iOS       │
     │                         13. on failure: increment attempts, maybe lock   │
     │                                       │                                  │
     │ 14. 200 OK { user: {...} }            │                                  │
     │     Set-Cookie: mar_session=...       │                                  │
     │◀──────────────────────────────────────┤                                  │
     │                                       │                                  │
     │ 15. subsequent requests carry cookie/header → middleware loads user     │
```

## 4. Configuration

### 4.1 `Auth.config`

The single declaration that defines authentication for an app, five fields:

```elm
authConfig : Auth User
authConfig =
    Auth.config
        { entity          = users
        , identify        = \u -> u.email
        , email           = { from = "noreply@example.com", subject = "Your sign-in code" }
        , signup          = \email -> { email = email, name = "", role = Member }
        , sessionDuration = Time.days 30
        }
```

**Type:**

```elm
config :
    { entity          : Entity user
    , identify        : user -> String
    , email           : { from : String, subject : String }
    , signup          : String -> userExceptId
    , sessionDuration : Duration
    }
    -> Auth user

type Auth user   -- opaque
```

Field by field:

- **`entity`**: the user table. Row type must contain at least `{ id : a.id, email : String }` (Mar's row polymorphism enforces this).
- **`identify`**: projects the email out of the user record. Always `user -> String`.
- **`email`**: sender address and subject line of the code email. The body is fixed by the framework (see §4.2).
- **`signup`**: called when `request-code` arrives for an email not in the table. Returns the user record minus `id` (which the entity assigns). The framework persists it via `Repo.create` and proceeds with the code flow. To refuse signups today, raise an error from inside this function (a dedicated `invitationOnly` policy may come back if the pattern is common).
- **`sessionDuration`**: how long a session stays valid after a successful `verify-code`. Genuine product decision: 15 minutes for a banking app, 30 days for a consumer SaaS, 1 year for "remember me forever" social apps. Affects both the cookie's `Max-Age` and the server-side `auth_sessions.expires_at`. After the duration elapses the session is hard-expired (no refresh; the user signs in again).

Everything else, cookie name, code length and TTL, retry attempts, rate limits, email body, SameSite, is fixed by safe defaults. See §15 for what would have to change to make any of them configurable.

### 4.2 The email body

The framework sends a fixed transactional message:

```
Subject: <subject from Auth.config>

Your sign-in code is 123456.

It expires in 10 minutes. If you didn't request this, you can ignore this email.
```

The subject line carries the app's brand; the body is intentionally generic. If branded body templates become a real need, they'll come back via an additional `body` field on the `email` record.

### 4.3 `mar.json` integration

`Auth.config` reads two things from the manifest:

1. **SMTP credentials**: already documented as `mail.smtpHost` / `mail.smtpPort` / `mail.smtpUsername` / `mail.smtpPassword`. The auth runtime uses these to send codes; user code never touches the SMTP layer.
2. **Session secret**: used to derive HMACs for stored code/token hashes. New manifest key:
   ```json
   {
     "auth": {
       "sessionSecret": "env:MAR_SESSION_SECRET"
     }
   }
   ```
   `mar dev` generates a random secret on first run and writes it to `.mar/dev-secrets.json` (gitignored), so local development works without any setup. Production deploys must set the env var; `mar build` rejects manifests that use a literal value (the same secret-checking rule already enforced for `smtpPassword`).

These are just defaults; the user can override per-app via additional `Auth.config` fields if they ever need to.

## 5. Auto-managed entities

The framework adds two tables on first call to `Auth.config`. They are migrated automatically (same mechanism as user entities), they live in the same SQLite database (via `database.path` in mar.json), and user code does not query them.

### 5.1 `auth_codes`

```sql
CREATE TABLE _mar_auth_codes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    email       TEXT    NOT NULL,
    code_hash   TEXT    NOT NULL,        -- HMAC-SHA256(secret, code)
    attempts    INTEGER NOT NULL DEFAULT 0,
    expires_at  INTEGER NOT NULL,         -- unix seconds
    created_at  INTEGER NOT NULL,
    locked_at   INTEGER                    -- non-null = too many attempts; reject
);
CREATE INDEX _mar_auth_codes_email_idx ON _mar_auth_codes(email);
CREATE INDEX _mar_auth_codes_expires_idx ON _mar_auth_codes(expires_at);
```

A background sweep (every ~5 min, single goroutine) deletes rows where `expires_at < now() - 1 day`. The extra day of grace is just to give us a small audit window during incident triage; nothing in the user-facing flow depends on it.

### 5.2 `auth_sessions`

```sql
CREATE TABLE _mar_auth_sessions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash  TEXT    NOT NULL UNIQUE,   -- HMAC-SHA256(secret, raw_token)
    user_id     INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    user_agent  TEXT,                      -- best-effort, for the user's session list
    ip_address  TEXT                       -- best-effort
);
CREATE INDEX _mar_auth_sessions_token_idx ON _mar_auth_sessions(token_hash);
CREATE INDEX _mar_auth_sessions_user_idx ON _mar_auth_sessions(user_id);
```

The token sent to the client is `<base64(random 32 bytes)>`. The DB stores its HMAC. A leaked DB does not expose live sessions. (HMAC instead of plain SHA256 so an attacker who steals the DB still needs the session secret to forge a token that matches a stored hash.)

The `_mar_` prefix is reserved, user `Entity.define` calls with table names starting with `_mar_` are rejected at parse time by the existing manifest checker.

### 5.3 The user entity

The user's `users : Entity User` is what it always was; the framework does not touch its schema. The `User.id` field becomes the foreign key for `auth_sessions.user_id`, but the FK is logical (not enforced by SQLite) so that user-side deletes don't have to coordinate with framework tables.

When the `signup` callback runs (because the email wasn't in the table), the framework's INSERT happens through normal `Repo.create users { ... }`, same code path as user code, so all `Entity` constraints (NOT NULL, etc.) apply.

## 6. Server primitives

### 6.1 Auto-registered endpoints

When the app provides an `Auth.config` (section 4), the runtime auto-registers the auth endpoints. The app does not mount them; they exist as soon as auth is configured:

| Method | Path | Body | Response |
|---|---|---|---|
| POST | `/_auth/request-code` | `{ email : String }` | `{ ok: true }` (always, see section 8) |
| POST | `/_auth/verify-code` | `{ email : String, code : String }` | `{ user : <User row> }` plus `Set-Cookie` (or `{ user, sessionToken }` for non-cookie clients) |
| POST | `/_auth/logout` | (empty) | `{ ok: true }` plus a `Set-Cookie` clearing the session |
| GET  | `/_auth/whoami` | (none) | `{ user: <User row> | null }` |

The `/_auth/*` prefix is reserved, like `/_mar/*`: a service declared at one of those paths is rejected at compile time. Frontend code never calls these paths directly; it goes through the `Auth.*` Effects in section 7.

### 6.2 Protecting a service

A service is made authenticated by wrapping its handler with `Auth.protect`:

```elm
Auth.protect : Service req resp -> (req -> User -> Effect resp) -> ExposedService
```

`Auth.protect` injects the signed-in `User` as the handler's second argument and rejects the request with `401` before the handler runs when there is no valid session. The frontend sees the same `Service` value either way:

```elm
listMyNotesImpl : () -> User -> Effect (List Note)
listMyNotesImpl _ user =
    Repo.findBy notes { authorId = user.id }

services =
    [ Auth.protect Shared.listMyNotes listMyNotesImpl
    , Auth.protect Shared.createNote  createNoteImpl
    ]
```

There is no built-in role enum or role group: a handler that needs a role check reads it off the injected `user` and returns the appropriate outcome (or `Effect.fail` for a hard refusal). Services that do not need a user use `Service.implement` instead of `Auth.protect` and receive no user.

On a protected request the runtime:

1. reads the session cookie (or the `Authorization: Bearer ...` header, see section 7),
2. looks up `auth_sessions` by `HMAC(secret, token)`,
3. if found and not expired, loads the user via `Repo.findById users session.user_id`, updates `last_used_at`, and calls the handler with that user,
4. otherwise returns `401`, and the handler never runs.

## 7. Frontend primitives

The frontend never calls `/_auth/*` directly. The Auth module exposes Effects
that wrap those calls and deliver typed outcomes, one outcome union per
endpoint (a page only matches what can happen to it; there is no shared
auth error catch-all). Transport failures arrive as `Service.Error` in the
Err, exactly like a `Service.call`.

```elm
type Auth.RequestOutcome
    = CodeSent          -- never reveals whether the email has an account
    | InvalidEmail      -- malformed address (format only)
    | RateLimited

type Auth.VerifyOutcome user
    = SignedIn user     -- the app's own user record
    | WrongCode
    | TooManyAttempts
```

```elm
Auth.requestCode : { email : String }
    -> (Result Service.Error Auth.RequestOutcome -> msg) -> Effect msg
-- CodeSent answers for unknown emails too, to avoid enumeration.

Auth.verifyCode : { email : String, code : String }
    -> (Result Service.Error (Auth.VerifyOutcome user) -> msg) -> Effect msg
-- On Auth.SignedIn the cookie/Bearer token is already stored. The user value
-- is the complete row from the user entity, in the app's own User type.

Auth.logout : (Result String () -> msg) -> Effect msg
-- Clears the cookie / forgets the Bearer token client-side, AND invalidates
-- the session row server-side. Idempotent.

Auth.me : (Result String (Maybe user) -> msg) -> Effect msg
-- One-shot fetch of the current user. Nothing when not authenticated.
```

The outcome constructors are matched qualified, like `Service.Error`'s:

```elm
CodeVerified (Ok (Auth.SignedIn user)) -> ...
CodeVerified (Ok Auth.WrongCode)       -> ...
CodeVerified (Ok Auth.TooManyAttempts) -> ...
CodeVerified (Err why)                 -> ... -- transport; Service.errorToString
```

Note that `Auth.verifyCode` resolves to a `User`, not a `Session`. Sessions are an implementation detail; user code never holds a token.

### 7.1 Bootstrap on page load

When the runtime fetches `/_mar/program.json` to boot a frontend, the server inlines the current user (if authenticated) into the bootstrap payload:

```js
// program.json
{
  "module": { ... },
  "entry": "main",
  "session": { "user": { "id": 7, "email": "a@b.c", "name": "Alice" } }
}
```

This means a `Page.protected` page gets its `User` **synchronously** on first mount: no flash of unauthenticated content and no extra request. After login (`Auth.verifyCode` resolves), the runtime refreshes this in-memory bootstrap so the next page also sees the current user. For long-running sessions a page can call `Auth.me` to re-fetch.

### 7.2 Page-level auth requirement (recap)

A page chooses its auth posture through the `Page.*` combinator it is built from (see [`mar.md` section 5.2](./mar.md)):

| Combinator | Behavior on navigation |
|---|---|
| `Page.create` / `Page.dynamic` | Mounts unconditionally (public). |
| `Page.protected` / `Page.dynamicProtected` | Runs `Auth.me`; mounts with the `User` if there is a session, otherwise redirects to the sign-in page. |

The sign-in page is declared once, in `Auth.config`:

```elm
auth : Auth { id : Int, email : String }
auth =
    Auth.config
        { entity          = Backend.Users.users
        , identify        = \u -> u.email
        , signInPage      = Frontend.SignIn.page
        , email           = { subject = "Your sign-in code" }
        , signup          = \userEmail -> { email = userEmail }
        , sessionDuration = Time.days 30
        }
```

`Page.protected` redirects there when there is no session. After a successful `Auth.verifyCode`, the sign-in page calls `Auth.completeSignIn`, which returns the user to wherever the redirect came from (or home).

## 8. Security considerations

The framework owns these concerns; the user does not need to think about them.

### 8.1 Code generation
- **Length:** 6 digits (configurable: 4–10).
- **Source:** `crypto/rand`. Never `math/rand`.
- **Encoding:** decimal digits only (so codes are easy to type; no ambiguous `0`/`O`).
- **Storage:** HMAC-SHA256 with the session secret. No reversible encryption, no plaintext storage. Codes only exist in plaintext for the moment between generation and email send.

### 8.2 Comparison
- **Constant-time:** all hash comparisons use `crypto/subtle.ConstantTimeCompare`. No early-exit on first-byte mismatch.

### 8.3 Email enumeration
- **Symmetric responses:** `request-code` always returns `200 OK { ok: true }` after the same minimum-time delay (~150 ms) regardless of whether the email exists. Attackers cannot tell from the response whether an account exists.
- **Symmetric errors:** `verify-code` returns the same generic `InvalidCode` for "no such email", "no such code", "expired code", "wrong code". Distinct only when locked out (then `TooManyAttempts`) or rate-limited (then `RateLimited`).

### 8.4 Brute force
- **Per-code attempts:** `auth_codes.attempts` counter; after 5 wrong attempts (fixed) `locked_at` is set, the code becomes unusable, and `TooManyAttempts` is returned for any further attempt.
- **Per-email request rate:** 3 codes per email per hour (fixed). Sliding window in memory (no extra DB pressure); restarting the server resets it. `RateLimited` carries `retryAfter` in the body.
- **Per-IP request rate:** 20 codes per IP per hour (fixed). Same mechanism. IP is read from `X-Forwarded-For` if `mar.json` has `server.trustProxyHeaders: true`, otherwise from the connection.

### 8.5 Session hygiene
- **Token entropy:** 32 random bytes from `crypto/rand`, base64url-encoded.
- **Cookie attributes:** `HttpOnly; SameSite=Lax; Path=/`. `Secure` is added automatically when the request was over HTTPS (or `server.publicUrl` starts with `https://`). `Lax` is fixed (not configurable), it blocks classic CSRF on cross-site POSTs while still letting legitimate top-level navigations (clicking a link in an email pointing back to the app) carry the cookie. Apps that need `Strict` would have to opt into a more conservative posture explicitly; that knob isn't shipped in v1.
- **Rotation:** on every login (verify-code), the token is fresh, even if the same user logs in twice in parallel, neither knows the other's token. There is no "token refresh"; sessions live for `session.duration` and then expire absolutely. `last_used_at` is purely informational (for an eventual "your active sessions" UI).
- **Logout invalidation:** `auth_sessions` row is deleted server-side, so a stolen cookie stops working at the moment of logout. Cookie is also overwritten with `Max-Age=0` to clear the browser store.

### 8.6 What the framework does NOT do (intentionally)
- **No CSRF token for `/_auth/*`:** verify-code requires knowing both the email and the unguessable code, which already serves as a per-request authenticator. logout is idempotent and reading-only side effects don't matter for auth state. (For ordinary service calls the existing CSRF policy applies, see [`mar.md`](./mar.md).)
- **No password reset flow:** there are no passwords.
- **No email-change confirmation:** out of scope for v1; user-facing apps can implement it on top of the user entity.

## 9. Dev ergonomics

### 9.1 Email in development

Configuring SMTP for local dev is friction. When `mar dev` is running and:

- `mail.smtpHost` is unset, OR
- `mail.smtpHost` is `"localhost"` and no SMTP server is listening, OR
- the env var `MAR_DEV_EMAIL_SINK=stdout` is set,

the framework switches the email transport to a **dev sink** that:

1. Logs the email (subject, recipient, body) to the dev server's stdout.
2. Pushes the email into the dev dock's "Mail" panel (a new panel registered when `Auth.config` is detected in the app), so the developer can read codes without a real inbox.
3. Returns success to the handler so the flow continues end-to-end.

In production builds (`mar build`), this sink is unavailable; missing SMTP config makes `Auth.config` log a startup warning and `request-code` returns `EmailDeliveryFailed`.

## 10. iOS

The same backend serves the iOS app. The differences:

- **No cookies.** The auth response from `/_auth/verify-code` returns the session token in the JSON body when the request carries the header `X-Mar-Client: ios`. The iOS runtime sets this header automatically on every request originating from `Service.call`.
- **Token storage.** The Swift runtime (`MarRuntimeIOS`) provides a small Keychain wrapper. After `Auth.verifyCode` resolves on iOS, the runtime stores the returned token under `kSecAttrAccount = "mar.session"`. Subsequent requests read it back and add `Authorization: Bearer <token>`.
- **Logout.** `Auth.logout` calls the same endpoint, then clears the Keychain entry.
- **`Auth.me` semantics.** Identical: returns `Maybe User` based on the stored token's validity.

User code is **identical** between web and iOS: the differences live entirely in the runtime. The app writes one sign-in page, both clients run it, and both reach a logged-in state through the same `Auth.verifyCode` call.


## 11. Sample app: complete login

The runnable end-to-end versions live in the repo: `examples/hello-auth` (the smallest email-plus-code login with one protected page) and `examples/team-notes` (multi-page, with dynamic routes). The pieces, in brief:

**Main.mar** wires the app and configures auth:

```elm
auth : Auth { id : Int, email : String }
auth =
    Auth.config
        { entity          = Backend.Users.users
        , identify        = \u -> u.email
        , signInPage      = Frontend.SignIn.page
        , email           = { subject = "Your sign-in code" }
        , signup          = \userEmail -> { email = userEmail }
        , sessionDuration = Time.days 30
        }


main : Effect ()
main =
    App.fullstack
        { services = []
        , pages    = [ Frontend.SignIn.page, Frontend.Home.page ]
        }
```

**Backend/Users.mar** declares the user entity `Auth.config` points at:

```elm
users : Entity Shared.User
users =
    Entity.define
        { name = "users"
        , columns =
            { id    = Entity.serial
            , email = Entity.text Entity.notNull
            }
        , uniques = [["email"]]
        }
```

**Frontend/SignIn.mar** runs the two-step flow with typed outcomes (no error strings on the wire). The branches below are abbreviated; see the examples for full bodies:

```elm
type Msg
    = DraftChanged String
    | Submitted
    | CodeRequested (Result Service.Error Auth.RequestOutcome)
    | CodeVerified (Result Service.Error (Auth.VerifyOutcome Shared.User))


update : Msg -> Model -> (Model, Effect Msg)
update msg model =
    case msg of
        Submitted ->
            case model.step of
                AskEmail email ->
                    ( model, Auth.requestCode { email = email } CodeRequested )

                AskCode email code ->
                    ( model, Auth.verifyCode { email = email, code = code } CodeVerified )

                _ ->
                    ( model, Effect.none )

        CodeRequested (Ok Auth.CodeSent)       -> -- move to the code page
        CodeRequested (Ok Auth.InvalidEmail)   -> -- show "not a valid email"
        CodeRequested (Ok Auth.RateLimited)    -> -- show "too many requests"
        CodeRequested (Err why)                -> -- Service.errorToString why

        CodeVerified (Ok (Auth.SignedIn _))    -> ( model, Auth.completeSignIn )
        CodeVerified (Ok Auth.WrongCode)       -> -- show "wrong code"
        CodeVerified (Ok Auth.TooManyAttempts) -> -- show "too many tries"
        CodeVerified (Err why)                 -> -- Service.errorToString why

        DraftChanged value ->
            ( updateDraft value model, Effect.none )


page : Page
page =
    Page.create
        { path = "/sign-in", title = "Sign in"
        , init = init, update = update, view = view
        }
```

`Auth.completeSignIn` sends the user back to wherever a 401 redirected them (or home).

**Frontend/Home.mar** is protected, so `init` receives the `User` synchronously and there is no flash of unauthenticated content:

```elm
init : Shared.User -> (Model, Effect Msg)
init user =
    ( { user = user }, Effect.none )


page : Page
page =
    Page.protected
        { path = "/", title = "Home"
        , init = init, update = update, view = view
        }
```

## 12. Type signature reference

The current `Auth` surface (sections 4, 6, and 7 cover each in context):

```elm
-- Configuration (Main.mar, top level)
Auth.config : { entity, identify, signInPage, email, signup, sessionDuration } -> Auth user

-- Backend: protect a service handler; injects the signed-in user, 401s without a session
Auth.protect : Service req resp -> (req -> User -> Effect resp) -> ExposedService

-- Frontend: the sign-in flow
Auth.requestCode    : { email : String }
    -> (Result Service.Error Auth.RequestOutcome -> msg) -> Effect msg
Auth.verifyCode     : { email : String, code : String }
    -> (Result Service.Error (Auth.VerifyOutcome user) -> msg) -> Effect msg
Auth.completeSignIn : Effect msg
Auth.me             : (Result String (Maybe user) -> msg) -> Effect msg
Auth.logout         : (Result String () -> msg) -> Effect msg
```

The outcome unions (constructors are qualified, like `Service.Error`'s):

```elm
type Auth.RequestOutcome
    = CodeSent          -- never reveals whether the email has an account
    | InvalidEmail
    | RateLimited

type Auth.VerifyOutcome user
    = SignedIn user
    | WrongCode
    | TooManyAttempts
```

A page chooses its auth posture with `Page.create` / `Page.protected` / `Page.dynamic` / `Page.dynamicProtected` (see [`mar.md` section 5.2](./mar.md)).

## 13. Future additions

Not in v1, revisited when concrete need arises. None of these change the interface above:

- **Session list and revocation.** A service plus UI to render and revoke `auth_sessions` rows for the current user.
- **Email change flow.** Confirm the new address, then update.
- **Audit log.** An opt-in `_mar_auth_log` table.
- **Granular permissions.** A capability or policy model beyond per-handler role checks.
- **Refresh tokens.** Sessions currently live for `sessionDuration` and then hard-expire; a short access token plus a longer refresh token is doable but adds complexity v1 does not need.
- **Password or OAuth sign-in.** These would add new `Auth.*` Effects (`Auth.signInWithPassword`, `Auth.signInWithGoogle`) without changing the email-code flow.
