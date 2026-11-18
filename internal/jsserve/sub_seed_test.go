package jsserve

import (
	"errors"
	"strings"
	"testing"

	"mar/internal/parity"
)

// A subscription to a source with a current state (Device.watch,
// Keyboard.watch, Gamepad.watch) hands that state to the page when the page
// starts listening, and again on every change. The reconciler merges
// subscribers by key, so starting to listen is not the same as the source
// starting: a page pushed from another page that watches the device joins a
// source that is already running, and so does a page that starts watching
// while a shared store already does. Both used to get nothing until the
// device changed, which on a desktop can be never.
//
// A page shown again with the model it had (Back, a sheet closing) is handed
// the state only if it changed while the page was covered, as if it had kept
// listening; a fresh model (push, replace, a reset) always is.
//
// parity.SubsSource runs through the first scripts here on a simulator too
// (internal/iosbundle/ios_sub_seed_test.go), with the same expected strings.

// subsPrelude boots the program on `start` and gives the drivers an awaitable
// flush. A seed is a microtask, so a driver has to yield before reading the
// screen: a synchronous driver would read it before any seed lands.
const subsPrelude = `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const flush = () => new Promise((r) => setTimeout(r, 0));
const seen = [];
function press(label, node) {
  node = node || marRoot;
  if (node.textContent === label && node._on && node._on.click) { node.click(); return true; }
  for (const c of (node.children || [])) if (press(label, c)) return true;
  return false;
}
`

func runSubsDriver(t *testing.T, src, driver string) string {
	t.Helper()
	program, err := parity.Compile(src, SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}
	out, err := parity.RunWeb(runtimeJS, program, subsPrelude+driver)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node not installed")
	}
	if err != nil {
		t.Fatalf("node run: %v", err)
	}
	return out
}

// subsScript lands on parity.SubsSource's first page and runs `steps`.
func subsScript(steps string) string {
	return `
const note = (label) => seen.push(label + ':' + (screenText().match(/[NABPLK]=\d+/) || ['?'])[0]);
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);
(async () => {
await flush(); note('start');
` + steps + `
process.stdout.write(seen.join(' '));
})().catch((e) => { process.stdout.write('THREW ' + e); });
`
}

func TestAWatchingPageGetsTheDeviceEachTimeItIsShown(t *testing.T) {
	got := runSubsDriver(t, parity.SubsSource, subsScript(`
globalThis.__marNav.push('/a'); await flush(); note('push-a');
globalThis.__marNav.push('/b'); await flush(); note('push-b');
history.back(); await flush(); note('back-a');
globalThis.__marNav.push('/b'); await flush(); note('push-b-again');
globalThis.__marNav.replace('/a'); await flush(); note('replace-a');
`))
	want := "start:N=0 push-a:A=1 push-b:B=1 back-a:A=1 push-b-again:B=1 replace-a:A=1"
	if got != want {
		t.Fatalf("a page did not get the device's state when it was shown.\n got: %s\nwant: %s\n\n"+
			"push-b:B=0    → the page joined A's running source and was handed nothing.\n"+
			"replace-a:A=0 → a replacing page joined the running source and was handed nothing.\n"+
			"any =2        → a seed delivered twice.", got, want)
	}
}

// Back shows the page underneath with the model it had. Nothing changed while
// it was covered, so it is handed nothing: it already has the state.
func TestBackWithNothingChangedHandsNothing(t *testing.T) {
	got := runSubsDriver(t, parity.SubsSource, subsScript(`
globalThis.__marNav.push('/a'); await flush(); note('push-a');
globalThis.__marNav.push('/b'); await flush(); note('push-b');
history.back(); await flush(); note('back-a');
`))
	want := "start:N=0 push-a:A=1 push-b:B=1 back-a:A=1"
	if got != want {
		t.Fatalf("Back handed a page a state it already had.\n got: %s\nwant: %s", got, want)
	}
}

// Handing an unchanged state again on Back is not harmless: a page that
// navigates on the device's state would be sent forward again by every Back,
// and the user could never get back to it.
func TestBackDoesNotReplayAStateThePageActedOn(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text)


type alias Model =
    { n : Int }


type Msg
    = Got Device


routeList : Path {}
routeList = "/list"


home : Page
home =
    Page.create
        { path = "/", title = "Home"
        , init = ( { n = 0 }, Cmd.none )
        , update = \msg m ->
            case msg of
                Got d -> ( { n = m.n + 1 }, if d.width < 1000 then Nav.pushTo routeList {} else Cmd.none )
        , view = \m -> vstack [] [ text [] ("Home=" ++ String.fromInt m.n) ]
        , subscriptions = \_ -> Device.watch Got
        }


list : Page
list =
    Page.create
        { path = "/list", title = "List"
        , init = ( { n = 0 }, Cmd.none )
        , update = \_ m -> ( { n = m.n + 1 }, Cmd.none )
        , view = \m -> vstack [] [ text [] ("List=" ++ String.fromInt m.n) ]
        , subscriptions = \_ -> Device.watch Got
        }


main : Cmd ()
main =
    App.frontend [ home, list ]
`
	got := runSubsDriver(t, src, `
const note = (l) => seen.push(l + ':' + location.pathname + ':' + (screenText().match(/(Home|List)=\d+/) || ['?'])[0]);
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.innerWidth = 800;
window.marRun(program);
(async () => {
await flush(); await flush(); note('narrow');
history.back(); await flush(); await flush(); note('back');
process.stdout.write(seen.join(' '));
})().catch((e) => { process.stdout.write('THREW ' + e); });
`)
	want := "narrow:/list:List=1 back:/:Home=1"
	if got != want {
		t.Fatalf("Back replayed a state the page had acted on.\n got: %s\nwant: %s", got, want)
	}
}

func TestAPageJoiningItsStoresRunningSourceGetsTheDevice(t *testing.T) {
	got := runSubsDriver(t, parity.SubsSource, subsScript(`
globalThis.__marNav.push('/k'); await flush(); note('push-k');
globalThis.__marNav.replace('/p'); await flush(); note('replace-p');
globalThis.__marNav.push('/l'); await flush(); note('push-l');
press('arm'); await flush(); note('armed');
`))
	want := "start:N=0 push-k:K=1 replace-p:P=0 push-l:L=0 armed:L=1"
	if got != want {
		t.Fatalf("a page that started watching while its store already did was not handed the state.\n"+
			" got: %s\nwant: %s", got, want)
	}
}

// The seed goes to the subscriber that joined, not to every subscriber of the
// source: the store has had the state since it started, and a page joining it
// must not hand the store a second copy. (The phone's store count is not
// compared here: each phone page runs its own copy of the store's
// subscription, so the store hears from every page that mounts.)
func TestAJoiningPageDoesNotReplayTheStateToTheStore(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text, button)


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


page : Page
page =
    Page.withShared store
        (\count ->
            Page.create
                { path = "/"
                , title = "L"
                , init = ( { n = 0, armed = False }, Cmd.none )
                , update = \msg m ->
                    case msg of
                        Arm -> ( { m | armed = True }, Cmd.none )
                        Got _ -> ( { m | n = m.n + 1 }, Cmd.none )
                , view = \m -> vstack [] [ text [] ("L=" ++ String.fromInt m.n ++ " store=" ++ String.fromInt count), button [] Arm "arm" ]
                , subscriptions = \m -> if m.armed then Device.watch Got else Sub.none
                }
        )


main : Cmd ()
main =
    App.frontend [ page ]
`
	got := runSubsDriver(t, src, `
const note = (l) => seen.push(l + ':' + (screenText().match(/L=\d+ store=\d+/) || ['?'])[0]);
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);
(async () => {
await flush(); note('start');
press('arm'); await flush(); note('armed');
process.stdout.write(seen.join(' '));
})().catch((e) => { process.stdout.write('THREW ' + e); });
`)
	want := "start:L=0 store=1 armed:L=1 store=1"
	if got != want {
		t.Fatalf("a page joining the store's source changed what the store received.\n got: %s\nwant: %s", got, want)
	}
}

// Who receives a seed is decided when it is delivered. A page that leaves
// before its seed lands gets nothing, and the page that replaced it gets its
// own, once: not two seeds, and not the seed that was meant for the other.
func TestASeedGoesToThePageThatIsStillListening(t *testing.T) {
	got := runSubsDriver(t, parity.SubsSource, subsScript(`
globalThis.__marNav.push('/a');
globalThis.__marNav.push('/b'); await flush(); note('push-b');
history.back(); await flush(); note('back-a');
`))
	want := "start:N=0 push-b:B=1 back-a:A=1"
	if got != want {
		t.Fatalf("a seed reached the wrong page or arrived twice.\n got: %s\nwant: %s\n\n"+
			"push-b:B=2 → B got A's seed and its own.\n"+
			"back-a:A=0 → A was not handed the state when it was shown again.", got, want)
	}
}

// subsWidthSource shows the width each page last received, so a stale state
// is visible, not only a missing message.
const subsWidthSource = `module Main exposing (main)


import UI exposing (vstack, text, navigationStack, navigationTitle)


type alias Model =
    { seen : Int, w : Int }


type Msg
    = GotDevice Device


init : (Model, Cmd Msg)
init = ( { seen = 0, w = 0 }, Cmd.none )


update : Msg -> Model -> (Model, Cmd Msg)
update msg model =
    case msg of
        GotDevice d -> ( { seen = model.seen + 1, w = d.width }, Cmd.none )


viewFor : String -> Model -> View Msg
viewFor name model =
    navigationStack [ navigationTitle name ]
        [ vstack [] [ text [] (name ++ " seen=" ++ String.fromInt model.seen ++ " w=" ++ String.fromInt model.w) ] ]


watching : String -> String -> Page
watching path name =
    Page.create
        { path = path, title = name, init = init, update = update
        , view = viewFor name, subscriptions = \_ -> Device.watch GotDevice }


pageA : Page
pageA =
    watching "/a" "A"


pageB : Page
pageB =
    watching "/b" "B"


pageS : Page
pageS =
    Page.sheet (watching "/s" "S")


main : Cmd ()
main =
    App.frontend [ pageA, pageB, pageS ]
`

// A page covered by another hears nothing while it is covered, and Back hands
// it the device as it is now, not as it was when it was left.
//
// Under a sheet the web and the phone differ in how the covered page gets
// there: the phone keeps it listening under the sheet, the web stops it while
// the sheet is up and hands it the state when the sheet closes. Either way it
// shows the current width; a dismissal with nothing changed is one message
// more here than on the phone.
func TestACoveredPageCatchesUpWhenItIsShownAgain(t *testing.T) {
	driver := `
const re = /[ABS] seen=\d+ w=\d+/;
const note = (l) => seen.push(l + '[' + (screenText().match(re) || ['?'])[0] + ']');
const notes = (l) => seen.push(l + '[root=' + (screenText().match(re) || ['?'])[0] + ' sheet=' + ((sheetText().match(re) || ['-'])[0]) + ']');
window.marRun(program);
(async () => {
await flush(); note('mount');
globalThis.__marNav.push('/b'); await flush();
window.innerWidth = 500; fireWindow('resize'); await flush(); note('b-resized');
history.back(); await flush(); note('back-a');
globalThis.__marNav.push('/s'); await flush(); notes('presented');
window.innerWidth = 700; fireWindow('resize'); await flush(); notes('s-resized');
history.back(); await flush(); notes('dismissed');
process.stdout.write(seen.join(' '));
})().catch((e) => { process.stdout.write('THREW ' + e); });
`
	got := runSubsDriver(t, subsWidthSource, driver)
	want := "mount[A seen=1 w=900] b-resized[B seen=2 w=500] back-a[A seen=2 w=500] " +
		"presented[root=A seen=2 w=500 sheet=S seen=1 w=500] " +
		"s-resized[root=A seen=2 w=500 sheet=S seen=2 w=700] " +
		"dismissed[root=A seen=3 w=700 sheet=-]"
	if got != want {
		t.Fatalf("a page shown again kept a stale device state.\n got: %s\nwant: %s", got, want)
	}
}

// A page whose init sends you straight back (a bootstrap gate) re-inits the
// page you came from, before the redirecting page ever subscribed. The page
// is shown again with a fresh model, so it is handed the state again, though
// its source never stopped.
func TestAPageReInitByARedirectGetsTheStateAgain(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text)


type alias Model =
    { n : Int }


type Msg
    = Got Device


routeA : Path {}
routeA = "/a"


pageA : Page
pageA =
    Page.create
        { path = "/a", title = "A"
        , init = ( { n = 0 }, Cmd.none )
        , update = \_ m -> ( { n = m.n + 1 }, Cmd.none )
        , view = \m -> vstack [] [ text [] ("A=" ++ String.fromInt m.n) ]
        , subscriptions = \_ -> Device.watch Got
        }


pageB : Page
pageB =
    Page.create
        { path = "/b", title = "B"
        , init = ( { n = 0 }, Nav.replaceTo routeA {} )
        , update = \_ m -> ( m, Cmd.none )
        , view = \m -> vstack [] [ text [] ("B=" ++ String.fromInt m.n) ]
        , subscriptions = \_ -> Sub.none
        }


main : Cmd ()
main =
    App.frontend [ pageA, pageB ]
`
	got := runSubsDriver(t, src, `
const note = (l) => seen.push(l + ':' + (screenText().match(/[AB]=\d+/) || ['?'])[0]);
window.marRun(program);
(async () => {
await flush(); note('start');
globalThis.__marNav.push('/b'); await flush(); note('redirected');
process.stdout.write(seen.join(' '));
})().catch((e) => { process.stdout.write('THREW ' + e); });
`)
	want := "start:A=1 redirected:A=1"
	if got != want {
		t.Fatalf("a page re-initialized by a redirect was left without the state.\n got: %s\nwant: %s", got, want)
	}
}

// A hot reload restarts every source, which hands every subscriber its state
// once: one message, not one per source restart and join.
func TestAHotReloadHandsTheStateOnce(t *testing.T) {
	got := runSubsDriver(t, subsWidthSource, `
const note = (l) => seen.push(l + '[' + (screenText().match(/A seen=\d+/) || ['?'])[0] + ']');
window.marRun(program);
(async () => {
await flush(); note('mount');
global.fetch = () => Promise.resolve({ ok: true, json: () => Promise.resolve(program) });
await window.marReload(); await flush(); note('reloaded');
process.stdout.write(seen.join(' '));
})().catch((e) => { process.stdout.write('THREW ' + e); });
`)
	want := "mount[A seen=1] reloaded[A seen=2]"
	if got != want {
		t.Fatalf("a hot reload did not hand the state exactly once.\n got: %s\nwant: %s", got, want)
	}
}

// The held-key and gamepad mirrors are the same kind of source: a page shown
// by navigation is handed the keys held and the pad as they are now, and a page
// shown again by Back is handed what changed while it was covered (a key went
// down on B) and not what did not (the pad).
func TestKeyboardAndGamepadMirrorsFollowThePage(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text)


type alias Model =
    { k : Int, g : Int }


type Msg
    = GotKeys { down : List Keyboard.Key }
    | GotPad { connected : Bool, leftX : Int, leftY : Int, rightX : Int, rightY : Int, down : List Gamepad.Button }


init : (Model, Cmd Msg)
init = ( { k = 0, g = 0 }, Cmd.none )


update : Msg -> Model -> (Model, Cmd Msg)
update msg model =
    case msg of
        GotKeys _ -> ( { model | k = model.k + 1 }, Cmd.none )
        GotPad _ -> ( { model | g = model.g + 1 }, Cmd.none )


watching : String -> String -> Page
watching path name =
    Page.create
        { path = path, title = name, init = init, update = update
        , view = \m -> vstack [] [ text [] (name ++ " k=" ++ String.fromInt m.k ++ " g=" ++ String.fromInt m.g) ]
        , subscriptions = \_ -> Sub.batch [ Keyboard.watch GotKeys, Gamepad.watch GotPad ] }


pageA : Page
pageA =
    watching "/a" "A"


pageB : Page
pageB =
    watching "/b" "B"


main : Cmd ()
main =
    App.frontend [ pageA, pageB ]
`
	got := runSubsDriver(t, src, `
const note = (l) => seen.push(l + '[' + (screenText().match(/[AB] k=\d+ g=\d+/) || ['?'])[0] + ']');
window.marRun(program);
(async () => {
await flush(); note('mount');
globalThis.__marNav.push('/b'); await flush(); note('on-b');
fireWindow('keydown', { code: 'KeyA', target: { tagName: 'DIV' }, preventDefault() {} }); await flush(); note('b-keydown');
history.back(); await flush(); note('back-a');
process.stdout.write(seen.join(' '));
})().catch((e) => { process.stdout.write('THREW ' + e); });
`)
	want := "mount[A k=1 g=1] on-b[B k=1 g=1] b-keydown[B k=2 g=1] back-a[A k=2 g=1]"
	if got != want {
		t.Fatalf("the keyboard or gamepad mirror did not hand a page its state.\n got: %s\nwant: %s", got, want)
	}
}

// A source stopped inside one turn (a page that watches, then one that does
// not, before the seed lands) leaves a seed queued for a record that is gone.
// It must do nothing: it would reach the page on screen through a tagger that
// belonged to the page that left. Then a page that watches again gets its own
// seed from a new record, once.
func TestASeedForAStoppedSourceIsDropped(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text)


type alias Model =
    { seen : Int }


type Msg
    = Got Device


page : String -> String -> (Model -> Sub Msg) -> Page
page path name subs =
    Page.create
        { path = path, title = name
        , init = ( { seen = 0 }, Cmd.none )
        , update = \_ m -> ( { seen = m.seen + 1 }, Cmd.none )
        , view = \m -> vstack [] [ text [] (name ++ " seen=" ++ String.fromInt m.seen) ]
        , subscriptions = subs
        }


pageA : Page
pageA =
    page "/a" "A" (\_ -> Device.watch Got)


pageB : Page
pageB =
    page "/b" "B" (\_ -> Device.watch Got)


pageC : Page
pageC =
    page "/c" "C" (always Sub.none)


main : Cmd ()
main =
    App.frontend [ pageC, pageA, pageB ]
`
	got := runSubsDriver(t, src, `
const note = (l) => seen.push(l + '[' + (screenText().match(/[ABC] seen=\d+/) || ['?'])[0] + ']');
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/c');
window.marRun(program);
(async () => {
await flush(); note('start');
globalThis.__marNav.push('/a');
globalThis.__marNav.push('/c'); await flush(); note('push-c');
globalThis.__marNav.push('/b'); await flush(); note('push-b');
process.stdout.write(seen.join(' '));
})().catch((e) => { process.stdout.write('THREW ' + e); });
`)
	want := "start[C seen=0] push-c[C seen=0] push-b[B seen=1]"
	if got != want {
		t.Fatalf("a seed queued for a stopped source reached the page on screen.\n got: %s\nwant: %s", got, want)
	}
}

// A second store that starts watching while the first already does joins a
// running source: it is handed the state, and the first is not handed it again.
// (Each store is read on a page of its own: a page reads one store.)
func TestAStoreJoiningARunningSourceGetsTheDevice(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (vstack, text, button)


type StoreMsg
    = Got Device
    | Arm


first : App.Shared Int StoreMsg
first =
    App.shared
        { init = ( 0, Cmd.none )
        , update = \msg n -> case msg of
            Got _ -> ( n + 1, Cmd.none )
            Arm -> ( n, Cmd.none )
        , subscriptions = \_ -> Device.watch Got
        }


type alias Later =
    { n : Int, armed : Bool }


second : App.Shared Later StoreMsg
second =
    App.shared
        { init = ( { n = 0, armed = False }, Cmd.none )
        , update = \msg m -> case msg of
            Got _ -> ( { m | n = m.n + 1 }, Cmd.none )
            Arm -> ( { m | armed = True }, Cmd.none )
        , subscriptions = \m -> if m.armed then Device.watch Got else Sub.none
        }


type Msg
    = Press


pageOne : Page
pageOne =
    Page.withShared first
        (\one ->
            Page.create
                { path = "/one"
                , title = "One"
                , init = ( (), Cmd.none )
                , update = \_ m -> ( m, Cmd.none )
                , view = \_ -> vstack [] [ text [] ("one=" ++ String.fromInt one) ]
                , subscriptions = always Sub.none
                }
        )


pageTwo : Page
pageTwo =
    Page.withShared second
        (\two ->
            Page.create
                { path = "/"
                , title = "Two"
                , init = ( (), Cmd.none )
                , update = \_ m -> ( m, Cmd.toShared second Arm )
                , view = \_ -> vstack [] [ text [] ("two=" ++ String.fromInt two.n), button [] Press "arm" ]
                , subscriptions = always Sub.none
                }
        )


main : Cmd ()
main =
    App.frontend [ pageTwo, pageOne ]
`
	got := runSubsDriver(t, src, `
const note = (l) => seen.push(l + ':' + (screenText().match(/(one|two)=\d+/) || ['?'])[0]);
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);
(async () => {
await flush(); note('start');
press('arm'); await flush(); note('armed');
globalThis.__marNav.push('/one'); await flush(); note('first');
process.stdout.write(seen.join(' '));
})().catch((e) => { process.stdout.write('THREW ' + e); });
`)
	want := "start:two=0 armed:two=1 first:one=1"
	if got != want {
		t.Fatalf("a store joining a running source was not handed the state, or the other store was handed it again.\n"+
			" got: %s\nwant: %s", got, want)
	}
}

// The dev panel's time-travel reset gives the page a fresh model, which is
// handed the device's state like any fresh model. The reset is reached through
// the runtime's own function, exposed for the test by editing the source text
// before it is evaluated: the dock that calls it is not built in the fake
// browser.
func TestATimeTravelResetHandsTheFreshModelTheState(t *testing.T) {
	program, err := parity.Compile(subsWidthSource, SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}
	hook := "function resetTimeTravel() {"
	if !strings.Contains(runtimeJS, hook) {
		t.Fatal("resetTimeTravel is gone from runtime.js; this test reaches it by name")
	}
	runtime := strings.Replace(runtimeJS, hook,
		"globalThis.__marResetTimeTravel = () => { pushTimeTravelState = () => {}; resetTimeTravel(); }; "+hook, 1)
	got, err := parity.RunWeb(runtime, program, subsPrelude+`
const note = (l) => seen.push(l + '[' + (screenText().match(/A seen=\d+ w=\d+/) || ['?'])[0] + ']');
window.marRun(program);
(async () => {
await flush(); note('mount');
window.innerWidth = 500; fireWindow('resize'); await flush(); note('resized');
globalThis.__marResetTimeTravel(); await flush(); note('reset');
process.stdout.write(seen.join(' '));
})().catch((e) => { process.stdout.write('THREW ' + e); });
`)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node not installed")
	}
	if err != nil {
		t.Fatalf("node run: %v", err)
	}
	want := "mount[A seen=1 w=900] resized[A seen=2 w=500] reset[A seen=1 w=500]"
	if got != want {
		t.Fatalf("the time-travel reset did not hand the fresh model the state.\n got: %s\nwant: %s\n\n"+
			"THREW apply: not a function → reset applied init to () though init is the tuple itself.\n"+
			"reset[A seen=0 w=0] → the fresh model kept the old page's owner and was handed nothing.", got, want)
	}
}

// The order that once handed K's init message to another page on the phone
// (internal/iosbundle/ios_sub_seed_test.go, subs-remount), same string.
func TestAnInitMessageReachesItsPage(t *testing.T) {
	got := runSubsDriver(t, parity.SubsSource, subsScript(`
globalThis.__marNav.push('/p'); await flush();
globalThis.__marNav.push('/l'); await flush();
press('arm'); await flush();
globalThis.__marNav.push('/k'); await flush(); note('push-k');
`))
	want := "start:N=0 push-k:K=1"
	if got != want {
		t.Fatalf("K's init message did not reach K.\n got: %s\nwant: %s", got, want)
	}
}
