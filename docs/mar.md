# Mar

Mar is a full-stack functional language for building typed web and mobile apps with a single source of truth. Backend and frontend are written in the same language, share types end-to-end, and compile together.

The syntax is Elm-style. The semantics are pure functional with effects tracked in types. Validation, authentication, and data flow are all checked at compile time.

## 1. Overview

### 1.1 Philosophy

- **Pure by default.** Side effects are values, a `Task a` on the backend and a `Cmd msg` on the frontend, that the runtime runs. User code describes; runtime acts.
- **Compile-time correctness over runtime checks.** Types catch as much as possible.
- **No hidden magic in user code.** Magic is allowed only at the boundary (HTTP encode/decode, schema migrations, etc.), never in the middle of business logic.
- **High level by default.** Low-level escape hatches are added only when proven necessary.
- **Single source of truth.** A change in the backend is immediately visible to the frontend; no codegen step.

### 1.2 What Mar is

- A language for typed CRUD apps, internal tools, dashboards, and small/medium SaaS.
- Backend (HTTP server + database) and frontend (web + iOS) in one codebase.
- Statically typed with Hindley-Milner inference, row polymorphism, and exhaustive pattern matching.

### 1.3 What Mar is not

- Not a systems language (no manual memory management, no FFI to arbitrary C).
- Not a general-purpose UI framework (the view vocabulary is opinionated, not free-form).
- Not a runtime-dynamic language (everything checked at compile time).

## 2. Project Structure

### 2.1 Layout

```
project/
  mar.json              -- manifest + config
  Main.mar              -- entry point: App.fullstack { services, pages }
  Shared.mar            -- types + Service contracts shared by both halves
  Backend/
    Users.mar           -- entities + service handlers
    Tasks.mar
  Frontend/
    Routes.mar          -- typed paths
    SignIn.mar          -- one MVU page per file
    Home.mar
```

A module's path mirrors its name: `Frontend/Home.mar` is `module Frontend.Home`.

### 2.2 mar.json

The project manifest. Pure JSON, no interpolation. Strict schema (unknown fields are compile errors).

```json
{
  "name": "my-app",
  "locale": "en",
  "entry": "Main.mar",

  "server": {
    "port": 3000,
    "host": "0.0.0.0",
    "publicUrl": "https://my-app.example.com"
  },

  "database": {
    "path": "./data/app.db",
    "journalMode": "wal",
    "synchronous": "normal",
    "foreignKeys": true
  },

  "auth": {
    "sessionTtlHours": 720,
    "codeTtlMinutes": 10
  },

  "mail": {
    "from": "noreply@example.com",
    "smtpHost": "smtp.example.com",
    "smtpPort": 587,
    "smtpUsername": "app",
    "smtpPassword": "env:SMTP_PASSWORD"
  },

  "ios": {
    "bundleIdentifier": "com.example.myapp",
    "displayName": "My App",
    "serverUrl": "https://my-app.example.com"
  }
}
```

#### `locale`

Required, and deliberately without a default. It is the language the app is
written in, as a BCP 47 tag: `"en"`, `"pt-BR"`, `"es-419"`.

It has to be declared because it is what a screen reader reads the app with.
The value becomes the page's `lang` attribute on the web and
`CFBundleDevelopmentRegion` on iOS, and browsers also use it to hyphenate and
to decide whether to offer a translation. Defaulting it to `"en"` would mean
every app written in another language is silently mislabelled, which is worse
than being asked one question at the start. `mar new` writes `"en"` into the
template, so the answer is one line to change.

The tag has to be spelled canonically. `pt_BR`, `PT-br` and `en-USA` all parse,
and all mean something other than what they look like, so the compiler rejects
them with the correct spelling in the message. App code reads the same value
back as `App.locale`.

#### Environment variables

Any string field can reference an env var with the `env:` prefix:

```json
"smtpPassword": "env:SMTP_PASSWORD"
"port": "env:PORT"
```

The runtime reads the env var at startup. If missing, the server fails to start with a clear error.

#### Secrets

Some fields are marked as **secret** in Mar's internal schema (e.g., `mail.smtpPassword`). These **cannot** be literal values in `mar.json`, they must use the `env:` form. Compile error otherwise.

### 2.3 Module system

Standard Elm conventions:

```elm
module Backend.Posts exposing (posts, services)

import Entity exposing (..)
import Repo
import Shared
```

Rules:

- **No cycles.** If `A` imports `B`, `B` cannot import `A`.
- **Path = module name.** `Backend/Posts.mar` is `module Backend.Posts`.
- **Private by default.** Only what's in `exposing (...)` is visible.
- **`exposing (..)`** is recommended for builder DSLs (`Entity`, `Default`); qualified imports are recommended for `Repo` and similar.

### 2.4 Migrations

Auto-derived from entity declarations on server startup. No hand-written migration files.

- A `mar_schema_migrations` table tracks what's been applied.
- Non-destructive changes (add column, add index, create table) apply automatically.
- Destructive changes (drop column, change type, foreign key on existing table) **block startup** with a clear error and hint.
- Startup logs:
  - Idle: `[mar] Database: ./data/app.db (12 tables, schema up to date)`
  - Applied: lists each operation with timing
  - Warning: lists extra columns or other notices
  - Error: refuses to start, suggests manual SQL

### 2.5 Static assets (`public/`)

Files in a project's `public/` folder are served at the site root and
travel with the build:

- `mar dev` serves them live (`public/logo.png` → `/logo.png`, subfolders
  preserved).
- `mar build` copies the whole tree into `dist/` so they ship with the
  deployed bundle. Dotfiles (`.DS_Store`, `.env`, …) are skipped.
- Reference them by absolute path, e.g. `image [] { src = "/logo.png", alt = "…" }`.

The asset is fetched over HTTP like any other resource, the same on web
and on iOS/Android (`AsyncImage` fetches from the app's server). It is
**not** inlined into the page or bundled into the native app binary, so a
reachable host must serve `public/` at runtime.

Reserved: `mar build` refuses a `public/` path that collides with a
generated file (`index.html`, `runtime.js`, `program.json`, `_headers`)
or a runtime route prefix (`_mar/`, `_auth/`).

### 2.6 PWA (installable web app)

Every `App.frontend` app is an installable PWA out of the box, `mar dev`
serves a Web App Manifest + icons and `mar build` writes them into
`dist/`, so "Add to Home Screen" produces a real app icon that opens
fullscreen on iOS, Android, and desktop. No per-app boilerplate.

Customize it with an optional `pwa` block in `mar.json` (every field
optional; the mandatory manifest `name` comes from the top-level
`name`):

```json
{
  "name": "Daily Checklist",
  "pwa": {
    "shortName": "Checklist",
    "icon": "./icon.png",
    "themeColor": "#0071e3",
    "backgroundColor": "#ffffff"
  }
}
```

- **shortName**: home-screen label (default: `name`).
- **icon**: project-relative master PNG. **Must be a square PNG, at
  least 512×512** (`mar dev` / `mar build` fail fast otherwise); Mar
  downscales it to every needed size. Default: a generated solid-color
  tile, so a valid icon always exists.
- **themeColor / backgroundColor**: hex, default `#ffffff`.

Generated endpoints (served in dev, written to `dist/` by build):
`/_mar/manifest.json`, `/_mar/icon-180.png`, `/_mar/icon-192.png`,
`/_mar/icon-512.png`.

## 3. Basic Types

### 3.1 Syntax

Standard Elm syntax:

- `type alias Foo = { ... }` for records
- `type Bar = A | B Int | C String String` for sum types
- `\x -> body` for lambdas
- `case expr of ...` for pattern matching
- `let x = ... in body` for local bindings
- `let x <- task in body` for chaining backend Tasks (sugared `Task.andThen`)
- `|>` for pipelines

Booleans combine with `&&` and `||`. Negation is the function `not`, not a prefix operator — Mar has none — so it is written `not busy` and composes like anything else: `List.filter (\m -> not m.seen) rows`.

**`&&` and `||` short-circuit.** When the left operand already decides the answer, the right one is not evaluated. So a guard is a real guard:

```
safe : Int -> Bool
safe n =
    n > 0 && 1000000 // n < 5000
```

That matters more than it would in a total language. Mar is pure, but an expression can still fail two ways — the [53-bit Int bound](#numbers) and the recursion budget — and both are reachable from an operand. Without short-circuiting, `depth < 80 && walk tree` spends the very budget the `depth < 80` was written to protect, and it spends it on a phone where the budget is smallest.

There is no way to observe the difference through a value: the truth table is the ordinary one. The difference is only ever which programs run.

A small numeric kit sits beside it, all bare and named as in Elm: `max`, `min` and `clamp` (which ride Comparable, so they order Char and String too), `abs` (Int or Decimal), and two remainders — `modBy` follows the divisor's sign, which is what wrapping an index wants, while `remainderBy` follows the dividend's and so stays in step with `//`. Both take the divisor first, so `modBy 8` is the wrapping function. Together with `always` and `linkTo` these are the whole bare-name surface of the language; everything else is qualified.

### 3.2 Numbers

Two number types, both exact; there is no floating point anywhere in the language.

- **`Int`**: whole numbers, 53 bits wide — `-9007199254740991` to `9007199254740991`. Counting, indices, ticks, pixel math.
- **`Decimal`**: exact base-10 numbers, written with a point (`19.99`). Stored as coefficient + scale: at most 34 significant digits and at most 34 places after the point, with zeros at the end of the fraction counting toward neither; `1.50` keeps its two places for display, while `==` compares numerically (`1.50 == 1.5` is `True`).

`+ - *` work on both types and are always exact. Two *quantities* never mix — with `n : Int`, `n + 1.5` is a type error, and `Decimal.fromInt` is how you cross. A *literal* is different: it has no committed type until the context gives it one, so `price : Decimal` / `price = 1` is fine and `1 + 1.50` is `2.50`. The adaptation is one-way — `count : Int = 1.5` stays an error (ADR 0013). Division comes in two spellings:

- **`//` is integer division.** Truncates toward zero and is total: `7 // 2 == 3`, `-7 // 2 == -3`, `n // 0 == 0` on every runtime. For the remainder, reach for `modBy` or `remainderBy` above.
- **`/` is Decimal division and returns `Decimal.Division`**, the unresolved exact quotient. It has no codec and no arithmetic; exactly two resolvers turn it into a value, so every rounding names its mode and scale at the call site:

```elm
third : Decimal
third = 1.0 / 3.0 |> Decimal.rounded Decimal.HalfEven 4     -- 0.3333

split : { quotient : Decimal, remainder : Decimal }
split = Decimal.withRemainder 2 (100.00 / 3)
    -- { quotient = 33.33, remainder = 0.01 } and q * b + r == a always holds
```

`List.sum` and `List.product` add or multiply either kind — `List number -> number` — and their empty-list answers (`0` and `1`) come out as whatever the context asked for, because the compiler writes that choice onto the call (ADR 0014).

**Leaving `Int`'s range is an error, not a wrapped number** (ADR 0021). The width is 53 bits because that is the widest all three runtimes agree on: JavaScript has no integers, so an `Int` in the browser is a double, and past 2^53 it silently stops being able to tell `9007199254740993` from `9007199254740992`. Refusing is the only answer that reads the same everywhere. A literal past the range is refused by the parser, `String.toInt` returns `Nothing` for text it cannot represent, and an out-of-range integer arriving from the database raises while one arriving in a request body is answered as a malformed request. For values that genuinely need 64 bits — external ids, snowflakes — carry them as `String`.

**Leaving `Decimal`'s bounds is an error, never a rounding** (ADR 0039). A Decimal holds at most 34 significant digits and at most 34 places after the point, and zeros at the end of the fraction count toward neither: when a result carries more of them than fits, it drops only as many as it must and the value is unchanged. A result that *needs* more is an error, because dropping a nonzero digit would be a rounding nobody wrote. `+` and `-` keep the larger scale of their operands, so of the three operators only `*`, which adds them, can need more than 34 places: `List.product (List.repeat 18 0.10)` is `0.0000000000000000010000000000000000`, while `List.product (List.repeat 18 0.11)` raises. A value that needs more than 34 places within 34 digits is closer to zero than 0.1, so money never meets this bound. The bound holds for every intermediate result too: a product that needs more than 34 places raises even when the program would round it afterwards, so round the factors first. `Decimal.toScale`, `Decimal.rounded` and `Decimal.withRemainder` take a scale from 0 to 34, and the remainder of `withRemainder n (a / b)` is worked out at `n` places plus those of `b` (or those of `a`, if more) and then fitted, so a divisor with places of its own can make it raise when a nonzero digit lands past place 34. A result shows fewer places than asked for, or than the larger scale of its operands, only at the 34-digit edge, and a product whose scales add up past 34 shows at most 34; either way, trailing zeros are all that go: `Decimal.toScale Decimal.HalfEven 34 12345.6` shows 29. Text gets the same fit: a literal that does not fit is refused by the compiler, `Decimal.fromString` returns `Nothing`, and a value arriving as JSON is refused while it is decoded, like any other malformed value. Zero always fits.

Rounding modes: `Decimal.HalfEven` (banker's), `HalfUp`, `Down`, `Up`, `Floor`, `Ceiling`. The rest of the module: `Decimal.zero`, `fromInt`, `fromCents` / `toCents`, `fromString` / `toString`, `toScale`, `round` / `floor` / `ceiling` / `truncate` (to `Int`), `toIntWith`, `abs`, `negate`, `compare`. On the wire (services, JSON) a Decimal travels as a string under a marker, so no JSON parser ever routes the digits through binary floats; `JSON.decode` also reads JSON numbers textually into Decimals, in plain or exponent notation, within the bounds every Decimal has (34 significant digits, 34 places after the point).

### 3.3 Text

A `String` is a sequence of Unicode **scalar values**, and a `Char` is one of them. Every length, width, position, equality and ordering counts scalars, so the answer is the same on the server, in the browser and on the phone (ADR 0038).

- **`String.length`** counts characters, not bytes and not what a keyboard calls a keystroke: `"café"` is 4, `"日本"` is 2, `"😀"` is 1, and a flag is 2, because a flag is written as two regional indicators.
- **`String.indexes`, `String.padLeft`, `String.padRight`** count in the same unit, so a position you get back is a position you can reason about: `String.indexes "a" "café a"` is `[1, 5]`.
- **`==` and `<`** compare the scalars. Two spellings of one word are two strings: `"café"` written with a precomposed accent is not `"cafe"` followed by a combining one. This is what `Dict` and `Set` key on, so it decides what a dictionary contains.
- **`String.toList`** is the same sequence as a list, so `String.length s == List.length (String.toList s)` for every `s`.

Case mapping is Unicode's **full** mapping and may change the length of a string: `String.toUpper "straße"` is `"STRASSE"`, and a Greek capital sigma lowercases to its final form at the end of a word. A `Char` holds one scalar and has nowhere to put a longer answer, so `Char.toUpper` and `Char.toLower` use the **simple** mapping instead and return the character unchanged when the full one would be longer — `Char.toUpper 'ß'` is `'ß'`, while `String.toUpper "ß"` is `"SS"`.

The character predicates are Unicode's, not ASCII's: `Char.isDigit` is true for a decimal digit in any script, and `Char.isAlpha` for a letter in any script.

Normalisation is not implicit anywhere. If an app needs two spellings to compare equal, that is a transformation it applies on purpose, not a rule one runtime applies on its behalf.

### 3.4 Angles and trigonometry (`Math`)

An angle is not an `Int`. It is an `Angle`, and the constructor names the unit — the same rule `Duration` and `Width`/`Height` already follow (ADR 0029):

```elm
Math.degrees 45          -- whole degrees
Math.deciDegrees 450     -- tenths of a degree: the same angle, said finer
Math.turns 64            -- brads, 256 to a turn: also the same angle
```

Any `Int` is a valid argument, because every constructor wraps: `Math.degrees 360` is `Math.degrees 0` and `Math.degrees -90` is `Math.degrees 270`. The algebra comes with the type, so nothing in a game ever writes the wrap by hand:

```elm
heading = Math.add model.heading (Math.deciDegrees (turn * 15))
away    = Math.opposite model.heading
```

Trigonometry answers in **thousandths**, the fixed-point convention the rest of the API already uses for percent:

```elm
Math.sin : Angle -> Int          -- -1000 .. 1000
Math.cos : Angle -> Int
Math.atan2 : Int -> Int -> Angle -- atan2 y x, y first (as in Elm); y points up
Math.isqrt : Int -> Int          -- floor of the square root; 0 at or below zero
```

Every one of them is total: `Math.atan2 0 0` is 0°, `Math.isqrt -5` is 0. There is nothing to guard.

Two things follow from the type. The first is that a unit mix-up is a compile error rather than a game that turns ten times too slowly: `Canvas.Rotate` and `Math.sin` take the same `Angle`, so a heading goes straight from `Math` to the canvas. The second is that the answers are **identical on every runtime**: all three read one generated quarter-wave table (`internal/mathgen`) instead of calling the host's trigonometry, which is what lets a rules engine be replayed on the server and the client, a recorded bot run prove a level, and time travel re-run old messages.

The internal resolution is a tenth of a degree: a program can tell 45.0° from 45.1° and nothing finer. That number is not observable: no function hands it back.

**A `Time` is a millisecond from `0000-01-01T00:00:00.000Z` to `9999-12-31T23:59:59.999Z`**, in the Gregorian calendar extended back before 1582 (proleptic) and in UTC, the same on the server, in the browser and on the phone: one instant has one date everywhere. Its text is RFC 3339 with a four-digit year, `2023-11-14T22:13:20.000Z`, which is how `Time.toIso` writes it and how it crosses the wire. `Time.fromIso` reads that form only (a `Z` or a `±HH:MM` offset is required, and a fraction past the milliseconds is dropped) and gives `Nothing` for anything else or for a moment outside the range. Every operation that builds a Time (`Time.add`, `Time.sub`, `Time.fromYMD`, `Time.addDays`, `Time.addMonths`, `Time.addYears`) raises "Time out of range" rather than leave it; `Time.fromYMD` carries an out-of-range month or day over first, so `Time.fromYMD 10000 0 1` is `9999-12-01`.

**A `Duration` is a whole number of milliseconds**, the unit a `Time` is counted in, so `Time.add`, `Time.sub` and `Time.diff` are exact: `Time.add t (Time.millis 1001)` moves `t` by exactly 1001 ms, and `Time.diff a b` is what separates them to the millisecond. It is built only by a constructor that names the unit (`Time.millis`, `Time.seconds`, `Time.minutes`, `Time.hours`, `Time.days`, `Time.weeks`) or by `Time.diff`, and it holds the range of `Int` in milliseconds: an interval past that, such as `Time.weeks` of a very large number, raises "Duration out of range" rather than wrapping. `Time.toSeconds` gives whole seconds, toward zero. On the wire a Duration is `{"__duration": 1500}`, and it can be part of a service contract or a pinned `JSON.decode` like any other value.

**A `Date` is a day of the calendar**, with no time of day and no time zone: `2026-11-17` is the same day on the server, in the browser and on the phone, wherever each of them is. A Date is for what people write as a day (a birthday, a due date, the night a sleep log is about) and a Time for a moment (when a row was written). Its days are the days of a Time, `0000-01-01` to `9999-12-31`, so `Date.toTime` (midnight UTC) and `Date.fromTime` (the day in UTC, the one `Time.year`, `Time.month` and `Time.day` read) always succeed; `Date.fromYMD`, `Date.addDays`, `Date.addMonths` and `Date.addYears` raise "Date out of range" rather than leave it, and `Date.fromYMD` carries an out-of-range month or day over as `Time.fromYMD` does. Its text is `YYYY-MM-DD`, which `Date.toIso` writes and `Date.fromIso` reads, nothing else; on the wire a Date is `{"__date": "2026-11-17"}`. Like a Time, a Date has no order of its own: compare with `Date.before`, `Date.after` or `Date.diffDays`, and sort by `Date.toIso`.

`Date.today : Task Date` is today where the person is: the device's clock, read in the device's time zone. It is the one place a time zone enters a program, and it exists on the frontend only, a compile error in a handler: the server has no person and no time zone of its own. A handler takes the day from the request, and a check against the server's clock allows a day either way of `Date.fromTime now`, because the time zones people live in run from UTC-12 to UTC+14.

### 3.5 Nominal IDs

Every entity ID is a nominal wrap of a primitive:

```elm
type UserId = UserId Int
type PostId = PostId Int
type SlugId = SlugId String
```

This prevents mixing IDs of different entities at compile time. Mar's auto-derived codecs encode them transparently (the wrapper disappears on the wire).

### 3.6 Task a and Cmd msg

Side effects are values of two distinct types, one per side of the app.

**`Task a`** is the backend's value-monad: "a computation that, when run, produces an `a` (or aborts)." Read it as an "await". Service handlers return `Task resp`, `Repo.*` return `Task`, and `Time.now : Task Time`. You chain Tasks to compute a value. One Task reads something only the device has and exists on the frontend alone: `Date.today`.

```elm
type Task a

Task.succeed  : a -> Task a
Task.fail     : String -> Task a          -- abort a backend handler with a message
Task.map      : (a -> b) -> Task a -> Task b
Task.andThen  : (a -> Task b) -> Task a -> Task b
Task.sequence : List (Task a) -> Task (List a)
Task.forEach  : (a -> Task ()) -> List a -> Task ()
```

**`Cmd msg`** is the frontend's message-monoid (Mar's `Cmd`, like Elm's): a description of work the MVU runtime should do, whose result returns as a `Msg`. A page's `init` and `update` return `(Model, Cmd Msg)`; `Service.call` and `Nav.*` build a `Cmd`.

```elm
type Cmd msg

Cmd.none    : Cmd msg
Cmd.batch   : List (Cmd msg) -> Cmd msg
Cmd.perform : (a -> msg) -> Task a -> Cmd msg   -- run a Task, deliver its value as a Msg
```

`Cmd.perform` is the bridge from a `Task` to the frontend loop (Elm's `Task.perform`): it runs the `Task` and hands the produced value to `update` as a `Msg`. It is the only way a Task's result reaches the frontend. `Time.now` is the same `Task Time` on both sides; the frontend reaches the loop with `Cmd.perform GotNow Time.now`, and reads the person's day with `Cmd.perform GotToday Date.today`.

The two types stay separate on purpose. They used to be one `Effect a` that carried both algebras, and the overlap let a real bug compile: a value-producing effect like `Time.now` returned straight from a frontend `update` silently did nothing, because the loop only delivers messages. Now `Task` is a value and `Cmd` is a message: returning a `Task` where a `Cmd Msg` is expected is a compile error, so the footgun is gone.

Neither type carries an error index. On the backend a `Task` aborts with `Task.fail`, whose String becomes the `Err` the frontend receives; reserve it for genuine failures and keep matchable domain errors in the service's response value (a typed union) instead. On the frontend a failure travels inside the value: a `Service.call` delivers `Result Service.Error resp`, where `Service.Error` is a union (`Offline` / `Unauthorized` / `ServerError String`) the frontend cases on.

### 3.7 Task chaining: `let <-`

Sugar for `Task.andThen`, on the backend. Each `<-` binds the result of one `Task` before the next runs:

```elm
toggle id =
    let
        found <- Repo.findById tasks id
    in
    case found of
        Just task -> Repo.update tasks id { done = not task.done }
        Nothing   -> Task.succeed Nothing
```

Equivalent to `Repo.findById tasks id |> Task.andThen (\found -> ...)`.

### 3.8 Error handling

Three kinds of failure, three homes. The rule of thumb: transport is a
shared union, domain is a per-endpoint outcome in the response value, and
`Task.fail` is the abort channel for broken invariants only.

**Transport** (offline, expired session, server failure): every call can hit
these, so every `Service.call` delivers the same union in its `Err`:

```elm
type Service.Error
    = Offline              -- request never reached the server
    | Unauthorized         -- session gone (401)
    | ServerError String   -- the server refused; carries its message
```

Match it qualified, or fold it for display:

```elm
Fetched (Err Service.Offline) -> -- show a retry
Fetched (Err why)             -> -- Service.errorToString why
```

**Domain** (email taken, wrong code, body too long): specific to one
service, so it lives in that service's response type as a union of the
outcomes it can actually produce. Never a shared catch-all: a page should
only have to match what can happen to it.

```elm
type SignupOutcome = Created User | EmailTaken | TeamFull

signup : Service NewUser SignupOutcome
signup = Service.declare POST "/signup"
```

The handler is `NewUser -> Task SignupOutcome`, so the backend can only
produce declared outcomes, and the frontend's case is checked for
exhaustiveness. Patterns nest flat:

```elm
Done (Ok (Created user)) -> ...
Done (Ok EmailTaken)     -> ...
Done (Ok TeamFull)       -> ...
Done (Err why)           -> ...
```

Exhaustiveness covers every subject, not only unions (ADR 0022). A list needs both `[]` and `x :: rest`, since together they name every list there is. `Int`, `String` and `Char` need a catch-all (`_ ->`, or a bare name), because their values cannot be enumerated and no list of literals covers them — `case n of 1 -> "one"` is a compile error, not a program that fails on the second input. And a branch the ones above it already cover is refused too: it could never run, so it is a typo or a misreading rather than a spare. The message names what is missing (`[]`, `Just _`, `Blue`) or which branch is dead.

The auth flow follows the same shape with framework-provided outcomes:
`Auth.requestCode` delivers `Auth.RequestOutcome` (`Auth.CodeSent` /
`Auth.InvalidEmail` / `Auth.RateLimited`) and `Auth.verifyCode` delivers
`Auth.VerifyOutcome user` (`Auth.SignedIn user` / `Auth.WrongCode` /
`Auth.TooManyAttempts`).

**Abort** (`Task.fail "..."`): for broken invariants in backend handlers,
not for outcomes the frontend reacts to. The string surfaces to the client
as `ServerError`, display-only. If a page needs to branch on a case, the
case belongs in the service's response type.

Error copy belongs to the view: the wire carries data (constructors), and
each frontend chooses its own words for each case.

## 4. Backend

### 4.1 Entity API

An entity is a database-backed record. It carries schema only, no API and no business logic. `Entity.define` takes the table name, a `columns` record whose field names and types mirror the record, and a `uniques` list:

```elm
type alias Task =
    { id        : Int
    , name      : String
    , done      : Bool
    , createdAt : Time
    , userId    : Int
    , position  : Int
    }

tasks : Entity Task
tasks =
    Entity.define
        { name = "tasks"
        , columns =
            { id        = Entity.serial
            , name      = Entity.text Entity.notNull
            , done      = Entity.bool Entity.notNull
            , createdAt = Entity.timestamp Entity.notNull
            , userId    = Entity.int Entity.notNull
            , position  = Entity.int Entity.notNull
            }
        , uniques = []
        }
```

#### Column builders

Each column is one of:

- `Entity.serial`: auto-incrementing integer primary key, filled by the runtime on insert.
- `Entity.int Entity.notNull`
- `Entity.text Entity.notNull`
- `Entity.bool Entity.notNull`
- `Entity.timestamp Entity.notNull`: a `Time` column.
- `Entity.date Entity.notNull`: a `Date` column. A `Time` column declared again as a `Date` becomes one when every row holds a midnight UTC (a row with a time of day blocks the migration, since which day it stands for depends on where its person was), and a `Date` column declared again as a `Time` becomes the midnight UTC of each day.
- `Entity.enum [Open, InProgress, Done] Entity.notNull`: a tags-only union, stored as a CHECKed text column so only those values can be written.

`Entity.notNull` marks a column as required. Because `Entity.serial` is filled by the runtime, `Repo.create` takes the record without that field.

#### Unique indexes

`uniques` is a list of column-name groups; each inner list is one unique index, composite when it names more than one column:

```elm
, uniques = [["commentId", "userId", "emoji"]]   -- one reaction per user per comment
```

Computed defaults (a slug from a title, a creation time) belong in the handler, not the entity: read `Time.now` and pass the value to `Repo.create`.

### 4.2 Codecs

100% derived from types. **No codec API exposed to user code.**

- `type alias` records → JSON object with same field names (camelCase).
- `type X = X Inner` (single-constructor wrap) → encoded as `Inner` (transparent).
- `type Status = Active | Inactive` (tags only) → JSON string with lowercase camelCase.
- Sum types with payload → `{ "tag": "constructorName", ...payload }`.
- `Maybe a` → value or `null`. Optional fields on decode.
- `List a` → JSON array.
- Primitives: `Int`, `String`, `Bool`, `Char`, `Time` (RFC 3339, UTC, years 0000 to 9999), `Date` (`YYYY-MM-DD` under `__date`), `()`.

Mar uses these codecs at the HTTP boundary (request body, response body, path params, query params) and at the DB boundary. User code never sees JSON or constructs codecs manually.

For external APIs (Stripe, etc.), an explicit `Codec` module may be added later.

### 4.3 Data access (Repo)

`Repo.*` reads and writes entity rows. Every operation runs inside a backend handler and returns a `Task`:

```elm
Repo.all        : Entity a -> Task (List a)
Repo.findById   : Entity a -> Int -> Task (Maybe a)
Repo.findBy     : Entity a -> fields -> Task (List a)
Repo.create     : Entity a -> fields -> Task a
Repo.update     : Entity a -> Int -> fields -> Task (Maybe a)
Repo.deleteById : Entity a -> Int -> Task ()
```

`findBy` filters by example: pass a record of the columns to match, and it returns every row whose values equal them. `create` takes the full row minus the `serial` id. `update` takes the id and a record of just the columns to change, and answers `Nothing` when no row has that id. `deleteById` is idempotent.

```elm
listTasksImpl : () -> Shared.User -> Task (List Shared.Task)
listTasksImpl _ user =
    Repo.findBy tasks { userId = user.id }
        |> Task.map sortByPosition
```

There is no query-builder, predicate, pagination, or relation API today. Compose with `Task.andThen` and ordinary Mar (`List.filter`, `List.sortWith`, `List.map`) over the rows you read. Raw SQL is not exposed to app code.

### 4.4 Services

A `Service req resp` is a typed contract for one server call: `req` is what the client sends, `resp` what it gets back. The same value is shared by both halves, declared once in the shared module with a verb and a path:

```elm
listTasks : Service () (List Task)
listTasks = Service.declare GET "/tasks"

getTask : Service { id : Int } (Maybe Task)
getTask = Service.declare GET "/tasks/{id:Int}"

addTask : Service { name : String } AddTaskOutcome
addTask = Service.declare POST "/tasks"
```

`Service.declare VERB "/path"` fixes the HTTP method (`GET` / `POST` / `PUT` / `PATCH` / `DELETE`) and the path; the type annotation fixes `req` and `resp`, and is required: without it the contract infers as `Service a b`, which the two sides would instantiate separately, so `mar check` refuses it. A path may carry typed `{name:Type}` params that name fields of the request record: `getTask`'s `{id:Int}` binds `req.id` into the URL. The backend pairs each contract with a handler, the frontend calls it:

```elm
Service.declare       : Method -> String -> Service req resp
Service.implement     : Service req resp -> (req -> Task resp) -> ExposedService
Service.call          : Service req resp -> req -> (Result Service.Error resp -> msg) -> Cmd msg
Service.errorToString : Service.Error -> String
```

The verb determines how the request travels and what the handler may do:

- **Wire transport.** Path params fill their `{name:Type}` slots in the URL. For `GET` and `DELETE` the remaining request fields ride in a query param; for `POST`, `PUT`, and `PATCH` they ride in the JSON body. The response is always the JSON-encoded `resp`.
- **A `GET` or `DELETE` request is a record, or `()`.** It has no body, so it travels as fields; the compiler rejects any other request type on these verbs. A `POST`, `PUT` or `PATCH` request can be any value with a wire form.
- **`GET` is read-only.** The compiler rejects a `GET` whose handler reaches `Repo.create`, `Repo.update`, or `Repo.deleteById`. A call that mutates must be `POST` / `PUT` / `PATCH` / `DELETE`.

A handler is `req -> Task resp`, so it can only produce the declared response. Most calls should be authenticated, which is what `Auth.protect` is for:

```elm
Auth.protect : Service req resp -> (req -> User -> Task resp) -> ExposedService
```

`Auth.protect` injects the signed-in `User` as the second argument and rejects the request with 401, before the handler runs, when there is no valid session. The frontend sees the same `Service` value either way and never knows whether a handler was wrapped.

```elm
-- Backend.Tasks
addTaskImpl : { name : String } -> Shared.User -> Task Shared.AddTaskOutcome
addTaskImpl input user =
    if String.trim input.name == "" then
        Task.succeed Shared.NameEmpty
    else
        Repo.create tasks { name = input.name, done = False, {- ... -} }
            |> Task.map Shared.Added

services =
    [ Auth.protect Shared.listTasks listTasksImpl
    , Auth.protect Shared.addTask   addTaskImpl
    ]
```

On the frontend, `Service.call` turns a contract into a `Cmd` that dispatches a `Msg`. The `Result` carries `Service.Error` in its `Err` (transport failure) and the declared `resp` in `Ok` (which holds the domain outcome). See section 3.6 for the full error model.

```elm
update msg model =
    case msg of
        AddClicked ->
            ( model, Service.call Shared.addTask { name = model.draft } Added )

        Added (Ok (Shared.Added task)) -> -- ...
        Added (Ok Shared.NameEmpty)    -> -- ...
        Added (Err why)                -> -- Service.errorToString why
```

### 4.5 Wiring (`App.fullstack`)

`Main.mar` is the only module that sees both halves. It builds the auth config, lists the services and pages, and hands them to `App.fullstack`:

```elm
main : Cmd ()
main =
    App.fullstack
        { services = Backend.Tasks.services
        , pages    =
            [ Frontend.SignIn.page
            , Frontend.Home.page
            ]
        }
```

- `services` is the concatenation of each backend module's exposed services. Each one carries the verb and path it was declared with, so the runtime routes incoming requests to the right handler.
- `pages` enumerates every frontend route; the runtime dispatches by `path`.

`App.fullstack` takes exactly `{ services, pages }`. A backend-only app uses `App.backend { services }`; HTTP is exposed only through services.

### 4.6 Auth

Mar ships passwordless auth: the user enters an email, receives a one-time code, and exchanges it for a session. The app brings its own `User` entity and registers it through `Auth.config`:

```elm
auth : Auth { id : Int, email : String }
auth =
    Auth.config
        { entity          = Backend.Users.users
        , identify        = \u -> u.email
        , signInPage      = Frontend.SignIn.page
        , email           = { subject = "Your sign-in code" }
        , signup          = \userEmail -> { email = userEmail }
        , sessionDuration = Time.days 30
        }
```

Protect a service with `Auth.protect` (section 4.4) and a page with `Page.protected` (section 5). The sign-in flow runs through `Auth.requestCode` and `Auth.verifyCode`, which deliver the per-call outcomes `Auth.RequestOutcome` and `Auth.VerifyOutcome user` (section 3.6). The full flow, every config field, and the SMTP and dev-code setup live in [auth.md](auth.md).

### 4.7 Errors

Backend handlers return `Task resp`; there is no error type parameter. The three kinds of failure, and where each one lives, are covered in full in section 3.6. In short:

- A handler returns its declared response. Domain outcomes are constructors of that response type, matched by the frontend.
- `Task.fail "message"` aborts the handler; the message reaches the client as `Service.ServerError` and is display-only.
- Offline, expired-session (401), and server failures are turned into the `Service.Error` union by the runtime and delivered in the call's `Err`.

## 5. Frontend

### 5.1 MVU model

Each page is its own independent MVU loop with `Model`, `Msg`, `init`, `update`, and `view`. `init` and `update` return `(Model, Cmd Msg)`:

```elm
init   : (Model, Cmd Msg)
update : Msg -> Model -> (Model, Cmd Msg)
view   : Model -> View Msg
```

The runtime instantiates a page on navigation and swaps it out when the user navigates away.

### 5.2 Page value

Each page module exports a `page`, built with one of the `Page.*` combinators. They all take the same record, `{ path, title, init, update, view }`; the combinator decides what `init` / `update` / `view` receive:

```elm
Page.create           -- public, static path; init : (Model, Cmd Msg)
Page.protected        -- runs Auth.me on entry, hands the User in; init : User -> (Model, Cmd Msg)
Page.dynamic          -- path carries typed args; init : args -> (Model, Cmd Msg)
Page.dynamicProtected -- both; init : User -> args -> (Model, Cmd Msg)
```

```elm
page : Page
page =
    Page.protected
        { path   = "/"
        , title  = "Team Notes"
        , init   = init
        , update = update
        , view   = view
        }
```

`Page.protected` bootstraps the session: it runs `Auth.me`, redirects to the sign-in page when there is no valid session, and otherwise passes the `User` to `init`. (`Page.adminProtected` and `Page.dynamicAdminProtected` gate on the admin session instead.)

A dynamic page's `path` is a typed route: a path string with `{name:Type}` placeholders, kept in a `Routes` module so links and pages agree on the shape:

```elm
-- Frontend/Routes.mar
home       = "/"
verifyCode = "/sign-in/verify/{email:String}"
```

The placeholder values are parsed and delivered to `init` as a record (`{ email : String }`).

`Page.sheet` wraps any of them and changes one thing: how the page is
SHOWN. Navigating to it leaves the page you came from on screen and lays
this one over it in a sheet, which is what a bounded task wants — take
attendance, compose a message, edit a record: something the reader
finishes or abandons. Keep the plain push for places the reader browses
*into*, where going deeper is the point.

```elm
page : Page
page =
    Page.sheet
        (Page.dynamicProtected
            { path   = Frontend.Routes.takeAttendance
            , title  = "Take attendance"
            , init   = init
            , update = update
            , view   = view
            , subscriptions = \_ _ _ -> Sub.none
            }
        )
```

It stays a real route: same path, same history entry, same deep link. Back,
Escape and a tap outside dismiss it, and `Nav.dismiss` is the same verb for
the sheet's own Cancel / Done button. Two consequences worth designing
around:

- **Opened cold it renders full screen.** A deep link, a reload or a
  shared URL has no page behind it to present over, so the route mounts
  like any other page. Write it so it reads on its own.
- **The covered page waits underneath**, and comes back with the same
  model and the same scroll. A result it asked for before the sheet opened
  still reaches it while it is covered. A navigation or a sound that result
  asks for is ignored, and a timer or a sound loop it turns on waits until
  the sheet closes: the person is looking at the sheet. Nothing
  refetches on dismissal, so if the task changed data the page underneath
  is showing, that page will be stale until something asks it to reload.

A presented route has no navigation bar of its own — the sheet is not on
the stack — so its own header is the chrome: the dismiss on one side, the
title centered, the confirming action on the other.

### 5.3 View vocabulary

Views are abstract: no HTML, CSS, or SwiftUI in user code. Mar renders natively per platform (HTML/CSS for web, SwiftUI for iOS). Every element takes a list of attributes as its first argument, even when empty (`text []`, `section []`), so adding an attribute never changes the call shape.

```elm
navigationStack [ navigationTitle "Sign in" ]
    [ form
        [ section []
            [ text [] "Enter your email and we'll send a code."
            , textField [ email, submit Submitted ] "Email" draft DraftChanged
            ]
        , section []
            [ button [] Submitted "Send me a code" ]
        ]
    ]
```

The building blocks: `navigationStack` / `navigationTitle`, `form` / `section`, `row` / `column` / `spacer`, `text` / `title` / `subtitle` / `paragraph`, `textField`, `picker`, `datePicker`, `button`, `link` / `navigationLink`, `list` / `keyedList`, `toggle`, `image`, `sheet` / `confirm`, `errorText`, `centered`, and `empty` (renders nothing). `button [] Submitted "Verify"` takes its message and label directly; `textField` takes its label, the current value, and an on-change message. `datePicker [] day DatePicked` is a date-only field, and it is pure: it shows exactly the `Date` you pass and picking fires `(Date -> msg)` with the chosen day. It does not read the clock; to start on "today", seed the field with `Cmd.perform GotToday Date.today` (hold it as `Maybe Date`, render the picker once seeded). Web renders `<input type="date">`, iOS a SwiftUI `DatePicker`; both offer the days from 1582-10-15, the first day of the Gregorian calendar, to 9999-12-31, and the day picked is the `Date` handed back, the same on both and in every time zone. A `Date` before 1582-10-15 shows as that first day on both, and nothing is sent until a day is picked.

### 5.4 Navigation

The `Nav` module drives navigation. In a view, `navigationLink` pushes a destination when tapped; in `update`, `Nav.pushTo` / `Nav.replaceTo` (and `Nav.push` / `Nav.replace`) return a `Cmd` that navigates:

```elm
update msg model =
    case msg of
        Saved (Ok note) ->
            ( model, Nav.replaceTo (Frontend.Routes.noteDetail note.id) )
```

`replace` swaps the current entry, so Back does not return to it; `push` adds one. After a successful sign-in, `Auth.completeSignIn` returns the user to wherever a 401 sent them (or home).

`Nav.dismiss` closes a route that is being presented (`Page.sheet`) — the verb its own Cancel / Done needs, doing exactly what a tap outside or Back does. With nothing presented it steps back one screen, and at the app's first screen it does nothing, so a sheet route opened cold cannot walk the reader off the site.

Each screen on the stack has a model of its own, even two of the same page: from A to B and to A again, Back twice finds the first A as you left it. A result a page asked for (a `Service.call`, a `Cmd.perform`) goes back to that page wherever you have gone since: a page covered by another one is updated while it waits underneath, but it does not navigate or play a sound, since you are looking at another screen. A result for a screen that has left the stack is dropped, and the browser's Forward onto a screen you went Back from starts that screen over, as a push does.

### 5.5 Shared state (`App.shared`)

Page models live on the navigation stack: going forward re-runs a page's `init`, coming Back restores the screen you left. State that has to survive that trip — a profile fetched once, a theme, an unread count, a cart — belongs in a shared store instead.

An app-owned module holds the model, the messages and one binding:

```elm
def : App.Shared Model Msg
def =
    App.shared { init = init, update = update, subscriptions = subscriptions }
```

A page **reads** it by wrapping any of the six page constructors, and the value reaches `init` / `update` / `view` by ordinary partial application:

```elm
page : Page
page =
    Page.withShared Frontend.Global.def
        (\global ->
            Page.create { path = "/", init = init, update = update, view = view global, subscriptions = always Sub.none }
        )
```

A page **writes** to it the way it talks to itself, by sending a message:

```elm
AddClicked id ->
    ( model, Cmd.toShared Frontend.Global.def (Frontend.Global.Added id) )
```

Pages never assign the shared model; the shared module owns its own `update`, with the usual exhaustive `case`. `Cmd.toShared` is an ordinary `Cmd`, so it batches with `Cmd.batch` and composes with `Cmd.perform`.

There is no registration step: `Main` does not mention the store, and the runtime finds it through the pages that use it. Every use site names the same `def`, which is what makes them agree on `Model` and `Msg` at compile time (ADR-0026).

Semantics worth knowing:

- `init` runs once, before the first page's `init`. Its `Cmd` — typically the one `Service.call` that fills the store — runs after the first render.
- A shared change repaints the page on screen: its builder is re-applied with the new value. The page's own model is untouched and its `update` is **not** called.
- A `Cmd.toShared` issued before a navigation still applies after it. Shared is page-independent, so unlike a page msg it is never dropped at the dispatch boundary.
- `subscriptions` here are alive for the life of the store, not the page. A `Time.every` is an app-wide heartbeat.
- The store dies with the tab. Persistence is out of scope; a `localStorage`-backed variant is future work.

Shared is not a server cache. There is no revalidation or staleness policy — if the data can rot, the app decides when to refresh it. And it is not a place for page state: if only one screen cares, it stays in that screen's model.

See `examples/shared-cart`.

### 5.6 The app's language (`App.locale`)

`App.locale : String` is the tag declared as `locale` in mar.json (section
2.2), read back where the app can use it:

```elm
greeting : String
greeting =
    if App.locale == "pt-BR" then
        "Ola"

    else
        "Hello"
```

It is a value, not a function, because it cannot change while the program runs:
it is a fact about the build, not about the device. Every runtime reads it from
the place that already carried it, so no field was added to the bundle: the
browser from the `<html lang>` the shell wrote, iOS from `Info.plist`, the Go
server from the manifest at boot.

This is the app's *own* language, not the user's. Reporting what the person at
the keyboard prefers, and letting them choose, is a separate question with a
separate answer (`docs/proposals/language.md`).

## 6. Client and server

The same `Service` value (section 4.4) is the contract for both halves: declared once in the shared module with its verb and path, implemented on the backend (`Service.implement`, or `Auth.protect` for an authenticated call), and invoked from a page with `Service.call`. The verb and path are transparent to the caller: `Service.call` looks the same whatever method a service uses. Renaming a service or changing its `req` / `resp` breaks both sides at compile time, with no code-generation step.

```elm
-- Shared (declared once):
addTask : Service { name : String } AddTaskOutcome
addTask = Service.declare POST "/tasks"

-- Backend (Backend.Tasks.services):
Auth.protect Shared.addTask addTaskImpl

-- Frontend (in update):
update msg model =
    case msg of
        SubmitClicked ->
            ( { model | submitting = True }
            , Service.call Shared.addTask { name = model.draft } Added
            )

        Added (Ok (Shared.Added task)) -> -- ...
        Added (Ok Shared.NameEmpty)    -> -- ...
        Added (Err why)                -> -- Service.errorToString why
```

The `Result` the page receives carries `Service.Error` in its `Err` (transport failure) and the declared response in `Ok`, which holds any domain outcome. See section 3.6 for the full error model.

### 6.1 Noticing a deploy from an open tab

A page load pins the runtime and the program together: the HTML carries the program inline, both revalidate on every load, and a fresh load is always internally consistent. The case a fresh load cannot cover is the tab nobody reloads. Leave an app open, deploy, and that tab keeps running last week's code while its service calls land on today's server.

Every response the server sends carries its identity:

```
X-Mar-Program: <hash of the program being served>
X-Mar-Runtime: <the mar version that built the server>
```

The runtime remembers what it saw on the first response and compares every later one. No polling and no extra request: the evidence rides along with traffic the app was making anyway (service calls, and the `Auth.me` check that protected pages do). When something disagrees, a small bar appears in the bottom corner with a **Reload** button. Nothing is blocked, because the page is still running a complete and internally consistent version of the app, just not the newest one.

The two headers mean different things and the bar says so:

| what changed | message |
| --- | --- |
| program | "A new version is available." The app's own code was redeployed; this page still works, it is just old. |
| runtime | "This page is out of date. Reload to keep going." The framework moved, so the wire format this page speaks may no longer be the one the server answers in, and service calls can start failing in ways that look like app bugs. |

A response with neither header changes nothing. That is what an older server, or a proxy that drops what it does not recognise, looks like, and silence is not evidence of a deploy.

The bar never appears under `mar dev`: hot reload already applies changes, and every save would otherwise raise it.

## 7. Main.mar

Entry point. A single `App.fullstack { ... }` call, returning `Cmd ()`:

```elm
module Main exposing (main)

import Backend.Tasks
import Backend.Users
import Frontend.SignIn
import Frontend.VerifyCode
import Frontend.Home


main : Cmd ()
main =
    App.fullstack
        { services = Backend.Tasks.services
        , pages    =
            [ Frontend.SignIn.page
            , Frontend.VerifyCode.page
            , Frontend.Home.page
            ]
        }
```

`services` and `pages` are explicit lists; Mar does not auto-discover (see section 4.5). Auth is configured by a top-level `auth = Auth.config { ... }` binding that the runtime picks up (section 4.6).

## 8. Deferred / Future Work

The following are intentionally not in the MVP. They will be revisited when concrete need arises.

- **Subscriptions** (timer, websocket, server-sent events), Elm-style `Sub Msg`.
- **Theme customization** via a `mar.json` `theme` field for colors and typography.
- **Escape hatches for platform-specific behavior**: haptics, push notifications, file downloads.
- **Custom HTTP clients** for external APIs (Stripe, etc.) via an explicit `Codec` API.
- **Crud scaffold helpers** (`Crud.scaffold entity`) if examples become repetitive.
- **Ownership helpers** for the read-then-check pattern, if it recurs.
- **Cross-screen shared state** (an elm-land-style global `Shared` model): page models live on the navigation stack. Going somewhere new re-runs that page's `init` (and refetches); going Back restores the screen you left, model intact. So forward navigation still refetches per screen, and shared data is currently refetched or kept on the server; a `Shared` store to hold it once on the client is future work. See ADR 0009.
- **Loading-state abstraction**: each page currently handles its own loading state.
- **Multiple environments in `mar.json`**: currently a single config, env vars handle differences.

## 9. Examples

See `examples/`:

- `hello-auth/` is the smallest end-to-end app: email plus one-time-code sign-in and a single protected page.
- `daily-checklist/` is a per-user CRUD app: entities, `Repo`, `Service` contracts, `Auth.protect`, drag-to-reorder, and a typed domain outcome (`AddTaskOutcome`).
- `team-notes/` adds multiple pages, dynamic routes (`Page.dynamic`), and a detail page.
- `mini-twitter/` is the full-featured one: three entities (User / Tweet / Follow), passwordless email auth, handle-based profiles, a follow graph, typed routes, and an MVU page per route. See `examples/mini-twitter/README.md` for a reading order.
