# Baseline

A daily mood log that reports what actually moves it.

```
mar dev examples/baseline
```

Sign in with an email and a one-time code (printed to the dev server log
when no SMTP is configured), answer a few setup questions, then log a mood,
an energy level and a handful of surroundings once a day.

## What it is for

Everyone has heard that sleep matters and that too much coffee makes you
anxious. That is true on average and nearly useless in person, because the
variation between people is enormous. Finding out which one you are takes an
experiment: several weeks of measurement, then a comparison.

Baseline is the infrastructure for that experiment, cut down small enough
that somebody actually keeps it up. It knows nothing on day one. After two
months it can compare your own best days against your own worst ones and
report what showed up on each side.

## The shape of the app

The unusual thing here, and the reason this example exists, is where the
work happens:

- **`Shared.mar` is the brain.** The factor catalogue, the day arithmetic,
  the comparison engine, the week summary and the cycle phases are all pure
  functions in the shared module — the same file that declares the wire
  types both halves agree on.
- **`Backend/Log.mar` is thin.** Four tables and six handlers. It stores
  rows, keeps them the caller's own, and enforces the two rules only a
  server can: one entry per day, and no rewriting a day older than the edit
  window.
- **The frontend does the reporting.** One request fetches a window of raw
  rows; every screen computes its numbers from that window with the shared
  functions, so navigating between reports costs nothing.

That split is a product decision as much as an architectural one. The
reports are the product, and being able to read exactly how each number is
derived — in one file, next to the types — is what makes "it never claims
more than the data supports" checkable rather than promised.

## The methodology, briefly

- **Bands are the groups.** Every factor is answered in named options, so
  the report never invents a cutoff: the bands people answered in *are* the
  groups being compared.
- **Ends, not middles.** The top of a scale against the bottom; the middle
  is left out. With five bands and a floor on sample size, band-by-band
  would never have enough of anything.
- **Both lags, always.** Same day and next day, for every factor, reporting
  whichever moved more. Most apps of this kind only cross factors with the
  same day, which is why they never find the effect of a drink: on the night
  you drink, the mood is usually fine.
- **Ten days at each end, minimum**, and differences under half a point are
  reported as no difference. Silence is cheaper than being believed once and
  wrong.
- **Association, never cause.** The copy says these days come together. The
  reader knows things about their own life the app never will.

## Screens

| | |
| --- | --- |
| `/sign-in` | the landing page: what it is, what it will not do, then the email box |
| `/setup` | the one-time questions that decide which fields exist for you |
| `/` | today's log, and the week underneath it |
| `/month` | the month as a grid, one square a day, coloured by mood |
| `/patterns` | the series, the findings, and what is still missing |
| `/day/{dayKey}` | one day, presented over the calendar; editable for three days |
| `/settings` | what you track, sign out, delete everything |

## The tap strip

The one custom control. Every answer is a row of named cells drawn on a
`Canvas`, and one tap picks it.

A native picker would cost two taps per field — one to open, one to choose —
and with eight fields that is sixteen taps every evening. The whole product
rests on the daily log taking well under a minute, so the control that costs
the least wins. It also keeps the answer visible next to the alternatives
instead of collapsing it into a summary line.

It is drawn against a transparent canvas so it inherits whatever surface the
platform is painting. The one thing that cannot be inherited is contrast,
which is why the idle cell and its label come from `Device.watch`
(`prefersDark`) — two colours flip, and everything else in the palette is
mid-toned enough to read on both.

## Not in this version

- Notifications, so there is no reminder to log. That is the single feature
  that would help the habit most, and it is why the daily screen is so hard
  on friction: the app has to assume you open it because you want to.
- A shareable report to take to an appointment.
- Custom factors of your own.
