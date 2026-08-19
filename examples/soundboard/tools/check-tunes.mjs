// THE LOOP INVARIANT, CHECKED.
//
//   Every part of a looping piece must total the same number of milliseconds.
//
// `Sound.loop` takes its period from the LONGEST voice in the Sound, so a part
// that is short by a cell does not shorten the loop: it opens a hole of silence
// at the end of that one part, every time round, and the arrangement is heard
// coming apart at the seam. Nothing in the type system says a word about this,
// and the failure is a musical one, so the only way it stays true is if
// something counts. The four tunes and the drum kit each state their arithmetic
// in a comment ("16 bars = 20480ms PER VOICE"); this checks the claim against
// the Sound the runtime would actually schedule.
//
//   node examples/soundboard/tools/check-tunes.mjs
//
// How it reads the music: `mar build` writes the whole compiled program into
// the page as JSON, and the REAL runtime (internal/jsserve/runtime.js, under
// node) flattens a Sound into the flat voice list a browser would schedule.
// So the numbers below are the ones that will be heard, not a second opinion
// about the source: an edit to a track cannot leave this agreeing with a
// version of the file that no longer exists.
//
// Recovering the PARTS from that flat list: Sound.chord lays its parts out one
// after another, each starting again at delay 0, so a voice at delay 0 that
// follows a later one begins a new part. A chord nested INSIDE a part (the drum
// fill, the layered snares) sits at some non-zero offset and so does not look
// like a boundary, which is what makes the count trustworthy. Write a track
// whose top-level part is itself a chord starting at 0 and this will over-count
// it; the per-part list is printed so that is visible rather than mysterious.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const example = path.dirname(here);
const repo = path.dirname(path.dirname(example));
const require = createRequire(import.meta.url);

// The looping tracks, in the order the buttons play them (Main.musicOf), plus
// Demo.showcase, which is written to loop and is not wired to a button yet.
// `mar build` keeps only what the app can reach, so a piece nothing plays is
// not in the program at all: that is reported and skipped, not failed, and the
// day a button plays it this starts counting it with no change here.
const TRACKS = ['Main.emberwake', 'Main.moonglass', 'Main.alleycatSwing',
  'Main.undertow', 'Main.drumKit', 'Demo.showcase'];

// ---------- 1. compile, and lift the program out of the page ----------

const out = fs.mkdtempSync(path.join(os.tmpdir(), 'soundboard-check-'));
try {
  execFileSync(path.join(repo, 'mar'), ['build', example, '--out', out], { stdio: 'pipe' });
} catch (e) {
  console.error('mar build failed:\n' + (e.stdout || '') + (e.stderr || ''));
  process.exit(1);
}
const html = fs.readFileSync(path.join(out, 'index.html'), 'utf8');
const m = html.match(/<script type="application\/json" id="mar-program">([\s\S]*?)<\/script>/);
if (!m) {
  console.error('no compiled program in the built page');
  process.exit(1);
}
const program = JSON.parse(m[1]);

// ---------- 2. evaluate under the real runtime ----------
// runtime.js is a browser file; it needs a window-shaped room to load in. It
// never draws anything here, only evaluates.

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
try { g.navigator = { userAgent: 'node' }; } catch { /* already a getter-only global */ }
g.matchMedia = () => ({ matches: false, addEventListener() {}, addListener() {} });
g.requestAnimationFrame = (f) => setTimeout(() => f(0), 0);
g.fetch = () => Promise.reject(new Error('offline'));
// node warns that localStorage is unavailable the moment runtime.js touches it.
// Nothing here needs storage, and the warning is louder than the report is.
process.removeAllListeners('warning');
process.on('warning', (w) => {
  if (!/localStorage/.test(w.message)) console.warn(w.name + ': ' + w.message);
});
require(path.join(repo, 'internal', 'jsserve', 'runtime.js'));

// ---------- 3. the count ----------

const end = (v) => (v.delayMs || 0) + (v.ms || 0);

function partsOf(voices) {
  const parts = [];
  let cur = null, prev = -1;
  for (const v of voices) {
    const at = v.delayMs || 0;
    if (cur === null || (at === 0 && at < prev)) {
      cur = { voices: 0, total: 0 };
      parts.push(cur);
    }
    cur.voices++;
    cur.total = Math.max(cur.total, end(v));
    prev = at;
  }
  return parts;
}

let bad = 0;
let checked = 0;
for (const name of TRACKS) {
  let snd = null;
  try {
    snd = g.__marEvalRaw(program, name);
  } catch {
    console.log(`  --   ${name.padEnd(19)} not in the build: nothing plays it, so nothing to count`);
    continue;
  }
  if (!snd || !snd.voices) {
    console.error(`${name}: not a Sound`);
    bad++;
    continue;
  }
  checked++;
  const period = Math.max(...snd.voices.map(end));
  const parts = partsOf(snd.voices);
  const off = parts.filter((p) => p.total !== period);
  const totals = parts.map((p) => p.total).join(' ');
  console.log(
    `${off.length ? 'DRIFT' : '  ok '}  ${name.padEnd(19)} period ${String(period).padStart(6)}ms` +
    `  ${String(snd.voices.length).padStart(4)} voices  ${parts.length} parts: ${totals}`);
  if (off.length) {
    bad++;
    for (const p of off) {
      console.log(`        a part totals ${p.total}ms, which is ${period - p.total}ms of silence at every seam`);
    }
  }
}

fs.rmSync(out, { recursive: true, force: true });
if (bad) {
  console.log(`\n${bad} of the ${checked + bad} pieces in the build do not hold the invariant.`);
  process.exit(1);
}
console.log(`\nall ${checked} hold: every part of every loop totals the loop's own period.`);
