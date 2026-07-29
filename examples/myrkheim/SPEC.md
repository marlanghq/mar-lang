# MYRKHEIM

**A simple first-person shooter for the Mar language.** A Wolfenstein-3D-class
raycaster with a Norse theme: three burial barrows, draugr in the dark, a spear
that never misses, and the stolen Dawn Rune at the bottom. Flat-shaded walls,
billboard enemies, endless night as the fog.

This is the pseudo-3D lineage taken one step further: seasons-gp proved 1/z strip
projection at 60fps; Myrkheim turns the strips vertical and casts one ray per
column. Same Int fixed-point discipline, same zero-language-changes rule.

- Folder: `examples/myrkheim/`
- Topology: `App.frontend` (canvas game, no backend, no database)
- Dev port: **3028** (3025 seasons-gp, 3026 soundboard, 3027 pulse-runner) —
  add to the workspace `launch.json` at M0
- Target: ~15–20 minutes for the campaign (3 barrows + boss)
- Hard rule: **zero language changes.** Everything below is buildable with
  today's Mar (Canvas rect/group, Keyboard, Gamepad, onDrag, Sound, Time.every,
  Random/LCG). Anything that would need a new primitive is in "Out of scope".

---

## 1. Pillars

1. **A real FPS, honestly scoped.** Wolfenstein-class: grid world, one floor
   height, no looking up/down. Inside that box, everything a shooter needs:
   dread, telegraphs, dodging, keys, a boss.
2. **The dark is the renderer.** Flat-shaded walls + aggressive distance fog is
   not a limitation dressed up — it IS the art direction. An endless-night
   barrow where draugr eyes glow before their bodies resolve out of the murk.
3. **Comfort is non-negotiable.** No head-bob, no screen shake, no FOV pumping,
   ever (see §9.3). All impact reads through color and sound, never through
   camera motion.
4. **Runs everywhere.** Keyboard, gamepad and two-thumb touch from day one;
   60fps on an iPhone (the 1× canvas ADR-0001 makes this realistic).

## 2. Story

The jarl **SKALLI HALF-ROT** would not stay buried. He crawled out of his howe,
stole the **DAWN RUNE** from the village waystone, and dragged it down through
three barrows to the threshold of Hel. Without the rune, the sun cannot rise;
the fjord has been dark for forty days.

You are **VIGDIS**, last of the waystone's shieldmaidens. Odin lends you one
thing: a cast of **GUNGNIR'S BLESSING** on your spear — *a thrown spear that
never misses what it is aimed at, and always returns to the hand.* (This is the
fiction for hitscan with no ammo: aim true, throw, it comes back.)

Descend the three barrows. Take the rune back. Bring the dawn.

Ending: placing the rune on the final waystone floods the palette from
barrow-black to dawn-gold over a few seconds — the world changes color, the
camera never moves. Post-credits line: "The sun rose. The birds pretended
nothing had happened."

Tone: NES-terse, dry Norse humor in banners only ("BARROW II — colder", "the
spear returns. it always returns."). All UI text in English.

## 3. The game at a glance

| Thing | Count / value |
|---|---|
| Levels ("barrows") | 3, each a 24×24 grid, 4–6 min each |
| Enemy types | 2 (draugr, wraith) + 1 boss (Draugr Jarl) |
| Weapon | 1 — the returning spear (hitscan, no ammo, cooldown) |
| Player health | 6 pips shown as 3 hearts |
| Pickups | mead horn (+2 pips), barrow rune (key), gold ring (score) |
| Doors | plain (auto-open) and rune-locked (need that barrow's rune) |
| Modes | **Saga** (checkpoint at each barrow) / **Einherjar** (one life) |
| Native view | 320×200 logical, 160 columns of 2px, integer-letterboxed |
| Raycast | DDA, dir/plane camera, Int fixed-point ×1024 |
| Music | ambient drone (explore) + 120 BPM drum loop (combat) + jingles |

## 4. The raycaster (how 3D works in Int-only Mar)

The technical heart of the spec. Wolfenstein 3D shipped on a 286 with no FPU —
integer fixed-point raycasting is period-accurate, not a workaround.

### 4.1 Units and the camera

- One map cell = **1024 units**. Player position `(px, py)` in units.
- Heading is **quantized**: `heading : Int` in `0..239`, each step = 1.5°.
  The direction vector comes from a **pasted lookup table** of 240 pairs
  `(dirX, dirY)`, each scaled ×1024, generated once by `tools/gen_tables.py`
  (checked in, reproducible). One O(n) list lookup **per frame**, not per ray.
- Camera plane is derived, no second table:
  `plane = (-dirY * 683 / 1024, dirX * 683 / 1024)` → FOV ≈ 67°.
- Why a table instead of rotating the vector by per-tick sin/cos constants:
  repeated rounded rotations drift in magnitude; a quantized heading is
  drift-free forever and time-travel-friendly (heading is just an Int).

### 4.2 Casting (per frame)

For each of the **160 columns** `i`:

1. `camX = (2*i - 159)` scaled so ray = `dir + plane * camX` (fixed-point).
2. **DDA** through the 24×24 grid: `deltaDist` needs two Int divisions per ray
   (`~1024*1024 / |rayDir|`, guard rayDir=0 → clamp 1); then the classic
   side-compare loop. Products stay far below 2^53 (JS number-safe).
3. First solid cell hit → perpendicular distance `perpDist` (avoids fisheye),
   face side (N/S vs E/W), and cell type.
4. Strip: `h = 200 * 1024 / perpDist` clamped to view; draw ONE 2px-wide rect
   centered vertically, color = wall palette × face shade × fog band.

Ceiling = 1 dark rect; floor = 3 horizontal bands (darker toward the horizon).
Total per frame ≈ **164 wall/back rects + sprites + HUD** — the same order as
seasons-gp (64 strips + scenery + rivals) and mini-rpg (~250 tiles), both
proven at 60fps.

### 4.3 Sprites (enemies, pickups, bolts)

Billboards, exactly the seasons-gp rival pipeline turned indoors:

- Transform world → camera space (2 muls), project screen X and scale
  `size = 200 * 1024 / perpDist`.
- Depth test: keep the per-column wall distances (a 160-entry List built during
  the cast); a sprite draws only if closer than the wall at its **center
  column** (cheap; corner pop-in accepted — Wolf3D-era artifact).
- Draw far → near. Each enemy is 6–12 rects (blocky sprite, 2-frame walk).
  ≤ 6 sprites visible by level design.

### 4.4 Fog = the night

4 shade bands by `perpDist` (≈ 1.5 / 3 / 5.5 / 8.5 cells); beyond ~10 cells
everything is barrow-black. Fog color is the barrow's palette, so each level
reads differently at a glance. Draugr eyes (2 bright rects) ignore fog — you
see eyes before you see the draugr. That's the horror beat and it costs nothing.

### 4.5 Performance budget and the M0 gate

- Cast: 160 rays × ~6–14 DDA steps ≈ **≤ 2.2k loop iterations/frame** — the new
  hot loop, on top of a rect count we already know is fine.
- Estimated total interpreter load ≈ seasons-gp's frame (which also does
  per-strip scenery math + 8 rivals). Expected to hold 60fps desktop AND
  iPhone-at-1×, **but this is the project's one real risk**, so:
- **M0 is a gate**: empty barrow, move + turn, fps counter. If a MacBook or an
  iPhone can't hold 60fps, fallback ladder before any content is built:
  106 columns of 3px (Wolf3D low-detail used 8px!), then 80×4px. If even that
  fails, we stop and renegotiate the whole game.

## 5. World

### 5.1 The three barrows

| # | Name | Palette / fog | Twist |
|---|---|---|---|
| I | **HOWE OF ASH** | cold grey-blue, pale fog | tutorial pacing: doors, first draugr, first rune |
| II | **IRON DEEP** | rust + ember on black | wraiths introduced; forked layout, more locked doors |
| III | **HEL'S THRESHOLD** | sick green on black | dense ambushes; ends in the Jarl's arena + the Dawn Rune |

Each barrow: find its **rune** (key), open the rune door(s), reach the
**waystone** (exit). Gold rings and mead horns reward poking into side rooms.

### 5.2 Map format (authored by hand, one string per row)

24 strings of 24 chars per barrow, parsed at init via the Char/String stdlib:

```
########################
#P.....#....G...#..H...#
#..##..d..####..%..##..#
#..#R..#....#........E.#
####################D###
...
```

| Glyph | Meaning |
|---|---|
| `#` / `%` | stone wall / carved wall (alt tint, same collision) |
| `T` | torch wall (brighter face — cosmetic landmark for navigation) |
| `.` | floor |
| `d` / `D` | plain door / rune-locked door |
| `P` / `X` | player spawn / waystone (exit) |
| `R` / `H` / `G` | barrow rune / mead horn / gold ring |
| `E` / `W` / `J` | draugr / wraith / the Jarl (barrow III only) |

## 6. Core mechanics

### 6.1 Movement and collision

- Forward 60 units/tick (~3.5 cells/s), strafe 45, backpedal 45. Turn
  1 heading-step/tick held (= 90°/s, Wolf3D-ish).
- Collision: axis-separated move with wall slide (mini-rpg pattern), player
  radius 240 units. Sliding along walls, never sticking on corners.
- No sprint, no jump, no crouch. Barrows are for walking with intent.

### 6.2 The spear (combat)

- Fire = hitscan along the **center ray** (crosshair dot): nearest enemy whose
  screen column range covers the center AND is closer than the wall there takes
  1 damage. Gungnir fiction: no ammo, no reload.
- Cooldown 36 ticks (0.6s): the throw + return animation IS the cooldown,
  read on the weapon overlay (arm back → release → glowing return).
- Enemy hit feedback (diegetic): the enemy sprite flashes white 4 ticks and
  its eyes flare. Kill: the sprite collapses into a small bone-dust pile
  (a floor billboard that stays). The object changes; no particle confetti.

### 6.3 Damage to the player

- Draugr swing (melee, telegraphed) = 2 pips. Wraith bolt = 1 pip. Jarl = 2.
- Feedback: 4 thin **solid** red rects framing the view for 12 ticks (rgb has
  no alpha — border frame, not overlay) + low thud + drone dips for a beat.
  The camera does not move. Ever.
- 0 pips → Saga: "THE BARROW KEEPS YOU — CONTINUE?" restart current barrow,
  score keeps. Einherjar: "VALHALLA AWAITS" → title, run over.

### 6.4 Doors, keys, pickups

- **Plain door `d`**: opens on walk-contact (rumble sound). No interact button
  exists — contact is the interaction, on every input device for free.
- **Rune door `D`**: contact with rune in hand → unlocks (chime + the door
  face color-drains to fog over 18 ticks, then passable). Without the rune it
  thuds and the HUD rune slot blinks.
- Pickups on walk-contact: rune (jingle + HUD slot fills), mead horn (+2 pips,
  cap 6), gold ring (score + sparkle tick via LCG).

### 6.5 Determinism

LCG in the model drives wander jitter and sparkles; everything else is
input-driven. Time-travel in the dev dock scrubs the whole barrow reliably.

## 7. Bestiary

Telegraph doctrine: **every attack is announced ≥ 700ms (42 ticks) ahead**,
readable at fog distance.

| Enemy | HP | Behavior |
|---|---|---|
| **DRAUGR** (melee) | 3 | Dormant until line-of-sight ≤ 8 cells (LOS = one DDA between cells, reusing the caster). Shambles at ~1.3 cells/s, wall-slides. In reach: raises axe (42 ticks, eyes flare, rising groan) → swing. Loses you → guards last seen spot 5s, then sleeps. |
| **WRAITH** (ranged) | 2 | Hovers, keeps 3–6 cells, slow strafe. Telegraph: gathers a glow (42 ticks, rising whine) → throws a **bolt**: billboard projectile, 80 units/tick (~1.2s across its range), fully dodgeable by strafing. Max 2 bolts alive per wraith. |
| **DRAUGR JARL** (boss, barrow III) | 24 | Big billboard (1.5× draugr). Melee combo (2 pips) + at 16 and 8 HP lets out a horn-call that wakes 2 draugr from floor dust piles in the arena (recycles the death-pile visual as a summon — diegetic). Arena is a 7×7 hall with 4 pillars for cover. Health bar appears (like heartwood bosses). |

## 8. Screens and UI

| Screen | Content |
|---|---|
| **Title** | "MYRKHEIM" big pixel logo, slow fog drift behind (palette bands, no camera motion), "SAGA / EINHERJAR" select, mute toggle top-right (M / tap). |
| **Barrow intro** | Black card: "BARROW I — HOWE OF ASH" + one dry line, 2s. |
| **In game (HUD)** | Hearts top-left (pixText style, hand-rolled 3×5 font like mini-soccer), rune slot + gold top-right, crosshair dot center, weapon overlay bottom-right. |
| **Waystone (level clear)** | Banner: time, kills, gold for that barrow. Any key/tap → next intro. |
| **Victory** | Rune placed → palette floods to dawn-gold over ~3s, horns; totals; "the sun rose." |
| **Game over** | Per mode (§6.3). |

## 9. Art direction

### 9.1 Look

Untextured raycaster owned as a style: near-black world, 4-band fog, walls
tinted per barrow with N/S faces one shade lighter than E/W (the classic depth
cue), torch walls as warm landmarks, glowing eyes in the dark. Think
"Wolfenstein by way of a woodcut".

### 9.2 Weapon overlay

Spear held low-right, 3 frames (idle / thrown-empty-hand / return-glow), drawn
as rects in screen space. The overlay animates; the view never does.

### 9.3 Motion comfort — NON-NEGOTIABLE

- **No head-bob. No screen shake. No FOV changes. No camera roll.**
- Damage/impact = static color (border frame, palette dips) + sound only.
- Constant turn rate (no acceleration curves); constant move speed.
- Fog gives strong depth cues, which reduces vection discomfort.
These are hard requirements, not polish preferences.

## 10. Audio

All Sound module, all proven primitives (duty, vibrato, noise, sequence).

- **Explore bed**: `Sound.ambient` — 55Hz triangle drone + slow vibrato, per-
  barrow detune. Barrow III adds a faint dissonant second voice.
- **Combat loop**: `Sound.loop`, war drums + low pulse bass, **120 BPM** (the
  vsync-tick doctrine: beat = 500ms). Engages while any enemy is alerted,
  disengages 3s after combat ends (state-gated subscription, like seasons-gp's
  engine note).
- **SFX one-shots** (`Sound.play`, drained from the model like seasons-gp):
  spear throw (saw sweep down) / spear return (short sweep **up** — it comes
  back to you: direction matches motion) / hit thunk (noise burst) / draugr
  groan (slow-attack low saw) / wraith whine (rising sine = charging) / bolt
  (falling pitch as it approaches — toward you = descending) / door rumble /
  rune chime / gold tick / hurt thud / waystone bell / Jarl horn / dawn fanfare.
- Mute: `Sound.setMuted`, M key + speaker tap, like the other games.

## 11. Input

| Device | Bindings |
|---|---|
| **Keyboard** | W/↑ forward, S/↓ back, A/D strafe, ←/→ turn, Space = throw, M = mute. (Open question #2: A/D turn-vs-strafe default.) |
| **Gamepad** | One stick exposed (`Gamepad.onStick`): stick Y = forward/back, stick X = turn, A = throw, B = strafe modifier (hold: stick X strafes instead of turning), Start = start/pause. |
| **Touch** | The seasons-gp letterbox pattern: LEFT pad = move (drag: Y forward/back, X strafe), RIGHT pad = turn (horizontal drag) + **THROW button** above it. Pads live in the letterbox (side bezels landscape, bottom row portrait), never over the picture. First touch shows them; keyboard/pad use hides them. |

No pointer lock in Mar → no true mouse-look; desktop turning is keys (or drag).
That's authentically Wolfenstein anyway.

## 12. Architecture

### 12.1 Modules

```
Main.mar      -- page wiring, update, subscriptions, input plumbing
Cast.mar      -- DDA raycaster: castColumn, LOS check, projection helpers
World.mar     -- map parse (strings -> grid), cell queries, door state
Actors.mar    -- draugr/wraith/jarl step functions, bolts, telegraphs
Draw.mar      -- strips, sprites, weapon overlay, HUD, pixel font
Levels.mar    -- the 3 barrow maps + palettes + intro lines (data only)
Tables.mar    -- pasted dir table (240 pairs) + fog thresholds (data only)
Sfx.mar       -- Cue union -> Sound values, drone/drum builders
tools/gen_tables.py  -- regenerates Tables.mar's numbers (checked in)
```

### 12.2 Model sketch

```
{ mode, tick, seed, barrowIx
, px, py, heading            -- all Int (units / heading steps)
, hp, rune, gold, kills, timeT
, cool                       -- spear cooldown ticks
, doors  : List { cx, cy, openT, locked }
, actors : List { kind, x, y, hp, state, stateT, flashT }
, bolts  : List { x, y, dx, dy }
, dust   : List { x, y }     -- bone piles (dead draugr + Jarl summon pool)
, cols   : ()                -- per-frame depth list is LOCAL to view, not model
, sfx : List Cue, muted, usedTouch, usedKeys, pads...
}
```

Tick pipeline: `input → move+slide → doors → actors (LOS, telegraphs, moves)
→ bolts → combat resolve → pickups → cues → view casts and draws`.
The cast happens in `view` (pure, from model) — update never raycasts except
the single center-ray on throw and per-actor LOS.

### 12.3 Mar gotchas checklist (baked in from the other games)

- Top-level VALUES evaluate in file order — tables/levels are data-only
  modules, functions elsewhere (the VUnit trap).
- No min/max/abs/clamp/modBy — local imin/imax/iclamp/modI/absI.
- pixText helpers return `List Shape` — wrap lone rects in `[ ]` inside
  `List.concat`.
- `Canvas.rgb` has no alpha → "vignette" is 4 solid border rects.
- Beat-synced music = 120 BPM / 500ms per beat (vsync ticks).
- Canvas is 1× + pixelated (ADR-0001) — design reads at 320×200 exactly.
- Sound.loop/ambient gating by mode/state, one-shots drained from `model.sfx`.

## 13. Milestones (each playable, each a feedback round)

| M | Deliverable | Gate |
|---|---|---|
| **M0** | Raycaster spike: barrow I geometry, move/turn/collide, fog, fps counter. | **60fps on desktop AND an iPhone, or fallback ladder (§4.5) before continuing.** |
| **M1** | Doors, rune, pickups, waystone, HUD, barrow intro/clear screens. Barrow I walkable start-to-exit. | Feels like exploring, navigable without a map. |
| **M2** | Combat: spear, draugr + wraith with telegraphs, damage, death, dust piles, game over per mode. | A fight is readable and dodgeable at fog distance. |
| **M3** | Barrows II–III content, the Jarl + arena + summons, Saga/Einherjar, victory dawn. | Campaign completable ~15–20 min. |
| **M4** | Audio pass (drone/drums/SFX), touch pads, title polish, final balance. | Plays great on phone + desktop, shippable. |

## 14. Verification plan

- **Reference implementation first**: `tools/raycast_ref.py` — the same DDA in
  Python; golden-test a handful of (pos, heading, column) → (perpDist, side)
  tuples against the Mar implementation via a debug dump. Catches fixed-point
  mistakes without eyeballing.
- `mar check` after every stage; browser proof via Claude Preview on a spare
  port (never Marcio's 3028 session).
- M0 perf: on-screen fps counter + a scripted spin-in-place and corridor walk;
  numbers reported per device before content work starts.
- Determinism: fixed seed + scripted input replay reaches identical model
  (supports dev-dock time-travel too).
- Balance bots are NOT planned (unlike pulse-runner) — combat here is spatial,
  not rhythmic; hand-testing + telegraph timing constants suffice.

## 15. Out of scope (v1) and future ideas

- **Wall textures / sprites-from-images** — needs an image-blit Canvas
  primitive (framework change; would also unlock a Doom look).
- **True mouse-look** — needs a pointer-lock subscription (framework change,
  goes through the [mar-init-env-flags]-style discussion first).
- Doom-class geometry (variable heights, look up/down), minimap, save codes,
  multiple weapons/ammo economy, difficulty selector, secrets/push-walls,
  sliding-door partial-occlusion rendering (v1 doors color-drain instead).

## 16. Open questions (answer before M0)

1. **Name**: MYRKHEIM ok? (Alternatives: BARROW, DAWN RUNE, HALF-ROT.)
2. **Keyboard scheme default**: A/D = strafe with ←/→ = turn (modern), or
   A/D = turn (classic Wolf3D tank)? Both exist either way; which is default?
3. **Scope check**: 3 barrows + boss (~15–20 min) — or start with 1 barrow +
   boss (~7 min) and grow if it's fun?
4. **Violence flavor**: draugr crumble to bone-dust, no blood/gore — matches
   the family-friendly line of the other examples. Confirm.
5. **FPS comfort**: §9.3 bans all camera motion FX, but first-person turning
   itself can bother motion-sensitive players. Want an optional "comfort turn"
   (slower 60°/s) toggle on the title screen, or is 90°/s fine to start?
