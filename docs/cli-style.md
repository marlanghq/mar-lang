# CLI style guide

How `mar`'s command-line output should look. Two concerns: **spacing**
(blank lines around message blocks) and **color** (a small palette
applied consistently across every subcommand).

> **Status.** Proposal, incomplete in places. Please review §3 (color
> rules) and §4 (concrete examples) and tell me where the choices feel
> wrong before we sweep the rest of the CLI to match.

The goal is the same in both: when a user runs `mar foo`, the output
should be **scannable**: they should see what changed, where to look
next, and any warning at a glance, without parsing prose word by word.

## 1. Spacing

The simplest formulation:

> **Blank line BEFORE every multi-line block. Blank line AFTER only
> when the block is the LAST thing the command will print before
> exiting (returning control to the user's shell).**

This is what the rest of the rules reduce to. Why:

1. **Blank BEFORE always**: separates the block from whatever
   preceded it (a previous block, a user's earlier command output,
   or the shell prompt). Without it, blocks visually fuse.

2. **Blank AFTER only at end of process**: when control returns to
   the shell, a trailing blank gives the prompt room to breathe.
   Without it, the prompt smashes against the last output line:
   ```
   The dev panel URL is http://localhost:3000/_mar/admin.
   $
   ```
   With it:
   ```
   The dev panel URL is http://localhost:3000/_mar/admin.

   $
   ```

3. **No blank AFTER mid-process**: if another block follows in the
   SAME run (e.g. a boot-time hint followed by the dev banner),
   adding blank-after-each gives TWO blanks between them, reads
   as a gap, not a separator. Only the FOLLOWING block adds its
   own leading blank; the preceding one stays tight.

4. **No blank lines mid-pipeline** for terse one-liners that aren't
   the final output, e.g. a single-line error that exits before any
   following block would have printed. But a one-liner that's the LAST
   thing a command prints still takes §1.1 + §1.2's blanks: it's
   handing control back to the shell, so even a terse confirmation
   (`already in admins`, `(no change)`) gets framed rather than
   smashed against the prompt.

### Concrete mapping

| Output type | Blank BEFORE | Blank AFTER |
|---|---|---|
| `mar admin add EMAIL` (happy path, one-shot) | ✓ | ✓ (returns to shell) |
| `mar admin add EMAIL` (single-line "already in") | ✓ | ✓ (returns to shell) |
| `mar dev` boot-time hint (followed by banner) | ✓ | ✗ |
| `mar dev` startup banner (followed by stable runtime) | ✓ | ✓ (Press Ctrl+C output, etc.) |
| `mar build` production warn (followed by build output) | ✓ | ✗ |
| `Error:` / `Hint:` blocks (one-shot exit) | ✓ | ✓ (handled by `fprintError`/`fprintHint`) |

### How the helpers enforce this

`fprintError`/`fprintHint`/`fprintWarn` in `cmd/mar/color.go` add the
leading AND trailing blank automatically, call sites don't have to
remember. When two helpers chain (Error → Hint), the second one's
leading blank is suppressed via a package-level state flag so the
pair shows ONE blank between, not two.

Multi-line hints (continuation text under the same `Hint:` block)
go into the format string with `\n      ` separators, NOT into
separate `fmt.Fprintf` calls. If you split them across raw stderr
writes, the helper's trailing blank lands between the Hint header
and the continuation, breaking the block visually.

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

The second example stays one line, no surrounding blank lines because
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

## 3. Color rules, when to use which

**Each color has exactly one role.** When in doubt, leave it
uncolored or dim. Reusing a color for "things that aren't quite
its role" dilutes the signal, readers stop trusting the cue.

| Color       | Role                                                                          | Examples                                                       |
|-------------|-------------------------------------------------------------------------------|----------------------------------------------------------------|
| **red**     | Errors, dangerous actions, destructive confirmations                          | `Error:`, `mar fly destroy` warnings, failed validation        |
| **green**   | **Executables**: commands the user should run                                | `mar admin add YOUR_EMAIL`, `mar fly deploy`, `lsof -ti:3000`  |
| **yellow**  | Hints, recoverable warnings, "did you mean" suggestions                       | `Hint:`, `Warn:`, missing-config nudges                        |
| **cyan**    | **Links and addressable identifiers**: URLs, paths in URLs, emails, names    | `http://localhost:3000`, `/_mar/admin`, emails, app names, region codes |
| **magenta** | **Paths and config keys**: file paths, env vars, db tables, config slots    | `mar.json`, `_mar_admins`, `env:SESSION_SECRET`, `mar.json["server"]["port"]` |
| **bold**    | Section headers, key labels                                                   | `Fly app name`, `Next steps:`, `Press Enter to use:`           |
| **dim**     | Auxiliary / status text that isn't itself a value                            | "Loading…", "Hot reload enabled.", `Local:` / `Admin:` labels |

### 3.1 Distinguishing close-but-distinct cases

Some pairs sit near each other; here's how to tell them apart:

- **green vs cyan**: green = "you can RUN this" (a command). cyan
  = "you can OPEN/REFERENCE this" (a URL, an email, an app name).
  `mar admin add YOUR_EMAIL` is **green** (run it). Its URL output
  `http://localhost:3000/_mar/admin` is **cyan** (open it).

- **cyan vs magenta**: cyan = the live, addressable surface (URLs,
  emails, app names, things you act on or send messages to).
  magenta = the configuration surface (filesystem paths inside the
  project, env var names, framework table names, things you grep
  for or edit). Rule of thumb: if you'd paste it into a browser
  or send it to someone, cyan. If you'd grep your codebase for it,
  magenta.

- **dim**: anything that's not itself a value, label, or command.
  Status descriptors ("Hot reload enabled.", "Loading…"), footer
  hints ("Save any .mar file to rebuild."), separator labels
  (`Local:`). Dim says "I'm context, not the thing".

### 3.1 Combining rules

- **Error + path**: red prefix, magenta path.
  `Error: failed to read mar.json`, `Error:` red, `mar.json` magenta.
- **Hint + command**: yellow prefix, green command.
  `Hint: try mar admin add me@example.com`, `Hint:` yellow, `mar admin add me@example.com` green.
- **Header + value**: bold header, cyan value.
  `Press Enter to use: notes-app`, `Press Enter to use:` bold, `notes-app` cyan.
- **List of identifiers** (e.g. `mar admin list`): each email cyan, no
  prefix coloring.

### 3.2 What NOT to color

- **Plain prose body text.** A green sentence is harder to read than a
  plain one. Color individual words (a path, a command, an identifier)
  inside an otherwise-plain sentence.
- **Bullet markers (`→`, `•`, `-`).** They're already visual; coloring
  them adds noise.
- **The literal commands the framework prints to the user** (i.e. the
  `mar fly destroy: type the app name to confirm` prompts). Those
  should stay plain, coloring the framework's own status lines
  competes for attention with the inline highlights they contain.

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

### 4.1 `mar admin add EMAIL`, happy path

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

### 4.2 `mar admin list`, happy path

```

admins (from mar.json):
  me@example.com
  ops@example.com

```

Coloring:
- `mar.json` → **magenta**
- each email → **cyan**

### 4.3 `mar admin list`, empty

```

admins (from mar.json):
  (none)

Run mar admin add YOUR_EMAIL to enable the admin panel.

```

Coloring:
- `mar.json` → **magenta**
- `(none)` → **yellow** (it's a soft warning, your panel is locked)
- `mar admin add YOUR_EMAIL` → **green** (next-step command)

### 4.4 `mar admin add EMAIL`, invalid email (single-line error)

```
Error: mar admin add: "notanemail" is not a valid email
```

No surrounding blank lines (one-line error).

Coloring:
- `Error:` → **red**
- `"notanemail"` → **cyan** (user input being rejected)

### 4.5 `mar dev`, port already in use (multi-line error with hint)

Runtime errors that block the process from starting deserve an
actionable hint, not just the raw Go error string. The structure
mirrors `Error: <one line>` followed by `Hint: <how to fix>` with
the relevant fix-it command and config path inline.

```
Error: port 3000 is already in use.

Hint: another process (perhaps another mar dev?) is bound to this port.
      free it with lsof -ti:3000 | xargs kill,
      or change mar.json["server"]["port"] to something else.

```

Coloring:
- `Error:` → **red**
- `Hint:` → **yellow**
- `mar dev` (the second occurrence, identifying the likely culprit) → **green**
- `lsof -ti:3000 | xargs kill` → **green** (command the user should run)
- `mar.json["server"]["port"]` → **magenta** (config path)

Apply the same shape to any runtime error that has a known fix:
turn it into `Error: <plain-language summary>` + `Hint: <fix>`. Raw
Go error strings are acceptable for genuinely unexpected failures
(via `fprintError("mar dev: %v", err)`), but anything we can predict
should be friendlier.

### 4.5 `mar admin remove EMAIL`, happy path

```

mar admin remove: ops@example.com removed from admins

  → mar.json updated
  → next deploy will sync this to _mar_admins on production
  → existing admin sessions for ops@example.com will be revoked at next boot

```

Coloring:
- `ops@example.com` → **cyan** (twice, same identifier)
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
Tendency: no, it's already visually distinct, color would compete
with the colored identifier inside the line.

**Q2.** Should hints use a literal `Hint:` prefix or just yellow body
text?
Tendency: keep `Hint:`, the prefix is what makes the recoverability
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
