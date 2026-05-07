# Authentication & Authorization

A specification for the first authentication mechanism Mar will ship: **passwordless email codes**. The user receives a one-time code by email, types it back, and gets a session.

This document is a design document, not an API reference. It is meant to be detailed enough to be used as the implementation blueprint.

> **Status.** Specification only. The `Auth.*` and `Routes.*` symbols referenced in the existing [`mar.md`](./mar.md) are aspirational — this document defines what they should mean and how they should behave.

## 1. Goals & non-goals

### Goals

- **Passwordless by default.** Email + 6-digit code. No password storage, no password reset flow, no "forgot password" page.
- **User owns the UI.** The framework provides handlers, not screens. Users compose their own login / verify / logged-in screens with the regular `Screen.with` MVU pattern. Mar must not ship a default login page.
- **One configuration, two clients.** Same backend serves the browser frontend and the iOS app (the latter via Bearer tokens instead of cookies).
- **Type-safe end to end.** The `User` record type is whatever the user defines; `Auth.requireUser` and the screen-init injection thread that exact type through to handlers and screens.
- **Drop-in.** Adding auth to an existing project is one `Auth.config` call plus a `Routes.authenticated` group around protected routes.
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

1. **`Auth.config`** — declarative description of how authentication works for this app: which entity holds users, how to identify them (which field is the email), how long sessions live, how to deliver the code.
2. **Auto-managed entities** — `auth_codes` and `auth_sessions`. Created and migrated by the runtime. The user never queries them directly.
3. **Server primitives** — `Routes.authenticated`, `Routes.requireRole`, `Auth.endpoints`, plus `init`-signature-driven injection for screens.
4. **Frontend primitives** — `Auth.requestCode`, `Auth.verifyCode`, `Auth.logout`, `Auth.me`. Plain `Effect` values the user wires into their `update` like any other backend call.

The login screen is just a normal screen. The verify screen is just a normal screen. The two are the user's UX; the framework is what their messages talk to.

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
     │ 8. user reads email, types 6-digit code into the user-built screen       │
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

The single declaration that defines authentication for an app — five fields:

```elm
authConfig : Auth User
authConfig =
    Auth.config
        { entity          = users
        , identify        = \u -> u.email
        , email           = { from = "noreply@example.com", subject = "Your sign-in code" }
        , signup          = \email -> { email = email, name = "", role = Member }
        , sessionDuration = Duration.days 30
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

- **`entity`** — the user table. Row type must contain at least `{ id : a.id, email : String }` (Mar's row polymorphism enforces this; same pattern other `Endpoint.*` helpers use per [`mar.md` §4.7](./mar.md)).
- **`identify`** — projects the email out of the user record. Always `user -> String`.
- **`email`** — sender address and subject line of the code email. The body is fixed by the framework (see §4.2).
- **`signup`** — called when `request-code` arrives for an email not in the table. Returns the user record minus `id` (which the entity assigns). The framework persists it via `Repo.create` and proceeds with the code flow. To refuse signups today, raise an error from inside this function (a dedicated `invitationOnly` policy may come back if the pattern is common).
- **`sessionDuration`** — how long a session stays valid after a successful `verify-code`. Genuine product decision: 15 minutes for a banking app, 30 days for a consumer SaaS, 1 year for "remember me forever" social apps. Affects both the cookie's `Max-Age` and the server-side `auth_sessions.expires_at`. After the duration elapses the session is hard-expired (no refresh; the user signs in again).

Everything else — cookie name, code length and TTL, retry attempts, rate limits, email body, SameSite — is fixed by safe defaults. See §15 for what would have to change to make any of them configurable.

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

1. **SMTP credentials** — already documented as `mail.smtpHost` / `mail.smtpPort` / `mail.smtpUsername` / `mail.smtpPassword`. The auth runtime uses these to send codes; user code never touches the SMTP layer.
2. **Session secret** — used to derive HMACs for stored code/token hashes. New manifest key:
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

The `_mar_` prefix is reserved — user `Entity.define` calls with table names starting with `_mar_` are rejected at parse time by the existing manifest checker.

### 5.3 The user entity

The user's `users : Entity User` is what it always was; the framework does not touch its schema. The `User.id` field becomes the foreign key for `auth_sessions.user_id`, but the FK is logical (not enforced by SQLite) so that user-side deletes don't have to coordinate with framework tables.

When the `signup` callback runs (because the email wasn't in the table), the framework's INSERT happens through normal `Repo.create users { ... }` — same code path as user code, so all `Entity` constraints (NOT NULL, etc.) apply.

## 6. Server primitives

### 6.1 Auto-registered endpoints

```elm
Auth.endpoints : Auth user -> List Route
```

Returns three routes that the user mounts in their `routes` list:

| Method | Path | Body | Response |
|---|---|---|---|
| POST | `/_auth/request-code` | `{ email : String }` | `{ ok: true }` (always — see §8) |
| POST | `/_auth/verify-code` | `{ email : String, code : String }` | `{ user : <User row> }` + `Set-Cookie` (or `{ user, sessionToken }` for non-cookie clients) |
| POST | `/_auth/logout` | (empty) | `{ ok: true }` + `Set-Cookie` clearing the session |
| GET  | `/_auth/whoami` | (none) | `{ user: <User row> | null }` |

These routes are part of `Auth.endpoints` and are NOT a separate group — the user composes them into their `routes` list:

```elm
routes : List Route
routes =
    List.concat
        [ Auth.endpoints authConfig
        , Routes.public
            [ ... ]
        , Routes.authenticated authConfig
            [ ... ]
        ]
```

The endpoints sit at well-known paths (`/_auth/*`). Like `/_mar/*`, the `_auth` prefix is reserved — user `Endpoint.create "/_auth/foo"` is rejected at type-check time by the existing reserved-path check (extended to include `_auth`).

### 6.2 Route policies

Existing primitives in [`mar.md` §4.8](./mar.md), unchanged in shape but now refer to the `Auth` value instead of an `Entity`:

```elm
Routes.public        : List Route -> List Route
Routes.authenticated : Auth user -> List Route -> List Route
Routes.requireRole   : Auth user -> (user -> Bool) -> List Route -> List Route
Routes.optional      : Auth user -> List Route -> List Route   -- handler sees Maybe user
```

`Routes.requireRole` takes a user-supplied predicate so role models stay in user code (no built-in role enum). Typical usage:

```elm
Routes.requireRole authConfig (\u -> u.role == Admin)
    [ purgeAll |> Endpoint.implement createPurge
    ]
```

When a request hits a route inside `Routes.authenticated`:

1. Middleware reads the session cookie (or `Authorization: Bearer …` header — see §7).
2. Looks up `auth_sessions` by `HMAC(secret, token)`.
3. If found and not expired: loads the user via `Repo.findById users session.user_id`. Updates `last_used_at`. Attaches user to the request. Calls handler.
4. If not found / expired / missing: returns `401 Unauthorized` with `{"error":"not authenticated"}`. Handler is not called.

### 6.3 Handler-level access

When a route is inside `Routes.authenticated authConfig`, the handler signature receives the user as an extra positional argument:

```elm
listMyNotes : User -> Effect (ResponseError NoTag) (List Note)
listMyNotes user =
    Repo.all notes
        |> Repo.where_ (\n -> Repo.eq n.authorId user.id)
        |> Repo.allEffect

myNotesRoute : Route
myNotesRoute =
    listEndpoint |> Endpoint.implement listMyNotes
```

For `Routes.optional`:

```elm
publicProfile : Maybe User -> ProfileId -> Effect (ResponseError NoTag) Profile
publicProfile maybeUser profileId = ...
```

The user argument is the LAST positional parameter (after path args, body, etc.), to keep handlers without auth visually identical to handlers with auth.

> **Mechanism.** This relies on the same arity-based injection used for screen `init` ([`mar.md` §5.2](./mar.md)): when `Endpoint.implement` connects a handler into a route inside `Routes.authenticated`, the type checker accepts handler types that take an additional `User` parameter and the runtime threads it in. If a handler omits the `User` parameter, that's also accepted (and the user simply isn't passed). This keeps the policy declaration where it belongs (the `Routes.authenticated` group) without forcing every handler to declare the parameter.

### 6.4 Programmatic helpers (handler-internal)

For the rare case where a handler inside a public group still wants to know if there's a session:

```elm
Auth.maybeUser : Auth user -> Effect (ResponseError NoTag) (Maybe user)
```

This Effect returns the user from the current request's session, or `Nothing` if there isn't one. Implemented by reading the request context the runtime threads into the handler — no extra DB hit beyond what `Routes.optional` would do.

## 7. Frontend primitives

The frontend never calls `/_auth/*` directly. The Auth module exposes Effects that wrap those calls and decode their responses into mar values.

```elm
type AuthError
    = InvalidCode
    | CodeExpired
    | TooManyAttempts
    | RateLimited { retryAfter : Duration }
    | EmailDeliveryFailed     -- only if mail.* not configured or SMTP rejected
    | NotAuthenticated         -- /_auth/whoami returned no session
    | Network NetworkError     -- transport failures, surfaced like any other Endpoint.call
```

```elm
Auth.requestCode : { email : String } -> Effect AuthError ()
-- Always succeeds with () to avoid email enumeration. Errors are reserved for
-- transport failures and rate-limiting, never "user not found".

Auth.verifyCode  : { email : String, code : String } -> Effect AuthError User
-- On success, the cookie/Bearer token is set automatically before the Effect
-- resolves. The User value is the complete row from the user entity, decoded
-- using whatever User type the app declared.

Auth.logout      : Effect AuthError ()
-- Clears the cookie / forgets the Bearer token client-side, AND invalidates the
-- session row server-side. Idempotent.

Auth.me          : Effect AuthError (Maybe User)
-- One-shot fetch of the current user. Resolves to Nothing if not authenticated;
-- only emits AuthError for transport problems.
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

This means screens that declare `init : User -> (Model, Effect Never Msg)` get the user **synchronously** on first mount. No flash of unauthenticated content, no extra request. After login (`Auth.verifyCode` resolves), the runtime updates this in-memory bootstrap so the next screen the user navigates to also sees the fresh user. For long-running sessions the user can call `Auth.me` to refresh.

### 7.2 Screen-level auth requirement (recap)

The init-signature inference described in [`mar.md` §5.2](./mar.md) is the screen-side companion to `Routes.authenticated`:

| `init` signature | What runtime does on navigation |
|---|---|
| `(Model, Effect Never Msg)` | Mounts unconditionally (truly public) |
| `User -> (Model, Effect Never Msg)` | If session exists, mounts with user. Otherwise, redirects to the screen registered as `Auth.loginScreen`. |
| `Maybe User -> (Model, Effect Never Msg)` | Mounts unconditionally; passes `Nothing` if no session. |

The redirect target is set by the user:

```elm
main : Effect Never ()
main =
    App.fullstack
        { api      = ...
        , pages    = [ HomePage.screen, NotesPage.screen, LoginPage.screen ]
        , auth     = Auth.frontend authConfig (Auth.loginScreen LoginPage.screen)
        }
```

`Auth.frontend` builds a frontend-side configuration value with the routing wired in. `Auth.loginScreen` declares which screen handles the unauthenticated state. The screen passed there must have `init : Maybe NavTarget -> (Model, Effect Never Msg)` so the runtime can pass it the original target (so post-login the user lands where they were trying to go).

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
- **Cookie attributes:** `HttpOnly; SameSite=Lax; Path=/`. `Secure` is added automatically when the request was over HTTPS (or `server.publicUrl` starts with `https://`). `Lax` is fixed (not configurable) — it blocks classic CSRF on cross-site POSTs while still letting legitimate top-level navigations (clicking a link in an email pointing back to the app) carry the cookie. Apps that need `Strict` would have to opt into a more conservative posture explicitly; that knob isn't shipped in v1.
- **Rotation:** on every login (verify-code), the token is fresh — even if the same user logs in twice in parallel, neither knows the other's token. There is no "token refresh"; sessions live for `session.duration` and then expire absolutely. `last_used_at` is purely informational (for an eventual "your active sessions" UI).
- **Logout invalidation:** `auth_sessions` row is deleted server-side, so a stolen cookie stops working at the moment of logout. Cookie is also overwritten with `Max-Age=0` to clear the browser store.

### 8.6 What the framework does NOT do (intentionally)
- **No CSRF token for `/_auth/*`:** verify-code requires knowing both the email and the unguessable code, which already serves as a per-request authenticator. logout is idempotent and reading-only side effects don't matter for auth state. (For non-auth routes the existing CSRF policy applies — see [`mar.md`](./mar.md).)
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

- **No cookies.** The auth response from `/_auth/verify-code` returns the session token in the JSON body when the request carries the header `X-Mar-Client: ios`. The iOS runtime sets this header automatically on every request originating from `Endpoint.call` / `Service.call`.
- **Token storage.** The Swift runtime (`MarRuntimeIOS`) provides a small Keychain wrapper. After `Auth.verifyCode` resolves on iOS, the runtime stores the returned token under `kSecAttrAccount = "mar.session"`. Subsequent requests read it back and add `Authorization: Bearer <token>`.
- **Logout.** `Auth.logout` calls the same endpoint, then clears the Keychain entry.
- **`Auth.me` semantics.** Identical: returns `Maybe User` based on the stored token's validity.

User code in `LoginPage.mar` is **identical** between web and iOS — the differences live entirely in the runtime. The user writes one screen, both clients run it, both arrive at a logged-in state through the same `Auth.verifyCode |> Effect.toMsg` pipeline.

## 11. Sample app: complete login

This is the full set of files the user writes. Nothing is hidden; the framework provides what's referenced.

### `Main.mar`

```elm
module Main exposing (main)

import App
import Auth
import Routes
import Users exposing (users)
import Endpoints
import HomePage
import LoginPage
import VerifyPage

authConfig : Auth Users.User
authConfig =
    Auth.config
        { entity          = users
        , identify        = .email
        , email           = { from = "noreply@myapp.test", subject = "Sign in to MyApp" }
        , signup          = \email -> { email = email, name = "", role = Member }
        , sessionDuration = Duration.days 30
        }

main : Effect Never ()
main =
    App.fullstack
        { api =
            { routes = List.concat
                [ Auth.endpoints authConfig
                , Routes.public
                    [ Endpoints.healthcheck
                    ]
                , Routes.authenticated authConfig
                    [ Endpoints.listMyNotes
                    , Endpoints.createNote
                    ]
                ]
            , services = []
            }
        , pages = [ HomePage.screen, LoginPage.screen, VerifyPage.screen ]
        , auth  = Auth.frontend authConfig (Auth.loginScreen LoginPage.screen)
        }
```

### `Users.mar`

```elm
module Users exposing (User, Role, users)

import Entity

type Role = Member | Admin

type alias User =
    { id    : UserId
    , email : String
    , name  : String
    , role  : Role
    }

users : Entity User
users =
    Entity.define "users"
        { id    = Entity.serial
        , email = Entity.text Entity.notNull
        , name  = Entity.text Entity.notNull
        , role  = Entity.enum [Member, Admin] Member
        }
```

### `LoginPage.mar` — user-built UI

```elm
module LoginPage exposing (screen)

import Auth
import Effect
import Navigation
import Screen
import View exposing (..)

type alias Model =
    { emailDraft  : String
    , submitting  : Bool
    , error       : Maybe String
    , postLoginTo : Maybe Navigation.Target  -- where to send the user after success
    }

type Msg
    = EmailChanged String
    | SubmitClicked
    | RequestSettled (Result AuthError ())

init : Maybe Navigation.Target -> (Model, Effect Never Msg)
init target =
    ( { emailDraft  = ""
      , submitting  = False
      , error       = Nothing
      , postLoginTo = target
      }
    , Effect.none
    )

update : Msg -> Model -> (Model, Effect Never Msg)
update msg model =
    case msg of
        EmailChanged s ->
            ({ model | emailDraft = s, error = Nothing }, Effect.none)

        SubmitClicked ->
            if String.isEmpty model.emailDraft then
                ({ model | error = Just "Enter an email" }, Effect.none)
            else
                ( { model | submitting = True, error = Nothing }
                , Auth.requestCode { email = model.emailDraft }
                    |> Effect.toMsg RequestSettled
                )

        RequestSettled (Ok ()) ->
            -- Hand off to VerifyPage with the email (and the original target).
            ( model
            , Navigation.push (VerifyPage.targetFor
                { email = model.emailDraft, after = model.postLoginTo })
            )

        RequestSettled (Err (RateLimited details)) ->
            ( { model
                | submitting = False
                , error = Just ("Too many attempts. Try again in " ++ Duration.toHumanString details.retryAfter ++ ".")
              }
            , Effect.none
            )

        RequestSettled (Err _) ->
            ( { model | submitting = False, error = Just "Couldn't send the code. Try again." }
            , Effect.none
            )

view : Model -> View Msg
view model =
    section []
        [ title "Sign in"
        , text "We'll email you a one-time code."
        , field
            [ label "Email"
            , value model.emailDraft
            , onChange EmailChanged
            , disabled model.submitting
            , errorMessage model.error
            ]
        , button
            [ onClick SubmitClicked
            , intent Primary
            , disabled (model.submitting || String.isEmpty model.emailDraft)
            ]
            [ text (if model.submitting then "Sending…" else "Send code") ]
        ]

screen : Screen (Maybe Navigation.Target) Model Msg
screen =
    Screen.with
        { path   = "/login"
        , init   = init
        , update = update
        , view   = view
        }
```

### `VerifyPage.mar` — user-built UI

```elm
module VerifyPage exposing (screen, targetFor)

import Auth
import Effect
import Navigation
import Screen
import View exposing (..)

type alias PathArgs =
    { email : String
    , after : Maybe Navigation.Target
    }

type alias Model =
    { codeDraft   : String
    , email       : String
    , postLoginTo : Maybe Navigation.Target
    , submitting  : Bool
    , error       : Maybe String
    }

type Msg
    = CodeChanged String
    | SubmitClicked
    | VerifySettled (Result AuthError User)

init : PathArgs -> (Model, Effect Never Msg)
init args =
    ( { codeDraft = ""
      , email = args.email
      , postLoginTo = args.after
      , submitting = False
      , error = Nothing
      }
    , Effect.none
    )

update : Msg -> Model -> (Model, Effect Never Msg)
update msg model =
    case msg of
        CodeChanged s ->
            ({ model | codeDraft = String.trim s, error = Nothing }, Effect.none)

        SubmitClicked ->
            ( { model | submitting = True }
            , Auth.verifyCode { email = model.email, code = model.codeDraft }
                |> Effect.toMsg VerifySettled
            )

        VerifySettled (Ok _user) ->
            ( model
            , Navigation.push (Maybe.withDefault Navigation.home model.postLoginTo)
            )

        VerifySettled (Err InvalidCode) ->
            ( { model | submitting = False, error = Just "That code didn't match. Try again." }
            , Effect.none
            )

        VerifySettled (Err CodeExpired) ->
            ( { model | submitting = False, error = Just "That code has expired. Request a new one." }
            , Effect.none
            )

        VerifySettled (Err TooManyAttempts) ->
            ( { model | submitting = False, error = Just "Too many wrong attempts. Request a new code." }
            , Effect.none
            )

        VerifySettled (Err _) ->
            ( { model | submitting = False, error = Just "Something went wrong. Try again." }
            , Effect.none
            )

view : Model -> View Msg
view model =
    section []
        [ title "Enter your code"
        , text ("We sent a 6-digit code to " ++ model.email ++ ".")
        , field
            [ label "Code"
            , value model.codeDraft
            , onChange CodeChanged
            , disabled model.submitting
            , inputMode Numeric
            , errorMessage model.error
            ]
        , button
            [ onClick SubmitClicked
            , intent Primary
            , disabled (model.submitting || String.length model.codeDraft < 6)
            ]
            [ text (if model.submitting then "Verifying…" else "Verify") ]
        ]

screen : Screen PathArgs Model Msg
screen =
    Screen.with
        { path   = "/login/verify/:email"
        , init   = init
        , update = update
        , view   = view
        }

targetFor : { email : String, after : Maybe Navigation.Target } -> Navigation.Target
targetFor args = ...
```

### `HomePage.mar` — protected, uses User injection

```elm
module HomePage exposing (screen)

import Auth
import Effect
import Endpoints
import Screen
import Users exposing (User)
import View exposing (..)

type alias Model =
    { user  : User
    , notes : List Note
    }

type Msg
    = NotesLoaded (Result NetworkError (List Note))
    | LogoutClicked
    | LoggedOut (Result AuthError ())

init : User -> (Model, Effect Never Msg)
init user =
    ( { user = user, notes = [] }
    , Endpoint.call Endpoints.listMyNotes ()
        |> Effect.toMsg NotesLoaded
    )

update : Msg -> Model -> (Model, Effect Never Msg)
update msg model =
    case msg of
        NotesLoaded (Ok notes)  -> ({ model | notes = notes }, Effect.none)
        NotesLoaded (Err _)     -> (model, Effect.none)
        LogoutClicked           -> (model, Auth.logout |> Effect.toMsg LoggedOut)
        LoggedOut _             -> (model, Navigation.push Navigation.home)

view : Model -> View Msg
view model =
    section []
        [ row [] [ text ("Hi, " ++ model.user.name), button [onClick LogoutClicked] [text "Sign out"] ]
        , list (List.map renderNote model.notes)
        ]

screen : Screen () Model Msg
screen =
    Screen.with { path = "/", init = init, update = update, view = view }
```

That's the entire user-facing surface — three small files for two screens plus a home page. The framework handles the rest.

## 12. Type signature reference

### Module `Auth`

```elm
-- Configuration
type Auth user   -- opaque

config :
    { entity          : Entity user
    , identify        : user -> String
    , email           : { from : String, subject : String }
    , signup          : String -> userExceptId
    , sessionDuration : Duration
    }
    -> Auth user

-- Server: route registration
endpoints : Auth user -> List Route

-- Server: handler-internal helpers
maybeUser : Auth user -> Effect (ResponseError NoTag) (Maybe user)

-- Frontend: top-level wiring
type Frontend user   -- opaque, passed to App.fullstack via `auth = ...`

frontend    : Auth user -> LoginScreen -> Frontend user
loginScreen : Screen (Maybe Navigation.Target) m msg -> LoginScreen

-- Frontend: Effects
type AuthError
    = InvalidCode
    | CodeExpired
    | TooManyAttempts
    | RateLimited { retryAfter : Duration }
    | EmailDeliveryFailed
    | NotAuthenticated
    | Network NetworkError

requestCode : { email : String } -> Effect AuthError ()
verifyCode  : { email : String, code : String } -> Effect AuthError User
logout      : Effect AuthError ()
me          : Effect AuthError (Maybe User)
```

### Module `Routes` (additions to existing)

```elm
public        : List Route -> List Route
authenticated : Auth user -> List Route -> List Route
optional      : Auth user -> List Route -> List Route
requireRole   : Auth user -> (user -> Bool) -> List Route -> List Route
```

### Module `App` (one new field)

```elm
fullstack :
    { api      : { routes : List Route, services : List Service }
    , pages    : List Page
    , auth     : Maybe (Auth.Frontend user)   -- new; Nothing = no auth
    }
    -> Effect Never ()
```

## 13. Comparison with Phoenix

| Concern | Phoenix `mix phx.gen.auth` | Mar `Auth.config` |
|---|---|---|
| Where the code lives | Generated into your project (you own ~2k LOC) | Library; you own ~30 LOC of declaration |
| User entity | Phoenix generates `User` with hashed_password | You define `User`; Mar requires only `id` + `email` |
| Login UI | Generated `.html.heex` templates you can edit | You write the `Screen` from scratch — no default UI shipped |
| Route protection | `pipe_through [:require_authenticated_user]` — pipeline composition | `Routes.authenticated authConfig [...]` — list grouping |
| Session storage | Cookie, signed with secret_key_base; or DB-backed via `Plug.Session` | Cookie holding opaque token; DB row in `_mar_auth_sessions` (always) |
| Token primitives | `Phoenix.Token.sign` / `verify` (HMAC) | HMAC-SHA256 with `auth.sessionSecret` |
| Password reset | Generated flow | N/A (no passwords) |
| Magic link | Add-on (`magic_link` library, `Phoenix.LiveView.assign_user`) | First-class; only auth method in v1 |
| Dev SMTP | `Swoosh.Adapters.Local` (preview at `/dev/mailbox`) | Dev dock "Mail" panel (same idea, in the existing dock) |
| Test ergonomics | Conn helpers (`log_in_user`) | TBD — will land alongside Mar's test harness |

The biggest divergence: Phoenix gives you a working starting point that you customize by editing generated files. Mar gives you a tight type-safe API surface with zero generated code. Phoenix wins on flexibility (tweak any line); Mar wins on cohesion (one config, no maintenance burden when the framework upgrades). For an Elm-style language, the latter fits — Elm itself never went the codegen route.

## 14. Implementation plan (Go side)

For when this is built. The work splits into seven independent tracks; track 1 unblocks the rest.

1. **`internal/auth` package** — pure crypto + storage helpers:
   - `Generate(length int) string` (digits via `crypto/rand`)
   - `Hash(secret, value string) string` (HMAC-SHA256, base64url)
   - `Compare(a, b string) bool` (`subtle.ConstantTimeCompare`)
   - `RandomToken() string` (32 bytes, base64url)
   - `MigrateSchema(*sql.DB) error` for the two `_mar_*` tables.

2. **`internal/runtime` builtins** — register the runtime values:
   - `authConfig` builtin — accepts the record, captures into a `*runtime.AuthRegistration` reference held on the runtime env.
   - `authEndpoints` builtin — returns a `VList` of `VRoute` records pointing at internal handlers.
   - `routesAuthenticated`, `routesPublic`, `routesOptional`, `routesRequireRole` — wrap `Route` values with policy metadata read by the dispatcher.

3. **`internal/jsserve` dispatcher** — when serving an HTTP request:
   - Read `Cookie: mar_session` (or `Authorization: Bearer …` for `X-Mar-Client: ios`).
   - HMAC the token, look up `_mar_auth_sessions`, attach user to request context.
   - For routes inside `authenticated` group: 401 if no user; otherwise call handler with user appended to args.

4. **`internal/jsserve` `/_auth/*` handlers** — request-code, verify-code, me, logout. ~300 lines including rate limiting.

5. **SMTP delivery** — net/smtp wrapper that reads `mar.json mail.*`, sends `text/plain` email. Probably ~80 lines. Dev sink as a flag.

6. **Bootstrap injection** — `program.json` builder reads request session and adds `session: { user }` to payload. ~20 lines change in `internal/scaffold` / `internal/jsserve`.

7. **iOS runtime** — `MarSession.swift` (Keychain helpers ~80 lines), `MarHTTP.swift` modification to add `X-Mar-Client: ios` and `Authorization: Bearer` headers conditionally (~20 lines).

Type-checker work, in `internal/typecheck`:
- Recognize the `Auth a` opaque type and propagate the `a` parameter through `Routes.authenticated` and the handler-injection rule.
- Reserve the `_auth` path prefix (extend the existing `_mar` reservation).
- Validate the `User` record against the `Auth.config`'s row constraints (must contain `id` and `email`; the `signup` callback's return type must match the user record minus `id`).

The init-signature inference for screens is already implemented (per `mar.md` §5.2); the auth track only needs to extend the inference rule to cover `User` and `Maybe User` parameters and the redirect logic.

## 15. Future work

Items deliberately deferred from v1, listed so the v1 surface doesn't paint us into a corner. Each is purely additive — bringing any of them back doesn't change existing user code, just adds an optional field to `Auth.config` (or a new constructor / Effect / endpoint).

### Configuration knobs intentionally fixed today

These were considered for v1 and left out in favor of safe defaults. They're easy to bring back if real apps need them:

- **Custom email body** — additional `body : { code, ttl } -> String` field on the `email` record. Default stays the framework template.
- **Custom cookie name** — `cookieName : String` field. Useful only when running multiple Mar apps under the same parent domain.
- **Code length / TTL** — `codeLength : Int`, `codeTTL : Duration`. 6 digits / 10 minutes are fine for nearly every app.
- **Rate limits** — `requestRateLimit : { perEmail : Rate, perIP : Rate }`. Defaults are 3/hour per email and 20/hour per IP.
- **Max attempts before lockout** — `maxAttempts : Int`. Default 5.
- **`SameSite=Strict`** — for stricter CSRF posture; current default `Lax` works for typical apps.
- **`invitationOnly` policy** — first-class refusal of unknown emails. Today the user achieves this by returning an error from `signup`.
- **Lifecycle hooks** — `onCodeRequested`, `onCodeVerified`, `onLogout` for audit logging. Today the user wraps their own handlers.
- **Test capture sink** — `Auth.captureToList ref` for writing assertions over sent emails. Will land alongside the Mar test harness.
- **Sessions dev panel** — UI in the dev dock for inspecting and force-revoking active sessions.

### New surfaces

- **Password authentication.** A second authentication method via a new `method : Method` field on `Auth.config` (`Auth.emailCode {...}` becomes one variant; `Auth.password { hashAlgorithm = Argon2id, ... }` becomes another). Adds `/_auth/sign-in-password` endpoint.
- **OAuth / SSO.** A third method family (`Auth.oauth { provider = Google, clientId = ..., redirectUri = ... }`). Adds `/_auth/oauth/<provider>/start` and `/_auth/oauth/<provider>/callback`.
- **Multi-factor.** Once a second method exists, `Auth.config` gains `factors = [Auth.emailCode {...}, Auth.totp {...}]`. Verify endpoint takes a step parameter.
- **Account linking.** Resolving the same human across multiple identification methods.
- **Session list / revocation UI.** Endpoint + helper to render and revoke `auth_sessions` rows for the current user.
- **Email change flow.** A confirm-the-new-email-then-update pattern.
- **Audit log.** A built-in `_mar_auth_log` table opt-in via `Auth.config { ..., audit = True }`.
- **Granular permissions.** Going from predicate-based `requireRole` to a capability/policy DSL.
- **Refresh tokens.** Currently sessions live for `sessionDuration` and then hard-expire. A refresh model with shorter access tokens + longer refresh tokens is doable but adds complexity v1 doesn't need.
- **Time-based code (TOTP) as a primary factor.** Same user-side shape as email code; just a different transport.

### What makes additions non-breaking

- `Auth user` is opaque; new fields can be added without changing user code that doesn't reference them.
- `Routes.*` policy wrappers are stable.
- The frontend Effects (`requestCode`, `verifyCode`, `logout`, `me`) are the universal interface; password / OAuth flows would add NEW Effects (`Auth.signInWithPassword`, `Auth.signInWithGoogle`) without changing these.
