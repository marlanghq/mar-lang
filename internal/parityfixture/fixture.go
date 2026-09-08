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


-- Three copies of one pluralisation rule -- wire.go, runtime.js and
-- MarWire.swift each have their own plural(n, noun) -- and it writes the
-- wire errors a person reads. Nothing compared them: no case in this fixture
-- produced a plural until this one.
--
-- Found structurally rather than by vocabulary: a scan for function names
-- implemented in two or more runtimes turns up 69 pairs, and this was one with
-- no comment claiming they should agree. An unclaimed pair is worse than a
-- claimed one, because nobody even said it mattered.
pluralWireError : String
pluralWireError =
    case pairOf "[1,\"a\",3]" of
        Ok _ ->
            "ok"

        Err e ->
            e


pairOf : String -> Result String ( Int, String )
pairOf s =
    JSON.decode s


asDec : String -> Result String { v : Decimal }
asDec s =
    JSON.decode s


sayDec : String -> String
sayDec s =
    case asDec s of
        Ok r ->
            "ok " ++ Decimal.toString r.v

        Err e ->
            "err " ++ e


numNotation : String
numNotation =
    String.join "|"
        [ sayDec "{\"v\":1e30}"
        , sayDec "{\"v\":1000000000000000000000000000000}"
        , sayDec "{\"v\":19.99}"
        , sayDec "{\"v\":1.5e-3}"
        , sayDec "{\"v\":12345678901234567890}"
        ]


asWholeNum : String -> Result String { v : Int }
asWholeNum s =
    JSON.decode s


sayWhole : String -> String
sayWhole s =
    case asWholeNum s of
        Ok r ->
            "ok " ++ String.fromInt r.v

        Err e ->
            "err " ++ e


numRefusals : String
numRefusals =
    String.join "|"
        [ sayDec "{\"v\":1e400}"
        , sayWhole "{\"v\":1e30}"
        , sayWhole "{\"v\":42}"
        , sayWhole "{\"v\":9007199254740992}"
        ]


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


-- A Path segment typed as a USER'S OWN enum. This is what
-- runtime.RegisterEnumType exists for: a union whose constructors are all
-- nullary is path-eligible, and a URL segment names one of them. The earlier
-- enumPath value did not have an enum in it at all, it had a String and an
-- Int, so the promise it was recorded as covering was not covered.
--
-- (No backticks in this file: Source is a Go raw string, and one ends it.)
type Role
    = Admin
    | Member


member : Path { who : Role }
member =
    "/team/{who:Role}"


rolePath : String
rolePath =
    String.join "|"
        [ linkTo member { who = Admin }
        , linkTo member { who = Member }
        ]


-- Integer division. Both clients carry the same comment beside it -- "//,
-- truncating Int division, total: /0 yields 0 on every runtime" -- and nothing
-- compared it. Total means it answers for every pair, division by zero
-- included, so there is no error to notice a difference by: the wrong answer
-- is just a number.
--
-- Truncation toward zero is the half that bites: -7 // 2 is -3 and not -4, and
-- a language that floors instead would agree on the first case and differ on
-- the second.
intDivision : String
intDivision =
    String.join "|"
        (List.map String.fromInt
            [ 7 // 2
            , (-7) // 2
            , 7 // (-2)
            , 7 // 0
            , 0 // 0
            , (-7) // 0
            ]
        )


-- More of the pairs the structural scan turned up. Every line below was
-- verified by BREAKING the function it claims to reach and watching this value
-- fail -- because two of the claims I first wrote here were wrong, and reading
-- the code had not told me so:
--
--   padLeft / padRight   padString: invert its left/right flag and 00042
--                        becomes 42000
--   isqrt                isqrtInt: return the input and 4 becomes 17
--   toScale              decToScale: return the input unrounded and 1.00|-1.2
--                        becomes 1.005|-1.25
--   comparison           decCompare: return 1 always and lt becomes ge, and
--                        decimalScale's eq becomes ne
--   modBy, remainderBy   are their OWN implementations on each runtime, NOT
--                        posMod: the phone writes the sign correction by hand
--                        in the builtin. posMod is reached through Angle, and
--                        this value does not exercise it
--
-- The rounding modes here are honest but not load-bearing: decimalEdge already
-- fails when HalfEven breaks, and this one does not.
pairedHelpers : String
pairedHelpers =
    String.join "|"
        [ String.padLeft 5 '0' "42"
        , String.padRight 4 '.' "ab"
        , String.fromInt (Math.isqrt 17)
        , Decimal.toString (Decimal.toScale Decimal.HalfEven 2 1.005)
        , Decimal.toString (Decimal.toScale Decimal.Down 1 (-1.25))
        , if 1.10 < 1.2 then
            "lt"

          else
            "ge"
        , String.fromInt (modBy 3 (-7))
        , String.fromInt (remainderBy 3 (-7))
        ]


-- posMod, the one pair the previous value was claimed to cover and did not.
--
-- An Angle is opaque: it is built from an Int and leaves only through sin and
-- cos, which are periodic and therefore hide exactly the wrapping posMod does.
-- JSON.encode is the way out -- an Angle rides the wire as its raw deci value,
-- so the normalisation becomes visible.
--
-- The negative cases are the whole point: plain % gives -900 where posMod gives
-- 2700, and a runtime that reached for the obvious operator would agree on
-- every positive angle and differ on every negative one.
angles : String
angles =
    String.join "|"
        [ JSON.encode (Math.degrees 45)
        , JSON.encode (Math.degrees (-90))
        , JSON.encode (Math.opposite (Math.degrees 30))
        , JSON.encode (Math.add (Math.degrees 350) (Math.degrees 30))
        , JSON.encode (Math.subtract (Math.degrees 10) (Math.degrees 30))
        , JSON.encode (Math.turns (-1))
        , String.fromInt (Math.sin (Math.degrees 30))
        , String.fromInt (Math.cos (Math.degrees 60))
        ]


-- Device.touchOnly / canHover, which the Go runtime's comment says mirror the
-- JS logic. Recorded as uncomparable because the record comes from the host's
-- matchMedia -- which was wrong: only the SOURCE is host-dependent, and the
-- LOGIC is a pure function of a closed record anyone can write. A phone, a
-- desktop, a tablet with a stylus, and a hybrid that reports both.
-- A let binding cannot take a parameter in Mar, so this is a top-level one.
askDevice : Device -> String
askDevice d =
    (if Device.touchOnly d then
        "t"

     else
        "-"
    )
        ++ (if Device.canHover d then
                "h"

            else
                "-"
           )


devices : String
devices =
    String.join "|"
        [ askDevice { pointer = Coarse, anyFine = False, supportsHover = False, width = 390, height = 844, prefersDark = False, prefersReducedMotion = False }
        , askDevice { pointer = Fine, anyFine = True, supportsHover = True, width = 1440, height = 900, prefersDark = False, prefersReducedMotion = False }
        , askDevice { pointer = Coarse, anyFine = True, supportsHover = False, width = 1024, height = 1366, prefersDark = False, prefersReducedMotion = False }
        , askDevice { pointer = Coarse, anyFine = False, supportsHover = True, width = 800, height = 1280, prefersDark = False, prefersReducedMotion = False }
        ]


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
	"numNotation",
	"numRefusals",
	"pluralWireError",
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
	"devices",
	"intDivision",
	"pairedHelpers",
	"angles",
	"seeded",
	"enumPath",
	"rolePath",
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

// AuthPortCases is the body the sign-in endpoint answered with and the one
// sentence both clients must turn it into.
//
// The auth port was the wire port that asserted NOTHING. Whatever
// /_auth/verify-code answered went straight into `Auth.SignedIn` and reached
// user code typed as the app's own user record — so `{"id":"1"}` handed a
// String to a program the checker had promised an Int to, and neither runtime
// said a word. What each did instead was worse than silence:
//
//	browser  a missing field threw while reading it, the promise's outer
//	         catch answered Offline, and the user was told their connection
//	         was down about a server that had answered fine
//	phone    a body that would not parse became `()`, so `SignedIn ()` was
//	         delivered, and Auth.me reported a signed-in person as signed OUT
//
// The checker knows the user type at the call site — it is what pins the type
// variable in `Auth.VerifyOutcome user` — so the shape is stamped on the node
// exactly like Service.call's response shape, and both clients check it.
type AuthPortCase struct {
	// Status is 200 unless the case is about the endpoint REFUSING, which is
	// the other half of this port: a domain code the endpoint is known to emit
	// becomes a typed outcome in the Ok, and anything else is transport and
	// becomes a Service.Error in the Err. Both clients carry that mapping,
	// both say they mirror the other, and nothing compared them.
	Status int
	Body   string
	Says   string // "" means the body is accepted as a user
	// Outcome is the ctor the program should receive for a case the server
	// refused that is still an Ok in Mar's terms.
	Outcome string
	Why     string
}

var AuthPortCases = []AuthPortCase{
	{200, `{"id":1,"email":"a@b.c"}`, "", "",
		"the user the endpoint is supposed to answer with"},
	{200, `{"nope":1}`, "the response does not match what this app expects: the value is missing the field `id`", "",
		"a record with none of the fields the program was promised"},
	{200, `{"id":"1","email":"a@b.c"}`, "the response does not match what this app expects: `id` should be a whole number, got a string", "",
		"the right fields with the wrong type: a String where the checker promised an Int"},
	{200, `[1,2]`, "the response does not match what this app expects: the value should be an object, got a list", "",
		"JSON that is not an object at all"},
	{200, `null`, "the response does not match what this app expects: the value should be an object, got nothing", "",
		"a null where a user belongs"},
	{200, `<html>`, "decode failed: the document is not valid JSON", "",
		"a sign-in page from a proxy instead of a user"},

	// The refusal half. A code the endpoint is known to emit is not an error
	// the app shows -- it is a screen the app has a case for -- and a code it
	// does not know is transport.
	{400, `{"error":"invalid_code"}`, "", "WrongCode",
		"the code the person typed was wrong: a screen, not an error"},
	{400, `{"error":"too_many_attempts"}`, "", "TooManyAttempts",
		"too many tries: also a screen"},
	{500, `{"error":"something_else"}`, "something_else", "",
		"a code the endpoint is not known to emit is transport, and reads as one"},
	{500, `not json at all`, "not json at all", "",
		"a refusal with no envelope carries the body itself"},
}

// AuthExpiryCase is what a 401 from a Service.call decides, given three facts.
//
// This was the last promise in the census with no comparator, and its reason
// was "both hang off stateful objects the offline harnesses cannot build".
// True of the objects; not true of the decision, which is a function of three
// strings and a flag. Reading the two implementations side by side said they
// agreed -- and four readings the same day turned out to be wrong, so the
// decision was extracted on both sides and is compared here.
//
// What stays on the platform, and should: WHERE the remembered path is kept
// and how it is filtered. The browser puts it in ?next=, which comes back
// through a URL a person can type, so it runs isSafeReturnPath over it; the
// phone keeps it in memory, reached only from its own nav stack. That is
// chrome. The lifecycle is these three answers.
type AuthExpiryCase struct {
	SignInPath         string
	AlreadyRedirecting bool
	CurrentPath        string
	Handled            bool
	Navigate           bool
	Capture            string // "" means nothing captured
	Why                string
}

var AuthExpiryCases = []AuthExpiryCase{
	{"", false, "/tasks", false, false, "",
		"no sign-in page configured: the Err reaches the app, which at least sees something"},
	{"/sign-in", false, "/tasks/edit/42", true, true, "/tasks/edit/42",
		"a session that expired deep in the app: take over, and remember where"},
	{"/sign-in", true, "/tasks", true, false, "",
		"the second 401 of a burst: taken over already, and it must not navigate twice"},
	{"/sign-in", false, "/sign-in", true, true, "",
		"already on the sign-in page: nothing to come back to, and ?next=/sign-in is a loop"},
	{"/sign-in", false, "", true, true, "",
		"no current path yet, the window between cold start and the first route"},
}
