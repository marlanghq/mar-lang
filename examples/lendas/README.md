# LENDAS

A two-player, **asynchronous collectible card game** about Latin
American myth — and the Mar example that combines the whole stack in
one app: email-code **Auth**, REST **services**, SQLite **entities**,
an App-UI **lobby**, and a fully animated **canvas** card table with
sound. Portuguese (pt-BR) copy throughout; the full rules live in
[REGRAS.md](REGRAS.md).

```
mar dev examples/lendas     # port 3030
```

Dev magic-codes print to the server log (no SMTP needed). Open two
browser sessions with different e-mails to play a real match against
yourself.

## The game

Hearthstone-simple, Magic-flavored: 20 life, energy that grows +1 per
turn (cap 8) and refills, creatures with summoning sickness, Guardiões
(taunt), Ímpeto (charge), "Ao entrar" battlecries, targeted spells, a
5-slot board, hand cap with burn, and a fatigue clock when the deck
runs dry. Three fixed 20-card decks: **Floresta** (Brazilian folklore
— Saci, Curupira, Boitatá, Mapinguari), **Sol** (Andes + Mesoamerica —
Guerreiro Jaguar, Inti, Quetzalcóatl) and **Águas** (rivers and sea —
Iara, Boto, Iemanjá, Minhocão). Thirty unique cards, each with its own
hand-drawn canvas emblem.

Matches are **asynchronous**: challenge a rival by nick (one open
match per pair), each of you plays whenever you can, close the tab and
come back — the match waits. When it IS your rival's turn, the table
polls and **replays their moves as animations** when they land.

## How it works

- **One rules engine, two runtimes.** `Shared.mar` holds the whole
  game: card bank, deterministic shuffle, and `applyMoveBy`, a
  validating reducer over compact move strings (`"J 2 -"`, `"A 0 h"`,
  `"E"`). The **Go server replays the stored log to judge every
  incoming move**; the **JS client replays the same log to draw the
  board**. The shuffle uses a Park-Miller LCG whose products stay
  inside float64's exact range, so both runtimes compute identical
  states from the same seed.
- **The move log is the source of truth.** A `games` row stores the
  matchup, decks, seed and denormalized turn/winner; every play
  appends one `moves` row. Optimistic concurrency: the client sends
  the move with its expected index; a stale index answers
  `MoveDesatualizado` and the client refetches.
- **The canvas table** (`Frontend/Match.mar`) fetches
  `{seed, decks, moves}`, replays instantly on load (resuming skips
  the theatrics), then feeds every NEW move — yours or theirs —
  through an animation queue: card slams, attack lunges, hit flashes,
  floating damage numbers, spell rings, a turn banner, and chiptune
  cues (`Sound.play`) for each beat.
- **Client-side prevention, server-side truth.** Taps pre-flight the
  move through the same engine (illegal moves never leave the client
  and explain themselves in a toast); the server never trusts the
  client anyway.
- Honest note: the client receives the full seed + log, so a curious
  opponent can peek at your hand with DevTools. Hiding private zones
  takes a server-side view layer this example deliberately skips.

## Screens

- `/entrar` + code — email sign-in (framework Auth).
- `/apelido` — one-time nick gate.
- `/` — the lobby: received/sent challenges, running matches (with
  SUA VEZ badges), finished games; challenge-by-nick with a deck pick.
- `/jogo/{id}` — the canvas table.
