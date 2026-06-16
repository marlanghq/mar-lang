# team-notes — Authorization Example

A small multi-tenant SaaS demonstrating mar's authentication +
authorization primitives:

- **Authentication**: passwordless email-code login (`Auth.config`,
  `Auth.requestCode`, `Auth.verifyCode`, `Auth.me`, `Auth.protect`)
- **Authorization** (this example's focus):
  - `Auth.requireRole` — RBAC gate
  - `Auth.authorize`   — ABAC gate via policy function
  - `Auth.requireOwner` — sugar for "user owns this resource"

Every user belongs to a Team and is either a `Member` or `Admin`
within it. Members can read + create team notes; admins can
additionally edit + delete them. Roles are a real type
(`type Role = Member | Admin`), persisted via `Entity.enum` with a
SQL `CHECK` constraint so misspellings are caught at compile time
*and* at the database layer.

## Run it

```sh
mar dev examples/team-notes
```

Then in another terminal, drive the API directly:

```sh
# 1. Sign up alice (auto-promoted to "member" of team 1 on first login).
curl -X POST localhost:3000/_auth/request-code \
  -H 'Content-Type: application/json' \
  -d '{"email":"alice@team.com"}' -c /tmp/alice.cookie
# code prints in the dev server terminal — copy it
curl -X POST localhost:3000/_auth/verify-code \
  -H 'Content-Type: application/json' \
  -d '{"email":"alice@team.com","code":"<paste>"}' \
  -b /tmp/alice.cookie -c /tmp/alice.cookie

# 2. Create a note (200, allowed).
curl -X POST localhost:3000/team-notes \
  -H 'Content-Type: application/json' \
  -d '{"body":"hello"}' -b /tmp/alice.cookie

# 3. Try to delete it as alice — 403, members can't delete.
curl -X DELETE localhost:3000/team-notes/1 -b /tmp/alice.cookie

# 4. Promote alice to admin and retry — 200.
sqlite3 examples/team-notes/team-notes.db \
  "UPDATE users SET role='Admin' WHERE email='alice@team.com';"
curl -X DELETE localhost:3000/team-notes/1 -b /tmp/alice.cookie

# 5. Try to write a bogus role directly via SQL — DB rejects.
sqlite3 examples/team-notes/team-notes.db \
  "INSERT INTO users (email, teamId, role) VALUES ('x@y.com', 1, 'Owner');"
# Error: CHECK constraint failed: role IN ('Member', 'Admin')
```

## Reading the security policy

Open `Backend/Notes.mar` and scroll to `services = [...]`. The wiring
reads as a security policy table:

```mar
services =
    [ Auth.protect Shared.listTeamNotes  listTeamNotes
    , Auth.protect Shared.createNote     createNote

    , Auth.protect Shared.editNote editNote
        |> Auth.authorize loadEditTarget sameTeam

    , Auth.protect Shared.deleteNote deleteNote
        |> Auth.authorize    loadDeleteTarget sameTeam
        |> Auth.requireRole  Admin
    ]
```

| Service       | Who can call                                | Failure modes |
|---------------|---------------------------------------------|---------------|
| listTeamNotes | any authed user (sees only their team)      | 401 |
| createNote    | any authed user (created in their team)     | 401 |
| editNote      | any authed user IF the note is in their team| 401 / 404 / 403 |
| deleteNote    | admins IF the note is in their team         | 401 / 404 / 403 |

The wiring tells the security story without the reader needing to
read a single handler body.

## How the gates work

When a request hits `DELETE /team-notes/1`, the dispatcher
runs three gates in order:

1. **`Auth.protect`** — validates the session cookie. No session →
   `401 not authenticated`. With session, the User is loaded.
2. **`Auth.authorize loadDeleteTarget sameTeam`** — runs the loader;
   if the row doesn't exist → `404 not found`. If it does, runs
   the policy with `(input, user, resource)`. False → `403 forbidden`.
3. **`Auth.requireRole Admin`** — extracts `user.role` via the
   `role` getter from `Auth.config`; compares structurally with
   `Admin`. Mismatch → `403 forbidden`. Because `Admin` is a real
   constructor, `Auth.requireRole Adimn` is a compile error.

Only if all three pass does the handler run. The handler itself has
**zero** auth code — it just edits or deletes the note.

## Frontend gating

`Frontend/Home.mar` is mounted via `Page.protected`, not
`Page.create`:

```mar
page = Page.protected
    { path = "/"
    , title = "Team Notes"
    , init = init, update = update, view = view
    }
```

The redirect target for unauthed users isn't declared per page.
It comes from `Auth.config { signInPage = Frontend.SignIn.page }`
in `Main.mar` — one place for the whole app. Renaming SignIn's path
or moving it to a different module updates the redirect everywhere
because `signInPage` is a `Page` reference, not a string.

## Session expiry

`Auth.config { sessionDuration = Time.days 30 }`. The framework reads
this as the cookie's `Max-Age`. `Time` is a typed `Duration` —
`Time.seconds N`, `Time.minutes N`, `Time.hours N`, `Time.days N`,
`Time.weeks N` — so the unit at the call site is always self-evident
(no `2592000` magic numbers).

## Files

```
team-notes/
├── mar.json                     -- { "name": "team-notes" }
├── Main.mar                     -- Auth.config (with `role` getter) + App.fullstack
├── Shared.mar                   -- type Role = Member | Admin; User, Note + contracts
├── Frontend/
│   ├── SignIn.mar               -- /sign-in   (Page.create, public)
│   └── Home.mar                 -- /          (Page.protected, role-aware UI)
└── Backend/
    ├── Users.mar                -- users entity (Entity.enum [Member, Admin])
    └── Notes.mar                -- notes entity + handlers + services with decorators
```

## See also

- `docs/authorization-proposal.md` — full design rationale, rejected
  alternatives, implementation map.
- `examples/notes-auth-multipage/` — simpler example with auth but
  no authorization (per-user filter only, no roles or ABAC).
