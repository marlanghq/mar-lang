# Vortex

A tube shooter, in the line the 1981 arcade **Tempest** started and **Tempest
2000** finished. You are a claw on the near rim of a geometric well. Everything
that wants you climbs the lanes toward that rim, and the only thing between you
and it is how fast you can get around the ring.

```bash
mar dev examples/vortex
```

Port 3042.

## Controls

| | |
|---|---|
| Left / Right, or A / D | walk the rim, one way round or the other |
| Space or Z | fire — hold for a stream, tap for a burst |
| any key | start |
| controller: d-pad or left stick | walk the rim |
| controller: any face or shoulder button | fire |

On a controller the stick is analog, so a nudge crawls you one lane over and
a shove sprints you across the web; the d-pad is a switch and walks at the
keyboard's one speed. This is the input the genre was built for: the 1981
cabinet had a spinner and nothing else, and Tempest 2000 shipped a rotary
controller whose encoder is wired straight into the pad's left and right.

On a phone: a stick on the bottom left, fire on the bottom right. They appear
only on a device with no pointer (`Device.touchOnly`), and the top of the
screen is inert so your thumbs are never over the well you are reading.

Firing is the same rule on both, and it is the arcade's rule: **hold and it
keeps firing, but only eight shots exist**. What stops you is not a slow
trigger, it is a quantity — hold it down and you spend the allowance, and the
gap while you wait for shots to clear is the gap something arrives in. A tap
fires instantly, so a fast finger still beats the held stream and hits the
ceiling instead of the timer. That is worth preserving because it is the whole
reason the button is interesting: the decision is *which lane deserves the next
one*, not *how fast can I press*.

The stick is a stick and not a pair of arrows for two reasons. A closed web
has no ends, so a control that maps *this* far along a strip to *that* lane
runs out of strip on a shape you can walk forever; deflection means speed, and
speed never runs out. And two buttons big enough to hit reliably are two
buttons big enough to be in the way of a game you play by reading. You grab
the stick anywhere in the left half — wherever the thumb lands becomes its
centre — so the mark on screen can be small: it is a reminder of where your
thumb goes, not a target.

## What is in it

One enemy, one gun, and a well that changes shape every level.

The enemy climbs a lane toward you, flipping lane to lane on the way, and
where in the well it does that is the whole difficulty of the game.

Deep down it flips FAST, in a direction of its own. Near the rim it flips
SLOWLY, and toward you.

That is the way round it is for a reason, and it took a playtest to find. The
obvious arrangement is the opposite: calm at the bottom, frantic hunting at
the top. It plays terribly. A creature that re-aims three times a second
inside the last stretch is not something you dodge, it is something you guess
at, because the correction lands before you have finished stepping out of the
way, and losing to that reads as unfair. Slow it down up close and the same
move becomes a threat you can answer: it will come for you, about once every
second and a half, so the lane it is in is worth reading. Meanwhile the fast
flipping moved to where it costs nothing to watch, and it pays for itself
down there twice over: it fills the well with motion, and it spreads the wave
across the whole web instead of letting every creature converge on the one
lane you happen to be standing in.

If it gets all the way up, it does not end your run on the spot: it takes the
rim and comes along it for you, and it costs a life only when it reaches you.
That chase is the best thing in the game to watch, and it needs one rule to
exist at all — a fuse. A rim-walker moves slower than you, so a player who
keeps running is never caught, and you cannot shoot one either, because a shot
goes down the lane you are standing in and standing in its lane is how you
die. Left alone it would sit there forever and the level, which clears when
the well is empty, could never end. So its welcome runs out: a few seconds up
there, blinking as they go, and then it is gone. Running away works. Standing
still is what costs you.

That is the whole roster, and it is enough because the difficulty comes from
somewhere else: sixteen web shapes that cycle with a new ink each time round
(some are closed rings you can walk forever, some are open lines with two ends
you can be cornered against), how fast they climb, how tightly they flip, and
how many are in the well at once. A second creature would be a second thing to
learn, and this game is asking you to learn a place, not a bestiary.

Behind all of it is a field of stars pouring out of the vanishing point, on
the same perspective curve as the lanes. It is not decoration: the well is a
wireframe with nothing inside it, and without the stars the space between the
lanes is flat black and reads as *empty*. Fill it with points that are
unmistakably far away and the tube stops being a shape drawn on a background
and becomes a shape you are looking through.

Between levels the claw dives down the tube and out the far end, which is the
one moment the camera gets to show what it is.

The title screen is the game playing itself — a demo pilot walking the rim on
a well that changes every few seconds — behind a logo that arrives out of the
vanishing point.

### How it got here

This started as a faithful Tempest 2000 clone: five enemies, four power-ups, a
superzapper, spikes, jump, warp tokens, a bonus round. All of it worked and
none of it was the reason the thing was fun. What was fun was the well — the
sense of looking *into* something, and of a shape rushing out of it — and
every system on top was another rule standing between a player and that. So
they were removed, and the source got about forty per cent shorter.

## How the camera works

A tube shooter lives or dies on one illusion: that the well is a solid object
you are looking *into*, not a ring of lines you are sliding along. Three things
sell it, all of them in `project` and `camera`:

1. **A real perspective divide.** A point at depth `z` lands at
   `far + (rim - far) * D / (D + z)`. That hyperbola is why something at the far
   end crawls and the same thing at your feet is on you before you finish the
   thought — constant speed in the well, accelerating on screen. A linear
   interpolation gets the picture right and the feel completely wrong.

2. **The far end is not the middle of the screen.** It leans *away* from where
   you stand on the rim, so walking around the ring swings the whole tube the
   other way. Pin the vanishing point to the centre and the same geometry turns
   back into a flat ring.

3. **And nothing else moves it.** The camera answers the player and no one
   else, so a still player is a still frame — the stars stream, but they are
   content moving inside a fixed frame, the same as an enemy climbing a lane,
   and the vanishing point they pour out of does not budge until you walk.
   The lean is eased rather than snapped,
   because the claw jumps a whole lane at a time and following that instantly
   would make the well twitch once per step — easing turns the same steps into
   a camera gliding to keep up, and it settles to a full stop a few frames
   after you stop walking. Each web has its own fixed orientation, set once on
   arrival, and holds it.

What it never does is shake. Death and impact change the object — colour,
shape, the claw going pale as it leaves the web — and leave the frame where it
is.

## Notes on the code

- Mar has no floating point, so everything is fixed point at 1024 and every
  angle is a **brad**: one 256th of a turn, which makes wrapping a `modBy 256`
  instead of a division by 360. Sine and cosine come from `Math`, which reads
  one table built into every runtime, so a given run plays identically on the
  web and on iOS.
- Canvas has rectangles, circles and triangles but no line, and this game is
  nothing but lines at arbitrary angles. `seg` builds one as two triangles from
  the perpendicular of its own direction. The perpendicular is computed in
  fixed point and **rounded** — in plain integers it truncates to zero for any
  line more than a couple of pixels long, the two triangles collapse to zero
  area, and most of the web simply does not appear.
- The claw is built from five points on its own lane and goes through the same
  projection as the web, so it is always welded to the lane it occupies: it
  foreshortens with the lane, leans with the camera, and cannot drift off the
  rim the way a screen-space sprite would.
- An explosion carries the depth it happened at, not just the lane. Without
  that every kill flashes at the near rim however far down the well it
  happened, which looks like a bug and is one.

Vortex is an original game written as an homage; it is not affiliated with
Atari or with Tempest.
