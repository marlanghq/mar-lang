// Canvas (v0.0.7) — the 2D draw-list, native mirror of the web canvas in
// internal/jsserve/runtime.js. The .mar builders assemble opaque Shape / Color
// / Transform values (rect / circle / Canvas.text / group / rgb / Canvas.
// Translate|Scale|Rotate|Left|Center|Right); `canvas` is a View carrying the
// shape list + the input attrs (onTap / watchSize / onDrag / onRelease /
// watchPointers). The
// SwiftUI `Canvas` renderer replays the list every frame and threads gestures +
// size back as Msgs.
//
// Unlike the web (which draws at 1 CSS px + image-rendering: pixelated to dodge
// a retina fill-rate trap — ADR-0001), SwiftUI's Canvas already works in points,
// so the shapes' coordinate space IS the size watchSize reports. No dpr scaling.
//
// Compile-checked, not run, in the build environment — pixels verified on device.

import Foundation
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

// MARK: - builtins

enum MarCanvas {
    static func register(_ env: Env) {
        func attr(_ name: String, _ value: MarValue) -> MarValue {
            .record(fields: ["name": .string(name), "value": value], order: ["name", "value"])
        }
        func collectAttrs(_ list: MarValue) -> [MarView.Attr] {
            guard case .list(let xs) = list else { return [] }
            return xs.compactMap {
                if case .record(let fs, _) = $0, case .string(let name)? = fs["name"] {
                    return MarView.Attr(name: name, value: fs["value"] ?? .unit)
                }
                return nil
            }
        }

        // Color: rgb r g b -> Color (opaque __Color ctor); rgba adds an
        // opacity percent int (0-100), since Mar has no floats.
        env.defineFn("rgb", "Canvas.rgb", 3) { a in .ctor(tag: "__Color", args: [a[0], a[1], a[2]], origin: nil) }
        env.defineFn("rgba", "Canvas.rgba", 4) { a in .ctor(tag: "__Color", args: [a[0], a[1], a[2], a[3]], origin: nil) }

        // Shapes.
        env.defineFn("rect", "Canvas.rect", 5) { a in .ctor(tag: "rect", args: a, origin: nil) }
        env.defineFn("circle", "Canvas.circle", 4) { a in .ctor(tag: "circle", args: a, origin: nil) }
        // triangle : x1 y1 x2 y2 x3 y3 color -> Shape  (a filled triangle)
        env.defineFn("triangle", "Canvas.triangle", 7) { a in .ctor(tag: "triangle", args: a, origin: nil) }
        // Canvas.text : x y size align color str  (bare key `canvasText` keeps
        // the bare name `text` free for the module that exposes it).
        env.defineFn("canvasText", "Canvas.text", 6) { a in .ctor(tag: "canvasText", args: a, origin: nil) }
        // group : List Transform -> List Shape -> Shape
        env.defineFn("group", "Canvas.group", 2) { a in .ctor(tag: "group", args: [a[0], a[1]], origin: nil) }

        // Transform / Align / Blend constructors (Canvas.Translate, ...) come
        // from the generated registry — see MarBuiltinCtors.swift.

        // CanvasMode constructors — global (bare), nullary, like Pointer's
        // Coarse / Fine. The mandatory first arg of `canvas`.
        env.define("Pixelated", .ctor(tag: "Pixelated", args: [], origin: nil))
        env.define("Crisp",     .ctor(tag: "Crisp", args: [], origin: nil))

        // canvas : CanvasMode -> List (Attr Canvas) -> List Shape -> View a.
        // The mode + shapes ride as attrs; MarCanvasView reads them + the
        // input attrs. (SwiftUI's Canvas already draws at native retina, so
        // Crisp is the free default here; the mode still rides for parity.)
        env.defineFn("canvas", "Canvas.canvas", 3) { a in
            var attrs = collectAttrs(a[1])
            attrs.append(MarView.Attr(name: "canvasMode", value: a[0]))
            attrs.append(MarView.Attr(name: "shapes", value: a[2]))
            return .view(MarView(tag: "canvas", attrs: attrs, children: [], text: "", msg: nil, key: nil))
        }

        // Input attrs -> Attr Canvas. Written as literal defineFn calls (not a
        // loop) so the drift test sees the dotted names. Occurrences carry
        // (Int -> Int -> msg); watchSize carries the box (Int -> Int -> msg) and
        // watchPointers the pointer list (List { id, x, y } -> msg).
        env.defineFn("onTap", "Canvas.onTap", 1) { a in attr("onTap", a[0]) }
        env.defineFn("watchSize", "Canvas.watchSize", 1) { a in attr("watchSize", a[0]) }
        env.defineFn("watchPointers", "Canvas.watchPointers", 1) { a in attr("watchPointers", a[0]) }
        env.defineFn("onRelease", "Canvas.onRelease", 1) { a in attr("onRelease", a[0]) }
        env.defineFn("onDrag", "Canvas.onDrag", 1) { a in attr("onDrag", a[0]) }
        // Desktop-input trio (parity). Registered so a shared .mar using them
        // compiles and runs on iOS. onAltTap fires on long-press; onHover and
        // onWheel are inert on pure touch (no hover / no wheel), exactly as the
        // web ones are — a trackpad/pencil mapping is a later refinement.
        env.defineFn("onHover", "Canvas.onHover", 1) { a in attr("onHover", a[0]) }
        env.defineFn("onAltTap", "Canvas.onAltTap", 1) { a in attr("onAltTap", a[0]) }
        env.defineFn("onWheel", "Canvas.onWheel", 1) { a in attr("onWheel", a[0]) }
    }
}

// MARK: - renderer

/// SwiftUI host for a `canvas` view. Replays the shape list every render and
/// wires size (watchSize) + pointer (onTap / onDrag / onRelease + the
/// watchPointers mirror) back to the dispatch loop as Msgs.
struct MarCanvasView: View {
    let view: MarView
    let dispatch: (MarValue) -> Void

    private func handler(_ name: String) -> MarValue? {
        view.attrs.first(where: { $0.name == name })?.value
    }
    private var shapes: [MarValue] {
        if case .list(let xs)? = view.attrs.first(where: { $0.name == "shapes" })?.value { return xs }
        return []
    }

    /// Fire a (Int -> Int -> msg) handler with a point.
    private func fire(_ name: String, _ x: Int, _ y: Int) {
        guard let h = handler(name),
              let partial = try? Eval.apply(h, .int(x)),
              let msg = try? Eval.apply(partial, .int(y)) else { return }
        dispatch(msg)
    }

    /// Fire the watchPointers handler with the whole pointer list (one record
    /// { id, x, y } per active finger), the pointer-mirror snapshot.
    private func firePointers(_ pts: [(Int, Int, Int)]) {
        guard let h = handler("watchPointers") else { return }
        let list = MarValue.list(pts.map { t in
            MarValue.record(fields: ["id": .int(t.0), "x": .int(t.1), "y": .int(t.2)], order: ["id", "x", "y"])
        })
        if let msg = try? Eval.apply(h, list) { dispatch(msg) }
    }

    var body: some View {
        GeometryReader { geo in
            Canvas { ctx, size in
                MarCanvasView.draw(shapes, into: ctx, size: size)
            }
            .contentShape(Rectangle())
            #if canImport(UIKit)
            // All pointer input — the tap / drag / release edges AND the
            // watchPointers mirror — flows through ONE multi-touch view, the way
            // the web feeds both from the same pointer handlers. Real
            // multi-touch: each finger is tracked under a small stable id.
            .overlay(
                MarPointerCapture(
                    onTap: { x, y in fire("onTap", x, y) },
                    onDrag: { x, y in fire("onDrag", x, y) },
                    onRelease: { x, y in fire("onRelease", x, y) },
                    onPointers: { pts in firePointers(pts) }
                )
            )
            #else
            .gesture(
                DragGesture(minimumDistance: 0)
                    .onChanged { g in
                        if g.translation == .zero { fire("onTap", Int(g.location.x), Int(g.location.y)) }
                        fire("onDrag", Int(g.location.x), Int(g.location.y))
                    }
                    .onEnded { g in fire("onRelease", Int(g.location.x), Int(g.location.y)) }
            )
            #endif
            .onAppear { fire("watchSize", Int(geo.size.width), Int(geo.size.height)) }
            .onChange(of: geo.size) { _, new in fire("watchSize", Int(new.width), Int(new.height)) }
        }
        // Full-bleed game surface: fill under the safe areas and hide the
        // NavigationStack bar so the canvas owns the whole screen, the way the
        // web canvas fills its container. The game draws its own framing/bezel,
        // and onResize reports this full size so the shapes lay out to fit.
        .ignoresSafeArea()
        .toolbar(.hidden, for: .navigationBar)
    }

    // MARK: draw-list replay

    static func draw(_ shapes: [MarValue], into ctx: GraphicsContext, size: CGSize) {
        for s in shapes { drawShape(s, into: ctx) }
    }

    /// rgb carries 3 channels; rgba carries a 4th, an opacity percent int.
    private static func color(_ v: MarValue?) -> Color {
        guard case .ctor(let t, let a, _)? = v, t == "__Color", a.count >= 3 else { return .black }
        func c(_ i: Int) -> Double { if case .int(let n) = a[i] { return Double(n) / 255 }; return 0 }
        var alpha = 1.0
        if a.count == 4, case .int(let pct) = a[3] { alpha = min(100, max(0, Double(pct))) / 100 }
        return Color(red: c(0), green: c(1), blue: c(2), opacity: alpha)
    }
    private static func d(_ v: MarValue) -> Double { if case .int(let n) = v { return Double(n) }; if case .float(let x) = v { return x }; return 0 }

    private static func drawShape(_ s: MarValue, into ctx: GraphicsContext) {
        guard case .ctor(let tag, let a, _) = s else { return }
        switch tag {
        case "rect" where a.count == 5:
            let rect = CGRect(x: d(a[0]), y: d(a[1]), width: d(a[2]), height: d(a[3]))
            ctx.fill(Path(rect), with: .color(color(a[4])))
        case "circle" where a.count == 4:
            let r = d(a[2])
            let rect = CGRect(x: d(a[0]) - r, y: d(a[1]) - r, width: r * 2, height: r * 2)
            ctx.fill(Path(ellipseIn: rect), with: .color(color(a[3])))
        case "triangle" where a.count == 7:
            var p = Path()
            p.move(to: CGPoint(x: d(a[0]), y: d(a[1])))
            p.addLine(to: CGPoint(x: d(a[2]), y: d(a[3])))
            p.addLine(to: CGPoint(x: d(a[4]), y: d(a[5])))
            p.closeSubpath()
            ctx.fill(p, with: .color(color(a[6])))
        case "canvasText" where a.count == 6:
            var str = ""; if case .string(let x) = a[5] { str = x }
            let sizePt = d(a[2])
            let align: String = { if case .ctor(let t, _, _) = a[3] { return t }; return "Left" }()
            let anchor: UnitPoint = align == "Center" ? .center : (align == "Right" ? .trailing : .leading)
            let text = Text(str).font(.system(size: sizePt, weight: .semibold, design: .monospaced)).foregroundColor(color(a[4]))
            ctx.draw(text, at: CGPoint(x: d(a[0]), y: d(a[1])), anchor: anchor)
        case "group" where a.count == 2:
            var transforms: [MarValue] = []
            if case .list(let ts) = a[0] { transforms = ts }
            var kids: [MarValue] = []
            if case .list(let ks) = a[1] { kids = ks }
            let alpha = groupAlpha(transforms)
            let mode = groupBlend(transforms)
            // No Alpha: draw straight through under the group's blend mode.
            // Free — a copied context is the whole cost, and GraphicsContext
            // being a value type means the scoping needs no save/restore.
            // Erase included: erasing shape by shape leaves dst × Π(1-aᵢ),
            // exactly what one stamp of their union would leave.
            if alpha == nil {
                var sub = ctx
                if let mode { sub.blendMode = mode }
                for t in transforms { apply(t, to: &sub) }
                for k in kids { drawShape(k, into: sub) }
            } else if alpha! > 0 {
                // Alpha means "composite the group, THEN fade it", not "fade
                // each shape": drawLayer renders the children into their own
                // layer and stamps it once at this opacity, so overlapping
                // parts inside the group never blend through each other. The
                // web renderer does the same with an offscreen canvas.
                //
                // A Blend mode rides on the stamp AND inside the layer, so
                // stacked Add discs still sum with each other. Erase is the
                // exception: its children paint normally to build one
                // silhouette and only the stamp cuts, which is what makes
                // `Alpha 50 + Erase` a half-strength hole everywhere instead
                // of a deeper bite where two erasers overlap.
                var outer = ctx
                outer.opacity = alpha!
                if let mode { outer.blendMode = mode }
                outer.drawLayer { inner in
                    if let mode, mode != .destinationOut { inner.blendMode = mode }
                    for t in transforms { apply(t, to: &inner) }
                    for k in kids { drawShape(k, into: inner) }
                }
            }
        default:
            break
        }
    }

    /// Apply one Transform to a (copied) context. Scale is percent (100 = 1x)
    /// and Rotate is whole degrees, matching the Int-only Canvas model.
    /// Alpha is not a matrix op — groupAlpha pulls it out before this runs.
    private static func apply(_ t: MarValue, to ctx: inout GraphicsContext) {
        guard case .ctor(let tag, let a, _) = t else { return }
        switch tag {
        case "Translate" where a.count == 2:
            ctx.translateBy(x: d(a[0]), y: d(a[1]))
        case "Scale" where a.count == 2:
            ctx.scaleBy(x: d(a[0]) / 100, y: d(a[1]) / 100)
        case "Rotate" where a.count == 1:
            ctx.rotate(by: .degrees(d(a[0])))
        default:
            break
        }
    }

    /// Canvas.Alpha on a group as a 0...1 fraction, or nil when absent.
    /// Repeats multiply, matching the web renderer and nested alpha groups.
    private static func groupAlpha(_ transforms: [MarValue]) -> Double? {
        var out: Double? = nil
        for t in transforms {
            guard case .ctor(let tag, let a, _) = t, tag == "Alpha", a.count == 1 else { continue }
            let pct = min(100, max(0, d(a[0])))
            out = (out ?? 1) * (pct / 100)
        }
        return out
    }

    /// Canvas.Blend on a group, or nil when absent (which means Normal — what
    /// every group did before Blend existed). Unlike Alpha, repeats do NOT
    /// combine: opacities compose, modes don't, so the last one wins. The
    /// mapping mirrors the web's globalCompositeOperation names exactly.
    private static func groupBlend(_ transforms: [MarValue]) -> GraphicsContext.BlendMode? {
        var out: GraphicsContext.BlendMode? = nil
        for t in transforms {
            guard case .ctor(let tag, let a, _) = t, tag == "Blend", a.count == 1,
                  case .ctor(let mode, _, _) = a[0] else { continue }
            switch mode {
            case "Normal":   out = .normal
            case "Add":      out = .plusLighter
            case "Multiply": out = .multiply
            case "Screen":   out = .screen
            case "Erase":    out = .destinationOut
            default:         break
            }
        }
        return out
    }
}

// MARK: - multi-touch pointer capture (watchPointers + tap/drag/release)

#if canImport(UIKit)
/// SwiftUI bridge to a UIKit multi-touch view. On iOS it owns ALL canvas pointer
/// input: it reports every active finger (Canvas.watchPointers) and also fires
/// the tap / drag / release edges, one per touch, mirroring the web's per-press
/// pointer handlers (internal/jsserve/runtime.js setupCanvas).
struct MarPointerCapture: UIViewRepresentable {
    let onTap: (Int, Int) -> Void
    let onDrag: (Int, Int) -> Void
    let onRelease: (Int, Int) -> Void
    let onPointers: ([(Int, Int, Int)]) -> Void

    func makeUIView(context: Context) -> MarTouchView {
        let v = MarTouchView()
        v.bind(onTap: onTap, onDrag: onDrag, onRelease: onRelease, onPointers: onPointers)
        return v
    }
    func updateUIView(_ v: MarTouchView, context: Context) {
        v.bind(onTap: onTap, onDrag: onDrag, onRelease: onRelease, onPointers: onPointers)
    }
}

/// Tracks every active UITouch under a small stable integer id (0,1,2,…
/// smallest free, assigned on contact, reusable on release) and its position in
/// the view's coordinate space (= shape coords, points). Reports the whole list
/// on any change. Touch positions are in points, no dpr scaling (SwiftUI Canvas
/// works in points), matching Canvas.watchPointers' contract.
final class MarTouchView: UIView {
    private var onTap: ((Int, Int) -> Void)?
    private var onDrag: ((Int, Int) -> Void)?
    private var onRelease: ((Int, Int) -> Void)?
    private var onPointers: (([(Int, Int, Int)]) -> Void)?

    private var ids: [ObjectIdentifier: Int] = [:]
    private var pos: [ObjectIdentifier: CGPoint] = [:]

    override init(frame: CGRect) { super.init(frame: frame); commonInit() }
    required init?(coder: NSCoder) { super.init(coder: coder); commonInit() }
    private func commonInit() { isMultipleTouchEnabled = true; backgroundColor = .clear }

    func bind(onTap: @escaping (Int, Int) -> Void, onDrag: @escaping (Int, Int) -> Void,
              onRelease: @escaping (Int, Int) -> Void, onPointers: @escaping ([(Int, Int, Int)]) -> Void) {
        self.onTap = onTap; self.onDrag = onDrag; self.onRelease = onRelease; self.onPointers = onPointers
    }

    private func smallestFreeId() -> Int {
        let used = Set(ids.values); var i = 0; while used.contains(i) { i += 1 }; return i
    }
    private func emitPointers() {
        let items = ids.keys
            .compactMap { k -> (Int, CGPoint)? in guard let id = ids[k], let p = pos[k] else { return nil }; return (id, p) }
            .sorted { $0.0 < $1.0 }
            .map { (id, p) in (id, Int(p.x), Int(p.y)) }
        onPointers?(items)
    }

    override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent?) {
        for t in touches {
            let k = ObjectIdentifier(t); let p = t.location(in: self)
            ids[k] = smallestFreeId(); pos[k] = p
            onTap?(Int(p.x), Int(p.y))
        }
        emitPointers()
    }
    override func touchesMoved(_ touches: Set<UITouch>, with event: UIEvent?) {
        for t in touches {
            let k = ObjectIdentifier(t); let p = t.location(in: self)
            pos[k] = p
            onDrag?(Int(p.x), Int(p.y))
        }
        emitPointers()
    }
    private func finish(_ touches: Set<UITouch>) {
        for t in touches {
            let k = ObjectIdentifier(t); let p = pos[k] ?? t.location(in: self)
            onRelease?(Int(p.x), Int(p.y))
            ids[k] = nil; pos[k] = nil
        }
        emitPointers()
    }
    override func touchesEnded(_ touches: Set<UITouch>, with event: UIEvent?) { finish(touches) }
    override func touchesCancelled(_ touches: Set<UITouch>, with event: UIEvent?) { finish(touches) }
}
#endif
