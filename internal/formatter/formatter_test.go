package formatter

import (
	"strings"
	"testing"
)

func TestFormatIsIdempotent(t *testing.T) {
	src := `
(define todo (entity (fields ((title string) (done bool)))))
(define-app todos (entities todo))
`

	once, err := Format(src)
	if err != nil {
		t.Fatalf("first format failed: %v", err)
	}
	twice, err := Format(once)
	if err != nil {
		t.Fatalf("second format failed: %v", err)
	}
	if once != twice {
		t.Fatalf("formatter is not idempotent\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
}

func TestFormatCanonicalOutput(t *testing.T) {
	src := `(define todo (entity (fields ((title string) (done bool)))))(define-app todos (entities todo))`

	formatted, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	expected := "" +
		"(define todo\n" +
		"  (entity\n" +
		"    (fields ((title string) (done bool)))))\n\n" +
		"(define-app todos\n" +
		"  (entities todo))\n"

	if formatted != expected {
		t.Fatalf("unexpected formatted output\n--- expected ---\n%s\n--- got ---\n%s", expected, formatted)
	}
}

func TestFormatKeepsStructuralHeadersReadable(t *testing.T) {
	src := `
(define-record demo-model (count int) (loading bool))

(define-screen demo
  (msg (loaded value) failed)
  (init ((demo-model (count 0) (loading true)) ()))
  (update msg model (match msg ((loaded value) ((assoc model (count value) (loading false)) ())) (failed ((assoc model (loading false)) ()))))
  (view model (section (title "Demo") (text "This sentence is intentionally long enough to force a multiline view body") (button "Load" (loaded (get model count))))))

(define-app demo-app (frontend (screens demo)))
`

	formatted, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	for _, want := range []string{
		"  (update msg model\n",
		"    (match msg\n",
		"  (view model\n",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q\n--- got ---\n%s", want, formatted)
		}
	}
	for _, unwanted := range []string{
		"  (update\n    msg\n    model\n",
		"    (match\n      msg\n",
		"  (view\n    model\n",
	} {
		if strings.Contains(formatted, unwanted) {
			t.Fatalf("formatted output should not contain %q\n--- got ---\n%s", unwanted, formatted)
		}
	}
}

func TestFormatKeepsDefappReferenceListsConsistent(t *testing.T) {
	src := `
(define todo (entity (fields ((title string)))))
(define post (entity (fields ((body string)))))
(define (open-todos) (query todo))
(define (open-posts) (query post))
(define rename-todo (action (input ((todo-id int))) (update todo todo-id ((title "done")))))
(define rename-post (action (input ((post-id int))) (update post post-id ((body "done")))))

(define-app demo
  (backend
    (entities todo post)
    (queries open-todos open-posts)
    (actions rename-todo rename-post)))
`

	formatted, err := Format(src)
	if err != nil {
		t.Fatalf("format failed: %v", err)
	}

	for _, want := range []string{
		"    (entities\n      todo\n      post)\n",
		"    (queries\n      open-todos\n      open-posts)\n",
		"    (actions\n      rename-todo\n      rename-post)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q\n--- got ---\n%s", want, formatted)
		}
	}
}

func TestFormatRejectsInvalidSource(t *testing.T) {
	src := `(define todo (entity (fields ((title string))))`

	if _, err := Format(src); err == nil {
		t.Fatal("expected format to fail for invalid source")
	}
}
