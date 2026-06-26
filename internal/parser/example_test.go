package parser

import (
	"testing"
)

// These tests parse short snippets representative of the migrated examples.
// Once we have actual .mar files for the new syntax, we can replace these
// with file-based tests.

func TestParsePersonalTodoBackend(t *testing.T) {
	src := `module PersonalTodo exposing (..)

import Entity exposing (..)
import Default exposing (..)
import Service exposing (Service)
import Effect exposing (Effect)
import Db


type TodoId = TodoId Int

type alias Todo =
    { id : TodoId
    , title : String
    , done : Bool
    , user : UserId
    }

type alias TodoInput =
    { title : String
    , done : Bool
    }


todos : Entity Todo
todos =
    entity Todo


list : Service () (List Todo)
list = Service.declare GET "/todos"
`
	mod, err := Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(mod.Decls) < 5 {
		t.Fatalf("expected several decls, got %d", len(mod.Decls))
	}
}

func TestParseTimelineScreen(t *testing.T) {
	src := `module Screens.Timeline exposing (..)

import Service
import Effect exposing (Effect)


type alias Model =
    { posts : List Post
    , body : String
    }


type Msg
    = TimelineLoaded (Result Error (List Post))
    | BodyChanged String
    | SubmitClicked


update msg model =
    case msg of
        BodyChanged value ->
            ( model, Cmd.none )

        SubmitClicked ->
            ( model, Cmd.none )

        TimelineLoaded result ->
            case result of
                Ok posts -> ( model, Cmd.none )
                Err _ -> ( model, Cmd.none )
`
	mod, err := Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(mod.Decls) < 3 {
		t.Fatalf("expected several decls, got %d", len(mod.Decls))
	}
}

func TestParseLetWithBindArrow(t *testing.T) {
	src := `module Foo exposing (..)

deletePost id user =
    let
        post <- findOne id
    in
    if post.author == user.id then
        delete id
    else
        fail
`
	mod, err := Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(mod.Decls) != 1 {
		t.Fatalf("want 1 decl, got %d", len(mod.Decls))
	}
}

func TestParsePipeline(t *testing.T) {
	src := `module Foo exposing (..)

services =
    Service.implement Shared.createPost createPost
        |> Auth.authorize loadPost ownsPost
        |> Auth.requireRole Admin
`
	_, err := Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
}
