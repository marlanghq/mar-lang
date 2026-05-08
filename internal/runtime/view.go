package runtime

import (
	"fmt"
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

// viewBuiltins exposes the view DSL — SwiftUI-style declarative
// vocabulary (UI.navigationStack, UI.form, UI.section, UI.textField,
// ...). Containers emit VView nodes that the renderers (web JS, iOS
// Swift) translate to native widgets.
//
//	UI.navigationStack [ UI.navigationTitle "Counter" ]
//	    [ UI.form
//	        [ UI.section [] [ UI.text (String.fromInt model) ]
//	        , UI.section [] [ UI.button [] Inc "+" ]
//	        ]
//	    ]
//
// `Attr` is opaque: at runtime it's a VRecord {name, value} produced
// only by modifier functions (UI.navigationTitle, UI.disabled, etc.).
// Constructors iterate the attrs list, copy each into VView.Attrs, and
// the renderer reads them per-platform.
func viewBuiltins() map[string]Value {
	return map[string]Value{
		// Input-kind attrs reached via UI.email / UI.password /
		// UI.newPassword / UI.numeric / UI.oneTimeCode / UI.submit.
		// Renderers translate the flag names into per-platform
		// behavior: HTML `type` / `autocomplete` / `inputmode` on
		// web, `keyboardType` / `textContentType` on iOS.
		"viewSubmit": nativeFn(1, func(args []Value) (Value, error) {
			return makeAttr("submit", args[0]), nil
		}),
		"viewEmail":       flagAttr("inputKindEmail"),
		"viewPassword":    flagAttr("inputKindPassword"),
		"viewNewPassword": flagAttr("inputKindNewPassword"),
		"viewNumeric":     flagAttr("inputKindNumeric"),
		"viewOneTimeCode": flagAttr("inputKindOneTimeCode"),

		// ---------- UI module: SwiftUI-style declarative vocabulary ----------
		//
		// Containers emit VView with platform-meaningful tags
		// ("navigationStack", "form", "uiList", "uiSection", "hstack",
		// "vstack"). The renderers (web JS, iOS Swift) recognize these
		// tags and produce native widgets.

		"navigationStack": nativeFn(2, containerCtor("navigationStack")),
		"form":            nativeFn(1, contentOnlyContainer("form")),
		"list":            nativeFn(1, contentOnlyContainer("uiList")),
		"uiSection":       nativeFn(2, containerCtor("uiSection")),
		"hstack":          nativeFn(2, containerCtor("hstack")),
		"vstack":          nativeFn(2, containerCtor("vstack")),

		// textField : List Attr -> String placeholder -> String value -> (String -> msg) -> View msg
		// Mirrors SwiftUI's TextField("Email", text: $value): the
		// placeholder, current value, and onChange callback come in
		// as separate positional args, with modifiers (submit, email,
		// password, etc.) in the leading attrs list.
		"textField": nativeFn(4, func(args []Value) (Value, error) {
			attrs, err := collectAttrs(args[0], "UI.textField")
			if err != nil {
				return nil, err
			}
			placeholder, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.textField: expected String placeholder (got %T)", args[1])
			}
			value, ok := args[2].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.textField: expected String value (got %T)", args[2])
			}
			onChange := args[3]
			attrs = append(attrs, VAttr{Name: "placeholder", Value: placeholder})
			return VView{Tag: "textField", Text: value.V, Msg: onChange, Attrs: attrs}, nil
		}),

		// Modifier attrs. Each produces a VAttr that the appropriate
		// container reads from its attrs list. The renderer is the
		// authoritative consumer — these are just labeled name/value
		// pairs at runtime.

		"navigationTitle": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.navigationTitle: expected String (got %T)", args[0])
			}
			return makeAttr("navigationTitle", s), nil
		}),

		// trailing / leading : View msg -> Attr
		// Wraps a view as a toolbar item placed at the right / left of
		// the navigation stack's title bar. The renderer pulls these
		// out of navigationStack's attrs to build the platform-native
		// toolbar.
		"trailing": nativeFn(1, func(args []Value) (Value, error) {
			v, ok := args[0].(VView)
			if !ok {
				return nil, fmt.Errorf("UI.trailing: expected View (got %T)", args[0])
			}
			return makeAttr("trailing", v), nil
		}),
		"leading": nativeFn(1, func(args []Value) (Value, error) {
			v, ok := args[0].(VView)
			if !ok {
				return nil, fmt.Errorf("UI.leading: expected View (got %T)", args[0])
			}
			return makeAttr("leading", v), nil
		}),

		"header": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.header: expected String (got %T)", args[0])
			}
			return makeAttr("header", s), nil
		}),
		"footer": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.footer: expected String (got %T)", args[0])
			}
			return makeAttr("footer", s), nil
		}),

		// numericCode is a flag the renderer reads as "set keyboard
		// to numeric AND content-type to one-time-code". Bundles the
		// common OTP / 2FA case so user code doesn't have to pass
		// `[ numeric, oneTimeCode ]` everywhere. Single attr at the
		// type level (TAttr); the renderer expands it.
		"numericCode": flagAttr("inputKindNumericCode"),

		// uiText : String -> View msg
		// Plain text leaf — no attrs.
		"uiText": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.text: expected String (got %T)", args[0])
			}
			return VView{Tag: "text", Text: s.V}, nil
		}),

		// uiButton : List Attr -> msg -> String -> View msg
		// Button that dispatches `msg` on tap. The attrs list carries
		// modifiers like `disabled`; the renderer reads them to tune
		// behavior (e.g. SwiftUI's `.disabled(Bool)`, HTML's `disabled`
		// attribute).
		"uiButton": nativeFn(3, func(args []Value) (Value, error) {
			attrs, err := collectAttrs(args[0], "UI.button")
			if err != nil {
				return nil, err
			}
			label, ok := args[2].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.button: expected String label (got %T)", args[2])
			}
			return VView{Tag: "button", Text: label.V, Msg: args[1], Attrs: attrs}, nil
		}),

		// uiDisabled : Bool -> Attr
		// `disabled True` → button is greyed-out and ignores taps.
		// `disabled False` → no-op (kept symmetric so user code can
		// pass a derived Bool without conditionally building the list).
		"uiDisabled": nativeFn(1, func(args []Value) (Value, error) {
			b, ok := args[0].(VBool)
			if !ok {
				return nil, fmt.Errorf("UI.disabled: expected Bool (got %T)", args[0])
			}
			return makeAttr("disabled", b), nil
		}),

		// uiTitle / uiSubtitle : String -> View msg
		// Heading + secondary heading. Reuse the existing "title" /
		// "subtitle" tags so the renderer doesn't need new branches.
		"uiTitle": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.title: expected String (got %T)", args[0])
			}
			return VView{Tag: "title", Text: s.V}, nil
		}),
		"uiSubtitle": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.subtitle: expected String (got %T)", args[0])
			}
			return VView{Tag: "subtitle", Text: s.V}, nil
		}),

		// uiLink : Path r -> r -> String -> View msg
		// Build the URL via the same path-pattern machinery as linkTo,
		// then emit a "link" view tag with `href` attr. The renderers
		// (web <a>, iOS NavigationLink) consume that to produce the
		// platform-native clickable element.
		"uiLink": nativeFn(3, func(args []Value) (Value, error) {
			url, err := buildPathURL(args[0], args[1], "UI.link")
			if err != nil {
				return nil, err
			}
			label, ok := args[2].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.link: expected String label (got %T)", args[2])
			}
			return VView{
				Tag:   "link",
				Text:  label.V,
				Attrs: []VAttr{{Name: "href", Value: VString{V: url}}},
			}, nil
		}),

		// uiEmpty : View msg
		// No-op placeholder — renders to nothing. Useful in
		// conditional view fragments (`if cond then x else empty`).
		"uiEmpty": VView{Tag: "empty"},

		// uiCentered : View msg -> View msg
		// Wraps `child` in a container that fills the available space
		// and centers it. Renderers translate the "centered" tag to
		// platform-native max-size + center alignment.
		"uiCentered": nativeFn(1, func(args []Value) (Value, error) {
			child, ok := args[0].(VView)
			if !ok {
				return nil, fmt.Errorf("UI.centered: expected View (got %T)", args[0])
			}
			return VView{Tag: "centered", Children: []Value{child}}, nil
		}),
	}
}

// contentOnlyContainer builds a 1-arg native: (List View) -> View.
// Used for `form` / `list` where SwiftUI's container takes content
// directly without a modifiers slot.
func contentOnlyContainer(tag string) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		children, err := unwrapViewList(args[0])
		if err != nil {
			return nil, fmt.Errorf("UI.%s: %v", tag, err)
		}
		return VView{Tag: tag, Children: children}, nil
	}
}

// containerCtor builds a 2-arg native: (List Attr, List View) -> View.
func containerCtor(tag string) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		attrs, err := collectAttrs(args[0], "UI."+tag)
		if err != nil {
			return nil, err
		}
		children, err := unwrapViewList(args[1])
		if err != nil {
			return nil, fmt.Errorf("UI.%s: %v", tag, err)
		}
		return VView{Tag: tag, Children: children, Attrs: attrs}, nil
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

// flagAttr returns a constant Attr (no payload) — for input-kind
// flags (email / numeric / oneTimeCode / etc.).
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

