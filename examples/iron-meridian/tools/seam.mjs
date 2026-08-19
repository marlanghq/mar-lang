import { voicesOf, render } from './render.mjs';
const RT = process.argv[2], PROG = process.argv[3];
const SR = 44100;
const rms = (a, i, n) => { let s = 0; for (let k = 0; k < n; k++) s += a[i + k] ** 2; return Math.sqrt(s / n); };
console.log('tema   emenda(fim/inicio)   maior silencio   voz mais alta   vozes');
for (let m = 1; m <= 5; m++) {
  const vs = voicesOf(RT, PROG, `Probe.t${m}`);
  const pcm = render(vs);
  const total = Math.max(...vs.map(v => (v.delayMs || 0) + (v.ms || 0)));
  const end = Math.floor((total / 1000) * SR);
  // the seam: last 2s of the loop against the first 2s, since bar N hands to bar 1
  const a = rms(pcm, end - 2 * SR, 2 * SR), b = rms(pcm, 0, 2 * SR);
  // longest stretch where nothing at all is sounding (a hole the player would notice)
  const win = Math.floor(SR * 0.1);
  let gap = 0, run = 0;
  for (let i = 0; i + win < end; i += win) {
    if (rms(pcm, i, win) < 0.0015) { run++; if (run > gap) gap = run; } else run = 0;
  }
  // the loudest single note, as a share of the peak: a stab that jumps out
  let loudest = 0;
  for (const v of vs) if (v.wave !== 'Rest') loudest = Math.max(loudest, v.volume || 0);
  console.log(`  ${m}    ${(a / b).toFixed(2)}x` + ' '.repeat(16) +
    `${(gap * 0.1).toFixed(1)}s` + ' '.repeat(13) + `${loudest}` + ' '.repeat(13) + vs.length);
}
