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

// NavSource is a three-page app: a counter you can bump, a second page to
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

// BoxSource is a canvas that reports its whole box -- size AND the four
// safe-area insets -- and prints what it last heard. The printed line is the
// only way to see what watchSize delivered, and it counts the messages, so a
// duplicate or a missing one is visible as a number rather than inferred.
//
// The insets are the point: they are what an app has to keep controls out of,
// and the case that breaks them (a 180-degree turn in landscape, which moves
// the notch without changing the size) is invisible in any test that only
// watches the size. See docs/adrs/0034-a-mirror-reports-the-whole-box.md.
const BoxSource = `module Main exposing (main)


import Canvas exposing (canvas, rect, rgb, watchSize)
import UI exposing (navigationStack, navigationTitle, vstack, text)


type alias Box =
    { w : Int, h : Int, top : Int, right : Int, bottom : Int, left : Int }


type alias Model =
    { box : Box, seen : Int }


type Msg
    = Sized Box


init : (Model, Cmd Msg)
init =
    ( { box = { w = 0, h = 0, top = 0, right = 0, bottom = 0, left = 0 }, seen = 0 }
    , Cmd.none
    )


update : Msg -> Model -> (Model, Cmd Msg)
update msg model =
    case msg of
        Sized box ->
            ( { model | box = box, seen = model.seen + 1 }, Cmd.none )


line : Model -> String
line model =
    "box=" ++ String.fromInt model.box.w ++ "x" ++ String.fromInt model.box.h
        ++ " insets=" ++ String.fromInt model.box.top
        ++ "," ++ String.fromInt model.box.right
        ++ "," ++ String.fromInt model.box.bottom
        ++ "," ++ String.fromInt model.box.left
        ++ " seen=" ++ String.fromInt model.seen


view : Model -> View Msg
view model =
    navigationStack [ navigationTitle "Box" ]
        [ vstack []
            [ text [] (line model)
            , canvas Pixelated [ watchSize Sized ] [ rect 0 0 4 4 (rgb 1 2 3) ]
            ]
        ]


page : Page
page =
    Page.create
        { path = "/"
        , title = "Box"
        , init = init
        , update = update
        , view = view
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ page ]
`

// SubsSource is an app whose pages watch the device, to check that a page
// gets the device's state each time it is shown: when it is pushed, when Back
// returns to it, when it replaces another, and when it starts watching while
// a shared store already does.
//
// Each count is the number of Device messages that page received. A page
// shown once, with nothing changing, must read 1: 0 is a page left without
// the state, 2 is a state handed over twice.
//
// N is the landing page and watches nothing, so the first page that watches
// is shown by a navigation. The store's own count is not shown: what is under
// test is what each page receives. (On the web the store watches from the
// first render, so A and B join its running source; on the phone each page
// runtime starts its own. The counts are the same either way.)
const SubsSource = `module Main exposing (main)


import UI exposing (vstack, text, button, navigationStack, navigationTitle)


type StoreMsg
    = StoreGot Device


store : App.Shared Int StoreMsg
store =
    App.shared
        { init = ( 0, Cmd.none )
        , update = \_ n -> ( n + 1, Cmd.none )
        , subscriptions = \_ -> Device.watch StoreGot
        }


type alias Model =
    { n : Int, armed : Bool }


type Msg
    = Arm
    | Got Device


init : (Model, Cmd Msg)
init = ( { n = 0, armed = False }, Cmd.none )


update : Msg -> Model -> (Model, Cmd Msg)
update msg model =
    case msg of
        Arm -> ( { model | armed = True }, Cmd.none )
        Got _ -> ( { model | n = model.n + 1 }, Cmd.none )


viewFor : String -> Model -> View Msg
viewFor label model =
    navigationStack [ navigationTitle label ]
        [ vstack []
            [ text [] (label ++ "=" ++ String.fromInt model.n)
            , button [] Arm "arm"
            ]
        ]


page : String -> String -> (Model -> Sub Msg) -> Page
page path label subs =
    Page.create
        { path = path
        , title = label
        , init = init
        , update = update
        , view = viewFor label
        , subscriptions = subs
        }


watchingAlways : Model -> Sub Msg
watchingAlways _ =
    Device.watch Got


-- L starts watching only when "arm" is pressed, while the store it reads
-- has been watching all along: it joins a source that is already running.
watchingOnceArmed : Model -> Sub Msg
watchingOnceArmed model =
    if model.armed then Device.watch Got else Sub.none


-- K arms itself in init: it starts watching in the same turn as the store's
-- source starts, and is handed the state once.
armAtOnce : (Model, Cmd Msg)
armAtOnce =
    ( { n = 0, armed = False }, Cmd.perform (\_ -> Arm) Time.now )


pageN : Page
pageN =
    page "/" "N" (always Sub.none)


pageA : Page
pageA =
    page "/a" "A" watchingAlways


pageB : Page
pageB =
    page "/b" "B" watchingAlways


pageP : Page
pageP =
    Page.withShared store (\_ -> page "/p" "P" (always Sub.none))


pageL : Page
pageL =
    Page.withShared store (\_ -> page "/l" "L" watchingOnceArmed)


pageK : Page
pageK =
    Page.withShared store
        (\_ ->
            Page.create
                { path = "/k"
                , title = "K"
                , init = armAtOnce
                , update = update
                , view = viewFor "K"
                , subscriptions = watchingOnceArmed
                }
        )


main : Cmd ()
main =
    App.frontend [ pageN, pageA, pageB, pageP, pageL, pageK ]
`

// RouteSource checks that a value an update asks for goes back to the page
// that asked, though the same update also navigated away. A's "go" both
// pushes B and performs a task whose result is 5; the 5 belongs to A, now
// covered. B's update counts any message it is handed, so a result that went
// to the page on screen shows on B. On the 5, A asks to push B again: a
// covered page does not navigate, so Back from B still lands on A.
//
// T's "twice" pushes B and then C in one update. T is on screen when its
// effects start, so both run, though the first push has covered it: covered
// is decided when the effects start, not as each one runs.
//
// R's "load" opens the sheet S and asks for a value. The value reaches R
// while S covers it, and turns R's timer on. The timer waits for R to be on
// screen again: it starts when S closes, never behind it.
const RouteSource = `module Main exposing (main)


import UI exposing (vstack, text, button, navigationStack, navigationTitle)


type Msg
    = Go
    | Stamp Int


type alias Model =
    { n : Int }


pageA : Page
pageA =
    Page.create
        { path = "/"
        , title = "A"
        , init = ( { n = 0 }, Cmd.none )
        , update =
            \msg model ->
                case msg of
                    Go -> ( model, Cmd.batch [ Nav.push "/b", Cmd.perform Stamp (Task.succeed 5) ] )
                    Stamp k -> ( { n = k }, Nav.push "/b" )
        , view =
            \model ->
                navigationStack [ navigationTitle "A" ]
                    [ vstack [] [ text [] ("A=" ++ String.fromInt model.n), button [] Go "go" ] ]
        , subscriptions = always Sub.none
        }


pageB : Page
pageB =
    Page.create
        { path = "/b"
        , title = "B"
        , init = ( { n = 0 }, Cmd.none )
        , update = \_ model -> ( { n = model.n + 1 }, Cmd.none )
        , view =
            \model ->
                navigationStack [ navigationTitle "B" ]
                    [ vstack [] [ text [] ("B=" ++ String.fromInt model.n) ] ]
        , subscriptions = always Sub.none
        }


pageC : Page
pageC =
    Page.create
        { path = "/c"
        , title = "C"
        , init = ( { n = 0 }, Cmd.none )
        , update = \_ model -> ( { n = model.n + 1 }, Cmd.none )
        , view =
            \model ->
                navigationStack [ navigationTitle "C" ]
                    [ vstack [] [ text [] ("C=" ++ String.fromInt model.n) ] ]
        , subscriptions = always Sub.none
        }


type TMsg
    = Twice


pageT : Page
pageT =
    Page.create
        { path = "/t"
        , title = "T"
        , init = ( { n = 0 }, Cmd.none )
        , update = \_ model -> ( model, Cmd.batch [ Nav.push "/b", Nav.push "/c" ] )
        , view =
            \model ->
                navigationStack [ navigationTitle "T" ]
                    [ vstack [] [ text [] ("T=" ++ String.fromInt model.n), button [] Twice "twice" ] ]
        , subscriptions = always Sub.none
        }


type RMsg
    = Load
    | Ready Int
    | Tick Time


pageR : Page
pageR =
    Page.create
        { path = "/r"
        , title = "R"
        , init = ( { n = 0, ready = False }, Cmd.none )
        , update =
            \msg model ->
                case msg of
                    Load -> ( model, Cmd.batch [ Nav.push "/s", Cmd.perform Ready (Task.succeed 1) ] )
                    Ready _ -> ( { model | ready = True }, Cmd.none )
                    Tick _ -> ( { model | n = 1 }, Cmd.none )
        , view =
            \model ->
                navigationStack [ navigationTitle "R" ]
                    [ vstack [] [ text [] ("R=" ++ String.fromInt model.n), button [] Load "load" ] ]
        , subscriptions =
            \model ->
                if model.ready then
                    Time.every (Time.millis 100) Tick

                else
                    Sub.none
        }


pageS : Page
pageS =
    Page.sheet
        (Page.create
            { path = "/s"
            , title = "S"
            , init = ( { n = 0 }, Cmd.none )
            , update = \_ model -> ( { n = model.n + 1 }, Cmd.none )
            , view =
                \model ->
                    navigationStack [ navigationTitle "S" ]
                        [ vstack [] [ text [] ("S=" ++ String.fromInt model.n) ] ]
            , subscriptions = always Sub.none
            }
        )


main : Cmd ()
main =
    App.frontend [ pageA, pageB, pageC, pageT, pageR, pageS ]
`

// SheetsSource is a sheet route that opens another sheet route. A is the
// page underneath; S and N are sheets, and each has its own Done.
//
// The second sheet starts fresh, and closing it finds the first as it was
// left: the first sheet's model belongs to its entry, like a covered page's.
// How the two are drawn is the platform's call (the web swaps the sheet's
// content, iOS presents the second over the first); which model each screen
// has is not.
const SheetsSource = `module Main exposing (main)


import UI exposing (vstack, text, button, navigationStack, navigationTitle)


type alias Model =
    { n : Int }


type Msg
    = Bump
    | More
    | Done


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        Bump -> ( { model | n = model.n + 1 }, Cmd.none )
        More -> ( model, Nav.push "/n" )
        Done -> ( model, Nav.dismiss )


viewFor : String -> Model -> View Msg
viewFor label model =
    navigationStack [ navigationTitle label ]
        [ vstack []
            [ text [] (label ++ "=" ++ String.fromInt model.n)
            , button [] Bump "bump"
            , button [] More "more"
            , button [] Done "done"
            ]
        ]


pageA : Page
pageA =
    Page.create
        { path = "/"
        , title = "A"
        , init = ( { n = 0 }, Cmd.none )
        , update = update
        , view = viewFor "A"
        , subscriptions = always Sub.none
        }


pageS : Page
pageS =
    Page.sheet
        (Page.create
            { path = "/s"
            , title = "S"
            , init = ( { n = 0 }, Cmd.none )
            , update = update
            , view = viewFor "S"
            , subscriptions = always Sub.none
            }
        )


pageN : Page
pageN =
    Page.sheet
        (Page.create
            { path = "/n"
            , title = "N"
            , init = ( { n = 0 }, Cmd.none )
            , update = update
            , view = viewFor "N"
            , subscriptions = always Sub.none
            }
        )


main : Cmd ()
main =
    App.frontend [ pageA, pageS, pageN ]
`

// SharedWrapSource nests Page.sheet and Page.withShared every way a program
// can write them, and has a page that reads two stores.
//
// P is Page.sheet around a Page.withShared, L is a Page.sheet inside one,
// and K reads two stores with the Page.sheet between them: all three are
// presented. T reads two stores and is pushed. Each shows a number built
// from the stores it reads (A's 7 and B's 9), so a page drawn against the
// wrong store, or not drawn at all, shows a different number. R reads a
// store twice and shows how many device messages each store was handed.
const SharedWrapSource = `module Main exposing (main)


import UI exposing (vstack, text, button, navigationStack, navigationTitle)


type alias Store =
    { hits : Int, seen : Int }


type StoreMsg
    = Hit
    | Seen Device


storeUpdate : StoreMsg -> Store -> ( Store, Cmd StoreMsg )
storeUpdate msg store =
    case msg of
        Hit -> ( { store | hits = store.hits + 1 }, Cmd.none )
        Seen _ -> ( { store | seen = store.seen + 1 }, Cmd.none )


-- Both stores hand in the very same tagger. Each store's device messages
-- still have to reach that store, and only once.
storeSubs : Store -> Sub StoreMsg
storeSubs _ =
    Device.watch Seen


storeA : App.Shared Store StoreMsg
storeA =
    App.shared
        { init = ( { hits = 7, seen = 0 }, Cmd.none )
        , update = storeUpdate
        , subscriptions = storeSubs
        }


storeB : App.Shared Store StoreMsg
storeB =
    App.shared
        { init = ( { hits = 9, seen = 0 }, Cmd.none )
        , update = storeUpdate
        , subscriptions = storeSubs
        }


type alias Model =
    { n : Int }


type Msg
    = HitA
    | HitB


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        HitA -> ( model, Cmd.toShared storeA Hit )
        HitB -> ( model, Cmd.toShared storeB Hit )


screen : String -> Int -> Model -> View Msg
screen label shown _ =
    navigationStack [ navigationTitle label ]
        [ vstack []
            [ text [] (label ++ "=" ++ String.fromInt shown)
            , button [] HitA "hit-a"
            , button [] HitB "hit-b"
            ]
        ]


plain : String -> String -> Int -> Page
plain path label shown =
    Page.create
        { path = path
        , title = label
        , init = ( { n = 0 }, Cmd.none )
        , update = update
        , view = screen label shown
        , subscriptions = always Sub.none
        }


pageA : Page
pageA =
    plain "/" "A" 0


pageP : Page
pageP =
    Page.sheet (Page.withShared storeA (\a -> plain "/p" "P" a.hits))


pageL : Page
pageL =
    Page.withShared storeA (\a -> Page.sheet (plain "/l" "L" a.hits))


pageK : Page
pageK =
    Page.withShared storeA
        (\a -> Page.sheet (Page.withShared storeB (\b -> plain "/k" "K" (a.hits * 100 + b.hits))))


pageT : Page
pageT =
    Page.withShared storeA
        (\a -> Page.withShared storeB (\b -> plain "/t" "T" (a.hits * 100 + b.hits)))


-- R reads A, then B, then A again: how many device messages each store got.
pageR : Page
pageR =
    Page.withShared storeA
        (\a -> Page.withShared storeB (\b -> Page.withShared storeA (\_ -> plain "/r" "R" (a.seen * 100 + b.seen))))


main : Cmd ()
main =
    App.frontend [ pageA, pageP, pageL, pageK, pageT, pageR ]
`
