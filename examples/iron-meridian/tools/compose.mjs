// Generator for Iron Meridian's soundtrack.
//
// Writes Music.mar from a description of five pieces. Its job is to make the
// things that must be true true BY CONSTRUCTION: every pattern is exactly 16
// steps, every drum pattern is exactly 16 slots, and every voice plays the same
// number of bars, so Sound.loop's invariant (all voices summing to identical
// milliseconds) cannot be broken by a typo. It also refuses to write a piece
// under three minutes.
//
// Four voices: bass, counter, lead, kit. 150 ms a step, so a bar is 2.4 s.
//
// WHERE THE MUSIC CAME FROM. Four of the five are ported from the Star Trek
// shooter (~/Downloads/startrek-monteiro/mar/Main.mar), where each was a
// one-minute faction cue. The patterns below are those patterns; movement I of
// each piece is that cue's original order list, unchanged, and everything after
// it is new. Mission 5 is original.
//
// THE RULES THE OLD SOUNDTRACK HAD, AND WHY THEY ARE GONE. The bed this
// replaces enforced a harsh-interval budget of 2% (no semitones, no tritones),
// a 392 Hz ceiling, and no percussion. Those checks are deleted rather than
// relaxed: the Klingon piece opens on a tritone fanfare and the Borg piece is
// built on a fixed b2 beep, so a budget that rejects them is a budget that
// rejects the music. What remains checked is what can silently break the loop.

import fs from 'node:fs';

const _ = 0;
const STEP_MS = 150;
const BARS_MIN = 75;              // 75 * 16 * 150 ms = 180 s

// Transpose a pattern by s semitones, leaving rests alone. A tracker's oldest
// tool, and the reason four one-minute cues could become four three-minute
// pieces without inventing four new pieces: a restatement a fourth up is a real
// device, not filler.
const tr = (p, s) => p.map(([hz, n]) => [hz === 0 ? 0 : Math.round(hz * Math.pow(2, s / 12)), n]);

// =====================================================================
// 1. DUST AND IRON  (Star Trek: the Klingon cue)
//
// D minor at 100 BPM. Form of the original 24 bars: a two-bar G#dim7 fanfare on
// the Ab-D tritone, statement and answer, a real A7 cadence with the leading
// tone, an octave-up pass, a bridge that drops to Bb-C major and puts the hook
// over foreign harmony, one bar of drums alone, a chromatic climb in
// accelerating 16ths, a restatement where the counter doubles the hook an octave
// below the lead, a gallop peak, a C-augmented tension bar, and the climb again
// into bar 1. The hook is the rising fourth D4-G4, braced by a bass that moves
// underneath the held G so the 11th turns into a root instead of hanging.
// =====================================================================
const kBass = [
  [[73, 3], [_, 1], [73, 2], [73, 1], [117, 3], [_, 1], [98, 2], [98, 2], [110, 1]],   // 0 D2, the hook's floor
  [[117, 3], [_, 1], [117, 2], [117, 1], [78, 3], [_, 1], [78, 2], [78, 2], [73, 1]],  // 1 Bb2 to Eb2
  [[104, 3], [_, 1], [104, 2], [104, 1], [104, 3], [_, 1], [123, 2], [123, 2], [147, 1]], // 2 Ab2: the tritone bar
  [[110, 3], [_, 1], [110, 2], [110, 1], [110, 2], [139, 2], [165, 2], [196, 2], [110, 1]], // 3 A2 walking up
  [[117, 6], [_, 1], [117, 1], [131, 3], [_, 1], [131, 2], [65, 2]],                   // 4 the bridge, Bb-C
  [[_, 16]],                                                                           // 5 tacet
  [[110, 3], [_, 1], [110, 2], [117, 2], [123, 2], [131, 2], [131, 1], [139, 1], [139, 1], [139, 1]], // 6 the chromatic climb
  [[73, 3], [_, 1], [73, 2], [73, 1], [73, 3], [_, 1], [73, 2], [104, 2], [104, 1]],   // 7 D2 souring to Ab2
  // --- new: the continuation ---
  [[73, 8], [110, 8]],                                                                 // 8 attrition: root and fifth, whole halves
  [[87, 8], [65, 8]],                                                                  // 9 F2 down to C2
  [[98, 6], [73, 4], [110, 6]],                                                        // 10 G2 D2 A2, slow
  [[73, 1], [73, 1], [73, 2], [110, 1], [110, 1], [110, 2], [87, 1], [87, 1], [87, 2], [73, 4]], // 11 the gallop
  [[117, 4], [131, 4], [147, 4], [110, 4]],                                            // 12 Bb C D A: the walk home
];
kBass.push(tr(kBass[0], 5), tr(kBass[7], 5), tr(kBass[3], 5));                          // 13 14 15: G minor

const kCounter = [
  [[147, 2], [175, 1], [_, 1], [220, 2], [196, 1], [175, 1], [147, 3], [_, 1], [175, 2], [156, 1], [147, 1]], // 0
  [[_, 1], [175, 2], [233, 1], [147, 2], [175, 2], [196, 2], [_, 1], [233, 2], [196, 2], [175, 1]],           // 1
  [[208, 2], [294, 1], [208, 1], [_, 1], [294, 2], [208, 2], [247, 2], [294, 1], [247, 2], [208, 1], [175, 1]], // 2
  [[220, 3], [_, 1], [220, 2], [196, 2], [277, 2], [_, 1], [330, 2], [277, 2], [220, 1]],                     // 3
  [[233, 3], [_, 1], [233, 2], [175, 2], [262, 3], [_, 1], [330, 2], [262, 2]],                               // 4
  [[_, 16]],                                                                                                  // 5 tacet
  [[_, 1], [220, 2], [_, 1], [208, 2], [196, 2], [_, 1], [175, 2], [165, 2], [220, 3]],                       // 6 the descent, turning up to the dominant
  [[_, 1], [147, 2], [196, 6], [_, 1], [196, 3], [175, 3]],                                                   // 7 the hook, doubled low
  // --- new ---
  [[220, 8], [196, 8]],                                                                                       // 8
  [[175, 6], [233, 4], [196, 6]],                                                                             // 9
  [[147, 16]],                                                                                                // 10 one note, held
];
kCounter.push(tr(kCounter[0], 5), tr(kCounter[3], 5), tr(kCounter[6], 5));               // 11 12 13: G minor

const kLead = [
  [[_, 1], [294, 2], [392, 5], [_, 1], [392, 3], [440, 2], [392, 1], [349, 1]],   // 0 THE HOOK: D4 up to G4, held
  [[_, 1], [262, 2], [349, 5], [_, 1], [349, 3], [392, 2], [349, 1], [311, 1]],   // 1 the answer, C4-F4-Eb4
  [[_, 1], [294, 2], [392, 5], [_, 1], [392, 3], [415, 2], [392, 1], [349, 1]],   // 2 the hook, soured to Ab4
  [[_, 1], [587, 2], [784, 5], [_, 1], [784, 3], [831, 2], [784, 1], [698, 1]],   // 3 the hook an octave up
  [[_, 1], [523, 2], [698, 5], [_, 1], [698, 3], [784, 2], [698, 1], [622, 1]],   // 4 the answer an octave up
  [[415, 2], [_, 1], [294, 3], [_, 2], [415, 2], [_, 1], [494, 2], [_, 1], [587, 2]], // 5 the tritone fanfare
  [[466, 3], [523, 3], [587, 3], [_, 1], [659, 2], [587, 2], [523, 2]],           // 6 over the bridge
  [[_, 10], [330, 2], [392, 2], [_, 1], [294, 1]],                                // 7 the E-natural call
  [[_, 16]],                                                                      // 8 tacet
  // --- new ---
  [[_, 2], [392, 3], [349, 2], [311, 3], [294, 4], [_, 2]],                       // 9 the field: the hook, exhausted
  [[880, 6], [784, 4], [698, 6]],                                                 // 10 high and slow, over the peak
];
kLead.push(tr(kLead[0], 5), tr(kLead[1], 5), tr(kLead[6], 5));                      // 11 12 13: G minor

const kDrum = [
  [74, 18, 0, 26, 46, 18, 0, 60, 0, 26, 0, 0, 46, 0, 24, 0],       // 0
  [74, 18, 0, 0, 46, 20, 0, 0, 66, 0, 22, 48, 0, 0, 28, 20],       // 1
  [78, 0, 0, 0, 50, 0, 0, 70, 0, 0, 0, 50, 0, 0, 44, 46],          // 2 sparse: the fanfare bar
  [76, 20, 24, 20, 48, 20, 24, 20, 72, 20, 24, 20, 48, 22, 26, 30],// 3 the gallop
  [74, 0, 0, 68, 0, 0, 70, 0, 46, 0, 0, 24, 46, 0, 0, 0],          // 4
  [80, 0, 0, 0, 76, 0, 0, 0, 80, 0, 0, 74, 0, 40, 44, 48],         // 5
  [74, 0, 22, 0, 46, 0, 0, 0, 30, 32, 36, 40, 44, 48, 50, 50],     // 6 the accelerating climb
  // --- new ---
  [46, 0, 0, 0, 0, 0, 20, 0, 44, 0, 0, 0, 0, 0, 22, 0],            // 7 attrition
  [78, 0, 0, 0, 0, 0, 0, 0, 52, 0, 0, 0, 0, 0, 26, 30],            // 8 half time, heavy
  [76, 0, 24, 0, 50, 0, 24, 0, 76, 24, 0, 24, 50, 26, 30, 34],     // 9 drums alone
];

const T1 = {
  id: 1, title: 'Dust and Iron', wave: 'Sawtooth',
  note: 'D minor at 100 BPM, and the war march of the five. The hook is the rising fourth D4-G4 over a bass that keeps moving underneath the held G.',
  vol: { bass: 26, counter: 17, lead: 22, drum: 62 },
  bass: kBass, counter: kCounter, lead: kLead, drum: kDrum,
  form: [
    // I  the original cue, bars 1-10: fanfare, statement and answer, the A7
    //    cadence, the octave-up pass.
    {
      bars: 10, gain: { bass: 100, counter: 92, lead: 96, drum: 92 },
      bass: [7, 2, 0, 1, 7, 1, 2, 3, 1, 0],
      counter: [2, 2, 0, 1, 6, 1, 2, 3, 1, 1],
      lead: [5, 8, 0, 1, 2, 4, 5, 7, 3, 4],
      drum: [2, 6, 0, 1, 0, 1, 2, 6, 3, 3],
    },
    // II the bridge, the bar of drums alone, and the chromatic climb.
    {
      bars: 5, gain: { bass: 74, counter: 66, lead: 70, drum: 78 },
      bass: [4, 1, 4, 5, 6],
      counter: [4, 1, 4, 5, 3],
      lead: [6, 1, 0, 8, 7],
      drum: [4, 4, 0, 5, 6],
    },
    // III the restatement, the gallop peak, the C-augmented bar, the climb.
    {
      bars: 9, gain: { bass: 100, counter: 96, lead: 100, drum: 100 },
      bass: [7, 4, 7, 0, 2, 1, 0, 4, 6],
      counter: [7, 4, 7, 1, 2, 7, 0, 4, 3],
      lead: [3, 4, 2, 1, 5, 3, 4, 3, 7],
      drum: [3, 0, 1, 3, 3, 3, 0, 2, 6],
    },
    // IV the field. Everything the first three movements were is taken away:
    //    whole-bar notes, the hook only as a memory of itself, the kit down to
    //    two hits a bar. Twelve bars is long enough to stop being a transition.
    {
      bars: 12, gain: { bass: 58, counter: 50, lead: 44, drum: 52 },
      bass: [8, 9, 8, 10],
      counter: [8, 9, 8, 10],
      lead: [8, 9, 8, 8],
      drum: [7, 7, 7, 8],
    },
    // V  a fourth up, in G minor: the same hook, the same bass, one key over.
    {
      bars: 12, gain: { bass: 86, counter: 80, lead: 88, drum: 84 },
      bass: [13, 14, 13, 15],
      counter: [11, 12, 11, 13],
      lead: [11, 12, 11, 13],
      drum: [0, 1, 2, 3],
    },
    // VI the break: bass and kit, nothing else, and the gallop underneath.
    {
      bars: 10, gain: { bass: 96, counter: 100, lead: 100, drum: 100 },
      bass: [11, 11, 12, 11],
      counter: [5],
      lead: [8],
      drum: [9, 9, 3, 9],
    },
    // VII the peak: the hook an octave up, the counter doubling it below.
    {
      bars: 12, gain: { bass: 100, counter: 94, lead: 100, drum: 100 },
      bass: [3, 0, 6, 2],
      counter: [3, 4, 2, 0],
      lead: [3, 4, 6, 10],
      drum: [3, 5, 3, 4],
    },
    // VIII the descent, ending on the E-natural call so bar 76 launches bar 1.
    {
      bars: 6, gain: { bass: 78, counter: 70, lead: 68, drum: 76 },
      bass: [0, 1, 7, 7, 4, 6],
      counter: [0, 1, 7, 5, 4, 6],
      lead: [0, 1, 5, 8, 7, 7],
      drum: [0, 1, 2, 0, 6, 6],
    },
  ],
};

// =====================================================================
// 2. THE SILO GAMBIT  (Star Trek: the Romulan cue)
//
// D minor implied and never stated: a D2 pedal that enters on step 1, never the
// downbeat, and flicks A#2 instead of a third; a chromatic wedge that descends
// one step further each time it returns; an E7b5 floor whose symmetry hides its
// own root; a G# minor bridge at the tritone pole. The hook has a fixed rhythm,
// A4(6) A#4(2) A4(4) E4(4), restated as a sequence and inverted up high. The
// counter is capped at D#4 so it never crosses the lead.
// =====================================================================
const rBass = [
  [[_, 1], [73, 7], [_, 1], [73, 4], [_, 2], [110, 1]],        // 0 the pedal, off the downbeat
  [[_, 1], [73, 6], [117, 3], [_, 1], [73, 3], [_, 1], [110, 1]], // 1 with the A#
  [[73, 3], [69, 4], [65, 5], [123, 3], [_, 1]],               // 2 the wedge, stopping on B2
  [[73, 3], [69, 4], [65, 4], [123, 2], [117, 2], [_, 1]],     // 3 the wedge, one further
  [[_, 2], [82, 6], [_, 1], [117, 4], [82, 2], [104, 1]],      // 4 the E7b5 floor
  [[_, 1], [104, 7], [78, 4], [_, 1], [104, 2], [65, 1]],      // 5 the G# pole
  [[_, 1], [87, 7], [117, 4], [_, 1], [65, 2], [_, 1]],        // 6 F major, the release
  // --- new ---
  [[73, 16]],                                                   // 7 the pedal alone, a whole bar
  [[65, 8], [73, 8]],                                           // 8 C2 leaning up to D2
  [[87, 6], [82, 4], [78, 6]],                                  // 9 the wedge in slow motion
  [[73, 2], [_, 2], [73, 2], [_, 2], [69, 2], [_, 2], [65, 2], [_, 2]], // 10 the pedal, interrupted
];
rBass.push(tr(rBass[0], 6), tr(rBass[2], 6), tr(rBass[4], 6));  // 11 12 13: the tritone pole

const rCounter = [
  [[_, 1], [294, 6], [220, 2], [165, 7]],                       // 0 the imperial signature, falling
  [[_, 16]],                                                    // 1 tacet
  [[_, 3], [147, 4], [220, 4], [233, 5]],                       // 2
  [[_, 1], [220, 4], [233, 4], [247, 4], [262, 3]],             // 3 the chromatic creep
  [[_, 2], [165, 5], [208, 4], [233, 3], [147, 2]],             // 4
  [[_, 3], [208, 4], [247, 3], [311, 4], [277, 2]],             // 5
  [[_, 4], [175, 5], [262, 4], [220, 3]],                       // 6
  // --- new ---
  [[147, 8], [165, 8]],                                         // 7
  [[196, 5], [208, 5], [220, 6]],                               // 8 the creep, slower
  [[_, 6], [233, 4], [196, 6]],                                 // 9
];
rCounter.push(tr(rCounter[0], 6), tr(rCounter[3], 6));           // 10 11

const rLead = [
  [[_, 10], [440, 4], [466, 2]],                                // 0 the hook entering late
  [[440, 6], [466, 2], [440, 4], [330, 4]],                     // 1 THE HOOK
  [[_, 1], [392, 6], [415, 2], [392, 4], [294, 3]],             // 2 the sequence, a tone down
  [[_, 2], [659, 6], [622, 2], [659, 3], [494, 3]],             // 3 the inversion, up high
  [[_, 1], [415, 3], [440, 3], [466, 4], [494, 5]],             // 4 the climb, stage one
  [[_, 1], [523, 3], [554, 3], [587, 4], [740, 5]],             // 5 the climb, topping out on F#5
  [[_, 2], [349, 3], [440, 6], [392, 3], [349, 2]],             // 6 the release, an octave down
  // --- new ---
  [[_, 6], [587, 4], [554, 3], [494, 3]],                       // 7
  [[440, 8], [466, 4], [440, 4]],                               // 8 the hook, stretched
  [[_, 4], [330, 4], [349, 4], [392, 4]],                       // 9 the low answer
  [[_, 16]],                                                    // 10 tacet
];
rLead.push(tr(rLead[1], 6), tr(rLead[3], 6));                    // 11 12

const rDrum = [
  [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],             // 0 silence
  [0, 0, 0, 54, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],            // 1 one hit, off the beat
  [0, 0, 0, 52, 0, 0, 0, 0, 0, 0, 0, 56, 0, 0, 12, 0],          // 2
  [0, 0, 54, 0, 0, 0, 16, 0, 0, 58, 0, 0, 0, 14, 0, 20],        // 3
  [66, 0, 18, 0, 0, 22, 60, 0, 0, 16, 58, 0, 0, 20, 0, 24],     // 4 the escalation
  [0, 0, 60, 0, 20, 0, 26, 0, 62, 0, 22, 0, 24, 58, 0, 28],     // 5
  [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 12, 18, 24, 30],         // 6 the bare crescendo roll
  // --- new ---
  [64, 0, 0, 0, 44, 0, 0, 0, 64, 0, 0, 0, 44, 0, 0, 20],        // 7 flat, four to the bar
  [0, 0, 56, 0, 0, 20, 58, 0, 0, 18, 60, 0, 20, 0, 24, 0],      // 8
];

const T2 = {
  id: 2, title: 'The Silo Gambit', wave: 'Triangle',
  note: 'D minor implied and never stated. A pedal that enters off the downbeat, a chromatic wedge that descends one step further every time it returns, and a hook with a fixed rhythm that is sequenced, inverted and finally stretched.',
  vol: { bass: 28, counter: 15, lead: 19, drum: 54 },
  bass: rBass, counter: rCounter, lead: rLead, drum: rDrum,
  form: [
    // I  the original cue, bars 1-10.
    {
      bars: 10, gain: { bass: 88, counter: 76, lead: 84, drum: 70 },
      bass: [0, 1, 0, 1, 0, 2, 1, 4, 4, 3],
      counter: [0, 1, 2, 1, 4, 3, 2, 4, 3, 3],
      lead: [0, 0, 1, 0, 2, 1, 2, 1, 2, 1],
      drum: [1, 0, 1, 2, 2, 0, 2, 3, 3, 3],
    },
    // II bars 11-20: the escalation, the climb, the F major release by
    //    subtraction, and the seam figure.
    {
      bars: 10, gain: { bass: 100, counter: 88, lead: 96, drum: 92 },
      bass: [4, 5, 5, 5, 5, 6, 6, 6, 3, 4],
      counter: [4, 5, 5, 3, 5, 1, 6, 6, 0, 4],
      lead: [0, 4, 3, 4, 5, 6, 6, 2, 1, 0],
      drum: [6, 4, 5, 4, 6, 0, 1, 1, 2, 0],
    },
    // III the pedal alone. Nothing here is new material; it is the same cue with
    //    every part but the floor removed, which is the only way this piece can
    //    get quieter, since it was never loud.
    {
      bars: 10, gain: { bass: 68, counter: 54, lead: 48, drum: 40 },
      bass: [7, 8, 7, 9],
      counter: [1, 7, 1, 9],
      lead: [10, 10, 0, 10],
      drum: [0, 0, 1, 0],
    },
    // IV the wedge completes, and the kit finally keeps time.
    {
      bars: 12, gain: { bass: 90, counter: 78, lead: 82, drum: 76 },
      bass: [2, 3, 9, 10],
      counter: [3, 8, 3, 2],
      lead: [7, 2, 8, 4],
      drum: [7, 3, 7, 8],
    },
    // V  the tritone pole, six semitones from home and the furthest the piece
    //    ever goes. G# minor: the same wedge, the same hook.
    {
      bars: 12, gain: { bass: 96, counter: 84, lead: 92, drum: 88 },
      bass: [11, 12, 11, 13],
      counter: [10, 11, 10, 5],
      lead: [11, 12, 11, 5],
      drum: [4, 5, 4, 8],
    },
    // VI the release, held longer than the original could hold it: F major, the
    //    drums out entirely, the tune an octave down.
    {
      bars: 10, gain: { bass: 62, counter: 46, lead: 58, drum: 100 },
      bass: [6, 8, 6, 7],
      counter: [1, 6, 1, 1],
      lead: [6, 9, 6, 10],
      drum: [0],
    },
    // VII the escalation again, and the crescendo roll into bar 1.
    {
      bars: 12, gain: { bass: 100, counter: 90, lead: 100, drum: 96 },
      bass: [4, 5, 4, 3, 4, 0],
      counter: [4, 5, 3, 5, 4, 0],
      lead: [3, 5, 4, 5, 1, 0],
      drum: [4, 5, 4, 5, 6, 6],
    },
  ],
};

// =====================================================================
// 3. STATIC  (Star Trek: the Borg cue)
//
// E minor, and the piece where the development is all in the floor. The
// counter's F3 beep never changes pitch; the bass moves under it, so the same
// note is heard as b2, then a fourth over C, a m3 over D, a tritone over B, and
// finally the ROOT when the floor lurches to F. The phasing is real: 16 mod 3 =
// 1, so the three-step counter cells continue across barlines instead of
// resetting. The hook is a signal, not a tune: F5-E5-B4, always entering at step
// 2, never transposed.
// =====================================================================
const bBass = [
  [[82, 2], [82, 2], [82, 2], [82, 2], [82, 2], [82, 2], [82, 2], [82, 2]],  // 0 E2, the pulse floor
  [[82, 3], [82, 3], [82, 2], [_, 1], [82, 3], [82, 2], [82, 2]],            // 1 the floor, faulting
  [[65, 2], [65, 2], [65, 2], [65, 1], [82, 1], [65, 2], [65, 2], [65, 2], [65, 2]], // 2 C2: VI
  [[73, 3], [73, 3], [73, 2], [73, 2], [69, 1], [73, 3], [73, 2]],           // 3 D2: bVII
  [[123, 2], [123, 2], [123, 3], [78, 1], [123, 2], [123, 2], [123, 2], [_, 1], [87, 1]], // 4 B2: V
  [[87, 2], [87, 2], [87, 2], [87, 1], [123, 1], [87, 2], [87, 2], [87, 2], [87, 2]], // 5 F2: the lurch
  [[87, 3], [_, 1], [87, 2], [87, 1], [_, 2], [87, 3], [87, 1], [_, 1], [87, 1], [78, 1]], // 6 F2, faulting in time
  // --- new ---
  [[82, 16]],                                                                // 7 the floor, one note
  [[98, 2], [98, 2], [98, 2], [98, 1], [82, 1], [98, 2], [98, 2], [98, 2], [98, 2]], // 8 G2: III
  [[110, 2], [110, 2], [110, 2], [110, 1], [82, 1], [110, 2], [110, 2], [110, 2], [110, 2]], // 9 A2: iv
  [[_, 16]],                                                                 // 10 the machine stops
  [[82, 1], [82, 1], [82, 1], [82, 1], [82, 1], [82, 1], [82, 1], [82, 1], [82, 1], [82, 1], [82, 1], [82, 1], [82, 1], [82, 1], [82, 1], [82, 1]], // 11 16ths: the machine at speed
];

const bCounter = [
  [[175, 1], [_, 2], [175, 1], [_, 2], [175, 1], [_, 2], [175, 1], [_, 2], [175, 1], [_, 2], [175, 1]], // 0 the F3 beep, phase 0
  [[_, 2], [175, 1], [_, 2], [175, 1], [_, 2], [175, 1], [_, 2], [175, 1], [_, 2], [175, 1], [_, 1]],   // 1 phase 1
  [[_, 1], [175, 1], [_, 2], [175, 1], [_, 2], [175, 1], [_, 2], [175, 1], [_, 2], [175, 1], [_, 2]],   // 2 phase 2
  [[247, 10], [349, 6]],                                                     // 3 the two long tones
  [[_, 1], [349, 1], [_, 1], [247, 1], [_, 1], [349, 1], [_, 1], [247, 1], [_, 1], [349, 1], [_, 1], [247, 1], [_, 1], [349, 1], [_, 1], [247, 1]], // 4 the alarm
  [[247, 2], [_, 1], [330, 1], [349, 1], [_, 1], [247, 2], [_, 1], [370, 1], [_, 1], [330, 1], [349, 1], [_, 2], [247, 1]], // 5
  [[165, 3], [330, 3], [165, 2], [_, 1], [330, 3], [165, 2], [330, 2]],      // 6 octaves
  // --- new ---
  [[349, 16]],                                                               // 7 the beep, held: the same note, refusing to move
  [[_, 3], [175, 1], [_, 3], [175, 1], [_, 3], [175, 1], [_, 3], [175, 1]],  // 8 the beep on a 4-cycle: the phasing stops
  [[247, 4], [262, 4], [247, 4], [233, 4]],                                  // 9
];

const bLead = [
  [[_, 16]],                                                                 // 0 tacet
  [[_, 2], [698, 2], [659, 2], [_, 3], [494, 3], [_, 2], [698, 1], [_, 1]],  // 1 THE SIGNAL: F5-E5-B4
  [[_, 2], [698, 2], [659, 2], [_, 1], [988, 1], [_, 1], [494, 3], [_, 2], [659, 1], [_, 1]], // 2 the signal, answered high
  [[698, 1], [_, 4], [698, 1], [_, 4], [698, 1], [_, 4], [698, 1]],          // 3 the period-5 beep, phase 0
  [[_, 4], [698, 1], [_, 4], [698, 1], [_, 4], [698, 1], [_, 1]],            // 4 phase 1
  [[_, 3], [698, 1], [_, 4], [698, 1], [_, 4], [698, 1], [_, 2]],            // 5 phase 2
  [[_, 2], [1047, 1], [988, 1], [932, 1], [880, 1], [831, 1], [784, 1], [740, 1], [698, 1], [659, 1], [622, 1], [587, 1], [554, 1], [523, 2]], // 6 the glissando
  // --- new ---
  [[_, 2], [698, 2], [659, 2], [_, 3], [494, 6], [_, 1]],                    // 7 the signal, its tail held
  [[494, 2], [_, 6], [494, 2], [_, 6]],                                      // 8 the tritone alone
  [[_, 2], [698, 1], [_, 1], [698, 1], [_, 1], [698, 1], [_, 1], [659, 1], [_, 1], [659, 1], [_, 1], [494, 4]], // 9 the signal, stuttering
];

const bDrum = [
  [64, 0, 0, 12, 0, 0, 18, 0, 0, 0, 14, 0, 0, 26, 0, 10],
  [68, 0, 14, 0, 46, 10, 0, 64, 0, 12, 16, 66, 46, 0, 12, 22],
  [70, 0, 12, 0, 44, 0, 10, 0, 0, 8, 0, 62, 44, 0, 10, 26],
  [78, 0, 16, 20, 48, 0, 58, 0, 64, 0, 16, 22, 48, 56, 0, 24],
  [70, 0, 14, 0, 46, 0, 0, 44, 0, 20, 24, 28, 48, 54, 44, 60],
  [60, 0, 0, 14, 0, 10, 0, 0, 0, 0, 16, 0, 0, 12, 0, 0],          // 5 the machine idling
  [0, 74, 0, 14, 0, 0, 10, 66, 0, 12, 0, 0, 18, 62, 0, 20],       // 6 rotated: nothing on step 0
  // --- new ---
  [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],               // 7 stopped
  [72, 16, 16, 16, 48, 16, 16, 16, 68, 16, 16, 16, 48, 16, 16, 16], // 8 straight 16ths under everything
  [76, 0, 20, 24, 50, 0, 62, 0, 70, 0, 20, 26, 50, 60, 0, 30],    // 9 the drive
];

const T3 = {
  id: 3, title: 'Static', wave: 'Square',
  note: 'E minor. One alien beep that never changes pitch, and a floor that moves under it until the same note has been every interval in the piece and finally the root. The hook is a three-note siren, never transposed.',
  vol: { bass: 24, counter: 14, lead: 17, drum: 58 },
  bass: bBass, counter: bCounter, lead: bLead, drum: bDrum,
  form: [
    // I  the original cue, bars 1-12: the floor holds E, the counter phases, the
    //    signal has not arrived.
    {
      bars: 12, gain: { bass: 88, counter: 80, lead: 84, drum: 78 },
      bass: [0, 0, 0, 1, 0, 1, 0, 1, 2, 3, 0, 4],
      counter: [3, 3, 0, 1, 2, 0, 1, 2, 0, 1, 2, 0],
      lead: [0, 0, 0, 0, 0, 0, 3, 4, 5, 1, 2, 6],
      drum: [0, 0, 1, 1, 2, 4, 1, 2, 3, 1, 2, 4],
    },
    // II bars 13-24: the lurch to F, and the bars that fault in time as well as
    //    pitch.
    {
      bars: 12, gain: { bass: 100, counter: 92, lead: 96, drum: 94 },
      bass: [1, 2, 3, 4, 5, 5, 5, 6, 5, 6, 1, 4],
      counter: [4, 4, 5, 3, 5, 5, 5, 0, 1, 2, 6, 3],
      lead: [1, 2, 6, 0, 2, 1, 6, 3, 4, 5, 2, 1],
      drum: [3, 3, 1, 5, 3, 1, 1, 6, 3, 6, 3, 4],
    },
    // III the floor takes ground it has not taken: G, then A. The beep is still
    //    F3, so it is now a major seventh and then a minor seventh, and the
    //    piece has still not played a new note in that voice.
    {
      bars: 12, gain: { bass: 92, counter: 86, lead: 78, drum: 82 },
      bass: [8, 8, 9, 9],
      counter: [0, 1, 2, 0],
      lead: [8, 3, 8, 4],
      drum: [1, 2, 1, 8],
    },
    // IV the phasing stops. The beep lands on a four-cycle, so it agrees with
    //    the bar for the first time, and agreement in this piece is the
    //    unsettling event.
    {
      bars: 10, gain: { bass: 74, counter: 68, lead: 62, drum: 58 },
      bass: [7, 7, 0, 7],
      counter: [8, 8, 8, 7],
      lead: [0, 9, 0, 9],
      drum: [5, 5, 0, 5],
    },
    // V  the machine stops dead for a bar and then runs at speed. Sixteenths in
    //    the bass, sixteenths in the kit, the signal stuttering over it.
    {
      bars: 12, gain: { bass: 100, counter: 90, lead: 100, drum: 100 },
      bass: [10, 11, 11, 11],
      counter: [4, 5, 4, 9],
      lead: [9, 2, 9, 7],
      drum: [7, 8, 9, 8],
    },
    // VI the F lurch again, held twice as long as the original held it, with the
    //    counter refusing to move at all.
    {
      bars: 10, gain: { bass: 96, counter: 100, lead: 88, drum: 90 },
      bass: [5, 6, 5, 6],
      counter: [7, 7, 5, 7],
      lead: [1, 7, 1, 8],
      drum: [6, 3, 6, 9],
    },
    // VII the glissando down and back to the E floor, so bar 76 hands to bar 1.
    {
      bars: 8, gain: { bass: 78, counter: 72, lead: 76, drum: 70 },
      bass: [3, 2, 0, 0, 4, 0, 1, 0],
      counter: [9, 6, 0, 1, 2, 0, 1, 3],
      lead: [6, 2, 0, 0, 6, 1, 0, 0],
      drum: [2, 1, 0, 0, 4, 2, 5, 5],
    },
  ],
};

// =====================================================================
// 4. SCRAP TRUCE  (Star Trek: the Dominion cue)
//
// E phrygian dominant (E F G# A B C D), with D# used strictly as an ascending
// leading tone. Bars 9-14 of the original are a real key change to D harmonic
// minor, pivoted in by a full A7 and returned by a Bb-F-E cadence. The hook is
// E-F-G#(held)-rest-A-G#-F-rest-E(held): the augmented second gets the long note
// and the phrase has two rests, so it has a silhouette rather than being an
// undifferentiated 16th stream.
// =====================================================================
const dBass = [
  [[82, 3], [87, 3], [104, 3], [82, 3], [_, 4]],                  // 0 the riff, bare
  [[82, 3], [165, 3], [104, 3], [87, 3], [87, 3], [82, 1]],       // 1 the riff
  [[_, 1], [82, 3], [131, 2], [131, 3], [123, 2], [123, 3], [87, 2]], // 2 the answer, downbeat dropped
  [[110, 3], [110, 3], [139, 2], [110, 3], [196, 3], [139, 2]],   // 3 A7: the pivot
  [[73, 2], [73, 3], [147, 3], [110, 2], [73, 3], [175, 3]],      // 4 D harmonic minor
  [[98, 2], [98, 3], [196, 3], [117, 2], [98, 3], [147, 3]],      // 5 the bridge
  [[117, 3], [117, 3], [233, 2], [175, 3], [175, 3], [87, 2]],    // 6 the Bb-F-E cadence home
  [[82, 2], [87, 2], [92, 2], [98, 2], [104, 2], [110, 2], [117, 2], [123, 2]], // 7 the riser, stage one
  [[131, 2], [147, 2], [165, 2], [175, 2], [196, 1], [208, 1], [220, 1], [233, 1], [247, 1], [262, 1], [277, 1], [311, 1]], // 8 stage two
  [[82, 3], [87, 3], [104, 3], [123, 3], [247, 2], [165, 2]],     // 9 the climax, 3+3+3+3+4
  // --- new ---
  [[82, 3], [82, 3], [82, 2], [87, 3], [87, 3], [104, 2]],        // 10 the riff, flattened out
  [[82, 16]],                                                     // 11 the pedal
  [[_, 16]],                                                      // 12 tacet
  [[82, 1], [82, 1], [87, 1], [82, 1], [104, 1], [82, 1], [87, 1], [82, 1], [82, 1], [82, 1], [87, 1], [104, 1], [110, 1], [104, 1], [87, 1], [82, 1]], // 13 the machine 16ths
];
dBass.push(tr(dBass[1], 5), tr(dBass[2], 5), tr(dBass[9], 5));     // 14 15 16: A phrygian dominant

const dCounter = [
  [[_, 16]],                                                      // 0 tacet
  [[_, 4], [330, 6], [349, 6]],                                   // 1 the slow voice
  [[_, 1], [330, 2], [_, 1], [415, 2], [_, 1], [494, 2], [_, 1], [349, 2], [_, 1], [262, 2], [_, 1]], // 2 the arpeggio
  [[_, 2], [330, 2], [415, 4], [440, 2], [311, 2], [330, 2], [349, 2]], // 3 with the D# leading tone
  [[294, 4], [349, 4], [440, 3], [349, 3], [294, 2]],             // 4 the bridge
  [[294, 4], [466, 4], [392, 3], [466, 3], [294, 2]],             // 5
  [[277, 3], [330, 3], [392, 2], [277, 3], [330, 3], [392, 2]],   // 6
  [[466, 3], [294, 3], [349, 2], [349, 3], [440, 3], [262, 2]],   // 7
  [[165, 1], [208, 1], [247, 1], [330, 1], [247, 1], [208, 1], [165, 3], [175, 1], [220, 1], [262, 1], [349, 1], [262, 1], [220, 1], [175, 1]], // 8 the low arpeggio
  [[415, 4], [494, 4], [349, 4], [330, 4]],                       // 9 the climax
  [[415, 2], [494, 2], [698, 2], [494, 2], [415, 2], [349, 2], [415, 2], [494, 2]], // 10
  [[247, 4], [262, 4], [277, 4], [311, 4]],                       // 11 the chromatic riser
  // --- new ---
  [[165, 8], [175, 8]],                                           // 12 the semitone, held, an octave under the lead
  [[_, 2], [415, 2], [_, 2], [392, 2], [_, 2], [349, 2], [_, 2], [330, 2]], // 13 falling stabs
];
dCounter.push(tr(dCounter[2], 5), tr(dCounter[9], 5), tr(dCounter[13], 5));  // 14 15 16

const dLead = [
  [[_, 16]],                                                      // 0 tacet
  [[659, 1], [698, 1], [831, 3], [_, 1], [880, 1], [831, 1], [698, 2], [_, 1], [659, 4], [_, 1]], // 1 THE HOOK
  [[_, 2], [988, 1], [880, 1], [831, 3], [_, 1], [698, 1], [659, 2], [622, 1], [659, 2], [_, 2]], // 2 the answer
  [[880, 2], [784, 2], [659, 2], [554, 2], [440, 3], [_, 1], [554, 1], [587, 1], [659, 1], [587, 1]], // 3 A7
  [[587, 3], [698, 1], [587, 2], [440, 4], [_, 1], [466, 2], [440, 1], [587, 2]], // 4 the bridge
  [[466, 2], [587, 2], [698, 3], [587, 1], [466, 3], [392, 2], [440, 1], [466, 1], [554, 1]], // 5
  [[466, 2], [587, 2], [698, 2], [880, 2], [698, 2], [659, 2], [698, 3], [831, 1]], // 6 the cadence home
  [[659, 1], [698, 1], [831, 3], [_, 1], [1047, 1], [988, 1], [880, 2], [_, 1], [831, 1], [698, 1], [659, 2], [_, 1]], // 7 the hook, extended
  [[_, 13], [440, 1], [494, 1], [622, 1]],                        // 8 the pickup
  [[349, 1], [415, 1], [440, 1], [466, 1], [494, 1], [523, 1], [587, 1], [622, 1], [659, 2], [831, 2], [880, 2], [988, 1], [1047, 1]], // 9 the riser
  [[698, 1], [831, 1], [880, 1], [698, 1], [831, 1], [880, 1], [988, 1], [1047, 1], [988, 1], [880, 1], [831, 1], [698, 1], [659, 4]], // 10
  [[1319, 1], [1047, 1], [831, 1], [1319, 1], [1047, 1], [831, 1], [1319, 1], [1175, 1], [1047, 1], [698, 1], [831, 1], [1047, 1], [1319, 4]], // 11 the climax, E6
  // --- new ---
  [[659, 2], [698, 2], [831, 6], [_, 1], [784, 2], [659, 3]],     // 12 the hook, halved in speed
  [[_, 4], [415, 3], [440, 3], [466, 3], [494, 3]],               // 13 the low climb
];
dLead.push(tr(dLead[1], 5), tr(dLead[2], 5), tr(dLead[12], -12), tr(dLead[13], 5));  // 14 15 16 17

const dDrum = [
  [64, 0, 0, 58, 0, 0, 58, 0, 0, 54, 0, 0, 48, 0, 0, 0],          // 0 the tresillo pivot
  [68, 0, 16, 58, 0, 16, 60, 0, 16, 54, 0, 18, 50, 0, 20, 44],
  [70, 0, 18, 0, 46, 0, 60, 18, 0, 0, 58, 20, 46, 0, 18, 40],
  [0, 0, 60, 0, 46, 0, 0, 58, 20, 0, 46, 0, 60, 18, 0, 44],       // 3 nothing on the downbeat
  [66, 0, 0, 0, 44, 0, 20, 0, 58, 0, 0, 20, 44, 0, 0, 26],
  [68, 0, 20, 58, 0, 20, 46, 0, 58, 0, 20, 0, 46, 30, 38, 46],
  [70, 0, 24, 0, 48, 0, 24, 0, 66, 0, 26, 0, 30, 36, 42, 50],
  [72, 0, 20, 58, 46, 0, 60, 20, 0, 58, 20, 0, 46, 16, 20, 42],
  [62, 0, 0, 0, 0, 0, 0, 0, 44, 0, 0, 0, 0, 0, 0, 36],            // 8 the near-silent break
  [58, 14, 26, 14, 46, 16, 28, 16, 60, 18, 30, 18, 48, 20, 32, 24],
  [60, 20, 30, 22, 48, 24, 32, 26, 62, 28, 34, 30, 50, 34, 40, 46],
  [74, 22, 34, 22, 50, 22, 34, 24, 74, 24, 36, 24, 50, 26, 38, 30],
  [78, 20, 20, 72, 20, 20, 72, 20, 20, 70, 20, 20, 68, 24, 28, 50], // 12 the 3+3+3+3+4 unison
  // --- new ---
  [66, 0, 0, 0, 46, 0, 0, 0, 62, 0, 0, 0, 46, 0, 0, 0],           // 13 flat four
  [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],               // 14 out
];

const T4 = {
  id: 4, title: 'Scrap Truce', wave: 'Sawtooth',
  note: 'E phrygian dominant, the fast one, and the only piece that changes key: bars 9 to 14 are a real move to D harmonic minor, pivoted in by an A7 and returned by a Bb-F-E cadence. The hook puts its long note on the augmented second and leaves two rests, so it has a silhouette.',
  // The counter is the voice that made this piece read as busy: it is the third
  // stream and the one nobody is listening FOR. Two notches down.
  vol: { bass: 25, counter: 13, lead: 21, drum: 60 },
  bass: dBass, counter: dCounter, lead: dLead, drum: dDrum,
  form: [
    // I  the original cue, bars 1-8: the riff bare, statement and answer, the A7
    //    that pivots to the bridge.
    {
      bars: 8, gain: { bass: 92, counter: 84, lead: 90, drum: 86 },
      bass: [0, 0, 1, 2, 1, 2, 1, 3],
      counter: [0, 1, 2, 3, 2, 3, 2, 6],
      lead: [0, 8, 1, 2, 1, 2, 1, 3],
      drum: [0, 1, 2, 3, 2, 3, 2, 5],
    },
    // II bars 9-18: the D harmonic minor bridge, the restatement, the break.
    {
      bars: 10, gain: { bass: 100, counter: 92, lead: 96, drum: 94 },
      bass: [4, 5, 3, 4, 5, 6, 1, 2, 1, 0],
      counter: [4, 5, 6, 4, 5, 7, 8, 3, 8, 1],
      lead: [4, 5, 3, 4, 5, 6, 7, 2, 7, 0],
      drum: [4, 4, 5, 4, 4, 6, 7, 3, 7, 8],
    },
    // III bars 19-24: the riser that crosses barlines, and the climax on E6.
    {
      bars: 6, gain: { bass: 100, counter: 92, lead: 100, drum: 100 },
      bass: [7, 8, 9, 9, 9, 9],
      // The counter sits OUT on the bars where the lead is running sixteenths.
      // Four busy voices at once is not a climax, it is a wall: the riser and
      // the E6 hammer are the event, and they only read as one if something
      // gets out of their way.
      counter: [0, 11, 0, 9, 0, 10],
      lead: [0, 9, 10, 10, 11, 11],
      drum: [9, 10, 11, 11, 12, 12],
    },
    // IV the truce. The riff flattened to a pedal, the hook at half speed, the
    //    kit down to four flat hits, and for two bars nothing at all: the only
    //    place in the piece where the machine is not running.
    {
      bars: 12, gain: { bass: 66, counter: 54, lead: 58, drum: 50 },
      bass: [11, 10, 11, 12],
      counter: [12, 0, 12, 13],
      lead: [12, 0, 16, 0],
      drum: [13, 13, 14, 13],
    },
    // V  a fourth up, in A phrygian dominant, and the kit back at full speed.
    //
    //    EVERY pitched pattern here is transposed. Two of them were not: the
    //    counter's 13 and the lead's 13 are patterns written at home in E, and
    //    cycling them in among the +5 ones put the accompaniment a fourth away
    //    from the bass on one bar in four. That was not colour, it was two keys
    //    at once, and it is the sour patch near the end of this piece.
    {
      bars: 12, gain: { bass: 92, counter: 78, lead: 92, drum: 90 },
      bass: [14, 15, 14, 16],
      counter: [14, 16, 14, 15],
      lead: [14, 15, 14, 17],
      drum: [1, 2, 1, 7],
    },
    // VI the 16ths: bass and kit locked, the hook over the top of them.
    {
      bars: 12, gain: { bass: 100, counter: 84, lead: 100, drum: 98 },
      bass: [13, 13, 1, 13],
      // With the bass running sixteenths, an arpeggio on top of it is a third
      // stream of sixteenths and nothing can be followed. The counter drops out
      // on half the bars and the hook takes its HALF-SPEED form (12), so the
      // machine underneath stays busy and the tune over it does not.
      counter: [0, 2, 0, 9],
      lead: [1, 12, 2, 12],
      drum: [11, 9, 11, 10],
    },
    // VII the bridge one last time and the cadence back into the riff.
    {
      bars: 16, gain: { bass: 86, counter: 76, lead: 82, drum: 80 },
      bass: [4, 5, 6, 1, 2, 1, 3, 0],
      counter: [4, 5, 7, 2, 3, 2, 6, 1],
      lead: [4, 5, 6, 1, 2, 1, 3, 8],
      drum: [4, 5, 6, 1, 2, 3, 5, 0],
    },
  ],
};

// =====================================================================
// 5. MERIDIAN ENGINE  (new, for the last mission)
//
// C minor, and the only piece written for this game rather than ported into it.
// It is built on the Neapolitan: Db, a semitone above the root, used as a pedal
// under the whole middle of the piece, which is the same wound Static gets from
// its b2 beep and the same one the Klingon fanfare gets from its tritone -- so
// the finale is made of the campaign's own material without quoting any of it.
//
// Except once. Movement VII states mission 1's lead pattern verbatim. That
// pattern is C4 up to F4, held, and it is diatonic in C minor without a single
// note changed, so the campaign ends on the interval it opened with: the rising
// fourth from Dust and Iron, in the key of the last mission, with the engine
// running underneath it.
// =====================================================================
const eBass = [
  [[65, 2], [65, 2], [65, 2], [65, 2], [65, 2], [65, 2], [65, 2], [65, 2]],  // 0 C2, the engine idling
  [[65, 3], [65, 3], [65, 2], [_, 1], [65, 3], [65, 2], [65, 2]],            // 1 the same, faulting
  [[65, 2], [65, 2], [78, 2], [78, 2], [87, 2], [87, 2], [98, 2], [98, 2]],  // 2 C Eb F G, the walk up
  [[69, 2], [69, 2], [69, 2], [69, 1], [65, 1], [69, 2], [69, 2], [69, 2], [69, 2]], // 3 Db: the Neapolitan pedal
  [[104, 3], [104, 3], [104, 2], [98, 3], [98, 3], [87, 2]],                 // 4 Ab G F
  [[87, 3], [87, 3], [104, 2], [117, 3], [117, 3], [98, 2]],                 // 5 F Ab Bb G
  [[65, 1], [65, 1], [78, 1], [87, 1], [98, 1], [87, 1], [78, 1], [65, 1], [65, 1], [78, 1], [87, 1], [98, 1], [104, 1], [98, 1], [87, 1], [78, 1]], // 6 the engine at speed
  [[_, 16]],                                                                 // 7 tacet
  [[65, 4], [104, 4], [117, 4], [98, 4]],                                    // 8 i VI bVII v, whole bars
  [[98, 2], [98, 2], [123, 2], [123, 2], [131, 3], [98, 3], [65, 2]],        // 9 G, and the B natural: the one dominant
  [[65, 3], [65, 3], [65, 2], [65, 3], [65, 3], [65, 2]],                    // 10 3+3+2 twice: the asymmetric cell
  [[65, 3], [78, 3], [87, 3], [98, 3], [104, 2], [117, 2]],                  // 11 the climb, in unison with the lead
  [[65, 16]],                                                                // 12 one note, sixteen steps
  [[117, 3], [117, 3], [104, 2], [98, 3], [98, 3], [87, 2]],                 // 13 Bb Ab G F
];

const eCounter = [
  [[_, 16]],                                                                 // 0 tacet
  [[196, 4], [208, 4], [233, 4], [196, 4]],                                  // 1 G Ab Bb G
  [[_, 2], [311, 2], [262, 2], [_, 2], [311, 2], [349, 2], [_, 2], [262, 2]],// 2 the stabs
  [[156, 3], [196, 3], [233, 2], [156, 3], [196, 3], [131, 2]],              // 3 low, on the asymmetric cell
  [[277, 4], [247, 4], [262, 8]],                                            // 4 Db down through B to C: the Neapolitan resolving
  [[131, 1], [156, 1], [196, 1], [233, 1], [196, 1], [156, 1], [131, 3], [139, 1], [175, 1], [208, 1], [262, 1], [208, 1], [175, 1], [139, 1]], // 5 the arpeggio, and its Db answer
  [[208, 2], [196, 2], [175, 2], [156, 2], [175, 2], [196, 2], [208, 2], [233, 2]], // 6 the wave
  [[233, 6], [196, 4], [262, 6]],                                            // 7 slow
  [[262, 2], [311, 2], [349, 2], [392, 2], [349, 2], [311, 2], [262, 4]],    // 8 the arch
  [[139, 8], [131, 8]],                                                      // 9 Db leaning onto C, whole halves
];

const eLead = [
  [[_, 16]],                                                                 // 0 tacet
  [[_, 1], [392, 2], [415, 2], [523, 5], [_, 1], [466, 2], [392, 3]],        // 1 THE MOTTO: G-Ab-C held, falling back
  [[_, 1], [349, 2], [392, 2], [466, 5], [_, 1], [415, 2], [349, 3]],        // 2 the motto, a step down
  [[_, 1], [784, 2], [831, 2], [1047, 5], [_, 1], [932, 2], [784, 3]],       // 3 the motto an octave up
  [[_, 4], [311, 2], [349, 2], [392, 4], [349, 2], [311, 2]],                // 4 low, under the counter
  [[1047, 2], [932, 2], [831, 2], [784, 2], [698, 2], [622, 2], [587, 2], [523, 2]], // 5 the descent
  [[_, 1], [262, 2], [349, 5], [_, 1], [349, 3], [392, 2], [349, 1], [311, 1]], // 6 DUST AND IRON, verbatim: C4 up to F4
  [[_, 1], [523, 2], [698, 5], [_, 1], [698, 3], [784, 2], [698, 1], [622, 1]], // 7 the same, an octave up
  [[_, 8], [494, 2], [523, 2], [587, 2], [622, 2]],                          // 8 the leading tone, rising
  [[523, 1], [523, 1], [622, 1], [523, 1], [466, 1], [523, 1], [392, 1], [523, 1], [523, 1], [523, 1], [622, 1], [698, 1], [784, 2], [523, 2]], // 9 the hammer
  [[_, 2], [698, 3], [622, 3], [523, 4], [466, 2], [392, 2]],                // 10 the fall home
  [[277, 6], [262, 4], [277, 6]],                                            // 11 the Neapolitan, held bare
];

const eDrum = [
  [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],               // 0 out
  [62, 0, 0, 0, 0, 0, 0, 0, 44, 0, 0, 0, 0, 0, 0, 0],             // 1 half time, cold
  [66, 0, 16, 0, 46, 0, 16, 0, 62, 0, 16, 0, 46, 0, 16, 20],      // 2 the machine
  [70, 0, 18, 20, 48, 0, 60, 0, 66, 0, 18, 22, 48, 58, 0, 26],    // 3 driving
  [72, 0, 20, 0, 50, 0, 20, 0, 68, 0, 20, 0, 50, 24, 28, 32],     // 4
  [74, 20, 20, 68, 20, 20, 68, 20, 20, 66, 20, 20, 50, 24, 28, 44], // 5 3+3+3+3+4, locked with the bass
  [60, 0, 0, 14, 0, 0, 18, 0, 44, 0, 0, 16, 0, 0, 20, 0],         // 6 sparse
  [76, 0, 0, 0, 52, 0, 0, 0, 76, 0, 0, 0, 52, 30, 36, 42],        // 7 the arrival
  [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 20, 26, 34, 44],           // 8 the roll into the next bar
];

const T5 = {
  id: 5, title: 'Meridian Engine', wave: 'Sawtooth',
  note: 'C minor, new, and the only piece written for this game. Built on the Neapolitan Db as a pedal, which is the same semitone-above-the-root wound the other four get from a tritone or a b2. Movement VII states mission 1 verbatim.',
  vol: { bass: 27, counter: 17, lead: 22, drum: 62 },
  bass: eBass, counter: eCounter, lead: eLead, drum: eDrum,
  form: [
    // I  the engine, cold. The motto has not been stated.
    //
    //    The bass alternates a HELD bar with a MOVING one. It used to alternate
    //    the held bar with pattern 0, which is C2 struck eight times, and over
    //    ten bars under an almost-silent lead that is forty identical attacks on
    //    one pitch before anything else happens. A held note reads as an engine
    //    idling; the same note hit over and over reads as a fault.
    {
      bars: 10, gain: { bass: 62, counter: 44, lead: 40, drum: 48 },
      bass: [12, 8, 12, 8],
      counter: [0, 9, 0, 9],
      lead: [0, 0, 0, 4],
      drum: [1, 1, 6, 1],
    },
    // II the motto arrives.
    {
      bars: 12, gain: { bass: 84, counter: 70, lead: 88, drum: 78 },
      // The pulse arrives here, under the motto, where it is a floor and not the
      // only thing in the room -- and it MOVES on two bars in four.
      bass: [2, 1, 8, 1],
      counter: [1, 6, 1, 3],
      lead: [1, 2, 1, 8],
      drum: [2, 2, 3, 2],
    },
    // III the Neapolitan pedal. Db under everything for twelve bars, so the
    //    motto's C is now a semitone above the floor instead of the root.
    {
      bars: 12, gain: { bass: 92, counter: 82, lead: 90, drum: 84 },
      bass: [3, 3, 5, 3],
      counter: [4, 5, 4, 2],
      lead: [2, 11, 2, 10],
      drum: [3, 2, 3, 4],
    },
    // IV the dominant, and the only B natural in the piece. Then the engine at
    //    speed: 16ths in the bass, the motto an octave up.
    {
      bars: 12, gain: { bass: 100, counter: 90, lead: 100, drum: 96 },
      bass: [9, 6, 6, 6],
      counter: [5, 8, 5, 6],
      lead: [3, 9, 3, 5],
      drum: [4, 3, 4, 5],
    },
    // V  everything stops but the floor. Ten bars, and the longest quiet stretch
    //    in the campaign: the last mission earns one.
    {
      bars: 10, gain: { bass: 54, counter: 40, lead: 38, drum: 44 },
      bass: [12, 7, 12, 8],
      counter: [9, 0, 9, 7],
      lead: [0, 11, 0, 0],
      drum: [0, 6, 0, 8],
    },
    // VI the asymmetric cell: bass and kit both on 3+3+2, so the bar stops
    //    dividing in four for as long as it lasts.
    {
      bars: 10, gain: { bass: 94, counter: 84, lead: 92, drum: 92 },
      bass: [10, 10, 4, 10],
      counter: [3, 6, 3, 1],
      lead: [4, 9, 2, 9],
      drum: [5, 5, 3, 5],
    },
    // VII Dust and Iron, verbatim, over the engine. The rising fourth the
    //     campaign opened on, in the key it ends in.
    {
      bars: 12, gain: { bass: 100, counter: 94, lead: 100, drum: 100 },
      bass: [11, 6, 13, 6],
      counter: [8, 2, 8, 6],
      lead: [6, 7, 6, 3],
      drum: [7, 3, 7, 5],
    },
    // VIII the fall home, back to the cold engine of bar 1.
    {
      bars: 8, gain: { bass: 70, counter: 56, lead: 60, drum: 58 },
      bass: [5, 4, 8, 0, 2, 8, 12, 12],
      counter: [7, 4, 9, 0, 1, 9, 9, 0],
      lead: [10, 5, 2, 0, 4, 0, 0, 0],
      drum: [3, 2, 6, 1, 2, 6, 1, 1],
    },
  ],
};

const THEMES = [T1, T2, T3, T4, T5];
const PITCHED = ['bass', 'counter', 'lead'];

// ---------- validation ----------

const errs = [];
for (const t of THEMES) {
  for (const v of PITCHED) {
    t[v].forEach((p, i) => {
      const sum = p.reduce((a, [, n]) => a + n, 0);
      if (sum !== 16) errs.push(`tema ${t.id} ${v}[${i}] soma ${sum} passos, deveria ser 16`);
      p.forEach(([hz, n], j) => {
        if (n < 1) errs.push(`tema ${t.id} ${v}[${i}][${j}] duracao ${n}`);
        if (hz !== 0 && (hz < 30 || hz > 1400))
          errs.push(`tema ${t.id} ${v}[${i}][${j}] ${hz} Hz fora de 30..1400`);
      });
    });
  }
  t.drum.forEach((p, i) => {
    if (p.length !== 16) errs.push(`tema ${t.id} drum[${i}] tem ${p.length} passos, deveria ter 16`);
    p.forEach((x, j) => {
      if (x < 0 || x > 100) errs.push(`tema ${t.id} drum[${i}][${j}] velocidade ${x}`);
    });
  });

  const bars = t.form.reduce((a, m) => a + m.bars, 0);
  t.bars = bars;
  if (bars < BARS_MIN)
    errs.push(`tema ${t.id} tem ${bars} bars (${(bars * 16 * STEP_MS) / 1000}s), abaixo de ${BARS_MIN} (180s)`);

  // every movement must name a cycle for every voice, or a voice silently
  // plays fewer bars than the others and the loop drifts apart
  for (const m of t.form)
    for (const v of [...PITCHED, 'drum']) {
      if (!Array.isArray(m[v]) || m[v].length === 0)
        errs.push(`tema ${t.id} movimento de ${m.bars} bars nao tem ciclo para ${v}`);
      else
        for (const pi of m[v])
          if (t[v][pi] === undefined) errs.push(`tema ${t.id} ${v} referencia padrao ${pi}, que nao existe`);
    }
}

if (errs.length) {
  console.error('PADROES INVALIDOS:\n' + errs.map((e) => '  ' + e).join('\n'));
  process.exit(1);
}

// ---------- order lists, with the movement level folded in ----------

// Each (pattern, movement level) pair becomes its own entry in the emitted
// table. The alternative was a parallel per-bar list of levels read alongside
// the order list, but Mar has no cheap way to index a list by position (no
// List.get, no map2), so that would mean a List.drop per bar: quadratic on
// every program load, to save a table that costs nothing because it is
// generated. A silent bar takes level 100 whatever movement it is in, so the
// tacet patterns dedupe down to one.
function voiceData(theme, voice) {
  const drums = voice === 'drum';
  const index = new Map();
  const patterns = [];
  const order = [];
  for (const mv of theme.form) {
    const cyc = mv[voice];
    for (let b = 0; b < mv.bars; b++) {
      const pi = cyc[b % cyc.length];
      const src = theme[voice][pi];
      const sounds = drums ? src.some((x) => x > 0) : src.some(([hz]) => hz > 0);
      const g = sounds ? mv.gain[voice] : 100;
      const k = pi + ':' + g;
      if (!index.has(k)) {
        index.set(k, patterns.length);
        patterns.push(drums
          ? src.map((x) => (x > 0 ? Math.max(1, Math.round((x * g) / 100)) : 0))
          : src.map(([hz, n]) => [hz, n, g]));
      }
      order.push(index.get(k));
    }
  }
  return { patterns, order };
}

for (const t of THEMES) {
  t.vd = {};
  for (const v of [...PITCHED, 'drum']) t.vd[v] = voiceData(t, v);
}

// ---------- emit ----------

function fmtPattern(p) {
  return '[ ' + p.map(([hz, n, g]) =>
    (hz === 0 || g === 100) ? `nt ${hz} ${n}` : `ntv ${hz} ${n} ${g}`).join(', ') + ' ]';
}

function fmtPatternFn(name, pats, fmt) {
  const lines = [`${name} : Int -> ${fmt === fmtPattern ? 'List Note' : 'List Int'}`, `${name} i =`];
  pats.forEach((p, i) => {
    if (i === 0) lines.push(`    if i == 0 then`);
    else if (i < pats.length - 1) lines.push(`    else if i == ${i} then`);
    else lines.push(`    else`);
    lines.push(`        ${fmt(p)}`);
    if (i < pats.length - 1) lines.push('');
  });
  return lines.join('\n');
}

const fmtDrum = (p) => '[ ' + p.join(', ') + ' ]';

function listLiteral(name, xs) {
  const rows = [];
  for (let i = 0; i < xs.length; i += 24) rows.push(xs.slice(i, i + 24).join(', '));
  return `${name} : List Int\n${name} =\n    [ ${rows.join('\n    , ')}\n    ]`;
}

const header = fs.readFileSync(new URL('./music-header.mar.in', import.meta.url), 'utf8');
const parts = [header];

const ROMAN = ['I', 'II', 'III', 'IV', 'V', 'VI', 'VII', 'VIII', 'IX', 'X'];

for (const t of THEMES) {
  const secs = (t.bars * 16 * STEP_MS) / 1000;
  const mm = Math.floor(secs / 60), ss = Math.round(secs % 60);
  const movements = t.form.map((m, i) => `${ROMAN[i]} ${m.bars}`).join(', ');
  const lo = Math.min(...t.form.map((m) => m.gain.lead));
  const hi = Math.max(...t.form.map((m) => m.gain.lead));
  parts.push(`
-- --- mission ${t.id}, ${t.title}: ${t.note}
-- ${t.bars} bars, ${mm}m${String(ss).padStart(2, '0')}s. Movements (in bars): ${movements}.
-- The lead runs from level ${lo} to ${hi} across them. Movement I is the piece as
-- it was written; everything after it develops that material. ---

${fmtPatternFn(`m${t.id}Bass`, t.vd.bass.patterns, fmtPattern)}

${fmtPatternFn(`m${t.id}Counter`, t.vd.counter.patterns, fmtPattern)}

${fmtPatternFn(`m${t.id}Lead`, t.vd.lead.patterns, fmtPattern)}

${fmtPatternFn(`m${t.id}Drum`, t.vd.drum.patterns, fmtDrum)}

${listLiteral(`m${t.id}BassOrder`, t.vd.bass.order)}

${listLiteral(`m${t.id}CounterOrder`, t.vd.counter.order)}

${listLiteral(`m${t.id}LeadOrder`, t.vd.lead.order)}

${listLiteral(`m${t.id}DrumOrder`, t.vd.drum.order)}
`);
}

parts.push(`
-- The theme for a mission, 1 to 5. Skirmish draws a number in this range once
-- when it starts and passes it here, so a skirmish sounds like one of the
-- campaign missions rather than needing a sixth piece of music.
missionTheme : Int -> Sound
missionTheme m =`);

THEMES.forEach((t, i) => {
  const cond = i === 0 ? `    if m == ${t.id} then`
    : i < THEMES.length - 1 ? `    else if m == ${t.id} then`
      : `    else`;
  const tail = i === THEMES.length - 1
    ? `        -- ${t.title}, and the fallback for any number outside 1..5,\n        -- which is what skirmish rides on when its draw lands here.`
    : `        -- ${t.title}`;
  parts.push(`${cond}
${tail}
        Sound.chord
            [ bassOf Sound.${t.wave} ${t.vol.bass} (arrange m${t.id}Bass m${t.id}BassOrder)
            , counterOf ${t.vol.counter} (arrange m${t.id}Counter m${t.id}CounterOrder)
            , leadOf ${t.vol.lead} (arrange m${t.id}Lead m${t.id}LeadOrder)
            , drumLine ${t.vol.drum} (arrange m${t.id}Drum m${t.id}DrumOrder)
            ]
`);
});

parts.push(`
-- how many themes there are to draw from, so the draw and the table cannot
-- disagree
skirmishThemeCount : Int
skirmishThemeCount = ${THEMES.length}
`);

fs.writeFileSync(process.argv[2], parts.join('\n').replace(/\n{4,}/g, '\n\n\n'));

console.log('gerado:', process.argv[2]);
for (const t of THEMES) {
  const secs = (t.bars * 16 * STEP_MS) / 1000;
  let notes = 0, hits = 0, maxHz = 0;
  const hzs = [];
  for (const v of PITCHED) {
    for (const i of t.vd[v].order) {
      for (const [hz] of t.vd[v].patterns[i]) {
        if (hz > 0) { notes++; hzs.push(hz); if (hz > maxHz) maxHz = hz; }
      }
    }
  }
  for (const i of t.vd.drum.order) for (const x of t.vd.drum.patterns[i]) if (x > 0) hits++;
  hzs.sort((a, b) => a - b);
  console.log(
    `  tema ${t.id} ${t.title.padEnd(16)} ${String(t.bars).padStart(3)} bars  ` +
    `${Math.floor(secs / 60)}m${String(Math.round(secs % 60)).padStart(2, '0')}s  ` +
    `${String(notes).padStart(4)} notas  ${String(hits).padStart(4)} batidas  ` +
    `${((notes + hits) / secs).toFixed(2)} ev/s  ` +
    `mediana ${String(hzs[Math.floor(hzs.length / 2)]).padStart(4)} Hz  ` +
    `teto ${String(maxHz).padStart(4)} Hz`);
}

// The themes are also the input to tools/harmony.mjs, which measures what the
// vertical intervals actually are. Exported rather than duplicated: an analyzer
// reading a copy of the notes is an analyzer that can pass while the music is
// wrong.
export { THEMES, PITCHED, STEP_MS };
