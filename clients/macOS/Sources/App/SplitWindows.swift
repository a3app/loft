// Split-routing windows: lists, Keenetic import/export, repository, add a network.

import AppKit

/// One split-list row. Reused across reloads.
private final class SplitRow: FlippedView {
    let plate = LoftPlate(frame: .zero)
    let hit = ActionButton(frame: .zero)
    let name = NSTextField(labelWithString: "")
    let cidrs = NSTextField(labelWithString: "")
    let meta = NSTextField(labelWithString: "")
    let src = NSTextField(labelWithString: "")

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
        for v in [plate, hit, name, cidrs, meta, src] as [NSView] { addSubview(v) }
    }

    required init?(coder: NSCoder) { fatalError() }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        plate.frame = bounds
        hit.frame = bounds
    }
}

/// Lists window: via / past loft, import, export, repository.
final class SplitWindowController: NSWindowController {
    private let engine: SessionController
    private var tab = 0
    private let titleLab = DrawLab(frame: .zero)
    private let splitSw = ToggleSwitch(frame: .zero)
    private let sub = NSTextField(wrappingLabelWithString: "")
    private let native = NSTextField(labelWithString: "")
    private var tabSeg: SegControl!
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
        let w = makeLoftWindow(size: NSSize(width: 440, height: 520))
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
            NotificationCenter.default.post(name: .loftState, object: self?.engine)
        }
        sub.font = Palette.body(12.5)
        sub.textColor = Palette.mute
        native.font = Palette.mono(11.5)
        native.textColor = Palette.stone
        tabSeg = SegControl(frame: .zero, titles: ["", ""])
        tabSeg.onChange = { [weak self] v in
            self?.tab = v
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
        for v in [titleLab, splitSw, sub, native, tabSeg, listScroll, addBtn, footLine, imp, exp, repo] as [NSView] {
            c.addSubview(v)
        }
    }

    /// Reloads lists, toggle, and native hop caption.
    @objc func reload() {
        let c = loftBody(window!)
        let w = c.bounds.width > 1 ? c.bounds.width : 440
        let routes = engine.store.routes
        let on = engine.store.split
        titleLab.text = L10n.t("split")
        splitSw.on = on
        sub.stringValue = on ? L10n.t("split_sub") : L10n.t("split_sub_off")
        native.stringValue = on ? nativeLine() : ""
        native.isHidden = !on || native.stringValue.isEmpty
        imp.title = L10n.t("import")
        exp.title = L10n.t("export")
        repo.title = L10n.t("split_repo")
        addBtn.title = "+  " + L10n.t("split_add")
        let viaN = routes.cidrCount(true)
        let byN = routes.cidrCount(false)
        tabSeg.setTitles(["\(L10n.t("split_via"))  \(viaN)", "\(L10n.t("split_bypass"))  \(byN)"])
        tabSeg.value = tab

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
        tabSeg.frame = NSRect(x: 20, y: y, width: w - 40, height: 32)
        y += 40
        let listTop = y
        let sets = tab == 0 ? routes.viaSets() : routes.bypassSets()
        var ly: CGFloat = 0
        var seen = Set<String>()
        if sets.isEmpty {
            for (_, row) in rows { row.removeFromSuperview() }
            rows.removeAll()
            list.subviews.forEach { $0.removeFromSuperview() }
            let empty = Palette.wrap(L10n.t("split_empty"), font: Palette.body(13), color: Palette.mute, frame: NSRect(x: 8, y: 12, width: w - 56, height: 48), lines: 3)
            empty.identifier = NSUserInterfaceItemIdentifier("split-empty")
            list.addSubview(empty)
            ly = 72
        } else {
            list.subviews.filter { $0.identifier?.rawValue == "split-empty" }.forEach { $0.removeFromSuperview() }
            for s in sets {
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
        addBtn.frame = NSRect(x: 20, y: 0, width: 160, height: 22)
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
        addBtn.frame = NSRect(x: 20, y: listScroll.frame.maxY + 6, width: 180, height: 22)
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
        let h: CGFloat = 48
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
        row.name.stringValue = s.name
        row.name.frame = NSRect(x: 10, y: 6, width: width - 100, height: 18)
        let shown = s.cidrs.prefix(2).joined(separator: " · ") + (s.cidrs.count > 2 ? " · …" : "")
        row.cidrs.stringValue = shown
        row.cidrs.frame = NSRect(x: 10, y: 24, width: width - 100, height: 14)
        let net = s.cidrs.count == 1 ? L10n.t("split_net") : L10n.t("split_nets")
        row.meta.stringValue = "\(s.cidrs.count) \(net)"
        row.meta.frame = NSRect(x: width - 90, y: 6, width: 80, height: 14)
        row.src.stringValue = s.source == .repo ? L10n.t("split_src_repo") : L10n.t("split_src_me")
        row.src.frame = NSRect(x: width - 90, y: 22, width: 80, height: 14)
        return h
    }

    private func nativeLine() -> String {
        let gw = Priv.cachedGateway()
        let g = gw
        if g.isEmpty { return "" }
        var iff = Priv.cachedIface()
        if iff.hasPrefix("utun") || iff.hasPrefix("lo") { iff = "" }
        if iff.isEmpty {
            return String(format: L10n.t("split_native_gw"), g)
        }
        return String(format: L10n.t("split_native"), g, iff)
    }

    @objc private func addTap() {
        AppDelegate.shared?.showSplitAdd(nil, via: tab == 0)
    }

    @objc private func editTap(_ sender: NSButton) {
        let id = sender.identifier?.rawValue ?? ""
        guard let s = engine.store.routes.sets.first(where: { $0.id == id }) else { return }
        AppDelegate.shared?.showSplitAdd(s, via: s.via)
    }

    @objc private func impTap() { AppDelegate.shared?.showSplitImport() }
    @objc private func expTap() { AppDelegate.shared?.showSplitExport() }
    @objc private func repoTap() { AppDelegate.shared?.showSplitRepo() }
}

/// Keenetic / loft-routes drop-in.
final class SplitImportWindowController: NSWindowController {
    private let engine: SessionController
    private var pending: SplitFile?
    private var whereVia = true
    private var detect = ""
    private var drop: NSTextField!
    private var hint: NSTextField!
    private var whereLab: NSTextField!
    private var whereSeg: SegControl!
    private var go: PillButton!

    init(engine: SessionController) {
        self.engine = engine
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
            whereLab = Palette.label(L10n.t("split_import_where"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
            c.addSubview(whereLab)
            y += 18
            whereSeg = SegControl(frame: NSRect(x: 28, y: y, width: 384, height: 28), titles: [L10n.t("split_via"), L10n.t("split_bypass")], capsule: true)
            whereSeg.value = whereVia ? 0 : 1
            whereSeg.onChange = { [weak self] v in self?.whereVia = v == 0 }
            c.addSubview(whereSeg)
            y += 40
        }
        let n = pending?.lineCount ?? 0
        go = PillButton(frame: NSRect(x: 28, y: y, width: 384, height: 42))
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
            for i in f.sets.indices { f.sets[i].via = whereVia }
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
    private var viaList = true

    init(engine: SessionController) {
        self.engine = engine
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
            let wl = Palette.label(L10n.t("split_export_which"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
            c.addSubview(wl)
            y += 18
            let which = SegControl(frame: NSRect(x: 28, y: y, width: 384, height: 28), titles: [L10n.t("split_via"), L10n.t("split_bypass")], capsule: true)
            which.value = viaList ? 0 : 1
            which.onChange = { [weak self] v in
                self?.viaList = v == 0
                self?.paint()
            }
            c.addSubview(which)
            y += 40
        }
        let sample = loftFmt
            ? "route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube | via"
            : "route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube"
        let sampleH = max(40, Palette.textHeight(sample, font: Palette.mono(11), width: 360))
        let plate = LoftPlate(frame: NSRect(x: 28, y: y, width: 384, height: sampleH + 20))
        plate.fill = Palette.field
        plate.radius = 8
        let sm = Palette.wrap(sample, font: Palette.mono(11), color: Palette.stone, frame: NSRect(x: 12, y: 10, width: 360, height: sampleH), lines: 3)
        plate.addSubview(sm)
        c.addSubview(plate)
        y += sampleH + 32
        let go = PillButton(frame: NSRect(x: 28, y: y, width: 384, height: 42))
        go.lampOn = true
        go.title = loftFmt ? L10n.t("split_export_save") : L10n.t("split_export_save_keen")
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

    @objc private func saveTap() {
        let routes = engine.store.routes
        let sets: [SplitSet]
        if loftFmt {
            sets = routes.sets
        } else {
            sets = viaList ? routes.viaSets() : routes.bypassSets()
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
            p.nameFieldStringValue = (sets.first?.name.isEmpty == false ? sets[0].name : "routes") + ".bat"
        }
        p.begin { [weak self] r in
            guard r == .OK, let u = p.url else { return }
            try? SplitBat.emit(sets, loft: self?.loftFmt ?? true).write(to: u, atomically: true, encoding: .utf8)
            self?.window?.close()
            AppDelegate.shared?.showSplit()
        }
    }

    @objc private func backTap() {
        window?.close()
        AppDelegate.shared?.showSplit()
    }
}

/// Raw .bat URL, auto-update, pull now.
final class SplitRepoWindowController: NSWindowController {
    private let engine: SessionController
    private var urlF: LoftField!
    private var whereVia = true
    private var pulling = false

    init(engine: SessionController) {
        self.engine = engine
        whereVia = engine.store.routes.repoVia
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
        let r = engine.store.routes
        var y: CGFloat = 14
        let title = DrawLab(frame: NSRect(x: 28, y: y, width: 384, height: 28))
        title.text = L10n.t("split_repo")
        title.font = Palette.font(20)
        title.color = Palette.snow
        title.alignment = .center
        title.pillNudge = false
        c.addSubview(title)
        y += 34
        let bodyH = max(36, Palette.textHeight(L10n.t("split_repo_body"), font: Palette.body(13.5), width: 384))
        let body = Palette.wrap(L10n.t("split_repo_body"), font: Palette.body(13.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: bodyH), lines: 4)
        body.alignment = .center
        c.addSubview(body)
        y += bodyH + 12
        let ul = Palette.label(L10n.t("split_repo_url"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(ul)
        y += 18
        urlF = LoftField(frame: NSRect(x: 28, y: y, width: 384, height: 36), placeholder: "https://")
        urlF.stringValue = r.repoURL
        c.addSubview(urlF)
        y += 48
        let wl = Palette.label(L10n.t("split_import_where"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(wl)
        y += 18
        let whereSeg = SegControl(frame: NSRect(x: 28, y: y, width: 384, height: 28), titles: [L10n.t("split_via"), L10n.t("split_bypass")], capsule: true)
        whereSeg.value = whereVia ? 0 : 1
        whereSeg.onChange = { [weak self] v in self?.whereVia = v == 0 }
        c.addSubview(whereSeg)
        y += 40
        let capW: CGFloat = 320
        let n = Palette.label(L10n.t("split_repo_auto"), font: Palette.body(13.5), color: Palette.snow, frame: NSRect(x: 28, y: y, width: capW, height: 18))
        let dH = max(16, Palette.textHeight(L10n.t("split_repo_auto_d"), font: Palette.body(11.5), width: capW))
        let d = Palette.wrap(L10n.t("split_repo_auto_d"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y + 20, width: capW, height: dH), lines: 2)
        let sw = ToggleSwitch(frame: NSRect(x: 376, y: y + 2, width: 36, height: 22))
        sw.on = r.repoAuto
        sw.onChange = { [weak self] on in
            self?.engine.store.routes.repoAuto = on
            self?.engine.store.routes.save()
        }
        c.addSubview(n); c.addSubview(d); c.addSubview(sw)
        y += 20 + dH + 12
        if !r.repoLast.isEmpty {
            let last = Palette.label(r.repoLast, font: Palette.body(11.5), color: Palette.stone, frame: NSRect(x: 28, y: y, width: 384, height: 16))
            c.addSubview(last)
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

    private func harvest() {
        guard let f = urlF else { return }
        engine.store.routes.repoURL = f.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        engine.store.routes.repoVia = whereVia
        engine.store.routes.save()
    }

    @objc private func pullTap() {
        harvest()
        pulling = true
        paint()
        engine.store.routes.pullRepo { [weak self] err in
            self?.pulling = false
            if let err = err { loftNoticeKey(err) }
            NotificationCenter.default.post(name: .loftState, object: self?.engine)
            self?.paint()
        }
    }

    @objc private func backTap() {
        harvest()
        window?.close()
        AppDelegate.shared?.showSplit()
    }
}

/// One CIDR (or a comma list) and a service name.
final class SplitAddWindowController: NSWindowController {
    private let engine: SessionController
    private var editing: SplitSet?
    private var whereVia: Bool
    private var netF: LoftField!
    private var nameF: LoftField!

    init(engine: SessionController, set: SplitSet?, via: Bool) {
        self.engine = engine
        self.editing = set
        self.whereVia = set?.via ?? via
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
        let bodyH = max(36, Palette.textHeight(L10n.t("split_add_body"), font: Palette.body(13.5), width: 384))
        let body = Palette.wrap(L10n.t("split_add_body"), font: Palette.body(13.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: bodyH), lines: 4)
        body.alignment = .center
        c.addSubview(body)
        y += bodyH + 12
        let nl = Palette.label(L10n.t("split_add_net"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(nl)
        y += 18
        netF = LoftField(frame: NSRect(x: 28, y: y, width: 384, height: 36), placeholder: "142.250.0.0/16")
        netF.stringValue = editing?.cidrs.joined(separator: ", ") ?? ""
        c.addSubview(netF)
        y += 48
        let sl = Palette.label(L10n.t("split_add_name"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(sl)
        y += 18
        nameF = LoftField(frame: NSRect(x: 28, y: y, width: 384, height: 36), placeholder: "YouTube")
        nameF.stringValue = editing?.name ?? ""
        c.addSubview(nameF)
        y += 48
        let wl = Palette.label(L10n.t("split_import_where"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 384, height: 16))
        c.addSubview(wl)
        y += 18
        let whereSeg = SegControl(frame: NSRect(x: 28, y: y, width: 384, height: 28), titles: [L10n.t("split_via"), L10n.t("split_bypass")], capsule: true)
        whereSeg.value = whereVia ? 0 : 1
        whereSeg.onChange = { [weak self] v in self?.whereVia = v == 0 }
        c.addSubview(whereSeg)
        y += 40
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

    @objc private func goTap() {
        let raw = netF.stringValue
        var cidrs: [String] = []
        for piece in raw.split(whereSeparator: { $0 == "," || $0 == " " || $0 == ";" || $0 == "\n" }) {
            if let c = SplitBat.normalizeCIDR(String(piece)) { cidrs.append(c) }
        }
        if cidrs.isEmpty {
            loftNotice("split_add_bad")
            return
        }
        var name = nameF.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if name.isEmpty { name = cidrs[0] }
        var s = editing ?? SplitSet(id: "", name: name, via: whereVia, cidrs: cidrs, source: .me)
        s.name = name
        s.via = whereVia
        s.cidrs = cidrs
        s.source = .me
        engine.store.routes.upsert(s)
        NotificationCenter.default.post(name: .loftState, object: engine)
        window?.close()
        AppDelegate.shared?.showSplit()
    }

    @objc private func delTap() {
        if let id = editing?.id { engine.store.routes.remove(id: id) }
        NotificationCenter.default.post(name: .loftState, object: engine)
        window?.close()
        AppDelegate.shared?.showSplit()
    }

    @objc private func backTap() {
        window?.close()
        AppDelegate.shared?.showSplit()
    }
}

/// Notice whose text is already localized (repo pull errors).
func loftNoticeKey(_ text: String) {
    let a = NSAlert()
    a.messageText = text
    a.addButton(withTitle: L10n.t("ok"))
    a.runModal()
}
