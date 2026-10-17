// Menubar app: status item, popover, windows, and SessionController that talks to loft-front.

import AppKit

/// Menubar app: status item, popover, windows, and SessionController that talks to loft-front.
final class AppDelegate: NSObject, NSApplicationDelegate {
    /// The running app delegate, if NSApp has one.
    static var shared: AppDelegate? { NSApp.delegate as? AppDelegate }

    let engine = SessionController()
    private var item: NSStatusItem!
    private var popView: PopoverView!
    private var popPanel: NSPanel!
    private var aboutWC: AboutWindowController?
    private var listWC: ConnectionsWindowController?
    private var settingsWC: SettingsWindowController?
    private var editWC: EditWindowController?
    private var onboardWC: OnboardWindowController?
    private var journalWC: JournalWindowController?
    private var splitWC: SplitWindowController?
    private var splitImportWC: SplitImportWindowController?
    private var splitExportWC: SplitExportWindowController?
    private var splitRepoWC: SplitRepoWindowController?
    private var splitAddWC: SplitAddWindowController?
    private var localMon: Any?
    private var globalMon: Any?
    private var sparkTimer: Timer?
    private var menuLang: L10n.Lang?

    /// Boots the engine, status item, popover, and optional auto-connect.
    func applicationDidFinishLaunching(_ notification: Notification) {
        engine.boot()
        L10n.apply(engine.store.lang)
        installKeyMenu()

        item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        if let btn = item.button {
            btn.image = Palette.menuIcon(on: false)
            btn.image?.isTemplate = false
            btn.target = self
            btn.action = #selector(togglePopover(_:))
        }

        popView = PopoverView(frame: .zero)
        popView.onToggle = { [weak self] in self?.tapConnect() }
        popView.onOpen = { [weak self] in self?.hidePopover(); self?.showList() }
        popView.onQuit = { NSApp.terminate(nil) }
        popView.onReset = { [weak self] in self?.engine.resetRoutes() }
        popView.onSwitch = { [weak self] in self?.hidePopover(); self?.showList() }
        popView.onJournal = { [weak self] in self?.hidePopover(); self?.showJournal() }
        popView.onSettings = { [weak self] in self?.hidePopover(); self?.showSettings() }
        popView.onEdit = { [weak self] id in
            guard let p = self?.engine.store.profiles.first(where: { $0.id == id }) else { return }
            self?.hidePopover()
            self?.showEdit(p)
        }
        _ = popView.render(engine: engine)

        popPanel = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 300, height: 280),
            styleMask: [.borderless, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        popPanel.isOpaque = false
        popPanel.backgroundColor = .clear
        popPanel.hasShadow = true
        popPanel.level = .popUpMenu
        popPanel.isReleasedWhenClosed = false
        popPanel.hidesOnDeactivate = false
        popPanel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        popView.autoresizingMask = [.width, .height]
        popPanel.contentView = popView

        NotificationCenter.default.addObserver(forName: NSWindow.willCloseNotification, object: nil, queue: .main) { [weak self] note in
            guard note.object is LoftWindow else { return }
            DispatchQueue.main.async { self?.syncDock() }
        }

        NotificationCenter.default.addObserver(forName: .loftState, object: nil, queue: .main) { [weak self] _ in
            self?.refresh()
        }

        NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didWakeNotification, object: nil, queue: .main
        ) { [weak self] _ in
            self?.engine.noteWake()
        }
        NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.screensDidWakeNotification, object: nil, queue: .main
        ) { [weak self] _ in
            self?.engine.networkChanged()
        }

        sparkTimer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] _ in
            guard let self = self else { return }
            self.engine.notePath()
            self.engine.tickRates()
            let st = self.engine.state
            if self.engine.reconnectLeft != nil || self.engine.store.profiles.contains(where: { self.engine.reconnectLeft(of: $0.id) != nil }) {
                NotificationCenter.default.post(name: .loftState, object: self.engine)
                return
            }
            if st == .on || st == .connecting || st == .disconnecting || self.engine.anyOn {
                if self.popPanel.isVisible {
                    let sz = self.popView.render(engine: self.engine)
                    var f = self.popPanel.frame
                    let dy = sz.height - f.size.height
                    f.origin.y -= dy
                    f.size = sz
                    self.popPanel.setFrame(f, display: true)
                }
                self.listWC?.rebuild()
            }
        }
        if let t = sparkTimer { RunLoop.main.add(t, forMode: .common) }

        if engine.store.autoConnect, engine.store.active() != nil {
            engine.connect()
        }
        refresh()
        LoftLog.shared.diag = engine.store.diag
        syncDock()
    }

    /// Tears the session down so loft-priv and loft-front die before quit.
    func applicationWillTerminate(_ notification: Notification) {
        engine.halt()
    }

    /// Stays running with the status item when the last window closes.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    /// Dock click while a loft window exists: reopen the popover if none are visible.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if !flag { showPopover() }
        return true
    }

    /// True when a real loft window (not the popover) is on screen.
    private func hasLoftWindow() -> Bool {
        NSApp.windows.contains { $0 is LoftWindow && $0.isVisible }
    }

    /// Dock icon from the pref: always, only with a loft window, or never.
    /// Switching to accessory hides windows; loft windows are brought back.
    func syncDock() {
        let wantRegular: Bool
        switch engine.store.dock {
        case .always: wantRegular = true
        case .never: wantRegular = false
        case .window: wantRegular = hasLoftWindow()
        }
        let policy: NSApplication.ActivationPolicy = wantRegular ? .regular : .accessory
        guard NSApp.activationPolicy() != policy else { return }
        let popWas = popPanel.isVisible
        let keep = NSApp.windows.filter { $0 is LoftWindow && $0.isVisible }
        let key = keep.first { $0.isKeyWindow } ?? keep.first
        NSApp.setActivationPolicy(policy)
        let restore = { [weak self] in
            for w in keep { w.orderFrontRegardless() }
            if popWas { self?.popPanel.orderFrontRegardless() }
            key?.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
        }
        restore()
        DispatchQueue.main.async(execute: restore)
    }

    /// Hides the popover, shows `wc`, then applies the Dock pref.
    private func present(_ wc: NSWindowController?) {
        hidePopover()
        wc?.showWindow(nil)
        wc?.window?.makeKeyAndOrderFront(nil)
        syncDock()
        NSApp.activate(ignoringOtherApps: true)
    }

    /// App and Edit menus while loft is key (Dock / a loft window). Titles follow L10n.
    private func installKeyMenu() {
        if menuLang == L10n.lang, NSApp.mainMenu != nil { return }
        menuLang = L10n.lang
        let main = NSMenu()
        let appItem = NSMenuItem()
        let appMenu = NSMenu()
        appItem.submenu = appMenu
        let about = NSMenuItem(title: L10n.t("menu_about"), action: #selector(showAbout), keyEquivalent: "")
        about.target = self
        appMenu.addItem(about)
        appMenu.addItem(NSMenuItem.separator())
        let settings = NSMenuItem(title: L10n.t("settings"), action: #selector(showSettings), keyEquivalent: ",")
        settings.target = self
        appMenu.addItem(settings)
        let journal = NSMenuItem(title: L10n.t("journal"), action: #selector(showJournal), keyEquivalent: "")
        journal.target = self
        appMenu.addItem(journal)
        appMenu.addItem(NSMenuItem.separator())
        appMenu.addItem(withTitle: L10n.t("menu_hide"), action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        let hideOthers = NSMenuItem(title: L10n.t("menu_hide_others"), action: #selector(NSApplication.hideOtherApplications(_:)), keyEquivalent: "h")
        hideOthers.keyEquivalentModifierMask = [.command, .option]
        appMenu.addItem(hideOthers)
        appMenu.addItem(withTitle: L10n.t("menu_show_all"), action: #selector(NSApplication.unhideAllApplications(_:)), keyEquivalent: "")
        appMenu.addItem(NSMenuItem.separator())
        appMenu.addItem(withTitle: L10n.t("menu_quit"), action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        main.addItem(appItem)
        let editItem = NSMenuItem()
        let edit = NSMenu(title: L10n.t("menu_edit"))
        editItem.submenu = edit
        edit.addItem(withTitle: L10n.t("menu_cut"), action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.addItem(withTitle: L10n.t("copy"), action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.addItem(withTitle: L10n.t("menu_paste"), action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.addItem(NSMenuItem.separator())
        edit.addItem(withTitle: L10n.t("menu_select_all"), action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        main.addItem(editItem)
        NSApp.mainMenu = main
    }

    /// Shows or hides the status-item popover.
    @objc private func togglePopover(_ sender: Any?) {
        if popPanel.isVisible {
            hidePopover()
        } else {
            showPopover()
        }
    }

    /// Places the popover under the status item and starts click-outside monitors.
    private func showPopover() {
        guard let btn = item.button, let bw = btn.window else { return }
        let sz = popView.render(engine: engine)
        popPanel.setContentSize(sz)
        let br = btn.convert(btn.bounds, to: nil)
        let scr = bw.convertToScreen(br)
        var origin = NSPoint(x: scr.maxX - sz.width + 8, y: scr.minY - sz.height - 8)
        if let screen = bw.screen ?? NSScreen.main {
            origin.x = min(max(screen.visibleFrame.minX + 8, origin.x), screen.visibleFrame.maxX - sz.width - 8)
        }
        popPanel.setFrame(NSRect(origin: origin, size: sz), display: true)
        popPanel.orderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        installMonitors()
    }

    /// Hides the popover and drops click-outside monitors.
    func hidePopover() {
        popPanel.orderOut(nil)
        removeMonitors()
    }

    /// Local + global mouse-down monitors so a click outside dismisses the popover.
    private func installMonitors() {
        removeMonitors()
        localMon = NSEvent.addLocalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown]) { [weak self] e in
            self?.clickOutside(e)
            return e
        }
        globalMon = NSEvent.addGlobalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown]) { [weak self] e in
            self?.hidePopover()
            _ = e
        }
    }

    /// Removes local and global popover dismiss monitors.
    private func removeMonitors() {
        if let m = localMon { NSEvent.removeMonitor(m); localMon = nil }
        if let m = globalMon { NSEvent.removeMonitor(m); globalMon = nil }
    }

    /// Dismisses the popover unless the click is on it or the status item.
    private func clickOutside(_ e: NSEvent) {
        guard popPanel.isVisible else { return }
        if popView.trackingMenu { return }
        let screenPt: NSPoint
        if let w = e.window {
            screenPt = w.convertToScreen(NSRect(origin: e.locationInWindow, size: .zero)).origin
            if w === popPanel { return }
            if w === item.button?.window {
                let br = item.button?.convert(item.button!.bounds, to: nil) ?? .zero
                if br.contains(e.locationInWindow) { return }
            }
        } else {
            screenPt = NSEvent.mouseLocation
        }
        if popPanel.frame.contains(screenPt) { return }
        hidePopover()
    }

    /// Updates language, status-item icon, popover size, and the connections list.
    private func refresh() {
        L10n.apply(engine.store.lang)
        installKeyMenu()
        let on = engine.store.multi ? engine.anyOn : engine.state == .on
        let err = engine.store.multi
            ? (engine.anyError || engine.holdArmed)
            : (engine.state == .error || engine.holdArmed)
        item.button?.image = Palette.menuIcon(on: on, err: err)
        item.button?.image?.isTemplate = false
        if popPanel.isVisible {
            let sz = popView.render(engine: engine)
            var f = popPanel.frame
            let dy = sz.height - f.size.height
            f.origin.y -= dy
            f.size = sz
            popPanel.setFrame(f, display: true)
        }
        listWC?.rebuild()
    }

    /// Connects, or opens onboard if there is no profile yet.
    private func tapConnect() {
        if engine.store.profiles.isEmpty {
            hidePopover()
            showOnboard()
            return
        }
        engine.toggle()
    }

    /// «Открыть loft» opens the connections list. The old status pane showed one session.
    @objc func showStatus() {
        showList()
    }

    /// Shows the connections list window.
    @objc func showList() {
        if listWC == nil { listWC = ConnectionsWindowController(engine: engine) }
        listWC?.rebuild()
        present(listWC)
    }

    /// Shows the settings window.
    @objc func showSettings() {
        if settingsWC == nil { settingsWC = SettingsWindowController(engine: engine) }
        settingsWC?.reload()
        present(settingsWC)
    }

    /// Shows About, or brings the existing one forward.
    @objc func showAbout() {
        if aboutWC == nil { aboutWC = AboutWindowController(software: engine.software) }
        present(aboutWC)
        aboutWC?.checkNow()
    }

    /// Shows the journal window.
    @objc func showJournal() {
        if journalWC == nil { journalWC = JournalWindowController() }
        journalWC?.reload()
        present(journalWC)
    }

    /// Opens the profile editor for `p`.
    func showEdit(_ p: LoftProfile) {
        editWC = EditWindowController(engine: engine, profile: p)
        present(editWC)
    }

    /// Opens the split-routing lists. The Settings row is the door; the toggle stays on the row.
    func showSplit() {
        if splitWC == nil { splitWC = SplitWindowController(engine: engine) }
        splitWC?.reload()
        present(splitWC)
    }

    /// Keenetic / loft-routes import sheet.
    func showSplitImport() {
        splitImportWC = SplitImportWindowController(engine: engine)
        present(splitImportWC)
    }

    /// Keenetic / loft-routes export sheet.
    func showSplitExport() {
        splitExportWC = SplitExportWindowController(engine: engine)
        present(splitExportWC)
    }

    /// Raw .bat repository sheet.
    func showSplitRepo() {
        splitRepoWC = SplitRepoWindowController(engine: engine)
        present(splitRepoWC)
    }

    /// Add or edit one named CIDR set. `via` is the list tab when adding.
    func showSplitAdd(_ set: SplitSet?, via: Bool) {
        splitAddWC = SplitAddWindowController(engine: engine, set: set, via: via)
        present(splitAddWC)
    }

    /// Opens the two-step key/URL onboard window.
    func showOnboard() {
        onboardWC = OnboardWindowController(engine: engine)
        present(onboardWC)
    }

    /// Adds or removes a hidden login item via System Events.
    func setLogin(_ on: Bool) {
        let path = Bundle.main.bundlePath
        let quoted = path.replacingOccurrences(of: "\"", with: "\\\"")
        if on {
            let src = "tell application \"System Events\" to make login item at end with properties {path:\"\(quoted)\", hidden:true}"
            let p = Process()
            p.launchPath = "/usr/bin/osascript"
            p.arguments = ["-e", src]
            p.launch()
        } else {
            let name = (path as NSString).lastPathComponent.replacingOccurrences(of: ".app", with: "")
            let src = "tell application \"System Events\" to delete login item \"\(name)\""
            let p = Process()
            p.launchPath = "/usr/bin/osascript"
            p.arguments = ["-e", src]
            p.launch()
        }
    }
}
