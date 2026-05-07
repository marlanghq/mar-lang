# Deploying to Fly.io

The `mar fly` subcommand wraps the full Fly.io deployment lifecycle.
You don't have to invoke the `fly` CLI directly — every step is
behind a `mar fly` command.

Fly's edge proxy terminates TLS for free, so HTTPS is automatic. No
reverse proxy or cert management to configure on your side.

## What you get out of the box

- **HTTPS** with auto-renewing Let's Encrypt certs at the edge.
- **Persistent SQLite** via a fly volume mounted at `/data` inside
  the container; the runtime opens the DB from there via
  `MAR_DATABASE_PATH`.
- **Global anycast** routing — pick a primary region; fly handles
  the rest.
- **Auto-stop / auto-start** — your machine sleeps when idle and
  wakes on the next request. Cheap for low-traffic apps.

## One-time setup

Install the `fly` CLI from <https://fly.io/docs/flyctl/install/>.
That's it. `mar fly` triggers `fly auth login` for you the first
time you run a command that needs authentication.

## First deploy

From your project directory (the one containing `mar.json`):

```sh
mar fly init        # scaffold deploy/fly/{Dockerfile,fly.toml}
mar fly provision   # create the fly app + volume + secrets
mar fly deploy      # build linux-amd64 binary and ship it
```

Three commands; nothing else. Each step prints what it's doing and
the resource names involved.

### What each does

**`mar fly init`** is interactive. It prompts for:

1. **Fly app name** — defaults to your `mar.json` `name` (slugified
   to fly's lowercase-and-hyphens format). Press Enter to accept.
2. **Region** — shows a continent-grouped table of supported fly
   regions; type the code (e.g. `gru`, `iad`, `fra`).

Then it generates two files:

```
deploy/fly/
├── Dockerfile     # debian-slim + your linux-amd64 binary
└── fly.toml       # app name, region, port, env vars, volume mount
```

Both go into git — they're per-project configuration.
`shared-cpu-1x` + 256MB (the default in the template) is the
cheapest tier; bump if your traffic warrants it.

Re-running `mar fly init` against an existing `deploy/fly/` asks
before overwriting.

**CI / scripted init**: set `FLY_REGION=<code>` in the environment
to skip the region prompt. The app name is taken from `mar.json`
silently when stdin isn't a TTY.

**`mar fly provision`** runs four steps after a confirmation:

1. `fly auth whoami` — checks if you're logged in. If not, runs
   `fly auth login` (browser-based OAuth flow).
2. `fly apps create <name>` — creates the app on fly. **Fails if
   the app name is already taken.** If you want to recreate, run
   `mar fly destroy` first.
3. `fly volumes create <name>_data --region <region> --size 1` —
   creates the persistent SQLite volume. **Fails if the volume name
   already exists** under the same app.
4. **Secret prompts**. For each `env:VAR_NAME` reference in your
   `mar.json`, the wrapper prompts for the value (echo off) and
   pushes it via `fly secrets set`. Empty input skips that one.

If your `mar.json` doesn't reference any env: secrets, step 4 is a
no-op message.

The "fail loudly on already-exists" behavior is deliberate:
silently reusing whatever's already on fly tends to hide
misconfiguration. If you're attaching to an existing fly app
(rather than creating fresh), skip `mar fly provision` and edit
`deploy/fly/fly.toml` by hand instead.

**`mar fly deploy`** runs:

1. `mar build --target linux-amd64 --out deploy/fly/dist` — produces
   the self-contained Linux binary. The binary embeds every `.mar`
   source + `mar.json`; the SQLite driver is statically linked.
2. `fly deploy` from `deploy/fly/` — builds the Docker image and
   ships it to your fly app.

First deploy takes 1–2 minutes (Docker layer cache miss).
Subsequent deploys are seconds.

## Subsequent deploys

After the first deploy, the loop is:

```sh
git pull          # or just edit
mar fly deploy
```

That's it. The volume + secrets persist across deploys; only the
binary layer rebuilds.

## Operating the deployed app

```sh
mar fly logs       # tail logs from the running machine(s)
mar fly status     # show machines, regions, deploy history
```

Both pass through to the equivalent `fly logs` / `fly status`
commands; the wrapper just resolves the app name from your project's
`fly.toml` so you don't have to type `-a <name>` every time.

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
hitting the previous version. Fix the entity declaration (make the
field optional, or do a manual SQL backfill), redeploy.

To preview what a deploy will migrate:

```sh
mar migrate plan        # against your local DB
```

## Updating secrets

Rotate a key, change an SMTP password? Re-run provision:

```sh
mar fly provision
```

It walks the same prompts and `fly secrets set` is the same
operation that pushes new values. The next deploy (or fly's automatic
machine restart on secret change) picks them up.

You can also push a single secret directly:

```sh
fly secrets set RESEND_API_KEY=re_xxx -a <your-app>
```

`mar fly provision` is just the bulk path.

## Custom domain

```sh
fly certs create yourdomain.com -a <your-app>
```

Fly handles cert issuance + renewal. Then point a CNAME at
`<your-app>.fly.dev` (or follow fly's docs for an apex-domain A
record).

## Removing the app

```sh
mar fly destroy
```

Asks for confirmation twice — first a yes/no, then it makes you
type the app name. Destroys both the app and its volume; data is
lost.

After destroy, the deploy/fly/ files stay where they are. Run
`mar fly provision` to recreate, or delete deploy/fly/ if you're
walking away from fly entirely.

## Customization

The generated `Dockerfile` and `fly.toml` are starting points. Edit
freely — `mar fly init` won't overwrite without asking. Common
tweaks:

- **More memory** — bump `[[vm]] memory = "512mb"` or higher.
- **Multiple regions** — `fly.toml` supports a `primary_region` plus
  fly handles overflow. For active-active multi-region SQLite you'd
  need [LiteFS](https://fly.io/docs/litefs/) (out of scope for the
  generated config).
- **Health checks** — add `[[http_service.checks]] path = ...` to
  fly.toml. The framework doesn't ship a `/health` endpoint yet
  (BACKLOG item); `/_auth/whoami` is a workable proxy.
