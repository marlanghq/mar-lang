// Package conformance holds the one stdlib corpus every runtime has to agree
// about, and the hand-written answers all of them are measured against.
//
// Every pure stdlib function is implemented three times — Go, JavaScript,
// Swift — and nothing forces the three to agree. The drift tests check that
// each layer DEFINES a name; they cannot see that the layers answer
// differently. `==` was defined everywhere and returned False for every record
// in the browser while Go and iOS said True, silently, for as long as it took
// someone to notice.
//
// Agreement alone is the weaker property: runtimes can be wrong the same way.
// So `Expectations` is worked out from the corpus expressions rather than
// recorded from a run, and every runtime is checked against it. Agreement
// catches drift; the constant catches a shared mistake.
//
// The corpus lives here rather than beside one runtime because each runtime is
// exercised from its own package: Go and JS from internal/jsserve, Swift from
// internal/iosbundle. A corpus owned by one of them would be a corpus the
// others copy.
package conformance

import (
	"fmt"
	"sort"
	"strings"
)

// Source is the program every runtime evaluates. Its top-level `results` is a
// single String: one block per module, blocks joined by newline, cases within
// a block joined by `;`, each case `label=answer`.
//
// Answers are rendered as text on purpose. A structural comparison would need
// a value representation the three runtimes share, which is the very thing
// under test.
const Source = `module Conform exposing (..)


yn : Bool -> String
yn b =
    if b then
        "T"

    else
        "F"


is : List Int -> String
is xs =
    String.join "," (List.map String.fromInt xs)


mi : Maybe Int -> String
mi m =
    case m of
        Just n ->
            String.fromInt n

        Nothing ->
            "-"


ri : Result String Int -> String
ri r =
    case r of
        Ok n ->
            String.fromInt n

        Err e ->
            "E" ++ e


dictShow : Dict String Int -> String
dictShow d =
    String.join "," (List.map (\pair -> Tuple.first pair ++ ":" ++ String.fromInt (Tuple.second pair)) (Dict.toList d))


d1 : Dict String Int
d1 =
    Dict.fromList [ ( "b", 2 ), ( "a", 1 ) ]


d2 : Dict String Int
d2 =
    Dict.fromList [ ( "b", 20 ), ( "c", 3 ) ]


s1 : Set Int
s1 =
    Set.fromList [ 3, 1, 2, 1 ]


s2 : Set Int
s2 =
    Set.fromList [ 2, 4 ]


stringCases : String
stringCases =
    String.join ";"
        [ "any=" ++ yn (String.any Char.isDigit "ab1")
        , "cons=" ++ String.cons 'x' "yz"
        , "contains=" ++ yn (String.contains "ell" "hello")
        , "endsWith=" ++ yn (String.endsWith "lo" "hello")
        , "filter=" ++ String.filter Char.isAlpha "a1b2"
        , "foldl=" ++ String.fromInt (String.foldl (\_ n -> n + 1) 0 "abc")
        , "fromInt=" ++ String.fromInt (0 - 42)
        , "fromList=" ++ String.fromList [ 'h', 'i' ]
        , "indexes=" ++ is (String.indexes "a" "banana")
        , "join=" ++ String.join "-" [ "a", "b" ]
        , "length=" ++ String.fromInt (String.length "hello")
        , "map=" ++ String.map Char.toUpper "ab"
        , "padLeft=" ++ String.padLeft 4 '0' "7"
        , "padRight=" ++ String.padRight 3 '.' "x"
        , "repeat=" ++ String.repeat 3 "ab"
        , "replace=" ++ String.replace "a" "o" "banana"
        , "split=" ++ String.join "|" (String.split "," "a,b,c")
        , "startsWith=" ++ yn (String.startsWith "he" "hello")
        , "toInt=" ++ mi (String.toInt "12")
        -- The edge of Int (ADR: 53 bits). The coverage gate asks whether a
        -- FUNCTION is exercised, never over which VALUES, which is exactly how
        -- three runtimes came to disagree about Int for large numbers while
        -- every case here passed. These pin the boundary itself.
        , "toIntMax=" ++ mi (String.toInt "9007199254740991")
        , "toIntPastMax=" ++ mi (String.toInt "9007199254740992")
        , "toIntMin=" ++ mi (String.toInt "-9007199254740991")
        , "toIntPastMin=" ++ mi (String.toInt "-9007199254740992")
        , "fromIntMax=" ++ String.fromInt 9007199254740991
        , "fromIntMin=" ++ String.fromInt (0 - 9007199254740991)
        , "sumToMax=" ++ String.fromInt (9007199254740990 + 1)
        , "toIntBad=" ++ mi (String.toInt "x")
        , "toList=" ++ String.fromInt (List.length (String.toList "abc"))
        , "toLower=" ++ String.toLower "AbC"
        , "toUpper=" ++ String.toUpper "AbC"
        , "trim=" ++ String.trim "  hi  "
        ]


listCases : String
listCases =
    String.join ";"
        [ "all=" ++ yn (List.all (\n -> n > 0) [ 1, 2 ])
        , "any=" ++ yn (List.any (\n -> n > 1) [ 1, 2 ])
        , "concat=" ++ is (List.concat [ [ 1 ], [ 2, 3 ] ])
        , "concatMap=" ++ is (List.concatMap (\n -> [ n, n ]) [ 1, 2 ])
        , "drop=" ++ is (List.drop 2 [ 1, 2, 3 ])
        , "filter=" ++ is (List.filter (\n -> n > 1) [ 1, 2, 3 ])
        , "filterMap=" ++ is (List.filterMap (\n -> if n > 1 then Just n else Nothing) [ 1, 2 ])
        , "foldl=" ++ String.fromInt (List.foldl (\x acc -> acc * 10 + x) 0 [ 1, 2, 3 ])
        , "foldr=" ++ String.fromInt (List.foldr (\x acc -> acc * 10 + x) 0 [ 1, 2, 3 ])
        , "head=" ++ mi (List.head [ 7, 8 ])
        , "headEmpty=" ++ mi (List.head [])
        , "indexedMap=" ++ is (List.indexedMap (\i n -> i * 10 + n) [ 1, 2 ])
        , "intersperse=" ++ is (List.intersperse 0 [ 1, 2 ])
        , "isEmpty=" ++ yn (List.isEmpty [])
        , "length=" ++ String.fromInt (List.length [ 1, 2, 3 ])
        , "map=" ++ is (List.map (\n -> n * 2) [ 1, 2 ])
        , "maximum=" ++ mi (List.maximum [ 3, 1, 2 ])
        , "member=" ++ yn (List.member 2 [ 1, 2 ])
        , "minimum=" ++ mi (List.minimum [ 3, 1, 2 ])
        , "move=" ++ is (List.move 0 2 [ 1, 2, 3 ])
        , "partition=" ++ is (Tuple.first (List.partition (\n -> n > 1) [ 1, 2, 3 ]))
        , "product=" ++ String.fromInt (List.product [ 2, 3, 4 ])
        , "range=" ++ is (List.range 1 4)
        , "repeat=" ++ is (List.repeat 3 7)
        , "reverse=" ++ is (List.reverse [ 1, 2, 3 ])
        , "sort=" ++ is (List.sort [ 3, 1, 2 ])
        , "sortBy=" ++ ss (List.sortBy String.length [ "abc", "a", "ab" ])
        , "sortWith=" ++ is (List.sortWith (\a b -> if a < b then GT else if a > b then LT else EQ) [ 1, 3, 2 ])
        , "sum=" ++ String.fromInt (List.sum [ 1, 2, 3 ])
        , "tail=" ++ (case List.tail [ 1, 2, 3 ] of
                        Just t -> is t
                        Nothing -> "-"
                     )
        , "take=" ++ is (List.take 2 [ 1, 2, 3 ])
        ]


ss : List String -> String
ss xs =
    String.join "," xs


maybeCases : String
maybeCases =
    String.join ";"
        [ "andMap=" ++ mi (Maybe.andMap (Just 2) (Just (\n -> n + 1)))
        , "andThen=" ++ mi (Maybe.andThen (\n -> if n > 1 then Just (n * 2) else Nothing) (Just 3))
        , "andThenNo=" ++ mi (Maybe.andThen (\n -> if n > 1 then Just (n * 2) else Nothing) (Just 1))
        , "filter=" ++ mi (Maybe.filter (\n -> n > 1) (Just 2))
        , "filterOut=" ++ mi (Maybe.filter (\n -> n > 1) (Just 1))
        , "map=" ++ mi (Maybe.map (\n -> n + 1) (Just 1))
        , "map2=" ++ mi (Maybe.map2 (\a b -> a + b) (Just 1) (Just 2))
        , "map3=" ++ mi (Maybe.map3 (\a b c -> a + b + c) (Just 1) (Just 2) (Just 3))
        , "withDefault=" ++ String.fromInt (Maybe.withDefault 9 Nothing)
        ]


resultCases : String
resultCases =
    String.join ";"
        [ "andThen=" ++ ri (Result.andThen (\n -> Ok (n + 1)) (Ok 1))
        , "fromMaybe=" ++ ri (Result.fromMaybe "no" (Just 1))
        , "fromMaybeNo=" ++ ri (Result.fromMaybe "no" Nothing)
        , "map=" ++ ri (Result.map (\n -> n * 2) (Ok 2))
        , "mapError=" ++ ri (Result.mapError (\e -> e ++ "!") (Err "x"))
        , "toMaybe=" ++ mi (Result.toMaybe (Ok 5))
        , "toMaybeErr=" ++ mi (Result.toMaybe (Err "e"))
        , "withDefault=" ++ String.fromInt (Result.withDefault 9 (Err "e"))
        ]


tupleCases : String
tupleCases =
    String.join ";"
        [ "first=" ++ String.fromInt (Tuple.first ( 1, "a" ))
        , "second=" ++ Tuple.second ( 1, "a" )
        , "pair=" ++ String.fromInt (Tuple.first (Tuple.pair 4 "z"))
        , "mapFirst=" ++ String.fromInt (Tuple.first (Tuple.mapFirst (\n -> n + 1) ( 1, "a" )))
        , "mapSecond=" ++ Tuple.second (Tuple.mapSecond String.toUpper ( 1, "a" ))
        , "mapBoth=" ++ Tuple.second (Tuple.mapBoth (\n -> n) String.toUpper ( 1, "b" ))
        ]


-- The BARE globals — Elm's Basics, spelled without a qualifier. They had no
-- block here for as long as the corpus existed, because the coverage gate
-- walks the stdlib MODULE by module and a name with no dot is not module
-- surface. That is exactly where modBy diverged: its zero guard in the
-- browser compared an Int against a BigInt literal, so it was dead code, and
-- modBy 0 produced a NaN inside a value typed Int. Go and Swift were right;
-- nothing was looking at the third.
--
-- The three cases that matter are the ones nobody writes on purpose: divisor
-- zero, a negative divisor, and a negative divisor that divides exactly.
basicsCases : String
basicsCases =
    String.join ";"
        [ "abs=" ++ String.fromInt (abs (-7))
        , "absPos=" ++ String.fromInt (abs 7)
        , "always=" ++ String.fromInt (always 4 9)
        , "clampAbove=" ++ String.fromInt (clamp 1 10 42)
        , "clampBelow=" ++ String.fromInt (clamp 1 10 (-5))
        , "clampInside=" ++ String.fromInt (clamp 1 10 6)
        , "max=" ++ String.fromInt (max 3 9)
        , "min=" ++ String.fromInt (min 3 9)
        , "not=" ++ yn (not True)
        , "notFalse=" ++ yn (not False)
        , "modBy=" ++ String.fromInt (modBy 3 10)
        , "modByNegDividend=" ++ String.fromInt (modBy 8 (-1))
        , "modByNegDivisor=" ++ String.fromInt (modBy (-8) 1)
        , "modByNegExact=" ++ String.fromInt (modBy (-4) 8)
        , "modByZero=" ++ String.fromInt (modBy 0 5)
        , "remainderBy=" ++ String.fromInt (remainderBy 3 10)
        , "remainderByNeg=" ++ String.fromInt (remainderBy 8 (-1))
        , "remainderByZero=" ++ String.fromInt (remainderBy 0 5)
        ]


charCases : String
charCases =
    String.join ";"
        [ "fromCode=" ++ String.fromList [ Char.fromCode 65 ]
        , "isAlpha=" ++ yn (Char.isAlpha 'a')
        , "isDigit=" ++ yn (Char.isDigit '7')
        , "isLower=" ++ yn (Char.isLower 'a')
        , "isUpper=" ++ yn (Char.isUpper 'a')
        , "toCode=" ++ String.fromInt (Char.toCode 'A')
        , "toLower=" ++ String.fromList [ Char.toLower 'A' ]
        , "toUpper=" ++ String.fromList [ Char.toUpper 'a' ]
        ]


dictCases : String
dictCases =
    String.join ";"
        [ "fromList=" ++ dictShow d1
        , "empty=" ++ yn (Dict.isEmpty Dict.empty)
        , "isEmpty=" ++ yn (Dict.isEmpty d1)
        , "get=" ++ mi (Dict.get "a" d1)
        , "getMiss=" ++ mi (Dict.get "z" d1)
        , "insert=" ++ dictShow (Dict.insert "c" 3 d1)
        , "remove=" ++ dictShow (Dict.remove "a" d1)
        , "member=" ++ yn (Dict.member "b" d1)
        , "size=" ++ String.fromInt (Dict.size d1)
        , "singleton=" ++ dictShow (Dict.singleton "k" 9)
        , "keys=" ++ ss (Dict.keys d1)
        , "values=" ++ is (Dict.values d1)
        , "map=" ++ dictShow (Dict.map (\_ v -> v * 10) d1)
        , "filter=" ++ dictShow (Dict.filter (\_ v -> v > 1) d1)
        , "foldl=" ++ String.fromInt (Dict.foldl (\_ v acc -> acc * 10 + v) 0 d1)
        , "foldr=" ++ String.fromInt (Dict.foldr (\_ v acc -> acc * 10 + v) 0 d1)
        , "union=" ++ dictShow (Dict.union d1 d2)
        , "intersect=" ++ dictShow (Dict.intersect d1 d2)
        , "diff=" ++ dictShow (Dict.diff d1 d2)
        , "update=" ++ dictShow (Dict.update "a" (\_ -> Just 99) d1)
        , "partition=" ++ dictShow (Tuple.first (Dict.partition (\_ v -> v > 1) d1))
        , "toList=" ++ String.fromInt (List.length (Dict.toList d1))
        ]


setCases : String
setCases =
    String.join ";"
        [ "fromList=" ++ is (Set.toList s1)
        , "empty=" ++ yn (Set.isEmpty Set.empty)
        , "isEmpty=" ++ yn (Set.isEmpty s1)
        , "insert=" ++ is (Set.toList (Set.insert 9 s1))
        , "remove=" ++ is (Set.toList (Set.remove 1 s1))
        , "member=" ++ yn (Set.member 2 s1)
        , "size=" ++ String.fromInt (Set.size s1)
        , "singleton=" ++ is (Set.toList (Set.singleton 5))
        , "map=" ++ is (Set.toList (Set.map (\n -> n * 2) s1))
        , "filter=" ++ is (Set.toList (Set.filter (\n -> n > 1) s1))
        , "foldl=" ++ String.fromInt (Set.foldl (\v acc -> acc * 10 + v) 0 s1)
        , "foldr=" ++ String.fromInt (Set.foldr (\v acc -> acc * 10 + v) 0 s1)
        , "union=" ++ is (Set.toList (Set.union s1 s2))
        , "intersect=" ++ is (Set.toList (Set.intersect s1 s2))
        , "diff=" ++ is (Set.toList (Set.diff s1 s2))
        , "partition=" ++ is (Set.toList (Tuple.first (Set.partition (\n -> n > 1) s1)))
        , "toList=" ++ String.fromInt (List.length (Set.toList s1))
        ]


-- JSON answers come back with double quotes swapped for single ones and
-- backslashes for slashes. Those two characters are exactly the ones each
-- runtime escapes differently when it PRINTS a string, which is a property of
-- the harness and not of JSON.encode. Substituting them keeps the comparison
-- about the encoder: encodeEscape still shows that the inner quote was
-- escaped, with a slash standing where the backslash is.
q : String -> String
q s =
    String.replace "\\" "/" (String.replace "\"" "'" s)


jsonCases : String
jsonCases =
    String.join ";"
        [ "encodeInt=" ++ q (JSON.encode 42)
        , "encodeNeg=" ++ q (JSON.encode (0 - 7))
        , "encodeString=" ++ q (JSON.encode "hi")
        , "encodeEscape=" ++ q (JSON.encode "a\"b")
        , "encodeBool=" ++ q (JSON.encode True)
        , "encodeList=" ++ q (JSON.encode [ 1, 2, 3 ])
        , "encodeRecord=" ++ q (JSON.encode { b = 2, a = 1 })
        , "encodeNested=" ++ q (JSON.encode { xs = [ 1 ], s = "q" })
        , "encodeJust=" ++ q (JSON.encode (Just 3))
        , "encodeNothing=" ++ q (JSON.encode Nothing)
        , "encodeUnit=" ++ q (JSON.encode ())
        , "encodeTuple=" ++ q (JSON.encode ( 1, "a" ))
        , "encodeIgnoresFieldOrder=" ++ yn (JSON.encode { a = 1, b = 2 } == JSON.encode { b = 2, a = 1 })
        , "decodeInt=" ++ (case JSON.decode "7" of
                             Ok n -> String.fromInt n
                             Err _ -> "err"
                          )
        , "decodeBad=" ++ (case JSON.decode "{" of
                             Ok n -> String.fromInt n
                             Err _ -> "err"
                          )
        , "roundTrip=" ++ (case JSON.decode (JSON.encode { a = 5 }) of
                             Ok r -> String.fromInt r.a
                             Err _ -> "err"
                          )
        ]


mathCases : String
mathCases =
    String.join ";"
        [ "degEqDeci=" ++ yn (Math.degrees 45 == Math.deciDegrees 450)
        , "degWraps=" ++ yn (Math.degrees 360 == Math.degrees 0)
        , "degNegative=" ++ yn (Math.degrees (0 - 90) == Math.degrees 270)
        , "turnsQuarter=" ++ yn (Math.turns 64 == Math.degrees 90)
        , "turnsFull=" ++ yn (Math.turns 256 == Math.degrees 0)
        , "turnsOne=" ++ String.fromInt (Math.sin (Math.turns 1))
        , "sin0=" ++ String.fromInt (Math.sin (Math.degrees 0))
        , "sin30=" ++ String.fromInt (Math.sin (Math.degrees 30))
        , "sin45=" ++ String.fromInt (Math.sin (Math.degrees 45))
        , "sin60=" ++ String.fromInt (Math.sin (Math.degrees 60))
        , "sin90=" ++ String.fromInt (Math.sin (Math.degrees 90))
        , "sin180=" ++ String.fromInt (Math.sin (Math.degrees 180))
        , "sin270=" ++ String.fromInt (Math.sin (Math.degrees 270))
        , "sin360=" ++ String.fromInt (Math.sin (Math.degrees 360))
        , "sinNeg90=" ++ String.fromInt (Math.sin (Math.degrees (0 - 90)))
        , "sinHalfStep=" ++ String.fromInt (Math.sin (Math.deciDegrees 455))
        , "cos0=" ++ String.fromInt (Math.cos (Math.degrees 0))
        , "cos60=" ++ String.fromInt (Math.cos (Math.degrees 60))
        , "cos90=" ++ String.fromInt (Math.cos (Math.degrees 90))
        , "cos180=" ++ String.fromInt (Math.cos (Math.degrees 180))
        , "cos270=" ++ String.fromInt (Math.cos (Math.degrees 270))
        , "addWraps=" ++ yn (Math.add (Math.degrees 350) (Math.degrees 20) == Math.degrees 10)
        , "subWraps=" ++ yn (Math.subtract (Math.degrees 10) (Math.degrees 20) == Math.degrees 350)
        , "opposite=" ++ yn (Math.opposite (Math.degrees 30) == Math.degrees 210)
        , "oppositeTwice=" ++ yn (Math.opposite (Math.opposite (Math.degrees 30)) == Math.degrees 30)
        , "atan2Origin=" ++ yn (Math.atan2 0 0 == Math.degrees 0)
        , "atan2E=" ++ yn (Math.atan2 0 1 == Math.degrees 0)
        , "atan2NE=" ++ yn (Math.atan2 1 1 == Math.degrees 45)
        , "atan2N=" ++ yn (Math.atan2 1 0 == Math.degrees 90)
        , "atan2NW=" ++ yn (Math.atan2 1 (0 - 1) == Math.degrees 135)
        , "atan2W=" ++ yn (Math.atan2 0 (0 - 1) == Math.degrees 180)
        , "atan2SW=" ++ yn (Math.atan2 (0 - 1) (0 - 1) == Math.degrees 225)
        , "atan2S=" ++ yn (Math.atan2 (0 - 1) 0 == Math.degrees 270)
        , "atan2SE=" ++ yn (Math.atan2 (0 - 1) 1 == Math.degrees 315)
        , "atan2Thirty=" ++ yn (Math.atan2 1000 1732 == Math.degrees 30)
        , "atan2RoundTrip=" ++ String.fromInt (Math.sin (Math.atan2 500 866))
        , "isqrtZero=" ++ String.fromInt (Math.isqrt 0)
        , "isqrtNegative=" ++ String.fromInt (Math.isqrt (0 - 5))
        , "isqrtOne=" ++ String.fromInt (Math.isqrt 1)
        , "isqrtBelowSquare=" ++ String.fromInt (Math.isqrt 15)
        , "isqrtSquare=" ++ String.fromInt (Math.isqrt 16)
        , "isqrtAboveSquare=" ++ String.fromInt (Math.isqrt 17)
        , "isqrtMillion=" ++ String.fromInt (Math.isqrt 1000000)
        , "isqrtBig=" ++ String.fromInt (Math.isqrt 999999999999)
        ]


results : String
results =
    String.join "\n" [ basicsCases, stringCases, listCases, maybeCases, resultCases, tupleCases, charCases, dictCases, setCases, jsonCases, mathCases ]
`

// Entry is the value each runtime is asked to evaluate.
const Entry = "Conform.results"

// Blocks names the blocks `results` joins, in that order. Case labels repeat
// across modules — `map`, `foldl`, `filter`, `toList` — so a case is named for
// the block it landed in: `Dict.map`, not `map`.
var Blocks = []string{
	"Basics", "String", "List", "Maybe", "Result", "Tuple", "Char", "Dict", "Set", "JSON", "Math",
}

// Scope is the set of modules whose meaning must be identical everywhere: pure
// functions from values to values, where a difference between runtimes is a
// bug by definition. Every one of their functions has to appear in Source or
// the coverage gate fails the build.
var Scope = map[string]bool{
	"String": true, "List": true, "Maybe": true, "Result": true,
	"Tuple": true, "Char": true, "Dict": true, "Set": true, "JSON": true,
	// Math is the reason the corpus exists, in miniature: three hand-ported
	// kernels reading one generated table, where a single wrong fold would
	// make a game aim differently on the phone than in the browser.
	"Math": true,
}

// OutOfScopeBare is the bare-global counterpart to OutOfScope: a name spelled
// without a qualifier that cannot be compared as a value. Everything else in
// typecheck.BareGlobals() has to appear in Source, which is the gate that was
// missing when `modBy` diverged — the module walk skips dotless names, so the
// numeric kit had never been compared across runtimes at all.
var OutOfScopeBare = map[string]string{
	"linkTo": "builds a UI link; compared by the renderer parity tests",
}

// OutOfScope is every other stdlib module, with the reason it is not compared
// here. Together the two maps have to cover the whole stdlib — a module in
// neither fails the build, so a new one cannot slip in untested by being
// unnoticed.
var OutOfScope = map[string]string{
	// Pinned by a conformance file of its own.
	"Decimal": "covered by decimal_conformance_test.go",
	"Random":  "generators are effects; the algorithm is pinned in random_test.go",
	"Time":    "Time.now reads the clock; the arithmetic deserves its own file",

	// Render rather than compute: the answer is a screen, and the three
	// renderers are compared by the drift and parity tests instead.
	"UI":     "renders; compared by the renderer parity tests",
	"Canvas": "renders; compared by the pixel-golden tests",
	"Sound":  "synthesises audio; compared by the sound shaping tests",

	// Read the world. There is no value to compare, only a device state.
	"Keyboard": "mirrors device input state",
	"Gamepad":  "mirrors device input state",
	"Device":   "reports the host's capabilities",

	// Server-only: they never run in the browser or on the phone, so there is
	// no second implementation to disagree with.
	"Repo":    "server-only persistence",
	"Entity":  "server-only schema declaration",
	"Auth":    "server-only sessions and email codes",
	"Mar":     "server-only admin services",
	"Service": "declares and dispatches requests; the wire is checked by ADR 0017",

	// Structure rather than computation: what they build is a program, and the
	// runtimes are compared by running that program, which is this corpus.
	"App":  "wires the application together",
	"Page": "declares a route",
	"Nav":  "moves between routes",
	"Cmd":  "describes work for the runtime to perform",
	"Sub":  "describes what the runtime should listen to",
	"Task": "describes server work",
	"Http": "performs network requests",
}

// expectations are worked out from the expressions in Source rather than
// recorded from a run. That is the whole point: a value copied out of a runtime
// proves the runtime equals itself. `List.foldl` folding `\x acc -> acc * 10 + x`
// over [1,2,3] has to be 123 and `List.foldr` has to be 321 because of which
// end each starts from; `Dict.union` has to keep the left dictionary's `b` at
// 2; `Set.fromList [3,1,2,1]` has to be 1,2,3 because a set is sorted and
// deduplicated. If a runtime disagrees with a line here, the line is the
// argument.
const expectations = `
Basics.abs=7
Basics.absPos=7
Basics.always=4
Basics.clampAbove=10
Basics.clampBelow=1
Basics.clampInside=6
Basics.max=9
Basics.min=3
Basics.not=F
Basics.notFalse=T
Basics.modBy=1
Basics.modByNegDividend=7
Basics.modByNegDivisor=-7
Basics.modByNegExact=0
Basics.modByZero=0
Basics.remainderBy=1
Basics.remainderByNeg=-1
Basics.remainderByZero=0

String.any=T
String.cons=xyz
String.contains=T
String.endsWith=T
String.filter=ab
String.foldl=3
String.fromInt=-42
String.fromList=hi
String.indexes=1,3,5
String.join=a-b
String.length=5
String.map=AB
String.padLeft=0007
String.padRight=x..
String.repeat=ababab
String.replace=bonono
String.split=a|b|c
String.startsWith=T
String.toInt=12
String.toIntMax=9007199254740991
String.toIntPastMax=-
String.toIntMin=-9007199254740991
String.toIntPastMin=-
String.fromIntMax=9007199254740991
String.fromIntMin=-9007199254740991
String.sumToMax=9007199254740991
String.toIntBad=-
String.toList=3
String.toLower=abc
String.toUpper=ABC
String.trim=hi

List.all=T
List.any=T
List.concat=1,2,3
List.concatMap=1,1,2,2
List.drop=3
List.filter=2,3
List.filterMap=2
List.foldl=123
List.foldr=321
List.head=7
List.headEmpty=-
List.indexedMap=1,12
List.intersperse=1,0,2
List.isEmpty=T
List.length=3
List.map=2,4
List.maximum=3
List.member=T
List.minimum=1
List.move=2,3,1
List.partition=2,3
List.product=24
List.range=1,2,3,4
List.repeat=7,7,7
List.reverse=3,2,1
List.sort=1,2,3
List.sortBy=a,ab,abc
List.sortWith=3,2,1
List.sum=6
List.tail=2,3
List.take=1,2

Maybe.andMap=3
Maybe.andThen=6
Maybe.andThenNo=-
Maybe.filter=2
Maybe.filterOut=-
Maybe.map=2
Maybe.map2=3
Maybe.map3=6
Maybe.withDefault=9

Result.andThen=2
Result.fromMaybe=1
Result.fromMaybeNo=Eno
Result.map=4
Result.mapError=Ex!
Result.toMaybe=5
Result.toMaybeErr=-
Result.withDefault=9

Tuple.first=1
Tuple.second=a
Tuple.pair=4
Tuple.mapFirst=2
Tuple.mapSecond=A
Tuple.mapBoth=B

Char.fromCode=A
Char.isAlpha=T
Char.isDigit=T
Char.isLower=T
Char.isUpper=F
Char.toCode=65
Char.toLower=a
Char.toUpper=A

Dict.fromList=a:1,b:2
Dict.empty=T
Dict.isEmpty=F
Dict.get=1
Dict.getMiss=-
Dict.insert=a:1,b:2,c:3
Dict.remove=b:2
Dict.member=T
Dict.size=2
Dict.singleton=k:9
Dict.keys=a,b
Dict.values=1,2
Dict.map=a:10,b:20
Dict.filter=b:2
Dict.foldl=12
Dict.foldr=21
Dict.union=a:1,b:2,c:3
Dict.intersect=b:2
Dict.diff=a:1
Dict.update=a:99,b:2
Dict.partition=b:2
Dict.toList=2

Set.fromList=1,2,3
Set.empty=T
Set.isEmpty=F
Set.insert=1,2,3,9
Set.remove=2,3
Set.member=T
Set.size=3
Set.singleton=5
Set.map=2,4,6
Set.filter=2,3
Set.foldl=123
Set.foldr=321
Set.union=1,2,3,4
Set.intersect=2
Set.diff=1,3
Set.partition=2,3
Set.toList=3

JSON.encodeInt=42
JSON.encodeNeg=-7
JSON.encodeString='hi'
JSON.encodeEscape='a/'b'
JSON.encodeBool=true
JSON.encodeList=[1,2,3]
JSON.encodeRecord={'a':1,'b':2}
JSON.encodeNested={'s':'q','xs':[1]}
JSON.encodeJust={'__args':[3],'__ctor':'Just'}
JSON.encodeNothing={'__ctor':'Nothing'}
JSON.encodeUnit=null
JSON.encodeTuple=[1,'a']
JSON.encodeIgnoresFieldOrder=T
JSON.decodeInt=7
JSON.decodeBad=err
JSON.roundTrip=5

Math.degEqDeci=T
Math.degWraps=T
Math.degNegative=T
Math.turnsQuarter=T
Math.turnsFull=T
Math.turnsOne=24
Math.sin0=0
Math.sin30=500
Math.sin45=707
Math.sin60=866
Math.sin90=1000
Math.sin180=0
Math.sin270=-1000
Math.sin360=0
Math.sinNeg90=-1000
Math.sinHalfStep=713
Math.cos0=1000
Math.cos60=500
Math.cos90=0
Math.cos180=-1000
Math.cos270=0
Math.addWraps=T
Math.subWraps=T
Math.opposite=T
Math.oppositeTwice=T
Math.atan2Origin=T
Math.atan2E=T
Math.atan2NE=T
Math.atan2N=T
Math.atan2NW=T
Math.atan2W=T
Math.atan2SW=T
Math.atan2S=T
Math.atan2SE=T
Math.atan2Thirty=T
Math.atan2RoundTrip=500
Math.isqrtZero=0
Math.isqrtNegative=0
Math.isqrtOne=1
Math.isqrtBelowSquare=3
Math.isqrtSquare=4
Math.isqrtAboveSquare=4
Math.isqrtMillion=1000
Math.isqrtBig=999999
`

// Expectations parses the hand-written answers. Blank lines only group them for
// reading.
func Expectations() (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(expectations), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.Index(line, "=")
		if i <= 0 {
			return nil, fmt.Errorf("expectation line is not name=value: %q", line)
		}
		if _, dup := out[line[:i]]; dup {
			return nil, fmt.Errorf("expectation %q is written twice; the second would silently win", line[:i])
		}
		out[line[:i]] = line[i+1:]
	}
	return out, nil
}

// SplitCases turns one runtime's rendering of `results` into module-qualified
// cases. Keying on the bare label would lose cases rather than report them: six
// modules have a `map`, and only the last one parsed would be compared.
func SplitCases(out string) (map[string]string, error) {
	// Runtimes that render a string with newlines escaped hand back the two
	// characters `\n` where the corpus joined its blocks; runtimes that print
	// the string raw hand back a real newline. Accept either.
	lines := strings.Split(strings.ReplaceAll(out, `\n`, "\n"), "\n")
	if len(lines) != len(Blocks) {
		return nil, fmt.Errorf("the corpus produced %d blocks but Blocks names %d; "+
			"a block was added to `results` without being named there", len(lines), len(Blocks))
	}
	cases := map[string]string{}
	for i, line := range lines {
		for _, c := range strings.Split(line, ";") {
			if j := strings.Index(c, "="); j > 0 {
				cases[Blocks[i]+"."+c[:j]] = c[j+1:]
			}
		}
	}
	return cases, nil
}

// Check compares one runtime's answers to the hand-written ones and returns the
// problems, sorted, empty when the runtime is right. `who` names the runtime so
// a failure says which one is wrong.
func Check(who string, got map[string]string) ([]string, error) {
	want, err := Expectations()
	if err != nil {
		return nil, err
	}
	var problems []string
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: expected %s, but %s produces no such case", name, w, who))
			continue
		}
		if g != w {
			problems = append(problems, fmt.Sprintf("%s: expected %s, %s says %s", name, w, who, g))
		}
	}
	for name, g := range got {
		if _, ok := want[name]; !ok {
			problems = append(problems, fmt.Sprintf("%s: %s answers %s, but no hand-written expectation covers it", name, who, g))
		}
	}
	sort.Strings(problems)
	return problems, nil
}

// Difference reports the cases where two runtimes disagree, so a failure names
// the function instead of dumping two long strings side by side.
func Difference(aName string, a map[string]string, bName string, b map[string]string) []string {
	var out []string
	for name, av := range a {
		bv, ok := b[name]
		if !ok {
			out = append(out, bName+" produced no answer for "+name)
			continue
		}
		if av != bv {
			out = append(out, fmt.Sprintf("%s: %s=%s  %s=%s", name, aName, av, bName, bv))
		}
	}
	sort.Strings(out)
	return out
}
