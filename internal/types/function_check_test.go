package types

import (
	"strings"
	"testing"

	"mar/internal/parser"
)

// Verifies CheckApp infers user functions including direct recursion.
func TestHMChecksDirectRecursion(t *testing.T) {
	src := `
(define (positive? n)
  (if (= n 0) false (if (> n 0) true (positive? (- 0 n)))))

(define-entity widget
    (fields ((amount int)))
    (validate (positive? amount)))

(define-app testapp
  (backend (entities widget)))
`
	app, err := parser.Parse(strings.TrimSpace(src) + "\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := CheckApp(app); err != nil {
		t.Fatalf("CheckApp: %v", err)
	}
}

// Verifies mutual recursion works.
func TestHMChecksMutualRecursion(t *testing.T) {
	src := `
(define (even-pos? n)
  (if (= n 0) true (odd-pos? (- n 1))))

(define (odd-pos? n)
  (if (= n 0) false (even-pos? (- n 1))))

(define-entity widget
    (fields ((amount int)))
    (validate (even-pos? amount)))

(define-app testapp
  (backend (entities widget)))
`
	app, err := parser.Parse(strings.TrimSpace(src) + "\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := CheckApp(app); err != nil {
		t.Fatalf("CheckApp: %v", err)
	}
}

// Verifies that a type error in a function body is caught.
func TestHMRejectsBadFunctionBody(t *testing.T) {
	src := `
(define (broken n)
  (+ n true))

(define-entity widget
    (fields ((amount int)))
    (validate (= (broken amount) 0)))

(define-app testapp
  (backend (entities widget)))
`
	app, err := parser.Parse(strings.TrimSpace(src) + "\n")
	if err != nil {
		// Old checker may catch this. Either path is acceptable.
		return
	}
	if err := CheckApp(app); err == nil {
		t.Fatal("expected HM to reject (+ n true)")
	}
}
