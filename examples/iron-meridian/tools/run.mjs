import { voicesOf, render, writeWav, metrics } from './render.mjs';
const RT = process.argv[2], PROG = process.argv[3], OUT = process.argv[4];
const rows = [];
for (let m = 1; m <= 5; m++) {
  const vs = voicesOf(RT, PROG, `Probe.t${m}`);
  const pcm = render(vs);
  const secs = writeWav(`${OUT}/tema${m}.wav`, pcm);
  const k = metrics(vs, pcm);
  rows.push({ m, secs, ...k });
}
const f = (x, d = 2) => Number(x).toFixed(d);
console.log('tema  dur    notas/s  pico   rms    dinâmica  quieto  agudo>1200  medHz');
for (const r of rows) {
  console.log(
    `  ${r.m}  ${String(Math.floor(r.secs / 60))}m${String(Math.floor(r.secs % 60)).padStart(2, '0')}s` +
    `  ${f(r.notesPerSec, 1).padStart(6)}` +
    `  ${f(r.peak).padStart(5)}` +
    `  ${f(r.rmsMean, 3).padStart(5)}` +
    `  ${f(r.dynamicRange, 1).padStart(7)}x` +
    `  ${f(r.quietFraction * 100, 0).padStart(5)}%` +
    `  ${f(r.aboveGunfireBand * 100, 0).padStart(9)}%` +
    `  ${String(r.medianHz).padStart(5)}`);
}
