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

Eight cues, and every one of them is a **struck** thing: a piece clicking into
a column, a piece landing, four rows going at once. So every one of them has a
`Sound.decay`, the stage that lets a note fall the way a struck thing loses
energy instead of standing at full height until it is switched off. A note
without one is a rectangle, and a rectangle is the loudest way a sound can
announce that nothing hit anything. That single change is most of this section.

It costs level, so every volume here went up to pay for it: a shape that falls
delivers a fraction of the energy a rectangle of the same height does. The new
numbers were measured back against the old cues rather than guessed at, and
each one lands within a few decibels of where it was, with a higher peak and a
shorter tail. Sharper, not louder.

Three things beyond the envelope, and no more than three:

- **The hard drop has a body.** The falling triangle is the impact's speed; a
  `Sound.Sine` under it at 190 Hz is its weight. A sine is the one wave with no
  harmonics at all, which makes it the only way to put something that low under
  a click without also putting a buzz on top of it. The tiny speaker in a phone
  will not reproduce that layer and does not have to. A slam is now heavier
  than a piece settling on its own, which is the way round it should always
  have been.

- **The four-row clear is doubled.** Two copies of its root A, nine cents
  apart, beating slowly against each other, which is physically what more than
  one of something is. It is the one cue worth a whole extra voice: it is the
  sound the game is played for. Its four voices also stopped being equally
  loud, which is what a chord is: `Sound.volume` patches the LAST voice of a
  chord, so the old one had set its brightest note and left the rest louder.

- **The piece's own sounds come from where the piece is.** `Sound.pan`, thirty
  percent at the very most, off the piece's real centre across the ten columns
  — the shape's centre, not the box's, or the five pieces that are three cells
  wide inside that four-wide box lean the whole game to the right. A cleared row is the
  whole width of the well and a level is the whole run, so those stay in the
  middle. Pan is information here rather than decoration, which is the same
  rule the visual effects follow: the object moves, the screen never does.

One repair on the way through. The level-up fanfare was calling `Sound.arp`
with semitone offsets, and `Sound.arp` takes **hertz**: `[ 0, 4, 7, 12 ]` asked
for four steps between one and twelve hertz, so what actually played was a C
blinking through silence ten times a second. Written as notes it is the major
arpeggio it always meant to be.
