package scaffold

import "fmt"

// fullstackFiles returns the file set for `mar init` when the
// operator picks the fullstack kind. Layout mirrors the canonical
// multi-page examples (notes-fullstack, team-notes):
//
//	Main.mar              — wires App.fullstack
//	Shared.mar            — types + Service contracts
//	Backend.mar           — Entity + Service implementations
//	Frontend/Routes.mar   — centralized typed Paths
//	Frontend/Home.mar     — Home page (list + add form)
//	Frontend/About.mar    — About page (static info)
//
// Splitting pages into one-file-per-module from day one matches how
// real Mar projects grow.
func fullstackFiles(name string) map[string]string {
	files := sharedFiles(name)
	files["mar.json"] = fmt.Sprintf(`{
  "name": "%s"
}
`, name)
	files["Main.mar"] = `module Main exposing (main)


-- The entry point ties Backend services and the Frontend pages
-- together via App.fullstack.


import Backend
import Frontend.Home
import Frontend.About


main : Effect ()
main =
    App.fullstack
        { services = Backend.services
        , pages    =
            [ Frontend.Home.page
            , Frontend.About.page
            ]
        -- Usually api is empty, unless you need webhooks or custom REST routes.
        , api      = []
        }
`
	files["Shared.mar"] = `module Shared exposing (..)


-- Types and Service contracts shared by Frontend (the caller) and
-- Backend (the implementation). Keeping them here means the
-- frontend bundle ships only what it needs. No Entity or Repo
-- code reaches the browser.


type alias Entry =
    { id   : Int
    , name : String
    }


type alias NewEntry =
    { name : String
    }


-- Two services. Backend.mar implements them, the Frontend pages
-- call them. Putting the declarations here means both sides see
-- the same signature, so the typechecker catches any mismatch.
listEntries : Service () (List Entry)
listEntries = Service.declare


addEntry : Service NewEntry Entry
addEntry = Service.declare
`
	files["Backend.mar"] = `module Backend exposing (services)


import Shared


-- Database schema. Schema migrations are derived from this Entity
-- definition and auto-applied at startup.
entries : Entity Shared.Entry
entries =
    Entity.define
        { name = "entries"
        , columns =
            { id   = Entity.serial
            , name = Entity.text Entity.notNull
            }
        , uniques = []
        }


-- Service handlers.
listEntriesImpl : () -> Effect (List Shared.Entry)
listEntriesImpl _ =
    Repo.all entries


addEntryImpl : Shared.NewEntry -> Effect Shared.Entry
addEntryImpl input =
    Repo.create entries input


services =
    [ Service.implement Shared.listEntries listEntriesImpl
    , Service.implement Shared.addEntry   addEntryImpl
    ]
`
	files["Frontend/Routes.mar"] = `module Frontend.Routes exposing (home, about)


-- Centralized URL surface. Renaming a path here surfaces every
-- navigationLink call site as a compile-time error. Page.create
-- takes the raw String separately (the typechecker needs a String
-- literal for its route-pattern parser).


home : Path {}
home = "/"


about : Path {}
about = "/about"
`
	files["Frontend/Home.mar"] = fmt.Sprintf(`module Frontend.Home exposing (page)


import Shared
import Frontend.Routes
import UI exposing
    ( navigationStack, navigationTitle
    , list, section, header
    , hstack
    , text, textField, button, navigationLink, errorText
    , submit
    )


-- Home page: a list of entries with an inline add form. The Entries
-- ladder makes the fetch states explicit so the view never lies
-- about progress.


type Entries
    = Loading
    | Loaded (List Shared.Entry)
    | Failed String


type alias Model =
    { entries : Entries
    , draft   : String
    }


type Msg
    = EntriesFetched (Result String (List Shared.Entry))
    | DraftChanged String
    | AddClicked
    | EntryAdded (Result String Shared.Entry)


fetchEntries : Effect Msg
fetchEntries =
    Service.call Shared.listEntries () EntriesFetched


init : (Model, Effect Msg)
init =
    ( { entries = Loading, draft = "" }, fetchEntries )


update : Msg -> Model -> (Model, Effect Msg)
update msg model =
    case msg of
        EntriesFetched (Ok loaded) ->
            ( { model | entries = Loaded loaded }, Effect.none )

        EntriesFetched (Err why) ->
            ( { model | entries = Failed why }, Effect.none )

        DraftChanged value ->
            ( { model | draft = value }, Effect.none )

        AddClicked ->
            -- Stale-while-revalidate: keep the current list visible
            -- while the create + re-fetch round-trips.
            ( { model | draft = "" }
            , Service.call Shared.addEntry { name = model.draft } EntryAdded
            )

        EntryAdded (Ok _) ->
            ( model, fetchEntries )

        EntryAdded (Err _) ->
            ( model, Effect.none )


view : Model -> View Msg
view model =
    navigationStack [ navigationTitle "%s" ]
        [ list []
            [ section []
                [ hstack []
                    [ textField [ submit AddClicked ]
                        "New entry" model.draft DraftChanged
                    , button [] AddClicked "Add"
                    ]
                ]
            , entriesSection model.entries
            , section []
                [ navigationLink [] Frontend.Routes.about {}
                    (text "About this app")
                ]
            ]
        ]


entriesSection : Entries -> View Msg
entriesSection state =
    case state of
        Loading ->
            section [] [ text "Loading…" ]

        Failed why ->
            section [] [ errorText ("Couldn't load entries: " ++ why) ]

        Loaded items ->
            section [ header (String.fromInt (List.length items) ++ " entries") ]
                (List.map renderEntry (List.reverse items))


renderEntry : Shared.Entry -> View Msg
renderEntry entry =
    text entry.name


page : Page
page =
    Page.create
        { path = "/"
        , title = "%s"
        , init = init
        , update = update
        , view = view
        }
`, name, name)
	files["Frontend/About.mar"] = fmt.Sprintf(`module Frontend.About exposing (page)


import UI exposing
    ( navigationStack, navigationTitle
    , list, section, header
    , vstack
    , title, subtitle, text
    )


-- About page: static info reachable from Home via navigationLink.


type alias Model = ()


type Msg
    = NoOp


init : (Model, Effect Msg)
init = ((), Effect.none)


update : Msg -> Model -> (Model, Effect Msg)
update _ _ = ((), Effect.none)


view : Model -> View Msg
view _ =
    navigationStack [ navigationTitle "About" ]
        [ list []
            [ section []
                [ vstack []
                    [ title "%s"
                    , subtitle "A fullstack mar app."
                    ]
                ]
            , section [ header "Next steps" ]
                [ text "Edit Frontend/Home.mar to change the home page."
                , text "Edit Backend.mar to add services or entities."
                , text "Edit Shared.mar to add new types or services."
                ]
            ]
        ]


page : Page
page =
    Page.create
        { path = "/about"
        , title = "About"
        , init = init
        , update = update
        , view = view
        }
`, name)
	return files
}
