# Belm

Belm is an Elm-inspired language for backend development, implemented in Go, with a strong focus on readability, simplicity and maintainability.

## Goals

- Simple, declarative syntax (`entity`, `rule`, `authorize`, `auth`)
- Automatic REST CRUD
- SQLite as the database
- Email code login flow
- Rule-based authorization
- Safe automatic migrations

## Architecture (Go)

- [cmd/belm/main.go](/Users/marcio/dev/github/belm/cmd/belm/main.go): compiler/runtime CLI
- [internal/parser/parser.go](/Users/marcio/dev/github/belm/internal/parser/parser.go): `.belm` language parser
- [internal/expr/parser.go](/Users/marcio/dev/github/belm/internal/expr/parser.go): expression parser (`rule`/`authorize`)
- [internal/runtime](/Users/marcio/dev/github/belm/internal/runtime): HTTP server, auth/authz, and migrations
- [internal/sqlitecli/sqlitecli.go](/Users/marcio/dev/github/belm/internal/sqlitecli/sqlitecli.go): SQLite access via `sqlite3` binary (no external dependencies)

## Compiler Command

Compile `.belm` into an executable:

```bash
./belm compile examples/store.belm
```

Run development mode with hot reload (rebuild/restart on save):

```bash
./belm dev examples/store.belm
```

Show Belm CLI version and build metadata:

```bash
./belm version
```

Default output location is `build/<name>/<name>` where `<name>` comes from input filename:

- `examples/store.belm` -> `build/store/store`

Run the compiled executable:

```bash
./build/store/store serve
./build/store/store admin
./build/store/store backup
```

Optional output name:

```bash
./belm compile examples/store.belm bookstore-dev
# output: build/bookstore-dev/bookstore-dev
```

Run API + Admin panel and open browser (from the compiled executable):

```bash
./build/store/store admin
```

## Code Formatting

Belm provides a canonical formatter in Elm style (single official formatting style).

Format files in place:

```bash
./belm format examples/store.belm examples/todo.belm
```

Check formatting in CI (no writes):

```bash
./belm format --check examples/store.belm
```

Format from stdin:

```bash
cat examples/store.belm | ./belm format --stdin
```

## Auto-generated Clients

Belm generates client files in the same output folder as the executable:

- Elm: `<AppName>Client.elm`
- TypeScript: `<AppName>Client.ts`

Both clients include:

- `schema` (entity metadata)
- CRUD functions per entity:
- `list<Entity>`
- `get<Entity>`
- `create<Entity>`
- `update<Entity>`
- `delete<Entity>`
- typed action functions:
- `run<Action>`
- auth endpoints, when auth is enabled:
- `requestCode`
- `login`
- `logout`
- `me`

Elm client also exposes:

- `rowDecoder`

Usage example in Elm:

```elm
import StoreApiClient as Api

type Msg
    = GotUsers (Result Http.Error (List Api.Row))

load : Cmd Msg
load =
    Api.listUser
        { baseUrl = "http://localhost:4100", token = "" }
        GotUsers
```

Usage example in TypeScript:

```ts
import { Config, createBook, runPlaceBookOrder } from "./BookStoreApiClient";

const config: Config = {
  baseUrl: "http://localhost:4100",
  token: "<bearer-token>",
};

await createBook(config, {
  title: "Domain Modeling Made Functional",
  authorName: "Scott Wlaschin",
  isbn: "978-1-68050-254-1",
  price: 129.9,
  stock: 10,
});

await runPlaceBookOrder(config, {
  orderRef: "ORD-2026-0001",
  userId: 1,
  bookId: 1,
  quantity: 1,
  unitPrice: 129.9,
  lineTotal: 129.9,
  orderTotal: 129.9,
  notes: "first order",
});
```

## Admin Panel

An Admin panel (built with Elm and elm-ui) is also provided:

- code: [admin/src/Main.elm](/Users/marcio/dev/github/belm/admin/src/Main.elm)
- docs: [admin/README.md](/Users/marcio/dev/github/belm/admin/README.md)

It uses `GET /_belm/schema` to discover entities and lets you list/create/update/delete records.

## VS Code Extension (Syntax + LSP + Format)

A VS Code language extension for `.belm` files is available in:

- [vscode-belm](/Users/marcio/dev/github/belm/vscode-belm)

It provides:

- syntax highlighting
- snippets/autocomplete templates
- LSP diagnostics
- LSP keyword completion
- go to definition
- find references
- rename symbol
- hover docs
- document symbols (Outline)
- quick fixes (code actions)
- document formatting (`Format Document` and `formatOnSave`)

## Language Syntax

Minimal example:

```belm
app TodoApi
port 4100
database "./todo.db"

entity Todo {
  id: Int primary auto
  title: String
  done: Bool
  rule "Title must have at least 3 chars" when len(title) >= 3
}
```

### Statements

- `app <Name>`
- `port <number>`
- `database "<sqlite_path>"`
- `auth { ... }`
- `entity <Name> { ... }`
- `type alias <Name> = { ... }`
- `<actionName> : <InputAlias> -> Effect`
- `<actionName> = transaction [ insert Entity { field = input.value } ]`

### Fields

`<fieldName>: <Type> [primary] [auto] [optional]`

Types:

- `Int`
- `String`
- `Bool`
- `Float`

Attributes:

- `primary`: primary key
- `auto`: auto-increment (usually with `Int primary`)
- `optional`: nullable field

If no primary key is provided, Belm automatically adds:

`id: Int primary auto`

## Typed Actions (Elm-style)

Belm supports Elm-inspired typed actions for multi-entity writes in a single transaction.

```belm
type alias PlaceOrderInput =
  { userId : Int
  , total : Float
  , note : String
  }

placeOrder : PlaceOrderInput -> Result DomainError Effect
placeOrder =
  transaction
    [ insert Order { userId = input.userId, status = "created", total = input.total, note = input.note }
    , insert AuditLog { userId = input.userId, message = "order created" }
    ]
```

Behavior:

- compile-time type checks for action input and assigned entity fields
- friendly compile errors (`expects Float but got String`, missing required fields, unknown input fields)
- atomic execution (all steps succeed or all rollback)

## Business Rules (`rule`)

Inside `entity`:

```belm
rule "User must be 18 or older" when age >= 18
```

Operators:

- `and`, `or`, `not`
- `==`, `!=`, `>`, `>=`, `<`, `<=`
- `+`, `-`, `*`, `/`

Functions:

- `contains(text, part)`
- `startsWith(text, prefix)`
- `endsWith(text, suffix)`
- `len(value)`
- `matches(text, regex)`

Literals:

- `true`, `false`, `null`

If a rule fails, the API returns HTTP `422` with `error` and `details`.

## Authentication (`auth`)

Built-in email code login flow:

1. `POST /auth/request-code`
2. send the code by email
3. `POST /auth/login` (email + code) returns a bearer token
4. `POST /auth/logout` revokes the session

For first-login flows, `request-code` can auto-create the auth user when the selected `user_entity`
only requires inferable fields (for example `email` and `role`).

Configuration:

```belm
auth {
  user_entity User
  email_field email
  role_field role
  code_ttl_minutes 10
  session_ttl_hours 24
  email_transport console
  email_from "no-reply@store.local"
  email_subject "Your StoreApi login code"
  dev_expose_code true
}
```

Recommended framework pattern:

- keep auth identities in a dedicated `User` entity (table: `users`)
- use `auth_email` or `auth_user_id` in `authorize` expressions for ownership checks

`email_transport`:

- `console`: prints code in logs
- `sendmail`: uses local binary (`sendmail_path`)

## System Admin Authentication

Belm now includes a separate admin authentication flow for system endpoints and Belm Admin tools.

System admin endpoints:

- `POST /_belm/admin/request-code`
- `POST /_belm/admin/login`
- `POST /_belm/admin/logout`
- `GET /_belm/admin/me`
- `POST /_belm/admin/bootstrap` (first system admin only)

System-only admin endpoints:

- `GET /_belm/perf`
- `POST /_belm/backup`
- `GET /_belm/backups`

This system auth does not depend on your app `User` entity, so apps without `auth { ... }`
(for example `examples/todo.belm`) can still login as system admin in Belm Admin.

## Authorization (`authorize`)

Per CRUD operation:

```belm
authorize list when isRole("admin")
authorize get when auth_authenticated and (id == auth_user_id or isRole("admin"))
authorize create when true
authorize update when auth_authenticated and (id == auth_user_id or isRole("admin"))
authorize delete when isRole("admin")
```

Context available in authorization expressions:

- `auth_authenticated`
- `auth_email`
- `auth_user_id`
- `auth_role`
- entity fields (`id`, `userId`, etc.)

Extra function:

- `isRole("admin")`

## Generated Endpoints

For each entity `X`:

- `GET /xs`
- `GET /xs/:id`
- `POST /xs`
- `PUT /xs/:id`
- `PATCH /xs/:id`
- `DELETE /xs/:id`

Always:

- `GET /health`
- `GET /_belm/schema`
- `POST /_belm/admin/request-code`
- `POST /_belm/admin/login`
- `POST /_belm/admin/logout`
- `GET /_belm/admin/me`
- `POST /_belm/admin/bootstrap` (first system admin only)
- `GET /_belm/perf` (system admin only)
- `POST /_belm/backup` (system admin only)
- `GET /_belm/backups` (system admin only)

With auth enabled:

- `POST /auth/request-code`
- `POST /auth/login`
- `POST /auth/logout`
- `GET /auth/me`
- `POST /_belm/bootstrap-admin` (first user only)

For each typed action `myAction`:

- `POST /actions/myAction`

## Migrations

Migrations run automatically on startup.

Automatic behavior:

- creates missing tables
- adds new optional columns
- creates/migrates internal auth tables (app auth + system admin auth)
- records operations in `belm_schema_migrations`

Blocked (manual migration required):

- column type changes
- primary key changes
- nullability changes
- adding required fields to existing tables
- adding primary/auto columns to existing tables

When blocked, the server fails at startup with a clear error message.

## Full Example

Use [examples/store.belm](/Users/marcio/dev/github/belm/examples/store.belm), which already includes:

- business rules (email validation, role checks, stock/order constraints, etc.)
- email code auth
- role/ownership authorization with dedicated auth users
- entities: `User`, `Book`, `Order`, `OrderItem`, `AuditLog`
- typed action: `placeBookOrder`
