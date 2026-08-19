# How the music is made

`Music.mar` is **generated**. Edit `compose.mjs` and `music-header.mar.in`, not the
`.mar` file itself: the order lists are 76 to 86 numbers per voice per piece, four
voices, five pieces, and hand-editing them is how the 16-step invariant gets
broken.

```bash
node tools/compose.mjs Music.mar
```

`compose.mjs` holds the whole soundtrack as data: the patterns, the movement
form, and the per-movement dynamics. It refuses to write the file unless every
pattern sums to exactly 16 steps, every drum pattern has exactly 16 slots, every
movement names a cycle for every voice, and every piece runs past three minutes.
That is what keeps `Sound.loop`'s invariant (all voices summing to identical
milliseconds) true by construction rather than by anyone counting.

`music-header.mar.in` is the engine half — the four voice builders and the kit —
copied verbatim to the top of the output. It carries the `.in` suffix on purpose:
it declares `module Music`, so leaving it named `.mar` makes the project loader
see two modules of that name and refuse to build. `probe.mar.in` is the same
story.

## Where the music came from

Four of the five pieces are ported from the Star Trek shooter in
`~/Downloads/startrek-monteiro/mar/Main.mar`, where each was a one-minute faction
cue. Movement I of each piece is that cue's original order list, unchanged;
everything after it develops the material. `tr(pattern, semitones)` in the
generator is what makes a restatement in another key cheap — it is a tracker's
oldest tool, and it is why four one-minute cues could become four three-minute
pieces without inventing four new pieces. Mission 5 is original.

The soundtrack this replaced was a filtered ambient bed under three rules: a
harsh-interval budget of 2% (no semitone or tritone anywhere), a 392 Hz ceiling,
and no percussion. Those checks are **deleted rather than relaxed**, because the
Klingon piece opens on a tritone fanfare and the Borg piece is built on a fixed
b2 beep: a budget that rejects those is a budget that rejects the music. What
remains checked is only what can silently break the loop.

## Hearing it without launching the game

The render harness evaluates the module under the **real** JS runtime, so the
voice list is exactly what a browser would schedule, then synthesises it offline.
Only the last mile (voices to PCM) is reimplemented, as a direct port of
`scheduleVoice` in `internal/jsserve/runtime.js`.

```bash
go run tools/tunedump.go Music.mar tools/probe.mar.in > prog.json
node --stack-size=4000 tools/run.mjs $PWD/../../internal/jsserve/runtime.js prog.json ./wav
```

The runtime path must be **absolute**: `render.mjs` requires it relative to its
own file, not to the working directory.

## Is it in tune with itself?

`intervals.mjs` classifies every interval that sounds, over the RENDERED voice
list, and reports how much of the time is harsh. It folds every interval into one
octave, which is what makes it a blunt instrument: a minor 9th between the bass
and the lead is counted as a minor 2nd, so it reported 28% harsh for a piece with
no minor 2nds in it at all, and nobody could act on the number.

`harmony.mjs` answers the question you can act on. It reads the PATTERNS rather
than the render, so it is instant and it can name the pattern to edit; it keeps
the octave, so a minor 2nd and a minor 9th are different findings; and it weighs
by how long the interval is HELD, because a semitone that passes in one step is a
passing tone and the same semitone held for eight is a mistake.

```bash
node tools/harmony.mjs          # all five, worst bars first
node tools/harmony.mjs 4        # one theme, every flagged bar, with pattern ids
node tools/harmony.mjs 4 26     # one bar, note by note, before choosing a fix
```

It also runs a key-fit pass: weigh each pitch class by how many steps it sounds
in the bar, try all 24 diatonic scales, and report what is left over. That is the
detector for the other kind of wrong -- a movement that mixes transposed and
untransposed patterns and ends up with a foot in two keys, which has happened
here once and which no pairwise interval can see.

Use both. `intervals.mjs` tells you what the mix sounds like; `harmony.mjs` tells
you which line to move.

`run.mjs` prints duration, event density, peak, loudness spread, how much of each
piece sits below its own average, and how much lands above 1200 Hz. `bands.mjs`
adds the split below 100 Hz against 100-300 Hz, which is the band a laptop
speaker actually reproduces — a first pass at the old bed measured 97% below
100 Hz, which is not low but inaudible. `seam.mjs` checks the loop seam, hunts
for silent holes, and prints the **voice count**, which is the number that
matters most now: see below.

The renderer is validated against known tones before it is trusted: a 440 Hz
square must show its fundamental far above the noise floor, its odd harmonics in
1/n proportion, no even harmonics, and a triangle must show far less third
harmonic than a square. Run that check before believing any measurement it
produces.

## The voice count is a runtime cost, not just a statistic

These pieces schedule 3700 to 6500 voices per loop (about half of them rests).
The old bed scheduled roughly 500. Both runtimes book a **whole loop period at
once**:

- `soundLoopStart` in `runtime.js` advances its cursor by the full period, so one
  tick creates every node in the piece. Measured in the game: a single 197 ms
  task at mission start, building 2915 oscillators and 599 buffer sources, and it
  repeats every time the loop comes round.
- `MarSound.startLoop` appends every voice to `voices`, and `render` walks that
  whole array once per SAMPLE. Future voices are skipped by a bounds check but
  still visited, so 3500 scheduled voices is roughly 150 million iterations a
  second on the audio thread.

Neither is a property of the music; both are the scheduler booking a period
instead of a slice. If you make a piece denser or longer, check `seam.mjs`'s
voice count first.

## tunedump

`tunedump.go` carries a `//go:build ignore` tag, so `go build ./...` and
`go vet ./...` never see it. Naming it on the `go run` line is what builds it.
