# Backup smoke test — production checklist

Manual verification to run **once**, against a real Fly deployment,
before declaring auto-backup MVP-ready. The Go test suite covers the
local code paths; this checklist exercises the assumptions about
Fly's behavior that aren't testable from your laptop.

Estimated time: **~30 minutes**, plus deploy time for two builds.

## Prerequisites

- [ ] A Fly app deployed via `mar fly deploy` (any small project —
      even the scaffolded starter works).
- [ ] `mar.json` configured with at least one admin in `admins[]`
      (so you can log into the panel).
- [ ] `mar.json` has the database block with auto-backup enabled
      (the default — no need to add anything; just confirm it's
      not disabled):

      ```json
      {
        "database": {
          "autoBackup": { "enabled": true, "intervalHours": 6 }
        }
      }
      ```

      For the smoke test, override `intervalHours: 1` so you don't
      have to wait 6 hours for the first auto-backup to fire.

## Phase 1 — auto-backup runs in production

**Goal**: confirm the goroutine actually starts on Fly and writes
files to the volume.

1. [ ] Deploy with the 1h-interval auto-backup config:
       `mar fly deploy`
2. [ ] Wait ~70 seconds (1s grace + a small slack).
3. [ ] Check the catalog landed:
       `fly ssh console --app YOUR_APP -C "ls -la /data/backups/"`

   Expected: at least one `<timestamp>.tar.gz` file. The auto-
   backup goroutine logs to stderr; check `fly logs` for a line
   like `[mar auto-backup] took 2026-... 1.1 KB`.

4. [ ] Force a manual snapshot via CLI:
       `mar fly database backup`

   Expected output ends with "snapshot created in catalog: <id>".
   Verify the new file landed:
   `fly ssh console --app YOUR_APP -C "ls -la /data/backups/"`.

5. [ ] List the catalog:
       `mar fly database backups`

   Expected: at least 2 entries, newest first.

**Failure mode**: if `/data/backups/` doesn't exist or is empty,
the goroutine is failing silently. Check `fly logs` for
"could not create catalog dir" — usually permissions or volume
not mounted.

## Phase 2 — restore via CLI on the server

**Goal**: confirm `mar-runtime restore-db` swaps the DB on a real
Fly volume and the machine comes back up with the restored state.
(Restore is deliberately CLI-only — there is no panel button.)

1. [ ] Open the admin panel and note the live database state in the
       **Database** / **Tables** sections — pick something specific
       to verify the restore is real (e.g. count of rows in some
       table).
2. [ ] SSH in: `fly ssh console --app YOUR_APP`
3. [ ] Dry-run against a backup taken BEFORE the state you noted:

       ```
       mar-runtime restore-db /data /data/backups/<id>.tar.gz --dry-run
       ```

   Expected: plan output (bundle metadata, fingerprints, swap
   steps), no prompt, no changes.

4. [ ] Run it for real. Because the server holds the DB flock, the
       restore should first REFUSE with "a server is running" —
       that's the lock check working. Stop the machine's server
       process (or `fly machine stop` + start a console-only
       machine, depending on your setup) and run again:

       ```
       mar-runtime restore-db /data /data/backups/<id>.tar.gz
       ```

5. [ ] At the prompt, type the literal word `restore`.
6. [ ] Restart the machine (`fly machine restart`), reopen the
       panel, log back in (sessions are wiped on DB swap).
7. [ ] Check the restored state matches what was in that backup
       (the row counts you noted should be DIFFERENT now).
8. [ ] Verify the `.bak` was preserved:
       `fly ssh console --app YOUR_APP -C "ls -la /data/mar.db*"`

   Expected: `mar.db` (the restored snapshot), `mar.db.bak-<TS>`
   (your pre-restore state).

**Failure modes to watch for**:
- Restore runs while the server is up → the flock check failed;
  that's a bug, file it. It must refuse.
- Restore appeared to succeed but data didn't change → the machine
  didn't restart, so the running process kept the OLD inode open.
  Check `fly status` for restart count, restart, re-check.

## Phase 3 — schema mismatch refusal

**Goal**: confirm the schema fingerprint check refuses incompatible
backups instead of silently corrupting data.

1. [ ] Take a backup of the current state:
       `mar fly database backup`
2. [ ] In your local mar source, add a new entity (e.g. a new
       table) and deploy:

       ```mar
       newTable : Entity Foo
       newTable = Entity.define { ... }
       ```

       `mar fly deploy`

3. [ ] Try to restore the older backup (taken BEFORE the new
       entity) with `mar-runtime restore-db` over SSH, as in
       Phase 2.

   Expected: hard stop with the schema-mismatch message ("this
   backup was taken against a different schema"). No prompt is
   reached, the .bak is NOT created, database state unchanged.
   There is no --force; that's by design.

## Phase 4 — download for cold storage

**Goal**: confirm the download flow works end-to-end.

1. [ ] In the panel, click **Download** on any backup. The browser
       saves a `<timestamp>.tar.gz` file.
2. [ ] CLI download: `mar fly database backup download <id>`.
       File lands in `./backups/<id>.tar.gz` locally.
3. [ ] Inspect the bundle locally:

       ```
       tar -xzOf backups/<id>.tar.gz metadata.json | jq
       ```

       Verify metadata fields: `appName`, `marVersion`,
       `schemaFingerprint`, `envRefs` are sensible.

4. [ ] Ensure secrets are NOT inside the bundle:

       ```
       tar -xzOf backups/<id>.tar.gz mar.json | grep -i password
       ```

       Expected: `"smtpPassword": "env:..."` (literal env ref, not
       a resolved secret value).

## Sign-off

When all four phases pass, the auto-backup MVP is ready. The
remaining concerns I called out in the audit (`docs/admin-panel.md`
near the backup section) are non-blocking polish:

- **Multi-machine Fly setups** — the catalog is per-volume.
  Single-machine apps work fully; horizontal scaling needs
  separate design before this is safe to use as a recovery
  strategy.
- **Bundle versioning** — `metadata.json` doesn't yet carry a
  format version. Adding one is cheap and prevents future
  format changes from silently breaking old backups.

If anything in phases 1–3 fails, *don't ship*. File the failure
case and we adjust before declaring MVP-ready.
