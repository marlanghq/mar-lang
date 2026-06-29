package runtime

import "testing"

func TestEvalDictBasic(t *testing.T) {
	if got := runValue(t, `Dict.size (Dict.fromList [("a", 1), ("b", 2), ("a", 3)])`); got != "2" {
		t.Fatalf("size after dedup: %s", got)
	}
	src := `case Dict.get "b" (Dict.fromList [("a", 1), ("b", 2)]) of
    Just n -> n
    Nothing -> 0`
	if got := runValue(t, src); got != "2" {
		t.Fatalf("get: %s", got)
	}
}

func TestEvalDictInsertReplace(t *testing.T) {
	// Replacing an existing key changes the value, not the key order.
	src := `Dict.toList (Dict.insert "a" 99 (Dict.fromList [("a", 1), ("b", 2)]))`
	if got := runValue(t, src); got != `[("a", 99), ("b", 2)]` {
		t.Fatalf("insert-replace: %s", got)
	}
}

func TestEvalDictKeysAreSorted(t *testing.T) {
	// fromList must preserve sort-by-key invariant even when the input
	// list is out of order. Reading keys back gives ascending order.
	src := `Dict.keys (Dict.fromList [(3, "c"), (1, "a"), (2, "b")])`
	if got := runValue(t, src); got != "[1, 2, 3]" {
		t.Fatalf("sorted keys: %s", got)
	}
}

func TestEvalDictUnionLeftBiased(t *testing.T) {
	// Elm: union prefers values from the LEFT dict on collision.
	src := `Dict.get "a" (Dict.union (Dict.fromList [("a", 1)]) (Dict.fromList [("a", 99), ("b", 2)]))`
	if got := runValue(t, src); got != `Just 1` {
		t.Fatalf("union left-biased: %s", got)
	}
}

func TestEvalDictUpdate(t *testing.T) {
	src := `Dict.toList (Dict.update "a" (\m -> case m of
    Just n -> Just (n + 10)
    Nothing -> Just 0) (Dict.fromList [("a", 1), ("b", 2)]))`
	if got := runValue(t, src); got != `[("a", 11), ("b", 2)]` {
		t.Fatalf("update existing: %s", got)
	}
	src2 := `Dict.toList (Dict.update "c" (\m -> case m of
    Just n -> Just n
    Nothing -> Just 7) (Dict.fromList [("a", 1)]))`
	if got := runValue(t, src2); got != `[("a", 1), ("c", 7)]` {
		t.Fatalf("update insert-new: %s", got)
	}
	src3 := `Dict.toList (Dict.update "a" (\m -> Nothing) (Dict.fromList [("a", 1), ("b", 2)]))`
	if got := runValue(t, src3); got != `[("b", 2)]` {
		t.Fatalf("update remove: %s", got)
	}
}

func TestEvalSetDedupAndSort(t *testing.T) {
	src := `Set.toList (Set.fromList [3, 1, 2, 1, 3])`
	if got := runValue(t, src); got != "[1, 2, 3]" {
		t.Fatalf("set dedup: %s", got)
	}
}

func TestEvalSetUnionIntersectDiff(t *testing.T) {
	if got := runValue(t, `Set.toList (Set.union (Set.fromList [1,2,3]) (Set.fromList [3,4,5]))`); got != "[1, 2, 3, 4, 5]" {
		t.Fatalf("union: %s", got)
	}
	if got := runValue(t, `Set.toList (Set.intersect (Set.fromList [1,2,3]) (Set.fromList [2,3,4]))`); got != "[2, 3]" {
		t.Fatalf("intersect: %s", got)
	}
	if got := runValue(t, `Set.toList (Set.diff (Set.fromList [1,2,3,4]) (Set.fromList [2,4]))`); got != "[1, 3]" {
		t.Fatalf("diff: %s", got)
	}
}

func TestEvalDictJSONRoundtrip(t *testing.T) {
	// JSON.encode / JSON.decode preserves Dict structure through the
	// `__dict` wire marker. The decoded value is untyped (`any`), so
	// we check the encoded form rather than try to coax the typechecker
	// into accepting a heterogeneous round-trip.
	src := `JSON.encode (Dict.fromList [("a", 1), ("b", 2)])`
	if got := runValue(t, src); got != `"{\"__dict\":[[\"a\",1],[\"b\",2]]}"` {
		t.Fatalf("encode: %s", got)
	}
}

func TestEvalSetJSONRoundtrip(t *testing.T) {
	src := `JSON.encode (Set.fromList [3, 1, 2])`
	if got := runValue(t, src); got != `"{\"__set\":[1,2,3]}"` {
		t.Fatalf("encode: %s", got)
	}
}

func TestEvalListSortWithOrder(t *testing.T) {
	// Descending sort via List.sortWith — comparator returns
	// LT / EQ / GT, NOT -1/0/1. The whole point is that "less"
	// reads as `LT`, not as a magic Int constant.
	src := `List.sortWith (\a b -> if a > b then LT else if a < b then GT else EQ) [3, 1, 4, 1, 5, 9, 2, 6]`
	if got := runValue(t, src); got != "[9, 6, 5, 4, 3, 2, 1, 1]" {
		t.Fatalf("desc sort: %s", got)
	}
}
