// MarView → SwiftUI. Mirror of `createDOM` in runtime.js, with
// HTML-shaped output replaced by native iOS views.
//
// Each MarView tag maps to a SwiftUI primitive:
//
//   navigationStack  → NavigationStack content (.navigationTitle/.toolbar)
//   form             → Form
//   uiList           → List
//   uiSection        → Section(header:, footer:)
//   hstack / vstack  → HStack / VStack
//   textField        → TextField (or SecureField for password attrs)
//   button           → Button (dispatches msg on tap)
//   link             → Link (external URL) / NavigationLink (internal)
//   title / subtitle → Text + .font(.title2.weight(.bold)) / .headline
//   text             → Text
//   centered         → child wrapped in a max-size frame
//   empty            → EmptyView
//   spacer           → Spacer (push siblings along stack's main axis)
//   toggle           → Toggle bound to a Bool -> msg callback

import SwiftUI

struct MarRenderer: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    var body: some View {
        // Universal layout pass — every view honors the `width fill`
        // / `height fill` sizing attrs (the iOS mirror of the web's
        // applyLayoutAttrs + .mar-w-fill / .mar-h-fill classes).
        // Wrapped around the per-tag content so the attr composes
        // with any tag, exactly like the web's shared createDOM tail.
        content.modifier(MarFillSizing(view: view))
    }

    @ViewBuilder
    private var content: some View {
        switch view.tag {
        case "title":
            Text(view.text)
                .font(.title2.weight(.bold))
        case "subtitle":
            Text(view.text)
                .font(.headline)
                .foregroundStyle(.secondary)
        case "text":
            Text(view.text)
        case "errorText":
            // Red + semi-bold for destructive-state messages. Mirrors
            // the .mar-error-text CSS class in the JS renderer.
            Text(view.text)
                .font(.body.weight(.semibold))
                .foregroundStyle(.red)
                .accessibilityAddTraits(.isStaticText)

        case "image":
            // AsyncImage from the src URL. Mirrors the JS renderer's
            // <img class="mar-image"> + applyImageAttrs.
            MarUIImage(view: view)

        case "button":
            Button(view.text) {
                if let msg = view.msg {
                    dispatch(msg)
                }
            }
            .buttonStyle(.bordered)
            .disabled(boolAttr("disabled"))

        case "navigationLink":
            // Mar's `navigationLink` always wraps a child View
            // (mar's `text "label"` for the single-line case, a
            // vstack for the multi-line / list-row case). The
            // href encodes the destination path the user typed
            // into `navigationLink`.
            //
            // The href shape dictates which SwiftUI primitive we
            // use:
            //
            //   - Internal mar path (leading "/"): push onto the
            //     ambient NavigationStack via NavigationLink(value:).
            //     StackShell's navigationDestination(for: String.self)
            //     wakes up to render the destination.
            //
            //   - Absolute URL with scheme (https://, mailto:): hand
            //     off to the system via SwiftUI.Link — routes
            //     through LaunchServices to the appropriate app.
            //     Mar's `navigationLink` isn't supposed to take an
            //     external URL (it's for in-app navigation), but
            //     handling the case keeps the renderer robust if a
            //     future primitive wants to share the tag.
            //
            //   - Anything else: render the child plain (still
            //     visible, just not tappable).
            if let href = attrString("href") {
                let child = view.children.first
                if href.hasPrefix("/") {
                    NavigationLink(value: href) {
                        if let child {
                            MarRenderer(view: child, dispatch: dispatch)
                        }
                    }
                } else if let url = URL(string: href), url.scheme != nil {
                    Link(destination: url) {
                        if let child {
                            MarRenderer(view: child, dispatch: dispatch)
                        }
                    }
                } else if let child {
                    MarRenderer(view: child, dispatch: dispatch)
                }
            } else if let child = view.children.first {
                MarRenderer(view: child, dispatch: dispatch)
            }

        case "empty":
            EmptyView()

        case "spacer":
            // SwiftUI's Spacer expands along the parent stack's main
            // axis. Inside an HStack it pushes siblings apart
            // horizontally; inside a VStack, vertically. Standalone
            // it's a max-width invisible filler.
            Spacer()

        case "toggle":
            MarUIToggle(view: view, dispatch: dispatch)

        // ---------- UI.* (SwiftUI-style declarative vocabulary) ----------
        //
        // These tags map straight onto their SwiftUI namesakes, so
        // user code that wrote `navigationStack [ navigationTitle "x" ]
        // [ form [ section [...] [...] ] ]` gets a real
        // NavigationStack { Form { Section { ... } } } with all the
        // platform chrome (safe area, swipe-back, table styling)
        // out of the box.

        case "navigationStack":
            MarNavigationStack(view: view, dispatch: dispatch)

        case "form":
            // Lift editMode to the Form level so SwiftUI's chrome
            // (drag handles ≡, delete circles, etc.) shows up for
            // any Section with .onMove. editMode propagates DOWN
            // from Form into descendants — setting it inside a
            // Section is too deep, the Form above doesn\'t see it
            // and its chrome stays inactive.
            //
            // We scan children to detect whether ANY section
            // wants edit mode. Trade-off: when one section enters
            // editing, the entire Form\'s chrome activates — for
            // sections WITHOUT .onMove this is a visual no-op
            // (no handle to draw), so it doesn\'t bleed weird UI
            // into unrelated sections. In practice a page tends
            // to have one reorderable section per Form, so this
            // matches user expectations.
            Form {
                ForEach(0..<view.children.count, id: \.self) { i in
                    MarRenderer(view: view.children[i], dispatch: dispatch)
                }
            }
            .environment(\.editMode, .constant(anySectionEditing(view.children) ? .active : .inactive))

        case "uiList":
            List {
                ForEach(0..<view.children.count, id: \.self) { i in
                    MarRenderer(view: view.children[i], dispatch: dispatch)
                }
            }
            .environment(\.editMode, .constant(anySectionEditing(view.children) ? .active : .inactive))

        case "uiSection", "uiKeyedList":
            // Both render as SwiftUI Section with optional header/footer.
            // MarUISection reads onMove/onDelete attrs and uses the
            // per-child `key` attr (injected by UI.keyed for keyedList
            // children) as the ForEach identity. Sharing the renderer
            // is safe because the type-level distinction is enforced
            // by the typechecker — uiKeyedList children always carry
            // keys, while uiSection children may or may not.
            MarUISection(view: view, dispatch: dispatch)

        case "hstack":
            // SwiftUI-style: children HUG their content — no stretch.
            // To distribute, insert a `spacer` (pushes siblings
            // apart) or give a child `width fill` (claims the free
            // space) — mirroring HStack + Spacer() +
            // .frame(maxWidth: .infinity). textField stays greedy on
            // its own (like SwiftUI's TextField), so
            // `hstack [ textField, button ]` still works.
            //
            // CSS counterpart (kept in lockstep):
            //   .mar-hstack > *           { flex: 0 1 auto }   (hug)
            //   .mar-hstack > button      { flex: 0 0 auto }
            //   .mar-hstack > .mar-w-fill { flex: 1 1 0 }
            //   .mar-spacer / textField   → greedy
            //
            // The `align` attr picks the cross-axis (vertical)
            // alignment: top / center / bottom; default center.
            HStack(alignment: hstackAlignment(), spacing: 12) {
                ForEach(0..<view.children.count, id: \.self) { i in
                    MarRenderer(view: view.children[i], dispatch: dispatch)
                }
            }

        case "vstack":
            // Two modes, mirroring the web's align-items handling:
            //
            //   - No `align` attr (default): children FILL the column
            //     width (web's `align-items: stretch`), left-aligned.
            //     The pragmatic deviation from SwiftUI's hug-width
            //     VStack — column-fill is what almost every layout
            //     wants, and it keeps web + iOS in lockstep.
            //
            //   - `align leading/center/trailing`: children HUG and
            //     sit at that cross-axis position (web's align-items:
            //     flex-start/center/flex-end). The stack itself still
            //     spans the full width so the position is meaningful.
            if let al = vstackAlignment() {
                VStack(alignment: al, spacing: 8) {
                    ForEach(0..<view.children.count, id: \.self) { i in
                        MarRenderer(view: view.children[i], dispatch: dispatch)
                    }
                }
                .frame(maxWidth: .infinity,
                       alignment: Alignment(horizontal: al, vertical: .center))
            } else {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(0..<view.children.count, id: \.self) { i in
                        MarRenderer(view: view.children[i], dispatch: dispatch)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
            }

        case "textField":
            MarUITextField(view: view, dispatch: dispatch)

        case "textArea":
            MarUITextArea(view: view, dispatch: dispatch)

        case "picker":
            MarUIPicker(view: view, dispatch: dispatch)

        case "datePicker":
            MarUIDatePicker(view: view, dispatch: dispatch)

        case "centered":
            // Wraps a single child in a frame that fills the
            // available space and centers it. Used for full-
            // screen Loading / EmptyState views. The child's
            // intrinsic size is preserved; just the surrounding
            // container is stretched.
            Group {
                if let child = view.children.first {
                    MarRenderer(view: child, dispatch: dispatch)
                } else {
                    EmptyView()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .center)

        case "sheet":
            // iOS page sheet — same modal-from-bottom behavior we get
            // on web. SwiftUI's .sheet attaches at the window level
            // regardless of where the trigger view sits in the tree,
            // so anchoring the modifier on a zero-size Color.clear
            // works fine and keeps the rendered output invisible when
            // the sheet is closed.
            //
            // The Binding's getter reads our static "open" attr off
            // the view; the setter only acts on dismissals (false
            // transitions) — it dispatches the user's onDismiss Msg
            // so the parent Model flips its open flag, which on the
            // next render brings `open` back to false naturally.
            MarUISheet(view: view, dispatch: dispatch)

        case "confirmDialog":
            // SwiftUI .confirmationDialog — the system modal that
            // matches Apple Music's "delete from library" dialog.
            // Implemented as a zero-size Color.clear with the
            // modifier attached at this point in the tree; SwiftUI
            // routes the actual presentation to window-level
            // regardless. When the Mar program returns this view,
            // the dialog is open; when it returns UI.empty, the
            // view unmounts and the dialog dismisses.
            MarUIConfirmDialog(view: view, dispatch: dispatch)

        case "paragraph":
            // Flowing inline text. Each child is a `span` carrying its
            // own inline attrs; we fold them into one AttributedString
            // so SwiftUI renders the run styling — and, for link spans,
            // a tappable link that opens via the system — as a single
            // wrapping Text. Mirrors the .mar-paragraph / .mar-inline-*
            // CSS on web.
            Text(view.children.reduce(into: AttributedString()) { acc, span in
                acc.append(attributedFromSpan(span))
            })
            .frame(maxWidth: .infinity, alignment: .leading)

        default:
            // Unknown tag — render children straight so we don't
            // lose content when the user adds something not yet
            // mapped (e.g. a future view type).
            VStack(alignment: .leading) {
                ForEach(0..<view.children.count, id: \.self) { i in
                    MarRenderer(view: view.children[i], dispatch: dispatch)
                }
            }
        }
    }

    private func attrString(_ name: String) -> String? {
        for a in view.attrs where a.name == name {
            if case .string(let s) = a.value { return s }
        }
        return nil
    }

    private func boolAttr(_ name: String) -> Bool {
        for a in view.attrs where a.name == name {
            if case .bool(let b) = a.value { return b }
        }
        return false
    }

    // Cross-axis alignment from the stack `align` attr. Each stack
    // honors only its own axis's values plus center — vstack:
    // leading/center/trailing, hstack: top/center/bottom; a
    // wrong-axis value is ignored (falls back to the default), the
    // same policy as the web renderer's applyAlignAttr.
    private func vstackAlignment() -> HorizontalAlignment? {
        switch attrString("align") {
        case "leading":  return .leading
        case "center":   return .center
        case "trailing": return .trailing
        default:         return nil
        }
    }

    private func hstackAlignment() -> VerticalAlignment {
        switch attrString("align") {
        case "top":    return .top
        case "bottom": return .bottom
        default:       return .center
        }
    }
}

// MarFillSizing applies the universal `width fill` / `height fill`
// sizing attrs (Size records with __unit == "fill") to any rendered
// view — SwiftUI's .frame(maxWidth/maxHeight: .infinity), the iOS
// mirror of the web's contextual .mar-w-fill / .mar-h-fill classes.
// Alignment is leading/top so a filling text reads like a row cell
// (web parity: a stretched child's text stays left-aligned, not
// centered in its claimed space). chars / lines stay the inputs' own
// concern (MarUITextField / MarUITextArea read them directly); fill
// is the only Size unit that means something on arbitrary views.
private struct MarFillSizing: ViewModifier {
    let view: MarView

    @ViewBuilder
    func body(content: Content) -> some View {
        switch (hasFill("width"), hasFill("height")) {
        case (false, false):
            content
        case (true, false):
            content.frame(maxWidth: .infinity, alignment: .leading)
        case (false, true):
            content.frame(maxHeight: .infinity, alignment: .top)
        case (true, true):
            content.frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        }
    }

    private func hasFill(_ name: String) -> Bool {
        for a in view.attrs where a.name == name {
            if case .record(let fields, _) = a.value,
               case .string(let unit) = fields["__unit"] ?? .unit,
               unit == "fill" {
                return true
            }
        }
        return false
    }
}

// MARK: - Input kinds
//
// Mirrors the JS runtime's input-kind flag attrs so iOS Keychain,
// the keyboard, and AutoFill behave the same way password managers
// + form autofill do on the web. Each attr maps to a slot in
// `InputAttrs`; multiple attrs combine (e.g. `[numeric, oneTimeCode]`
// gets the numeric keyboard AND the iOS Mail OTP autofill).

private struct InputAttrs {
    var keyboardType: UIKeyboardType = .default
    var textContentType: UITextContentType? = nil
    var isSecure: Bool = false
}

/// Reads every input-kind flag attr off the view and folds them
/// into a single InputAttrs. Composable: `inputKindNumeric` plus
/// `inputKindOneTimeCode` gives a numeric keypad with the iOS
/// "Code from Mail" autofill suggestion.
private func readInputAttrs(_ view: MarView) -> InputAttrs {
    var attrs = InputAttrs()
    for a in view.attrs {
        switch a.name {
        case "inputKindEmail":
            attrs.keyboardType = .emailAddress
            attrs.textContentType = .emailAddress
        case "inputKindPassword":
            attrs.isSecure = true
            attrs.textContentType = .password
        case "inputKindNewPassword":
            attrs.isSecure = true
            attrs.textContentType = .newPassword
        case "inputKindNumeric":
            attrs.keyboardType = .numberPad
        case "inputKindOneTimeCode":
            attrs.textContentType = .oneTimeCode
        case "inputKindNumericCode":
            // UI.numericCode bundles numeric keypad + Code-from-Mail
            // autofill — the OTP/2FA case in one attr.
            attrs.keyboardType = .numberPad
            attrs.textContentType = .oneTimeCode
        default: break
        }
    }
    return attrs
}

/// Returns the Msg attached as `submit` on the view, or nil if absent.
private func submitAttr(_ view: MarView) -> MarValue? {
    for attr in view.attrs where attr.name == "submit" {
        return attr.value
    }
    return nil
}

/// Builds the styled AttributedString fragment for one `span` inline
/// atom. Reads the same flag attrs the JS renderer keys off —
/// inlineBold / inlineItalic / inlineStrikethrough / inlineCode — plus
/// inlineLink, which carries the destination URL. Attrs compose: a
/// span tagged [code, italic] renders monospaced + italic; a link span
/// becomes a tappable run that SwiftUI opens via the system.
private func attributedFromSpan(_ span: MarView) -> AttributedString {
    var s = AttributedString(span.text)
    var isBold = false
    var isItalic = false
    var isCode = false
    var isStrike = false
    var linkURL: URL? = nil
    for a in span.attrs {
        switch a.name {
        case "inlineBold":          isBold = true
        case "inlineItalic":        isItalic = true
        case "inlineStrikethrough": isStrike = true
        case "inlineCode":          isCode = true
        case "inlineLink":
            if case .string(let u) = a.value { linkURL = URL(string: u) }
        default:
            break
        }
    }
    var font: Font = isCode ? .system(.body, design: .monospaced) : .body
    if isBold { font = font.bold() }
    if isItalic { font = font.italic() }
    s.font = font
    if isStrike { s.strikethroughStyle = .single }
    if let url = linkURL {
        s.link = url
        s.foregroundColor = .accentColor
        s.underlineStyle = .single
    }
    return s
}

// MARK: - UI.* (SwiftUI-style declarative vocabulary)
//
// These bridges render mar's UI module nodes onto SwiftUI primitives:
// `navigationStack` → NavigationStack with .navigationTitle + .toolbar;
// `uiSection` → Section(header: footer:); `textField` → TextField with
// the input-kind attrs above.

/// Renders the body of a mar `navigationStack` view. Does NOT wrap
/// in a SwiftUI NavigationStack itself — ContentView's StackShell
/// (and MarApp's MarSinglePageView) already provide one as ambient
/// chrome. Wrapping again would nest stacks, which SwiftUI handles
/// poorly (the inner content can render as a black screen).
///
/// Instead we rely on `.navigationTitle(...)` and `.toolbar { ... }`
/// being preference-based modifiers — they propagate up to whichever
/// NavigationStack is the nearest ancestor.
private struct MarNavigationStack: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    var body: some View {
        // Group (rather than VStack) so child layout-impacting views
        // — Form, List — render at their natural height. VStack
        // collapses Form to zero height because Form measures itself
        // against its container's height which VStack doesn't provide.
        Group {
            if view.children.count == 1 {
                MarRenderer(view: view.children[0], dispatch: dispatch)
            } else {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(0..<view.children.count, id: \.self) { i in
                        MarRenderer(view: view.children[i], dispatch: dispatch)
                    }
                }
            }
        }
        .navigationTitle(titleString)
        .toolbar {
            if let leading = leadingView {
                ToolbarItem(placement: .topBarLeading) {
                    MarRenderer(view: leading, dispatch: dispatch)
                }
            }
            if let trailing = trailingView {
                ToolbarItem(placement: .topBarTrailing) {
                    MarRenderer(view: trailing, dispatch: dispatch)
                }
            }
        }
    }

    private var titleString: String {
        for a in view.attrs where a.name == "navigationTitle" {
            if case .string(let s) = a.value { return s }
        }
        return ""
    }

    private var trailingView: MarView? { findToolbarView("topBarTrailing") }
    private var leadingView: MarView? { findToolbarView("topBarLeading") }

    private func findToolbarView(_ name: String) -> MarView? {
        for a in view.attrs where a.name == name {
            if case .view(let v) = a.value { return v }
        }
        return nil
    }
}

/// SwiftUI Section with header/footer pulled from mar attrs. Lives
/// inside a Form or List — outside, SwiftUI just stacks the contents.
///
/// When the section carries an `onMove` attr with editing=true the
/// children become reorderable via SwiftUI's native `.onMove`
/// modifier (drag handles appear, VoiceOver gestures for reorder
/// work out of the box). The editMode env is forced .active so
/// the handles show without requiring the app to toggle env state.
///
/// Dispatch translation: SwiftUI's `.onMove` signature is
/// `(IndexSet, Int) -> Void` where the destination is an
/// insertion index *as if* the moved items had not yet been
/// removed. Mar's `(from, to)` and `List.move` semantics use the
/// post-removal target. When destination > source we subtract 1
/// so the same (from, to) means the same thing on JS and iOS.
private struct MarUISection: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    var body: some View {
        let onMoveAttr = readOnMoveAttr()
        let editing = onMoveAttr?.editing ?? false

        // Use the row's `key` attr (when present) as the ForEach
        // identity, NOT the positional index. SwiftUI's .onMove
        // animation depends on stable per-row identity: with
        // index-based id, the row at slot 0 is "still the same
        // row" after a move, just with different content — so
        // SwiftUI cross-fades the content instead of sliding the
        // row to its new position. Using the key (injected by
        // `UI.keyed` on every KeyedView) tracks each row as it
        // moves, producing the smooth slide animation iOS users
        // expect.
        //
        // Falls back to "__idx_N" for rows without a key —
        // happens only inside plain `section` (which doesn\'t
        // support reorder/delete), where positional identity is
        // fine because the children don\'t mutate.
        //
        // Only attach .onMove when editing — outside of edit mode
        // we don't want reorder gestures available. The actual
        // edit-mode CHROME (drag handles ≡ at row edges, delete
        // circles for any .onDelete) is drawn by the SURROUNDING
        // Form/List based on the editMode environment value (see
        // the form/uiList cases in MarRenderer for the lift to
        // list-level scope).
        let identifiedRows = view.children.enumerated().map { (i, child) in
            IdentifiedRow(id: rowIdentity(child, index: i), child: child)
        }
        let onDeleteAttr = readOnDeleteAttr()
        Section(
            header: header.map { Text($0) },
            footer: footer.map { Text($0) }
        ) {
            ForEach(identifiedRows) { row in
                MarRenderer(view: row.child, dispatch: dispatch)
            }
            .onMove(perform: editing ? performMove : nil)
            // `.onDelete` is always attached when the attr is
            // present — SwiftUI uses the SAME modifier for both
            // swipe-to-delete (normal mode) and the edit-mode
            // minus-circle. The editing flag in the Mar attr is
            // informational: iOS surfaces both at all times. (Web
            // doesn't have swipe so it uses editing to switch
            // between hover-reveal and permanent-LEFT layouts.)
            .onDelete(perform: onDeleteAttr != nil ? performDelete : nil)
        }
    }

    private var header: String? { attrString("header") }
    private var footer: String? { attrString("footer") }

    private func attrString(_ name: String) -> String? {
        for a in view.attrs where a.name == name {
            if case .string(let s) = a.value { return s.isEmpty ? nil : s }
        }
        return nil
    }

    private func performMove(from source: IndexSet, to destination: Int) {
        guard let attr = readOnMoveAttr(), let handler = attr.handler else { return }
        guard let from = source.first else { return }
        let to = (destination > from) ? destination - 1 : destination
        if from == to { return }
        do {
            let partial = try Eval.apply(handler, .int(from))
            let msg = try Eval.apply(partial, .int(to))
            Task { @MainActor in dispatch(msg) }
        } catch {
            // Same posture as other Mar effects: msg-build failure
            // logs in the console but doesn't crash the UI.
        }
    }

    private struct OnMoveAttr {
        let editing: Bool
        let handler: MarValue?
    }

    private func readOnMoveAttr() -> OnMoveAttr? {
        for a in view.attrs where a.name == "onMove" {
            if case .record(let fields, _) = a.value {
                var editing = false
                if case .bool(let b) = fields["editing"] ?? .unit { editing = b }
                return OnMoveAttr(editing: editing, handler: fields["handler"])
            }
        }
        return nil
    }

    private struct OnDeleteAttr {
        let editing: Bool
        let handler: MarValue?
    }

    private func readOnDeleteAttr() -> OnDeleteAttr? {
        for a in view.attrs where a.name == "onDelete" {
            if case .record(let fields, _) = a.value {
                var editing = false
                if case .bool(let b) = fields["editing"] ?? .unit { editing = b }
                return OnDeleteAttr(editing: editing, handler: fields["handler"])
            }
        }
        return nil
    }

    /// SwiftUI's `.onDelete` passes an IndexSet of rows to remove. We
    /// dispatch the Mar handler once per index — Mar's signature is
    /// `Int -> Msg`, so multiple deletes (rare in a single gesture
    /// but possible via multi-select in edit mode) become multiple
    /// dispatches in descending-index order. Descending so each
    /// subsequent index is still valid against the pre-deletion
    /// model: removing index 5 first then index 2 shifts nothing; the
    /// other order would silently delete the wrong row.
    private func performDelete(at offsets: IndexSet) {
        guard let attr = readOnDeleteAttr(), let handler = attr.handler else { return }
        let indices = offsets.sorted(by: >)
        for idx in indices {
            do {
                let msg = try Eval.apply(handler, .int(idx))
                Task { @MainActor in dispatch(msg) }
            } catch {
                // Mar effects don't crash the UI on dispatch failure
                // — log silently and move on.
            }
        }
    }
}

/// Mar's UI.toggle — SwiftUI Toggle with a label, a current-value
/// attr, and a `Bool -> msg` callback bound to the binding's setter.
/// SwiftUI handles the platform-native iOS switch chrome; we just
/// wire the binding so flips become dispatched messages.
private struct MarUIToggle: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    var body: some View {
        Toggle(view.text, isOn: binding)
    }

    private var currentValue: Bool {
        for a in view.attrs where a.name == "isOn" {
            if case .bool(let b) = a.value { return b }
        }
        return false
    }

    private var binding: Binding<Bool> {
        Binding(
            get: { currentValue },
            set: { newValue in
                guard let onChange = view.msg else { return }
                if let msg = try? Eval.apply(onChange, .bool(newValue)) {
                    dispatch(msg)
                }
            }
        )
    }
}

/// Mar's UI.sheet — anchors a SwiftUI `.sheet(isPresented:)` modifier
/// on a zero-size invisible view. The sheet itself renders at the
/// window level (SwiftUI handles that), so the anchor position
/// doesn't matter.
///
/// The binding is one-way in practice: `get` reads our static `open`
/// attr, `set` only acts on dismissal transitions (true → false). The
/// dismissal Msg goes to the parent's update which flips the flag in
/// the Model; the next render sees `open=false` and SwiftUI knows to
/// take down the sheet.
private struct MarUISheet: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    var body: some View {
        Color.clear
            .frame(width: 0, height: 0)
            .sheet(isPresented: binding) {
                ForEach(0..<view.children.count, id: \.self) { i in
                    MarRenderer(view: view.children[i], dispatch: dispatch)
                }
            }
    }

    private var isOpen: Bool {
        for a in view.attrs where a.name == "open" {
            if case .bool(let b) = a.value { return b }
        }
        return false
    }

    private var binding: Binding<Bool> {
        Binding(
            get: { isOpen },
            set: { newValue in
                // We only react to "user dismissed the sheet" — when
                // SwiftUI flips the binding from true → false. The
                // opposite transition is driven by the parent's Model;
                // attempting to honor it here would create a loop.
                if !newValue, let msg = view.msg {
                    dispatch(msg)
                }
            }
        )
    }
}

/// Mar's UI.confirm — SwiftUI .confirmationDialog modifier anchored on a
/// zero-size Color.clear. Same trick as MarUISheet: SwiftUI routes the
/// actual modal presentation to window level, so the anchor view's
/// position in the tree doesn't matter visually.
///
/// "Is presented" is implicit in whether this view is mounted at all
/// (the Mar program returns UI.confirm when active, UI.empty when not).
/// The binding's getter returns true while we're mounted; its setter
/// fires onCancel when SwiftUI flips it false (user tapped outside,
/// swiped down, or hit a Cancel button that SwiftUI added on its own
/// — though we provide our own explicit Cancel button via .cancel role).
private struct MarUIConfirmDialog: View {
    let view: MarView
    let dispatch: (MarValue) -> Void
    @State private var isPresented: Bool = true

    var body: some View {
        Color.clear
            .frame(width: 0, height: 0)
            .confirmationDialog(
                titleText,
                isPresented: Binding(
                    get: { isPresented },
                    set: { newValue in
                        // SwiftUI flips this to false on dismissal
                        // (Cancel button, outside tap, swipe down).
                        // Fire onCancel — the parent's Model will
                        // remove this view from the tree on the next
                        // render, and the local @State will be
                        // discarded along with it.
                        if !newValue {
                            isPresented = false
                            dispatchAttr("onCancel")
                        }
                    }
                ),
                titleVisibility: .visible
            ) {
                Button(confirmLabel, role: destructive ? .destructive : nil) {
                    dispatchAttr("onConfirm")
                }
                Button("Cancel", role: .cancel) {
                    dispatchAttr("onCancel")
                }
            }
    }

    private var titleText: String {
        for a in view.attrs where a.name == "title" {
            if case .string(let s) = a.value { return s }
        }
        return ""
    }
    private var confirmLabel: String {
        for a in view.attrs where a.name == "confirmLabel" {
            if case .string(let s) = a.value { return s }
        }
        return "Confirm"
    }
    private var destructive: Bool {
        for a in view.attrs where a.name == "destructive" {
            if case .bool(let b) = a.value { return b }
        }
        return false
    }
    private func dispatchAttr(_ name: String) {
        for a in view.attrs where a.name == name {
            // The handler is whatever Msg the user passed (a constructor
            // value, typically a no-arg Ctor like `DeleteConfirmed`).
            // We pass it straight to dispatch.
            dispatch(a.value)
            return
        }
    }
}

/// Mar's UI.textField — TextField with a placeholder argument and the
/// shared input-kind / submit logic.
private struct MarUITextField: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    var body: some View {
        let attrs = readInputAttrs(view)
        Group {
            if attrs.isSecure {
                SecureField(placeholder, text: binding)
            } else {
                TextField(placeholder, text: binding)
            }
        }
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
        .keyboardType(attrs.keyboardType)
        .textContentType(attrs.textContentType)
        .onSubmit {
            if let submitMsg = submitAttr(view) {
                dispatch(submitMsg)
            }
        }
        // Honor `disabled` on text fields too — not just buttons.
        // Without this, the JS side correctly locks the input
        // during a busy/loading service call but iOS keeps the
        // field editable: the operator can keep typing AFTER
        // pressing Send / Submit, and their post-submit keystrokes
        // silently land in the model AFTER the response resets
        // draft="" — a confusing "where did my text go" moment.
        // Mirrors the .disabled() applied to the button case
        // upstream in the renderer.
        .disabled(isDisabled(view))
        // Apply width attr when present. The unit is always `chars`
        // (the type system rules out `width (lines N)` at compile
        // time), so we translate to an approximate point width:
        // N * average-character-width at the body font. 9pt per char
        // is a rough approximation for the system body font; it's
        // not pixel-perfect with the running font but close enough
        // that `chars 6` feels like 6 character columns.
        .frame(maxWidth: widthInPoints(view))
    }

    private var placeholder: String {
        for a in view.attrs where a.name == "placeholder" {
            if case .string(let s) = a.value { return s }
        }
        return ""
    }

    private var binding: Binding<String> {
        Binding(
            get: { view.text },
            set: { newValue in
                guard let onChange = view.msg else { return }
                if let msg = try? Eval.apply(onChange, .string(newValue)) {
                    dispatch(msg)
                }
            }
        )
    }
}

/// Mar's UI.textArea — multi-line TextEditor mirroring textField's
/// wiring (value binding, width / height sizing, disabled). TextEditor
/// has no placeholder, so we overlay one while the value is empty.
private struct MarUITextArea: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    var body: some View {
        let lines = heightInLines(view) ?? 4
        ZStack(alignment: .topLeading) {
            if view.text.isEmpty {
                Text(placeholder)
                    .foregroundStyle(.secondary)
                    .padding(.top, 8)
                    .padding(.leading, 5)
                    .allowsHitTesting(false)
            }
            TextEditor(text: binding)
                .frame(minHeight: CGFloat(lines) * 22)
                .scrollContentBackground(.hidden)
        }
        .frame(maxWidth: widthInPoints(view))
        .disabled(isDisabled(view))
        .overlay(
            RoundedRectangle(cornerRadius: 8)
                .stroke(Color.secondary.opacity(0.3), lineWidth: 1)
        )
    }

    private var placeholder: String {
        for a in view.attrs where a.name == "placeholder" {
            if case .string(let s) = a.value { return s }
        }
        return ""
    }

    private var binding: Binding<String> {
        Binding(
            get: { view.text },
            set: { newValue in
                guard let onChange = view.msg else { return }
                if let msg = try? Eval.apply(onChange, .string(newValue)) {
                    dispatch(msg)
                }
            }
        )
    }
}

/// Mar's UI.picker — single-selection menu. The builtin stashes the
/// selected value, the option list, and the `(a -> String)` label fn
/// as attrs; the `(a -> msg)` onChange rides on `view.msg`. MarValue
/// isn't Hashable, so we tag options by index and match the current
/// selection by its rendered label (unique per option in a picker).
private struct MarUIPicker: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    var body: some View {
        let options = pickerOptions(view)
        let labels = options.map { pickerLabel(view, $0) }
        let selected = labels.firstIndex(of: pickerLabel(view, attrValue(view, "selected") ?? .unit)) ?? 0
        Picker("", selection: Binding<Int>(
            get: { selected },
            set: { i in
                guard i >= 0, i < options.count, let onChange = view.msg else { return }
                if let msg = try? Eval.apply(onChange, options[i]) {
                    dispatch(msg)
                }
            }
        )) {
            ForEach(0..<labels.count, id: \.self) { i in
                Text(labels[i]).tag(i)
            }
        }
        .pickerStyle(.menu)
        .labelsHidden()
        .disabled(isDisabled(view))
    }
}

/// Mar's UI.datePicker — a date-only field. The builtin stashes the
/// current Time as the `value` attr; the `(Time -> msg)` onChange rides
/// on `view.msg`. SwiftUI's DatePicker shows the device-local calendar
/// date; on change we normalize to the start of that local day (the web
/// renderer does the same) and hand back a Mar `.time`.
private struct MarUIDatePicker: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    private var current: Date {
        // value is a concrete Time — datePicker is pure; the program owns
        // the value and seeds "today" via `Cmd.perform GotToday Time.now`.
        // Read it directly, no clock fallback.
        if case .time(let ms)? = attrValue(view, "value") {
            return Date(timeIntervalSince1970: Double(ms) / 1000.0)
        }
        return Date(timeIntervalSince1970: 0)
    }

    var body: some View {
        DatePicker("", selection: Binding<Date>(
            get: { current },
            set: { newDate in
                guard let onChange = view.msg else { return }
                let day = Calendar.current.startOfDay(for: newDate)
                let ms = Int((day.timeIntervalSince1970 * 1000).rounded())
                if let msg = try? Eval.apply(onChange, .time(ms)) {
                    dispatch(msg)
                }
            }
        ), displayedComponents: [.date])
        .labelsHidden()
        .disabled(isDisabled(view))
    }
}

/// Reads the first attr named `name` off a view, or nil.
private func attrValue(_ view: MarView, _ name: String) -> MarValue? {
    for a in view.attrs where a.name == name { return a.value }
    return nil
}

/// The option list packed onto a picker view by the builtin.
private func pickerOptions(_ view: MarView) -> [MarValue] {
    if case .list(let xs)? = attrValue(view, "options") { return xs }
    return []
}

/// Applies the picker's `toLabel` function to one option value,
/// returning the display string ("" if it isn't a function/string).
private func pickerLabel(_ view: MarView, _ value: MarValue) -> String {
    guard let fn = attrValue(view, "toLabel"),
          let r = try? Eval.apply(fn, value),
          case .string(let s) = r else { return "" }
    return s
}

/// UI.image — AsyncImage with optional fixed size + content mode.
/// Without a `size` attr the image fills its container width and keeps
/// its aspect ratio; with one it pins width + height in points. fit
/// (default) shows the whole image; fill crops to cover. alt is always
/// present (required record field; "" = decorative).
private struct MarUIImage: View {
    let view: MarView

    var body: some View {
        let (w, h) = imageSize(view)
        let cover = imagePresent(view, "contentModeCover")
        AsyncImage(url: URL(string: imageAttrString(view, "src"))) { phase in
            if let image = phase.image {
                image
                    .resizable()
                    .aspectRatio(contentMode: cover ? .fill : .fit)
            } else if phase.error != nil {
                Color.gray.opacity(0.15)
            } else {
                Color.gray.opacity(0.08)
            }
        }
        .frame(width: w, height: h)
        .frame(maxWidth: w == nil ? .infinity : nil)
        .clipped()
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .accessibilityLabel(imageAttrString(view, "alt"))
    }
}

/// Reads a String attr off an image view, "" if absent.
private func imageAttrString(_ view: MarView, _ name: String) -> String {
    for a in view.attrs where a.name == name {
        if case .string(let s) = a.value { return s }
    }
    return ""
}

/// True when a flag attr (carrying .unit) is present.
private func imagePresent(_ view: MarView, _ name: String) -> Bool {
    for a in view.attrs where a.name == name { return true }
    return false
}

/// Reads the `size` attr ({w, h} of px length-values) into
/// (width?, height?) in points. nil when absent or malformed.
private func imageSize(_ view: MarView) -> (CGFloat?, CGFloat?) {
    for a in view.attrs where a.name == "size" {
        if case .record(let fields, _) = a.value {
            return (pxAmount(fields["w"]), pxAmount(fields["h"]))
        }
    }
    return (nil, nil)
}

/// Unwraps a px length-value record ({__unit:"px", amount:N}) to N.
private func pxAmount(_ v: MarValue?) -> CGFloat? {
    if case .record(let fields, _)? = v,
       case .int(let n)? = fields["amount"] {
        return CGFloat(n)
    }
    return nil
}

// isDisabled reads the `disabled` attr off a view. File-scope helper
// so any of the per-tag private structs (MarUITextField,
// MarUIToggle, etc.) can use it without the outer MarRenderer
// instance's private boolAttr method. Defaults to false when the
// attr is missing — same fallback the button case at the top of
// MarRenderer uses.
func isDisabled(_ view: MarView) -> Bool {
    for a in view.attrs where a.name == "disabled" {
        if case .bool(let b) = a.value { return b }
    }
    return false
}

// IdentifiedRow wraps a MarView with a stable String id so the
// SwiftUI ForEach inside MarUISection can use per-row identity
// for its move animation. The id comes from rowIdentity, which
// prefers the explicit `key` attr (injected by `UI.keyed` when
// building a KeyedView) and falls back to a positional placeholder.
struct IdentifiedRow: Identifiable {
    let id: String
    let child: MarView
}

// rowIdentity returns the row's `key` attr value, or a positional
// fallback when no key is present. Used by MarUISection's ForEach
// to track each row as it moves, so SwiftUI animates the position
// change as a slide instead of a content cross-fade.
func rowIdentity(_ v: MarView, index: Int) -> String {
    for a in v.attrs where a.name == "key" {
        if case .string(let s) = a.value { return s }
    }
    return "__idx_\(index)"
}

// anySectionEditing scans the given views for a uiSection that
// carries an `onMove` OR `onDelete` attr with editing=true. The
// Form / uiList cases call this at render time to decide whether
// to activate edit-mode chrome (drag handles ≡, delete circles)
// at the list-level environment.
//
// Why list-level: SwiftUI's editMode environment value flows
// DOWN from the enclosing List/Form into descendants — setting
// it inside a Section can\'t reach the Form\'s chrome rendering.
// So whichever section wants reorder/delete, we propagate up to
// the nearest list-shaped container.
//
// Both attrs are scanned because either independently warrants
// edit-mode chrome: onMove.editing surfaces drag handles,
// onDelete.editing surfaces the permanent red minus circle. An
// app might want one without the other (e.g. "let users delete
// rows but not reorder them").
func anySectionEditing(_ views: [MarView]) -> Bool {
    for v in views {
        if v.tag == "uiSection" || v.tag == "uiKeyedList" {
            for a in v.attrs where a.name == "onMove" || a.name == "onDelete" {
                if case .record(let fields, _) = a.value {
                    if case .bool(let b) = fields["editing"] ?? .unit, b {
                        return true
                    }
                }
            }
        }
    }
    return false
}

// widthInPoints reads a `width` attr off a view and translates the
// chars amount into approximate SwiftUI points. Returns nil if no
// width attr is set (so the caller can use .infinity for fill).
//
// Width = `amount * 11pt + 24pt`. The 11pt-per-char tracks the
// width of a digit in SF Body (17pt), the dominant case for sized
// inputs (numeric codes, ages, etc.); proportional letters in
// other inputs vary but average close to the same. The +24pt
// constant covers TextField\'s internal padding (~16pt) plus a
// few points of cursor / focus-ring room — without it, `chars 6`
// rendered as 54pt, which clipped the 6th digit of a verification
// code visibly.
//
// Returns CGFloat?.infinity-like nil when absent so the caller can
// pass it directly into .frame(maxWidth: ...) and get the default
// fill behavior.
func widthInPoints(_ view: MarView) -> CGFloat? {
    for a in view.attrs where a.name == "width" {
        if case .record(let fields, _) = a.value {
            if case .string(let unit) = fields["__unit"] ?? .unit,
               case .int(let amount) = fields["amount"] ?? .unit,
               unit == "chars" {
                // 11pt per char (SF body digit width) + 24pt for
                // TextField's internal padding + cursor room.
                return CGFloat(amount) * 11.0 + 24.0
            }
        }
    }
    return nil
}

// heightInLines reads a `height` attr and returns the lines amount.
// Used by textArea to drive SwiftUI's TextEditor frame.
func heightInLines(_ view: MarView) -> Int? {
    for a in view.attrs where a.name == "height" {
        if case .record(let fields, _) = a.value {
            if case .string(let unit) = fields["__unit"] ?? .unit,
               case .int(let amount) = fields["amount"] ?? .unit,
               unit == "lines" {
                return amount
            }
        }
    }
    return nil
}
