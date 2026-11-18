package jsserve

import (
	"errors"
	"testing"

	"mar/internal/parity"
)

// A message an effect produces goes to whoever issued the effect: the page
// whose init or update returned it (on screen or covered by another), or the
// shared store whose update did. It used to go to whatever page was on screen
// when it landed. A result that arrived after a navigation reached the next
// page, whose update could take it as its own, and the page that asked waited
// forever; a store's own Service.call never reached the store.
//
// A page's model belongs to its history entry, not its route: A, B, then A
// again is two models, and an entry that leaves the stack is gone (browser
// Forward onto it starts the page over, as a push does).
//
// A covered page's model is kept up to date, but it does not take the person
// off the page they are on (no navigation) or play sound behind it, and a
// failure in its update shows when it is on screen again.

func runRouting(t *testing.T, src, driver string) string {
	t.Helper()
	program, err := parity.Compile(src, SerializeModule)
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

// routingPrelude holds every fetch until the driver releases it with a body,
// so a result can be made to land after a navigation.
const routingPrelude = `
const fs = require('fs');
const pending = [];
globalThis.fetch = () => new Promise((res) => {
  pending.push((body) => res({ ok: true, status: 200, headers: { get: () => null }, text: () => Promise.resolve(String(body)) }));
});
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const flush = async () => { for (let i = 0; i < 4; i++) await new Promise((r) => setTimeout(r, 0)); };
const warns = [];
console.warn = (...a) => warns.push(a.join(' '));
const shown = () => (screenText().match(/[ABCS] [a-z0-9= ]+/) || ['?'])[0].trim();
const seen = [];
const note = (l) => seen.push(l + '[' + shown() + ']');
const release = (i, body) => { const f = pending[i]; pending[i] = null; f(body); };
const done = () => process.stdout.write(seen.join(' '));
`

func routingScript(steps string) string {
	return routingPrelude + `
window.marRun(program);
(async () => {
` + steps + `
done();
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`
}

// A loads on init; B is a page of its own with a counter whose update takes
// any message, which is how a misrouted result used to show.
const routingTwoPages = `module Main exposing (main)


import UI exposing (vstack, text, button)


loadIt : Service () Int
loadIt =
    Service.declare GET "/api/load"


type Msg
    = Got (Result Service.Error Int)


pageA : Page
pageA =
    Page.create
        { path = "/a", title = "A"
        , init = ( "loading", Service.call loadIt () Got )
        , update = \msg _ ->
            case msg of
                Got (Ok n) -> ( "loaded " ++ String.fromInt n, Cmd.none )
                Got (Err _) -> ( "failed", Cmd.none )
        , view = \m -> vstack [] [ text [] ("A " ++ m) ]
        , subscriptions = always Sub.none
        }


type BMsg
    = Tap


pageB : Page
pageB =
    Page.create
        { path = "/b", title = "B"
        , init = ( 0, Cmd.none )
        , update = \_ m -> ( m + 1, Cmd.none )
        , view = \m -> vstack [] [ text [] ("B " ++ String.fromInt m) ]
        , subscriptions = always Sub.none
        }


pageS : Page
pageS =
    Page.sheet
        (Page.create
            { path = "/s", title = "S"
            , init = ( 0, Cmd.none )
            , update = \_ m -> ( m + 1, Cmd.none )
            , view = \m -> vstack [] [ text [] ("S " ++ String.fromInt m) ]
            , subscriptions = always Sub.none
            })


main : Cmd ()
main =
    App.frontend [ pageA, pageB, pageS ]
`

func TestAResultReachesThePageThatAskedForIt(t *testing.T) {
	got := runRouting(t, routingTwoPages, routingScript(`
await flush(); note('mount');
globalThis.__marNav.push('/b'); await flush(); note('on-b');
release(0, 7); await flush(); note('released');
history.back(); await flush(); note('back-a');
`))
	want := "mount[A loading] on-b[B 0] released[B 0] back-a[A loaded 7]"
	if got != want {
		t.Fatalf("a result did not reach the page that asked for it.\n got: %s\nwant: %s\n\n"+
			"released[B 1] → the result went to the page on screen, whose update took it.\n"+
			"back-a[A loading] → the page that asked never got it.", got, want)
	}
}

// The page under a presented route gets its result but does not change the
// screen while it is covered: it shows the result when the route closes.
func TestAPageUnderASheetGetsItsResult(t *testing.T) {
	got := runRouting(t, routingTwoPages, routingScript(`
await flush(); note('mount');
globalThis.__marNav.push('/s'); await flush();
seen.push('sheet[root=' + shown() + ' sheet=' + (sheetText().match(/S \d+/) || ['-'])[0] + ']');
release(0, 7); await flush();
seen.push('released[root=' + shown() + ' sheet=' + (sheetText().match(/S \d+/) || ['-'])[0] + ']');
history.back(); await flush(); note('dismissed');
`))
	want := "mount[A loading] sheet[root=A loading sheet=S 0] released[root=A loading sheet=S 0] dismissed[A loaded 7]"
	if got != want {
		t.Fatalf("the page under a sheet did not get its result, or the sheet got it.\n got: %s\nwant: %s", got, want)
	}
}

func TestASharedStoreGetsItsOwnResult(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text)


loadIt : Service () Int
loadIt =
    Service.declare GET "/api/load"


type StoreMsg
    = Loaded (Result Service.Error Int)


store : App.Shared String StoreMsg
store =
    App.shared
        { init = ( "loading", Service.call loadIt () Loaded )
        , update = \msg _ ->
            case msg of
                Loaded (Ok n) -> ( "loaded " ++ String.fromInt n, Cmd.none )
                Loaded (Err _) -> ( "failed", Cmd.none )
        , subscriptions = \_ -> Sub.none
        }


type Msg
    = Tap


pageA : Page
pageA =
    Page.withShared store
        (\g ->
            Page.create
                { path = "/a", title = "A"
                , init = ( 0, Cmd.none )
                , update = \_ m -> ( m + 1, Cmd.none )
                , view = \m -> vstack [] [ text [] ("A store " ++ g ++ " page " ++ String.fromInt m) ]
                , subscriptions = always Sub.none
                }
        )


main : Cmd ()
main =
    App.frontend [ pageA ]
`
	got := runRouting(t, src, routingScript(`
await flush(); note('mount');
release(0, 7); await flush(); note('released');
`))
	want := "mount[A store loading page 0] released[A store loaded 7 page 0]"
	if got != want {
		t.Fatalf("a shared store's own result did not reach the store.\n got: %s\nwant: %s", got, want)
	}
}

// One update both navigates and asks for a value. The value is computed at
// once, after the push: it belongs to the page that asked, now covered.
func TestAResultOfAnUpdateThatNavigatedGoesBackToIt(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text, button)


type Msg
    = Go
    | Stamp Int


pageA : Page
pageA =
    Page.create
        { path = "/a", title = "A"
        , init = ( "fresh", Cmd.none )
        , update = \msg m ->
            case msg of
                Go -> ( "going", Cmd.batch [ Nav.push "/b", Cmd.perform Stamp (Task.succeed 5) ] )
                Stamp n -> ( "stamped " ++ String.fromInt n, Cmd.none )
        , view = \m -> vstack [] [ text [] ("A " ++ m), button [] Go "bump" ]
        , subscriptions = always Sub.none
        }


type BMsg
    = Other


pageB : Page
pageB =
    Page.create
        { path = "/b", title = "B"
        , init = ( 0, Cmd.none )
        , update = \_ m -> ( m + 1, Cmd.none )
        , view = \m -> vstack [] [ text [] ("B " ++ String.fromInt m) ]
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ pageA, pageB ]
`
	got := runRouting(t, src, routingScript(`
await flush(); note('mount');
bump(); await flush(); note('after-go');
history.back(); await flush(); note('back');
`))
	want := "mount[A fresh bump] after-go[B 0] back[A stamped 5 bump]"
	if got != want {
		t.Fatalf("a value asked for by an update that navigated did not go back to it.\n got: %s\nwant: %s", got, want)
	}
}

// A loads on init and may chain a second load or navigate on its result; B
// loads on init too.
const routingChain = `module Main exposing (main)


import UI exposing (vstack, text, button)


loadIt : Service () Int
loadIt =
    Service.declare GET "/api/load"


type Msg
    = Got (Result Service.Error Int)
    | Bump


type alias Model =
    { got : Int, last : Int, taps : Int }


pageA : Page
pageA =
    Page.create
        { path = "/a", title = "A"
        , init = ( { got = 0, last = 0, taps = 0 }, Service.call loadIt () Got )
        , update = \msg m ->
            case msg of
                Got (Ok n) ->
                    if n == 1 then ( { m | got = m.got + 1, last = n }, Service.call loadIt () Got )
                    else if n == 9 then ( { m | got = m.got + 1, last = n }, Nav.push "/c" )
                    else ( { m | got = m.got + 1, last = n }, Cmd.none )
                Got (Err _) -> ( m, Cmd.none )
                Bump -> ( { m | taps = m.taps + 1 }, Cmd.none )
        , view = \m -> vstack [] [ text [] ("A got=" ++ String.fromInt m.got ++ " last=" ++ String.fromInt m.last ++ " taps=" ++ String.fromInt m.taps), button [] Bump "bump" ]
        , subscriptions = always Sub.none
        }


type BMsg
    = BGot (Result Service.Error Int)


pageB : Page
pageB =
    Page.create
        { path = "/b", title = "B"
        , init = ( "loading", Service.call loadIt () BGot )
        , update = \msg _ ->
            case msg of
                BGot (Ok n) -> ( "loaded " ++ String.fromInt n, Cmd.none )
                BGot (Err _) -> ( "failed", Cmd.none )
        , view = \m -> vstack [] [ text [] ("B " ++ m) ]
        , subscriptions = always Sub.none
        }


pageC : Page
pageC =
    Page.create
        { path = "/c", title = "C"
        , init = ( 0, Cmd.none )
        , update = \_ m -> ( m, Cmd.none )
        , view = \_ -> vstack [] [ text [] "C here" ]
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ pageA, pageB, pageC ]
`

// B asks, Back pops B, and B is pushed again: the first B's result belongs
// to a page that is gone, and the new B gets only its own.
func TestAResultForAPageThatLeftIsDropped(t *testing.T) {
	got := runRouting(t, routingChain, routingScript(`
await flush(); release(0, 2); await flush(); note('a');
globalThis.__marNav.push('/b'); await flush(); note('b1');
history.back(); await flush(); note('back');
globalThis.__marNav.push('/b'); await flush(); note('b2');
release(1, 5); await flush(); note('old-released');
release(2, 6); await flush(); note('new-released');
`))
	want := "a[A got=1 last=2 taps=0 bump] b1[B loading] back[A got=1 last=2 taps=0 bump] " +
		"b2[B loading] old-released[B loading] new-released[B loaded 6]"
	if got != want {
		t.Fatalf("a result for a page that left the stack reached another.\n got: %s\nwant: %s", got, want)
	}
}

// A, B, then A again: two entries for one route, each with its model and
// its own results. Back to the first A finds it as it was left.
func TestTheSameRouteTwiceKeepsTwoModels(t *testing.T) {
	got := runRouting(t, parity.NavSource, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
window.marRun(program);
const seen = [];
const note = (l) => seen.push(l + ':' + (screenText().match(/[AB]=\d+/) || ['?'])[0]);
bump(); bump(); note('first-a');
globalThis.__marNav.push('/b'); bump(); note('b');
globalThis.__marNav.push('/a'); bump(); note('second-a');
history.back(); note('back-b');
history.back(); note('back-first-a');
process.stdout.write(seen.join(' '));
`)
	want := "first-a:A=2 b:B=1 second-a:A=1 back-b:B=1 back-first-a:A=2"
	if got != want {
		t.Fatalf("the two entries of one route shared a model.\n got: %s\nwant: %s", got, want)
	}

	results := runRouting(t, routingChain, routingScript(`
await flush(); bump(); note('a1');
globalThis.__marNav.push('/b'); await flush();
globalThis.__marNav.push('/a'); await flush(); note('a2');
release(0, 3); await flush(); note('a1-result');
release(2, 4); await flush(); note('a2-result');
history.back(); await flush(); history.back(); await flush(); note('back-a1');
`))
	wantResults := "a1[A got=0 last=0 taps=1 bump] a2[A got=0 last=0 taps=0 bump] " +
		"a1-result[A got=0 last=0 taps=0 bump] a2-result[A got=1 last=4 taps=0 bump] " +
		"back-a1[A got=1 last=3 taps=1 bump]"
	if results != wantResults {
		t.Fatalf("a result of one entry of a route reached the other.\n got: %s\nwant: %s", results, wantResults)
	}
}

// A covered page's result chains another request, which stays with it; the
// navigation its next result asks for is refused, and the person stays on B.
func TestACoveredPageDoesNotNavigate(t *testing.T) {
	got := runRouting(t, routingChain, routingPrelude+`
program.devMode = true;
window.marRun(program);
(async () => {
await flush(); note('a');
globalThis.__marNav.push('/b'); await flush(); note('b');
release(0, 1); await flush(); note('chained');
release(2, 9); await flush(); note('asked-to-navigate');
history.back(); await flush(); note('back-a');
seen.push('refused=' + warns.filter((w) => w.includes('ignored Nav.push from a page that is not on screen')).length);
done();
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	want := "a[A got=0 last=0 taps=0 bump] b[B loading] chained[B loading] asked-to-navigate[B loading] " +
		"back-a[A got=2 last=9 taps=0 bump] refused=1"
	if got != want {
		t.Fatalf("a covered page moved the person, or lost its chain.\n got: %s\nwant: %s", got, want)
	}
}

// Back pops B; browser Forward lands on B's entry again. It left the stack,
// so B starts over, and the result the first B asked for is dropped.
func TestForwardStartsAPoppedPageOver(t *testing.T) {
	got := runRouting(t, routingChain, routingScript(`
await flush(); release(0, 2); await flush(); note('a');
globalThis.__marNav.push('/b'); await flush(); note('b');
history.back(); await flush(); note('back');
history.forward(); await flush(); note('forward');
release(1, 5); await flush(); note('old-b-result');
release(2, 6); await flush(); note('new-b-result');
`))
	want := "a[A got=1 last=2 taps=0 bump] b[B loading] back[A got=1 last=2 taps=0 bump] " +
		"forward[B loading] old-b-result[B loading] new-b-result[B loaded 6]"
	if got != want {
		t.Fatalf("Forward did not start the popped page over.\n got: %s\nwant: %s", got, want)
	}
}

// A covered page that plays a sound on its result does not play it behind
// the page on screen.
func TestACoveredPageDoesNotPlaySound(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text)


loadIt : Service () Int
loadIt =
    Service.declare GET "/api/load"


type Msg
    = Got (Result Service.Error Int)


pageA : Page
pageA =
    Page.create
        { path = "/a", title = "A"
        , init = ( "loading", Service.call loadIt () Got )
        , update = \msg _ ->
            case msg of
                Got _ -> ( "done", Sound.play (Sound.tone Sound.Sine 440 100) )
        , view = \m -> vstack [] [ text [] ("A " ++ m) ]
        , subscriptions = always Sub.none
        }


pageB : Page
pageB =
    Page.create
        { path = "/b", title = "B"
        , init = ( 0, Cmd.none )
        , update = \_ m -> ( m, Cmd.none )
        , view = \m -> vstack [] [ text [] ("B " ++ String.fromInt m) ]
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ pageA, pageB ]
`
	got := runRouting(t, src, routingPrelude+`
program.devMode = true;
window.marRun(program);
(async () => {
await flush();
globalThis.__marNav.push('/b'); await flush();
release(0, 7); await flush();
history.back(); await flush(); note('back-a');
seen.push('refused=' + warns.filter((w) => w.includes('ignored Sound.play from a page that is not on screen')).length);
done();
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	want := "back-a[A done] refused=1"
	if got != want {
		t.Fatalf("a covered page played sound, or did not get its result.\n got: %s\nwant: %s", got, want)
	}
}

// A covered page whose update fails on its result: nothing shows over the
// page on screen, and the failure shows over the page that failed when Back
// brings it back.
func TestACoveredPageFailureShowsWhenItIsShownAgain(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text)


loadIt : Service () Int
loadIt =
    Service.declare GET "/api/load"


type Msg
    = Got (Result Service.Error Int)


pageA : Page
pageA =
    Page.create
        { path = "/a", title = "A"
        , init = ( 0, Service.call loadIt () Got )
        , update = \msg m ->
            case msg of
                Got (Ok n) -> ( n * 9007199254740991, Cmd.none )
                Got (Err _) -> ( m, Cmd.none )
        , view = \m -> vstack [] [ text [] ("A " ++ String.fromInt m) ]
        , subscriptions = always Sub.none
        }


pageB : Page
pageB =
    Page.create
        { path = "/b", title = "B"
        , init = ( 0, Cmd.none )
        , update = \_ m -> ( m, Cmd.none )
        , view = \m -> vstack [] [ text [] ("B " ++ String.fromInt m) ]
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ pageA, pageB ]
`
	got := runRouting(t, src, routingScript(`
await flush();
console.error = () => {};
globalThis.__marNav.push('/b'); await flush();
release(0, 7); await flush();
seen.push('on-b:' + (window.__marLastError ? 'error shown' : 'no error'));
history.back(); await flush();
seen.push('back-a:' + (window.__marLastError && window.__marLastError.startsWith('update failed') ? 'update failed shown' : 'no error'));
`))
	want := "on-b:no error back-a:update failed shown"
	if got != want {
		t.Fatalf("a covered page's failure showed at the wrong time.\n got: %s\nwant: %s", got, want)
	}
}

// parity.RouteSource, the same program and string as the iOS check
// (internal/iosbundle/ios_route_test.go).
func TestRouteSourceOnTheWeb(t *testing.T) {
	got := runRouting(t, parity.RouteSource, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const flush = async () => { for (let i = 0; i < 4; i++) await new Promise((r) => setTimeout(r, 0)); };
const seen = [];
const note = (l) => seen.push(l + ':' + (screenText().match(/[AB]=\d+/) || ['?'])[0]);
const go = (node) => { node = node || marRoot; if (node.textContent === 'go' && node._on && node._on.click) { node.click(); return true; } return (node.children || []).some((c) => go(c)); };
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);
(async () => {
await flush(); note('start');
go(); await flush(); note('after-go');
history.back(); await flush(); note('back-a');
process.stdout.write(seen.join(' '));
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	want := "start:A=0 after-go:B=0 back-a:A=5"
	if got != want {
		t.Fatalf("a result went to another page than the one that asked.\n got: %s\nwant: %s", got, want)
	}
}

// parity.RouteSource's route-twice, the same string as on the phone: T, on
// screen, pushes B and then C in one update, and both pushes run.
func TestRouteSourceTwiceOnTheWeb(t *testing.T) {
	got := runRouting(t, parity.RouteSource, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const flush = async () => { for (let i = 0; i < 4; i++) await new Promise((r) => setTimeout(r, 0)); };
const seen = [];
const note = (l) => seen.push(l + ':' + (screenText().match(/[ABCT]=\d+/) || ['?'])[0]);
const press = (label, node) => { node = node || marRoot; if (node.textContent === label && node._on && node._on.click) { node.click(); return true; } return (node.children || []).some((c) => press(label, c)); };
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);
(async () => {
await flush();
globalThis.__marNav.push('/t'); await flush(); note('on-t');
press('twice'); await flush(); note('after-twice');
history.back(); await flush(); note('back');
process.stdout.write(seen.join(' '));
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	want := "on-t:T=0 after-twice:C=0 back:B=0"
	if got != want {
		t.Fatalf("the page on screen lost an effect after its own push.\n got: %s\nwant: %s", got, want)
	}
}

// parity.RouteSource's route-sheet-subs, the same string as on the phone: the
// value R asked for arrives while the sheet covers it, and R's timer starts
// when the sheet closes.
func TestRouteSourceSheetSubsOnTheWeb(t *testing.T) {
	got := runRouting(t, parity.RouteSource, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const flush = async () => { for (let i = 0; i < 4; i++) await new Promise((r) => setTimeout(r, 0)); };
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const seen = [];
const note = (l) => seen.push(l + ':' + (screenText().match(/[ABCRT]=\d+/) || ['?'])[0]);
const press = (label, node) => { node = node || marRoot; if (node.textContent === label && node._on && node._on.click) { node.click(); return true; } return (node.children || []).some((c) => press(label, c)); };
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);
(async () => {
await flush();
globalThis.__marNav.push('/r'); await flush(); note('on-r');
press('load'); await flush(); await wait(600);
note('covered');
history.back(); await flush(); await wait(600);
note('closed');
process.stdout.write(seen.join(' '));
process.exit(0);
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	want := "on-r:R=0 covered:R=0 closed:R=1"
	if got != want {
		t.Fatalf("a covered page's subscriptions did not wait for it to be shown.\n got: %s\nwant: %s", got, want)
	}
}

// The effects of the page on screen all run, though the first one pushes
// another page: covered is decided when the effects start, as on the phone.
// The reference's own Cmd.batch example navigates and plays a sound.
func TestAnUpdateOnScreenRunsAllItsEffects(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text, button)


type Msg
    = Go
    | Go2


pageA : Page
pageA =
    Page.create
        { path = "/a", title = "A"
        , init = ( "fresh", Cmd.none )
        , update = \msg m ->
            case msg of
                Go -> ( "going", Cmd.batch [ Nav.push "/b", Sound.play (Sound.tone Sound.Square (Sound.c 5) 60) ] )
                Go2 -> ( "going2", Cmd.batch [ Nav.push "/b", Nav.push "/c" ] )
        , view = \m -> vstack [] [ text [] ("A " ++ m), button [] Go "bump", button [] Go2 "two" ]
        , subscriptions = always Sub.none
        }


pageB : Page
pageB =
    Page.create
        { path = "/b", title = "B"
        , init = ( 0, Cmd.none )
        , update = \_ m -> ( m + 1, Cmd.none )
        , view = \m -> vstack [] [ text [] ("B " ++ String.fromInt m) ]
        , subscriptions = always Sub.none
        }


pageC : Page
pageC =
    Page.create
        { path = "/c", title = "C"
        , init = ( 0, Cmd.none )
        , update = \_ m -> ( m + 1, Cmd.none )
        , view = \m -> vstack [] [ text [] ("C " ++ String.fromInt m) ]
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ pageA, pageB, pageC ]
`
	got := runRouting(t, src, routingPrelude+`
program.devMode = true;
window.marRun(program);
const press = (label, node) => { node = node || marRoot; if (node.textContent === label && node._on && node._on.click) { node.click(); return true; } return (node.children || []).some((c) => press(label, c)); };
(async () => {
await flush(); note('mount');
bump(); await flush(); note('after-go');
seen.push('soundRefused=' + warns.filter((w) => w.includes('ignored Sound.play')).length);
history.back(); await flush(); note('back');
press('two'); await flush(); note('after-two');
seen.push('navRefused=' + warns.filter((w) => w.includes('ignored Nav.push')).length);
done();
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	want := "mount[A fresh bump two] after-go[B 0] soundRefused=0 back[A going bump two] after-two[C 0] navRefused=0"
	if got != want {
		t.Fatalf("an update on screen lost an effect after its own push.\n got: %s\nwant: %s", got, want)
	}
}

// A hot reload while a page is covered and its request is in flight: the
// covered page's model is only kept for the reload, and the result still
// reaches it.
func TestAHotReloadKeepsACoveredPagesResult(t *testing.T) {
	got := runRouting(t, routingTwoPages, `
const fs = require('fs');
const pending = [];
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const programText = fs.readFileSync(process.argv[3], 'utf8');
const program = JSON.parse(programText);
globalThis.fetch = (url) => {
  if (String(url).includes('program.json')) return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(JSON.parse(programText)), text: () => Promise.resolve(programText) });
  return new Promise((res) => { pending.push((body) => res({ ok: true, status: 200, headers: { get: () => null }, text: () => Promise.resolve(String(body)) })); });
};
const flush = async () => { for (let i = 0; i < 6; i++) await new Promise((r) => setTimeout(r, 0)); };
const shown = () => (screenText().match(/[ABCS] [a-z0-9= ]+/) || ['?'])[0].trim();
const seen = [];
const note = (l) => seen.push(l + '[' + shown() + ']');
window.marRun(program);
(async () => {
await flush(); note('mount');
globalThis.__marNav.push('/b'); await flush(); note('on-b');
await window.marReload(); await flush(); note('reloaded');
pending[0]('7'); await flush(); note('released');
history.back(); await flush(); note('back-a');
process.stdout.write(seen.join(' '));
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	want := "mount[A loading] on-b[B 0] reloaded[B 0] released[B 0] back-a[A loaded 7]"
	if got != want {
		t.Fatalf("a hot reload lost a covered page's result.\n got: %s\nwant: %s", got, want)
	}
}

// History entries without an id (made before ids existed, as after an app
// update in an open tab) each get their own: two of them must not be taken
// for one entry and have the other's model freed.
func TestAnEntryWithoutAnIdGetsItsOwn(t *testing.T) {
	got := runRouting(t, parity.NavSource, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
entries = [
  { state: { marDepth: 0, prevTitle: '' }, path: '/a' },
  { state: { marDepth: 1, prevTitle: 'A' }, path: '/b' },
  { state: { marDepth: 2, prevTitle: 'B' }, path: '/a' },
];
cursor = 2;
window.marRun(program);
const seen = [];
const note = (l) => seen.push(l + ':' + (screenText().match(/[AB]=\d+/) || ['?'])[0]);
note('boot');
history.back(); bump(); note('back-b');
history.back(); bump(); note('back-a');
history.forward(); note('forward-b');
history.back(); note('back-a-again');
process.stdout.write(seen.join(' '));
`)
	want := "boot:A=0 back-b:B=1 back-a:A=1 forward-b:B=0 back-a-again:A=1"
	if got != want {
		t.Fatalf("entries without an id shared a model.\n got: %s\nwant: %s", got, want)
	}
}

const reviewProtected = `module Main exposing (main)


import UI exposing (vstack, text, button)


loadIt : Service () Int
loadIt =
    Service.declare GET "/api/load"


type Msg
    = Got (Result Service.Error Int)


pageP : Page
pageP =
    Page.protected
        { path = "/p", title = "P"
        , init = \_ -> ( "loading", Service.call loadIt () Got )
        , update = \_ msg _ ->
            case msg of
                Got (Ok n) -> ( "loaded " ++ String.fromInt n, Cmd.none )
                Got (Err _) -> ( "failed", Cmd.none )
        , view = \_ m -> vstack [] [ text [] ("A " ++ m) ]
        , subscriptions = \_ _ -> Sub.none
        }


type QMsg
    = Go


pageQ : Page
pageQ =
    Page.create
        { path = "/q", title = "Q"
        , init = ( 0, Cmd.none )
        , update = \_ m -> ( m, Nav.replace "/r" )
        , view = \m -> vstack [] [ text [] ("B " ++ String.fromInt m), button [] Go "bump" ]
        , subscriptions = always Sub.none
        }


pageR : Page
pageR =
    Page.create
        { path = "/r", title = "R"
        , init = ( 0, Cmd.none )
        , update = \_ m -> ( m + 1, Cmd.none )
        , view = \m -> vstack [] [ text [] ("C " ++ String.fromInt m) ]
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ pageP, pageQ, pageR ]
`

const protectedPrelude = `
const fs = require('fs');
const pending = [];
globalThis.fetch = (url) => {
  if (String(url).includes('whoami')) return Promise.resolve({ ok: true, status: 200, headers: { get: () => null }, text: () => Promise.resolve('{"id":1}') });
  return new Promise((res) => { pending.push((body) => res({ ok: true, status: 200, headers: { get: () => null }, text: () => Promise.resolve(String(body)) })); });
};
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const flush = async () => { for (let i = 0; i < 6; i++) await new Promise((r) => setTimeout(r, 0)); };
const shown = () => (screenText().match(/[ABCS] [a-z0-9= ]+/) || ['?'])[0].trim();
const seen = [];
const note = (l) => seen.push(l + '[' + shown() + ']');
entries[0].path = '/p';
globalThis.__marAuthSignInPath = '/q';
window.marRun(program);
`

// A protected page covered by others gets its result even when the cached
// answer to who is signed in was dropped (every Nav.replace drops it): it
// asks again, then delivers.
func TestACoveredProtectedPageGetsItsResultAfterAReplace(t *testing.T) {
	got := runRouting(t, reviewProtected, protectedPrelude+`
(async () => {
await flush(); note('mount');
globalThis.__marNav.push('/q'); await flush(); note('on-q');
bump(); await flush(); note('replaced-to-r');
pending[0]('7'); await flush(); note('released');
history.back(); await flush(); note('back-p');
process.stdout.write(seen.join(' '));
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	want := "mount[A loading] on-q[B 0 bump] replaced-to-r[C 0] released[C 0] back-p[A loaded 7]"
	if got != want {
		t.Fatalf("a covered protected page lost its result after a replace.\n got: %s\nwant: %s", got, want)
	}
}

// A covered page's update fails on one result and works on the next: the
// failure is cleared as it is for a page on screen, and nothing shows on Back.
func TestAHeldFailureClearsWhenALaterUpdateWorks(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text)


loadIt : Service () Int
loadIt =
    Service.declare GET "/api/load"


type Msg
    = Got (Result Service.Error Int)


pageA : Page
pageA =
    Page.create
        { path = "/a", title = "A"
        , init = ( 0, Cmd.batch [ Service.call loadIt () Got, Service.call loadIt () Got ] )
        , update = \msg m ->
            case msg of
                Got (Ok n) -> ( if n == 7 then n * 9007199254740991 else n * 9, Cmd.none )
                Got (Err _) -> ( m, Cmd.none )
        , view = \m -> vstack [] [ text [] ("A " ++ String.fromInt m) ]
        , subscriptions = always Sub.none
        }


pageB : Page
pageB =
    Page.create
        { path = "/b", title = "B"
        , init = ( 0, Cmd.none )
        , update = \_ m -> ( m, Cmd.none )
        , view = \m -> vstack [] [ text [] ("B " ++ String.fromInt m) ]
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ pageA, pageB ]
`
	got := runRouting(t, src, routingScript(`
await flush();
console.error = () => {};
globalThis.__marNav.push('/b'); await flush();
release(0, 7); await flush();
release(1, 2); await flush();
history.back(); await flush(); note('back-a');
seen.push('banner=' + (window.__marLastError ? 'lastError-set' : 'none') + ' bannerNodes=' + (document.body.children || []).filter((c) => String(c.className || '').includes('mar-runtime-error')).length);
`))
	want := "back-a[A 18] banner=none bannerNodes=0"
	if got != want {
		t.Fatalf("a held failure outlived an update that worked.\n got: %s\nwant: %s", got, want)
	}
}

const reviewSheetFwd = `module Main exposing (main)


import UI exposing (vstack, text, button)


type Msg
    = Bump
    | Open
    | Closed


type alias Model =
    { open : Bool, n : Int }


pageA : Page
pageA =
    Page.create
        { path = "/a", title = "A"
        , init = ( 0, Cmd.none )
        , update = \_ m -> ( m, Cmd.none )
        , view = \m -> vstack [] [ text [] ("A=" ++ String.fromInt m) ]
        , subscriptions = always Sub.none
        }


pageB : Page
pageB =
    Page.create
        { path = "/b", title = "B"
        , init = ( { open = False, n = 0 }, Cmd.none )
        , update = \msg m ->
            case msg of
                Bump -> ( { m | n = m.n + 1 }, Cmd.none )
                Open -> ( { m | open = True }, Cmd.none )
                Closed -> ( { m | open = False }, Cmd.none )
        , view = \m -> vstack [] [ text [] ("B=" ++ String.fromInt m.n), button [] Bump "bump", button [] Open "open", UI.sheet { open = m.open, onDismiss = Closed, outlet = "ed" } [ text [] "inner" ] ]
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ pageA, pageB ]
`

// B opens and closes a UI.sheet, Back twice, Forward twice: the page's entry
// and its sheet entry left the stack together, and come back as one new
// entry, so B keeps one model across them.
func TestForwardKeepsAPageAndItsSheetEntryTogether(t *testing.T) {
	got := runRouting(t, reviewSheetFwd, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const flush = async () => { for (let i = 0; i < 6; i++) await new Promise((r) => setTimeout(r, 0)); };
const press = (label, node) => { node = node || marRoot; if (node.textContent === label && node._on && node._on.click) { node.click(); return true; } return (node.children || []).some((c) => press(label, c)); };
const norm = (p) => (typeof p === 'string' && p.startsWith('http://test')) ? p.slice('http://test'.length).split('?')[0] : p;
const op = history.pushState, orp = history.replaceState;
history.pushState = function (st, t, p) { return op.call(this, st, t, norm(p)); };
history.replaceState = function (st, t, p) { return orp.call(this, st, t, norm(p)); };
window.marRun(program);
const seen = [];
const hist = () => entries.map((e, i) => (i === cursor ? '*' : '') + (e.state && e.state.__marSheet ? 'S' : 'P') + '(' + (e.state && e.state.marEntry ? String(e.state.marEntry).split('.').pop() : '-') + ')').join(',');
const note = (l) => seen.push(l + ':' + (screenText().match(/[AB]=\d+/) || ['?'])[0] + ' ' + hist());
(async () => {
globalThis.__marNav.push('/b'); await flush(); press('bump'); await flush(); press('open'); await flush(); note('b-open');
history.back(); await flush(); note('back-closes');
history.back(); await flush(); note('back-a');
history.forward(); await flush(); press('bump'); press('bump'); await flush(); note('fwd-b');
history.forward(); await flush(); note('fwd-sheet-entry');
history.back(); await flush(); note('back-b');
process.stdout.write(seen.join(' | '));
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	want := "b-open:B=1 P(1),P(2),*S(2) | back-closes:B=1 P(1),*P(2),S(2) | back-a:A=0 *P(1),P(2),S(2) | " +
		"fwd-b:B=2 P(1),*P(3),S(2) | fwd-sheet-entry:B=2 P(1),P(3),*S(3) | back-b:B=2 P(1),*P(3),S(3)"
	if got != want {
		t.Fatalf("Forward split a page from its sheet entry.\n got: %s\nwant: %s", got, want)
	}
}
