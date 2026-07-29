# Pulse Runner

A brutal one-button rhythm runner in the tradition of the early-2010s
"impossible" auto-runners: an orange square races right at constant speed
over spikes, blocks and pits, and **every obstacle sits on the beat grid of
the soundtrack**. Written in Mar with **zero language changes**.

```
mar dev examples/pulse-runner
```

## The game

One button. The square runs by itself; you only decide *when to jump* (hold
to keep hopping). Touch a spike or a block's side, or fall into a pit, and
the square shatters. The level is 128 beats long — about a minute — and gets
meaner every 32 beats: singles, then blocks you must mount, then pits, stairs
and beat-chains, then a finale that mixes everything.

| Mode | Death sends you… |
| --- | --- |
| HARDCORE | back to the very start |
| CHECKPOINTS | back to the last flag you crossed |

## Music sync

**The world plays the melody.** The groove (kick, offbeat hats, a round
triangle-sub bass) is a seamless 120 BPM `Sound.loop`; the high lead is not
on that tape at all — the game loop fires it as one-shot notes the tick the
player reaches each jump position (a pickup sixteenth half a beat before,
the melody note on the jump itself, walking the A-minor pentatonic). Flat
road, no melody; obstacle section, the melody rises with you — and no
audio-clock drift can pull the notes away from the moment you must press.
The grid is structural:

- Game ticks ride `requestAnimationFrame` (vsync-locked, ~16.7 ms at 60 Hz),
  so 30 ticks = **~500 ms = one beat at 120 BPM**. The square moves
  2 px/tick, so a beat is exactly **60 px** and a sixteenth note is **15 px**.
- Every obstacle is placed **from the beat you jump on**: a single spike for
  jump-beat B sits at sixteenth 4B+2, so the takeoff window is *centered on
  the kick*; pits at 4B+1; platform mounts at 4B+3; the four doubles at 4B+1
  (jump a hair early — the hard moments).
- A full hop lasts ~29 ticks ≈ **one beat**, with a quarter-turn spin in the
  air; holding jump hops on the kick.
- Checkpoints and segment changes sit exactly on **loop boundaries** (beats
  32/64/96): crossing a boundary swaps in that segment's track, and the
  music restarts whenever you spawn — always back on beat, in both modes.

The level was verified completable by an offline search over the exact same
integer physics — including by a bot that is only allowed to jump **exactly
on beat ticks**: jumping on the kick clears the whole level.

## Controls

- **Keyboard** — Space / Up / W = jump (hold to keep hopping) · M = mute
- **Gamepad** — A = jump · Start = start
- **Touch** — tap and hold anywhere = jump; tap the top-right speaker to mute
