package conformance

// The sound corpus, and why it is not part of the string corpus next door.
//
// Every other module in this package is compared the same way: a Mar
// expression is reduced to a String and three runtimes are asked to print the
// same thing. Sound cannot be reduced that way. A Sound is opaque to Mar code
// on purpose (you build it and you play it; nothing reads it back), and giving
// it a `describe : Sound -> String` just so a test could see inside would be
// putting the test harness into the language.
//
// So this corpus compares one layer down, from the host side: the VOICE LIST
// each runtime derives from a Sound before handing anything to its audio
// backend. Both runtimes already carry it as plain data inside the Sound value
// (`{k:'SND', voices:[...]}` in JS, `.ctor("__Snd", [.list(...)])` in Swift),
// which is what makes this comparable at all without either side growing a
// debug API.
//
// WHAT THIS CANNOT COMPARE, and the reason it stops here: the samples. The web
// hands the voice list to WebAudio nodes; iOS synthesises it by hand, sample by
// sample. Below this layer they are different machines: a BiquadFilter against
// a one-pole, an oversampled WaveShaper against a per-sample tanh, a resampled
// noise clip against sample-and-hold. Those differences are real and audible
// and this corpus will not catch them. It catches the other kind, the kind that
// is a plain bug: the two sides disagreeing about what a Sound MEANS, which is
// how the master level came to sit at 0.35 on one side and 0.5 on the other.
//
// Go is absent for a reason worth writing down: its Sound builtins are inert
// (see internal/runtime/sound.go), every builder returning one opaque value,
// because the server never synthesises. There is nothing to compare there, so
// this is a two-runtime corpus where the stdlib one has three.

// SoundSource is the fixture module. Every Sound combinator has to appear here
// or TestSoundCorpusCoversTheModule fails: the gate that the string corpus has
// for its own modules, since a combinator nobody wrote a fixture for is exactly
// the one free to drift.
//
// Each fixture is a top-level Sound, named for what it exercises. They are
// deliberately small and deliberately layered (a sweep inside a vibrato inside
// a volume), because nesting order is where the combinators either agree or
// quietly do not: `lowCut 340 (highCut 2600 s)` losing the 2600 was a real bug
// found this way.
const SoundSource = `module SoundConform exposing (..)


-- the plainest voice there is: one wave, one pitch, one duration
plain : Sound
plain =
    Sound.tone Sound.Square 440 200


-- every wave, so a renamed tag shows up as a diff instead of as silence
triangle : Sound
triangle =
    Sound.tone Sound.Triangle 220 120


sawtooth : Sound
sawtooth =
    Sound.tone Sound.Sawtooth 110 90


noise : Sound
noise =
    Sound.tone Sound.Noise 0 55


rest : Sound
rest =
    Sound.rest 75


-- volume is the number the master level scales, so it is the one that made
-- the same game come out louder on the phone
loud : Sound
loud =
    Sound.volume 34 (Sound.tone Sound.Square 330 100)


-- the envelope: both ends, and both below the 5ms floor the engines impose
enveloped : Sound
enveloped =
    Sound.release 120 (Sound.attack 40 (Sound.tone Sound.Triangle 260 400))


shortEnvelope : Sound
shortEnvelope =
    Sound.release 1 (Sound.attack 2 (Sound.tone Sound.Square 500 60))


-- the decay stage, both ends of it. the struck one falls to silence inside its own
-- length, which is the case that breaks a naive implementation: an exponential
-- ramp cannot be given a zero target on the web, and pow(0,0) is 1 on iOS.
struck : Sound
struck =
    Sound.decay 260 0 (Sound.attack 4 (Sound.tone Sound.Triangle 330 700))


-- and a pad: falls part way, then HOLDS there until the release.
settled : Sound
settled =
    Sound.release 300 (Sound.decay 420 65 (Sound.attack 180 (Sound.tone Sound.Sawtooth 147 1400)))


-- decay asked for on a note too short to hold it: the fall is clamped to the
-- space the attack and release leave, and must not run past the note.
crowded : Sound
crowded =
    Sound.release 40 (Sound.decay 900 20 (Sound.attack 30 (Sound.tone Sound.Square 440 80)))


-- a note shorter than the floor, and one of no length at all. Anything under
-- 20 ms is played as 20 ms, and the two runtimes have to floor it at the same
-- place: the web used to read a length of exactly 0 as a hundred milliseconds,
-- five times what the phone played and five times the space its own loop
-- scheduler reserves for it.
clipped : Sound
clipped =
    Sound.tone Sound.Square 440 5


instant : Sound
instant =
    Sound.tone Sound.Triangle 330 0


-- pitch over time: sweep, sweep with a hold, and the arp stepper
swept : Sound
swept =
    Sound.sweep 880 (Sound.tone Sound.Sawtooth 220 300)


held : Sound
held =
    Sound.holdPitch 90 (Sound.sweep 120 (Sound.tone Sound.Triangle 480 260))


-- arp over a CHORD, which is what pins the unit. The offsets are SEMITONES from
-- each voice's own pitch, so a major triad written once has to come back as one
-- figure above 262 and the same figure above 131. Under the old absolute-Hz
-- reading both voices would step through the same three frequencies and the
-- octave between them would collapse.
arpeggio : Sound
arpeggio =
    Sound.arp [ 4, 7 ]
        (Sound.chord
            [ Sound.tone Sound.Square 262 240
            , Sound.tone Sound.Square 131 240
            ]
        )


-- timbre: pulse width and the vibrato LFO
pulsed : Sound
pulsed =
    Sound.duty 12 (Sound.tone Sound.Square 196 180)


wobbly : Sound
wobbly =
    Sound.vibrato 28 9 (Sound.tone Sound.Sawtooth 165 320)


-- the other wobble: tremolo is amplitude where vibrato is pitch, and its depth
-- is a PERCENTAGE rather than cents. Over a chord, so it also pins that both
-- runtimes carry it per voice; clamped at 100, since past that there is nothing
-- left to take away.
throbbing : Sound
throbbing =
    Sound.tremolo 140 5
        (Sound.chord
            [ Sound.tone Sound.Triangle 220 900
            , Sound.tone Sound.Triangle 330 900
            ]
        )


-- the sine: the one wave with no harmonics, and the only one where duty is
-- meaningless. Both runtimes have to route it as a sine and not fall back to a
-- square, which is exactly what they used to do with an unknown tag.
pure : Sound
pure =
    Sound.duty 25 (Sound.tone Sound.Sine 110 400)


-- detune: a unison, which is what it exists for, written the way the one rule
-- forces -- each layer detuned BEFORE it joins the chord, rather than one
-- detune over the pair, which would now move both together and produce no beat.
unison : Sound
unison =
    Sound.chord
        [ Sound.detune (0 - 7) (Sound.tone Sound.Sawtooth 220 500)
        , Sound.detune 7 (Sound.tone Sound.Sawtooth 220 500)
        ]


-- detune on noise, where it means resampling the clip rather than shifting an
-- oscillator: the two runtimes reach the same ratio from opposite directions.
detunedNoise : Sound
detunedNoise =
    Sound.detune 600 (Sound.tone Sound.Noise 300 120)


-- detune on noise that has NO pitch, which is how every noise voice in this repo
-- is written. There is nothing to shift, and both runtimes have to agree about
-- that: the web used to resample the clip anyway, so the same call changed the
-- timbre in the browser and did nothing at all on the phone.
detunedSilence : Sound
detunedSilence =
    Sound.detune 600 (Sound.tone Sound.Noise 0 200)


-- pan over a CHORD: both layers have to come back placed. Kept as its own
-- fixture because a combinator wrapping a chord is the shape that used to reach
-- only the last voice, and most fixtures here wrap a single tone, where the two
-- behaviours are indistinguishable.
spread : Sound
spread =
    Sound.pan 40
        (Sound.chord
            [ Sound.tone Sound.Triangle 220 400
            , Sound.tone Sound.Triangle 330 400
            ]
        )


-- the negative side, in the idiom the language forces: there is no unary minus,
-- so a hard left is written (0 - 100). Pins that both runtimes carry the SIGN,
-- not just the magnitude, which is the one error a listener cannot hear as wrong
-- and only notices as an image that leans the wrong way.
hardLeft : Sound
hardLeft =
    Sound.pan (0 - 100) (Sound.tone Sound.Square 440 300)


-- out of range. Both runtimes clamp to +/-100, and they have to clamp to the
-- SAME place: the law reads pan directly, so an unclamped 400 becomes a gain of
-- -3, which is a polarity inversion three times too loud rather than silence.
overPanned : Sound
overPanned =
    Sound.pan 400 (Sound.tone Sound.Sawtooth 165 250)


-- the two cuts, nested both ways round: the outer one used to eat the inner
cut : Sound
cut =
    Sound.lowCut 340 (Sound.highCut 2600 (Sound.tone Sound.Noise 0 400))


cutFlipped : Sound
cutFlipped =
    Sound.highCut 2600 (Sound.lowCut 340 (Sound.tone Sound.Noise 0 400))


-- and the two shapes that combine voices: at once, and one after another
stacked : Sound
stacked =
    Sound.chord
        [ Sound.volume 22 (Sound.tone Sound.Square 262 300)
        , Sound.volume 11 (Sound.tone Sound.Triangle 131 300)
        ]


ordered : Sound
ordered =
    Sound.sequence
        [ Sound.tone Sound.Square 262 80
        , Sound.rest 40
        , Sound.tone Sound.Square 392 80
        ]


-- the pitch table, which nothing compared until this corpus existed. Each
-- helper answers a frequency in Hz, so putting all twelve into one chord lands
-- the whole octave in the voice list where it can be diffed. A4 is 440 by
-- definition; the other eleven are the equal-temperament steps around it, and a
-- single wrong exponent would make one runtime play a different note.
notes : Sound
notes =
    Sound.chord
        [ Sound.tone Sound.Square (Sound.c 4) 40
        , Sound.tone Sound.Square (Sound.cs 4) 40
        , Sound.tone Sound.Square (Sound.d 4) 40
        , Sound.tone Sound.Square (Sound.ds 4) 40
        , Sound.tone Sound.Square (Sound.e 4) 40
        , Sound.tone Sound.Square (Sound.f 4) 40
        , Sound.tone Sound.Square (Sound.fs 4) 40
        , Sound.tone Sound.Square (Sound.g 4) 40
        , Sound.tone Sound.Square (Sound.gs 4) 40
        , Sound.tone Sound.Square (Sound.a 4) 40
        , Sound.tone Sound.Square (Sound.as_ 4) 40
        , Sound.tone Sound.Square (Sound.b 4) 40
        ]


-- and the same helpers an octave apart, since the doubling is where an
-- off-by-one in the octave maths would show
octaves : Sound
octaves =
    Sound.chord
        [ Sound.tone Sound.Triangle (Sound.a 2) 40
        , Sound.tone Sound.Triangle (Sound.a 5) 40
        ]


-- gain: a percentage OF what is already there, which is why it wraps a chord
-- whose layers were mixed against each other first. Both come down by half and
-- the balance between them survives -- the thing Sound.volume cannot express,
-- because it would flatten both layers to one absolute level.
quieted : Sound
quieted =
    Sound.gain 50
        (Sound.chord
            [ Sound.volume 80 (Sound.tone Sound.Square 262 300)
            , Sound.volume 40 (Sound.tone Sound.Triangle 131 300)
            ]
        )


-- sweepBy: a bend measured in cents rather than a destination in Hz, so the two
-- layers keep their octave instead of both landing on one frequency. 700 cents
-- is a fifth.
bentUp : Sound
bentUp =
    Sound.sweepBy 700
        (Sound.chord
            [ Sound.tone Sound.Sawtooth 220 300
            , Sound.tone Sound.Sawtooth 440 300
            ]
        )


-- transpose: everything with a pitch moves by the same interval and nothing
-- else does. The sweep target has to travel with its note (or the bend would
-- widen), while the noise voice and the rest both carry freq 0 and have to come
-- back untouched -- 0 is "no pitch", not "a very low one".
moved : Sound
moved =
    Sound.transpose 5
        (Sound.chord
            [ Sound.sweep 440 (Sound.tone Sound.Square 220 300)
            , Sound.tone Sound.Noise 0 300
            , Sound.rest 300
            ]
        )


-- stretch: EVERY time in the voice scales, not just the note length. The
-- sequence bakes its delays at build time, so this pins that a stretched
-- passage stays in time with itself instead of playing long notes at the old
-- spacing. 50 is half speed; the envelope stretches with the note it shapes.
slowed : Sound
slowed =
    Sound.stretch 50
        (Sound.sequence
            [ Sound.release 60 (Sound.attack 40 (Sound.tone Sound.Square 262 160))
            , Sound.rest 80
            , Sound.holdPitch 90 (Sound.sweep 262 (Sound.tone Sound.Triangle 392 240))
            ]
        )


-- and the other direction, on the shape that catches stretch out: a CHORD whose
-- two voices end together on numbers that do not divide. Every voice of a chord
-- has to span the same total ms or a looped track walks a little further apart
-- on every pass, so both runtimes have to round the same way AND have to round
-- the END of the note rather than its length. These numbers are chosen so the
-- two disagree: 100 + 805 rounds to 67 + 537 = 604 one way and to 603 the other,
-- against a first voice that is 603 either way. A runtime that scales durations
-- one at a time comes back one millisecond long here, and only here.
hurried : Sound
hurried =
    Sound.stretch 150
        (Sound.chord
            [ Sound.decay 300 50 (Sound.tone Sound.Sawtooth 147 905)
            , Sound.sequence
                [ Sound.rest 100
                , Sound.tone Sound.Square 294 805
                ]
            ]
        )


-- the shape a game actually writes: several layers, each nested, the second
-- one starting only after the first has been going a while
scene : Sound
scene =
    Sound.chord
        [ Sound.volume 22 (Sound.vibrato 28 9 (Sound.sweep 38 (Sound.tone Sound.Sawtooth 165 820)))
        , Sound.sequence
            [ Sound.rest 90
            , Sound.volume 13 (Sound.tone Sound.Noise 0 70)
            , Sound.rest 80
            , Sound.volume 10 (Sound.tone Sound.Noise 0 55)
            ]
        ]
`

// SoundFixtures names the bindings to compare, in the order the report prints
// them. Listed rather than discovered so the report reads the same on every
// run and a dropped fixture is a diff, not a shorter list nobody counted.
var SoundFixtures = []string{
	"plain", "triangle", "sawtooth", "noise", "rest",
	"loud", "enveloped", "shortEnvelope", "clipped", "instant",
	"swept", "held", "arpeggio", "bentUp", "moved",
	"quieted", "slowed", "hurried",
	"struck", "settled", "crowded",
	"pulsed", "wobbly", "throbbing", "pure", "unison", "detunedNoise", "detunedSilence",
	"spread", "hardLeft", "overPanned",
	"cut", "cutFlipped",
	"stacked", "ordered",
	"notes", "octaves",
	"scene",
}

// SoundFields is the voice record, in the order every driver prints it. The
// order is fixed here rather than in each driver so the two cannot disagree
// about the layout while agreeing about the values, which would read as a diff
// on every line and hide the real one.
//
// A field absent from a voice prints as 0: "unasked" is a value both runtimes
// have to represent the same way, and a voice that grows a field on one side
// only is exactly what this is for.
var SoundFields = []string{
	"wave", "freq", "ms", "endFreq", "holdMs", "volume", "delayMs",
	"duty", "vibDepth", "vibRate", "tremDepth", "tremRate", "arp", "lowCut", "highCut",
	"attack", "release", "decay", "sustain", "detune", "pan",
}
