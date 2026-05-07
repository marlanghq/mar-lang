# CLI style guide

How `mar`'s command-line output should look. Two concerns: **spacing**
(blank lines around message blocks) and **color** (a small palette
applied consistently across every subcommand).

> **Status.** Proposal — incomplete in places. Please review §3 (color
> rules) and §4 (concrete examples) and tell me where the choices feel
> wrong before we sweep the rest of the CLI to match.

The goal is the same in both: when a user runs `mar foo`, the output
should be **scannable** — they should see what changed, where to look
next, and any warning at a glance, without parsing prose word by word.

## 1. Spacing

Three rules:

1. **Blank line before** every multi-line message block. Otherwise the
   block runs into the user's previous prompt or into the previous
   command's output.
2. **Blank line after** every multi-line message block. Lets the next
   prompt breathe.
3. **No blank line** for terse one-liners (single-line errors, simple
   confirmations like `(no change)`). Reserve the spacing budget for
   blocks that need visual separation.

Concrete: any output longer than two lines, or any output that ends
with hints / next-steps, gets blank lines on both sides. Single-line
output doesn't.

```
$ mar admin add me@example.com

mar admin add: me@example.com added to admins

  → mar.json updated
  → next deploy will sync this to _mar_admins on production

In development, the admin panel auth code prints to the terminal.
In production, codes are sent via the SMTP configured in mar.json["mail"].

The dev panel URL is http://localhost:3000/_mar/admin.

$
```

```
$ mar admin add me@example.com
mar admin add: me@example.com is already in admins
$
```

The second example stays one line — no surrounding blank lines because
there's nothing visual to separate from.

## 2. Color principles

- **Auto-disable** when stdout isn't a TTY (already implemented). ANSI
  codes in piped output are noise.
- **Respect `NO_COLOR=1`** (already implemented).
- **Carry meaning, not decoration.** Each color has a job; using red
  for "this looks dangerous" only works if red doesn't also appear in
  ten unrelated places.
- **Keep the palette small.** Six semantic slots (red / green / yellow
  / cyan / magenta / bold). More than that and users stop noticing.

## 3. Color rules — when to use which

| Color       | Used for                                                           | Examples                                                       |
|-------------|--------------------------------------------------------------------|----------------------------------------------------------------|
| **red**     | Error headlines, dangerous actions, destructive confirmations      | `Error:`, `mar fly destroy` warnings, failed validation        |
| **green**   | Success confirmations, *commands the user should run next*         | `✓ deployed`, `mar admin add YOUR_EMAIL`, `mar fly deploy`     |
| **yellow**  | Hints, recoverable warnings, "did you mean" suggestions            | `Hint:`, `warn:`, missing-config nudges                        |
| **cyan**    | Identifiers the user *typed or chose* — emails, app names, codes   | the email being added, `notes-app`, region codes (`gru`)       |
| **magenta** | File paths, env variable names                                     | `mar.json`, `deploy/fly/fly.toml`, `env:SESSION_SECRET`        |
| **bold**    | Section headers, key labels in interactive prompts                 | `Fly app name`, `Next steps:`, `Press Enter to use:`           |

### 3.1 Combining rules

- **Error + path**: red prefix, magenta path.
  `Error: failed to read mar.json` — `Error:` red, `mar.json` magenta.
- **Hint + command**: yellow prefix, green command.
  `Hint: try mar admin add me@example.com` — `Hint:` yellow, `mar admin add me@example.com` green.
- **Header + value**: bold header, cyan value.
  `Press Enter to use: notes-app` — `Press Enter to use:` bold, `notes-app` cyan.
- **List of identifiers** (e.g. `mar admin list`): each email cyan, no
  prefix coloring.

### 3.2 What NOT to color

- **Plain prose body text.** A green sentence is harder to read than a
  plain one. Color individual words (a path, a command, an identifier)
  inside an otherwise-plain sentence.
- **Bullet markers (`→`, `•`, `-`).** They're already visual; coloring
  them adds noise.
- **The literal commands the framework prints to the user** (i.e. the
  `mar fly init: deploy/fly already exists with content. Overwrite?`
  prompts). Those should stay plain — coloring the framework's own
  status lines competes for attention with the inline highlights they
  contain.

### 3.3 Color failure modes to avoid

- **"Status pill" overuse**: putting `[OK]` / `[FAIL]` markers on every
  line. Drowns the signal. Use a single colored verb at the start of
  the message instead.
- **Coloring user input** in interactive prompts (the bytes they're
  typing). The terminal already styles input how the user expects;
  we'd just override it badly.
- **Multiple colors in the same line** beyond the documented
  combinations above. Two colors in one line is the cap.

## 4. Concrete examples (after this guide is applied)

### 4.1 `mar admin add EMAIL` — happy path

```

mar admin add: me@example.com added to admins

  → mar.json updated
  → next deploy will sync this to _mar_admins on production

In development, the admin panel auth code prints to the terminal (no SMTP needed).
In production, codes are sent via the SMTP configured in mar.json["mail"].

The dev panel URL is http://localhost:3000/_mar/admin.

```

Coloring:
- `me@example.com` → **cyan** (user-typed identifier)
- `mar.json` and `mar.json["mail"]` → **magenta** (paths/keys)
- `_mar_admins` → **magenta** (DB table reference)
- `http://localhost:3000/_mar/admin` → **green** (URL the user should click/copy)

### 4.2 `mar admin list` — happy path

```

admins (from mar.json):
  me@example.com
  ops@example.com

```

Coloring:
- `mar.json` → **magenta**
- each email → **cyan**

### 4.3 `mar admin list` — empty

```

admins (from mar.json):
  (none)

Run mar admin add YOUR_EMAIL to enable the admin panel.

```

Coloring:
- `mar.json` → **magenta**
- `(none)` → **yellow** (it's a soft warning — your panel is locked)
- `mar admin add YOUR_EMAIL` → **green** (next-step command)

### 4.4 `mar admin add EMAIL` — invalid email (single-line error)

```
Error: mar admin add: "notanemail" is not a valid email
```

No surrounding blank lines (one-line error).

Coloring:
- `Error:` → **red**
- `"notanemail"` → **cyan** (user input being rejected)

### 4.5 `mar admin remove EMAIL` — happy path

```

mar admin remove: ops@example.com removed from admins

  → mar.json updated
  → next deploy will sync this to _mar_admins on production
  → existing admin sessions for ops@example.com will be revoked at next boot

```

Coloring:
- `ops@example.com` → **cyan** (twice — same identifier)
- `mar.json` and `_mar_admins` → **magenta**

### 4.6 No-op (already in list / not in list)

```
mar admin add: me@example.com is already in admins
```

```
mar admin remove: ghost@example.com is not in admins (nothing to do)
```

Both single-line, no blank lines around. The email gets **cyan**, no
other coloring.

## 5. Mapping to existing helpers

The `cmd/mar/color.go` helpers already exist:

```go
colorRed(s)     // status: errors, danger
colorGreen(s)   // status: success, "run this"
colorYellow(s)  // status: warnings, hints
colorCyan(s)    // identifiers (emails, names, codes)
colorMagenta(s) // paths, env names
colorBold(s)    // headers, key labels

errorPrefix()   // bold red "Error:"
hintPrefix()    // bold yellow "Hint:"
fprintError()   // stderr: errorPrefix + msg
fprintHint()    // stderr: hintPrefix + msg
```

Concrete usage pattern in admin.go:

```go
fmt.Println()
fmt.Printf("mar admin add: %s added to admins\n", colorCyan(email))
fmt.Println()
fmt.Println("  → " + colorMagenta("mar.json") + " updated")
fmt.Println("  → next deploy will sync this to " + colorMagenta("_mar_admins") + " on production")
fmt.Println()
// ... etc
fmt.Printf("The dev panel URL is %s.\n", colorGreen("http://localhost:3000/_mar/admin"))
fmt.Println()
```

## 6. Open questions

**Q1.** Should we color the `→` bullet character?
Tendency: no — it's already visually distinct, color would compete
with the colored identifier inside the line.

**Q2.** Should hints use a literal `Hint:` prefix or just yellow body
text?
Tendency: keep `Hint:` — the prefix is what makes the recoverability
explicit. Yellow body alone reads like a generic warning.

**Q3.** Do we ever bold an inline word for emphasis (vs. only colors
for inline)?
Tendency: very rarely. Bold is reserved for section headers; mixing it
with inline color emphasis competes for attention.

**Q4.** What about output meant to be parsed by tools (e.g. `mar admin
list` piped to `jq`)?
Already handled: NO_COLOR / non-TTY auto-disable. No JSON-output flag
for v1; if a user wants machine-readable, they can read `mar.json`
directly.
