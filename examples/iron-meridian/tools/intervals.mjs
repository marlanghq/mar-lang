import { voicesOf } from './render.mjs';
const RT = process.argv[2], PROG = process.argv[3];

// Classify every interval that actually SOUNDS, by how long it sounds for.
// "Weird" is a report about intervals, so intervals are what to measure.
const CLASS = [
  'unissono/oitava', 'SEGUNDA MENOR', 'segunda maior', 'terca menor', 'terca maior',
  'quarta', 'TRITONO', 'quinta', 'sexta menor', 'sexta maior', 'SETIMA MENOR', 'SETIMA MAIOR',
];
const HARSH = new Set([1, 6, 11]);   // minor 2nd, tritone, major 7th

for (let m = 1; m <= 5; m++) {
  const vs = voicesOf(RT, PROG, `Probe.t${m}`).filter((v) => v.wave !== 'Rest' && v.freq > 0);
  const end = Math.max(...vs.map((v) => v.delayMs + v.ms));
  const SLICE = 50;
  const time = new Map();
  let sounding = 0;
  for (let t = 0; t < end; t += SLICE) {
    const now = vs.filter((v) => v.delayMs <= t && t < v.delayMs + v.ms);
    if (now.length > 1) sounding += SLICE;
    for (let i = 0; i < now.length; i++) {
      for (let j = i + 1; j < now.length; j++) {
        // semitones between the two, folded into one octave
        const semis = Math.round(12 * Math.log2(now[j].freq / now[i].freq));
        const k = ((semis % 12) + 12) % 12;
        time.set(k, (time.get(k) || 0) + SLICE);
      }
    }
  }
  const total = [...time.values()].reduce((a, b) => a + b, 0);
  const harsh = [...time.entries()].filter(([k]) => HARSH.has(k)).reduce((a, [, v]) => a + v, 0);
  console.log(`\ntema ${m}  --  ${(100 * harsh / total).toFixed(1)}% do tempo de intervalo e' aspero`);
  [...time.entries()].sort((a, b) => b[1] - a[1]).forEach(([k, v]) => {
    const pct = 100 * v / total;
    if (pct < 1.5) return;
    console.log(`  ${CLASS[k].padEnd(16)} ${pct.toFixed(1).padStart(5)}%  ${'#'.repeat(Math.round(pct))}`);
  });
}
