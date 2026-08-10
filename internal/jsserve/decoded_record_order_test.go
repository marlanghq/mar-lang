package jsserve

import (
	"testing"
)

// A decoded record has no source order to inherit — the JSON text's key order
// is gone by the time each runtime holds a dictionary, and Go's map iteration
// is randomized outright. So the three runtimes have to agree on an order they
// can all produce, and sorted is the only one: iOS already sorted, Go now does,
// and this pins the web.
//
// This order is not observable from a Mar program — nothing in the language
// reads a record's field order, and JSON.encode sorts on the way out in all
// three, which is why there is no case for it in the shared conformance
// corpus. It surfaces in the two places a person READS a value rather than
// computes with it: debug output, and the "record has no field" message. That
// message is why it matters — it lists the fields the record does have, and a
// list that reads differently on each runtime (or, before the Go fix, on each
// run) is a poor answer to "what did I actually get?".
func TestDecodedRecordFieldOrderIsSorted(t *testing.T) {
	// Hand-written JSON with the keys deliberately out of order — the shape
	// that reaches JSON.decode from a file or a service that did not sort.
	src := `module Ord exposing (..)


decoded : Result String { alpha : Int, beta : Int, mid : Int, zeta : Int }
decoded =
    JSON.decode "{\"zeta\":1,\"alpha\":2,\"mid\":3,\"beta\":4}"
`

	// Read the record itself rather than a display string: the field order is
	// the thing under test, so the test has to look at where it is recorded.
	got := runSoundDriverSrc(t, src, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const result = globalThis.__marEvalRaw(program, 'Ord.decoded');
if (result.tag !== 'Ok') throw new Error('decode failed: ' + JSON.stringify(result));
process.stdout.write(result.args[0].order.join(','));
`)

	if want := "alpha,beta,mid,zeta"; got != want {
		t.Fatalf("decoded field order is not sorted — the runtimes disagree.\n got: %s\nwant: %s", got, want)
	}
}
