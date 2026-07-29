// Offline feasibility sim for pulse-runner — mirrors stepRun EXACTLY
// (spd/fp/grav/jumpV/pw/groundY, supportedAt, landTop, deadAt) and drives a
// bot that presses jump ONLY on the planned jump beats. Proves the extended
// 6-loop level is completable from the start and from every checkpoint.

const spd = 2, fp = 16, grav = 4, jumpV = 60, pw = 13, groundY = 164;
const unitPx = 15, beatPx = 60, loopPx = 1920, finishX = 17280;

// ----- the level (must match obs in Main.mar) -----
const obs = [
  // loop 1
  ['S',34,0],['S',50,0],['S',66,0],['S',82,0],['S',94,0],['S',98,0],['S',114,0],
  // loop 2
  ['B',139,12,1],['S',146,1],
  ['G',161,2],
  ['S',174,0],['S',178,0],
  ['S',186,0],
  ['S',193,0],['S',194,0],
  ['S',210,0],
  ['G',217,2],
  ['S',226,0],['S',230,0],['S',234,0],
  ['S',246,0],
  // loop 3
  ['S',266,0],
  ['G',273,2],
  ['S',282,0],['S',286,0],['S',290,0],['S',294,0],
  ['B',307,12,1],['S',314,1],
  ['S',330,0],
  ['G',337,2],
  ['S',345,0],['S',346,0],
  ['S',354,0],['S',358,0],['S',362,0],['S',366,0],
  ['S',378,0],
  // loop 4
  ['S',394,0],
  ['G',401,2],
  ['S',410,0],['S',414,0],['S',418,0],
  ['S',425,0],['S',426,0],
  ['B',435,16,1],['S',442,1],['S',450,1],
  ['S',465,0],['S',466,0],
  ['G',473,2],
  ['S',482,0],['S',486,0],['S',490,0],
  ['S',498,0],
  // loop 5 (new)
  ['S',522,0],
  ['S',529,0],['S',530,0],
  ['G',537,2],
  ['S',546,0],['S',550,0],['S',554,0],
  ['S',565,0],['S',566,0],
  ['B',579,16,1],['S',586,1],['S',594,1],
  ['S',601,0],['S',602,0],
  ['G',609,2],
  ['S',618,0],['S',622,0],['S',626,0],
  ['S',634,0],
  // loop 6 (new, the gauntlet)
  ['S',649,0],['S',650,0],
  ['G',657,2],
  ['S',665,0],['S',666,0],
  ['S',674,0],['S',678,0],['S',682,0],['S',686,0],['S',690,0],
  ['S',701,0],['S',702,0],
  ['B',715,20,1],['S',722,1],['S',730,1],
  ['G',745,2],
  ['S',754,0],['S',758,0],['S',762,0],
  // loop 7 (the crossing)
  ['S',777,0],['S',778,0],
  ['G',785,2],
  ['G',793,2],
  ['S',802,0],['S',806,0],['S',810,0],['S',814,0],['S',818,0],['S',822,0],
  ['S',833,0],['S',834,0],
  ['S',842,0],
  ['B',851,16,1],['S',858,1],['S',866,1],
  ['S',877,0],['S',878,0],
  ['S',886,0],['S',890,0],['S',894,0],
  // loop 8 (the high road)
  ['S',906,0],
  ['S',913,0],['S',914,0],
  ['G',921,2],
  ['B',931,28,1],['S',938,1],['S',946,1],['S',954,1],
  ['S',965,0],['S',966,0],
  ['G',973,2],
  ['S',982,0],['S',986,0],['S',990,0],['S',994,0],['S',998,0],
  ['S',1009,0],['S',1010,0],
  ['S',1018,0],
  // loop 9 (the storm home)
  ['S',1033,0],['S',1034,0],
  ['S',1042,0],['S',1046,0],['S',1050,0],['S',1054,0],['S',1058,0],['S',1062,0],['S',1066,0],
  ['G',1077,2],
  ['S',1085,0],['S',1086,0],
  ['G',1093,2],
  ['S',1101,0],['S',1102,0],
  ['B',1107,20,1],['S',1114,1],['S',1122,1],
  ['S',1133,0],['S',1134,0],
  ['S',1142,0],['S',1146,0],['S',1150,0],
];

// ----- jump plan (must match jumps1..jumps6 in Main.mar) -----
const jumps = {
  0: [8,12,16,20,23,24,28],
  1: [2,4,8,11,12,14,16,20,22,24,25,26,29],
  2: [2,4,6,7,8,9,12,14,18,20,22,24,25,26,27,30],
  3: [2,4,6,7,8,10,12,14,16,20,22,24,25,26,28],
  4: [2,4,6,8,9,10,13,16,18,20,22,24,26,27,28,30],
  5: [2,4,6,8,9,10,11,12,15,18,20,22,26,28,29,30],
  6: [2,4,6,8,9,10,11,12,13,16,18,20,22,24,27,29,30,31],
  7: [2,4,6,8,10,12,14,17,19,21,22,23,24,25,28,30],
  8: [2,4,5,6,7,8,9,10,13,15,17,19,20,22,24,27,29,30,31],
};
const jumpXs = new Set();
for (const [k, list] of Object.entries(jumps))
  for (const b of list) jumpXs.add((Number(k) * 32 + b) * beatPx);

// ----- geometry (mirrors spikeBoxes / blockRects / groundSegs) -----
const spikeBoxes = obs.filter(o => o[0] === 'S').map(([_, u, lift]) => {
  const x = u * unitPx, top = groundY - lift * unitPx - unitPx;
  return { x0: x + 4, x1: x + 11, y0: top + 5, y1: top + 15 };
});
const blockRects = obs.filter(o => o[0] === 'B').map(([_, u, w, h]) => (
  { x: u * unitPx, w: w * unitPx, top: groundY - h * unitPx }));
const groundSegs = (() => {
  let cur = -600; const segs = [];
  for (const o of obs) if (o[0] === 'G') {
    const [_, u, w] = o;
    segs.push({ x0: cur, x1: u * unitPx });
    cur = (u + w) * unitPx;
  }
  segs.push({ x0: cur, x1: finishX + 900 });
  return segs;
})();

const supportedAt = (px, topPx) =>
  (topPx === groundY && groundSegs.some(s => px + pw > s.x0 && px < s.x1)) ||
  blockRects.some(b => b.top === topPx && px + pw > b.x && px < b.x + b.w);

function landTop(px, prevFp, newFp) {
  let best = 999;
  for (const s of groundSegs)
    if (px + pw > s.x0 && px < s.x1 && prevFp <= groundY * fp && groundY * fp <= newFp)
      best = Math.min(best, groundY);
  for (const b of blockRects)
    if (px + pw > b.x && px < b.x + b.w && prevFp <= b.top * fp && b.top * fp <= newFp)
      best = Math.min(best, b.top);
  return best;
}

function deadAt(px, botFp) {
  const bot = Math.trunc(botFp / fp), top = bot - pw;
  if (botFp > (groundY + 12) * fp) return 'pit';
  for (const s of spikeBoxes)
    if (px < s.x1 && px + pw > s.x0 && top < s.y1 && bot > s.y0) return 'spike';
  for (const b of blockRects)
    if (px < b.x + b.w && px + pw > b.x && bot > b.top + 2 && top < groundY) return 'block side';
  return null;
}

// ----- the run (mirrors stepRun; bot presses only at planned beats) -----
function simFrom(cp) {
  let px = cp * loopPx, bot = groundY * fp, vy = 0, grounded = true;
  const missed = [];
  for (let t = 0; t < 300000; t++) {
    const px2 = px + spd;
    const topPx = Math.trunc(bot / fp);
    const gEdge = grounded && supportedAt(px2, topPx);
    const wantsJump = jumpXs.has(px2);
    if (wantsJump && !gEdge) missed.push(px2 / beatPx);
    const jumping = gEdge && wantsJump;
    const g1 = gEdge && !jumping;
    const vy1 = jumping ? -jumpV : grounded ? 0 : vy;
    const vy2 = g1 ? 0 : Math.min(120, vy1 + grav);
    const prev = bot;
    const botRaw = g1 ? bot : bot + vy2;
    const landed = !g1 && vy2 > 0 ? landTop(px2, prev, botRaw) : 999;
    const g2 = g1 || landed < 999;
    const bot2 = landed < 999 ? landed * fp : botRaw;
    const vy3 = landed < 999 ? 0 : vy2;
    const death = deadAt(px2, bot2);
    if (death) return { ok: false, why: death, x: px2, beat: (px2 / beatPx).toFixed(2), missed };
    if (px2 >= finishX) return { ok: true, missed };
    px = px2; bot = bot2; vy = vy3; grounded = g2;
  }
  return { ok: false, why: 'timeout', missed };
}

let fail = 0;
for (let cp = 0; cp <= 8; cp++) {
  const r = simFrom(cp);
  const miss = r.missed.length ? `  (airborne presses at beats: ${r.missed.join(', ')})` : '';
  if (r.ok) console.log(`OK   from checkpoint ${cp} (x=${cp * loopPx}) -> finish${miss}`);
  else { console.log(`FAIL from checkpoint ${cp}: ${r.why} at x=${r.x} (beat ${r.beat})${miss}`); fail = 1; }
}
process.exit(fail);
