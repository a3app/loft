// Loft windows: status, connections list, settings, profile editor, and onboard.
// Split-routing sheets live in SplitWindows.swift.

import AppKit

/// Status window: mark, connect pill, overlay/front/SOCKS cards, spark, footer.
final class StatusWindowController: NSWindowController {
    private let engine: SessionController
    private let mark = MarkView(frame: .zero)
    private let titleLab = NSTextField(labelWithString: "loft")
    private let pillBox = FlippedView(frame: .zero)
    private let pillLab = NSTextField(labelWithString: "")
    private let dot = FlippedView(frame: .zero)
    private let pillHit = ActionButton(frame: .zero)
    private let action = PillButton(frame: .zero)
    private let sOverK = NSTextField(labelWithString: "OVERLAY")
    private let sOverV = NSTextField(labelWithString: "—")
    private let sFrontK = NSTextField(labelWithString: "")
    private let sFrontV = NSTextField(labelWithString: "auto")
    private let sSocksK = NSTextField(labelWithString: "SOCKS5")
    private let sSocksV = NSTextField(labelWithString: ":1080")
    private let sparkL = MarqueeLab(frame: .zero)
    private let sparkR = NSTextField(labelWithString: "")
    private let journalBtn = PillButton(frame: .zero)
    private let resetBtn = PillButton(frame: .zero)
    private let splitBtn = PillButton(frame: .zero)
    private let setBtn = PillButton(frame: .zero)
    private let footLine = FlippedView(frame: .zero)
    private let cards = [LoftPlate(), LoftPlate(), LoftPlate()]
    /// Several on: the other live sessions, collapsed under the spark.
    private var others: SessionRows!

    /// Builds the status window around `engine` and reloads on `.loftState`.
    init(engine: SessionController) {
        self.engine = engine
        let w = makeLoftWindow(size: NSSize(width: 400, height: 500))
        super.init(window: w)
        w.level = .normal
        build()
        w.center()
        NotificationCenter.default.addObserver(self, selector: #selector(reload), name: .loftState, object: nil)
        reload()
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Lays out mark, pill, action, cards, spark, and footer buttons.
    private func build() {
        let c = loftBody(window!)
        titleLab.font = Palette.font(22)
        titleLab.textColor = Palette.snow
        titleLab.alignment = .center
        Palette.fitLine(titleLab)
        pillBox.wantsLayer = true
        pillBox.layer?.backgroundColor = Palette.field.cgColor
        pillBox.layer?.cornerRadius = 12
        pillBox.layer?.masksToBounds = false
        pillLab.font = Palette.body(11.5)
        pillLab.textColor = Palette.mute
        pillLab.alignment = .left
        Palette.fitLine(pillLab)
        pillHit.drawsWash = false
        pillHit.target = NSApp.delegate
        pillHit.action = #selector(AppDelegate.showList)
        dot.wantsLayer = true
        dot.layer?.cornerRadius = 3.5
        pillBox.addSubview(dot)
        pillBox.addSubview(pillLab)
        pillBox.addSubview(pillHit)
        action.target = self
        action.action = #selector(tap)
        for (card, labs) in zip(cards, [[sOverK, sOverV], [sFrontK, sFrontV], [sSocksK, sSocksV]]) {
            card.fill = Palette.field
            card.radius = 10
            for l in labs { card.addSubview(l) }
            c.addSubview(card)
        }
        for k in [sOverK, sFrontK, sSocksK] {
            k.font = Palette.body(10.5)
            k.textColor = Palette.mute
            Palette.fitLine(k)
        }
        for v in [sOverV, sFrontV, sSocksV] {
            v.font = Palette.mono(12)
            v.textColor = Palette.snow
            Palette.fitLine(v)
        }
        sparkL.font = Palette.body(11.5)
        sparkL.textColor = Palette.mute
        sparkR.font = Palette.body(11.5)
        sparkR.textColor = Palette.mute
        sparkR.alignment = .right
        Palette.fitLine(sparkR)
        footLine.wantsLayer = true
        footLine.layer?.backgroundColor = Palette.line.cgColor
        journalBtn.compact = true
        resetBtn.compact = true
        splitBtn.compact = true
        setBtn.compact = true
        journalBtn.target = NSApp.delegate
        journalBtn.action = #selector(AppDelegate.showJournal)
        resetBtn.target = self
        resetBtn.action = #selector(resetTap)
        splitBtn.target = NSApp.delegate
        splitBtn.action = #selector(AppDelegate.showSplit)
        setBtn.target = NSApp.delegate
        setBtn.action = #selector(AppDelegate.showSettings)
        c.addSubview(mark)
        c.addSubview(titleLab)
        c.addSubview(pillBox)
        c.addSubview(action)
        c.addSubview(sparkL)
        c.addSubview(sparkR)
        c.addSubview(footLine)
        c.addSubview(journalBtn)
        c.addSubview(resetBtn)
        c.addSubview(splitBtn)
        c.addSubview(setBtn)
        others = SessionRows(host: c)
        others.plateFill = Palette.field
        others.onPrefer = { [weak self] id in self?.engine.prefer(id) }
        others.onToggle = { [weak self] id in self?.engine.toggle(id) }
    }

    /// Relays session state into captions, cards, and spark.
    @objc func reload() {
        let e = engine
        let p = e.store.active()
        let on = e.state == .on
        let fail = e.state == .error
        let held = e.holdArmed
        let connecting = e.state == .connecting
        let disconnecting = e.state == .disconnecting
        let busy = connecting || disconnecting
        mark.state = (held || fail) ? .err : (on ? .on : .off)
        let pillText: String
        if on {
            let name: String
            if let p = p, !p.name.isEmpty { name = p.name }
            else if let p = p { name = Palette.hostOf(p.url) }
            else { name = L10n.t("pill_on") }
            let time = e.connectedAt.map { Palette.uptime($0) } ?? "0:00"
            pillText = "\(L10n.t("pill_on")): \(name) \(time)"
        } else {
            pillText = L10n.t("pill_off")
        }
        pillLab.stringValue = pillText
        dot.layer?.backgroundColor = ((held || fail) ? Palette.err : (on ? Palette.lamp : Palette.mute)).cgColor
        action.lampOn = on && !held && !busy
        action.isEnabled = !disconnecting
        action.title = e.actionTitle()
        setKicker(sOverK, "OVERLAY")
        setKicker(sFrontK, L10n.t("front").uppercased())
        setKicker(sSocksK, "SOCKS5")
        let simple = e.store.simple
        for card in cards { card.isHidden = simple }
        sparkL.isHidden = simple
        sparkR.isHidden = simple
        journalBtn.title = L10n.t("journal")
        resetBtn.title = L10n.t("reset")
        splitBtn.title = L10n.t("split_lists")
        setBtn.title = L10n.t("settings")
        if on {
            sOverV.stringValue = e.overlayIP.isEmpty ? "—" : e.overlayIP
            sFrontV.stringValue = e.frontName.isEmpty ? (p?.kind ?? "get") : e.frontName
        } else {
            sOverV.stringValue = "—"
            sFrontV.stringValue = "auto"
        }
        let spark = e.sparkLine()
        sparkL.textColor = spark.color
        if spark.pair {
            sparkR.stringValue = "↓ \(Palette.rate(e.downRate, bits: e.store.rateBits))"
            sparkR.textColor = Palette.mute
        } else {
            sparkR.stringValue = ""
        }
        let socksShown = on ? e.socksDisplay : ""
        if socksShown.isEmpty {
            sSocksV.stringValue = "—"
        } else {
            sSocksV.stringValue = ":" + Palette.socksPort(socksShown)
        }
        layout()
        let crawl = connecting || disconnecting
        sparkL.show(spark.text, scroll: spark.scroll, keepMoving: crawl)
    }

    /// Positions status chrome; simple view hides cards and spark.
    private func layout() {
        let c = loftBody(window!)
        let w = c.bounds.width > 1 ? c.bounds.width : 400
        let simple = engine.store.simple
        let wideSpark = engine.state == .error || engine.state == .connecting || engine.state == .disconnecting
        mark.frame = NSRect(x: round((w - 72) / 2), y: 16, width: 72, height: 72)
        titleLab.frame = NSRect(x: 22, y: 92, width: w - 44, height: 28)
        let pillNeed = Palette.textWidth(pillLab.stringValue, font: Palette.body(11.5)) + 36
        let pillW = min(w - 44, max(128, pillNeed))
        pillBox.frame = NSRect(x: round((w - pillW) / 2), y: 126, width: pillW, height: 24)
        dot.frame = NSRect(x: 10, y: 8.5, width: 7, height: 7)
        pillLab.frame = NSRect(x: 22, y: 3, width: pillW - 32, height: 18)
        pillHit.frame = pillBox.bounds
        action.frame = NSRect(x: 22, y: 166, width: w - 44, height: 42)
        if !simple {
            let gap: CGFloat = 8
            let parts = loftSplit(w - 44, count: 3, gap: gap)
            let pairs = [(sOverK, sOverV), (sFrontK, sFrontV), (sSocksK, sSocksV)]
            var cx: CGFloat = 22
            for (i, card) in cards.enumerated() {
                let cw = parts[i]
                card.frame = NSRect(x: cx, y: 226, width: cw, height: 58)
                pairs[i].0.frame = NSRect(x: 10, y: 8, width: cw - 20, height: 18)
                pairs[i].1.frame = NSRect(x: 10, y: 30, width: cw - 20, height: 20)
                cx += cw + gap
            }
            sparkL.frame = NSRect(x: 22, y: 300, width: wideSpark ? w - 44 : 160, height: 18)
            sparkR.frame = NSRect(x: w - 22 - 160, y: 300, width: 160, height: 18)
            sparkR.isHidden = wideSpark
        }
        let rest = simple ? [] : engine.otherLive()
        let restEnd = others.place(rest, engine: engine, x: 22, y: 330, width: w - 44, inset: 10, gap: 6)
        let bodyH: CGFloat = simple ? 310 : (rest.isEmpty ? 490 : max(490, restEnd + 88))
        footLine.frame = NSRect(x: 0, y: bodyH - 72, width: w, height: 1)
        let gap: CGFloat = 8
        let inner = w - 40
        let font = Palette.font(11.5)
        let minW: CGFloat = 56
        /// Minimum footer-button width for title `s`.
        func need(_ s: String) -> CGFloat { max(minW, Palette.textWidth(s, font: font) + 14) }
        var widths = [journalBtn, resetBtn, splitBtn, setBtn].map { need($0.title) }
        let extra = inner - widths.reduce(0, +) - gap * 3
        if extra >= 0 {
            let extraL = floor(extra / 2)
            widths[0] += extraL
            widths[3] += extra - extraL
        } else {
            var rest = extra
            for i in [0, 3, 1, 2] where rest < 0 {
                let room = widths[i] - minW
                guard room > 0 else { continue }
                let cut = min(room, -rest)
                widths[i] -= cut
                rest += cut
            }
        }
        widths = widths.map { round($0) }
        let by = bodyH - 58
        var x: CGFloat = 20
        let btns = [journalBtn, resetBtn, splitBtn, setBtn]
        for i in btns.indices {
            btns[i].frame = NSRect(x: x, y: by, width: widths[i], height: 36)
            x += widths[i] + gap
        }
        if let win = window {
            loftResize(win, bodyHeight: bodyH)
        }
    }

    /// Uppercase kicker with a little tracking.
    private func setKicker(_ field: NSTextField, _ text: String) {
        let p = NSMutableParagraphStyle()
        p.lineBreakMode = .byTruncatingTail
        field.attributedStringValue = NSAttributedString(string: text, attributes: [
            .font: Palette.body(10.5),
            .foregroundColor: Palette.mute,
            .kern: 0.45,
            .paragraphStyle: p,
        ])
    }

    /// Restores pre-loft routes.
    @objc private func resetTap() { engine.resetRoutes() }

    /// Connects, or opens onboard if there is no profile.
    @objc private func tap() {
        LoftLog.shared.tap("status", action.title)
        if engine.store.profiles.isEmpty {
            AppDelegate.shared?.showOnboard()
            return
        }
        engine.toggle()
    }
}

/// One connections-list row. Reused across rebuilds.
private final class ConnRow: FlippedView {
    let plate = LoftPlate(frame: .zero)
    let hit = ActionButton(frame: .zero)
    let act = PillButton(frame: .zero)
    let name = PassLab(labelWithString: "")
    let host = PassLab(labelWithString: "")
    let tagLab = PassLab(labelWithString: "")
    let side = PassLab(labelWithString: "")
    let edit = GhostButton(frame: .zero)

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        plate.radius = 10
        hit.drawsWash = false
        act.compact = true
        name.font = Palette.font(13.5)
        name.textColor = Palette.snow
        host.font = Palette.mono(11.5)
        host.textColor = Palette.mute
        tagLab.font = Palette.font(10)
        tagLab.textColor = Palette.lamp
        side.font = Palette.body(10.5)
        side.alignment = .right
        edit.font = Palette.body(11.5)
        edit.tone = Palette.mute
        edit.alignment = .right
        for t in [name, host, tagLab, side] {
            t.backgroundColor = .clear
            t.isBezeled = false
            t.drawsBackground = false
            t.isEditable = false
            t.isSelectable = false
            Palette.fitLine(t)
        }
        for v in [plate, hit, act, name, host, tagLab, side, edit] as [NSView] { addSubview(v) }
    }

    required init?(coder: NSCoder) { fatalError() }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        plate.frame = bounds
        hit.frame = bounds
    }
}

/// Connections list: profiles, multi toggle, import/export, drop target.
final class ConnectionsWindowController: NSWindowController {
    private let engine: SessionController
    private let titleLab = DrawLab(frame: .zero)
    private let addBtn = PillButton(frame: .zero)
    private let multiCap = DrawLab(frame: .zero)
    private let multiSw = ToggleSwitch(frame: .zero)
    private let sub = NSTextField(labelWithString: "")
    private var listScroll: NSScrollView!
    private let list = FlippedView(frame: .zero)
    private let imp = PillButton(frame: .zero)
    private let exp = PillButton(frame: .zero)
    private let set = PillButton(frame: .zero)
    private let footLine = FlippedView(frame: .zero)
    private let dropPlate = LoftPlate(frame: .zero)
    private let drop = NSTextField(wrappingLabelWithString: "")
    private var rows: [String: ConnRow] = [:]

    /// Builds the connections window around `engine` and rebuilds on `.loftState`.
    init(engine: SessionController) {
        self.engine = engine
        let w = makeLoftWindow(size: NSSize(width: 400, height: 520))
        super.init(window: w)
        w.level = .normal
        build()
        w.center()
        rebuild()
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Lays out title, multi toggle, drop plate, and footer buttons.
    private func build() {
        let c = loftBody(window!)
        titleLab.font = Palette.font(20)
        titleLab.color = Palette.snow
        titleLab.pillNudge = false
        addBtn.compact = true
        addBtn.lampOn = true
        addBtn.title = "+"
        addBtn.target = self
        addBtn.action = #selector(manual)
        multiCap.font = Palette.body(12.5)
        multiCap.color = Palette.mute
        multiCap.alignment = .right
        multiCap.pillNudge = false
        multiSw.onChange = { [weak self] on in
            self?.engine.setMulti(on)
            self?.rebuild()
        }
        sub.font = Palette.body(12.5)
        sub.textColor = Palette.mute
        drop.font = Palette.body(13)
        drop.textColor = Palette.mute
        drop.alignment = .center
        footLine.wantsLayer = true
        footLine.layer?.backgroundColor = Palette.line.cgColor
        imp.compact = true
        exp.compact = true
        set.compact = true
        imp.target = self; imp.action = #selector(impTap)
        exp.target = self; exp.action = #selector(expTap)
        set.target = self; set.action = #selector(setTap)
        dropPlate.radius = 12
        dropPlate.stroke = Palette.slate
        listScroll = loftOverlayScroll(frame: .zero)
        listScroll.documentView = list
        for v in [titleLab, addBtn, multiCap, multiSw, sub, listScroll, dropPlate, drop, footLine, imp, exp, set] as [NSView] {
            c.addSubview(v)
        }
        c.registerForDraggedTypes([.fileURL])
        c.onFiles = { [weak self] urls in self?.importURLs(urls) }
    }

    /// Rebuilds the profile rows from `engine.store`.
    @objc func rebuild() {
        let c = loftBody(window!)
        let w = c.bounds.width > 1 ? c.bounds.width : 400
        titleLab.text = L10n.t("conns")
        multiCap.text = L10n.t("several")
        multiSw.on = engine.store.multi
        sub.stringValue = engine.store.multi ? L10n.t("conns_sub_multi") : L10n.t("conns_sub")
        imp.title = L10n.t("import")
        exp.title = L10n.t("export")
        set.title = L10n.t("settings")
        drop.stringValue = L10n.t("drop")
        let titleFont = Palette.font(20)
        let capFont = Palette.body(12.5)
        let plusS = max(12, round(loftInkSize("П", font: titleFont).height * 1.10))
        let rowY: CGFloat = 8
        let rowH: CGFloat = 28
        let mid = rowY + rowH / 2
        let swW: CGFloat = 36
        let swH: CGFloat = 22
        let swX = w - 20 - swW
        multiSw.frame = NSRect(x: swX, y: round(mid - swH / 2), width: swW, height: swH)
        let capNeed = Palette.textWidth(multiCap.text, font: capFont)
        let titleNeed = Palette.textWidth(titleLab.text, font: titleFont)
        // Right cluster (caption + toggle) keeps its natural width; title yields if the row is tight.
        let capW = min(capNeed, max(24, swX - 8 - (20 + 80 + 8 + plusS + 12)))
        multiCap.frame = NSRect(x: swX - 8 - capW, y: round(mid - rowH / 2), width: capW, height: rowH)
        let titleMax = multiCap.frame.minX - 12 - 20 - 8 - plusS
        let titleW = min(titleNeed, max(80, titleMax))
        titleLab.frame = NSRect(x: 20, y: rowY, width: titleW, height: rowH)
        addBtn.frame = NSRect(x: 20 + titleW + 8, y: round(mid - plusS / 2), width: plusS, height: plusS)
        sub.lineBreakMode = .byTruncatingTail
        sub.usesSingleLineMode = true
        sub.frame = NSRect(x: 20, y: 40, width: w - 40, height: 18)
        var y: CGFloat = 0
        let simple = engine.store.simple
        let multi = engine.store.multi
        let busy = !multi && (engine.state == .connecting || engine.state == .disconnecting)
        let actFont = Palette.font(11.5)
        let actTitles = [L10n.t("pop_connect"), L10n.t("disconnect"), L10n.t("connecting"), L10n.t("disconnecting"), L10n.t("retry"), L10n.t("cancel"), String(format: L10n.t("retry_wait"), 15)]
        let actW = max(72, (actTitles.map { Palette.textWidth($0, font: actFont) }.max() ?? 72) + 18)
        let actH: CGFloat = 26
        let empty = engine.store.profiles.isEmpty
        drop.isHidden = !empty
        dropPlate.isHidden = !empty
        listScroll.isHidden = empty
        let kept = listScroll.contentView.bounds.origin
        var seen = Set<String>()
        for p in engine.profilesByFreshness() {
            seen.insert(p.id)
            let rowH: CGFloat = simple ? 48 : 56
            let rowW = w - 40
            let row = rows[p.id] ?? ConnRow(frame: .zero)
            if rows[p.id] == nil {
                rows[p.id] = row
                list.addSubview(row)
                row.hit.target = self
                row.hit.action = #selector(pick(_:))
                row.act.target = self
                row.act.action = #selector(toggleOne(_:))
                row.edit.target = self
                row.edit.action = #selector(editOne(_:))
            }
            row.frame = NSRect(x: 10, y: y, width: rowW, height: rowH)
            row.hit.frame = row.bounds
            row.hit.identifier = NSUserInterfaceItemIdentifier(p.id)
            row.act.identifier = NSUserInterfaceItemIdentifier(p.id)
            row.edit.identifier = NSUserInterfaceItemIdentifier(p.id)
            let active = p.id == engine.store.activeID || p.id == engine.displayID
            let st = engine.state(of: p.id)
            let live = st == .on
            row.plate.fill = (active || live) ? Palette.field : nil
            row.plate.stroke = (active || live) ? Palette.slate : nil
            if st == .error {
                row.act.title = engine.actionTitle(of: p.id)
                row.act.lampOn = false
                row.act.isEnabled = true
            } else if st == .disconnecting {
                row.act.title = L10n.t("disconnecting")
                row.act.lampOn = false
                row.act.isEnabled = false
            } else if st == .connecting {
                row.act.title = engine.actionTitle(of: p.id)
                row.act.lampOn = false
                row.act.isEnabled = true
            } else if live {
                row.act.title = L10n.t("disconnect")
                row.act.lampOn = true
                row.act.isEnabled = true
            } else {
                row.act.title = L10n.t("pop_connect")
                row.act.lampOn = false
                row.act.isEnabled = !busy
            }
            let actX: CGFloat = 8
            row.act.frame = NSRect(x: actX, y: round((rowH - actH) / 2), width: actW, height: actH)
            let textX = actX + actW + 8
            let sideStr: String
            if simple {
                sideStr = st == .error ? L10n.t("fail") : ""
            } else if multi {
                if live, engine.store.showExtIP {
                    let ip = engine.extIP(of: p.id)
                    if !ip.isEmpty { sideStr = ip }
                    else if engine.extIPBusy(of: p.id) { sideStr = "…" }
                    else if !engine.overlayIP(of: p.id).isEmpty { sideStr = engine.overlayIP(of: p.id) }
                    else { sideStr = L10n.t("conn_up") }
                } else if live, !engine.overlayIP(of: p.id).isEmpty { sideStr = engine.overlayIP(of: p.id) }
                else if st == .error { sideStr = L10n.t("fail") }
                else if live { sideStr = L10n.t("conn_up") }
                else { sideStr = p.kind }
            } else {
                sideStr = active ? L10n.t("active") : p.kind
            }
            let editStr = L10n.t("edit")
            let sideW = max(
                52,
                Palette.textWidth(sideStr, font: Palette.body(10.5)),
                Palette.textWidth(editStr, font: Palette.body(11.5))
            ) + 4
            let sideX = rowW - 8 - sideW
            let nameFont = Palette.font(13.5)
            let showTag = engine.defaultArmed(of: p.id) && !simple
            let tagStr = L10n.t("route_tag").uppercased()
            let tagFont = Palette.font(10)
            let tagW: CGFloat = showTag ? Palette.textWidth(tagStr, font: tagFont) : 0
            let nameMax = max(48, sideX - textX - 8 - (showTag ? tagW + 6 : 0))
            let nameW = min(Palette.textWidth(p.name, font: nameFont), nameMax)
            row.name.stringValue = p.name
            row.name.frame = NSRect(x: textX, y: simple ? 12 : 8, width: nameW, height: 22)
            row.tagLab.isHidden = !showTag
            if showTag {
                row.tagLab.stringValue = tagStr
                row.tagLab.frame = NSRect(x: textX + nameW + 6, y: 10, width: tagW, height: 18)
            }
            row.host.isHidden = simple
            if !simple {
                var sub = Palette.hostOf(p.url)
                if p.socksOn {
                    let port = Palette.socksPort(engine.store.socksBind(for: p))
                    if !port.isEmpty { sub += " · " + port }
                }
                row.host.stringValue = sub
                row.host.frame = NSRect(x: textX, y: 32, width: max(40, sideX - textX - 8), height: 18)
            }
            row.side.isHidden = sideStr.isEmpty
            if !sideStr.isEmpty {
                row.side.stringValue = sideStr
                row.side.textColor = (active || live) ? Palette.lamp : Palette.stone
                row.side.frame = NSRect(x: sideX, y: simple ? 6 : 8, width: sideW, height: 18)
            }
            let edY: CGFloat = (simple && sideStr.isEmpty) ? round((rowH - 18) / 2) : (simple ? 28 : 30)
            row.edit.title = editStr
            row.edit.frame = NSRect(x: sideX, y: edY, width: sideW, height: 18)
            y += row.frame.height + 4
        }
        for (id, row) in rows where !seen.contains(id) {
            row.removeFromSuperview()
            rows.removeValue(forKey: id)
        }
        let header: CGFloat = 66
        let footer: CGFloat = 12 + 1 + 14 + 36 + 20
        let contentH: CGFloat = empty ? 80 : max(1, y)
        if empty {
            drop.frame = NSRect(x: 20, y: header, width: w - 40, height: contentH)
            dropPlate.frame = drop.frame
        }
        let vis = (window?.screen ?? NSScreen.main)?.visibleFrame.height ?? 800
        let maxBody = max(280, vis - 28 - 20)
        let want = header + contentH + footer
        let scrollH: CGFloat
        let bodyH: CGFloat
        if want <= maxBody {
            scrollH = contentH
            bodyH = want
        } else {
            scrollH = max(80, maxBody - header - footer)
            bodyH = header + scrollH + footer
        }
        if !empty {
            let listW = w - 20
            list.frame = NSRect(x: 0, y: 0, width: listW, height: max(contentH, scrollH))
            listScroll.frame = NSRect(x: 10, y: header, width: listW, height: scrollH)
            let maxY = max(0, list.frame.height - listScroll.contentView.bounds.height)
            listScroll.contentView.scroll(to: NSPoint(x: 0, y: min(max(0, kept.y), maxY)))
            listScroll.reflectScrolledClipView(listScroll.contentView)
        }
        footLine.frame = NSRect(x: 0, y: header + scrollH + 12, width: w, height: 1)
        let btnY = footLine.frame.maxY + 14
        let parts = loftSplit(w - 40, count: 3, gap: 8)
        imp.frame = NSRect(x: 20, y: btnY, width: parts[0], height: 36)
        exp.frame = NSRect(x: 20 + parts[0] + 8, y: btnY, width: parts[1], height: 36)
        set.frame = NSRect(x: 20 + parts[0] + 8 + parts[1] + 8, y: btnY, width: parts[2], height: 36)
        loftResize(window!, bodyHeight: bodyH)
    }

    /// Selects the profile on this row without connecting.
    @objc private func pick(_ sender: NSButton) {
        let id = sender.identifier?.rawValue ?? ""
        if id.isEmpty || id == engine.store.activeID { return }
        paintSelection(id)
        engine.selectProfile(id)
    }

    /// Connects or disconnects the profile on this row.
    @objc private func toggleOne(_ sender: NSButton) {
        LoftLog.shared.tap("list", sender.title)
        let id = sender.identifier?.rawValue ?? ""
        guard engine.store.profiles.contains(where: { $0.id == id }) else { return }
        engine.toggle(id)
    }

    /// Highlights the row whose hit target is `id`.
    private func paintSelection(_ id: String) {
        for row in list.subviews.compactMap({ $0 as? ConnRow }) {
            let on = row.hit.identifier?.rawValue == id
            let live = on && engine.state(of: id) == .on
            row.plate.fill = (on || live) ? Palette.field : nil
            row.plate.stroke = (on || live) ? Palette.slate : nil
        }
    }

    /// Opens the editor for the profile on this row.
    @objc private func editOne(_ sender: NSButton) {
        let id = sender.identifier?.rawValue ?? ""
        guard let p = engine.store.profiles.first(where: { $0.id == id }) else { return }
        AppDelegate.shared?.showEdit(p)
    }

    /// Open-panel import of `.loft` / YAML / `.vpn` / QR image into the store.
    @objc private func impTap() {
        let p = shareOpenPanel()
        p.begin { [weak self] r in
            if r == NSApplication.ModalResponse.OK {
                self?.importURLs(p.urls.isEmpty ? (p.url.map { [$0] } ?? []) : p.urls)
            }
        }
    }

    /// Saves every share file / QR image; notices when none parsed.
    private func importURLs(_ urls: [URL]) {
        let images = urls.filter { QRScan.isImage($0) }
        let files = urls.filter { !QRScan.isImage($0) }
        var n = 0
        for u in files {
            if engine.store.importFile(u) != nil { n += 1 }
        }
        if images.isEmpty {
            if n == 0 { loftNotice("imp_fail") }
            NotificationCenter.default.post(name: .loftState, object: engine)
            rebuild()
            return
        }
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let decoded = images.map { ($0, QRScan.payloads(from: $0)) }
            DispatchQueue.main.async {
                guard let self = self else { return }
                var extra = n
                var qrEmpty = decoded.count == 1
                for (u, payloads) in decoded {
                    if !payloads.isEmpty { qrEmpty = false }
                    if self.engine.store.importPayloads(payloads, fileName: u.deletingPathExtension().lastPathComponent) != nil {
                        extra += 1
                    }
                }
                if extra == 0 {
                    loftNotice(qrEmpty ? "qr_none" : "qr_fail")
                }
                NotificationCenter.default.post(name: .loftState, object: self.engine)
                self.rebuild()
            }
        }
    }

    /// Save-panel export of the active profile (warns that the key is in the file).
    @objc private func expTap() {
        guard let prof = engine.store.active() else { return }
        let alert = NSAlert()
        alert.messageText = L10n.t("export")
        alert.informativeText = L10n.t("warn_export")
        alert.addButton(withTitle: L10n.t("export"))
        alert.addButton(withTitle: L10n.t("back"))
        if alert.runModal() != .alertFirstButtonReturn { return }
        let p = NSSavePanel()
        p.allowedFileTypes = ["loft"]
        p.nameFieldStringValue = "\(prof.name).loft"
        p.begin { [weak self] r in
            if r == NSApplication.ModalResponse.OK, let u = p.url {
                try? self?.engine.store.exportYAML(prof).write(to: u, atomically: true, encoding: .utf8)
            }
        }
    }

    /// Opens settings from the list footer.
    @objc private func setTap() { AppDelegate.shared?.showSettings() }

    /// Opens the editor with a blank GET profile.
    @objc private func manual() {
        let p = LoftProfile(id: "", name: "", token: "", url: "", kind: "get", path: "/static/js", socks: "auto", tun: true, mux: true, instanceID: "", file: "")
        AppDelegate.shared?.showEdit(p)
    }
}

/// Settings window: general, route, and client tabs of prefs.
final class SettingsWindowController: NSWindowController {
    private let engine: SessionController
    private var switches: [String: ToggleSwitch] = [:]
    private var tab = 0
    private var socksPortF: LoftField?
    private var extIPURLF: LoftField?

    /// Builds the settings window around `engine`.
    init(engine: SessionController) {
        self.engine = engine
        let w = makeLoftWindow(size: NSSize(width: 440, height: 420))
        super.init(window: w)
        w.level = .normal
        w.center()
        reload()
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Rebuilds the current tab from prefs.
    func reload() {
        commitSocksPort()
        commitExtIPURL()
        socksPortF = nil
        extIPURLF = nil
        let c = loftBody(window!)
        c.subviews.forEach { $0.removeFromSuperview() }
        switches.removeAll()
        let resetTitle = L10n.t("reset")
        let resetW = min(200, Palette.textWidth(resetTitle, font: Palette.font(11.5)) + 22)
        let headY: CGFloat = 16
        let headH: CGFloat = 28
        let title = DrawLab(frame: NSRect(x: 22, y: headY, width: 396 - resetW - 12, height: headH))
        title.text = L10n.t("settings")
        title.font = Palette.font(20)
        title.color = Palette.snow
        c.addSubview(title)
        let resetHead = PillButton(frame: NSRect(x: 22 + 396 - resetW, y: headY, width: resetW, height: headH))
        resetHead.compact = true
        resetHead.title = resetTitle
        resetHead.target = self
        resetHead.action = #selector(resetTap)
        c.addSubview(resetHead)
        var y: CGFloat = headY + headH + 12
        let tabs = SegControl(frame: NSRect(x: 22, y: y, width: 396, height: 32), titles: [L10n.t("set_gen"), L10n.t("route"), L10n.t("set_client")])
        tabs.value = tab
        tabs.onChange = { [weak self] v in
            self?.tab = v
            DispatchQueue.main.async { self?.reload() }
        }
        c.addSubview(tabs)
        y += 44
        let textW: CGFloat = 328
        let swX: CGFloat = 382
        /// One named pref row with a toggle.
        func pref(_ key: String, _ dkey: String, _ on: Bool) {
            let padT: CGFloat = 10
            let padB: CGFloat = 10
            let nameH: CGFloat = 20
            let n = Palette.label(L10n.t(key), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 22, y: y + padT, width: textW, height: nameH))
            let dStr = L10n.t(dkey)
            let dFont = Palette.body(11.5)
            let dH = max(16, min(32, Palette.textHeight(dStr, font: dFont, width: textW)))
            let d = Palette.wrap(dStr, font: dFont, color: Palette.mute, frame: NSRect(x: 22, y: y + padT + nameH, width: textW, height: dH), lines: 2)
            let rowH = padT + nameH + dH + padB
            let sw = ToggleSwitch(frame: NSRect(x: swX, y: y + round((rowH - 22) / 2), width: 36, height: 22))
            sw.on = on
            sw.onChange = { [weak self] v in self?.set(key, v) }
            switches[key] = sw
            let hit = ActionButton(frame: NSRect(x: 22, y: y, width: textW, height: rowH - 1))
            hit.drawsWash = false
            hit.identifier = NSUserInterfaceItemIdentifier(key)
            hit.target = self
            hit.action = #selector(prefHit(_:))
            let line = FlippedView(frame: NSRect(x: 22, y: y + rowH - 1, width: 396, height: 1))
            line.wantsLayer = true
            line.layer?.backgroundColor = Palette.line.cgColor
            c.addSubview(hit); c.addSubview(n); c.addSubview(d); c.addSubview(sw); c.addSubview(line)
            y += rowH
        }
        /// Toggle applies lists; Списки opens the split window.
        func splitRow() {
            let padT: CGFloat = 10
            let padB: CGFloat = 10
            let nameH: CGFloat = 20
            let goH: CGFloat = 22
            let n = Palette.label(L10n.t("split"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 22, y: y + padT, width: textW, height: nameH))
            let dStr = L10n.t("split_d")
            let dFont = Palette.body(11.5)
            let dH = max(16, min(32, Palette.textHeight(dStr, font: dFont, width: textW)))
            let d = Palette.wrap(dStr, font: dFont, color: Palette.mute, frame: NSRect(x: 22, y: y + padT + nameH, width: textW, height: dH), lines: 2)
            let go = GhostButton(frame: .zero)
            go.title = L10n.t("split_lists")
            go.font = Palette.body(12.5)
            go.tone = Palette.lamp
            go.alignment = .left
            go.target = NSApp.delegate
            go.action = #selector(AppDelegate.showSplit)
            let goW = max(40, Palette.textWidth(go.title, font: Palette.body(12.5)) + 8)
            let rowH = padT + nameH + dH + 6 + goH + padB
            let sw = ToggleSwitch(frame: NSRect(x: swX, y: y + padT + round((nameH - 22) / 2), width: 36, height: 22))
            sw.on = engine.store.split
            sw.onChange = { [weak self] v in self?.set("split", v) }
            switches["split"] = sw
            go.frame = NSRect(x: 22, y: y + padT + nameH + dH + 4, width: goW, height: goH)
            let line = FlippedView(frame: NSRect(x: 22, y: y + rowH - 1, width: 396, height: 1))
            line.wantsLayer = true
            line.layer?.backgroundColor = Palette.line.cgColor
            c.addSubview(n); c.addSubview(d); c.addSubview(go); c.addSubview(sw); c.addSubview(line)
            y += rowH
        }
        /// Bit/s vs B/s: two small pills stacked on the right.
        func rateRow() {
            let padT: CGFloat = 10
            let padB: CGFloat = 10
            let nameH: CGFloat = 20
            let bitW: CGFloat = 72
            let bitH: CGFloat = 22
            let bitGap: CGFloat = 4
            let stackH = bitH * 2 + bitGap
            let textW2: CGFloat = 396 - bitW - 12
            let n = Palette.label(L10n.t("rate_unit"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 22, y: y + padT, width: textW2, height: nameH))
            let dStr = L10n.t("rate_unit_d")
            let dFont = Palette.body(11.5)
            let dH = max(16, min(32, Palette.textHeight(dStr, font: dFont, width: textW2)))
            let d = Palette.wrap(dStr, font: dFont, color: Palette.mute, frame: NSRect(x: 22, y: y + padT + nameH, width: textW2, height: dH), lines: 2)
            let rowH = max(padT + nameH + dH + padB, padT + stackH + padB)
            let stackY = y + round((rowH - stackH) / 2)
            let rateSeg = SegControl(
                frame: NSRect(x: 22 + 396 - bitW, y: stackY, width: bitW, height: stackH),
                titles: [L10n.t("u_kbit"), L10n.t("u_kb")],
                vertical: true,
                compact: true
            )
            rateSeg.value = engine.store.rateBits ? 0 : 1
            rateSeg.onChange = { [weak self] v in
                self?.engine.store.rateBits = v == 0
                self?.engine.store.savePrefs()
                NotificationCenter.default.post(name: .loftState, object: self?.engine)
            }
            let line = FlippedView(frame: NSRect(x: 22, y: y + rowH - 1, width: 396, height: 1))
            line.wantsLayer = true
            line.layer?.backgroundColor = Palette.line.cgColor
            c.addSubview(n); c.addSubview(d); c.addSubview(rateSeg); c.addSubview(line)
            y += rowH
        }
        /// Always / window / never as a dropdown on the right.
        func dockRow() {
            let padT: CGFloat = 10
            let padB: CGFloat = 10
            let nameH: CGFloat = 20
            let titles = [L10n.t("dock_always"), L10n.t("dock_window"), L10n.t("dock_never")]
            let face = Palette.body(12.5)
            let selW = min(200, max(148, (titles.map { Palette.textWidth($0, font: face) }.max() ?? 148) + 36))
            let selH: CGFloat = 32
            let textW2: CGFloat = 396 - selW - 12
            let n = Palette.label(L10n.t("dock"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 22, y: y + padT, width: textW2, height: nameH))
            let dStr = L10n.t("dock_d")
            let dFont = Palette.body(11.5)
            let dH = max(16, min(32, Palette.textHeight(dStr, font: dFont, width: textW2)))
            let d = Palette.wrap(dStr, font: dFont, color: Palette.mute, frame: NSRect(x: 22, y: y + padT + nameH, width: textW2, height: dH), lines: 2)
            let rowH = max(padT + nameH + dH + padB, padT + selH + padB)
            let modes: [ProfileStore.DockMode] = [.always, .window, .never]
            let sel = LoftSelect(frame: NSRect(x: 22 + 396 - selW, y: y + round((rowH - selH) / 2), width: selW, height: selH))
            sel.titles = titles
            sel.value = modes.firstIndex(of: engine.store.dock) ?? 1
            sel.onChange = { [weak self] v in
                guard v >= 0, v < modes.count else { return }
                self?.engine.store.dock = modes[v]
                self?.engine.store.savePrefs()
                AppDelegate.shared?.syncDock()
            }
            let line = FlippedView(frame: NSRect(x: 22, y: y + rowH - 1, width: 396, height: 1))
            line.wantsLayer = true
            line.layer?.backgroundColor = Palette.line.cgColor
            c.addSubview(n); c.addSubview(d); c.addSubview(sel); c.addSubview(line)
            y += rowH
        }
        /// UI language as a dropdown on the right, same chrome as Dock.
        func langRow() {
            let padT: CGFloat = 10
            let padB: CGFloat = 10
            let nameH: CGFloat = 20
            let langs = L10n.Lang.allCases
            let titles = langs.map { $0.nativeName }
            let face = Palette.body(12.5)
            let selW = min(200, max(148, (titles.map { Palette.textWidth($0, font: face) }.max() ?? 148) + 36))
            let selH: CGFloat = 32
            let textW2: CGFloat = 396 - selW - 12
            let n = Palette.label(L10n.t("lang"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 22, y: y + padT, width: textW2, height: nameH))
            let dStr = L10n.t("lang_d")
            let dFont = Palette.body(11.5)
            let dH = max(16, min(32, Palette.textHeight(dStr, font: dFont, width: textW2)))
            let d = Palette.wrap(dStr, font: dFont, color: Palette.mute, frame: NSRect(x: 22, y: y + padT + nameH, width: textW2, height: dH), lines: 2)
            let rowH = max(padT + nameH + dH + padB, padT + selH + padB)
            let sel = LoftSelect(frame: NSRect(x: 22 + 396 - selW, y: y + round((rowH - selH) / 2), width: selW, height: selH))
            sel.titles = titles
            sel.value = langs.firstIndex(of: L10n.lang) ?? 0
            sel.onChange = { [weak self] v in
                guard langs.indices.contains(v) else { return }
                let next = langs[v]
                self?.engine.store.lang = next.rawValue
                L10n.lang = next
                self?.engine.store.savePrefs()
                self?.reload()
                NotificationCenter.default.post(name: .loftState, object: self?.engine)
            }
            let line = FlippedView(frame: NSRect(x: 22, y: y + rowH - 1, width: 396, height: 1))
            line.wantsLayer = true
            line.layer?.backgroundColor = Palette.line.cgColor
            c.addSubview(n); c.addSubview(d); c.addSubview(sel); c.addSubview(line)
            y += rowH
        }
        /// SOCKS5 port field on the client tab.
        func socksPortRow() {
            let padT: CGFloat = 10
            let padB: CGFloat = 10
            let nameH: CGFloat = 20
            let fieldW: CGFloat = 80
            let fieldH: CGFloat = 32
            let textW2: CGFloat = 396 - fieldW - 12
            let n = Palette.label(L10n.t("socks_port"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 22, y: y + padT, width: textW2, height: nameH))
            let dStr = L10n.t("socks_port_d")
            let dFont = Palette.body(11.5)
            let dH = max(16, min(32, Palette.textHeight(dStr, font: dFont, width: textW2)))
            let d = Palette.wrap(dStr, font: dFont, color: Palette.mute, frame: NSRect(x: 22, y: y + padT + nameH, width: textW2, height: dH), lines: 2)
            let rowH = padT + nameH + dH + padB
            let f = LoftField(frame: NSRect(x: 22 + 396 - fieldW, y: y + round((rowH - fieldH) / 2), width: fieldW, height: fieldH), placeholder: "1080")
            f.stringValue = "\(engine.store.socksPort)"
            f.field.alignment = .center
            f.field.target = self
            f.field.action = #selector(socksPortCommit)
            (f.field.cell as? NSTextFieldCell)?.sendsActionOnEndEditing = true
            socksPortF = f
            let line = FlippedView(frame: NSRect(x: 22, y: y + rowH - 1, width: 396, height: 1))
            line.wantsLayer = true
            line.layer?.backgroundColor = Palette.line.cgColor
            c.addSubview(n); c.addSubview(d); c.addSubview(f); c.addSubview(line)
            y += rowH
        }
        /// Echo API URL, full width under the caption.
        func extIPURLRow() {
            let padT: CGFloat = 10
            let padB: CGFloat = 10
            let nameH: CGFloat = 20
            let fieldH: CGFloat = 32
            let n = Palette.label(L10n.t("ext_ip_url"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 22, y: y + padT, width: 396, height: nameH))
            let dStr = L10n.t("ext_ip_url_d")
            let dFont = Palette.body(11.5)
            let dH = max(16, min(32, Palette.textHeight(dStr, font: dFont, width: 396)))
            let d = Palette.wrap(dStr, font: dFont, color: Palette.mute, frame: NSRect(x: 22, y: y + padT + nameH, width: 396, height: dH), lines: 2)
            let fieldY = y + padT + nameH + dH + 6
            let rowH = padT + nameH + dH + 6 + fieldH + padB
            let f = LoftField(frame: NSRect(x: 22, y: fieldY, width: 396, height: fieldH), placeholder: ProfileStore.defaultExtIPURL)
            f.stringValue = engine.store.extIPURL
            f.field.target = self
            f.field.action = #selector(extIPURLCommit)
            (f.field.cell as? NSTextFieldCell)?.sendsActionOnEndEditing = true
            extIPURLF = f
            let line = FlippedView(frame: NSRect(x: 22, y: y + rowH - 1, width: 396, height: 1))
            line.wantsLayer = true
            line.layer?.backgroundColor = Palette.line.cgColor
            c.addSubview(n); c.addSubview(d); c.addSubview(f); c.addSubview(line)
            y += rowH
        }
        /// Defaults, Journal, and About under every tab.
        func linksRow() {
            let ghostFace = Palette.body(12.5)
            /// Width of a ghost title, at least 40pt.
            func ghostW(_ s: String) -> CGFloat {
                max(40, Palette.textWidth(s, font: ghostFace) + 16)
            }
            let items: [(String, AnyObject?, Selector)] = [
                (L10n.t("defaults"), self, #selector(defaultsTap)),
                (L10n.t("journal"), NSApp.delegate as AnyObject?, #selector(AppDelegate.showJournal)),
                (L10n.t("about"), NSApp.delegate as AnyObject?, #selector(AppDelegate.showAbout)),
            ]
            let widths = items.map { ghostW($0.0) }
            let gap: CGFloat = 20
            let total = widths.reduce(0, +) + gap * CGFloat(items.count - 1)
            var x = 22 + (396 - total) / 2
            for i in items.indices {
                let b = GhostButton(frame: NSRect(x: x, y: y, width: widths[i], height: 22))
                b.title = items[i].0
                b.font = ghostFace
                b.tone = Palette.mute
                b.alignment = .center
                b.target = items[i].1
                b.action = items[i].2
                c.addSubview(b)
                x += widths[i] + gap
            }
            y += 22
        }
        switch tab {
        case 1:
            pref("default_route", "default_route_d", engine.store.defaultRoute)
            pref("failover", "failover_d", engine.store.failover)
            pref("hold", "hold_d", engine.store.hold)
            splitRow()
        case 2:
            pref("auto", "auto_d", engine.store.autoConnect)
            pref("reconnect", "reconnect_d", engine.store.reconnect)
            pref("multi", "multi_d", engine.store.multi)
            pref("socks_share", "socks_share_d", engine.store.sharedSocks)
            socksPortRow()
            pref("ext_ip", "ext_ip_d", engine.store.showExtIP)
            extIPURLRow()
        default:
            pref("login", "login_d", engine.store.login)
            dockRow()
            pref("simple", "simple_d", engine.store.simple)
            rateRow()
            langRow()
            pref("upd_launch", "upd_launch_d", engine.store.checkUpdates)
            pref("diag", "diag_d", engine.store.diag)
        }
        y += 10
        linksRow()
        loftResize(window!, bodyHeight: y + 20)
    }

    /// Flips the toggle for the pref whose identifier is on `sender`.
    @objc private func prefHit(_ sender: NSButton) {
        let key = sender.identifier?.rawValue ?? ""
        guard let sw = switches[key] else { return }
        sw.flip()
    }

    /// Writes `key` into the store and applies side effects (login, diag, default route).
    private func set(_ key: String, _ on: Bool) {
        switch key {
        case "simple": engine.store.simple = on
        case "multi": engine.setMulti(on)
        case "default_route":
            engine.store.defaultRoute = on
            engine.syncDefaultRoute()
        case "hold": engine.store.hold = on
        case "failover": engine.store.failover = on
        case "split":
            engine.store.split = on
            engine.syncSplitRoutes()
        case "diag":
            engine.store.diag = on
            LoftLog.shared.diag = on
        case "socks_share": engine.store.sharedSocks = on
        case "login":
            engine.store.login = on
            AppDelegate.shared?.setLogin(on)
        case "auto": engine.store.autoConnect = on
        case "reconnect":
            engine.store.reconnect = on
            if !on { engine.cancelReconnect() }
        case "ext_ip":
            engine.store.showExtIP = on
            engine.refreshExtIP()
        case "upd_launch": engine.store.checkUpdates = on
        default: break
        }
        engine.store.savePrefs()
        NotificationCenter.default.post(name: .loftState, object: engine)
    }

    /// Reads the SOCKS port field into prefs.
    @objc private func socksPortCommit() { commitSocksPort() }

    /// Reads the echo API URL field into prefs.
    @objc private func extIPURLCommit() { commitExtIPURL() }

    /// Clamps and stores the Settings SOCKS port.
    private func commitSocksPort() {
        guard let f = socksPortF else { return }
        let raw = f.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if let n = Int(raw), n > 0 {
            let v = ProfileStore.clampPort(n)
            if v != engine.store.socksPort {
                engine.store.socksPort = v
                engine.store.savePrefs()
                NotificationCenter.default.post(name: .loftState, object: engine)
            }
        }
        f.stringValue = "\(engine.store.socksPort)"
    }

    /// Stores a valid http(s) echo URL and re-probes live sessions.
    private func commitExtIPURL() {
        guard let f = extIPURLF else { return }
        let v = ProfileStore.normalizeExtIPURL(f.stringValue)
        f.stringValue = v
        if v != engine.store.extIPURL {
            engine.store.extIPURL = v
            engine.store.savePrefs()
            engine.refreshExtIP()
        }
    }



    /// Restores pre-loft routes.
    @objc private func resetTap() { engine.resetRoutes() }

    /// Restores factory prefs (not language, not profiles) and applies side effects.
    @objc private func defaultsTap() {
        commitSocksPort()
        commitExtIPURL()
        let loginWas = engine.store.login
        engine.store.restoreDefaults()
        if loginWas { AppDelegate.shared?.setLogin(false) }
        LoftLog.shared.diag = engine.store.diag
        if !engine.store.reconnect { engine.cancelReconnect() }
        engine.syncDefaultRoute()
        engine.syncSplitRoutes()
        engine.refreshExtIP()
        AppDelegate.shared?.syncDock()
        reload()
        NotificationCenter.default.post(name: .loftState, object: engine)
    }
}

/// Profile editor: VPN type loft, SSTP, or VLESS, then type-specific fields.
final class EditWindowController: NSWindowController {
    private let engine: SessionController
    private var profile: LoftProfile
    private var nameF: LoftField!
    private var keyF: LoftField?
    private var userF: LoftField?
    private var urlF: LoftField?
    private var pathF: LoftField?
    private var sniF: LoftField?
    private var socksF: LoftField?
    private var vpnSeg: SegControl!
    private var kindSeg: SegControl?
    private var socksSw: ToggleSwitch?
    private var tunSw: ToggleSwitch?
    private var muxSw: ToggleSwitch?
    private var tlsSw: ToggleSwitch?
    private var papSw: ToggleSwitch?
    private var dnsF: LoftField?
    private var dnsReplaceSw: ToggleSwitch?
    private var dnsSystemSw: ToggleSwitch?
    private var form: FlippedView!
    private var formScroll: NSScrollView!
    private var formTop: CGFloat = 0
    private let saveBtn = PillButton(frame: .zero)
    private let backBtn = GhostButton(frame: .zero)
    private let delBtn = GhostButton(frame: .zero)
    private var tabSeg: SegControl!
    private var editTab = 0
    /// loft Main: paste `loft://` (false) or field form (true). Same profile.
    private var loftManual = false
    private var loftHostF: LoftField?
    private var loftPortF: LoftField?
    /// VLESS Main: paste `vless://` (false) or field form (true). Same share.
    private var vlessManual = false
    private var vlessHostF: LoftField?
    private var vlessPortF: LoftField?
    private var vlessFpF: LoftField?
    private var vlessPbkF: LoftField?
    private var vlessSidF: LoftField?
    private var vlessPathF: LoftField?
    private var vlessHdrF: LoftField?
    private var vlessSecSeg: SegControl?
    private var vlessNetSeg: SegControl?
    private var vlessVisionSw: ToggleSwitch?

    /// Builds the editor around `profile`.
    init(engine: SessionController, profile: LoftProfile) {
        self.engine = engine
        self.profile = profile
        let u = profile.url.lowercased()
        vlessManual = profile.isVLESS && !u.isEmpty && !u.hasPrefix("vless://")
        let w = makeLoftWindow(size: NSSize(width: 440, height: 620))
        super.init(window: w)
        w.level = .normal
        build()
        w.center()
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Name, VPN type, then the form for loft or SSTP.
    private func build() {
        let c = loftBody(window!)
        c.subviews.forEach { $0.removeFromSuperview() }
        let qrTitle = L10n.t("qr")
        let qrW = min(88, Palette.textWidth(qrTitle, font: Palette.font(11.5)) + 22)
        // 14pt matches the Back/Delete footer so the title is not cramped under the lights.
        let headY: CGFloat = 14
        let headH: CGFloat = 28
        let title = DrawLab(frame: NSRect(x: 28, y: headY, width: 384 - qrW - 12, height: headH))
        title.text = L10n.t("edit_title")
        title.font = Palette.font(20)
        title.color = Palette.snow
        title.alignment = .left
        title.pillNudge = false
        c.addSubview(title)
        let qr = PillButton(frame: NSRect(x: 28 + 384 - qrW, y: headY, width: qrW, height: headH))
        qr.compact = true
        qr.title = qrTitle
        qr.target = self
        qr.action = #selector(qrTap)
        c.addSubview(qr)
        var y: CGFloat = headY + headH + 8
        let nl = Palette.label(L10n.t("name"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(nl)
        y += 18
        nameF = LoftField(frame: NSRect(x: 28, y: y, width: 384, height: 36), placeholder: "")
        nameF.stringValue = profile.name
        c.addSubview(nameF)
        y += 46
        let types = [L10n.t("vpn_loft"), L10n.t("vpn_sstp"), L10n.t("vpn_vless"), L10n.t("vpn_trojan")]
        let typeFace = Palette.body(12)
        let cell = max(48, (types.map { Palette.textWidth($0, font: typeFace) }.max() ?? 48) + 16)
        let segW = min(384, cell * CGFloat(types.count) + 4)
        let rowH: CGFloat = 28
        let tl = Palette.label(L10n.t("vpn_type"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 28, y: y + round((rowH - 18) / 2), width: max(80, 384 - segW - 12), height: 18))
        c.addSubview(tl)
        vpnSeg = SegControl(frame: NSRect(x: 28 + 384 - segW, y: y, width: segW, height: rowH), titles: types, capsule: true)
        vpnSeg.value = vpnIndex(profile)
        vpnSeg.onChange = { [weak self] v in self?.switchVPN(v) }
        c.addSubview(vpnSeg)
        y += rowH + 14
        tabSeg = SegControl(frame: NSRect(x: 28, y: y, width: 384, height: 32), titles: [L10n.t("edit_main"), L10n.t("edit_adv")])
        tabSeg.value = editTab
        tabSeg.onChange = { [weak self] v in
            self?.harvest()
            self?.editTab = v
            self?.paintForm()
        }
        c.addSubview(tabSeg)
        y += 44
        formTop = y
        formScroll = loftOverlayScroll(frame: NSRect(x: 0, y: y, width: 440, height: 200))
        form = FlippedView(frame: NSRect(x: 0, y: 0, width: 440, height: 200))
        formScroll.documentView = form
        c.addSubview(formScroll)
        saveBtn.lampOn = true
        saveBtn.target = self
        saveBtn.action = #selector(saveTap)
        backBtn.font = Palette.body(12.5)
        backBtn.tone = Palette.mute
        backBtn.alignment = .left
        backBtn.target = self
        backBtn.action = #selector(backTap)
        delBtn.font = Palette.body(12.5)
        delBtn.tone = Palette.copper
        delBtn.alignment = .right
        delBtn.target = self
        delBtn.action = #selector(delTap)
        c.addSubview(saveBtn)
        c.addSubview(backBtn)
        c.addSubview(delBtn)
        paintForm()
    }

    /// `--shot` flips the editor tab without a click.
    func shotTab(_ i: Int) {
        harvest()
        editTab = i
        tabSeg.value = i
        paintForm()
    }

    /// `--shot` shows the VLESS field form.
    func shotManual() {
        vlessManual = true
        paintForm()
    }

    /// `--shot` shows the loft field form.
    func shotLoftManual() {
        loftManual = true
        paintForm()
    }

    /// Reloads the type-specific block and resizes the window.
    private func paintForm() {
        form.subviews.forEach { $0.removeFromSuperview() }
        userF = nil; pathF = nil; kindSeg = nil; muxSw = nil; sniF = nil; tlsSw = nil; papSw = nil
        dnsF = nil; dnsReplaceSw = nil; dnsSystemSw = nil
        keyF = nil; urlF = nil; socksF = nil; socksSw = nil; tunSw = nil
        loftHostF = nil; loftPortF = nil
        vlessHostF = nil; vlessPortF = nil; vlessFpF = nil; vlessPbkF = nil; vlessSidF = nil
        vlessPathF = nil; vlessHdrF = nil; vlessSecSeg = nil; vlessNetSeg = nil; vlessVisionSw = nil
        var y: CGFloat = 0
        /// Mute caption inside `form`.
        func lab(_ k: String) {
            let l = Palette.label(L10n.t(k), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
            form.addSubview(l)
            y += 18
        }
        /// Field inside `form`. 12pt under the field, 2pt under the caption via `lab`.
        func field(_ ph: String, _ val: String, secure: Bool = false) -> LoftField {
            let f = LoftField(frame: NSRect(x: 28, y: y, width: 384, height: 36), secure: secure, placeholder: ph)
            f.stringValue = val
            form.addSubview(f)
            y += 48
            return f
        }
        func capsuleWidth(_ titles: [String], icon: CGFloat = 0) -> CGFloat {
            let face = Palette.font(11.5)
            let text = titles.map { Palette.textWidth($0, font: face) }.max() ?? 48
            let pad: CGFloat = icon > 0 ? icon + 5 + 28 : 28
            return min(384, (text + pad) * CGFloat(max(1, titles.count)))
        }
        /// Quiet Link / Manual capsule. `set` runs after harvest, then the form rebuilds.
        func linkManual(_ on: Bool, _ set: @escaping (Bool) -> Void) {
            let modes = [L10n.t("vless_link"), L10n.t("vless_manual")]
            let modeSeg = SegControl(
                frame: NSRect(x: 28, y: y, width: capsuleWidth(modes, icon: 12), height: 28),
                titles: modes,
                capsule: true,
                icons: [.link, .edit]
            )
            modeSeg.value = on ? 1 : 0
            modeSeg.onChange = { [weak self] v in
                self?.harvest()
                set(v == 1)
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.24) { [weak self] in
                    self?.paintForm()
                }
            }
            form.addSubview(modeSeg)
            y += 40
        }
        let sstp = profile.isSSTP
        let vless = profile.isVLESS
        let trojan = profile.isTrojan
        let alt = sstp || vless || trojan
        let capW: CGFloat = 320
        func inbound(_ key: String, _ dkey: String, _ on: Bool) -> ToggleSwitch {
            let n = Palette.label(L10n.t(key), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 28, y: y, width: capW, height: 18))
            let dStr = L10n.t(dkey)
            let dFont = Palette.body(11.5)
            let dH = max(28, Palette.textHeight(dStr, font: dFont, width: capW))
            let d = Palette.wrap(dStr, font: dFont, color: Palette.mute, frame: NSRect(x: 28, y: y + 20, width: capW, height: dH), lines: 3)
            let sw = ToggleSwitch(frame: NSRect(x: 376, y: y + 2, width: 36, height: 22))
            sw.on = on
            form.addSubview(n); form.addSubview(d); form.addSubview(sw)
            y += 20 + dH + 14
            return sw
        }
        if editTab == 0 {
            if sstp {
                lab("sstp_user")
                userF = field("", profile.user)
                lab("sstp_pass")
                keyF = field("", profile.token, secure: true)
                lab("sstp_url")
                urlF = field("https://", profile.url)
                lab("sstp_sni")
                sniF = field("", profile.sni)
            } else if vless {
                linkManual(vlessManual) { [weak self] on in self?.vlessManual = on }
                if vlessManual {
                    let vs = ShareURI.vlessShare(profile)
                    lab("vless_uuid")
                    keyF = field("xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", vs.uuid, secure: true)
                    let gap: CGFloat = 10
                    let portW: CGFloat = 100
                    let hostW = 384 - portW - gap
                    let hl = Palette.label(L10n.t("vless_host"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: hostW, height: 16))
                    let pl = Palette.label(L10n.t("vless_port"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28 + hostW + gap, y: y, width: portW, height: 16))
                    form.addSubview(hl); form.addSubview(pl)
                    y += 18
                    let hf = LoftField(frame: NSRect(x: 28, y: y, width: hostW, height: 36), placeholder: "de1.example.com")
                    hf.stringValue = vs.host
                    let pf = LoftField(frame: NSRect(x: 28 + hostW + gap, y: y, width: portW, height: 36), placeholder: "443")
                    pf.stringValue = vs.port > 0 ? String(vs.port) : "443"
                    form.addSubview(hf); form.addSubview(pf)
                    vlessHostF = hf; vlessPortF = pf
                    y += 48
                    let col = (384 - gap) / 2
                    let secTitles = [L10n.t("vless_tls"), L10n.t("vless_reality")]
                    let netTitles = [L10n.t("vless_tcp"), L10n.t("vless_xhttp")]
                    let sl = Palette.label(L10n.t("vless_sec"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: col, height: 16))
                    let nl = Palette.label(L10n.t("vless_net"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28 + col + gap, y: y, width: col, height: 16))
                    form.addSubview(sl); form.addSubview(nl)
                    y += 18
                    let secW = min(col, capsuleWidth(secTitles))
                    let netW = min(col, capsuleWidth(netTitles))
                    vlessSecSeg = SegControl(frame: NSRect(x: 28, y: y, width: secW, height: 28), titles: secTitles, capsule: true)
                    vlessSecSeg?.value = vs.reality ? 1 : 0
                    vlessSecSeg?.onChange = { [weak self] _ in
                        self?.harvest()
                        self?.paintForm()
                    }
                    vlessNetSeg = SegControl(frame: NSRect(x: 28 + col + gap, y: y, width: netW, height: 28), titles: netTitles, capsule: true)
                    vlessNetSeg?.value = vs.xhttp ? 1 : 0
                    vlessNetSeg?.onChange = { [weak self] _ in
                        self?.harvest()
                        self?.paintForm()
                    }
                    if let s = vlessSecSeg { form.addSubview(s) }
                    if let n = vlessNetSeg { form.addSubview(n) }
                    y += 40
                    lab("sstp_sni")
                    sniF = field("www.example.com", vs.sni)
                    lab("vless_fp")
                    vlessFpF = field("chrome", vs.fingerprint)
                    if vs.reality {
                        lab("vless_pbk")
                        vlessPbkF = field("", vs.publicKey, secure: true)
                        lab("vless_sid")
                        vlessSidF = field("", vs.shortID)
                    }
                    if !vs.xhttp {
                        vlessVisionSw = inbound("vless_vision", "vless_vision_d", vs.vision)
                    }
                    if vs.xhttp {
                        lab("vless_path")
                        vlessPathF = field("/path", vs.path)
                        lab("vless_xhost")
                        vlessHdrF = field("", vs.headerHost)
                    }
                } else {
                    lab("vless_url")
                    urlF = field("vless://uuid@host:443?security=reality&pbk=…", profile.url, secure: true)
                }
            } else if trojan {
                lab("trojan_pass")
                keyF = field("", profile.token, secure: true)
                lab("trojan_url")
                urlF = field("trojan://", profile.url)
                lab("sstp_sni")
                sniF = field("", profile.sni)
            } else {
                linkManual(loftManual) { [weak self] on in self?.loftManual = on }
                if loftManual {
                    let ep = ShareURI.loftEndpoint(profile)
                    lab("key")
                    keyF = field("lt_", profile.token, secure: true)
                    let gap: CGFloat = 10
                    let portW: CGFloat = 100
                    let hostW = 384 - portW - gap
                    let hl = Palette.label(L10n.t("vless_host"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: hostW, height: 16))
                    let pl = Palette.label(L10n.t("vless_port"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28 + hostW + gap, y: y, width: portW, height: 16))
                    form.addSubview(hl); form.addSubview(pl)
                    y += 18
                    let hf = LoftField(frame: NSRect(x: 28, y: y, width: hostW, height: 36), placeholder: "cdn.example.com")
                    hf.stringValue = ep.host
                    let pf = LoftField(frame: NSRect(x: 28 + hostW + gap, y: y, width: portW, height: 36), placeholder: "443")
                    pf.stringValue = ep.port > 0 ? String(ep.port) : "443"
                    form.addSubview(hf); form.addSubview(pf)
                    loftHostF = hf; loftPortF = pf
                    y += 48
                    lab("front")
                    let fronts = LoftProfile.frontTitles
                    kindSeg = SegControl(frame: NSRect(x: 28, y: y, width: capsuleWidth(fronts), height: 28), titles: fronts, capsule: true)
                    kindSeg?.value = LoftProfile.frontIndex(profile.kind)
                    if let s = kindSeg { form.addSubview(s) }
                    y += 40
                    lab("vless_path")
                    pathF = field("/static/js", ep.path)
                } else {
                    lab("loft_uri")
                    keyF = field("loft://lt_@host/path", ShareURI.emit(profile), secure: true)
                }
            }
            if sstp {
                tlsSw = inbound("sstp_tls", "sstp_tls_d", profile.tlsCheck)
                papSw = inbound("sstp_pap", "sstp_pap_d", profile.papCHAP)
            } else if vless || trojan {
                tlsSw = inbound("sstp_tls", "sstp_tls_d", profile.tlsCheck)
            }
        } else {
            let defPort = engine.store.socksPort
            let socksL = Palette.label(L10n.t("socks"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 28, y: y, width: capW, height: 18))
            let socksHint = L10n.t("socks_d")
            let socksHintFont = Palette.body(11.5)
            let socksHintH = max(16, Palette.textHeight(socksHint, font: socksHintFont, width: capW))
            let socksD = Palette.wrap(socksHint, font: socksHintFont, color: Palette.mute, frame: NSRect(x: 28, y: y + 20, width: capW, height: socksHintH), lines: 2)
            let socksToggle = ToggleSwitch(frame: NSRect(x: 376, y: y + 2, width: 36, height: 22))
            socksToggle.on = profile.socksOn
            socksToggle.onChange = { [weak self] on in self?.armSocks(on) }
            form.addSubview(socksL)
            form.addSubview(socksD)
            form.addSubview(socksToggle)
            socksSw = socksToggle
            y += 20 + socksHintH + 8
            socksF = field("127.0.0.1:\(defPort)", profile.socksOn ? profile.dedicatedSocksDisplay(defaultPort: defPort) : "")
            armSocks(profile.socksOn)
            tunSw = inbound(alt ? "sstp_tun" : "tun", alt ? "sstp_tun_d" : "tun_d", profile.tun)
            if !alt {
                muxSw = inbound("mux", "mux_d", profile.mux)
            }
            dnsSystemSw = inbound("dns_system", "dns_system_d", profile.dnsSystem)
            let dnsL = Palette.label(L10n.t("sstp_dns"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 28, y: y, width: 384, height: 18))
            let dnsDKey = sstp ? "sstp_dns_ph" : (alt ? "dns_ph_vless" : "dns_ph_loft")
            let dnsDStr = L10n.t(dnsDKey)
            let dnsDFont = Palette.body(11.5)
            let dnsDH = max(16, Palette.textHeight(dnsDStr, font: dnsDFont, width: 384))
            let dnsD = Palette.wrap(dnsDStr, font: dnsDFont, color: Palette.mute, frame: NSRect(x: 28, y: y + 20, width: 384, height: dnsDH), lines: 2)
            form.addSubview(dnsL)
            form.addSubview(dnsD)
            y += 20 + dnsDH + 8
            dnsF = field("1.1.1.1, 8.8.8.8", profile.dns)
            if sstp {
                dnsReplaceSw = inbound("sstp_dns_replace", "sstp_dns_replace_d", profile.dnsReplace)
            }
        }
        finishForm(y)
    }

    /// Sizes the field scroll to the screen and pins Save / Back under it.
    private func finishForm(_ fieldsH: CGFloat) {
        let h = max(1, fieldsH)
        let kept = formScroll.contentView.bounds.origin
        form.frame = NSRect(x: 0, y: 0, width: 440, height: h)
        let footer: CGFloat = 12 + 42 + 8 + 22 + 14
        let vis = (window?.screen ?? NSScreen.main)?.visibleFrame.height ?? 800
        let maxBody = max(360, vis - 28 - 20)
        let want = formTop + h + footer
        let scrollH: CGFloat
        let bodyH: CGFloat
        if want <= maxBody {
            scrollH = h
            bodyH = want
        } else {
            scrollH = max(120, maxBody - formTop - footer)
            bodyH = formTop + scrollH + footer
        }
        formScroll.frame = NSRect(x: 0, y: formTop, width: 440, height: scrollH)
        saveBtn.title = L10n.t("save")
        saveBtn.frame = NSRect(x: 28, y: formScroll.frame.maxY + 12, width: 384, height: 42)
        backBtn.title = L10n.t("back")
        backBtn.frame = NSRect(x: 28, y: saveBtn.frame.maxY + 8, width: 120, height: 22)
        delBtn.title = L10n.t("delete")
        delBtn.frame = NSRect(x: 292, y: backBtn.frame.minY, width: 120, height: 22)
        delBtn.isHidden = profile.id.isEmpty
        loftResize(window!, bodyHeight: bodyH)
        let maxY = max(0, h - formScroll.contentView.bounds.height)
        formScroll.contentView.scroll(to: NSPoint(x: 0, y: min(max(0, kept.y), maxY)))
        formScroll.reflectScrolledClipView(formScroll.contentView)
    }

    /// Harvests fields, then shows loft, SSTP, or VLESS form.
    private func switchVPN(_ i: Int) {
        harvest()
        if i == 1 {
            profile.kind = "sstp"
            profile.mux = false
            profile.tun = true
        } else if i == 2 {
            profile.kind = "vless"
            profile.mux = false
            profile.tun = true
            vlessManual = false
        } else if i == 3 {
            profile.kind = "trojan"
            profile.mux = false
            profile.tun = true
        } else if profile.isSSTP || profile.isVLESS || profile.isTrojan {
            profile.kind = "get"
            loftManual = false
        }
        paintForm()
    }

    /// Copies visible fields into `profile`.
    private func harvest() {
        profile.name = nameF.stringValue
        if profile.isVLESS {
            harvestVLESS()
        } else if profile.isSSTP || profile.isTrojan {
            if let k = keyF {
                let tok = k.stringValue
                if !tok.isEmpty { profile.token = tok }
            }
            if let u = urlF { profile.url = u.stringValue }
        } else if loftManual {
            harvestLoft()
        } else if let k = keyF {
            let raw = k.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
            if let q = ShareURI.loft(raw, fileName: profile.id.isEmpty ? "edit" : profile.id) {
                profile.token = q.token
                profile.url = q.url
                profile.kind = q.kind
                if !q.path.isEmpty { profile.path = q.path }
                profile.cdn = q.cdn
                profile.maskPub = q.maskPub
            } else if !raw.isEmpty {
                profile.url = raw
            }
        }
        if let u = userF { profile.user = u.stringValue }
        if let s = sniF { profile.sni = s.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) }
        if let t = tlsSw { profile.tlsCheck = t.on }
        if let p = papSw { profile.papCHAP = p.on }
        if let d = dnsF { profile.dns = LoftProfile.normalizeDNS(d.stringValue) }
        if let r = dnsReplaceSw { profile.dnsReplace = r.on }
        if let s = dnsSystemSw { profile.dnsSystem = s.on }
        if let p = pathF, !loftManual { profile.path = p.stringValue }
        if let sw = socksSw {
            if sw.on {
                profile.socks = LoftProfile.normalizeSocks(socksF?.stringValue ?? "", defaultPort: engine.store.socksPort)
            } else {
                profile.socks = ""
            }
        }
        if let t = tunSw { profile.tun = t.on }
        if let m = muxSw { profile.mux = m.on }
        if let k = kindSeg, !loftManual, !profile.isSSTP, !profile.isVLESS, !profile.isTrojan {
            profile.kind = LoftProfile.frontKind(k.value)
        }
        if profile.isTrojan, profile.url.lowercased().hasPrefix("trojan://") {
            if let u = URL(string: profile.url), let pass = u.user, profile.token.isEmpty {
                profile.token = pass
            }
            if profile.sni.isEmpty, let host = URL(string: profile.url)?.host {
                profile.sni = host
            }
        }
    }

    /// Writes loft Main fields into token, url, kind, and path.
    private func harvestLoft() {
        let token = (keyF?.stringValue ?? profile.token).trimmingCharacters(in: .whitespacesAndNewlines)
        let host = (loftHostF?.stringValue ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let portS = (loftPortF?.stringValue ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let port = Int(portS) ?? 0
        let path = pathF?.stringValue ?? profile.path
        let kind = kindSeg.map { LoftProfile.frontKind($0.value) } ?? profile.kind
        ShareURI.applyLoftManual(&profile, token: token, host: host, port: port, path: path, kind: kind)
    }

    /// Writes VLESS Main fields into `profile.url` as a DuckSoft share.
    private func harvestVLESS() {
        if vlessManual, vlessHostF != nil || keyF != nil {
            var v = ShareURI.vlessShare(profile)
            if let k = keyF { v.uuid = k.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) }
            if let h = vlessHostF { v.host = h.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) }
            if let p = vlessPortF {
                let n = Int(p.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)) ?? v.port
                v.port = (n > 0 && n < 65536) ? n : 443
            }
            if let s = vlessSecSeg { v.security = s.value == 1 ? "reality" : "tls" }
            if let n = vlessNetSeg { v.network = n.value == 1 ? "xhttp" : "tcp" }
            if let s = sniF { v.sni = s.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) }
            if let f = vlessFpF { v.fingerprint = f.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) }
            if let k = vlessPbkF { v.publicKey = k.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) }
            if let d = vlessSidF { v.shortID = d.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) }
            if let w = vlessVisionSw { v.vision = w.on }
            if let p = vlessPathF { v.path = p.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) }
            if let h = vlessHdrF { v.headerHost = h.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) }
            v.name = nameF.stringValue
            if let t = tlsSw { v.insecure = !t.on }
            profile.token = v.uuid
            profile.sni = v.sni
            profile.url = ShareURI.emitVLESS(v)
            return
        }
        if let u = urlF { profile.url = u.stringValue }
        if let parsed = ShareURI.parseVLESS(profile.url) {
            profile.token = parsed.uuid
            if profile.sni.isEmpty { profile.sni = parsed.sni }
            if parsed.insecure, tlsSw == nil { profile.tlsCheck = false }
        }
    }

    /// Dims and locks the SOCKS field when SOCKS is off.
    private func armSocks(_ on: Bool) {
        guard let f = socksF else { return }
        f.alphaValue = on ? 1 : 0.4
        f.field.isEditable = on
        f.field.isSelectable = on
    }

    /// Writes the form into the store; forces TUN if both inbounds are off.
    @objc private func saveTap() {
        harvest()
        var p = profile
        let lowURL = p.url.lowercased()
        if lowURL.hasPrefix("loft://"), let q = ShareURI.loft(p.url, fileName: p.id) {
            if p.name.isEmpty { p.name = q.name }
            p.token = q.token
            p.url = q.url
            p.kind = q.kind
            p.path = q.path
            p.cdn = q.cdn
            p.maskPub = q.maskPub
        } else if lowURL.hasPrefix("vless://"), let q = ShareURI.vless(p.url, fileName: p.id) {
            if p.name.isEmpty { p.name = q.name }
            p.token = q.token
            p.url = q.url
            p.kind = "vless"
            if p.sni.isEmpty { p.sni = q.sni }
            p.mux = false
        } else if lowURL.hasPrefix("trojan://"), let q = ShareURI.trojan(p.url, fileName: p.id) {
            if p.name.isEmpty { p.name = q.name }
            p.token = q.token
            p.url = q.url
            p.kind = "trojan"
            if p.sni.isEmpty { p.sni = q.sni }
            p.mux = false
        }
        if p.isSSTP {
            p.tun = true
            p.mux = false
        }
        if p.isVLESS || p.isTrojan {
            p.mux = false
        }
        if !p.tun && !p.socksOn { p.tun = true }
        if p.id.isEmpty {
            p.id = engine.store.slug(p.name)
            if p.id.isEmpty { p.id = UUID().uuidString }
        }
        if p.name.isEmpty { p.name = Palette.hostOf(p.url) }
        engine.applyEdited(p)
        engine.store.ensureActive()
        if engine.store.activeID.isEmpty { engine.store.select(p.id) }
        window?.close()
    }

    /// Removes this profile from the store and closes.
    @objc private func delTap() {
        engine.store.remove(profile.id)
        NotificationCenter.default.post(name: .loftState, object: engine)
        window?.close()
    }

    /// Closes without saving.
    @objc private func backTap() { window?.close() }

    /// Fills the form from a QR image of a loft / VLESS / SSTP share.
    @objc private func qrTap() {
        harvest()
        let p = NSOpenPanel()
        p.allowedFileTypes = QRScan.imageExt.sorted()
        p.begin { [weak self] r in
            guard r == NSApplication.ModalResponse.OK, let u = p.url, let self = self else { return }
            DispatchQueue.global(qos: .userInitiated).async {
                let payloads = QRScan.payloads(from: u)
                DispatchQueue.main.async {
                    let hits = self.engine.store.parseSharePayloads(payloads, fileName: u.deletingPathExtension().lastPathComponent)
                    guard let q = hits.first else {
                        loftNotice(payloads.isEmpty ? "qr_none" : "qr_fail")
                        return
                    }
                    self.applyShare(q)
                }
            }
        }
    }

    /// Copies share fields into the editor and rebuilds the type-specific form.
    private func applyShare(_ q: LoftProfile) {
        profile.name = q.name
        profile.token = q.token
        profile.user = q.user
        profile.url = q.url
        profile.kind = q.kind
        profile.path = q.path
        profile.cdn = q.cdn
        profile.sni = q.sni
        profile.tlsCheck = q.tlsCheck
        profile.papCHAP = q.papCHAP
        profile.dns = q.dns
        profile.dnsReplace = q.dnsReplace
        profile.dnsSystem = q.dnsSystem
        if q.isSSTP || q.isVLESS || q.isTrojan { profile.mux = false }
        else { profile.mux = q.mux }
        vlessManual = false
        nameF.stringValue = profile.name
        vpnSeg.value = vpnIndex(profile)
        paintForm()
    }

    /// Capsule index: loft / SSTP / VLESS / Trojan.
    private func vpnIndex(_ p: LoftProfile) -> Int {
        if p.isSSTP { return 1 }
        if p.isVLESS { return 2 }
        if p.isTrojan { return 3 }
        return 0
    }
}

/// Two-step onboard: access key, then URL/kind/path, then connect.
final class OnboardWindowController: NSWindowController {
    private let engine: SessionController
    private var step = 1
    private var token = ""
    private var url = "https://"
    private var path = "/static/v3/build"
    private var kind = "get"
    private var field: LoftField!
    private var pathF: LoftField?
    private var seg: SegControl?

    /// Builds the onboard window around `engine` and paints step 1.
    init(engine: SessionController) {
        self.engine = engine
        let w = makeLoftWindow(size: NSSize(width: 440, height: 520))
        super.init(window: w)
        w.level = .normal
        paint()
        w.center()
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Rebuilds step 1 (key) or step 2 (URL).
    private func paint() {
        let c = loftBody(window!)
        c.subviews.forEach { $0.removeFromSuperview() }
        let mark = MarkView(frame: NSRect(x: 188, y: 16, width: 64, height: 64))
        mark.state = .on
        c.addSubview(mark)
        let stepL = Palette.label(step == 1 ? L10n.t("key_step") : L10n.t("url_step"), font: Palette.font(11), color: Palette.mute, frame: NSRect(x: 36, y: 88, width: 368, height: 16))
        stepL.alignment = .center
        let title = Palette.label(step == 1 ? L10n.t("key_title") : L10n.t("url_title"), font: Palette.font(22), color: Palette.snow, frame: NSRect(x: 36, y: 108, width: 368, height: 28))
        title.alignment = .center
        let body = NSTextField(wrappingLabelWithString: step == 1 ? L10n.t("key_body") : L10n.t("url_body"))
        body.font = Palette.body(13.5)
        body.textColor = Palette.mute
        body.alignment = .center
        body.frame = NSRect(x: 36, y: 142, width: 368, height: 36)
        c.addSubview(stepL); c.addSubview(title); c.addSubview(body)

        var y: CGFloat = 190
        if step == 1 {
            field = LoftField(frame: NSRect(x: 36, y: y, width: 368, height: 40), placeholder: "lt_")
            field.stringValue = token
            c.addSubview(field)
            y += 48
            let hint = NSTextField(wrappingLabelWithString: L10n.t("key_hint"))
            hint.font = Palette.body(12)
            hint.textColor = Palette.mute
            hint.frame = NSRect(x: 36, y: y, width: 368, height: 32)
            c.addSubview(hint)
            y += 48
            let go = PillButton(frame: NSRect(x: 36, y: y, width: 368, height: 42))
            go.title = L10n.t("continue")
            go.lampOn = true
            go.target = self
            go.action = #selector(next)
            c.addSubview(go)
            y += 50
            let alt = GhostButton(frame: NSRect(x: 36, y: y, width: 368, height: 22))
            alt.title = L10n.t("key_orfile")
            alt.font = Palette.body(12.5)
            alt.tone = Palette.mute
            alt.alignment = .center
            alt.target = self
            alt.action = #selector(imp)
            c.addSubview(alt)
        } else {
            let ul = Palette.label(L10n.t("url"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 36, y: y, width: 368, height: 16))
            c.addSubview(ul)
            y += 18
            field = LoftField(frame: NSRect(x: 36, y: y, width: 368, height: 40), placeholder: "https://")
            field.stringValue = url
            c.addSubview(field)
            y += 48
            let tl = Palette.label(L10n.t("url_type"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 36, y: y, width: 368, height: 16))
            c.addSubview(tl)
            y += 18
            let s = SegControl(frame: NSRect(x: 36, y: y, width: 368, height: 36), titles: LoftProfile.frontTitles, compact: true)
            s.value = LoftProfile.frontIndex(kind)
            s.onChange = { [weak self] v in self?.kind = LoftProfile.frontKind(v) }
            seg = s
            c.addSubview(s)
            y += 44
            let pl = Palette.label(L10n.t("path"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 36, y: y, width: 368, height: 16))
            c.addSubview(pl)
            y += 18
            let pf = LoftField(frame: NSRect(x: 36, y: y, width: 368, height: 40), placeholder: "/static/v3/build")
            pf.stringValue = path
            pathF = pf
            c.addSubview(pf)
            y += 48
            let hint = NSTextField(wrappingLabelWithString: L10n.t("url_hint"))
            hint.font = Palette.body(12)
            hint.textColor = Palette.mute
            hint.frame = NSRect(x: 36, y: y, width: 368, height: 36)
            c.addSubview(hint)
            y += 44
            let go = PillButton(frame: NSRect(x: 36, y: y, width: 368, height: 42))
            go.title = L10n.t("url_go")
            go.lampOn = true
            go.target = self
            go.action = #selector(finish)
            c.addSubview(go)
            y += 48
            let back = GhostButton(frame: NSRect(x: 36, y: y, width: 368, height: 22))
            back.title = L10n.t("back")
            back.font = Palette.body(12.5)
            back.tone = Palette.mute
            back.alignment = .center
            back.target = self
            back.action = #selector(prev)
            c.addSubview(back)
        }
    }

    /// Stores the key and advances to the URL step.
    @objc private func next() {
        token = field.stringValue.trimmingCharacters(in: .whitespaces)
        if token.isEmpty { return }
        step = 2
        paint()
    }

    /// Keeps URL/path and returns to the key step.
    @objc private func prev() {
        url = field.stringValue
        path = pathF?.stringValue ?? path
        step = 1
        paint()
    }

    /// Saves a new profile, selects it, and connects.
    @objc private func finish() {
        url = field.stringValue.trimmingCharacters(in: .whitespaces)
        path = pathF?.stringValue ?? path
        if url.isEmpty || url == "https://" { return }
        var p = LoftProfile(id: "", name: Palette.hostOf(url), token: token, url: url, kind: kind, path: path.isEmpty ? "/static/js" : path, socks: "auto", tun: true, mux: true, instanceID: "", file: "")
        p.id = engine.store.slug(p.name)
        if p.id.isEmpty { p.id = UUID().uuidString }
        engine.store.saveEdited(p)
        engine.store.select(p.id)
        NotificationCenter.default.post(name: .loftState, object: engine)
        window?.close()
        engine.connect()
    }

    /// Imports a `.loft` / YAML / `.vpn` / QR image instead of typing a key.
    @objc private func imp() {
        let p = shareOpenPanel()
        p.begin { [weak self] r in
            guard r == NSApplication.ModalResponse.OK, let u = p.url, let self = self else { return }
            if QRScan.isImage(u) {
                DispatchQueue.global(qos: .userInitiated).async {
                    let payloads = QRScan.payloads(from: u)
                    DispatchQueue.main.async {
                        if let prof = self.engine.store.importPayloads(payloads, fileName: u.deletingPathExtension().lastPathComponent) {
                            self.engine.store.select(prof.id)
                            NotificationCenter.default.post(name: .loftState, object: self.engine)
                            self.window?.close()
                            return
                        }
                        loftNotice(payloads.isEmpty ? "qr_none" : "qr_fail")
                    }
                }
                return
            }
            if let prof = self.engine.store.importFile(u) {
                self.engine.store.select(prof.id)
                NotificationCenter.default.post(name: .loftState, object: self.engine)
                self.window?.close()
                return
            }
            loftNotice("imp_fail")
        }
    }
}

/// Open panel for `.loft`, YAML, `.vpn`, text share URIs, and QR images.
private func shareOpenPanel() -> NSOpenPanel {
    let p = NSOpenPanel()
    p.allowedFileTypes = ["loft", "yaml", "yml", "vpn", "txt"] + QRScan.imageExt.sorted()
    p.allowsMultipleSelection = true
    return p
}
