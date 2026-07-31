// Package navfixture holds the one Mar program the navigation-lifecycle
// tests run, on every platform that has a runtime.
//
// It lives in its own package for a reason that is the whole point of the
// exercise: "the same program behaves the same way on the web and on iOS" is
// only a claim about the same program if both tests literally read the same
// bytes. Two copies of a source string drift, and the drift lands in the one
// place where it is invisible — the tests keep passing while they quietly stop
// comparing anything.
//
// internal/jsserve drives it through runtime.js in a fake browser;
// internal/iosbundle drives it through the Swift runtime on a simulator.
package navfixture

// Source is a three-page app: a counter you can bump, a second page to
// navigate to, and a third that is the same page declared with Page.sheet.
//
// The label in each view makes a screen self-identifying, so a wrong-page
// render fails loudly instead of looking like a wrong counter — the two
// failures have very different causes and an assertion that cannot tell them
// apart is not worth much.
const Source = `module Main exposing (main)


import UI exposing (vstack, text, button, navigationStack, navigationTitle)


type alias Model =
    { n : Int }


type Msg
    = Bump


init : (Model, Cmd Msg)
init = ( { n = 0 }, Cmd.none )


update : Msg -> Model -> (Model, Cmd Msg)
update _ model = ( { model | n = model.n + 1 }, Cmd.none )


viewFor : String -> Model -> View Msg
viewFor label model =
    navigationStack [ navigationTitle label ]
        [ vstack []
            [ text [] (label ++ "=" ++ String.fromInt model.n)
            , button [] Bump "bump"
            ]
        ]


pageA : Page
pageA =
    Page.create
        { path = "/a"
        , title = "A"
        , init = init
        , update = update
        , view = viewFor "A"
        , subscriptions = always Sub.none
        }


pageB : Page
pageB =
    Page.create
        { path = "/b"
        , title = "B"
        , init = init
        , update = update
        , view = viewFor "B"
        , subscriptions = always Sub.none
        }


-- Same page in every respect except how it is shown: Page.sheet asks the
-- runtime to lay it OVER the screen it was reached from instead of replacing
-- it. Nothing else about the page changes, which is the claim under test.
pageS : Page
pageS =
    Page.sheet
        (Page.create
            { path = "/s"
            , title = "S"
            , init = init
            , update = update
            , view = viewFor "S"
            , subscriptions = always Sub.none
            }
        )


main : Cmd ()
main =
    App.frontend [ pageA, pageB, pageS ]
`
