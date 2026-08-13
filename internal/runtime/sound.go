package runtime

import "math"

// Sound (docs/proposals/sound.md) is chip-audio. Builders assemble an opaque
// Sound value; Sound.play turns it into a Cmd, Sound.loop / Sound.voice into
// a Sub. The real synthesis (WebAudio) lives in the JS runtime: these Go
// builtins are inert, present only so top-level Sound constants force without
// crashing during server-side eval and the builtin vocabulary stays uniform
// across runtimes (the drift parity tests).
func soundBuiltins() map[string]Value {
	// opaque Sound data on the backend; the JS runtime carries the real voices.
	sound := VCtor{Tag: "Sound"}
	inertSound := func(arity int) Value {
		return nativeFn(arity, func(args []Value) (Value, error) { return sound, nil })
	}
	inertEffect := func(tag string) Value {
		return VEffect{Tag: tag, Run: func() (Value, error) { return VUnit{}, nil }}
	}
	m := map[string]Value{
		// Sound.Wave constructors: real values (used as Sound.tone's first arg,
		// so top-level Sound constants force them). The frontend synth reads the tag.
		"Sound.Square":   VCtor{Tag: "Square"},
		"Sound.Triangle": VCtor{Tag: "Triangle"},
		"Sound.Sawtooth": VCtor{Tag: "Sawtooth"},
		"Sound.Noise":    VCtor{Tag: "Noise"},

		// builders: opaque Sound in, opaque Sound out (never synthesised here).
		"soundTone":      inertSound(3),
		"soundVolume":    inertSound(2),
		"soundSweep":     inertSound(2),
		"soundLowCut":    inertSound(2),
		"soundHighCut":   inertSound(2),
		"soundHoldPitch": inertSound(2),
		"soundAttack":    inertSound(2),
		"soundRelease":   inertSound(2),
		"soundDuty":      inertSound(2),
		"soundVibrato":   inertSound(3),
		"soundArp":       inertSound(2),
		"soundRest":      inertSound(1),
		"soundChord":     inertSound(1),
		"soundSequence":  inertSound(1),

		// play : Sound -> Cmd msg ; loop / ambient / once : Sound -> Sub msg.
		"soundPlay":  nativeFn(1, func(args []Value) (Value, error) { return inertEffect("soundPlay"), nil }),
		"soundLoop":  nativeFn(1, func(args []Value) (Value, error) { return inertEffect("soundLoop"), nil }),
		"soundVoice": nativeFn(1, func(args []Value) (Value, error) { return inertEffect("soundVoice"), nil }),
		"soundGlide": nativeFn(1, func(args []Value) (Value, error) { return inertEffect("soundGlide"), nil }),
		"soundOnce":  nativeFn(1, func(args []Value) (Value, error) { return inertEffect("soundOnce"), nil }),

		// setMuted / master : app-owned audio controls (inert Cmds here).
		"soundSetMuted": nativeFn(1, func(args []Value) (Value, error) { return inertEffect("soundSetMuted"), nil }),
		"soundMaster":   nativeFn(1, func(args []Value) (Value, error) { return inertEffect("soundMaster"), nil }),
	}

	// Note helpers: octave -> Hz (equal temperament, A4 = 440). REAL values (not
	// inert) so a top-level Sound constant built from a pitch forces correctly.
	// The slice index is the semitone above C.
	noteNames := []string{"c", "cs", "d", "ds", "e", "f", "fs", "g", "gs", "a", "as_", "b"}
	for semitone, name := range noteNames {
		s := semitone
		m["soundPitch_"+name] = nativeFn(1, func(args []Value) (Value, error) {
			oct := int64(0)
			if len(args) > 0 {
				if iv, ok := args[0].(VInt); ok {
					oct = iv.V
				}
			}
			midi := 12*(oct+1) + int64(s)
			hz := int64(math.Round(440 * math.Pow(2, float64(midi-69)/12)))
			return VInt{V: hz}, nil
		})
	}
	return m
}
