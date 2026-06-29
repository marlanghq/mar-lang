package scaffold

import "fmt"

// frontendFiles returns the file set for `mar init` when the
// operator picks the frontend-only kind. Layout:
//
//	Main.mar     — wires App.frontend
//	Routes.mar   — centralized typed Paths
//	Home.mar     — Home page (counter)
//	About.mar    — About page (static info)
//
// No Frontend/ subdir since there's no Backend to separate from —
// everything in a frontend-only project is frontend.
func frontendFiles(name string) map[string]string {
	files := sharedFiles(name)
	files["mar.json"] = fmt.Sprintf(`{
  "name": "%s"
}
`, name)
	files["Main.mar"] = `module Main exposing (main)


-- The entry point wires both pages via App.frontend. Add another
-- page by creating its module and listing it here.


import Home
import About


main : Effect ()
main =
    App.frontend [ Home.page, About.page ]
`
	files["Routes.mar"] = `module Routes exposing (home, about)


-- Centralized URL surface. Renaming a path here surfaces every
-- navigationLink call site as a compile-time error. Page.create
-- takes the raw String separately (the typechecker needs a String
-- literal for its route-pattern parser).


home : Path {}
home = "/"


about : Path {}
about = "/about"
`
	files["Home.mar"] = fmt.Sprintf(`module Home exposing (page)


import Routes
import UI exposing
    ( navigationStack, navigationTitle
    , list, section, header
    , vstack
    , text, button, navigationLink
    )


-- Home page: a counter with a link to About. Demonstrates MVU
-- (Model + Msg + init/update/view) and inter-page navigation.


type alias Model = Int


type Msg
    = Increment
    | Decrement


init : (Model, Effect Msg)
init = (0, Effect.none)


update : Msg -> Model -> (Model, Effect Msg)
update msg model =
    case msg of
        Increment -> (model + 1, Effect.none)
        Decrement -> (model - 1, Effect.none)


view : Model -> View Msg
view model =
    navigationStack [ navigationTitle "%s" ]
        [ list []
            [ section [ header "Counter" ]
                [ vstack []
                    [ button [] Increment "+"
                    , text (String.fromInt model)
                    , button [] Decrement "-"
                    ]
                ]
            , section []
                [ navigationLink [] Routes.about {}
                    (text "About this app")
                ]
            ]
        ]


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
	files["About.mar"] = fmt.Sprintf(`module About exposing (page)


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
                    , subtitle "A frontend-only mar app."
                    ]
                ]
            , section [ header "Next steps" ]
                [ text "Edit Home.mar to change the home page."
                , text "Add new pages by creating a module + listing it in Main.mar."
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
