# Mar, Authorization Primitives

**Status**: **Implemented** (2026-05-05). Server-side enforcement live.
**Author**: Claude (with Marcio).
**Originally drafted**: 2026-05-04. Landed: 2026-05-05.
**Tracks**: Caminho B from the auth roadmap.

## Quick start

See `examples/team-notes/` for a runnable end-to-end example. The key
APIs:

```mar
-- Define your roles as a closed sum type, persisted via Entity.enum:
type Role = Member | Admin

users = Entity.define "users"
    { ...
    , role = Entity.enum [Member, Admin] Entity.notNull
    }

-- Auth.config carries everything the framework needs at the auth
-- layer, including where unauthed users get redirected and how long
-- a session lives:
auth = Auth.config
    { entity          = Backend.Users.users
    , identify        = \u -> u.email
    , role            = \u -> u.role             -- where roles live
    , signInPage      = Frontend.SignIn.page     -- redirect target — Page reference, not String
    , email           = { ... }
    , signup          = ...
    , sessionDuration = Time.days 30             -- typed Duration, not raw seconds
    }

-- Page.protected gates a route on Auth.me. The `signInPage` from
-- Auth.config above is the redirect target — no per-page repetition:
homePage =
    Page.protected
        { path = "/"
        , title = "Home"
        , init = init, update = update, view = view
        }

-- Decorators stack on Auth.protect ExposedServices:
services =
    [ Auth.protect Shared.editNote editNote
        |> Auth.authorize loadEditTarget sameTeam        -- ABAC

    , Auth.protect Shared.deleteNote deleteNote
        |> Auth.authorize    loadDeleteTarget sameTeam
        |> Auth.requireRole  Admin                       -- RBAC, type-safe

    , Auth.protect Shared.editProfile editProfile
        |> Auth.requireOwner loadProfile (\p -> p.userId)  -- ownership sugar
    ]
```

The dispatcher applies gates in order before invoking the handler.
Reject codes: **401** for missing session, **403** for role/policy
denial, **404** for `Nothing`-returning loaders. See "Worked example"
section below for the full security policy table this example
expresses.

## TL;DR

Mar now has both **authentication** (passwordless email codes,
sessions, `Auth.protect` for service handlers) and first-class
**authorization** primitives (this proposal). Apps express ownership
and permissions through composable decorators on `ExposedService`,
not by hand-rolling checks inside handler bodies.

Three primitives:

1. **`Auth.requireRole`**: RBAC: gate a service by the logged user's role.
2. **`Auth.authorize`**: ABAC: gate a service by an arbitrary policy
   over `(input, user, resource)`.
3. **`Auth.requireOwner`**: ergonomic shortcut for the common
   "user owns this resource" case.

All three compose with `Auth.protect` and `Service.implement` and
don't change the rest of the language.

## Why this matters

The current example (`notes-auth-multipage`) only models one
authorization scheme: **filter by `user.id`**:

```mar
listMine _ user =
    Repo.findBy notes { authorId = user.id }
```

That works for "list my own notes" but breaks down the moment the app
needs:

| Real scenario | Why current setup fails |
|---|---|
| `editNote noteId body` | Need to fetch the note, check `note.authorId == user.id`, reject if not. No primitive, every handler reinvents this dance. |
| Admin-only operations (delete a user, view all orgs) | No notion of `user.role`. App authors hand-roll `if user.email in adminList then ...`. |
| Multi-tenant apps (user belongs to org A, only sees org A data) | Tenant scoping is the same shape as ownership but with `orgId` instead of `userId`. Same hand-roll. |
| Role hierarchies (editor < admin < owner) | No language support for ordered roles. |
| Per-resource ACLs ("user X has read-only on doc Y") | Policy lives in a separate table, handlers query it manually. |

Without primitives, each app reinvents the wheel inconsistently. Some
handlers forget the check entirely (security bug). Others reject
post-mutation (data leaks before rollback). The pattern needs to be
**hard to get wrong**.

## Design principles

These shape every choice below:

1. **Decorators, not redefinitions.** A protected+authorized service
   reads as `Auth.protect contract handler |> Auth.requireRole Admin`
  , each layer adds one concern. No "smart constructor" that bundles
   everything and hides the composition.
2. **Policies are pure functions.** No DSL, no policy file. A policy
   is `(input, user, resource) -> Bool`. Makes them trivially
   testable and lintable.
3. **Fail closed.** A missing decorator = no protection. The default
   behavior of `Auth.protect` is "any logged-in user can call this";
   the language doesn't auto-infer ownership from field names.
4. **One pre-flight, then the handler.** Authorization runs **before**
   the business logic, fetching whatever it needs to make the
   decision. Handler runs only if all gates pass. No mid-handler
   rejections.
5. **Roles are nominal.** A role is a custom type the user defines;
   the framework stores its name. No magic string roles, no inferred
   roles from email patterns.
6. **No silent inheritance.** Each service declares its own gates
   explicitly. There's no project-wide "all services need admin" rule
   that hides the requirement from the call site.

## Proposed primitives

### 1. Roles, user-defined custom type

The user defines what roles their app has:

```mar
type Role
    = Member
    | Editor
    | Admin
```

The `User` record gains a `role` field (or whatever the user names
it). `Auth.config` learns where to find it:

```mar
auth =
    Auth.config
        { entity   = Backend.Users.users
        , identify = \u -> u.email
        , role     = \u -> u.role          -- NEW: tells the framework where roles live
        , email    = { ... }
        , signup   = \email -> { email = email, role = Member }
        , sessionDuration = 2592000
        }
```

The `role` getter is **optional**. Apps without roles just omit it;
`Auth.requireRole` then errors at compile time on those apps.

### 2. `Auth.requireRole`, RBAC decorator

```mar
Auth.requireRole : role -> ExposedService -> ExposedService
```

Wraps an `ExposedService` (already produced by `Auth.protect`) so the
framework rejects the request with `403` unless the logged user's
role matches.

```mar
services =
    [ Auth.protect      Shared.listMine    listMine
    , Auth.protect      Shared.createMine  createMine
    , Auth.protect      Shared.deleteUser  deleteUser
        |> Auth.requireRole Admin                       -- only Admins can delete users
    ]
```

Reads: "expose `deleteUser` requiring auth, and additionally requiring
the logged user to have role Admin."

**Type-safe**: the `role` argument's type unifies with whatever
`Auth.config.role` returns. If the user defined `Role = Member |
Editor | Admin`, only those values are valid here. Misspell `Adimn`
and you get a compile error, not a 500 at runtime.

### 3. `Auth.authorize`, ABAC decorator

For policies that depend on the **resource** being acted on, not just
the user's role:

```mar
Auth.authorize :
    (input -> User -> Effect resource)        -- how to load the resource
    -> (input -> User -> resource -> Bool)           -- the policy
    -> ExposedService
    -> ExposedService
```

The framework runs the loader, applies the policy, and rejects with
`403` if it returns False. Handler runs only on True.

```mar
canEditNote : { id : Int, body : String } -> Shared.User -> Shared.Note -> Bool
canEditNote _ user note =
    note.authorId == user.id


loadNote : { id : Int, body : String } -> Shared.User -> Effect Shared.Note
loadNote input _ =
    Repo.findOne notes { id = input.id }
        |> Effect.unwrap "note not found"


editNote : { id : Int, body : String } -> Shared.User -> Effect Shared.Note
editNote input _ =
    Repo.update notes { id = input.id } { body = input.body }


services =
    [ Auth.protect Shared.editNote editNote
        |> Auth.authorize loadNote canEditNote
    ]
```

Reads: "expose `editNote` requiring auth, and additionally check that
the loaded note passes `canEditNote`."

**Why two functions instead of one?** Splitting load from policy lets
the framework cache the resource once, and lets the policy be a pure
function (no I/O, trivial unit tests). It also makes "load failed"
distinct from "policy denied" in error messages.

### 4. `Auth.requireOwner`, ergonomic shortcut

The most common ABAC case is "this resource has an owner field, and
it must equal `user.id`". Sugar for that:

```mar
Auth.requireOwner :
    (input -> User -> Effect resource)
    -> (resource -> Int)                             -- ownerId field selector
    -> ExposedService
    -> ExposedService
```

The above `editNote` example collapses to:

```mar
services =
    [ Auth.protect Shared.editNote editNote
        |> Auth.requireOwner loadNote (\note -> note.authorId)
    ]
```

Internally desugars to `Auth.authorize loadNote (\_ user resource ->
selector resource == user.id)`.

### 5. Combining gates

Multiple decorators stack, each can reject independently:

```mar
services =
    [ Auth.protect Shared.archiveOrgData handler
        |> Auth.requireRole Admin
        |> Auth.requireOwner loadOrg (\org -> org.ownerId)
    ]
```

Reads top-to-bottom: must be authed, must be Admin, must own the org.
Any gate failing returns 403; the handler runs only on the happy path.

## Rejected alternatives

### Alternative A: Single `Auth.policy` with a record

Instead of three decorators, one combined:

```mar
Auth.policy
    { role     = Just Admin
    , owner    = Just (loadOrg, .ownerId)
    , custom   = Just (\input user -> ...)
    }
```

**Rejected** because:
- Forces the user to think about all gates upfront, even when only one applies.
- Doesn't compose, to add a 4th check later, the record schema grows.
- The `Maybe` ceremony for unused gates clutters the call site.

### Alternative B: Type-level role hierarchy (`Role` is a comparable enum)

```mar
type Role = Member < Editor < Admin       -- ordered

Auth.requireRole AtLeast Editor handler   -- accepts Editor or Admin
```

**Rejected for v1** because:
- Adds a language feature (ordered enums).
- 90% of apps have ≤3 flat roles.
- Easy to add later if demand emerges; design is forward-compatible.

### Alternative C: Annotation-style (`@requires(role=Admin)`)

```mar
@requires(role = Admin)
deleteUser : ...
```

**Rejected** because:
- Mar has no annotation syntax today.
- Decorators-as-functions are more uniform with the rest of the
  language (`|>`, `Auth.protect`, `Service.implement`).
- Annotations decay to docs when parsers don't fully understand them.

### Alternative D: Policy DSL (Pundit/CanCan-style)

```mar
policies = Policy.define
    { Note  = { read = anyone, write = owner, delete = admin }
    , Org   = { read = members, write = admin, delete = owner }
    }
```

**Rejected** because:
- Couples policies to entity types globally, can't have "different
  policies for different services touching the same entity."
- Hides the gate from the service definition site; reading
  `Backend.Notes.services` no longer tells you what's protected.
- DSLs are hard to evolve without breaking changes.

The decorator approach lets each service declare exactly what it
needs at the wiring site, with no separate registry to keep in sync.

## Worked example: team-notes

Imagine a small SaaS: every user belongs to a Team, can be Member or
Admin within it. Members can create + read team notes; Admins can
also edit + delete.

```mar
-- Shared.mar
type Role = Member | Admin

type alias User =
    { id    : Int
    , email : String
    , teamId : Int
    , role  : Role
    }

type alias Note =
    { id     : Int
    , body   : String
    , teamId : Int       -- which team owns this note
    , authorId : Int
    }

listTeamNotes : Service () (List Note)
listTeamNotes = Service.declare GET "/team-notes"

createNote : Service { body : String } Note
createNote = Service.declare POST "/team-notes"

editNote : Service { id : Int, body : String } Note
editNote = Service.declare PUT "/team-notes/{id:Int}"

deleteNote : Service { id : Int } ()
deleteNote = Service.declare DELETE "/team-notes/{id:Int}"


-- Backend/Notes.mar (sketch)
listTeamNotes _ user =
    Repo.findBy notes { teamId = user.teamId }

createNote input user =
    Repo.create notes
        { body = input.body, teamId = user.teamId, authorId = user.id }

editNote input _ =
    Repo.update notes { id = input.id } { body = input.body }

deleteNote input _ =
    Repo.delete notes { id = input.id }

loadNote input _ =
    Repo.findOne notes { id = input.id } |> Effect.unwrap "note not found"

-- Policy: a user can edit/delete a note iff it belongs to their team.
sameTeam : a -> Shared.User -> Shared.Note -> Bool
sameTeam _ user note = note.teamId == user.teamId

services =
    [ -- Anyone authed in the team can read/create
      Auth.protect Shared.listTeamNotes listTeamNotes
    , Auth.protect Shared.createNote    createNote

      -- Edits require sameTeam (any team member can edit any team note)
    , Auth.protect Shared.editNote editNote
        |> Auth.authorize loadNote sameTeam

      -- Deletes additionally require Admin role
    , Auth.protect Shared.deleteNote deleteNote
        |> Auth.authorize    loadNote sameTeam
        |> Auth.requireRole  Admin
    ]
```

The wiring **reads as a security policy table**: anyone scanning
`services = [...]` knows exactly who can do what without reading
handler bodies.

A complete runnable (post-implementation) example lives at
`examples/team-notes/`.

## Implementation (landed 2026-05-05)

What's live in the runtime:

### Type-level (`internal/typecheck/env.go`)

Add three forall-typed primitives:

```go
"authRequireRole": TForall{
    Vars: []int{a.ID, b.ID, role.ID},
    Body: TArrow{
        From: TVar{ID: role.ID},
        To: TArrow{
            From: TExposedService(),
            To:   TExposedService(),
        },
    },
},

"authAuthorize": TForall{
    Vars: []int{a.ID, b.ID, res.ID, user.ID},
    Body: TArrow{
        From: TArrow{From: a, To: TArrow{From: user, To: TEffect(TString, res)}},  // loader
        To: TArrow{
            From: TArrow{From: a, To: TArrow{From: user, To: TArrow{From: res, To: TBool}}},  // policy
            To: TArrow{
                From: TExposedService(),
                To:   TExposedService(),
            },
        },
    },
},

"authRequireOwner": TForall{
    // sugar: same as authorize with policy = (\_ user res -> selector res == user.id)
    ...
},
```

`Auth.config` gains an optional `role` field:

```go
"role": TArrow{From: TVar{ID: user.ID}, To: TVar{ID: role.ID}},  // optional via row tail
```

### Runtime, Go (server)

`VService` (in `internal/runtime/service.go`) gains three policy
fields, each nil when the corresponding decorator wasn't applied:

```go
type VService struct {
    Handler      Value
    OriginModule string
    OriginName   string
    RequiresUser bool

    // Authorization gates (this proposal).
    RequireRole  Value  // value compared against auth.role(user)
    LoadResource Value  // input -> user -> Effect (Maybe resource)
    Policy       Value  // input -> user -> resource -> Bool
}
```

`VAuth` gains an optional `Role Value` field captured from
`Auth.config { ..., role = ... }`.

The dispatcher (`ExposedServiceToRoute`) calls two helpers after
loading the User but before invoking the handler:

- `checkRoleGate(required, user)`, applies the registered role
  getter to extract the user's role; structural-equals against
  `required`. Mismatch → 403. Misconfiguration (decorator used but
  no `role` in Auth.config) → 500 with a clear message.
- `checkABACGate(loader, policy, input, user)`, applies loader,
  runs its Effect; `Nothing` → 404; `Just resource` is fed through
  the policy; `False` → 403.

For `Auth.requireOwner`, the runtime synthesizes a policy closure at
decorator time: `\input user resource -> selector(resource) == user.id`
(via Go's `equalValues`). No new types or new dispatcher branches,
it desugars cleanly onto the ABAC machinery.

### Runtime, JS (browser) and iOS

Authorization always runs server-side. Both client runtimes treat
the decorators as pass-throughs: they exist in the env so calls
evaluate, but no enforcement happens, the server's dispatcher is
the single source of truth. This matches how `Auth.protect` already
behaves on the client.

### Tests

- Manual end-to-end: `examples/team-notes/` was driven via curl
  exercising 401 (no session), 403 (role denial via `requireRole`),
  403 (policy denial via `authorize`), 404 (loader returned
  Nothing), and 200 (all gates pass). All status codes match.
- Unit tests: `internal/runtime/auth_test.go` covers each decorator
  attaching its policy field and the structural equality used by
  `requireOwner`.

## Migration from current `Auth.protect`-only code

**Zero breaking changes.** The new decorators are additive. Existing
services continue to work exactly as today. Authors opt in by
appending `|> Auth.requireRole Admin` (etc.) to a wiring line.

The current `notes-auth-multipage` example (no roles, single-user
ownership via filter) stays identical, it's the simplest case the
new primitives also support, just with no additional decorators.

## Known limitations

1. **No per-request resource caching.** If three services on the
   same request need the same loaded resource, we run the loader
   three times. Add caching when profiling shows it matters.

2. **No audit log.** Every reject is silent in v1 (just an HTTP
   status). Production deployments should add structured logging of
   `(user.id, service, reason, timestamp)` separately.

3. **Enum roles must be zero-arg constructors.** `Entity.enum`
   accepts `[Member, Admin]` but rejects `[Tagged Int]`. For
   roles this is fine; for ADTs with payloads (e.g. `Status =
   Active | Banned String`), use `Entity.text` and serialize
   manually until a richer ADT-to-SQL story lands.

## Resolved during implementation

- **Type-safe roles via `Entity.enum`** (landed 2026-05-05). Roles
  are now full custom types: `type Role = Member | Admin`,
  `Auth.requireRole Admin` rejects misspellings at compile time,
  the SQLite column gets a `CHECK(role IN ('Member','Admin'))` so
  even non-mar clients can't insert garbage. See
  `examples/team-notes/Backend/Users.mar` for the declaration form.
- **Where does the `Role` type live?** In `Shared.mar` so frontend
  can pattern-match on it for conditional UI. Backend imports for
  the `Entity.enum` declaration and the gate.
- **Where does the redirect target live?** Centralized in
  `Auth.config { signInPage = ... }`, a `Page` reference, not a
  string. `Page.protected` carries no `redirect` field; the dispatcher
  reads from `Auth.config` at render time. Renaming the sign-in
  page's path propagates everywhere because `signInPage` references
  the Page value directly. Rationale: per-page redirect was DRY-
  hostile (every protected page repeating `redirect = "/sign-in"`)
  and string-typed (typo-prone, no refactor support). Page reference
  + central config fixes both at once.
- **`sessionDuration` is a `Duration` type**, not a raw `Int`.
  Constructed via `Time.days N` / `Time.hours N` / etc., no
  ambiguity about units at the call site. The previous form
  (`sessionDuration = 2592000`) still works for back-compat but
  every example now uses `Time.days 30`.
- **Error message granularity**: client sees a generic `"forbidden"`
  / `"not found"`. Server logs can be added later for debugging.
- **Policy testability**: policies are already pure functions,
  testable via the existing test helpers, no new framework needed.

## Wire format note (for ADTs across the JSON boundary)

Custom-type constructors round-trip through JSON using a marker:

| Ctor shape    | Wire format              | Example          |
|---------------|--------------------------|------------------|
| zero-arg      | `{"__ctor": "Tag"}`      | `{"__ctor": "Member"}` |
| with payload  | `{"__ctor": "Tag", "__args": [...]}` | `{"__ctor": "Tagged", "__args": [42]}` |
| `Just x`      | `x` (transparent)        | `{"id": 1}` |
| `Nothing`     | `null`                   | `null` |
| `Ok x`        | `x` (transparent)        | depends on x |
| `Err msg`     | `{"error": msg}`         | `{"error": "boom"}` |

`Maybe` and `Result` keep their convenience encodings for backward
compatibility; everything else uses the marker. Both Go encoders
(`encodeValue`, `valueToAny`) and both client decoders (JS
`jsToMar` / `marToJs`, iOS `MarJSONCodec`) follow this convention.
