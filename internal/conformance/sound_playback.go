package conformance

// The playback half of the Sound conformance corpus.
//
// sound.go compares the voice list each runtime BUILDS from a Sound, and says
// in its own header that it cannot see further than that. This file is what
// stands on the other side: the two drivers that ask each runtime what it would
// actually PLAY, sampled as curves.
//
// The cost of not having had it is on the record. Four defects fixed on
// 2026-08-20 all lived past the voice record, and not one of them was visible
// in one: the web's master ceiling flattened above full scale while iOS kept
// bending; a held noise bed was 9.0 dB quieter on iOS; a note of length zero
// played 100 ms on the web and 20 ms on iOS; and Sound.detune on an unpitched
// noise voice moved the browser and not the phone.
//
// WHAT IS COMPARED, per voice:
//
//	span     the length the runtime gives the note, read off its own scheduling
//	gain(t)  the scalar the raw source is multiplied by: envelope, level, tremolo
//	freq(t)  the pitch actually sounding: sweep or arpeggio, detune, vibrato
//
// and what is deliberately left alone: pan (a placement, already compared as a
// record field, and its law has its own test), the master level (one number for
// the whole app, pinned separately), the WAVEFORM (the browser must band-limit
// its oscillators and iOS uses polyBLEP - different by construction), the noise
// samples (random on both), and the filters (a biquad against a one-pole, a
// known divergence specified in docs/proposals/sound-mixer.md).
//
// THE HONESTY QUESTION, which decides whether any of this is worth running.
// Neither driver computes an answer:
//
//   - iOS calls MarSound.conformanceCurves, which uses the decoder the player
//     uses, the duration rule the scheduler uses, and controlAt - the function
//     the audio thread calls for every sample. Not a copy of it.
//   - the web cannot be asked directly, because it does not compute these
//     curves at all: it describes them to WebAudio as automation events and the
//     browser walks them. So the driver captures the events the real
//     scheduleVoice writes and reads them back. That evaluator implements the
//     BROWSER, not the runtime - every value in it came out of the runtime, and
//     all it adds is the interpolation between two events that the Web Audio
//     specification fixes. The line matters enough that the driver self-tests
//     that interpolation against hand-computed values before comparing anything.

// PlaybackSamples is how many points across a note each curve is sampled at.
// The grid is the corpus's own - both drivers apply the same rule to the
// voice's `ms` - so a disagreement about the note's SPAN shows up as a differing
// span rather than as two curves sampled in different places.
const PlaybackSamples = 24

// Tolerances.
//
// Gain is an amplitude with an absolute floor rather than a level in dB,
// because a tremolo at full depth touches exactly zero and there is no zero in
// dB. 1e-6 of full scale is 120 dB down.
//
// Pitch is in cents, the unit a listener hears in. Half a cent is a fortieth of
// the smallest interval anyone can name, and it is far wider than the two
// runtimes' arithmetic can drift: one interpolates a ramp the browser will
// walk, the other evaluates a closed form, so exact equality would be a promise
// about floating point rather than about sound.
const (
	PlaybackGainAbsTol = 1e-6
	PlaybackGainRelTol = 1e-4
	PlaybackCentsTol   = 0.5
	PlaybackSpanTol    = 1e-9
)

// PlaybackDriverJS is the web half. Argv: runtime.js program.json names samples
// (or "--selftest" in place of the names, which runs the evaluator's own checks
// and prints one line per case).
const PlaybackDriverJS = `
const fs = require('fs');

// ---- an AudioParam that remembers what was asked of it ----------------------
const mkParam = () => ({
  _events: [], _inputs: [], _base: 0,
  get value() { return this._base; },
  set value(v) { this._base = v; },
  setValueAtTime(v, t) { this._events.push({ k: 'set', v, t }); return this; },
  linearRampToValueAtTime(v, t) { this._events.push({ k: 'lin', v, t }); return this; },
  exponentialRampToValueAtTime(v, t) { this._events.push({ k: 'exp', v, t }); return this; },
  setTargetAtTime(v, t, tc) { this._events.push({ k: 'target', v, t, tc }); return this; },
  cancelScheduledValues(t) { this._events = this._events.filter((e) => e.t < t); return this; },
});

// The automation timeline at time t.
//
// The rule a naive reading gets wrong, and the one this whole evaluator turns
// on: a RAMP does not set a value at its own time, it describes the interval
// BEFORE it. So the value at t is governed by the NEXT event when that event is
// a ramp, and by the previous event otherwise.
function automationAt(param, t) {
  const es = [...param._events].sort((a, b) => a.t - b.t);
  if (es.length === 0) return param._base;
  if (t < es[0].t) return param._base;
  let i = 0;
  while (i + 1 < es.length && es[i + 1].t <= t) i++;
  const prev = es[i], next = es[i + 1];
  if (next && (next.k === 'lin' || next.k === 'exp') && t < next.t) {
    const span = next.t - prev.t;
    if (span <= 0) return next.v;
    const u = (t - prev.t) / span;
    if (next.k === 'lin') return prev.v + (next.v - prev.v) * u;
    // An exponential ramp cannot cross or touch zero; the spec forbids it and
    // this runtime floors both ends precisely so it never asks for one.
    if (prev.v === 0 || next.v === 0 || prev.v * next.v < 0) return prev.v;
    return prev.v * Math.pow(next.v / prev.v, u);
  }
  if (prev.k === 'target') {
    const before = i > 0 ? es[i - 1].v : param._base;
    return prev.v + (before - prev.v) * Math.exp(-(t - prev.t) / prev.tc);
  }
  return prev.v;
}

// A connected signal SUMS with the automation value. That is what lets an LFO
// on 'detune' compose with a static detune, and a tremolo LFO ride its carrier.
function paramAt(param, t) {
  let v = automationAt(param, t);
  for (const inp of param._inputs) v += sourceAt(inp, t);
  return v;
}
function sourceAt(node, t) {
  if (node.kind === 'gain') {
    let v = paramAt(node.gain, t);
    for (const inp of node._in) v *= sourceAt(inp, t);
    return v;
  }
  if (node.kind === 'osc') {
    if (node._startAt != null && t < node._startAt) return 0;
    const ph = 2 * Math.PI * paramAt(node.frequency, t) * (t - (node._startAt || 0));
    // real[1] === 1 is a cosine, which is the tremolo LFO; the default is a sine.
    return node._wave && node._wave.real && node._wave.real[1] === 1 ? Math.cos(ph) : Math.sin(ph);
  }
  return 0;
}

// ---- a fake WebAudio that records rather than sounds ------------------------
let nodes = [];
const mk = (kind, extra) => {
  const n = Object.assign({
    kind, _in: [], _out: [], _startAt: null, _stopAt: null, _wave: null,
    connect(d) { if (d && d._events) d._inputs.push(this); else if (d) { d._in.push(this); this._out.push(d); } return d; },
    disconnect() {}, start(t, offset) { this._startAt = t; this._offset = offset || 0; }, stop(t) { this._stopAt = t; },
    setPeriodicWave(w) { this._wave = w; },
  }, extra || {});
  nodes.push(n);
  return n;
};
const ctx = {
  currentTime: 0, sampleRate: 48000, state: 'running', resume() {},
  destination: mk('destination'),
  createGain() { const n = mk('gain'); n.gain = mkParam(); n.gain._base = 1; return n; },
  createOscillator() { const n = mk('osc', { type: 'sine' }); n.frequency = mkParam(); n.detune = mkParam(); return n; },
  createBufferSource() { const n = mk('src', { buffer: null, loop: false }); n.playbackRate = mkParam(); n.playbackRate._base = 1; n.detune = mkParam(); return n; },
  createBiquadFilter() { const n = mk('biquad', { type: '' }); n.frequency = mkParam(); n.Q = mkParam(); return n; },
  createBuffer: (c, len) => ({ length: len, getChannelData: () => new Float32Array(len) }),
  createPeriodicWave: (re, im) => ({ real: [...re], imag: [...im] }),
  createChannelMerger: () => mk('merger'),
  createWaveShaper: () => mk('shaper', { curve: null, oversample: '' }),
};

const g = globalThis;
g.window = g;
g.AudioContext = function () { return ctx; };
g.document = {
  addEventListener() {}, head: { appendChild() {} },
  body: { appendChild() {}, classList: { add() {}, remove() {} } },
  documentElement: { classList: { add() {}, remove() {} }, style: {} },
  createElement() { return { style: {}, classList: { add() {}, remove() {} }, appendChild() {}, setAttribute() {} }; },
  getElementById() { return null; }, querySelector() { return null; }, querySelectorAll() { return []; },
};
g.location = { pathname: '/', href: 'http://x/', search: '', hash: '' };
g.history = { pushState() {}, replaceState() {}, back() {} };
try { g.navigator = { userAgent: 'node' }; } catch (e) { /* already a getter-only global */ }
g.matchMedia = () => ({ matches: false, addEventListener() {}, addListener() {} });
g.requestAnimationFrame = (f) => setTimeout(() => f(0), 0);
g.fetch = () => Promise.reject(new Error('offline'));
g.setInterval = () => 1;
g.clearInterval = () => {};
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));

// ---- the two curves --------------------------------------------------------
// Walk from the source to the voice's own sink, multiplying every gain on the
// way. A gain that feeds a ChannelMerger is the PAN pair and is skipped.
// The noise clip carries an amplitude envelope of its own, and it is not in any
// gain node: makeNoiseLoop tapers the first and last 4 ms of the buffer to hide
// the seam where the loop wraps, and a one-shot noise voice starts at buffer
// offset 0, so every noise hit fades in over those 4 ms. It is deterministic and
// it is part of what multiplies the source, so it belongs in this answer.
function clipTaper(src, t) {
  if (src.kind !== 'src' || src._startAt == null) return 1;
  const sr = 48000;
  const fade = Math.max(1, Math.floor(sr * 0.004));
  const pos = ((t - src._startAt) * Math.max(0.0001, paramAt(src.playbackRate, t)) + (src._offset || 0)) * sr;
  if (pos <= 0) return 0;
  return Math.min(1, pos / fade);
}

function gainAt(src, sink, t) {
  let out = clipTaper(src, t), node = src, guard = 0;
  while (node && node !== sink && guard++ < 64) {
    const to = node._out[0];
    if (!to) break;
    if (to.kind === 'gain' && to !== sink && !to._out.some((o) => o.kind === 'merger')) out *= paramAt(to.gain, t);
    node = to;
  }
  return out;
}
function freqAt(v, src, t) {
  if (src.kind === 'osc') return paramAt(src.frequency, t) * Math.pow(2, paramAt(src.detune, t) / 1200);
  if (src.kind === 'src') {
    // A PITCHED noise clip: the pitch it is resampled to, which is the rate the
    // iOS sample-and-hold runs at. The same number on both sides.
    //
    // An UNPITCHED one (freq 0, which is how every noise voice in this repo is
    // written) is made by different means on the two runtimes -- a clip at its
    // natural rate here, a fresh random per sample there -- so there is no
    // shared pitch. What IS shared, and worth comparing, is whether anything
    // moved it: this comes back as 440 exactly when the clip runs at its
    // natural speed, and iOS answers 440 for "nothing moved it" too. A pitch
    // word that reaches a voice with no pitch on one runtime and not the other
    // shows up here as a rate that is no longer 1.
    return 440 * paramAt(src.playbackRate, t) * Math.pow(2, paramAt(src.detune, t) / 1200);
  }
  return 0;
}

// The sampling grid, and it is the CORPUS's, not either runtime's: both drivers
// apply this to the voice's own ms, so a disagreement about the note's SPAN
// shows up as a differing span rather than as two curves sampled in different
// places.
//
// The last point is a hair INSIDE the note rather than on its end, because
// neither runtime plays the end: the web schedules no arpeggio step at it and
// iOS drops a voice the moment t reaches its length. Sampling there compares
// what two runtimes do after the note is over, which is nothing, differently.
function gridAt(ms, k, n) {
  const span = Math.max(20, ms || 0) / 1000;
  return k < n ? span * k / n : span * (1 - 1e-6);
}

// ---- the evaluator's own checks --------------------------------------------
// Hand-computed, because the evaluator is the one part of this harness that is
// not the runtime itself. If these do not hold, nothing below means anything.
function selftest() {
  const lines = [];
  const P = (build) => { const p = mkParam(); build(p); return p; };
  const near = (a, b) => Math.abs(a - b) < 1e-9;
  const check = (name, got, want) => lines.push((near(got, want) ? 'ok   ' : 'FAIL ') + name + ' got=' + got + ' want=' + want);

  const a = P((p) => { p._base = 7; p.setValueAtTime(2, 1); });
  check('before the first event, the param holds its own value', automationAt(a, 0.5), 7);
  check('a set takes effect at its own time', automationAt(a, 1), 2);
  check('and holds after it', automationAt(a, 5), 2);

  const b = P((p) => { p.setValueAtTime(1, 0); p.linearRampToValueAtTime(3, 2); });
  check('a linear ramp is half way at half time', automationAt(b, 1), 2);
  check('a linear ramp arrives', automationAt(b, 2), 3);
  check('and holds after arriving', automationAt(b, 3), 3);

  const c = P((p) => { p.setValueAtTime(1, 0); p.exponentialRampToValueAtTime(100, 2); });
  check('an exponential ramp is the geometric mean at half time', automationAt(c, 1), 10);
  check('an exponential ramp arrives', automationAt(c, 2), 100);

  const d = P((p) => { p.setValueAtTime(0.5, 0); p.setValueAtTime(0.25, 1); p.exponentialRampToValueAtTime(0.0001, 2); });
  check('a ramp interpolates from the PREVIOUS event, not from the first', automationAt(d, 1.5), 0.25 * Math.pow(0.0001 / 0.25, 0.5));

  const e = P((p) => { p._base = 5; p.setValueAtTime(5, 0); });
  const osc = mk('osc', { type: 'sine' }); osc.frequency = mkParam(); osc.detune = mkParam();
  osc.frequency.setValueAtTime(1, 0); osc._startAt = 0;
  const lg = mk('gain'); lg.gain = mkParam(); lg.gain._base = 2;
  osc.connect(lg); lg.connect(e);
  // a 1 Hz sine through a gain of 2, a quarter of a cycle in: 5 + 2*sin(pi/2)
  check('a connected signal SUMS with the automation', paramAt(e, 0.25), 7);
  check('a connected source contributes nothing before it starts', paramAt(e, -1), 5);

  process.stdout.write(lines.join('\n'));
}

// HOW LOUD IS THE WAVE ITSELF, before any envelope: (rms, dc) over one cycle.
//
// The web runtime does not synthesise a wave at all - it names one, and the
// browser makes it. So this reads what was NAMED and applies the spec:
//
//   node.type          the four standard shapes, whose Fourier series the spec
//                      fixes. Their levels are the textbook ones.
//   setPeriodicWave    the coefficients the runtime handed over, normalised the
//                      way createPeriodicWave normalises by default - to a PEAK
//                      of 1, which is the step this comparison exists to see.
//
// Same line as the automation evaluator: the runtime says what it wants and the
// spec says what that means.
function waveLevel(node) {
  const n = 4096;
  let sum = 0, sumSq = 0;
  const sample = (p) => {
    if (node._wave) {
      const im = node._wave.imag, re = node._wave.real;
      let x = 0;
      for (let k = 1; k < im.length; k++) x += im[k] * Math.sin(2 * Math.PI * k * p) + re[k] * Math.cos(2 * Math.PI * k * p);
      return x;
    }
    switch (node.type) {
      case 'sine': return Math.sin(2 * Math.PI * p);
      case 'square': return p < 0.5 ? 1 : -1;
      case 'sawtooth': return 2 * p - 1;
      case 'triangle': return 4 * Math.abs(p - 0.5) - 1;
      default: return 0;
    }
  };
  // createPeriodicWave normalises to a peak of 1 unless asked not to; the four
  // standard types are already at their spec amplitude.
  let peak = 0;
  const xs = [];
  for (let i = 0; i < n; i++) { const x = sample(i / n); xs.push(x); peak = Math.max(peak, Math.abs(x)); }
  const k = node._wave && peak > 0 ? 1 / peak : 1;
  for (const x of xs) { sum += x * k; sumSq += (x * k) * (x * k); }
  return [Math.sqrt(sumSq / n), sum / n];
}

if (process.argv[3] === '--selftest') { selftest(); } else if (process.argv[3] === '--loop') {

// ONE CYCLE of Sound.loop. Not a curve and not per voice: it is the tempo, and
// a tempo that differs makes a whole soundtrack run at a different speed on one
// platform - a bigger failure than any single note's level, and one no per-voice
// comparison can see.
//
// Read from the scheduler itself: __marTestLoopStart returns the playhead it
// built, and the period on it is the number the cycle actually advances by.
const program4 = JSON.parse(fs.readFileSync(process.argv[4], 'utf8'));
const names4 = JSON.parse(process.argv[5]);
const loopRows = [];
for (const name of names4) {
  const snd = g.__marEvalRaw(program4, 'SoundConform.' + name);
  const st = g.__marTestLoopStart(snd);
  loopRows.push([name, 0, 'period', st ? st.period.toPrecision(12) : 'none'].join(' '));
}
process.stdout.write(loopRows.join('\n'));

} else if (process.argv[3] === '--bed') {

// What a HELD source (Sound.voice / Sound.glide / Sound.ambient) settles at.
//
// Here the web's gain IS a function of time - two events and an exponential ramp
// - so it is read at times rather than driven per sample the way iOS has to be.
// What comes back is the same three numbers: the mean over a window after it has
// settled, and the extremes inside that window, so a modulator that reaches the
// output on one runtime and not the other shows up as a different spread.
//
// The NOISE bed needs one correction, and it is a statement about statistics
// rather than about either runtime. The web sums TWO independent clips into a
// 0.7 trim; iOS draws one random per sample and trims by 0.99. Independent
// sources add in POWER, so k clips at a trim of g sit at g*sqrt(k) of one clip -
// which is what makes 0.7*sqrt(2) and 0.99 the same bed.
const program3 = JSON.parse(fs.readFileSync(process.argv[4], 'utf8'));
const names3 = JSON.parse(process.argv[5]);
const settle = Number(process.argv[6]);
const window = Number(process.argv[7]);
const bedRows = [];
for (const name of names3) {
  const snd = g.__marEvalRaw(program3, 'SoundConform.' + name);
  const voices = ((snd && snd.voices) || []).filter((v) => v.wave !== 'Rest');
  voices.forEach((v, i) => {
    const before = nodes.length;
    const h = g.__marTestHeldStart({ voices: [v] });
    const made = nodes.slice(before);
    const src = made.find((x) => x.kind === 'osc' || x.kind === 'src');
    const sinkGain = h && h.nodes && h.nodes[0] ? h.nodes[0].gain : null;
    if (!src || !sinkGain) { bedRows.push([name, i, 'nobed'].join(' ')); return; }
    // count the independent sources feeding whatever this one feeds
    const mix = src._out[0];
    const siblings = made.filter((x) => x.kind === 'src' && x._out[0] === mix).length;
    const k = v.wave === 'Noise' && siblings > 1 ? Math.sqrt(siblings) : 1;
    // Walk as far as this VOICE's own envelope gain and stop there. Past it is
    // the master and the ceiling, which belong to the app rather than to the
    // voice and are compared on their own.
    const envGain = h.nodes[0].gain;
    const gainOf = (t) => {
      let out = k, node = src, guard = 0;
      while (node && guard++ < 64) {
        const to = node._out[0];
        if (!to) break;
        if (to.kind === 'gain' && !to._out.some((o) => o.kind === 'merger')) out *= paramAt(to.gain, t);
        if (to === envGain) break;
        node = to;
      }
      return out;
    };
    let sum = 0, lo = Infinity, hi = -Infinity;
    const N2 = 512;
    for (let s = 0; s < N2; s++) {
      const t = settle + window * s / N2;
      const val = gainOf(t);
      sum += val; lo = Math.min(lo, val); hi = Math.max(hi, val);
    }
    bedRows.push([name, i, 'bed', (sum / N2).toPrecision(12), lo.toPrecision(12), hi.toPrecision(12)].join(' '));
  });
}
process.stdout.write(bedRows.join('\n'));

} else if (process.argv[3] === '--wavelevel') {

const program2 = JSON.parse(fs.readFileSync(process.argv[4], 'utf8'));
const names2 = JSON.parse(process.argv[5]);
const rows = [];
for (const name of names2) {
  const snd = g.__marEvalRaw(program2, 'SoundConform.' + name);
  ((snd && snd.voices) || []).forEach((v, i) => {
    if (v.wave === 'Rest' || v.wave === 'Noise') { rows.push([name, i, 'skip'].join(' ')); return; }
    const sink = ctx.createGain();
    const before = nodes.length;
    g.__marTestScheduleVoice(ctx, v, 0, sink);
    const src = nodes.slice(before).find((x) => x.kind === 'osc');
    if (!src) { rows.push([name, i, 'skip'].join(' ')); return; }
    const [rms, dc] = waveLevel(src);
    rows.push([name, i, 'wave', rms.toPrecision(12), dc.toPrecision(12)].join(' '));
  });
}
process.stdout.write(rows.join('\n'));

} else if (process.argv[3] === '--ceiling') {

// The master transfer function, answered by the runtime's own hook, which walks
// the table the way the shaper does: pre-gain, index, and the CLAMP at the edge
// of the table's domain. Not the pure formula next to it -- the formula agrees
// with iOS even in the case this sweep exists to catch, which is what the table
// does PAST its own domain.
//
// The sweep goes to 4, well beyond the table's span, because that is the only
// place the two can differ: below the knee both are the identity, and between
// the knee and the span both bend by the same tanh.
const pts = [];
for (let i = 0; i <= 80; i++) {
  const x = -4 + i * 0.1;
  pts.push(['ceiling', i, x.toPrecision(12), g.__marSoundCeilingAt(x).toPrecision(12)].join(' '));
}
process.stdout.write(pts.join('\n'));

} else {

const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const names = JSON.parse(process.argv[4]);
const N = Number(process.argv[5]);
const out = [];
for (const name of names) {
  const snd = g.__marEvalRaw(program, 'SoundConform.' + name);
  const voices = (snd && snd.voices) || [];
  voices.forEach((v, i) => {
    if (v.wave === 'Rest') { out.push([name, i, 'rest'].join(' ')); return; }
    const sink = ctx.createGain();
    const before = nodes.length;
    g.__marTestScheduleVoice(ctx, v, 0, sink);
    const src = nodes.slice(before).find((x) => x.kind === 'osc' || x.kind === 'src');
    if (!src) { out.push([name, i, 'nosource'].join(' ')); return; }
    // The span the RUNTIME gave the note, read off its own scheduling rather
    // than recomputed here: the note stops at t0 + dur + the 30 ms tail.
    out.push([name, i, 'span', (src._stopAt - src._startAt - 0.03).toPrecision(12)].join(' '));
    const t0 = (v.delayMs || 0) / 1000;
    for (let k = 0; k <= N; k++) {
      const t = t0 + gridAt(v.ms, k, N);
      out.push([name, i, k, gainAt(src, sink, t).toPrecision(12), freqAt(v, src, t).toPrecision(12)].join(' '));
    }
  });
}
process.stdout.write(out.join('\n'));

}
`

// PlaybackDriverSwift is the iOS half. Argv: program.json names samples
const PlaybackDriverSwift = `import Foundation

// It asks MarSound for the curves and prints them. It does not compute them:
// see MarSound.conformanceCurves for why that distinction is the whole value of
// this test.
let data = try! Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[1]))
let program = try! MarJSONCodec.decodeProgram(data)
let env = MarBuiltins.makeEnv()
do {
    for m in program.modules { try MarLoader.load(module: m, into: env) }
} catch {
    FileHandle.standardError.write("mar error: \(error)\n".data(using: .utf8)!)
    exit(2)
}

// The comparison parses both sides as numbers, so what matters is not that the
// two spellings match but that neither rounds away a difference the other sees.
func f(_ x: Double) -> String { String(format: "%.12g", x) }

if CommandLine.arguments[2] == "--loop" {
    let names2 = CommandLine.arguments[3].split(separator: ",").map(String.init)
    var pts: [String] = []
    for name in names2 {
        guard let v = env.lookup("SoundConform." + name) else { exit(1) }
        if let p = MarSound.conformanceLoopPeriod(v) {
            pts.append("\(name) 0 period \(f(p))")
        } else {
            pts.append("\(name) 0 period none")
        }
    }
    FileHandle.standardOutput.write(pts.joined(separator: "\n").data(using: .utf8)!)
    exit(0)
}

if CommandLine.arguments[2] == "--bed" {
    let names2 = CommandLine.arguments[3].split(separator: ",").map(String.init)
    let settle = Double(CommandLine.arguments[4])!
    let window = Double(CommandLine.arguments[5])!
    var pts: [String] = []
    for name in names2 {
        guard let v = env.lookup("SoundConform." + name) else { exit(1) }
        let curves = MarSound.conformanceBedLevel(v, sampleRate: 48000,
                                                  settleSeconds: settle, windowSeconds: window)
        for (i, c) in curves.enumerated() {
            pts.append("\(name) \(i) bed \(f(c.mean)) \(f(c.lo)) \(f(c.hi))")
        }
    }
    FileHandle.standardOutput.write(pts.joined(separator: "\n").data(using: .utf8)!)
    exit(0)
}

if CommandLine.arguments[2] == "--wavelevel" {
    let names2 = CommandLine.arguments[3].split(separator: ",").map(String.init)
    var pts: [String] = []
    for name in names2 {
        guard let v = env.lookup("SoundConform." + name) else { exit(1) }
        for (i, w) in MarSound.conformanceWaveLevel(v, sampleRate: 48000).enumerated() {
            pts.append(w.skip ? "\(name) \(i) skip" : "\(name) \(i) wave \(f(w.rms)) \(f(w.dc))")
        }
    }
    FileHandle.standardOutput.write(pts.joined(separator: "\n").data(using: .utf8)!)
    exit(0)
}

if CommandLine.arguments[2] == "--ceiling" {
    // The master transfer function. This side computes the tanh directly, so it
    // has no domain to fall off; the web walks a table and the sweep goes past
    // that table's span because that is the only place the two can differ.
    var pts: [String] = []
    for i in 0...80 {
        let x = -4 + Double(i) * 0.1
        pts.append("ceiling \(i) \(f(x)) \(f(MarSound.ceiling(x)))")
    }
    FileHandle.standardOutput.write(pts.joined(separator: "\n").data(using: .utf8)!)
    exit(0)
}

let names = CommandLine.arguments[2].split(separator: ",").map(String.init)
let n = Int(CommandLine.arguments[3])!

var out: [String] = []
for name in names {
    guard let v = env.lookup("SoundConform." + name) else {
        FileHandle.standardError.write("unbound: \(name)\n".data(using: .utf8)!)
        exit(1)
    }
    for (i, curve) in MarSound.conformanceCurves(v, samples: n).enumerated() {
        if curve.isRest {
            out.append("\(name) \(i) rest")
            continue
        }
        out.append("\(name) \(i) span \(f(curve.span))")
        for (k, p) in curve.points.enumerated() {
            out.append("\(name) \(i) \(k) \(f(p.amp)) \(f(p.freq))")
        }
    }
}
FileHandle.standardOutput.write(out.joined(separator: "\n").data(using: .utf8)!)
`

// KnownWaveLevelGaps records divergences in raw wave LEVEL that are real,
// measured, and not yet decided.
//
// A conformance test that fails for a known reason gets ignored, and a known
// reason that is not written down gets forgotten. So each entry pins the
// measurement at the value it was taken at: the test still fails if it MOVES,
// in either direction, and the fix for a fixed one is to delete its line.
type WaveLevelGap struct {
	WebRMS, IOSRMS float64
	WebDC, IOSDC   float64
	Why            string
}

var KnownWaveLevelGaps = map[string]WaveLevelGap{
	"pulsed": {
		WebRMS: 0.2663, IOSRMS: 0.9962,
		WebDC: 0, IOSDC: -0.7600,
		Why: "Sound.duty, and it is two divergences at once.\n\n" +
			"LEVEL, 11.5 dB. The web asks the browser for a 32-harmonic PeriodicWave and does not pass\n" +
			"disableNormalization, so the browser rescales it to a PEAK of 1 - and the peak of a narrow\n" +
			"pulse built from 32 harmonics is its Gibbs overshoot, not its plateau. iOS builds a plain\n" +
			"+/-1 pulse. Comparing only the part you can hear (the AC, ignoring the offset below) the gap\n" +
			"is 7.8 dB. The web is also discontinuous with itself: duty 50 does not take that branch at\n" +
			"all, so the level jumps as duty crosses 50.\n\n" +
			"OFFSET. A +/-1 pulse of width d has a mean of 2d-1, so the iOS wave carries -0.76 of DC at\n" +
			"duty 12 and the web's odd-harmonic series carries none. The envelope multiplies it, so it is\n" +
			"not a click, but it is headroom spent on nothing and it makes the note cross the ceiling\n" +
			"knee on one polarity before the other.\n\n" +
			"Both are recorded rather than fixed because fixing them changes the level of every duty\n" +
			"voice in every app in this repo, which is a decision and not a patch. Pinned here so it\n" +
			"cannot move quietly.",
	},
}

// How long a held source is driven before it is measured, and over how long a
// window. Two seconds is far past any attack in the corpus; a fifth of a second
// is several cycles of the slowest tremolo anyone would write.
const (
	BedSettleSeconds = 2.0
	BedWindowSeconds = 0.2
)
