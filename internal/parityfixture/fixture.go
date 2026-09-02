// Package parityfixture holds the one Mar program that all three runtimes are
// asked to evaluate, and the list of values whose answers must agree.
//
// It exists because the comparison used to live twice: once in the browser
// test and once in the phone test, each with its own copy of the source. Two
// copies is the shape of bug this repository has hit more than any other — a
// check live on one path and absent on the other — and here it was not even
// hypothetical: a value added to one corpus was simply not compared on the
// other runtime, silently, forever.
//
// Every value below stands in for a PROMISE some runtime's comments make in
// prose. The comment beside the JS implementation says "mirrors the Go
// runtime" or "keep these identical"; a comment is a promise, not a guarantee,
// and this is where each one becomes a guarantee. When you add a builtin whose
// answer has to match across runtimes, add a value here and nowhere else.
package parityfixture

// Source is the program. Every declaration is a String so the three runtimes
// can be compared by their printed answers without a shared serializer.
const Source = `module Main exposing (..)


parse : String -> Result String { id : Int, name : String }
parse s =
    JSON.decode s


say : String -> String
say s =
    case parse s of
        Ok r ->
            "ok " ++ String.fromInt r.id ++ " " ++ r.name

        Err e ->
            "err " ++ e


good : String
good =
    say "{\"id\":1,\"name\":\"a\"}"


wrongType : String
wrongType =
    say "{\"id\":\"1\",\"name\":\"a\"}"


missingField : String
missingField =
    say "{\"id\":1}"


notAnObject : String
notAnObject =
    say "[1,2]"


-- A document that is not JSON. This one carried Foundation's message on the
-- phone until recently, and Foundation's message changes with the language the
-- phone is set to.
notJSONAtAll : String
notJSONAtAll =
    say "<html>"


note : Path { slug : String }
note =
    "/notes/{slug:String}"


-- The path encoder: three runtimes once had three answers for this.
link : String
link =
    linkTo note { slug = "a b/c" }


-- Decimal is exact on both sides or money is wrong on one of them.
money : String
money =
    Decimal.toString (1.50 + 2.25)


-- "equality is numeric, scale is display metadata" says the comment beside the
-- JS Decimal. Two runtimes could agree on the sum and still disagree here.
decimalScale : String
decimalScale =
    String.join "|"
        [ Decimal.toString 1.50
        , Decimal.toString 1.5
        , if 1.50 == 1.5 then
            "eq"

          else
            "ne"
        ]


-- Int is 53 bits on every runtime, not 64 on one of them.
bigInt : String
bigInt =
    String.fromInt (9007199254740991 - 1)


sorted : String
sorted =
    String.join "," (List.sort [ "b", "a", "C" ])


-- "Pure splice, mirrors the Go impl: defensive no-op on from == to or
-- out-of-bounds indices." Three of these four are the defensive cases, which
-- is exactly where two implementations drift without anyone noticing.
listMove : String
listMove =
    String.join "|"
        [ String.join "" (List.move 0 2 [ "a", "b", "c" ])
        , String.join "" (List.move 1 1 [ "a", "b", "c" ])
        , String.join "" (List.move 5 0 [ "a", "b", "c" ])
        , String.join "" (List.move (-1) 0 [ "a", "b", "c" ])
        ]


-- "sanitizeCodePoint mirrors the Go side: out-of-range or surrogate inputs
-- collapse to U+FFFD." A runtime that skipped the surrogate half would hand
-- back a lone surrogate and only fail once someone rendered it.
charEdges : String
charEdges =
    String.join "|"
        (List.map (\n -> String.fromInt (Char.toCode (Char.fromCode n)))
            [ -1, 55296, 57343, 1114112, 65 ]
        )


-- "Each constructor multiplies by its unit's seconds-count", except millis,
-- which divides. A unit table is the kind of thing that gets one entry wrong.
durations : String
durations =
    String.join "|"
        (List.map String.fromInt
            [ Time.toSeconds (Time.weeks 2)
            , Time.toSeconds (Time.hours 3)
            , Time.toSeconds (Time.minutes 90)
            , Time.toSeconds (Time.seconds 7)
            ]
        )


-- Set and Dict are ORDERED in Mar, and the order is part of the answer: it is
-- what a user sees when a page lists them.
collections : String
collections =
    String.join "|"
        [ String.join "," (Set.toList (Set.fromList [ "b", "a", "b", "C" ]))
        , String.join "," (Dict.keys (Dict.fromList [ ( "z", 1 ), ( "a", 2 ) ]))
        , String.join ","
            (List.map String.fromInt
                (Dict.values (Dict.fromList [ ( "z", 1 ), ( "a", 2 ) ]))
            )
        ]


-- The largest Int a Decimal can become, from both directions. The bound is
-- 53 bits, and two runtimes used to use int64's instead: the same money
-- computation returned a number on the server and the phone and was refused in
-- the browser. Inside the bound all three must agree exactly, which is what
-- this checks; the refusal outside it is pinned in internal/runtime.
decimalEdge : String
decimalEdge =
    String.join "|"
        [ String.fromInt (Decimal.truncate 9007199254740991.0)
        , String.fromInt (Decimal.toCents 90071992547409.91)
        , String.fromInt (Decimal.round 9007199254740990.6)
        ]


-- PCG-XSH-RR, written out three times: 64-bit state in a BigInt in the
-- browser, a UInt64 on the phone, a uint64 on the server. A generator that
-- drifts does not fail — it deals a different hand, and the two players in the
-- same game see different cards. From one seed the three sequences are either
-- the same list or the bug.
seeded : String
seeded =
    let
        ( ns, _ ) =
            Random.step (Random.list 8 (Random.int 1 1000)) (Random.initialSeed 20260902)
    in
    String.join "," (List.map String.fromInt ns)


-- A Path segment typed as a union: the segment the URL carries has to name the
-- same constructor on all three, or a link opens a different page.
numbered : Path { slug : String, n : Int }
numbered =
    "/notes/{slug:String}/{n:Int}"


enumPath : String
enumPath =
    linkTo numbered { slug = "x y", n = 42 }


-- The copy a user reads when a call fails. The code beside each of these says
-- to keep them identical across the three runtimes, and nothing checked it,
-- because comparing the phone used to cost a simulator.
serviceErrors : String
serviceErrors =
    String.join "|"
        [ Service.errorToString Service.Offline
        , Service.errorToString Service.Unauthorized
        , Service.errorToString Service.RateLimited
        , Service.errorToString (Service.ServerError "boom")
        ]
`

// Names are the values compared, in the order a failure should report them.
var Names = []string{
	"good",
	"wrongType",
	"missingField",
	"notAnObject",
	"notJSONAtAll",
	"link",
	"money",
	"decimalScale",
	"bigInt",
	"sorted",
	"listMove",
	"charEdges",
	"durations",
	"collections",
	"decimalEdge",
	"seeded",
	"enumPath",
	"serviceErrors",
}

// HTTPErrorCase is one (status, body) a service can fail with, and the single
// sentence both clients must turn it into.
//
// This one cannot be a value in Source: a failed call is an effect, not an
// expression, and the server is the thing that FAILS rather than a third
// answer to compare against. So the agreement is pinned here instead, and each
// client's test asserts its own runtime against this table — which is still
// one table rather than two copies, and that is the property that matters.
//
// `{"error":""}` is in here because it was the divergence: the browser said
// `HTTP 500` and the phone handed the user the literal `{"error":""}`, which
// is precisely the raw JSON that decodeServerError exists to keep off screens.
type HTTPErrorCase struct {
	Status int
	Body   string
	Says   string
	Why    string
}

var HTTPErrorCases = []HTTPErrorCase{
	{500, `{"error":"note_not_found"}`, "note_not_found",
		"the envelope the framework's own handlers send"},
	{500, `{"error":""}`, "HTTP 500",
		"an empty code is no code; the status is more use than the raw envelope"},
	{500, `{"error":123}`, `{"error":123}`,
		"an error field that is not a string is not a code"},
	{500, `{"errors":"x"}`, `{"errors":"x"}`,
		"a near-miss field name is not the field"},
	{500, `[1,2]`, `[1,2]`,
		"JSON that is not an object has no envelope to read"},
	{502, `Bad Gateway`, "Bad Gateway",
		"a bare body from something that is not the framework"},
	{503, ``, "HTTP 503",
		"an empty body still names the status rather than nothing"},
	{500, `{"error":"  "}`, "  ",
		"blank is not empty: the server said something, even if it is spaces"},
	{500, `null`, `null`,
		"a JSON fragment is not an object either"},

	// A body that ARRIVES fine and is not JSON. The status is 200, so these
	// go down the decode path rather than the error path -- the other half of
	// the same port, and where the phone was answering in whatever language
	// it had been set to: Foundation's localizedDescription, "The data
	// couldn't be read because it isn't in the correct format."
	{200, `<html>`, "decode failed: the document is not valid JSON",
		"a page where a document was expected"},
	{200, ``, "decode failed: the document is not valid JSON",
		"a 200 with nothing in it"},
	{200, `{"a":1`, "decode failed: the document is not valid JSON",
		"JSON that stops halfway"},
}
