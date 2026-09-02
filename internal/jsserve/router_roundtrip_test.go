package jsserve

import (
	"errors"
	"strings"
	"testing"

	"mar/internal/parity"
)

// The other half of the round trip, in the browser.
//
// `linkTo` writes a path parameter into a URL and the ROUTER reads it back.
// The server has a census for both halves; the browser had a test for neither
// until now, and this is the half that decides which page a URL lands on.
// Found cold by `make guards-js`: `matchPathPattern` and `decodePathSegment`
// were entered by no test at all.
//
// The trip here is the real one: the app builds the link with `linkTo`, the
// driver navigates to exactly that URL, and the page reads its parameter back.
// A value that survives that is a link a user can click.
const routerRoundTripSrc = `module Main exposing (main)


import UI exposing (vstack, text)


note : Path { slug : String }
note =
    "/notes/{slug:String}"


type Msg
    = Noop


init : { slug : String } -> (String, Cmd Msg)
init params =
    (params.slug, Cmd.none)


update : { slug : String } -> Msg -> String -> (String, Cmd Msg)
update _ _ model =
    (model, Cmd.none)


view : { slug : String } -> String -> View Msg
view _ model =
    vstack [] [ text [] ("slug=[" ++ model ++ "]") ]


notePage : Page
notePage =
    Page.dynamic
        { path = note
        , title = "Note"
        , init = init
        , update = update
        , view = view
        , subscriptions = \_ _ -> Sub.none
        }


home : Page
home =
    Page.create
        { path = "/"
        , title = "Home"
        , init = (0, Cmd.none)
        , update = \_ m -> (m, Cmd.none)
        , view = \_ -> vstack [] [ text [] "home" ]
        , subscriptions = always Sub.none
        }


linkFor : String -> String
linkFor s =
    linkTo note { slug = s }


main : Cmd ()
main =
    App.frontend [ home, notePage ]
`

func TestTheRouterReadsBackWhatLinkToWrote(t *testing.T) {
	// Each slug is a value a real app would put in a URL, and each one broke
	// something at some point: the slash split the segment in two, the space
	// and the accent needed encoding, and the unreserved set must survive
	// untouched or the URL stops being the one the pattern matches.
	for _, slug := range []string{"ok", "a b", "a/b", "a?b", "a#b", "a&b", "ç", "a-b._~c"} {
		t.Run(slug, func(t *testing.T) {
			driver := `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));

// The app builds the link, the way a view would.
const url = window.__marEvalValue(program, 'Main.linkFor').replace(/^"|"$/g, '');
// Navigate there BEFORE mounting: this is a cold load of a shared link.
history.replaceState({ marDepth: 0, prevTitle: '' }, '', url);
window.marRun(program);
process.stdout.write(url + ' -> ' + screenText());
`
			// `linkFor` takes an argument, so the driver cannot just evaluate
			// it: bake the slug into a nullary value instead.
			src := strings.Replace(routerRoundTripSrc,
				"linkFor : String -> String\nlinkFor s =\n    linkTo note { slug = s }",
				"linkFor : String\nlinkFor =\n    linkTo note { slug = "+quoteMar(slug)+" }", 1)
			prog, err := parity.Compile(src, SerializeModule)
			if err != nil {
				t.Fatalf("compiling: %v", err)
			}
			out, err := parity.RunWeb(runtimeJS, prog, driver)
			if errors.Is(err, parity.ErrNoNode) {
				t.Skip("node not installed")
			}
			if err != nil {
				t.Fatalf("node run: %v", err)
			}
			want := "slug=[" + slug + "]"
			if !strings.Contains(out, want) {
				t.Errorf("the link the app built did not route back to its own page.\n  got:  %s\n  want: it to contain %q", out, want)
			}
		})
	}
}

// quoteMar renders a Go string as a Mar string literal.
func quoteMar(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
