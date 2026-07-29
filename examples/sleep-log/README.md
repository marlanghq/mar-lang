# Sleep Log

Rate how you slept, one night at a time, and see what the last 30 add up to.
Your log is yours: sign in with a one-time code, and nobody else can read it.

```
mar dev examples/sleep-log
```

In dev there is no SMTP to configure. The sign-in codes are printed to the
server log, so you copy the six digits from your terminal.

## What it does

Pick a date, tap a rating, done. The date field opens on today, so logging last
night is two taps. Tapping a different rating for a date you already rated
**corrects** that night instead of adding a second one.

Rated a night by mistake? **Clear this night** appears once the chosen date has
a rating, and removes it.

The report below covers the last 30 nights:

- how many of the 30 you actually logged
- the typical night, named rather than scored (`Good`, not `3.6 / 5`)
- the current run of good nights
- how the nights split across the scale
- every logged night, newest first

The run counts **calendar days**, not list entries, so a gap breaks it: a week
you never logged is not a week you slept well. And if today has no entry yet it
counts from yesterday, because otherwise every morning would report a broken
streak before you had the chance to log anything.

## How it is built

| File | Holds |
| --- | --- |
| `Shared.mar` | the user, the scale, the `Night` record, the two service contracts |
| `Backend/Users.mar` | the users table Auth.config reads |
| `Backend/Nights.mar` | the Entity, and the handlers behind those contracts |
| `Frontend/SignIn.mar`, `Frontend/VerifyCode.mar` | the two halves of the code flow |
| `Frontend/Log.mar` | the screen |
| `Main.mar` | Auth.config, and the only module that sees both halves |

Three decisions are worth reading the code for.

**The caller is never a parameter.** Neither service takes a user. `Auth.protect`
injects the signed-in one on the server, so there is no field a client could set
to ask for somebody else's nights. Every read filters by that user, every write
stamps them.

**The unique index is composite, and it has to be.** `(userId, date)`, not
`date`. A unique index on the date alone would mean the first person to log the
20th locks that date for everyone else — a bug that only appears once the app
has a second user, which is exactly the kind that ships unnoticed.

**The date is the identity of a night.** That only works if every date agrees on
what a day is, which is why `startOfDay` rounds to midnight before storing or
matching. Without it, a morning entry and an evening entry on the same day are
two different keys, and the index cannot help.

One more, smaller: the report window is 30 **days**, not 30 rows. `inWindow`
filters by date from today. Taking the most recent 30 records instead would
stretch silently across a gap and call a month you never logged a good month.

The Entity lives in `Backend/` because it is server-only furniture, and keeping
it out of the page keeps it out of the browser bundle.

## Data

SQLite at `./sleep-log.db`, created on first run and gitignored. The schema is
derived from the records, so there is no migration to write by hand: the scale
becomes a `CHECK` constraint and the uniques become real indexes.

For production, set `mail.from` and the `smtp.*` env refs in `mar.json` so the
codes reach real inboxes.
