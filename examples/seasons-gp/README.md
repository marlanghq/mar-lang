# Seasons GP

A pseudo-3D endless racer in the tradition of early-80s endurance racers,
drawn somewhere between the NES and the SNES. Written in Mar with **zero
language changes** — the same primitives the other canvas games use.

```
mar dev examples/seasons-gp
```

## The game

An F1-style gantry counts you in — three red lights, lights out, go. You race
forever. Each **season** is a timed stage: reach the checkered flags before
the clock runs out, or the race is over. Four seasons make a year, and each
new year is a little harder — faster rivals, denser traffic, sharper curves,
a tighter clock. Even the mountain range on the horizon is reshuffled each
year.

| Season | Twist |
| --- | --- |
| SPRING | gentle warm-up, snow caps still on the peaks |
| SUMMER | rush hour — much more traffic |
| AUTUMN | night — you only see taillights, cabin lights in the hills |
| WINTER | snow — sluggish wheel, the curve flings the car |

Curves, traffic lanes, rival speeds, scenery and gap timing are randomized
(an LCG lives in the model, so the dev-dock time travel stays deterministic).
Hitting a car kills your speed — no lives, just lost seconds. Riding the
grass grinds you down to ~70 km/h.

## Controls

- **Keyboard** — arrows/WASD steer · Space or Up = gas · Down = brake · M = mute
- **Gamepad** — stick or d-pad steers · A = gas · B = brake · Start = start
- **Touch** — two-thumb pads appear after your first touch, placed in the
  **letterbox around the picture** (side bezels in landscape, a row below the
  picture in portrait — never over the game): drag **STEER** to turn and hold
  **GAS** / **BRAKE**. Steering is independent of the throttle, so you can
  turn while coasting or braking into a corner. Tap the top-right speaker to
  mute.

## How it works

- **Road**: 64 horizontal strips with true 1/z perspective, all Int
  fixed-point. A flat road projects to straight edges, so strip half-width
  shrinks linearly; the hyperbolic part lives in each strip's world distance
  (`zOf i = zNear * N / (N - i)`), which drives the crawling rumble bands,
  the lane dashes, the roadside scenery and sprite projection.
- **Backdrop**: a four-band gradient sky, two parallax mountain ranges (far
  ridge at quarter speed, peaks at half), seasonal palettes, snow caps in
  spring, lit cabins at night. Everything at the edges renders through a
  clipping helper so nothing spills onto the letterbox.
- **Roadside**: world-spaced trees and bushes, hashed per slot so the mix
  varies; foliage follows the season.
- **Engine sound**: one continuously-held `Sound.ambient` triangle that the
  runtime retunes in place — the ambient key ignores frequency, so a new pitch
  each frame glides the same oscillator (no clicks, no crossfade). The pitch
  simulates a 2-speed gearbox: it climbs within a gear and dips at the single
  shift. On the grid, before lights-out, you can blip the throttle to rev in
  neutral. SFX are one-shot `Sound.play` cues drained from the model, and the
  mute button is `Sound.setMuted`.
- **Night**: distant rivals render as two red taillights only; the player's
  headlights paint a lighter wedge on the 12 nearest strips.
- **Winter**: steering eases in at 1/14 per tick instead of 1/3, and the
  centrifugal fling is stronger; snowflakes drift down deterministically.
