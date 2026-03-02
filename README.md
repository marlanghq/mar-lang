# Belm

Belm is a new backend programming language inspired by Elm.

- Pure-by-design model: application code is declarative (entities + types), while effects (HTTP + SQLite) are handled by the generated runtime.
- Simple syntax: no classes, no inheritance, no hidden state.
- Backend focus: compile to a production-friendly REST API with SQLite-backed CRUD.
- Business logic included: entity rules are validated on `POST`/`PUT`/`PATCH`.

## Host Language

Belm is hosted in **TypeScript/Node.js** (implemented here as ESM JavaScript running on Node.js).  
Reason: we can compile and run immediately with zero external dependencies using built-in modules:

- `node:http` for REST API serving
- `node:sqlite` for SQLite persistence

## Language Syntax

```belm
app TodoApi
port 4000
database "./todo.db"

entity Todo {
  id: Int primary auto
  title: String
  done: Bool
  rule "Title must have at least 3 chars" when len(title) >= 3
}
```

### Supported statements

- `app <Name>`
- `port <number>`
- `database "<sqlite_file_path>"`
- `entity <Name> { ... }`
- `rule "<message>" when <expression>` (inside an `entity`)

### Field syntax

`<fieldName>: <Type> [primary] [auto] [optional]`

Types:

- `Int`
- `String`
- `Bool`
- `Float`

Attributes:

- `primary`: primary key
- `auto`: auto increment (valid for integer keys)
- `optional`: nullable field

### Rule syntax

Rules are boolean expressions. If a rule returns `false`, Belm responds with HTTP `422`.

Operators:

- `and`, `or`, `not`
- `==`, `!=`, `>`, `>=`, `<`, `<=`
- `+`, `-`, `*`, `/`

Functions:

- `contains(text, part)`
- `startsWith(text, prefix)`
- `endsWith(text, suffix)`
- `len(value)` (string/array length, otherwise `0`)
- `matches(text, regex)`

Example:

```belm
entity Customer {
  id: Int primary auto
  name: String
  email: String
  age: Int
  rule "Customer must be at least 18 years old" when age >= 18
  rule "Email must look valid" when matches(email, "^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$")
}
```

If no primary key is declared, Belm automatically adds:

`id: Int primary auto`

## What the compiler generates

For each `entity X`, Belm generates:

- SQLite table (snake_case pluralized)
- REST resource path (`/xs`)
- Endpoints:
  - `GET /xs`
  - `GET /xs/:id`
  - `POST /xs`
  - `PUT /xs/:id` (also supports `PATCH`)
  - `DELETE /xs/:id`

Extra endpoint:

- `GET /health`

If a rule fails, response includes:

- `error` (rule message)
- `details.entity`
- `details.rule`

## Compile and run

```bash
node src/belmc.mjs examples/todo.belm build/todo.server.mjs
node build/todo.server.mjs
```

Server output includes URL, SQLite path, and generated resource routes.

## Example requests

Create:

```bash
curl -X POST http://localhost:4000/todos \
  -H "content-type: application/json" \
  -d '{"title":"buy milk","done":false}'
```

List:

```bash
curl http://localhost:4000/todos
```

Update:

```bash
curl -X PUT http://localhost:4000/todos/1 \
  -H "content-type: application/json" \
  -d '{"done":true}'
```

Delete:

```bash
curl -X DELETE http://localhost:4000/todos/1
```

## Project layout

- `src/belmc.mjs`: Belm compiler
- `examples/todo.belm`: sample source program
- `build/`: generated servers
