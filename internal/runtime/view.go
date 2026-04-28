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
type VView struct {
	Tag      string  // "section", "row", "column", "button", "text", "title", ...
	Attrs    []VAttr // attributes (onClick, value, intent, ...)
	Children []Value // can be VView or VString (for text content)
	Text     string  // for leaf text nodes
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

// viewBuiltins exposes the view DSL.
//
//	View.section : List View -> View
//	View.row     : List View -> View
//	View.column  : List View -> View
//	View.title   : String -> View
//	View.subtitle: String -> View
//	View.text    : String -> View
//	View.button  : String -> View
//	View.link    : String -> String -> View   -- url, label
//
// (No attributes/messages yet; adding interactivity is a later step.)
func viewBuiltins() map[string]Value {
	return map[string]Value{
		"viewSection": nativeFn(1, func(args []Value) (Value, error) {
			children, err := unwrapViewList(args[0])
			if err != nil {
				return nil, err
			}
			return VView{Tag: "section", Children: children}, nil
		}),
		"viewRow": nativeFn(1, func(args []Value) (Value, error) {
			children, err := unwrapViewList(args[0])
			if err != nil {
				return nil, err
			}
			return VView{Tag: "row", Children: children}, nil
		}),
		"viewColumn": nativeFn(1, func(args []Value) (Value, error) {
			children, err := unwrapViewList(args[0])
			if err != nil {
				return nil, err
			}
			return VView{Tag: "column", Children: children}, nil
		}),
		"viewText": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("View.text: expected String")
			}
			return VView{Tag: "text", Text: s.V}, nil
		}),
		"viewTitle": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("View.title: expected String")
			}
			return VView{Tag: "title", Text: s.V}, nil
		}),
		"viewSubtitle": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("View.subtitle: expected String")
			}
			return VView{Tag: "subtitle", Text: s.V}, nil
		}),
		"viewButton": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("View.button: expected String")
			}
			return VView{Tag: "button", Text: s.V}, nil
		}),
		"viewLink": nativeFn(2, func(args []Value) (Value, error) {
			url, ok1 := args[0].(VString)
			label, ok2 := args[1].(VString)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("View.link: expected String, String")
			}
			return VView{
				Tag:   "link",
				Text:  label.V,
				Attrs: []VAttr{{Name: "href", Value: url}},
			}, nil
		}),
		"viewList": nativeFn(1, func(args []Value) (Value, error) {
			children, err := unwrapViewList(args[0])
			if err != nil {
				return nil, err
			}
			return VView{Tag: "list", Children: children}, nil
		}),

		// Render: View -> String  (server-side rendering to HTML)
		"viewRender": nativeFn(1, func(args []Value) (Value, error) {
			v, ok := args[0].(VView)
			if !ok {
				return nil, fmt.Errorf("View.render: expected View")
			}
			return VString{V: renderViewHTML(v)}, nil
		}),
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
