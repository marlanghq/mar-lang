# Mar-Trix

A falling-block puzzle game. The playfield in this genre has a proper name —
the **matrix** — which happens to sit one letter away from the language the
game is written in. So the game is named after its own matrix, and the matrix
is named after Mar.

```
mar dev examples/mar-trix
```

## Controls

| | |
| --- | --- |
| arrows | move left and right, down to soft-drop |
| up / X | rotate clockwise |
| Z | rotate counter-clockwise |
| C / left shift | hold |
| space | hard drop |
| space / enter | start, and play again |

On a controller, the layout is the console one, because that is the one a
player already has in their hands.

| | |
| --- | --- |
| d-pad or left stick | move left and right, down to soft-drop |
| d-pad up | hard drop |
| A / X | rotate one way |
| B / Y | rotate the other |
| any shoulder | hold |
| start / select | start, and play again |

Both rotations get two buttons rather than one, because which face button
turns which way is the single thing every falling-block game disagrees about,
and a player who guesses wrong should get a rotation rather than nothing. Up
is a hard drop and the stick's up is deliberately not: a thumb resting on an
analog stick would slam pieces down all game.

None of this is a second code path. A pad snapshot is translated into the very
same keys the keyboard sends, so delayed auto-shift, soft drop and every
edge-triggered action work on a controller without one line of game code
knowing a controller exists. Three input devices, one vocabulary.

## What is in it

The genre feels good because of everything *around* the falling, and all of
it is here:

- **7-bag randomiser.** Every seven pieces contains each kind exactly once,
  shuffled. Pure random gives real droughts, and a player who waited eleven
  pieces for a line piece blames the game — correctly.
- **Ghost piece**, drawn as an outline in the piece's own colour, so a hard
  drop is a decision and not a gamble.
- **Hold**, once per piece.
- **Lock delay with a reset budget.** Moving a piece that is already resting
  restarts the countdown, so you can slide it into place — fifteen times,
  after which it locks anyway.
- **Delayed auto-shift.** The first repeat waits ten frames, then a step
  every two: the difference between a game that feels unresponsive and one
  that feels uncontrollable.
- **Wall kicks**, including the floor kick that rotates a piece out of a
  well instead of refusing.
- **Combo and back-to-back** scoring, in whole points.

## Integers all the way down

Mar has no floats, so every number here is an `Int` — including the gravity
accumulator, which counts *frames* rather than seconds, and the score, which
is therefore exact. That is not a workaround: it means a given piece sequence
lands identically on the web and on iOS, frame for frame.

The one place a fractional value would normally appear — a sine wave for the
title bob and the prompt pulse — is a triangle wave instead:

```mar
wave : Int -> Int -> Int
wave span t =
    abs (modBy (span * 2) t - span) - span // 2
```

A triangle is arguably what you want anyway: it spends no time loitering at
the extremes.

## Drawing

Everything is `Canvas`, and every dimension is derived from the live canvas
size, so the same code fills a phone and a desktop window. Three techniques
carry the look, none of which needs a primitive Mar does not have:

- **A cell is five flat rectangles**: a dark gap around it, the colour, a lit
  top edge, a shaded bottom edge, and a paler square set into the middle. No
  gradient, and it still reads as a glazed tile.
- **The background is a dead matrix** — one flat cell every three, hue picked
  by position, all of it at three or four percent alpha. A few hundred
  rectangles buy a texture that no flat fill can, and the game ends up played
  against a dim, empty copy of its own field.
- **A cleared row lights in `Canvas.Blend Canvas.Add`**, from the middle
  outwards, so stacked tiles brighten rather than average toward grey. That
  is what makes it read as light instead of paint.

The effects follow the house rule: change the **object**, never the screen.
A cleared row lights up and collapses, a hard-dropped piece flashes where it
landed, the frame pulses warm on a level up. Nothing shakes, nothing jitters,
and the matrix never moves under your hands.

## Sound

Eight cues, all built from `Sound.tone` / `Sound.sweep` / `Sound.chord` /
`Sound.arp` — a short square blip for a move, a noise tick for a lock, a
falling sweep for a hard drop, a four-voice chord for a four-row clear, an
arpeggio for a level up, and a long descending sweep when the run ends.
