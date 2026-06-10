package jsserve

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"

	"mar/internal/ast"
	"mar/internal/runtime"
)

// LiveProgram holds the program state currently being served. Mutated
// atomically when hot-reload swaps in freshly compiled output. The HTTP
// handlers read from it on every request, so a swap doesn't need to
// restart the server.
type LiveProgram struct {
	mu          sync.RWMutex
	port        int
	apiRoutes   []runtime.Value
	programJSON []byte
	programHash string // cached sha256 prefix of programJSON; recomputed on Update()
	title       string
	entry       string
	appName     string // mar.json `name` — preferred over title for native clients
	hasAPI      bool   // true when serving as full-stack (route /api/* through dispatchBackend)
	lastError   string // most recent compile error; "" when last compile succeeded
	devMode     bool   // false = production stub: no SSE, no dev banner, no time-travel panel
}

// SetAppName records the project's canonical name (from mar.json
// `name`). Preferred over Title() for surfaces a human consumes
// directly (mDNS instance name, dev banner) — title follows the
// last loaded module which can be surprising in fullstack projects
// whose entry chains through Frontend.
func (lp *LiveProgram) SetAppName(name string) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.appName = name
}

// AppName returns the project's canonical name. Falls back to Title
// (last user module) when the manifest didn't supply one.
func (lp *LiveProgram) AppName() string {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	if lp.appName != "" {
		return lp.appName
	}
	return lp.title
}

// SetDevMode toggles dev affordances on the served program (dev banner,
// SSE compile-error channel, time-travel panel). Defaults to false; the
// dev server flips it to true at startup. Production runtime leaves it
// off so end users don't see a debug dock.
func (lp *LiveProgram) SetDevMode(on bool) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.devMode = on
}

// SetPort sets the listening port. Called once on first capture; ignored
// on subsequent updates so a code change can't move the server mid-session.
func (lp *LiveProgram) SetPort(p int) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	if lp.port == 0 {
		lp.port = p
	}
}

func (lp *LiveProgram) Port() int {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.port
}

// Update replaces the served AST + routes + entry atomically.
//
//   - frontend-only:  routes=nil, mods=non-empty, entry=non-empty
//   - backend-only:   routes=non-empty, mods=nil, entry=""
//   - fullstack:      routes=non-empty, mods=non-empty, entry="main"
//
// makeProgramJSON is skipped when mods is empty (backend-only doesn't
// ship a browser bundle); programJSON stays nil.
func (lp *LiveProgram) Update(routes []runtime.Value, mods []*ast.Module, entry string) error {
	var progJSON []byte
	title := "mar app"
	if len(mods) > 0 {
		var err error
		progJSON, err = makeProgramJSON(mods, entry, lp.devMode, true)
		if err != nil {
			return err
		}
		// Pick the last user-named module as the page title (the synthetic
		// __entry module that the CLI appends has Name == nil, so it's
		// skipped here).
		for i := len(mods) - 1; i >= 0; i-- {
			if nm := mods[i].Name; len(nm) > 0 {
				title = nm[len(nm)-1]
				break
			}
		}
	}
	// Cache the program hash so the version-headers middleware (which
	// runs on every request) doesn't re-sha the bytes. Recomputed only
	// here, on each Update — i.e. once per compile / live-reload swap.
	var progHash string
	if len(progJSON) > 0 {
		progHash = programETag(progJSON)
	}
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.apiRoutes = routes
	lp.programJSON = progJSON
	lp.programHash = progHash
	lp.title = title
	lp.entry = entry
	lp.hasAPI = routes != nil
	return nil
}

// ProgramHash returns the cached sha256 prefix of the served program
// JSON. Used by the X-Mar-Program header middleware so each request
// can advertise the current program identity without re-hashing.
// Empty when no program is loaded (backend-only mode).
func (lp *LiveProgram) ProgramHash() string {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.programHash
}

// HasFrontend reports whether a browser bundle has been compiled (so
// /, /_mar/runtime.js, /_mar/program.json should serve content).
// Backend-only mode leaves this false.
func (lp *LiveProgram) HasFrontend() bool {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return len(lp.programJSON) > 0
}

func (lp *LiveProgram) Routes() []runtime.Value {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.apiRoutes
}

func (lp *LiveProgram) ProgramJSON() []byte {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.programJSON
}

func (lp *LiveProgram) Title() string {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.title
}

func (lp *LiveProgram) HasAPI() bool {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.hasAPI
}

// SetError records the most recent compile error. The dev banner reads
// this and shows it to connected browsers via SSE.
func (lp *LiveProgram) SetError(msg string) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.lastError = msg
}

// ClearError marks the project as compiling cleanly again.
func (lp *LiveProgram) ClearError() {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.lastError = ""
}

func (lp *LiveProgram) LastError() string {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.lastError
}

// ReloadHub broadcasts dev events (reload / compile error / error
// cleared) to connected browser tabs via Server-Sent Events. Messages
// are JSON-encoded so the client can dispatch on type.
//
// On subscribe, the hub also resends the current LiveProgram error
// state (if any) so a tab opened mid-error sees the banner without
// having to wait for the next change.
type ReloadHub struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
	lp      *LiveProgram
}

func NewReloadHub(lp *LiveProgram) *ReloadHub {
	return &ReloadHub{clients: map[chan string]struct{}{}, lp: lp}
}

func (h *ReloadHub) subscribe() chan string {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := make(chan string, 4)
	h.clients[c] = struct{}{}
	return c
}

func (h *ReloadHub) unsubscribe(c chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	close(c)
}

// Reload tells every client to refetch and remount. Implies the error
// banner can be hidden — the new program supersedes any prior error.
func (h *ReloadHub) Reload() {
	h.broadcast(`{"type":"reload"}`)
}

// Error pushes a compile error message. Clients show it as a banner.
func (h *ReloadHub) Error(msg string) {
	h.broadcast(jsonError(msg))
}

// OK clears the error banner. Sent when a previously-failed compile
// becomes successful (and gets followed by a Reload).
func (h *ReloadHub) OK() {
	h.broadcast(`{"type":"ok"}`)
}

func (h *ReloadHub) broadcast(payload string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c <- payload:
		default:
			// Slow client — drop. Next event will catch them up.
		}
	}
}

// ServeReload is an HTTP handler for SSE events. Browsers connect once
// and stay connected.
func (h *ReloadHub) ServeReload(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	c := h.subscribe()
	defer h.unsubscribe(c)

	// Initial comment to flush headers.
	if _, err := io.WriteString(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	// Send the current error state (if any) so a tab opened during a
	// broken build sees the banner immediately.
	if h.lp != nil {
		if msg := h.lp.LastError(); msg != "" {
			if _, err := io.WriteString(w, "data: "+jsonError(msg)+"\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}

	for {
		select {
		case payload, ok := <-c:
			if !ok {
				return
			}
			if _, err := io.WriteString(w, "data: "+payload+"\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// jsonError builds the SSE payload for a compile error. The message is
// JSON-escaped so newlines and quotes survive the wire, and any ANSI
// escape codes from the terminal-flavored formatter are stripped — the
// browser overlay renders text, not VT100 sequences, so leaving them
// in produced garbled output like "[1;31mType error:[0m argument ...".
func jsonError(msg string) string {
	b, err := json.Marshal(map[string]string{"type": "error", "message": stripANSI(msg)})
	if err != nil {
		return `{"type":"error","message":"<encoding failed>"}`
	}
	return string(b)
}

// stripANSI removes ANSI CSI escape sequences (color, cursor moves,
// SGR resets, etc.) from a string. We see them on the wire because
// diag.Format renders for a TTY when stderr is interactive, and the
// dev-mode `mar` process always has a TTY on stderr — even when the
// SAME string is also being shipped to the browser via SSE, where ANSI
// is just noise.
//
// The pattern covers the CSI family `ESC [ ... <final byte>` which
// is what diag uses (\x1b[1;31m, \x1b[0m, \x1b[38;5;245m). Other
// escape forms (OSC, single-char escapes) aren't generated by our
// formatters; leaving them in costs nothing if they ever appear.
var ansiCSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripANSI(s string) string {
	return ansiCSI.ReplaceAllString(s, "")
}

// bakeAuth controls whether the user app's resolved Auth.config (signInPath)
// is threaded into the bundle. The user app needs it (Page.protected's expiry
// redirect); the admin panel must NOT inherit it — it's a separate program
// with its own (admin) auth, and inheriting the user signInPath makes a 401 on
// a Mar.Admin.* call try to redirect to the user sign-in.
func makeProgramJSON(mods []*ast.Module, entry string, devMode bool, bakeAuth bool) ([]byte, error) {
	if len(mods) == 0 {
		return nil, fmt.Errorf("no modules to serialize")
	}
	// Send modules separately so the runtime registers each decl
	// under both its bare name and a qualified `Module.name` form.
	// Merging them into one module silently overwrote same-named
	// decls across modules — `Frontend.SignIn.page` and
	// `Frontend.Home.page` collapsed into one, breaking multi-page
	// auth-protected apps.
	serialized := make([]any, 0, len(mods))
	for _, m := range mods {
		serialized = append(serialized, SerializeModule(m))
	}
	out := map[string]any{
		"modules": serialized,
		"entry":   entry,
		"devMode": devMode,
	}
	// Bake auth metadata that Page.protected needs on the client
	// (signInPath) directly into the bundle. Main.mar isn't in the
	// browser bundle (only modules reachable from the page list are),
	// so the client can't run `auth = Auth.config { ... }` itself —
	// we hand the resolved values off via this side channel instead.
	if cfg := runtime.CurrentAuth(); bakeAuth && cfg != nil {
		auth := map[string]any{}
		if cfg.SignInPath != "" {
			auth["signInPath"] = cfg.SignInPath
		}
		if len(auth) > 0 {
			out["auth"] = auth
		}
	}
	return json.Marshal(out)
}
