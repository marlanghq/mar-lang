# Mar

Mar is a full-stack functional language for building typed web and mobile apps with a single source of truth. Backend and frontend are written in the same language, share types end-to-end, and compile together.

The syntax is Elm-style. The semantics are pure functional with effects tracked in types. Validation, authentication, and data flow are all checked at compile time.

## 1. Overview

### 1.1 Philosophy

- **Pure by default.** Side effects are values (`Effect e a`) that the runtime executes. User code describes; runtime acts.
- **Compile-time correctness over runtime checks.** Types catch as much as possible.
- **No hidden magic in user code.** Magic is allowed only at the boundary (HTTP encode/decode, schema migrations, etc.) — never in the middle of business logic.
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
  src/
    Main.mar            -- entry point: App.application { routes, screens }
    Posts.mar           -- backend module (entities + endpoints + handlers)
    Comments.mar
    Screens/
      Home.mar          -- frontend screens (one per file)
      Timeline.mar
      ProfileDetail.mar
```

A module's path mirrors its name: `src/Screens/Home.mar` is `module Screens.Home`.

### 2.2 mar.json

The project manifest. Pure JSON, no interpolation. Strict schema (unknown fields are compile errors).

```json
{
  "name": "my-app",
  "entry": "src/Main.mar",

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

Some fields are marked as **secret** in Mar's internal schema (e.g., `mail.smtpPassword`). These **cannot** be literal values in `mar.json` — they must use the `env:` form. Compile error otherwise.

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

### 3.3 Effect e a

The single type for effectful computations:

```elm
type Effect e a
```

Read as: "a computation that, when executed, produces either an `e` (failure) or an `a` (success)."

Used for backend handlers, database operations, network calls, and frontend commands. The runtime executes; user code only describes.

API:

```elm
Effect.succeed   : a -> Effect e a
Effect.fail      : e -> Effect e a
Effect.none      : Effect Never msg
Effect.map       : (a -> b) -> Effect e a -> Effect e b
Effect.andThen   : (a -> Effect e b) -> Effect e a -> Effect e b
Effect.mapError  : (e -> e2) -> Effect e a -> Effect e2 a
Effect.batch     : List (Effect Never msg) -> Effect Never msg
Effect.toMsg     : (Result e a -> msg) -> Effect e a -> Effect Never msg
```

`Effect Never msg` is "an effect that cannot fail and produces a Msg" — used in MVU `init` and `update` returns.

### 3.4 Effect chaining: `let <-`

Sugar for `andThen`:

```elm
deletePost id user =
    let
        post <- Db.findOne posts id
    in
    if post.author == user.id then
        Db.delete posts id
    else
        Effect.fail (Forbidden "not your post")
```

Equivalent to `Db.findOne posts id |> Effect.andThen (\post -> ...)`.

## 4. Backend

### 4.1 Entity API

An entity is a database-backed record. It carries schema only — no API, no business logic.

```elm
type alias Post =
    { id : PostId
    , author : UserId
    , body : String
    , createdAt : DateTime
    , updatedAt : DateTime
    }

posts : Entity Post
posts =
    entity Post
        |> primaryKey .id
        |> foreignKey .author users
        |> default .id Autoincrement
        |> default .author CurrentUserRef
        |> default .createdAt NowTimestamp
        |> default .updatedAt AutoUpdateTimestamp
```

#### Builder API

```elm
entity        : (a -> a) -> Entity a
primaryKey    : (a -> id) -> Entity a -> Entity a
foreignKey    : (a -> fk) -> Entity b -> Entity a -> Entity a
nullable      : List (a -> field) -> Entity a -> Entity a   -- override (default is NOT NULL except Maybe)
unique        : List (a -> field) -> Entity a -> Entity a
uniqueGroup   : List (a -> field) -> Entity a -> Entity a   -- composite unique
indexed       : (a -> field) -> Entity a -> Entity a
indexedGroup  : List (a -> field) -> Entity a -> Entity a
default       : (a -> field) -> DefaultValue field -> Entity a -> Entity a
softDelete    : (a -> Maybe DateTime) -> Entity a -> Entity a
onDelete      : (a -> fk) -> CascadeAction -> Entity a -> Entity a
```

#### Default value tags

Pure tags only. No user code in defaults.

```elm
type DefaultValue a
    = StaticDefault a       -- literal value
    | Autoincrement         -- Int / wrapped Int
    | NowTimestamp          -- DateTime, set on insert only
    | AutoUpdateTimestamp   -- DateTime, set on insert AND every update
    | CurrentUserRef        -- UserId / wrapped, takes session user
    | RandomUuid            -- UUID-like

type CascadeAction = Cascade | SetNull | Restrict
```

Computed defaults (e.g., a slug from the title) belong in the handler, not the entity.

### 4.2 Codecs

100% derived from types. **No codec API exposed to user code.**

- `type alias` records → JSON object with same field names (camelCase).
- `type X = X Inner` (single-constructor wrap) → encoded as `Inner` (transparent).
- `type Status = Active | Inactive` (tags only) → JSON string with lowercase camelCase.
- Sum types with payload → `{ "tag": "constructorName", ...payload }`.
- `Maybe a` → value or `null`. Optional fields on decode.
- `List a` → JSON array.
- Primitives: `Int`, `Float`, `String`, `Bool`, `Date` (ISO 8601), `DateTime` (ISO 8601 UTC), `()`.

Mar uses these codecs at the HTTP boundary (request body, response body, path params, query params) and at the DB boundary. User code never sees JSON or constructs codecs manually.

For external APIs (Stripe, etc.), an explicit `Codec` module may be added later.

### 4.3 Effects and effect contexts

`Db.*` operations and `Endpoint.call` return `Effect (ResponseError tag) a`. They cannot be called outside a handler context — the compiler enforces this.

### 4.4 Queries

Built as values, executed at the boundary.

#### Construction

```elm
Db.from    : Entity a -> Query a
Db.where   : Predicate a -> Query a -> Query a
Db.orderBy : (a -> field) -> Direction -> Query a -> Query a
Db.limit   : Int -> Query a -> Query a

type Direction = Asc | Desc
```

#### Predicates (named combinators, no lambdas)

```elm
Db.eq, Db.neq, Db.lt, Db.gt, Db.lte, Db.gte
    : (a -> field) -> field -> Predicate a

Db.between : (a -> field) -> field -> field -> Predicate a
Db.in_     : (a -> field) -> List field -> Predicate a

Db.and  : List (Predicate a) -> Predicate a
Db.or   : List (Predicate a) -> Predicate a
Db.not_ : Predicate a -> Predicate a

Db.contains   : (a -> String) -> String -> Predicate a
Db.startsWith : (a -> String) -> String -> Predicate a
Db.endsWith   : (a -> String) -> String -> Predicate a

Db.isJust    : (a -> Maybe x) -> Predicate a
Db.isNothing : (a -> Maybe x) -> Predicate a
```

#### Extraction

```elm
Db.list   : Query a -> Effect (ResponseError tag) (List a)
Db.first  : Query a -> Effect (ResponseError tag) (Maybe a)
Db.count  : Query a -> Effect (ResponseError tag) Int
Db.exists : Query a -> Effect (ResponseError tag) Bool
```

#### Pagination (cursor-based)

```elm
type Cursor   -- opaque, signed by the server

type alias Page a =
    { items : List a
    , nextCursor : Maybe Cursor   -- Nothing = end
    }

Db.paginate
    : { query : Query a, limit : Int, cursor : Maybe Cursor }
    -> Effect (ResponseError tag) (Page a)
```

The query must have `orderBy`. Mar adds the primary key as automatic tiebreaker for stable cursors.

#### Single-row operations by ID

```elm
Db.findOne : Entity a -> id -> Effect (ResponseError tag) a
Db.insert  : Entity a -> a  -> Effect (ResponseError tag) a
Db.update  : Entity a -> id -> List (Setter a) -> Effect (ResponseError tag) a
Db.delete  : Entity a -> id -> Effect (ResponseError tag) ()

Db.set : (a -> field) -> field -> Setter a
```

`Db.update` returns the updated row.

#### Bulk operations

```elm
Db.deleteWhere : Entity a -> Predicate a -> Effect (ResponseError tag) Int
Db.updateWhere : Entity a -> Predicate a -> List (Setter a) -> Effect (ResponseError tag) Int
```

Both return the count of affected rows.

#### Relations (preload, no joins)

To avoid N+1 with related data, use `preload` (one-to-one or many-to-one) or `preloadMany` (one-to-many):

```elm
Db.preload
    : Entity related
    -> (a -> id)
    -> List a
    -> Effect (ResponseError tag) (a -> related)

Db.preloadMany
    : Entity related
    -> (related -> id)   -- FK accessor on related
    -> List a
    -> (a -> id)         -- ID accessor on parent
    -> Effect (ResponseError tag) (a -> List related)
```

Returns a function (a -> related) that the user applies. One query per `preload` call, regardless of list size.

```elm
let
    posts <- Db.list (Db.from posts |> Db.orderBy .createdAt Desc)
    getAuthor <- Db.preload users .author posts
    getTags <- Db.preloadMany tags .postId posts .id
in
Effect.succeed
    (List.map (\p ->
        { post = p
        , author = getAuthor p
        , tags = getTags p
        }
    ) posts)
```

### 4.5 Endpoints

An `Endpoint i o tag` is a typed contract for an HTTP route, used by both backend (`Endpoint.implement`) and frontend (`Endpoint.call`).

```elm
type Endpoint pathArgs input output errorTag
```

#### Aliases per HTTP verb

```elm
type alias Get path output         = Endpoint path () output ()
type alias Post input output tag   = Endpoint () input output tag
type alias Patch path input output tag = Endpoint path input output tag
type alias Delete path             = Endpoint path () () ()
```

GET and DELETE have no body and no validation, so their aliases drop those slots. POST and PATCH commonly have validation, so they keep the tag slot.

#### Constructors

```elm
Endpoint.get    : String -> Endpoint path () output ()
Endpoint.post   : String -> Endpoint () input output ()
Endpoint.patch  : String -> Endpoint path input output ()
Endpoint.delete : String -> Endpoint path () () ()
```

#### Modifiers

```elm
Endpoint.requireAuth : Entity { a | id : UserId, email : String } -> Endpoint p i o tag -> Endpoint p i o tag
Endpoint.public      : Endpoint p i o tag -> Endpoint p i o tag
Endpoint.requireRole : Entity { a | role : role, ... } -> role -> Endpoint p i o tag -> Endpoint p i o tag

Endpoint.validate
    : (input -> Result (List (FieldError tag)) input)
    -> Endpoint p i o ()
    -> Endpoint p i o tag

Endpoint.validateWithUser
    : (User -> input -> Result (List (FieldError tag)) input)
    -> Endpoint p i o ()
    -> Endpoint p i o tag
```

#### Implementation (backend) and call (client)

```elm
Endpoint.implement : (handlerSignature) -> Endpoint p i o tag -> Route
Endpoint.call      : Endpoint p i o tag -> p -> i -> Effect (ResponseError tag) o
```

The handler signature mirrors the Endpoint shape:
- Path params first, then body, then runtime-injected (`User`).

Example:

```elm
showPost : PostId -> Effect (ResponseError ()) Post
showPost id = Db.findOne posts id

createPost : PostInput -> User -> Effect (ResponseError PostField) Post
createPost input user = Db.insert posts { input | author = user.id }

updatePost : PostId -> PostInput -> User -> Effect (ResponseError PostField) Post
updatePost id input user = ...
```

### 4.6 Validation

Field tag types per entity, listing only fields with validation:

```elm
type PostField = Body

validatePostInput : PostInput -> Result (List (FieldError PostField)) PostInput
validatePostInput input =
    if String.length input.body >= 1 then Ok input
    else Err [ FieldError.new Body "post body cannot be empty" ]
```

For validation that needs user context:

```elm
validateFollowInput : User -> FollowInput -> Result (List (FieldError FollowField)) FollowInput
validateFollowInput user input =
    if user.id /= input.followed then Ok input
    else Err [ FieldError.new Followed "you cannot follow yourself" ]
```

#### FieldError API

```elm
type alias FieldError tag =
    { field : tag
    , message : String
    }

FieldError.new       : tag -> String -> FieldError tag
FieldError.firstFor  : tag -> List (FieldError tag) -> Maybe String
FieldError.errorsFor : tag -> List (FieldError tag) -> List String
FieldError.clear     : tag -> List (FieldError tag) -> List (FieldError tag)
```

Validation errors are caught at the Endpoint boundary, returned as HTTP 422 with structured body. The client receives them as `Validation (List (FieldError tag))` in `ResponseError` — already typed (no parsing needed).

### 4.7 Auth

Mar does not provide a fixed `User` entity. The app defines its own User record, including the fields Mar's auth helpers require:

```elm
type alias User =
    { id : UserId         -- required
    , email : String      -- required
    , role : Role         -- optional, but enables requireRole
    , displayName : Maybe String
    , handle : Maybe String
    , createdAt : DateTime
    , updatedAt : DateTime
    }

users : Entity User
users =
    entity User
        |> primaryKey .id
        |> unique [ .email ]
        |> ...
```

Auth helpers use row polymorphism:

```elm
Endpoint.requireAuth : Entity { a | id : UserId, email : String } -> ...
Endpoint.requireRole : Entity { a | role : role, ... } -> role -> ...
```

If the User record is missing `email`, calling `requireAuth users` fails to compile with a clear error.

The app registers its User entity with Mar's runtime via `Auth.config`:

```elm
authConfig : Auth.Config
authConfig = Auth.config { userEntity = users }
```

This is referenced in `Main.mar` so the runtime knows which entity is the auth user.

### 4.8 Routes

Routes are organized into groups by access policy. Every route must declare its policy — there is no implicit default.

```elm
routes : List Route
routes =
    List.concat
        [ Routes.public
            [ list   |> Endpoint.implement (\_ -> listAll)
            , show   |> Endpoint.implement getOne
            ]

        , Routes.authenticated users
            [ create |> Endpoint.implement createOne
            , update |> Endpoint.implement updateOne
            , delete |> Endpoint.implement deleteOne
            ]

        , Routes.requireRole users Admin
            [ purgeAll |> Endpoint.implement createPurge
            ]
        ]
```

Routes:

```elm
Routes.public        : List Route -> List Route
Routes.authenticated : Entity { a | id : UserId, email : String } -> List Route -> List Route
Routes.requireRole   : Entity { a | role : role, ... } -> role -> List Route -> List Route
```

Routes inside a group inherit the group's policy. Individual routes can add extra restrictions (e.g., extra validation) but cannot conflict with the group's policy.

### 4.9 Errors

`ResponseError tag` is the error type for any handler:

```elm
type ResponseError tag
    = Db DbError
    | Validation (List (FieldError tag))
    | Forbidden String
    | BadRequest String
    | NotFoundResource String
    | Conflict String
    | Unauthorized String
    | Custom Int String

type DbError
    = NotFound
    | UniqueViolation { field : String }
    | ForeignKeyViolation { field : String }
```

HTTP status mapping is automatic:

| Variant | Status | Body |
|---|---|---|
| `Db NotFound` | 404 | `{"error":"not found"}` |
| `Db (UniqueViolation {field})` | 409 | `{"error":"<field> already taken"}` |
| `Db (ForeignKeyViolation {field})` | 422 | `{"error":"<field> does not exist"}` |
| `Validation [...]` | 422 | `{"errors":[{"field":"...","message":"..."}, ...]}` |
| `Forbidden msg` | 403 | `{"error": msg}` |
| `BadRequest msg` | 400 | `{"error": msg}` |
| `NotFoundResource msg` | 404 | `{"error": msg}` |
| `Conflict msg` | 409 | `{"error": msg}` |
| `Unauthorized msg` | 401 | `{"error": msg}` |
| `Custom status msg` | status | `{"error": msg}` |

Connection failures and other infra errors are intercepted by the runtime; they never reach the handler.

## 5. Frontend

### 5.1 MVU model

Each screen is its own independent MVU loop with `Model`, `Msg`, `init`, `update`, and `view`.

```elm
init : <pathArgs> -> [User] -> (Model, Effect Never Msg)
update : Msg -> Model -> (Model, Effect Never Msg)
view : Model -> View Msg
```

The runtime instantiates a screen on navigation, swaps it out when the user navigates away.

### 5.2 Screen value

Each screen module exports a typed `Screen` value:

```elm
type Screen pathArgs model msg

screen : Screen PostId Model Msg
screen =
    Screen.with
        { path = "/posts/:postId"
        , init = init
        , update = update
        , view = view
        }
```

The first type parameter (`pathArgs`) carries the URL-derived arguments. `User` (when injected by the runtime) is not in this type — it's part of `init`'s signature.

The init function declares what it needs:

| Init signature | What runtime injects |
|---|---|
| `(Model, Effect Never Msg)` | Nothing (truly static) |
| `path -> (Model, Effect Never Msg)` | URL path arg |
| `User -> (Model, Effect Never Msg)` | Logged user (auth required) |
| `Maybe User -> (Model, Effect Never Msg)` | Optional user (public screen) |
| `path -> User -> (Model, Effect Never Msg)` | Both |

The runtime infers the auth requirement from whether `init` declares `User` or `Maybe User`.

### 5.3 View vocabulary

Views are abstract — no HTML, CSS, or SwiftUI in user code. Mar renders natively per platform (HTML/CSS for web, SwiftUI for iOS).

Elements are semantic, not visual:

```elm
section [] [ ... ]
row [] [ ... ]
column [] [ ... ]

title "Heading"
subtitle "Sub"
text "Body"

button [ onClick Save, intent Primary ] [ text "Save" ]

field
    [ label "Name"
    , value model.name
    , onChange NameChanged
    , disabled model.saving
    , errorMessage (FieldError.firstFor Name model.errors)
    ]

field
    [ label "Bio"
    , multiline True
    , value model.bio
    , onChange BioChanged
    ]

list (List.map renderItem model.items)
banner [ intent Error ] [ text "Failed" ]
empty
```

### 5.4 Intent

Semantic styling tags. The theme maps each Intent to platform-appropriate visual styles.

```elm
type Intent
    = Primary       -- main action
    | Subtle        -- secondary
    | Destructive   -- irreversible
    | Error         -- something went wrong
    | Warning       -- attention
    | Success       -- confirmation
    | Info          -- neutral message
```

Used as an attribute on buttons, banners, badges:

```elm
button [ intent Primary ]     [ text "Save" ]
button [ intent Destructive ] [ text "Delete account" ]
banner [ intent Error ]       [ text "Could not save" ]
banner [ intent Success ]     [ text "Profile updated" ]
```

Theme is hardcoded in MVP (no customization in `mar.json` yet).

### 5.5 Navigation

The `Navigation` module provides view attributes for declarative navigation and effects for programmatic navigation.

```elm
import Navigation

Navigation.target : NavigateTarget -> Attr msg              -- view attribute
Navigation.push   : NavigateTarget -> Effect Never msg      -- effect
Navigation.back   : Effect Never msg                         -- effect
```

#### Declarative (in view)

```elm
button [ Navigation.target (Screens.PostDetail.to post.id) ]
    [ text "View post" ]

row [ Navigation.target (Screens.ProfileDetail.to user.id) ]
    [ text user.handle ]
```

#### Programmatic (in update)

```elm
update msg model =
    case msg of
        PostCreated (Ok post) ->
            ( { model | submitting = False }
            , Navigation.push (Screens.PostDetail.to post.id)
            )
```

#### Screen.to

Each screen module manually exports its `to` function:

```elm
to : PostId -> NavigateTarget
to = Screen.to screen
```

Type-checked: `Screens.PostDetail.to "abc"` fails to compile (expects PostId).

## 6. Client ↔ Server

The same `Endpoint` value is used on both sides:

- **Backend** uses `Endpoint.implement` to bind a handler.
- **Frontend** uses `Endpoint.call` to invoke remotely.

```elm
-- Shared (defined once):
publishPost : Endpoint.Post PostInput Post PostField
publishPost = Endpoint.post "/posts" |> Endpoint.validate validatePostInput

-- Backend:
Routes.authenticated users
    [ publishPost |> Endpoint.implement createPost
    ]

-- Frontend:
update msg model =
    case msg of
        SubmitClicked ->
            ( { model | submitting = True }
            , Endpoint.call publishPost () { body = model.body }
                |> Effect.toMsg PostCreated
            )

        PostCreated (Err (Validation errors)) ->
            ( { model | fieldErrors = errors, submitting = False }
            , Effect.none
            )
```

Renaming an endpoint, changing its input type, or modifying its validation tag breaks both backend and frontend at compile time. No code generation step.

## 7. Main.mar

Entry point. A single `App.application { ... }` call:

```elm
module Main exposing (main)

import App exposing (App)

import Posts
import Comments
import Follows

import Screens.Home
import Screens.Timeline
import Screens.Profiles
import Screens.ProfileDetail
import Screens.PostDetail
import Screens.CommentDetail


main : App
main =
    App.application
        { routes =
            List.concat
                [ Posts.routes
                , Comments.routes
                , Follows.routes
                ]
        , screens =
            [ Screens.Home.screen
            , Screens.Timeline.screen
            , Screens.Profiles.screen
            , Screens.ProfileDetail.screen
            , Screens.PostDetail.screen
            , Screens.CommentDetail.screen
            ]
        }
```

`routes` and `screens` are explicit lists. Mar does not auto-discover.

## 8. Deferred / Future Work

The following are intentionally not in the MVP. They will be revisited when concrete need arises.

- **Subscriptions** (timer, websocket, server-sent events) — Elm-style `Sub Msg`.
- **Theme customization** — `mar.json` `theme` field for colors, typography.
- **Escape hatches for platform-specific behavior** — haptic feedback, push notifications, file downloads.
- **Custom HTTP clients** for external APIs (Stripe, etc.) — explicit `Codec` API.
- **Crud scaffold helpers** (`Crud.scaffold entity`) — if examples become repetitive.
- **Ownership helpers** (`Routes.requireOwner`) — same.
- **State preservation across navigation** — currently screens are destroyed on navigate-away.
- **Loading screen abstraction** — currently each screen handles its own loading state.
- **Multiple environments in `mar.json`** — currently a single config; env vars handle differences.
- **`responseErrorToString`** — built-in default messages for `ResponseError` variants.

## 9. Examples

See `examples/`:

- `personal-todo.mar` — minimal CRUD with ownership.
- `pet-food-log.mar` — validation + ownership + custom query.
- `mini-twitter.mar` — full-featured: 6 entities, validations, queries, 6 MVU screens with forms, auth, and follow/like flows.
