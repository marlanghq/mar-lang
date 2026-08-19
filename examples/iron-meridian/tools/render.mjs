// Offline renderer for Mar's Sound values.
//
// The Sound tree is flattened by the REAL runtime (runtime.js, evaluated under
// node) so the voice list here is exactly what a browser would schedule. Only
// the last mile — voices to PCM — is reimplemented, and it is a direct port of
// scheduleVoice + soundCeiling from internal/jsserve/runtime.js.
//
// Purpose: hear the music without launching the game, and measure it.

import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const SR = 44100;

// ---------- 1. evaluate the Mar module under the real runtime ----------

export function voicesOf(runtimeJs, programJson, entry, arg) {
  const g = globalThis;
  g.window = g;
  g.document = {
    addEventListener() {}, head: { appendChild() {} },
    body: { appendChild() {}, classList: { add() {}, remove() {} } },
    documentElement: { classList: { add() {}, remove() {} }, style: {} },
    createElement() { return { style: {}, classList: { add() {}, remove() {} }, appendChild() {}, setAttribute() {} }; },
    getElementById() { return null; }, querySelector() { return null; }, querySelectorAll() { return []; },
  };
  g.location = { pathname: '/', href: 'http://x/', search: '', hash: '' };
  g.history = { pushState() {}, replaceState() {}, back() {} };
  // node 26 defines navigator as a getter-only global
  try { g.navigator = { userAgent: 'node' }; } catch { /* already there, fine */ }
  g.matchMedia = () => ({ matches: false, addEventListener() {}, addListener() {} });
  g.requestAnimationFrame = (f) => setTimeout(() => f(0), 0);
  g.fetch = () => Promise.reject(new Error('offline'));
  if (!g.__marEvalRaw) require(runtimeJs);

  const program = JSON.parse(fs.readFileSync(programJson, 'utf8'));
  const snd = g.__marEvalRaw(program, entry, arg == null ? undefined : { k: 'I', n: arg });
  if (!snd || !snd.voices) throw new Error('not a Sound: ' + JSON.stringify(snd).slice(0, 120));
  return snd.voices;
}

// ---------- 2. voices to PCM: a port of scheduleVoice ----------

const MIN_RAMP_MS = 5;
const rampSec = (ms) => Math.max(MIN_RAMP_MS, ms == null ? 0 : ms) / 1000;

// Band-limited pulse, same harmonic series the runtime hands to createPeriodicWave:
// the nth harmonic of a duty-d pulse is (2/nπ)·sin(nπd).
function pulseSample(phase, duty) {
  const d = Math.max(5, Math.min(95, duty)) / 100;
  let s = 0;
  for (let n = 1; n <= 32; n++) s += (2 / (n * Math.PI)) * Math.sin(n * Math.PI * d) * Math.sin(n * phase);
  return s;
}

function oscSample(wave, phase, duty) {
  const t = phase / (2 * Math.PI);           // 0..1 through the cycle
  if (wave === 'Triangle') return 4 * Math.abs(t - Math.floor(t + 0.5)) - 1;
  if (wave === 'Sawtooth') return 2 * (t - Math.floor(t + 0.5));
  if (duty && duty !== 50) return pulseSample(phase, duty);
  return t - Math.floor(t) < 0.5 ? 1 : -1;   // plain square
}

// One-pole biquad pair, matching the runtime's highpass(Q .5) / lowpass(Q .4).
function biquad(type, freq, Q) {
  const w = 2 * Math.PI * freq / SR, cw = Math.cos(w), sw = Math.sin(w), al = sw / (2 * Q);
  let b0, b1, b2;
  const a0 = 1 + al, a1 = -2 * cw, a2 = 1 - al;
  if (type === 'lowpass') { b0 = (1 - cw) / 2; b1 = 1 - cw; b2 = (1 - cw) / 2; }
  else { b0 = (1 + cw) / 2; b1 = -(1 + cw); b2 = (1 + cw) / 2; }
  let x1 = 0, x2 = 0, y1 = 0, y2 = 0;
  return (x) => {
    const y = (b0 / a0) * x + (b1 / a0) * x1 + (b2 / a0) * x2 - (a1 / a0) * y1 - (a2 / a0) * y2;
    x2 = x1; x1 = x; y2 = y1; y1 = y;
    return y;
  };
}

// The runtime's noise buffer is a looping random clip resampled by pitch.
let noiseClip = null;
function noise(i) {
  if (!noiseClip) {
    noiseClip = new Float32Array(SR * 4);
    let s = 12345;
    for (let k = 0; k < noiseClip.length; k++) { s = (s * 1103515245 + 12345) & 0x7fffffff; noiseClip[k] = (s / 0x3fffffff) - 1; }
  }
  return noiseClip[i % noiseClip.length];
}

export function render(voices, opts = {}) {
  const master = opts.master == null ? 0.35 : opts.master;
  let end = 0;
  for (const v of voices) end = Math.max(end, (v.delayMs || 0) + (v.ms || 0));
  const n = Math.ceil((end / 1000 + 0.3) * SR);
  const buf = new Float32Array(n);

  for (const v of voices) {
    if (v.wave === 'Rest') continue;
    const t0 = (v.delayMs || 0) / 1000;
    const dur = Math.max(0.02, (v.ms || 100) / 1000);
    const peak = Math.max(0.0002, Math.max(0, Math.min(100, v.volume == null ? 60 : v.volume)) / 100);
    const atk = Math.min(dur / 2, Math.max(0.0005, rampSec(v.attack)));
    const rel = Math.min(dur - atk, rampSec(v.release));
    const i0 = Math.round(t0 * SR), len = Math.round(dur * SR);

    const lp = v.highCut > 0 ? biquad('lowpass', v.highCut, 0.4) : null;
    const hp = v.lowCut > 0 ? biquad('highpass', v.lowCut, 0.5) : null;

    const f0 = Math.max(1, v.freq || 440);
    const noiseRate = (f) => Math.max(0.05, Math.min(8, Math.max(1, f) / 440));
    let phase = 0, npos = 0;

    for (let k = 0; k < len; k++) {
      const t = k / SR;
      // envelope: exponential ramps between 0.0001 and peak, exactly as scheduled
      let env;
      if (t < atk) env = 0.0001 * Math.pow(peak / 0.0001, t / atk);
      else if (t < dur - rel) env = peak;
      else env = peak * Math.pow(0.0001 / peak, (t - (dur - rel)) / Math.max(1e-6, rel));

      let s;
      if (v.wave === 'Noise') {
        let rate = noiseRate(f0);
        if (v.freq > 0 && v.endFreq && v.endFreq !== v.freq) {
          const hold = Math.min(dur, Math.max(0, (v.holdMs || 0) / 1000));
          const u = t <= hold ? 0 : (t - hold) / Math.max(1e-6, dur - hold);
          rate = noiseRate(f0) + (noiseRate(v.endFreq) - noiseRate(f0)) * Math.min(1, u);
        }
        s = noise(Math.floor(npos));
        npos += rate;
      } else {
        let f = f0;
        if (v.arp && v.arp.length) {
          const seq = [f0].concat(v.arp.map((x) => Math.max(1, x)));
          f = seq[Math.floor(t / 0.02) % seq.length];
        } else if (v.endFreq && v.endFreq !== v.freq) {
          const hold = Math.min(dur, Math.max(0, (v.holdMs || 0) / 1000));
          const u = t <= hold ? 0 : (t - hold) / Math.max(1e-6, dur - hold);
          f = f0 + (Math.max(1, v.endFreq) - f0) * Math.min(1, u);
        }
        if (v.vibDepth > 0) f *= Math.pow(2, (v.vibDepth * Math.sin(2 * Math.PI * Math.max(1, v.vibRate) * t)) / 1200);
        phase += (2 * Math.PI * f) / SR;
        s = oscSample(v.wave, phase, v.duty);
      }
      if (hp) s = hp(s);
      if (lp) s = lp(s);
      const idx = i0 + k;
      if (idx >= 0 && idx < n) buf[idx] += s * env;
    }
  }

  // master gain, then the soft ceiling (waveshaper, knee 0.7 + tanh)
  const KNEE = 0.7;
  for (let i = 0; i < n; i++) {
    const x = buf[i] * master;
    const a = Math.abs(x);
    const y = a <= KNEE ? a : KNEE + (1 - KNEE) * Math.tanh((a - KNEE) / (1 - KNEE));
    buf[i] = x < 0 ? -y : y;
  }
  return buf;
}

export function writeWav(file, pcm) {
  const n = pcm.length, buf = Buffer.alloc(44 + n * 2);
  buf.write('RIFF', 0); buf.writeUInt32LE(36 + n * 2, 4); buf.write('WAVE', 8);
  buf.write('fmt ', 12); buf.writeUInt32LE(16, 16); buf.writeUInt16LE(1, 20);
  buf.writeUInt16LE(1, 22); buf.writeUInt32LE(SR, 24); buf.writeUInt32LE(SR * 2, 28);
  buf.writeUInt16LE(2, 32); buf.writeUInt16LE(16, 34);
  buf.write('data', 36); buf.writeUInt32LE(n * 2, 40);
  for (let i = 0; i < n; i++) buf.writeInt16LE(Math.max(-32768, Math.min(32767, Math.round(pcm[i] * 32767))), 44 + i * 2);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, buf);
  return n / SR;
}

// ---------- 3. measurement ----------

export function metrics(voices, pcm) {
  const total = Math.max(...voices.map((v) => (v.delayMs || 0) + (v.ms || 0)));
  const sounding = voices.filter((v) => v.wave !== 'Rest');
  const pitched = sounding.filter((v) => v.wave !== 'Noise' && v.freq > 0);

  // loudness envelope in 2-second windows: how much the piece BREATHES
  const win = SR * 2, rms = [];
  for (let i = 0; i + win <= pcm.length; i += win) {
    let s = 0;
    for (let k = 0; k < win; k++) s += pcm[i + k] * pcm[i + k];
    rms.push(Math.sqrt(s / win));
  }
  // a loop, not Math.max(...pcm): spreading millions of samples overflows the stack
  let peak = 0;
  for (let i = 0; i < pcm.length; i++) { const a = Math.abs(pcm[i]); if (a > peak) peak = a; }
  const mean = rms.reduce((a, b) => a + b, 0) / rms.length;
  const quiet = rms.filter((r) => r < mean * 0.7).length / rms.length;
  const loud = rms.filter((r) => r > mean * 1.3).length / rms.length;

  const freqs = pitched.map((v) => v.freq).sort((a, b) => a - b);
  const pct = (p) => freqs[Math.floor(freqs.length * p)] || 0;

  return {
    seconds: total / 1000,
    voices: sounding.length,
    notesPerSec: sounding.length / (total / 1000),
    peak,
    rmsMean: mean,
    rmsMin: Math.min(...rms),
    rmsMax: Math.max(...rms),
    dynamicRange: Math.max(...rms) / Math.max(1e-9, Math.min(...rms)),
    quietFraction: quiet,
    loudFraction: loud,
    medianHz: pct(0.5),
    p90Hz: pct(0.9),
    maxHz: freqs[freqs.length - 1] || 0,
    aboveGunfireBand: pitched.filter((v) => v.freq > 1200).length / Math.max(1, pitched.length),
  };
}
