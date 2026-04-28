package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// VApp packages an MVU program (init + update + view) into a runnable value.
type VApp struct {
	InitFn   Value
	UpdateFn Value
	ViewFn   Value
}

func (VApp) isValue() {}
func (a VApp) Display() string { return "<app>" }

// VScreen packages a single screen (path + MVU functions) for multi-screen apps.
type VScreen struct {
	Path     string
	InitFn   Value
	UpdateFn Value
	ViewFn   Value
}

func (VScreen) isValue() {}
func (s VScreen) Display() string {
	return fmt.Sprintf("<screen:%s>", s.Path)
}

// appBuiltins exposes the App API.
//
//	App.create : init -> update -> view -> App
//	App.serve  : Int -> App -> Effect String ()
//
// The MVU program is stateful per browser session (cookie-based). On each
// HTTP request:
//   - GET  /            -> render view of current model.
//   - POST /__msg       -> apply update with the named msg, then 303 to /.
//
// MVP shape:
//   - init   : () -> Model  (no Effect — we don't run init effects yet)
//   - update : Msg -> Model -> Model
//   - view   : Model -> View
//
// Buttons in the view emit msgs by being rendered as forms posting to /__msg.
// The view runtime walks the View tree at render time, replacing buttons with
// HTML forms. Msg encoding: a constructor name plus stringified args.
//
// This is server-rendered + form-based interaction; no JS required.
func appBuiltins() map[string]Value {
	return map[string]Value{
		"appCreate": nativeFn(3, func(args []Value) (Value, error) {
			return VApp{InitFn: args[0], UpdateFn: args[1], ViewFn: args[2]}, nil
		}),
		"appServe": nativeFn(2, appServeImpl),

		// Screen.create : String -> initFn -> updateFn -> viewFn -> Screen
		"screenCreate": nativeFn(4, func(args []Value) (Value, error) {
			path, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("Screen.create: expected String path")
			}
			return VScreen{
				Path:     path.V,
				InitFn:   args[1],
				UpdateFn: args[2],
				ViewFn:   args[3],
			}, nil
		}),
		// App.serveScreens : Int -> List Screen -> Effect String ()
		"appServeScreens": nativeFn(2, appServeScreensImpl),
	}
}

// session stores the current model for a single browser session.
type session struct {
	model Value
}

func appServeImpl(args []Value) (Value, error) {
	portV, ok1 := args[0].(VInt)
	app, ok2 := args[1].(VApp)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("App.serve: expected Int port and App")
	}
	port := int(portV.V)

	var (
		mu       sync.Mutex
		sessions = map[string]*session{}
	)

	getSession := func(req *http.Request, w http.ResponseWriter) *session {
		var sid string
		if c, err := req.Cookie("mar_session"); err == nil {
			sid = c.Value
		}
		mu.Lock()
		defer mu.Unlock()
		if sid != "" {
			if s, ok := sessions[sid]; ok {
				return s
			}
		}
		// New session: generate id, run init.
		buf := make([]byte, 16)
		_, _ = rand.Read(buf)
		sid = hex.EncodeToString(buf)
		http.SetCookie(w, &http.Cookie{Name: "mar_session", Value: sid, Path: "/"})

		// init() -> Model
		initVal, err := apply(app.InitFn, VUnit{})
		if err != nil {
			return &session{model: VUnit{}}
		}
		s := &session{model: initVal}
		sessions[sid] = s
		return s
	}

	return VEffect{
		Tag: "appServe",
		Run: func() (Value, error) {
			mux := http.NewServeMux()
			mux.HandleFunc("/__msg", func(w http.ResponseWriter, req *http.Request) {
				if req.Method != "POST" {
					http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
					return
				}
				_ = req.ParseForm()
				msgName := req.FormValue("msg")
				if msgName == "" {
					http.Error(w, "missing msg", http.StatusBadRequest)
					return
				}
				// Build the Msg value. If the form has additional fields,
				// pack them into a record and wrap with the constructor.
				var msgVal Value = VCtor{Tag: msgName}
				extras := map[string]Value{}
				var order []string
				for k, vs := range req.PostForm {
					if k == "msg" || len(vs) == 0 {
						continue
					}
					extras[k] = VString{V: vs[0]}
					order = append(order, k)
				}
				if len(extras) > 0 {
					msgVal = VCtor{
						Tag:  msgName,
						Args: []Value{VRecord{Fields: extras, Order: order}},
					}
				}
				s := getSession(req, w)
				mu.Lock()
				newModel, err := apply(app.UpdateFn, msgVal)
				if err != nil {
					mu.Unlock()
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				newModel, err = apply(newModel, s.model)
				if err != nil {
					mu.Unlock()
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				s.model = newModel
				mu.Unlock()
				http.Redirect(w, req, "/", http.StatusSeeOther)
			})

			mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
				s := getSession(req, w)
				mu.Lock()
				vVal, err := apply(app.ViewFn, s.model)
				mu.Unlock()
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				v, ok := vVal.(VView)
				if !ok {
					http.Error(w, "view did not return a View", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = io.WriteString(w, "<!doctype html><html><body>")
				renderInteractiveHTML(w, v)
				_, _ = io.WriteString(w, "</body></html>")
			})

			fmt.Printf("[mar] App listening on :%d\n", port)
			err := http.ListenAndServe(fmt.Sprintf(":%d", port), mux)
			return VUnit{}, err
		},
	}, nil
}

// appServeScreensImpl serves multiple MVU screens, each at its own path.
//
// Per browser session, each screen has its own model (lazy-initialized on
// first visit). Buttons in a screen post msgs to /__msg/<path> so the
// runtime knows which screen's update to call.
func appServeScreensImpl(args []Value) (Value, error) {
	portV, ok1 := args[0].(VInt)
	listV, ok2 := args[1].(VList)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("App.serveScreens: expected Int port and List Screen")
	}
	port := int(portV.V)

	screens := map[string]VScreen{} // path -> screen
	var paths []string
	for _, v := range listV.Elements {
		s, ok := v.(VScreen)
		if !ok {
			return nil, fmt.Errorf("App.serveScreens: list element not a Screen")
		}
		screens[s.Path] = s
		paths = append(paths, s.Path)
	}

	type sessionScreens struct {
		models map[string]Value // path -> model
	}
	var (
		mu       sync.Mutex
		sessions = map[string]*sessionScreens{}
	)

	getOrInitScreenModel := func(s VScreen, sess *sessionScreens) Value {
		if m, ok := sess.models[s.Path]; ok {
			return m
		}
		m, err := apply(s.InitFn, VUnit{})
		if err != nil {
			m = VUnit{}
		}
		sess.models[s.Path] = m
		return m
	}

	getSession := func(req *http.Request, w http.ResponseWriter) *sessionScreens {
		var sid string
		if c, err := req.Cookie("mar_session"); err == nil {
			sid = c.Value
		}
		if sid != "" {
			if s, ok := sessions[sid]; ok {
				return s
			}
		}
		buf := make([]byte, 16)
		_, _ = rand.Read(buf)
		sid = hex.EncodeToString(buf)
		http.SetCookie(w, &http.Cookie{Name: "mar_session", Value: sid, Path: "/"})
		s := &sessionScreens{models: map[string]Value{}}
		sessions[sid] = s
		return s
	}

	return VEffect{
		Tag: "appServeScreens",
		Run: func() (Value, error) {
			mux := http.NewServeMux()

			// /__msg/<path>: dispatch msg to the screen at <path>
			mux.HandleFunc("/__msg/", func(w http.ResponseWriter, req *http.Request) {
				if req.Method != "POST" {
					http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
					return
				}
				screenPath := strings.TrimPrefix(req.URL.Path, "/__msg")
				screen, ok := screens[screenPath]
				if !ok {
					http.NotFound(w, req)
					return
				}
				_ = req.ParseForm()
				msgName := req.FormValue("msg")
				if msgName == "" {
					http.Error(w, "missing msg", http.StatusBadRequest)
					return
				}
				var msgVal Value = VCtor{Tag: msgName}
				extras := map[string]Value{}
				var order []string
				for k, vs := range req.PostForm {
					if k == "msg" || len(vs) == 0 {
						continue
					}
					extras[k] = VString{V: vs[0]}
					order = append(order, k)
				}
				if len(extras) > 0 {
					msgVal = VCtor{Tag: msgName, Args: []Value{VRecord{Fields: extras, Order: order}}}
				}

				mu.Lock()
				sess := getSession(req, w)
				model := getOrInitScreenModel(screen, sess)
				newModel, err := apply(screen.UpdateFn, msgVal)
				if err != nil {
					mu.Unlock()
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				newModel, err = apply(newModel, model)
				if err != nil {
					mu.Unlock()
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				sess.models[screenPath] = newModel
				mu.Unlock()
				http.Redirect(w, req, screenPath, http.StatusSeeOther)
			})

			// Catch-all GET: find the matching screen and render its view.
			mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
				screen, ok := screens[req.URL.Path]
				if !ok {
					http.NotFound(w, req)
					return
				}
				mu.Lock()
				sess := getSession(req, w)
				model := getOrInitScreenModel(screen, sess)
				vVal, err := apply(screen.ViewFn, model)
				mu.Unlock()
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				v, ok := vVal.(VView)
				if !ok {
					http.Error(w, "view did not return a View", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = io.WriteString(w, "<!doctype html><html><body>")
				renderScreenInteractiveHTML(w, v, screen.Path)
				_, _ = io.WriteString(w, "</body></html>")
			})

			fmt.Printf("[mar] App listening on :%d (screens: %s)\n", port, strings.Join(paths, ", "))
			err := http.ListenAndServe(fmt.Sprintf(":%d", port), mux)
			return VUnit{}, err
		},
	}, nil
}

// renderScreenInteractiveHTML mirrors renderInteractiveHTML but routes msg
// posts to /__msg<screenPath> so the right screen handles them.
func renderScreenInteractiveHTML(w io.Writer, v VView, screenPath string) {
	msgURL := "/__msg" + screenPath
	switch v.Tag {
	case "button":
		fmt.Fprintf(w, `<form method="post" action="%s" style="display:inline">`, escapeAttr(msgURL))
		fmt.Fprintf(w, `<input type="hidden" name="msg" value="%s">`, escapeAttr(v.Text))
		fmt.Fprintf(w, `<button type="submit">%s</button>`, escapeHTML(v.Text))
		fmt.Fprintf(w, `</form>`)
	case "form":
		fmt.Fprintf(w, `<form method="post" action="%s">`, escapeAttr(msgURL))
		fmt.Fprintf(w, `<input type="hidden" name="msg" value="%s">`, escapeAttr(v.Text))
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				renderScreenInteractiveHTML(w, cv, screenPath)
			}
		}
		fmt.Fprintf(w, `<button type="submit">submit</button></form>`)
	case "section":
		_, _ = io.WriteString(w, "<section>")
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				renderScreenInteractiveHTML(w, cv, screenPath)
			}
		}
		_, _ = io.WriteString(w, "</section>")
	case "row":
		_, _ = io.WriteString(w, `<div class="row">`)
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				renderScreenInteractiveHTML(w, cv, screenPath)
			}
		}
		_, _ = io.WriteString(w, "</div>")
	case "column":
		_, _ = io.WriteString(w, `<div class="column">`)
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				renderScreenInteractiveHTML(w, cv, screenPath)
			}
		}
		_, _ = io.WriteString(w, "</div>")
	case "list":
		_, _ = io.WriteString(w, "<ul>")
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				_, _ = io.WriteString(w, "<li>")
				renderScreenInteractiveHTML(w, cv, screenPath)
				_, _ = io.WriteString(w, "</li>")
			}
		}
		_, _ = io.WriteString(w, "</ul>")
	default:
		// Fall back to the non-interactive renderer for leaf elements.
		var sb strings.Builder
		writeView(&sb, v)
		_, _ = io.WriteString(w, sb.String())
	}
}

// renderInteractiveHTML walks a view, rendering buttons as forms that POST
// the bound msg back to /__msg.
//
// MVP convention: a button's onClick msg is encoded as the button's text.
// (Once we add proper msg attributes, this becomes typed.)
func renderInteractiveHTML(w io.Writer, v VView) {
	switch v.Tag {
	case "button":
		fmt.Fprintf(w, `<form method="post" action="/__msg" style="display:inline">`)
		fmt.Fprintf(w, `<input type="hidden" name="msg" value="%s">`, escapeAttr(v.Text))
		fmt.Fprintf(w, `<button type="submit">%s</button>`, escapeHTML(v.Text))
		fmt.Fprintf(w, `</form>`)
	case "text":
		fmt.Fprintf(w, "<span>%s</span>", escapeHTML(v.Text))
	case "title":
		fmt.Fprintf(w, "<h1>%s</h1>", escapeHTML(v.Text))
	case "subtitle":
		fmt.Fprintf(w, "<h2>%s</h2>", escapeHTML(v.Text))
	case "link":
		href := ""
		for _, a := range v.Attrs {
			if a.Name == "href" {
				if s, ok := a.Value.(VString); ok {
					href = s.V
				}
			}
		}
		fmt.Fprintf(w, `<a href="%s">%s</a>`, escapeAttr(href), escapeHTML(v.Text))
	case "section":
		_, _ = io.WriteString(w, "<section>")
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				renderInteractiveHTML(w, cv)
			}
		}
		_, _ = io.WriteString(w, "</section>")
	case "row":
		_, _ = io.WriteString(w, `<div class="row">`)
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				renderInteractiveHTML(w, cv)
			}
		}
		_, _ = io.WriteString(w, "</div>")
	case "column":
		_, _ = io.WriteString(w, `<div class="column">`)
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				renderInteractiveHTML(w, cv)
			}
		}
		_, _ = io.WriteString(w, "</div>")
	case "list":
		_, _ = io.WriteString(w, "<ul>")
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				_, _ = io.WriteString(w, "<li>")
				renderInteractiveHTML(w, cv)
				_, _ = io.WriteString(w, "</li>")
			}
		}
		_, _ = io.WriteString(w, "</ul>")
	case "form":
		// View.form makes the inputs inside post a msg with the named fields.
		fmt.Fprintf(w, `<form method="post" action="/__msg">`)
		fmt.Fprintf(w, `<input type="hidden" name="msg" value="%s">`, escapeAttr(v.Text))
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				renderInteractiveHTML(w, cv)
			}
		}
		fmt.Fprintf(w, `<button type="submit">submit</button></form>`)
	case "input":
		name := ""
		for _, a := range v.Attrs {
			if a.Name == "name" {
				if s, ok := a.Value.(VString); ok {
					name = s.V
				}
			}
		}
		fmt.Fprintf(w, `<input type="text" name="%s" value="%s">`, escapeAttr(name), escapeAttr(v.Text))
	case "textarea":
		name := ""
		for _, a := range v.Attrs {
			if a.Name == "name" {
				if s, ok := a.Value.(VString); ok {
					name = s.V
				}
			}
		}
		fmt.Fprintf(w, `<textarea name="%s">%s</textarea>`, escapeAttr(name), escapeHTML(v.Text))
	case "empty":
		// nothing
	default:
		var sb strings.Builder
		writeView(&sb, v)
		_, _ = io.WriteString(w, sb.String())
	}
}
