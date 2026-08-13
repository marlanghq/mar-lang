package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
// Two properties, and BOTH matter. The ceiling has to be inescapable, or it is
// not a ceiling. It also has to be the exact identity below the knee, or every
// app that was already in range quietly changes tone the day it lands.
func TestMasterCeilingIsSoftAndTransparent(t *testing.T) {
	got := runSoundDriverSrc(t, soundShapingSrc, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
globalThis.window = {};

// The curve is the whole mechanism, and it is pure: read it straight out.
const curve = globalThis.__marSoundCeilingCurve();
const n = curve.length;
const at = (x) => {                       // the table spans the input range -1..1
  const i = Math.round(((x + 1) / 2) * (n - 1));
  return curve[Math.max(0, Math.min(n - 1, i))];
};
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
// input past +-1 clamps to the end of the table, so that entry IS the hard limit
out.push('limitUnder1=' + (Math.abs(curve[n - 1]) < 1 ? 'yes' : 'no:' + curve[n - 1].toFixed(4)));
// monotonic, so it cannot fold a loud signal back down into a quiet one
let mono = true;
for (let i = 1; i < n; i++) if (curve[i] < curve[i - 1] - 1e-6) mono = false;
out.push('monotonic=' + (mono ? 'yes' : 'no'));
// and odd, so it adds no DC offset
out.push('odd=' + (Math.abs(at(0.9) + at(-0.9)) < 1e-3 ? 'yes' : 'no'));
process.stdout.write(out.join(' '));
`)
	want := "id(0)=yes id(0.1)=yes id(0.35)=yes id(0.6)=yes id(0.69)=yes " +
		"maxUnder1=yes limitUnder1=yes monotonic=yes odd=yes"
	if got != want {
		t.Fatalf("the master ceiling does not hold its two promises.\n got: %s\nwant: %s\n"+
			"an id(...)=no means a mix that was already in range now sounds different;\n"+
			"a maxUnder1/limitUnder1=no means the output can still be driven past full scale and clip.", got, want)
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
}
