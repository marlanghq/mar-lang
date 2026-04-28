package jsserve

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	title       string
	entry       string
	hasAPI      bool   // true when serving as full-stack (route /api/* through dispatchBackend)
	lastError   string // most recent compile error; "" when last compile succeeded
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

// Update replaces the served AST + routes + entry atomically. Pass nil
// for routes when not serving an API (browser-only mode).
func (lp *LiveProgram) Update(routes []runtime.Value, mods []*ast.Module, entry string) error {
	progJSON, err := makeProgramJSON(mods, entry)
	if err != nil {
		return err
	}
	title := "mar app"
	if len(mods) > 0 && len(mods[len(mods)-1].Name) > 0 {
		nm := mods[len(mods)-1].Name
		title = nm[len(nm)-1]
	}
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.apiRoutes = routes
	lp.programJSON = progJSON
	lp.title = title
	lp.entry = entry
	lp.hasAPI = routes != nil
	return nil
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
// JSON-escaped so newlines and quotes survive the wire.
func jsonError(msg string) string {
	b, err := json.Marshal(map[string]string{"type": "error", "message": msg})
	if err != nil {
		return `{"type":"error","message":"<encoding failed>"}`
	}
	return string(b)
}

func makeProgramJSON(mods []*ast.Module, entry string) ([]byte, error) {
	if len(mods) == 0 {
		return nil, fmt.Errorf("no modules to serialize")
	}
	merged := mergeModules(mods)
	return json.Marshal(map[string]any{
		"module": SerializeModule(merged),
		"entry":  entry,
	})
}
