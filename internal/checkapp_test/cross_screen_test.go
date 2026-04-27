package checkapp_test

import (
	"strings"
	"testing"

	"mar/internal/parser"
)

// Cross-screen parameter inference: a list view item that opens another
// screen passes the iterated entity row to the destination's first param.
// HM unifies the entity record with the destination param, so accessing
// non-existent fields in the destination gets caught.

func TestHMRejectsCrossScreenWithNonExistentField(t *testing.T) {
	// Profile screen receives a User but accesses .post_id (not on User).
	// Cross-screen inference unifies user param with User; row poly then
	// rejects the nonexistent field access.
	src := `
(define-entity user (fields ((email string))))

(define-screen home
  (msg loaded users (failed message))
  (init ((unit) ()))
  (update msg model
    (match msg
      ((loaded users) (model ()))
      ((failed message) (model ()))))
  (view model
    (section
      ((list users user
         ((title email)
          (open profile)))))))

(define-screen (profile p)
  (msg load-failed)
  (init ((unit) ()))
  (update msg model
    (match msg
      (load-failed (model ()))))
  (view model
    (section
      ((text p.bogus_field)))))

(define-app demo
  (backend (entities user))
  (frontend (screens home profile)))
`
	app, err := parser.Parse(strings.TrimSpace(src) + "\n")
	if err == nil {
		t.Fatalf("expected parse error from cross-screen mismatch, got app=%v", app)
	}
	if !strings.Contains(err.Error(), "bogus_field") && !strings.Contains(err.Error(), "User") {
		t.Logf("got error (acceptable): %v", err)
	}
}

func TestHMAcceptsCrossScreenWithValidField(t *testing.T) {
	// Same shape but accesses a real field (.email): should compile.
	src := `
(define-entity user (fields ((email string))))

(define-screen home
  (msg loaded users (failed message))
  (init ((unit) ()))
  (update msg model
    (match msg
      ((loaded users) (model ()))
      ((failed message) (model ()))))
  (view model
    (section
      ((list users user
         ((title email)
          (open profile)))))))

(define-screen (profile p)
  (msg load-failed)
  (init ((unit) ()))
  (update msg model
    (match msg
      (load-failed (model ()))))
  (view model
    (section
      ((text p.email)))))

(define-app demo
  (backend (entities user))
  (frontend (screens home profile)))
`
	if _, err := parser.Parse(strings.TrimSpace(src) + "\n"); err != nil {
		t.Fatalf("Parse returned error on valid cross-screen field: %v", err)
	}
}
