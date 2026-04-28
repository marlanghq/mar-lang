package runtime

import (
	"strings"
	"testing"
)

func TestViewRenderText(t *testing.T) {
	src := `module M exposing (..)
result = View.render (View.text [] "hello")
`
	if got := runModule(t, src, "result"); !strings.Contains(got, "hello") {
		t.Fatalf("got %s", got)
	}
}

func TestViewRenderSection(t *testing.T) {
	src := `module M exposing (..)
result = View.render (View.section [] [View.title [] "Hi", View.text [] "world"])
`
	got := runModule(t, src, "result")
	if !strings.Contains(got, "<h1>") || !strings.Contains(got, "Hi") {
		t.Fatalf("got %s", got)
	}
	if !strings.Contains(got, "world") {
		t.Fatalf("got %s", got)
	}
}

func TestViewRenderList(t *testing.T) {
	src := `module M exposing (..)
result = View.render (View.list [] [View.text [] "a", View.text [] "b"])
`
	got := runModule(t, src, "result")
	if !strings.Contains(got, "<ul>") || !strings.Contains(got, "<li>") {
		t.Fatalf("got %s", got)
	}
}

func TestViewEscapesHTML(t *testing.T) {
	src := `module M exposing (..)
result = View.render (View.text [] "<script>")
`
	got := runModule(t, src, "result")
	if strings.Contains(got, "<script>") {
		t.Fatalf("not escaped: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatalf("expected escape: %s", got)
	}
}

func TestEntityCreate(t *testing.T) {
	src := `module M exposing (..)
ent = Entity.create "users"
        |> Entity.field "id" Entity.int
        |> Entity.field "email" Entity.text
        |> Entity.primaryKey "id"
        |> Entity.notNull "email"
        |> Entity.unique ["email"]
`
	got := runModule(t, src, "ent")
	if !strings.Contains(got, "users") {
		t.Fatalf("expected entity:users in display, got %s", got)
	}
}

func TestEffectForEach(t *testing.T) {
	src := `module M exposing (..)
xs = [1, 2, 3]
go = Effect.forEach (\_ -> Effect.succeed ()) xs
`
	got := runModule(t, src, "go")
	// go is an Effect, just confirm the value exists.
	if !strings.Contains(got, "effect") {
		t.Fatalf("got %s", got)
	}
}
