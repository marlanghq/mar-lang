package jsserve

import (
	"errors"
	"strings"
	"testing"

	"mar/internal/parity"
)

// Clicking a link inside the app has to stay inside the app. `matchesAnyPage`
// is what decides that: it answers "is this href one of my pages?", and a no
// lets the browser follow the href, which is a full page reload — every bit of
// client state gone, on a link the app itself rendered.
//
// It was cold. The cold-load path (open the URL directly) and the click path
// are two different entry points into the same matcher, and only the first had
// a test — the shape this work keeps finding, one function apart.
//
// The awkward slug is the point: the link is built by `linkTo`, which
// percent-encodes, and recognised by the matcher, which decodes. If those two
// ever disagree, this is where a user feels it.
const linkClickSrc = `module Main exposing (main)


import UI exposing (vstack, text, navigationLink)


note : Path { slug : String }
note =
    "/notes/{slug:String}"


type Msg
    = Noop


home : Page
home =
    Page.create
        { path = "/"
        , title = "Home"
        , init = (0, Cmd.none)
        , update = \_ m -> (m, Cmd.none)
        , view = \_ ->
            vstack []
                [ navigationLink [] note { slug = "a b/c" } (text [] "go")
                ]
        , subscriptions = always Sub.none
        }


init : { slug : String } -> (String, Cmd Msg)
init params =
    (params.slug, Cmd.none)


notePage : Page
notePage =
    Page.dynamic
        { path = note
        , title = "Note"
        , init = init
        , update = \_ _ m -> (m, Cmd.none)
        , view = \_ m -> vstack [] [ text [] ("slug=[" ++ m ++ "]") ]
        , subscriptions = \_ _ -> Sub.none
        }


main : Cmd ()
main =
    App.frontend [ home, notePage ]
`

func TestClickingAnInAppLinkStaysInTheApp(t *testing.T) {
	program, err := parity.Compile(linkClickSrc, SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}

	out, err := parity.RunWeb(runtimeJS, program, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);

// Find the anchor the app rendered and click it the way a person would.
function findLink(node) {
  if (node.tagName === 'A' && node.getAttribute && node.getAttribute('href')) return node;
  for (const c of (node.children || [])) { const f = findLink(c); if (f) return f; }
  return null;
}
const a = findLink(marRoot);
const href = a ? a.getAttribute('href') : '(no link rendered)';
let defaultPrevented = false;
if (a) {
  a.dispatch('click', {
    target: a,
    preventDefault: () => { defaultPrevented = true; },
    stopPropagation: () => {},
  });
}
process.stdout.write(href + ' | prevented=' + defaultPrevented + ' | ' + screenText());
`)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node not installed")
	}
	if err != nil {
		t.Fatalf("node run: %v", err)
	}

	// The link the app built, encoded.
	if !strings.Contains(out, "/notes/a%20b%2Fc") {
		t.Errorf("the rendered href is not the encoded link: %s", out)
	}
	// The click was intercepted rather than left to the browser.
	if !strings.Contains(out, "prevented=true") {
		t.Errorf("the click fell through to a full page reload: %s", out)
	}
	// And it landed on the page, with the parameter decoded back.
	if !strings.Contains(out, "slug=[a b/c]") {
		t.Errorf("the click did not route to the note page: %s", out)
	}
}
