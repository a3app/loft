// Shared loft UI chrome: flipped views, mark, plates, buttons, fields, and borderless windows.

import AppKit
import CoreText
import QuartzCore

/// Label that lets clicks pass through to the row or plate behind it.
final class PassLab: NSTextField {
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

/// Single-line label drawn with `loftTextY`, so it shares a pill's vertical center.
final class DrawLab: FlippedView {
    var text = "" { didSet { needsDisplay = true } }
    var font = Palette.font(20) { didSet { needsDisplay = true } }
    var color = Palette.snow { didSet { needsDisplay = true } }
    var alignment: NSTextAlignment = .left { didSet { needsDisplay = true } }
    /// Extra lift after geometric center. −2 matches pill titles; 0 is true center.
    var pillNudge = true { didSet { needsDisplay = true } }

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func draw(_ dirtyRect: NSRect) {
        let p = NSMutableParagraphStyle()
        p.alignment = alignment
        p.lineBreakMode = .byTruncatingTail
        let attrs: [NSAttributedString.Key: Any] = [
            .font: font, .foregroundColor: color, .paragraphStyle: p,
        ]
        if !pillNudge {
            drawInk(attrs)
            return
        }
        let t = text as NSString
        let sz = t.size(withAttributes: attrs)
        var y = loftTextY(bounds.height, sz.height, flipped: isFlipped)
        y += isFlipped ? -2 : 2
        t.draw(in: NSRect(x: 0, y: y, width: bounds.width, height: sz.height + 2), withAttributes: attrs)
    }

    /// Centers the glyph ink, so a caption without descenders still sits on the toggle mid-line.
    private func drawInk(_ attrs: [NSAttributedString.Key: Any]) {
        loftDrawInk(text, attrs: attrs, in: bounds, alignment: alignment, flipped: true)
    }
}

/// Top-down coordinate view; first mouse click counts without focusing the window.
class FlippedView: NSView {
    /// File URLs dropped on this view (`.loft`, YAML, `.vpn`, QR image).
    var onFiles: (([URL]) -> Void)?

    override var isFlipped: Bool { true }
    /// Lets a first click on the view fire without an extra focus click.
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        guard onFiles != nil else { return [] }
        return fileURLs(from: sender).isEmpty ? [] : .copy
    }

    override func prepareForDragOperation(_ sender: NSDraggingInfo) -> Bool {
        onFiles != nil && !fileURLs(from: sender).isEmpty
    }

    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        let urls = fileURLs(from: sender)
        guard let onFiles = onFiles, !urls.isEmpty else { return false }
        onFiles(urls)
        return true
    }

    /// File URLs on the dragging pasteboard.
    private func fileURLs(from sender: NSDraggingInfo) -> [URL] {
        let urls = sender.draggingPasteboard.readObjects(forClasses: [NSURL.self], options: nil) as? [URL] ?? []
        return urls.filter { $0.isFileURL }
    }
}

/// Lamp-fill tag (popover TUN). Caps sit on capHeight in the plate;
/// em-box / ink-box sat TUN on the top rim.
final class LampTag: FlippedView {
    var text = "TUN" { didSet { needsDisplay = true } }

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
    }

    required init?(coder: NSCoder) { fatalError() }

    static func size(for text: String) -> NSSize {
        let w = (text as NSString).size(withAttributes: attrs).width
        return NSSize(width: max(28, ceil(w) + 12), height: 16)
    }

    private static var tagFont: NSFont { Palette.font(10, weight: .bold) }

    private static var attrs: [NSAttributedString.Key: Any] {
        [
            .font: tagFont,
            .foregroundColor: Palette.ink,
            .kern: 0.5,
        ]
    }

    override func draw(_ dirtyRect: NSRect) {
        Palette.lamp.setFill()
        loftRound(bounds, radius: 3.5).fill()
        let line = CTLineCreateWithAttributedString(NSAttributedString(string: text, attributes: Self.attrs))
        guard let ctx = NSGraphicsContext.current?.cgContext else { return }
        ctx.saveGState()
        ctx.translateBy(x: 0, y: bounds.height)
        ctx.scaleBy(x: 1, y: -1)
        ctx.textMatrix = .identity
        let ink = CTLineGetImageBounds(line, ctx)
        let cap = Self.tagFont.capHeight
        let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 2
        /// Snaps to the backing grid so the caps sit on the plate mid-line.
        func snap(_ v: CGFloat) -> CGFloat { (v * scale).rounded() / scale }
        let x = snap((bounds.width - ink.width) / 2 - ink.origin.x)
        let y = snap((bounds.height - cap) / 2)
        ctx.textPosition = CGPoint(x: x, y: y)
        CTLineDraw(line, ctx)
        ctx.restoreGState()
    }
}

/// One-line ticker. `scroll` loops the string; otherwise it sits and truncates.
final class MarqueeLab: FlippedView {
    private let a = NSTextField(labelWithString: "")
    private let b = NSTextField(labelWithString: "")
    private var timer: Timer?
    private var text = ""
    private var scrolling = false
    private var keepMoving = false
    private var x: CGFloat = 0
    private var piece: CGFloat = 0
    private let gap: CGFloat = 36

    var font: NSFont = Palette.body(11.5) {
        didSet { a.font = font; b.font = font }
    }
    var textColor: NSColor = Palette.mute {
        didSet { a.textColor = textColor; b.textColor = textColor }
    }

    /// Two clipped labels and a 30 fps timer used when the string overflows.
    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        wantsLayer = true
        layer?.masksToBounds = true
        for lab in [a, b] {
            lab.font = font
            lab.textColor = textColor
            lab.usesSingleLineMode = true
            lab.lineBreakMode = .byClipping
            lab.cell?.wraps = false
            lab.cell?.isScrollable = false
            addSubview(lab)
        }
        b.isHidden = true
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    deinit { timer?.invalidate() }

    /// Sets the ticker string. `scroll` loops it; `keepMoving` crawls even if it fits.
    func show(_ s: String, scroll: Bool, keepMoving: Bool = false) {
        let same = s == text && scroll == scrolling && keepMoving == self.keepMoving
        text = s
        scrolling = scroll
        self.keepMoving = keepMoving
        a.stringValue = s
        b.stringValue = s
        a.font = font
        b.font = font
        a.textColor = textColor
        b.textColor = textColor
        if same && timer != nil { return }
        x = 0
        relayout()
        arm()
    }

    /// Relays a size change into ticker layout and the scroll timer.
    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        relayout()
        arm()
    }

    /// Arms or stops the timer when the view joins or leaves a window.
    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        arm()
    }

    /// Starts a 30 fps timer while the ticker is visible and scrolling.
    private func arm() {
        let live = window?.isVisible == true && !isHidden && scrolling && bounds.width > 1 && !b.isHidden
        if live {
            if timer == nil {
                let t = Timer.scheduledTimer(withTimeInterval: 1.0 / 30.0, repeats: true) { [weak self] _ in
                    self?.tick()
                }
                RunLoop.main.add(t, forMode: .common)
                timer = t
            }
        } else {
            timer?.invalidate()
            timer = nil
        }
    }

    /// Advances the ticker by 0.7 pt and wraps when a full piece has scrolled off.
    private func tick() {
        guard window?.isVisible == true, !isHidden, scrolling, piece > 0 else {
            timer?.invalidate()
            timer = nil
            return
        }
        x -= 0.7
        if -x >= piece { x += piece }
        place()
    }

    /// Truncates if the string fits; otherwise lays two copies for a loop.
    private func relayout() {
        let h = bounds.height
        let w = Palette.textWidth(text, font: font)
        if !scrolling {
            a.lineBreakMode = .byTruncatingTail
            a.frame = NSRect(x: 0, y: 0, width: max(bounds.width, 1), height: h)
            b.isHidden = true
            piece = w
            return
        }
        let overflow = w > bounds.width + 1
        if !overflow && !keepMoving {
            a.lineBreakMode = .byTruncatingTail
            a.frame = NSRect(x: 0, y: 0, width: max(bounds.width, 1), height: h)
            b.isHidden = true
            piece = w
            return
        }
        a.lineBreakMode = .byClipping
        // Copies sit at least a clip-width apart so a short ticker still crawls.
        piece = max(w + gap, bounds.width + 24)
        b.isHidden = false
        place()
    }

    /// Positions the two ticker copies `piece` apart.
    private func place() {
        let h = bounds.height
        a.frame = NSRect(x: x, y: 0, width: piece, height: h)
        b.frame = NSRect(x: x + piece, y: 0, width: piece, height: h)
    }
}

/// Rounded plate with an inside 1pt stroke so the outline sits fully in-view
/// (CALayer `borderWidth` is centered on the edge and clips unevenly).
final class LoftPlate: FlippedView {
    var fill: NSColor? { didSet { needsDisplay = true } }
    var stroke: NSColor? { didSet { needsDisplay = true } }
    var radius: CGFloat = 10 { didSet { needsDisplay = true } }

    /// Fills the plate and strokes a 1pt outline fully inside the bounds.
    override func draw(_ dirtyRect: NSRect) {
        if let fill = fill {
            fill.setFill()
            loftRound(bounds, radius: radius).fill()
        }
        if let stroke = stroke {
            loftStrokeInside(bounds, radius: radius, color: stroke)
        }
    }
}

/// Lamp state for the loft mark: idle, connected, or error.
enum MarkState { case off, on, err }

/// Vector loft mark: body plus a lamp that follows `MarkState`.
final class MarkView: FlippedView {
    var state: MarkState = .off { didSet { needsDisplay = true } }
    var inkBody = false { didSet { needsDisplay = true } }

    /// Paints the mark into `bounds` using `state` and `inkBody`.
    override func draw(_ dirtyRect: NSRect) {
        MarkView.paint(in: bounds, state: state, body: inkBody ? Palette.ink : Palette.stone)
    }

    /// Draws the mark scaled to `bounds`. `body` is the chimney fill.
    static func paint(in bounds: NSRect, state: MarkState, body: NSColor) {
        let s = min(bounds.width, bounds.height) / 96
        guard s > 0 else { return }
        NSGraphicsContext.saveGraphicsState()
        let t = NSAffineTransform()
        t.translateX(by: (bounds.width - 96 * s) / 2, yBy: (bounds.height - 96 * s) / 2)
        t.scale(by: s)
        t.concat()
        body.setFill()
        bodyPath().fill()
        switch state {
        case .on: Palette.lamp.setFill()
        case .err: Palette.err.setFill()
        case .off: Palette.slate.setFill()
        }
        lampPath().fill()
        NSGraphicsContext.restoreGraphicsState()
    }

    /// Raster mark at `size` for menus and shots.
    static func image(size: NSSize, state: MarkState, ink: Bool = false) -> NSImage {
        let img = NSImage(size: size, flipped: true) { rect in
            MarkView.paint(in: rect, state: state, body: ink ? Palette.ink : Palette.stone)
            return true
        }
        img.isTemplate = false
        return img
    }

    /// Chimney outline in 96×96 design space.
    private static func bodyPath() -> NSBezierPath {
        let p = NSBezierPath()
        p.move(to: NSPoint(x: 37.76, y: 6.72))
        addArc(p, from: NSPoint(x: 37.76, y: 6.72), rx: 0.96, ry: 0.96, rot: 0, large: false, sweep: true, to: NSPoint(x: 39.01, y: 6.74))
        p.line(to: NSPoint(x: 57.6, y: 29.52))
        p.line(to: NSPoint(x: 57.6, y: 63.76))
        addArc(p, from: NSPoint(x: 57.6, y: 63.76), rx: 4.4, ry: 4.4, rot: 0, large: false, sweep: false, to: NSPoint(x: 62, y: 68.16))
        p.line(to: NSPoint(x: 84.8, y: 68.16))
        addArc(p, from: NSPoint(x: 84.8, y: 68.16), rx: 3.2, ry: 3.2, rot: 0, large: false, sweep: true, to: NSPoint(x: 88, y: 71.36))
        p.line(to: NSPoint(x: 88, y: 86.16))
        addArc(p, from: NSPoint(x: 88, y: 86.16), rx: 3.84, ry: 3.84, rot: 0, large: false, sweep: true, to: NSPoint(x: 84.16, y: 90))
        p.line(to: NSPoint(x: 21.44, y: 90))
        addArc(p, from: NSPoint(x: 21.44, y: 90), rx: 3.84, ry: 3.84, rot: 0, large: false, sweep: true, to: NSPoint(x: 17.6, y: 86.16))
        p.line(to: NSPoint(x: 17.6, y: 29.52))
        p.close()
        return p
    }

    /// Rounded lamp window in 96×96 design space.
    private static func lampPath() -> NSBezierPath {
        return NSBezierPath(roundedRect: NSRect(x: 28.8, y: 36.24, width: 17.6, height: 21.84), xRadius: 2.56, yRadius: 2.56)
    }

    /// SVG elliptical arc from `p1` to `p2`, approximated with cubic beziers.
    private static func addArc(_ path: NSBezierPath, from p1: NSPoint, rx: CGFloat, ry: CGFloat, rot: CGFloat, large: Bool, sweep: Bool, to p2: NSPoint) {
        var rx = abs(rx), ry = abs(ry)
        if rx < 0.001 || ry < 0.001 { path.line(to: p2); return }
        let phi = rot * CGFloat.pi / 180
        let cosφ = cos(phi), sinφ = sin(phi)
        let dx = (p1.x - p2.x) / 2, dy = (p1.y - p2.y) / 2
        let x1p = cosφ * dx + sinφ * dy
        let y1p = -sinφ * dx + cosφ * dy
        let lam = (x1p * x1p) / (rx * rx) + (y1p * y1p) / (ry * ry)
        if lam > 1 { let s = sqrt(lam); rx *= s; ry *= s }
        let sign: CGFloat = (large == sweep) ? -1 : 1
        let num = rx * rx * ry * ry - rx * rx * y1p * y1p - ry * ry * x1p * x1p
        let den = rx * rx * y1p * y1p + ry * ry * x1p * x1p
        let co = sign * sqrt(max(0, num / max(den, 0.0001)))
        let cxp = co * (rx * y1p / ry)
        let cyp = co * -(ry * x1p / rx)
        let cx = cosφ * cxp - sinφ * cyp + (p1.x + p2.x) / 2
        let cy = sinφ * cxp + cosφ * cyp + (p1.y + p2.y) / 2
        /// Signed angle from vector u to vector v.
        func ang(_ ux: CGFloat, _ uy: CGFloat, _ vx: CGFloat, _ vy: CGFloat) -> CGFloat {
            let u = sqrt(ux * ux + uy * uy), v = sqrt(vx * vx + vy * vy)
            let a = acos(max(-1, min(1, (ux * vx + uy * vy) / max(u * v, 0.0001))))
            return (ux * vy - uy * vx < 0) ? -a : a
        }
        let θ1 = ang(1, 0, (x1p - cxp) / rx, (y1p - cyp) / ry)
        var dθ = ang((x1p - cxp) / rx, (y1p - cyp) / ry, (-x1p - cxp) / rx, (-y1p - cyp) / ry)
        if !sweep && dθ > 0 { dθ -= 2 * .pi }
        if sweep && dθ < 0 { dθ += 2 * .pi }
        let n = max(1, Int(ceil(abs(dθ) / (.pi / 2))))
        let step = dθ / CGFloat(n)
        for i in 0..<n {
            let a1 = θ1 + CGFloat(i) * step
            let a2 = a1 + step
            let k = tan(step / 4) * 4 / 3
            /// Point on the ellipse at angle `a`.
            func pt(_ a: CGFloat) -> NSPoint {
                let x = rx * cos(a), y = ry * sin(a)
                return NSPoint(x: cosφ * x - sinφ * y + cx, y: sinφ * x + cosφ * y + cy)
            }
            /// Tangent vector at angle `a`.
            func tv(_ a: CGFloat) -> (CGFloat, CGFloat) {
                let x = -rx * sin(a), y = ry * cos(a)
                return (cosφ * x - sinφ * y, sinφ * x + cosφ * y)
            }
            let pa = pt(a1), pb = pt(a2)
            let (tx1, ty1) = tv(a1)
            let (tx2, ty2) = tv(a2)
            path.curve(to: pb,
                       controlPoint1: NSPoint(x: pa.x + k * tx1, y: pa.y + k * ty1),
                       controlPoint2: NSPoint(x: pb.x - k * tx2, y: pb.y - k * ty2))
        }
    }
}

/// Pixel-grid heart (A³ colophon): 7×6 cells, loft lamp.
final class HeartView: FlippedView {
    override func draw(_ dirtyRect: NSRect) {
        NSGraphicsContext.current?.shouldAntialias = false
        NSGraphicsContext.current?.imageInterpolation = .none
        Palette.lamp.setFill()
        let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 1
        var u = min(bounds.width / 7, bounds.height / 6)
        u = floor(u * scale) / scale
        if u < 1 / scale { u = 1 / scale }
        let ox = floor(((bounds.width - 7 * u) / 2) * scale) / scale
        let oy = floor(((bounds.height - 6 * u) / 2) * scale) / scale
        /// Fills a `w`×`h` cell at grid (`x`, `y`).
        func cell(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat) {
            NSBezierPath(rect: NSRect(x: ox + x * u, y: oy + y * u, width: w * u, height: h * u)).fill()
        }
        cell(1, 0, 2, 1); cell(4, 0, 2, 1)
        cell(0, 1, 7, 2)
        cell(1, 3, 5, 1)
        cell(2, 4, 3, 1)
        cell(3, 5, 1, 1)
    }
}

/// Filled rounded button. `lampOn` uses the lamp fill; `compact` shrinks padding.
final class PillButton: NSButton {
    var lampOn = false { didSet { needsDisplay = true } }
    var compact = false
    private var hover = false
    private var track: NSTrackingArea?

    override var isEnabled: Bool {
        get { super.isEnabled }
        set { super.isEnabled = newValue; needsDisplay = true; window?.invalidateCursorRects(for: self) }
    }

    /// Borderless pill; `lampOn` chooses the fill later in `draw`.
    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        loftWireButton(self)
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Lets a first click fire without an extra focus click.
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    /// Redraws so the pressed fill tracks highlight.
    override func highlight(_ flag: Bool) {
        super.highlight(flag)
        needsDisplay = true
    }

    /// Pointing-hand cursor while enabled.
    override func resetCursorRects() {
        if isEnabled { addCursorRect(bounds, cursor: .pointingHand) }
    }

    /// Hover wash so the pill reads as a control before the press.
    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let t = track { removeTrackingArea(t) }
        let t = NSTrackingArea(
            rect: bounds,
            options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect],
            owner: self,
            userInfo: nil
        )
        addTrackingArea(t)
        track = t
    }

    override func mouseEntered(with event: NSEvent) {
        hover = true
        needsDisplay = true
    }

    override func mouseExited(with event: NSEvent) {
        hover = false
        needsDisplay = true
    }

    /// Holds the press until mouse-up so the fill paints and the borderless
    /// window does not steal the gesture (`isMovableByWindowBackground`).
    override func mouseDown(with event: NSEvent) {
        guard isEnabled else { return }
        setPressed(true)
        let tgt = target
        let act = action
        let win = window
        var inside = true
        var keep = true
        while keep {
            guard let w = win ?? window else { break }
            guard let e = w.nextEvent(matching: [.leftMouseUp, .leftMouseDragged]) else { break }
            let hit: Bool
            if window != nil {
                hit = bounds.contains(convert(e.locationInWindow, from: nil))
            } else {
                hit = inside
            }
            switch e.type {
            case .leftMouseDragged:
                inside = hit
                setPressed(hit && window != nil)
            case .leftMouseUp:
                keep = false
                setPressed(false)
                if inside, let act = act {
                    _ = NSApp.sendAction(act, to: tgt, from: self)
                }
            default:
                break
            }
        }
    }

    /// Darken and paint now so the press is visible before the next event.
    private func setPressed(_ on: Bool) {
        isHighlighted = on
        needsDisplay = true
        displayIfNeeded()
    }

    /// Draws the pill fill, optional press darken, and centered title.
    override func draw(_ dirtyRect: NSRect) {
        let pressed = isEnabled && isHighlighted
        let plus = compact && title == "+"
        let rad: CGFloat = plus ? min(5, bounds.height * 0.28) : (compact ? 8 : 10)
        let on = lampOn && isEnabled
        var fill = on ? Palette.lamp : Palette.slate
        if hover && isEnabled && !pressed {
            fill = fill.blended(withFraction: 0.12, of: .white) ?? fill
        }
        if pressed { fill = loftPress(fill) }
        fill.setFill()
        var box = bounds
        if pressed { box = box.insetBy(dx: 1, dy: 1) }
        loftRound(box, radius: max(4, rad - (pressed ? 1 : 0))).fill()
        let color = on ? Palette.ink : (isEnabled ? Palette.snow : Palette.mute)
        if plus {
            drawPlus(in: box, color: color)
            return
        }
        let p = NSMutableParagraphStyle()
        p.alignment = .center
        p.lineBreakMode = .byTruncatingTail
        let font = compact ? Palette.font(11.5) : Palette.font(14)
        let attrs: [NSAttributedString.Key: Any] = [
            .font: font, .foregroundColor: color, .paragraphStyle: p,
            .kern: -0.28
        ]
        let t = title as NSString
        let sz = t.size(withAttributes: attrs)
        let inset: CGFloat = compact ? 6 : 10
        var y = loftTextY(bounds.height, sz.height, flipped: isFlipped)
        if pressed { y += isFlipped ? 1 : -1 }
        t.draw(in: NSRect(x: inset, y: y, width: bounds.width - inset * 2, height: sz.height + 2), withAttributes: attrs)
    }

    /// Geometric plus in the ink of a TUN badge, pixel-snapped to the plate center.
    private func drawPlus(in box: NSRect, color: NSColor) {
        let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 2
        /// Pixel-snaps `v` so both arms share one center.
        func snap(_ v: CGFloat) -> CGFloat { (v * scale).rounded() / scale }
        let cx = snap(box.midX)
        let cy = snap(box.midY)
        let thick = snap(max(1.5, box.width * 0.12))
        let arm = snap(max(3.5, box.width * 0.22))
        color.setFill()
        NSBezierPath(rect: NSRect(x: cx - arm, y: cy - thick / 2, width: arm * 2, height: thick)).fill()
        NSBezierPath(rect: NSRect(x: cx - thick / 2, y: cy - arm, width: thick, height: arm * 2)).fill()
    }
}

/// Text-only button; hover and press lighten `tone`.
final class GhostButton: NSButton {
    var tone: NSColor = Palette.mute { didSet { needsDisplay = true } }
    private var hover = false
    private var track: NSTrackingArea?

    /// Borderless text button; hover tracking is installed in `updateTrackingAreas`.
    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        loftWireButton(self)
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Lets a first click fire without an extra focus click.
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    /// Redraws so the press color tracks highlight.
    override func highlight(_ flag: Bool) {
        super.highlight(flag)
        needsDisplay = true
    }

    /// Pointing-hand cursor while enabled.
    override func resetCursorRects() {
        if isEnabled { addCursorRect(bounds, cursor: .pointingHand) }
    }

    /// Tracks mouse enter/exit for the hover tint.
    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let t = track { removeTrackingArea(t) }
        let t = NSTrackingArea(
            rect: bounds,
            options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect],
            owner: self,
            userInfo: nil
        )
        addTrackingArea(t)
        track = t
    }

    /// Turns the hover tint on.
    override func mouseEntered(with event: NSEvent) {
        hover = true
        needsDisplay = true
    }

    /// Turns the hover tint off.
    override func mouseExited(with event: NSEvent) {
        hover = false
        needsDisplay = true
    }

    /// Redraws around the AppKit mouse-down so the press color is visible.
    override func mouseDown(with event: NSEvent) {
        needsDisplay = true
        super.mouseDown(with: event)
        needsDisplay = true
    }

    /// Full frame is the hit target. `point` is in the superview, like `frame`.
    override func hitTest(_ point: NSPoint) -> NSView? {
        guard isEnabled, !isHidden, frame.contains(point) else { return nil }
        return self
    }

    /// Draws the title in `tone`, lightened on hover/press.
    override func draw(_ dirtyRect: NSRect) {
        let pressed = isEnabled && isHighlighted
        let face = font ?? Palette.body(12.5)
        let color: NSColor
        if !isEnabled {
            color = Palette.mute
        } else {
            let lit = tone.blended(withFraction: 0.55, of: Palette.snow) ?? Palette.snow
            color = pressed ? loftPress(lit) : (hover ? lit : tone)
        }
        let p = NSMutableParagraphStyle()
        p.alignment = alignment
        p.lineBreakMode = .byTruncatingTail
        let attrs: [NSAttributedString.Key: Any] = [
            .font: face,
            .foregroundColor: color,
            .paragraphStyle: p
        ]
        let t = title as NSString
        let sz = t.size(withAttributes: attrs)
        var y = loftTextY(bounds.height, sz.height, flipped: isFlipped)
        if pressed { y += isFlipped ? 1 : -1 }
        t.draw(in: NSRect(x: 0, y: y, width: bounds.width, height: sz.height + 2), withAttributes: attrs)
    }
}

/// 36×22 track switch. Knob and fill ease like a macOS toggle.
final class ToggleSwitch: FlippedView {
    var on = false {
        didSet {
            if oldValue != on { apply(animated: live) }
        }
    }
    var onChange: ((Bool) -> Void)?
    private var pressed = false {
        didSet {
            if oldValue != pressed { apply(animated: live) }
        }
    }
    private let knob = FlippedView(frame: .zero)
    private var live = false

    /// Forces the switch to 36×22 regardless of the incoming frame size.
    override init(frame frameRect: NSRect) {
        super.init(frame: NSRect(x: frameRect.origin.x, y: frameRect.origin.y, width: 36, height: 22))
        wantsLayer = true
        layer?.cornerRadius = 11
        layer?.masksToBounds = true
        knob.wantsLayer = true
        knob.layer?.backgroundColor = NSColor.white.cgColor
        knob.layer?.cornerRadius = 9
        addSubview(knob)
        apply(animated: false)
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Snaps the knob once the switch is in a window; later flips animate.
    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        live = window != nil
        apply(animated: false)
    }

    /// Lets a first click fire without an extra focus click.
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    /// Pointing-hand cursor over the whole switch.
    override func resetCursorRects() {
        addCursorRect(bounds, cursor: .pointingHand)
    }

    /// Toggles `on` and notifies `onChange`.
    func flip() {
        on.toggle()
        onChange?(on)
    }

    /// Tracks press and flips on mouse-up inside the bounds.
    override func mouseDown(with event: NSEvent) {
        pressed = true
        var keep = true
        while keep {
            guard let e = window?.nextEvent(matching: [.leftMouseUp, .leftMouseDragged]) else { break }
            let p = convert(e.locationInWindow, from: nil)
            switch e.type {
            case .leftMouseDragged:
                pressed = bounds.contains(p)
            case .leftMouseUp:
                keep = false
                let inside = bounds.contains(p)
                pressed = false
                if inside { flip() }
            default:
                break
            }
        }
    }

    /// Eases the knob and track. Press stretches the knob; flip slides it.
    private func apply(animated: Bool) {
        let d: CGFloat = 18
        let extra: CGFloat = pressed ? 4 : 0
        let w = d + extra
        let pad: CGFloat = 2
        let x: CGFloat = on ? bounds.width - w - pad : pad
        let dest = NSRect(x: x, y: pad, width: w, height: d)
        var track = on ? Palette.lamp : Palette.slate
        if pressed { track = loftPress(track) }
        let cg = track.cgColor
        if animated {
            let anim = CABasicAnimation(keyPath: "backgroundColor")
            anim.fromValue = layer?.backgroundColor
            anim.toValue = cg
            anim.duration = pressed ? 0.10 : 0.20
            anim.timingFunction = CAMediaTimingFunction(name: .easeInEaseOut)
            layer?.add(anim, forKey: "bg")
            layer?.backgroundColor = cg
            NSAnimationContext.runAnimationGroup { ctx in
                ctx.duration = pressed ? 0.10 : 0.20
                ctx.timingFunction = CAMediaTimingFunction(name: .easeInEaseOut)
                knob.animator().frame = dest
            }
        } else {
            layer?.removeAnimation(forKey: "bg")
            layer?.backgroundColor = cg
            knob.frame = dest
        }
    }

    override var intrinsicContentSize: NSSize { NSSize(width: 36, height: 22) }
}

/// 18×18 lamp plate with a check. Click flips `on`.
final class LoftCheck: FlippedView {
    var on = false { didSet { needsDisplay = true } }
    var onChange: ((Bool) -> Void)?

    override init(frame frameRect: NSRect) {
        super.init(frame: NSRect(x: frameRect.origin.x, y: frameRect.origin.y, width: 18, height: 18))
    }

    required init?(coder: NSCoder) { fatalError() }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func resetCursorRects() {
        addCursorRect(bounds, cursor: .pointingHand)
    }

    override func mouseDown(with event: NSEvent) {
        var keep = true
        while keep {
            guard let e = window?.nextEvent(matching: [.leftMouseUp, .leftMouseDragged]) else { break }
            if e.type == .leftMouseUp {
                keep = false
                if bounds.contains(convert(e.locationInWindow, from: nil)) {
                    on.toggle()
                    onChange?(on)
                }
            }
        }
    }

    override func draw(_ dirtyRect: NSRect) {
        let box = bounds.insetBy(dx: 0.5, dy: 0.5)
        let path = loftRound(box, radius: 4)
        (on ? Palette.lamp : Palette.slate).setFill()
        path.fill()
        guard on else { return }
        let mark = NSBezierPath()
        mark.lineWidth = 1.6
        mark.lineCapStyle = .round
        mark.lineJoinStyle = .round
        mark.move(to: NSPoint(x: 4.5, y: 9.5))
        mark.line(to: NSPoint(x: 7.5, y: 12.5))
        mark.line(to: NSPoint(x: 13.5, y: 5.5))
        Palette.ink.setStroke()
        mark.stroke()
    }
}

/// Rounded single-line field. `secure` bullets the value and adds a reveal control.
final class LoftField: FlippedView, NSTextFieldDelegate {
    private(set) var field: NSTextField
    private let secret: Bool
    private let placeholder: String
    private var revealed = false
    private var eye: LoftEyeButton?
    var stringValue: String {
        get { field.stringValue }
        set { field.stringValue = newValue }
    }

    /// Builds a bordered field. Secret fields start as `NSSecureTextField`.
    init(frame: NSRect, secure: Bool = false, placeholder: String = "") {
        secret = secure
        self.placeholder = placeholder
        field = LoftField.makeField(secure: secure, placeholder: placeholder)
        super.init(frame: frame)
        field.delegate = self
        addSubview(field)
        if secret {
            let b = LoftEyeButton(frame: .zero)
            b.target = self
            b.action = #selector(toggleReveal)
            b.toolTip = L10n.t("show_secret")
            b.setAccessibilityLabel(L10n.t("show_secret"))
            eye = b
            addSubview(b)
        }
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Borderless mono field; secure uses bullets until the eye opens it.
    private static func makeField(secure: Bool, placeholder: String) -> NSTextField {
        let field: NSTextField = secure ? NSSecureTextField(frame: .zero) : NSTextField(frame: .zero)
        field.isBordered = false
        field.isBezeled = false
        field.drawsBackground = false
        field.backgroundColor = .clear
        field.textColor = Palette.snow
        field.font = Palette.mono(13)
        field.focusRingType = .none
        field.placeholderString = placeholder
        loftWireLine(field)
        if let cell = field.cell as? NSTextFieldCell {
            cell.placeholderAttributedString = NSAttributedString(
                string: placeholder,
                attributes: [.foregroundColor: Palette.mute, .font: Palette.mono(13)]
            )
        }
        return field
    }

    /// Fills the field plate and strokes the inside 1pt border.
    override func draw(_ dirtyRect: NSRect) {
        Palette.field.setFill()
        loftRound(bounds, radius: 10).fill()
        loftStrokeInside(bounds, radius: 10, color: Palette.slate)
    }

    /// One-line text inset 12pt; secret fields leave a 28pt slot for the eye.
    override func layout() {
        super.layout()
        let textH: CGFloat = min(20, max(16, bounds.height - 16))
        let textY = floor((bounds.height - textH) / 2)
        if secret {
            let btn: CGFloat = 28
            eye?.frame = NSRect(
                x: bounds.width - btn - 4,
                y: floor((bounds.height - btn) / 2),
                width: btn,
                height: btn
            )
            field.frame = NSRect(x: 12, y: textY, width: bounds.width - 12 - btn - 6, height: textH)
        } else {
            field.frame = NSRect(x: 12, y: textY, width: bounds.width - 24, height: textH)
        }
    }

    /// Forwards first-responder to the inner text field.
    override func becomeFirstResponder() -> Bool {
        window?.makeFirstResponder(field) ?? false
    }

    /// Rebuilds the inner field as secure or plain, keeping the string and caret.
    @objc private func toggleReveal() {
        revealed.toggle()
        let val = field.stringValue
        let target = field.target
        let action = field.action
        let align = field.alignment
        let editable = field.isEditable
        let selectable = field.isSelectable
        var sel: NSRange?
        if let ed = field.currentEditor() { sel = ed.selectedRange }
        let wasFirst = window?.firstResponder === field.currentEditor() || window?.firstResponder === field
        field.removeFromSuperview()
        field = LoftField.makeField(secure: secret && !revealed, placeholder: placeholder)
        field.stringValue = val
        field.target = target
        field.action = action
        field.alignment = align
        field.isEditable = editable
        field.isSelectable = selectable
        field.delegate = self
        addSubview(field, positioned: .below, relativeTo: eye)
        needsLayout = true
        layoutSubtreeIfNeeded()
        if wasFirst {
            window?.makeFirstResponder(field)
            if let sel = sel, let ed = field.currentEditor() {
                let maxLen = (field.stringValue as NSString).length
                let loc = min(sel.location, maxLen)
                ed.selectedRange = NSRange(location: loc, length: min(sel.length, maxLen - loc))
            }
        }
        eye?.open = revealed
        let tip = L10n.t(revealed ? "hide_secret" : "show_secret")
        eye?.toolTip = tip
        eye?.setAccessibilityLabel(tip)
    }

    /// Keeps Return from inserting a line; the field is single-line.
    func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        if commandSelector == #selector(insertNewline(_:)) || commandSelector == #selector(insertNewlineIgnoringFieldEditor(_:)) {
            window?.selectNextKeyView(nil)
            return true
        }
        return false
    }

    /// Pasted newlines flatten so the caret stays on one line.
    func control(_ control: NSControl, textView: NSTextView, shouldChangeTextIn affectedCharRange: NSRange, replacementString: String?) -> Bool {
        guard let s = replacementString, s.contains(where: { $0.isNewline }) else { return true }
        let flat = s.components(separatedBy: .newlines).joined()
        let old = field.stringValue as NSString
        field.stringValue = old.replacingCharacters(in: affectedCharRange, with: flat)
        if let ed = field.currentEditor() {
            let loc = affectedCharRange.location + (flat as NSString).length
            ed.selectedRange = NSRange(location: loc, length: 0)
        }
        return false
    }
}

/// Eye inside a secret `LoftField`. `open` is the revealed (no bullets) state.
final class LoftEyeButton: NSButton {
    var open = false { didSet { needsDisplay = true } }

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        loftWireButton(self)
        title = ""
    }

    required init?(coder: NSCoder) { fatalError() }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func highlight(_ flag: Bool) {
        super.highlight(flag)
        needsDisplay = true
    }

    override func resetCursorRects() {
        addCursorRect(bounds, cursor: .pointingHand)
    }

    override func mouseDown(with event: NSEvent) {
        needsDisplay = true
        super.mouseDown(with: event)
        needsDisplay = true
    }

    /// Feather Icons MIT eye / eye-off (24×24, 2pt round stroke). `open` is eye-off.
    override func draw(_ dirtyRect: NSRect) {
        if isHighlighted {
            NSColor.white.withAlphaComponent(0.08).setFill()
            loftRound(bounds, radius: 8).fill()
        }
        let color = (isHighlighted || open) ? Palette.snow : Palette.mute
        color.setStroke()
        let pad: CGFloat = 5
        let box = bounds.insetBy(dx: pad, dy: pad)
        guard box.width > 4, box.height > 4 else { return }
        /// SVG 24×24 (y down) → button coords (y up).
        func pt(_ x: CGFloat, _ y: CGFloat) -> NSPoint {
            NSPoint(x: box.minX + x / 24 * box.width, y: box.maxY - y / 24 * box.height)
        }
        let w = max(1.35, min(box.width, box.height) / 12)
        let almond = NSBezierPath()
        almond.move(to: pt(1, 12))
        almond.curve(to: pt(12, 4), controlPoint1: pt(1, 12), controlPoint2: pt(5, 4))
        almond.curve(to: pt(23, 12), controlPoint1: pt(19, 4), controlPoint2: pt(23, 12))
        almond.curve(to: pt(12, 20), controlPoint1: pt(23, 12), controlPoint2: pt(19, 20))
        almond.curve(to: pt(1, 12), controlPoint1: pt(5, 20), controlPoint2: pt(1, 12))
        almond.close()
        almond.lineWidth = w
        almond.lineCapStyle = .round
        almond.lineJoinStyle = .round
        almond.stroke()
        let c = pt(12, 12)
        let r = 3 / 24 * min(box.width, box.height)
        let pupil = NSBezierPath(ovalIn: NSRect(x: c.x - r, y: c.y - r, width: r * 2, height: r * 2))
        pupil.lineWidth = w
        pupil.stroke()
        if open {
            let slash = NSBezierPath()
            slash.move(to: pt(1, 1))
            slash.line(to: pt(23, 23))
            slash.lineWidth = w
            slash.lineCapStyle = .round
            slash.stroke()
        }
    }
}

/// Single-line editable cell: no wrap, horizontal scroll with the caret.
func loftWireLine(_ field: NSTextField) {
    field.usesSingleLineMode = true
    field.maximumNumberOfLines = 1
    field.lineBreakMode = .byClipping
    if let cell = field.cell as? NSTextFieldCell {
        cell.wraps = false
        cell.isScrollable = true
        cell.usesSingleLineMode = true
        cell.lineBreakMode = .byClipping
        cell.truncatesLastVisibleLine = false
    }
}

/// Closed field that pops an `NSMenu` of `titles`. `value` is the selected index.
final class LoftSelect: FlippedView {
    var titles: [String] = [] { didSet { syncTitle() } }
    var value = 0 { didSet { syncTitle() } }
    var onChange: ((Int) -> Void)?
    private var pressed = false
    private let lab = PassLab(labelWithString: "")
    private let chev = PassLab(labelWithString: "▾")

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        setup()
    }

    required init?(coder: NSCoder) {
        super.init(coder: coder)
        setup()
    }

    /// Title and chevron as labels, same inset as `LoftField`.
    private func setup() {
        lab.font = Palette.body(12.5)
        lab.textColor = Palette.snow
        lab.lineBreakMode = .byTruncatingTail
        lab.cell?.usesSingleLineMode = true
        chev.font = Palette.body(11)
        chev.textColor = Palette.mute
        chev.alignment = .center
        addSubview(lab)
        addSubview(chev)
        syncTitle()
    }

    /// Copies the selected title into the label.
    private func syncTitle() {
        lab.stringValue = (value >= 0 && value < titles.count) ? titles[value] : ""
    }

    override func layout() {
        super.layout()
        let inset: CGFloat = 6
        let chevW: CGFloat = 16
        let h = max(8, bounds.height - inset * 2)
        lab.frame = NSRect(x: 12, y: inset, width: max(8, bounds.width - 12 - 8 - chevW), height: h)
        chev.frame = NSRect(x: bounds.width - 12 - chevW, y: inset, width: chevW, height: h)
    }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func resetCursorRects() {
        addCursorRect(bounds, cursor: .pointingHand)
    }

    /// Opens the menu under the field.
    override func mouseDown(with event: NSEvent) {
        pressed = true
        needsDisplay = true
        let m = NSMenu()
        m.autoenablesItems = false
        for (i, t) in titles.enumerated() {
            let it = NSMenuItem(title: t, action: #selector(pick(_:)), keyEquivalent: "")
            it.target = self
            it.tag = i
            it.state = i == value ? .on : .off
            m.addItem(it)
        }
        let loc = NSPoint(x: 0, y: isFlipped ? bounds.height : 0)
        m.popUp(positioning: nil, at: loc, in: self)
        pressed = false
        needsDisplay = true
    }

    @objc private func pick(_ sender: NSMenuItem) {
        value = sender.tag
        onChange?(value)
    }

    /// Field plate only; title lives in `lab`.
    override func draw(_ dirtyRect: NSRect) {
        var fill = Palette.field
        if pressed { fill = loftPress(fill) }
        fill.setFill()
        loftRound(bounds, radius: 10).fill()
        loftStrokeInside(bounds, radius: 10, color: Palette.slate)
    }
}

/// Feather-style stroke glyph next to a `SegBit` title.
enum SegIcon {
    case none
    /// Two loops — a chain link.
    case link
    /// Pencil, tip toward the visual top-right.
    case edit

    /// Strokes the glyph in `box`. Uses the current context’s Y so the pencil is not inverted.
    func stroke(in box: NSRect, color: NSColor) {
        guard self != .none, box.width > 4, box.height > 4 else { return }
        color.setStroke()
        let s = min(box.width, box.height)
        let x0 = box.minX + (box.width - s) / 2
        let y0 = box.minY + (box.height - s) / 2
        let r = NSRect(x: x0, y: y0, width: s, height: s)
        let w = max(1.15, s / 11)
        let yDown = NSGraphicsContext.current?.isFlipped ?? false
        /// Visual top of `r` in the current context.
        func top(_ pad: CGFloat) -> CGFloat { yDown ? r.minY + pad : r.maxY - pad }
        /// Visual bottom of `r` in the current context.
        func bot(_ pad: CGFloat) -> CGFloat { yDown ? r.maxY - pad : r.minY + pad }
        switch self {
        case .none:
            break
        case .link:
            let loopW = s * 0.78
            let loopH = s * 0.40
            let d = s * 0.17
            let dx = d * 0.707
            let dy = d * 0.707 * (yDown ? 1 : -1)
            func loop(cx: CGFloat, cy: CGFloat) {
                let p = NSBezierPath(roundedRect: NSRect(x: -loopW / 2, y: -loopH / 2, width: loopW, height: loopH), xRadius: loopH / 2, yRadius: loopH / 2)
                var xf = AffineTransform.identity
                xf.translate(x: cx, y: cy)
                xf.rotate(byDegrees: yDown ? -45 : 45)
                p.transform(using: xf)
                p.lineWidth = w
                p.lineCapStyle = .round
                p.lineJoinStyle = .round
                p.stroke()
            }
            loop(cx: r.midX + dx, cy: r.midY - dy)
            loop(cx: r.midX - dx, cy: r.midY + dy)
        case .edit:
            let pad = s * 0.16
            let tip = NSPoint(x: r.maxX - pad, y: top(pad))
            let tail = NSPoint(x: r.minX + pad, y: bot(pad))
            let dx = tip.x - tail.x
            let dy = tip.y - tail.y
            let len = max(0.001, hypot(dx, dy))
            let px = -dy / len * s * 0.13
            let py = dx / len * s * 0.13
            let shaft = NSBezierPath()
            shaft.move(to: NSPoint(x: tail.x + px, y: tail.y + py))
            shaft.line(to: NSPoint(x: tail.x - px, y: tail.y - py))
            let cut = s * 0.22
            let cx = tip.x - dx / len * cut
            let cy = tip.y - dy / len * cut
            shaft.line(to: NSPoint(x: cx - px, y: cy - py))
            shaft.line(to: tip)
            shaft.line(to: NSPoint(x: cx + px, y: cy + py))
            shaft.close()
            shaft.lineWidth = w
            shaft.lineCapStyle = .round
            shaft.lineJoinStyle = .round
            shaft.stroke()
        }
    }
}

/// One segment of `SegControl`; `on` uses the lamp fill unless `capsule`.
final class SegBit: NSButton {
    var on = false { didSet { needsDisplay = true } }
    var compact = false
    var capsule = false
    var lampThumb = false
    var icon = SegIcon.none

    /// Borderless segment bit in DemiBold 12.5.
    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        loftWireButton(self)
        font = Palette.font(12.5)
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Lets a first click fire without an extra focus click.
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    /// Redraws so the press fill tracks highlight.
    override func highlight(_ flag: Bool) {
        super.highlight(flag)
        needsDisplay = true
    }

    /// Pointing-hand cursor over the bit.
    override func resetCursorRects() {
        addCursorRect(bounds, cursor: .pointingHand)
    }

    /// Redraws around the AppKit mouse-down so the press fill is visible.
    override func mouseDown(with event: NSEvent) {
        needsDisplay = true
        super.mouseDown(with: event)
        needsDisplay = true
    }

    /// Draws the selected or idle pill and the title.
    override func draw(_ dirtyRect: NSRect) {
        let pressed = isEnabled && isHighlighted
        if !capsule {
            var fill = on ? Palette.lamp : Palette.field
            let border = on ? Palette.lamp : Palette.slate
            if pressed { fill = loftPress(fill) }
            let radius: CGFloat = compact ? 7 : 9
            fill.setFill()
            loftRound(bounds, radius: radius).fill()
            loftStrokeInside(bounds, radius: radius, color: border)
        }
        let p = NSMutableParagraphStyle()
        p.alignment = .center
        let face: NSColor
        if capsule {
            face = on ? (lampThumb ? Palette.ink : Palette.snow) : Palette.mute
        } else {
            face = on ? Palette.ink : Palette.stone
        }
        let attrs: [NSAttributedString.Key: Any] = [
            .font: Palette.font(compact || (capsule && !lampThumb) ? 11.5 : 12.5),
            .foregroundColor: face,
            .paragraphStyle: p
        ]
        let t = title as NSString
        let sz = t.size(withAttributes: attrs)
        let yDown = NSGraphicsContext.current?.isFlipped ?? isFlipped
        let press: CGFloat = pressed ? (yDown ? 1 : -1) : 0
        let iconSide: CGFloat = lampThumb ? 14 : (compact || capsule ? 12 : 13)
        let iconGap: CGFloat = lampThumb ? 7 : 5
        let hasIcon = icon != .none
        let total = sz.width + (hasIcon ? iconSide + iconGap : 0)
        let x0 = max(4, (bounds.width - total) / 2)
        var inkBox = bounds
        inkBox.origin.y += press
        if hasIcon {
            let iy = round((bounds.height - iconSide) / 2 + press)
            icon.stroke(in: NSRect(x: x0, y: iy, width: iconSide, height: iconSide), color: face)
            inkBox.origin.x = x0 + iconSide + iconGap
            inkBox.size.width = max(4, sz.width + 2)
            loftDrawInk(title, attrs: attrs, in: inkBox, alignment: .left, flipped: yDown)
        } else {
            loftDrawInk(title, attrs: attrs, in: inkBox, alignment: .center, flipped: yDown)
        }
    }
}

/// Row of `SegBit`s; `value` is the selected index.
final class SegControl: FlippedView {
    private var buttons: [SegBit] = []
    private let vertical: Bool
    private let capsule: Bool
    private let lampThumb: Bool
    private let thumb = FlippedView(frame: .zero)
    private var thumbReady = false
    var value = 0 { didSet { paint() } }
    var onChange: ((Int) -> Void)?

    /// Builds one `SegBit` per title. `vertical` stacks them; `compact` shrinks the pills.
    /// `capsule` is one track with a sliding thumb (macOS segmented control).
    /// `lampThumb` is the gold selected pill (Link / Manual).
    init(frame: NSRect, titles: [String], vertical: Bool = false, compact: Bool = false, capsule: Bool = false, lampThumb: Bool = false, icons: [SegIcon] = []) {
        self.vertical = vertical
        self.capsule = capsule && !vertical
        self.lampThumb = self.capsule && lampThumb
        super.init(frame: frame)
        if self.capsule {
            wantsLayer = true
            layer?.backgroundColor = Palette.field.cgColor
            layer?.borderWidth = 1
            layer?.borderColor = Palette.slate.cgColor
            thumb.wantsLayer = true
            thumb.layer?.backgroundColor = self.lampThumb
                ? Palette.lamp.cgColor
                : NSColor(srgbRed: 0x4A/255, green: 0x42/255, blue: 0x3C/255, alpha: 1).cgColor
            addSubview(thumb)
        }
        for (i, t) in titles.enumerated() {
            let b = SegBit(frame: .zero)
            b.title = t
            b.compact = compact
            b.capsule = self.capsule
            b.lampThumb = self.lampThumb
            b.icon = i < icons.count ? icons[i] : .none
            b.tag = i
            b.target = self
            b.action = #selector(tap(_:))
            addSubview(b)
            buttons.append(b)
        }
        paint()
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Splits the width across the bits with a 6pt gap, or stacks them when `vertical`.
    override func layout() {
        super.layout()
        let n = buttons.count
        guard n > 0 else { return }
        if vertical {
            let gap: CGFloat = 4
            let h = max(1, (bounds.height - gap * CGFloat(n - 1)) / CGFloat(n))
            var y: CGFloat = 0
            for b in buttons {
                b.frame = NSRect(x: 0, y: y, width: bounds.width, height: h)
                y += h + gap
            }
            return
        }
        if capsule {
            layer?.cornerRadius = bounds.height / 2
            thumb.layer?.cornerRadius = max(4, (bounds.height - 4) / 2)
            let parts = loftSplit(bounds.width, count: n, gap: 0)
            var x: CGFloat = 0
            for (i, b) in buttons.enumerated() {
                b.frame = NSRect(x: x, y: 0, width: parts[i], height: bounds.height)
                x += parts[i]
            }
            slideThumb(animated: thumbReady)
            thumbReady = true
            return
        }
        let gap: CGFloat = 6
        let parts = loftSplit(bounds.width, count: n, gap: gap)
        var x: CGFloat = 0
        for (i, b) in buttons.enumerated() {
            b.frame = NSRect(x: x, y: 0, width: parts[i], height: bounds.height)
            x += parts[i] + gap
        }
    }

    /// Selects `sender.tag` and notifies `onChange`.
    @objc private func tap(_ sender: NSButton) {
        value = sender.tag
        onChange?(value)
        paint()
        if capsule { slideThumb(animated: thumbReady) }
    }

    /// Marks the selected bit `on`.
    private func paint() {
        for (i, b) in buttons.enumerated() {
            b.on = i == value
        }
    }

    /// Moves the capsule thumb onto the selected bit.
    private func slideThumb(animated: Bool) {
        guard capsule, value >= 0, value < buttons.count else { return }
        let inset: CGFloat = lampThumb ? 3 : 2
        var r = buttons[value].frame.insetBy(dx: inset, dy: inset)
        if r.width < 4 { r.size.width = 4 }
        if r.height < 4 { r.size.height = 4 }
        if animated {
            NSAnimationContext.runAnimationGroup { ctx in
                ctx.duration = 0.22
                ctx.timingFunction = CAMediaTimingFunction(name: .easeInEaseOut)
                thumb.animator().frame = r
            }
        } else {
            thumb.frame = r
        }
    }
}

/// Decorative traffic lights; only the red disc closes the window.
final class TrafficLights: FlippedView {
    var onClose: (() -> Void)?
    private var pressRed = false

    /// Pins the cluster to 56×14 at the usual window origin.
    override init(frame frameRect: NSRect) {
        super.init(frame: NSRect(x: 14, y: 12, width: 56, height: 14))
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Lets a first click fire without an extra focus click.
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    /// Pointing-hand only over the red disc.
    override func resetCursorRects() {
        addCursorRect(NSRect(x: 0, y: 0, width: 16, height: bounds.height), cursor: .pointingHand)
    }

    /// Draws the three discs; red darkens while pressed.
    override func draw(_ dirtyRect: NSRect) {
        var red = Palette.trafficR
        if pressRed { red = loftPress(red) }
        red.setFill(); NSBezierPath(ovalIn: NSRect(x: 0, y: 1, width: 12, height: 12)).fill()
        Palette.trafficY.setFill(); NSBezierPath(ovalIn: NSRect(x: 19, y: 1, width: 12, height: 12)).fill()
        Palette.trafficG.setFill(); NSBezierPath(ovalIn: NSRect(x: 38, y: 1, width: 12, height: 12)).fill()
    }

    /// Closes via `onClose` if mouse-up lands on the red disc.
    override func mouseDown(with event: NSEvent) {
        let p = convert(event.locationInWindow, from: nil)
        guard p.x < 16 else { return }
        pressRed = true
        needsDisplay = true
        var keep = true
        while keep {
            guard let e = window?.nextEvent(matching: [.leftMouseUp, .leftMouseDragged]) else { break }
            let q = convert(e.locationInWindow, from: nil)
            switch e.type {
            case .leftMouseDragged:
                let inside = q.x < 16 && bounds.contains(q)
                if pressRed != inside {
                    pressRed = inside
                    needsDisplay = true
                }
            case .leftMouseUp:
                keep = false
                let inside = q.x < 16 && bounds.contains(q)
                pressRed = false
                needsDisplay = true
                if inside { onClose?() }
            default:
                break
            }
        }
    }
}

/// Window chrome: ink plate, traffic lights, and a body below the title bar.
final class LoftRoot: FlippedView {
    let lights = TrafficLights(frame: .zero)
    let body = FlippedView(frame: .zero)

    /// Ink rounded plate with traffic lights wired to `window?.close()`.
    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        wantsLayer = true
        layer?.backgroundColor = Palette.ink.cgColor
        layer?.cornerRadius = 12
        layer?.masksToBounds = true
        lights.onClose = { [weak self] in self?.window?.close() }
        addSubview(lights)
        addSubview(body)
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Inside 1pt window outline.
    override func draw(_ dirtyRect: NSRect) {
        loftStrokeInside(bounds, radius: 12, color: Palette.winLine)
    }

    /// Traffic lights at the top; body fills the rest.
    override func layout() {
        super.layout()
        lights.frame = NSRect(x: 14, y: 12, width: 56, height: 14)
        body.frame = NSRect(x: 0, y: 28, width: bounds.width, height: bounds.height - 28)
    }
}

/// Borderless window that can still become key and main.
final class LoftWindow: NSWindow {
    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { true }
}

/// Borderless movable loft window with `LoftRoot` as the content view.
func makeLoftWindow(size: NSSize) -> NSWindow {
    let w = LoftWindow(
        contentRect: NSRect(origin: .zero, size: size),
        styleMask: [.borderless],
        backing: .buffered,
        defer: false
    )
    w.isOpaque = false
    w.backgroundColor = .clear
    w.hasShadow = true
    w.isMovableByWindowBackground = true
    w.isReleasedWhenClosed = false
    w.level = .normal
    let root = LoftRoot(frame: NSRect(origin: .zero, size: size))
    root.autoresizingMask = [.width, .height]
    w.contentView = root
    return w
}

/// Content body of a loft window (below the traffic lights).
func loftBody(_ w: NSWindow) -> FlippedView {
    return (w.contentView as! LoftRoot).body
}

/// One-button notice using an L10n key.
func loftNotice(_ key: String) {
    let a = NSAlert()
    a.messageText = L10n.t(key)
    a.addButton(withTitle: L10n.t("ok"))
    a.runModal()
}

/// `LoftRoot` content view of a loft window.
func loftRoot(_ w: NSWindow) -> LoftRoot {
    return w.contentView as! LoftRoot
}

/// Resizes `w` so the body is `bodyHeight`, keeping the top edge fixed.
func loftResize(_ w: NSWindow, bodyHeight: CGFloat) {
    let size = NSSize(width: w.frame.width, height: max(120, bodyHeight + 28))
    var f = w.frame
    f.origin.y += f.size.height - size.height
    f.size = size
    if let vis = (w.screen ?? NSScreen.main)?.visibleFrame {
        if f.maxY > vis.maxY { f.origin.y -= f.maxY - vis.maxY }
        if f.minY < vis.minY { f.origin.y = vis.minY }
        if f.maxX > vis.maxX { f.origin.x -= f.maxX - vis.maxX }
        if f.minX < vis.minX { f.origin.x = vis.minX }
    }
    w.setFrame(f, display: true)
    loftRoot(w).frame = NSRect(origin: .zero, size: size)
}

/// Overlay scroller, no gutter, clear over the ink plate.
func loftOverlayScroll(frame: NSRect) -> NSScrollView {
    let s = NSScrollView(frame: frame)
    s.drawsBackground = false
    s.backgroundColor = .clear
    s.hasVerticalScroller = true
    s.hasHorizontalScroller = false
    s.autohidesScrollers = true
    s.borderType = .noBorder
    s.scrollerStyle = .overlay
    s.scrollerKnobStyle = .light
    s.horizontalScrollElasticity = .none
    s.contentView.drawsBackground = false
    s.contentView.backgroundColor = .clear
    return s
}

/// Invisible hit target; optional white wash while highlighted.
final class ActionButton: NSButton {
    var drawsWash = true
    var washRadius: CGFloat = 8

    /// Borderless empty-title hit target.
    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        loftWireButton(self)
        title = ""
    }
    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Lets a first click fire without an extra focus click.
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    /// Redraws so the wash tracks highlight.
    override func highlight(_ flag: Bool) {
        super.highlight(flag)
        needsDisplay = true
    }

    /// Pointing-hand cursor over the hit target.
    override func resetCursorRects() {
        addCursorRect(bounds, cursor: .pointingHand)
    }

    /// Redraws around the AppKit mouse-down so the wash is visible.
    override func mouseDown(with event: NSEvent) {
        needsDisplay = true
        super.mouseDown(with: event)
        needsDisplay = true
    }

    /// Optional 8% white wash while highlighted.
    override func draw(_ dirtyRect: NSRect) {
        guard drawsWash, isHighlighted else { return }
        let rad = min(washRadius, max(4, min(bounds.width, bounds.height) / 2))
        NSColor.white.withAlphaComponent(0.08).setFill()
        loftRound(bounds, radius: rad).fill()
    }
}

/// Borderless momentary button with no focus ring.
func loftWireButton(_ b: NSButton) {
    b.isBordered = false
    b.bezelStyle = .regularSquare
    b.setButtonType(.momentaryPushIn)
    b.focusRingType = .none
}

/// Darkens `c` for a pressed fill.
func loftPress(_ c: NSColor) -> NSColor {
    c.blended(withFraction: 0.34, of: .black) ?? c
}

/// Rounded-rect path with `radius` on every corner.
func loftRound(_ rect: NSRect, radius: CGFloat) -> NSBezierPath {
    NSBezierPath(roundedRect: rect, xRadius: radius, yRadius: radius)
}

/// 1pt stroke fully inside `rect`, so it does not clip on one side.
func loftStrokeInside(_ rect: NSRect, radius: CGFloat, color: NSColor, width: CGFloat = 1) {
    let box = rect.insetBy(dx: width / 2, dy: width / 2)
    let r = max(0, radius - width / 2)
    let p = loftRound(box, radius: r)
    p.lineWidth = width
    color.setStroke()
    p.stroke()
}

/// Ink size of `s` in `font` (cap of «П», not the em-box).
func loftInkSize(_ s: String, font: NSFont) -> NSSize {
    let line = CTLineCreateWithAttributedString(NSAttributedString(string: s, attributes: [.font: font]))
    let r = CTLineGetImageBounds(line, nil)
    return NSSize(width: ceil(r.width), height: ceil(r.height))
}

/// Draws `text` so glyph ink is vertically centered in `box`.
func loftDrawInk(_ text: String, attrs: [NSAttributedString.Key: Any], in box: NSRect, alignment: NSTextAlignment, flipped: Bool) {
    let line = CTLineCreateWithAttributedString(NSAttributedString(string: text, attributes: attrs))
    guard let ctx = NSGraphicsContext.current?.cgContext else { return }
    let ink = CTLineGetImageBounds(line, nil)
    if ink.width < 0.5 || ink.height < 0.5 { return }
    let x: CGFloat
    switch alignment {
    case .right: x = box.width - ink.width - ink.origin.x
    case .center: x = (box.width - ink.width) / 2 - ink.origin.x
    default: x = -ink.origin.x
    }
    let y = (box.height - ink.height) / 2 - ink.origin.y
    ctx.saveGState()
    if flipped {
        ctx.translateBy(x: box.minX, y: box.maxY)
        ctx.scaleBy(x: 1, y: -1)
    } else {
        ctx.translateBy(x: box.minX, y: box.minY)
    }
    ctx.textMatrix = .identity
    ctx.textPosition = CGPoint(x: round(x), y: round(y))
    CTLineDraw(line, ctx)
    ctx.restoreGState()
}

/// Vertical origin that optically centers `text` in `height`.
func loftTextY(_ height: CGFloat, _ text: CGFloat, flipped: Bool) -> CGFloat {
    var y = (height - text) / 2
    if !flipped { y -= 1 }
    return round(y)
}

/// Splits `total` into `count` widths with `gap` between, leftover pixels on the left.
func loftSplit(_ total: CGFloat, count: Int, gap: CGFloat) -> [CGFloat] {
    let n = max(1, count)
    let inner = total - gap * CGFloat(n - 1)
    let base = floor(inner / CGFloat(n))
    var rem = inner - base * CGFloat(n)
    var parts: [CGFloat] = []
    parts.reserveCapacity(n)
    for _ in 0..<n {
        let extra: CGFloat = rem >= 1 ? 1 : rem
        parts.append(base + extra)
        rem = max(0, rem - extra)
    }
    return parts
}

private extension NSBezierPath {
    /// Sets `color` and fills this path.
    func fill(with color: NSColor) {
        color.setFill()
        fill()
    }
}
