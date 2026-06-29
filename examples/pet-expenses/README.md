# Pet Expenses

A per-user expense tracker for pet owners, written in Mar as a fullstack app
(SQLite backend + a frontend that renders to web and iOS from the same code).

It is the worked example behind a simple question: *would a "track what I spend
on my pet" app pass App Store review?* The topic is fine — what matters is real
functionality (categories, totals) plus the account-management pieces Apple
requires.

## The model

Most pet spending — food, litter — is for **all** your pets at once, not one in
particular. So the app is built around the **expense feed**, not the pets:

- An expense belongs to you and has a category, amount, optional note, and a
  **pet tag that defaults to "All pets."** You only pick a specific pet for a
  one-off (a vet visit).
- Pets are just those tags, managed on their own screen.

## What it does

- **Email one-time-code sign in** (no passwords): enter email, then the 6-digit
  code.
- **Home = the expense feed**: a running total, a per-category breakdown, and a
  **by-month** report, then every expense newest-first (each row shows the date it
  was logged). "New expense" logs one (category, amount, optional note, and a pet
  picker that starts at "All pets"). Delete one with the native list gesture —
  swipe on iOS, hover-reveal on web — so no Delete button clutters each row.
- **Pets** (top-bar button → its own screen): add/remove pets (name + kind).
  Deleting a pet keeps its expenses; they move to "all pets" rather than being
  destroyed. Delete is tucked behind that screen's **Edit** toggle.
- **Settings** (top-bar button): the rare account actions — sign out and
  **delete account** (wipes your expenses, pets, and user row, then signs you
  out; App Store guideline 5.1.1(v)). Every destructive action goes through a
  confirmation dialog (`UI.confirm`).

Every read is scoped to the signed-in user, server-side.

## Layout

```
Shared.mar               types, service contracts, option lists, money + pet-label + date helpers
Backend/Users.mar        users entity (backs Auth)
Backend/Expenses.mar     expenses entity + handlers + pet re-tag / cascade helpers
Backend/Pets.mar         pets entity + CRUD (delete re-tags expenses to "all pets")
Backend/Account.mar      account deletion (expenses -> pets -> user)
Frontend/Routes.mar      typed URL surface
Frontend/SignIn.mar      email entry
Frontend/VerifyCode.mar  code entry (dynamic route, carries the email)
Frontend/Expenses.mar    HOME: expense feed + summary + add (Pets/Settings in the top bar)
Frontend/Pets.mar        pet management (add / list / delete under Edit)
Frontend/Settings.mar    account hub: sign out + delete account
Main.mar                 Auth.config + App.fullstack wiring
```

## Run it

```
mar dev examples/pet-expenses
```

In dev mode the sign-in code is printed to the server log, so you can sign in
without configuring SMTP. For production, set `SESSION_SECRET` + the mailer
credentials (`mar.json` `mail` block) and deploy with `mar fly deploy`.

## Notes

- **Money is stored as a whole currency unit** (an `Int`), formatted as `$N`.
  That keeps totals exact and the example small; a production app would store
  cents and format with a locale-aware currency formatter.
- **Each expense is dated.** The "New expense" form has a `datePicker` that
  defaults to today and can be changed, so you can backdate an expense. The
  chosen day is sent as `spentAt`; if you leave the picker untouched the backend
  stamps `Time.now`. The date drives the newest-first ordering, the date on each
  row, and the by-month report.
- **An expense's pet is a tag, not an owner.** `petId = 0` (`Shared.allPetsId`)
  means "all pets"; a real pet id (serials start at 1) means it was just for
  that one. There's no nullable column in Mar today, so the sentinel stands in
  for "no specific pet."
- **`mar.json` ships with placeholders** (`bundleId`, the mail `from` domain,
  the `SESSION_SECRET`/`SMTP_PASSWORD` env refs). Replace them before shipping.
- This covers the account-management bar for the App Store; a real submission
  also needs a privacy policy + the App Privacy labels (it collects an email), a
  real app icon, and a reviewer demo account.
