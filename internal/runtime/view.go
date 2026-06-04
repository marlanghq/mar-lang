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
		"list":            nativeFn(2, containerCtor("uiList")),
		"uiSection":       nativeFn(2, containerCtor("uiSection")),
		"uiKeyedList":     nativeFn(2, containerCtor("uiKeyedList")),
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

		// textArea : List Attr -> String placeholder -> String value -> (String -> msg) -> View msg
		// Multi-line variant of textField. iOS: TextEditor wrapped
		// to look like a form-row TextField. Web: <textarea> with
		// the same focus-ring + padding so it sits cleanly next to
		// neighboring textFields inside a section.
		"textArea": nativeFn(4, func(args []Value) (Value, error) {
			attrs, err := collectAttrs(args[0], "UI.textArea")
			if err != nil {
				return nil, err
			}
			placeholder, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.textArea: expected String placeholder (got %T)", args[1])
			}
			value, ok := args[2].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.textArea: expected String value (got %T)", args[2])
			}
			onChange := args[3]
			attrs = append(attrs, VAttr{Name: "placeholder", Value: placeholder})
			return VView{Tag: "textArea", Text: value.V, Msg: onChange, Attrs: attrs}, nil
		}),

		// picker : List Attr -> a -> List a -> (a -> String) -> (a -> msg) -> View msg
		// Single-selection field for enum-like inputs that have too
		// many variants to render as a vertical stack of toggles.
		// Mirrors SwiftUI's Picker(selection: $value) { ForEach
		// options { Text(toLabel(option)).tag(option) } }. iOS gets
		// the native menu / wheel; web gets a styled <select>. The
		// renderer stashes options + toLabel + selected as attrs so
		// it can rebuild the option list and resolve the picked
		// value on change without retaining JS closures over user
		// values.
		"picker": nativeFn(5, func(args []Value) (Value, error) {
			attrs, err := collectAttrs(args[0], "UI.picker")
			if err != nil {
				return nil, err
			}
			selected := args[1]
			options, ok := args[2].(VList)
			if !ok {
				return nil, fmt.Errorf("UI.picker: expected List options (got %T)", args[2])
			}
			toLabel := args[3]
			onChange := args[4]
			attrs = append(attrs,
				VAttr{Name: "selected", Value: selected},
				VAttr{Name: "options", Value: options},
				VAttr{Name: "toLabel", Value: toLabel},
			)
			return VView{Tag: "picker", Msg: onChange, Attrs: attrs}, nil
		}),

		// Modifier attrs. Each produces a VAttr that the appropriate
		// container reads from its attrs list. The renderer is the
		// authoritative consumer — these are just labeled name/value
		// pairs at runtime.

		"navigationTitle": stringAttrCtor("navigationTitle", "UI.navigationTitle"),

		// topBarTrailing / topBarLeading : View msg -> Attr NavStack
		// Toolbar item placed at the trailing / leading edge of the
		// top bar. Names match SwiftUI's `.topBarTrailing` /
		// `.topBarLeading` placement (iOS 17+). The renderer reads
		// these off navigationStack's attrs to build the platform-
		// native toolbar; on web they sit beside the back chevron
		// (when there's one), on iOS they ARE the toolbar items.
		"uiTopBarTrailing": viewAttrCtor("topBarTrailing", "UI.topBarTrailing"),
		"uiTopBarLeading":  viewAttrCtor("topBarLeading", "UI.topBarLeading"),

		"header": stringAttrCtor("header", "UI.header"),
		"footer": stringAttrCtor("footer", "UI.footer"),

		// numericCode is a flag the renderer reads as "set keyboard
		// to numeric AND content-type to one-time-code". Bundles the
		// common OTP / 2FA case so user code doesn't have to pass
		// `[ numeric, oneTimeCode ]` everywhere. Single attr at the
		// type level (TAttr); the renderer expands it.
		"numericCode": flagAttr("inputKindNumericCode"),

		// uiText : String -> View msg
		// Plain text leaf — no attrs.
		"uiText": textLeaf("text", "UI.text"),

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

		// uiKeyed : String -> View msg -> KeyedView msg
		// Wraps a regular View in a stable identity (the String key)
		// so it can be a child of UI.keyedList. At runtime the
		// "KeyedView" distinction is erased — the value is a VView
		// with an internal `key` attr appended. The compiler-only
		// type guarantee ensures keyedList children always carry
		// the key the reconciler needs to match rows across
		// reorders / deletes / inserts.
		"uiKeyed": nativeFn(2, func(args []Value) (Value, error) {
			keyStr, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.keyed: expected String key (got %T)", args[0])
			}
			view, ok := args[1].(VView)
			if !ok {
				return nil, fmt.Errorf("UI.keyed: expected View (got %T)", args[1])
			}
			// Append the key as an attr. Renderers read it back when
			// reconciling keyedList children. We copy the Attrs slice
			// so callers that hold a reference to the original VView
			// don\'t see their attrs mutate.
			attrs := make([]VAttr, len(view.Attrs), len(view.Attrs)+1)
			copy(attrs, view.Attrs)
			attrs = append(attrs, VAttr{Name: "key", Value: keyStr})
			view.Attrs = attrs
			return view, nil
		}),

		// uiOnMove : Bool -> (Int -> Int -> msg) -> Attr KeyedList
		// Makes a `keyedList` reorderable. The Bool toggles edit mode
		// (drag affordance visible / hidden); the callback receives
		// (from, to) indices once the user completes a drag or a
		// keyboard reorder.
		//
		// Both pieces are bundled into one attr so the type system
		// guarantees they're always declared together — eliminates
		// the "edit mode set but no handler" silent bug.
		"uiOnMove": nativeFn(2, func(args []Value) (Value, error) {
			editing, ok := args[0].(VBool)
			if !ok {
				return nil, fmt.Errorf("UI.onMove: expected Bool as first arg (got %T)", args[0])
			}
			// Pack both pieces into a VRecord so the renderer can
			// pull editing + handler off in one go.
			payload := VRecord{Fields: map[string]Value{
				"editing": editing,
				"handler": args[1], // the (Int -> Int -> msg) function
			}}
			return makeAttr("onMove", payload), nil
		}),

		// uiOnDelete : Bool -> (Int -> msg) -> Attr KeyedList
		// Makes a `keyedList`'s rows deletable. The Bool toggles edit
		// mode (where deletion is always visible on every row); when
		// False, the web renderer reveals the delete affordance on
		// hover, and iOS surfaces it via swipe-to-delete. The handler
		// receives the index of the deleted row and returns a Msg.
		//
		// Same packaging shape as onMove: editing + handler bundled
		// into a single attr so the type system can't accept one
		// without the other.
		//
		// Host is KeyedList because per-row delete needs stable
		// identity to animate the disappearance of the right row —
		// which is exactly what keyedList guarantees via its
		// `KeyedView` children.
		"uiOnDelete": nativeFn(2, func(args []Value) (Value, error) {
			editing, ok := args[0].(VBool)
			if !ok {
				return nil, fmt.Errorf("UI.onDelete: expected Bool as first arg (got %T)", args[0])
			}
			payload := VRecord{Fields: map[string]Value{
				"editing": editing,
				"handler": args[1], // the (Int -> msg) function
			}}
			return makeAttr("onDelete", payload), nil
		}),

		// uiTitle / uiSubtitle : String -> View msg
		// Heading + secondary heading. Reuse the existing "title" /
		// "subtitle" tags so the renderer doesn't need new branches.
		"uiTitle":    textLeaf("title", "UI.title"),
		"uiSubtitle": textLeaf("subtitle", "UI.subtitle"),

		// uiErrorText : String -> View msg
		// Error message — semantically distinct from `text` so the
		// renderer can style it with destructive intent (red + semi-
		// bold). The "errorText" tag is read by the JS renderer (CSS
		// class `.mar-error-text`) and the iOS renderer
		// (.foregroundStyle(.red).fontWeight(.semibold)).
		"uiErrorText": textLeaf("errorText", "UI.errorText"),

		// uiImage : List (Attr Image) -> { src, alt } -> View msg
		// Emits an "image" tag carrying src + alt (and any size/fit/fill
		// attrs). alt is a required record field, never optional.
		"uiImage": nativeFn(2, func(args []Value) (Value, error) {
			attrs, err := collectAttrs(args[0], "UI.image")
			if err != nil {
				return nil, err
			}
			rec, ok := args[1].(VRecord)
			if !ok {
				return nil, fmt.Errorf("UI.image: expected { src, alt } record (got %T)", args[1])
			}
			src, _ := rec.Fields["src"].(VString)
			alt, _ := rec.Fields["alt"].(VString)
			attrs = append(attrs,
				VAttr{Name: "src", Value: src},
				VAttr{Name: "alt", Value: alt},
			)
			return VView{Tag: "image", Attrs: attrs}, nil
		}),

		// uiParagraph : List (Inline msg) -> View msg
		// Block of flowing inline text. Children are VViews with tag
		// "span" produced by uiSpan; the renderer flows them into one
		// wrapping <p> / AttributedString.
		"uiParagraph": nativeFn(1, func(args []Value) (Value, error) {
			children, err := unwrapViewList(args[0])
			if err != nil {
				return nil, fmt.Errorf("UI.paragraph: %v", err)
			}
			return VView{Tag: "paragraph", Children: children}, nil
		}),

		// uiSpan : List (Attr Inline) -> String -> Inline msg
		// Inline text run with styling attrs. Tag "span" on the wire;
		// attrs carry bold/italic/strikethrough/code/link as named
		// markers. The runtime preserves order so the renderer can
		// honor composition (e.g. bold + link → bold-styled <a>).
		"uiSpan": nativeFn(2, func(args []Value) (Value, error) {
			attrs, err := collectAttrs(args[0], "UI.span")
			if err != nil {
				return nil, err
			}
			text, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.span: expected String (got %T)", args[1])
			}
			return VView{Tag: "span", Text: text.V, Attrs: attrs}, nil
		}),

		// Inline attrs. The bare style markers (bold / italic /
		// strikethrough / code) carry no payload; the renderer reads
		// the attr name to decide which CSS class / iOS modifier to
		// apply. `link` is the one parameterized inline attr: its
		// payload is the destination URL as a String.
		"inlineBold":          flagAttr("inlineBold"),
		"inlineItalic":        flagAttr("inlineItalic"),
		"inlineStrikethrough": flagAttr("inlineStrikethrough"),
		"inlineCode":          flagAttr("inlineCode"),
		"inlineLink": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.link: expected String (got %T)", args[0])
			}
			return makeAttr("inlineLink", s), nil
		}),

		// uiChars / uiLines — sizing units. Each wraps an Int into an
		// opaque-from-user-code "Width" / "Height" value (VRecord with
		// __unit marker so the renderer knows what the number means).
		// chars → horizontal characters, lines → vertical lines.
		"uiChars": nativeFn(1, func(args []Value) (Value, error) {
			n, ok := args[0].(VInt)
			if !ok {
				return nil, fmt.Errorf("UI.chars: expected Int (got %T)", args[0])
			}
			return lengthValue("chars", n.V), nil
		}),
		"uiLines": nativeFn(1, func(args []Value) (Value, error) {
			n, ok := args[0].(VInt)
			if !ok {
				return nil, fmt.Errorf("UI.lines: expected Int (got %T)", args[0])
			}
			return lengthValue("lines", n.V), nil
		}),

		// uiWidth / uiHeight — attribute builders. They take the Width
		// / Height value (built via uiChars / uiLines) and wrap it as
		// an Attr. The renderer reads the unit + amount and applies
		// the appropriate styling (max-width / min-height in CSS,
		// .frame(maxWidth:idealHeight:) in SwiftUI).
		"uiWidth": nativeFn(1, func(args []Value) (Value, error) {
			return makeAttr("width", args[0]), nil
		}),
		"uiHeight": nativeFn(1, func(args []Value) (Value, error) {
			return makeAttr("height", args[0]), nil
		}),

		// uiPx — pixel sizing unit for images. Same shape as uiChars/
		// uiLines (a __unit-tagged length value), but tagged "px".
		"uiPx": nativeFn(1, func(args []Value) (Value, error) {
			n, ok := args[0].(VInt)
			if !ok {
				return nil, fmt.Errorf("UI.px: expected Int (got %T)", args[0])
			}
			return lengthValue("px", n.V), nil
		}),
		// uiSize — fixed width + height for an image. Packs both Pixels
		// values into the attr payload; the renderer reads w/h.
		"uiSize": nativeFn(2, func(args []Value) (Value, error) {
			return makeAttr("size", VRecord{
				Fields: map[string]Value{"w": args[0], "h": args[1]},
				Order:  []string{"w", "h"},
			}), nil
		}),
		// uiFit / uiFill — content-mode flags for images.
		"uiFit":  flagAttr("contentModeFit"),
		"uiFill": flagAttr("contentModeFill"),

		// uiNavigationLink : List Attr -> Path r -> r -> View msg -> View msg
		// Mirror of SwiftUI's `NavigationLink(value:){content}`.
		// Builds the destination URL via the typed Path machinery,
		// then emits a "navigationLink" view tag carrying the URL
		// in `href` and the user-supplied label View as a child.
		// Renderers (iOS NavigationLink, web `<a class="mar-navigation-link">`)
		// wrap the child in a platform-native tappable area. The
		// leading attrs list lets callers pass `disabled` to make a
		// link inert (greyed out, click swallowed) — same shape
		// every other interactive primitive uses.
		"uiNavigationLink": nativeFn(4, func(args []Value) (Value, error) {
			attrs, err := collectAttrs(args[0], "UI.navigationLink")
			if err != nil {
				return nil, err
			}
			url, err := buildPathURL(args[1], args[2], "UI.navigationLink")
			if err != nil {
				return nil, err
			}
			child, ok := args[3].(VView)
			if !ok {
				return nil, fmt.Errorf("UI.navigationLink: expected View label (got %T)", args[3])
			}
			attrs = append(attrs, VAttr{Name: "href", Value: VString{V: url}})
			return VView{
				Tag:      "navigationLink",
				Attrs:    attrs,
				Children: []Value{child},
			}, nil
		}),

		// uiEmpty : View msg
		// No-op placeholder — renders to nothing. Useful in
		// conditional view fragments (`if cond then x else empty`).
		"uiEmpty": VView{Tag: "empty"},

		// uiSpacer : View msg
		// Mirror of SwiftUI's `Spacer()` — expands to fill the
		// available space along the containing stack's main axis.
		// Used to push siblings apart (e.g. label left, button
		// right inside an hstack).
		"uiSpacer": VView{Tag: "spacer"},

		// uiToggle : List Attr -> String -> Bool -> (Bool -> msg) -> View msg
		// Mirror of SwiftUI's `Toggle(label, isOn: $value)`. The
		// current state is carried as the `isOn` attr; the label
		// goes in Text; the `Bool -> msg` callback goes in Msg.
		// Renderers (iOS Toggle, web styled checkbox) bind to the
		// attr and dispatch `msg(newValue)` on flip. The leading
		// attrs list lets callers pass `disabled` (and future
		// modifiers) the same way they do for textField / button /
		// picker — uniform API across every interactive primitive.
		"uiToggle": nativeFn(4, func(args []Value) (Value, error) {
			attrs, err := collectAttrs(args[0], "UI.toggle")
			if err != nil {
				return nil, err
			}
			label, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.toggle: expected String label (got %T)", args[1])
			}
			isOn, ok := args[2].(VBool)
			if !ok {
				return nil, fmt.Errorf("UI.toggle: expected Bool value (got %T)", args[2])
			}
			attrs = append(attrs, VAttr{Name: "isOn", Value: isOn})
			return VView{
				Tag:   "toggle",
				Text:  label.V,
				Attrs: attrs,
				Msg:   args[3],
			}, nil
		}),

		// uiSheet : { open, onDismiss, outlet } -> List (View msg) -> View msg
		//
		// Modal sheet (iOS-style page sheet) controlled by the parent's
		// Model. Mirrors SwiftUI's `.sheet(isPresented:)`. The framework
		// renderer reads the three config fields off the view's Attrs:
		//
		//   open      — bool flag; renderer overlays/hides accordingly
		//   onDismiss — Msg dispatched when user dismisses (backdrop click,
		//               Escape key, swipe-down gesture, browser back)
		//   outlet    — required identifier; used by the web renderer for
		//               history-state tracking so the browser back button
		//               closes the sheet, and by the iOS Swift renderer as
		//               a routing tag
		//
		// Children carry the sheet's content. By convention parents pass a
		// single root view (e.g. a navigationStack with toolbar + list),
		// but a list of siblings also works.
		"uiSheet": nativeFn(2, func(args []Value) (Value, error) {
			rec, ok := args[0].(VRecord)
			if !ok {
				return nil, fmt.Errorf("UI.sheet: expected config record (got %T)", args[0])
			}
			open, ok := rec.Fields["open"].(VBool)
			if !ok {
				return nil, fmt.Errorf("UI.sheet: `open` must be Bool")
			}
			outlet, ok := rec.Fields["outlet"].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.sheet: `outlet` must be String")
			}
			onDismiss, ok := rec.Fields["onDismiss"]
			if !ok {
				return nil, fmt.Errorf("UI.sheet: `onDismiss` is required")
			}
			kids, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("UI.sheet: children must be List View (got %T)", args[1])
			}
			children := make([]Value, len(kids.Elements))
			copy(children, kids.Elements)
			return VView{
				Tag: "sheet",
				Attrs: []VAttr{
					{Name: "open", Value: open},
					{Name: "outlet", Value: outlet},
				},
				Msg:      onDismiss, // dispatched on dismiss
				Children: children,
			}, nil
		}),

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

		// uiConfirm : { title, confirmLabel, destructive, onConfirm,
		//               onCancel } -> View msg
		//
		// Modal destructive-action confirmation. The view itself
		// carries the dialog's config; the platform renderers turn
		// it into a SwiftUI .confirmationDialog (iOS) or a
		// position:fixed alert overlay (web). When the parent's
		// `case` returns `UI.empty` instead, the dialog isn't
		// mounted at all — so `isPresented` is implicit in whether
		// the view ever appears in the tree.
		"uiConfirm": nativeFn(1, func(args []Value) (Value, error) {
			rec, ok := args[0].(VRecord)
			if !ok {
				return nil, fmt.Errorf("UI.confirm: expected config record (got %T)", args[0])
			}
			title, ok := rec.Fields["title"].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.confirm: `title` must be String")
			}
			confirmLabel, ok := rec.Fields["confirmLabel"].(VString)
			if !ok {
				return nil, fmt.Errorf("UI.confirm: `confirmLabel` must be String")
			}
			destructive, ok := rec.Fields["destructive"].(VBool)
			if !ok {
				return nil, fmt.Errorf("UI.confirm: `destructive` must be Bool")
			}
			onConfirm, ok := rec.Fields["onConfirm"]
			if !ok {
				return nil, fmt.Errorf("UI.confirm: `onConfirm` is required")
			}
			onCancel, ok := rec.Fields["onCancel"]
			if !ok {
				return nil, fmt.Errorf("UI.confirm: `onCancel` is required")
			}
			return VView{
				Tag: "confirmDialog",
				Attrs: []VAttr{
					{Name: "title", Value: title},
					{Name: "confirmLabel", Value: confirmLabel},
					{Name: "destructive", Value: destructive},
					// Both message handlers stashed as attrs (rather
					// than the more typical `Msg` slot which holds
					// only one) because the dialog has two distinct
					// dispatch paths. Renderer reads both off Attrs
					// when wiring the buttons.
					{Name: "onConfirm", Value: onConfirm},
					{Name: "onCancel", Value: onCancel},
				},
			}, nil
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

// stringAttrCtor builds a 1-arg native that wraps its String input
// as an Attr record. Shared by navigationTitle / header / footer
// and any future modifier that reads as `Attr` carrying a label.
// `attrName` is the wire-format key the renderer reads back;
// `label` is the prefix on the type-error message (e.g.
// "UI.navigationTitle").
func stringAttrCtor(attrName, label string) Value {
	return nativeFn(1, func(args []Value) (Value, error) {
		s, ok := args[0].(VString)
		if !ok {
			return nil, fmt.Errorf("%s: expected String (got %T)", label, args[0])
		}
		return makeAttr(attrName, s), nil
	})
}

// viewAttrCtor is the View-typed counterpart to stringAttrCtor.
// Used by trailing / leading where the attr value is a child View
// (a toolbar item) rather than a label.
func viewAttrCtor(attrName, label string) Value {
	return nativeFn(1, func(args []Value) (Value, error) {
		v, ok := args[0].(VView)
		if !ok {
			return nil, fmt.Errorf("%s: expected View (got %T)", label, args[0])
		}
		return makeAttr(attrName, v), nil
	})
}

// textLeaf builds a 1-arg native that emits a VView with the
// supplied tag and the input String in Text. Used by uiText /
// uiTitle / uiSubtitle — three primitives that share the same
// "wrap a string as a view leaf" shape.
func textLeaf(viewTag, label string) Value {
	return nativeFn(1, func(args []Value) (Value, error) {
		s, ok := args[0].(VString)
		if !ok {
			return nil, fmt.Errorf("%s: expected String (got %T)", label, args[0])
		}
		return VView{Tag: viewTag, Text: s.V}, nil
	})
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

// lengthValue wraps an Int with a "unit" tag — the runtime representation
// of the typecheck's opaque Width / Height types. The renderer reads the
// __unit field to know whether the amount is chars or lines (and could,
// in the future, accept additional units without changing the API).
func lengthValue(unit string, amount int64) VRecord {
	return VRecord{
		Fields: map[string]Value{
			"__unit": VString{V: unit},
			"amount": VInt{V: amount},
		},
		Order: []string{"__unit", "amount"},
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
