// Process entry for the loft macOS menubar app.
// `--probe` handshakes every saved profile (no TUN) and exits; `--shot` dumps UI PNGs; otherwise NSApp runs.

import AppKit

if CommandLine.arguments.contains("--probe") {
    let engine = SessionController()
    engine.store.load()
    FrontHelper.reapOrphans()
    let list = engine.store.profiles
    if list.isEmpty {
        fputs("no profile\n", stderr)
        exit(1)
    }
    var failed = 0
    for p in list {
        fputs("probe \(p.name) \(p.cliType()) \(p.url) \(p.path)\n", stderr)
        let h = FrontHelper()
        do {
            try h.start(profile: p, socksBind: "off", probe: true)
            fputs("ok overlay=\(h.overlayIP)\n", stderr)
            h.stop()
        } catch {
            fputs("fail \(error.localizedDescription)\n", stderr)
            failed += 1
        }
    }
    exit(failed == 0 ? 0 : 2)
}

if CommandLine.arguments.contains("--shot") {
    let app = NSApplication.shared
    app.setActivationPolicy(.accessory)
    L10n.lang = .ru
    let engine = SessionController()
    // Load prefs only. `boot()` reaps loft-front / loft-priv and must not run beside a live session.
    engine.store.load()
    let dir = "/tmp/loft-shots"
    try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
    /// Writes `view` as PNG under `/tmp/loft-shots/{name}.png`.
    func dump(_ view: NSView, _ name: String) {
        view.layoutSubtreeIfNeeded()
        let b = view.bounds
        guard b.width > 1, b.height > 1 else { return }
        guard let rep = view.bitmapImageRepForCachingDisplay(in: b) else { return }
        view.cacheDisplay(in: b, to: rep)
        if let data = rep.representation(using: .png, properties: [:]) {
            try? data.write(to: URL(fileURLWithPath: "\(dir)/\(name).png"))
            fputs("wrote \(dir)/\(name).png \(Int(b.width))x\(Int(b.height))\n", stderr)
        }
    }
    for (name, on, err) in [("menu-off", false, false), ("menu-on", true, false), ("menu-err", false, true)] {
        let img = Palette.menuIcon(on: on, err: err)
        img.size = NSSize(width: 36, height: 36)
        if let tiff = img.tiffRepresentation,
           let rep = NSBitmapImageRep(data: tiff),
           let data = rep.representation(using: .png, properties: [:]) {
            try? data.write(to: URL(fileURLWithPath: "\(dir)/\(name).png"))
            fputs("wrote \(dir)/\(name).png\n", stderr)
        }
    }

    let pop = PopoverView(frame: .zero)
    let psz = pop.render(engine: engine)
    pop.frame = NSRect(origin: .zero, size: psz)
    let popWin = NSWindow(contentRect: pop.frame, styleMask: [.borderless], backing: .buffered, defer: false)
    popWin.contentView = pop
    popWin.orderBack(nil)
    dump(pop, "popover")

    let about = AboutWindowController(software: engine.software)
    about.showWindow(nil)
    if let v = about.window?.contentView { dump(v, "about") }

    let onboard = OnboardWindowController(engine: engine)
    onboard.showWindow(nil)
    if let v = onboard.window?.contentView { dump(v, "onboard") }

    let status = StatusWindowController(engine: engine)
    status.showWindow(nil)
    if let v = status.window?.contentView { dump(v, "status") }

    let list = ConnectionsWindowController(engine: engine)
    list.showWindow(nil)
    if let v = list.window?.contentView { dump(v, "list") }

    let set = SettingsWindowController(engine: engine)
    set.showWindow(nil)
    if let v = set.window?.contentView { dump(v, "settings") }

    engine.store.split = true
    engine.store.routes.seedShot()
    let split = SplitWindowController(engine: engine)
    split.showWindow(nil)
    if let v = split.window?.contentView { dump(v, "split") }
    split.shotZone("local")
    if let v = split.window?.contentView { dump(v, "split-local") }
    let splitin = SplitImportWindowController(engine: engine, zone: "global")
    splitin.showWindow(nil)
    if let v = splitin.window?.contentView { dump(v, "splitin") }
    let splitout = SplitExportWindowController(engine: engine, zone: "global")
    splitout.showWindow(nil)
    if let v = splitout.window?.contentView { dump(v, "splitout") }
    let splitrepo = SplitRepoWindowController(engine: engine, zone: "global")
    splitrepo.showWindow(nil)
    if let v = splitrepo.window?.contentView { dump(v, "splitrepo") }
    let splitlists = SplitListWindowController(engine: engine, back: .repo("global"))
    splitlists.showWindow(nil)
    if let v = splitlists.window?.contentView { dump(v, "splitlists") }
    let splitadd = SplitAddWindowController(engine: engine, set: nil, zone: "global")
    splitadd.showWindow(nil)
    if let v = splitadd.window?.contentView { dump(v, "splitadd") }
    let zones = SplitZonesWindowController(engine: engine)
    zones.showWindow(nil)
    if let v = zones.window?.contentView { dump(v, "zones") }
    let zoneedit = SplitZoneEditWindowController(engine: engine, zone: "local")
    zoneedit.showWindow(nil)
    if let v = zoneedit.window?.contentView { dump(v, "zoneedit") }
    let zoneadd = SplitZoneAddWindowController(engine: engine, draft: SplitZoneDraft())
    zoneadd.showWindow(nil)
    if let v = zoneadd.window?.contentView { dump(v, "zoneadd") }

    if let p = engine.store.active() {
        let edit = EditWindowController(engine: engine, profile: p)
        edit.showWindow(nil)
        if let v = edit.window?.contentView { dump(v, "edit") }
        edit.shotLoftManual()
        if let v = edit.window?.contentView { dump(v, "edit-loft") }
        edit.shotTab(1)
        if let v = edit.window?.contentView { dump(v, "edit-adv") }
        var vp = p
        vp.kind = "vless"
        vp.url = "vless://11111111-1111-1111-1111-111111111111@loft.example.com:444?security=tls&fp=chrome&type=tcp&sni=www.example.com"
        vp.token = "11111111-1111-1111-1111-111111111111"
        let editV = EditWindowController(engine: engine, profile: vp)
        editV.showWindow(nil)
        editV.shotManual()
        if let v = editV.window?.contentView { dump(v, "edit-vless") }
    }

    // Several on: one popover and one status window walk through accordion states, so a row
    // left over from an earlier state would show up overlapping the next shot.
    let ids = engine.store.profiles.map { $0.id }
    if ids.count >= 3 {
        let (a, b, c) = (ids[0], ids[1], ids[2])
        let now = Date()
        let day = now.addingTimeInterval(-90_061)
        engine.store.multi = true
        let accPop = PopoverView(frame: .zero)
        let accWin = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 300, height: 300), styleMask: [.borderless], backing: .buffered, defer: false)
        accWin.contentView = accPop
        accWin.orderBack(nil)
        let accStatus = StatusWindowController(engine: engine)
        accStatus.showWindow(nil)
        typealias Step = (String, () -> Void)
        let steps: [Step] = [
            ("1-main-on-one", { engine.shotMain(""); engine.shotSet(a, .on, since: now.addingTimeInterval(-720), fresh: 3); engine.shotSet(b, .on, since: now.addingTimeInterval(-24), fresh: 2); engine.shotMain(a) }),
            ("2-three-err", { engine.shotSet(c, .error, err: "сервер не отвечает", fresh: 1) }),
            ("3-main-err", { engine.shotSet(a, .error, err: "сервер не отвечает") }),
            ("4-prefer-b", { engine.shotMain(b) }),
            ("5-connecting", { engine.shotSet(a, .connecting); engine.shotSet(c, .connecting) }),
            ("6-others-gone", { engine.shotSet(a, .off); engine.shotSet(c, .off) }),
            ("7-back-days", { engine.shotSet(a, .on, since: day, fresh: 5) }),
            ("8-multi-off", { engine.store.multi = false }),
            ("9-multi-on", { engine.store.multi = true }),
            ("10-simple", { engine.store.simple = true }),
            ("11-full", { engine.store.simple = false }),
            ("12-all-off", { engine.shotMain("") }),
        ]
        for (name, step) in steps {
            step()
            let sz = accPop.render(engine: engine)
            accWin.setContentSize(sz)
            accPop.frame = NSRect(origin: .zero, size: sz)
            dump(accPop, "acc-pop-\(name)")
            accStatus.reload()
            if let v = accStatus.window?.contentView { dump(v, "acc-status-\(name)") }
        }
        engine.store.multi = false
        engine.store.simple = false
        engine.shotMain("")
    }

    LoftLog.shared.add("loft", "open node-ws ws wss://node.example.com/api/v1/sync path=/static/js")
    LoftLog.shared.add("front", "spawn ws wss://node.example.com/api/v1/sync path=/static/js socks=127.0.0.1:1080 tun=1")
    LoftLog.shared.add("front", L10n.t("err_h2_mask"))
    LoftLog.shared.add("front", "STATUS state=on ip=10.8.0.2 tun=0")
    LoftLog.shared.add("front", "tun: operation not permitted")
    LoftLog.shared.add("loft", "on overlay=10.8.0.2 tun=0 socks=127.0.0.1:1080")
    let journal = JournalWindowController()
    journal.showWindow(nil)
    if let v = journal.window?.contentView { dump(v, "journal") }

    exit(0)
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
