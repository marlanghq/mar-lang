// What the five themes ACTUALLY sound like vertically.
//
// compose.mjs checks that each pattern sums to 16 steps and that no note leaves
// the audible range. Neither of those can hear a chord. The complaint that sent
// me here -- "parece que tem várias partes dissonantes" -- is about what happens
// when three voices land on the same step, and nothing in the pipeline had ever
// looked at that.
//
// The measurement, in one sentence: expand every voice to one pitch per 16th
// step, and for every step where two voices sound, record the interval and how
// long it is HELD. A semitone that passes in one step is a passing tone; the
// same semitone held for eight steps is a mistake you can hear from the next
// room. Duration is what separates the two, so duration is what this counts.
//
// Octave matters too. A minor 2nd inside one octave beats hard; the same pitch
// classes two octaves apart is a minor 9th and is a colour, not a clash. So the
// absolute distance decides, not the interval class.
//
//   node harmony.mjs            all five themes, worst bars first
//   node harmony.mjs 4          one theme, every flagged bar
//   node harmony.mjs 4 26       one bar, note by note, which is what you need
//                               before you can choose a replacement note

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

// Read our own argument BEFORE handing argv over: compose.mjs writes its output
// to argv[2] as a side effect of being imported, so that slot is not ours to
// keep. (Overwriting it first is how the theme filter silently did nothing.)
const wanted = process.argv[2] ? Number(process.argv[2]) : null;
process.argv[2] = path.join(os.tmpdir(), 'harmony-throwaway.mar');
const { THEMES, PITCHED } = await import('./compose.mjs');
fs.rmSync(process.argv[2], { force: true });

const NAMES = ['C', 'C#', 'D', 'D#', 'E', 'F', 'F#', 'G', 'G#', 'A', 'A#', 'B'];
const midi = (hz) => Math.round(12 * Math.log2(hz / 440) + 69);
const spell = (m) => NAMES[((m % 12) + 12) % 12] + (Math.floor(m / 12) - 1);

// One pitch per step for a whole theme, plus which movement each bar is in.
function expand(theme, voice) {
  const steps = [];
  const barMovement = [];
  const barPattern = [];
  theme.form.forEach((mv, mi) => {
    const cyc = mv[voice];
    for (let b = 0; b < mv.bars; b++) {
      barMovement.push(mi);
      barPattern.push(cyc[b % cyc.length]);
      const pat = theme[voice][cyc[b % cyc.length]];
      for (const [hz, n] of pat) for (let k = 0; k < n; k++) steps.push(hz > 0 ? midi(hz) : null);
    }
  });
  return { steps, barMovement, barPattern };
}

// A run of consecutive steps where the same two pitches sound together.
function held(theme) {
  const voices = PITCHED.map((v) => ({ name: v, ...expand(theme, v) }));
  const nSteps = Math.max(...voices.map((v) => v.steps.length));
  const runs = [];
  const open = new Map();

  for (let s = 0; s <= nSteps; s++) {
    const now = new Map();
    for (let i = 0; i < voices.length; i++)
      for (let j = i + 1; j < voices.length; j++) {
        const a = voices[i].steps[s], b = voices[j].steps[s];
        if (a == null || b == null) continue;
        const lo = Math.min(a, b), hi = Math.max(a, b);
        now.set(`${i}:${j}:${lo}:${hi}`, { i, j, lo, hi });
      }
    for (const [k, v] of open) if (!now.has(k)) { runs.push(v); open.delete(k); }
    for (const [k, v] of now)
      if (open.has(k)) open.get(k).len++;
      else open.set(k, { ...v, start: s, len: 1 });
  }
  for (const v of open.values()) runs.push(v);
  return { runs, voices };
}

// How bad is this interval, held this long?
//
// The thresholds are deliberately blunt. A semitone or a major 7th inside one
// octave is the only thing that is wrong on its own; a tritone is a colour this
// music uses ON PURPOSE (theme 1 opens on one) and is only reported when it is
// sustained; a major 2nd is reported at length only, because a held second is
// the sound of two voices that were meant to be in unison and are not.
function verdict(dist, len) {
  const cls = dist % 12;
  const sameOctave = dist < 12;
  // A MINOR 2ND inside one octave is the one interval that is wrong on its own.
  if (cls === 1 && sameOctave) return len >= 2 ? 'min2' : 'passing';
  // A MAJOR 7TH is not. Over the root it is the leading tone, and theme 1's A7
  // cadence is built on exactly that. Flagged only when it is really sitting
  // there, and weighted far below a minor 2nd; read it as "check this", not as
  // "this is broken".
  if (cls === 11 && sameOctave) return len >= 3 ? 'maj7' : null;
  if (cls === 1 && !sameOctave) return len >= 6 ? 'wide-9th' : null;
  // A tritone is a colour this music uses ON PURPOSE (theme 1 opens on one).
  if (cls === 6) return len >= 6 ? 'tritone-held' : null;
  if ((cls === 2 || cls === 10) && sameOctave) return len >= 6 ? 'second-held' : null;
  return null;
}

const WEIGHT = { min2: 10, maj7: 2, 'tritone-held': 2, 'second-held': 2, 'wide-9th': 1, passing: 0 };

// ---------- the other kind of wrong: two keys at once ----------
//
// The complaint that is NOT about a single interval is "this bar sounds like it
// is in the wrong key". That is what happens when a movement mixes transposed
// and untransposed patterns -- it already happened once in theme 4 -- and no
// pairwise interval catches it, because every pair on its own is consonant.
//
// So: weigh each pitch class by how many STEPS it sounds in the bar, try all 24
// diatonic scales, and keep the best fit. What is left over is the weight that
// does not belong to any one key. A bar with a heavy leftover is a bar with a
// foot in two keys.
const MAJOR = [0, 2, 4, 5, 7, 9, 11];
const MINOR = [0, 2, 3, 5, 7, 8, 10];
const SCALES = [];
for (let r = 0; r < 12; r++) {
  SCALES.push({ name: `${NAMES[r]} maj`, set: new Set(MAJOR.map((x) => (x + r) % 12)) });
  SCALES.push({ name: `${NAMES[r]} min`, set: new Set(MINOR.map((x) => (x + r) % 12)) });
}

function keyFit(theme, voices, bar) {
  const w = new Array(12).fill(0);
  let total = 0;
  for (const v of voices)
    for (let s = bar * 16; s < bar * 16 + 16; s++) {
      const m = v.steps[s];
      if (m == null) continue;
      w[((m % 12) + 12) % 12]++;
      total++;
    }
  if (total === 0) return null;
  let best = null;
  for (const sc of SCALES) {
    let out = 0;
    for (let pc = 0; pc < 12; pc++) if (!sc.set.has(pc)) out += w[pc];
    if (!best || out < best.out) best = { name: sc.name, out };
  }
  return { ...best, total, pct: Math.round((best.out * 100) / total) };
}

const only = wanted;
const dumpBar = process.argv[3] ? Number(process.argv[3]) : null;

// One bar, spelled out. Choosing a replacement note is impossible without
// seeing what the other voices are doing on the same step.
if (only && dumpBar != null) {
  const t = THEMES.find((x) => x.id === only);
  const voices = PITCHED.map((v) => ({ name: v, ...expand(t, v) }));
  console.log(`\n${t.title} bar ${dumpBar} (movement ${voices[0].barMovement[dumpBar] + 1})`);
  console.log('patterns: ' + PITCHED.map((v, i) => `${v}[${voices[i].barPattern[dumpBar]}]`).join('  '));
  console.log('step  ' + PITCHED.map((v) => v.padEnd(10)).join(''));
  for (let k = 0; k < 16; k++) {
    const s0 = dumpBar * 16 + k;
    console.log(
      String(k).padStart(4) + '  ' +
      voices.map((v) => (v.steps[s0] == null ? '.' : spell(v.steps[s0])).padEnd(10)).join(''));
  }
  process.exit(0);
}
let grandTotal = 0;

for (const t of THEMES) {
  if (only && t.id !== only) continue;
  const { runs, voices } = held(t);
  const flagged = [];
  for (const r of runs) {
    const v = verdict(r.hi - r.lo, r.len);
    if (!v || v === 'passing') continue;
    const bar = Math.floor(r.start / 16);
    flagged.push({
      ...r, kind: v, bar,
      movement: voices[0].barMovement[bar],
      pair: `${PITCHED[r.i]}[${voices[r.i].barPattern[bar]}]/${PITCHED[r.j]}[${voices[r.j].barPattern[bar]}]`,
      note: `${spell(r.lo)}+${spell(r.hi)}`,
      score: WEIGHT[v] * r.len,
    });
  }
  const byBar = new Map();
  for (const f of flagged) {
    const k = f.bar;
    if (!byBar.has(k)) byBar.set(k, { bar: k, movement: f.movement, score: 0, items: [] });
    const e = byBar.get(k);
    e.score += f.score;
    e.items.push(f);
  }
  const bars = [...byBar.values()].sort((a, b) => b.score - a.score);
  const total = bars.reduce((a, b) => a + b.score, 0);
  grandTotal += total;

  // the key-fit pass, which sees what no pair of notes can
  const strayBars = [];
  for (let b = 0; b < t.bars; b++) {
    const k = keyFit(t, voices, b);
    if (k && k.pct >= 15) strayBars.push({ bar: b, movement: voices[0].barMovement[b], ...k });
  }

  const harsh = flagged.filter((f) => f.kind === 'min2').length;
  console.log(`\n=== ${t.id}. ${t.title} — ${t.bars} bars, interval score ${total}, ${harsh} minor-2nd runs, ${strayBars.length} bars off-key`);
  if (strayBars.length) {
    console.log('  off-key bars (weight of notes outside the best-fitting scale):');
    for (const s of strayBars.sort((a, b) => b.pct - a.pct).slice(0, only ? 99 : 6))
      console.log(`      bar ${String(s.bar).padStart(3)} (mov ${s.movement + 1})  best fit ${s.name.padEnd(7)} ${String(s.pct).padStart(2)}% of the bar outside it`);
  }
  const show = only ? bars : bars.slice(0, 8);
  for (const b of show) {
    if (b.score === 0) continue;
    console.log(`  bar ${String(b.bar).padStart(3)} (mov ${b.movement + 1})  score ${String(b.score).padStart(4)}`);
    for (const f of b.items.sort((x, y) => y.score - x.score)) {
      const at = f.start % 16;
      console.log(`      ${f.kind.padEnd(12)} ${f.pair.padEnd(15)} ${f.note.padEnd(10)} ${f.hi - f.lo} semitones, ${f.len} steps, from step ${at}`);
    }
  }
  if (!only && bars.length > 8) console.log(`  … and ${bars.length - 8} more bars with a lower score (run with the theme id to see all)`);
}

console.log(`\ntotal across the themes shown: ${grandTotal}`);
