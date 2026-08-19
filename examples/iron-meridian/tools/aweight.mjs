import { voicesOf, render } from './render.mjs';
const SR = 44100;
// A-weighting: the standard approximation of how sensitivity falls off at the
// extremes. At 44 Hz it is about -30 dB, at 200 Hz about -11. So raw energy
// exaggerates the low end by roughly a factor of ten in perceived terms, and a
// band split measured in energy answers a question nobody asked.
function aGain(f) {
  const f2 = f * f;
  const r = (12194 ** 2 * f2 * f2) /
    ((f2 + 20.6 ** 2) * Math.sqrt((f2 + 107.7 ** 2) * (f2 + 737.9 ** 2)) * (f2 + 12194 ** 2));
  return 10 ** ((20 * Math.log10(r) + 2.0) / 20);
}
function band(pcm, lo, hi, weighted) {
  const seg = pcm.subarray(0, Math.min(pcm.length, SR * 60));
  let sum = 0;
  for (let p = 0; p < 14; p++) {
    const f = lo * Math.pow(hi / lo, p / 13);
    const k = Math.round((seg.length * f) / SR), w = (2 * Math.PI * k) / seg.length, c = 2 * Math.cos(w);
    let s1 = 0, s2 = 0, s0 = 0;
    for (let i = 0; i < seg.length; i++) { s0 = seg[i] + c * s1 - s2; s2 = s1; s1 = s0; }
    const e = Math.sqrt(Math.abs(s1 * s1 + s2 * s2 - c * s1 * s2)) / seg.length;
    sum += weighted ? e * aGain(f) : e;
  }
  return sum;
}
console.log('       energia crua            ponderada em A (percebido)');
console.log('tema   <100Hz  100-300Hz      <100Hz  100-300Hz  >400Hz');
for (let m = 1; m <= 5; m++) {
  const pcm = render(voicesOf(process.argv[2], process.argv[3], `Probe.t${m}`));
  const r = [band(pcm,35,99,0), band(pcm,100,300,0)];
  const a = [band(pcm,35,99,1), band(pcm,100,300,1), band(pcm,400,3000,1)];
  const rt = r[0]+r[1], at = a[0]+a[1]+a[2];
  const p = (x,t) => (100*x/t).toFixed(0).padStart(5) + '%';
  console.log(`  ${m}   ${p(r[0],rt)}  ${p(r[1],rt)}       ${p(a[0],at)}  ${p(a[1],at)}  ${p(a[2],at)}`);
}
