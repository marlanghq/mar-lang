# Iron Meridian

A complete real-time strategy game written in Mar. Command and Conquer
economy, StarCraft style faction asymmetry, a five mission campaign with
a story, and a skirmish mode where all three factions are playable.

```
mar dev examples/iron-meridian
```

Desktop-first: play it with a mouse and keyboard on a big screen.
Press H in a mission for the full field manual.

## The game

Mars, 2231. Earth has been silent for eleven years, terraforming is half
done, and something old under the equatorial ridge is keeping the air
breathable. Three factions fight over what is left:

- **The Compact** (steel blue): the colonial administration's military.
  Balanced units, repairable armor, artillery superweapon.
- **The Scrapline Combine** (rust orange): seceded miner guilds. Cheap,
  fast, fragile units; wrecked vehicles leave scrap its Reclaimers eat
  for bonus credits; EMP superweapon; suicide Ram Trucks.
- **The Vessari** (violet and teal): the Engine's own custodians. Few and
  expensive, every unit carries a regenerating shield, buildings must
  grow inside a Spire lattice, purge beam superweapon.

Campaign (five missions, played as the Compact): establish a base under
raider pressure, race for derelict silos across a canyon belt, survive
the first Vessari warp surges, fight beside your old enemy to relieve a
besieged crawler city, then kill the Prime Conduit ring before the
Engine sterilizes the settled belt. Skirmish: pick any faction, any
opponent, three difficulty levels, on the Rust Basin map.

## How to play

- Left click selects. Click the ground to move, an enemy to attack, ore
  with a worker selected to harvest, a derelict with your engineer
  selected to capture it. Drag for a box select.
- The sidebar is the Red Alert economy: structures build in the queue,
  then you click the map to place them (they must touch your base).
  Power shortages slow production and kill the radar.
- A + click is attack-move. S stops, G holds. Shift+digit stores a
  control group, the digit recalls it. Q grabs the army on screen.
- B and T arm the build tabs with per-cell hotkey letters. V fires the
  superweapon once its building is up and charged.
- Arrows scroll, Space jumps to the last alert, Minus and Equal change
  the game speed, P pauses, M mutes, Esc opens the field menu.
- Backquote toggles the autopilot (the bot plays your side; useful to
  watch a battle or to learn a mission's pacing).

## For Mar readers

The interesting bits, module by module:

- `Sim.mar` is the whole 30 Hz simulation: C&C build queues, harvest
  cycles, BFS flow-field movement, staggered combat scans, projectiles
  and beams, capture channels, EMP stuns, scheduled superweapon strikes
  and incremental fog of war. Deterministic (LCG in the model).
- `Maps.mar` parses textured ASCII-style grids, pre-merges terrain into
  per-row draw runs, builds shared BFS distance fields and keeps fog
  draw runs cached per row.
- `Bot.mar` is a macro AI: keeps its economy alive, follows a per-race
  base plan, leans its army composition, telegraphs attack waves and
  pulls home to defend. Difficulty is a pair of knobs (aggro, first
  wave delay).
- `Draw.mar` renders everything procedurally: 24 unit sprites and 33
  structures from rects, circles and triangles, with team paint, damage
  smoke, shield shimmer and additive glow (Canvas.Blend Add).
- `Sfx.mar` is chip audio only: per-faction acknowledgement blips,
  weapon families, klaxons and stings. Missions run zero music by
  design.
- `Missions.mar` + `Story.mar` hold the campaign: stamped maps, starting
  bases, per-mission sides, briefings, radio lines and epilogues.
  Mission triggers (timed raids, warp surges, purge beams, hold timers)
  live in `Main.mar` next to the input layer.

Roughly ten thousand lines of Mar, no engine underneath: the tree-walking
interpreter drives the whole thing.
