// Package parity holds what the cross-runtime checks share: the Mar
// programs both runtimes run, and the fake browser the web half runs in.
//
// It lives in its own package for a reason that is the whole point of the
// exercise: "the same program behaves the same way on the web and on iOS" is
// only a claim about the same program if both tests literally read the same
// bytes. Two copies of a source string drift, and the drift lands in the one
// place where it is invisible: the tests keep passing while they quietly stop
// comparing anything.
//
// internal/jsserve drives it through runtime.js in a fake browser;
// internal/iosbundle drives it through the Swift runtime on a simulator.
package parity

// Source is a three-page app: a counter you can bump, a second page to
// navigate to, and a third that is the same page declared with Page.sheet.
//
// The label in each view makes a screen self-identifying, so a wrong-page
// render fails loudly instead of looking like a wrong counter: the two
// failures have very different causes and an assertion that cannot tell them
// apart is not worth much.
const NavSource = `module Main exposing (main)


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


-- A presented route that NESTS: /a/nested sits under /a in the url, the way
-- a real one does (/classes/3/attendance under /classes/3). Opened cold it
-- has a parent to be presented over; pageS, top-level, has none and falls
-- back to the app's first page. Two fixtures for the two branches.
pageN : Page
pageN =
    Page.sheet
        (Page.create
            { path = "/a/nested"
            , title = "N"
            , init = init
            , update = update
            , view = viewFor "N"
            , subscriptions = always Sub.none
            }
        )


main : Cmd ()
main =
    App.frontend [ pageA, pageB, pageS, pageN ]
`

// UISource puts one of every widget on a single screen, with a counter so that
// pressing the first button changes what is written and the "after one tap"
// half of the comparison means something.
//
// A fixture rather than the 33 examples on purpose: this one runs in a test,
// every time, with no dev server and no network. The examples sweep covers
// breadth once; this covers the primitives forever.
const UISource = `module Main exposing (main)


import UI exposing
    ( navigationStack, navigationTitle, form, section, header, footer
    , vstack, hstack, text, button, textField, errorText, list
    , toggle, spacer, empty, disabled, width, chars
    )


type alias Model =
    { n : Int
    , draft : String
    , on : Bool
    }


type Msg
    = Bump
    | DraftChanged String
    | Toggled Bool


init : (Model, Cmd Msg)
init = ( { n = 0, draft = "typed", on = True }, Cmd.none )


update : Msg -> Model -> (Model, Cmd Msg)
update msg model =
    case msg of
        Bump ->
            ( { model | n = model.n + 1 }, Cmd.none )

        DraftChanged s ->
            ( { model | draft = s }, Cmd.none )

        Toggled b ->
            ( { model | on = b }, Cmd.none )


view : Model -> View Msg
view model =
    navigationStack [ navigationTitle "Surface" ]
        [ form
            [ section [ header "Counter", footer "the model, on screen" ]
                [ text [] ("n=" ++ String.fromInt model.n)
                , hstack [] [ text [] "left", spacer, text [] "right" ]
                , button [] Bump "bump"
                , button [ disabled True ] Bump "inert"
                ]
            , section [ header "Input" ]
                [ textField
                    [ width (chars 20) ]
                    "Label" model.draft DraftChanged
                , toggle [] "Switch" model.on Toggled
                , errorText "a problem"
                , empty
                ]
            , section [ header "List" ]
                [ list [] (List.map row [ 1, 2, 3 ]) ]
            ]
        ]


row : Int -> View Msg
row i =
    vstack [] [ text [] ("row " ++ String.fromInt i) ]


page : Page
page =
    Page.create
        { path = "/"
        , title = "Surface"
        , init = init
        , update = update
        , view = view
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ page ]
`

// CanvasSource is the same idea for the draw list: every Shape builder, both
// colour builders, nested groups, and each transform, all derived from the
// counter so a tap moves the whole scene.
//
// Canvas screens were the blind spot the text comparison could not see: they
// put no words on screen, so both platforms reported an empty string and
// "matched" by having nothing to say. What they DO produce is a list of
// shapes, built by the same Mar code from the same model, so that list, not
// the pixels, is the thing to compare.
const CanvasSource = `module Main exposing (main)


import Canvas exposing (canvas, rect, circle, triangle, group, rgb, rgba)
import UI exposing (navigationStack, navigationTitle, vstack, button, text)


type alias Model =
    { n : Int }


type Msg
    = Bump


init : (Model, Cmd Msg)
init = ( { n = 0 }, Cmd.none )


update : Msg -> Model -> (Model, Cmd Msg)
update _ model = ( { model | n = model.n + 1 }, Cmd.none )


scene : Int -> List Shape
scene n =
    [ rect n (n * 2) 40 30 (rgb 10 20 30)
    , circle (100 - n) 50 (8 + n) (rgba 200 100 50 128)
    , triangle 0 0 (10 + n) 0 5 (20 + n) (rgb 0 0 0)
    , Canvas.text (5 + n) 60 9 Canvas.Left (rgb 255 255 255) ("n=" ++ String.fromInt n)
    , group [ Canvas.Translate n 7, Canvas.Rotate (Math.degrees (n * 15)), Canvas.Scale 2 2 ]
        [ rect 1 2 3 4 (rgb 1 2 3)
        , group [ Canvas.Blend Canvas.Add, Canvas.Alpha 128 ]
            [ circle 0 0 (n + 1) (rgb 9 9 9) ]
        ]
    ]


view : Model -> View Msg
view model =
    navigationStack [ navigationTitle "Shapes" ]
        [ vstack []
            [ text [] ("n=" ++ String.fromInt model.n)
            , button [] Bump "bump"
            , canvas Pixelated [] (scene model.n)
            ]
        ]


page : Page
page =
    Page.create
        { path = "/"
        , title = "Shapes"
        , init = init
        , update = update
        , view = view
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ page ]
`
