package runtime

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/expr"
	"mar/internal/model"
)

func TestValidateFalseReturnsDefaultValidationError(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "validate-false.db"), `
(define purchase
  ((fields
     ((amount decimal)))
   (validate
     (> amount 0))
   (authorize
     (((read create update delete)
        true)))))

(define-app demo
  (entities purchase))
`)

	rec := doRuntimeRequest(r, http.MethodPost, "/purchases", `{"amount":0}`, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Validation failed for Purchase") {
		t.Fatalf("expected default validation message, got body=%s", rec.Body.String())
	}
}

func TestValidateErrorReturnsCustomValidationMessage(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "validate-error.db"), `
(define (must-be-positive amount)
  (if (> amount 0)
      true
      (error "amount must be positive")))

(define purchase
  ((fields
     ((amount decimal)))
   (validate
     (must-be-positive amount))
   (authorize
     (((read create update delete)
        true)))))

(define-app demo
  (entities purchase))
`)

	rec := doRuntimeRequest(r, http.MethodPost, "/purchases", `{"amount":0}`, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "amount must be positive") {
		t.Fatalf("expected custom validation message, got body=%s", rec.Body.String())
	}
}

func TestAuthorizeFalseReturnsDefaultAuthorizationError(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "authorize-false.db"), `
(define purchase
  ((fields
     ((amount decimal)))
   (authorize
     (((read update delete)
        true)
      (create false)))))

(define-app demo
  (entities purchase))
`)

	rec := doRuntimeRequest(r, http.MethodPost, "/purchases", `{"amount":10}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Authentication required") {
		t.Fatalf("expected default authorization message, got body=%s", rec.Body.String())
	}
}

func TestAuthorizeErrorReturnsCustomAuthorizationMessage(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "authorize-error.db"), `
(define purchase
  ((fields
     ((amount decimal)))
   (authorize
     (((read update delete)
        true)
      (create (error "custom access denied"))))))

(define-app demo
  (entities purchase))
`)

	rec := doRuntimeRequest(r, http.MethodPost, "/purchases", `{"amount":10}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "custom access denied") {
		t.Fatalf("expected custom authorization message, got body=%s", rec.Body.String())
	}
}

func TestAuthorizeErrorDuringListStopsWholeRequest(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "authorize-list-error.db"), `
(define purchase
  ((fields
     ((amount decimal)))
   (authorize
     ((read
        (if (> amount 10)
            true
            (error "row access denied")))
      ((create update delete)
       true)))))

(define-app demo
  (entities purchase))
`)

	if rec := doRuntimeRequest(r, http.MethodPost, "/purchases", `{"amount":25}`, ""); rec.Code != http.StatusCreated {
		t.Fatalf("expected first create to succeed, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRuntimeRequest(r, http.MethodPost, "/purchases", `{"amount":5}`, ""); rec.Code != http.StatusCreated {
		t.Fatalf("expected second create to succeed, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec := doRuntimeRequest(r, http.MethodGet, "/purchases", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "row access denied") {
		t.Fatalf("expected list to stop on custom error, got body=%s", rec.Body.String())
	}
}

func TestOptionalFieldsAreMaybeValuesInValidation(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "optional-validation.db"), `
(define-entity profile
    (fields
      ((handle string optional)))
    (validate
      (match handle
        ((nothing)
         true)
        ((just value)
         (if (>= (length value) 3)
           true
           (error "handle must have at least 3 characters")))))
    (authorize
      (((read create update delete)
        true))))

(define-app demo
  (backend
    (entities profile)))
`)

	if rec := doRuntimeRequest(r, http.MethodPost, "/profiles", `{}`, ""); rec.Code != http.StatusCreated {
		t.Fatalf("expected missing optional handle to succeed, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec := doRuntimeRequest(r, http.MethodPost, "/profiles", `{"handle":"ab"}`, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected short handle to fail, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "handle must have at least 3 characters") {
		t.Fatalf("expected custom validation message, got body=%s", rec.Body.String())
	}

	if rec := doRuntimeRequest(r, http.MethodPost, "/profiles", `{"handle":"abc"}`, ""); rec.Code != http.StatusCreated {
		t.Fatalf("expected valid handle to succeed, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestValidateRequiresBoolAtRuntime(t *testing.T) {
	validator, err := expr.Parse(`"truthy string"`, expr.ParserOptions{})
	if err != nil {
		t.Fatalf("parse validator failed: %v", err)
	}
	entity := &model.Entity{Name: "Todo", Validate: `"truthy string"`}
	r := &Runtime{
		validators: map[string]expr.Expr{"Todo": validator},
	}

	err = r.validateEntity(entity, map[string]any{})
	if err == nil {
		t.Fatal("expected validation to reject non-bool result")
	}
	apiErr, ok := err.(*apiError)
	if !ok {
		t.Fatalf("expected apiError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusUnprocessableEntity || apiErr.Code != "entity_validation_failed" {
		t.Fatalf("unexpected validation error: %+v", apiErr)
	}
	if !strings.Contains(apiErr.Message, "must return bool") {
		t.Fatalf("unexpected message: %q", apiErr.Message)
	}
}

func TestAuthorizeRequiresBoolAtRuntime(t *testing.T) {
	authorization, err := expr.Parse(`"truthy string"`, expr.ParserOptions{})
	if err != nil {
		t.Fatalf("parse authorization failed: %v", err)
	}
	entity := &model.Entity{Name: "Todo"}
	r := &Runtime{
		authUser: &model.Entity{Name: "User"},
		authorizers: map[string]map[string]expr.Expr{
			"Todo": {"create": authorization},
		},
	}

	_, err = r.evaluateAuthorization(entity, "create", authSession{Authenticated: true}, map[string]any{})
	if err == nil {
		t.Fatal("expected authorization to reject non-bool result")
	}
	apiErr, ok := err.(*apiError)
	if !ok {
		t.Fatalf("expected apiError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusInternalServerError || apiErr.Code != "authorization_misconfigured" {
		t.Fatalf("unexpected authorization error: %+v", apiErr)
	}
	if !strings.Contains(apiErr.Message, "must return bool") {
		t.Fatalf("unexpected message: %q", apiErr.Message)
	}
}
