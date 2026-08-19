import { voicesOf, render, writeWav, metrics } from './render.mjs';
const RT = process.argv[2], PROG = process.argv[3], OUT = process.argv[4];
const SR = 44100;

// Energy in a band, by a bank of Goertzel probes across it. Cheaper than an FFT
// and enough to answer "would a laptop speaker reproduce this".
function bandEnergy(pcm, lo, hi, probes = 14) {
  let sum = 0;
  const seg = pcm.subarray(0, Math.min(pcm.length, SR * 60));   // first minute
  for (let p = 0; p < probes; p++) {
    const f = lo * Math.pow(hi / lo, p / (probes - 1));
    const k = Math.round((seg.length * f) / SR), w = (2 * Math.PI * k) / seg.length;
    const c = 2 * Math.cos(w);
    let s1 = 0, s2 = 0, s0 = 0;
    for (let i = 0; i < seg.length; i++) { s0 = seg[i] + c * s1 - s2; s2 = s1; s1 = s0; }
    sum += Math.sqrt(Math.abs(s1 * s1 + s2 * s2 - c * s1 * s2)) / seg.length;
  }
  return sum;
}
const rms = (a, i, n) => { let s = 0; for (let k = 0; k < n; k++) s += a[i + k] ** 2; return Math.sqrt(s / n); };

console.log('tema  dur     nt/s   pico   rms    dinâmica  quieto  <100Hz  100-300Hz  >400Hz  emenda');
for (let m = 1; m <= 5; m++) {
  const vs = voicesOf(RT, PROG, `Probe.t${m}`);
  const pcm = render(vs);
  writeWav(`${OUT}/tema${m}.wav`, pcm);
  const k = metrics(vs, pcm);
  const lo = bandEnergy(pcm, 35, 99), mid = bandEnergy(pcm, 100, 300), hi = bandEnergy(pcm, 400, 3000);
  const tot = lo + mid + hi;
  const total = Math.max(...vs.map(v => (v.delayMs || 0) + (v.ms || 0)));
  const end = Math.floor((total / 1000) * SR);
  const seam = rms(pcm, end - 2 * SR, 2 * SR) / rms(pcm, 0, 2 * SR);
  const pc = (x) => (100 * x / tot).toFixed(0).padStart(5) + '%';
  console.log(`  ${m}  ${Math.floor(k.seconds/60)}m${String(Math.round(k.seconds%60)).padStart(2,'0')}s` +
    `  ${k.notesPerSec.toFixed(2)}  ${k.peak.toFixed(2)}  ${k.rmsMean.toFixed(3)}` +
    `  ${k.dynamicRange.toFixed(1).padStart(7)}x  ${(k.quietFraction*100).toFixed(0).padStart(5)}%` +
    `  ${pc(lo)}  ${pc(mid)}     ${pc(hi)}  ${seam.toFixed(2)}x`);
}
