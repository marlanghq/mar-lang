# Deploying to Fly.io

`mar fly` wraps the full Fly.io deployment lifecycle behind a single
command. You configure the app once in `mar.json`; every subsequent
operation reads from there.

Fly's edge proxy terminates TLS for free, so HTTPS is automatic. No
reverse proxy or cert management to configure on your side.

## What you get out of the box

- **HTTPS** with auto-renewing Let's Encrypt certs at the edge.
- **Persistent SQLite** via a Fly volume mounted at `/data` inside
  the container; the runtime opens the DB from there. (Fullstack
  topologies only, frontend-only apps skip the volume.)
- **Global anycast** routing, pick a primary region; Fly handles
  the rest.
- **Auto-stop / auto-start**: your machine sleeps when idle and
  wakes on the next request. Cheap for low-traffic apps.
- **No Docker knobs.** The Dockerfile + `fly.toml` are generated on
  every deploy from the project's topology, deployed, and discarded.
  Customization happens in `mar.json`, not in handwritten infra files.

## One-time setup

Install the `fly` CLI from <https://fly.io/docs/flyctl/install/>.
That's it. `mar fly` triggers `fly auth login` for you the first
time you run a command that needs authentication.

## Configuring deploy.fly in mar.json

Every `mar fly` command reads from the `deploy.fly` block:

```json
{
  "name": "my-app",
  "deploy": {
    "fly": {
      "app":    "my-app-name",
      "region": "gru",
      "memory": "256mb"
    }
  }
}
```

All three fields are required:

| Field    | What it is                                                                 |
|----------|----------------------------------------------------------------------------|
| `app`    | Globally unique on Fly; becomes `<app>.fly.dev`. Lowercase, hyphens only. |
| `region` | One of Fly's region codes (`gru`, `iad`, `fra`, `nrt`, …). Pick close to your users. |
| `memory` | Machine size, `256mb`, `512mb`, `1gb`, `2gb`, `4gb`, or `8gb`.            |

If the block is missing or malformed, `mar fly deploy` (or any
other `mar fly *`) prints a paste-ready snippet plus the full list
of valid regions and memory sizes.

## First deploy

From your project directory (the one containing `mar.json`):

```sh
mar fly deploy
```

That's it. The single command:

1. Validates the `deploy.fly` block.
2. Detects topology (frontend / backend / fullstack) by running your
   `main`.
3. Logs into Fly if needed (`fly auth login` in your browser).
4. Creates the Fly app if it doesn't exist yet.
5. Creates the persistent volume (fullstack/backend only) on first
   run.
6. Prompts for any `env:VAR` secrets declared in `mar.json` that
   aren't already set on the Fly app, and pushes them.
7. Generates Dockerfile + `fly.toml` in `/tmp/mar-fly-*`.
8. Builds the linux-amd64 binary (or static `dist/` for
   frontend-only).
9. Runs `fly deploy`.
10. Polls the machine until it boots healthy, then opens the URL.

On success the temp directory is deleted; on failure it's preserved
so you can inspect what was generated.

First deploy takes 1–2 minutes (Docker layer cache miss). Subsequent
deploys are seconds.

## Subsequent deploys

```sh
git pull          # or just edit
mar fly deploy
```

The volume + secrets persist across deploys; only the binary layer
rebuilds.

To skip the auto-open-in-browser step (CI / SSH / headless):

```sh
mar fly deploy --no-open
```

`CI=true` in the environment also disables the auto-open.

## Previewing what'll be deployed

```sh
mar fly preview
```

Prints app name, region, memory, topology, target URL, volume, port,
and which `env:VAR` secrets are declared, without contacting Fly.
Useful for diffing config changes before running `deploy`.

## Operating the deployed app

```sh
mar fly logs       # tail logs from the running machine(s)
mar fly status     # show machines, regions, deploy history
```

Both resolve the app name from `mar.json` so you don't need
`-a <name>`. `mar fly logs` shows machine lifecycle + runtime boot
output; per-request data lives in the admin panel at `/_mar/admin`.

## Managing secrets

`mar fly deploy` handles bulk secret pushing automatically. For
targeted rotation / inspection:

```sh
mar fly secrets list             # cross-reference mar.json vs Fly
mar fly secrets set NAME=value   # push one (or NAME alone to prompt)
mar fly secrets unset NAME       # remove one
```

These validate against `mar.json`, `list` shows missing declared
refs and orphaned ones; `unset` warns if the name is still
referenced. See `mar fly secrets --help` for details.

## Database operations

```sh
mar fly database backup                  # force a snapshot now
mar fly database backups                 # list the catalog
mar fly database backup download <id>    # pull a snapshot locally
```

Backups land in the on-volume catalog at `/data/backups/<id>.tar.gz`,
the same place the runtime's auto-backup scheduler writes to.
Restore lives in the admin panel UI (`/_mar/admin`), it needs a
schema-match check + machine restart which is hard to express
tersely in CLI output.

## Admin inspection

```sh
mar fly admin list
```

Read-only view of production's `_mar_admins` table. Adding /
removing admins happens via `mar.json` + `mar fly deploy` (the
committed config is the source of truth).

## Migrations

Schema migrations run automatically on every boot. If you add an
entity field, the next `mar fly deploy` will:

1. Build with the new schema.
2. Boot the new container.
3. Migrator detects the live SQLite is missing the column, applies
   `ALTER TABLE ADD COLUMN`, records it in `_mar_schema_migrations`.
4. Container starts serving traffic.

Blocked migrations (e.g. adding NOT NULL to a populated table) make
the boot fail before the listener opens, so traffic continues
hitting the previous version. Fix the entity declaration, redeploy.

To preview what a deploy will migrate:

```sh
mar migrate plan
```

## Custom domain

```sh
fly certs create yourdomain.com -a <your-app>
```

Fly handles cert issuance + renewal. Then point a CNAME at
`<your-app>.fly.dev` (or follow Fly's docs for an apex-domain A
record).

## Removing the app

```sh
mar fly destroy
```

Asks for confirmation twice, first a yes/no, then it makes you
type the app name. Destroys both the app and its volume; data is
lost. The `deploy.fly` block in `mar.json` stays put; re-run
`mar fly deploy` to recreate.

## Frontend-only deploys

A `mar.json` without a backend / fullstack `main` deploys via Caddy
serving the static `dist/` output. No volume, no SQLite, no
runtime. `mar fly preview` shows a note about CDN alternatives,
the static bundle is portable to any of them, built with
`mar build`.

## Topology and Docker, under the hood

Mar abstracts Docker entirely. Every `mar fly deploy` regenerates
the Dockerfile + `fly.toml` in `/tmp/mar-fly-<random>/`, runs
`fly deploy` from there, and deletes the temp dir on success
(preserves on failure for debugging).

The generated images:

- **frontend-only**: `caddy:alpine` serving `dist/` as static files.
- **backend / fullstack**: `debian:bookworm-slim` + your
  linux-amd64 binary at `/app/<binary>`. SQLite is embedded;
  `/data` is mounted from the Fly volume.

If something the framework provides doesn't match what your app
needs, that's a feature request, not a customization knob. There
are no template files on disk to edit.
