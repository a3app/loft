// Status-item popover: connect/disconnect, profile rows, spark line, and footer links.

import AppKit

/// Status-item popover: connect/disconnect, profile rows, spark line, and footer links.
final class PopoverView: FlippedView {
    var onToggle: (() -> Void)?
    var onOpen: (() -> Void)?
    var onQuit: (() -> Void)?
    var onSwitch: (() -> Void)?
    var onReset: (() -> Void)?
    var onJournal: (() -> Void)?
    var onSettings: (() -> Void)?
    /// Opens the profile editor for a connection id from the exclusive menu.
    var onEdit: ((String) -> Void)?
    /// True while the connection `NSMenu` is tracking, so a click on it does not dismiss the popover.
    private(set) var trackingMenu = false
    private weak var engine: SessionController?

    private let mark = MarkView(frame: .zero)
    private let titleLab = NSTextField(labelWithString: "")
    private let subDot = FlippedView(frame: .zero)
    private let subLab = NSTextField(labelWithString: "")
    private let actionBtn = PillButton(frame: .zero)
    private let resetBtn = PillButton(frame: .zero)
    private let kConn = NSTextField(labelWithString: "")
    private let vConn = GhostButton(frame: .zero)
    private let kOver = NSTextField(labelWithString: "Overlay")
    private let vOver = NSTextField(labelWithString: "")
    private let tunBadge = LampTag(frame: .zero)
    private let kExt = NSTextField(labelWithString: "")
    private let vExt = NSTextField(labelWithString: "")
    private let kRoute = NSTextField(labelWithString: "")
    private let vRoute = NSTextField(labelWithString: "")
    private let kSocks = NSTextField(labelWithString: "SOCKS5")
    private let vSocks = NSTextField(labelWithString: "")
    private let sparkL = MarqueeLab(frame: .zero)
    private let sparkR = NSTextField(labelWithString: "")
    private let rateUp = NSTextField(labelWithString: "")
    private let rateDown = NSTextField(labelWithString: "")
    private let openBtn = GhostButton(frame: .zero)
    private let journalBtn = GhostButton(frame: .zero)
    private let quitBtn = GhostButton(frame: .zero)
    private let line = FlippedView(frame: .zero)
    private let connHit = ActionButton(frame: .zero)
    /// Several on: line above the collapsed sessions.
    private let othersLine = FlippedView(frame: .zero)
    private lazy var others = SessionRows(host: self)
    private var showRows = true
    private var showNet = false
    private var showReset = false
    private var showRates = false
    private var showSocks = false
    private var showTun = false
    private var showExt = false

    /// Lets a first click on the popover fire without an extra focus click.
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    /// Builds the popover chrome: mark, action, rows, spark, and footer links.
    override init(frame frameRect: NSRect) {
        super.init(frame: NSRect(x: 0, y: 0, width: 300, height: 280))
        wantsLayer = true
        layer?.backgroundColor = Palette.popFill.cgColor
        layer?.cornerRadius = 12
        layer?.masksToBounds = true

        titleLab.font = Palette.body(13)
        titleLab.textColor = Palette.snow
        subDot.wantsLayer = true
        subDot.layer?.cornerRadius = 3.5
        subLab.font = Palette.body(12)
        subLab.textColor = Palette.mute

        actionBtn.target = self
        actionBtn.action = #selector(tapToggle)
        resetBtn.target = self
        resetBtn.action = #selector(tapReset)

        for lab in [kConn, kOver, kExt, kRoute, kSocks, vOver, vExt, vRoute, vSocks, titleLab, subLab, sparkR, rateUp, rateDown] {
            lab.usesSingleLineMode = true
            lab.lineBreakMode = .byTruncatingTail
            lab.cell?.wraps = false
            lab.cell?.truncatesLastVisibleLine = true
        }
        for lab in [kConn, kOver, kExt, kRoute, kSocks] {
            lab.font = Palette.body(12.5)
            lab.textColor = Palette.mute
        }
        for lab in [vOver, vExt, vRoute, vSocks] {
            lab.font = Palette.mono(11.5)
            lab.textColor = Palette.snow
            lab.alignment = .right
        }
        tunBadge.isHidden = true
        vConn.font = Palette.mono(11.5)
        vConn.tone = Palette.stone
        vConn.alignment = .right
        vConn.target = self
        vConn.action = #selector(tapSwitch)
        sparkL.font = Palette.body(11.5)
        sparkL.textColor = Palette.mute
        sparkR.font = Palette.body(11.5)
        sparkR.textColor = Palette.mute
        sparkR.alignment = .right
        for lab in [rateUp, rateDown] {
            lab.font = Palette.body(11.5)
            lab.textColor = Palette.mute
            lab.alignment = .right
        }

        line.wantsLayer = true
        line.layer?.backgroundColor = Palette.line.cgColor
        othersLine.wantsLayer = true
        othersLine.layer?.backgroundColor = Palette.line.cgColor
        others.onPrefer = { [weak self] id in self?.engine?.prefer(id) }
        others.onToggle = { [weak self] id in self?.engine?.toggle(id) }

        openBtn.font = Palette.body(12.5)
        openBtn.tone = Palette.stone
        openBtn.alignment = .left
        openBtn.target = self
        openBtn.action = #selector(tapOpen)

        quitBtn.font = Palette.body(12.5)
        quitBtn.tone = Palette.stone
        quitBtn.alignment = .right
        quitBtn.target = self
        quitBtn.action = #selector(tapQuit)

        journalBtn.font = Palette.body(12.5)
        journalBtn.tone = Palette.stone
        journalBtn.alignment = .center
        journalBtn.target = self
        journalBtn.action = #selector(tapSettings)

        connHit.title = ""
        connHit.drawsWash = false
        connHit.target = self
        connHit.action = #selector(tapSwitch)

        // connHit under the row so the value can still hover as a link.
        for v in [mark, titleLab, subDot, subLab, rateUp, rateDown, actionBtn, resetBtn, connHit, kConn, vConn, kOver, tunBadge, vOver, kExt, vExt, kRoute, vRoute, kSocks, vSocks, sparkL, sparkR, othersLine, line, openBtn, journalBtn, quitBtn] as [NSView] {
            addSubview(v)
        }
    }

    /// Inside 1pt popover outline.
    override func draw(_ dirtyRect: NSRect) {
        loftStrokeInside(bounds, radius: 12, color: NSColor(white: 1, alpha: 0.08))
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Relays session state into labels and returns the fitted popover size.
    func render(engine: SessionController) -> NSSize {
        self.engine = engine
        let p = engine.displayProfile()
        let name: String
        if let p = p, !p.name.isEmpty { name = p.name }
        else if let p = p { name = Palette.hostOf(p.url) }
        else { name = "—" }
        let simple = engine.store.simple
        let held = engine.holdArmed
        let on = engine.state == .on
        let fail = engine.state == .error
        let connecting = engine.state == .connecting
        let disconnecting = engine.state == .disconnecting
        let busy = connecting || disconnecting
        showRows = !simple
        showNet = on && !held && !fail
        showReset = held

        var markState: MarkState = .off
        if held || fail { markState = .err }
        else if on { markState = .on }
        mark.state = markState

        if on, let from = engine.connectedAt {
            titleLab.stringValue = "\(L10n.t("pill_on"))  \(Palette.uptime(from))"
        } else {
            titleLab.stringValue = on ? L10n.t("pill_on") : L10n.t("pop_off")
        }
        titleLab.textColor = Palette.snow
        subLab.stringValue = name
        subLab.textColor = (held || fail) ? Palette.err : Palette.mute
        subDot.layer?.backgroundColor = ((held || fail) ? Palette.err : (on ? Palette.lamp : Palette.mute)).cgColor

        actionBtn.lampOn = on && !held && !fail && !busy
        actionBtn.isEnabled = !disconnecting
        actionBtn.title = engine.actionTitle()
        resetBtn.title = L10n.t("reset")
        resetBtn.lampOn = false
        resetBtn.isHidden = !showReset

        kConn.stringValue = L10n.t("conn")
        vConn.title = name + "\u{2004}›"
        kOver.stringValue = L10n.t("overlay")
        kExt.stringValue = L10n.t("ext_ip_lab")
        kRoute.stringValue = L10n.t("route")

        let socksLive = engine.socksDisplay
        showSocks = showNet && !socksLive.isEmpty
        showTun = showNet && engine.tunLive
        showExt = showNet && engine.store.showExtIP
        vSocks.stringValue = showSocks ? socksLive : ""
        vRoute.stringValue = showNet ? engine.routeCaption : ""
        vRoute.textColor = Palette.snow
        vOver.stringValue = showNet ? (engine.overlayIP.isEmpty ? "—" : engine.overlayIP) : ""
        vOver.textColor = Palette.snow
        if showExt {
            if !engine.extIP.isEmpty { vExt.stringValue = engine.extIP }
            else if engine.extIPBusy { vExt.stringValue = "…" }
            else { vExt.stringValue = "—" }
        } else {
            vExt.stringValue = ""
        }
        vExt.textColor = Palette.snow

        let spark = engine.sparkLine()
        showRates = spark.pair
        let upStr = "↑ \(Palette.rate(engine.upRate, bits: engine.store.rateBits))"
        let downStr = "↓ \(Palette.rate(engine.downRate, bits: engine.store.rateBits))"
        rateUp.stringValue = showRates ? upStr : ""
        rateDown.stringValue = showRates ? downStr : ""
        sparkL.textColor = spark.color
        sparkR.stringValue = ""

        openBtn.title = L10n.t("open")
        journalBtn.title = L10n.t("settings")
        quitBtn.title = L10n.t("quit")

        let pad: CGFloat = 14
        let w: CGFloat = 300
        var y: CGFloat = pad
        mark.frame = NSRect(x: pad, y: y, width: 36, height: 36)
        let textX = pad + 46
        let rateFont = Palette.body(11.5)
        let rateW: CGFloat = showRates
            ? max(64, Palette.textWidth(upStr, font: rateFont), Palette.textWidth(downStr, font: rateFont))
            : 0
        let rateGap: CGFloat = showRates ? 8 : 0
        let textW = w - textX - pad - rateW - rateGap
        titleLab.frame = NSRect(x: textX, y: y + 1, width: textW, height: 18)
        let subY: CGFloat = y + 20
        let subH: CGFloat = 16
        let dotS: CGFloat = 7
        subDot.frame = NSRect(x: textX, y: subY + round((subH - dotS) / 2) + 3, width: dotS, height: dotS)
        subLab.frame = NSRect(x: textX + 11, y: subY, width: max(20, textW - 11), height: subH)
        rateUp.frame = NSRect(x: w - pad - rateW, y: y + 1, width: rateW, height: 18)
        rateDown.frame = NSRect(x: w - pad - rateW, y: subY, width: rateW, height: subH)
        rateUp.isHidden = !showRates
        rateDown.isHidden = !showRates
        y += 36 + 14
        actionBtn.frame = NSRect(x: pad, y: y, width: w - pad * 2, height: 40)
        y += 40
        if showReset {
            y += 6
            resetBtn.frame = NSRect(x: pad, y: y, width: w - pad * 2, height: 40)
            y += 40
        }
        if showRows {
            y += 12
            let inset: CGFloat = pad + 2
            let keyW: CGFloat = 118
            let valX = inset + keyW
            let valW = w - inset - valX
            let rowH: CGFloat = 28
            let labH: CGFloat = 18
            let labY = { (rowY: CGFloat) in rowY + round((rowH - labH) / 2) }
            /// Places a mute key label on a 28pt row.
            func placeKey(_ k: NSTextField, _ rowY: CGFloat) {
                k.frame = NSRect(x: inset, y: labY(rowY), width: keyW, height: labH)
                k.isHidden = false
            }
            let connY = y
            placeKey(kConn, connY)
            vConn.frame = NSRect(x: valX, y: labY(connY), width: valW, height: labH)
            vConn.isHidden = false
            connHit.frame = NSRect(x: pad, y: connY, width: w - pad * 2, height: rowH)
            connHit.isHidden = false
            y += rowH
            /// Places a key/value row and advances `y`.
            func row(_ k: NSTextField, _ v: NSTextField) {
                placeKey(k, y)
                v.frame = NSRect(x: valX, y: labY(y), width: valW, height: labH)
                v.isHidden = false
                y += rowH
            }
            if showNet {
                if showExt {
                    row(kExt, vExt)
                } else {
                    kExt.isHidden = true
                    vExt.isHidden = true
                }
                let overY = y
                let keyFont = kOver.font ?? Palette.body(12.5)
                let overW = Palette.textWidth(kOver.stringValue, font: keyFont)
                kOver.frame = NSRect(x: inset, y: labY(overY), width: overW, height: labH)
                kOver.isHidden = false
                if showTun {
                    let tag = L10n.t("net_tag")
                    tunBadge.text = tag
                    let sz = LampTag.size(for: tag)
                    tunBadge.frame = NSRect(
                        x: inset + overW + 6,
                        y: overY + round((rowH - sz.height) / 2),
                        width: sz.width,
                        height: sz.height
                    )
                    tunBadge.isHidden = false
                } else {
                    tunBadge.isHidden = true
                }
                vOver.frame = NSRect(x: valX, y: labY(overY), width: valW, height: labH)
                vOver.isHidden = false
                y += rowH
                row(kRoute, vRoute)
            } else {
                kOver.isHidden = true
                vOver.isHidden = true
                kExt.isHidden = true
                vExt.isHidden = true
                kRoute.isHidden = true
                vRoute.isHidden = true
                tunBadge.isHidden = true
            }
            if showSocks {
                row(kSocks, vSocks)
            } else {
                kSocks.isHidden = true
                vSocks.isHidden = true
            }
            y += 4
            sparkL.frame = NSRect(x: inset, y: y, width: w - inset * 2, height: 16)
            sparkR.isHidden = true
            sparkL.isHidden = false
            sparkL.show(spark.text, scroll: spark.scroll, keepMoving: connecting || disconnecting)
            y += 24
            let rest = engine.otherLive()
            othersLine.isHidden = rest.isEmpty
            if !rest.isEmpty {
                othersLine.frame = NSRect(x: pad, y: y - 2, width: w - pad * 2, height: 1)
                y = others.place(rest, engine: engine, x: pad, y: y + 4, width: w - pad * 2, inset: 2, gap: 0) + 6
            }
        } else {
            othersLine.isHidden = true
            _ = others.place([], engine: engine, x: 0, y: 0, width: 0, inset: 0, gap: 0)
            for v in [kConn, vConn, kOver, vOver, tunBadge, kExt, vExt, kRoute, vRoute, kSocks, vSocks, sparkL, sparkR, connHit] as [NSView] {
                v.isHidden = true
            }
            sparkL.show("", scroll: false)
            y += 8
        }
        line.frame = NSRect(x: pad, y: y, width: w - pad * 2, height: 1)
        y += 8
        /// Width of a ghost title, at least 32pt.
        func ghostW(_ b: GhostButton) -> CGFloat {
            max(32, Palette.textWidth(b.title, font: b.font ?? Palette.body(12.5)))
        }
        let oW = ghostW(openBtn)
        let jW = ghostW(journalBtn)
        let qW = ghostW(quitBtn)
        openBtn.frame = NSRect(x: pad, y: y, width: oW, height: 22)
        quitBtn.frame = NSRect(x: w - pad - qW, y: y, width: qW, height: 22)
        let minJ = pad + oW + 8
        let maxJ = w - pad - qW - 8 - jW
        let midJ = round((w - jW) / 2)
        let jX = maxJ >= minJ ? min(max(midJ, minJ), maxJ) : midJ
        journalBtn.frame = NSRect(x: jX, y: y, width: jW, height: 22)
        y += 22 + 10
        let h = y
        frame = NSRect(x: 0, y: 0, width: w, height: h)
        return NSSize(width: w, height: h)
    }

    /// Connect/disconnect (or retry) from the main pill.
    @objc private func tapToggle() {
        LoftLog.shared.tap("popover", actionBtn.title)
        onToggle?()
    }
    /// Opens the status window.
    @objc private func tapOpen() { onOpen?() }
    /// Quits the app.
    @objc private func tapQuit() { onQuit?() }
    /// Restores pre-loft routes after a hold.
    @objc private func tapReset() { onReset?() }
    /// Connection row: menu of profiles with status, connect, TUN, and default route.
    @objc private func tapSwitch() {
        guard let engine = engine else { onSwitch?(); return }
        if engine.store.profiles.isEmpty {
            onSwitch?()
            return
        }
        let m = NSMenu()
        m.autoenablesItems = false
        let multi = engine.store.multi
        let rows: [(LoftProfile, String)] = engine.profilesByFreshness().map { p in
            let name = p.name.isEmpty ? Palette.hostOf(p.url) : p.name
            let st = menuStatus(engine, p.id)
            return (p, st.isEmpty ? name : "\(name)  ·  \(st)")
        }
        let rowW = max(160, rows.map { ConnPickRow.width(for: $0.1) }.max() ?? 160)
        for (p, title) in rows {
            let it = NSMenuItem(title: title, action: multi ? #selector(menuPrefer(_:)) : nil, keyEquivalent: "")
            it.target = multi ? self : nil
            it.representedObject = p.id
            it.state = p.id == engine.displayID ? .on : .off
            it.submenu = connectionSubmenu(engine, p)
            if multi {
                let row = ConnPickRow(title: title, checked: it.state == .on, pid: p.id, width: rowW)
                row.onPick = { [weak self] id in
                    self?.engine?.prefer(id)
                }
                it.view = row
            }
            m.addItem(it)
        }
        trackingMenu = true
        m.popUp(positioning: nil, at: menuOrigin(for: m), in: self)
        trackingMenu = false
    }

    /// Top-left of `menu` in this view: right of the popover when it fits, else left.
    private func menuOrigin(for menu: NSMenu) -> NSPoint {
        let gap: CGFloat = 6
        let size = menu.size
        let rowY = connHit.frame.minY
        guard let win = window else {
            return NSPoint(x: bounds.maxX + gap, y: rowY)
        }
        let popScr = win.convertToScreen(convert(bounds, to: nil))
        let vis = (win.screen ?? NSScreen.main)?.visibleFrame ?? popScr
        let need = size.width + gap + 8
        let roomRight = vis.maxX - popScr.maxX
        let roomLeft = popScr.minX - vis.minX
        let x: CGFloat
        if roomRight >= need {
            x = bounds.maxX + gap
        } else if roomLeft >= need {
            x = -size.width - gap
        } else if roomLeft > roomRight {
            x = -size.width - gap
        } else {
            x = bounds.maxX + gap
        }
        return NSPoint(x: x, y: rowY)
    }

    /// Connect/disconnect, tunnel, default-route, then the profile editor.
    private func connectionSubmenu(_ engine: SessionController, _ p: LoftProfile) -> NSMenu {
        let sub = NSMenu()
        sub.autoenablesItems = false
        let st = engine.state(of: p.id)
        let act = NSMenuItem(title: engine.actionTitle(of: p.id), action: #selector(menuToggle(_:)), keyEquivalent: "")
        act.target = self
        act.representedObject = p.id
        act.isEnabled = st != .disconnecting
        sub.addItem(act)
        sub.addItem(.separator())
        let tun = NSMenuItem(title: L10n.t("net_tag"), action: #selector(menuTun(_:)), keyEquivalent: "")
        tun.target = self
        tun.representedObject = p.id
        tun.state = p.tun ? .on : .off
        tun.isEnabled = p.tun ? p.socksOn : true
        sub.addItem(tun)
        let route = NSMenuItem(title: L10n.t("default_route"), action: #selector(menuDefault(_:)), keyEquivalent: "")
        route.target = self
        route.representedObject = p.id
        route.state = engine.defaultArmed(of: p.id) ? .on : .off
        route.isEnabled = st == .on && engine.tunLive(of: p.id)
        sub.addItem(route)
        sub.addItem(.separator())
        let setup = NSMenuItem(title: L10n.t("pop_setup"), action: #selector(menuSetup(_:)), keyEquivalent: "")
        setup.target = self
        setup.representedObject = p.id
        sub.addItem(setup)
        return sub
    }

    /// After the profile name: IP or “connected” while up; empty while idle.
    private func menuStatus(_ engine: SessionController, _ id: String) -> String {
        switch engine.state(of: id) {
        case .on:
            if engine.store.showExtIP {
                let ip = engine.extIP(of: id)
                if !ip.isEmpty { return ip }
            }
            return L10n.t("pill_on")
        case .off: return ""
        case .connecting: return L10n.t("connecting")
        case .disconnecting: return L10n.t("disconnecting")
        case .error: return L10n.t("err")
        }
    }

    @objc private func menuPrefer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        LoftLog.shared.tap("pop-menu", "prefer")
        engine?.prefer(id)
    }

    @objc private func menuToggle(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        LoftLog.shared.tap("pop-menu", sender.title)
        engine?.toggle(id)
    }

    @objc private func menuTun(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String, let engine = engine else { return }
        guard let p = engine.store.profiles.first(where: { $0.id == id }) else { return }
        engine.setProfileTun(id, on: !p.tun)
    }

    @objc private func menuDefault(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String, let engine = engine else { return }
        engine.setSessionDefault(id, on: !engine.defaultArmed(of: id))
    }

    @objc private func menuSetup(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        LoftLog.shared.tap("pop-menu", "setup")
        onEdit?(id)
    }
    /// Opens the journal (wired if a journal footer is shown).
    @objc private func tapJournal() { onJournal?() }
    /// Opens settings. The center footer button currently calls this.
    @objc private func tapSettings() { onSettings?() }
}

/// One connection row in the popover menu. Click sets priority; the system still
/// draws the submenu chevron so Connect / tunnel / default / setup stay on the arrow.
private final class ConnPickRow: NSView {
    static let rowH: CGFloat = 22
    static let checkW: CGFloat = 20
    static let chevronW: CGFloat = 18
    private static var menuFont: NSFont { NSFont.menuFont(ofSize: 0) }

    let pid: String
    private let title: String
    private let checked: Bool
    var onPick: ((String) -> Void)?

    init(title: String, checked: Bool, pid: String, width: CGFloat) {
        self.title = title
        self.checked = checked
        self.pid = pid
        super.init(frame: NSRect(x: 0, y: 0, width: width, height: Self.rowH))
    }

    required init?(coder: NSCoder) { nil }

    static func width(for title: String) -> CGFloat {
        let w = (title as NSString).size(withAttributes: [.font: menuFont]).width
        return ceil(checkW + 8 + w + chevronW + 10)
    }

    override var intrinsicContentSize: NSSize { frame.size }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        updateTracking()
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        updateTracking()
    }

    /// Hover so `draw` sees `enclosingMenuItem.isHighlighted`.
    private func updateTracking() {
        for a in trackingAreas { removeTrackingArea(a) }
        addTrackingArea(NSTrackingArea(
            rect: bounds,
            options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect],
            owner: self,
            userInfo: nil
        ))
    }

    override func mouseEntered(with event: NSEvent) { needsDisplay = true }
    override func mouseExited(with event: NSEvent) { needsDisplay = true }

    /// Clicks on the chevron (right ~28pt) fall through so the submenu still opens.
    override func hitTest(_ point: NSPoint) -> NSView? {
        if point.x > bounds.maxX - 28 { return nil }
        return super.hitTest(point)
    }

    override func draw(_ dirtyRect: NSRect) {
        let on = enclosingMenuItem?.isHighlighted ?? false
        if on {
            NSColor.selectedMenuItemColor.setFill()
            bounds.fill()
        }
        let ink: NSColor = on ? .selectedMenuItemTextColor : .controlTextColor
        let attrs: [NSAttributedString.Key: Any] = [.font: Self.menuFont, .foregroundColor: ink]
        if checked {
            let mark = "✓"
            let sz = (mark as NSString).size(withAttributes: attrs)
            let y = ((bounds.height - sz.height) / 2).rounded()
            (mark as NSString).draw(at: NSPoint(x: 6, y: y), withAttributes: attrs)
        }
        let sz = (title as NSString).size(withAttributes: attrs)
        let y = ((bounds.height - sz.height) / 2).rounded()
        (title as NSString).draw(at: NSPoint(x: Self.checkW, y: y), withAttributes: attrs)
        let chev = "›"
        let csz = (chev as NSString).size(withAttributes: attrs)
        let cx = bounds.maxX - Self.chevronW + (Self.chevronW - csz.width) / 2
        (chev as NSString).draw(at: NSPoint(x: cx.rounded(), y: y), withAttributes: attrs)
    }

    override func mouseUp(with event: NSEvent) {
        onPick?(pid)
        enclosingMenuItem?.menu?.cancelTracking()
    }
}
