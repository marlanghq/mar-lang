package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/typecheck"
)

// Sound.pan places a voice between the ears. The conformance corpus already
// proves the web and iOS agree about the pan VALUE on a voice; these tests are
// about the thing a value comparison cannot see: the node graph the web synth
// builds from it.
//
// Two claims, and the first is the load-bearing one:
//
//  1. At pan 0 the graph is EXACTLY what it was before pan existed: one edge
//     from the envelope gain to its destination, no extra nodes. That is what
//     makes "no existing sound moved" a fact rather than an argument about
//     arithmetic, and it is why the law is 1.0/1.0 at centre instead of the
//     textbook equal-power 0.707 a StereoPannerNode would apply to a mono input.
//  2. Off centre it builds two constant gains into a 2-input merger, with the
//     gains the law names and each edge on its own merger input.
//
// The existing graph walk in sound_shaping_test.go cannot make either claim: it
// follows only the FIRST outgoing edge of each node and records only biquads, so
// a fan-out into two gains is invisible to it and its assertions stay green
// whatever pan does. These read the node list and the edge list directly.
const soundPanSrc = `module Pan exposing (..)


-- centre, spelled out. The interesting case: an explicit Sound.pan 0 has to
-- build the same graph as a sound that never mentions pan at all.
centred : Sound
centred =
    Sound.pan 0 (Sound.tone Sound.Square 440 200)


-- and a sound that never mentions it, to compare against
plain : Sound
plain =
    Sound.tone Sound.Square 440 200


-- 40% right: both gains present, left attenuated, right still full
right40 : Sound
right40 =
    Sound.pan 40 (Sound.tone Sound.Square 440 200)


-- hard left. Written (0 - 100) because Mar has no unary minus once a function
-- has been applied: Sound.pan -100 s parses as a subtraction.
hardLeft : Sound
hardLeft =
    Sound.pan (0 - 100) (Sound.tone Sound.Square 440 200)


-- out of range, to pin that the clamp happens before the law sees it. Without
-- it gainL would be 1 - 400/100 = -3: a polarity inversion three times too
-- loud, which cancels against the other voices instead of falling silent.
overPanned : Sound
overPanned =
    Sound.pan 400 (Sound.tone Sound.Square 440 200)


-- a CHORD panned once. pan is patchAll, so both layers move; under patchLast
-- (which is what detune does, and what copying detune would give) only the
-- second voice would carry it.
spreadChord : Sound
spreadChord =
    Sound.pan 60
        (Sound.chord
            [ Sound.tone Sound.Triangle 220 400
            , Sound.tone Sound.Triangle 330 400
            ]
        )


playCentred =
    Sound.play centred


playPlain =
    Sound.play plain


playRight40 =
    Sound.play right40


playHardLeft =
    Sound.play hardLeft


playOverPanned =
    Sound.play overPanned
`

// runPanDriver compiles soundPanSrc and runs the given node driver against the
// real runtime.js, exactly as runSoundDriver does for the shaping tests. The
// fake AudioContext here is a separate one on purpose: it has to expose
// createChannelMerger, which the shaping fakes deliberately do not, since their
// job is to prove an unpanned sound never asks for it.
func runPanDriver(t *testing.T, driver string) string {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	mod, err := parser.Parse(soundPanSrc)
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
	for name, body := range map[string]string{
		"runtime.js":   runtimeJS,
		"program.json": string(programJSON),
		"driver.js":    driver,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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

// The fake WebAudio. Records nodes, edges AND the merger input index each edge
// landed on, because the index is where a plausible-looking pan goes wrong:
// `gl.connect(merger)` without it sums both gains into input 0 and pans nothing
// while sounding almost right.
const panFakeAudio = `
let nodes = [], edges = [], nextId = 1;
const param = () => ({
  value: 0,
  setValueAtTime() {}, setTargetAtTime() {}, cancelScheduledValues() {},
  linearRampToValueAtTime() {}, exponentialRampToValueAtTime() {},
});
function mk(kind, extra) {
  const n = Object.assign({ __id: nextId++, __kind: kind,
    connect(dst, outIdx, inIdx) {
      edges.push([this.__id, dst && dst.__id, inIdx === undefined ? -1 : inIdx]);
      return dst;
    },
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
  createChannelMerger: (n) => mk('merger:' + n, {}),
};
globalThis.window = { AudioContext: function () { return ctx; } };
`

// Claim one: pan 0 costs nothing. Counted as "how many nodes does playing this
// sound create", which is the only formulation that cannot be satisfied by a
// merger wired at unity gain.
func TestCentredPanBuildsNoExtraNodes(t *testing.T) {
	got := runPanDriver(t, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
`+panFakeAudio+`
// Kinds created while playing one sound, minus the nodes ensureAudio builds once
// (master gain + ceiling shaper), which are shared by every sound.
function kindsOf(name) {
  globalThis.__marEvalRaw(program, name).run();   // warm: builds master + ceiling
  nodes = []; edges = []; nextId = 100;
  globalThis.__marEvalRaw(program, name).run();
  return name.replace('Pan.play', '') + '=' + nodes.map(n => n.__kind).sort().join(',');
}
process.stdout.write([kindsOf('Pan.playPlain'), kindsOf('Pan.playCentred')].join(' '));
`)
	// A voice is an oscillator plus its envelope gain. Nothing else, and the same
	// nothing else whether pan was named or not.
	want := "Plain=gain,osc Centred=gain,osc"
	if got != want {
		t.Fatalf("an unpanned or centred sound must build the graph it always built.\n got: %s\nwant: %s", got, want)
	}
}

// Claim two: off centre, the law. Reads the two gain values and checks each one
// reached its own merger input.
func TestPanBuildsTwoGainsIntoAMerger(t *testing.T) {
	got := runPanDriver(t, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
`+panFakeAudio+`
function panOf(name) {
  globalThis.__marEvalRaw(program, name).run();   // warm
  nodes = []; edges = []; nextId = 100;
  globalThis.__marEvalRaw(program, name).run();
  const merger = nodes.find(n => n.__kind.startsWith('merger'));
  const label = name.replace('Pan.play', '');
  if (!merger) return label + '=NO-MERGER';
  // The two edges INTO the merger, in input-index order, reporting the gain
  // value of whatever fed each one.
  const byId = new Map(nodes.map(n => [n.__id, n]));
  const ins = edges.filter(e => e[1] === merger.__id)
    .sort((a, b) => a[2] - b[2])
    .map(e => {
      const src = byId.get(e[0]);
      const g = src && src.gain ? src.gain.value : NaN;
      return e[2] + ':' + (Math.round(g * 1000) / 1000);
    });
  return label + '=' + merger.__kind + '[' + ins.join('|') + ']';
}
process.stdout.write([
  panOf('Pan.playRight40'), panOf('Pan.playHardLeft'), panOf('Pan.playOverPanned'),
].join(' '));
`)
	// pan 40  -> left drops to 0.6, right stays 1
	// pan -100 -> left stays 1, right silent
	// pan 400 -> clamped to 100: left silent, right stays 1
	want := "Right40=merger:2[0:0.6|1:1] HardLeft=merger:2[0:1|1:0] OverPanned=merger:2[0:0|1:1]"
	if got != want {
		t.Fatalf("the pan gains or the merger inputs are wrong.\n got: %s\nwant: %s", got, want)
	}
}

// pan is patchAll, and this is the only test that can tell. Every other
// patchAll fixture in the repo wraps a single tone, so a patchLast
// implementation would pass all of them: `Sound.detune` sits two blocks away in
// runtime.js with an IDENTICAL signature and the opposite doctrine, so copying
// the wrong neighbour is a one-character mistake with no test to catch it.
//
// Read off the voice records rather than the graph: patchAll is a claim about
// the value, and the value is where it is decided.
func TestPanPlacesEveryVoiceOfAChord(t *testing.T) {
	got := runPanDriver(t, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const pans = (name) =>
  name.replace('Pan.', '') + '=' +
  globalThis.__marEvalRaw(program, name).voices.map(v => v.pan || 0).join(',');
process.stdout.write([pans('Pan.spreadChord'), pans('Pan.centred')].join(' '));
`)
	// Both layers of the chord, not just the last one.
	want := "spreadChord=60,60 centred=0"
	if got != want {
		t.Fatalf("pan must place EVERY voice (patchAll), not only the last.\n got: %s\nwant: %s", got, want)
	}
}
