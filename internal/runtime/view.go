package runtime

import (
	"fmt"
	"strings"
)

// VView is a tree of view nodes — the runtime representation of a `View Msg`
// expression.
//
// Views are pure descriptions; the renderer (web or iOS) reads them and
// produces native UI.
//
// `Msg` is the bound message for interactive nodes:
//   - For "button" nodes: the Msg value to dispatch on click (typically a
//     VCtor like Increment, or VCtor with args).
//   - For "form" nodes: the constructor function (VFn with CtorTag, or
//     a VCtor for nullary) that wraps the form's collected fields.
//   - For other nodes: nil.
//
// Text holds the visible label or input value (purely presentational).
type VView struct {
	Tag      string  // "section", "row", "column", "button", "text", "title", ...
	Attrs    []VAttr // attributes (onClick, value, intent, ...)
	Children []Value // can be VView or VString (for text content)
	Text     string  // visible label / input value
	Msg      Value   // bound message for interactive nodes (button click, form submit)
}

// VAttr is a key/value attribute on a view node.
type VAttr struct {
	Name  string
	Value Value
}

func (VView) isValue() {}
func (v VView) Display() string {
	return fmt.Sprintf("<view:%s>", v.Tag)
}

// viewBuiltins exposes the view DSL — elm-ui-style. Every constructor
// takes a `List Attr` as its first argument; layout modifiers
// (View.padding, View.fillX, ...) produce Attr values that go into
// that list. Common usage:
//
//	View.column [ View.spacing 16, View.padding 24 ]
//	    [ View.button [] Increment "+"
//	    , View.text [] "hello"
//	    ]
//
// `Attr` is opaque: at runtime it's a VRecord {name, value} produced
// only by modifier functions. Constructors iterate the list, copy each
// into VView.Attrs, and the renderer (web today, native runtimes
// later) translates names like "padding" / "fillX" / "center" to
// platform-appropriate output.
func viewBuiltins() map[string]Value {
	return map[string]Value{
		"viewSection":  nativeFn(2, containerCtor("section")),
		"viewRow":      nativeFn(2, containerCtor("row")),
		"viewColumn":   nativeFn(2, containerCtor("column")),
		"viewList":     nativeFn(2, containerCtor("list")),
		"viewText":     nativeFn(2, leafTextCtor("text", "View.text")),
		"viewTitle":    nativeFn(2, leafTextCtor("title", "View.title")),
		"viewSubtitle": nativeFn(2, leafTextCtor("subtitle", "View.subtitle")),

		// View.button : List Attr -> msg -> String -> View msg
		"viewButton": nativeFn(3, func(args []Value) (Value, error) {
			attrs, err := collectAttrs(args[0], "View.button")
			if err != nil {
				return nil, err
			}
			msg := args[1]
			label, ok := args[2].(VString)
			if !ok {
				return nil, fmt.Errorf("View.button: expected String label (got %T)", args[2])
			}
			return VView{Tag: "button", Text: label.V, Msg: msg, Attrs: attrs}, nil
		}),

		// View.link : List Attr -> String -> String -> View msg   (href, label)
		"viewLink": nativeFn(3, func(args []Value) (Value, error) {
			attrs, err := collectAttrs(args[0], "View.link")
			if err != nil {
				return nil, err
			}
			url, ok1 := args[1].(VString)
			label, ok2 := args[2].(VString)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("View.link: expected (List Attr, String href, String label)")
			}
			attrs = append(attrs, VAttr{Name: "href", Value: url})
			return VView{Tag: "link", Text: label.V, Attrs: attrs}, nil
		}),

		// View.keyedList : List Attr -> List (String, View msg) -> View msg
		"viewKeyedList": nativeFn(2, func(args []Value) (Value, error) {
			attrs, err := collectAttrs(args[0], "View.keyedList")
			if err != nil {
				return nil, err
			}
			list, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("View.keyedList: expected List of (key, view)")
			}
			children := make([]Value, len(list.Elements))
			for i, el := range list.Elements {
				t, ok := el.(VTuple)
				if !ok || len(t.Members) != 2 {
					return nil, fmt.Errorf("View.keyedList: element %d is not a (key, view) tuple", i)
				}
				keyV, kok := t.Members[0].(VString)
				viewV, vok := t.Members[1].(VView)
				if !kok || !vok {
					return nil, fmt.Errorf("View.keyedList: element %d expected (String, View)", i)
				}
				tagged := viewV
				tagged.Attrs = append([]VAttr(nil), viewV.Attrs...)
				tagged.Attrs = append(tagged.Attrs, VAttr{Name: "__key", Value: keyV})
				children[i] = tagged
			}
			return VView{Tag: "keyedList", Children: children, Attrs: attrs}, nil
		}),

		// View.input : List Attr -> String -> (String -> msg) -> View msg
		"viewInput":    nativeFn(3, inputCtor("input", "View.input")),
		"viewTextarea": nativeFn(3, inputCtor("textarea", "View.textarea")),

		// View.empty : View — no attrs, used as a placeholder.
		"viewEmpty": VView{Tag: "empty"},

		// Render: View -> String  (server-side rendering to HTML)
		"viewRender": nativeFn(1, func(args []Value) (Value, error) {
			v, ok := args[0].(VView)
			if !ok {
				return nil, fmt.Errorf("View.render: expected View")
			}
			return VString{V: renderViewHTML(v)}, nil
		}),

		// Layout modifiers — produce Attr values (VRecord with name +
		// value fields) consumed by the constructors above.
		"viewPadding":  nativeFn(1, intAttr("padding", "View.padding")),
		"viewSpacing":  nativeFn(1, intAttr("spacing", "View.spacing")),
		"viewWidth":    nativeFn(1, intAttr("width", "View.width")),
		"viewHeight":   nativeFn(1, intAttr("height", "View.height")),
		"viewFillX":    flagAttr("fillX"),
		"viewFillY":    flagAttr("fillY"),
		"viewFill":     flagAttr("fill"),
		"viewCenterX":  flagAttr("centerX"),
		"viewCenterY":  flagAttr("centerY"),
		"viewCenter":   flagAttr("center"),
	}
}

// containerCtor builds a 2-arg native: (List Attr, List View) -> View.
func containerCtor(tag string) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		attrs, err := collectAttrs(args[0], "View."+tag)
		if err != nil {
			return nil, err
		}
		children, err := unwrapViewList(args[1])
		if err != nil {
			return nil, fmt.Errorf("View.%s: %v", tag, err)
		}
		return VView{Tag: tag, Children: children, Attrs: attrs}, nil
	}
}

// leafTextCtor builds a 2-arg native: (List Attr, String) -> View.
func leafTextCtor(tag, label string) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		attrs, err := collectAttrs(args[0], label)
		if err != nil {
			return nil, err
		}
		s, ok := args[1].(VString)
		if !ok {
			return nil, fmt.Errorf("%s: expected String (got %T)", label, args[1])
		}
		return VView{Tag: tag, Text: s.V, Attrs: attrs}, nil
	}
}

// inputCtor builds a 3-arg native:
//
//	(List Attr, String currentValue, (String -> msg) onChange) -> View
func inputCtor(tag, label string) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		attrs, err := collectAttrs(args[0], label)
		if err != nil {
			return nil, err
		}
		value, ok := args[1].(VString)
		if !ok {
			return nil, fmt.Errorf("%s: expected String currentValue (got %T)", label, args[1])
		}
		onChange := args[2]
		return VView{Tag: tag, Text: value.V, Msg: onChange, Attrs: attrs}, nil
	}
}

// collectAttrs reads a VList of Attr records (each {name, value}) and
// returns them as []VAttr ready to drop into a VView.
func collectAttrs(v Value, label string) ([]VAttr, error) {
	list, ok := v.(VList)
	if !ok {
		return nil, fmt.Errorf("%s: expected List Attr (got %T)", label, v)
	}
	out := make([]VAttr, 0, len(list.Elements))
	for i, el := range list.Elements {
		rec, ok := el.(VRecord)
		if !ok {
			return nil, fmt.Errorf("%s: attr %d is not an Attr record (got %T)", label, i, el)
		}
		nameV, _ := rec.Fields["name"].(VString)
		out = append(out, VAttr{Name: nameV.V, Value: rec.Fields["value"]})
	}
	return out, nil
}

// intAttr builds a 1-arg native: Int -> Attr. The Attr is a VRecord
// {name, value} the constructors collect.
func intAttr(name, label string) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		n, ok := args[0].(VInt)
		if !ok {
			return nil, fmt.Errorf("%s: expected Int (got %T)", label, args[0])
		}
		return makeAttr(name, n), nil
	}
}

// flagAttr returns a constant Attr (no payload) — for fillX, center, etc.
func flagAttr(name string) Value {
	return makeAttr(name, VUnit{})
}

func makeAttr(name string, value Value) VRecord {
	return VRecord{
		Fields: map[string]Value{
			"name":  VString{V: name},
			"value": value,
		},
		Order: []string{"name", "value"},
	}
}

// unwrapViewList expects a VList of VView elements and returns them as []Value.
func unwrapViewList(v Value) ([]Value, error) {
	l, ok := v.(VList)
	if !ok {
		return nil, fmt.Errorf("expected List View")
	}
	for _, e := range l.Elements {
		if _, ok := e.(VView); !ok {
			return nil, fmt.Errorf("list element is not a View (got %T)", e)
		}
	}
	return l.Elements, nil
}

// renderViewHTML produces a simple HTML representation of a view tree.
func renderViewHTML(v VView) string {
	var sb strings.Builder
	writeView(&sb, v)
	return sb.String()
}

func writeView(sb *strings.Builder, v VView) {
	switch v.Tag {
	case "text":
		sb.WriteString("<span>")
		sb.WriteString(escapeHTML(v.Text))
		sb.WriteString("</span>")
	case "title":
		sb.WriteString("<h1>")
		sb.WriteString(escapeHTML(v.Text))
		sb.WriteString("</h1>")
	case "subtitle":
		sb.WriteString("<h2>")
		sb.WriteString(escapeHTML(v.Text))
		sb.WriteString("</h2>")
	case "button":
		sb.WriteString("<button>")
		sb.WriteString(escapeHTML(v.Text))
		sb.WriteString("</button>")
	case "link":
		href := ""
		for _, a := range v.Attrs {
			if a.Name == "href" {
				if s, ok := a.Value.(VString); ok {
					href = s.V
				}
			}
		}
		sb.WriteString(`<a href="`)
		sb.WriteString(escapeAttr(href))
		sb.WriteString(`">`)
		sb.WriteString(escapeHTML(v.Text))
		sb.WriteString("</a>")
	case "section":
		sb.WriteString("<section>")
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				writeView(sb, cv)
			}
		}
		sb.WriteString("</section>")
	case "row":
		sb.WriteString(`<div class="row">`)
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				writeView(sb, cv)
			}
		}
		sb.WriteString("</div>")
	case "column":
		sb.WriteString(`<div class="column">`)
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				writeView(sb, cv)
			}
		}
		sb.WriteString("</div>")
	case "list":
		sb.WriteString("<ul>")
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				sb.WriteString("<li>")
				writeView(sb, cv)
				sb.WriteString("</li>")
			}
		}
		sb.WriteString("</ul>")
	case "empty":
		// nothing
	case "input":
		// Plain (non-interactive) render — no onChange wiring.
		sb.WriteString(`<input type="text" value="`)
		sb.WriteString(escapeAttr(v.Text))
		sb.WriteString(`">`)
	case "textarea":
		sb.WriteString(`<textarea>`)
		sb.WriteString(escapeHTML(v.Text))
		sb.WriteString(`</textarea>`)
	default:
		sb.WriteString("<div>")
		for _, c := range v.Children {
			if cv, ok := c.(VView); ok {
				writeView(sb, cv)
			}
		}
		sb.WriteString("</div>")
	}
}

func escapeHTML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

func escapeAttr(s string) string {
	return strings.NewReplacer(`"`, `&quot;`, `'`, `&#39;`).Replace(s)
}
