package jsserve

import "testing"

// The loop scheduler had no behavioural coverage at all: every sound test in
// this repo checks the VALUE layer (the voice records a Sound produces, and
// that the three runtimes agree on them), and nothing checked what
// soundLoopStart DOES with those records. A scheduler can book the wrong times,
// book a voice twice, drop the last note of every period, or dump a whole piece
// in one synchronous burst, and `go test ./...` stays green.
//
// That last one was not hypothetical. It booked a WHOLE PERIOD per wake, which
// is fine for a four-bar jingle and quietly fatal for a three-minute score:
// measured in Iron Meridian, one 197 ms task building 2915 oscillators, at the
// start of a mission and again every time the loop came round.
//
// These tests drive the real soundLoopStart against a fake AudioContext with a
// clock the test moves by hand, so what they assert is the actual booking.
const soundLoopSrc = `module Loop exposing (..)


-- A long piece: 40 notes of 100 ms, so the period is 4000 ms and the onsets
-- land on exact multiples of 100. Stands in for a score.
piece : Sound
piece =
    Sound.sequence
        (List.map
            (\i -> Sound.tone Sound.Square (200 + i * 10) 100)
            (List.range 0 39)
        )


-- pocket-synth's struck patch at its shortest: four voices, all at delay 0,
-- deliberately UNEQUAL, and a period of 140 ms — shorter than the 140 ms
-- look-ahead horizon, so more than one period comes due in a single wake.
struck : Sound
struck =
    Sound.chord
        [ Sound.tone Sound.Sawtooth 880 30
        , Sound.tone Sound.Square 440 80
        , Sound.tone Sound.Square 440 140
        , Sound.tone Sound.Triangle 880 110
        ]


-- A period defined by a TRAILING REST: one 100 ms note then 900 ms of silence.
-- The rest sounds nothing, but it is what makes the loop 1000 ms long, so a
-- scheduler that drops rests before computing the period plays this ten times
-- too fast.
padded : Sound
padded =
    Sound.sequence
        [ Sound.tone Sound.Square 440 100
        , Sound.rest 900
        ]
`

// A fake WebAudio whose clock the driver moves, plus a captured setInterval so
// the pump can be stepped by hand. Every oscillator start(t) is recorded as
// milliseconds, which is the schedule the loop actually booked.
const soundLoopHarness = `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));

let now = 0;
let booked = [];
const param = () => ({
  value: 0,
  setValueAtTime() {}, setTargetAtTime() {}, cancelScheduledValues() {},
  linearRampToValueAtTime() {}, exponentialRampToValueAtTime() {},
});
function mk(kind, extra) {
  return Object.assign({
    __kind: kind,
    connect(dst) { return dst; },
    disconnect() {}, setPeriodicWave() {}, stop() {},
    start(t) { if (kind === 'osc' || kind === 'src') booked.push(Math.round(t * 1000)); },
  }, extra);
}
const ctx = {
  get currentTime() { return now; },
  sampleRate: 44100, state: 'running', resume() {},
  destination: { connect() {} },
  createGain: () => mk('gain', { gain: param() }),
  createOscillator: () => mk('osc', { frequency: param(), detune: param(), type: '' }),
  createBufferSource: () => mk('src', { playbackRate: param(), detune: param(), buffer: null, loop: false }),
  createBiquadFilter: () => mk('biquad', { frequency: param(), Q: param(), type: '' }),
  createBuffer: (ch, len) => ({ getChannelData: () => new Float32Array(len) }),
  createPeriodicWave: () => ({}),
  createWaveShaper: () => mk('shaper', { curve: null, oversample: '' }),
};
globalThis.window = { AudioContext: function () { return ctx; } };

let pump = null;
globalThis.setInterval = (fn) => { pump = fn; return 1; };
globalThis.clearInterval = () => {};

// Start a loop and step the clock through the given instants (in ms), the way
// setInterval would. Returns every start time booked, plus the per-wake counts
// -- worst is the regression guard: it must not grow with the piece.
function runLoop(name, stepsMs) {
  now = 0; booked = []; pump = null;
  globalThis.__marStartSub('loop', globalThis.__marEvalRaw(program, name));
  const first = booked.length;
  let worst = first, prev = booked.length;
  for (const ms of stepsMs) {
    now = ms / 1000;
    pump();
    if (booked.length - prev > worst) worst = booked.length - prev;
    prev = booked.length;
  }
  return { first: first, worst: worst, all: booked.slice() };
}
`

// THE REGRESSION. A three-minute score is ~3000 sounding voices; booking them
// in one wake is the 197 ms freeze. The horizon is 0.14 s, so one wake may only
// book what starts inside it — for a 100 ms grid that is a couple of notes, and
// it must not grow with the length of the piece.
func TestLoopBooksASliceNotAWholePeriod(t *testing.T) {
	got := runSoundDriverSrc(t, soundLoopSrc, soundLoopHarness+`
// Wake every 30 ms, as setInterval does, across a whole period and a bit.
const steps = [];
for (let ms = 30; ms <= 4200; ms += 30) steps.push(ms);
const r = runLoop('Loop.piece', steps);
process.stdout.write('worst=' + r.worst + ' total=' + r.all.length);
`)
	// The notes are 100 ms apart and the horizon is 140 ms, so no wake can ever
	// owe more than ONE of them per 30 ms wake, however long the piece is.
	// come due inside [0, 4200+140) ms: the forty of the first pass plus the
	// first three of the second.
	if got != "worst=1 total=43" {
		t.Fatalf("a single wake booked more than its slice.\n got: %s\nwant: worst=1 total=43\n"+
			"A worst near 40 means the whole period is booked at once — the 197 ms freeze is back.", got)
	}
}

// Booking a slice is only correct if the slice lands where the period would
// have. Every onset must be origin + k*period + delayMs, exactly once.
func TestLoopBooksEveryVoiceOnceAtItsOwnOnset(t *testing.T) {
	got := runSoundDriverSrc(t, soundLoopSrc, soundLoopHarness+`
const steps = [];
for (let ms = 30; ms <= 4500; ms += 30) steps.push(ms);
const r = runLoop('Loop.piece', steps);
// origin is currentTime + 0.06 = 60 ms; onsets are 60 + 100k.
const want = [];
for (let k = 0; k < r.all.length; k++) want.push(60 + 100 * k);
const sorted = r.all.slice().sort((a, b) => a - b);
const dupes = sorted.filter((x, i) => i > 0 && x === sorted[i - 1]);
process.stdout.write(
  (JSON.stringify(sorted) === JSON.stringify(want) ? 'onsets=exact' : 'onsets=' + JSON.stringify(sorted.slice(0, 8))) +
  ' dupes=' + dupes.length +
  ' wrapped=' + (r.all.length > 40 ? 'yes' : 'no'));
`)
	// Past 4000 ms the loop must wrap into its second pass and keep counting on
	// the same 100 ms grid: that is what proves the cursor re-anchors by adding
	// the period rather than re-reading the wall clock.
	if got != "onsets=exact dupes=0 wrapped=yes" {
		t.Fatalf("the slice cursor did not reproduce the period's schedule.\n got: %s\n"+
			"want: onsets=exact dupes=0 wrapped=yes", got)
	}
}

// pocket-synth loops a 140 ms sound (a struck note re-attacking under a held
// key). That is shorter than the 140 ms horizon, so ONE wake has to book more
// than one period. A cursor that advances at most one period per wake turns the
// arpeggio into a crawl; this is the shape that catches it.
func TestLoopHandlesAPeriodShorterThanTheHorizon(t *testing.T) {
	got := runSoundDriverSrc(t, soundLoopSrc, soundLoopHarness+`
// Wake every 30 ms out to 600 ms. Onsets are 60 + 140k and all four voices
// share delay 0, so each period contributes exactly four.
const steps = [];
for (let ms = 30; ms <= 600; ms += 30) steps.push(ms);
const r = runLoop('Loop.struck', steps);
const sorted = r.all.slice().sort((a, b) => a - b);
const distinct = [...new Set(sorted)];
const counts = distinct.map(t => sorted.filter(x => x === t).length);
process.stdout.write('onsets=' + JSON.stringify(distinct) +
  ' perOnset=' + JSON.stringify([...new Set(counts)]));
`)
	// 60, 200, 340, 480, 620 — five periods on an unbroken 140 ms grid, four
	// voices each, none doubled and none skipped.
	want := "onsets=[60,200,340,480,620] perOnset=[4]"
	if got != want {
		t.Fatalf("a period shorter than the horizon did not wrap correctly.\n got: %s\nwant: %s\n"+
			"A gap in the grid means the pump advances at most one period per wake, which slows pocket-synth's arp.", got, want)
	}
}

// The period comes from ALL voices, rests included. Most drum and melody cells
// in this repo are `Sound.sequence [ tone, Sound.rest n ]`, so the period is
// very often defined by a Rest — a scheduler that filters rests out before
// measuring plays every one of those tunes far too fast.
func TestLoopPeriodCountsTrailingRests(t *testing.T) {
	got := runSoundDriverSrc(t, soundLoopSrc, soundLoopHarness+`
// padded is a 100 ms note plus 900 ms of rest. Step past three periods.
const r = runLoop('Loop.padded', [500, 1000, 1500, 2000, 2500, 3000]);
process.stdout.write('onsets=' + JSON.stringify(r.all.slice().sort((a, b) => a - b)));
`)
	// One note a second, at 60 / 1060 / 2060 / 3060. If the rest were dropped
	// from the period this would be a 100 ms loop and fire ten times as often.
	want := "onsets=[60,1060,2060,3060]"
	if got != want {
		t.Fatalf("the trailing rest stopped defining the period.\n got: %s\nwant: %s", got, want)
	}
}

// A backgrounded tab throttles setInterval, so a wake can arrive long after the
// last one. Web Audio clamps a start time in the past to "now", so booking the
// whole missed stretch would fire all of it at once — the exact burst the slice
// scheduler exists to avoid. Missed audio is DROPPED.
func TestLoopDropsWhatIsAlreadyPastInsteadOfBursting(t *testing.T) {
	got := runSoundDriverSrc(t, soundLoopSrc, soundLoopHarness+`
// Start, then jump ten seconds — two and a half periods of a 40-note piece.
// A catch-up would book ~1000 voices, all in the past.
const r = runLoop('Loop.piece', [10000]);
const late = r.all.filter(t => t < 10000).length;
process.stdout.write('afterJump=' + (r.all.length - r.first) + ' inThePast=' + late);
`)
	// The jump wake may only book what starts inside [10.0s, 10.14s): one note on
	// a 100 ms grid, and nothing at a time already gone. A catch-up would book
	// the ~99 notes of the missed stretch, every one of them in the past, and
	// Web Audio would fire the lot at once.
	if got != "afterJump=1 inThePast=1" {
		t.Fatalf("a stalled pump did not drop the missed stretch.\n got: %s\n"+
			"want: afterJump=1 inThePast=1 (the one voice in the past is the first wake's, at 60 ms)", got)
	}
}
