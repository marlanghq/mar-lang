import { voicesOf } from './render.mjs';
const vs = voicesOf(process.argv[2], process.argv[3], 'Probe.t1').filter(v => v.wave !== 'Rest' && v.freq > 0);
const BAR = 16 * 200;
const NAME = {1:'2a menor', 6:'tritono', 11:'7a maior'};
const hits = new Map();
const end = Math.max(...vs.map(v => v.delayMs + v.ms));
for (let ms = 0; ms < end; ms += 50) {
  const now = vs.filter(v => v.delayMs <= ms && ms < v.delayMs + v.ms);
  for (let i = 0; i < now.length; i++) for (let j = i + 1; j < now.length; j++) {
    const k = ((Math.round(12 * Math.log2(now[j].freq / now[i].freq)) % 12) + 12) % 12;
    if (k === 1 || k === 6 || k === 11) {
      const key = `${NAME[k]}  ${Math.min(now[i].freq,now[j].freq)} contra ${Math.max(now[i].freq,now[j].freq)}`;
      const e = hits.get(key) || { ms: 0, bars: new Set() };
      e.ms += 50; e.bars.add(1 + Math.floor(ms / BAR));
      hits.set(key, e);
    }
  }
}
console.log('onde a aspereza restante do tema 1 vive:');
[...hits.entries()].sort((a,b) => b[1].ms - a[1].ms).forEach(([k, e]) => {
  const bars = [...e.bars].sort((a,b)=>a-b);
  console.log(`  ${k.padEnd(30)} ${(e.ms/1000).toFixed(1)}s  compassos ${bars.slice(0,8).join(',')}${bars.length>8?'...':''}`);
});
