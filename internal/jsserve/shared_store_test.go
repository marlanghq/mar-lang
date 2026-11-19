package jsserve

import (
	"errors"
	"testing"

	"mar/internal/parity"
)

// Page models die on forward navigation (ADR 0009). A shared store is the one
// thing that does not, and these tests drive the real runtime to prove it:
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
// Pressing `share` from page B is the sharper version of the same check: B's
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

// parity.SharedWrapSource, the same string as TestSharedWrappersOnIOS: every
// way to nest Page.sheet and Page.withShared presents the page, and a page
// that reads two stores is drawn and follows both. P's own hit-a moves store
// A, which every later page reads.
func TestSheetAndSharedWrappersCompose(t *testing.T) {
	program, err := parity.Compile(parity.SharedWrapSource, SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}
	got, err := parity.RunWeb(runtimeJS, program, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);
const flush = async () => { for (let i = 0; i < 4; i++) await new Promise((r) => setTimeout(r, 0)); };
const seen = [];
const label = /[APLKT]=\d+/;
const root  = () => (screenText().match(label) || ['-'])[0];
const sheet = () => (sheetText().match(label) || ['-'])[0];
const note = (l) => seen.push(l + ':root=' + root() + ',sheet=' + sheet());
const sheetHost = () => (document.body.children || []).filter((c) => c.classList && c.classList.contains('mar-page-sheet') && c.classList.contains('mar-sheet-open')).pop();
const press = (text, node) => { if (!node) return false; if (node.textContent === text && node._on && node._on.click) { node.click(); return true; } return (node.children || []).some((c) => press(text, c)); };
(async () => {
await flush(); note('start');
globalThis.__marNav.push('/p'); await flush(); note('p');
press('hit-a', sheetHost()); await flush(); note('p-hit');
history.back(); await flush();
globalThis.__marNav.push('/l'); await flush(); note('l');
history.back(); await flush();
globalThis.__marNav.push('/k'); await flush(); note('k');
history.back(); await flush();
globalThis.__marNav.push('/t'); await flush(); note('t');
press('hit-a', marRoot); await flush(); note('hit-a');
press('hit-b', marRoot); await flush(); note('hit-b');
process.stdout.write(seen.join(' '));
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node not installed")
	}
	if err != nil {
		t.Fatalf("node run: %v", err)
	}
	want := "start:root=A=0,sheet=- p:root=A=0,sheet=P=7 p-hit:root=A=0,sheet=P=8 " +
		"l:root=A=0,sheet=L=8 k:root=A=0,sheet=K=809 " +
		"t:root=T=809,sheet=- hit-a:root=T=909,sheet=- hit-b:root=T=910,sheet=-"
	if got != want {
		t.Fatalf("Page.sheet and Page.withShared do not compose.\n got: %s\nwant: %s\n\n"+
			"p:root=P=7,sheet=- → Page.sheet around a Page.withShared was pushed, not presented.\n"+
			"t:root=A=0          → the page that reads two stores was never drawn.", got, want)
	}
}

// parity.SharedWrapSource's R, the same string as TestSharedWrappersOnIOS's
// second run: both stores hand Device.watch the very same tagger, and R reads
// store A twice. Each store is handed the device once.
func TestStoresThatShareATaggerEachGetTheirOwn(t *testing.T) {
	program, err := parity.Compile(parity.SharedWrapSource, SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}
	got, err := parity.RunWeb(runtimeJS, program, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);
const flush = async () => { for (let i = 0; i < 4; i++) await new Promise((r) => setTimeout(r, 0)); };
const seen = [];
const note = (l) => seen.push(l + ':root=' + ((screenText().match(/[AR]=\d+/) || ['-'])[0]));
(async () => {
await flush();
globalThis.__marNav.push('/r'); await flush(); note('r');
process.stdout.write(seen.join(' '));
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node not installed")
	}
	if err != nil {
		t.Fatalf("node run: %v", err)
	}
	if want := "r:root=R=101"; got != want {
		t.Fatalf("a store was handed the device a number of times other than once.\n got: %s\nwant: %s\n\n"+
			"R=2   → store A's messages went to store B.\n"+
			"R=201 → store A, read twice, got each message twice.", got, want)
	}
}

// Page.sheet changes where a page is drawn and nothing else: wrapped around
// Page.adminProtected, the admin gate still sends a visitor with no admin
// session to the admin sign-in.
func TestASheetKeepsTheAdminGate(t *testing.T) {
	src := `module Main exposing (main)


import UI exposing (text)


home : Page
home =
    Page.create
        { path = "/", title = "Home", init = ( 0, Cmd.none )
        , update = \_ m -> ( m, Cmd.none ), view = \_ -> text [] "home"
        , subscriptions = always Sub.none }


ops : Page
ops =
    Page.sheet
        (Page.adminProtected
            { path = "/ops", title = "Ops", init = \_ -> ( 0, Cmd.none )
            , update = \_ _ m -> ( m, Cmd.none ), view = \_ _ -> text [] "ops"
            , subscriptions = \_ _ -> Sub.none })


main : Cmd ()
main =
    App.frontend [ home, ops ]
`
	program, err := parity.Compile(src, SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}
	got, err := parity.RunWeb(runtimeJS, program, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
let assigned = '-';
window.location.assign = (u) => { assigned = u; };
global.fetch = () => Promise.resolve({ ok: false, status: 401, json: () => Promise.resolve({}) });
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/ops');
window.marRun(program);
const flush = async () => { for (let i = 0; i < 6; i++) await new Promise((r) => setTimeout(r, 0)); };
(async () => {
await flush();
process.stdout.write('assigned=' + assigned + ' shows-ops=' + /ops/.test(screenText() + ' ' + sheetText()));
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node not installed")
	}
	if err != nil {
		t.Fatalf("node run: %v", err)
	}
	if want := "assigned=/_mar/admin/login shows-ops=false"; got != want {
		t.Fatalf("Page.sheet dropped the admin gate.\n got: %s\nwant: %s", got, want)
	}
}

// parity.StoreLifeSource, the same string as TestAStoreLivesAsLongAsTheAppOnIOS:
// a store watching the device is handed it once whatever pages come and go, a
// store a page only writes to still runs its init Cmd after the first render,
// and a page a sheet covers stops listening.
func TestAStoreLivesAsLongAsTheApp(t *testing.T) {
	program, err := parity.Compile(parity.StoreLifeSource, SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}
	got, err := parity.RunWeb(runtimeJS, program, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);
const flush = async () => { for (let i = 0; i < 4; i++) await new Promise((r) => setTimeout(r, 0)); };
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const seen = [];
const top = () => (screenText().match(/[NPCTBS]=\d+/) || ['-'])[0];
const note = (l) => seen.push(l + ':' + top());
const ticks = () => { const m = screenText().match(/T=(\d+)/); return m ? Number(m[1]) : -1; };
(async () => {
await flush();
globalThis.__marNav.push('/c'); await flush(); note('c');
globalThis.__marNav.push('/p'); await flush(); note('p');
history.back(); await flush(); note('back-c');
history.back(); await flush();
globalThis.__marNav.push('/c'); await flush(); note('c-again');
globalThis.__marNav.push('/b'); await flush(); note('b');
history.back(); await flush();
globalThis.__marNav.push('/t'); await wait(450);
const before = ticks();
globalThis.__marNav.push('/s'); await wait(800);
history.back(); await flush();
const after = ticks();
seen.push('t-under-sheet:' + (before >= 0 && after - before <= 1 ? 'frozen' : 'ticking'));
process.stdout.write(seen.join(' '));
process.exit(0);
})().catch((e) => process.stdout.write('THREW ' + e.stack));
`)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node not installed")
	}
	if err != nil {
		t.Fatalf("node run: %v", err)
	}
	want := "c:C=101 p:P=0 back-c:C=101 c-again:C=101 b:B=2 t-under-sheet:frozen"
	if got != want {
		t.Fatalf("a store or a covered page lives differently than it should.\n got: %s\nwant: %s\n\n"+
			"C=100 → the store a page only writes to never ran its init Cmd.", got, want)
	}
}
