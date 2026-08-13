// Top-level state for the runtime. Owns:
//
//   - APIClient (program.json fetch + service POSTs)
//   - Discovery (Bonjour browser, DEBUG only)
//   - Program loader (fetches /_mar/program.json, decodes, runs main)
//
// The interesting work happens in `loadAll()`: fetch the JSON, decode
// into Program, build a fresh env via MarBuiltins.makeEnv, load the
// user's module, run `main` (an Effect that captures the page list
// into AppContext), then expose the captured pages so ContentView
// can render them.
//
// MainActor + @Observable so SwiftUI tracks property reads
// automatically. APIClient is an actor so network calls happen off
// the main thread.
//
// Backend URL resolution:
//   - RELEASE: always the baked Info.plist MarBaseURL (set from
//     mar.json's `ios.serverUrl`). Bonjour is compiled out.
//   - DEBUG: same baked URL by default, but the Bonjour discovery
//     loop overrides it when a `_mar._tcp` service appears on the
//     LAN: typically `mar dev` running on the laptop. Two guards
//     keep the convenience from turning into a trap:
//       * the service's mDNS instance name must equal this app's
//         MarAppName (mar dev advertises its mar.json `name`), so
//         another project's dev server on the same LAN is ignored
//         instead of silently hijacking the backend;
//       * every adoption is announced: a banner in the UI
//         (devOverrideLabel, rendered by ContentView) and a console
//         line, so "I could swear it was pointing at production"
//         can't happen again.

import Foundation
import Observation
import SwiftUI

@MainActor
@Observable
final class AppViewModel {
    enum LoadState: Equatable {
        case idle
        case loading
        case loaded
        case failed(String)
    }

    private(set) var state: LoadState = .idle

    /// Pages decoded from the user's `main`. Empty → backend-only app
    /// (the iOS shell shows a placeholder; iOS apps without pages are
    /// uncommon).
    private(set) var pages: [DecodedPage] = []

    /// Currently-active backend URL as a string. Starts at the baked
    /// Info.plist value; in DEBUG, swaps to a discovered Bonjour
    /// endpoint when one appears.
    private(set) var baseURLString: String

    /// Non-nil once the server has told us it runs a mar newer or
    /// older than the one compiled into this binary, and we therefore
    /// refused to apply its program. Holds the server's version so the
    /// banner can name it. See `loadAll` for the reasoning.
    private(set) var incompatibleServerRuntime: String?

    /// Non-nil while the app is talking to a Bonjour-discovered dev
    /// server instead of the baked URL ("192.168.0.12:3033"). Drives
    /// the DEBUG banner in ContentView. Always nil in RELEASE (the
    /// discovery loop that sets it is compiled out).
    private(set) var devOverrideLabel: String?

    #if DEBUG
    let discovery = Discovery()
    #endif

    @ObservationIgnored
    private let api: APIClient

    /// Bytes of the program.json last successfully loaded. Lets the
    /// background refresh path detect "nothing changed" and skip the
    /// expensive (and state-resetting) re-run entirely. In typical
    /// usage the embedded snapshot and the fetched program are
    /// byte-identical (the user hasn't deployed since the .ipa was
    /// built), so this short-circuits ~every refresh.
    @ObservationIgnored
    private var lastLoadedProgramBytes: Data?

    init() {
        let initial = AppViewModel.bakedBaseURL()
        self.baseURLString = initial
        let url = URL(string: initial) ?? URL(string: "http://localhost:3000")!
        self.api = APIClient(baseURL: url)
        MarDispatcher.shared.baseURL = url

        // Instant cold-start. If `mar build --target ios` embedded a
        // program.json snapshot in the app bundle, decode and execute
        // it synchronously here so the first frame paints immediately.
        // The network fetch in loadAll() then refreshes the in-memory
        // program with whatever the server is serving right now.
        //
        // Failure to decode the embedded snapshot is logged and
        // swallowed: state stays .idle so the regular fetch path
        // takes over and surfaces any real error to the user.
        if let embedded = AppViewModel.loadEmbeddedProgram() {
            do {
                try self.runProgramSync(embedded)
                self.state = .loaded
            } catch {
                #if DEBUG
                print("[mar] embedded program.json decode failed: \(error)")
                #endif
            }
        }

        #if DEBUG
        // Bonjour discovery is debug-only. App Store / TestFlight
        // builds never browse the local network: that would be
        // both wasteful (cellular networks have no _mar._tcp peer)
        // and a privacy / spoofing concern (a hostile WiFi could
        // advertise a fake mar backend).
        discovery.onServersChanged = { [weak self] in
            self?.maybeAutoPick()
        }
        discovery.start()
        #endif
    }

    #if DEBUG
    /// Builds every declared page once and prints what each one drew, then
    /// gets out of the way. Off unless MAR_ROUTE_SMOKE=1 is in the
    /// environment, so a normal Debug run never pays for it.
    ///
    /// Exists because "the app launched and the first screen looks right" is
    /// a weak check, and the failure it missed was severe: with the module
    /// loader sharing one namespace, EVERY page rendered the last-loaded
    /// module's `view` while keeping its own path and title. One screen can
    /// look fine while the rest of the app is wrong. This walks all of them,
    /// which is what "runs on both platforms" has to mean.
    ///
    /// The title printed is the page's own `navigationTitle`, not the route:
    /// a route whose title belongs to another module is exactly that bug.
    @MainActor
    private static func routeSmoke(_ pages: [DecodedPage]) {
        print("[mar] ROUTE SMOKE \(pages.count) page(s)")
        for page in pages {
            // A Page.protected view takes the app's OWN user entity as its
            // first argument, and that type is different in every app: there
            // is no generic User to hand it. Say so instead of rendering it
            // without one and reporting the resulting partial application as
            // a failure of the page.
            if page.isProtected {
                print("[mar] ROUTE \(page.path) -> NEEDS SESSION (not covered)")
                continue
            }
            // Dynamic pages need params; a page whose pattern cannot be
            // filled is reported rather than skipped silently. The CONCRETE
            // url is what gets printed, because the web half of the
            // comparison is sent to whatever these lines name and has to
            // land on the same page with the same values.
            var url = page.path
            var params: MarValue? = nil
            if page.isDynamic {
                guard let visit = page.sampleVisit() else {
                    print("[mar] ROUTE \(page.path) -> SKIPPED (no sample params)")
                    continue
                }
                url = visit.url
                params = visit.params
            }
            let runtime = PageRuntime(page: page, user: nil, params: params)
            guard let view = runtime.currentView() else {
                print("[mar] ROUTE \(url) -> DREW NOTHING \(runtime.lastError ?? "")")
                continue
            }
            let title = view.attrs.first { $0.name == "navigationTitle" }
                .flatMap { attr -> String? in
                    if case .string(let s) = attr.value { return s }
                    return nil
                } ?? page.title
            print("[mar] ROUTE \(url) -> \(view.tag) titled \"\(title)\"")
            // Everything this screen puts in front of a person, as a set. The
            // web runtime can be asked the same question about the same
            // program, and the two answers have to agree: that comparison is
            // what "equivalent", rather than "it also rendered", means.
            print("[mar] TEXT \(url) \(AppViewModel.visibleText(view))")
            // One interaction, so the check covers responding and not just
            // drawing: dispatch the first message the screen offers and ask
            // again. A runtime that draws correctly but ignores taps differs
            // here and nowhere else.
            // A canvas screen puts no words on screen, so the line above says
            // the same empty thing on both platforms and "matches" by having
            // nothing to say. What it DOES produce is a draw list, built by
            // the same Mar code from the same model, so print that too.
            if let shapes = AppViewModel.drawList(view) {
                print("[mar] SHAPES \(url) \(shapes)")
            }
            // One interaction, so the check covers responding and not just
            // drawing: dispatch the first message the screen offers and ask
            // again. A runtime that draws correctly but ignores taps differs
            // here and nowhere else.
            if let msg = AppViewModel.firstMessage(view) {
                runtime.dispatch(msg)
                if let after = runtime.currentView() {
                    print("[mar] TEXT+1 \(url) \(AppViewModel.visibleText(after))")
                    if let shapes = AppViewModel.drawList(after) {
                        print("[mar] SHAPES+1 \(url) \(shapes)")
                    }
                }
            }
        }
        print("[mar] ROUTE SMOKE DONE")
    }

    /// The first canvas on the screen, as a canonical string, or nil when
    /// there is no canvas.
    ///
    /// Canonical, not pretty: the web half prints the same shape list the same
    /// way, and the two strings are compared character for character. Which is
    /// why this dumps everything it is given instead of deciding what matters
    ///: "what matters" is a judgement, and two judgements drift. There is no
    /// judgement here, only structure.
    private static func drawList(_ view: MarView) -> String? {
        if view.tag == "canvas",
           let shapes = view.attrs.first(where: { $0.name == "shapes" })?.value {
            return dumpValue(shapes)
        }
        for child in view.children {
            if let found = drawList(child) { return found }
        }
        // A view can also carry views inside its attributes (a section's
        // header, a toolbar's items), and a canvas is legal there too.
        for attr in view.attrs {
            if case .view(let v) = attr.value, let found = drawList(v) { return found }
        }
        return nil
    }

    /// Two runtimes, two spellings for the same value, and neither is visible
    /// to a program: a colour is one constructor with three or four arguments
    /// here and two constructors on the web, and the canvas text shape is
    /// registered under a different name on each side. Both are internal
    /// bookkeeping, a Mar program cannot pattern-match a Shape, so they are
    /// folded together HERE rather than silently, and the same folding is
    /// spelled out in the web driver.
    ///
    /// Anything else keeps its own tag, minus the module prefix (Canvas.Add
    /// here, Add there).
    private static func canonicalTag(_ tag: String) -> String {
        let bare = tag.contains(".") ? String(tag.split(separator: ".").last!) : tag
        switch bare {
        case "rgb", "rgba", "__Color": return "color"
        case "canvasText": return "text"
        default: return bare
        }
    }

    /// tag(arg,arg) for constructors, [a,b] for lists, literals bare. The
    /// format is arbitrary; being identical on both platforms is the point.
    private static func dumpValue(_ v: MarValue) -> String {
        switch v {
        case .int(let n):
            return String(n)
        case .string(let s):
            return "\"\(s)\""
        case .bool(let b):
            return b ? "true" : "false"
        case .unit:
            return "()"
        case .list(let xs):
            return "[" + xs.map(dumpValue).joined(separator: ",") + "]"
        case .tuple(let xs):
            return "(" + xs.map(dumpValue).joined(separator: ",") + ")"
        case .angle(let deci):
            // Deci-degrees, matching webdriver.go: the resolution is
            // normative, so an angle off by a tenth reads as a difference.
            return "\(deci)ddeg"
        case .ctor(let tag, let args, _):
            let name = canonicalTag(tag)
            if args.isEmpty { return name }
            return name + "(" + args.map(dumpValue).joined(separator: ",") + ")"
        default:
            // Named rather than skipped: a value shape neither side expected
            // should look like a difference, not like agreement.
            return "?" + Eval.typeOf(v)
        }
    }

    /// Drives one navigation script against the LIVE app and prints what each
    /// step left on screen. Off unless MAR_NAV_LIFECYCLE names a script.
    ///
    /// This is the iOS half of the navigation-lifecycle check that
    /// internal/jsserve/nav_lifecycle_test.go runs against runtime.js: same
    /// program (navfixture.Source), same steps, same expected strings. Until
    /// it existed, ADR-0009 was verified on the web by a test and on iOS by a
    /// person clicking, which is not a thing you can put in a suite.
    ///
    /// It has to run in a launched app, and that is not laziness about the
    /// setup. On iOS nothing in the runtime tracks the navigation stack: each
    /// entry of the NavigationStack builds its own MarPageHost whose @State
    /// gives it its own PageRuntime, and popping destroys that host while the
    /// one underneath was never torn down. The rule "a push re-inits and Back
    /// restores" IS SwiftUI's view identity. A harness that stood in for
    /// SwiftUI would be testing the stand-in.
    ///
    /// So the driver only pokes and reads: it moves the app through
    /// AppContext: the same calls Nav.push, Nav.replace and a Back gesture
    /// make: waits for SwiftUI to settle, and reports what the mounted pages
    /// are showing.
    /// The app reloads its program in the background right after cold start,
    /// and the driver must not run twice against the same launch.
    private static var navLifecycleStarted = false

    @MainActor
    private static func navLifecycle(_ script: String) async {
        let ctx = AppContext.shared

        // SwiftUI rebuilds on the run loop, so every step has to hand control
        // back before the next read. This is a wait, not a poll, because the
        // thing being waited on (mount/unmount having run) is exactly what is
        // under test: polling until it looked right would hide a page that
        // mounts late.
        func settle() async {
            try? await Task.sleep(nanoseconds: 300_000_000)
        }

        // The counter a page is showing, read from the Mar view the way the
        // renderer would. "-" means nothing is there, which is a real answer:
        // it is what a dismissed sheet should report.
        // The fixture's page labels. Named once so adding a page to the
        // fixture cannot silently start reporting "?" for it, which is how
        // the nested-sheet case first read as a runtime bug.
        let parity: Set<Character> = ["A", "B", "S", "N"]
        func counter(_ runtime: PageRuntime?) -> String {
            guard let runtime, let view = runtime.currentView() else { return "-" }
            for piece in visibleText(view).components(separatedBy: " ¦ ") {
                let parts = piece.components(separatedBy: "=")
                if parts.count == 2, let head = parts.first?.first,
                   parity.contains(head), Int(parts[1]) != nil {
                    return piece
                }
            }
            return "?"
        }

        // The live page for a route: the most recent mounted runtime with
        // that path.
        //
        // Not "the last thing that mounted", which is the obvious reading and
        // is wrong. A pop destroys the pushed host, but `onDisappear`, and so
        // `unmount`: lands only after the pop animation finishes, so for a
        // few hundred milliseconds the page being dismissed is still the most
        // recently mounted one. Reading it made Back look like it had shown
        // the wrong screen when the app was doing the right thing.
        //
        // Asking by route removes the race instead of sleeping past it: the
        // answer is a function of navPath, which moves synchronously. It keeps
        // every failure the naive version could catch: a Back that re-inits
        // finds a NEW runtime at that path (counter 0), and a push that reused
        // a model finds the OLD one (counter still 2).
        //
        // Most recent, not first: after /a → /b → /a two runtimes answer to
        // "/a", and the one on top is the one the user is looking at.
        func live(_ path: String?) -> PageRuntime? {
            guard let path else { return nil }
            return MarLiveRuntimes.stack.last { $0.path == path }
        }

        // A pushed stack keeps the screen underneath mounted too, so "more
        // than one page is alive" does NOT mean one is presented.
        //
        // And the route table cannot answer it either: asking it whether the
        // top path is a sheet route only says what the shell was SUPPOSED to
        // do. A shell that resolved the route correctly and then pushed it
        // would pass that check while showing the wrong thing (found by
        // sabotage, not by review). So the answer comes from the shell: it
        // publishes what it presented as it renders.
        func presented() -> PageRuntime? {
            guard let top = MarPresentation.current else { return nil }
            return live(top)
        }

        func topRuntime() -> PageRuntime? { live(ctx.navPath.last) }

        // One tap on whatever the screen offers first: the same thing the
        // web driver does by clicking the button it finds by label.
        func bump() {
            guard let runtime = topRuntime(), let view = runtime.currentView(),
                  let msg = firstMessage(view) else { return }
            runtime.dispatch(msg)
        }

        var seen: [String] = []
        func note(_ label: String) {
            seen.append("\(label):\(counter(topRuntime()))")
        }
        // Both surfaces, every step. Reading only the top would pass on a
        // runtime that refused to present at all, and reading only the sheet
        // would miss the covered page being wiped.
        func noteBoth(_ label: String) {
            let root = counter(MarLiveRuntimes.stack.first)
            let sheet = presented().map { counter($0) } ?? "-"
            seen.append("\(label):root=\(root),sheet=\(sheet)")
        }

        await settle()

        switch script {
        case "push":
            note("mount")
            bump(); bump()
            await settle()
            note("bumped")

            ctx.navigate(path: "/b", replace: false)
            await settle()
            note("push-b")

            // What the Back button and the swipe-back gesture both do: drop
            // the top entry. StackShell's binding writes exactly this.
            ctx.navPath.removeLast()
            await settle()
            note("back-a")

            ctx.navigate(path: "/b", replace: false)
            await settle()
            ctx.navigate(path: "/a", replace: false)
            await settle()
            note("push-a")

        case "replace":
            bump(); bump()
            await settle()
            seen.append(counter(topRuntime()))
            ctx.navigate(path: "/b", replace: true)
            await settle()
            seen.append(counter(topRuntime()))

        case "sheet":
            bump(); bump()
            await settle()
            noteBoth("start")

            ctx.navigate(path: "/s", replace: false)
            await settle()
            noteBoth("presented")

            ctx.navPath.removeLast()
            await settle()
            noteBoth("dismissed")

            ctx.navigate(path: "/s", replace: false)
            await settle()
            noteBoth("reopened")

        case "sheet-cold", "sheet-cold-nested":
            // Seeded before the first render (see runProgramSync), so this is
            // what a shared link or a relaunch lands on.
            noteBoth("cold")

        default:
            print("[mar] NAV UNKNOWN SCRIPT \(script)")
            return
        }

        print("[mar] NAV \(seen.joined(separator: " "))")
        print("[mar] NAV DONE")
    }

    /// The screen's visible strings, deduplicated and sorted.
    ///
    /// A SET, not the tree: SwiftUI and the DOM nest differently and repeat
    /// text in different places (a nav title appears once here and twice
    /// there), so comparing structure would report differences that no user
    /// could see. What a user CAN see is which words are on the screen, and
    /// that has to be identical on both platforms.
    private static func visibleText(_ view: MarView) -> String {
        var out = Set<String>()
        func add(_ s: String) {
            let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
            if !t.isEmpty { out.insert(t) }
        }
        // Attributes carry three different kinds of thing, and only two of
        // them are on screen:
        //
        //   views  (a toolbar's leading/trailing items): recurse
        //   lists  (a picker's `options`)               - recurse
        //   plain strings: ONLY the few that render as text
        //
        // Both halves of this had to be learned from a failed run. Reading
        // just a whitelist of names missed pickers and toolbars; reading
        // every string instead pulled in `href`, `src`, `alt` and `align`,
        // which no one sees and which the DOM does not put in textContent
        // either. Either mistake reports a difference that isn't one.
        let textAttrs: Set<String> = ["navigationTitle", "header", "footer", "placeholder"]
        func walkValue(_ v: MarValue, named name: String) {
            switch v {
            case .string(let s): if textAttrs.contains(name) { add(s) }
            case .list(let xs): xs.forEach { walkValue($0, named: name) }
            case .view(let inner): walk(inner)
            default: break
            }
        }
        func walk(_ v: MarView) {
            add(v.text)
            // A picker shows `toLabel option`, never the option itself: the
            // options can be Ints or custom-type ctors with no text in them at
            // all. Reading the raw list found nothing on screens where the web
            // (which renders the label into each <option>) found nineteen
            // labels. So apply the same function the renderer applies.
            if v.tag == "picker",
               let options = v.attrs.first(where: { $0.name == "options" })?.value,
               let toLabel = v.attrs.first(where: { $0.name == "toLabel" })?.value,
               case .list(let opts) = options {
                for opt in opts {
                    if let labeled = try? Eval.apply(toLabel, opt),
                       case .string(let s) = labeled { add(s) }
                }
            }
            for attr in v.attrs { walkValue(attr.value, named: attr.name) }
            v.children.forEach(walk)
        }
        walk(view)
        return out.sorted().joined(separator: " ¦ ")
    }

    /// The first BUTTON's message, in tree order.
    ///
    /// Buttons specifically, not "anything tappable", because the web half of
    /// this comparison has to pick the same thing and a DOM node cannot say
    /// whether a tap sends a message or follows a link. Both sides say "the
    /// first button", a definition that means the same on each, so a
    /// difference in the result is a difference in the runtimes.
    private static func firstMessage(_ view: MarView) -> MarValue? {
        // A disabled button is skipped, because the web half cannot press one
        // either: there the click lands on a disabled DOM node and does
        // nothing. Dispatching its msg here goes around the UI entirely and
        // reports a difference: the showcase's "tap is ignored" demo counted
        // up on iOS and stayed put on the web for exactly this reason.
        let disabled = view.attrs.contains { attr in
            guard attr.name == "disabled" else { return false }
            if case .bool(let b) = attr.value { return b }
            return true
        }
        if view.tag == "button", !disabled, let msg = view.msg, !isFunction(msg) { return msg }
        for child in view.children {
            if let found = firstMessage(child) { return found }
        }
        return nil
    }

    /// A textField's `msg` is a FUNCTION of the typed string, not a message.
    /// Dispatching it would hand `update` a closure and prove nothing.
    private static func isFunction(_ v: MarValue) -> Bool {
        if case .fn = v { return true }
        return false
    }
    #endif

    /// Reads the embedded program.json from Bundle.main if present.
    /// Returns nil when scaffolds without Resources/program.json are
    /// running (older builds or corrupt installs): callers must
    /// gracefully fall through to the network path.
    private static func loadEmbeddedProgram() -> Data? {
        guard let url = Bundle.main.url(forResource: "program", withExtension: "json") else {
            return nil
        }
        return try? Data(contentsOf: url)
    }

    /// The baked Info.plist `MarRuntimeVersion`: the mar that generated
    /// this app, and therefore the version of the Swift runtime inside
    /// the binary. nil disables the compatibility check entirely, and
    /// two cases produce nil on purpose:
    ///
    ///   - the key is missing (scaffold generated before it existed);
    ///   - the value is "dev", which is what an unstamped local build
    ///     gets. During development the app and the server are rebuilt
    ///     from the same tree constantly and a version string would
    ///     only get in the way; the whole point of the check is the
    ///     shipped app whose runtime is frozen in the App Store.
    private static func bakedRuntimeVersion() -> String? {
        guard let v = Bundle.main.object(forInfoDictionaryKey: "MarRuntimeVersion") as? String,
              !v.isEmpty, v != "dev" else {
            return nil
        }
        return v
    }

    /// The baked Info.plist `MarBaseURL`, with a localhost fallback so
    /// the app still launches if someone shipped without setting one
    /// (the build emits a Warn in that case; this is just defensive).
    private static func bakedBaseURL() -> String {
        if let baked = Bundle.main.object(forInfoDictionaryKey: "MarBaseURL") as? String,
           !baked.isEmpty {
            return baked
        }
        return "http://localhost:3000"
    }

    #if DEBUG
    /// The baked Info.plist `MarAppName`: the raw mar.json `name`,
    /// which is exactly what `mar dev` advertises as its mDNS instance
    /// name. Empty on scaffolds generated before the key existed:
    /// those keep the historical accept-any behavior.
    private static func bakedAppName() -> String {
        (Bundle.main.object(forInfoDictionaryKey: "MarAppName") as? String) ?? ""
    }

    /// Names of foreign dev servers we already warned about, so the
    /// console line prints once per server instead of once per
    /// Bonjour churn event.
    @ObservationIgnored
    private var ignoredDevServers: Set<String> = []

    private func maybeAutoPick() {
        let expected = AppViewModel.bakedAppName()

        // Only adopt a dev server advertising THIS app's name.
        // Bonjour renames colliding instances to "name (2)", so accept
        // that suffix too.
        let matches: (DiscoveredServer) -> Bool = { s in
            if expected.isEmpty { return true }
            return s.name == expected || s.name.hasPrefix(expected + " (")
        }

        for s in discovery.servers where s.url != nil && !matches(s) {
            if ignoredDevServers.insert(s.name).inserted {
                print("[mar] ignoring mar dev \"\(s.name)\" on the LAN — this app is \"\(expected)\"")
            }
        }

        guard let picked = discovery.servers.first(where: { $0.url != nil && matches($0) }),
              let url = picked.url else { return }
        let s = url.absoluteString
        guard s != baseURLString else { return }
        baseURLString = s
        devOverrideLabel = "\(picked.host):\(picked.port)"
        print("[mar] DEBUG: connected to mar dev at \(s) — the baked server URL is NOT being used")
        Task {
            await api.setBaseURL(url)
            MarDispatcher.shared.baseURL = url
            await loadAll()
        }
    }
    #endif

    func loadAll() async {
        // If we already have a program loaded (typically from the
        // embedded snapshot, but also from a previous successful
        // fetch), this fetch is a *refresh*: don't flash the
        // loading screen. Failure stays silent: keep showing what we
        // already have rather than wiping it for an offline user.
        let hadProgram = !pages.isEmpty
        if !hadProgram {
            state = .loading
        }
        do {
            let fetched = try await api.fetchProgram()

            // THE AST IS DATA; THE BUILTINS IT NAMES ARE NOT.
            //
            // Over-the-air update means the program travels and the
            // Swift runtime does not: it is compiled into this binary
            // and can only change through the App Store. So a program
            // built by a different mar can name a builtin this app has
            // never heard of, and the failure would land far from here:
            // an `unbound name` thrown mid-view, on some screen the user
            // happened to open, presented as a crash-shaped runtime
            // failure with nothing actionable in it.
            //
            // Refusing up front turns that into one honest sentence.
            // The app keeps running the program it already has: the
            // embedded snapshot from the .ipa, or whatever it fetched
            // before, which is by construction a program this binary
            // CAN run.
            //
            // Deliberately an exact-match check rather than semver
            // ordering: the question is not "is the server newer" but
            // "were these two built together". A server older than the
            // app is just as capable of mismatching (the app may name a
            // builtin the older server's compiler emitted differently),
            // and inventing a compatibility range would mean promising
            // one we do not test.
            if let serverRuntime = fetched.runtime,
               let ours = AppViewModel.bakedRuntimeVersion(),
               serverRuntime != ours {
                incompatibleServerRuntime = serverRuntime
                print("[mar] refusing program from server running mar \(serverRuntime); "
                      + "this app was built with \(ours). Update the app.")
                if !hadProgram {
                    state = .failed(
                        "This app was built with mar \(ours) but the server is running mar "
                        + "\(serverRuntime). Update the app to continue.")
                }
                return
            }
            // Either the versions match or one side declined to say.
            // Both are "carry on": a missing header is what an older
            // server or a header-stripping proxy looks like, and
            // refusing on silence would brick every app pointed at one.
            incompatibleServerRuntime = nil

            try runProgramSync(fetched.data)
            state = .loaded
        } catch {
            if hadProgram {
                #if DEBUG
                print("[mar] background refresh failed; keeping current program: \(error)")
                #endif
                return
            }
            let msg = (error as? APIError)?.errorDescription
                ?? (error as? MarRuntimeError)?.errorDescription
                ?? error.localizedDescription
            state = .failed(msg)
        }
    }

    /// Decode + execute the user's mar program. Side-effect: fills
    /// `pages` with whatever `main` captured via App.frontend /
    /// App.fullstack. Synchronous because the body is all CPU work
    /// (decode + interpreter eval): the async wrapper is kept on
    /// the public loadAll caller for the network fetch.
    ///
    /// Cheap to call repeatedly: when `data` matches the bytes from
    /// the last successful load, we no-op entirely. This matters
    /// because the cold-start flow loads the embedded snapshot AND
    /// fires a background fetch of /_mar/program.json: the two are
    /// usually identical, and re-running main on identical bytes
    /// would needlessly tear down the user's navigation / auth
    /// state (currentPath, currentUser, etc. in AppContext).
    private func runProgramSync(_ data: Data) throws {
        if let prev = lastLoadedProgramBytes, prev == data {
            return
        }
        let program = try MarJSONCodec.decodeProgram(data)
        // First load (no pages yet) gets a clean slate so we don't
        // inherit stale state from a corrupted previous attempt.
        // Subsequent loads preserve navigation + auth state: losing
        // them on every background refresh would bounce a
        // mid-session user back to the sign-in screen.
        let isInitialLoad = AppContext.shared.pages.isEmpty
        if isInitialLoad {
            AppContext.shared.reset()
        }
        // Auth metadata from the server-resolved Auth.config: the
        // mobile bundle doesn't include Main.mar, so the JS+Swift
        // runtimes can't run `auth = Auth.config { ... }` themselves.
        if !program.authSignInPath.isEmpty {
            AppContext.shared.signInPath = program.authSignInPath
        }
        // Shared stores are keyed by the order their `App.shared` calls are
        // evaluated, so the counter has to start over with the program. Skip
        // this and the background refresh that follows every cold start mints
        // `shared:1` for the same def: the pages decoded from the new program
        // bind to a fresh empty store while the screen already on the stack
        // still reads the old one, and the two silently disagree. The MODELS
        // are preserved across the reload: a refresh should no more empty the
        // cart than a `mar dev` save should.
        MarSharedRegistry.beginProgramLoad()
        let env = MarBuiltins.makeEnv()
        for module in program.modules {
            try MarLoader.load(module: module, into: env)
        }

        // Resolve the entry: typically `main`. The Go side stamps
        // entry as "main" or the synthetic "__entry" depending on
        // load path. Try both.
        let entry: MarValue
        if let v = env.lookup(program.entry) {
            entry = v
        } else if let v = env.lookup("main") {
            entry = v
        } else {
            throw MarRuntimeError.message("entry not found: \(program.entry)")
        }

        guard case .effect(let eff) = entry else {
            throw MarRuntimeError.message("entry is not an Effect")
        }
        // Run main: captures pages into AppContext.shared.
        _ = try eff.run()

        let decoded = AppContext.shared.decodedPages()
        #if DEBUG
        if ProcessInfo.processInfo.environment["MAR_ROUTE_SMOKE"] == "1" {
            AppViewModel.routeSmoke(decoded)
        }
        #endif
        self.pages = decoded
        // Seed the navigation stack only on the initial load. On a
        // refresh (program changed but user was already navigating),
        // keep wherever they are, but if the user's current top-of-
        // stack no longer exists in the new program, reset to a
        // sensible root so the renderer doesn't blank out on a
        // missing match.
        if isInitialLoad {
            if let first = decoded.first {
                AppContext.shared.seedRoot(first.path, pages: decoded)
            }
        } else {
            let current = AppContext.shared.currentPath
            let stillExists = decoded.contains { $0.matchURL(current) != nil }
            if !stillExists, let first = decoded.first {
                AppContext.shared.navigate(path: first.path, replace: true)
            }
        }
        #if DEBUG
        if let script = ProcessInfo.processInfo.environment["MAR_NAV_LIFECYCLE"],
           !script.isEmpty, !AppViewModel.navLifecycleStarted {
            AppViewModel.navLifecycleStarted = true
            // Landing on the sheet route has to happen BEFORE the first
            // render, or it is a push with a page underneath rather than the
            // cold load the fallback is about. This runs while the shell is
            // still waiting on `state == .loaded`, so the stack it first sees
            // is already the deep-linked one.
            // Land on a presented route the way a shared link would: through
            // the same seeding the app uses, so the parent it should be
            // presented over comes along.
            if script == "sheet-cold" {
                AppContext.shared.navPath = AppContext.stackFor("/s", pages: decoded)
            }
            if script == "sheet-cold-nested" {
                AppContext.shared.navPath = AppContext.stackFor("/a/nested", pages: decoded)
            }
            MarLiveRuntimes.reset()
            Task { @MainActor in
                await AppViewModel.navLifecycle(script)
            }
        }
        #endif
        lastLoadedProgramBytes = data
    }
}
