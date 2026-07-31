package jsserve

import (
	"errors"
	"testing"

	"mar/internal/parity"
)

// Page models die on forward navigation (ADR 0009). A shared store is the one
// thing that does not, and these tests drive the real runtime to prove it —
// same fake browser as nav_lifecycle_test.go, same reason: the only honest
// assertion is what the screen SHOWS, because that is the only thing a stale
// or frozen model can lie about.
//
// Two pages and two counters, deliberately side by side in one line of text:
//
//	A local=<page model>  shared=<store>
//
// so a single reading catches both failure modes at once. A store that reset
// would show shared=0 after a navigation; a page whose builder was applied only
// at mount would show a shared count that stopped moving.
const sharedStoreSrc = `module Main exposing (main)


import UI exposing (vstack, text, button, navigationStack, navigationTitle)


type alias Store =
    { hits : Int }


type StoreMsg
    = Hit


storeInit : (Store, Cmd StoreMsg)
storeInit = ( { hits = 0 }, Cmd.none )


storeUpdate : StoreMsg -> Store -> (Store, Cmd StoreMsg)
storeUpdate msg model =
    case msg of
        Hit -> ( { model | hits = model.hits + 1 }, Cmd.none )


store : App.Shared Store StoreMsg
store =
    App.shared
        { init = storeInit
        , update = storeUpdate
        , subscriptions = \_ -> Sub.none
        }


type alias Model =
    { local : Int }


type Msg
    = Local
    | Share


init : (Model, Cmd Msg)
init = ( { local = 0 }, Cmd.none )


update : Msg -> Model -> (Model, Cmd Msg)
update msg model =
    case msg of
        Local ->
            ( { local = model.local + 1 }, Cmd.none )

        Share ->
            ( model, Cmd.toShared store Hit )


viewA : Store -> Model -> View Msg
viewA g model =
    navigationStack [ navigationTitle "A" ]
        [ vstack []
            [ text [] ("A local=" ++ String.fromInt model.local ++ " shared=" ++ String.fromInt g.hits)
            , button [] Local "local"
            , button [] Share "share"
            ]
        ]


pageA : Page
pageA =
    Page.withShared store
        (\g ->
            Page.create
                { path = "/a"
                , title = "A"
                , init = init
                , update = update
                , view = viewA g
                , subscriptions = always Sub.none
                }
        )


viewB : Store -> Model -> View Msg
viewB g model =
    navigationStack [ navigationTitle "B" ]
        [ vstack []
            [ text [] ("B local=" ++ String.fromInt model.local ++ " shared=" ++ String.fromInt g.hits)
            , button [] Share "share"
            ]
        ]


pageB : Page
pageB =
    Page.withShared store
        (\g ->
            Page.create
                { path = "/b"
                , title = "B"
                , init = init
                , update = update
                , view = viewB g
                , subscriptions = always Sub.none
                }
        )


main : Cmd ()
main =
    App.frontend [ pageA, pageB ]
`

// The header every driver needs: load the runtime, boot the program, and give
// the test a way to press a button by its label.
const sharedDriverPrelude = `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
window.marRun(program);

function press(label, node) {
  node = node || marRoot;
  if (node.textContent === label && node._on && node._on.click) { node.click(); return true; }
  for (const c of (node.children || [])) if (press(label, c)) return true;
  return false;
}
const shown = () => (screenText().match(/[AB] local=\d+ shared=\d+/) || ['?'])[0];
`

// The whole point of the feature, in one trip: bump BOTH counters, walk away,
// walk back. The page counter starts over (that is ADR 0009 working); the
// shared one keeps counting (that is this feature working).
//
// The second half matters as much as the first. A runtime that simply never
// reset anything would pass a test that only checked the store, so the page
// counter is read at every step as the control.
func TestSharedStoreSurvivesNavigationAndPageModelDoesNot(t *testing.T) {
	got := runSharedDriver(t, sharedDriverPrelude+`
const seen = [];
const note = (label) => seen.push(label + '[' + shown() + ']');

note('mount');
press('local'); press('local');
press('share'); press('share'); press('share');
note('bumped');

globalThis.__marNav.push('/b');
note('on-b');

globalThis.__marNav.push('/a');
note('back-on-a');

process.stdout.write(seen.join(' '));
`)
	want := "mount[A local=0 shared=0] bumped[A local=2 shared=3] on-b[B local=0 shared=3] back-on-a[A local=0 shared=3]"
	if got != want {
		t.Fatalf("shared state did not survive navigation the way page state does not.\n got: %s\nwant: %s\n\n"+
			"on-b[... shared=0]       → the store was rebuilt per page instead of shared between them.\n"+
			"back-on-a[A local=2 ...] → the PAGE model survived a forward push, which is the ADR-0009 bug.",
			got, want)
	}
}

// A shared change has to repaint the page that is up, even though that page's
// own model never moved and its update never ran. That is what makes
// Page.withShared a live read rather than a snapshot taken at mount: the
// builder is re-applied with the new model before the view is rebuilt.
//
// Pressing `share` from page B is the sharper version of the same check — B's
// button sends to the store, and B's text is only correct if B was rebuilt.
func TestSharedChangeRepaintsTheCurrentPage(t *testing.T) {
	got := runSharedDriver(t, sharedDriverPrelude+`
globalThis.__marNav.push('/b');
const before = shown();
press('share');
const afterOne = shown();
press('share');
process.stdout.write([before, afterOne, shown()].join(' | '));
`)
	want := "B local=0 shared=0 | B local=0 shared=1 | B local=0 shared=2"
	if got != want {
		t.Fatalf("a shared change did not repaint the page on screen.\n got: %s\nwant: %s\n\n"+
			"a frozen `shared=0` means the withShared builder ran once at mount and was never re-applied.",
			got, want)
	}
}

func runSharedDriver(t *testing.T, driver string) string {
	t.Helper()
	program, err := parity.Compile(sharedStoreSrc, SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}
	out, err := parity.RunWeb(runtimeJS, program, driver)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node not installed")
	}
	if err != nil {
		t.Fatalf("node run: %v", err)
	}
	return out
}
