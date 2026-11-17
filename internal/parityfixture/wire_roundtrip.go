package parityfixture

import (
	"fmt"
	"strings"
)

// The wire round trip.
//
// Every value that can cross the wire is ENCODED by one runtime and DECODED by
// another. Each side round-trips its own bytes perfectly whether or not the
// other side reads them, so a test of one side against itself cannot see where
// the two disagree: the thing under test is the PAIR.
//
// So the fixture is one table of typed values, and each value is sent along
// every direction a Mar program can send one:
//
//	client -> server   Service request: a field of a POST body, a field of a
//	                   GET or DELETE `?q=`, and the whole request value as a
//	                   POST body (a GET or DELETE request is always a record)
//	server -> client   Service response, decoded with the shape the call
//	                   site's Service.call carries
//	in-runtime         JSON.encode bytes (must agree across runtimes), and
//	                   JSON.decode of a runtime's own JSON.encode
//
// and arrives as the value it left as. "The value" is judged by WireDump, a
// typed rendering each test implements over its runtime's own value
// representation, never by a runtime's encoder or its `==`: the encoders are
// the thing under test, and `==` is numeric on Decimal, so it cannot see a
// scale lost on the way.

// WireCase is one value. Type and Expr are Mar source; Name becomes part of
// every identifier generated for the case, so it is CamelCase.
type WireCase struct {
	Name string
	Type string
	Expr string
}

// WireCases is the table. One row per KIND, plus the edges each kind has:
// bounds, scale, unicode, escaping, field order, nesting, and values whose
// JSON a transparent encoding would spell like another's.
var WireCases = []WireCase{
	{Name: "IntZero", Type: "Int", Expr: "0"},
	{Name: "IntMax", Type: "Int", Expr: "9007199254740991"},
	{Name: "IntMin", Type: "Int", Expr: "-9007199254740991"},

	{Name: "DecScale", Type: "Decimal", Expr: "1.50"},
	{Name: "DecWhole", Type: "Decimal", Expr: "3"},
	{Name: "DecNeg", Type: "Decimal", Expr: "-0.05"},
	{Name: "Dec34Digits", Type: "Decimal", Expr: "1234567890123456789012345678.901234"},
	{Name: "Dec34Places", Type: "Decimal", Expr: "0.1234567890123456789012345678901234"},

	{Name: "StrEmpty", Type: "String", Expr: `""`},
	{Name: "StrUnicode", Type: "String", Expr: `"héllo 日本 🎉 🇧🇷"`},
	// JSON.encode writes a string as JavaScript's JSON.stringify does: the
	// escapes by name, the other controls as \u00xx, everything else as
	// itself. These rows hold every class of character that rule treats
	// apart.
	{Name: "StrEscapes", Type: "String", Expr: `"q\" b\\ n\n t\t r\r"`},
	{Name: "StrHTML", Type: "String", Expr: `"</script> &amp; <b>"`},
	{Name: "StrControls", Type: "String", Expr: "String.fromList (List.map Char.fromCode [ 0, 1, 8, 12, 27, 31, 127 ])"},
	{Name: "StrSeparators", Type: "String", Expr: "String.fromList (List.map Char.fromCode [ 8232, 8233, 47, 133, 160, 65279, 65535 ])"},
	{Name: "StrQuery", Type: "String", Expr: `"a+b c&d=e%f?g#h/i"`},

	{Name: "BoolTrue", Type: "Bool", Expr: "True"},
	{Name: "BoolFalse", Type: "Bool", Expr: "False"},

	{Name: "CharAscii", Type: "Char", Expr: "'a'"},
	{Name: "CharAstral", Type: "Char", Expr: "'🎉'"},
	{Name: "CharNewline", Type: "Char", Expr: `'\n'`},

	{Name: "TimeMillis", Type: "Time", Expr: "Time.add (Time.fromYMD 2023 11 14) (Time.millis 123)"},
	{Name: "TimeBeforeEpoch", Type: "Time", Expr: "Time.sub (Time.fromYMD 1970 1 1) (Time.millis 1)"},

	{Name: "DateToday", Type: "Date", Expr: "Date.fromYMD 2026 11 17"},
	{Name: "DateYearZero", Type: "Date", Expr: "Date.fromYMD 0 1 1"},
	{Name: "DateLast", Type: "Date", Expr: "Date.fromYMD 9999 12 31"},
	{Name: "DateInRecord", Type: "{ due : Date, title : String }", Expr: `{ due = Date.fromYMD 2026 2 28, title = "rent" }`},
	{Name: "DateMaybe", Type: "Maybe Date", Expr: "Just (Date.fromYMD 1969 12 31)"},
	{Name: "DateList", Type: "List Date", Expr: "[ Date.fromYMD 2024 2 29, Date.fromYMD 1582 10 15 ]"},

	{Name: "AngleDeg", Type: "Angle", Expr: "Math.degrees 45"},
	{Name: "AngleDeci", Type: "Angle", Expr: "Math.deciDegrees 3599"},

	{Name: "DurationSub", Type: "Duration", Expr: "Time.millis 1500"},
	{Name: "DurationWhole", Type: "Duration", Expr: "Time.seconds 90"},
	{Name: "DurationTenth", Type: "Duration", Expr: "Time.millis 100"},
	{Name: "DurationOdd", Type: "Duration", Expr: "Time.millis 1001"},
	{Name: "DurationNeg", Type: "Duration", Expr: "Time.millis (-2500)"},
	{Name: "DurationMax", Type: "Duration", Expr: "Time.millis 9007199254740991"},

	{Name: "Unit", Type: "()", Expr: "()"},

	{Name: "ListEmpty", Type: "List Int", Expr: "[]"},
	{Name: "ListInts", Type: "List Int", Expr: "[ 3, 1, 2 ]"},
	{Name: "ListRecords", Type: "List { n : Int }", Expr: "[ { n = 1 }, { n = 2 } ]"},

	{Name: "Tuple2", Type: "( Int, String )", Expr: `( 1, "a" )`},
	{Name: "Tuple3", Type: "( Int, String, Bool )", Expr: `( 1, "a", True )`},

	{Name: "RecordOrder", Type: "{ zeta : Int, alpha : String }", Expr: `{ zeta = 1, alpha = "a" }`},
	{Name: "RecordNested", Type: "{ inner : { b : Int, a : List String } }", Expr: `{ inner = { b = 1, a = [ "x" ] } }`},
	{Name: "RecordMaybeNothing", Type: "{ m : Maybe Int }", Expr: "{ m = Nothing }"},

	{Name: "MaybeNothing", Type: "Maybe Int", Expr: "Nothing"},
	{Name: "MaybeJust", Type: "Maybe Int", Expr: "Just 1"},
	{Name: "MaybeJustNothing", Type: "Maybe (Maybe Int)", Expr: "Just Nothing"},
	{Name: "MaybeJustUnit", Type: "Maybe ()", Expr: "Just ()"},

	{Name: "ResultOk", Type: "Result String Int", Expr: "Ok 1"},
	{Name: "ResultErr", Type: "Result String Int", Expr: `Err "bad"`},
	{Name: "ResultOkRecord", Type: "Result String { a : Int }", Expr: "Ok { a = 1 }"},
	// An Ok of a record with an `error` field, and an Err of the same text:
	// a transparent Result encoding would spell the two alike.
	{Name: "ResultOkErrorField", Type: "Result String { error : String }", Expr: `Ok { error = "boom" }`},
	{Name: "ResultErrSameText", Type: "Result String { error : String }", Expr: `Err "boom"`},
	{Name: "ResultOkUnit", Type: "Result String ()", Expr: "Ok ()"},
	// A union inside an instantiation of ITSELF, under other arguments: a
	// type of its own, not a cycle back to the outer one. In the second and
	// third the outer's arm would accept the inner value, so a shape that
	// took the inner one for the outer would coerce it into the outer's type
	// (an Int into a Decimal, a List into a tuple).
	{Name: "ResultInResult", Type: "Result String (Result String Int)", Expr: "Ok (Ok 5)"},
	{Name: "ResultArgsDiffer", Type: "Result Decimal (Result Int String)", Expr: "Ok (Err 3)"},
	{Name: "ResultListNotTuple", Type: "Result ( Int, Int ) (Result (List Int) String)", Expr: "Ok (Err [ 1, 2 ])"},
	{Name: "ResultsInARecord", Type: "Result String { items : List (Result String Int) }", Expr: `Ok { items = [ Ok 1, Err "x" ] }`},
	{Name: "BoxInBox", Type: "Box (Box Int)", Expr: "Box (Box 7)"},

	{Name: "EnumNullary", Type: "Color", Expr: "Green"},
	{Name: "UnionArgs", Type: "Shape", Expr: "Rect 2 3"},
	{Name: "UnionMaybeArg", Type: "Shape", Expr: `Label "x" Nothing`},
	{Name: "UnionParam", Type: "Box Int", Expr: "Box 7"},
	{Name: "UnionRecursive", Type: "Tree", Expr: "Node Leaf 1 (Node Leaf 2 Leaf)"},
	{Name: "UnionCarriesResult", Type: "Outcome", Expr: "Done (Ok 5)"},
	// A tuple one level BELOW the point where the type re-enters itself,
	// which a shape names as a Ref rather than spelling out again.
	{Name: "RecursiveTuple", Type: "Chain", Expr: "Link ( 1, 2 ) (Link ( 3, 4 ) End)"},
	{Name: "TupleInMaybe", Type: "Maybe ( Int, String )", Expr: `Just ( 1, "a" )`},

	{Name: "DictStringKeys", Type: "Dict String Int", Expr: `Dict.fromList [ ( "b", 2 ), ( "a", 1 ) ]`},
	{Name: "DictIntKeys", Type: "Dict Int String", Expr: `Dict.fromList [ ( 10, "x" ), ( 2, "y" ) ]`},
	{Name: "DictTupleKeys", Type: "Dict ( Int, Int ) String", Expr: `Dict.fromList [ ( ( 1, 2 ), "p" ) ]`},
	{Name: "DictEmpty", Type: "Dict String Int", Expr: "Dict.empty"},
	{Name: "DictTupleValues", Type: "Dict String ( Int, String )", Expr: `Dict.fromList [ ( "k", ( 1, "a" ) ) ]`},
	{Name: "SetInts", Type: "Set Int", Expr: "Set.fromList [ 3, 1, 2 ]"},
	{Name: "SetTuples", Type: "Set ( Int, Int )", Expr: "Set.fromList [ ( 2, 1 ), ( 1, 2 ) ]"},
	{Name: "SetStrings", Type: "Set String", Expr: `Set.fromList [ "b", "a" ]`},

	{Name: "NestedResults", Type: "List (Maybe (Result String Int))", Expr: `[ Just (Ok 1), Just (Err "e"), Nothing ]`},
	{Name: "DictOfResults", Type: "Dict String (Result String Int)", Expr: `Dict.fromList [ ( "k", Ok 1 ) ]`},
}

// WireLegs are the directions a request can take. Each is a separate service
// on the server so one direction's failure cannot hide another's.
var WireLegs = []struct {
	Prefix string // identifier prefix, and the path segment
	Verb   string
	Whole  bool // the case's value IS the request, rather than its `v` field
}{
	{"post", "POST", false},
	{"get", "GET", false},
	{"delete", "DELETE", false},
	{"postWhole", "POST", true},
}

// WireTypesSource is the module header and the unions the table's types
// name, for a test that builds its own declarations around WireCases.
func WireTypesSource() string {
	return `module Main exposing (..)


type Color
    = Red
    | Green
    | Blue


type Shape
    = Circle Int
    | Rect Int Int
    | Label String (Maybe Int)


type Box a
    = Box a


type Tree
    = Leaf
    | Node Tree Int Tree


type Outcome
    = Done (Result String Int)
    | Pending


type Chain
    = End
    | Link ( Int, Int ) Chain
`
}

// WireRoundTripSource generates the program. Types for the unions the table
// uses come first; then, per case, the value, its four request contracts, its
// response contract, the client calls, and its JSON.encode / JSON.decode.
func WireRoundTripSource() string {
	var b strings.Builder
	b.WriteString(WireTypesSource())
	decl := func(name, typ, body string) {
		fmt.Fprintf(&b, "\n\n%s : %s\n%s =\n    %s\n", name, typ, name, body)
	}
	for _, c := range WireCases {
		v := "v" + c.Name
		decl(v, c.Type, c.Expr)
		decl("enc"+c.Name, "String", "JSON.encode "+v)
		decl("dec"+c.Name, "Result String ("+c.Type+")", "JSON.decode enc"+c.Name)
		for _, leg := range WireLegs {
			svc := leg.Prefix + c.Name
			req, arg := "{ v : "+c.Type+" }", "{ v = "+v+" }"
			if leg.Whole {
				req, arg = "("+c.Type+")", v
			}
			decl(svc, "Service "+req+" ()",
				fmt.Sprintf(`Service.declare %s "/w/%s/%s"`, leg.Verb, strings.ToLower(leg.Prefix), strings.ToLower(c.Name)))
			decl("call"+upperFirst(svc), "Cmd ()", fmt.Sprintf(`Service.call %s %s (\_ -> ())`, svc, arg))
		}
		decl("resp"+c.Name, "Service () ("+c.Type+")",
			fmt.Sprintf(`Service.declare GET "/w/resp/%s"`, strings.ToLower(c.Name)))
		decl("callResp"+c.Name, "Cmd (Result Service.Error ("+c.Type+"))",
			fmt.Sprintf(`Service.call resp%s () (\r -> r)`, c.Name))
	}
	return b.String()
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
