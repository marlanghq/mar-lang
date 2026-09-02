package runtime

import "testing"

// A path parameter typed `Int` was parsed at 64 bits and admitted whole.
// `inIntRange` is the single predicate for "is this a Mar Int" (ADR 0021),
// and this door skipped it.
//
// Measured, on `GET /api/item/{id:Int}`:
//
//	/api/item/9007199254740991   200, the value intact
//	/api/item/9007199254740993   200, the value intact — on the SERVER
//
// The browser cannot hold that number: it reads 9007199254740992. So the
// same URL, typed into the address bar, meant one thing on the server, a
// different one in the browser, and a third on the phone, whose Int is 64
// bits and parsed it exactly.

func TestAPathIntBeyondFiftyThreeBitsDoesNotMatch(t *testing.T) {
	for _, raw := range []string{
		"9007199254740992", // 2^53, the first one that is not exact
		"9007199254740993",
		"9223372036854775807", // int64's ceiling
		"-9007199254740992",
		"-9223372036854775808",
	} {
		if v, ok := decodePathSegment(raw, "Int"); ok {
			t.Errorf("%s is outside the range of a Mar Int, and matched as %s", raw, v.Display())
		}
	}
}

func TestAPathIntInsideTheRangeStillMatches(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int64
	}{
		{"0", 0},
		{"1", 1},
		{"-1", -1},
		{"9007199254740991", MaxSafeInt},
		{"-9007199254740991", MinSafeInt},
	} {
		v, ok := decodePathSegment(tc.raw, "Int")
		if !ok {
			t.Errorf("%s is a perfectly good Int and did not match", tc.raw)
			continue
		}
		if n, isInt := v.(VInt); !isInt || n.V != tc.want {
			t.Errorf("%s matched as %v, want %d", tc.raw, v, tc.want)
		}
	}
}

func TestTheBoundIsTheSameOneEverythingElseUses(t *testing.T) {
	// Not a second copy of the rule: the same predicate the read path and
	// arithmetic go through, which is the point of having one.
	if inIntRange(MaxSafeInt+1) || inIntRange(MinSafeInt-1) {
		t.Fatal("inIntRange is the single answer to `is this a Mar Int`")
	}
	if _, ok := decodePathSegment("9007199254740992", "Int"); ok != inIntRange(9007199254740992) {
		t.Fatal("the path parser and the predicate disagree")
	}
}
