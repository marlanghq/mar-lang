package jsserve

import (
	"errors"
	"strings"
	"testing"

	"mar/internal/parity"
)

// `UI.onMove` hands the program a pair of indices: where the row was, where it
// went. A wrong pair does not fail — it delivers a different reorder than the
// one the person performed, and the list quietly ends up in an order nobody
// asked for.
//
// The keyboard path (space grabs, arrows move, space drops, escape cancels)
// had no test, and could not have had one: it hangs off a delegated listener,
// and the fake browser answered every delegated click and keydown with
// silence. Getting here took four fixes to the fake, each one a thing a real
// browser does and it did not — the last being that `insertBefore` on a node
// that already has a parent MOVES it rather than cloning it.
//
// The assertion is the MESSAGE, not the DOM: the DOM moving is the runtime
// talking to itself, and what the program acts on is the pair of indices.
const listReorderSrc = `module Main exposing (main)


import UI exposing (vstack, text, keyedList, keyed, onMove)


type Msg
    = Moved Int Int


type alias Model =
    { items : List String
    , last : String
    }


init : (Model, Cmd Msg)
init =
    ({ items = [ "a", "b", "c" ], last = "none" }, Cmd.none)


-- The model records the pair and does NOT reorder: the test is about what the
-- runtime reports, and a model that also moved things would hide a wrong pair
-- behind a list that happens to look right.
update : Msg -> Model -> (Model, Cmd Msg)
update msg model =
    case msg of
        Moved from to ->
            ({ model | last = String.fromInt from ++ "->" ++ String.fromInt to }, Cmd.none)


row : String -> KeyedView Msg
row s =
    keyed s (text [] s)


view : Model -> View Msg
view model =
    vstack []
        [ text [] ("last=" ++ model.last)
        , keyedList [ onMove True Moved ] (List.map row model.items)
        ]


page : Page
page =
    Page.create
        { path = "/"
        , title = "List"
        , init = init
        , update = update
        , view = view
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ page ]
`

func TestKeyboardReorderReportsTheIndicesItMoved(t *testing.T) {
	cases := []struct{ name, keys, want string }{
		{"one row down", `' ', 'ArrowDown', ' '`, "last=0->1"},
		{"two rows down", `' ', 'ArrowDown', 'ArrowDown', ' '`, "last=0->2"},
		{"grabbed and dropped in place", `' ', ' '`, "last=none"},
		{"escape cancels the move", `' ', 'ArrowDown', 'Escape'`, "last=none"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			program, err := parity.Compile(listReorderSrc, SerializeModule)
			if err != nil {
				t.Fatalf("compiling the fixture: %v", err)
			}
			out, err := parity.RunWeb(runtimeJS, program, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);

// The drag handles the runtime renders in edit mode; the first one is the
// first row, which is the row every case below grabs.
const handles = [];
(function walk(n) {
  if (n.classList && n.classList.contains('mar-drag-handle')) handles.push(n);
  for (const c of (n.children || [])) walk(c);
})(marRoot);
if (handles.length !== 3) { process.stdout.write('expected 3 drag handles, got ' + handles.length); }
else {
  for (const k of [`+tc.keys+`]) {
    handles[0].dispatch('keydown', {
      key: k, target: handles[0], preventDefault() {}, stopPropagation() {},
    });
  }
  process.stdout.write((screenText().match(/last=[^ ]*/) || ['last=?'])[0]);
}
`)
			if errors.Is(err, parity.ErrNoNode) {
				t.Skip("node not installed")
			}
			if err != nil {
				t.Fatalf("node run: %v", err)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("got %q, want %q", out, tc.want)
			}
		})
	}
}
