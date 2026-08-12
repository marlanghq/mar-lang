package refgen

// This file and content_app.go are the only hand-authored parts of the
// reference; this one covers the pure-data core. Everything else (the
// signatures, the page, the search) is generated from the compiler.
//
//   categories   — how each module's functions are grouped and ordered, like
//                  elm/core's @docs sections. Every exported function must
//                  appear in exactly one group (checked by the coverage test).
//   descriptions — a two-to-three sentence explanation per function.
//   examples     — worked `expr == expected` lines. Each is a REAL Mar
//                  equality expression that examples_test.go compiles and runs,
//                  requiring True, so a wrong example fails the build. (The
//                  app-side modules also use a compile-only form, for the
//                  things that have no value worth comparing; see
//                  content_app.go.)
//
// Descriptions and examples are emitted into Mar string literals with Go's %q,
// so quotes and backslashes inside them are escaped correctly.

var categories = map[string][]CatGroup{
	"List": {
		{"Create", []string{"range", "repeat"}},
		{"Transform", []string{"map", "indexedMap", "filter", "filterMap", "foldl", "foldr"}},
		{"Combine", []string{"concat", "concatMap", "intersperse"}},
		{"Sort", []string{"sort", "sortBy", "sortWith"}},
		{"Query", []string{"isEmpty", "length", "member", "all", "any"}},
		{"Aggregate", []string{"sum", "product", "maximum", "minimum"}},
		{"Deconstruct", []string{"head", "tail", "take", "drop", "partition"}},
		{"Rearrange", []string{"reverse", "move"}},
	},
	"String": {
		{"Build", []string{"cons", "repeat", "fromInt", "fromList"}},
		{"Transform", []string{"map", "filter", "foldl", "toUpper", "toLower", "trim", "padLeft", "padRight", "replace"}},
		{"Split and join", []string{"split", "join", "indexes"}},
		{"Query", []string{"length", "contains", "startsWith", "endsWith", "any"}},
		{"Convert", []string{"toInt", "toList"}},
	},
	"Maybe": {
		{"Common", []string{"withDefault", "map", "andThen", "filter"}},
		{"Combine", []string{"map2", "map3", "andMap"}},
	},
	"Result": {
		{"Common", []string{"withDefault", "map", "andThen"}},
		{"Errors", []string{"mapError"}},
		{"Convert", []string{"fromMaybe", "toMaybe"}},
	},
	// Basics has no module of its own: these are the builtins written bare.
	// The reference groups them under a name the language does not have, so
	// they are findable at all — see refgen.BasicsModule.
	"Basics": {
		{"Logic", []string{"not"}},
		{"Compare", []string{"max", "min", "clamp"}},
		{"Numbers", []string{"abs", "modBy", "remainderBy"}},
		{"Functions", []string{"always"}},
		{"Links", []string{"linkTo"}},
	},
	"Math": {
		{"Build an angle", []string{"degrees", "deciDegrees", "turns"}},
		{"Angle arithmetic", []string{"add", "subtract", "opposite"}},
		{"Trigonometry", []string{"sin", "cos", "atan2"}},
		{"Roots", []string{"isqrt"}},
	},
	"Tuple": {
		{"Read", []string{"first", "second"}},
		{"Build", []string{"pair"}},
		{"Transform", []string{"mapFirst", "mapSecond", "mapBoth"}},
	},
	"Char": {
		{"Convert", []string{"toCode", "fromCode", "toUpper", "toLower"}},
		{"Classify", []string{"isDigit", "isAlpha", "isUpper", "isLower"}},
	},
	"Dict": {
		{"Create", []string{"empty", "singleton", "fromList"}},
		{"Read", []string{"get", "member", "size", "isEmpty"}},
		{"Modify", []string{"insert", "remove", "update"}},
		{"Transform", []string{"map", "filter", "foldl", "foldr", "partition"}},
		{"Combine", []string{"union", "intersect", "diff"}},
		{"Deconstruct", []string{"toList", "keys", "values"}},
	},
	"Set": {
		{"Create", []string{"empty", "singleton", "fromList"}},
		{"Read", []string{"member", "size", "isEmpty"}},
		{"Modify", []string{"insert", "remove"}},
		{"Transform", []string{"map", "filter", "foldl", "foldr", "partition"}},
		{"Combine", []string{"union", "intersect", "diff"}},
		{"Deconstruct", []string{"toList"}},
	},
	"Decimal": {
		{"Create", []string{"fromInt", "fromCents", "fromString", "zero"}},
		{"Convert", []string{"toString", "toCents", "toScale"}},
		{"To a whole number", []string{"truncate", "round", "floor", "ceiling", "toIntWith"}},
		{"Division", []string{"rounded", "withRemainder"}},
		{"Rounding modes", []string{"Up", "Down", "Ceiling", "Floor", "HalfUp", "HalfEven"}},
		{"Math", []string{"abs", "negate", "compare"}},
	},
}

var descriptions = map[string]string{
	// --- List ---
	"List.range":       "Builds the list of integers from the low bound up to the high bound, inclusive. If the low bound is greater than the high bound, the result is empty.",
	"List.repeat":      "Builds a list containing the same value a given number of times.",
	"List.map":         "Applies a function to every element, producing a new list of the results. The list keeps its length and order.",
	"List.indexedMap":  "Like map, but the function also receives each element's position, counting from zero.",
	"List.filter":      "Keeps only the elements for which the test returns True, preserving their order.",
	"List.filterMap":   "Maps each element to a Maybe and keeps the values that come back as Just, dropping the Nothings. It is map and filter in a single pass, handy for parsing.",
	"List.foldl":       "Reduces the list to a single value by walking it left to right, threading an accumulator. The step function takes the element first, then the accumulator.",
	"List.foldr":       "Reduces the list to a single value by walking it right to left, threading an accumulator. The step function takes the element first, then the accumulator.",
	"List.concat":      "Flattens a list of lists into one list, keeping order.",
	"List.concatMap":   "Maps each element to a list and concatenates all the results. Use it when each element expands into zero or more elements.",
	"List.intersperse": "Puts a separator value between every pair of elements.",
	"List.sort":        "Sorts values from lowest to highest. Works on any comparable elements: numbers, strings, characters, and tuples of those.",
	"List.sortBy":      "Sorts the elements by a key computed from each one, from lowest key to highest.",
	"List.sortWith":    "Sorts using a custom comparison that returns an Order (LT, EQ, or GT) for any two elements. Reach for this when neither sort nor sortBy expresses the ordering you need.",
	"List.isEmpty":     "True when the list has no elements.",
	"List.length":      "The number of elements in the list.",
	"List.member":      "True when the value appears somewhere in the list.",
	"List.all":         "True when every element passes the test. True for the empty list, since there is nothing to fail.",
	"List.any":         "True when at least one element passes the test.",
	"List.sum":         "Adds all the numbers together. Works on a list of Int or a list of Decimal, whichever the context calls for; the sum of the empty list is zero.",
	"List.product":     "Multiplies all the numbers together. Works on a list of Int or a list of Decimal, whichever the context calls for; the product of the empty list is one.",
	"List.maximum":     "The largest element, or Nothing when the list is empty.",
	"List.minimum":     "The smallest element, or Nothing when the list is empty.",
	"List.head":        "The first element, or Nothing when the list is empty.",
	"List.tail":        "Everything after the first element, or Nothing when the list is empty. Note that the tail of a one-element list is Just the empty list, not Nothing.",
	"List.take":        "Keeps the first n elements. Taking more than the list holds keeps them all.",
	"List.drop":        "Drops the first n elements. Dropping more than the list holds leaves the empty list.",
	"List.partition":   "Splits the list into a pair: the elements that pass the test, and the elements that do not, each keeping its order.",
	"List.reverse":     "The same elements in the opposite order.",
	"List.move":        "Moves the element at one index to another, shifting the elements in between. An index outside the list leaves it unchanged. This backs drag-to-reorder lists.",

	// --- String ---
	"String.cons":       "Adds a character to the front of a string.",
	"String.repeat":     "Repeats a string a given number of times, joined end to end.",
	"String.fromInt":    "Renders an integer as its decimal text.",
	"String.fromList":   "Builds a string from a list of characters. The inverse of toList.",
	"String.join":       "Joins a list of strings into one, placing the separator between each pair. The inverse of split.",
	"String.map":        "Applies a function to every character, producing a new string of the same length.",
	"String.filter":     "Keeps only the characters for which the test returns True.",
	"String.foldl":      "Reduces the characters of a string to a single value, walking left to right and threading an accumulator.",
	"String.toUpper":    "Uppercases every character.",
	"String.toLower":    "Lowercases every character.",
	"String.trim":       "Removes whitespace from both ends of the string.",
	"String.padLeft":    "Pads the string on the left with a given character until it reaches a target length. A string already that long is returned unchanged.",
	"String.padRight":   "Pads the string on the right with a given character until it reaches a target length. A string already that long is returned unchanged.",
	"String.replace":    "Replaces every occurrence of one substring with another.",
	"String.split":      "Breaks a string apart on a separator, returning the list of pieces. The inverse of join.",
	"String.indexes":    "Every starting position where the first string occurs inside the second. Empty when there is no match.",
	"String.length":     "The number of characters in the string.",
	"String.contains":   "True when the first string occurs somewhere inside the second.",
	"String.startsWith": "True when the second string begins with the first.",
	"String.endsWith":   "True when the second string ends with the first.",
	"String.any":        "True when at least one character passes the test.",
	"String.toInt":      "Parses a whole number, returning Just the integer, or Nothing when the text is not a valid number.",
	"String.toList":     "Explodes the string into its list of characters. The inverse of fromList.",

	// --- Maybe ---
	"Maybe.withDefault": "Unwraps a Just, or falls back to the given value when it is Nothing. The usual way to leave the Maybe world with a sensible default.",
	"Maybe.map":         "Transforms the value inside a Just and leaves Nothing untouched. Lets you work on a value that might be missing without checking first.",
	"Maybe.andThen":     "Chains a step that itself returns a Maybe, short-circuiting to Nothing the moment any step is Nothing. Use it to sequence operations that can each fail.",
	"Maybe.filter":      "Keeps the value only when it passes the test, otherwise Nothing.",
	"Maybe.map2":        "Combines two Maybe values with a function. The result is Nothing if either input is Nothing.",
	"Maybe.map3":        "Combines three Maybe values with a function. The result is Nothing if any input is Nothing.",
	"Maybe.andMap":      "Applies a function wrapped in a Maybe to a value wrapped in a Maybe. Chained after map, it combines any number of Maybe values, one argument at a time.",

	// --- Result ---
	"Result.withDefault": "Unwraps an Ok, or falls back to the given value when it is an Err. The usual way out of the Result world once you have decided the reason for failing does not matter here.",
	"Result.map":         "Applies a function to the value inside an Ok, leaving an Err untouched. The error rides along without being looked at.",
	"Result.andThen":     "Chains a step that can itself fail. The first Err short-circuits everything after it, which is what lets a pipeline of fallible steps read straight down the page.",
	"Result.mapError":    "Applies a function to the error inside an Err, leaving an Ok untouched. Use it to translate someone else's failure into your own error type.",
	"Result.fromMaybe":   "Turns a Maybe into a Result, using the given error for Nothing. This is where an absent value acquires a reason for being absent.",
	"Result.toMaybe":     "Turns a Result into a Maybe, discarding the error. The opposite trade: you keep the value and forget why it failed.",

	// --- Tuple ---
	"Basics.max":         "The larger of two values. Rides Comparable, so it orders Char and String too, not only numbers.",
	"Basics.min":         "The smaller of two values.",
	"Basics.clamp":       "Pins a value into a range: `clamp low high x`. Bounds come first so it partially applies, and a value already inside the range comes back untouched.",
	"Basics.abs":         "Distance from zero, for Int or Decimal. A Decimal keeps its scale.",
	"Basics.modBy":       "Remainder that follows the DIVISOR's sign, so a positive divisor never yields a negative. This is the one for wrapping an index or a coordinate. Divisor first, so `modBy 8` is the wrapping function.",
	"Basics.remainderBy": "Remainder that follows the DIVIDEND's sign, staying in step with `//` so that `(n // d) * d + remainderBy d n` is `n`. Reach for modBy when you want wrapping instead.",
	"Basics.not":         "Flips a Bool. Mar has no prefix operator, so negation is an ordinary function: `not busy` rather than `!busy`.",
	"Basics.always":      "Ignores its second argument and returns the first. `always x` is the constant function `\\_ -> x`, which is how a page says `subscriptions = always Sub.none`.",
	"Basics.linkTo":      "Builds the URL string for a typed route, filling its placeholders from a record. The route and the link share one declaration, so renaming a path is a compile error rather than a broken link.",
	"Tuple.first":        "The first element of a pair.",
	"Tuple.second":       "The second element of a pair.",
	"Tuple.pair":         "Builds a pair from two values. Useful where a function is wanted, since the (,) syntax is not one.",
	"Tuple.mapFirst":     "Applies a function to the first element, leaving the second alone.",
	"Tuple.mapSecond":    "Applies a function to the second element, leaving the first alone.",
	"Tuple.mapBoth":      "Applies one function to each side of the pair. The two sides can end up different types.",

	// --- Char ---
	"Char.toCode":   "The Unicode code point of a character.",
	"Char.fromCode": "The character at a Unicode code point.",
	"Char.toUpper":  "The upper-case form of a character, or the character itself when it has none.",
	"Char.toLower":  "The lower-case form of a character, or the character itself when it has none.",
	"Char.isDigit":  "True for the digits 0 through 9.",
	"Char.isAlpha":  "True for letters.",
	"Char.isUpper":  "True for upper-case letters.",
	"Char.isLower":  "True for lower-case letters.",

	// --- Dict ---
	"Dict.empty":     "A dictionary with nothing in it.",
	"Dict.singleton": "A dictionary holding exactly one key and value.",
	"Dict.fromList":  "Builds a dictionary from a list of key-value pairs. A repeated key keeps the last pair, so this doubles as a way to de-duplicate.",
	"Dict.get":       "Looks a key up, giving Nothing when it is not there. The Maybe is the point: a missing key is a value you handle, not a crash.",
	"Dict.member":    "True when the key is present, without fetching the value.",
	"Dict.size":      "How many entries the dictionary holds.",
	"Dict.isEmpty":   "True when the dictionary holds nothing.",
	"Dict.insert":    "Adds a key and value, replacing whatever that key held before.",
	"Dict.remove":    "Drops a key. Removing a key that was never there changes nothing.",
	"Dict.update":    "Rewrites one key by way of a function over its current Maybe. Returning Nothing removes it, so insert, change and delete are one operation.",
	"Dict.map":       "Applies a function to every value, keeping the keys. The function sees the key as well, since a value often means something only next to its key.",
	"Dict.filter":    "Keeps the entries the test says True for, judging by key and value together.",
	"Dict.foldl":     "Reduces the dictionary to a single value, walking keys in ascending order.",
	"Dict.foldr":     "Reduces the dictionary to a single value, walking keys in descending order.",
	"Dict.partition": "Splits into two dictionaries: the entries that pass the test, and the rest. One pass instead of filtering twice.",
	"Dict.union":     "All entries from both. When a key is in both, the LEFT dictionary wins.",
	"Dict.intersect": "Only the keys present in both, keeping the left dictionary's values.",
	"Dict.diff":      "The left dictionary minus every key present in the right one.",
	"Dict.toList":    "Every key-value pair, in ascending key order.",
	"Dict.keys":      "Every key, in ascending order.",
	"Dict.values":    "Every value, ordered by its key.",

	// --- Set ---
	"Set.empty":     "A set with nothing in it.",
	"Set.singleton": "A set holding exactly one value.",
	"Set.fromList":  "Builds a set from a list, dropping duplicates.",
	"Set.member":    "True when the value is in the set.",
	"Set.size":      "How many values the set holds.",
	"Set.isEmpty":   "True when the set holds nothing.",
	"Set.insert":    "Adds a value. Adding one that is already there changes nothing.",
	"Set.remove":    "Drops a value. Removing one that was never there changes nothing.",
	"Set.map":       "Applies a function to every value. The result can be smaller, since two values may map onto one.",
	"Set.filter":    "Keeps the values the test says True for.",
	"Set.foldl":     "Reduces the set to a single value, walking in ascending order.",
	"Set.foldr":     "Reduces the set to a single value, walking in descending order.",
	"Set.partition": "Splits into two sets: the values that pass the test, and the rest.",
	"Set.union":     "Every value present in either set.",
	"Set.intersect": "Only the values present in both sets.",
	"Set.diff":      "The left set minus every value present in the right one.",
	"Set.toList":    "Every value, in ascending order.",

	// --- Decimal ---
	"Math.degrees":     "An angle in whole degrees. Any Int works: 360 is the same angle as 0, and -90 is the same as 270.",
	"Math.deciDegrees": "An angle in tenths of a degree, which is as fine as an Angle goes. Reach for it when whole degrees are too coarse: a turn rate, a slow sweep.",
	"Math.turns":       "An angle in brads: 256 to a full turn, the unit a game usually counts a heading in. One brad is not a whole number of tenths, so it floors; a full turn is exact.",
	"Math.add":         "Two angles added, wrapped back into one turn. This is how a heading turns without anyone writing the wrap.",
	"Math.subtract":    "One angle minus another, wrapped back into one turn.",
	"Math.opposite":    "The angle pointing the other way, half a turn from this one. Facing away, bouncing back.",
	"Math.sin":         "The sine of an angle in thousandths, so -1000 to 1000. The same integer on every runtime, because it comes from one checked-in table rather than the host's trigonometry.",
	"Math.cos":         "The cosine of an angle in thousandths, so -1000 to 1000.",
	"Math.atan2":       "The angle of the vector (x, y). Note that y comes first. The y axis points UP here, so a canvas whose y grows downward negates it at the call site. atan2 0 0 is 0 degrees rather than an error.",
	"Math.isqrt":       "The whole part of a square root. Anything at or below zero gives 0, so there is nothing to guard. For a distance, square first and then take the root: Math.isqrt (dx * dx + dy * dy).",

	"Decimal.fromInt":       "An exact Decimal from a whole number.",
	"Decimal.fromCents":     "A Decimal from a count of hundredths, which is how money usually arrives from a database or an API.",
	"Decimal.fromString":    "Parses a Decimal, giving Nothing when the text is not one. The scale comes from the text, so \"2.50\" keeps its two places.",
	"Decimal.zero":          "The Decimal zero.",
	"Decimal.toString":      "The Decimal written out, keeping its scale.",
	"Decimal.toCents":       "The value as a count of hundredths.",
	"Decimal.toScale":       "Moves the value to a given number of decimal places, rounding the named way when places are lost.",
	"Decimal.truncate":      "Drops the fraction, moving toward zero.",
	"Decimal.round":         "The nearest whole number.",
	"Decimal.floor":         "The nearest whole number at or below the value.",
	"Decimal.ceiling":       "The nearest whole number at or above the value.",
	"Decimal.toIntWith":     "A whole number, rounded the way you name. Use it when the choice of rounding is part of the rule you are implementing, not an afterthought.",
	"Decimal.rounded":       "The result of a division, rounded the named way to a given number of places. This is the resolver you use when a rule tells you how to round.",
	"Decimal.withRemainder": "Splits a division into a quotient at the given scale plus what is left over. Nothing is lost, so money can be shared out without cents appearing or vanishing.",
	"Decimal.abs":           "The value without its sign.",
	"Decimal.negate":        "The value with its sign flipped.",
	"Decimal.compare":       "Orders two Decimals, giving LT, EQ or GT.",
	"Decimal.Up":            "Rounds away from zero.",
	"Decimal.Down":          "Rounds toward zero.",
	"Decimal.Ceiling":       "Rounds toward positive infinity.",
	"Decimal.Floor":         "Rounds toward negative infinity.",
	"Decimal.HalfUp":        "Rounds to the nearest, sending an exact half away from zero. This is the rounding most people mean.",
	"Decimal.HalfEven":      "Rounds to the nearest, sending an exact half to the even neighbour. Banker's rounding: over many values the halves cancel instead of drifting upward.",
}

var examples = map[string][]string{
	// --- List ---
	"List.range":       {"List.range 1 4 == [1, 2, 3, 4]", "List.range 5 1 == []"},
	"List.repeat":      {"List.repeat 3 0 == [0, 0, 0]"},
	"List.map":         {"List.map (\\x -> x * x) [1, 2, 3] == [1, 4, 9]"},
	"List.indexedMap":  {"List.indexedMap (\\i x -> i) [5, 6, 7] == [0, 1, 2]"},
	"List.filter":      {"List.filter (\\x -> x > 2) [1, 2, 3, 4] == [3, 4]"},
	"List.filterMap":   {"List.filterMap String.toInt [\"3\", \"x\", \"5\"] == [3, 5]"},
	"List.foldl":       {"List.foldl (\\x acc -> x :: acc) [] [1, 2, 3] == [3, 2, 1]"},
	"List.foldr":       {"List.foldr (\\x acc -> x + acc) 0 [1, 2, 3] == 6"},
	"List.concat":      {"List.concat [[1, 2], [3], [4, 5]] == [1, 2, 3, 4, 5]"},
	"List.concatMap":   {"List.concatMap (\\x -> [x, x]) [1, 2] == [1, 1, 2, 2]"},
	"List.intersperse": {"List.intersperse 0 [1, 2, 3] == [1, 0, 2, 0, 3]"},
	"List.sort":        {"List.sort [3, 1, 2] == [1, 2, 3]"},
	"List.sortBy":      {"List.sortBy String.length [\"ccc\", \"a\", \"bb\"] == [\"a\", \"bb\", \"ccc\"]"},
	"List.member":      {"List.member 2 [1, 2, 3] == True"},
	"List.any":         {"List.any (\\x -> x > 2) [1, 2, 3] == True"},
	"List.all":         {"List.all (\\x -> x > 0) [1, 2, 3] == True", "List.all (\\x -> x > 0) [] == True"},
	"List.sum":         {"List.sum [1, 2, 3] == 6", "List.sum [1.50, 2.25] == 3.75", "List.sum [] == 0"},
	"List.product":     {"List.product [1, 2, 3, 4] == 24", "List.product [1.5, 2.0] == 3.00", "List.product [] == 1"},
	"List.maximum":     {"List.maximum [3, 1, 2] == Just 3", "List.maximum [] == Nothing"},
	"List.minimum":     {"List.minimum [3, 1, 2] == Just 1", "List.minimum [] == Nothing"},
	"List.head":        {"List.head [1, 2, 3] == Just 1", "List.head [] == Nothing"},
	"List.tail":        {"List.tail [1, 2, 3] == Just [2, 3]", "List.tail [] == Nothing"},
	"List.take":        {"List.take 2 [1, 2, 3, 4] == [1, 2]", "List.take 9 [1, 2, 3] == [1, 2, 3]"},
	"List.drop":        {"List.drop 2 [1, 2, 3, 4] == [3, 4]", "List.drop 9 [1, 2, 3] == []"},
	"List.partition":   {"List.partition (\\x -> x > 2) [1, 2, 3, 4] == ([3, 4], [1, 2])"},
	"List.reverse":     {"List.reverse [1, 2, 3] == [3, 2, 1]"},
	"List.isEmpty":     {"List.isEmpty [] == True", "List.isEmpty [1] == False"},
	"List.length":      {"List.length [1, 2, 3] == 3"},
	"List.sortWith":    {"List.sortWith (\\a b -> if a > b then LT else GT) [1, 3, 2] == [3, 2, 1]"},
	"List.move":        {"List.move 0 2 [1, 2, 3] == [2, 3, 1]", "List.move 0 9 [1, 2, 3] == [1, 2, 3]"},

	// --- Result ---
	"Result.withDefault": {"Result.withDefault 0 (Ok 5) == 5", "Result.withDefault 0 (Err \"boom\") == 0"},
	"Result.map":         {"Result.map (\\x -> x + 1) (Ok 1) == Ok 2", "Result.map (\\x -> x + 1) (Err \"boom\") == Err \"boom\""},
	"Result.andThen":     {"Result.andThen (\\x -> Ok (x * 2)) (Ok 3) == Ok 6", "Result.andThen (\\x -> Ok (x * 2)) (Err \"boom\") == Err \"boom\""},
	"Result.mapError":    {"Result.mapError String.toUpper (Err \"boom\") == Err \"BOOM\"", "Result.mapError String.toUpper (Ok 1) == Ok 1"},
	// Concrete on purpose, unlike its neighbours. map/andThen/withDefault are
	// structural, so abstract data keeps them readable; fromMaybe is not. Its
	// first argument is the sentence somebody will read when the value is
	// missing, and "missing" taught nothing about that. A profile field that
	// genuinely may be unset shows the real job: absence becoming a reason.
	"Result.fromMaybe": {"Result.fromMaybe \"No nickname set\" (Just \"ana\") == Ok \"ana\"", "Result.fromMaybe \"No nickname set\" Nothing == Err \"No nickname set\""},
	"Result.toMaybe":   {"Result.toMaybe (Ok 5) == Just 5", "Result.toMaybe (Err \"boom\") == Nothing"},

	// --- Tuple ---
	"Basics.max":         {"max 3 7 == 7", "max \"abc\" \"abd\" == \"abd\""},
	"Basics.min":         {"min 3 7 == 3"},
	"Basics.clamp":       {"clamp 0 10 42 == 10", "clamp 0 10 5 == 5"},
	"Basics.abs":         {"abs (0 - 5) == 5", "abs 2.50 == 2.50"},
	"Basics.modBy":       {"modBy 3 10 == 1", "modBy 8 (0 - 1) == 7"},
	"Basics.remainderBy": {"remainderBy 3 10 == 1", "remainderBy 8 (0 - 1) == 0 - 1"},
	"Basics.not":         {"not True == False", "List.filter (\\n -> not (n > 2)) [ 1, 2, 3 ] == [ 1, 2 ]"},
	"Basics.always":      {"always 7 \"ignored\" == 7"},
	"Basics.linkTo":      {"task : Path { id : Int }\ntask = \"/task/{id:Int}\"\n\nhref = linkTo task { id = 7 }"},
	"Tuple.first":        {"Tuple.first (1, \"a\") == 1"},
	"Tuple.second":       {"Tuple.second (1, \"a\") == \"a\""},
	"Tuple.pair":         {"Tuple.pair 1 \"a\" == (1, \"a\")"},
	"Tuple.mapFirst":     {"Tuple.mapFirst (\\x -> x + 1) (1, \"a\") == (2, \"a\")"},
	"Tuple.mapSecond":    {"Tuple.mapSecond String.toUpper (1, \"a\") == (1, \"A\")"},
	"Tuple.mapBoth":      {"Tuple.mapBoth (\\x -> x + 1) String.toUpper (1, \"a\") == (2, \"A\")"},

	// --- Char ---
	"Char.toCode":   {"Char.toCode 'A' == 65"},
	"Char.fromCode": {"Char.fromCode 65 == 'A'"},
	"Char.toUpper":  {"Char.toUpper 'a' == 'A'", "Char.toUpper '7' == '7'"},
	"Char.toLower":  {"Char.toLower 'A' == 'a'"},
	"Char.isDigit":  {"Char.isDigit '7' == True", "Char.isDigit 'x' == False"},
	"Char.isAlpha":  {"Char.isAlpha 'x' == True", "Char.isAlpha '7' == False"},
	"Char.isUpper":  {"Char.isUpper 'X' == True", "Char.isUpper 'x' == False"},
	"Char.isLower":  {"Char.isLower 'x' == True", "Char.isLower 'X' == False"},

	// --- Dict ---
	"Dict.empty":     {"Dict.size Dict.empty == 0"},
	"Dict.singleton": {"Dict.toList (Dict.singleton 1 \"a\") == [(1, \"a\")]"},
	"Dict.fromList":  {"Dict.toList (Dict.fromList [(2, \"b\"), (1, \"a\")]) == [(1, \"a\"), (2, \"b\")]", "Dict.toList (Dict.fromList [(1, \"a\"), (1, \"z\")]) == [(1, \"z\")]"},
	"Dict.get":       {"Dict.get 1 (Dict.fromList [(1, \"a\")]) == Just \"a\"", "Dict.get 9 (Dict.fromList [(1, \"a\")]) == Nothing"},
	"Dict.member":    {"Dict.member 1 (Dict.fromList [(1, \"a\")]) == True", "Dict.member 9 (Dict.fromList [(1, \"a\")]) == False"},
	"Dict.size":      {"Dict.size (Dict.fromList [(1, \"a\"), (2, \"b\")]) == 2"},
	"Dict.isEmpty":   {"Dict.isEmpty Dict.empty == True", "Dict.isEmpty (Dict.singleton 1 \"a\") == False"},
	"Dict.insert":    {"Dict.toList (Dict.insert 2 \"b\" (Dict.singleton 1 \"a\")) == [(1, \"a\"), (2, \"b\")]", "Dict.toList (Dict.insert 1 \"z\" (Dict.singleton 1 \"a\")) == [(1, \"z\")]"},
	"Dict.remove":    {"Dict.toList (Dict.remove 1 (Dict.singleton 1 \"a\")) == []", "Dict.toList (Dict.remove 9 (Dict.singleton 1 \"a\")) == [(1, \"a\")]"},
	"Dict.update":    {"Dict.toList (Dict.update 1 (\\v -> Just \"z\") (Dict.singleton 1 \"a\")) == [(1, \"z\")]", "Dict.toList (Dict.update 1 (\\v -> Nothing) (Dict.singleton 1 \"a\")) == []"},
	"Dict.map":       {"Dict.toList (Dict.map (\\k v -> v ++ \"!\") (Dict.singleton 1 \"a\")) == [(1, \"a!\")]"},
	"Dict.filter":    {"Dict.toList (Dict.filter (\\k v -> k > 1) (Dict.fromList [(1, \"a\"), (2, \"b\")])) == [(2, \"b\")]"},
	"Dict.foldl":     {"Dict.foldl (\\k v acc -> acc + k) 0 (Dict.fromList [(1, \"a\"), (2, \"b\")]) == 3"},
	"Dict.foldr":     {"Dict.foldr (\\k v acc -> acc + k) 0 (Dict.fromList [(1, \"a\"), (2, \"b\")]) == 3"},
	"Dict.partition": {"Dict.toList (Tuple.first (Dict.partition (\\k v -> k > 1) (Dict.fromList [(1, \"a\"), (2, \"b\")]))) == [(2, \"b\")]"},
	"Dict.union":     {"Dict.toList (Dict.union (Dict.singleton 1 \"left\") (Dict.singleton 1 \"right\")) == [(1, \"left\")]"},
	"Dict.intersect": {"Dict.toList (Dict.intersect (Dict.fromList [(1, \"a\"), (2, \"b\")]) (Dict.singleton 1 \"z\")) == [(1, \"a\")]"},
	"Dict.diff":      {"Dict.toList (Dict.diff (Dict.fromList [(1, \"a\"), (2, \"b\")]) (Dict.singleton 1 \"z\")) == [(2, \"b\")]"},
	"Dict.toList":    {"Dict.toList (Dict.fromList [(2, \"b\"), (1, \"a\")]) == [(1, \"a\"), (2, \"b\")]"},
	"Dict.keys":      {"Dict.keys (Dict.fromList [(2, \"b\"), (1, \"a\")]) == [1, 2]"},
	"Dict.values":    {"Dict.values (Dict.fromList [(2, \"b\"), (1, \"a\")]) == [\"a\", \"b\"]"},

	// --- Set ---
	"Set.empty":     {"Set.size Set.empty == 0"},
	"Set.singleton": {"Set.toList (Set.singleton 1) == [1]"},
	"Set.fromList":  {"Set.toList (Set.fromList [3, 1, 2]) == [1, 2, 3]", "Set.toList (Set.fromList [1, 1, 2]) == [1, 2]"},
	"Set.member":    {"Set.member 1 (Set.fromList [1, 2]) == True", "Set.member 9 (Set.fromList [1, 2]) == False"},
	"Set.size":      {"Set.size (Set.fromList [1, 2, 2]) == 2"},
	"Set.isEmpty":   {"Set.isEmpty Set.empty == True", "Set.isEmpty (Set.singleton 1) == False"},
	"Set.insert":    {"Set.toList (Set.insert 3 (Set.fromList [1, 2])) == [1, 2, 3]", "Set.toList (Set.insert 1 (Set.fromList [1, 2])) == [1, 2]"},
	"Set.remove":    {"Set.toList (Set.remove 1 (Set.fromList [1, 2])) == [2]", "Set.toList (Set.remove 9 (Set.fromList [1, 2])) == [1, 2]"},
	"Set.map":       {"Set.toList (Set.map (\\x -> x * 2) (Set.fromList [1, 2])) == [2, 4]"},
	"Set.filter":    {"Set.toList (Set.filter (\\x -> x > 1) (Set.fromList [1, 2, 3])) == [2, 3]"},
	"Set.foldl":     {"Set.foldl (\\x acc -> acc + x) 0 (Set.fromList [1, 2, 3]) == 6"},
	"Set.foldr":     {"Set.foldr (\\x acc -> acc + x) 0 (Set.fromList [1, 2, 3]) == 6"},
	"Set.partition": {"Set.toList (Tuple.first (Set.partition (\\x -> x > 1) (Set.fromList [1, 2, 3]))) == [2, 3]"},
	"Set.union":     {"Set.toList (Set.union (Set.fromList [1, 2]) (Set.fromList [2, 3])) == [1, 2, 3]"},
	"Set.intersect": {"Set.toList (Set.intersect (Set.fromList [1, 2]) (Set.fromList [2, 3])) == [2]"},
	"Set.diff":      {"Set.toList (Set.diff (Set.fromList [1, 2]) (Set.fromList [2, 3])) == [1]"},
	"Set.toList":    {"Set.toList (Set.fromList [3, 1, 2]) == [1, 2, 3]"},

	// --- Decimal ---
	"Math.degrees":     {"Math.sin (Math.degrees 30) == 500", "Math.degrees 360 == Math.degrees 0"},
	"Math.deciDegrees": {"Math.deciDegrees 450 == Math.degrees 45"},
	"Math.turns":       {"Math.turns 64 == Math.degrees 90"},
	"Math.add":         {"Math.add (Math.degrees 350) (Math.degrees 20) == Math.degrees 10"},
	"Math.subtract":    {"Math.subtract (Math.degrees 10) (Math.degrees 20) == Math.degrees 350"},
	"Math.opposite":    {"Math.opposite (Math.degrees 30) == Math.degrees 210"},
	"Math.sin":         {"Math.sin (Math.degrees 90) == 1000"},
	"Math.cos":         {"Math.cos (Math.degrees 60) == 500"},
	"Math.atan2":       {"Math.atan2 1 1 == Math.degrees 45", "Math.atan2 0 0 == Math.degrees 0"},
	"Math.isqrt":       {"Math.isqrt 17 == 4", "Math.isqrt (0 - 5) == 0"},

	"Decimal.fromInt":       {"Decimal.toCents (Decimal.fromInt 3) == 300"},
	"Decimal.fromCents":     {"Decimal.toCents (Decimal.fromCents 250) == 250"},
	"Decimal.fromString":    {"Maybe.map Decimal.toCents (Decimal.fromString \"2.50\") == Just 250", "Decimal.fromString \"abc\" == Nothing"},
	"Decimal.zero":          {"Decimal.toCents Decimal.zero == 0"},
	"Decimal.toString":      {"Decimal.toString (Decimal.fromCents 250) == \"2.50\""},
	"Decimal.toCents":       {"Decimal.toCents (Decimal.fromCents 250) == 250"},
	"Decimal.toScale":       {"Decimal.toString (Decimal.toScale Decimal.HalfUp 2 (Decimal.fromInt 3)) == \"3.00\""},
	"Decimal.truncate":      {"Decimal.truncate (Decimal.fromCents 299) == 2"},
	"Decimal.round":         {"Decimal.round (Decimal.fromCents 260) == 3", "Decimal.round (Decimal.fromCents 240) == 2"},
	"Decimal.floor":         {"Decimal.floor (Decimal.fromCents 299) == 2"},
	"Decimal.ceiling":       {"Decimal.ceiling (Decimal.fromCents 201) == 3"},
	"Decimal.toIntWith":     {"Decimal.toIntWith Decimal.HalfUp (Decimal.fromCents 250) == 3", "Decimal.toIntWith Decimal.HalfEven (Decimal.fromCents 250) == 2"},
	"Decimal.rounded":       {"Decimal.toString (Decimal.rounded Decimal.HalfUp 2 (Decimal.fromInt 10 / Decimal.fromInt 3)) == \"3.33\""},
	"Decimal.withRemainder": {"Decimal.toCents ((Decimal.withRemainder 2 (Decimal.fromInt 10 / Decimal.fromInt 4)).quotient) == 250"},
	"Decimal.abs":           {"Decimal.toCents (Decimal.abs (Decimal.fromCents (0 - 250))) == 250"},
	"Decimal.negate":        {"Decimal.toCents (Decimal.negate (Decimal.fromCents 250)) == 0 - 250"},
	"Decimal.compare":       {"Decimal.compare (Decimal.fromInt 1) (Decimal.fromInt 2) == LT"},
	"Decimal.Up":            {"Decimal.toIntWith Decimal.Up (Decimal.fromCents 210) == 3"},
	"Decimal.Down":          {"Decimal.toIntWith Decimal.Down (Decimal.fromCents 290) == 2"},
	"Decimal.Ceiling":       {"Decimal.toIntWith Decimal.Ceiling (Decimal.fromCents 210) == 3"},
	"Decimal.Floor":         {"Decimal.toIntWith Decimal.Floor (Decimal.fromCents 290) == 2"},
	"Decimal.HalfUp":        {"Decimal.toIntWith Decimal.HalfUp (Decimal.fromCents 250) == 3"},
	"Decimal.HalfEven":      {"Decimal.toIntWith Decimal.HalfEven (Decimal.fromCents 250) == 2"},

	// --- String ---
	"String.cons":       {"String.cons 'h' \"i\" == \"hi\""},
	"String.repeat":     {"String.repeat 3 \"ab\" == \"ababab\""},
	"String.fromInt":    {"String.fromInt 42 == \"42\""},
	"String.fromList":   {"String.fromList ['c', 'a', 't'] == \"cat\""},
	"String.join":       {"String.join \", \" [\"a\", \"b\", \"c\"] == \"a, b, c\""},
	"String.filter":     {"String.filter (\\c -> c /= ' ') \"a b c\" == \"abc\""},
	"String.toUpper":    {"String.toUpper \"hello\" == \"HELLO\""},
	"String.toLower":    {"String.toLower \"HELLO\" == \"hello\""},
	"String.trim":       {"String.trim \"  hi  \" == \"hi\""},
	"String.padLeft":    {"String.padLeft 5 '.' \"42\" == \"...42\"", "String.padLeft 2 '.' \"hello\" == \"hello\""},
	"String.padRight":   {"String.padRight 5 '.' \"42\" == \"42...\"", "String.padRight 2 '.' \"hello\" == \"hello\""},
	"String.replace":    {"String.replace \"cat\" \"dog\" \"cat and cat\" == \"dog and dog\""},
	"String.split":      {"String.split \",\" \"a,b,c\" == [\"a\", \"b\", \"c\"]"},
	"String.indexes":    {"String.indexes \"a\" \"banana\" == [1, 3, 5]", "String.indexes \"z\" \"banana\" == []"},
	"String.length":     {"String.length \"hello\" == 5"},
	"String.contains":   {"String.contains \"ll\" \"hello\" == True"},
	"String.startsWith": {"String.startsWith \"he\" \"hello\" == True"},
	"String.endsWith":   {"String.endsWith \"lo\" \"hello\" == True"},
	"String.toInt":      {"String.toInt \"42\" == Just 42", "String.toInt \"4x\" == Nothing"},
	"String.toList":     {"String.toList \"cat\" == ['c', 'a', 't']"},
	"String.map":        {"String.map Char.toUpper \"cat\" == \"CAT\""},
	"String.foldl":      {"String.foldl (\\c acc -> acc + 1) 0 \"cat\" == 3"},
	"String.any":        {"String.any (\\c -> c == 'a') \"cat\" == True"},

	// --- Maybe ---
	"Maybe.withDefault": {"Maybe.withDefault 0 (Just 5) == 5", "Maybe.withDefault 0 Nothing == 0"},
	"Maybe.map":         {"Maybe.map (\\x -> x + 1) (Just 4) == Just 5", "Maybe.map (\\x -> x + 1) Nothing == Nothing"},
	"Maybe.andThen":     {"Maybe.andThen List.head (Just [1, 2, 3]) == Just 1", "Maybe.andThen (\\x -> if x > 0 then Just x else Nothing) (Just 0) == Nothing"},
	"Maybe.filter":      {"Maybe.filter (\\x -> x > 2) (Just 5) == Just 5", "Maybe.filter (\\x -> x > 2) (Just 1) == Nothing"},
	"Maybe.map2":        {"Maybe.map2 (\\a b -> a + b) (Just 2) (Just 3) == Just 5", "Maybe.map2 (\\a b -> a + b) (Just 2) Nothing == Nothing"},
	"Maybe.map3":        {"Maybe.map3 (\\a b c -> a + b + c) (Just 1) (Just 2) (Just 3) == Just 6", "Maybe.map3 (\\a b c -> a + b + c) (Just 1) Nothing (Just 3) == Nothing"},
	"Maybe.andMap":      {"Maybe.andMap (Just 2) (Just (\\x -> x + 1)) == Just 3"},
}

// blurbs is the one-line description of each module, shown on the reference
// index and at the top of the module's own page. It lives here rather than in
// the website so that adding a module cannot leave a blank row behind: the
// coverage test below requires one for every module in Modules.
var blurbs = map[string]string{
	"Basics":  "The handful of builtins written without a module prefix.",
	"List":    "Build, transform, filter, and fold lists.",
	"String":  "Inspect and reshape text, character by character.",
	"Maybe":   "Work with values that might be absent.",
	"Result":  "Carry a value or the reason it could not be produced.",
	"Tuple":   "Read and rework the two halves of a pair.",
	"Char":    "Single characters: their codes, their case, what kind they are.",
	"Dict":    "Look values up by key, in sorted order.",
	"Set":     "A collection with no duplicates and no order of its own.",
	"Decimal": "Exact decimal arithmetic, with rounding you choose rather than inherit.",
	"Math":    "Angles, trigonometry, and square roots in whole numbers, identical on every runtime.",
}

// moduleGroups is how the reference index is carved up: not by layer or by
// implementation, but by what you are trying to do when you go looking. One
// flat list was right at nine modules and a wall at twenty-nine.
//
// The order is the arc of building something: the values you compute with, the
// two ways to put something on a screen, how anything happens at all, your
// server, then somebody else's.
//
// Screens and Games sit together because they are siblings — UI and Canvas are
// the two ways to draw, not two stages of anything — and Effects follows both
// because it serves both: every update returns a Cmd whether it is a form or a
// game loop. Games spent one revision between Effects and the backend, which
// put the server below the games and split the frontend from the backend it
// talks to.
//
// Time and Random live in Data because that is what they are — a value type and
// a description of a value, on the same shelf as Decimal; they were a section
// of their own for one revision and read as an orphan pair.
//
// It lives here, not in the website, for the same reason blurbs do: the
// coverage test below requires every module in Modules to appear in exactly one
// group, so a module cannot be added and then silently belong to no section.
var moduleGroups = []ModuleGroup{
	{"Data", []string{"Basics", "List", "String", "Maybe", "Result", "Tuple", "Char", "Dict", "Set", "Decimal", "Math", "Time", "Random"}},
	{"Screens", []string{"App", "Page", "Nav", "UI"}},
	{"Games and media", []string{"Canvas", "Sound", "Keyboard", "Gamepad", "Device"}},
	{"Effects", []string{"Cmd", "Sub", "Task"}},
	{"Server and database", []string{"Service", "Entity", "Repo", "Auth"}},
	{"Talking to the outside", []string{"Http", "JSON"}},
}
