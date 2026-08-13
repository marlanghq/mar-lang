package typecheck

// soundNoteNames: the 12 chromatic note helpers in semitone order (index = the
// semitone above C, so index 0 = C, 4 = E, 9 = A). Each is Sound.<name> : Int ->
// Int, mapping an octave to an equal-temperament Hz (A4 = 440). `as_` is A# (the
// trailing underscore avoids the `as` keyword).
var soundNoteNames = []string{"c", "cs", "d", "ds", "e", "f", "fs", "g", "gs", "a", "as_", "b"}

// soundWaves is the chip-audio waveform set. The Sound.Wave union's
// constructors mirror these one-to-one (Sound.Square, ...), so
// `case w of Sound.Square -> ...` is exhaustiveness-checked and a typo is a
// COMPILE error. The JS runtime maps each to a WebAudio oscillator type (Noise
// to a noise buffer). Qualified-only, like Keyboard.Key / Gamepad.Button, so
// the four names don't pollute the global scope. See docs/proposals/sound.md.
var soundWaves = []string{"Square", "Triangle", "Sawtooth", "Noise"}

// soundWaveCustomType registers the Sound.Wave union for exhaustiveness
// checking. Mirrors gamepadButtonCustomType / keyboardKeyCustomType.
func soundWaveCustomType() CustomType {
	ctors := make(map[string]CustomCtor, len(soundWaves))
	for _, w := range soundWaves {
		ctors[w] = CustomCtor{Result: TSoundWave}
	}
	order := make([]string, len(soundWaves))
	copy(order, soundWaves)
	return CustomType{
		Name:         "Sound.Wave",
		Params:       nil,
		Constructors: ctors,
		CtorOrder:    order,
		Module:       "Sound",
	}
}

// soundWaveBindings is the value-env half: each wave constructor is a nullary
// value of type Sound.Wave bound under its qualified name (Sound.Square, ...).
// Unlike Keyboard/Gamepad, these ARE used as values (the first argument to
// Sound.tone), so the Go and JS runtimes register matching runtime values.
func soundWaveBindings() map[string]Type {
	out := make(map[string]Type, len(soundWaves))
	for _, w := range soundWaves {
		out["Sound."+w] = TSoundWave
	}
	return out
}
