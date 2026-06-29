# hello-auth

The smallest Mar app with authentication. Three pages, one entity,
zero business logic — just enough to show how email + magic-link
sign-in plugs into a Mar app.

## What it does

| Route | Page | What you see |
| --- | --- | --- |
| `/sign-in` | `SignIn` | Email input, submits to `Auth.requestCode` |
| `/sign-in/verify/{email}` | `VerifyCode` | 6-digit code input, submits to `Auth.verifyCode` |
| `/` | `Home` | "Hello, *email*" + sign-out button (protected) |

## Run it

```sh
mar dev examples/hello-auth
```

Open <http://localhost:8080>. You'll be bounced to `/sign-in`.

In dev mode magic codes are printed to the server log — copy-paste
the 6-digit value into the verify screen. No SMTP setup required.

## Structure

```
examples/hello-auth/
├── mar.json            -- project name
├── Main.mar            -- Auth.config + App.fullstack wiring
├── Shared.mar          -- User type (id + email)
├── Backend/
│   └── Users.mar       -- users entity (Entity.define)
└── Frontend/
    ├── Routes.mar      -- typed paths
    ├── SignIn.mar      -- email entry
    ├── VerifyCode.mar  -- code entry
    └── Home.mar        -- protected hello page
```

## Anatomy of the sign-in flow

The flow is split across two pages on purpose:

1. **`SignIn`** (`/sign-in`) — captures the email, calls
   `Auth.requestCode`, then **pushes** to the verify page carrying
   the email as a typed path segment.

2. **`VerifyCode`** (`/sign-in/verify/{email}`) — captures the code,
   calls `Auth.verifyCode`, then calls **`Auth.completeSignIn`** which
   returns the user to wherever a 401 redirected them from (or `/`
   by default) and clears the back stack.

Splitting the two screens (instead of using a single page with a
state machine) gives you the navigation back button for free: a
user who mistyped their email taps Back and gets a fresh email
field, no extra "Wrong email? Start over" link required.

## Going to production

In dev mode `mar dev` prints magic codes to the terminal — that's
why the example's `mar.json` only declares `name`. For a real
deployment two things change:

1. The mail codes have to be **emailed for real**, which means
   wiring an SMTP provider in `mar.json`.
2. A few secrets (session key, SMTP password) live as **`env:`
   references** — values get pushed to Fly via `mar fly provision`,
   so they never sit in git.

### Step 1 — Pick a mail provider

You need an SMTP-capable transactional email service. Any provider
works (SendGrid, AWS SES, Mailgun, Postmark, Brevo), but
**[Resend](https://resend.com)** is the path with least friction
for a small project:

- Free tier covers 3,000 emails/month — plenty for sign-ins
- API-key authentication (no SPF/DKIM dance to get started, though
  you'll want that later for your own domain)
- Their SMTP endpoint just works with Mar's settings below

The instructions below use Resend as the example. The other
providers differ only in `smtpHost` + `smtpUsername` / how they hand
out the password.

#### Get your API key

1. Sign up at <https://resend.com>
2. Verify a domain you own (Resend won't let you send from
   `@gmail.com`, `@outlook.com`, etc. — even with the API key,
   mail would bounce. Free Resend domains aren't an option either;
   you need a domain you control).
3. Add the DKIM / SPF records Resend shows you to your DNS
4. Generate an API key at <https://resend.com/api-keys>. Copy it
   somewhere safe — it starts with `re_...`.

### Step 2 — Expand `mar.json`

Replace the minimal `mar.json` with the production shape:

```json
{
  "name": "hello-auth",
  "database": {
    "path": "./hello-auth.db"
  },
  "auth": {
    "sessionSecret": "env:SESSION_SECRET"
  },
  "mail": {
    "from": "no-reply@yourdomain.com",
    "smtpHost": "smtp.resend.com",
    "smtpUsername": "resend",
    "smtpPassword": "env:SMTP_PASSWORD"
  }
}
```

Field-by-field:

| Field | Required? | What goes here |
|---|---|---|
| `auth.sessionSecret` | yes | `env:SESSION_SECRET` — a random 32+ byte string Mar uses to sign cookies. `mar fly provision` generates one for you if you press Enter at the prompt. |
| `mail.from` | yes | The "From:" address recipients see. Must be on the domain you verified with Resend. Don't use `@gmail.com` etc. — `mar build` rejects free-mail domains. |
| `mail.smtpHost` | yes | `smtp.resend.com` for Resend. For other providers: SendGrid → `smtp.sendgrid.net`, AWS SES → `email-smtp.<region>.amazonaws.com`, Postmark → `smtp.postmarkapp.com`. |
| `mail.smtpPort` | optional | Defaults to `587` (works for all the providers above). Set explicitly to `465` if your provider uses implicit TLS. |
| `mail.smtpUsername` | yes | `resend` (literal string) for Resend. SendGrid uses `apikey`; AWS SES uses your SMTP IAM username; Postmark uses your server token. |
| `mail.smtpPassword` | yes | `env:SMTP_PASSWORD` — the API key (Resend's `re_...`) or the provider-issued SMTP password. NEVER inline it; always via `env:`. |

Anything declared as `env:VAR_NAME` is a **reference**, not the
value. The actual value lives only on Fly (set via secrets) and in
your shell's environment for local prod tests. Mar's `mar build`
verifies every `env:` ref resolves before producing a binary.

### Step 3 — Deploy

```sh
mar fly init examples/hello-auth        # pick fly app name + region
mar fly provision examples/hello-auth   # prompts for SESSION_SECRET, SMTP_PASSWORD
mar fly deploy examples/hello-auth      # build + ship
```

`mar fly provision` will walk every `env:VAR_NAME` reference in
your `mar.json` and prompt with **echo turned off**:

```
SESSION_SECRET: ********    ← Enter to auto-generate
SMTP_PASSWORD:  ********    ← paste your re_... Resend API key
```

After the deploy completes you'll get a URL like
`https://hello-auth-yourname.fly.dev`. Sign-up there — the magic
code arrives in your inbox within a few seconds.

### Rotating secrets

To change a value later:

```sh
mar fly provision    # re-prompts every env: ref; press Enter to keep existing
```

Or for a single secret:

```sh
fly secrets set SMTP_PASSWORD=re_new_value -a hello-auth-yourname
```

Fly restarts the machine automatically when secrets change.

### What if I see "free-mail domain" errors?

`mar build` rejects `mail.from` values on shared providers
(`@gmail.com`, `@outlook.com`, `@yahoo.com`, ...) at compile time
— those would silently bounce in production because SMTP providers
only let you send from a domain you've verified via DKIM/SPF.

If you don't have a custom domain yet, the cheapest path is a `$10
/ year` Namecheap or Porkbun domain pointed at Resend.

### Useful docs

- [`docs/deployment-fly.md`](../../docs/deployment-fly.md) — full
  Fly deployment lifecycle
- [`docs/auth.md`](../../docs/auth.md) — auth config reference
- [Resend SMTP docs](https://resend.com/docs/send-with-smtp) —
  if you hit a provider-specific snag
