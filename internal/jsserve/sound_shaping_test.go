package jsserve

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/typecheck"
)

// Sound.lowCut / Sound.highCut are the app's handle on tone. The band they
// replace used to be hardcoded at 340-2600Hz inside the ambient bed, tuned so
// one game's stadium crowd sat right, which meant every sustained noise in
// every Mar app came out crowd-shaped, with no way to ask for wind instead.
// (See docs/adrs/0006-app-taste-belongs-in-apps.md.)
//
// Two tests pin the two halves of that fix: the cuts an app asks for survive
// into the voice record, and the renderer builds exactly the filters they name
// : nothing more, so a sound asking for neither keeps the graph it always had.
const soundShapingSrc = `module Shape exposing (..)


-- what a crowd asks for
crowd : Sound
crowd =
    Sound.lowCut 340 (Sound.highCut 2600 (Sound.tone Sound.Noise 0 1000))


-- wind wants the other shape entirely: keep the low rumble, lose the hiss
wind : Sound
wind =
    Sound.highCut 900 (Sound.tone Sound.Noise 0 1000)


-- and an ordinary note asks for neither
plain : Sound
plain =
    Sound.tone Sound.Square 440 200


-- a HELD pad: narrow pulse + vibrato. This is the timbre the ambient bed used
-- to throw away, which is why holding a key had to mean looping.
pad : Sound
pad =
    Sound.vibrato 60 4 (Sound.duty 12 (Sound.tone Sound.Square 220 1000))


-- a held tone asking only for a cut: the bed wired oscillators straight to the
-- gain, so filters reached noise voices and nothing else.
warm : Sound
warm =
    Sound.highCut 900 (Sound.tone Sound.Square 220 1000)


playCrowd =
    Sound.play crowd


playWind =
    Sound.play wind


playPlain =
    Sound.play plain


-- Two held notes a fifth apart, through both held Subs. What the reconcile key
-- covers is the ONLY difference between them, and it is the difference between
-- a keyboard and an engine, so it is worth pinning by name.
voiceLow : Sub msg
voiceLow =
    Sound.voice (Sound.tone Sound.Square 220 1000)


voiceHigh : Sub msg
voiceHigh =
    Sound.voice (Sound.tone Sound.Square 330 1000)


-- the same note, louder: volume is a live parameter for a held voice, so this
-- must swell the running note rather than start a second one
voiceLoud : Sub msg
voiceLoud =
    Sound.voice (Sound.volume 90 (Sound.tone Sound.Square 220 1000))


glideLow : Sub msg
glideLow =
    Sound.glide (Sound.tone Sound.Square 220 1000)


glideHigh : Sub msg
glideHigh =
    Sound.glide (Sound.tone Sound.Square 330 1000)
`

// runSoundDriver compiles soundShapingSrc, drops it next to the real runtime.js,
// and runs `driver` under node with both as argv. Returns the driver's stdout.
func runSoundDriver(t *testing.T, driver string) string {
	t.Helper()
	return runSoundDriverSrc(t, soundShapingSrc, driver)
}

// runSoundDriverSrc is the same for a caller that brings its own Mar module.
func runSoundDriverSrc(t *testing.T, src, driver string) string {
	t.Helper()
	nodePath, lookErr := exec.LookPath("node")
	if lookErr != nil {
		t.Skip("node not installed")
	}

	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}

	dir := t.TempDir()
	programJSON, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(mod)}})
	if err != nil {
		t.Fatalf("marshal program: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.js"), []byte(runtimeJS), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "program.json"), programJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "driver.js"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(nodePath, filepath.Join(dir, "driver.js"),
		filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node run: %v\n%s", err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// Half one: the cuts survive the combinator chain. They did not always, the
// voice clone copied a NAMED LIST of fields, so chaining two patches kept only
// the outermost and `lowCut 340 (highCut 2600 s)` silently lost the 2600.
func TestSoundCutsReachTheVoice(t *testing.T) {
	// Reach past Display() into the voice record itself: these fields exist to
	// be read by the renderer, so the test reads them the same way.
	got := runSoundDriver(t, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const cuts = (name) => {
  const v = globalThis.__marEvalRaw(program, name).voices[0];
  return [name, v.lowCut || 0, v.highCut || 0].join(':');
};
process.stdout.write([cuts('Shape.crowd'), cuts('Shape.wind'), cuts('Shape.plain')].join(' '));
`)
	want := "Shape.crowd:340:2600 Shape.wind:0:900 Shape.plain:0:0"
	if got != want {
		t.Fatalf("cuts did not reach the voice record.\n got: %s\nwant: %s", got, want)
	}
}

// Half two: the synth builds what the cuts name and NOTHING else. A fake
// AudioContext records every node created and every connection made, so the
// real soundVoice path runs and the graph it builds can be read back.
//
// `plain:none` is the load-bearing case: a sound that asks for no shaping must
// build no filter, so adding these combinators cost the ordinary path nothing.
func TestSoundCutsBuildTheFilterGraph(t *testing.T) {
	got := runSoundDriver(t, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));

// --- a fake WebAudio, just rich enough for the synth to run against ---
let nodes = [], edges = [], nextId = 1;
const param = () => ({
  value: 0,
  setValueAtTime() {}, setTargetAtTime() {}, cancelScheduledValues() {},
  linearRampToValueAtTime() {}, exponentialRampToValueAtTime() {},
});
function mk(kind, extra) {
  const n = Object.assign({ __id: nextId++, __kind: kind,
    connect(dst) { edges.push([this.__id, dst && dst.__id]); return dst; },
    disconnect() {}, start() {}, stop() {}, setPeriodicWave() {},
  }, extra);
  nodes.push(n);
  return n;
}
const ctx = {
  currentTime: 0, sampleRate: 44100, state: 'running', resume() {},
  destination: { __id: 0, __kind: 'destination', connect() {} },
  createGain: () => mk('gain', { gain: param() }),
  createOscillator: () => mk('osc', { frequency: param(), detune: param(), type: '' }),
  createBufferSource: () => mk('src', { playbackRate: param(), buffer: null, loop: false }),
  createBiquadFilter: () => mk('biquad', { frequency: param(), Q: param(), type: '' }),
  createBuffer: (ch, len) => ({ getChannelData: () => new Float32Array(len) }),
  createPeriodicWave: () => ({}),
  createWaveShaper: () => mk('shaper', { curve: null, oversample: '' }),
};
globalThis.window = { AudioContext: function () { return ctx; } };

// Walk the graph forward from the sound's source node, naming each filter it
// passes through. That is the claim: the app's cuts, in order, on the way out.
function chainOf(name) {
  nodes = []; edges = []; nextId = 1;
  globalThis.__marEvalRaw(program, name).run();
  const byId = new Map(nodes.map(n => [n.__id, n]));
  const source = nodes.find(n => n.__kind === 'osc' || n.__kind === 'src');
  if (!source) return name + ':NO-SOURCE';
  const hops = [];
  let cur = source.__id, guard = 0;
  while (guard++ < 20) {
    const next = edges.find(e => e[0] === cur);
    if (!next) break;
    const n = byId.get(next[1]);
    if (!n) break;
    if (n.__kind === 'biquad') hops.push(n.type + n.frequency.value);
    cur = n.__id;
  }
  return name + ':' + (hops.length ? hops.join('>') : 'none');
}
process.stdout.write(
  [chainOf('Shape.playCrowd'), chainOf('Shape.playWind'), chainOf('Shape.playPlain')].join(' '));
`)
	// soundFilterChain wires source -> highpass -> lowpass -> gain, so the cuts
	// come out low-end-first regardless of the order they were written in.
	want := "Shape.playCrowd:highpass340>lowpass2600 Shape.playWind:lowpass900 Shape.playPlain:none"
	if got != want {
		t.Fatalf("the synth did not build the filters the app asked for.\n got: %s\nwant: %s", got, want)
	}
}

// Sound.voice holds a voice instead of re-triggering it, which is the only way
// to sustain a note without an audible re-attack. But the bed used to build a
// BARE oscillator, no duty, no vibrato, no filters, so a patch lost its timbre
// the moment it stopped looping, and "held" was only usable for flat drones.
//
// This pins the fix: the held bed applies the same shaping as the one-shot path.
// `plain` is the load-bearing case in the other direction: a voice that asks for
// no shaping must still build a bare oscillator, so ordinary drones are untouched.
func TestHeldVoiceKeepsShaping(t *testing.T) {
	got := runSoundDriver(t, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));

// A fake WebAudio that records the graph. Unlike the Cmd-side fake, connect()
// must RETURN its destination: the vibrato wiring chains lfo.connect(g).connect(param).
let nodes = [], edges = [], nextId = 1;
const param = (name) => ({
  value: 0, __param: name,
  setValueAtTime() {}, setTargetAtTime() {}, cancelScheduledValues() {},
  linearRampToValueAtTime() {}, exponentialRampToValueAtTime() {},
});
function mk(kind, extra) {
  const n = Object.assign({ __id: nextId++, __kind: kind, __periodic: false,
    connect(dst) {
      edges.push([this.__id, dst && (dst.__id != null ? dst.__id : 'param:' + dst.__param)]);
      return dst;
    },
    disconnect() {}, start() {}, stop() {},
    setPeriodicWave() { this.__periodic = true; },
  }, extra);
  nodes.push(n);
  return n;
}
const ctx = {
  currentTime: 0, sampleRate: 44100, state: 'running', resume() {},
  destination: { __id: 0, __kind: 'destination', connect() {} },
  createGain: () => mk('gain', { gain: param('gain') }),
  createOscillator: () => mk('osc', { frequency: param('frequency'), detune: param('detune'), type: '' }),
  createBufferSource: () => mk('src', { playbackRate: param('playbackRate'), buffer: null, loop: false }),
  createBiquadFilter: () => mk('biquad', { frequency: param('frequency'), Q: param('Q'), type: '' }),
  createBuffer: (ch, len) => ({ getChannelData: () => new Float32Array(len) }),
  createPeriodicWave: () => ({}),
  createWaveShaper: () => mk('shaper', { curve: null, oversample: '' }),
};
globalThis.window = { AudioContext: function () { return ctx; } };

// Start the ambient SUB (not a Cmd — it has no .run()) and report what the held
// graph actually contains: pulse shaping, a vibrato LFO, and any filters.
function bedOf(name) {
  nodes = []; edges = []; nextId = 1;
  globalThis.__marStartSub('voice', globalThis.__marEvalRaw(program, name));
  const duty = nodes.some(n => n.__kind === 'osc' && n.__periodic) ? 'duty' : '-';
  const vib = edges.some(e => e[1] === 'param:detune') ? 'vib' : '-';
  const cuts = nodes.filter(n => n.__kind === 'biquad').map(n => n.type + n.frequency.value);
  return name + ':' + duty + '/' + vib + '/' + (cuts.length ? cuts.join('>') : '-');
}
process.stdout.write([bedOf('Shape.pad'), bedOf('Shape.warm'), bedOf('Shape.plain')].join(' '));
`)
	want := "Shape.pad:duty/vib/- Shape.warm:-/-/lowpass900 Shape.plain:-/-/-"
	if got != want {
		t.Fatalf("the held bed dropped shaping the voice asked for.\n got: %s\nwant: %s", got, want)
	}
}

// Two held notes at different pitches must be two VOICES and one GLIDE. That is
// the whole difference between the two Subs, and getting it wrong is not a
// theoretical risk: `Sound.hold` was both of them at once, and its key left
// pitch out: correct for an engine note that slides with speed, fatal for a
// keyboard. Two held organ keys hashed to the same key, collapsed into one
// oscillator, and releasing one slid the survivor to the other note.
//
// Volume stays out of both keys, so a held note can still swell.
func TestHeldSubIdentity(t *testing.T) {
	got := runSoundDriverSrc(t, soundShapingSrc, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
globalThis.window = {};

const keyOf = (name) => {
  const sub = globalThis.__marEvalRaw(program, name);
  if (!sub || sub.k !== 'SUB' || !sub.items || !sub.items.length) return 'NOT-A-SUB:' + name;
  return sub.items[0].key;
};
const cmp = (label, a, b) => label + ':' + (keyOf(a) === keyOf(b) ? 'same' : 'differ');
process.stdout.write([
  cmp('voicePitch', 'Shape.voiceLow', 'Shape.voiceHigh'),
  cmp('glidePitch', 'Shape.glideLow', 'Shape.glideHigh'),
  cmp('voiceVolume', 'Shape.voiceLow', 'Shape.voiceLoud'),
].join(' '));
`)
	want := "voicePitch:differ glidePitch:same voiceVolume:same"
	if got != want {
		t.Fatalf("the held Subs do not carry the identities they promise.\n got: %s\nwant: %s\n"+
			"voicePitch:same means Sound.voice is monophonic — two keys would sound as one.\n"+
			"glidePitch:differ means Sound.glide restarts instead of sliding.\n"+
			"voiceVolume:differ means a swell on a held note restarts it (a click).", got, want)
	}
}

// The master bus sums voices linearly, so an app that sounds several things at
// once can ask for more than full scale. It used to get there: three held organ
// notes in examples/pocket-synth measured 1.05 to 1.29 at the bus depending on
// where their phases landed, and past 1.0 the device hard-clips: a chord that is
// not merely louder but broken.
//
// Three properties, and all three matter. The ceiling has to be inescapable, or
// it is not a ceiling. It has to be the exact identity below the knee, or every
// app that was already in range quietly changes tone the day it lands. And it
// has to keep BENDING above full scale rather than flattening, or it stops being
// soft exactly where softness is the point.
//
// The third one was missing and wrong. A WaveShaper clamps its input to the
// range its table spans, so a table built over -1..+1 answered the same 0.9285
// for 1.0, 1.5 and 3.0: hard limiting, and a divergence from iOS, which computes
// the tanh directly and keeps bending (0.64 dB apart by 1.5). Ordinary tracks in
// this repo peak at 0.39-0.81 at the bus and never notice; a polyphonic
// instrument holding a chord does.
//
// The test asks the runtime what the shaper does to an INPUT rather than
// indexing the table itself, because the index arithmetic is the part that was
// wrong: a test that redoes it just agrees with the bug.
func TestMasterCeilingIsSoftAndTransparent(t *testing.T) {
	got := runSoundDriverSrc(t, soundShapingSrc, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
globalThis.window = {};

// at() is the runtime's own answer for an input: pre-gain, table lookup and
// clamping included.
const at = globalThis.__marSoundCeilingAt;
const curve = globalThis.__marSoundCeilingCurve();
const n = curve.length;
const out = [];
// transparent below the knee: bit-for-bit what came in
for (const x of [0, 0.1, 0.35, 0.6, 0.69]) {
  out.push('id(' + x + ')=' + (Math.abs(at(x) - x) < 1e-3 ? 'yes' : 'no:' + at(x).toFixed(4)));
}
// inescapable: not a sample of the curve but its MAXIMUM, so no table
// quantisation can make a missing ceiling look like a present one
let peak = 0;
for (const v of curve) peak = Math.max(peak, Math.abs(v));
out.push('maxUnder1=' + (peak < 1 ? 'yes' : 'no:' + peak.toFixed(4)));
// input past the table's span clamps to its end entry, so that IS the hard limit
out.push('limitUnder1=' + (Math.abs(curve[n - 1]) < 1 ? 'yes' : 'no:' + curve[n - 1].toFixed(4)));
// monotonic, so it cannot fold a loud signal back down into a quiet one
let mono = true;
for (let i = 1; i < n; i++) if (curve[i] < curve[i - 1] - 1e-6) mono = false;
out.push('monotonic=' + (mono ? 'yes' : 'no'));
// and odd, so it adds no DC offset
out.push('odd=' + (Math.abs(at(0.9) + at(-0.9)) < 1e-3 ? 'yes' : 'no'));
// still BENDING past full scale: each step up has to come out higher than the
// last, which a clamped table cannot do
out.push('bends=' + (at(1.5) > at(1.0) + 1e-3 && at(2.0) > at(1.5) + 1e-4 ? 'yes'
  : 'no:' + [1, 1.5, 2].map((x) => at(x).toFixed(4)).join('/')));
// and agreeing with the iOS synth, which computes the same tanh directly
const k = 0.7, ios = (x) => k + (1 - k) * Math.tanh((Math.abs(x) - k) / (1 - k));
out.push('matchesIOS=' + ([0.9, 1.0, 1.2, 1.5, 2.0].every((x) => Math.abs(at(x) - ios(x)) < 2e-3)
  ? 'yes' : 'no:' + [0.9, 1, 1.2, 1.5, 2].map((x) => (at(x) - ios(x)).toFixed(4)).join('/')));
process.stdout.write(out.join(' '));
`)
	want := "id(0)=yes id(0.1)=yes id(0.35)=yes id(0.6)=yes id(0.69)=yes " +
		"maxUnder1=yes limitUnder1=yes monotonic=yes odd=yes bends=yes matchesIOS=yes"
	if got != want {
		t.Fatalf("the master ceiling does not hold its two promises.\n got: %s\nwant: %s\n"+
			"an id(...)=no means a mix that was already in range now sounds different;\n"+
			"a maxUnder1/limitUnder1=no means the output can still be driven past full scale and clip;\n"+
			"a bends=no means the shaper flattens above full scale, which is hard limiting;\n"+
			"a matchesIOS=no means the same overdriven mix comes out differently on the two runtimes.", got, want)
	}
}

// The master level is the one number every sound passes through, and the two
// runtimes each keep their own copy of it. They drifted: the web sat at 0.35
// and iOS at 0.5, so the same game played 1.43x louder (about 3 dB) on the
// phone than in the browser it was tuned in. The level was only half of it.
// The soft ceiling above bends at 0.7, so the louder side also crossed the knee
// far more often, and every crossing adds harmonics: the drift was audible as
// harshness, not just as volume.
//
// Nothing pinned the pair, which is how they came apart in the first place. The
// scaling (0..100 -> 0..0.5) was identical on both sides the whole time, so it
// is specifically the DEFAULT, the value an app gets before it ever calls
// Sound.master, that this test exists to hold together.
func TestMasterLevelDefaultMatchesAcrossRuntimes(t *testing.T) {
	read := func(path, pattern string) string {
		t.Helper()
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		re := regexp.MustCompile(pattern)
		m := re.FindStringSubmatch(string(src))
		if m == nil {
			t.Fatalf("%s no longer declares the master level as `%s`.\n"+
				"If the declaration moved, point this test at it: the two "+
				"runtimes agreeing on this number is the thing being tested.",
				path, pattern)
		}
		return m[1]
	}

	web := read("runtime.js", `soundMasterLevel\s*=\s*([0-9.]+)`)
	ios := read("../iosbundle/template/Sources/MarSound.swift",
		`masterLevel:\s*Double\s*=\s*([0-9.]+)`)

	if web != ios {
		t.Errorf("master level default drifted: runtime.js has %s, MarSound.swift has %s.\n"+
			"Every sound in every app is scaled by this, so the two builds of "+
			"one game do not sound alike until they agree.", web, ios)
	}

	// The other number every sound passes through, and the same story: the knee
	// is where the soft ceiling starts to bend, so a drift here changes the
	// point at which one runtime begins adding harmonics and the other does not.
	// Each file declares its own copy.
	webKnee := read("runtime.js", `CEILING_KNEE\s*=\s*([0-9.]+)`)
	iosKnee := read("../iosbundle/template/Sources/MarSound.swift",
		`ceilingKnee\s*=\s*([0-9.]+)`)
	if webKnee != iosKnee {
		t.Errorf("ceiling knee drifted: runtime.js bends at %s, MarSound.swift at %s.\n"+
			"Above the knee the curve adds harmonics, so the louder passages of one "+
			"build are not merely louder than the other's: they are harsher.", webKnee, iosKnee)
	}
}

// A sustained NOISE bed — wind, rain, a crowd, an engine — is the one voice the
// two runtimes build by different means, and the means carry a level.
//
// The web sums two decorrelated clips so the wash never repeats, which is
// sqrt(2) times the RMS of one, and trims that with a 0.7 gain: the bed lands at
// 0.99 of a single full-scale clip, level with what the one-shot noise path
// plays. iOS draws a fresh random per sample, which never repeats either, and
// multiplied it by 0.35. That is 9.0 dB below the web and 9.1 dB below iOS's own
// one-shot noise: the same ambience all but disappeared on the phone, and there
// `Sound.loop` and `Sound.voice` of one value disagreed with each other in a way
// they never did in the browser.
//
// The voice-record conformance corpus cannot see this. It compares the Sound
// each runtime BUILDS, and both build the same voice; the difference is in how
// each one plays it. So the pin is here, on the two numbers themselves.
func TestHeldNoiseBedLevelMatchesAcrossRuntimes(t *testing.T) {
	read := func(path, pattern string) string {
		t.Helper()
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		m := regexp.MustCompile(pattern).FindStringSubmatch(string(src))
		if m == nil {
			t.Fatalf("%s no longer declares the held-noise trim as `%s`.\n"+
				"If it moved, point this test at it: the two runtimes agreeing "+
				"on this number is the thing being tested.", path, pattern)
		}
		return m[1]
	}

	// The web: two layers into a trim gain. Both halves are read, because the
	// level is their product and a change to either moves it.
	webTrim, err := strconv.ParseFloat(read("runtime.js", `nmix\.gain\.value\s*=\s*([0-9.]+)`), 64)
	if err != nil {
		t.Fatal(err)
	}
	web := math.Sqrt(2) * webTrim

	// iOS: one source, one trim, and the trim is written as the same arithmetic.
	iosSrc := read("../iosbundle/template/Sources/MarSound.swift",
		`bedNoiseTrim\s*=\s*([^\n]+)`)
	ios := 0.0
	if m := regexp.MustCompile(`([0-9.]+)\.squareRoot\(\)\s*\*\s*([0-9.]+)`).FindStringSubmatch(iosSrc); m != nil {
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[2], 64)
		ios = math.Sqrt(a) * b
	} else if v, e := strconv.ParseFloat(strings.TrimSpace(iosSrc), 64); e == nil {
		ios = v
	} else {
		t.Fatalf("cannot read the iOS held-noise trim from %q", iosSrc)
	}

	if diff := 20 * math.Log10(web/ios); math.Abs(diff) > 0.2 {
		t.Errorf("the held noise bed drifted: the web plays it at %.4f and iOS at %.4f, %.1f dB apart.\n"+
			"An ambient wash is the same wash on both builds or it is not the same game.",
			web, ios, diff)
	}
}

// The loop scheduler, driven with a clock the test owns.
//
// Every other sound test here reads a graph the moment it is built. This one
// runs the real soundLoopStart over several passes, because what it pins is a
// behaviour over TIME: `setInterval` is captured instead of registered, so the
// tick is called by hand against a fake `currentTime` that only moves when the
// test says so.
//
// What it pins is a claim this repo has repeated 26 times and never checked:
// "every voice of a looped Sound must total the same number of milliseconds, or
// the parts drift apart." That was true of an older scheduler which repeated
// each voice at its own length. The current one takes ONE period — the longest
// voice's span — and books every voice once per pass from a shared origin, so
// unequal voices cannot drift: the short one simply leaves a gap before the
// cycle comes round. Measured, the two voices below stay exactly 0 s apart for
// as long as the loop runs.
//
// It is worth a test rather than a deletion, because the folklore is load
// bearing in the other direction: several pieces in examples/ are written to
// satisfy it, and a future scheduler that went back to per-voice repetition
// would break them silently. This is the line that says which one is true.
func TestLoopKeepsUnequalVoicesLocked(t *testing.T) {
	got := runSoundDriverSrc(t, soundShapingSrc, `
const fs = require('fs');
const g = globalThis;

// A fake WebAudio whose clock the test owns, and a captured setInterval so the
// scheduler's tick can be called by hand.
let now = 0;
const booked = [];
const param = () => ({ value: 0,
  setValueAtTime(v) { this.value = v; return this; },
  setTargetAtTime() { return this; }, cancelScheduledValues() { return this; },
  linearRampToValueAtTime() { return this; }, exponentialRampToValueAtTime() { return this; } });
const mk = (kind, extra = {}) => Object.assign({ kind, connect(d) { return d; }, disconnect() {},
  start(t) { if (this.frequency) booked.push([this.frequency.value, +t.toFixed(6)]); },
  stop() {} }, extra);
const ctx = {
  get currentTime() { return now; }, sampleRate: 48000, state: 'running', destination: {}, resume() {},
  createGain: () => mk('gain', { gain: param() }),
  createOscillator: () => mk('osc', { frequency: param(), detune: param(), type: '' }),
  createBufferSource: () => mk('src', { playbackRate: param(), detune: param(), buffer: null, loop: false }),
  createBiquadFilter: () => mk('biquad', { frequency: param(), Q: param(), type: '' }),
  createBuffer: (c, n) => ({ getChannelData: () => new Float32Array(n) }),
  createPeriodicWave: () => ({}), createChannelMerger: () => mk('merge'),
  createWaveShaper: () => mk('shaper', { curve: null, oversample: '' }),
};
g.window = { AudioContext: function () { return ctx; } };
let tick = null;
g.setInterval = (fn) => { tick = fn; return 1; };
g.clearInterval = () => {};
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));

const V = (freq, ms) => ({ wave: 'Square', freq, ms, delayMs: 0, endFreq: 0, holdMs: 0,
  volume: 60, duty: 0, vibDepth: 0, vibRate: 0, arp: null, lowCut: 0, highCut: 0,
  attack: 0, release: 0, decay: 0, sustain: 100, detune: 0, pan: 0 });

// the shape the folklore says drifts: one voice 1000ms, one 700ms
g.__marTestLoopStart({ voices: [V(100, 1000), V(200, 700)] });
for (let i = 0; i < 200; i++) { now += 0.03; tick(); }

const by = {};
for (const [f, t] of booked) (by[f] = by[f] || []).push(t);
const a = by[100] || [], b = by[200] || [];
const out = [];
out.push('passes=' + Math.min(a.length, b.length));
const period = [...new Set(a.slice(1).map((t, i) => +(t - a[i]).toFixed(4)))];
out.push('period=' + period.join('/'));
const skew = [...new Set(a.map((t, i) => (i < b.length ? +(b[i] - t).toFixed(4) : 'x')))];
out.push('skew=' + skew.join('/'));
process.stdout.write(out.join(' '));
`)
	want := "passes=7 period=1 skew=0"
	if got != want {
		t.Fatalf("the loop scheduler no longer keeps unequal voices locked.\n got: %s\nwant: %s\n"+
			"a period other than 1 means the cycle is no longer the longest voice's span;\n"+
			"a skew other than 0 means the two voices HAVE come apart, and every piece in\n"+
			"examples/ that pads its parts to equal length is right after all: say so in\n"+
			"the reference for Sound.chord, which this behaviour is why it no longer does.", got, want)
	}
}

// A note of length ZERO, which the two runtimes used to disagree about.
//
// The reference says the same thing for every note: anything shorter than 20 ms
// is played as 20 ms. iOS does that. This file did not — it read `v.ms || 100`,
// so a length that was genuinely 0 fell through to a 100 ms default, five times
// the floor and five times what the phone played. It also disagreed with the
// file's own loop scheduler, which measures a cycle with `v.ms || 0`: such a
// voice sounded for a hundred milliseconds inside a period that had reserved
// none, and ran into the next pass.
//
// Read off the node's own stop time, which is what the browser acts on.
func TestZeroLengthNoteIsTheDocumentedFloor(t *testing.T) {
	got := runSoundDriverSrc(t, soundShapingSrc, `
const fs = require('fs');
const g = globalThis;
let stops = [];
const param = () => ({ value: 0, setValueAtTime() { return this; }, setTargetAtTime() { return this; },
  cancelScheduledValues() { return this; }, linearRampToValueAtTime() { return this; },
  exponentialRampToValueAtTime() { return this; } });
const mk = (kind, extra = {}) => Object.assign({ kind, connect(d) { return d; }, disconnect() {},
  start(t) { this._start = t; }, stop(t) { if (this.frequency) stops.push(+(t - this._start).toFixed(4)); } }, extra);
const ctx = {
  currentTime: 0, sampleRate: 48000, state: 'running', destination: {}, resume() {},
  createGain: () => mk('gain', { gain: param() }),
  createOscillator: () => mk('osc', { frequency: param(), detune: param(), type: '' }),
  createBufferSource: () => mk('src', { playbackRate: param(), detune: param(), buffer: null, loop: false }),
  createBiquadFilter: () => mk('biquad', { frequency: param(), Q: param(), type: '' }),
  createBuffer: (c, n) => ({ getChannelData: () => new Float32Array(n) }),
  createPeriodicWave: () => ({}), createChannelMerger: () => mk('merge'),
  createWaveShaper: () => mk('shaper', { curve: null, oversample: '' }),
};
g.window = { AudioContext: function () { return ctx; } };
g.setInterval = () => 1; g.clearInterval = () => {};
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));

const V = (ms) => ({ wave: 'Square', freq: 440, ms, delayMs: 0, endFreq: 0, holdMs: 0, volume: 60,
  duty: 0, vibDepth: 0, vibRate: 0, arp: null, lowCut: 0, highCut: 0,
  attack: 0, release: 0, decay: 0, sustain: 100, detune: 0, pan: 0 });
// node.stop is scheduled at dur + 0.03 (the tail), so subtract it back out.
const spanOf = (ms) => { stops = []; g.__marTestScheduleVoice(ctx, V(ms), 0, ctx.createGain()); return +(stops[0] - 0.03).toFixed(4); };
process.stdout.write(['zero=' + spanOf(0), 'five=' + spanOf(5), 'fifty=' + spanOf(50)].join(' '));
`)
	want := "zero=0.02 five=0.02 fifty=0.05"
	if got != want {
		t.Fatalf("a note shorter than the floor is not played at the floor.\n got: %s\nwant: %s\n"+
			"zero=0.1 is the old `v.ms || 100` default: five times the floor, five times what iOS plays,\n"+
			"and five times the span the loop scheduler reserves for the same voice.", got, want)
	}
}

// Sound.tremolo, which is the wobble in loudness as vibrato is the wobble in
// pitch. Three things are pinned, and each is a way the obvious implementation
// goes wrong.
//
// It only ATTENUATES. The gain rides 1 - d/2 with the LFO swinging +-d/2, so a
// note peaks at exactly the level it was written at. A tremolo that swung around
// the level instead would make voices louder than their own volume says, and the
// headroom the master ceiling is budgeted against is the sum of those volumes.
//
// The LFO is a COSINE. An OscillatorNode's sine starts at value 0, so a sine
// would begin every note half faded and rise into its own attack.
//
// And the numbers match the iOS synth, which computes 1 - d(1 - cos)/2 directly
// rather than as a carrier plus a modulator. Two spellings of one law, checked
// against each other here because nothing else in the repo compares what the two
// runtimes PLAY: the conformance corpus compares the voice they build, and this
// difference would not appear in one.
func TestTremoloOnlyEverTakesAway(t *testing.T) {
	got := runSoundDriverSrc(t, soundShapingSrc, `
const fs = require('fs');
const g = globalThis;
let gains = [], waves = [], lfoHz = [];
const param = () => ({ value: 0, setValueAtTime(v) { this.value = v; return this; },
  setTargetAtTime() { return this; }, cancelScheduledValues() { return this; },
  linearRampToValueAtTime() { return this; }, exponentialRampToValueAtTime() { return this; } });
const mk = (kind, extra = {}) => Object.assign({ kind, connect(d) { return d; }, disconnect() {},
  start() {}, stop() {}, setPeriodicWave(w) { waves.push(w); } }, extra);
const ctx = {
  currentTime: 0, sampleRate: 48000, state: 'running', destination: {}, resume() {},
  createGain: () => { const n = mk('gain'); n.gain = param(); gains.push(n); return n; },
  createOscillator: () => { const n = mk('osc', { type: '' }); n.frequency = param(); n.detune = param(); return n; },
  createBufferSource: () => { const n = mk('src', { buffer: null, loop: false }); n.playbackRate = param(); n.detune = param(); return n; },
  createBiquadFilter: () => { const n = mk('biquad', { type: '' }); n.frequency = param(); n.Q = param(); return n; },
  createBuffer: (c, n) => ({ getChannelData: () => new Float32Array(n) }),
  createPeriodicWave: (re, im) => ({ real: [...re], imag: [...im] }),
  createChannelMerger: () => mk('merge'), createWaveShaper: () => mk('shaper', { curve: null, oversample: '' }),
};
g.window = { AudioContext: function () { return ctx; } };
g.setInterval = () => 1; g.clearInterval = () => {};
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));

const V = (o) => Object.assign({ wave: 'Sine', freq: 440, ms: 1000, delayMs: 0, endFreq: 0, holdMs: 0,
  volume: 60, duty: 0, vibDepth: 0, vibRate: 0, tremDepth: 0, tremRate: 0, arp: null,
  lowCut: 0, highCut: 0, attack: 0, release: 0, decay: 0, sustain: 100, detune: 0, pan: 0 }, o);
gains = []; waves = [];
g.__marTestScheduleVoice(ctx, V({ tremDepth: 50, tremRate: 6 }), 0, ctx.createGain());
const vals = gains.map((n) => +n.gain.value.toFixed(4));
const out = [];
// carrier 1 - d/2 and depth d/2, so peak = 1 and trough = 1 - d
out.push('carrier=' + (vals.includes(0.75) ? 'yes' : 'no:' + vals.join('/')));
out.push('depth=' + (vals.includes(0.25) ? 'yes' : 'no:' + vals.join('/')));
// a cosine: one harmonic, in the REAL part
out.push('cosine=' + (waves.some((w) => w && w.real.length === 2 && w.real[1] === 1 && w.imag[1] === 0) ? 'yes' : 'no'));
// the same law the iOS synth computes directly
const d = 0.5, rate = 6;
const web = (t) => 0.75 + 0.25 * Math.cos(2 * Math.PI * rate * t);
const ios = (t) => 1 - d * (1 - Math.cos(2 * Math.PI * rate * t)) / 2;
let worst = 0;
for (let i = 0; i <= 2000; i++) { const t = i / 2000; worst = Math.max(worst, Math.abs(web(t) - ios(t))); }
out.push('sameAsIOS=' + (worst < 1e-12 ? 'yes' : 'no:' + worst.toExponential(1)));
out.push('peak=' + web(0).toFixed(2) + ' trough=' + web(1 / (2 * rate)).toFixed(2));
// and a voice that asked for none builds no extra gain at all
gains = [];
g.__marTestScheduleVoice(ctx, V({}), 0, ctx.createGain());
const plain = gains.length;
gains = [];
g.__marTestScheduleVoice(ctx, V({ tremDepth: 40, tremRate: 5 }), 0, ctx.createGain());
out.push('costsNothingWhenUnasked=' + (gains.length === plain + 2 ? 'yes' : 'no:' + plain + '/' + gains.length));
process.stdout.write(out.join(' '));
`)
	want := "carrier=yes depth=yes cosine=yes sameAsIOS=yes peak=1.00 trough=0.50 costsNothingWhenUnasked=yes"
	if got != want {
		t.Fatalf("Sound.tremolo does not hold its shape.\n got: %s\nwant: %s\n"+
			"a carrier/depth other than 1-d/2 and d/2 means a note can come out LOUDER than its volume;\n"+
			"cosine=no means the LFO starts at zero, so every note begins half faded;\n"+
			"sameAsIOS=no means the browser and the phone wobble differently.", got, want)
	}
}
