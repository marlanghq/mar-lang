// Package conformance holds the one stdlib corpus every runtime has to agree
// about, and the hand-written answers all of them are measured against.
//
// Every pure stdlib function is implemented three times: Go, JavaScript,
// Swift, and nothing forces the three to agree. The drift tests check that
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


-- Text that is not ASCII.
--
-- A Mar String is a sequence of Unicode SCALAR VALUES (ADR 0038): every
-- length, width, position, equality and ordering counts scalars, which is what
-- a Char already is. The three runtimes store a String differently -- Go in
-- UTF-8, the browser in UTF-16, the phone in whatever Swift chose -- so each
-- has a natural unit of its own that is not this one, and each has to be held
-- to the shared answer.
--
-- ASCII cannot tell those units apart. That is what makes this block the one
-- that earns its keep: every case below is chosen so the storage shows
-- through, and every one of them has a single right answer.
--
-- None of the strings is exotica. They are what a Brazilian address book holds
-- in its first row: an accented letter, the same letter spelled with a
-- combining accent, a CJK pair, an emoji, and a flag.
--
accentKeys : Dict String Int
accentKeys =
    Dict.fromList [ ( "café", 1 ), ( "café", 2 ) ]


textCases : String
textCases =
    String.join ";"
        -- How long is a String. The unit question, stated five ways.
        [ "lenAcute=" ++ String.fromInt (String.length "café")
        , "lenCombining=" ++ String.fromInt (String.length "café")
        , "lenCJK=" ++ String.fromInt (String.length "日本")
        , "lenGrin=" ++ String.fromInt (String.length "😀")
        , "lenFlag=" ++ String.fromInt (String.length "🇧🇷")

        -- What String.toList yields, which is the same question about Char.
        , "toListAcute=" ++ String.fromInt (List.length (String.toList "café"))
        , "toListCombining=" ++ String.fromInt (List.length (String.toList "café"))
        , "toListGrin=" ++ String.fromInt (List.length (String.toList "😀"))
        , "toListFlag=" ++ String.fromInt (List.length (String.toList "🇧🇷"))

        -- The law a reader would assume without being told: a String is as
        -- long as the list of its Chars. It holds in one runtime of three.
        , "lawLenCombining=" ++ yn (String.length "café" == List.length (String.toList "café"))
        , "lawLenFlag=" ++ yn (String.length "🇧🇷" == List.length (String.toList "🇧🇷"))

        -- A Char above the BMP has to survive the round trip and the classifiers.
        , "codeAcute=" ++ String.fromInt (Char.toCode 'é')
        , "codeGrin=" ++ String.fromInt (Char.toCode '😀')
        , "fromCodeHigh=" ++ String.fromList [ Char.fromCode 128512 ]
        , "fromCodeSurrogate=" ++ String.fromInt (Char.toCode (Char.fromCode 55296))
        , "fromCodePastMax=" ++ String.fromInt (Char.toCode (Char.fromCode 1114112))
        , "fromCodeMax=" ++ String.fromInt (Char.toCode (Char.fromCode 1114111))
        , "fromCodeNegative=" ++ String.fromInt (Char.toCode (Char.fromCode (0 - 1)))
        , "charUpperSharpS=" ++ String.fromList [ Char.toUpper 'ß' ]
        , "charLowerDotted=" ++ String.fromList [ Char.toLower 'İ' ]
        , "charUpperAcute=" ++ String.fromList [ Char.toUpper 'é' ]
        , "isAlphaAcute=" ++ yn (Char.isAlpha 'é')
        , "isUpperAcute=" ++ yn (Char.isUpper 'É')

        -- Padding has to MEASURE before it can fill, so it inherits the unit
        -- question and adds one of its own: a runtime that measures in bytes
        -- stops padding at all once the text is wide enough in bytes.
        , "padAcute=" ++ String.padLeft 6 '.' "café"
        , "padCJK=" ++ String.padLeft 4 '.' "日本"
        , "padGrin=" ++ String.padLeft 3 '.' "😀"
        , "padRightAcute=" ++ String.padRight 6 '.' "café"

        -- Case mapping that changes LENGTH. Unicode says the uppercase of the
        -- sharp s is two letters; a runtime mapping one scalar at a time
        -- cannot say that.
        , "upperAcute=" ++ String.toUpper "café"
        , "lowerAcute=" ++ String.toLower "CAFÉ"
        , "upperSharpS=" ++ String.toUpper "straße"
        , "upperLigature=" ++ String.toUpper "ﬁle"
        , "lowerDotted=" ++ String.toLower "İstanbul"
        , "lowerFinalSigma=" ++ String.toLower "ΣΟΦΟΣ"

        -- Where a substring is. The answer is an index, so it is the unit
        -- question again, and the ASCII case could not tell bytes from
        -- anything else.
        , "indexesAcute=" ++ is (String.indexes "é" "café")
        , "indexesAfterAcute=" ++ is (String.indexes "x" "éx")
        , "indexesAfterGrin=" ++ is (String.indexes "x" "😀x")

        -- Whether two spellings of the same word are the same String. This one
        -- is not cosmetic: Dict and Set decide key identity with it, so the
        -- answer decides what a dictionary CONTAINS.
        , "eqNormal=" ++ yn ("café" == "café")
        , "ltNormal=" ++ yn ("café" < "café")
        , "containsAcute=" ++ yn (String.contains "é" "café")
        , "containsCombining=" ++ yn (String.contains "é" "café")
        , "dictAccentSize=" ++ String.fromInt (Dict.size accentKeys)
        , "dictAccentGet=" ++ mi (Dict.get "café" accentKeys)
        , "setAccentSize=" ++ String.fromInt (Set.size (Set.fromList [ "café", "café" ]))

        -- Order above the BMP. A runtime comparing UTF-16 units sorts every
        -- astral character below the replacement character, because a leading
        -- surrogate is numerically smaller than it.
        , "sortAstral=" ++ String.join "|" (List.sort [ "😀", "�", "z" ])

        -- The per-Char functions, over a letter that is not ASCII.
        , "mapAcute=" ++ String.map Char.toUpper "café"
        , "filterAcute=" ++ String.filter Char.isAlpha "café!"
        , "foldlAcute=" ++ String.fromInt (String.foldl (\_ n -> n + 1) 0 "café")
        , "consAcute=" ++ String.cons 'é' "x"
        , "anyAcuteAlpha=" ++ yn (String.any Char.isAlpha "é")
        , "endsAcute=" ++ yn (String.endsWith "é" "café")
        , "trimAcute=" ++ String.trim "  café  "
        , "splitAcute=" ++ String.join "|" (String.split "é" "café")
        , "replaceAcute=" ++ String.replace "é" "e" "café"
        , "replaceEmpty=" ++ String.replace "" "-" "ab"
        , "splitEmpty=" ++ String.join "|" (String.split "" "aé")
        , "splitEmptyAstral=" ++ String.join "|" (String.split "" "a😀")
        , "replaceEmptyAstral=" ++ String.replace "" "-" "a😀"
        , "fromListMixed=" ++ String.fromList [ 'é', '😀' ]
        , "repeatAcute=" ++ String.repeat 2 "é"
        , "joinAcute=" ++ String.join "é" [ "a", "b" ]
        , "startsCombining=" ++ yn (String.startsWith "café" "cafés")
        , "charLowerAcute=" ++ String.fromList [ Char.toLower 'É' ]
        , "isLowerAcute=" ++ yn (Char.isLower 'é')

        -- A digit that is not an ASCII digit. Input validation is built out of
        -- this one, and the three runtimes ask three different questions: a
        -- Unicode category, a regular expression, or a property of a scalar.
        , "isDigitArabic=" ++ yn (Char.isDigit '٣')
        , "isAlphaCJK=" ++ yn (Char.isAlpha '日')
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
        , "encodeOk=" ++ q (JSON.encode okOne)
        , "encodeErr=" ++ q (JSON.encode errBad)
        , "encodeJustOk=" ++ q (JSON.encode (Just okOne))
        , "roundTripOk=" ++ showNested (decodeNested (JSON.encode okOne))
        , "roundTripErr=" ++ showNested (decodeNested (JSON.encode errBad))
        , "encodeDuration=" ++ q (JSON.encode (Time.millis 100))
        , "encodeDurationNeg=" ++ q (JSON.encode (Time.millis (-2500)))

        -- Keys in code point order: a digit before a letter, a capital
        -- before a small one, U+FF41 before U+1D465.
        , "encodeKeyOrder=" ++ q (JSON.encode { a10 = 1, a2 = 2, endTime = 3, ends = 4, posX = 5, position = 6 })
        , "encodeKeyOrderWide=" ++ q (JSON.encode { x𝑥 = 1, xａ = 2 })
        ]


-- A Result is a constructor like any other on the wire: tagged, so every
-- runtime reads its own encoding and the others' back as the same Result.
okOne : Result String Int
okOne =
    Ok 1


errBad : Result String Int
errBad =
    Err "bad"


decodeNested : String -> Result String (Result String Int)
decodeNested s =
    JSON.decode s


showNested : Result String (Result String Int) -> String
showNested d =
    case d of
        Ok (Ok n) ->
            "Ok " ++ String.fromInt n

        Ok (Err e) ->
            "Err " ++ e

        Err _ ->
            "err"


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


-- The logical operators SHORT-CIRCUIT, and this block is where the three
-- runtimes have to agree about it. Two of its cases are not comparisons but
-- assertions: a runtime that evaluates the right operand does not produce a
-- different answer, it raises an Int overflow and takes the whole corpus down.
-- That is the intended failure mode, because a runtime that is strict here is
-- not slightly wrong, it is wrong about which programs run at all.
--
-- (No backticks in this file: Source is a Go raw string, and one would end it.)
shortCircuitCases : String
shortCircuitCases =
    String.join ";"
        [ "andTT=" ++ yn (True && True)
        , "andTF=" ++ yn (True && False)
        , "andFT=" ++ yn (False && True)
        , "andFF=" ++ yn (False && False)
        , "orTT=" ++ yn (True || True)
        , "orTF=" ++ yn (True || False)
        , "orFT=" ++ yn (False || True)
        , "orFF=" ++ yn (False || False)

        -- the left operand decides, so the right one must never be reached
        , "andSkips=" ++ yn (False && (9007199254740991 + 1) > 0)
        , "orSkips=" ++ yn (True || (9007199254740991 + 1) > 0)

        -- the controls: the left operand does NOT decide, so the right one IS
        -- evaluated. Without these, a runtime that never evaluated the right
        -- operand at all would also pass.
        , "andEvaluates=" ++ yn (True && 2 > 1)
        , "orEvaluates=" ++ yn (False || 2 > 1)
        ]


results : String
orderingCases : String
orderingCases =
    String.join ";"
        [ "ltInt=" ++ yn (1 < 2)
        , "ltString=" ++ yn ("abc" < "abd")
        , "ltChar=" ++ yn ('a' < 'b')
        , "tupleFirstWins=" ++ yn (( 1, "z" ) < ( 2, "a" ))
        , "tupleTieBreaks=" ++ yn (( 1, "a" ) < ( 1, "b" ))
        , "tupleEqual=" ++ yn (( 1, "a" ) < ( 1, "a" ))
        , "tupleGe=" ++ yn (( 2, 0 ) >= ( 1, 9 ))
        , "leEqualTuple=" ++ yn (( 1, "a" ) <= ( 1, "a" ))
        , "leList=" ++ yn ([ 1 ] <= [ 1, 2 ])
        , "geChar=" ++ yn ('a' >= 'a')
        , "neInt=" ++ yn (1 /= 2)
        , "neEqual=" ++ yn (1 /= 1)
        , "neRecord=" ++ yn ({ p = 1 } /= { p = 2 })
        , "neTuple=" ++ yn (( 1, "a" ) /= ( 1, "b" ))
        , "tripleTie=" ++ yn (( 1, 2, 3 ) < ( 1, 2, 4 ))
        , "listPrefixIsLess=" ++ yn ([ 1 ] < [ 1, 2 ])
        , "listFirstWins=" ++ yn ([ 2 ] < [ 1, 3 ])
        , "listEmptyIsLeast=" ++ yn ([] < [ 1 ])
        , "listEqual=" ++ yn ([ 1, 2 ] < [ 1, 2 ])
        , "listOfTuples=" ++ yn ([ ( 1, "a" ) ] < [ ( 1, "b" ) ])
        , "sortTuples=" ++ ss (List.map Tuple.second (List.sort [ ( 2, "b" ), ( 1, "a" ), ( 2, "a" ) ]))
        , "sortLists=" ++ String.join "|" (List.map is (List.sort [ [ 2 ], [ 1, 2 ], [ 1 ] ]))
        , "sortByTupleKey=" ++ is (List.sortBy (\n -> ( modBy 2 n, n )) [ 1, 2, 3, 4 ])
        , "maxTuple=" ++ (case List.maximum [ ( 1, 9 ), ( 2, 0 ) ] of
                            Just p -> String.fromInt (Tuple.first p)
                            Nothing -> "-"
                         )
        , "minList=" ++ (case List.minimum [ [ 2 ], [ 1, 5 ] ] of
                            Just l -> is l
                            Nothing -> "-"
                        )
        , "maxOfTuples=" ++ String.fromInt (Tuple.first (max ( 1, 9 ) ( 2, 0 )))
        , "sortStability=" ++ ss (List.map Tuple.second (List.sortBy Tuple.first [ ( 1, "a" ), ( 0, "b" ), ( 1, "c" ), ( 0, "d" ) ]))
        , "dictTupleKeys=" ++ String.fromInt (Dict.size (Dict.fromList [ ( ( 1, 2 ), 1 ), ( ( 3, 4 ), 2 ), ( ( 1, 2 ), 3 ) ]))
        , "dictDupKeepsLast=" ++ mi (Dict.get ( 1, 2 ) (Dict.fromList [ ( ( 1, 2 ), 1 ), ( ( 3, 4 ), 2 ), ( ( 1, 2 ), 3 ) ]))
        , "dictTupleOrder=" ++ ss (List.map (\k -> String.fromInt (Tuple.first k) ++ "/" ++ String.fromInt (Tuple.second k)) (Dict.keys (Dict.fromList [ ( ( 2, 1 ), 0 ), ( ( 1, 9 ), 0 ), ( ( 1, 2 ), 0 ) ])))
        , "setTupleKeys=" ++ String.fromInt (Set.size (Set.fromList [ ( 1, 2 ), ( 3, 4 ), ( 1, 2 ) ]))
        , "setListItems=" ++ String.join "|" (List.map is (Set.toList (Set.fromList [ [ 2 ], [ 1 ], [ 2 ] ])))
        ]


type alias DecUser =
    { name : String
    , age : Int
    }


type DecStatus
    = DecDraft
    | DecSent


type DecShape
    = DecCircle Int
    | DecNamed String Int


type DecThread
    = DecThread String (Maybe DecThread)


type alias DecOpt =
    { note : Maybe String
    , amount : Decimal
    }


du : Result String DecUser -> String
du r =
    case r of
        Ok u ->
            "ok:" ++ u.name ++ ":" ++ String.fromInt u.age

        Err e ->
            e


ds : Result String DecStatus -> String
ds r =
    case r of
        Ok s ->
            case s of
                DecDraft ->
                    "ok:draft"

                DecSent ->
                    "ok:sent"

        Err e ->
            e


dl : Result String (List Int) -> String
dl r =
    case r of
        Ok xs ->
            "ok:" ++ is xs

        Err e ->
            e


dp : Result String DecShape -> String
dp r =
    case r of
        Ok s ->
            case s of
                DecCircle n ->
                    "ok:circle:" ++ String.fromInt n

                DecNamed n k ->
                    "ok:" ++ n ++ ":" ++ String.fromInt k

        Err e ->
            e


dth : Result String DecThread -> String
dth r =
    case r of
        Ok (DecThread name _) ->
            "ok:" ++ name

        Err e ->
            e


dt : Result String ( Int, String ) -> String
dt r =
    case r of
        Ok t ->
            "ok:" ++ String.fromInt (Tuple.first t) ++ ":" ++ Tuple.second t

        Err e ->
            e


dan : Result String Angle -> String
dan r =
    case r of
        Ok a ->
            if a == Math.deciDegrees 900 then
                "eq90deg"

            else
                "some other angle"

        Err e ->
            e


-- An instant built from its ISO text, so the corpus can talk about one
-- without a Time.fromMillis to build it with. Time.fromYMD 1970 1 1 is the
-- epoch, which is what a malformed literal falls back to.
instant : String -> Time
instant iso =
    case Time.fromIso iso of
        Just t ->
            t

        Nothing ->
            Time.fromYMD 1970 1 1


isoOf : String -> String
isoOf iso =
    Time.toIso (instant iso)


dtm : Result String Time -> String
dtm r =
    case r of
        Ok t ->
            String.fromInt (Time.toMillis t)

        Err e ->
            e


dde : Result String Decimal -> String
dde r =
    case r of
        Ok d ->
            Decimal.toString d

        Err e ->
            e


ddi : Result String (Dict Int Int) -> String
ddi r =
    case r of
        Ok d ->
            String.join "," (List.map String.fromInt (Dict.values d))

        Err e ->
            e


dse : Result String (Set Int) -> String
dse r =
    case r of
        Ok st ->
            String.join "," (List.map String.fromInt (Set.toList st))

        Err e ->
            e


ddu : Result String Duration -> String
ddu r =
    case r of
        Ok d ->
            Time.toIso (Time.add (Time.fromYMD 1970 1 1) d)

        Err e ->
            e


dkeyed : Result String { a : Int } -> String
dkeyed r =
    case r of
        Ok v ->
            JSON.encode v

        Err e ->
            e


unpinned : String -> String
unpinned s =
    case JSON.decode s of
        Ok _ ->
            "ok"

        Err e ->
            e


dcodes : Result String String -> String
dcodes r =
    case r of
        Ok s ->
            String.join "," (List.map (\c -> String.fromInt (Char.toCode c)) (String.toList s))

        Err e ->
            e


dch : Result String Char -> String
dch r =
    case r of
        Ok c ->
            "ok:" ++ String.fromList [ c ]

        Err e ->
            e


do : Result String DecOpt -> String
do r =
    case r of
        Ok o ->
            "ok:" ++ Maybe.withDefault "-" o.note ++ ":" ++ Decimal.toString o.amount

        Err e ->
            e



-- Decimal arrived here from a Go-vs-JS test that could not see the third
-- runtime, which is exactly where Equality was before it moved. Three
-- hand-ported implementations of exact arithmetic, and the one that runs on
-- the phone was never compared with the other two. It is money.
decimalCases : String
decimalCases =
    String.join ";"
        [ "add=" ++ Decimal.toString (0.1 + 0.2)
        , "addScale=" ++ Decimal.toString (1.10 + 2.20)
        , "sub=" ++ Decimal.toString (0.5 - 0.25)
        , "mul=" ++ Decimal.toString (1.5 * 1.5)
        , "mulSmall=" ++ Decimal.toString (0.10 * 0.10)
        , "negLiteral=" ++ Decimal.toString (-(12.50))
        , "abs=" ++ Decimal.toString (Decimal.abs (-2.5))
        , "negate=" ++ Decimal.toString (Decimal.negate 1.50)
        , "intDiv=" ++ String.fromInt (7 // 2)
        , "intDivNeg=" ++ String.fromInt (-7 // 2)
        , "intDivZero=" ++ String.fromInt (7 // 0)
        , "eqScale=" ++ yn (1.50 == 1.5)
        , "lt=" ++ yn (1.5 < 1.55)
        , "roundHalfEven4=" ++ Decimal.toString (Decimal.rounded Decimal.HalfEven 4 (1.0 / 3.0))
        , "roundHalfUp0=" ++ Decimal.toString (Decimal.rounded Decimal.HalfUp 0 (5.0 / 2.0))
        , "roundHalfEven0=" ++ Decimal.toString (Decimal.rounded Decimal.HalfEven 0 (5.0 / 2.0))
        , "roundDown=" ++ Decimal.toString (Decimal.rounded Decimal.Down 0 (-7.0 / 2.0))
        , "roundUp=" ++ Decimal.toString (Decimal.rounded Decimal.Up 0 (-7.0 / 2.0))
        , "roundFloor=" ++ Decimal.toString (Decimal.rounded Decimal.Floor 0 (-7.0 / 2.0))
        , "roundCeiling=" ++ Decimal.toString (Decimal.rounded Decimal.Ceiling 0 (-7.0 / 2.0))
        , "roundByZero=" ++ Decimal.toString (Decimal.rounded Decimal.HalfEven 2 (5.0 / 0.0))
        , "withRemainder=" ++ fmtQR (Decimal.withRemainder 2 (100.00 / Decimal.fromInt 3))
        , "withRemainderZero=" ++ fmtQR (Decimal.withRemainder 2 (5.0 / 0.0))
        , "toScaleHalfEvenA=" ++ Decimal.toString (Decimal.toScale Decimal.HalfEven 2 1.005)
        , "toScaleHalfEvenB=" ++ Decimal.toString (Decimal.toScale Decimal.HalfEven 2 1.015)
        , "fromCents=" ++ Decimal.toString (Decimal.fromCents 1234)
        , "toCents=" ++ String.fromInt (Decimal.toCents 12.34)
        , "truncate=" ++ String.fromInt (Decimal.truncate (-2.9))
        , "roundHalfEvenA=" ++ String.fromInt (Decimal.round 2.5)
        , "roundHalfEvenB=" ++ String.fromInt (Decimal.round 3.5)
        , "floor=" ++ String.fromInt (Decimal.floor (-2.1))
        , "ceiling=" ++ String.fromInt (Decimal.ceiling (-2.1))
        , "toIntWith=" ++ String.fromInt (Decimal.toIntWith Decimal.Up 2.1)
        , "compare=" ++ ordStr (Decimal.compare 1.5 1.50)
        , "fromStringOk=" ++ maybeDec (Decimal.fromString "12.50")
        , "fromStringBad=" ++ maybeDec (Decimal.fromString "nope")
        , "zero=" ++ Decimal.toString Decimal.zero

        -- List.sum / List.product over Decimal: the polymorphic half of
        -- the numeric kit, where the fold has to keep the scale rather
        -- than fall back to floating point.
        , "sumScales=" ++ Decimal.toString (List.sum [ 1.50, 2.25 ])
        , "sumCarry=" ++ Decimal.toString (List.sum [ 0.1, 0.2 ])
        , "sumOne=" ++ Decimal.toString (List.sum [ 19.99 ])
        , "sumEmpty=" ++ Decimal.toString (List.sum emptyMoney)
        , "productDec=" ++ Decimal.toString (List.product [ 1.5, 2.0 ])
        , "productEmpty=" ++ Decimal.toString (List.product emptyMoney)
        , "sumInt=" ++ String.fromInt (List.sum [ 1, 2, 3 ])
        , "productInt=" ++ String.fromInt (List.product [ 2, 3, 4 ])
        , "sumField=" ++ Decimal.toString (List.sum (List.map .amount decRows))
        ]


-- Declared below the list that reads them, on purpose: the checker orders
-- value declarations by dependency, so source order carries no meaning.
emptyMoney : List Decimal
emptyMoney =
    []


decRows : List { amount : Decimal }
decRows =
    [ { amount = 10.00 }, { amount = 2.50 }, { amount = 0.05 } ]


ordStr : Order -> String
ordStr o =
    case o of
        LT ->
            "LT"

        EQ ->
            "EQ"

        GT ->
            "GT"


fmtQR : { quotient : Decimal, remainder : Decimal } -> String
fmtQR r =
    Decimal.toString r.quotient ++ "|" ++ Decimal.toString r.remainder


maybeDec : Maybe Decimal -> String
maybeDec m =
    case m of
        Nothing ->
            "Nothing"

        Just d ->
            Decimal.toString d


-- Time was outside the corpus, and that is where a data loss hid: the wire
-- form and Time.toIso rendered SECONDS on the server and milliseconds in the
-- browser, so every service response carrying a sub-second instant lost it,
-- in one direction, silently. Three implementations of a calendar, compared
-- by nothing.
timeCases : String
timeCases =
    String.join ";"
        [ "toIso=" ++ Time.toIso base
        , "toIsoFraction=" ++ Time.toIso (instant "2023-11-14T22:13:20.123Z")
        , "fromIsoBad=" ++ maybeIso (Time.fromIso "nope")
        , "fromIsoNoFraction=" ++ maybeIso (Time.fromIso "2023-11-14T22:13:20Z")
        , "toMillis=" ++ String.fromInt (Time.toMillis base)
        , "durToSeconds=" ++ String.fromInt (Time.toSeconds (Time.minutes 3))
        , "fromYMD=" ++ Time.toIso (Time.fromYMD 2023 11 14)
        , "year=" ++ String.fromInt (Time.year base)
        , "month=" ++ String.fromInt (Time.month base)
        , "day=" ++ String.fromInt (Time.day base)
        , "hour=" ++ String.fromInt (Time.hour base)
        , "minute=" ++ String.fromInt (Time.minute base)
        , "second=" ++ String.fromInt (Time.second base)
        , "addSeconds=" ++ Time.toIso (Time.add base (Time.seconds 90))
        , "addMinutes=" ++ Time.toIso (Time.add base (Time.minutes 3))
        , "addHours=" ++ Time.toIso (Time.add base (Time.hours 2))
        , "addMillis=" ++ Time.toIso (Time.add base (Time.millis 250))
        , "sub=" ++ Time.toIso (Time.sub base (Time.days 1))
        , "addDays=" ++ Time.toIso (Time.addDays base 20)
        , "addMonths=" ++ Time.toIso (Time.addMonths base 3)
        , "addMonthsClamps=" ++ Time.toIso (Time.addMonths (instant "2024-01-31T00:00:00Z") 1)
        , "addYears=" ++ Time.toIso (Time.addYears base 1)
        , "addYearsLeap=" ++ Time.toIso (Time.addYears (instant "2024-02-29T00:00:00Z") 1)
        , "weeks=" ++ Time.toIso (Time.add base (Time.weeks 2))
        , "diff=" ++ String.fromInt (Time.toSeconds (Time.diff base (instant "2023-11-14T22:14:20.000Z")))
        , "diffBackwards=" ++ String.fromInt (Time.toSeconds (Time.diff (instant "2023-11-14T22:14:20.000Z") base))

        -- A Duration is whole milliseconds, so a shift by one is exact and
        -- Time.diff keeps what it measured.
        , "addOddMillis=" ++ Time.toIso (Time.add base (Time.millis 1001))
        , "subOddMillis=" ++ Time.toIso (Time.sub base (Time.millis 1003))
        , "diffMillis=" ++ yn (Time.diff base (Time.add base (Time.millis 1500)) == Time.millis 1500)
        , "diffRoundTrip=" ++ Time.toIso (Time.add base (Time.diff base (instant "2023-11-14T22:13:21.250Z")))
        , "toSecondsTowardZero=" ++ String.fromInt (Time.toSeconds (Time.diff (Time.add base (Time.millis 1500)) base))
        , "before=" ++ yn (Time.before base (Time.addDays base 1))
        , "after=" ++ yn (Time.after base (Time.addDays base 1))
        , "epochRoundTrip=" ++ Time.toIso (Time.fromYMD 1970 1 1)
        ]


base : Time
base =
    instant "2023-11-14T22:13:20.000Z"


maybeIso : Maybe Time -> String
maybeIso m =
    case m of
        Nothing ->
            "Nothing"

        Just t ->
            Time.toIso t


-- Random has a seedable half, Random.initialSeed and Random.step, so
-- the generators ARE comparable as values, and nothing was comparing them.
-- A shuffled deck, a level layout, an enemy roll: same seed, and the phone
-- has to deal the same cards as the browser. Three hand-ported generators.
randomCases : String
randomCases =
    String.join ";"
        [ "intOnce=" ++ String.fromInt (Tuple.first (Random.step (Random.int 1 6) (Random.initialSeed 42)))
        , "intAgainSameSeed=" ++ String.fromInt (Tuple.first (Random.step (Random.int 1 6) (Random.initialSeed 42)))
        , "intOtherSeed=" ++ String.fromInt (Tuple.first (Random.step (Random.int 1 6) (Random.initialSeed 7)))
        , "intWideRange=" ++ String.fromInt (Tuple.first (Random.step (Random.int 0 1000000) (Random.initialSeed 1)))
        , "intSinglePoint=" ++ String.fromInt (Tuple.first (Random.step (Random.int 5 5) (Random.initialSeed 99)))
        , "intNegativeRange=" ++ String.fromInt (Tuple.first (Random.step (Random.int (0 - 10) 10) (Random.initialSeed 3)))
        , "constant=" ++ String.fromInt (Tuple.first (Random.step (Random.constant 7) (Random.initialSeed 1)))
        , "map=" ++ String.fromInt (Tuple.first (Random.step (Random.map (\n -> n * 2) (Random.int 1 6)) (Random.initialSeed 42)))
        , "list=" ++ ss (List.map String.fromInt (Tuple.first (Random.step (Random.list 5 (Random.int 1 6)) (Random.initialSeed 42))))
        , "pair=" ++ pairStr (Tuple.first (Random.step (Random.pair (Random.int 1 6) (Random.int 1 6)) (Random.initialSeed 42)))
        , "uniform=" ++ String.fromInt (Tuple.first (Random.step (Random.uniform 10 [ 20, 30 ]) (Random.initialSeed 42)))
        , "andThen=" ++ String.fromInt (Tuple.first (Random.step (Random.andThen (\n -> Random.int n (n + 1)) (Random.int 1 6)) (Random.initialSeed 42)))
        , "sequence=" ++ ss (List.map String.fromInt (rollThree (Random.initialSeed 42)))
        , "map2=" ++ String.fromInt (Tuple.first (Random.step (Random.map2 (\a b -> a * 100 + b) (Random.int 1 6) (Random.int 1 6)) (Random.initialSeed 42)))
        , "map3=" ++ String.fromInt (Tuple.first (Random.step (Random.map3 (\a b c -> a * 10000 + b * 100 + c) (Random.int 1 6) (Random.int 1 6) (Random.int 1 6)) (Random.initialSeed 42)))
        ]


pairStr : ( Int, Int ) -> String
pairStr p =
    String.fromInt (Tuple.first p) ++ "/" ++ String.fromInt (Tuple.second p)


-- Three draws from ONE seed, threading it forward: the part that proves the
-- seed advances the same way everywhere, not just that one draw matches.
rollThree : Random.Seed -> List Int
rollThree s0 =
    let
        first =
            Random.step (Random.int 1 100) s0

        second =
            Random.step (Random.int 1 100) (Tuple.second first)

        third =
            Random.step (Random.int 1 100) (Tuple.second second)
    in
    [ Tuple.first first, Tuple.first second, Tuple.first third ]


-- linkTo was excused from the corpus as "builds a UI link; compared by the
-- renderer parity tests". It returns a STRING, which is exactly what this
-- corpus compares, and the renderer tests compare screens rather than the URL
-- a link carries. Measured under that excuse, for a String param:
--
--     value    Go            JS            Swift
--     "a b"    /item/a b     /item/a%20b   /item/a%20b
--     "a/b"    /item/a/b     /item/a%2Fb   /item/a/b
--     "a?b"    /item/a?b     /item/a%3Fb   /item/a%3Fb
--
-- The server did no encoding at all, so a slug with a slash became TWO
-- segments and stopped matching its own pattern, and one with a ? became a
-- query string.
linkCases : String
linkCases =
    String.join ";"
        [ "plain=" ++ linkTo itemPath { slug = "ok" }
        , "space=" ++ linkTo itemPath { slug = "a b" }
        , "slash=" ++ linkTo itemPath { slug = "a/b" }
        , "question=" ++ linkTo itemPath { slug = "a?b" }
        , "hash=" ++ linkTo itemPath { slug = "a#b" }
        , "accent=" ++ linkTo itemPath { slug = "ç" }
        , "ampersand=" ++ linkTo itemPath { slug = "a&b" }
        , "quote=" ++ linkTo itemPath { slug = "a'b" }
        , "parens=" ++ linkTo itemPath { slug = "a(b)" }
        , "unreserved=" ++ linkTo itemPath { slug = "a-b._~c" }
        , "empty=" ++ linkTo itemPath { slug = "" }
        , "intParam=" ++ linkTo countPath { n = 42 }
        , "intNegative=" ++ linkTo countPath { n = -7 }
        , "enumParam=" ++ linkTo rolePath { role = LinkAdmin }
        ]


itemPath : Path { slug : String }
itemPath =
    "/item/{slug:String}"


countPath : Path { n : Int }
countPath =
    "/count/{n:Int}"


rolePath : Path { role : LinkRole }
rolePath =
    "/who/{role:LinkRole}"


type LinkRole
    = LinkAdmin
    | LinkMember

decodeCases : String
decodeCases =
    String.join ";"
        [ "ok=" ++ du (JSON.decode "{\"name\":\"ana\",\"age\":3}")
        , "notObject=" ++ du (JSON.decode "42")
        , "missingField=" ++ du (JSON.decode "{\"name\":\"ana\"}")
        , "wrongFieldType=" ++ du (JSON.decode "{\"name\":7,\"age\":3}")
        , "nullIsNotARecord=" ++ du (JSON.decode "null")
        , "extraIgnored=" ++ du (JSON.decode "{\"name\":\"ana\",\"age\":3,\"x\":1}")
        , "badTag=" ++ ds (JSON.decode "{\"__ctor\":\"Purple\"}")
        , "goodTag=" ++ ds (JSON.decode "{\"__ctor\":\"DecSent\"}")
        , "listOk=" ++ dl (JSON.decode "[1,2]")
        , "listElem=" ++ dl (JSON.decode "[1,\"x\"]")
        , "notAList=" ++ dl (JSON.decode "5")
        , "optionalAbsent=" ++ do (JSON.decode "{\"amount\":3}")
        , "optionalNull=" ++ do (JSON.decode "{\"note\":null,\"amount\":3}")
        , "optionalJust=" ++ do (JSON.decode "{\"note\":{\"__ctor\":\"Just\",\"__args\":[\"hi\"]},\"amount\":3}")
        , "optionalRaw=" ++ do (JSON.decode "{\"note\":\"hi\",\"amount\":3}")

        -- A union with a PAYLOAD, and a tuple: the two shapes the guard
        -- announced itself on and then checked nothing about.
        , "ctorOk=" ++ dp (JSON.decode "{\"__ctor\":\"DecCircle\",\"__args\":[3]}")
        , "ctorTwoArgs=" ++ dp (JSON.decode "{\"__ctor\":\"DecNamed\",\"__args\":[\"a\",2]}")
        , "ctorUnknownTag=" ++ dp (JSON.decode "{\"__ctor\":\"DecRoxo\",\"__args\":[1]}")
        , "ctorWrongArity=" ++ dp (JSON.decode "{\"__ctor\":\"DecCircle\",\"__args\":[]}")
        , "ctorWrongArgType=" ++ dp (JSON.decode "{\"__ctor\":\"DecCircle\",\"__args\":[\"x\"]}")
        , "ctorNotACtor=" ++ dp (JSON.decode "{}")

        -- A type that re-enters itself under a Maybe: the re-entry below the
        -- Just is checked like the first level.
        , "threadOk=" ++ dth (JSON.decode "{\"__ctor\":\"DecThread\",\"__args\":[\"a\",{\"__ctor\":\"Just\",\"__args\":[{\"__ctor\":\"DecThread\",\"__args\":[\"b\",{\"__ctor\":\"Nothing\"}]}]}]}")
        , "threadDeepWrong=" ++ dth (JSON.decode "{\"__ctor\":\"DecThread\",\"__args\":[\"a\",{\"__ctor\":\"Just\",\"__args\":[{\"__ctor\":\"DecThread\",\"__args\":[5,{\"__ctor\":\"Nothing\"}]}]}]}")
        , "tupleOk=" ++ dt (JSON.decode "[7,\"abc\"]")
        , "tupleShort=" ++ dt (JSON.decode "[7]")
        , "tupleWrongType=" ++ dt (JSON.decode "[\"x\",\"abc\"]")
        , "tupleNotAList=" ++ dt (JSON.decode "{\"a\":1}")

        -- An Angle has to survive the wire: it goes into App.shared, through
        -- time travel, and into a rules engine replayed on both sides. The
        -- server branch tested for a Go float64 while the decoder runs
        -- with UseNumber, so it was DEAD: the browser and the phone rebuilt
        -- an Angle from these bytes and the server handed back a record.
        , "angleOk=" ++ dan (JSON.decode "{\"__angle\":900}")
        , "angleWraps=" ++ dan (JSON.decode "{\"__angle\":4500}")
        , "angleFraction=" ++ dan (JSON.decode "{\"__angle\":1.5}")

        -- The rest of the tagged family, for the same reason: the Angle
        -- branch was DEAD in one runtime and nothing was comparing them.
        , "timeTagged=" ++ dtm (JSON.decode "{\"__time\":\"2023-11-14T22:13:20Z\"}")
        , "timeNotATime=" ++ dtm (JSON.decode "{\"__time\":\"nope\"}")
        , "decTagged=" ++ dde (JSON.decode "{\"__dec\":\"1.50\"}")
        , "decNotADec=" ++ dde (JSON.decode "{\"__dec\":\"x\"}")
        , "dictTagged=" ++ ddi (JSON.decode "{\"__dict\":[[1,2]]}")
        , "dictNotPairs=" ++ ddi (JSON.decode "{\"__dict\":[[1]]}")
        , "setTagged=" ++ dse (JSON.decode "{\"__set\":[2,1,2]}")
        , "setNotAList=" ++ dse (JSON.decode "{\"__set\":\"x\"}")

        -- A Set and a Dict are SORTED, so building one compares its members.
        -- Go and JS failed that comparison with a message about comparison
        -- rather than about the request; Swift did not fail at all and built
        -- a collection of mixed types.
        , "setMixedTypes=" ++ dse (JSON.decode "{\"__set\":[1,\"x\"]}")
        , "dictMixedKeys=" ++ ddi (JSON.decode "{\"__dict\":[[1,2],[\"a\",3]]}")

        -- Time.toIso rendered seconds on the server and milliseconds in the
        -- browser, and the WIRE used the server form: measured, a Time
        -- carrying 1700000000123 encoded to "…:20Z" and decoded back to
        -- …000. Every service response with a sub-second Time lost it,
        -- silently, in one direction only.
        , "isoMillis=" ++ isoOf "2023-11-14T22:13:20.123Z"
        , "isoWhole=" ++ isoOf "2023-11-14T22:13:20Z"
        , "isoRoundTrip=" ++ dtm (JSON.decode (JSON.encode (instant "2023-11-14T22:13:20.123Z")))

        -- A Char is one Unicode scalar, and the payload is EXTERNAL: no
        -- Mar encoder can write more than one. All three used to take the
        -- FIRST scalar and drop the rest, and they disagreed about an
        -- empty one: the server errored while the browser and the phone
        -- answered U+FFFD. Three answers to one JSON document.
        , "charOk=" ++ dch (JSON.decode "{\"__char\":\"a\"}")
        , "charTooMany=" ++ dch (JSON.decode "{\"__char\":\"abc\"}")
        , "charEmpty=" ++ dch (JSON.decode "{\"__char\":\"\"}")
        , "charAstral=" ++ dch (JSON.decode "{\"__char\":\"\\ud83d\\ude00\"}")

        -- A String is a sequence of Unicode scalars, so an escape of a
        -- surrogate that is not half of a pair reads as U+FFFD. The last
        -- case is an escaped backslash followed by text, not an escape.
        , "loneHigh=" ++ dcodes (JSON.decode "\"x\\ud800y\"")
        , "loneLow=" ++ dcodes (JSON.decode "\"x\\udc00y\"")
        , "pairKept=" ++ dcodes (JSON.decode "\"\\ud83d\\ude00\"")
        , "loneThenPair=" ++ dcodes (JSON.decode "\"\\ud800\\ud83d\\ude00\"")
        , "loneInChar=" ++ dch (JSON.decode "{\"__char\":\"\\udfff\"}")
        , "escapedBackslash=" ++ dcodes (JSON.decode "\"\\\\ud800\"")

        -- A Duration is {"__duration": ms}: an integer literal inside the
        -- range of Int, and nothing else.
        , "durationOk=" ++ ddu (JSON.decode "{\"__duration\":1500}")
        , "durationNeg=" ++ ddu (JSON.decode "{\"__duration\":-1}")
        , "durationFraction=" ++ ddu (JSON.decode "{\"__duration\":1.5}")
        , "durationExponent=" ++ ddu (JSON.decode "{\"__duration\":1e3}")
        , "durationTooLong=" ++ ddu (JSON.decode "{\"__duration\":9007199254740992}")
        , "durationNotANumber=" ++ ddu (JSON.decode "{\"__duration\":\"5\"}")
        , "durationLeadingZero=" ++ ddu (JSON.decode "{\"__duration\":007}")
        , "durationTrailingDot=" ++ ddu (JSON.decode "{\"__duration\":1.}")
        , "durationNotADuration=" ++ q (ddu (JSON.decode "1500"))
        , "durationIsATime=" ++ q (ddu (JSON.decode "{\"__time\":\"2023-11-14T22:13:20Z\"}"))
        , "loneKey=" ++ q (dkeyed (JSON.decode "{\"a\":1,\"\\ud800\":2}"))

        -- With no type to decode into, a tagged value that cannot be one is
        -- refused in its own words too, and only a document that does not
        -- parse is "not valid JSON".
        , "unpinnedDuration=" ++ unpinned "{\"__duration\":1.5}"
        , "unpinnedAngle=" ++ unpinned "{\"__angle\":1.5}"
        , "unpinnedChar=" ++ unpinned "{\"__char\":\"abc\"}"
        , "unpinnedNotJSON=" ++ unpinned "{\"a\":"
        ]


type alias EqP =
    { a : Int, b : Bool }


eqOne : EqP
eqOne =
    { a = 1, b = True }


eqTwo : EqP
eqTwo =
    { a = 1, b = True }


eqOther : EqP
eqOther =
    { a = 2, b = True }


-- The same fields written in the other order. Field order is display
-- metadata: it decides how a record renders, so it must not affect identity.
eqFlipped : EqP
eqFlipped =
    { b = True, a = 1 }


eqNone : Maybe EqP
eqNone =
    Nothing


equalityCases : String
equalityCases =
    String.join ";"
        [ "recSelf=" ++ yn (eqOne == eqOne)
        , "recEqual=" ++ yn (eqOne == eqTwo)
        , "recFieldOrder=" ++ yn (eqOne == eqFlipped)
        , "recDiffer=" ++ yn (eqOne == eqOther)
        , "recNeEqual=" ++ yn (eqOne /= eqTwo)
        , "recNeDiffer=" ++ yn (eqOne /= eqOther)

        -- records nested in a record
        , "nestSame=" ++ yn ({ inner = eqOne, tag = "x" } == { inner = eqTwo, tag = "x" })
        , "nestTag=" ++ yn ({ inner = eqOne, tag = "x" } == { inner = eqTwo, tag = "y" })
        , "nestInner=" ++ yn ({ inner = eqOne, tag = "x" } == { inner = eqOther, tag = "x" })

        -- the container cases recurse through record equality, so each of
        -- these broke too while the record case was missing on the web
        , "justSame=" ++ yn (Just eqOne == Just eqTwo)
        , "justDiffer=" ++ yn (Just eqOne == Just eqOther)
        , "justVsNothing=" ++ yn (Just eqOne == eqNone)
        , "nothingSame=" ++ yn (eqNone == eqNone)
        , "listSame=" ++ yn ([ eqOne, eqOther ] == [ eqTwo, eqOther ])
        , "listDiffer=" ++ yn ([ eqOne ] == [ eqOther ])
        , "tupleSame=" ++ yn (( eqOne, 2 ) == ( eqTwo, 2 ))
        , "tupleDiffer=" ++ yn (( eqOne, 2 ) == ( eqTwo, 3 ))

        -- List.member is structural equality wearing another name
        , "memberFound=" ++ yn (List.member eqTwo [ eqOther, eqOne ])
        , "memberMissing=" ++ yn (List.member eqOther [ eqOne, eqTwo ])

        -- A Duration is milliseconds, so the unit one was BUILT with is not
        -- part of its identity.
        , "durSame=" ++ yn (Time.seconds 1 == Time.seconds 1)
        , "durMinutes=" ++ yn (Time.seconds 60 == Time.minutes 1)
        , "durHours=" ++ yn (Time.hours 1 == Time.minutes 60)
        , "durDiffer=" ++ yn (Time.seconds 1 == Time.seconds 2)
        , "durNe=" ++ yn (Time.seconds 1 /= Time.seconds 2)
        , "durInJust=" ++ yn (Just (Time.seconds 1) == Just (Time.seconds 1))
        , "durInRecord=" ++ yn ({ d = Time.seconds 1 } == { d = Time.seconds 1 })

        -- Time: the same moment is the same Unix millisecond.
        , "timeSame=" ++ yn (Time.fromYMD 2026 8 11 == Time.fromYMD 2026 8 11)
        , "timeDiffer=" ++ yn (Time.fromYMD 2026 8 11 == Time.fromYMD 2026 8 12)
        , "timeInRecord=" ++ yn ({ at = Time.fromYMD 2026 8 11 } == { at = Time.fromYMD 2026 8 11 })
        , "timeInList=" ++ yn (List.member (Time.fromYMD 2026 8 11) [ Time.fromYMD 2026 8 12, Time.fromYMD 2026 8 11 ])

        -- Dict / Set were handled on the web and on iOS and missing from Go
        -- only, so the same comparison answered True in the browser and on
        -- the phone and False on the server.
        , "dictOrder=" ++ yn (Dict.fromList [ ( 1, "a" ), ( 2, "b" ) ] == Dict.fromList [ ( 2, "b" ), ( 1, "a" ) ])
        , "dictValue=" ++ yn (Dict.fromList [ ( 1, "a" ) ] == Dict.fromList [ ( 1, "b" ) ])
        , "dictSize=" ++ yn (Dict.fromList [ ( 1, "a" ) ] == Dict.fromList [ ( 1, "a" ), ( 2, "b" ) ])
        , "setOrder=" ++ yn (Set.fromList [ 3, 1, 2 ] == Set.fromList [ 1, 2, 3 ])
        , "setDiffer=" ++ yn (Set.fromList [ 1 ] == Set.fromList [ 2 ])
        , "setInRecord=" ++ yn ({ s = Set.fromList [ 1, 2 ] } == { s = Set.fromList [ 2, 1 ] })
        ]


results =
    String.join "\n" [ basicsCases, stringCases, textCases, listCases, maybeCases, resultCases, tupleCases, charCases, dictCases, setCases, jsonCases, mathCases, shortCircuitCases, orderingCases, decodeCases, decimalCases, timeCases, randomCases, linkCases, equalityCases ]
`

// Entry is the value each runtime is asked to evaluate.
const Entry = "Conform.results"

// Blocks names the blocks `results` joins, in that order. Case labels repeat
// across modules, `map`, `foldl`, `filter`, `toList`, so a case is named for
// the block it landed in: `Dict.map`, not `map`.
var Blocks = []string{
	"Basics", "String", "Text", "List", "Maybe", "Result", "Tuple", "Char", "Dict", "Set", "JSON", "Math",
	"ShortCircuit",
	// Ordering is the one block that is about OPERATORS rather than named
	// functions, which is why it had to be written by hand: the coverage
	// gate walks qualified stdlib names and bare globals, and `<`, `>`,
	// `<=`, `>=` are in neither set. Comparison was three implementations
	// with nothing comparing them — the same blind spot that hid `modBy`.
	"Ordering",
	// Decode is about what JSON.decode does with a value that does NOT fit
	// the type the call site asked for. It answered `Ok` for anything that
	// parsed, in all three, and the mismatch surfaced later and elsewhere.
	// The messages are pinned to the character because three runtimes now
	// answer the same question and the only thing keeping their answers the
	// same sentence is this block. Parse failures are deliberately absent:
	// the three host JSON parsers word those differently and always have.
	"Decode",
	// Equality moved here from a Go-vs-JS test that could not see the third
	// runtime. It is the block with the best claim to a place in a SHARED
	// corpus: `==` is one switch per runtime with nothing forcing the three
	// to cover the same kinds, and it has already been wrong twice — records
	// answered False on the web while Go and iOS said True, and Dict and Set
	// answered False on the server while the other two said True.
	// Decimal came from the same place, for the same reason, and it is
	// money: three hand-ported implementations of exact arithmetic, and
	// the one on the phone was never compared with the other two.
	"Decimal",
	// Time was outside it, and that is where the milliseconds went.
	"Time",
	// Random has a seedable half, so its generators are comparable as
	// values. A shuffled deck has to deal the same cards on the phone.
	"Random",
	// linkTo returns a String, which is what this corpus compares. The
	// excuse that the renderer tests covered it was how three different
	// URL encodings lived side by side.
	"Link",
	"Equality",
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
	// Decimal, Time and Random join the gate, not just the corpus. Being
	// compared once is not the point: the point is that a function added
	// tomorrow cannot enter without being compared, which is exactly how
	// `Time.toIso` came to render seconds on the server and milliseconds
	// in the browser with nothing noticing.
	"Decimal": true,
	"Time":    true,
	"Random":  true,
}

// OutOfScopeBare is the bare-global counterpart to OutOfScope: a name spelled
// without a qualifier that cannot be compared as a value. Everything else in
// typecheck.BareGlobals() has to appear in Source, which is the gate that was
// missing when `modBy` diverged: the module walk skips dotless names, so the
// numeric kit had never been compared across runtimes at all.
var OutOfScopeBare = map[string]string{}

// OutOfScopeFunc excuses a SINGLE function, which the module-level map cannot
// do. It exists because three modules came into Scope carrying a handful of
// effects: the rest of Decimal, Time and Random is pure and comparable, and
// excusing the whole module for the sake of `Time.now` is how `Time.toIso`
// went years rendering seconds on the server and milliseconds in the browser.
//
// The gate consults this map rather than looking for the name in the corpus
// text, so an excuse has to be written down here rather than being any mention
// anywhere — a comment naming the function would otherwise satisfy it.
var OutOfScopeFunc = map[string]string{
	"Time.now":        "reads the wall clock; there is no answer two runtimes could agree on",
	"Time.every":      "a subscription; its ticking is pinned by the vsync tests",
	"Random.seed":     "a Task that reads entropy; the generators it feeds are covered",
	"Random.generate": "a Cmd; Random.step is the same algorithm with the seed in hand",
}

// OutOfScope is every other stdlib module, with the reason it is not compared
// here. Together the two maps have to cover the whole stdlib: a module in
// neither fails the build, so a new one cannot slip in untested by being
// unnoticed.
var OutOfScope = map[string]string{
	// Render rather than compute: the answer is a screen, and the three
	// renderers are compared by the drift and parity tests instead.
	"UI":     "renders; compared by the renderer parity tests",
	"Canvas": "renders; compared by the pixel-golden tests",
	// Out of THIS corpus, which reduces everything to a String, and a Sound
	// cannot be one. It is not out of conformance: sound.go in this package
	// holds its own corpus, compared voice by voice across the two runtimes
	// that synthesise, and gated the same way.
	//
	// The old reason here said "compared by the sound shaping tests", and
	// those ran node only. An exemption that names cover it does not have is
	// worse than no exemption, because it stops anyone from looking: that is
	// how the master level sat at 0.35 on one side and 0.5 on the other.
	"Sound": "builds audio, not a value; compared by the sound corpus (sound.go)",

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

// TextGap is one case in the Text block where the three runtimes answer
// differently, pinned at what each of them answers.
//
// A conformance test that fails for a known reason gets ignored, and a known
// reason that is not written down gets forgotten. KnownWaveLevelGaps in
// sound_playback.go is the same bargain as this one: a case listed here does
// not fail for DISAGREEING, and does fail the moment any runtime's answer
// MOVES, in either direction. The fix for a fixed one is to delete its entry,
// which the coverage gate then requires an expectation for.
//
// Each Why says whether the entry is a defect with an answer the other
// runtimes already agree on, or a question the language has not answered.
type TextGap struct {
	Go, JS, Swift string
	Why           string
}

func (g TextGap) answerFor(who string) (string, bool) {
	switch who {
	case "Go":
		return g.Go, true
	case "JS":
		return g.JS, true
	case "Swift":
		return g.Swift, true
	}
	return "", false
}

// KnownTextGaps is empty, and that is the point: the three runtimes answer
// every case in the Text block identically.
//
// Keep the mechanism. The next divergence someone measures and cannot settle
// in the same sitting belongs here rather than in a document: an entry does
// not fail for DISAGREEING, and does fail the moment any runtime's answer
// MOVES, in either direction, which is what stops a known problem from
// quietly becoming a different one.
var KnownTextGaps = map[string]TextGap{}

// expectations are worked out from the expressions in Source rather than
// recorded from a run. That is the whole point: a value copied out of a runtime
// proves the runtime equals itself. `List.foldl` folding `\x acc -> acc * 10 + x`
// over [1,2,3] has to be 123 and `List.foldr` has to be 321 because of which
// end each starts from; `Dict.union` has to keep the left dictionary's `b` at
// 2; `Set.fromList [3,1,2,1]` has to be 1,2,3 because a set is sorted and
// deduplicated. If a runtime disagrees with a line here, the line is the
// argument.
const expectations = `
Equality.recSelf=T
Equality.recEqual=T
Equality.recFieldOrder=T
Equality.recDiffer=F
Equality.recNeEqual=F
Equality.recNeDiffer=T
Equality.nestSame=T
Equality.nestTag=F
Equality.nestInner=F
Equality.justSame=T
Equality.justDiffer=F
Equality.justVsNothing=F
Equality.nothingSame=T
Equality.listSame=T
Equality.listDiffer=F
Equality.tupleSame=T
Equality.tupleDiffer=F
Equality.memberFound=T
Equality.memberMissing=F
Equality.durSame=T
Equality.durMinutes=T
Equality.durHours=T
Equality.durDiffer=F
Equality.durNe=T
Equality.durInJust=T
Equality.durInRecord=T
Equality.timeSame=T
Equality.timeDiffer=F
Equality.timeInRecord=T
Equality.timeInList=T
Equality.dictOrder=T
Equality.dictValue=F
Equality.dictSize=F
Equality.setOrder=T
Equality.setDiffer=F
Equality.setInRecord=T
Ordering.ltInt=T
Ordering.ltString=T
Ordering.ltChar=T
Ordering.tupleFirstWins=T
Ordering.tupleTieBreaks=T
Ordering.tupleEqual=F
Ordering.tupleGe=T
Ordering.leEqualTuple=T
Ordering.leList=T
Ordering.geChar=T
Ordering.neInt=T
Ordering.neEqual=F
Ordering.neRecord=T
Ordering.neTuple=T
Ordering.tripleTie=T
Ordering.listPrefixIsLess=T
Ordering.listFirstWins=F
Ordering.listEmptyIsLeast=T
Ordering.listEqual=F
Ordering.listOfTuples=T
Ordering.sortTuples=a,a,b
Ordering.sortLists=1|1,2|2
Ordering.sortByTupleKey=2,4,1,3
Ordering.maxTuple=2
Ordering.minList=1,5
Ordering.maxOfTuples=2
Ordering.sortStability=b,d,a,c
Ordering.dictTupleKeys=2
Ordering.dictDupKeepsLast=3
Ordering.dictTupleOrder=1/2,1/9,2/1
Ordering.setTupleKeys=2
Ordering.setListItems=1|2
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

Text.lenAcute=4
Text.lenCJK=2
Text.toListAcute=4
Text.toListCombining=5
Text.toListGrin=1
Text.toListFlag=2
Text.codeAcute=233
Text.codeGrin=128512
Text.fromCodeHigh=😀
Text.fromCodeSurrogate=65533
Text.fromCodePastMax=65533
Text.fromCodeMax=1114111
Text.fromCodeNegative=65533
Text.charUpperSharpS=ß
Text.charLowerDotted=i
Text.charUpperAcute=É
Text.isAlphaAcute=T
Text.isUpperAcute=T
Text.upperAcute=CAFÉ
Text.lowerAcute=café
Text.indexesAcute=3
Text.ltNormal=F
Text.containsAcute=T
Text.mapAcute=CAFÉ
Text.filterAcute=café
Text.foldlAcute=4
Text.consAcute=éx
Text.anyAcuteAlpha=T
Text.endsAcute=T
Text.trimAcute=café
Text.splitAcute=caf|
Text.replaceAcute=cafe
Text.lenCombining=5
Text.lenGrin=1
Text.lenFlag=2
Text.lawLenCombining=T
Text.lawLenFlag=T
Text.indexesAfterGrin=1
Text.padGrin=..😀
Text.eqNormal=F
Text.containsCombining=F
Text.startsCombining=F
Text.dictAccentSize=2
Text.dictAccentGet=1
Text.setAccentSize=2
Text.splitEmpty=a|é
Text.splitEmptyAstral=a|😀
Text.replaceEmpty=a-b
Text.replaceEmptyAstral=a-😀
Text.lowerFinalSigma=σοφος
Text.padAcute=..café
Text.padCJK=..日本
Text.padRightAcute=café..
Text.indexesAfterAcute=1
Text.upperSharpS=STRASSE
Text.sortAstral=z|�|😀
Text.upperLigature=FILE
Text.lowerDotted=i̇stanbul
Text.fromListMixed=é😀
Text.repeatAcute=éé
Text.joinAcute=aéb
Text.charLowerAcute=é
Text.isLowerAcute=T
Text.isDigitArabic=T
Text.isAlphaCJK=T

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
JSON.encodeOk={'__args':[1],'__ctor':'Ok'}
JSON.encodeErr={'__args':['bad'],'__ctor':'Err'}
JSON.encodeJustOk={'__args':[{'__args':[1],'__ctor':'Ok'}],'__ctor':'Just'}
JSON.roundTripOk=Ok 1
JSON.roundTripErr=Err bad
JSON.encodeDuration={'__duration':100}
JSON.encodeDurationNeg={'__duration':-2500}
JSON.encodeKeyOrder={'a10':1,'a2':2,'endTime':3,'ends':4,'posX':5,'position':6}
JSON.encodeKeyOrderWide={'xａ':2,'x𝑥':1}

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

ShortCircuit.andTT=T
ShortCircuit.andTF=F
ShortCircuit.andFT=F
ShortCircuit.andFF=F
ShortCircuit.orTT=T
ShortCircuit.orTF=T
ShortCircuit.orFT=T
ShortCircuit.orFF=F

ShortCircuit.andSkips=F
ShortCircuit.orSkips=T
ShortCircuit.andEvaluates=T
ShortCircuit.orEvaluates=T
` + decodeExpectations + decimalExpectations + timeExpectations + randomExpectations + linkExpectations

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
		// A case in KnownTextGaps is answered differently by the three
		// runtimes on purpose, and is measured against the answer THIS
		// runtime was pinned at rather than against a shared one. It
		// still fails if that answer moves.
		if gap, ok := KnownTextGaps[name]; ok {
			pinned, isRuntime := gap.answerFor(who)
			switch {
			case !isRuntime:
				problems = append(problems, fmt.Sprintf("%s: KnownTextGaps pins Go, JS and Swift, and this run calls itself %q", name, who))
			case g != pinned:
				problems = append(problems, fmt.Sprintf("%s: a known divergence MOVED. %s was pinned at %s and now answers %s.\n"+
					"Either a fix landed, in which case delete the entry once all three agree and write\n"+
					"the expectation they agree on, or something drifted.\n%s", name, who, pinned, g, gap.Why))
			}
			continue
		}
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
		// Reported by Check instead, against each runtime's own pinned
		// answer: these disagree knowingly, and saying so twice would
		// bury the cases that disagree by accident.
		if _, known := KnownTextGaps[name]; known {
			continue
		}
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

// The decode messages name fields in backticks, and the block above is a Go
// raw string literal, which a backtick ends. Interpreted string literals have
// no such trouble, so these live here rather than being reworded to suit the
// file they are written in.
const linkExpectations = "" +
	"Link.accent=/item/%C3%A7" + "\n" +
	"Link.ampersand=/item/a%26b" + "\n" +
	"Link.empty=/item/" + "\n" +
	"Link.enumParam=/who/linkadmin" + "\n" +
	"Link.hash=/item/a%23b" + "\n" +
	"Link.intNegative=/count/-7" + "\n" +
	"Link.intParam=/count/42" + "\n" +
	"Link.parens=/item/a%28b%29" + "\n" +
	"Link.plain=/item/ok" + "\n" +
	"Link.question=/item/a%3Fb" + "\n" +
	"Link.quote=/item/a%27b" + "\n" +
	"Link.slash=/item/a%2Fb" + "\n" +
	"Link.space=/item/a%20b" + "\n" +
	"Link.unreserved=/item/a-b._~c" + "\n" +
	""

const randomExpectations = "" +
	"Random.andThen=4" + "\n" +
	"Random.constant=7" + "\n" +
	"Random.intAgainSameSeed=3" + "\n" +
	"Random.intNegativeRange=-1" + "\n" +
	"Random.intOnce=3" + "\n" +
	"Random.intOtherSeed=1" + "\n" +
	"Random.intSinglePoint=5" + "\n" +
	"Random.intWideRange=535848" + "\n" +
	"Random.list=3,6,2,3,3" + "\n" +
	"Random.map=6" + "\n" +
	"Random.map2=306" + "\n" +
	"Random.map3=30602" + "\n" +
	"Random.pair=3/6" + "\n" +
	"Random.sequence=95,20,60" + "\n" +
	"Random.uniform=30" + "\n" +
	""

const timeExpectations = "" +
	"Time.addDays=2023-12-04T22:13:20.000Z" + "\n" +
	"Time.addHours=2023-11-15T00:13:20.000Z" + "\n" +
	"Time.addMillis=2023-11-14T22:13:20.250Z" + "\n" +
	"Time.addMinutes=2023-11-14T22:16:20.000Z" + "\n" +
	"Time.addMonths=2024-02-14T22:13:20.000Z" + "\n" +
	"Time.addMonthsClamps=2024-02-29T00:00:00.000Z" + "\n" +
	"Time.addSeconds=2023-11-14T22:14:50.000Z" + "\n" +
	"Time.addYears=2024-11-14T22:13:20.000Z" + "\n" +
	"Time.addYearsLeap=2025-02-28T00:00:00.000Z" + "\n" +
	"Time.after=F" + "\n" +
	"Time.before=T" + "\n" +
	"Time.day=14" + "\n" +
	"Time.diff=60" + "\n" +
	"Time.diffBackwards=-60" + "\n" +
	"Time.addOddMillis=2023-11-14T22:13:21.001Z" + "\n" +
	"Time.subOddMillis=2023-11-14T22:13:18.997Z" + "\n" +
	"Time.diffMillis=T" + "\n" +
	"Time.diffRoundTrip=2023-11-14T22:13:21.250Z" + "\n" +
	"Time.toSecondsTowardZero=-1" + "\n" +
	"Time.durToSeconds=180" + "\n" +
	"Time.epochRoundTrip=1970-01-01T00:00:00.000Z" + "\n" +
	"Time.fromIsoBad=Nothing" + "\n" +
	"Time.fromIsoNoFraction=2023-11-14T22:13:20.000Z" + "\n" +
	"Time.fromYMD=2023-11-14T00:00:00.000Z" + "\n" +
	"Time.hour=22" + "\n" +
	"Time.minute=13" + "\n" +
	"Time.month=11" + "\n" +
	"Time.second=20" + "\n" +
	"Time.sub=2023-11-13T22:13:20.000Z" + "\n" +
	"Time.toIso=2023-11-14T22:13:20.000Z" + "\n" +
	"Time.toIsoFraction=2023-11-14T22:13:20.123Z" + "\n" +
	"Time.toMillis=1700000000000" + "\n" +
	"Time.weeks=2023-11-28T22:13:20.000Z" + "\n" +
	"Time.year=2023" + "\n" +
	""

const decimalExpectations = "" +
	"Decimal.abs=2.5" + "\n" +
	"Decimal.add=0.3" + "\n" +
	"Decimal.addScale=3.30" + "\n" +
	"Decimal.ceiling=-2" + "\n" +
	"Decimal.compare=EQ" + "\n" +
	"Decimal.eqScale=T" + "\n" +
	"Decimal.floor=-3" + "\n" +
	"Decimal.fromCents=12.34" + "\n" +
	"Decimal.fromStringBad=Nothing" + "\n" +
	"Decimal.fromStringOk=12.50" + "\n" +
	"Decimal.intDiv=3" + "\n" +
	"Decimal.intDivNeg=-3" + "\n" +
	"Decimal.intDivZero=0" + "\n" +
	"Decimal.lt=T" + "\n" +
	"Decimal.mul=2.25" + "\n" +
	"Decimal.mulSmall=0.0100" + "\n" +
	"Decimal.negLiteral=-12.50" + "\n" +
	"Decimal.negate=-1.50" + "\n" +
	"Decimal.roundByZero=0.00" + "\n" +
	"Decimal.roundCeiling=-3" + "\n" +
	"Decimal.roundDown=-3" + "\n" +
	"Decimal.roundFloor=-4" + "\n" +
	"Decimal.roundHalfEven0=2" + "\n" +
	"Decimal.roundHalfEven4=0.3333" + "\n" +
	"Decimal.roundHalfEvenA=2" + "\n" +
	"Decimal.roundHalfEvenB=4" + "\n" +
	"Decimal.roundHalfUp0=3" + "\n" +
	"Decimal.roundUp=-4" + "\n" +
	"Decimal.sub=0.25" + "\n" +
	"Decimal.toCents=1234" + "\n" +
	"Decimal.toIntWith=3" + "\n" +
	"Decimal.toScaleHalfEvenA=1.00" + "\n" +
	"Decimal.toScaleHalfEvenB=1.02" + "\n" +
	"Decimal.truncate=-2" + "\n" +
	"Decimal.withRemainder=33.33|0.01" + "\n" +
	"Decimal.withRemainderZero=0.00|5.000" + "\n" +
	"Decimal.zero=0" + "\n" +
	"Decimal.productDec=3.00" + "\n" +
	"Decimal.productEmpty=1" + "\n" +
	"Decimal.productInt=24" + "\n" +
	"Decimal.sumCarry=0.3" + "\n" +
	"Decimal.sumEmpty=0" + "\n" +
	"Decimal.sumField=12.55" + "\n" +
	"Decimal.sumInt=6" + "\n" +
	"Decimal.sumOne=19.99" + "\n" +
	"Decimal.sumScales=3.75" + "\n" +
	""

const decodeExpectations = "Decode.ok=ok:ana:3\n" +
	"Decode.notObject=the value should be an object, got a whole number\n" +
	"Decode.missingField=the value is missing the field `age`\n" +
	"Decode.wrongFieldType=`name` should be a string, got a whole number\n" +
	"Decode.nullIsNotARecord=the value should be an object, got nothing\n" +
	"Decode.extraIgnored=ok:ana:3\n" +
	"Decode.badTag=the value should be one of `DecDraft`, `DecSent`, got `Purple`\n" +
	"Decode.goodTag=ok:sent\n" +
	"Decode.listOk=ok:1,2\n" +
	"Decode.listElem=`[1]` should be a whole number, got a string\n" +
	"Decode.notAList=the value should be a list, got a whole number\n" +
	"Decode.optionalAbsent=ok:-:3\n" +
	"Decode.optionalNull=ok:-:3\n" +
	"Decode.optionalJust=ok:hi:3\n" +
	"Decode.optionalRaw=`note` should be a Maybe (leave the field out for Nothing), got a string\n" +
	"Decode.ctorOk=ok:circle:3" + "\n" +
	"Decode.ctorTwoArgs=ok:a:2" + "\n" +
	"Decode.ctorUnknownTag=the value should be one of `DecCircle`, `DecNamed`, got `DecRoxo`" + "\n" +
	"Decode.ctorWrongArity=the value: `DecCircle` takes 1 value, got 0" + "\n" +
	"Decode.ctorWrongArgType=`.DecCircle[0]` should be a whole number, got a string" + "\n" +
	"Decode.ctorNotACtor=the value should be one of `DecCircle`, `DecNamed`, got an empty object" + "\n" +
	"Decode.threadOk=ok:a" + "\n" +
	"Decode.threadDeepWrong=`.DecThread[1].DecThread[0]` should be a string, got a whole number" + "\n" +
	"Decode.isoMillis=2023-11-14T22:13:20.123Z" + "\n" +
	"Decode.isoWhole=2023-11-14T22:13:20.000Z" + "\n" +
	"Decode.isoRoundTrip=1700000000123" + "\n" +
	"Decode.timeTagged=1700000000000" + "\n" +
	"Decode.timeNotATime=the value should be a date and time, got `nope`" + "\n" +
	"Decode.decTagged=1.50" + "\n" +
	"Decode.decNotADec=the value should be a decimal number, got `x`" + "\n" +
	"Decode.dictTagged=2" + "\n" +
	"Decode.dictNotPairs=the value should be a key/value pair of two items" + "\n" +
	"Decode.setTagged=1,2" + "\n" +
	"Decode.setNotAList=the value should be a list of items" + "\n" +
	"Decode.setMixedTypes=the value should be a list of items of one type" + "\n" +
	"Decode.dictMixedKeys=the value should be a list of key/value pairs whose keys are one type" + "\n" +
	"Decode.angleOk=eq90deg" + "\n" +
	"Decode.angleWraps=eq90deg" + "\n" +
	"Decode.angleFraction=the value should be a whole number of deci-degrees, got 1.5" + "\n" +
	"Decode.charOk=ok:a" + "\n" +
	"Decode.charTooMany=the value should be a single character, got 3" + "\n" +
	"Decode.charEmpty=the value should be a single character, got 0" + "\n" +
	"Decode.charAstral=ok:\U0001F600" + "\n" +
	"Decode.loneHigh=120,65533,121" + "\n" +
	"Decode.loneLow=120,65533,121" + "\n" +
	"Decode.pairKept=128512" + "\n" +
	"Decode.loneThenPair=65533,128512" + "\n" +
	"Decode.loneInChar=ok:\uFFFD" + "\n" +
	"Decode.escapedBackslash=92,117,100,56,48,48" + "\n" +
	"Decode.durationOk=1970-01-01T00:00:01.500Z" + "\n" +
	"Decode.durationNeg=1969-12-31T23:59:59.999Z" + "\n" +
	"Decode.durationFraction=the value should be a whole number of milliseconds, got 1.5" + "\n" +
	"Decode.durationExponent=the value should be a whole number of milliseconds, got 1e3" + "\n" +
	"Decode.durationTooLong=Duration out of range: a duration of 9007199254740992 milliseconds is outside the range of a Duration (-9007199254740991 to 9007199254740991 milliseconds)" + "\n" +
	"Decode.durationNotANumber=the value should be a whole number of milliseconds" + "\n" +
	"Decode.durationLeadingZero=the document is not valid JSON" + "\n" +
	"Decode.durationTrailingDot=the document is not valid JSON" + "\n" +
	"Decode.durationNotADuration=the value should be a duration (whole milliseconds under '__duration'), got a whole number" + "\n" +
	"Decode.durationIsATime=the value should be a duration (whole milliseconds under '__duration'), got a time" + "\n" +
	"Decode.loneKey={'a':1,'\uFFFD':2}" + "\n" +
	"Decode.unpinnedDuration=the value should be a whole number of milliseconds, got 1.5" + "\n" +
	"Decode.unpinnedAngle=the value should be a whole number of deci-degrees, got 1.5" + "\n" +
	"Decode.unpinnedChar=the value should be a single character, got 3" + "\n" +
	"Decode.unpinnedNotJSON=the document is not valid JSON" + "\n" +
	"Decode.tupleOk=ok:7:abc" + "\n" +
	"Decode.tupleShort=the value should be a list of 2 values, got 1" + "\n" +
	"Decode.tupleWrongType=`[0]` should be a whole number, got a string" + "\n" +
	"Decode.tupleNotAList=the value should be a list of 2 values, got an object with a" + "\n"
