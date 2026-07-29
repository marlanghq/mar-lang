# mini-soccer — Super Striker

A tiny **8-bit** arcade football game, in the spirit of the Sega Master System
classics (*Great Soccer*, *World Cup*). Top-down single-screen pitch, both goals
in view, you against the CPU. Everything — the chunky sprites, the scoreboard
font, the dotted centre circle — is pixel-blocky, drawn from plain rectangles.

You control **one** blue player at a time. Control follows the ball when a
team-mate wins it, and you switch to the nearest defender yourself with
**Shift** — the game never yanks a player out of your hands. Everyone else runs
itself. That, plus a single context button, is the whole Master-System feel: no
menus, no combos, just run and kick.

## Controls

Play with the **keyboard**, a **gamepad**, or the **on-screen touch joystick** —
all three are live at once.

**Keyboard** — **WASD**/**Arrows** move (the amber arrow marks who you steer),
**L** shoots when you have the ball and **steals the ball** when you don't,
**K** is the sliding tackle ("carrinho"), **J** cycles control through the blue
outfielders (nearest, then next-nearest). The action keys sit on the right
hand's home row to pair with WASD. **Space** (or **L**) starts a match from the
title and returns there at full time. **M** (or the speaker icon in the top-right
corner) mutes and un-mutes all sound.

**Gamepad** — left stick or d-pad move, **A** shoot/tackle, **B**/**X** slide,
**Y** or a bumper switches player. Any button starts from the title.

**Touch** (or mouse) — a floating joystick on the **left** half of the screen
(press and drag to steer) plus the **A** / **X** / **SW** buttons bottom-right.

Attack the **right** goal; defend the **left** one. A match is two 45-second
halves, and the side that just conceded takes the kickoff — like real football.
The teams walk on from the sides before each half, and after a goal they trot
back into position before the ball is set again.

## Run it

```sh
mar dev examples/mini-soccer
```

Then open the printed URL. (This example pins its dev port to `3021` in
`mar.json` so it doesn't collide with the default `3000`.)

## How it's built

One file, [Main.mar](Main.mar). The gameplay, art, and animation use only the
primitives the [mini-rpg](../mini-rpg) already had; the **input** layer added two
small, general Mar primitives (used here, reusable by any game):

- **`Gamepad`** — a controller module mirroring `Keyboard`:
  `Gamepad.onButtonDown` / `onButtonUp : (Gamepad.Button -> msg) -> Sub msg` and
  `Gamepad.onStick : (Int -> Int -> msg) -> Sub msg` (the JS runtime polls the
  Gamepad API each frame and diffs it). Web-first, like `Keyboard` / `Canvas`.
- **`Canvas.onDrag`** — pointer-move on the canvas (the companion to `onTap` /
  `onRelease`), so the on-screen joystick can steer as your finger slides.

The rest:

- a `Time.every` tick drives the whole simulation (movement, ball physics, AI)
  at a smooth **60fps** — the calm pace comes from small per-tick step sizes, not
  from a low frame rate,
- `Keyboard`, `Gamepad`, and an on-screen joystick all fold into the same `held`
  state + actions, so every input method drives one code path,
- the entire 8-bit look is drawn from **one** primitive, `Canvas` `rect`: the
  sprites are outlined blocks shaded with a lit edge + a shadow edge (short
  sleeves, white socks, boots), the HUD and titles use a hand-rolled 3×5 **pixel
  font** (see the `glyph` table), and the centre circle is a ring of dots — no
  smooth `circle`, no system font.
- the players are **animated** from those same rects: a two-frame run cycle
  (legs and arms swing out of phase, the body bobs 1px) plays while a player
  moves and freezes to a stance when it stops, the ball's spot orbits so a loose
  ball reads as rolling, and a **sliding-tackle** pose lays the player out with a
  lunging boot and a dust trail — all driven by one `animTick` counter.
- everything (players + ball) is drawn back-to-front by its `y`, so overlaps
  layer correctly and a ball dribbled upfield tucks behind its carrier.
- the match is a little state machine (`Title → Warmup → Playing → Break → …`):
  the teams **walk on** from the sides into formation before each half, a goal
  freezes play for a `GOAL!` banner and then the teams **trot back** before the
  ball is set again — all with the same movement + animation code.
- **sound** comes from the new `Sound` module (chiptune, no audio files): the
  short one-shots (kick, whistle, slide, a rising start flourish) are
  `Sound.play` commands. Play itself is otherwise quiet - the crowd only shows up
  as part of a **goal celebration**: scoring fires a big layered "GOAAAL" fanfare
  and brings up a `Sound.ambient` roar bed for the length of the `GOAL!` break (with
  confetti and a pulsing banner), then it all stops. A speaker button (top-right,
  or **M**) mutes everything. See the `Sound` section below.

Everything is `Int` (Mar has no `Float`): the ball uses integer velocity with
per-tick friction, distances are compared squared to avoid square roots, and the
small helpers at the bottom of the file (`absI`, `iclamp`, `isign`, `d2`, …)
stand in for the `Basics` functions Mar doesn't ship. The 640×400 pitch is
scaled into the canvas by a **whole number** (`Canvas.Scale`) so pixels never
blur.

One thing worth knowing if you read the source: Mar evaluates top-level **value**
bindings eagerly, in file order, so a zero-argument binding can only name things
defined above it. That's why `mk` sits above the roster it builds, and why the
static pitch shapes are built inside the `fieldShapes` **function** (functions
capture their environment lazily, so order doesn't matter for them).

## Sound

Audio uses the `Sound` module, a small retro-chip synth (no audio files; the JS
runtime drives WebAudio oscillators). A sound is built from plain `Int`s —
frequency in Hz, length in ms, volume 0–100 — over a `Sound.Wave`
(`Square` / `Triangle` / `Sawtooth` / `Noise`):

```mar
kickSfx = Sound.volume 70 (Sound.tone Sound.Square 180 70)
```

`Sound.sweep` glides the pitch (for the whistle and the goal siren), `Sound.chord`
stacks voices, and `Sound.sequence` plays them one after another. There are two
ways to make noise, matching Mar's two effect kinds:

- **one-shots are `Cmd`s** — `Sound.play : Sound -> Cmd msg`, fired from `update`
  exactly like `Random.generate`. The game keeps a tiny `sfx : List Cue` on the
  model (a plain enum, so the time-travel debugger can still serialize the
  model), and one `emit` helper drains it into `Cmd.batch` each update.
- **the crowd is a `Sub`** — `Sound.ambient : Sound -> Sub msg`, returned from
  `subscriptions`. It plays a *steady ambient bed* (one continuous voice held at a
  level, fading in and out) for as long as it's in the subscription set. Here
  `musicSub` returns it **only** during the `GOAL!` break, so the roar swells in
  with the celebration and stops when play resumes - the rest of the match is
  quiet, on purpose. The runtime starts, cross-fades, and stops the bed for you
  (the same reconciliation `Time.every` uses). There's no menu tune either:
  browsers block audio until you interact, so a title track never started cleanly.

Mute is just game state: a `muted : Bool` on the model (toggled by the speaker
button or **M**, and carried across matches). `emit` and `musicSub` produce no
sound while it's set - the Elm-idiomatic way, no global audio switch. For a
scripted/headless override you can still call `window.marSoundMute(true)` from the
console (or set `localStorage.marMuted = "1"` before loading). Audio can't be
heard in a throttled headless preview, so it's best checked in a real browser tab.

`Sound` is web-first, like `Canvas`, `Keyboard`, and `Gamepad`: the JS runtime
synthesizes it, and the iOS renderer is deferred for now.
