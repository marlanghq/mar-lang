package refgen

import "strings"

// The second hand-authored half of the reference: the modules an app reaches
// for once it stops being pure data — drawing, sound, time, randomness, views,
// navigation, effects, and the backend.
//
// It is split from content.go only for navigation; the maps here are merged
// into the ones there by init below, and every entry obeys the same rules:
// exhaustive categories, a description each, and at least one example that
// examples_test.go compiles against the live compiler.
//
// The examples differ from content.go's in two ways. A List example can state
// an equality and be run; a Canvas.rect call cannot — a Shape has no value
// worth comparing against. Those are compiled instead of evaluated, which is
// what keeps them honest: change the signature and the snippet stops
// typechecking. And where a call needs something declared first — a route, a
// table, an endpoint — the example declares it on its own line, with a name.
// Inlining the declaration into the call fits on one line and teaches nothing:
// what you get is a wall with the interesting part buried in the middle.
func init() {
	registerKeys()
	for mod, groups := range appCategories {
		categories[mod] = groups
	}
	for q, d := range appDescriptions {
		descriptions[q] = d
	}
	for q, ex := range appExamples {
		examples[q] = ex
	}
	for mod, b := range appBlurbs {
		blurbs[mod] = b
	}
}

// The task table every Repo example reads from. One string, reused, so the six
// pages agree with each other and there is one place to fix. tasksTable adds
// the blank line that separates it from the call underneath.
//
// It is one flush-left line on purpose. The page draws an example one line per
// element and its code style collapses runs of whitespace, so an indented
// continuation line would show up unindented — and unindented Mar does not
// parse, which would put non-compiling code on a page whose whole promise is
// that the code compiles. Every multi-line example here is a sequence of
// top-level declarations, each complete on its own line.
const (
	tasksTableOnly = "tasks = Entity.define { name = \"task\", columns = { id = Entity.serial, title = Entity.text Entity.notNull, done = Entity.bool Entity.notNull }, uniques = [] }"

	tasksTable = tasksTableOnly + "\n\n"
)

// letters, digits, and the function keys are spelled out rather than looped
// over in the category table, because the categories carry display order and
// the order of a keyboard is not alphabetical everywhere it matters.
var (
	keyLetters   = []string{"KeyA", "KeyB", "KeyC", "KeyD", "KeyE", "KeyF", "KeyG", "KeyH", "KeyI", "KeyJ", "KeyK", "KeyL", "KeyM", "KeyN", "KeyO", "KeyP", "KeyQ", "KeyR", "KeyS", "KeyT", "KeyU", "KeyV", "KeyW", "KeyX", "KeyY", "KeyZ"}
	keyDigits    = []string{"Digit0", "Digit1", "Digit2", "Digit3", "Digit4", "Digit5", "Digit6", "Digit7", "Digit8", "Digit9"}
	keyFunctions = []string{"F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12"}
	keyNumpad    = []string{"Numpad0", "Numpad1", "Numpad2", "Numpad3", "Numpad4", "Numpad5", "Numpad6", "Numpad7", "Numpad8", "Numpad9", "NumpadAdd", "NumpadSubtract", "NumpadMultiply", "NumpadDivide", "NumpadDecimal", "NumpadEnter"}

	// The keys whose plain-English name is not derivable from the constructor.
	keyProse = map[string]string{
		"ArrowUp": "the up arrow", "ArrowDown": "the down arrow",
		"ArrowLeft": "the left arrow", "ArrowRight": "the right arrow",
		"Backquote": "the backtick", "Minus": "the hyphen", "Equal": "the equals sign",
		"BracketLeft": "the opening square bracket", "BracketRight": "the closing square bracket",
		"Backslash": "the backslash", "Semicolon": "the semicolon", "Quote": "the apostrophe",
		"Comma": "the comma", "Period": "the full stop", "Slash": "the forward slash",
		"ShiftLeft": "the left shift", "ShiftRight": "the right shift",
		"ControlLeft": "the left control", "ControlRight": "the right control",
		"AltLeft": "the left alt", "AltRight": "the right alt",
		"MetaLeft": "the left command or Windows key", "MetaRight": "the right command or Windows key",
		"CapsLock": "caps lock", "NumLock": "num lock", "ScrollLock": "scroll lock",
		"PrintScreen": "print screen", "ContextMenu": "the context-menu key",
		"PageUp": "page up", "PageDown": "page down",
		"NumpadAdd": "plus on the numeric keypad", "NumpadSubtract": "minus on the numeric keypad",
		"NumpadMultiply": "multiply on the numeric keypad", "NumpadDivide": "divide on the numeric keypad",
		"NumpadDecimal": "the decimal point on the numeric keypad", "NumpadEnter": "enter on the numeric keypad",
	}
)

// registerKeys fills in the two input enumerations. They are generated rather
// than typed out because they are a picture of a physical device, not a design:
// 104 hand-written lines of "The A key." would be 104 chances to get one wrong,
// and the compiler already owns the list. The categories still say which key
// belongs where, so the page reads as a keyboard rather than an alphabetical
// dump.
func registerKeys() {
	describe := func(name string) string {
		if p, ok := keyProse[name]; ok {
			return "True while " + p + " is held."
		}
		switch {
		case strings.HasPrefix(name, "Key"):
			return "True while " + strings.TrimPrefix(name, "Key") + " is held."
		case strings.HasPrefix(name, "Digit"):
			return "True while " + strings.TrimPrefix(name, "Digit") + " on the number row is held."
		case strings.HasPrefix(name, "Numpad"):
			return "True while " + strings.TrimPrefix(name, "Numpad") + " on the numeric keypad is held."
		case strings.HasPrefix(name, "F") && len(name) > 1:
			return "True while " + name + " is held."
		}
		return "True while " + strings.ToLower(name) + " is held."
	}
	for _, group := range appCategories["Keyboard"] {
		for _, name := range group.Funcs {
			if name == "watch" {
				continue
			}
			appDescriptions["Keyboard."+name] = describe(name)
			appExamples["Keyboard."+name] = []string{"held keys = List.member Keyboard." + name + " keys.down"}
		}
	}
	for _, group := range appCategories["Gamepad"] {
		for _, name := range group.Funcs {
			if name == "watch" {
				continue
			}
			appExamples["Gamepad."+name] = []string{"held pad = List.member Gamepad." + name + " pad.down"}
		}
	}
}

var appCategories = map[string][]CatGroup{
	"Canvas": {
		{"The canvas", []string{"canvas"}},
		{"Shapes", []string{"rect", "circle", "triangle", "text"}},
		{"Colors", []string{"rgb", "rgba"}},
		{"Groups and transforms", []string{"group", "Translate", "Rotate", "Scale", "Alpha", "Blend"}},
		{"Blend modes", []string{"Normal", "Add", "Multiply", "Screen", "Erase"}},
		{"Text alignment", []string{"Left", "Center", "Right"}},
		{"Pointer input", []string{"onTap", "onAltTap", "onDrag", "onRelease", "onHover", "onWheel", "watchPointers"}},
		{"Size", []string{"watchSize"}},
	},
	"Sound": {
		{"Make a sound", []string{"tone", "rest"}},
		{"Wave shapes", []string{"Square", "Triangle", "Sawtooth", "Noise"}},
		{"Note pitches", []string{"c", "cs", "d", "ds", "e", "f", "fs", "g", "gs", "a", "as_", "b"}},
		{"Shape a sound", []string{"attack", "release", "sweep", "holdPitch", "vibrato", "duty", "arp", "volume", "lowCut", "highCut"}},
		{"Combine sounds", []string{"chord", "sequence"}},
		{"Play it", []string{"play", "once", "loop", "voice", "glide"}},
		{"Global audio", []string{"master", "setMuted"}},
	},
	"Time": {
		{"Create", []string{"fromYMD", "fromIso", "now"}},
		{"Read the parts", []string{"year", "month", "day", "hour", "minute", "second"}},
		{"Durations", []string{"millis", "seconds", "minutes", "hours", "days", "weeks", "toSeconds"}},
		{"Shift a time", []string{"add", "sub", "addDays", "addMonths", "addYears"}},
		{"Compare", []string{"before", "after", "diff"}},
		{"Convert", []string{"toIso", "toMillis"}},
		{"Subscribe", []string{"every"}},
	},
	"Random": {
		{"Generators", []string{"int", "uniform", "constant"}},
		{"Combine", []string{"pair", "list", "map", "map2", "map3", "andThen"}},
		{"Run it", []string{"generate"}},
		{"Seeded (pure, runs anywhere)", []string{"initialSeed", "step", "seed"}},
	},
	"UI": {
		{"Text", []string{"title", "subtitle", "text", "errorText", "paragraph", "span"}},
		{"Inline styles", []string{"bold", "italic", "code", "strikethrough", "link"}},
		{"Controls", []string{"button", "toggle", "textField", "textArea", "picker", "datePicker"}},
		{"Input attributes", []string{"email", "password", "newPassword", "numeric", "numericCode", "oneTimeCode", "submit", "disabled"}},
		{"Layout", []string{"vstack", "hstack", "spacer", "centered", "align", "leading", "center", "trailing", "top", "bottom"}},
		{"Sizing", []string{"width", "height", "fill", "chars", "lines", "px", "size"}},
		{"Forms and lists", []string{"form", "section", "list", "header", "footer"}},
		{"Reorderable lists", []string{"keyed", "keyedList", "onMove", "onDelete"}},
		{"Navigation", []string{"navigationStack", "navigationTitle", "navigationLink", "topBarLeading", "topBarTrailing"}},
		{"Overlays", []string{"sheet", "confirm"}},
		{"Images", []string{"image", "fit", "cover"}},
		{"Nothing at all", []string{"empty"}},
	},
	"Page": {
		{"Static routes", []string{"create", "protected", "adminProtected"}},
		{"Routes with parameters", []string{"dynamic", "dynamicProtected", "dynamicAdminProtected"}},
		{"How a route is presented", []string{"sheet"}},
		{"Reading shared state", []string{"withShared"}},
	},
	"Nav": {
		{"Go somewhere", []string{"push", "pushTo"}},
		{"Replace where you are", []string{"replace", "replaceTo"}},
		{"Close what is open", []string{"dismiss"}},
	},
	"Cmd": {
		{"Common", []string{"none", "batch", "perform"}},
		{"Writing shared state", []string{"toShared"}},
	},
	"Sub": {
		{"Common", []string{"none", "batch"}},
	},
	"Task": {
		{"Create", []string{"succeed", "fail"}},
		{"Chain", []string{"map", "andThen"}},
		{"Many at once", []string{"sequence", "forEach"}},
	},
	"Service": {
		{"Declare and implement", []string{"declare", "implement"}},
		{"Call it", []string{"call"}},
		{"When it fails", []string{"Offline", "Unauthorized", "RateLimited", "ServerError", "errorToString"}},
	},
	"Http": {
		{"Requests", []string{"get", "post"}},
	},
	"JSON": {
		{"Convert", []string{"encode", "decode"}},
	},
	"App": {
		{"Entry points", []string{"frontend", "backend", "fullstack"}},
		{"State that outlives navigation", []string{"shared"}},
	},
	"Device": {
		{"Watch", []string{"watch"}},
		{"Ask about it", []string{"touchOnly", "canHover"}},
	},
	"Entity": {
		{"Define a table", []string{"define"}},
		{"Column types", []string{"serial", "int", "text", "bool", "decimal", "timestamp", "enum"}},
		{"Constraints", []string{"notNull"}},
	},
	"Repo": {
		{"Read", []string{"all", "findById", "findBy"}},
		{"Write", []string{"create", "update", "deleteById"}},
	},
	"Keyboard": {
		{"Watch", []string{"watch"}},
		{"Letters", keyLetters},
		{"Digits", keyDigits},
		{"Arrows", []string{"ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"}},
		{"Editing", []string{"Enter", "Space", "Backspace", "Delete", "Tab", "Escape", "Insert"}},
		{"Moving around", []string{"Home", "End", "PageUp", "PageDown"}},
		{"Modifiers", []string{"ShiftLeft", "ShiftRight", "ControlLeft", "ControlRight", "AltLeft", "AltRight", "MetaLeft", "MetaRight", "CapsLock"}},
		{"Punctuation", []string{"Backquote", "Minus", "Equal", "BracketLeft", "BracketRight", "Backslash", "Semicolon", "Quote", "Comma", "Period", "Slash"}},
		{"Function keys", keyFunctions},
		{"Numeric keypad", keyNumpad},
		{"System", []string{"PrintScreen", "ScrollLock", "Pause", "NumLock", "ContextMenu"}},
	},
	"Gamepad": {
		{"Watch", []string{"watch"}},
		{"Face buttons", []string{"A", "B", "X", "Y"}},
		{"Shoulders and triggers", []string{"L1", "L2", "R1", "R2", "L3", "R3"}},
		{"D-pad", []string{"Up", "Down", "Left", "Right"}},
		{"Menu", []string{"Select", "Start"}},
	},
	"Auth": {
		{"Configure", []string{"config"}},
		{"Sign in", []string{"requestCode", "verifyCode", "completeSignIn"}},
		{"Requesting a code", []string{"CodeSent", "InvalidEmail", "RateLimited"}},
		{"Checking a code", []string{"SignedIn", "WrongCode", "TooManyAttempts"}},
		{"The current user", []string{"me", "logout"}},
		{"Guard a service", []string{"protect", "requireRole", "requireOwner", "authorize"}},
	},
}

var appDescriptions = map[string]string{
	// Canvas
	"Canvas.canvas":        "A drawing surface, given a mode, attributes, and a draw list. Shapes are drawn in order, so later ones sit on top. Crisp smooths as it scales, Pixelated keeps hard pixel edges.",
	"Canvas.rect":          "A filled rectangle, from its top-left corner plus a width and a height.",
	"Canvas.circle":        "A filled circle, from its center and a radius.",
	"Canvas.triangle":      "A filled triangle, from its three corners in order.",
	"Canvas.text":          "A line of text at a position, with a size, an alignment, and a color. The alignment says which part of the text lands on the x you gave.",
	"Canvas.rgb":           "An opaque color from red, green, and blue, each 0 to 255.",
	"Canvas.rgba":          "A color with a fourth channel for opacity, 0 for invisible and 255 for solid. Use it per shape; for a whole layer at once, reach for Alpha instead.",
	"Canvas.group":         "Applies a list of transforms to a list of shapes. Transforms apply in order, and only inside the group, so the rest of the draw list is untouched.",
	"Canvas.Translate":     "Shifts everything in the group by x and y.",
	"Canvas.Rotate":        "Turns everything in the group by an angle in degrees, clockwise, around the group origin.",
	"Canvas.Scale":         "Resizes everything in the group, as a percentage per axis: 100 is unchanged, 200 is double, 50 is half.",
	"Canvas.Alpha":         "Fades the whole group as one layer, 0 to 100. This is not the same as giving each shape a translucent color: overlapping shapes inside the group will not show through each other.",
	"Canvas.Blend":         "Sets how the group combines with what is already drawn beneath it.",
	"Canvas.Normal":        "The default: the group covers what is under it.",
	"Canvas.Add":           "Adds the colors together, so overlaps get brighter. The one to reach for with glows, sparks, and explosions.",
	"Canvas.Multiply":      "Multiplies the colors, so overlaps get darker. Good for shadows and tinting.",
	"Canvas.Screen":        "The inverse of Multiply: lightens, but saturates towards white instead of clipping.",
	"Canvas.Erase":         "Cuts the group out of what is beneath it, leaving a hole.",
	"Canvas.Left":          "The x you gave is the left edge of the text.",
	"Canvas.Center":        "The x you gave is the middle of the text.",
	"Canvas.Right":         "The x you gave is the right edge of the text.",
	"Canvas.onTap":         "Fires when a pointer presses the canvas, with the position in canvas coordinates.",
	"Canvas.onAltTap":      "Fires on a right-click, or its touch equivalent, with the position.",
	"Canvas.onDrag":        "Fires as a pressed pointer moves across the canvas.",
	"Canvas.onRelease":     "Fires when the pointer lifts, with the position it lifted at. Pair it with onTap to measure how long something was held.",
	"Canvas.onHover":       "Fires as an unpressed pointer moves over the canvas. Nothing to hover with on a touch screen, so treat it as an enhancement.",
	"Canvas.onWheel":       "Fires on a scroll wheel or a trackpad scroll, with the horizontal and vertical amounts.",
	"Canvas.watchPointers": "Mirrors every pointer currently touching the canvas, each with an id and a position. This is the one to use for multi-touch, where two thumbs are two separate pointers.",
	"Canvas.watchSize":     "Mirrors the canvas size in pixels, and fires again whenever it changes. Read your layout off this rather than assuming a fixed size.",

	// Sound
	"Sound.tone":      "A note: a wave shape, a frequency in Hz, and a length in milliseconds. Everything else in this module either builds one of these or reshapes it.",
	"Sound.rest":      "Silence for a number of milliseconds. Useful inside a sequence to place a gap.",
	"Sound.Square":    "A hollow, buzzy wave. The classic lead and bass voice of 8-bit music.",
	"Sound.Triangle":  "A soft, rounded wave. Mellower than square, and the usual choice for a bass line.",
	"Sound.Sawtooth":  "A bright, harsh wave with every harmonic present. Cuts through a busy mix.",
	"Sound.Noise":     "No pitch at all, just noise. This is what drums, explosions, and wind are made of.",
	"Sound.c":         "The frequency of C in the given octave, so C4 is middle C.",
	"Sound.cs":        "The frequency of C sharp in the given octave.",
	"Sound.d":         "The frequency of D in the given octave.",
	"Sound.ds":        "The frequency of D sharp in the given octave.",
	"Sound.e":         "The frequency of E in the given octave.",
	"Sound.f":         "The frequency of F in the given octave.",
	"Sound.fs":        "The frequency of F sharp in the given octave.",
	"Sound.g":         "The frequency of G in the given octave.",
	"Sound.gs":        "The frequency of G sharp in the given octave.",
	"Sound.a":         "The frequency of A in the given octave, so A4 is the 440 Hz tuning reference.",
	"Sound.as_":       "The frequency of A sharp in the given octave. The trailing underscore is there because as is a reserved word.",
	"Sound.b":         "The frequency of B in the given octave.",
	"Sound.attack":    "How long the note takes to reach full volume, in milliseconds. Small values are percussive, larger ones swell in.",
	"Sound.release":   "How long the note takes to fade to silence at its end, in milliseconds. This is the tail that keeps a note from stopping abruptly.",
	"Sound.sweep":     "Glides the pitch to an end frequency over the length of the note. Sweeping down is a laser or a falling bomb, sweeping up is a power-up.",
	"Sound.holdPitch": "Holds the starting pitch for this many milliseconds before a sweep begins, so the note speaks at its true pitch first.",
	"Sound.vibrato":   "Wobbles the pitch, given a depth and a rate in Hz. A little makes a note sound played rather than generated.",
	"Sound.duty":      "The width of a square wave pulse, as a percentage. 50 is the hollow default, and moving away from it thins the tone.",
	"Sound.arp":       "Steps the pitch through a list of semitone offsets over the life of the note. The chiptune trick for a chord from a single voice.",
	"Sound.volume":    "Scales this sound alone, 0 to 100, without touching anything else that is playing.",
	"Sound.lowCut":    "Removes everything below a frequency, thinning the body out.",
	"Sound.highCut":   "Removes everything above a frequency, muffling the sound as if it were behind a wall.",
	"Sound.chord":     "Plays several sounds at the same instant, as one sound. Every voice must add up to the same total length, or the parts drift out of step.",
	"Sound.sequence":  "Plays several sounds one after another, as one sound. This is how a melody is built.",
	"Sound.play":      "Plays a sound once, right now, as a command. The one to use for a hit, a jump, or a coin.",
	"Sound.once":      "Plays a sound once as a subscription, when the subscription first appears. Because it is a subscription, it belongs to the state that asked for it.",
	"Sound.loop":      "Repeats a sound for as long as the subscription is returned. Stop returning it and the loop ends.",
	"Sound.voice":     "Holds one voice for as long as the subscription is returned, without ever restarting it. A voice's pitch is part of what makes it that voice, so returning two pitches sounds two notes at once: this is what a keyboard wants, one voice per held key.",
	"Sound.glide":     "Holds one voice whose pitch is a live setting rather than part of its identity, so handing it a new pitch slides the running note there instead of starting a second one. An engine that rises with speed, a siren. Only ever one note at a time, the way glide works on a real synth.",
	"Sound.master":    "Sets the overall output volume, 0 to 100, for everything the app plays.",
	"Sound.setMuted":  "Silences or unsilences all audio, leaving the volume setting untouched.",

	// Time
	"Time.fromYMD":   "A time at midnight on the given year, month, and day.",
	"Time.fromIso":   "Reads an ISO 8601 string, giving Nothing when it is not one.",
	"Time.now":       "The current time, as a task. It is a task rather than a value because reading the clock is an effect: two calls can disagree.",
	"Time.year":      "The year.",
	"Time.month":     "The month, 1 for January through 12 for December.",
	"Time.day":       "The day of the month, starting at 1.",
	"Time.hour":      "The hour, 0 to 23.",
	"Time.minute":    "The minute, 0 to 59.",
	"Time.second":    "The second, 0 to 59.",
	"Time.millis":    "A duration of that many milliseconds.",
	"Time.seconds":   "A duration of that many seconds.",
	"Time.minutes":   "A duration of that many minutes.",
	"Time.hours":     "A duration of that many hours.",
	"Time.days":      "A duration of that many days.",
	"Time.weeks":     "A duration of that many weeks.",
	"Time.toSeconds": "How many whole seconds a duration is.",
	"Time.add":       "Moves a time forward by a duration.",
	"Time.sub":       "Moves a time backward by a duration.",
	"Time.addDays":   "Moves a time by whole days. Negative counts move backward.",
	"Time.addMonths": "Moves a time by whole months, keeping the day of the month where the target month has one. Adding a month to the 31st lands on the last day of a shorter month.",
	"Time.addYears":  "Moves a time by whole years.",
	"Time.before":    "True when the first time is earlier than the second.",
	"Time.after":     "True when the first time is later than the second.",
	"Time.diff":      "How long it is from the first time to the second. Counting backwards gives a negative duration.",
	"Time.toIso":     "Formats a time as an ISO 8601 string. This is the form to store and to send over the wire.",
	"Time.toMillis":  "Milliseconds since the Unix epoch.",
	"Time.every":     "Fires on an interval for as long as the subscription is returned, handing over the current time. This is the heartbeat of an animation or a clock.",

	// Random
	"Random.int":         "A generator of whole numbers between a low and a high bound, both included.",
	"Random.uniform":     "A generator that picks one of the given values with equal chance. The first one is separate so the list can never be empty.",
	"Random.constant":    "A generator that always produces the same value. Useful as the boring branch of an andThen.",
	"Random.pair":        "Combines two generators into one that produces both as a tuple.",
	"Random.list":        "A generator of a list of a given length, drawing each element from the given generator.",
	"Random.map":         "Transforms whatever a generator produces.",
	"Random.map2":        "Combines two generators with a function of two arguments.",
	"Random.map3":        "Combines three generators with a function of three arguments.",
	"Random.andThen":     "Chains generators, where the second one depends on what the first produced. This is how you roll a die and then draw that many cards.",
	"Random.generate":    "Turns a generator into a command that produces a value and hands it back as a message. Nothing is random until this runs, which is what keeps generators themselves pure and reusable.",
	"Random.initialSeed": "Makes a Seed from any Int. The same Int always makes the same Seed, so a whole run is reproducible from one number — which is how a shared game replays identically on the server and the client.",
	"Random.step":        "Runs a generator against a Seed, purely: it returns the value AND the next Seed to thread into the following draw. Unlike generate it needs no command and runs on any side, so the server can shuffle a deck fairly.",
	"Random.seed":        "A task that reads real operating-system entropy and returns a fresh, unpredictable Seed. It runs on the client and the server, so it is the honest source for a shuffle nobody can guess.",

	// UI
	"UI.title":           "A large heading, for the one thing a screen is about.",
	"UI.subtitle":        "Secondary text, smaller and dimmer, for the line under something.",
	"UI.text":            "A run of text. The attribute list is where size, width, and alignment go.",
	"UI.errorText":       "Text styled as a problem. Use it for the reason something failed, next to the thing that failed.",
	"UI.paragraph":       "Flowing text built from inline pieces, so styling can change mid-sentence without breaking the line.",
	"UI.span":            "One inline piece of a paragraph, with its own styles.",
	"UI.bold":            "Heavier text.",
	"UI.italic":          "Slanted text.",
	"UI.code":            "Monospaced text, for names of things in code.",
	"UI.strikethrough":   "Text with a line through it, for what no longer applies.",
	"UI.link":            "Turns an inline piece into a link to a URL.",
	"UI.button":          "A tappable button that sends a message. The message is a value, not a callback, so what the button does is decided in update.",
	"UI.toggle":          "An on-off switch with a label, sending the new state when it flips.",
	"UI.textField":       "A single-line text input, given a placeholder, its current value, and what to send when it changes.",
	"UI.textArea":        "A multi-line text input. Give it a height in lines to control how tall it starts.",
	"UI.picker":          "A chooser over a list of options, given the current one, how to label each, and what to send on a change. The options are your own values, not strings, so nothing has to be parsed back.",
	"UI.datePicker":      "A date chooser, given the current date and what to send when it changes.",
	"UI.email":           "Marks an input as an email address, so the keyboard and autofill match.",
	"UI.password":        "Marks an input as a password, hiding what is typed and offering the saved one.",
	"UI.newPassword":     "Marks an input as a password being chosen, so a password manager offers to generate and save one.",
	"UI.numeric":         "Marks an input as a number, bringing up the number keyboard.",
	"UI.numericCode":     "Marks an input as a numeric code, like a PIN.",
	"UI.oneTimeCode":     "Marks an input as a one-time code, so a code that just arrived by mail or message is offered for autofill.",
	"UI.submit":          "The message to send when the field is submitted, from the keyboard return key or an enter press.",
	"UI.disabled":        "Greys a control out and stops it responding.",
	"UI.vstack":          "Stacks views top to bottom.",
	"UI.hstack":          "Places views side by side. It hugs its contents; add a spacer to push them apart.",
	"UI.spacer":          "Takes up whatever room is left, pushing everything after it to the far end.",
	"UI.centered":        "Centers a single view in the space it is given.",
	"UI.align":           "Sets how a stack lines its children up across the other axis.",
	"UI.leading":         "Line up at the start edge.",
	"UI.center":          "Line up in the middle.",
	"UI.trailing":        "Line up at the end edge.",
	"UI.top":             "Line up at the top.",
	"UI.bottom":          "Line up at the bottom.",
	"UI.width":           "Sets how wide a view is.",
	"UI.height":          "Sets how tall a view is.",
	"UI.fill":            "Take all the room available on this axis.",
	"UI.chars":           "A width measured in characters, so a field sized for a postcode stays that size as the font changes.",
	"UI.lines":           "A height measured in lines of text.",
	"UI.px":              "An exact number of pixels. Used for images, where the real size matters.",
	"UI.size":            "Sets an image to an exact width and height.",
	"UI.form":            "A screen made of sections, laid out the way a settings screen is.",
	"UI.section":         "A group of rows inside a form or list, optionally with a header above and a footer below.",
	"UI.list":            "A plain list of rows, without a form's grouping.",
	"UI.header":          "A label above a section.",
	"UI.footer":          "Explanatory text under a section. The place for the sentence that would otherwise clutter a label.",
	"UI.keyed":           "Attaches a stable identity to a row, so the runtime can tell a moved row from a changed one.",
	"UI.keyedList":       "A list of keyed rows. Rows can be reordered and deleted, and because each carries a key, they animate to their new place instead of being redrawn.",
	"UI.onMove":          "Lets rows be dragged, sending the index they came from and the one they went to. The Bool is whether it is currently allowed.",
	"UI.onDelete":        "Lets rows be deleted, sending the index. The Bool is whether it is currently allowed.",
	"UI.navigationStack": "The root of a screen that can push other screens, holding the title and the toolbar.",
	"UI.navigationTitle": "The title shown at the top of a navigation stack.",
	"UI.navigationLink":  "A row that pushes another page when tapped, addressed by a typed path and its parameters. The parameters are checked at compile time, so a link cannot point at a route that does not exist.",
	"UI.topBarLeading":   "Puts a view at the leading end of the toolbar.",
	"UI.topBarTrailing":  "Puts a view at the trailing end of the toolbar.",
	"UI.sheet":           "A panel that slides up over the screen while open is true, sending onDismiss when it is closed.",
	"UI.confirm":         "A confirmation dialog. Mark it destructive when the action cannot be undone, and the confirm button is styled as a warning.",
	"UI.image":           "An image, given a source and alt text. The alt text is required rather than optional, because an image without it is unusable to anyone who cannot see it.",
	"UI.fit":             "Scale the image until it fits entirely, leaving space if the shapes disagree.",
	"UI.cover":           "Scale the image until it fills the space, cropping the overflow.",
	"UI.empty":           "A view that renders nothing. The honest branch of an if that sometimes shows something.",

	// Page
	"Page.create":                "A page at a fixed path, with its own model, update, view, and subscriptions. This is the ordinary one.",
	"Page.protected":             "A page that requires a signed-in user. The user is handed to init, update, view, and subscriptions, so there is no way to render the page without one.",
	"Page.adminProtected":        "A page that requires an admin session, for screens that manage the app itself.",
	"Page.dynamic":               "A page whose path carries parameters, like an item id. The parameters arrive as a record, already parsed and typed.",
	"Page.dynamicProtected":      "A page with both path parameters and a required signed-in user.",
	"Page.sheet":                 "Present a page over the screen it was reached from, instead of pushing it. For a task the reader finishes or abandons. Opened cold it renders full-screen.",
	"Page.withShared":            "Wrap any page so it can read the app-wide state built by App.shared. The builder runs again whenever that state changes, so the page always sees the current value.",
	"Page.dynamicAdminProtected": "A page with both path parameters and a required admin session.",

	// Nav
	"Nav.push":      "Goes to a path, adding it to the history so back returns here.",
	"Nav.pushTo":    "Goes to a typed path with its parameters, checked at compile time.",
	"Nav.replace":   "Goes to a path without adding to the history, so back skips over the screen being left.",
	"Nav.dismiss":   "Closes a route being presented as a sheet (see Page.sheet), or steps back one screen. Does nothing at the first screen.",
	"App.shared":    "Build the one app-wide model that outlives navigation: loaded once, readable from any page, and unaffected by moving between screens. Pages read it with Page.withShared and change it with Cmd.toShared.",
	"Cmd.toShared":  "Send a message to the app-wide state built by App.shared. A page never assigns that state; it asks, and the shared update decides.",
	"Nav.replaceTo": "Replaces the current screen with a typed path and its parameters.",

	// Cmd
	"Cmd.none":    "Do nothing. What update returns when the message changed the model and nothing else.",
	"Cmd.batch":   "Runs several commands together. There is no ordering between them.",
	"Cmd.perform": "Runs a task and hands the result back as a message. This is the bridge from the task world into update.",

	// Sub
	"Sub.none":  "Subscribe to nothing.",
	"Sub.batch": "Subscribes to several things at once.",

	// Task
	"Task.succeed":  "A task that immediately produces a value. Useful as the trivial branch of a chain.",
	"Task.fail":     "A task that immediately fails with a message.",
	"Task.map":      "Transforms the value a task produces, leaving failure alone.",
	"Task.andThen":  "Chains tasks, where the second depends on the first result. A failure anywhere short-circuits the rest.",
	"Task.sequence": "Runs a list of tasks and collects their results in order.",
	"Task.forEach":  "Runs a task for each item in a list, for the effects rather than the results.",

	// Service
	"Service.declare":       "Declares an endpoint by verb and path. Both sides of the app share this one declaration, which is why a call and its implementation cannot disagree about the shape of a request.",
	"Service.implement":     "Gives a declared service its backend behaviour, as a task over the request.",
	"Service.call":          "Calls a declared service from the frontend, handing the outcome back as a message. The result carries a typed error rather than a string, so every failure has to be handled by name.",
	"Service.Offline":       "The request never reached the server.",
	"Service.Unauthorized":  "The server refused because nobody is signed in, or the signed-in user may not do this.",
	"Service.RateLimited":   "The server is asking for fewer requests. Wait and try again.",
	"Service.ServerError":   "The server failed, with a message describing how.",
	"Service.errorToString": "Turns a service error into a sentence fit to show a person.",

	// Http
	"Http.get":  "Fetches a URL from the frontend, handing back the body or a failure message. This is the escape hatch for talking to somebody else's server; for your own backend, use Service. It exists on the client only: a backend service cannot make an outbound call, so anything needing a secret key has to be reached some other way.",
	"Http.post": "Posts a body to a URL from the frontend, handing back the response or a failure message. Client only, like get.",

	// JSON
	"JSON.encode": "Turns a value into JSON text.",
	"JSON.decode": "Reads JSON text into a value, giving Err with a reason when it does not fit the type expected of it.",

	// App
	"App.frontend":  "An app that is only a frontend: a list of pages, no server, no database.",
	"App.backend":   "An app that is only a backend: a list of exposed services.",
	"App.fullstack": "An app that is both, sharing types and service declarations across the two halves.",

	// Device
	"Device.watch":     "Mirrors what kind of device this is and stays current: the pointer, the screen size, and whether dark mode or reduced motion is asked for. Read capabilities from here rather than guessing from a user agent string.",
	"Device.touchOnly": "True when the only pointer is a finger. The test for whether to show touch controls.",
	"Device.canHover":  "True when there is a pointer that can hover, so a hover state is worth showing at all.",

	// Entity
	"Entity.define":    "Defines a table: a name, its columns, and any groups of columns that have to be unique together. Migrations are derived from this, so the schema follows the code rather than being kept in step by hand.",
	"Entity.serial":    "An integer primary key the database fills in.",
	"Entity.int":       "A whole-number column.",
	"Entity.text":      "A text column.",
	"Entity.bool":      "A true-or-false column.",
	"Entity.decimal":   "An exact decimal column, given how many digits to keep after the point. This is the one for money, where the small errors of a floating-point number are not acceptable.",
	"Entity.timestamp": "A point-in-time column.",
	"Entity.enum":      "A column limited to one of a fixed set of values.",
	"Entity.notNull":   "Requires the column to have a value.",

	// Repo
	"Repo.all":        "Every row of a table.",
	"Repo.findById":   "The row with this id, or Nothing when there is none.",
	"Repo.findBy":     "Every row matching the given fields.",
	"Repo.create":     "Inserts a row and gives back the stored version, including whatever the database filled in.",
	"Repo.update":     "Updates the given fields of one row, giving back the new version, or Nothing when the id matched nothing. Fields you leave out are left alone.",
	"Repo.deleteById": "Deletes the row with this id.",

	// Keyboard and Gamepad. The key entries are filled in by registerKeys; only
	// the two watches and the named buttons are authored.
	"Keyboard.watch": "Mirrors which keys are held right now, and fires again whenever that changes. It is a mirror of state rather than a stream of presses, so a key held across frames stays in the list instead of arriving once.",
	"Gamepad.watch":  "Mirrors the state of the first connected controller: whether one is there, both sticks as values from -100 to 100, and which buttons are held.",
	"Gamepad.A":      "The lower face button. Confirm, jump.",
	"Gamepad.B":      "The right face button. Cancel, back.",
	"Gamepad.X":      "The left face button.",
	"Gamepad.Y":      "The upper face button.",
	"Gamepad.L1":     "The left shoulder button.",
	"Gamepad.L2":     "The left trigger.",
	"Gamepad.L3":     "Pressing the left stick in.",
	"Gamepad.R1":     "The right shoulder button.",
	"Gamepad.R2":     "The right trigger.",
	"Gamepad.R3":     "Pressing the right stick in.",
	"Gamepad.Up":     "Up on the d-pad.",
	"Gamepad.Down":   "Down on the d-pad.",
	"Gamepad.Left":   "Left on the d-pad.",
	"Gamepad.Right":  "Right on the d-pad.",
	"Gamepad.Select": "The select or view button.",
	"Gamepad.Start":  "The start or menu button.",

	// Auth
	"Auth.config":          "Turns on sign-in for the app and says how codes are sent.",
	"Auth.requestCode":     "Asks the server to mail a sign-in code to an address.",
	"Auth.verifyCode":      "Sends a code back to be checked, signing the person in when it matches.",
	"Auth.completeSignIn":  "Finishes signing in and moves on to the app, resetting the navigation history so back does not return to the sign-in screen.",
	"Auth.CodeSent":        "The code is on its way.",
	"Auth.InvalidEmail":    "That address is not one.",
	"Auth.RateLimited":     "Too many codes asked for too quickly. Wait before asking again.",
	"Auth.SignedIn":        "The code matched, carrying the user who is now signed in.",
	"Auth.WrongCode":       "That code is not the one, or it has expired.",
	"Auth.TooManyAttempts": "Too many wrong codes. Start over with a fresh one.",
	"Auth.me":              "Asks who is signed in, giving Nothing when nobody is.",
	"Auth.logout":          "Ends the session.",
	"Auth.protect":         "Requires a signed-in user for a service, and hands that user to the implementation. Rejection happens before your code runs.",
	"Auth.requireRole":     "Further requires the signed-in user to hold a role.",
	"Auth.requireOwner":    "Further requires the signed-in user to own the row being touched, given how to find the row and how to read its owner off it.",
	"Auth.authorize":       "The general form behind requireOwner: find the row a request is about, then decide whether this user may have it.",
}

var appExamples = map[string][]string{
	// Canvas
	"Canvas.canvas":        {"Canvas.canvas Pixelated [] [ Canvas.rect 0 0 64 64 (Canvas.rgb 16 16 24) ]"},
	"Canvas.rect":          {"Canvas.rect 8 8 32 16 (Canvas.rgb 220 40 40)"},
	"Canvas.circle":        {"Canvas.circle 64 64 12 (Canvas.rgb 255 220 0)"},
	"Canvas.triangle":      {"Canvas.triangle 0 12 8 0 16 12 (Canvas.rgb 90 200 120)"},
	"Canvas.text":          {"Canvas.text 64 20 12 Canvas.Center (Canvas.rgb 255 255 255) \"SCORE\""},
	"Canvas.rgb":           {"Canvas.rgb 220 40 40"},
	"Canvas.rgba":          {"Canvas.rect 0 0 64 64 (Canvas.rgba 0 0 0 128)"},
	"Canvas.group":         {"Canvas.group [ Canvas.Translate 40 20 ] [ Canvas.circle 0 0 8 (Canvas.rgb 255 255 255) ]"},
	"Canvas.Translate":     {"Canvas.Translate 40 20"},
	"Canvas.Rotate":        {"Canvas.group [ Canvas.Rotate 90 ] [ Canvas.rect 0 0 16 4 (Canvas.rgb 255 255 255) ]"},
	"Canvas.Scale":         {"Canvas.Scale 200 200"},
	"Canvas.Alpha":         {"Canvas.group [ Canvas.Alpha 50 ] [ Canvas.circle 0 0 8 (Canvas.rgb 255 255 255) ]"},
	"Canvas.Blend":         {"Canvas.group [ Canvas.Blend Canvas.Add ] [ Canvas.circle 0 0 8 (Canvas.rgb 255 180 60) ]"},
	"Canvas.Normal":        {"Canvas.Blend Canvas.Normal"},
	"Canvas.Add":           {"Canvas.Blend Canvas.Add"},
	"Canvas.Multiply":      {"Canvas.Blend Canvas.Multiply"},
	"Canvas.Screen":        {"Canvas.Blend Canvas.Screen"},
	"Canvas.Erase":         {"Canvas.Blend Canvas.Erase"},
	"Canvas.Left":          {"Canvas.text 8 20 12 Canvas.Left (Canvas.rgb 255 255 255) \"HP\""},
	"Canvas.Center":        {"Canvas.text 64 20 12 Canvas.Center (Canvas.rgb 255 255 255) \"PAUSED\""},
	"Canvas.Right":         {"Canvas.text 120 20 12 Canvas.Right (Canvas.rgb 255 255 255) \"99\""},
	"Canvas.onTap":         {"Canvas.onTap (\\x y -> (x, y))"},
	"Canvas.onAltTap":      {"Canvas.onAltTap (\\x y -> (x, y))"},
	"Canvas.onDrag":        {"Canvas.onDrag (\\x y -> (x, y))"},
	"Canvas.onRelease":     {"Canvas.onRelease (\\x y -> (x, y))"},
	"Canvas.onHover":       {"Canvas.onHover (\\x y -> (x, y))"},
	"Canvas.onWheel":       {"Canvas.onWheel (\\dx dy -> dy)"},
	"Canvas.watchPointers": {"type Msg = PointersMoved (List { id : Int, x : Int, y : Int })\n\npointers = Canvas.watchPointers PointersMoved"},
	"Canvas.watchSize":     {"Canvas.watchSize (\\w h -> (w, h))"},

	// Sound
	"Sound.tone":      {"Sound.tone Sound.Square (Sound.a 4) 120"},
	"Sound.rest":      {"Sound.sequence [ Sound.tone Sound.Square (Sound.c 4) 100, Sound.rest 50, Sound.tone Sound.Square (Sound.g 4) 100 ]"},
	"Sound.Square":    {"Sound.tone Sound.Square (Sound.c 4) 120"},
	"Sound.Triangle":  {"Sound.tone Sound.Triangle (Sound.c 2) 240"},
	"Sound.Sawtooth":  {"Sound.tone Sound.Sawtooth (Sound.e 4) 120"},
	"Sound.Noise":     {"Sound.tone Sound.Noise 200 60"},
	"Sound.c":         {"Sound.c 4 == 262"},
	"Sound.cs":        {"Sound.tone Sound.Square (Sound.cs 4) 120"},
	"Sound.d":         {"Sound.tone Sound.Square (Sound.d 4) 120"},
	"Sound.ds":        {"Sound.tone Sound.Square (Sound.ds 4) 120"},
	"Sound.e":         {"Sound.e 4 == 330"},
	"Sound.f":         {"Sound.tone Sound.Square (Sound.f 4) 120"},
	"Sound.fs":        {"Sound.tone Sound.Square (Sound.fs 4) 120"},
	"Sound.g":         {"Sound.g 3 == 196"},
	"Sound.gs":        {"Sound.tone Sound.Square (Sound.gs 4) 120"},
	"Sound.a":         {"Sound.a 4 == 440"},
	"Sound.as_":       {"Sound.tone Sound.Square (Sound.as_ 4) 120"},
	"Sound.b":         {"Sound.tone Sound.Square (Sound.b 4) 120"},
	"Sound.attack":    {"Sound.attack 40 (Sound.tone Sound.Triangle (Sound.c 4) 400)"},
	"Sound.release":   {"Sound.release 120 (Sound.tone Sound.Triangle (Sound.c 4) 400)"},
	"Sound.sweep":     {"Sound.sweep 80 (Sound.tone Sound.Sawtooth 900 200)"},
	"Sound.holdPitch": {"Sound.holdPitch 60 (Sound.sweep 80 (Sound.tone Sound.Sawtooth 900 200))"},
	"Sound.vibrato":   {"Sound.vibrato 8 6 (Sound.tone Sound.Triangle (Sound.a 4) 500)"},
	"Sound.duty":      {"Sound.duty 25 (Sound.tone Sound.Square (Sound.c 5) 120)"},
	"Sound.arp":       {"Sound.arp [ 0, 4, 7 ] (Sound.tone Sound.Square (Sound.c 4) 240)"},
	"Sound.volume":    {"Sound.volume 40 (Sound.tone Sound.Noise 200 60)"},
	"Sound.lowCut":    {"Sound.lowCut 400 (Sound.tone Sound.Sawtooth (Sound.c 3) 200)"},
	"Sound.highCut":   {"Sound.highCut 900 (Sound.tone Sound.Sawtooth (Sound.c 4) 200)"},
	"Sound.chord":     {"Sound.chord [ Sound.tone Sound.Square (Sound.c 4) 300, Sound.tone Sound.Square (Sound.e 4) 300, Sound.tone Sound.Square (Sound.g 4) 300 ]"},
	"Sound.sequence":  {"Sound.sequence [ Sound.tone Sound.Square (Sound.c 4) 120, Sound.tone Sound.Square (Sound.e 4) 120 ]"},
	"Sound.play":      {"Sound.play (Sound.tone Sound.Square (Sound.c 5) 80)"},
	"Sound.once":      {"Sound.once (Sound.tone Sound.Noise 200 60)"},
	"Sound.loop":      {"Sound.loop (Sound.tone Sound.Triangle (Sound.c 2) 500)"},
	"Sound.voice":     {"Sound.voice (Sound.tone Sound.Sawtooth (Sound.a 3) 400)"},
	"Sound.glide":     {"Sound.glide (Sound.tone Sound.Triangle (Sound.a 2) 400)"},
	"Sound.master":    {"Sound.master 70"},
	"Sound.setMuted":  {"Sound.setMuted True"},

	// Time
	"Time.fromYMD":   {"Time.year (Time.fromYMD 2026 7 22) == 2026"},
	"Time.fromIso":   {"Maybe.map Time.year (Time.fromIso \"2026-07-22T00:00:00Z\") == Just 2026"},
	"Time.now":       {"Cmd.perform (\\t -> Time.year t) Time.now"},
	"Time.year":      {"Time.year (Time.fromYMD 2026 7 22) == 2026"},
	"Time.month":     {"Time.month (Time.fromYMD 2026 7 22) == 7"},
	"Time.day":       {"Time.day (Time.fromYMD 2026 7 22) == 22"},
	"Time.hour":      {"Time.hour (Time.fromYMD 2026 7 22) == 0"},
	"Time.minute":    {"Time.minute (Time.fromYMD 2026 7 22) == 0"},
	"Time.second":    {"Time.second (Time.fromYMD 2026 7 22) == 0"},
	"Time.millis":    {"Time.toSeconds (Time.millis 2500) == 2"},
	"Time.seconds":   {"Time.toSeconds (Time.seconds 90) == 90"},
	"Time.minutes":   {"Time.toSeconds (Time.minutes 2) == 120"},
	"Time.hours":     {"Time.toSeconds (Time.hours 1) == 3600"},
	"Time.days":      {"Time.toSeconds (Time.days 1) == 86400"},
	"Time.weeks":     {"Time.toSeconds (Time.weeks 1) == 604800"},
	"Time.toSeconds": {"Time.toSeconds (Time.minutes 3) == 180"},
	"Time.add":       {"Time.day (Time.add (Time.fromYMD 2026 7 22) (Time.days 3)) == 25"},
	"Time.sub":       {"Time.day (Time.sub (Time.fromYMD 2026 7 22) (Time.days 1)) == 21"},
	"Time.addDays":   {"Time.month (Time.addDays (Time.fromYMD 2026 7 30) 3) == 8"},
	"Time.addMonths": {"Time.month (Time.addMonths (Time.fromYMD 2026 7 22) 1) == 8"},
	"Time.addYears":  {"Time.year (Time.addYears (Time.fromYMD 2026 7 22) 4) == 2030"},
	"Time.before":    {"Time.before (Time.fromYMD 2026 1 1) (Time.fromYMD 2026 7 22) == True"},
	"Time.after":     {"Time.after (Time.fromYMD 2026 7 22) (Time.fromYMD 2026 1 1) == True"},
	"Time.diff":      {"Time.toSeconds (Time.diff (Time.fromYMD 2026 7 22) (Time.fromYMD 2026 7 23)) == 86400"},
	"Time.toIso":     {"String.startsWith \"2026-07-22\" (Time.toIso (Time.fromYMD 2026 7 22)) == True"},
	"Time.toMillis":  {"Time.toMillis (Time.fromYMD 1970 1 1) == 0"},
	"Time.every":     {"type Msg = Tick Time\n\nsubscriptions model = Time.every (Time.seconds 1) Tick"},

	// Random
	"Random.int":         {"Random.int 1 6"},
	"Random.uniform":     {"Random.uniform \"rock\" [ \"paper\", \"scissors\" ]"},
	"Random.constant":    {"Random.constant 7"},
	"Random.pair":        {"Random.pair (Random.int 1 6) (Random.int 1 6)"},
	"Random.list":        {"Random.list 5 (Random.int 1 6)"},
	"Random.map":         {"Random.map (\\n -> n * 10) (Random.int 1 6)"},
	"Random.map2":        {"Random.map2 (\\a b -> a + b) (Random.int 1 6) (Random.int 1 6)"},
	"Random.map3":        {"Random.map3 (\\a b c -> a + b + c) (Random.int 1 6) (Random.int 1 6) (Random.int 1 6)"},
	"Random.andThen":     {"Random.andThen (\\n -> Random.list n (Random.int 1 6)) (Random.int 1 3)"},
	"Random.generate":    {"Random.generate (\\n -> n) (Random.int 1 6)"},
	"Random.initialSeed": {"Random.initialSeed 42"},
	"Random.step":        {"Random.step (Random.int 1 6) (Random.initialSeed 42)"},
	"Random.seed":        {"Task.map (\\s -> Random.step (Random.int 1 6) s) Random.seed"},

	// UI
	"UI.title":           {"UI.title \"Settings\""},
	"UI.subtitle":        {"UI.subtitle \"Signed in as you@example.com\""},
	"UI.text":            {"UI.text [] \"Hello\"", "UI.text [ UI.width UI.fill ] \"Stretches across the row\""},
	"UI.errorText":       {"UI.errorText \"That code has expired.\""},
	"UI.paragraph":       {"UI.paragraph [ UI.span [] \"Built with \", UI.span [ UI.bold ] \"Mar\" ]"},
	"UI.span":            {"UI.span [ UI.italic ] \"quietly\""},
	"UI.bold":            {"UI.span [ UI.bold ] \"important\""},
	"UI.italic":          {"UI.span [ UI.italic ] \"aside\""},
	"UI.code":            {"UI.span [ UI.code ] \"List.map\""},
	"UI.strikethrough":   {"UI.span [ UI.strikethrough ] \"9.99\""},
	"UI.link":            {"UI.span [ UI.link \"https://mar-lang.dev\" ] \"the site\""},
	"UI.button":          {"UI.button [] 0 \"Save\""},
	"UI.toggle":          {"UI.toggle [] \"Dark mode\" True (\\on -> on)"},
	"UI.textField":       {"UI.textField [] \"Email\" \"\" (\\s -> s)"},
	"UI.textArea":        {"UI.textArea [ UI.height (UI.lines 4) ] \"Notes\" \"\" (\\s -> s)"},
	"UI.picker":          {"UI.picker [] 1 [ 1, 2, 3 ] (\\n -> String.fromInt n) (\\n -> n)"},
	"UI.datePicker":      {"UI.datePicker [] (Time.fromYMD 2026 7 22) (\\t -> t)"},
	"UI.email":           {"UI.textField [ UI.email ] \"Email\" \"\" (\\s -> s)"},
	"UI.password":        {"UI.textField [ UI.password ] \"Password\" \"\" (\\s -> s)"},
	"UI.newPassword":     {"UI.textField [ UI.newPassword ] \"Choose a password\" \"\" (\\s -> s)"},
	"UI.numeric":         {"UI.textField [ UI.numeric ] \"Quantity\" \"\" (\\s -> s)"},
	"UI.numericCode":     {"UI.textField [ UI.numericCode ] \"PIN\" \"\" (\\s -> s)"},
	"UI.oneTimeCode":     {"UI.textField [ UI.oneTimeCode ] \"Code\" \"\" (\\s -> s)"},
	"UI.submit":          {"UI.textField [ UI.submit 0 ] \"Email\" \"\" (\\s -> s)"},
	"UI.disabled":        {"UI.button [ UI.disabled True ] 0 \"Save\""},
	"UI.vstack":          {"UI.vstack [] [ UI.title \"Mar\", UI.subtitle \"A friendly language\" ]"},
	"UI.hstack":          {"UI.hstack [] [ UI.text [] \"Total\", UI.spacer, UI.text [] \"42\" ]"},
	"UI.spacer":          {"UI.hstack [] [ UI.text [] \"Left\", UI.spacer, UI.text [] \"Right\" ]"},
	"UI.centered":        {"UI.centered (UI.text [] \"Loading\")"},
	"UI.align":           {"UI.vstack [ UI.align UI.center ] [ UI.text [] \"Centered\" ]"},
	"UI.leading":         {"UI.align UI.leading"},
	"UI.center":          {"UI.align UI.center"},
	"UI.trailing":        {"UI.align UI.trailing"},
	"UI.top":             {"UI.align UI.top"},
	"UI.bottom":          {"UI.align UI.bottom"},
	"UI.width":           {"UI.text [ UI.width (UI.chars 6) ] \"SW1A\""},
	"UI.height":          {"UI.textArea [ UI.height (UI.lines 6) ] \"Notes\" \"\" (\\s -> s)"},
	"UI.fill":            {"UI.text [ UI.width UI.fill ] \"Takes the whole row\""},
	"UI.chars":           {"UI.width (UI.chars 8)"},
	"UI.lines":           {"UI.height (UI.lines 3)"},
	"UI.px":              {"UI.size (UI.px 64) (UI.px 64)"},
	"UI.size":            {"UI.image [ UI.size (UI.px 64) (UI.px 64) ] { src = \"/logo.png\", alt = \"The Mar logo\" }"},
	"UI.form":            {"UI.form [ UI.section [ UI.header \"Account\" ] [ UI.text [] \"you@example.com\" ] ]"},
	"UI.section":         {"UI.section [ UI.header \"Account\", UI.footer \"We never share this.\" ] [ UI.text [] \"you@example.com\" ]"},
	"UI.list":            {"UI.list [] [ UI.text [] \"One\", UI.text [] \"Two\" ]"},
	"UI.header":          {"UI.section [ UI.header \"Danger zone\" ] [ UI.button [] 0 \"Delete account\" ]"},
	"UI.footer":          {"UI.section [ UI.footer \"Codes expire after ten minutes.\" ] [ UI.text [] \"Check your mail\" ]"},
	"UI.keyed":           {"UI.keyed \"task-1\" (UI.text [] \"Buy milk\")"},
	"UI.keyedList":       {"UI.keyedList [] [ UI.keyed \"task-1\" (UI.text [] \"Buy milk\") ]"},
	"UI.onMove":          {"UI.onMove True (\\from to -> (from, to))"},
	"UI.onDelete":        {"UI.onDelete True (\\index -> index)"},
	"UI.navigationStack": {"UI.navigationStack [ UI.navigationTitle \"Inbox\" ] [ UI.text [] \"Nothing here yet\" ]"},
	"UI.navigationTitle": {"UI.navigationStack [ UI.navigationTitle \"Settings\" ] []"},
	"UI.navigationLink":  {"moduleDoc : Path { moduleName : String }\nmoduleDoc = \"/reference/{moduleName:String}\"\n\nrow = UI.navigationLink [] moduleDoc { moduleName = \"List\" } (UI.text [] \"List\")"},
	"UI.topBarLeading":   {"UI.navigationStack [ UI.topBarLeading (UI.button [] 0 \"Cancel\") ] []"},
	"UI.topBarTrailing":  {"UI.navigationStack [ UI.topBarTrailing (UI.button [] 0 \"Edit\") ] []"},
	"UI.sheet":           {"UI.sheet { open = True, onDismiss = 0, outlet = \"editor\" } [ UI.text [] \"Edit task\" ]"},
	"UI.confirm":         {"UI.confirm { title = \"Delete this task?\", confirmLabel = \"Delete\", destructive = True, onConfirm = 1, onCancel = 0 }"},
	"UI.image":           {"UI.image [] { src = \"/logo.png\", alt = \"The Mar logo\" }"},
	"UI.fit":             {"UI.image [ UI.fit ] { src = \"/logo.png\", alt = \"The Mar logo\" }"},
	"UI.cover":           {"UI.image [ UI.cover ] { src = \"/hero.png\", alt = \"A night sea\" }"},
	"UI.empty":           {"UI.empty"},

	// Page. A page is a record of six fields; on one line it is a wall, so it
	// gets the shape it has in a real file.
	"Page.create":                {"page = Page.create { path = \"/\", title = \"Home\", init = (0, Cmd.none), update = \\msg model -> (model, Cmd.none), view = \\model -> UI.text [] (String.fromInt model), subscriptions = \\model -> Sub.none }"},
	"Page.protected":             {"page = Page.protected { path = \"/me\", title = \"Me\", init = \\user -> (0, Cmd.none), update = \\user msg model -> (model, Cmd.none), view = \\user model -> UI.empty, subscriptions = \\user model -> Sub.none }"},
	"Page.adminProtected":        {"page = Page.adminProtected { path = \"/admin\", title = \"Admin\", init = \\session -> (0, Cmd.none), update = \\session msg model -> (model, Cmd.none), view = \\session model -> UI.empty, subscriptions = \\session model -> Sub.none }"},
	"Page.dynamic":               {"task : Path { id : Int }\ntask = \"/task/{id:Int}\"\n\npage = Page.dynamic { path = task, title = \"Task\", init = \\params -> (params.id, Cmd.none), update = \\params msg model -> (model, Cmd.none), view = \\params model -> UI.text [] (String.fromInt params.id), subscriptions = \\params model -> Sub.none }"},
	"Page.withShared":            {"cart : App.Shared Int msg\ncart =\n    App.shared\n        { init = (0, Cmd.none)\n        , update = \\_ model -> (model, Cmd.none)\n        , subscriptions = \\_ -> Sub.none\n        }\n\npage = Page.withShared cart (\\count -> Page.create { path = \"/\", title = \"Cart\", init = ((), Cmd.none), update = \\msg model -> (model, Cmd.none), view = \\model -> UI.text [] (String.fromInt count), subscriptions = \\model -> Sub.none })"},
	"Page.sheet":                 {"page = Page.sheet (Page.create { path = \"/compose\", title = \"New message\", init = (\"\", Cmd.none), update = \\msg model -> (model, Cmd.none), view = \\model -> UI.empty, subscriptions = \\model -> Sub.none })"},
	"Page.dynamicProtected":      {"task : Path { id : Int }\ntask = \"/task/{id:Int}\"\n\npage = Page.dynamicProtected { path = task, title = \"Task\", init = \\user params -> (params.id, Cmd.none), update = \\user params msg model -> (model, Cmd.none), view = \\user params model -> UI.empty, subscriptions = \\user params model -> Sub.none }"},
	"Page.dynamicAdminProtected": {"user : Path { id : Int }\nuser = \"/admin/user/{id:Int}\"\n\npage = Page.dynamicAdminProtected { path = user, title = \"User\", init = \\session params -> (params.id, Cmd.none), update = \\session params msg model -> (model, Cmd.none), view = \\session params model -> UI.empty, subscriptions = \\session params model -> Sub.none }"},

	// Nav
	"Nav.push":      {"Nav.push \"/settings\""},
	"Nav.pushTo":    {"task : Path { id : Int }\ntask = \"/task/{id:Int}\"\n\ngoToTask = Nav.pushTo task { id = 7 }"},
	"Nav.replace":   {"Nav.replace \"/\""},
	"App.shared":    {"cart : App.Shared Int msg\ncart =\n    App.shared\n        { init = (0, Cmd.none)\n        , update = \\_ model -> (model, Cmd.none)\n        , subscriptions = \\_ -> Sub.none\n        }"},
	"Cmd.toShared":  {"cart : App.Shared Int Bool\ncart =\n    App.shared\n        { init = (0, Cmd.none)\n        , update = \\_ model -> (model, Cmd.none)\n        , subscriptions = \\_ -> Sub.none\n        }\n\nemptyIt = Cmd.toShared cart True"},
	"Nav.dismiss":   {"closeSheet : (Int, Cmd msg)\ncloseSheet = (0, Nav.dismiss)"},
	"Nav.replaceTo": {"task : Path { id : Int }\ntask = \"/task/{id:Int}\"\n\nshowTask = Nav.replaceTo task { id = 7 }"},

	// Cmd
	"Cmd.none":    {"Cmd.none"},
	"Cmd.batch":   {"Cmd.batch [ Nav.push \"/\", Sound.play (Sound.tone Sound.Square (Sound.c 5) 60) ]"},
	"Cmd.perform": {"type Msg = GotNow Time\n\nstarted = Cmd.perform GotNow Time.now"},

	// Sub
	"Sub.none":  {"Sub.none"},
	"Sub.batch": {"Sub.batch [ Time.every (Time.seconds 1) (\\t -> Time.second t), Sub.none ]"},

	// Task
	"Task.succeed":  {"Task.succeed 42"},
	"Task.fail":     {"Task.fail \"no such task\""},
	"Task.map":      {"Task.map (\\n -> n + 1) (Task.succeed 1)"},
	"Task.andThen":  {"Task.andThen (\\n -> Task.succeed (n * 2)) (Task.succeed 21)"},
	"Task.sequence": {"Task.sequence [ Task.succeed 1, Task.succeed 2 ]"},
	"Task.forEach":  {"Task.forEach (\\n -> Task.succeed ()) [ 1, 2, 3 ]"},

	// Service. The declaration is the thing both sides share, so it gets a name
	// instead of being spelled out again inside every call.
	"Service.declare":       {"Service.declare GET \"/tasks\"", "Service.declare POST \"/task/{id:Int}/done\""},
	"Service.implement":     {"listTasks = Service.declare GET \"/tasks\"\n\nservedOnTheBackend = Service.implement listTasks (\\request -> Task.succeed [])"},
	"Service.call":          {"listTasks = Service.declare GET \"/tasks\"\n\nfetchedFromTheFrontend = Service.call listTasks () (\\result -> result)"},
	"Service.Offline":       {"Service.errorToString Service.Offline"},
	"Service.Unauthorized":  {"Service.errorToString Service.Unauthorized"},
	"Service.RateLimited":   {"Service.errorToString Service.RateLimited"},
	"Service.ServerError":   {"Service.errorToString (Service.ServerError \"the database is unreachable\")"},
	"Service.errorToString": {"Service.errorToString Service.Offline"},

	// Http
	"Http.get":  {"Http.get \"https://example.com/ping\" (\\result -> result)"},
	"Http.post": {"Http.post \"https://example.com/echo\" \"hello\" (\\result -> result)"},

	// JSON
	"JSON.encode": {"JSON.encode { name = \"Ada\", born = 1815 }"},
	"JSON.decode": {"Result.withDefault 0 (JSON.decode \"1\")"},

	// App
	"App.frontend":  {"App.frontend []"},
	"App.backend":   {"App.backend { services = [] }"},
	"App.fullstack": {"App.fullstack { services = [], pages = [] }"},

	// Device
	"Device.watch":     {"type Msg = Resized Int Int\n\nsubscriptions model = Device.watch (\\device -> Resized device.width device.height)"},
	"Device.touchOnly": {"Device.watch (\\device -> Device.touchOnly device)"},
	"Device.canHover":  {"Device.watch (\\device -> Device.canHover device)"},

	// Entity
	"Entity.define":    {tasksTableOnly},
	"Entity.serial":    {"Entity.serial"},
	"Entity.int":       {"Entity.int Entity.notNull"},
	"Entity.text":      {"Entity.text Entity.notNull"},
	"Entity.bool":      {"Entity.bool Entity.notNull"},
	"Entity.decimal":   {"Entity.decimal 2 Entity.notNull"},
	"Entity.timestamp": {"Entity.timestamp Entity.notNull"},
	"Entity.enum":      {"Entity.enum [ \"todo\", \"doing\", \"done\" ] Entity.notNull"},
	"Entity.notNull":   {"Entity.text Entity.notNull"},

	// Repo. Each one declares the table it reads and then names what the call
	// is for, so the line you came to read says what it does.
	"Repo.all":        {tasksTable + "everyTask = Repo.all tasks"},
	"Repo.findById":   {tasksTable + "taskNumber7 = Repo.findById tasks 7"},
	"Repo.findBy":     {tasksTable + "stillToDo = Repo.findBy tasks { done = False }"},
	"Repo.create":     {tasksTable + "added = Repo.create tasks { title = \"Buy milk\", done = False }"},
	"Repo.update":     {tasksTable + "markedDone = Repo.update tasks 7 { done = True }"},
	"Repo.deleteById": {tasksTable + "removed = Repo.deleteById tasks 7"},

	// Keyboard and Gamepad watches. The keys and buttons are filled in by
	// registerKeys.

	// Every callback below produces the app's Msg. That is what the free `a`
	// in these signatures means, and getting it wrong is invisible to the
	// example harness: a lambda returning Bool still compiles, it just yields
	// a Sub Bool that no real app would ever write. So each example declares
	// a Msg and shows the call in the slot it belongs to.
	"Keyboard.watch": {"type Msg = KeysChanged (List Keyboard.Key)\n\nsubscriptions model = Keyboard.watch (\\keys -> KeysChanged keys.down)"},
	"Gamepad.watch":  {"type Msg = StickMoved Int Int\n\nsubscriptions model = Gamepad.watch (\\pad -> StickMoved pad.leftX pad.leftY)"},

	// Auth. The guards wrap a service that has a name, so you can see what is
	// being guarded and what the guard is.
	"Auth.config":          {"Auth.config { from = \"hello@example.com\" }"},
	"Auth.requestCode":     {"Auth.requestCode { email = \"you@example.com\" } (\\result -> result)"},
	"Auth.verifyCode":      {"Auth.verifyCode { email = \"you@example.com\", code = \"123456\" } (\\result -> result)"},
	"Auth.completeSignIn":  {"Auth.completeSignIn"},
	"Auth.CodeSent":        {"Auth.CodeSent"},
	"Auth.InvalidEmail":    {"Auth.InvalidEmail"},
	"Auth.RateLimited":     {"Auth.RateLimited"},
	"Auth.SignedIn":        {"Auth.SignedIn { email = \"you@example.com\" }"},
	"Auth.WrongCode":       {"Auth.WrongCode"},
	"Auth.TooManyAttempts": {"Auth.TooManyAttempts"},
	"Auth.me":              {"Auth.me (\\result -> result)"},
	"Auth.logout":          {"Auth.logout (\\result -> result)"},
	"Auth.protect":         {"whoAmI = Service.declare GET \"/me\"\n\nsignedInOnly = Auth.protect whoAmI (\\request user -> Task.succeed user)"},
	"Auth.requireRole":     {"stats = Service.implement (Service.declare GET \"/stats\") (\\request -> Task.succeed ())\n\nadminsOnly = Auth.requireRole \"admin\" stats"},
	"Auth.requireOwner":    {"editTask = Service.implement (Service.declare POST \"/task\") (\\request -> Task.succeed ())\n\nfindTheTask = \\request user -> Task.succeed Nothing\n\nownersOnly = Auth.requireOwner findTheTask (\\task -> task.userId) editTask"},
	"Auth.authorize":       {"editTask = Service.implement (Service.declare POST \"/task\") (\\request -> Task.succeed ())\n\nfindTheTask = \\request user -> Task.succeed Nothing\n\nmayEdit = \\request user task -> True\n\nguarded = Auth.authorize findTheTask mayEdit editTask"},
}

// appBlurbs completes the one-liners for the modules defined in this file. It
// is merged into blurbs by init, above.
var appBlurbs = map[string]string{
	"Time":     "Points in time and the distances between them.",
	"Random":   "Generators of random values, run only when you say so.",
	"Canvas":   "Draw shapes, text, and colour to a surface, and read the pointer back.",
	"Sound":    "Build a sound out of waves, shape it, and play it.",
	"UI":       "The view vocabulary: text, controls, layout, lists, and navigation.",
	"Page":     "Declare the screens of an app and what each one needs to render.",
	"Nav":      "Move between screens.",
	"Cmd":      "Things to do once, on the way out of update.",
	"Sub":      "Things to keep listening to for as long as the state asks.",
	"Task":     "Work that can fail, composed before it is run.",
	"Service":  "One endpoint declaration, shared by the frontend that calls it and the backend that answers.",
	"Http":     "Call somebody else's server, from the frontend only.",
	"JSON":     "Turn values into JSON text and back.",
	"App":      "The entry point: frontend, backend, or both.",
	"Device":   "What kind of device this is, and what it can do.",
	"Entity":   "Describe a database table; migrations follow from it.",
	"Repo":     "Read and write rows of a table.",
	"Auth":     "Sign people in by mailed code, and guard what they may reach.",
	"Keyboard": "Which keys are held right now.",
	"Gamepad":  "The sticks and buttons of a connected controller.",
}
