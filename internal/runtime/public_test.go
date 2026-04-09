package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPublicServesEmbeddedFiles(t *testing.T) {
	requireSQLite3(t)

	publicDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(publicDir, "index.html"), []byte("<html><body>hello</body></html>"), 0o644); err != nil {
		t.Fatalf("write index.html failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicDir, "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("write app.js failed: %v", err)
	}

	app := mustParseApp(t, `
app FrontApi
database "./front.db"

public {
  dir "./frontend/dist"
  mount "/"
  spa_fallback "index.html"
}

entity Todo {
  title: String
}
`)
	app.Database = filepath.Join(t.TempDir(), "public-files.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetPublicFiles(os.DirFS(publicDir))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("expected index html body, got %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/app.js", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /app.js, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("expected JS body, got %q", rec.Body.String())
	}
}

func TestPublicSPAFallbackForRoutesWithoutFileExtension(t *testing.T) {
	requireSQLite3(t)

	publicDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(publicDir, "index.html"), []byte("<html><body>spa</body></html>"), 0o644); err != nil {
		t.Fatalf("write index.html failed: %v", err)
	}

	app := mustParseApp(t, `
app FrontApi
database "./front.db"

public {
  dir "./frontend/dist"
  mount "/"
  spa_fallback "index.html"
}

entity Todo {
  title: String
}
`)
	app.Database = filepath.Join(t.TempDir(), "public-spa.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetPublicFiles(os.DirFS(publicDir))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders/123", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for SPA fallback, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "spa") {
		t.Fatalf("expected fallback html, got %q", rec.Body.String())
	}
}

func TestPublicSPAFallbackDoesNotMaskMissingAssets(t *testing.T) {
	requireSQLite3(t)

	publicDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(publicDir, "index.html"), []byte("<html><body>spa</body></html>"), 0o644); err != nil {
		t.Fatalf("write index.html failed: %v", err)
	}

	app := mustParseApp(t, `
app FrontApi
database "./front.db"

public {
  dir "./frontend/dist"
  mount "/"
  spa_fallback "index.html"
}

entity Todo {
  title: String
}
`)
	app.Database = filepath.Join(t.TempDir(), "public-asset-404.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetPublicFiles(os.DirFS(publicDir))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing.js", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing asset, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Route not found") {
		t.Fatalf("expected route-not-found error body, got %q", rec.Body.String())
	}
}

func TestRootRedirectsToAppUIWhenNoPublicAppIsConfigured(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
app TodoApi
database "./todo.db"

entity Todo {
  title: String
}
`)
	app.Database = filepath.Join(t.TempDir(), "root-redirect.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetAppUIFiles(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>admin</body></html>")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 for / without public app, got %d body=%s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); location != "/_mar" {
		t.Fatalf("expected redirect to /_mar, got %q", location)
	}
}

func TestAppUIServedUnderMarPrefix(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
app FrontApi
database "./front.db"

public {
  dir "./frontend/dist"
  mount "/"
  spa_fallback "index.html"
}

entity Todo {
  title: String
}
`)
	app.Database = filepath.Join(t.TempDir(), "app-ui-prefix.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetPublicFiles(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>public</body></html>")},
	})
	r.SetAppUIFiles(fstest.MapFS{
		"index.html":   &fstest.MapFile{Data: []byte("<html><body>admin</body></html>")},
		"dist/app.js":  &fstest.MapFile{Data: []byte("console.log('admin')")},
		"dist/app.css": &fstest.MapFile{Data: []byte("body{}")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_mar", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_mar, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin") {
		t.Fatalf("expected app UI html body, got %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/_mar/dist/app.js", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_mar/dist/app.js, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin") {
		t.Fatalf("expected app UI js body, got %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "public") {
		t.Fatalf("expected public html body, got %q", rec.Body.String())
	}
}

func TestSchemaEndpointStillWorksUnderMarPrefix(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
app FrontApi
database "./front.db"

entity Todo {
  title: String
}
`)
	app.Database = filepath.Join(t.TempDir(), "mar-schema-endpoint.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	r.SetAppUIFiles(fstest.MapFS{
		"index.html":   &fstest.MapFile{Data: []byte("<html><body>app-ui</body></html>")},
		"schema":       &fstest.MapFile{Data: []byte("not-json")},
		"dist/app.js":  &fstest.MapFile{Data: []byte("console.log('app-ui')")},
		"dist/app.css": &fstest.MapFile{Data: []byte("body{}")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_mar/schema", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_mar/schema, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"appName":"FrontApi"`) {
		t.Fatalf("expected schema JSON body, got %q", rec.Body.String())
	}
}

func TestSchemaEndpointIncludesEnumValues(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
app EnumApi
database "./enum.db"

type MembershipStatus {
  Active
  Inactive
}

entity GymMember {
  status: MembershipStatus
}

type alias CreateGymMemberInput =
  { status : MembershipStatus
  }

action createGymMember {
  input: CreateGymMemberInput

  create GymMember {
    status: input.status
  }
}
`)
	app.Database = filepath.Join(t.TempDir(), "mar-schema-enum-values.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_mar/schema", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_mar/schema, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode schema response: %v", err)
	}

	entities, ok := payload["entities"].([]any)
	if !ok {
		t.Fatalf("expected entities array, got %#v", payload["entities"])
	}

	var foundEntityField bool
	for _, rawEntity := range entities {
		entityMap, ok := rawEntity.(map[string]any)
		if !ok || entityMap["name"] != "GymMember" {
			continue
		}
		fields, ok := entityMap["fields"].([]any)
		if !ok {
			t.Fatalf("expected fields array, got %#v", entityMap["fields"])
		}
		for _, rawField := range fields {
			fieldMap, ok := rawField.(map[string]any)
			if !ok || fieldMap["name"] != "status" {
				continue
			}
			enumValues, ok := fieldMap["enumValues"].([]any)
			if !ok {
				t.Fatalf("expected enumValues for GymMember.status, got %#v", fieldMap["enumValues"])
			}
			if len(enumValues) != 2 || enumValues[0] != "Active" || enumValues[1] != "Inactive" {
				t.Fatalf("unexpected enumValues for GymMember.status: %#v", enumValues)
			}
			foundEntityField = true
		}
	}
	if !foundEntityField {
		t.Fatal("expected GymMember.status to appear in schema payload")
	}

	inputAliases, ok := payload["inputAliases"].([]any)
	if !ok {
		t.Fatalf("expected inputAliases array, got %#v", payload["inputAliases"])
	}

	var foundAliasField bool
	for _, rawAlias := range inputAliases {
		aliasMap, ok := rawAlias.(map[string]any)
		if !ok || aliasMap["name"] != "CreateGymMemberInput" {
			continue
		}
		fields, ok := aliasMap["fields"].([]any)
		if !ok {
			t.Fatalf("expected alias fields array, got %#v", aliasMap["fields"])
		}
		for _, rawField := range fields {
			fieldMap, ok := rawField.(map[string]any)
			if !ok || fieldMap["name"] != "status" {
				continue
			}
			enumValues, ok := fieldMap["enumValues"].([]any)
			if !ok {
				t.Fatalf("expected enumValues for CreateGymMemberInput.status, got %#v", fieldMap["enumValues"])
			}
			if len(enumValues) != 2 || enumValues[0] != "Active" || enumValues[1] != "Inactive" {
				t.Fatalf("unexpected enumValues for CreateGymMemberInput.status: %#v", enumValues)
			}
			foundAliasField = true
		}
	}
	if !foundAliasField {
		t.Fatal("expected CreateGymMemberInput.status to appear in schema payload")
	}
}

func TestSchemaEndpointIncludesActionInputRelationEntity(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
app Blog
database "./blog.db"

entity Post {
  title: String
}

type alias PublishPostInput =
  { post : ref Post
  }

action publishPost {
  input: PublishPostInput

  loadedPost = load Post {
    id: input.post
  }

  update Post {
    id: loadedPost.id
    title: loadedPost.title
  }
}
`)
	app.Database = filepath.Join(t.TempDir(), "mar-schema-action-input-rel.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_mar/schema", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_mar/schema, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode schema response: %v", err)
	}

	inputAliases, ok := payload["inputAliases"].([]any)
	if !ok {
		t.Fatalf("expected inputAliases array, got %#v", payload["inputAliases"])
	}

	for _, rawAlias := range inputAliases {
		aliasMap, ok := rawAlias.(map[string]any)
		if !ok || aliasMap["name"] != "PublishPostInput" {
			continue
		}
		fields, ok := aliasMap["fields"].([]any)
		if !ok {
			t.Fatalf("expected alias fields array, got %#v", aliasMap["fields"])
		}
		for _, rawField := range fields {
			fieldMap, ok := rawField.(map[string]any)
			if !ok || fieldMap["name"] != "post" {
				continue
			}
			if got := fieldMap["type"]; got != "Int" {
				t.Fatalf("expected PublishPostInput.post type Int, got %#v", got)
			}
			if got := fieldMap["relationEntity"]; got != "Post" {
				t.Fatalf("expected PublishPostInput.post relationEntity Post, got %#v", got)
			}
			return
		}
	}

	t.Fatal("expected PublishPostInput.post to appear in schema payload")
}

func TestSchemaEndpointIncludesFrontend(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
app Blog
database "./blog.db"

entity Post {
  title: String
}

frontend {
  screen Home {
    title "Blog"

    section "Browse" {
      list Post {
        title title
        destination PostDetail
      }
    }
  }

  screen PostDetail for Post {
    title "Post"

    section {
      field title
    }
  }
}
`)
	app.Database = filepath.Join(t.TempDir(), "mar-schema-frontend.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_mar/schema", nil)
	r.handleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /_mar/schema, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode schema response: %v", err)
	}

	frontend, ok := payload["frontend"].(map[string]any)
	if !ok {
		t.Fatalf("expected frontend object, got %#v", payload["frontend"])
	}
	screens, ok := frontend["screens"].([]any)
	if !ok || len(screens) != 2 {
		t.Fatalf("expected 2 frontend screens, got %#v", frontend["screens"])
	}
	first, ok := screens[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first screen object, got %#v", screens[0])
	}
	if first["name"] != "Home" || first["title"] != "Blog" {
		t.Fatalf("unexpected first screen: %#v", first)
	}
}
