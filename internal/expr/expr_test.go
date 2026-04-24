package expr

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseEvalLispyExpressions(t *testing.T) {
	ctx := map[string]any{
		"title":        "Todo",
		"email":        "dev@company.com",
		"current_user": TaggedValue{Tag: "authenticated", Values: []any{int64(7), "dev@company.com", "admin"}},
	}

	tests := []struct {
		expression string
		want       any
	}{
		{expression: `(>= (length title) 3)`, want: true},
		{expression: `(contains "@" email)`, want: true},
		{expression: `(starts-with "dev@" email)`, want: true},
		{expression: `(ends-with "@company.com" email)`, want: true},
		{expression: `(matches "^[^@]+@company\\.com$" email)`, want: true},
		{expression: `(authenticated? current-user)`, want: true},
		{expression: `(anonymous? current-user)`, want: false},
		{expression: `(same-user? current-user 7)`, want: true},
		{expression: `(same-user? current-user 8)`, want: false},
		{expression: `(has-role? current-user "admin")`, want: true},
		{expression: `(has-role? current-user "member")`, want: false},
		{expression: `(and (authenticated? current-user) (has-role? current-user "admin"))`, want: true},
		{expression: `(if (authenticated? current-user) "ok" "nope")`, want: "ok"},
		{expression: `(cond ((has-role? current-user "member") "member") (else "admin"))`, want: "admin"},
	}

	opts := ParserOptions{AllowedVariables: map[string]struct{}{
		"title":        {},
		"email":        {},
		"current_user": {},
	}}

	for _, tc := range tests {
		node, err := Parse(tc.expression, opts)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", tc.expression, err)
		}
		got, err := node.Eval(ctx)
		if err != nil {
			t.Fatalf("Eval(%q) returned error: %v", tc.expression, err)
		}
		if got != tc.want {
			t.Fatalf("Eval(%q) = %#v, want %#v", tc.expression, got, tc.want)
		}
	}
}

func TestDecimalArithmeticIsExact(t *testing.T) {
	tests := []struct {
		expression string
		want       string
	}{
		{expression: `(+ 0.1 0.2)`, want: "0.3"},
		{expression: `(/ 1 3)`, want: "1/3"},
		{expression: `(* (/ 1 3) 3)`, want: "1"},
	}

	for _, tc := range tests {
		node, err := Parse(tc.expression, ParserOptions{})
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", tc.expression, err)
		}
		got, err := node.Eval(map[string]any{})
		if err != nil {
			t.Fatalf("Eval(%q) returned error: %v", tc.expression, err)
		}
		if fmt.Sprint(got) != tc.want {
			t.Fatalf("Eval(%q) = %v, want %s", tc.expression, got, tc.want)
		}
	}
}

func TestParseEvalErrorAndUserFunction(t *testing.T) {
	opts := ParserOptions{
		AllowedVariables: map[string]struct{}{
			"owner_id": {},
		},
		AllowedFunctions: map[string]int{
			"require_owner": 1,
		},
	}

	requireOwner, err := Parse(`(if (same-user? current-user owner-id) true (error "owner only"))`, ParserOptions{
		AllowedVariables: map[string]struct{}{"owner_id": {}},
		AllowedFunctions: map[string]int{"require_owner": 1},
	})
	if err != nil {
		t.Fatalf("Parse helper returned error: %v", err)
	}

	node, err := Parse(`(require-owner owner-id)`, opts)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	ctx := map[string]any{
		"current_user": TaggedValue{Tag: "authenticated", Values: []any{int64(7), "owner@example.com", "admin"}},
		"owner_id":     int64(9),
		"__functions":  map[string]UserFunction{"require_owner": {Params: []string{"owner_id"}, Body: requireOwner}},
	}
	_, err = node.Eval(ctx)
	if err == nil {
		t.Fatal("expected Eval to fail")
	}
	if raised, ok := err.(RaisedError); !ok || raised.Message != "owner only" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestParseRejectsInvalidErrorShape(t *testing.T) {
	opts := ParserOptions{AllowedVariables: map[string]struct{}{}}

	if _, err := Parse(`(error)`, opts); err == nil {
		t.Fatal("expected missing-message error")
	}
	if _, err := Parse(`(error title)`, opts); err == nil {
		t.Fatal("expected non-string error payload to fail")
	}
}

func TestParseRejectsDynamicOrInvalidMatchesPattern(t *testing.T) {
	opts := ParserOptions{AllowedVariables: map[string]struct{}{
		"pattern": {},
		"email":   {},
	}}

	if _, err := Parse(`(matches pattern email)`, opts); err == nil {
		t.Fatal("expected dynamic regex pattern to fail")
	} else if !strings.Contains(err.Error(), "matches expects a static regex literal as first argument") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Parse(`(matches "[" email)`, opts); err == nil {
		t.Fatal("expected invalid regex pattern to fail")
	} else if !strings.Contains(err.Error(), "matches regex is invalid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseEvalRecordConstructors(t *testing.T) {
	opts := ParserOptions{
		AllowedVariables: map[string]struct{}{
			"title": {},
		},
		AllowedRecords: map[string][]string{
			"todo_model": {"title", "done"},
		},
	}

	tests := []struct {
		expression string
		want       map[string]any
	}{
		{
			expression: `(todo-model title false)`,
			want:       map[string]any{"title": "Ship it", "done": false},
		},
		{
			expression: `(todo-model (done true) (title title))`,
			want:       map[string]any{"title": "Ship it", "done": true},
		},
	}

	for _, tc := range tests {
		node, err := Parse(tc.expression, opts)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", tc.expression, err)
		}
		got, err := node.Eval(map[string]any{"title": "Ship it"})
		if err != nil {
			t.Fatalf("Eval(%q) returned error: %v", tc.expression, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Eval(%q) = %#v, want %#v", tc.expression, got, tc.want)
		}
	}

	if _, err := Parse(`(todo-model "x")`, opts); err == nil {
		t.Fatal("expected positional constructor arity error")
	}
	if _, err := Parse(`(todo-model (missing true) (title title))`, opts); err == nil {
		t.Fatal("expected named constructor field error")
	}
}

func TestParseEvalLetLambdaAndCollections(t *testing.T) {
	opts := ParserOptions{
		AllowedVariables: map[string]struct{}{
			"numbers": {},
			"model":   {},
		},
		AllowedFunctions: map[string]int{
			"double": 1,
		},
	}

	doubleBody, err := Parse(`(+ value value)`, ParserOptions{
		AllowedVariables: map[string]struct{}{"value": {}},
		AllowedFunctions: map[string]int{"double": 1},
	})
	if err != nil {
		t.Fatalf("Parse(double) returned error: %v", err)
	}

	ctx := map[string]any{
		"numbers": []any{int64(1), int64(2), int64(3)},
		"model":   map[string]any{"count": int64(1), "loading": true},
		"__functions": map[string]UserFunction{
			"double": {Params: []string{"value"}, Body: doubleBody},
		},
	}

	tests := []struct {
		expression string
		want       any
	}{
		{expression: `(let ((a 1) (b 2)) (+ a b))`, want: int64(3)},
		{expression: `(let* ((a 1) (b (+ a 2))) b)`, want: int64(3)},
		{expression: `(begin 1 2 3)`, want: int64(3)},
		{expression: `(map (lambda (n) (+ n 1)) numbers)`, want: []any{int64(2), int64(3), int64(4)}},
		{expression: `(map double numbers)`, want: []any{int64(2), int64(4), int64(6)}},
		{expression: `(filter (lambda (n) (> n 1)) numbers)`, want: []any{int64(2), int64(3)}},
		{expression: `(fold-left (lambda (acc n) (+ acc n)) 0 numbers)`, want: int64(6)},
		{expression: `(fold-right (lambda (n acc) (+ n acc)) 0 numbers)`, want: int64(6)},
		{expression: `(cons 0 numbers)`, want: []any{int64(0), int64(1), int64(2), int64(3)}},
		{expression: `(first numbers)`, want: TaggedValue{Tag: "just", Values: []any{int64(1)}}},
		{expression: `(first ())`, want: TaggedValue{Tag: "nothing"}},
		{expression: `(rest numbers)`, want: []any{int64(2), int64(3)}},
		{expression: `(empty? ())`, want: true},
		{expression: `(get model count)`, want: int64(1)},
	}

	for _, tc := range tests {
		node, err := Parse(tc.expression, opts)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", tc.expression, err)
		}
		got, err := node.Eval(ctx)
		if err != nil {
			t.Fatalf("Eval(%q) returned error: %v", tc.expression, err)
		}
		if !Equal(got, tc.want) {
			t.Fatalf("Eval(%q) = %#v, want %#v", tc.expression, got, tc.want)
		}
	}
}

func TestParseEvalMatch(t *testing.T) {
	opts := ParserOptions{AllowedVariables: map[string]struct{}{"value": {}}}

	node, err := Parse(`(match value ((just x) x) ((nothing) 0))`, opts)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	got, err := node.Eval(map[string]any{"value": TaggedValue{Tag: "just", Values: []any{int64(7)}}})
	if err != nil {
		t.Fatalf("Eval returned error: %v", err)
	}
	if got != int64(7) {
		t.Fatalf("unexpected match result: %#v", got)
	}
}

func TestParseEvalResultTags(t *testing.T) {
	okNode, err := Parse(`(ok 42)`, ParserOptions{AllowedVariables: map[string]struct{}{}})
	if err != nil {
		t.Fatalf("Parse(ok) returned error: %v", err)
	}
	okValue, err := okNode.Eval(map[string]any{})
	if err != nil {
		t.Fatalf("Eval(ok) returned error: %v", err)
	}
	if !Equal(okValue, TaggedValue{Tag: "ok", Values: []any{int64(42)}}) {
		t.Fatalf("unexpected ok result: %#v", okValue)
	}

	errNode, err := Parse(`(err "nope")`, ParserOptions{AllowedVariables: map[string]struct{}{}})
	if err != nil {
		t.Fatalf("Parse(err) returned error: %v", err)
	}
	errValue, err := errNode.Eval(map[string]any{})
	if err != nil {
		t.Fatalf("Eval(err) returned error: %v", err)
	}
	if !Equal(errValue, TaggedValue{Tag: "err", Values: []any{"nope"}}) {
		t.Fatalf("unexpected err result: %#v", errValue)
	}
}

func TestParseEvalListLiteral(t *testing.T) {
	node, err := Parse(`((unit) ())`, ParserOptions{AllowedVariables: map[string]struct{}{}})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	got, err := node.Eval(map[string]any{})
	if err != nil {
		t.Fatalf("Eval returned error: %v", err)
	}
	want := []any{TaggedValue{Tag: "unit"}, []any{}}
	if !Equal(got, want) {
		t.Fatalf("Eval returned %#v, want %#v", got, want)
	}
}

func TestParseAcceptsOpaqueOperations(t *testing.T) {
	tests := []string{
		`(from post (where (same-user? current-user author)) (order-by created-at desc) (limit 20))`,
		`(create post ((body "hello")))`,
		`(update post post-id ((body "edited")))`,
		`(delete post post-id)`,
		`(command (publish-post post-id) posted failed)`,
		`(go post-detail selected-post)`,
		`(back)`,
	}

	opts := ParserOptions{
		AllowedVariables: map[string]struct{}{
			"post_id": {},
		},
		AllowedFunctions: map[string]int{},
		AllowedCommands: map[string]int{
			"publish_post": 1,
		},
	}

	for _, tc := range tests {
		if _, err := Parse(tc, opts); err != nil {
			t.Fatalf("Parse(%q) returned error: %v", tc, err)
		}
	}
}

func TestParseRejectsInlineCommandOperations(t *testing.T) {
	_, err := Parse(`(command (create post ((body "hello"))) posted failed)`, ParserOptions{
		AllowedVariables: map[string]struct{}{},
		AllowedFunctions: map[string]int{},
		AllowedCommands: map[string]int{
			"publish_post": 1,
		},
	})
	if err == nil {
		t.Fatal("expected Parse to fail for inline command operation")
	}
}

func TestParseRejectsNonBooleanIfConditionAtRuntime(t *testing.T) {
	node, err := Parse(`(if 1 true false)`, ParserOptions{AllowedVariables: map[string]struct{}{}})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if _, err := node.Eval(map[string]any{}); err == nil {
		t.Fatal("expected if to reject non-bool condition")
	}
}

func TestParseRejectsCondWithoutFinalElse(t *testing.T) {
	opts := ParserOptions{AllowedVariables: map[string]struct{}{"amount": {}}}

	_, err := Parse(`(cond ((> amount 100) "large") ((< amount 0) "invalid"))`, opts)
	if err == nil {
		t.Fatal("expected cond without else to fail")
	}
	if err.Error() != "cond requires a final else clause" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsCondElseBeforeLast(t *testing.T) {
	opts := ParserOptions{AllowedVariables: map[string]struct{}{"amount": {}}}

	_, err := Parse(`(cond (else "normal") ((> amount 100) "large"))`, opts)
	if err == nil {
		t.Fatal("expected cond with early else to fail")
	}
	if err.Error() != "cond else clause must be last" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStringFunctionsRejectDynamicCoercionAtRuntime(t *testing.T) {
	tests := []struct {
		expression string
		want       string
	}{
		{expression: `(contains 123 "123")`, want: "contains expects string arguments"},
		{expression: `(starts-with true "true")`, want: "starts-with expects string arguments"},
		{expression: `(ends-with 123 "3")`, want: "ends-with expects string arguments"},
		{expression: `(matches "[0-9]+" 123)`, want: "matches expects string arguments"},
		{expression: `(length true)`, want: "length expects string or list"},
	}

	for _, tc := range tests {
		node, err := Parse(tc.expression, ParserOptions{})
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", tc.expression, err)
		}
		_, err = node.Eval(map[string]any{})
		if err == nil {
			t.Fatalf("Eval(%q) expected error", tc.expression)
		}
		if err.Error() != tc.want {
			t.Fatalf("Eval(%q) error = %v, want %q", tc.expression, err, tc.want)
		}
	}
}

func TestRecordOperationsRejectMissingFieldsAtRuntime(t *testing.T) {
	ctx := map[string]any{
		"model": map[string]any{"count": int64(1)},
	}
	opts := ParserOptions{AllowedVariables: map[string]struct{}{"model": {}}}
	tests := []struct {
		expression string
		want       string
	}{
		{expression: `(get model missing)`, want: `record has no field "missing"`},
		{expression: `(assoc model (missing 2))`, want: `record has no field "missing"`},
	}

	for _, tc := range tests {
		node, err := Parse(tc.expression, opts)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", tc.expression, err)
		}
		_, err = node.Eval(ctx)
		if err == nil {
			t.Fatalf("Eval(%q) expected error", tc.expression)
		}
		if err.Error() != tc.want {
			t.Fatalf("Eval(%q) error = %v, want %q", tc.expression, err, tc.want)
		}
	}
}

func TestParseRejectsUnknownIdentifier(t *testing.T) {
	opts := ParserOptions{AllowedVariables: map[string]struct{}{"title": {}}}

	_, err := Parse(`(> amount-paid 0)`, opts)
	if err == nil {
		t.Fatal("expected Parse to fail for unknown identifier")
	}
}
