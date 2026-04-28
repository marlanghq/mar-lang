# mar

mar is a small, statically typed, full-stack functional language. It is
inspired by Elm: same general syntax, Hindley-Milner type inference with row
polymorphism, pure functions with effects as values.

This branch (`ml-experiments`) is a fresh implementation. The previous
Lisp-based version has been removed.

## Status

Working today, end to end:

- Lexer, parser, type checker (full HM with row polymorphism, custom types,
  cross-module type aliases).
- Tree-walking interpreter with closures, currying, pattern matching
  (including cons / list patterns), custom-type constructors, records.
- Stdlib: `List`, `String`, `Maybe`, `Result`, `Effect`, `IO`, `JSON`,
  `Server`, `Response`, `Db`, `Entity`, `View`.
- Multi-file projects with shared types and qualified names.
- `mar.json` manifest with strict schema, `env:VAR` references, secret
  enforcement.
- Real I/O via `IO.print/println/readLine`.
- HTTP server with path params (`/posts/:id`), backed by Go `net/http`.
- SQLite via `Db.open/exec/query/queryOne`.
- `Entity` builder with auto-migrations (`Entity.migrate`).
- Server-side view rendering (`View.render` produces HTML).
- Server-side MVU runtime (`App.create` + `App.serve`): full
  init / update / view loop, session state per browser, buttons rendered
  as HTML forms that POST messages back. No JS required.

## Try it

```bash
go build -o mar ./cmd/mar

# REPL
./mar repl
> 1 + 2
3 : Int

# Run a single file
./mar run examples/factorial.mar
3628800

# Type-check a project
./mar check examples/blog

# Run a project (entry value Module.name)
./mar run examples/multi Main.main

# Run a real HTTP server with SQLite
./mar run examples/notes-entity.mar &
curl -X POST -d "first note" http://localhost:3002/notes
curl http://localhost:3002/notes
```

## Examples

| File | Demonstrates |
|------|--------------|
| `examples/hello.mar` | basic types, custom types, generics |
| `examples/factorial.mar` | recursion + annotations |
| `examples/calculator.mar` | recursive ADT evaluation |
| `examples/inventory.mar` | records, custom types, pipelines, stdlib |
| `examples/wordcount.mar` | cons patterns, recursive list ops |
| `examples/sum-of-squares.mar` | qualified names (`List.map`) |
| `examples/people.mar` | filter / map / records |
| `examples/list-ops.mar` | composition, partial application |
| `examples/mini-eval.mar` | a tiny expression-language interpreter, in mar |
| `examples/effects.mar` | `Effect.succeed/fail/map/andThen` |
| `examples/hello-io.mar` | real I/O via Effects |
| `examples/echo.mar` | interactive (reads stdin) |
| `examples/server.mar` | minimal two-route HTTP server |
| `examples/notes-app.mar` | full CRUD HTTP API + SQLite (raw SQL) |
| `examples/notes-entity.mar` | as above, with `Entity` schema and auto-migrations |
| `examples/db.mar` | direct SQLite access |
| `examples/view-page.mar` | server-side view rendering to HTML |
| `examples/counter.mar` | interactive MVU app: init / update / view, session state |
| `examples/multi/` | multi-file project, simplest |
| `examples/blog/` | 3-file project + `mar.json` |

## CLI

```
mar parse <file>            syntax check
mar check <file|dir>        type check (file or project)
mar run <file|dir> [val]    type-check + run; defaults to value `main`.
                            For projects, use `Module.name`.
mar repl                    interactive
mar config <dir>            load and print mar.json
mar version                 print version
```

## Design

See `docs/mar.md` for the full reference and `docs/managed-effects.md` for
the rationale behind the effect / MVU model.

## Layout

```
cmd/mar/             CLI entry point
internal/lexer/      tokenizer
internal/parser/     parser
internal/ast/        AST types
internal/typecheck/  Hindley-Milner inference
internal/runtime/    tree-walking interpreter, stdlib, server, db, view, ...
internal/project/    multi-file loader, mar.json manifest
docs/                language reference
examples/            working programs
```

## Note

Parts of this project were developed with the assistance of generative AI tools.
