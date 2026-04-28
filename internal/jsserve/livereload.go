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
	hasAPI      bool // true when serving as full-stack (route /api/* through dispatchBackend)
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

// ReloadHub broadcasts "the program changed, reload" events to all
// connected browser tabs over Server-Sent Events. One channel per
// connected client; broadcast is non-blocking (fire-and-forget).
type ReloadHub struct {
	mu      sync.Mutex
	clients map[chan struct{}]struct{}
}

func NewReloadHub() *ReloadHub {
	return &ReloadHub{clients: map[chan struct{}]struct{}{}}
}

func (h *ReloadHub) subscribe() chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := make(chan struct{}, 1)
	h.clients[c] = struct{}{}
	return c
}

func (h *ReloadHub) unsubscribe(c chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	close(c)
}

func (h *ReloadHub) Broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c <- struct{}{}:
		default:
			// Client's buffer is full — they'll catch the next event.
		}
	}
}

// ServeReload is an HTTP handler for SSE reload events. Browsers connect
// once and stay connected; on each Broadcast they get a "data: reload\n\n"
// line which the runtime.js client interprets as "tear down and remount."
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

	// Initial comment to flush headers and confirm the connection is open.
	if _, err := io.WriteString(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	for {
		select {
		case _, ok := <-c:
			if !ok {
				return
			}
			if _, err := io.WriteString(w, "data: reload\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
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
