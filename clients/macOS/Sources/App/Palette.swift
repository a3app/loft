// Colors, fonts, and small formatters used by the loft menubar UI.
// Every window takes its colors (dark "ink" background, gold "lamp" accent,
// "snow" text, "mute" grey) and fonts from here, so the look stays the same
// everywhere. The widgets that use them are in Chrome.swift.

import AppKit

/// Colors, fonts, and small formatters used by the loft menubar UI.
enum Palette {
    static let ink = NSColor(srgbRed: 0x1A/255, green: 0x17/255, blue: 0x14/255, alpha: 1)
    static let ink2 = NSColor(srgbRed: 0x24/255, green: 0x1F/255, blue: 0x1B/255, alpha: 1)
    static let slate = NSColor(srgbRed: 0x3A/255, green: 0x33/255, blue: 0x2E/255, alpha: 1)
    static let stone = NSColor(srgbRed: 0xB7/255, green: 0xA9/255, blue: 0x9A/255, alpha: 1)
    static let copper = NSColor(srgbRed: 0xC6/255, green: 0x7B/255, blue: 0x3A/255, alpha: 1)
    static let lamp = NSColor(srgbRed: 0xE8/255, green: 0xB8/255, blue: 0x4A/255, alpha: 1)
    static let lampHi = NSColor(srgbRed: 0xF3/255, green: 0xD2/255, blue: 0x7A/255, alpha: 1)
    static let snow = NSColor(srgbRed: 0xF3/255, green: 0xED/255, blue: 0xE4/255, alpha: 1)
    static let paper = NSColor(srgbRed: 0xE8/255, green: 0xDF/255, blue: 0xD2/255, alpha: 1)
    static let mute = NSColor(srgbRed: 0x8A/255, green: 0x7E/255, blue: 0x72/255, alpha: 1)
    static let line = NSColor(srgbRed: 0xF3/255, green: 0xED/255, blue: 0xE4/255, alpha: 0.12)
    static let ok = NSColor(srgbRed: 0x7D/255, green: 0xBA/255, blue: 0x6A/255, alpha: 1)
    static let err = NSColor(srgbRed: 0xE2/255, green: 0x4B/255, blue: 0x4B/255, alpha: 1)
    static let field = NSColor(srgbRed: 0x21/255, green: 0x1C/255, blue: 0x19/255, alpha: 1)
    static let winLine = NSColor(srgbRed: 0x2E/255, green: 0x28/255, blue: 0x24/255, alpha: 1)
    static let popFill = NSColor(srgbRed: 32/255, green: 28/255, blue: 25/255, alpha: 0.94)
    static let trafficR = NSColor(srgbRed: 1, green: 0x5F/255, blue: 0x57/255, alpha: 1)
    static let trafficY = NSColor(srgbRed: 0xFE/255, green: 0xBC/255, blue: 0x2E/255, alpha: 1)
    static let trafficG = NSColor(srgbRed: 0x28/255, green: 0xC8/255, blue: 0x40/255, alpha: 1)

    /// Avenir Next at `size`, falling back to the system font.
    static func font(_ size: CGFloat, weight: NSFont.Weight = .semibold) -> NSFont {
        let name: String
        switch weight {
        case .bold, .heavy, .black: name = "AvenirNext-Bold"
        case .semibold, .medium: name = "AvenirNext-DemiBold"
        default: name = "AvenirNext-Medium"
        }
        return NSFont(name: name, size: size)
            ?? NSFont(name: "Avenir Next Demi Bold", size: size)
            ?? NSFont.systemFont(ofSize: size, weight: weight)
    }

    /// Medium Avenir Next for body copy.
    static func body(_ size: CGFloat = 13) -> NSFont {
        return NSFont(name: "AvenirNext-Medium", size: size)
            ?? NSFont(name: "Avenir Next", size: size)
            ?? NSFont.systemFont(ofSize: size)
    }

    /// SF Mono for keys, IPs, and log lines.
    static func mono(_ size: CGFloat = 12) -> NSFont {
        return NSFont(name: "SFMono-Regular", size: size)
            ?? NSFont(name: "SF Mono", size: size)
            ?? NSFont.userFixedPitchFont(ofSize: size)
            ?? NSFont.systemFont(ofSize: size)
    }

    private static var widthCache: [String: CGFloat] = [:]
    private static var iconCache: [Int: NSImage] = [:]

    /// Single-line width of `s` in `font`, plus a 2pt fudge.
    static func textWidth(_ s: String, font: NSFont) -> CGFloat {
        let key = "\(font.fontName)|\(font.pointSize)|\(s)"
        if let w = widthCache[key] { return w }
        let cell = NSTextFieldCell(textCell: s)
        cell.font = font
        cell.usesSingleLineMode = true
        cell.lineBreakMode = .byTruncatingTail
        cell.wraps = false
        let sz = cell.cellSize(forBounds: NSRect(x: 0, y: 0, width: 10_000, height: 40))
        let w = ceil(sz.width) + 2
        if widthCache.count > 256 { widthCache.removeAll(keepingCapacity: true) }
        widthCache[key] = w
        return w
    }

    /// Wrapped height of `s` in `font` at `width`.
    static func textHeight(_ s: String, font: NSFont, width: CGFloat) -> CGFloat {
        let r = (s as NSString).boundingRect(
            with: NSSize(width: width, height: 400),
            options: [.usesLineFragmentOrigin, .usesFontLeading],
            attributes: [.font: font]
        )
        return ceil(r.height)
    }

    /// Truncates `t` to a single line with an ellipsis.
    static func fitLine(_ t: NSTextField) {
        t.usesSingleLineMode = true
        t.lineBreakMode = .byTruncatingTail
        t.cell?.wraps = false
        t.cell?.isScrollable = false
        t.cell?.truncatesLastVisibleLine = true
    }

    /// Non-editable single-line label. Clicks fall through to the row behind.
    static func label(_ s: String, font: NSFont, color: NSColor, frame: NSRect) -> NSTextField {
        let t = PassLab(labelWithString: s)
        t.font = font
        t.textColor = color
        t.frame = frame
        t.backgroundColor = .clear
        t.isBezeled = false
        t.drawsBackground = false
        t.isEditable = false
        t.isSelectable = false
        fitLine(t)
        return t
    }

    /// Non-editable wrapping label, capped at `lines`.
    static func wrap(_ s: String, font: NSFont, color: NSColor, frame: NSRect, lines: Int = 3) -> NSTextField {
        let t = NSTextField(wrappingLabelWithString: s)
        t.font = font
        t.textColor = color
        t.backgroundColor = .clear
        t.isBezeled = false
        t.drawsBackground = false
        t.isEditable = false
        t.isSelectable = false
        t.maximumNumberOfLines = lines
        t.preferredMaxLayoutWidth = frame.width
        t.lineBreakMode = .byWordWrapping
        t.cell?.wraps = true
        t.cell?.truncatesLastVisibleLine = true
        t.frame = frame
        return t
    }

    /// Status-item mark at 1x/2x/3x. `err` paints the lamp red. Cached per state.
    static func menuIcon(on: Bool, err: Bool = false) -> NSImage {
        let key = err ? 2 : (on ? 1 : 0)
        if let img = iconCache[key] { return img }
        let st: MarkState = err ? .err : (on ? .on : .off)
        let pt = NSSize(width: 18, height: 18)
        let img = NSImage(size: pt)
        for scale in [1, 2, 3] {
            guard let rep = NSBitmapImageRep(
                bitmapDataPlanes: nil,
                pixelsWide: 18 * scale,
                pixelsHigh: 18 * scale,
                bitsPerSample: 8,
                samplesPerPixel: 4,
                hasAlpha: true,
                isPlanar: false,
                colorSpaceName: .deviceRGB,
                bytesPerRow: 0,
                bitsPerPixel: 0
            ) else { continue }
            rep.size = pt
            NSGraphicsContext.saveGraphicsState()
            if let ctx = NSGraphicsContext(bitmapImageRep: rep) {
                NSGraphicsContext.current = ctx
                let flip = NSAffineTransform()
                flip.translateX(by: 0, yBy: pt.height)
                flip.scaleX(by: 1, yBy: -1)
                flip.concat()
                MarkView.paint(in: NSRect(origin: .zero, size: pt), state: st, body: Palette.snow)
            }
            NSGraphicsContext.restoreGraphicsState()
            img.addRepresentation(rep)
        }
        img.isTemplate = false
        iconCache[key] = img
        return img
    }

    /// 16pt template menu glyph. SF Symbol on 11+, `draw` on 10.13–10.15.
    static func menuGlyph(_ symbol: String, draw: @escaping (NSRect) -> Void) -> NSImage {
        if #available(macOS 11.0, *) {
            if let img = NSImage(systemSymbolName: symbol, accessibilityDescription: nil) {
                img.isTemplate = true
                img.size = NSSize(width: 16, height: 16)
                return img
            }
        }
        let pt = NSSize(width: 16, height: 16)
        let img = NSImage(size: pt, flipped: true) { rect in
            NSColor.black.setStroke()
            draw(rect)
            return true
        }
        img.isTemplate = true
        return img
    }

    /// Empty 16pt slot so a check-only item keeps the Tools icon column.
    static func blankMenuIcon() -> NSImage {
        let img = NSImage(size: NSSize(width: 16, height: 16))
        img.isTemplate = true
        return img
    }

    /// Check for Tools → Split routing, same slot as Settings / Routes / Journal.
    static func checkMenuIcon() -> NSImage {
        menuGlyph("checkmark") { rect in
            let p = NSBezierPath()
            p.lineWidth = 1.6
            p.lineCapStyle = .round
            p.lineJoinStyle = .round
            p.move(to: NSPoint(x: 3.2, y: 8.2))
            p.line(to: NSPoint(x: 6.4, y: 11.4))
            p.line(to: NSPoint(x: 12.8, y: 4.6))
            p.stroke()
        }
    }

    /// Window for Loft → Open Loft.
    static func openLoftMenuIcon() -> NSImage {
        menuGlyph("macwindow") { rect in
            let win = NSBezierPath(
                roundedRect: NSRect(x: 2.2, y: 3.2, width: 11.6, height: 9.6),
                xRadius: 1.4,
                yRadius: 1.4
            )
            win.lineWidth = 1.2
            win.stroke()
            let bar = NSBezierPath()
            bar.move(to: NSPoint(x: 2.2, y: 6.2))
            bar.line(to: NSPoint(x: 13.8, y: 6.2))
            bar.lineWidth = 1.2
            bar.stroke()
        }
    }

    /// Globe for Loft → Language.
    static func langMenuIcon() -> NSImage {
        menuGlyph("globe") { rect in
            let inset = rect.insetBy(dx: 1.5, dy: 1.5)
            let ring = NSBezierPath(ovalIn: inset)
            ring.lineWidth = 1.2
            ring.stroke()
            let mer = NSBezierPath()
            mer.move(to: NSPoint(x: inset.midX, y: inset.minY))
            mer.curve(
                to: NSPoint(x: inset.midX, y: inset.maxY),
                controlPoint1: NSPoint(x: inset.minX + 1.2, y: inset.midY),
                controlPoint2: NSPoint(x: inset.maxX - 1.2, y: inset.midY)
            )
            mer.lineWidth = 1.2
            mer.stroke()
            let eq = NSBezierPath()
            eq.move(to: NSPoint(x: inset.minX, y: inset.midY))
            eq.line(to: NSPoint(x: inset.maxX, y: inset.midY))
            eq.lineWidth = 1.2
            eq.stroke()
        }
    }

    /// Gear for Tools → Settings.
    static func settingsMenuIcon() -> NSImage {
        menuGlyph("gearshape") { rect in
            let c = NSPoint(x: rect.midX, y: rect.midY)
            let teeth = 6
            let outer: CGFloat = 6.4
            let valley: CGFloat = 4.2
            let path = NSBezierPath()
            path.lineWidth = 1.2
            path.lineJoinStyle = .round
            for i in 0..<teeth {
                let a0 = CGFloat(i) / CGFloat(teeth) * .pi * 2 - .pi / 2
                let step = .pi / CGFloat(teeth)
                let p0 = NSPoint(x: c.x + cos(a0) * outer, y: c.y + sin(a0) * outer)
                let p1 = NSPoint(x: c.x + cos(a0 + step) * valley, y: c.y + sin(a0 + step) * valley)
                if i == 0 { path.move(to: p0) } else { path.line(to: p0) }
                path.line(to: p1)
            }
            path.close()
            path.stroke()
            let hole: CGFloat = 1.8
            let inner = NSBezierPath(ovalIn: NSRect(x: c.x - hole, y: c.y - hole, width: hole * 2, height: hole * 2))
            inner.lineWidth = 1.2
            inner.stroke()
        }
    }

    /// Branch for Tools → Routes.
    static func routesMenuIcon() -> NSImage {
        menuGlyph("arrow.triangle.branch") { rect in
            let p = NSBezierPath()
            p.lineWidth = 1.2
            p.lineCapStyle = .round
            p.lineJoinStyle = .round
            p.move(to: NSPoint(x: rect.midX, y: rect.maxY - 2))
            p.line(to: NSPoint(x: rect.midX, y: rect.midY))
            p.move(to: NSPoint(x: rect.midX, y: rect.midY))
            p.line(to: NSPoint(x: rect.minX + 3.2, y: rect.minY + 3))
            p.move(to: NSPoint(x: rect.midX, y: rect.midY))
            p.line(to: NSPoint(x: rect.maxX - 3.2, y: rect.minY + 3))
            p.stroke()
        }
    }

    /// Document for Tools → Journal.
    static func journalMenuIcon() -> NSImage {
        menuGlyph("doc.text") { rect in
            let page = NSBezierPath(roundedRect: NSRect(x: 3.5, y: 1.8, width: 9, height: 12.4), xRadius: 1.2, yRadius: 1.2)
            page.lineWidth = 1.2
            page.stroke()
            for i in 0..<3 {
                let y = 5.2 + CGFloat(i) * 2.8
                let line = NSBezierPath()
                line.move(to: NSPoint(x: 5.4, y: y))
                line.line(to: NSPoint(x: i == 2 ? 9.2 : 10.6, y: y))
                line.lineWidth = 1.1
                line.stroke()
            }
        }
    }

    /// Host from `url`, or the raw string if it has no scheme.
    static func hostOf(_ url: String) -> String {
        if let u = URL(string: url), let h = u.host, !h.isEmpty { return h }
        var s = url
        if let r = s.range(of: "://") { s = String(s[r.upperBound...]) }
        if let r = s.range(of: "/") { s = String(s[..<r.lowerBound]) }
        return s.isEmpty ? url : s
    }

    /// Port suffix of `host:port`, or `"1080"` if `addr` is empty.
    static func socksPort(_ addr: String) -> String {
        if let r = addr.range(of: ":", options: .backwards) {
            return String(addr[r.upperBound...])
        }
        return addr.isEmpty ? "1080" : addr
    }

    /// Elapsed time from `from`: `m:ss`, `h:mm:ss`, or `1д 12:03:45` after a day.
    static func uptime(_ from: Date) -> String {
        let s = max(0, Int(Date().timeIntervalSince(from)))
        let d = s / 86_400
        let h = (s % 86_400) / 3600
        let m = (s % 3600) / 60
        let sec = s % 60
        if d > 0 {
            return String(format: "%d%@ %d:%02d:%02d", d, L10n.t("u_day"), h, m, sec)
        }
        if h > 0 { return String(format: "%d:%02d:%02d", h, m, sec) }
        return String(format: "%d:%02d", m, sec)
    }

    /// Formats `n` bytes/s as bit/s or B/s with SI/1024 units.
    static func rate(_ n: Int, bits: Bool) -> String {
        if bits {
            let b = max(0, n) * 8
            if b < 1000 { return "\(b) \(L10n.t("u_bit"))" }
            if b < 1_000_000 {
                let k = Double(b) / 1000.0
                if k < 10 { return String(format: "%.1f %@", k, L10n.t("u_kbit")) }
                return String(format: "%.0f %@", k, L10n.t("u_kbit"))
            }
            return String(format: "%.1f %@", Double(b) / 1_000_000.0, L10n.t("u_mbit"))
        }
        if n < 1024 { return "\(max(0, n)) \(L10n.t("u_b"))" }
        if n < 1024 * 1024 {
            let k = Double(n) / 1024.0
            if k < 10 { return String(format: "%.1f %@", k, L10n.t("u_kb")) }
            return String(format: "%.0f %@", k, L10n.t("u_kb"))
        }
        return String(format: "%.1f %@", Double(n) / (1024.0 * 1024.0), L10n.t("u_mb"))
    }
}
