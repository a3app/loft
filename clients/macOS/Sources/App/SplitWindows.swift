// Split-routing windows: zones, Keenetic import/export, repository, list catalog, add a network.

import AppKit

/// Where the list catalog returns after a pick.
enum SplitListBack {
    case repo(String)
    case zoneEdit(String)
    case zoneAdd
}

/// Draft for the add-zone sheet (survives a trip to the list catalog).
struct SplitZoneDraft {
    var name = ""
    var def = true
    var repo = ""
    var auto = true
}

/// One split-list row. Reused across reloads.
private final class SplitRow: FlippedView {
    let plate = LoftPlate(frame: .zero)
    let hit = ActionButton(frame: .zero)
    let name = PassLab(labelWithString: "")
    let cidrs = PassLab(labelWithString: "")
    let meta = PassLab(labelWithString: "")
    let src = PassLab(labelWithString: "")

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        plate.radius = 8
        hit.drawsWash = false
        name.font = Palette.font(13.5)
        name.textColor = Palette.snow
        cidrs.font = Palette.mono(11.5)
        cidrs.textColor = Palette.mute
        meta.font = Palette.body(11)
        meta.textColor = Palette.mute
        meta.alignment = .right
        src.font = Palette.font(10)
        src.textColor = Palette.copper
        src.alignment = .right
        for t in [name, cidrs, meta, src] {
            t.backgroundColor = .clear
            t.isBezeled = false
            t.drawsBackground = false
            t.isEditable = false
            t.isSelectable = false
            Palette.fitLine(t)
        }
        for v in [plate, hit, name, cidrs, meta, src] as [NSView] { addSubview(v) }
    }

    required init?(coder: NSCoder) { fatalError() }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        plate.frame = bounds
        hit.frame = bounds
    }
}

/// Lists window: zone tabs, import, export, repository.
final class SplitWindowController: NSWindowController {
    private let engine: SessionController
    private var zoneId = "global"
    private var query = ""
    private let titleLab = DrawLab(frame: .zero)
    private let splitSw = ToggleSwitch(frame: .zero)
    private let sub = NSTextField(wrappingLabelWithString: "")
    private let native = NSTextField(labelWithString: "")
    private let zoneLab = NSTextField(labelWithString: "")
    private let zonesBtn = GhostButton(frame: .zero)
    private var tabSeg: SegControl?
    private var tabZoneIds: [String] = []
    private let policy = NSTextField(labelWithString: "")
    private var searchF: LoftField!
    private var listScroll: NSScrollView!
    private let list = FlippedView(frame: .zero)
    private let addBtn = GhostButton(frame: .zero)
    private let imp = PillButton(frame: .zero)
    private let exp = PillButton(frame: .zero)
    private let repo = PillButton(frame: .zero)
    private let footLine = FlippedView(frame: .zero)
    private var rows: [String: SplitRow] = [:]

    /// Builds the split lists window around `engine`.
    init(engine: SessionController) {
        self.engine = engine
        let w = makeLoftWindow(size: NSSize(width: 440, height: 600))
        super.init(window: w)
        w.level = .normal
        build()
        w.center()
        reload()
    }

    required init?(coder: NSCoder) { fatalError() }

    private func build() {
        let c = loftBody(window!)
        titleLab.font = Palette.font(20)
        titleLab.color = Palette.snow
        titleLab.pillNudge = false
        splitSw.onChange = { [weak self] on in
            self?.engine.store.split = on
            self?.engine.store.savePrefs()
            self?.engine.syncSplitRoutes()
            NotificationCenter.default.post(name: .loftState, object: self?.engine)
        }
        sub.font = Palette.body(12.5)
        sub.textColor = Palette.mute
        native.font = Palette.mono(11.5)
        native.textColor = Palette.stone
        zoneLab.font = Palette.body(11.5)
        zoneLab.textColor = Palette.mute
        zonesBtn.font = Palette.font(12)
        zonesBtn.tone = Palette.lamp
        zonesBtn.alignment = .right
        zonesBtn.target = self
        zonesBtn.action = #selector(zonesTap)
        policy.font = Palette.body(12.5)
        policy.textColor = Palette.mute
        searchF = LoftField(frame: .zero, placeholder: L10n.t("split_search"))
        searchF.onEdit = { [weak self] s in
            self?.query = s
            self?.reload()
        }
        listScroll = loftOverlayScroll(frame: .zero)
        listScroll.documentView = list
        addBtn.font = Palette.body(12.5)
        addBtn.tone = Palette.mute
        addBtn.alignment = .left
        addBtn.target = self
        addBtn.action = #selector(addTap)
        imp.compact = true
        exp.compact = true
        repo.compact = true
        imp.target = self; imp.action = #selector(impTap)
        exp.target = self; exp.action = #selector(expTap)
        repo.target = self; repo.action = #selector(repoTap)
        footLine.wantsLayer = true
        footLine.layer?.backgroundColor = Palette.line.cgColor
        for v in [titleLab, splitSw, sub, native, zoneLab, zonesBtn, policy, searchF, listScroll, addBtn, footLine, imp, exp, repo] as [NSView] {
            c.addSubview(v)
        }
    }

    /// Reloads lists, toggle, zone tabs, and native hop caption.
    @objc func reload() {
        let c = loftBody(window!)
        let w = c.bounds.width > 1 ? c.bounds.width : 440
        let routes = engine.store.routes
        let on = engine.store.split
        let ids = routes.zoneIds()
        if !ids.contains(zoneId) { zoneId = ids.contains("global") ? "global" : (ids.first ?? "global") }
        titleLab.text = L10n.t("split")
        splitSw.on = on
        sub.stringValue = on ? L10n.t("split_sub") : L10n.t("split_sub_off")
        native.stringValue = on ? nativeLine() : ""
        native.isHidden = !on || native.stringValue.isEmpty
        zoneLab.stringValue = L10n.t("split_zone")
        zonesBtn.title = L10n.t("split_zones")
        let defVia = routes.zoneDef(zoneId)
        policy.stringValue = routes.zoneLabel(zoneId) + " · " + L10n.t(defVia ? "split_via" : "split_bypass")
        imp.title = L10n.t("import")
        exp.title = L10n.t("export")
        repo.title = L10n.t("split_repo")
        addBtn.title = "+  " + L10n.t("split_add")
        searchF.field.placeholderString = L10n.t("split_search")

        let labels = ids.map { id in
            "\(routes.zoneLabel(id))  \(routes.sets(in: id).count)"
        }
        if tabSeg == nil || tabZoneIds != ids {
            tabSeg?.removeFromSuperview()
            let seg = SegControl(frame: .zero, titles: labels.isEmpty ? [""] : labels)
            let selected = ids.firstIndex(of: zoneId) ?? 0
            seg.value = selected
            seg.onChange = { [weak self] v in
                guard let self = self, self.tabZoneIds.indices.contains(v) else { return }
                self.zoneId = self.tabZoneIds[v]
                self.reload()
            }
            tabSeg = seg
            tabZoneIds = ids
            c.addSubview(seg)
        } else if let seg = tabSeg {
            seg.setTitles(labels)
            if let i = ids.firstIndex(of: zoneId) { seg.value = i }
        }

        let headY: CGFloat = 14
        let headH: CGFloat = 28
        titleLab.frame = NSRect(x: 20, y: headY, width: w - 20 - 36 - 16, height: headH)
        splitSw.frame = NSRect(x: w - 20 - 36, y: headY + 3, width: 36, height: 22)
        let subH = max(32, Palette.textHeight(sub.stringValue, font: Palette.body(12.5), width: w - 40))
        sub.frame = NSRect(x: 20, y: headY + headH + 6, width: w - 40, height: subH)
        var y = sub.frame.maxY + 6
        if !native.isHidden {
            native.frame = NSRect(x: 20, y: y, width: w - 40, height: 16)
            y += 22
        }
        zoneLab.frame = NSRect(x: 20, y: y, width: 120, height: 16)
        zonesBtn.frame = NSRect(x: w - 20 - 120, y: y - 2, width: 120, height: 20)
        y += 22
        tabSeg?.frame = NSRect(x: 20, y: y, width: w - 40, height: 32)
        y += 40
        policy.frame = NSRect(x: 20, y: y, width: w - 40, height: 20)
        y += 24
        searchF.frame = NSRect(x: 20, y: y, width: w - 40, height: 36)
        y += 44
        let listTop = y
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let listed = routes.sets(in: zoneId).filter { q.isEmpty || $0.name.lowercased().contains(q) }
        var ly: CGFloat = 0
        var seen = Set<String>()
        if listed.isEmpty {
            for (_, row) in rows { row.removeFromSuperview() }
            rows.removeAll()
            list.subviews.forEach { $0.removeFromSuperview() }
            let empty = Palette.wrap(L10n.t("split_empty"), font: Palette.body(13), color: Palette.mute, frame: NSRect(x: 8, y: 12, width: w - 56, height: 48), lines: 3)
            empty.identifier = NSUserInterfaceItemIdentifier("split-empty")
            list.addSubview(empty)
            ly = 72
        } else {
            list.subviews.filter { $0.identifier?.rawValue == "split-empty" }.forEach { $0.removeFromSuperview() }
            for s in listed {
                seen.insert(s.id)
                ly += addRow(s, y: ly, width: w - 40)
            }
            for (id, row) in rows where !seen.contains(id) {
                row.removeFromSuperview()
                rows.removeValue(forKey: id)
            }
        }
        list.alphaValue = on ? 1 : 0.4
        list.frame = NSRect(x: 0, y: 0, width: w - 40, height: max(1, ly))
        let footer: CGFloat = 12 + 1 + 12 + 36 + 16
        let addH: CGFloat = 28
        let vis = (window?.screen ?? NSScreen.main)?.visibleFrame.height ?? 800
        let maxBody = max(360, vis - 28 - 20)
        let content = ly
        let scrollH: CGFloat
        let bodyH: CGFloat
        if listTop + content + addH + footer <= maxBody {
            scrollH = max(80, content)
            bodyH = listTop + scrollH + addH + footer
        } else {
            scrollH = max(80, maxBody - listTop - addH - footer)
            bodyH = listTop + scrollH + addH + footer
        }
        listScroll.frame = NSRect(x: 20, y: listTop, width: w - 40, height: scrollH)
        addBtn.frame = NSRect(x: 20, y: listScroll.frame.maxY + 6, width: 180, height: 24)
        footLine.frame = NSRect(x: 0, y: addBtn.frame.maxY + 10, width: w, height: 1)
        let btnY = footLine.frame.maxY + 12
        let btnW = (w - 40 - 16) / 3
        imp.frame = NSRect(x: 20, y: btnY, width: btnW, height: 36)
        exp.frame = NSRect(x: 20 + btnW + 8, y: btnY, width: btnW, height: 36)
        repo.frame = NSRect(x: 20 + (btnW + 8) * 2, y: btnY, width: btnW, height: 36)
        loftResize(window!, bodyHeight: bodyH)
    }

    /// One service row. Returns the used height.
    private func addRow(_ s: SplitSet, y: CGFloat, width: CGFloat) -> CGFloat {
        let h: CGFloat = 52
        let row = rows[s.id] ?? SplitRow(frame: .zero)
        if rows[s.id] == nil {
            rows[s.id] = row
            list.addSubview(row)
            row.hit.target = self
            row.hit.action = #selector(editTap(_:))
        }
        row.frame = NSRect(x: 0, y: y, width: width, height: h - 4)
        row.hit.frame = row.bounds
        row.hit.identifier = NSUserInterfaceItemIdentifier(s.id)
        let shownName = s.source == .ignore && s.name.lowercased() == "ignore" ? L10n.t("split_ignore") : s.name
        row.name.stringValue = shownName
        row.name.frame = NSRect(x: 10, y: 6, width: width - 100, height: 22)
        let shown = s.cidrs.prefix(2).joined(separator: " · ") + (s.cidrs.count > 2 ? " · …" : "")
        row.cidrs.stringValue = shown
        row.cidrs.frame = NSRect(x: 10, y: 28, width: width - 100, height: 16)
        row.meta.stringValue = L10n.nets(s.cidrs.count)
        row.meta.frame = NSRect(x: width - 90, y: 6, width: 80, height: 18)
        row.src.stringValue = srcLabel(s.source)
        row.src.frame = NSRect(x: width - 90, y: 26, width: 80, height: 16)
        return h
    }

    private func srcLabel(_ src: SplitSource) -> String {
        switch src {
        case .repo: return L10n.t("split_src_repo")
        case .ignore: return L10n.t("split_src_ignore")
        case .me: return L10n.t("split_src_me")
        }
    }

    private func nativeLine() -> String {
        let g = Priv.cachedGateway()
        if g.isEmpty { return "" }
        var iff = Priv.cachedIface()
        if iff.hasPrefix("utun") || iff.hasPrefix("lo") { iff = "" }
        if iff.isEmpty {
            return String(format: L10n.t("split_native_gw"), g)
        }
        return String(format: L10n.t("split_native"), g, iff)
    }

    @objc private func addTap() {
        AppDelegate.shared?.showSplitAdd(nil, zone: zoneId)
    }

    @objc private func editTap(_ sender: NSButton) {
        let id = sender.identifier?.rawValue ?? ""
        guard let s = engine.store.routes.sets.first(where: { $0.id == id }) else { return }
        AppDelegate.shared?.showSplitAdd(s, zone: s.zone)
    }

    @objc private func zonesTap() { AppDelegate.shared?.showSplitZones() }
    @objc private func impTap() { AppDelegate.shared?.showSplitImport(zone: zoneId) }
    @objc private func expTap() { AppDelegate.shared?.showSplitExport(zone: zoneId) }
    @objc private func repoTap() { AppDelegate.shared?.showSplitRepo(zone: zoneId) }

    /// `--shot` helper: select a zone tab and relayout.
    func shotZone(_ id: String) {
        zoneId = id
        reload()
    }
}

/// Keenetic / loft-routes drop-in.
final class SplitImportWindowController: NSWindowController {
    private let engine: SessionController
    private var pending: SplitFile?
    private var zoneIds: [String] = []
    private var whereZone: String
    private var detect = ""
    private var drop: NSTextField!
    private var hint: NSTextField!

    init(engine: SessionController, zone: String) {
        self.engine = engine
        self.whereZone = zone
        let w = makeLoftWindow(size: NSSize(width: 440, height: 420))
        super.init(window: w)
        w.level = .normal
        build()
        w.center()
    }

    required init?(coder: NSCoder) { fatalError() }

    private func build() {
        let c = loftBody(window!)
        c.onFiles = { [weak self] urls in
            if let u = urls.first { self?.loadFile(u) }
        }
        c.registerForDraggedTypes([.fileURL])
        paint()
    }

    private func paint() {
        let c = loftBody(window!)
        c.subviews.forEach { $0.removeFromSuperview() }
        var y: CGFloat = 14
        let title = DrawLab(frame: NSRect(x: 28, y: y, width: 384, height: 28))
        title.text = L10n.t("split_import_title")
        title.font = Palette.font(20)
        title.color = Palette.snow
        title.alignment = .center
        title.pillNudge = false
        c.addSubview(title)
        y += 34
        let bodyH = max(36, Palette.textHeight(L10n.t("split_import_body"), font: Palette.body(13.5), width: 384))
        let body = Palette.wrap(L10n.t("split_import_body"), font: Palette.body(13.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: bodyH), lines: 4)
        body.alignment = .center
        c.addSubview(body)
        y += bodyH + 12
        let plate = LoftPlate(frame: NSRect(x: 28, y: y, width: 384, height: 72))
        plate.radius = 12
        plate.stroke = Palette.slate
        drop = Palette.wrap(L10n.t("split_import_drop"), font: Palette.body(13), color: Palette.mute, frame: NSRect(x: 16, y: 18, width: 352, height: 36), lines: 2)
        drop.alignment = .center
        plate.addSubview(drop)
        c.addSubview(plate)
        y += 84
        hint = Palette.label(detect, font: Palette.body(11.5), color: Palette.stone, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        hint.alignment = .center
        c.addSubview(hint)
        y += detect.isEmpty ? 4 : 22
        let showWhere = pending == nil || pending?.needsWhere == true
        if showWhere {
            let whereLab = Palette.label(L10n.t("split_zone"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
            c.addSubview(whereLab)
            y += 18
            zoneIds = engine.store.routes.zoneIds()
            let sel = LoftSelect(frame: NSRect(x: 28, y: y, width: 384, height: 36))
            sel.titles = zoneIds.map { engine.store.routes.zoneLabel($0) }
            sel.value = zoneIds.firstIndex(of: whereZone) ?? 0
            sel.onChange = { [weak self] v in
                guard let self = self, self.zoneIds.indices.contains(v) else { return }
                self.whereZone = self.zoneIds[v]
            }
            c.addSubview(sel)
            y += 48
        }
        let n = pending?.lineCount ?? 0
        let go = PillButton(frame: NSRect(x: 28, y: y, width: 384, height: 42))
        go.lampOn = true
        go.title = n > 0 ? String(format: L10n.t("split_import_go"), n) : L10n.t("split_import_pick")
        go.target = self
        go.action = #selector(goTap)
        c.addSubview(go)
        y += 50
        let back = GhostButton(frame: NSRect(x: 28, y: y, width: 384, height: 22))
        back.title = L10n.t("back")
        back.font = Palette.body(12.5)
        back.tone = Palette.mute
        back.alignment = .center
        back.target = self
        back.action = #selector(backTap)
        c.addSubview(back)
        y += 22 + 14
        loftResize(window!, bodyHeight: y)
    }

    @objc private func pickTap() {
        let p = NSOpenPanel()
        p.allowedFileTypes = ["bat", "txt", "loft-routes"]
        p.allowsMultipleSelection = false
        p.begin { [weak self] r in
            if r == .OK, let u = p.url { self?.loadFile(u) }
        }
    }

    private func loadFile(_ url: URL) {
        let text = (try? String(contentsOf: url, encoding: .utf8))
            ?? (try? String(contentsOf: url, encoding: .windowsCP1251))
            ?? ""
        let f = SplitBat.parse(text)
        pending = f
        if f.sets.isEmpty {
            detect = L10n.t("split_import_none")
        } else {
            let kind = f.loftHeader || !f.needsWhere ? L10n.t("split_detect_loft") : L10n.t("split_detect_keen")
            let n = String(format: L10n.t("split_detect_n"), f.lineCount)
            detect = f.needsWhere ? "\(kind) · \(n) · \(L10n.t("split_detect_nodir"))" : "\(kind) · \(n)"
        }
        paint()
    }

    @objc private func goTap() {
        guard var f = pending, !f.sets.isEmpty else {
            pickTap()
            return
        }
        if f.needsWhere {
            for i in f.sets.indices { f.sets[i].zone = whereZone }
        }
        engine.store.routes.ingest(f.sets, asRepo: false)
        NotificationCenter.default.post(name: .loftState, object: engine)
        window?.close()
        AppDelegate.shared?.showSplit()
    }

    @objc private func backTap() {
        window?.close()
        AppDelegate.shared?.showSplit()
    }
}

/// Keenetic .bat or loft-routes export.
final class SplitExportWindowController: NSWindowController {
    private let engine: SessionController
    private var loftFmt = true
    private var zoneIds: [String] = []
    private var whereZone: String

    init(engine: SessionController, zone: String) {
        self.engine = engine
        self.whereZone = zone
        let w = makeLoftWindow(size: NSSize(width: 440, height: 420))
        super.init(window: w)
        w.level = .normal
        paint()
        w.center()
    }

    required init?(coder: NSCoder) { fatalError() }

    private func paint() {
        let c = loftBody(window!)
        c.subviews.forEach { $0.removeFromSuperview() }
        var y: CGFloat = 14
        let title = DrawLab(frame: NSRect(x: 28, y: y, width: 384, height: 28))
        title.text = L10n.t("split_export_title")
        title.font = Palette.font(20)
        title.color = Palette.snow
        title.alignment = .center
        title.pillNudge = false
        c.addSubview(title)
        y += 34
        let bodyH = max(36, Palette.textHeight(L10n.t("split_export_body"), font: Palette.body(13.5), width: 384))
        let body = Palette.wrap(L10n.t("split_export_body"), font: Palette.body(13.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: bodyH), lines: 4)
        body.alignment = .center
        c.addSubview(body)
        y += bodyH + 12
        let fl = Palette.label(L10n.t("split_export_fmt"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(fl)
        y += 18
        let fmt = SegControl(frame: NSRect(x: 28, y: y, width: 384, height: 28), titles: [L10n.t("split_export_loft"), L10n.t("split_export_keen")], capsule: true)
        fmt.value = loftFmt ? 0 : 1
        fmt.onChange = { [weak self] v in
            self?.loftFmt = v == 0
            self?.paint()
        }
        c.addSubview(fmt)
        y += 40
        if !loftFmt {
            let wl = Palette.label(L10n.t("split_zone"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
            c.addSubview(wl)
            y += 18
            zoneIds = engine.store.routes.zoneIds()
            let sel = LoftSelect(frame: NSRect(x: 28, y: y, width: 384, height: 36))
            sel.titles = zoneIds.map { engine.store.routes.zoneLabel($0) }
            sel.value = zoneIds.firstIndex(of: whereZone) ?? 0
            sel.onChange = { [weak self] v in
                guard let self = self, self.zoneIds.indices.contains(v) else { return }
                self.whereZone = self.zoneIds[v]
                self.paint()
            }
            c.addSubview(sel)
            y += 48
        }
        let sample = loftFmt
            ? "REM loft-routes 1.0\nREM zone: global\nroute ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube\nREM zone: local\nroute ADD 10.0.0.0 MASK 255.0.0.0 0.0.0.0 :: rem LAN"
            : "route ADD 10.0.0.0 MASK 255.0.0.0 0.0.0.0 :: rem LAN"
        let sampleH = max(56, Palette.textHeight(sample, font: Palette.mono(11), width: 360))
        let plate = LoftPlate(frame: NSRect(x: 28, y: y, width: 384, height: sampleH + 20))
        plate.fill = Palette.field
        plate.radius = 8
        let sm = Palette.wrap(sample, font: Palette.mono(11), color: Palette.stone, frame: NSRect(x: 12, y: 10, width: 360, height: sampleH), lines: 6)
        plate.addSubview(sm)
        c.addSubview(plate)
        y += sampleH + 32
        let go = PillButton(frame: NSRect(x: 28, y: y, width: 384, height: 42))
        go.lampOn = true
        go.title = loftFmt ? L10n.t("split_export_save") : String(format: L10n.t("split_export_save_keen"), keenFileName())
        go.target = self
        go.action = #selector(saveTap)
        c.addSubview(go)
        y += 50
        let back = GhostButton(frame: NSRect(x: 28, y: y, width: 384, height: 22))
        back.title = L10n.t("back")
        back.font = Palette.body(12.5)
        back.tone = Palette.mute
        back.alignment = .center
        back.target = self
        back.action = #selector(backTap)
        c.addSubview(back)
        y += 22 + 14
        loftResize(window!, bodyHeight: y)
    }

    /// A Keenetic export is named after the zone's first service.
    private func keenFileName() -> String {
        let sets = engine.store.routes.sets(in: whereZone)
        return sets.first.map { $0.name.isEmpty ? "routes" : $0.name } ?? "routes"
    }

    @objc private func saveTap() {
        let routes = engine.store.routes
        let sets: [SplitSet]
        if loftFmt {
            sets = routes.sets
        } else {
            sets = routes.sets(in: whereZone)
        }
        if sets.isEmpty || sets.allSatisfy({ $0.cidrs.isEmpty }) {
            loftNotice("split_export_empty")
            return
        }
        let p = NSSavePanel()
        p.allowedFileTypes = ["bat"]
        if loftFmt {
            p.nameFieldStringValue = "loft-routes.bat"
        } else {
            p.nameFieldStringValue = keenFileName() + ".bat"
        }
        p.begin { [weak self] r in
            guard r == .OK, let u = p.url, let self = self else { return }
            try? SplitBat.emit(sets, loft: self.loftFmt, zoneOrder: routes.zoneIds()).write(to: u, atomically: true, encoding: .utf8)
            self.window?.close()
            AppDelegate.shared?.showSplit()
        }
    }

    @objc private func backTap() {
        window?.close()
        AppDelegate.shared?.showSplit()
    }
}

/// Raw .bat URL of one zone, auto-update, pull now.
final class SplitRepoWindowController: NSWindowController {
    private let engine: SessionController
    private let zoneId: String
    private var urlF: LoftField!
    private var pulling = false

    init(engine: SessionController, zone: String) {
        self.engine = engine
        self.zoneId = zone
        let w = makeLoftWindow(size: NSSize(width: 440, height: 480))
        super.init(window: w)
        w.level = .normal
        paint()
        w.center()
    }

    required init?(coder: NSCoder) { fatalError() }

    private func paint() {
        harvest()
        let c = loftBody(window!)
        c.subviews.forEach { $0.removeFromSuperview() }
        guard let z = engine.store.routes.zoneById(zoneId) ?? engine.store.routes.zoneById("global") else { return }
        var y: CGFloat = 14
        let title = DrawLab(frame: NSRect(x: 28, y: y, width: 384, height: 28))
        title.text = L10n.t("split_repo")
        title.font = Palette.font(20)
        title.color = Palette.snow
        title.alignment = .center
        title.pillNudge = false
        c.addSubview(title)
        y += 34
        let cap = engine.store.routes.zoneLabel(z.id) + " · " + L10n.t("split_repo_body")
        let bodyH = max(36, Palette.textHeight(cap, font: Palette.body(13.5), width: 384))
        let body = Palette.wrap(cap, font: Palette.body(13.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: bodyH), lines: 5)
        body.alignment = .center
        c.addSubview(body)
        y += bodyH + 12
        y = addRepoURL(into: c, y: y, url: z.repo)
        let capW: CGFloat = 320
        let n = Palette.label(L10n.t("split_repo_auto"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 28, y: y, width: capW, height: 18))
        let dH = max(16, Palette.textHeight(L10n.t("split_repo_auto_d"), font: Palette.body(11.5), width: capW))
        let d = Palette.wrap(L10n.t("split_repo_auto_d"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y + 20, width: capW, height: dH), lines: 2)
        let sw = ToggleSwitch(frame: NSRect(x: 376, y: y + 2, width: 36, height: 22))
        sw.on = z.auto
        sw.onChange = { [weak self] on in
            guard let self = self, let i = self.engine.store.routes.zoneIndex(self.zoneId) else { return }
            self.engine.store.routes.zones[i].auto = on
            self.engine.store.routes.save()
        }
        c.addSubview(n); c.addSubview(d); c.addSubview(sw)
        y += 20 + dH + 12
        if !z.repoLast.isEmpty {
            let last = Palette.label(z.repoLast, font: Palette.body(11.5), color: Palette.stone, frame: NSRect(x: 28, y: y, width: 384, height: 16))
            c.addSubview(last)
            y += 22
        } else {
            let none = Palette.label(L10n.t("split.repo.none"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
            c.addSubview(none)
            y += 22
        }
        let go = PillButton(frame: NSRect(x: 28, y: y, width: 384, height: 42))
        go.lampOn = true
        go.title = pulling ? L10n.t("upd_checking") : L10n.t("split_repo_pull")
        go.isEnabled = !pulling
        go.target = self
        go.action = #selector(pullTap)
        c.addSubview(go)
        y += 50
        let back = GhostButton(frame: NSRect(x: 28, y: y, width: 384, height: 22))
        back.title = L10n.t("back")
        back.font = Palette.body(12.5)
        back.tone = Palette.mute
        back.alignment = .center
        back.target = self
        back.action = #selector(backTap)
        c.addSubview(back)
        y += 22 + 14
        loftResize(window!, bodyHeight: y)
    }

    private func addRepoURL(into c: NSView, y: CGFloat, url: String) -> CGFloat {
        let ul = Palette.label(L10n.t("split_repo_url"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(ul)
        let y = y + 18
        let pickTitle = L10n.t("split_repo_pick")
        let pickW = min(120, max(72, Palette.textWidth(pickTitle, font: Palette.font(11.5)) + 24))
        let urlW = 384 - 8 - pickW
        urlF = LoftField(frame: NSRect(x: 28, y: y, width: urlW, height: 36), placeholder: "https://")
        urlF.stringValue = url
        c.addSubview(urlF)
        let pick = PillButton(frame: NSRect(x: 28 + urlW + 8, y: y, width: pickW, height: 36))
        pick.compact = true
        pick.title = pickTitle
        pick.target = self
        pick.action = #selector(pickTap)
        c.addSubview(pick)
        return y + 48
    }

    private func harvest() {
        guard let f = urlF, let i = engine.store.routes.zoneIndex(zoneId) else { return }
        engine.store.routes.zones[i].repo = SplitListCatalog.rewriteLegacy(f.stringValue.trimmingCharacters(in: .whitespacesAndNewlines))
        engine.store.routes.save()
    }

    @objc private func pullTap() {
        harvest()
        pulling = true
        paint()
        engine.store.routes.pullRepo(zone: zoneId) { [weak self] err in
            self?.pulling = false
            if let err = err { loftNoticeKey(err) }
            NotificationCenter.default.post(name: .loftState, object: self?.engine)
            self?.paint()
        }
    }

    @objc private func pickTap() {
        harvest()
        window?.close()
        AppDelegate.shared?.showSplitLists(back: .repo(zoneId))
    }

    @objc private func backTap() {
        harvest()
        window?.close()
        AppDelegate.shared?.showSplit()
    }
}

/// Catalog of lists in the loft repository. A row fills the zone URL.
final class SplitListWindowController: NSWindowController {
    private let engine: SessionController
    private let back: SplitListBack
    private var entries: [SplitListEntry]

    init(engine: SessionController, back: SplitListBack) {
        self.engine = engine
        self.back = back
        self.entries = SplitListCatalog.bundled()
        let w = makeLoftWindow(size: NSSize(width: 440, height: 360))
        super.init(window: w)
        w.level = .normal
        paint()
        w.center()
        SplitListCatalog.fetch { [weak self] rows in
            guard let self = self else { return }
            self.entries = rows
            self.paint()
        }
    }

    required init?(coder: NSCoder) { fatalError() }

    private func currentURL() -> String {
        switch back {
        case .repo(let id), .zoneEdit(let id):
            return engine.store.routes.zoneById(id)?.repo.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        case .zoneAdd:
            return AppDelegate.shared?.zoneAddDraft.repo.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        }
    }

    private func paint() {
        let c = loftBody(window!)
        c.subviews.forEach { $0.removeFromSuperview() }
        let cur = currentURL()
        var y: CGFloat = 14
        let title = DrawLab(frame: NSRect(x: 28, y: y, width: 384, height: 28))
        title.text = L10n.t("split_lists")
        title.font = Palette.font(20)
        title.color = Palette.snow
        title.alignment = .center
        title.pillNudge = false
        c.addSubview(title)
        y += 34
        let bodyH = max(36, Palette.textHeight(L10n.t("split_lists_body"), font: Palette.body(13.5), width: 384))
        let body = Palette.wrap(L10n.t("split_lists_body"), font: Palette.body(13.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: bodyH), lines: 4)
        body.alignment = .center
        c.addSubview(body)
        y += bodyH + 8
        for e in entries {
            y += addRow(e, y: y, current: cur, into: c)
        }
        y += 8
        let backBtn = GhostButton(frame: NSRect(x: 28, y: y, width: 384, height: 22))
        backBtn.title = L10n.t("back")
        backBtn.font = Palette.body(12.5)
        backBtn.tone = Palette.mute
        backBtn.alignment = .center
        backBtn.target = self
        backBtn.action = #selector(backTap)
        c.addSubview(backBtn)
        y += 22 + 14
        loftResize(window!, bodyHeight: y)
    }

    /// One catalog row. Returns the used height.
    private func addRow(_ e: SplitListEntry, y: CGFloat, current: String, into c: NSView) -> CGFloat {
        let h: CGFloat = 48
        let live = !current.isEmpty && current == e.url
        let plate = LoftPlate(frame: NSRect(x: 28, y: y, width: 384, height: h - 4))
        plate.radius = 8
        if live { plate.fill = Palette.field }
        c.addSubview(plate)
        let name = Palette.label(e.title(), font: Palette.font(13.5), color: live ? Palette.lamp : Palette.snow, frame: NSRect(x: 38, y: y + 6, width: 280, height: 18))
        let file = Palette.label(e.file, font: Palette.mono(11.5), color: Palette.mute, frame: NSRect(x: 38, y: y + 24, width: 280, height: 14))
        let meta = Palette.label(L10n.t("split_src_repo"), font: Palette.body(11), color: Palette.mute, frame: NSRect(x: 322, y: y + 14, width: 80, height: 14))
        meta.alignment = .right
        let hit = ActionButton(frame: plate.frame)
        hit.drawsWash = true
        hit.identifier = NSUserInterfaceItemIdentifier(e.id)
        hit.target = self
        hit.action = #selector(rowTap(_:))
        c.addSubview(name)
        c.addSubview(file)
        c.addSubview(meta)
        c.addSubview(hit)
        return h
    }

    @objc private func rowTap(_ sender: NSButton) {
        let id = sender.identifier?.rawValue ?? ""
        guard let e = entries.first(where: { $0.id == id }) else { return }
        switch back {
        case .repo(let zid), .zoneEdit(let zid):
            if let i = engine.store.routes.zoneIndex(zid) {
                engine.store.routes.zones[i].repo = e.url
                engine.store.routes.save()
            }
        case .zoneAdd:
            AppDelegate.shared?.zoneAddDraft.repo = e.url
        }
        window?.close()
        goBack()
    }

    @objc private func backTap() {
        window?.close()
        goBack()
    }

    private func goBack() {
        switch back {
        case .repo(let id):
            AppDelegate.shared?.showSplitRepo(zone: id)
        case .zoneEdit(let id):
            AppDelegate.shared?.showSplitZoneEdit(id)
        case .zoneAdd:
            AppDelegate.shared?.showSplitZoneAdd(fresh: false)
        }
    }
}

/// One CIDR (or a comma list) and a service name. Zone is membership, not direction.
final class SplitAddWindowController: NSWindowController {
    private let engine: SessionController
    private var editing: SplitSet?
    private var zoneIds: [String] = []
    private var whereZone: String
    private var asIgnore = false
    private var netText = ""
    private var nameText = ""
    private var netF: LoftField!
    private var nameF: LoftField!

    init(engine: SessionController, set: SplitSet?, zone: String) {
        self.engine = engine
        self.editing = set
        self.whereZone = set?.zone.isEmpty == false ? set!.zone : zone
        self.asIgnore = set?.source == .ignore
        self.netText = set?.cidrs.joined(separator: ", ") ?? ""
        self.nameText = set?.name ?? ""
        let w = makeLoftWindow(size: NSSize(width: 440, height: 420))
        super.init(window: w)
        w.level = .normal
        paint()
        w.center()
    }

    required init?(coder: NSCoder) { fatalError() }

    private func paint() {
        let c = loftBody(window!)
        c.subviews.forEach { $0.removeFromSuperview() }
        var y: CGFloat = 14
        let title = DrawLab(frame: NSRect(x: 28, y: y, width: 384, height: 28))
        title.text = L10n.t("split_add_title")
        title.font = Palette.font(20)
        title.color = Palette.snow
        title.alignment = .center
        title.pillNudge = false
        c.addSubview(title)
        y += 34
        let bodyKey = asIgnore ? "split_add_body_ignore" : "split_add_body"
        let bodyH = max(36, Palette.textHeight(L10n.t(bodyKey), font: Palette.body(13.5), width: 384))
        let body = Palette.wrap(L10n.t(bodyKey), font: Palette.body(13.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: bodyH), lines: 4)
        body.alignment = .center
        c.addSubview(body)
        y += bodyH + 12
        let kl = Palette.label(L10n.t("split_add_kind"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(kl)
        y += 18
        let kind = SegControl(frame: NSRect(x: 28, y: y, width: 384, height: 28), titles: [L10n.t("split_add_route"), L10n.t("split_add_ignore")], capsule: true)
        kind.value = asIgnore ? 1 : 0
        kind.onChange = { [weak self] v in
            self?.harvest()
            self?.asIgnore = v == 1
            self?.paint()
        }
        c.addSubview(kind)
        y += 40
        let nl = Palette.label(L10n.t("split_add_net"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(nl)
        y += 18
        netF = LoftField(frame: NSRect(x: 28, y: y, width: 384, height: 36), placeholder: "142.250.0.0/16")
        netF.stringValue = netText
        c.addSubview(netF)
        y += 48
        let sl = Palette.label(L10n.t("split_add_name"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(sl)
        y += 18
        nameF = LoftField(frame: NSRect(x: 28, y: y, width: 384, height: 36), placeholder: asIgnore ? L10n.t("split_ignore") : "YouTube")
        nameF.stringValue = nameText
        c.addSubview(nameF)
        y += 48
        let wl = Palette.label(L10n.t("split_zone"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(wl)
        y += 18
        zoneIds = engine.store.routes.zoneIds()
        let sel = LoftSelect(frame: NSRect(x: 28, y: y, width: 384, height: 36))
        sel.titles = zoneIds.map { engine.store.routes.zoneLabel($0) }
        sel.value = zoneIds.firstIndex(of: whereZone) ?? 0
        sel.onChange = { [weak self] v in
            guard let self = self, self.zoneIds.indices.contains(v) else { return }
            self.whereZone = self.zoneIds[v]
        }
        c.addSubview(sel)
        y += 48
        let go = PillButton(frame: NSRect(x: 28, y: y, width: 384, height: 42))
        go.lampOn = true
        go.title = editing == nil ? L10n.t("split_add_go") : L10n.t("split_add_save")
        go.target = self
        go.action = #selector(goTap)
        c.addSubview(go)
        y += 50
        let back = GhostButton(frame: NSRect(x: 28, y: y, width: 180, height: 22))
        back.title = L10n.t("back")
        back.font = Palette.body(12.5)
        back.tone = Palette.mute
        back.alignment = .left
        back.target = self
        back.action = #selector(backTap)
        c.addSubview(back)
        if editing != nil {
            let del = GhostButton(frame: NSRect(x: 232, y: y, width: 180, height: 22))
            del.title = L10n.t("delete")
            del.font = Palette.body(12.5)
            del.tone = Palette.copper
            del.alignment = .right
            del.target = self
            del.action = #selector(delTap)
            c.addSubview(del)
        }
        y += 22 + 14
        loftResize(window!, bodyHeight: y)
    }

    private func harvest() {
        if let f = netF { netText = f.stringValue }
        if let f = nameF { nameText = f.stringValue }
    }

    @objc private func goTap() {
        harvest()
        var cidrs: [String] = []
        for piece in netText.split(whereSeparator: { $0 == "," || $0 == " " || $0 == ";" || $0 == "\n" }) {
            if let c = SplitBat.normalizeCIDR(String(piece)) { cidrs.append(c) }
        }
        if cidrs.isEmpty {
            loftNotice("split_add_bad")
            return
        }
        var name = nameText.trimmingCharacters(in: .whitespacesAndNewlines)
        if name.isEmpty { name = asIgnore ? "ignore" : cidrs[0] }
        if let old = editing, old.source == .repo {
            let kept = Set(cidrs)
            let dropped = old.cidrs.filter { !kept.contains($0) }
            engine.store.routes.rememberIgnore(cidrs: dropped, zone: old.zone)
        }
        var s = editing ?? SplitSet(id: "", name: name, zone: whereZone, cidrs: cidrs, source: asIgnore ? .ignore : .me)
        s.name = name
        s.zone = whereZone
        s.cidrs = cidrs
        s.source = asIgnore ? .ignore : .me
        engine.store.routes.upsert(s)
        NotificationCenter.default.post(name: .loftState, object: engine)
        window?.close()
        AppDelegate.shared?.showSplit()
    }

    @objc private func delTap() {
        if let old = editing {
            if old.source == .repo {
                engine.store.routes.rememberIgnore(cidrs: old.cidrs, zone: old.zone)
            }
            engine.store.routes.remove(id: old.id)
        }
        NotificationCenter.default.post(name: .loftState, object: engine)
        window?.close()
        AppDelegate.shared?.showSplit()
    }

    @objc private func backTap() {
        window?.close()
        AppDelegate.shared?.showSplit()
    }
}

/// Zone list: name, direction, built-in mark.
final class SplitZonesWindowController: NSWindowController {
    private let engine: SessionController

    init(engine: SessionController) {
        self.engine = engine
        let w = makeLoftWindow(size: NSSize(width: 440, height: 420))
        super.init(window: w)
        w.level = .normal
        paint()
        w.center()
    }

    required init?(coder: NSCoder) { fatalError() }

    func reload() { paint() }

    private func paint() {
        let c = loftBody(window!)
        c.subviews.forEach { $0.removeFromSuperview() }
        var y: CGFloat = 14
        let title = DrawLab(frame: NSRect(x: 28, y: y, width: 384, height: 28))
        title.text = L10n.t("split_zones")
        title.font = Palette.font(20)
        title.color = Palette.snow
        title.alignment = .center
        title.pillNudge = false
        c.addSubview(title)
        y += 34
        let bodyH = max(36, Palette.textHeight(L10n.t("split_zones_add_body"), font: Palette.body(13.5), width: 384))
        let body = Palette.wrap(L10n.t("split_zones_add_body"), font: Palette.body(13.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: bodyH), lines: 4)
        body.alignment = .center
        c.addSubview(body)
        y += bodyH + 8
        let routes = engine.store.routes
        for id in routes.zoneIds() {
            y += addRow(id, y: y, into: c)
        }
        let add = GhostButton(frame: NSRect(x: 28, y: y, width: 200, height: 22))
        add.title = "+  " + L10n.t("split_zones_add")
        add.font = Palette.body(12.5)
        add.tone = Palette.mute
        add.alignment = .left
        add.target = self
        add.action = #selector(addTap)
        c.addSubview(add)
        y += 28
        let back = GhostButton(frame: NSRect(x: 28, y: y, width: 384, height: 22))
        back.title = L10n.t("back")
        back.font = Palette.body(12.5)
        back.tone = Palette.mute
        back.alignment = .center
        back.target = self
        back.action = #selector(backTap)
        c.addSubview(back)
        y += 22 + 14
        loftResize(window!, bodyHeight: y)
    }

    private func addRow(_ id: String, y: CGFloat, into c: NSView) -> CGFloat {
        let routes = engine.store.routes
        let z = routes.zoneById(id)
        let h: CGFloat = 52
        let plate = LoftPlate(frame: NSRect(x: 28, y: y, width: 384, height: h - 4))
        plate.radius = 8
        c.addSubview(plate)
        let n = routes.sets(in: id).count
        var sub = L10n.nets(n)
        if let file = zoneRepoFile(z?.repo ?? "") { sub += " · " + file }
        let name = Palette.label(routes.zoneLabel(id), font: Palette.font(13.5), color: Palette.snow, frame: NSRect(x: 38, y: y + 6, width: 240, height: 18))
        let u = Palette.label(sub, font: Palette.mono(11.5), color: Palette.mute, frame: NSRect(x: 38, y: y + 24, width: 240, height: 14))
        let dir = Palette.label(L10n.t((z?.def ?? routes.zoneDef(id)) ? "split_via" : "split_bypass"), font: Palette.body(11), color: Palette.mute, frame: NSRect(x: 280, y: y + 6, width: 122, height: 14))
        dir.alignment = .right
        if z?.builtin == true {
            let mark = Palette.label(L10n.t("split_zones_builtin"), font: Palette.font(10), color: Palette.copper, frame: NSRect(x: 280, y: y + 22, width: 122, height: 14))
            mark.alignment = .right
            c.addSubview(mark)
        }
        let hit = ActionButton(frame: plate.frame)
        hit.drawsWash = true
        hit.identifier = NSUserInterfaceItemIdentifier(id)
        hit.target = self
        hit.action = #selector(rowTap(_:))
        c.addSubview(name)
        c.addSubview(u)
        c.addSubview(dir)
        c.addSubview(hit)
        return h
    }

    @objc private func rowTap(_ sender: NSButton) {
        let id = sender.identifier?.rawValue ?? ""
        AppDelegate.shared?.showSplitZoneEdit(id)
    }

    @objc private func addTap() { AppDelegate.shared?.showSplitZoneAdd(fresh: true) }
    @objc private func backTap() {
        window?.close()
        AppDelegate.shared?.showSplit()
    }
}

/// Edit one zone: name (locked if built-in), direction, repository.
final class SplitZoneEditWindowController: NSWindowController {
    private let engine: SessionController
    private let zoneId: String
    private var nameF: LoftField!
    private var urlF: LoftField!
    private var defVia: Bool
    private var painted = false

    init(engine: SessionController, zone: String) {
        self.engine = engine
        self.zoneId = zone
        self.defVia = engine.store.routes.zoneDef(zone)
        let w = makeLoftWindow(size: NSSize(width: 440, height: 520))
        super.init(window: w)
        w.level = .normal
        paint()
        w.center()
    }

    required init?(coder: NSCoder) { fatalError() }

    private func paint() {
        if painted { harvest() }
        painted = true
        let c = loftBody(window!)
        c.subviews.forEach { $0.removeFromSuperview() }
        let routes = engine.store.routes
        guard var z = routes.zoneById(zoneId) else { return }
        z.def = defVia
        var y: CGFloat = 14
        let title = DrawLab(frame: NSRect(x: 28, y: y, width: 384, height: 28))
        title.text = L10n.t("split_zones_edit_title")
        title.font = Palette.font(20)
        title.color = Palette.snow
        title.alignment = .center
        title.pillNudge = false
        c.addSubview(title)
        y += 34
        let n = routes.sets(in: z.id).count
        var cap = L10n.nets(n)
        if z.builtin { cap = L10n.t("split_zones_builtin") + " · " + cap }
        let body = Palette.wrap(cap, font: Palette.body(13.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 20), lines: 1)
        body.alignment = .center
        c.addSubview(body)
        y += 28
        let nl = Palette.label(L10n.t("split_zones_name"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(nl)
        y += 18
        nameF = LoftField(frame: NSRect(x: 28, y: y, width: 384, height: 36), placeholder: "Local")
        nameF.stringValue = routes.zoneLabel(z.id)
        nameF.isEnabled = !z.builtin
        c.addSubview(nameF)
        y += 48
        let dl = Palette.label(L10n.t("split_rdef"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(dl)
        y += 18
        let dir = SegControl(frame: NSRect(x: 28, y: y, width: 384, height: 28), titles: [L10n.t("split_via"), L10n.t("split_bypass")], capsule: true)
        dir.value = defVia ? 0 : 1
        dir.onChange = { [weak self] v in
            self?.defVia = v == 0
            self?.paint()
        }
        c.addSubview(dir)
        y += 40
        let ul = Palette.label(L10n.t("split_repo_url"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(ul)
        y += 18
        let pickTitle = L10n.t("split_repo_pick")
        let pickW = min(120, max(72, Palette.textWidth(pickTitle, font: Palette.font(11.5)) + 24))
        let urlW = 384 - 8 - pickW
        urlF = LoftField(frame: NSRect(x: 28, y: y, width: urlW, height: 36), placeholder: "https://")
        urlF.stringValue = z.repo
        c.addSubview(urlF)
        let pick = PillButton(frame: NSRect(x: 28 + urlW + 8, y: y, width: pickW, height: 36))
        pick.compact = true
        pick.title = pickTitle
        pick.target = self
        pick.action = #selector(pickTap)
        c.addSubview(pick)
        y += 48
        let capW: CGFloat = 320
        let an = Palette.label(L10n.t("split_repo_auto"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 28, y: y, width: capW, height: 18))
        let dH = max(16, Palette.textHeight(L10n.t("split_repo_auto_d"), font: Palette.body(11.5), width: capW))
        let ad = Palette.wrap(L10n.t("split_repo_auto_d"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y + 20, width: capW, height: dH), lines: 2)
        let sw = ToggleSwitch(frame: NSRect(x: 376, y: y + 2, width: 36, height: 22))
        sw.on = z.auto
        sw.onChange = { [weak self] on in
            guard let self = self, let i = self.engine.store.routes.zoneIndex(self.zoneId) else { return }
            self.engine.store.routes.zones[i].auto = on
            self.engine.store.routes.save()
        }
        c.addSubview(an); c.addSubview(ad); c.addSubview(sw)
        y += 20 + dH + 16
        let go = PillButton(frame: NSRect(x: 28, y: y, width: 384, height: 42))
        go.lampOn = true
        go.title = L10n.t("split_zones_save")
        go.target = self
        go.action = #selector(saveTap)
        c.addSubview(go)
        y += 50
        if !z.builtin {
            let del = GhostButton(frame: NSRect(x: 28, y: y, width: 384, height: 22))
            del.title = L10n.t("split_zones_del")
            del.font = Palette.body(12.5)
            del.tone = Palette.err
            del.alignment = .center
            del.target = self
            del.action = #selector(delTap)
            c.addSubview(del)
            y += 22
            let move = Palette.wrap(L10n.t("split_zones_move"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16), lines: 1)
            c.addSubview(move)
            y += 20
        }
        let back = GhostButton(frame: NSRect(x: 28, y: y, width: 384, height: 22))
        back.title = L10n.t("back")
        back.font = Palette.body(12.5)
        back.tone = Palette.mute
        back.alignment = .center
        back.target = self
        back.action = #selector(backTap)
        c.addSubview(back)
        y += 22 + 14
        loftResize(window!, bodyHeight: y)
    }

    private func harvest() {
        guard let i = engine.store.routes.zoneIndex(zoneId) else { return }
        engine.store.routes.zones[i].def = defVia
        if let f = urlF {
            engine.store.routes.zones[i].repo = SplitListCatalog.rewriteLegacy(f.stringValue.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        if let n = nameF, !engine.store.routes.zones[i].builtin {
            let name = n.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
            if !name.isEmpty { engine.store.routes.zones[i].name = name }
        }
        engine.store.routes.save()
    }

    @objc private func saveTap() {
        harvest()
        window?.close()
        AppDelegate.shared?.showSplitZones()
    }

    @objc private func pickTap() {
        harvest()
        window?.close()
        AppDelegate.shared?.showSplitLists(back: .zoneEdit(zoneId))
    }

    @objc private func delTap() {
        engine.store.routes.deleteZone(zoneId)
        window?.close()
        AppDelegate.shared?.showSplitZones()
    }

    @objc private func backTap() {
        harvest()
        window?.close()
        AppDelegate.shared?.showSplitZones()
    }
}

/// New user zone.
final class SplitZoneAddWindowController: NSWindowController {
    private let engine: SessionController
    private var nameF: LoftField!
    private var urlF: LoftField!
    private var defVia: Bool
    private var autoOn: Bool

    init(engine: SessionController, draft: SplitZoneDraft) {
        self.engine = engine
        self.defVia = draft.def
        self.autoOn = draft.auto
        let w = makeLoftWindow(size: NSSize(width: 440, height: 520))
        super.init(window: w)
        w.level = .normal
        paint(draft: draft)
        w.center()
    }

    required init?(coder: NSCoder) { fatalError() }

    private func paint(draft: SplitZoneDraft) {
        let c = loftBody(window!)
        c.subviews.forEach { $0.removeFromSuperview() }
        var y: CGFloat = 14
        let title = DrawLab(frame: NSRect(x: 28, y: y, width: 384, height: 28))
        title.text = L10n.t("split_zones_add_title")
        title.font = Palette.font(20)
        title.color = Palette.snow
        title.alignment = .center
        title.pillNudge = false
        c.addSubview(title)
        y += 34
        let bodyH = max(36, Palette.textHeight(L10n.t("split_zones_add_body"), font: Palette.body(13.5), width: 384))
        let body = Palette.wrap(L10n.t("split_zones_add_body"), font: Palette.body(13.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: bodyH), lines: 4)
        body.alignment = .center
        c.addSubview(body)
        y += bodyH + 12
        let nl = Palette.label(L10n.t("split_zones_name"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(nl)
        y += 18
        nameF = LoftField(frame: NSRect(x: 28, y: y, width: 384, height: 36), placeholder: "Home")
        nameF.stringValue = draft.name
        c.addSubview(nameF)
        y += 48
        let dl = Palette.label(L10n.t("split_rdef"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(dl)
        y += 18
        let dir = SegControl(frame: NSRect(x: 28, y: y, width: 384, height: 28), titles: [L10n.t("split_via"), L10n.t("split_bypass")], capsule: true)
        dir.value = defVia ? 0 : 1
        dir.onChange = { [weak self] v in
            guard let self = self else { return }
            self.defVia = v == 0
            self.harvestDraft()
            self.paint(draft: AppDelegate.shared?.zoneAddDraft ?? SplitZoneDraft())
        }
        c.addSubview(dir)
        y += 40
        let ul = Palette.label(L10n.t("split_repo_url"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(ul)
        y += 18
        let pickTitle = L10n.t("split_repo_pick")
        let pickW = min(120, max(72, Palette.textWidth(pickTitle, font: Palette.font(11.5)) + 24))
        let urlW = 384 - 8 - pickW
        urlF = LoftField(frame: NSRect(x: 28, y: y, width: urlW, height: 36), placeholder: "https://")
        urlF.stringValue = draft.repo
        c.addSubview(urlF)
        let pick = PillButton(frame: NSRect(x: 28 + urlW + 8, y: y, width: pickW, height: 36))
        pick.compact = true
        pick.title = pickTitle
        pick.target = self
        pick.action = #selector(pickTap)
        c.addSubview(pick)
        y += 48
        let capW: CGFloat = 320
        let an = Palette.label(L10n.t("split_repo_auto"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 28, y: y, width: capW, height: 18))
        let dH = max(16, Palette.textHeight(L10n.t("split_repo_auto_d"), font: Palette.body(11.5), width: capW))
        let ad = Palette.wrap(L10n.t("split_repo_auto_d"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y + 20, width: capW, height: dH), lines: 2)
        let sw = ToggleSwitch(frame: NSRect(x: 376, y: y + 2, width: 36, height: 22))
        sw.on = autoOn
        sw.onChange = { [weak self] on in
            self?.autoOn = on
            self?.harvestDraft()
        }
        c.addSubview(an); c.addSubview(ad); c.addSubview(sw)
        y += 20 + dH + 16
        let go = PillButton(frame: NSRect(x: 28, y: y, width: 384, height: 42))
        go.lampOn = true
        go.title = L10n.t("split_zones_add_go")
        go.target = self
        go.action = #selector(addTap)
        c.addSubview(go)
        y += 50
        let back = GhostButton(frame: NSRect(x: 28, y: y, width: 384, height: 22))
        back.title = L10n.t("back")
        back.font = Palette.body(12.5)
        back.tone = Palette.mute
        back.alignment = .center
        back.target = self
        back.action = #selector(backTap)
        c.addSubview(back)
        y += 22 + 14
        loftResize(window!, bodyHeight: y)
    }

    private func harvestDraft() {
        var d = AppDelegate.shared?.zoneAddDraft ?? SplitZoneDraft()
        d.name = nameF?.stringValue ?? d.name
        d.repo = SplitListCatalog.rewriteLegacy(urlF?.stringValue ?? d.repo)
        d.def = defVia
        d.auto = autoOn
        AppDelegate.shared?.zoneAddDraft = d
    }

    @objc private func addTap() {
        harvestDraft()
        let d = AppDelegate.shared?.zoneAddDraft ?? SplitZoneDraft()
        let name = d.name.trimmingCharacters(in: .whitespacesAndNewlines)
        if name.isEmpty {
            loftNotice("split_zones_name")
            return
        }
        let id = engine.store.routes.zoneSlug(name)
        engine.store.routes.upsertZone(SplitZone(
            id: id, name: name, builtin: false, def: d.def,
            repo: d.repo.trimmingCharacters(in: .whitespacesAndNewlines),
            auto: d.auto, repoLast: "", repoAt: nil, repoCount: 0
        ))
        AppDelegate.shared?.zoneAddDraft = SplitZoneDraft()
        window?.close()
        AppDelegate.shared?.showSplitZones()
    }

    @objc private func pickTap() {
        harvestDraft()
        window?.close()
        AppDelegate.shared?.showSplitLists(back: .zoneAdd)
    }

    @objc private func backTap() {
        harvestDraft()
        window?.close()
        AppDelegate.shared?.showSplitZones()
    }
}

private func zoneRepoFile(_ repo: String) -> String? {
    let u = repo.trimmingCharacters(in: .whitespacesAndNewlines)
    if u.isEmpty { return nil }
    return u.split(separator: "?").first.flatMap { $0.split(separator: "#").first }.flatMap { $0.split(separator: "/").last }.map(String.init)
}

/// Notice whose text is already localized (repo pull errors).
func loftNoticeKey(_ text: String) {
    let a = NSAlert()
    a.messageText = text
    a.addButton(withTitle: L10n.t("ok"))
    a.runModal()
}
