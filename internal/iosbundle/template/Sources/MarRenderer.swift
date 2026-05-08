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

import SwiftUI

struct MarRenderer: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    var body: some View {
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

        case "button":
            Button(view.text) {
                if let msg = view.msg {
                    dispatch(msg)
                }
            }
            .buttonStyle(.bordered)
            .disabled(boolAttr("disabled"))

        case "link":
            if let href = attrString("href"), let url = URL(string: href) {
                Link(view.text, destination: url)
            } else {
                Text(view.text).foregroundStyle(.tint)
            }

        case "empty":
            EmptyView()

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
            Form {
                ForEach(0..<view.children.count, id: \.self) { i in
                    MarRenderer(view: view.children[i], dispatch: dispatch)
                }
            }

        case "uiList":
            List {
                ForEach(0..<view.children.count, id: \.self) { i in
                    MarRenderer(view: view.children[i], dispatch: dispatch)
                }
            }

        case "uiSection":
            MarUISection(view: view, dispatch: dispatch)

        case "hstack":
            HStack(alignment: .center, spacing: 12) {
                ForEach(0..<view.children.count, id: \.self) { i in
                    MarRenderer(view: view.children[i], dispatch: dispatch)
                }
            }

        case "vstack":
            VStack(alignment: .leading, spacing: 8) {
                ForEach(0..<view.children.count, id: \.self) { i in
                    MarRenderer(view: view.children[i], dispatch: dispatch)
                }
            }

        case "textField":
            MarUITextField(view: view, dispatch: dispatch)

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

    private var trailingView: MarView? { findToolbarView("trailing") }
    private var leadingView: MarView? { findToolbarView("leading") }

    private func findToolbarView(_ name: String) -> MarView? {
        for a in view.attrs where a.name == name {
            if case .view(let v) = a.value { return v }
        }
        return nil
    }
}

/// SwiftUI Section with header/footer pulled from mar attrs. Lives
/// inside a Form or List — outside, SwiftUI just stacks the contents.
private struct MarUISection: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    var body: some View {
        Section(
            header: header.map { Text($0) },
            footer: footer.map { Text($0) }
        ) {
            ForEach(0..<view.children.count, id: \.self) { i in
                MarRenderer(view: view.children[i], dispatch: dispatch)
            }
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
