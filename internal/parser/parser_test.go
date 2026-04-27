package parser

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseBuildsAppFromSExpressions(t *testing.T) {
	src := `
(define app-config
  ((database "./pet-food-log.db")
   (server
     ((port 4300)))))

(define app-auth
  ((from "no-reply@example.com")
   (subject "Your code")
   (smtp-host "smtp.example.com")
   (smtp-username "apikey")
   (smtp-password-env "SMTP_PASSWORD")))

(define-entity purchase
    (fields
      ((purchase-date date)
       (amount-paid decimal)))
    (belongs-to
      ((user)))
    (defaults
      ((user current-user)))
    (validate
      (if (> amount-paid 0)
          true
          (error "amount paid must be greater than zero")))
    (authorize
      ((read (same-user? current-user user))
       (create (authenticated? current-user)))))

(define-record purchases-model
  (items (list purchase)))

(define-screen purchases
  (title "Purchases")
  (init
    ((purchases-model ())
     ()))
  (view model
    (section
      ((list items purchase
         ((title purchase-date)
          (subtitle amount-paid)))))))

(define-app pet-food-log
  (config app-config)
  (auth app-auth)
  (entities purchase)
  (screens purchases))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if app.AppName != "pet-food-log" {
		t.Fatalf("unexpected app name: %q", app.AppName)
	}
	if app.Port != 4300 {
		t.Fatalf("unexpected port: %d", app.Port)
	}
	if app.Database != "./pet-food-log.db" {
		t.Fatalf("unexpected database: %q", app.Database)
	}
	if app.Auth == nil || app.Auth.EmailFrom != "no-reply@example.com" {
		t.Fatalf("unexpected auth config: %+v", app.Auth)
	}
	if len(app.Entities) != 2 {
		t.Fatalf("expected built-in User plus entity, got %d", len(app.Entities))
	}

	purchase := app.Entities[1]
	if purchase.Name != "Purchase" {
		t.Fatalf("unexpected entity name: %q", purchase.Name)
	}
	if purchase.Table != "purchase" {
		t.Fatalf("unexpected table: %q", purchase.Table)
	}
	if purchase.Validate != "(if (> amount-paid 0) true (error \"amount paid must be greater than zero\"))" {
		t.Fatalf("unexpected validate expression: %q", purchase.Validate)
	}
	if len(purchase.Authorizations) != 2 {
		t.Fatalf("unexpected authorizations: %+v", purchase.Authorizations)
	}

	foundUserRelation := false
	for _, field := range purchase.Fields {
		if field.Name == "user" {
			foundUserRelation = true
			if field.RelationEntity != "User" || !field.CurrentUser {
				t.Fatalf("unexpected relation field: %+v", field)
			}
		}
	}
	if !foundUserRelation {
		t.Fatal("expected belongs-to field user")
	}

	if app.Screens == nil || len(app.Screens.Screens) != 1 {
		t.Fatalf("unexpected screens: %+v", app.Screens)
	}
	screen := app.Screens.Screens[0]
	if screen.Name != "Purchases" || screen.Title != "Purchases" {
		t.Fatalf("unexpected screen: %+v", screen)
	}
	item := screen.Sections[0].Items[0]
	if item.Kind != "list" || item.ModelField != "items" || item.Entity != "Purchase" || item.TitleField != "purchase_date" || item.SubtitleField != "amount_paid" {
		t.Fatalf("unexpected list item: %+v", item)
	}
}

func TestParseRejectsImplicitQueryLists(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)
       (done bool)))
    (belongs-to
      ((user)))
    (defaults
      ((user current-user))))

(define-query (my-todos)
  (from todo)
    (where (same-user? current-user user))
    (order-by title asc))

(define-screen home
  (title "Home")
  (view
    (section
      ((list (my-todos)
         ((title title)
          (subtitle done)))))))

(define-app todos
  (entities todo)
  (screens home))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected implicit query list to be rejected")
	}
	if !strings.Contains(err.Error(), "list expects (list model-field entity") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSupportsModelBackedLists(t *testing.T) {
	src := `
(define-entity post
    (fields ((body string)))
    (belongs-to ((author user))))

(define-query (posts-by-author author-id)
  (from post)
    (where (= author author-id))
    (order-by created-at desc)
    (limit 20))

(define-record timeline-model
  (posts (list post)))

(define-screen timeline
  (msg
    (loaded posts)
    (failed message))
  (init
    ((timeline-model ())
     ((command (posts-by-author 1) loaded failed))))
  (update msg model
    (match msg
      ((loaded posts) ((assoc model (posts posts)) ()))
      ((failed message) (model ()))))
  (view model
    (section
      "Posts"
      ((list posts post
         ((title body)))))))

(define-app mini-twitter
  (backend
    (entities post)
    (queries posts-by-author))
  (frontend
    (screens timeline)))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	item := app.Screens.Screens[0].Sections[0].Items[0]
	if item.Kind != "list" || item.ModelField != "posts" || item.Entity != "Post" || item.TitleField != "body" {
		t.Fatalf("unexpected model-backed list item: %+v", item)
	}
}

func TestParseRejectsUnusedFunction(t *testing.T) {
	src := `
(define (helper value)
  value)

(define-screen home
  (view
    (section
      ((link "Home" home)))))

(define-app demo
  (frontend
    (screens home)))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), `function "helper" is defined but never used`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsScreenNotExposedInDefapp(t *testing.T) {
	src := `
(define-screen home
  (view
    (section
      ((link "Home" home)))))

(define-screen settings
  (view
    (section
      ((link "Home" home)))))

(define-app demo
  (frontend
    (screens home)))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), `screen "settings" is defined but not exposed in define-app`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseInfersScreenParameterTypeFromListOpen(t *testing.T) {
	src := `
(define-entity user
    (fields ((handle string)))
    (authorize ((read true))))

(define-entity post
    (fields ((body string)))
    (belongs-to ((author user)))
    (authorize ((read true))))

(define-record profiles-model
  (people (list user)))

(define-record profile-model
  (handle string))

(define-query (all-users)
  (from user))

(define-screen profiles
  (msg
    (loaded people)
    (failed message))
  (init
    ((profiles-model ())
     ((command (all-users) loaded failed))))
  (update msg model
    (match msg
      ((loaded people) ((assoc model (people people)) ()))
      ((failed message) (model ()))))
  (view model
    (section
      ((list people user
         ((title handle)
          (open profile-detail)))))))

(define-screen (profile-detail user)
  (init
    ((profile-model user.handle)
     ()))
  (view model
    (section
      ((text user.handle)))))

(define-app demo
  (entities user post)
  (queries all-users)
  (frontend
    (screens profiles profile-detail)))
`

	_, err := Parse(src)
	if err != nil {
		t.Fatalf("expected parse success, got %v", err)
	}
}

func TestParseInfersScreenParameterTypeFromGo(t *testing.T) {
	src := `
(define-entity user
    (fields ((handle string)))
    (authorize ((read true))))

(define-record profiles-model
  (people (list user)))

(define-record profile-model
  (handle string))

(define-query (all-users)
  (from user))

(define-screen profiles
  (msg
    (loaded people)
    (failed message))
  (init
    ((profiles-model ())
     ((command (all-users) loaded failed))))
  (update msg model
    (match msg
      ((loaded people) ((assoc model (people people)) ()))
      ((failed message) (model ()))))
  (view model
    (section
      ((list people user
         ((title handle)
          (open profile-detail)))))))

(define-screen (profile-detail user)
  (msg edit-clicked)
  (init
    ((unit)
     ()))
  (update msg model
    (match msg
      (edit-clicked
        ((unit)
         ((go edit-profile user))))))
  (view
    (section
      ((button "Edit" edit-clicked)))))

(define-screen (edit-profile user)
  (init
    ((profile-model user.handle)
     ()))
  (view
    (section
      ((link "Back" profiles)))))

(define-app demo
  (entities user)
  (queries all-users)
  (frontend
    (screens profiles profile-detail edit-profile)))
`

	_, err := Parse(src)
	if err != nil {
		t.Fatalf("expected parse success, got %v", err)
	}
}

func TestParseRejectsUnknownIdentifiersInsideValidate(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)))
    (validate
      (> amount-paid 0)))

(define-app todos
  (entities todo))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if got := err.Error(); got == "" || got == "expected parse error" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSupportsUniqueAndCompositeTypes(t *testing.T) {
	src := `
(define-record post-item
  (body string))

(define-record feed-page
  (items (list post-item))
  (next-cursor (maybe cursor))
  (load-result (result string (list post-item))))

(define-entity post
    (fields
      ((body string)))
    (belongs-to
      ((author user)))
    (unique
      ((author body))))

(define-app demo
  (entities post))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if len(app.Records) != 2 {
		t.Fatalf("expected two records, got %+v", app.Records)
	}
	recordIndex := -1
	for i := range app.Records {
		if app.Records[i].Name == "feed_page" {
			recordIndex = i
			break
		}
	}
	if recordIndex < 0 {
		t.Fatalf("expected feed_page record, got %+v", app.Records)
	}
	fields := app.Records[recordIndex].Fields
	if len(fields) != 3 {
		t.Fatalf("expected feed_page fields, got %+v", app.Records)
	}
	if got := fields[1].Type; got != "(maybe cursor)" {
		t.Fatalf("unexpected maybe cursor type: %q", got)
	}
	if got := fields[2].Type; got != "(result string (list post_item))" {
		t.Fatalf("unexpected result type: %q", got)
	}
	postIndex := -1
	for i := range app.Entities {
		if app.Entities[i].Name == "Post" {
			postIndex = i
			break
		}
	}
	if postIndex < 0 {
		t.Fatalf("expected Post entity, got %+v", app.Entities)
	}
	if len(app.Entities[postIndex].Unique) != 1 {
		t.Fatalf("expected one unique constraint, got %+v", app.Entities[postIndex].Unique)
	}
	if app.Entities[postIndex].Unique[0].Fields[0] != "author" || app.Entities[postIndex].Unique[0].Fields[1] != "body" {
		t.Fatalf("unexpected unique constraint: %+v", app.Entities[postIndex].Unique[0])
	}
}

func TestParseSupportsBackendFrontendQueriesAndActions(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)
       (done bool)))
    (belongs-to
      ((user)))
    (defaults
      ((user current-user)
       (done false))))

(define-query (open-todos)
  (from todo)
    (where (same-user? current-user user))
    (order-by created-at desc)
    (limit 20))

(define-action complete-todo
    (input
      ((todo-id int)))
    (update todo todo-id
      ((done true))))

(define-record home-model
  (todos (list todo)))

(define-screen home
  (title "Home")
  (msg
    (loaded todos)
    (failed message))
  (init
    ((home-model ())
     ((command (open-todos) loaded failed))))
  (update msg model
    (match msg
      ((loaded todos) ((assoc model (todos todos)) ()))
      ((failed message) (model ()))))
  (view model
    (section
      ((list todos todo
         ((title title)))))))

(define-app todos
  (backend
    (entities todo)
    (queries open-todos)
    (actions complete-todo))
  (frontend
    (screens home)))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if len(app.Queries) != 1 {
		t.Fatalf("expected one query, got %+v", app.Queries)
	}
	if app.Queries[0].Name != "open_todos" || app.Queries[0].Entity != "Todo" {
		t.Fatalf("unexpected query metadata: %+v", app.Queries[0])
	}
	if app.Queries[0].Limit == nil || *app.Queries[0].Limit != 20 {
		t.Fatalf("unexpected query limit: %+v", app.Queries[0])
	}

	if len(app.InputAliases) != 1 {
		t.Fatalf("expected one input alias, got %+v", app.InputAliases)
	}
	if app.InputAliases[0].Name != "CompleteTodoInput" {
		t.Fatalf("unexpected input alias: %+v", app.InputAliases[0])
	}

	if len(app.Actions) != 1 {
		t.Fatalf("expected one action, got %+v", app.Actions)
	}
	if app.Actions[0].Name != "complete_todo" || app.Actions[0].InputAlias != "CompleteTodoInput" {
		t.Fatalf("unexpected action metadata: %+v", app.Actions[0])
	}
	if len(app.Actions[0].Steps) != 1 || app.Actions[0].Steps[0].Kind != "update" {
		t.Fatalf("unexpected action steps: %+v", app.Actions[0].Steps)
	}

	item := app.Screens.Screens[0].Sections[0].Items[0]
	if item.ModelField != "todos" || item.Entity != "Todo" || item.Filter != "" {
		t.Fatalf("unexpected screen item: %+v", item)
	}
}

func TestParseSupportsFunctionHelpersInAuthorizeAndValidate(t *testing.T) {
	src := `
(define (require-owner owner-id)
  (if (same-user? current-user owner-id)
      true
      (error "owner only")))

(define-entity purchase
    (fields
      ((amount-paid decimal)))
    (belongs-to
      ((user)))
    (defaults
      ((user current-user)))
    (validate
      (if (> amount-paid 0)
          true
          (error "must be positive")))
    (authorize
      (((read update)
         (require-owner user)))))

(define-app demo
  (entities purchase))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(app.Functions) != 1 {
		t.Fatalf("expected one function, got %d", len(app.Functions))
	}
	if app.Functions[0].Name != "require_owner" {
		t.Fatalf("unexpected function: %+v", app.Functions[0])
	}
	if app.Entities[1].Validate == "" {
		t.Fatal("expected validate expression to be set")
	}
}

func TestParseSupportsCurrentUserPredicatesInAuthorize(t *testing.T) {
	src := `
(define-entity purchase
    (fields
      ((amount int)))
    (belongs-to
      ((user)))
    (defaults
      ((user current-user)))
    (authorize
      ((read (same-user? current-user user))
       (create (authenticated? current-user))
       (delete (anonymous? current-user)))))

(define-app demo
  (entities purchase))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseSupportsGroupedAuthorizeActions(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)))
    (belongs-to
      ((user)))
    (authorize
      (((read update delete)
         (and (authenticated? current-user)
              (same-user? current-user user)))
       (create (authenticated? current-user)))))

(define-app demo
  (entities todo))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	auths := app.Entities[1].Authorizations
	if len(auths) != 4 {
		t.Fatalf("expected 4 authorizations, got %+v", auths)
	}
	if auths[0].Action != "read" || auths[1].Action != "update" || auths[2].Action != "delete" || auths[3].Action != "create" {
		t.Fatalf("unexpected authorization actions: %+v", auths)
	}
}

func TestParseRejectsNonBoolValidateCondition(t *testing.T) {
	src := `
(define-entity purchase
    (fields
      ((amount int)))
    (validate
      (if 123
          true
          false)))

(define-app store
  (entities purchase))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "validate: if condition must be bool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseTreatsErrorAsNeverInTypeInference(t *testing.T) {
	src := `
(define-entity purchase
    (fields
      ((amount int)))
    (validate
      (if (> amount 0)
          true
          (error "must be positive"))))

(define-app store
  (entities purchase))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseDoesNotLetErrorMaskConcreteTypeMismatch(t *testing.T) {
	src := `
(define-entity purchase
    (fields
      ((amount int)))
    (validate
      (if (> amount 0)
          (error "must be positive")
          "yes")))

(define-app store
  (entities purchase))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "entity Purchase validate: expression must return bool, got string") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsCondWithoutElseInValidate(t *testing.T) {
	src := `
(define-entity purchase
    (fields
      ((amount int)))
    (validate
      (cond
        ((> amount 100) true)
        ((< amount 0) false))))

(define-app store
  (entities purchase))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "entity Purchase validate: cond requires a final else clause") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsWrongTypedStringFunctionCall(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)
       (done bool)))
    (validate
      (contains done title)))

(define-app todos
  (entities todo))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "entity Todo validate: contains expects string arguments, got bool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsDynamicMatchesPattern(t *testing.T) {
	src := `
(define (has-match pattern value)
  (matches pattern value))

(define-entity todo
    (fields
      ((title string)))
    (validate
      (has-match "^ship" title)))

(define-app todos
  (entities todo))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "matches expects a static regex literal as first argument") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsInvalidStaticMatchesPattern(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)))
    (validate
      (matches "[" title)))

(define-app todos
  (entities todo))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "entity Todo validate: matches regex is invalid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsWrongTypedLengthCall(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)
       (done bool)))
    (validate
      (> (length done) 0)))

(define-app todos
  (entities todo))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "entity Todo validate: length expects string or list, got bool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseInfersStringFunctionParameterConstraint(t *testing.T) {
	src := `
(define (has-prefix prefix value)
  (starts-with prefix value))

(define-entity todo
    (fields
      ((done bool)))
    (validate
      (has-prefix "x" done)))

(define-app todos
  (entities todo))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "entity Todo validate: has_prefix parameter value expects string, got bool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsNonBoolQueryWhere(t *testing.T) {
	src := `
(define-entity todo
    (fields ((title string))))

(define-query (recent-todos)
  (from todo)
    (where title))

(define-app todos
  (entities todo)
  (queries recent-todos))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "query recent-todos where: expression must return bool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsActionFieldTypeMismatch(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)
       (done bool))))

(define-action complete-todo
    (input
      ((id int)))
    (update todo input.id
      ((done "yes"))))

(define-app todos
  (entities todo)
  (actions complete-todo))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "action complete_todo step update Todo field done: expects bool, got string") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseInfersQueryParameterTypeFromWhereComparison(t *testing.T) {
	src := `
(define-entity todo
    (fields ((title string))))

(define-query (todos-by-title wanted-title)
  (from todo)
    (where
      (and
        (= title wanted-title)
        (if wanted-title true false))))

(define-app todos
  (entities todo)
  (queries todos-by-title))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "query todos-by-title where: if condition must be bool, got string") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseStoresInferredQueryParameterTypes(t *testing.T) {
	src := `
(define-entity post
    (fields
      ((title string)
       (published bool)
       (score decimal))))

(define-query (matching-posts wanted-title wanted-published min-score)
  (from post)
    (where
      (and
        (= title wanted-title)
        (= published wanted-published)
        (> score min-score))))

(define-app blog
  (backend
    (entities post)
    (queries matching-posts)))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(app.Queries) != 1 {
		t.Fatalf("expected one query, got %+v", app.Queries)
	}
	got := app.Queries[0].ParameterTypes
	if got["wanted_title"] != "String" || got["wanted_published"] != "Bool" || got["min_score"] != "Decimal" {
		t.Fatalf("unexpected parameter types: %+v", got)
	}
}

func TestParseRejectsUninferableQueryParameterType(t *testing.T) {
	src := `
(define-entity todo
    (fields ((title string))))

(define-query (todos value)
  (from todo)
    (where true))

(define-app todos
  (backend
    (entities todo)
    (queries todos)))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "query todos parameter value: type could not be inferred") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsWrongTypedFieldDefault(t *testing.T) {
	src := `
(define-entity invoice
    (fields
      ((amount decimal)
       (paid bool)))
    (defaults
      ((amount true))))

(define-app billing
  (entities invoice))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "entity invoice default amount: expects decimal, got bool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsOrderedComparisonOnStringField(t *testing.T) {
	src := `
(define-entity todo
    (fields ((title string)))
    (validate
      (> title "abc")))

(define-app todos
  (entities todo))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "entity Todo validate: operator > expects ordered values, got string") {
		t.Fatalf("unexpected error: %v", err)
	}
}


func TestParseRejectsNonExhaustiveBackendMaybeMatch(t *testing.T) {
	src := `
(define-entity profile
    (fields
      ((handle string optional)))
    (validate
      (match handle
        ((just value) true))))

(define-app demo
  (entities profile))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "entity Profile validate: match is not exhaustive; missing nothing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsNonExhaustiveBackendResultMatch(t *testing.T) {
	src := `
(define-record load-model
  (value (result string int)))

(define-entity item
    (fields
      ((amount int)))
    (validate
      (let ((state (load-model (value (ok amount)))))
        (match (get state value)
          ((ok value) true)))))

(define-app demo
  (entities item))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "entity Item validate: match is not exhaustive; missing err") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsUsingFirstWithoutHandlingMaybe(t *testing.T) {
	src := `
(define-record nums
  (items (list int)))

(define-entity item
    (fields
      ((amount int)))
    (validate
      (let ((state (nums (items (cons amount ())))))
        (= amount (first (get state items))))))

(define-app demo
  (entities item))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	// HM rejects with "cannot compare int with (maybe int)". The original
	// test expected the legacy checker's message; HM's diagnostic is different
	// but catches the same problem.
	if !strings.Contains(err.Error(), "cannot compare") && !strings.Contains(err.Error(), "maybe") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseAllowsEmptyListWhenRecordFieldTypeIsKnown(t *testing.T) {
	src := `
(define-record nums
  (items (list int)))

(define-entity item
    (fields
      ((amount int)))
    (validate
      (let ((state (nums (items ()))))
        (= (length (get state items)) 0))))

(define-app demo
  (entities item))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseInfersMaybeConstructorFromBranch(t *testing.T) {
	src := `
(define-entity item
    (fields
      ((amount int)))
    (validate
      (match (if true (nothing) (just amount))
        ((nothing) true)
        ((just value) (= value amount)))))

(define-app demo
  (entities item))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseInfersResultConstructorFromBranch(t *testing.T) {
	src := `
(define-entity item
    (fields
      ((amount int)))
    (validate
      (match (if true (ok amount) (err "bad"))
        ((ok value) (= value amount))
        ((err error) false))))

(define-app demo
  (entities item))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseInfersMaybeAndResultConstructorsFromRecordFields(t *testing.T) {
	src := `
(define-record state
  (maybe-value (maybe int))
  (ok-value (result string int))
  (err-value (result string int)))

(define-entity item
    (fields
      ((amount int)))
    (validate
      (let ((state (state
                     (maybe-value (nothing))
                     (ok-value (ok amount))
                     (err-value (err "bad")))))
        true)))

(define-app demo
  (entities item))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseAcceptsResultFunctionWithFreeErrorType(t *testing.T) {
	// The HM checker accepts (ok value) returning result with a free error
	// type variable — that's just polymorphism. The legacy "ambiguous result"
	// rejection is no longer needed.
	src := `
(define (save-value value)
  (ok value))

(define-entity item
    (fields
      ((amount int)))
    (validate
      (match (save-value amount)
        ((ok value) true)
        ((err error) false))))

(define-app demo
  (entities item))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseAllowsFunctionReturnWhenMaybeTypeIsInferred(t *testing.T) {
	src := `
(define (maybe-value value)
  (if true (nothing) (just value)))

(define-entity item
    (fields
      ((amount int)))
    (validate
      (match (maybe-value amount)
        ((nothing) true)
        ((just value) (= value amount)))))

(define-app demo
  (entities item))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseAcceptsRecursiveFunctionViaHM(t *testing.T) {
	// HM (internal/types) handles recursion natively. Use a function whose
	// recursive call has a different argument than the parameter so the
	// termination heuristic accepts it.
	src := `
(define (count value)
  (if (= value 0) 0 (count (- value 1))))

(define-entity item
    (fields
      ((amount int)))
    (validate
      (= (count amount) 0)))

(define-app demo
  (entities item))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseAcceptsMutuallyRecursiveFunctionsViaHM(t *testing.T) {
	src := `
(define (left value)
  (right value))

(define (right value)
  (left value))

(define-entity item
    (fields
      ((amount int)))
    (validate
      (= (left amount) amount)))

(define-app demo
  (entities item))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseInfersMaybeAndResultListLiteralElements(t *testing.T) {
	src := `
(define-entity item
    (fields
      ((amount int)))
    (validate
      (and
        (= (length ((nothing) (just amount))) 2)
        (= (length ((ok amount) (err "bad"))) 2))))

(define-app demo
  (entities item))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseAllowsEmptyPredicateOnEmptyList(t *testing.T) {
	src := `
(define-entity item
    (fields
      ((amount int)))
    (validate
      (empty? ())))

(define-app demo
  (entities item))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseSupportsChildrenAndCreateInitialValues(t *testing.T) {
	src := `
(define-entity user
    (fields
      ((display-name string optional))))

(define-entity post
    (fields
      ((body string)))
    (belongs-to
      ((author user))))

(define-entity comment
    (fields
      ((body string)))
    (belongs-to
      ((post)
       (author user))))

(define-query (all-posts)
  (from post))

(define-record posts-model
  (posts (list post)))

(define-screen posts
  (msg
    (loaded items)
    (failed message))
  (init
    ((posts-model (posts ()))
     ((command (all-posts) loaded failed))))
  (update msg model
    (match msg
      ((loaded items)
       ((assoc model (posts items)) ()))
      ((failed message)
       (model ()))))
  (view model
    (section
      ((list posts post
         ((title body)
          (open post-detail)))))))

(define-screen (post-detail post)
  (view
    (section
      "Actions"
      ((create comment
        ((value post post.id)
          (field body)))))))

(define-app demo
  (backend
    (entities user post comment)
    (queries all-posts))
  (frontend
    (screens posts post-detail)))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	screen := app.Screens.Screens[1]
	items := screen.Sections[0].Items
	if len(items) != 1 {
		t.Fatalf("expected one item, got %+v", items)
	}
	if items[0].Kind != "create" || len(items[0].Values) != 1 {
		t.Fatalf("expected create item with preset value, got %+v", items[0])
	}
	if items[0].Values[0].Field != "post" || items[0].Values[0].Expression != "post.id" {
		t.Fatalf("unexpected create preset value: %+v", items[0].Values[0])
	}
}

func TestParseSupportsRecordsAndMVUScreens(t *testing.T) {
	src := `
(define-record order
  (id string)
  (title string))

(define-record orders-screen-model
  (orders (list order-row))
  (loading bool)
  (error (maybe string)))

(define-entity order-row
    (fields
      ((title string)
       (status string))))

(define-query (load-orders)
  (from order-row)
    (where true))

(define-screen orders-by-status
  (msg
    (loaded orders)
    (failed message)
    back)
  (init
    ((orders-screen-model
       (orders ())
       (loading true)
       (error (nothing)))
     ((command (load-orders) loaded failed))))
  (update msg model
    (match msg
      ((loaded orders)
       ((assoc model
          (orders orders)
          (loading false)
          (error (nothing)))
        ()))
      ((failed message)
       ((assoc model
          (loading false)
          (error (just message)))
        ()))
      (back
       (model
        ((back))))))
  (view model
    (section
      (title "Orders")
      (text "Hello"))))

(define-app pet-food-log
  (backend
    (entities order-row)
    (queries load-orders))
  (frontend
    (screens orders-by-status)))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if len(app.Records) != 2 {
		t.Fatalf("expected two records, got %d", len(app.Records))
	}
	var recordIndex = -1
	for i := range app.Records {
		if app.Records[i].Name == "orders_screen_model" {
			recordIndex = i
			break
		}
	}
	if recordIndex < 0 {
		t.Fatalf("expected orders_screen_model record, got %+v", app.Records)
	}
	if got := app.Records[recordIndex].Fields[0].Type; got != "(list order_row)" {
		t.Fatalf("unexpected record field type: %q", got)
	}

	if app.Screens == nil || len(app.Screens.Screens) != 1 {
		t.Fatalf("expected one screen, got %+v", app.Screens)
	}
	screen := app.Screens.Screens[0]
	if screen.Name != "OrdersByStatus" {
		t.Fatalf("unexpected screen name: %q", screen.Name)
	}
	if len(screen.Parameters) != 0 {
		t.Fatalf("unexpected screen parameters: %+v", screen.Parameters)
	}
	if len(screen.Messages) != 3 {
		t.Fatalf("unexpected messages: %+v", screen.Messages)
	}
	if screen.InitExpression == "" || screen.UpdateBody == "" || screen.ViewBody == "" {
		t.Fatalf("expected MVU clauses to be captured: %+v", screen)
	}
	if screen.ViewModel != "model" || screen.UpdateMessage != "msg" || screen.UpdateModel != "model" {
		t.Fatalf("unexpected MVU parameter names: %+v", screen)
	}
}

func TestParseSupportsStaticScreenWithViewOnly(t *testing.T) {
	src := `
(define-screen about
  (view
    (section
      (title "About")
      (text "Pet Food Log"))))

(define-app pet-food-log
  (screens about))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	screen := app.Screens.Screens[0]
	if screen.ViewBody == "" {
		t.Fatalf("expected view body to be captured: %+v", screen)
	}
	if screen.UpdateBody != "" || screen.InitExpression != "" || len(screen.Messages) != 0 {
		t.Fatalf("did not expect dynamic clauses on static screen: %+v", screen)
	}
}

func TestParseSupportsButtonsInViewNodes(t *testing.T) {
	src := `
(define-screen counter
  (msg
    increment)
  (init
    ((unit) ()))
  (update msg model
    (match msg
      (increment
       (model ()))))
  (view
    (section
      (title "Counter")
      (button "Like" (increment)))))

(define-app demo
  (screens counter))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	screen := app.Screens.Screens[0]
	if screen.View == nil {
		t.Fatalf("expected parsed view tree, got %+v", screen)
	}
	if len(screen.View.Children) != 1 {
		t.Fatalf("expected one child in section, got %+v", screen.View)
	}
	button := screen.View.Children[0]
	if button.Kind != "button" {
		t.Fatalf("expected button node, got %+v", button)
	}
	if button.Label != "Like" {
		t.Fatalf("unexpected button label: %+v", button)
	}
	if button.Message != "(increment)" {
		t.Fatalf("unexpected button message: %+v", button)
	}
}

func TestParseSupportsButtonsAndMVUClausesInRegularScreens(t *testing.T) {
	src := `
(define-query (all-posts)
  (from post))

(define-record posts-model
  (posts (list post)))

(define-screen posts
  (msg
    (loaded items)
    (failed message))
  (init
    ((posts-model (posts ()))
     ((command (all-posts) loaded failed))))
  (update msg model
    (match msg
      ((loaded items)
       ((assoc model (posts items)) ()))
      ((failed message)
       (model ()))))
  (view model
    (section
      ((list posts post
         ((title body)
          (open post-detail)))))))

(define-screen (post-detail post)
  (title "Post")
  (msg
    (like-clicked post-id)
    liked
    (like-failed message))
  (init
    ((unit) ()))
  (update msg model
    (match msg
      ((like-clicked post-id)
       (model
        ((command (like-post post-id) liked like-failed))))
      (liked
       (model ()))
      ((like-failed message)
       (model ()))))
  (view model
    (section
      "Actions"
      ((button "Like" (like-clicked post.id))))))

(define-action like-post
    (input
      ((post-id int)))
    (create post-like
      ((post post-id))))

(define-entity post
    (fields
      ((body string))))

(define-entity post-like
    (belongs-to
      ((post))))

(define-app demo
  (backend
    (entities post post-like)
    (queries all-posts)
    (actions like-post))
  (frontend
    (screens posts post-detail)))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	screen := app.Screens.Screens[1]
	if len(screen.Parameters) != 1 || screen.Parameters[0] != "post" {
		t.Fatalf("unexpected screen parameters: %+v", screen.Parameters)
	}
	if screen.InitExpression == "" || screen.UpdateBody == "" || len(screen.Messages) != 3 {
		t.Fatalf("expected mixed screen to keep MVU clauses: %+v", screen)
	}
	if len(screen.Sections) != 1 || len(screen.Sections[0].Items) != 1 {
		t.Fatalf("expected screen section item, got %+v", screen.Sections)
	}
	button := screen.Sections[0].Items[0]
	if button.Kind != "button" || button.Label != "Like" || button.Message != "(like-clicked post.id)" {
		t.Fatalf("unexpected button item: %+v", button)
	}
}

func TestParseSupportsInputItemsInRegularScreens(t *testing.T) {
	src := `
(define-record compose-model
  (body string)
  (submitting bool))

(define-screen compose
  (msg
    (body-changed value)
    submit-clicked
    published
    (publish-failed message))
  (init
    ((compose-model "" false) ()))
  (update msg model
    (match msg
      ((body-changed value)
       ((assoc model
          (body value))
        ()))
      (submit-clicked
       (model
        ((command (publish-post (get model body)) published publish-failed))))
      (published
       ((compose-model "" false)
        ()))
      ((publish-failed message)
       ((assoc model
          (submitting false))
        ()))))
  (view model
    (section
      "Composer"
      ((textarea "What's happening?" body body-changed (disabled submitting))
       (button "Post" (submit-clicked) (disabled submitting))))))

(define-action publish-post
    (input
      ((body string)))
    (create post
      ((body body))))

(define-entity post
    (fields
      ((body string))))

(define-app demo
  (backend
    (entities post)
    (actions publish-post))
  (frontend
    (screens compose)))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	screen := app.Screens.Screens[0]
	if len(screen.Sections) != 1 || len(screen.Sections[0].Items) != 2 {
		t.Fatalf("expected two section items, got %+v", screen.Sections)
	}
	textarea := screen.Sections[0].Items[0]
	if textarea.Kind != "textarea" || textarea.Label != "What's happening?" || textarea.ModelField != "body" || textarea.Message != "body_changed" || textarea.Disabled != "submitting" {
		t.Fatalf("unexpected textarea item: %+v", textarea)
	}
	button := screen.Sections[0].Items[1]
	if button.Kind != "button" || button.Disabled != "submitting" {
		t.Fatalf("unexpected button item: %+v", button)
	}
}

func TestParseInfersScreenMessagePayloadFromButtonArgument(t *testing.T) {
	src := `
(define-record home-model
  (count int))

(define-screen home
  (msg
    (selected value))
  (init
    ((home-model 0)
     ()))
  (update msg model
    (match msg
      ((selected value)
       ((assoc model (count value)) ()))))
  (view model
    (section
      "Home"
      ((button "Select" (selected (get model count)))))))

(define-app demo
  (frontend
    (screens home)))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseSupportsToggleAndSelectItems(t *testing.T) {
	src := `
(define-record editor-model
  (done bool)
  (visibility string))

(define-screen editor
  (msg
    (done-changed value)
    (visibility-changed value))
  (init
    ((editor-model false "public") ()))
  (update msg model
    (match msg
      ((done-changed value)
       ((assoc model
          (done value))
        ()))
      ((visibility-changed value)
       ((assoc model
          (visibility value))
        ()))))
  (view model
    (section
      "Editor"
      ((toggle "Done" done done-changed)
       (select "Visibility" visibility visibility-changed
         ((public "Public") (private "Private")))))))

(define-app demo
  (frontend
    (screens editor)))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	screen := app.Screens.Screens[0]
	if got := screen.Sections[0].Items[0].Kind; got != "toggle" {
		t.Fatalf("expected toggle item, got %q", got)
	}
	selectItem := screen.Sections[0].Items[1]
	if selectItem.Kind != "select" || len(selectItem.Options) != 2 || selectItem.Options[0].Value != "public" {
		t.Fatalf("unexpected select item: %+v", selectItem)
	}
}

func TestParseRejectsScreenEffectsInModelPosition(t *testing.T) {
	src := `
(define-screen profile
  (title "Profile")
  (view
    (section
      "Profile"
      ((text "Profile")))))

(define-screen home
  (msg open-profile)
  (init
    ((go profile) ()))
  (update msg model
    (model ()))
  (view model
    (section
      "Home"
      ((button "Open" open-profile)))))

(define-app demo
  (frontend
    (screens home profile)))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "go can only be used inside the effects list returned by screen init/update") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsSingleScreenEffectInsteadOfEffectList(t *testing.T) {
	src := `
(define-screen home
  (msg close)
  (init
    ((unit) (back)))
  (update msg model
    (model ()))
  (view model
    (section
      "Home"
      ((button "Close" close)))))

(define-app demo
  (frontend
    (screens home)))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "screen effects must be a list; wrap back in an extra pair of parentheses") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsButtonWithoutUpdate(t *testing.T) {
	src := `
(define-screen home
  (msg open-profile)
  (view
    (section
      "Home"
      ((button "Open" open-profile)))))

(define-app demo
  (frontend
    (screens home)))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "button requires a screen that defines update") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsButtonWithUnknownMessage(t *testing.T) {
	src := `
(define-screen home
  (msg save-clicked)
  (init
    ((unit) ()))
  (update msg model
    (model ()))
  (view model
    (section
      "Home"
      ((button "Open" missing-message)))))

(define-app demo
  (frontend
    (screens home)))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "button references unknown message") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsTransitionModelWithExtraRecordWrapper(t *testing.T) {
	src := `
(define-record home-model
  (title string))

(define-screen home
  (msg save-clicked)
  (init
    (((home-model "Hello")) ()))
  (update msg model
    (model ()))
  (view model
    (section
      "Home"
      ((button "Save" save-clicked)))))

(define-app demo
  (frontend
    (screens home)))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "extra pair of parentheses") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsLegacyAuthBuiltins(t *testing.T) {
	for _, legacy := range []string{"user-authenticated", "user-id", "user-email", "user-role", "anonymous"} {
		src := fmt.Sprintf(`
(define-entity todo
    (fields
      ((title string)))
    (authorize
      ((read %s))))

(define-app demo
  (auth ())
  (entities todo))
`, legacy)

		_, err := Parse(src)
		if err == nil {
			t.Fatalf("expected parse error for %s", legacy)
		}
		if !strings.Contains(err.Error(), "unknown variable") && !strings.Contains(err.Error(), "unknown identifier") {
			t.Fatalf("unexpected error for %s: %v", legacy, err)
		}
	}
}

func TestParseSupportsTypedCurrentUserHelpers(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)))
    (authorize
      ((read
         (or (has-role? current-user "admin")
             (match current-user
               ((authenticated id email role) true)
               ((anonymous) false)))))))

(define-app demo
  (auth ())
  (entities todo))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
}

func TestParseRejectsHasRoleWithNonStringRole(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)))
    (authorize
      ((read (has-role? current-user 1)))))

(define-app demo
  (auth ())
  (entities todo))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "has-role? expects string as second argument") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsCurrentUserMatchWithTooFewAuthenticatedBindings(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)))
    (authorize
      ((read
         (match current-user
           ((authenticated id) true)
           ((anonymous) false))))))

(define-app demo
  (auth ())
  (entities todo))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), `match pattern "authenticated" expects 3 values: id email role`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsCurrentUserMatchWithTooManyAuthenticatedBindings(t *testing.T) {
	src := `
(define-entity todo
    (fields
      ((title string)))
    (authorize
      ((read
         (match current-user
           ((authenticated id email role extra) true)
           ((anonymous) false))))))

(define-app demo
  (auth ())
  (entities todo))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), `match pattern "authenticated" expects 3 values: id email role`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsMaybeMatchWithNamedExpectedPayload(t *testing.T) {
	src := `
(define-entity profile
    (fields
      ((handle string optional)))
    (validate
      (match handle
        ((just) true)
        ((nothing) false))))

(define-app demo
  (entities profile))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), `match pattern "just" expects 1 values: value`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsResultMatchWithNamedExpectedPayload(t *testing.T) {
	src := `
(define-record load-model
  (value (result string int)))

(define-entity item
    (fields
      ((amount int)))
    (validate
      (let ((state (load-model (value (ok amount)))))
        (match (get state value)
          ((ok) true)
          ((err error) false)))))

(define-app demo
  (entities item))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), `match pattern "ok" expects 1 values: value`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSupportsDefineTypeForScreenState(t *testing.T) {
	src := `
(define-entity item
    (fields
      ((title string))))

(define-query (all-items)
  (from item))

(define-type timeline-state
  (loading)
  (loaded (items (list item)))
  (failed (message string)))

(define-record timeline-model
  (state timeline-state))

(define-screen timeline
  (msg
    (loaded-items items)
    (failed-load message))
  (init
    ((timeline-model (state (loading)))
     ((command (all-items) loaded-items failed-load))))
  (update msg model
    (match msg
      ((loaded-items items)
       ((assoc model (state (loaded items))) ()))
      ((failed-load message)
       ((assoc model (state (failed message))) ()))))
  (view model
    (section
      (title "Timeline")
      (text "Timeline"))))

(define-app demo
  (backend
    (entities item)
    (queries all-items))
  (frontend
    (screens timeline)))
`

	app, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(app.Types) != 1 {
		t.Fatalf("expected one type, got %+v", app.Types)
	}
	if got := app.Types[0].Variants[1].Fields[0].Name; got != "items" {
		t.Fatalf("unexpected variant payload field name: %q", got)
	}
}

func TestParseRejectsDefineTypeConstructorArityMismatch(t *testing.T) {
	src := `
(define-type load-state
  (loaded (count int))
  (failed (message string)))

(define-record model
  (state load-state))

(define-screen home
  (init
    ((model (state (loaded))) ()))
  (view model
    (section (title "Home") (text "Home"))))

(define-app demo
  (frontend
    (screens home)))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "loaded expects 1 arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsDefineScreenWrapper(t *testing.T) {
	src := `
(define-screen home
  (screen
    (view
      (section
        (title "Home")
        (text "Home")))))

(define-app demo
  (frontend
    (screens home)))
`

	_, err := Parse(src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), `unknown screen clause "screen"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSupportsTextItemWithStringAppendAndDatetimeConversion(t *testing.T) {
	src := `
(define-entity post
    (fields ((body string))))

(define-record posts-model
  (posts (list post)))

(define-screen posts
  (init ((posts-model ()) ()))
  (view model
    (section
      ((list posts post ((title body) (open post-detail)))))))

(define-screen (post-detail post)
  (view
    (section
      ((text (string-append "Created at " (datetime->string post.created-at)))))))

(define-app demo
  (backend
    (entities post))
  (frontend
    (screens posts post-detail)))
`

	if _, err := Parse(src); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
}
