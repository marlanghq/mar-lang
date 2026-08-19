import { voicesOf, render } from './render.mjs';
const RT = process.argv[2], PROG = process.argv[3];
const SR = 44100;
// The march is 4 impacts a bar of 3.2 s = one every 800 ms = 1.25 Hz. If the
// pulse is doing its job, the loudness ENVELOPE has a peak there. If it is
// buried under the pads, it will not.
function envPeak(pcm, from, to) {
  const hop = Math.round(SR * 0.01);                 // 100 Hz envelope
  const env = [];
  for (let i = from; i + hop < to; i += hop) {
    let s = 0; for (let k = 0; k < hop; k++) s += pcm[i + k] ** 2;
    env.push(Math.sqrt(s / hop));
  }
  const mean = env.reduce((a, b) => a + b, 0) / env.length;
  const ac = [];
  for (const f of [0.6, 0.8, 1.0, 1.25, 1.6, 2.0, 2.5]) {
    let re = 0, im = 0;
    for (let i = 0; i < env.length; i++) {
      const t = i / 100, w = 2 * Math.PI * f * t;
      re += (env[i] - mean) * Math.cos(w); im += (env[i] - mean) * Math.sin(w);
    }
    ac.push([f, Math.sqrt(re * re + im * im) / env.length / Math.max(1e-9, mean)]);
  }
  return ac;
}
for (const m of [1, 3]) {
  const vs = voicesOf(RT, PROG, `Probe.t${m}`);
  const pcm = render(vs);
  // movement III, the march: bars 27-38 of theme 1 -> around 84-120 s
  const rows = envPeak(pcm, 90 * SR, 118 * SR);
  console.log(`tema ${m}, modulacao do envelope no trecho de marcha:`);
  for (const [f, v] of rows) {
    const bar = '#'.repeat(Math.round(v * 120));
    console.log(`  ${f.toFixed(2)} Hz  ${v.toFixed(3)} ${bar}${f === 1.25 ? '   <- a marcha' : ''}`);
  }
}
