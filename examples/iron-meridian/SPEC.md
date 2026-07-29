# IRON MERIDIAN

A single-player real-time strategy game for Mar. Desktop-first: big screen,
mouse plus keyboard, hotkeys on everything. Inspired by the economy and
sidebar flow of Command and Conquer: Red Alert and by the control model and
race asymmetry of StarCraft 2.

Frontend-only (`App.frontend`), canvas Pixelated, SFX only during gameplay
(no music in missions; a quiet ambient hum is allowed on menu and briefing
screens only).

---

## 1. Setting and story

**Mars, 2231.** Terraforming is half done: the lowlands hold thin breathable
air, the highlands are still red death. Eleven years ago Earth went silent
mid-transmission. The colonists call it the Quiet. Nobody knows why.

Beneath the equatorial ridge sits the **Meridian Engine**, a buried machine
older than the colony, discovered during the first excavations and quietly
declared a geological anomaly. It regulates the new atmosphere. Almost
nobody knows that.

### Factions

- **THE COMPACT** (steel blue). The remnant colonial administration and its
  military arm, the Wardens. Doctrine: combined arms, repairable armor,
  disciplined defense. Boxy hulls, floodlights, antenna masts.
- **THE SCRAPLINE COMBINE** (rust orange). Miner guilds and free haulers who
  seceded during the Quiet. Doctrine: numbers, speed, salvage. Welded
  plates, mismatched paint, smoke.
- **THE VESSARI** (teal and violet). Not invaders: custodians grown by the
  Engine itself, awakened when terraforming crossed a threshold. Doctrine:
  few, expensive, shielded. Smooth shells, glowing seams, crystal.

### Cast

- **Warden-Commander Rea Sol** (player). Field commander, pragmatic.
- **Chief Engineer Oko Adeyemi**. Runs your base, warm, curious, first to
  notice the anomalies.
- **Marshal Dane Voss**. Compact high command. Hardliner. Wrong about
  everything that matters.
- **Vara Kesk**. Combine warlord. Antagonist in act one, ally by act three.
- **The Chorus**. The voice of the Vessari. Speaks in plural.

### Campaign arc (5 missions)

1. **Dust and Iron.** A Compact supply convoy is ambushed in the Tharsis
   scrapfields. Establish a forward base, restore power, survive Combine
   raids, then destroy the raider camp. Closing beat: Oko pulls a humming
   shard from the wreckage. It is nobody's alloy.
2. **The Silo Gambit.** Race the Combine for derelict extraction silos
   across a canyon belt. Capture and hold three of five silos, survive the
   counterattacks, then break the Combine forward base. Mid-mission a
   sinkhole swallows a silo and exposes glowing conduit veins running under
   the whole canyon. Voss, on the radio: "Mars is dead rock. Dead rock does
   not hum."
3. **Static.** A listening post goes dark. Rescue the survivors and hold
   the site against Vessari warp surges that arrive on the Engine's pulse,
   then push out and destroy the lattice nexus driving the surges. First
   contact with shields; the mission teaches focus fire and anti-shield
   tools. The Chorus speaks once: "You woke the clock. We keep the clock."
4. **Scrap Truce.** Kesk's crawler city is under Vessari siege. Voss
   forbids intervention. Rea goes anyway. Fight alongside an allied Combine
   AI: relieve the siege, then a joint assault on two Vessari bastions.
   Kesk tells the truth: the Combine seceded because they found the Engine
   first, and Earth's last order was to bury it.
5. **Meridian Engine.** The Engine begins a purge cycle that will sterilize
   the settled belt. Final assault into the crater: a Vessari prime nexus
   and a rogue Combine warband that wants to blow the Engine outright.
   Destroy the Prime Conduit ring, not the Engine, while dodging telegraphed
   purge beams. Victory forces the Chorus to parley. Mars keeps breathing.
   Voss is relieved of command. Epilogue.

Campaign is played as the Compact. Skirmish mode (on the title screen) makes
all three factions playable against a bot of any faction.

---

## 2. Economy (Red Alert model)

- **Ferrite fields**: orange crystal patches on the map. Harvesters carry
  loads to a refinery. Fields deplete slowly, forcing expansion. Rich
  **azurite** patches are worth double.
- **Derelict extractors**: neutral map structures. A capture unit converts
  one to your side for a steady credit trickle. Missions and skirmish maps
  place 3 to 6 of them; they are the map-control objective.
- **Power**: Compact and Combine build power plants; production slows to
  60 percent and radar (minimap detail) shuts off when under-powered.
  Vessari spires project a **lattice** instead: their buildings must sit
  inside lattice radius, and spires double as supply.
- **Supply**: per-side cap raised by supply structures, up to 60.
- **Salvage** (Combine flavor): destroyed vehicles leave scrap piles.
  Combine Reclaimers collect scrap as bonus credits. Other factions ignore
  scrap.
- **Construction**: C&C sidebar. Structures build in the sidebar queue,
  then are placed as a ready building (must touch your base footprint;
  Vessari also need lattice). One structure at a time per tab, units queue
  per production building with a shared per-tab queue readout.

## 3. Combat model

- Damage types vs armor classes, integer percent multipliers:

  | type \ armor | Infantry | Vehicle | Building | Air |
  |--------------|----------|---------|----------|-----|
  | Light (rifles)   | 100 | 40  | 25  | 60  |
  | Piercing (rockets)| 50 | 120 | 60  | 110 |
  | Blast (cannon/arty)| 70 | 90  | 130 | 0   |
  | Beam (vessari)   | 90  | 90  | 90  | 90  |

- Vessari units carry a regenerating **shield** layer over hull HP; shields
  regen out of combat and take full damage from all types (beams excepted:
  beams are flat). Anti-shield specialist deals bonus shield damage.
- Artillery outranges static defense. Static defense beats unsupported
  pushes. Air ignores terrain but dies to dedicated AA. Capture units win
  games and die to a stiff breeze.
- Superweapon per faction, long cooldown, telegraphed to the enemy.

## 4. Faction rosters

Costs in credits, supply in parentheses. Numbers are the starting draft and
get tuned during playtests.

### Compact (balanced)

Structures: Command Bastion (HQ), Power Station 300, Refinery 900 (includes
Hauler), Billet 250 (supply 8), Foundry 400 (infantry), Motor Pool 900
(vehicles), Airfield 800 (air), Watchtower 350 (anti-ground), Flak Tower 400
(anti-air), Armory 700 (tier 2 unlock), Artillery Beacon 1200 (superweapon:
area barrage).

Units: Hauler 600 (2), Sapper 350 (1, captures and repairs), Rifle Squad
180 (1, Light), Rocket Squad 260 (1, Piercing), Scout Car 250 (1, fast,
big sight), Bulwark Tank 700 (3, Blast), Thumper Artillery 850 (3, Blast,
siege range), Kestrel Gunship 900 (3, air, Piercing).

### Scrapline Combine (cheap swarm, salvage)

Structures: Crawler HQ, Junk Turbine 220, Smeltery 800 (includes Reclaimer),
Bunkroom 200 (supply 8), Rat Den 300, Chop Shop 700, Roost 650, Spike Nest
250 (anti-ground), Flak Rack 300 (anti-air), Warlord Hall 600 (tier 2),
Scrap Magnet 1000 (superweapon: EMP burst, disables vehicles and towers).

Units: Reclaimer 500 (2, also collects scrap), Hijacker 250 (1, captures),
Scrapper Pack 120 (1, Light), Spike Thrower 190 (1, Piercing), Dune Bike
180 (1, very fast, Light), Scorcher 380 (2, flame, Light area), Warthog
Halftrack 480 (2, Blast), Vulture Copter 600 (2, air, Light), Ram Truck
400 (2, suicide Blast vs buildings).

Units cost roughly 65 percent of Compact equivalents, move faster, die
faster.

### Vessari (elite, shields, lattice)

Structures: Genesis Font (HQ), Spire 350 (lattice + supply 8), Extract
Bloom 1000 (includes Tithe Drone), Chrysalis 450 (constructs), Forge Womb
1100 (heavy), Sky Cradle 950 (air), Pylon Thorn 450 (defense, anti-ground
and air), Aegis Node 600 (aura: nearby shields regen in combat), Ascendant
Gate 900 (tier 2), Purge Lens 1400 (superweapon: line beam).

Units: Tithe Drone 700 (2), Attuner 450 (1, captures), Lancer Construct
320 (1, Beam), Nullifier 420 (1, anti-air plus bonus vs shields), Sliver
Skiff 300 (1, very fast harass, Light), Warden Colossus 1300 (4, Beam,
heavy), Rift Skiff 1100 (3, air, Beam).

Roughly 160 percent cost, shields on everything, slow to build.

## 5. Controls (desktop)

Platform facts (verified in the runtime): the canvas delivers onTap
(pointer down), onDrag (move while pressed) and onRelease (pointer up),
plus a desktop-input trio that is null on touch: onHover (unpressed cursor
move), onAltTap (right-click / two-finger tap) and onWheel (scroll delta).
All canvas-local. Keyboard.onKeyDown/onKeyUp deliver physical keys including
ShiftLeft/ShiftRight, Escape and digits; only Space and arrows are
preventDefaulted, so F-keys stay off the bindings (F1 would open browser
help). Held modifiers are released on window blur (the runtime synthesizes
the missing keyup), so Shift never sticks after focus loss.

Mouse (one-button model, classic C&C1):
- Press and release in place: contextual click. On own unit: select. On
  ground with selection: move. On enemy: attack. On ore with harvesters:
  harvest. On capturable with a capture unit: capture. On own production
  building: select it (then a ground click sets its rally).
- Press and sweep: selection box (owned units inside select on release).
- Right-click (onAltTap): cancels the armed action (placement / attack-move),
  otherwise clears the selection. Never issues an order, so it is unambiguous.
- Wheel (onWheel): scrolls the camera vertically.
- Minimap: click or drag moves the camera; with a selection, a click on
  the minimap orders an attack-move there.
- Placement mode (after a structure finishes in the sidebar): ghost
  follows the pointer via onDrag after press; click places, Esc cancels.

Keyboard:
- Arrows / WASD: scroll camera. Space: jump to last alert ping.
- A: attack-move armed (next click confirms). S: stop. G: hold ground.
- 1..5: recall control group. Shift held + digit: assign group.
- Q: select all combat units on screen. E: cycle own production buildings.
- B: structures tab. T: units tab. Per-icon letter hotkeys shown on the
  icons themselves (row order fixed per race).
- Esc: cancel placement / clear selection / pause menu. H: help overlay.
- M: mute. Minus / Equal: game speed. P: pause.

## 6. Screens

Title (logo, campaign, skirmish, help) -> Briefing (portrait dialogue,
objectives, LAUNCH) -> Playing (HUD below) -> Victory / Defeat (stats,
next mission or retry). Campaign progress lives in the session (mission
select lists every mission; completing one marks it and recommends the
next). Help overlay lists every hotkey.

HUD: top bar (credits with tick animation, power meter, supply, mission
clock, alerts). Right sidebar: minimap (fog, pings, camera box), build tabs
(Structures / Units) with icon grid, cost, hotkey letter, progress fill,
and a placement ghost flow. Bottom: an always-reserved command console
spanning the world width (the viewport shrinks to leave room, so it never
overlaps the map). Nothing selected shows tips; one unit shows a live-sprite
portrait with name, role, HP and Damage/Range/Speed; a squad shows a
portrait grid with per-unit HP (click a portrait to single that unit out,
shift-click to drop it); a building shows its card. Order buttons (Attack /
Stop / Hold) sit on the right. Enemy structures are selectable for inspection.

## 7. Engine sketch (Mar specifics)

- Int-only math. Positions in fixed-point px*16. 16-way facing via a
  precomputed dx/dy table (per-mille). LCG Park-Miller 16807 RNG.
- Tick: `Time.every (Time.millis 16)`; simulation advances on alternate
  ticks (30 Hz), rendering and camera every frame. Robust to the runtime's
  burst catch-up (pure function of elapsed ticks).
- Map: procedural per mission. Each mission builds a blank field with
  `baseRows` then paints terrain with stamp ops (rect / lane / ore /
  extractor); the result parses to a tile Dict (idx = y*w+x). Tiles:
  regolith, basalt (blocked), chasm (blocked), ice, verdant, ferrite,
  azurite, ramp. Terrain pre-merged into per-row color runs once at load;
  the draw clips runs to the camera viewport.
- Pathing: BFS distance field from the order target, shared by the whole
  commanded group; units descend the field with local avoidance and a
  stuck counter that re-noises their step. Fields cached per target tile.
- Targeting: staggered scans (unit.id mod 8 == tick mod 8). Projectiles are
  src->dst interpolations with per-type speed; beams are instant with a
  2-tick visual.
- Fog of war: per-side visibility grid, 3 states (hidden, seen, visible),
  refreshed incrementally every 8 ticks from unit sight radii; drawn as
  merged runs (black, and seen-dim). Minimap terrain runs precomputed;
  fog runs cached on refresh; entity dots every frame.
- Entity budget: about 60 units + 30 buildings per side, 3 sides max,
  ~80 projectiles, ~60 FX. Perf gate: ms/frame instrument stays under 12ms
  on the dev machine during a 3-way battle.
- FX taste: diegetic only. No screen shake. Explosions are local circles
  with Add blend, smoke is rgba grey, shield hits are Add arcs, purge beam
  telegraph is a growing ring on the ground.

## 8. Sound design (SFX only in missions)

Chip-synth one-shots via Sound: ui click, error buzz, per-faction move and
attack acknowledgements (blip families: Compact square, Combine saw-ish
duty, Vessari sine-vibrato), building placed thunk, construction complete
arp, unit ready blip, low power warning, insufficient funds buzz, base
under attack klaxon plus minimap ping, weapon shots per damage type,
explosions small and large, capture chime, victory and defeat stings,
purge beam charge whine. Menu and briefing screens may run a quiet
Sound.ambient pad; missions run zero music.

## 9. Bot

Per-side state machine evaluated once per second, per difficulty level
(Relaxed / Standard / Iron in skirmish; campaign missions pin tuned
settings):

1. OPENING: scripted build order.
2. ECONOMY: keep harvester count, rebuild lost eco, expand toward the
   nearest live ferrite when saturated or depleted, capture near extractors.
3. ARMY: maintain a composition target; every 45s scout the player's mix
   and lean counters 20 percent.
4. ATTACK: wave timer with growing budget; path to the weakest known player
   structure; retreat the wave at 40 percent losses; ping "enemy forces
   inbound" alert to the player when a wave launches (fair telegraph).
5. DEFEND: pull the wave home when the base is hit; emergency defense
   structure if credits allow.

Beatable by design: bot income multiplier <= 1.0 except Iron, imperfect
focus fire, capped simultaneous production, no worker harass on Relaxed.

## 10. Mission pacing (15+ minutes each)

Three-act structure per mission: quiet setup, a scripted wrinkle, a final
push. Minimum-length levers: timed survival phases, capture-and-hold
timers, multi-base objectives, and bot base sizes that need two or three
assault cycles. Autoplay E2E verifies each mission is winnable and times
the expected completion; target band 15 to 25 minutes at Standard speed.

## 11. File layout

```
examples/iron-meridian/
  mar.json          port 3034, frontend-only
  README.md
  SPEC.md           this file
  Main.mar          MVU glue, screens, input, mission triggers, menus
  Types.mar         shared types + math helpers (LCG, facing LUT)
  Balance.mar       stat tables (functions, not top-level values)
  Maps.mar          tile parse, terrain runs, BFS fields, fog, placement
  Missions.mar      mission + skirmish defs: procedural maps, spawns, objectives
  Story.mar         campaign dialogue and briefing text
  Sim.mar           the tick: economy, production, movement, combat
  Bot.mar           AI (pure: reads state, returns actions)
  Draw.mar          world rendering
  Hud.mar           sidebar, topbar, command console, minimap
  Sfx.mar           sound cues
```

Flat modules at the example root (the serpro-quest pattern). Import
layering keeps cycles impossible: Types imports nothing; Balance, Maps,
Story, Sfx import Types; Missions, Sim and Bot import Types+Balance+Maps;
Draw and Hud import Types+Balance+Maps; Main imports everything.

Mar gotchas honored: no decimal literals anywhere; no min/max/abs helpers
(local ones defined); List.foldl is accumulator-first; top-level data lives
in functions to dodge eager eval order; copy avoids em-dashes and middle
dots; all in-game text in English.
