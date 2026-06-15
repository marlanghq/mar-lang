# Mar

Mar is a full-stack functional language for building typed web and mobile apps with a single source of truth. Backend and frontend are written in the same language, share types end-to-end, and compile together.

The syntax is Elm-style. The semantics are pure functional with effects tracked in types. Validation, authentication, and data flow are all checked at compile time.

## 1. Overview

### 1.1 Philosophy

- **Pure by default.** Side effects are values (`Effect a`) that the runtime executes. User code describes; runtime acts.
- **Compile-time correctness over runtime checks.** Types catch as much as possible.
- **No hidden magic in user code.** Magic is allowed only at the boundary (HTTP encode/decode, schema migrations, etc.), never in the middle of business logic.
- **High level by default.** Low-level escape hatches are added only when proven necessary.
- **Single source of truth.** A change in the backend is immediately visible to the frontend; no codegen step.

### 1.2 What Mar is

- A language for typed CRUD apps, internal tools, dashboards, and small/medium SaaS.
- Backend (HTTP server + database) and frontend (web + iOS) in one codebase.
- Statically typed with Hindley-Milner inference, row polymorphism, and exhaustive pattern matching.

### 1.3 What Mar is not

- Not a systems language (no manual memory management, no FFI to arbitrary C).
- Not a general-purpose UI framework (the view vocabulary is opinionated, not free-form).
- Not a runtime-dynamic language (everything checked at compile time).

## 2. Project Structure

### 2.1 Layout

```
project/
  mar.json              -- manifest + config
  Main.mar              -- entry point: App.fullstack { services, pages, api }
  Shared.mar            -- types + Service contracts shared by both halves
  Backend/
    Users.mar           -- entities + service handlers
    Tasks.mar
  Frontend/
    Routes.mar          -- typed paths
    SignIn.mar          -- one MVU page per file
    Home.mar
```

A module's path mirrors its name: `Frontend/Home.mar` is `module Frontend.Home`.

### 2.2 mar.json

The project manifest. Pure JSON, no interpolation. Strict schema (unknown fields are compile errors).

```json
{
  "name": "my-app",
  "entry": "Main.mar",

  "server": {
    "port": 3000,
    "host": "0.0.0.0",
    "publicUrl": "https://my-app.example.com"
  },

  "database": {
    "path": "./data/app.db",
    "journalMode": "wal",
    "synchronous": "normal",
    "foreignKeys": true
  },

  "auth": {
    "sessionTtlHours": 720,
    "codeTtlMinutes": 10
  },

  "mail": {
    "from": "noreply@example.com",
    "smtpHost": "smtp.example.com",
    "smtpPort": 587,
    "smtpUsername": "app",
    "smtpPassword": "env:SMTP_PASSWORD"
  },

  "ios": {
    "bundleIdentifier": "com.example.myapp",
    "displayName": "My App",
    "serverUrl": "https://my-app.example.com"
  }
}
```

#### Environment variables

Any string field can reference an env var with the `env:` prefix:

```json
"smtpPassword": "env:SMTP_PASSWORD"
"port": "env:PORT"
```

The runtime reads the env var at startup. If missing, the server fails to start with a clear error.

#### Secrets

Some fields are marked as **secret** in Mar's internal schema (e.g., `mail.smtpPassword`). These **cannot** be literal values in `mar.json`, they must use the `env:` form. Compile error otherwise.

### 2.3 Module system

Standard Elm conventions:

```elm
module Posts exposing (Post, PostId, posts, routes)

import Entity exposing (..)
import Default exposing (..)
import Db
```

Rules:

- **No cycles.** If `A` imports `B`, `B` cannot import `A`.
- **Path = module name.** `src/Posts/Entity.mar` is `module Posts.Entity`.
- **Private by default.** Only what's in `exposing (...)` is visible.
- **`exposing (..)`** is recommended for builder DSLs (`Entity`, `Default`, `Routes`); qualified imports are recommended for `Db` and similar.

### 2.4 Migrations

Auto-derived from entity declarations on server startup. No hand-written migration files.

- A `mar_schema_migrations` table tracks what's been applied.
- Non-destructive changes (add column, add index, create table) apply automatically.
- Destructive changes (drop column, change type, foreign key on existing table) **block startup** with a clear error and hint.
- Startup logs:
  - Idle: `[mar] Database: ./data/app.db (12 tables, schema up to date)`
  - Applied: lists each operation with timing
  - Warning: lists extra columns or other notices
  - Error: refuses to start, suggests manual SQL

### 2.5 Static assets (`public/`)

Files in a project's `public/` folder are served at the site root and
travel with the build:

- `mar dev` serves them live (`public/logo.png` → `/logo.png`, subfolders
  preserved).
- `mar build` copies the whole tree into `dist/` so they ship with the
  deployed bundle. Dotfiles (`.DS_Store`, `.env`, …) are skipped.
- Reference them by absolute path, e.g. `image [] { src = "/logo.png", alt = "…" }`.

The asset is fetched over HTTP like any other resource, the same on web
and on iOS/Android (`AsyncImage` fetches from the app's server). It is
**not** inlined into the page or bundled into the native app binary, so a
reachable host must serve `public/` at runtime.

Reserved: `mar build` refuses a `public/` path that collides with a
generated file (`index.html`, `runtime.js`, `program.json`, `_headers`)
or a runtime route prefix (`_mar/`, `_auth/`, `api/`, `services/`).

### 2.6 PWA (installable web app)

Every `App.frontend` app is an installable PWA out of the box, `mar dev`
serves a Web App Manifest + icons and `mar build` writes them into
`dist/`, so "Add to Home Screen" produces a real app icon that opens
fullscreen on iOS, Android, and desktop. No per-app boilerplate.

Customize it with an optional `pwa` block in `mar.json` (every field
optional; the mandatory manifest `name` comes from the top-level
`name`):

```json
{
  "name": "Daily Checklist",
  "pwa": {
    "shortName": "Checklist",
    "icon": "./icon.png",
    "themeColor": "#0071e3",
    "backgroundColor": "#ffffff"
  }
}
```

- **shortName**: home-screen label (default: `name`).
- **icon**: project-relative master PNG. **Must be a square PNG, at
  least 512×512** (`mar dev` / `mar build` fail fast otherwise); Mar
  downscales it to every needed size. Default: a generated solid-color
  tile, so a valid icon always exists.
- **themeColor / backgroundColor**: hex, default `#ffffff`.

Generated endpoints (served in dev, written to `dist/` by build):
`/_mar/manifest.json`, `/_mar/icon-180.png`, `/_mar/icon-192.png`,
`/_mar/icon-512.png`.

## 3. Basic Types

### 3.1 Syntax

Standard Elm syntax:

- `type alias Foo = { ... }` for records
- `type Bar = A | B Int | C String String` for sum types
- `\x -> body` for lambdas
- `case expr of ...` for pattern matching
- `let x = ... in body` for local bindings
- `let x <- effect in body` for effect chaining (sugared `andThen`)
- `|>` for pipelines

### 3.2 Nominal IDs

Every entity ID is a nominal wrap of a primitive:

```elm
type UserId = UserId Int
type PostId = PostId Int
type SlugId = SlugId String
```

This prevents mixing IDs of different entities at compile time. Mar's auto-derived codecs encode them transparently (the wrapper disappears on the wire).

### 3.3 Effect a

The single type for effectful computations, Mar's `Cmd`:

```elm
type Effect a
```

Read as: "a computation that, when executed, eventually produces an `a`." One
type parameter, like Elm's `Cmd`. Errors are values, never a type index: a
failure travels inside the `a` (a `Result`), not in the effect type. A
`Service.call` delivers `Result Service.Error resp`, where `Service.Error` is a
union (`Offline` / `Unauthorized` / `ServerError String`) the frontend cases on.

Used for backend handlers, database operations, network calls, and frontend
commands. The runtime executes; user code only describes.

API:

```elm
Effect.succeed   : a -> Effect a
Effect.fail      : String -> Effect a   -- abort a backend handler with a message
Effect.none      : Effect a
Effect.map       : (a -> b) -> Effect a -> Effect b
Effect.andThen   : (a -> Effect b) -> Effect a -> Effect b
Effect.batch     : List (Effect a) -> Effect a
Effect.forEach   : (a -> Effect ()) -> List a -> Effect ()
Effect.sequence  : List (Effect a) -> Effect (List a)
```

A page's `init` and `update` return `(Model, Effect Msg)`. `Effect.fail` is the
backend abort channel: its String becomes the `Err` the frontend receives, so
reserve it for genuine failures and keep matchable domain errors in the
service's response value (a typed union) instead.

### 3.4 Effect chaining: `let <-`

Sugar for `andThen`. Each `<-` binds the result of one effect before the next runs:

```elm
toggle id =
    let
        found <- Repo.findById tasks id
    in
    case found of
        Just task -> Repo.update tasks id { done = not task.done }
        Nothing   -> Effect.succeed Nothing
```

Equivalent to `Repo.findById tasks id |> Effect.andThen (\found -> ...)`.

### 3.5 Error handling

Three kinds of failure, three homes. The rule of thumb: transport is a
shared union, domain is a per-endpoint outcome in the response value, and
`Effect.fail` is the abort channel for broken invariants only.

**Transport** (offline, expired session, server failure): every call can hit
these, so every `Service.call` delivers the same union in its `Err`:

```elm
type Service.Error
    = Offline              -- request never reached the server
    | Unauthorized         -- session gone (401)
    | ServerError String   -- the server refused; carries its message
```

Match it qualified, or fold it for display:

```elm
Fetched (Err Service.Offline) -> -- show a retry
Fetched (Err why)             -> -- Service.errorToString why
```

**Domain** (email taken, wrong code, body too long): specific to one
endpoint, so it lives in that endpoint's response type as a union of the
outcomes it can actually produce. Never a shared catch-all: a page should
only have to match what can happen to it.

```elm
type SignupOutcome = Created User | EmailTaken | TeamFull

signup : Service NewUser SignupOutcome
```

The handler is `NewUser -> Effect SignupOutcome`, so the backend can only
produce declared outcomes, and the frontend's case is checked for
exhaustiveness. Patterns nest flat:

```elm
Done (Ok (Created user)) -> ...
Done (Ok EmailTaken)     -> ...
Done (Ok TeamFull)       -> ...
Done (Err why)           -> ...
```

The auth endpoints follow the same shape with framework-provided outcomes:
`Auth.requestCode` delivers `Auth.RequestOutcome` (`Auth.CodeSent` /
`Auth.InvalidEmail` / `Auth.RateLimited`) and `Auth.verifyCode` delivers
`Auth.VerifyOutcome user` (`Auth.SignedIn user` / `Auth.WrongCode` /
`Auth.TooManyAttempts`).

**Abort** (`Effect.fail "..."`): for broken invariants in backend handlers,
not for outcomes the frontend reacts to. The string surfaces to the client
as `ServerError`, display-only. If a page needs to branch on a case, the
case belongs in the response type.

Error copy belongs to the view: the wire carries data (constructors), and
each frontend chooses its own words for each case.

## 4. Backend

### 4.1 Entity API

An entity is a database-backed record. It carries schema only, no API and no business logic. `Entity.define` takes the table name, a `columns` record whose field names and types mirror the record, and a `uniques` list:

```elm
type alias Task =
    { id        : Int
    , name      : String
    , done      : Bool
    , createdAt : Time
    , userId    : Int
    , position  : Int
    }

tasks : Entity Task
tasks =
    Entity.define
        { name = "tasks"
        , columns =
            { id        = Entity.serial
            , name      = Entity.text Entity.notNull
            , done      = Entity.bool Entity.notNull
            , createdAt = Entity.timestamp Entity.notNull
            , userId    = Entity.int Entity.notNull
            , position  = Entity.int Entity.notNull
            }
        , uniques = []
        }
```

#### Column builders

Each column is one of:

- `Entity.serial`: auto-incrementing integer primary key, filled by the runtime on insert.
- `Entity.int Entity.notNull`
- `Entity.text Entity.notNull`
- `Entity.bool Entity.notNull`
- `Entity.timestamp Entity.notNull`: a `Time` column.
- `Entity.enum [Open, InProgress, Done] Entity.notNull`: a tags-only union, stored as a CHECKed text column so only those values can be written.

`Entity.notNull` marks a column as required. Because `Entity.serial` is filled by the runtime, `Repo.create` takes the record without that field.

#### Unique indexes

`uniques` is a list of column-name groups; each inner list is one unique index, composite when it names more than one column:

```elm
, uniques = [["commentId", "userId", "emoji"]]   -- one reaction per user per comment
```

Computed defaults (a slug from a title, a creation time) belong in the handler, not the entity: read `Time.now` and pass the value to `Repo.create`.

### 4.2 Codecs

100% derived from types. **No codec API exposed to user code.**

- `type alias` records → JSON object with same field names (camelCase).
- `type X = X Inner` (single-constructor wrap) → encoded as `Inner` (transparent).
- `type Status = Active | Inactive` (tags only) → JSON string with lowercase camelCase.
- Sum types with payload → `{ "tag": "constructorName", ...payload }`.
- `Maybe a` → value or `null`. Optional fields on decode.
- `List a` → JSON array.
- Primitives: `Int`, `Float`, `String`, `Bool`, `Char`, `Time` (ISO 8601 UTC), `()`.

Mar uses these codecs at the HTTP boundary (request body, response body, path params, query params) and at the DB boundary. User code never sees JSON or constructs codecs manually.

For external APIs (Stripe, etc.), an explicit `Codec` module may be added later.

### 4.3 Data access (Repo)

`Repo.*` reads and writes entity rows. Every operation runs inside a backend handler and returns an `Effect`:

```elm
Repo.all        : Entity a -> Effect (List a)
Repo.findById   : Entity a -> Int -> Effect (Maybe a)
Repo.findBy     : Entity a -> fields -> Effect (List a)
Repo.create     : Entity a -> fields -> Effect a
Repo.update     : Entity a -> Int -> fields -> Effect (Maybe a)
Repo.deleteById : Entity a -> Int -> Effect ()
```

`findBy` filters by example: pass a record of the columns to match, and it returns every row whose values equal them. `create` takes the full row minus the `serial` id. `update` takes the id and a record of just the columns to change, and answers `Nothing` when no row has that id. `deleteById` is idempotent.

```elm
listTasksImpl : () -> Shared.User -> Effect (List Shared.Task)
listTasksImpl _ user =
    Repo.findBy tasks { userId = user.id }
        |> Effect.map sortByPosition
```

There is no query-builder, predicate, pagination, or relation API today. Compose with `Effect.andThen` and ordinary Mar (`List.filter`, `List.sortWith`, `List.map`) over the rows you read. Raw SQL is not exposed to app code.

### 4.4 Services

A `Service req resp` is a typed contract for one server call: `req` is what the client sends, `resp` what it gets back. The same value is shared by both halves, declared once in the shared module:

```elm
listTasks : Service () (List Task)
listTasks = Service.declare

addTask : Service { name : String } AddTaskOutcome
addTask = Service.declare
```

`Service.declare` is the same placeholder for every contract; the type annotation fixes `req` and `resp`. The backend pairs each contract with a handler, the frontend calls it:

```elm
Service.declare       : Service req resp
Service.implement     : Service req resp -> (req -> Effect resp) -> ExposedService
Service.call          : Service req resp -> req -> (Result Service.Error resp -> msg) -> Effect msg
Service.errorToString : Service.Error -> String
```

A handler is `req -> Effect resp`, so it can only produce the declared response. Most calls should be authenticated, which is what `Auth.protect` is for:

```elm
Auth.protect : Service req resp -> (req -> User -> Effect resp) -> ExposedService
```

`Auth.protect` injects the signed-in `User` as the second argument and rejects the request with 401, before the handler runs, when there is no valid session. The frontend sees the same `Service` value either way and never knows whether a handler was wrapped.

```elm
-- Backend.Tasks
addTaskImpl : { name : String } -> Shared.User -> Effect Shared.AddTaskOutcome
addTaskImpl input user =
    if String.trim input.name == "" then
        Effect.succeed Shared.NameEmpty
    else
        Repo.create tasks { name = input.name, done = False, {- ... -} }
            |> Effect.map Shared.Added

services =
    [ Auth.protect Shared.listTasks listTasksImpl
    , Auth.protect Shared.addTask   addTaskImpl
    ]
```

On the frontend, `Service.call` turns a contract into an `Effect` that dispatches a `Msg`. The `Result` carries `Service.Error` in its `Err` (transport failure) and the declared `resp` in `Ok` (which holds the domain outcome). See section 3.5 for the full error model.

```elm
update msg model =
    case msg of
        AddClicked ->
            ( model, Service.call Shared.addTask { name = model.draft } Added )

        Added (Ok (Shared.Added task)) -> -- ...
        Added (Ok Shared.NameEmpty)    -> -- ...
        Added (Err why)                -> -- Service.errorToString why
```

### 4.5 Wiring (`App.fullstack`)

`Main.mar` is the only module that sees both halves. It builds the auth config, lists the services and pages, and hands them to `App.fullstack`:

```elm
main : Effect ()
main =
    App.fullstack
        { services = Backend.Tasks.services
        , pages    =
            [ Frontend.SignIn.page
            , Frontend.Home.page
            ]
        , api      = []
        }
```

- `services` is the concatenation of each backend module's exposed services.
- `pages` enumerates every frontend route; the runtime dispatches by `path`.
- `api` holds custom REST endpoints (`Endpoint.*`) for webhooks or non-Mar clients. It is empty for almost every app: services cover normal client-server calls.

### 4.6 Auth

Mar ships passwordless auth: the user enters an email, receives a one-time code, and exchanges it for a session. The app brings its own `User` entity and registers it through `Auth.config`:

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

Protect a service with `Auth.protect` (section 4.4) and a page with `Page.protected` (section 5). The sign-in flow runs through `Auth.requestCode` and `Auth.verifyCode`, which deliver the per-endpoint outcomes `Auth.RequestOutcome` and `Auth.VerifyOutcome user` (section 3.5). The full flow, every config field, and the SMTP and dev-code setup live in [auth.md](auth.md).

### 4.7 Errors

Backend handlers return `Effect resp`; there is no error type parameter. The three kinds of failure, and where each one lives, are covered in full in section 3.5. In short:

- A handler returns its declared response. Domain outcomes are constructors of that response type, matched by the frontend.
- `Effect.fail "message"` aborts the handler; the message reaches the client as `Service.ServerError` and is display-only.
- Offline, expired-session (401), and server failures are turned into the `Service.Error` union by the runtime and delivered in the call's `Err`.

## 5. Frontend

### 5.1 MVU model

Each page is its own independent MVU loop with `Model`, `Msg`, `init`, `update`, and `view`. `init` and `update` return `(Model, Effect Msg)`:

```elm
init   : (Model, Effect Msg)
update : Msg -> Model -> (Model, Effect Msg)
view   : Model -> View Msg
```

The runtime instantiates a page on navigation and swaps it out when the user navigates away.

### 5.2 Page value

Each page module exports a `page`, built with one of the `Page.*` combinators. They all take the same record, `{ path, title, init, update, view }`; the combinator decides what `init` / `update` / `view` receive:

```elm
Page.create           -- public, static path; init : (Model, Effect Msg)
Page.protected        -- runs Auth.me on entry, hands the User in; init : User -> (Model, Effect Msg)
Page.dynamic          -- path carries typed args; init : args -> (Model, Effect Msg)
Page.dynamicProtected -- both; init : User -> args -> (Model, Effect Msg)
```

```elm
page : Page
page =
    Page.protected
        { path   = "/"
        , title  = "Team Notes"
        , init   = init
        , update = update
        , view   = view
        }
```

`Page.protected` bootstraps the session: it runs `Auth.me`, redirects to the sign-in page when there is no valid session, and otherwise passes the `User` to `init`. (`Page.adminProtected` and `Page.dynamicAdminProtected` gate on the admin session instead.)

A dynamic page's `path` is a typed route: a path string with `{name:Type}` placeholders, kept in a `Routes` module so links and pages agree on the shape:

```elm
-- Frontend/Routes.mar
home       = "/"
verifyCode = "/sign-in/verify/{email:String}"
```

The placeholder values are parsed and delivered to `init` as a record (`{ email : String }`).

### 5.3 View vocabulary

Views are abstract: no HTML, CSS, or SwiftUI in user code. Mar renders natively per platform (HTML/CSS for web, SwiftUI for iOS). Every element takes a list of attributes as its first argument, even when empty (`text []`, `section []`), so adding an attribute never changes the call shape.

```elm
navigationStack [ navigationTitle "Sign in" ]
    [ form
        [ section []
            [ text [] "Enter your email and we'll send a code."
            , textField [ email, submit Submitted ] "Email" draft DraftChanged
            ]
        , section []
            [ button [] Submitted "Send me a code" ]
        ]
    ]
```

The building blocks: `navigationStack` / `navigationTitle`, `form` / `section`, `row` / `column` / `spacer`, `text` / `title` / `subtitle` / `paragraph`, `textField`, `button`, `link` / `navigationLink`, `list` / `keyedList`, `toggle`, `image`, `sheet` / `confirm`, `errorText`, `centered`, and `empty` (renders nothing). `button [] Submitted "Verify"` takes its message and label directly; `textField` takes its label, the current value, and an on-change message.

### 5.4 Navigation

The `Nav` module drives navigation. In a view, `navigationLink` pushes a destination when tapped; in `update`, `Nav.pushTo` / `Nav.replaceTo` (and `Nav.push` / `Nav.replace`) return an `Effect` that navigates:

```elm
update msg model =
    case msg of
        Saved (Ok note) ->
            ( model, Nav.replaceTo (Frontend.Routes.noteDetail note.id) )
```

`replace` swaps the current entry, so Back does not return to it; `push` adds one. After a successful sign-in, `Auth.completeSignIn` returns the user to wherever a 401 sent them (or home).

## 6. Client and server

The same `Service` value (section 4.4) is the contract for both halves: declared once in the shared module, implemented on the backend (`Service.implement`, or `Auth.protect` for an authenticated call), and invoked from a page with `Service.call`. Renaming a service or changing its `req` / `resp` breaks both sides at compile time, with no code-generation step.

```elm
-- Shared (declared once):
addTask : Service { name : String } AddTaskOutcome
addTask = Service.declare

-- Backend (Backend.Tasks.services):
Auth.protect Shared.addTask addTaskImpl

-- Frontend (in update):
update msg model =
    case msg of
        SubmitClicked ->
            ( { model | submitting = True }
            , Service.call Shared.addTask { name = model.draft } Added
            )

        Added (Ok (Shared.Added task)) -> -- ...
        Added (Ok Shared.NameEmpty)    -> -- ...
        Added (Err why)                -> -- Service.errorToString why
```

The `Result` the page receives carries `Service.Error` in its `Err` (transport failure) and the declared response in `Ok`, which holds any domain outcome. See section 3.5 for the full error model.

## 7. Main.mar

Entry point. A single `App.fullstack { ... }` call, returning `Effect ()`:

```elm
module Main exposing (main)

import Backend.Tasks
import Backend.Users
import Frontend.SignIn
import Frontend.VerifyCode
import Frontend.Home


main : Effect ()
main =
    App.fullstack
        { services = Backend.Tasks.services
        , pages    =
            [ Frontend.SignIn.page
            , Frontend.VerifyCode.page
            , Frontend.Home.page
            ]
        , api      = []
        }
```

`services`, `pages`, and `api` are explicit lists; Mar does not auto-discover (see section 4.5). Auth is configured by a top-level `auth = Auth.config { ... }` binding that the runtime picks up (section 4.6).

## 8. Deferred / Future Work

The following are intentionally not in the MVP. They will be revisited when concrete need arises.

- **Subscriptions** (timer, websocket, server-sent events), Elm-style `Sub Msg`.
- **Theme customization** via a `mar.json` `theme` field for colors and typography.
- **Escape hatches for platform-specific behavior**: haptics, push notifications, file downloads.
- **Custom HTTP clients** for external APIs (Stripe, etc.) via an explicit `Codec` API.
- **Crud scaffold helpers** (`Crud.scaffold entity`) if examples become repetitive.
- **Ownership helpers** for the read-then-check pattern, if it recurs.
- **State preservation across navigation**: pages are currently rebuilt on navigate-away.
- **Loading-state abstraction**: each page currently handles its own loading state.
- **Multiple environments in `mar.json`**: currently a single config, env vars handle differences.

## 9. Examples

See `examples/`:

- `hello-auth/` is the smallest end-to-end app: email plus one-time-code sign-in and a single protected page.
- `daily-checklist/` is a per-user CRUD app: entities, `Repo`, `Service` contracts, `Auth.protect`, drag-to-reorder, and a typed domain outcome (`AddTaskOutcome`).
- `team-notes/` adds multiple pages, dynamic routes (`Page.dynamic`), and a detail page.
- `mini-twitter/` is the full-featured one: three entities (User / Tweet / Follow), passwordless email auth, handle-based profiles, a follow graph, typed routes, and an MVU page per route. See `examples/mini-twitter/README.md` for a reading order.
