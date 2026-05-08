# mar

mar is a small, statically typed, **full-stack web** language. Inspired by Elm:
same general syntax, Hindley-Milner type inference with row polymorphism, pure
functions, effects as values.

## Focus

mar exists to write web apps end to end — frontend, backend, or both. Every
program exports

```elm
main : Effect String ()
```

and chooses one of three topologies:

| `main` calls           | What you get                                                    |
|------------------------|-----------------------------------------------------------------|
| `App.frontend pages`   | Browser MVU app (HTML shell + JS interpreter, hot reload).      |
| `App.backend routes`   | HTTP server (server-rendered HTML, REST endpoints, or both).    |
| `App.fullstack { api, pages }` | Unified server: API at `/api/*`, browser app at `/`.    |

The CLI enforces the signature: `mar dev` rejects `main` with any other type up
front. There is no `mar run` for one-off scripts and no low-level `Server.serve`
to drop down to — the topologies are the public API.

## Status

Working today, end to end:

- Lexer, parser, type checker (full HM with row polymorphism, custom types,
  cross-module type aliases). Cycle detection on non-function value
  declarations.
- Tree-walking interpreter with closures, currying, pattern matching
  (including cons / list patterns), custom-type constructors, records.
- Stdlib: `List`, `String`, `Maybe`, `Result`, `Effect`, `JSON`,
  `Entity`, `Repo`, `View`, `App`, `Page`, `Endpoint`, `Response`, `Http`.
- Multi-file projects with shared types and qualified names.
- `mar.json` manifest with strict schema, `env:VAR` references, secret
  enforcement.
- SQLite via `Entity` (record-literal schema declaration) and `Repo`
  (typed CRUD: `all`, `findById`, `findBy`, `create`, `update`,
  `deleteById`). DB is opened lazily from `mar.json`'s `database.path`;
  schemas auto-migrate on first use.
- Server-side view rendering (`View.render` produces HTML) — see
  `examples/view-page.mar`.
- Browser MVU runtime: a JS interpreter that loads the parsed AST and runs
  init / update / view client-side, with real DOM and event handlers. No page
  reloads.
- Hot-reload dev server (`mar dev`) with SSE-based reload events and an
  in-browser banner for compile errors.
- Strictly immutable REPL (`mar repl`) — rebinding is rejected; `:reset`
  starts a fresh session.

## Try it

```bash
go build -o mar ./cmd/mar

# Browser-only counter (MVU).
./mar dev examples/counter.mar

# Server-rendered page (View.render → HTML).
./mar dev examples/view-page.mar

# Full-stack app: backend + frontend in one process.
./mar dev examples/notes-fullstack
```

## Examples

| File                          | Demonstrates                                                        |
|-------------------------------|---------------------------------------------------------------------|
| `examples/counter.mar`        | Browser MVU: init / update / view, the classic counter.             |
| `examples/clock.mar`          | Browser MVU + `Http.get` to an external endpoint.                   |
| `examples/todo-app.mar`       | MVU with form input, togglable list.                                |
| `examples/tasks.mar`          | Larger MVU app exercising layout modifiers (padding, spacing, …).   |
| `examples/multi-screen.mar`   | Multiple `Page`s at different paths, each with its own model.       |
| `examples/view-page.mar`      | `App.backend` doing pure server-side rendering via `View.render`.   |
| `examples/guestbook/`         | SSR + persistence via `Entity` + `Repo`; HTML form posts back to server, no JS in the browser. |
| `examples/notes-fullstack/`   | `App.fullstack`: REST CRUD via `Endpoint.list` / `Endpoint.create` + `Repo.*`; SQLite backend; browser frontend. |

## CLI

```
mar dev [path]              Run the app in dev mode (hot reload, dev banner,
                            browser-open). <path> is a .mar file or a project
                            directory containing Main.mar; defaults to ".".
                            Watches *.mar / *.json under the project dir; on
                            change recompiles and swaps in the new program
                            atomically. Compile errors show in a red banner
                            in the browser; the previous good version keeps
                            serving.
mar build [dir] [distDir]   Compile a frontend project to a static dist/
                            (HTML + runtime.js + program.json) — host
                            anywhere static.
mar init <name>             Scaffold a new project (Main.mar + mar.json).
mar check <file>            Parse and type-check a file (no run).
mar repl                    Interactive read-eval-print loop (immutable).
mar format [--check] <f>... Reformat in place. With --check, exit 1 if any
                            file would change.
mar lsp                     Run the Language Server over stdio (consumed by
                            the VSCode extension under vscode-mar/).
mar config <dir>            Load and print mar.json from the given project.
mar version                 Print the version.
```

## Design

See `docs/mar.md` for the language reference and `docs/managed-effects.md`
for the rationale behind the effect / MVU model.

## Layout

```
cmd/mar/             CLI entry point
internal/lexer/      tokenizer
internal/parser/     parser
internal/ast/        AST types
internal/typecheck/  Hindley-Milner inference
internal/runtime/    tree-walking interpreter, stdlib, db, view, ...
internal/project/    multi-file loader, mar.json manifest
internal/jsserve/    dev server, hot reload, browser-side runtime
internal/lsp/        Language Server (used by editor extensions)
docs/                language reference
examples/            working programs
```

## Note

Parts of this project were developed with the assistance of generative AI tools.
