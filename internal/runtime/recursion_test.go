package runtime

import (
	"strconv"
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/typecheck"
)

// Runaway recursion used to end the process, not the request. Go's stack
// overflow is a fatal error: `recover()` does not catch it, so the recover at
// the request boundary never ran and a server answering other users died
// because one request called a function that never reached a base case.
//
// These evaluate real Mar and require a returned error: the point is that the
// test process is still alive to report it.

func evalTop(t *testing.T, src, name string) (Value, error) {
	t.Helper()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}
	loaded, err := LoadModule(mod)
	if err != nil {
		return nil, err
	}
	return loaded.Get(name)
}

func wantRecursionError(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected a recursion error, got none", what)
	}
	if !strings.Contains(err.Error(), "too much recursion") {
		t.Fatalf("%s: expected the recursion guard, got: %v", what, err)
	}
}

func TestDirectRecursionIsRefusedInsteadOfKillingTheProcess(t *testing.T) {
	_, err := evalTop(t, `module M exposing (boom)


loop : Int -> Int
loop n =
    loop (n + 1)


boom : Int
boom =
    loop 0
`, "boom")
	wantRecursionError(t, err, "self-recursion")
}

func TestMutualRecursionIsRefused(t *testing.T) {
	_, err := evalTop(t, `module M exposing (boom)


ping : Int -> Int
ping n =
    pong (n + 1)


pong : Int -> Int
pong n =
    ping (n + 1)


boom : Int
boom =
    ping 0
`, "boom")
	wantRecursionError(t, err, "mutual recursion")
}

// The case a guard placed only in the evaluator would miss. `List.foldl` is a
// Go loop calling back into user code, so the recursion runs
// apply → Native → apply → ... and every lap would restart a naive counter at
// zero while the Go stack kept growing. The depth rides on the function value
// precisely so this trips.
func TestRecursionLaunderedThroughAHigherOrderBuiltinIsRefused(t *testing.T) {
	_, err := evalTop(t, `module M exposing (boom)


loop : Int -> Int
loop n =
    List.foldl (\_ _ -> loop (n + 1)) 0 [ 1 ]


boom : Int
boom =
    loop 0
`, "boom")
	wantRecursionError(t, err, "recursion through List.foldl")
}

func TestRecursionThroughListMapIsRefused(t *testing.T) {
	_, err := evalTop(t, `module M exposing (boom)


loop : Int -> List Int
loop n =
    List.map (\_ -> n) (loop (n + 1))


boom : List Int
boom =
    loop 0
`, "boom")
	wantRecursionError(t, err, "recursion through List.map")
}

// The guard has to leave honest programs alone. This recurses thousands of
// frames deep and must produce an answer, not an error.
func TestDeepButTerminatingRecursionStillAnswers(t *testing.T) {
	v, err := evalTop(t, `module M exposing (answer)


countDown : Int -> Int
countDown n =
    if n <= 0 then
        0

    else
        1 + countDown (n - 1)


answer : Int
answer =
    countDown 5000
`, "answer")
	if err != nil {
		t.Fatalf("5000 frames of legitimate recursion was refused: %v", err)
	}
	n, ok := v.(VInt)
	if !ok || n.V != 5000 {
		t.Fatalf("countDown 5000 = %v, want 5000", v.Display())
	}
}

// Depth is per call chain, not a package counter: two goroutines evaluating at
// once must not add up to a false positive, and one deep chain must not leave
// the next request starting halfway to the limit.
func TestDepthDoesNotLeakBetweenConcurrentEvaluations(t *testing.T) {
	const src = `module M exposing (answer)


countDown : Int -> Int
countDown n =
    if n <= 0 then
        0

    else
        1 + countDown (n - 1)


answer : Int
answer =
    countDown 400
`
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			mod, err := parser.Parse(src)
			if err != nil {
				done <- err
				return
			}
			if _, err := typecheck.CheckModule(mod); err != nil {
				done <- err
				return
			}
			loaded, err := LoadModule(mod)
			if err != nil {
				done <- err
				return
			}
			_, err = loaded.Get("answer")
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent evaluation %d failed: %v", i, err)
		}
	}
}

// `|>` and `<|` apply their operand, and the binop fast path calls the
// operator builtin directly instead of going through applyAt. If that path
// forgot to carry the depth, recursion written with a pipe would slip past the
// guard and take the process down.
func TestRecursionThroughPipeOperatorsIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"forward pipe", "n |> loop"},
		{"backward pipe", "loop <| n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := evalTop(t, `module M exposing (boom)


loop : Int -> Int
loop n =
    `+tc.body+`


boom : Int
boom =
    loop 0
`, "boom")
			wantRecursionError(t, err, tc.name)
		})
	}
}

// A user's mistake and a compiler bug should not read the same. `no case
// branch matched` is something a checked program CAN still do (a case over
// integer literals with no catch-all), so it stays a plain runtime error;
// everything the checker rules out is labelled as internal.
func TestCheckerRuledOutStatesReportAsInternalErrors(t *testing.T) {
	// Applying a non-function is impossible in checked code, so the guard is
	// reached only by handing the evaluator a value the checker never saw.
	_, err := Apply(VInt{V: 1}, VInt{V: 2})
	if err == nil {
		t.Fatal("applying a non-function was not refused")
	}
	if !strings.Contains(err.Error(), "internal error") {
		t.Errorf("a checker-ruled-out state should say it is a Mar bug, got: %v", err)
	}
	if !strings.Contains(err.Error(), "report it") {
		t.Errorf("the message should tell the reader what to do, got: %v", err)
	}

	// `no case branch matched` is NOT a compiler bug, and must not claim to
	// be one, but it can no longer be produced from checked source at all.
	// Exhaustiveness now covers unions, lists, tuples and the unbounded
	// scalars, so every `case` a program can write is total.
	//
	// What still reaches it is the runtime feeding a case a value its types
	// never sanctioned: an async result from a page the user already
	// navigated away from, delivered into the live page's `update`. Both
	// client runtimes depend on recognising it: see `marNoCaseBranch` in
	// internal/jsserve/runtime.js and MarRuntimeError.noMatch on iOS, which
	// drop the stale message instead of crashing the app.
	//
	// So the subject here is built in Go rather than written in Mar, the same
	// way the non-function above is: that is the only way in, and it is
	// exactly the way the runtime gets there.
	mod, perr := parser.Parse(`module M exposing (describe)


type Msg
    = Tick
    | Reset


describe : Msg -> String
describe msg =
    case msg of
        Tick ->
            "tick"

        Reset ->
            "reset"
`)
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	if _, cerr := typecheck.CheckModule(mod); cerr != nil {
		t.Fatalf("typecheck: %v", cerr)
	}
	loaded, lerr := LoadModule(mod)
	if lerr != nil {
		t.Fatalf("load: %v", lerr)
	}
	describe, gerr := loaded.Get("describe")
	if gerr != nil {
		t.Fatalf("get describe: %v", gerr)
	}
	// A constructor from some other page's Msg type, which is what actually
	// arrives when a torn-down page's effect resolves late.
	_, uerr := Apply(describe, VCtor{Tag: "SavedFromAnotherPage"})
	if uerr == nil {
		t.Fatal("a case with no matching branch should fail")
	}
	if !strings.Contains(uerr.Error(), "no case branch matched") {
		t.Fatalf("the runtimes match on this message to drop stale messages: %v", uerr)
	}
	if strings.Contains(uerr.Error(), "internal error") {
		t.Errorf("a message meant for another page is not a compiler bug: %v", uerr)
	}
}

// The guard has to hold for every SHAPE of recursion, not one.
//
// This table is the test that was missing. The suite used to exercise
// `loop n = loop (n + 1)` — a bare tail call, the cheapest thing the evaluator
// can do — and pass, while every ordinary shape blew the Go stack before the
// guard was reached. Measured then, fresh process per trial: the tail call hit
// the guard at 100k; `1 + deep (n-1)` died between 60k and 80k; a recursive
// call inside a tuple died between 50k and 55k. The one shape that worked was
// the one under test. See ADR 0033.
//
// Each case asserts the guard error AND, implicitly but importantly, that the
// process is still here to report it: a Go stack overflow is a fatal throw, so
// a regression does not fail this test — it takes the whole test binary down.
// A crashed `go test ./internal/runtime/` IS the failure signal for this file.
func TestRecursionGuardHoldsForEveryShape(t *testing.T) {
	shapes := []struct {
		name string
		body string
	}{
		{"tail call", "    if n == 0 then\n        0\n    else\n        deep (n - 1)"},
		{"sum after the call", "    if n == 0 then\n        0\n    else\n        1 + deep (n - 1)"},
		{"case around the call", "    case n of\n        0 -> 0\n        _ -> 1 + deep (n - 1)"},
		{"call inside a list", "    if n == 0 then\n        0\n    else\n        List.length [ deep (n - 1) ]"},
		{"call inside a tuple", "    if n == 0 then\n        0\n    else\n        Tuple.first ( 1 + deep (n - 1), 0 )"},
		{"call through a pipe", "    if n == 0 then\n        0\n    else\n        (n - 1) |> deep"},
		{"call under a let", "    if n == 0 then\n        0\n    else\n        let\n            inner =\n                deep (n - 1)\n        in\n        1 + inner"},
	}
	// Comfortably past the guard, so every shape must trip it rather than
	// finish. Written in terms of the derived depth so the test follows the
	// budget instead of pinning a number that goes stale with it.
	depth := MaxCallDepth * 3
	for _, s := range shapes {
		t.Run(s.name, func(t *testing.T) {
			src := "module M exposing (boom)\n\n\ndeep : Int -> Int\ndeep n =\n" + s.body +
				"\n\n\nboom : Int\nboom =\n    deep " + itoa(depth) + "\n"
			_, err := evalTop(t, src, "boom")
			wantRecursionError(t, err, s.name)
		})
	}
}

// The budget is derived, not chosen, so the arithmetic behind it is what needs
// pinning: a stack ceiling divided by the worst measured frame, with a quarter
// of it spent. If someone raises the depth without raising the measurement, or
// drops the safety factor, this is the line that argues back.
func TestMaxCallDepthStaysInsideItsStackBudget(t *testing.T) {
	if MaxCallDepth*worstCaseFrameBytes*stackSafetyFactor > stackCeilingBytes() {
		t.Fatalf("MaxCallDepth=%d at %d B/frame leaves less than a factor of %d against a %d B stack",
			MaxCallDepth, worstCaseFrameBytes, stackSafetyFactor, stackCeilingBytes())
	}
	if MaxCallDepth < minCallDepth {
		t.Fatalf("MaxCallDepth=%d is below the floor honest recursion needs", MaxCallDepth)
	}
	t.Logf("MaxCallDepth=%d from a %d B stack at %d B/frame with %dx in hand",
		MaxCallDepth, stackCeilingBytes(), worstCaseFrameBytes, stackSafetyFactor)
}

func itoa(n int) string { return strconv.Itoa(n) }
