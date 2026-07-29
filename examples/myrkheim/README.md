# Myrkheim

A Wolfenstein-3D-class first-person shooter written in Mar with **zero
language changes** — a DDA raycaster in Int fixed-point, flat-shaded walls,
distance fog, billboard enemies, gore, doors, keys, a boss, and a returning
spear that never misses. Full design in [SPEC.md](SPEC.md).

```
mar dev examples/myrkheim     # port 3028
```

## The game

The draugr jarl **Skalli Half-Rot** stole the **Dawn Rune**; the sun cannot
rise. You are **Vigdis**, last shieldmaiden of the waystone, armed with a
spear under Gungnir's blessing — it never misses what it is aimed at and
always returns to your hand. Descend the three barrows (Howe of Ash, Iron
Deep, Hel's Threshold), take the rune back from the Jarl, and bring the dawn.

- **Saga** — die and you restart the current barrow (score rolls back).
- **Einherjar** — one life. Valhalla.

Find each barrow's **rune** to open its rune-locked doors; reach the
**waystone** to descend. Mead horns heal, gold rings are for bragging.
Draugr telegraph their swing (~700 ms); wraith bolts are dodgeable by
strafing; the Jarl calls fallen bones back to their feet — including the
piles you made (and the remains your flasks leave behind).

## The bestiary

| Foe | Look | How it kills you | How you kill it |
|---|---|---|---|
| **Draugr** | walking dead, blue eyes | telegraphed axe swing | anything, 3 hits |
| **Wraith** | floating, violet eyes | ranged bolt, keeps distance | close it down, 2 hits |
| **Varg** | LOW, wide, amber eyes | fast lunge bite — and it HOWLS the pack awake | quick, but thin: 2 hits |
| **Skjaldmaer** | painted roundshield | slow advance, heavy swing | her shield BLOCKS while she walks — bait the swing, punish the opening (fire ignores shields) |
| **Volva** | hooded, green eyes, floats | blinks across the room, throws a TWIN bolt fan | catch her right after she reappears, 3 hits |
| **Jarl Skalli** | huge, amber eyes | heavy reach + raises the dead | the campaign |

## The arsenal

| # | Weapon | Ammo | How you get it |
|---|---|---|---|
| 1 | **Seax** | infinite (melee) | you start with it — short reach, quick swings |
| 2 | **Gungnir spear** | infinite (hitscan) | HIDDEN in barrow I — a walled-up armory; find the crack |
| 3 | **Hunting bow** | arrows | HIDDEN in barrow II — the bowyer was bricked in with his work |
| 4 | **Hewing axe** | axes | first axe pickup unlocks the slot (3 damage) |
| 5 | **Surtr-fire flask** | flasks | first flask unlocks it; explodes near YOU too |

Classic FPS progression: you start with the knife and EARN the rest —
the good weapons are behind cracked walls, and nothing stops you from
walking past them.
Ammo comes from pickups, draugr drops and secret caches. **Armor** absorbs
one pip of every hit while it lasts: a leather cuirass is +4, the iron
byrnie +8 (Doom rules, Norse wardrobe).

**Secrets**: some walls are cracked. Anything hard thrown at a crack
shatters it; the caches behind hold the good loot. The tally shows at each
waystone.

## Controls

- **Keyboard** — W/S move, A/D strafe, ←/→ turn, Space = throw,
  1-5 = weapon, Q = cycle, M = mute
- **Mouse** — click-drag on the view to turn
- **Gamepad** — stick Y move, stick X turn, A = throw, B = cycle weapon
- **Touch** — left pad moves, right pad turns, rune button throws, the
  small button above it cycles weapons; pads live in the letterbox

## How it works (the short version)

- **Raycaster**: 160 rays per frame (2px columns of a 320x200 view), classic
  DDA over a 24x24 grid, everything x1024 fixed point (one cell = 1024
  units). Wall strips are flat-shaded by face side and 4 fog bands; beyond
  ~10 cells the world is barrow-black — the dark is the art direction, and
  draugr eyes glow through it.
- **No trig at runtime**: the heading is quantized to 240 steps of 1.5° and
  the direction vector comes from a pasted table (`tools/gen_tables.py`),
  which also makes rotation drift-free. The camera plane is derived from it.
- **Clean stone**: walls are deliberately flat — face-side shade + fog
  bands only, so torches and cracked walls pop as landmarks. (A busier
  mortar-and-mottle look was tried and retired: at 2px columns it read
  as noise.)
- **Minimap**: the whole barrow at 2px per cell, top-right. Wall rows are
  pre-baked into horizontal runs (recomputed only when a door opens or a
  wall shatters), so it costs ~100 rects. Doors show in their own colors;
  once you hold the rune, the waystone glints gold — the rune knows the
  way home.
- **Sprites**: enemies, pickups, bolts, gore and piles are billboards
  projected through the camera matrix, sorted far-to-near, depth-tested
  against the wall distance at their screen column, and hand-clipped to the
  view (the scaled canvas group does not clip).
- **Maps**: authored programmatically and validated in `tools/gen_tables.py`
  (sealed border, rune reachable before its door, waystone gated, glyph
  counts exact), then pasted into `Main.mar` as plain strings.
- **Determinism**: one LCG in the model; the dev-dock time travel scrubs a
  whole barrow reliably.
- **Sound**: a held drone (`Sound.ambient`, retuned live — it leans sharper
  while anything hunts you), war drums at 120 BPM (`Sound.loop`, engaged by
  combat), and one-shot cues drained from the model like the other games.

Everything runs on today's Mar primitives: Canvas rects, Keyboard, Gamepad,
onDrag, Sound, Time.every, Dict, Random-style LCG.
