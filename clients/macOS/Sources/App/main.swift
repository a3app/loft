// Process entry for the loft macOS menubar app.
// `--probe` runs loft-front once and exits; `--shot` dumps UI PNGs; otherwise NSApp runs.

import AppKit

if CommandLine.arguments.contains("--probe") {
    let engine = SessionController()
    engine.boot()
    guard let p = engine.store.active() else {
        fputs("no profile\n", stderr)
        exit(1)
    }
    fputs("probe \(p.name) \(p.kind) \(p.url) \(p.path)\n", stderr)
    do {
        let h = FrontHelper()
        try h.start(profile: p, socksBind: engine.store.socksBind(for: p), probe: true)
        fputs("ok overlay=\(h.overlayIP)\n", stderr)
        h.stop()
        exit(0)
    } catch {
        fputs("fail \(error.localizedDescription)\n", stderr)
        exit(2)
    }
}

if CommandLine.arguments.contains("--shot") {
    let app = NSApplication.shared
    app.setActivationPolicy(.accessory)
    L10n.lang = .ru
    let engine = SessionController()
    engine.boot()
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

    engine.store.routes.seedShot()
    let split = SplitWindowController(engine: engine)
    split.showWindow(nil)
    if let v = split.window?.contentView { dump(v, "split") }
    let splitin = SplitImportWindowController(engine: engine)
    splitin.showWindow(nil)
    if let v = splitin.window?.contentView { dump(v, "splitin") }
    let splitout = SplitExportWindowController(engine: engine)
    splitout.showWindow(nil)
    if let v = splitout.window?.contentView { dump(v, "splitout") }
    let splitrepo = SplitRepoWindowController(engine: engine)
    splitrepo.showWindow(nil)
    if let v = splitrepo.window?.contentView { dump(v, "splitrepo") }
    let splitadd = SplitAddWindowController(engine: engine, set: nil, via: true)
    splitadd.showWindow(nil)
    if let v = splitadd.window?.contentView { dump(v, "splitadd") }

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
        vp.url = "vless://11111111-1111-1111-1111-111111111111@loft.iqos.help:444?security=tls&fp=chrome&type=tcp&sni=www.example.com"
        vp.token = "11111111-1111-1111-1111-111111111111"
        let editV = EditWindowController(engine: engine, profile: vp)
        editV.showWindow(nil)
        editV.shotManual()
        if let v = editV.window?.contentView { dump(v, "edit-vless") }
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
