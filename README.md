# Mar

A friendly language for fullstack apps.

Mar is a small, statically typed functional language for the whole stack:
web, iOS, Android, and the backend. One codebase, four targets. The
compiler emits the right output for each.

→ [mar-lang.dev](https://mar-lang.dev)

## What's in the box

- **Authentication, authorization, admin panel, migrations, database,
  backups**: all built into the language.
- **Native UI on iOS and Android, polished CSS on the web**: one UI
  vocabulary (`list`, `sheet`, `navigationStack`, …), composed once,
  rendered natively on each platform.
- **Type-safe end-to-end**: Hindley–Milner inference applied across
  pages, services, routes, and schemas.
- **One small binary** that runs, types, formats, and ships your
  project.
- **Over-the-air updates**: deploy on the server, clients pick up the
  new version without app store waits.

## Quick start

The normal way: head to
[mar-lang.dev/get-started](https://mar-lang.dev/get-started) for install
instructions and a walkthrough.

### Building from source

For the latest unreleased version, build the CLI yourself:

```bash
# Build the CLI.
go build -o mar ./cmd/mar

# Scaffold a new project.
./mar init hello

# Run the dev server (hot reload, opens browser).
./mar dev hello
```

Open `hello/Main.mar` and start editing.

## Examples

Reference projects live in [`examples/`](examples/). A few highlights:

- `examples/counter.mar`: smallest possible MVU app
- `examples/todo-app.mar`: lists, filters, local state
- `examples/hello-auth/`: passwordless email auth
- `examples/daily-checklist/`: reorder, delete, undo, per-user storage
- `examples/team-notes/`: multi-user, roles, reactions, mentions
- `examples/mar-website/`: the [mar-lang.dev](https://mar-lang.dev) site
  itself

## Status

Mar has no stable release yet. Language syntax and DB schema formats can
change between versions. Use it for personal projects and prototypes;
expect breaking changes.

See [Why Mar](https://mar-lang.dev/why) for what it is, what it isn't,
and the trade-offs.

## Docs

- Website: [mar-lang.dev](https://mar-lang.dev)

## License

MIT. See [`LICENSE`](LICENSE).

---

Parts of this project were developed with the assistance of generative AI tools.
