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
    private var splitListsWC: SplitListWindowController?
    private var splitAddWC: SplitAddWindowController?
    private var splitZonesWC: SplitZonesWindowController?
    private var splitZoneEditWC: SplitZoneEditWindowController?
    private var splitZoneAddWC: SplitZoneAddWindowController?
    /// Draft for the add-zone sheet; survives a trip to the list catalog.
    var zoneAddDraft = SplitZoneDraft()
    private var updateOfferWC: UpdateOfferWindowController?
    private var launchUpdater: Updater?
    private var localMon: Any?
    private var globalMon: Any?
    private var sparkTimer: Timer?
    private var menuLang: L10n.Lang?
    private var connectionsMenu: NSMenu?
    private var splitMenuItem: NSMenuItem?
    private var refreshWork: DispatchWorkItem?
    private var lastIconOn: Bool?
    private var lastIconErr: Bool?

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
            self?.scheduleRefresh()
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
            let st = self.engine.state
            let live = st == .on || st == .connecting || st == .disconnecting || self.engine.anyOn
            let reconnect = self.engine.reconnectLeft != nil
                || self.engine.store.profiles.contains(where: { self.engine.reconnectLeft(of: $0.id) != nil })
            if live && self.uiShowsRates() { self.engine.tickRates() }
            if reconnect {
                self.scheduleRefresh()
                return
            }
            if live { self.tickLiveUI() }
        }
        if let t = sparkTimer { RunLoop.main.add(t, forMode: .common) }

        if engine.store.autoConnect, engine.store.active() != nil {
            engine.connect()
        }
        refresh()
        LoftLog.shared.diag = engine.store.diag
        syncDock()
        maybeOfferUpdate()
    }

    /// Tears the session down so loft-priv and loft-front die before quit.
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        engine.halt { NSApp.reply(toApplicationShouldTerminate: true) }
        return .terminateLater
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

    /// Menu bar while Loft is key: Loft | Connections | Tools | Edit.
    private func installKeyMenu() {
        if menuLang == L10n.lang, NSApp.mainMenu != nil {
            refreshConnectionsMenu()
            paintSplitMenuItem()
            return
        }
        menuLang = L10n.lang
        let main = NSMenu()
        let appItem = NSMenuItem()
        let appMenu = NSMenu()
        appItem.submenu = appMenu
        let about = NSMenuItem(title: L10n.t("menu_about"), action: #selector(showAbout), keyEquivalent: "")
        about.target = self
        appMenu.addItem(about)
        let openLoft = NSMenuItem(title: L10n.t("open"), action: #selector(showList), keyEquivalent: "")
        openLoft.target = self
        openLoft.image = Palette.openLoftMenuIcon()
        appMenu.addItem(openLoft)
        appMenu.addItem(NSMenuItem.separator())
        let langItem = NSMenuItem(title: L10n.t("menu_lang"), action: nil, keyEquivalent: "")
        langItem.image = Palette.langMenuIcon()
        let langMenu = NSMenu()
        langMenu.autoenablesItems = false
        for code in L10n.Lang.allCases {
            let it = NSMenuItem(title: code.nativeName, action: #selector(menuSetLang(_:)), keyEquivalent: "")
            it.target = self
            it.representedObject = code.rawValue
            it.state = code == L10n.lang ? .on : .off
            langMenu.addItem(it)
        }
        langItem.submenu = langMenu
        appMenu.addItem(langItem)
        appMenu.addItem(NSMenuItem.separator())
        appMenu.addItem(withTitle: L10n.t("menu_hide"), action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        let hideOthers = NSMenuItem(title: L10n.t("menu_hide_others"), action: #selector(NSApplication.hideOtherApplications(_:)), keyEquivalent: "h")
        hideOthers.keyEquivalentModifierMask = [.command, .option]
        appMenu.addItem(hideOthers)
        appMenu.addItem(withTitle: L10n.t("menu_show_all"), action: #selector(NSApplication.unhideAllApplications(_:)), keyEquivalent: "")
        appMenu.addItem(NSMenuItem.separator())
        appMenu.addItem(withTitle: L10n.t("menu_quit"), action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        main.addItem(appItem)
        let connItem = NSMenuItem()
        let connMenu = NSMenu(title: L10n.t("conns"))
        connMenu.autoenablesItems = false
        connItem.submenu = connMenu
        connectionsMenu = connMenu
        main.addItem(connItem)
        let toolsItem = NSMenuItem()
        let tools = NSMenu(title: L10n.t("menu_tools"))
        tools.autoenablesItems = false
        toolsItem.submenu = tools
        let settings = NSMenuItem(title: L10n.t("settings"), action: #selector(showSettings), keyEquivalent: ",")
        settings.target = self
        settings.image = Palette.settingsMenuIcon()
        tools.addItem(settings)
        let splitOn = NSMenuItem(title: L10n.t("split"), action: #selector(menuToggleSplit(_:)), keyEquivalent: "")
        splitOn.target = self
        splitMenuItem = splitOn
        paintSplitMenuItem()
        tools.addItem(splitOn)
        let split = NSMenuItem(title: L10n.t("nav.split"), action: #selector(showSplit), keyEquivalent: "")
        split.target = self
        split.image = Palette.routesMenuIcon()
        tools.addItem(split)
        let journal = NSMenuItem(title: L10n.t("journal"), action: #selector(showJournal), keyEquivalent: "")
        journal.target = self
        journal.image = Palette.journalMenuIcon()
        tools.addItem(journal)
        main.addItem(toolsItem)
        let editItem = NSMenuItem()
        let edit = NSMenu(title: L10n.t("menu_edit"))
        editItem.submenu = edit
        edit.addItem(withTitle: L10n.t("menu_cut"), action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.addItem(withTitle: L10n.t("copy"), action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.addItem(withTitle: L10n.t("menu_paste"), action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.addItem(NSMenuItem.separator())
        edit.addItem(withTitle: L10n.t("menu_select_all"), action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        main.addItem(editItem)
        refreshConnectionsMenu()
        NSApp.mainMenu = main
    }

    /// Fills **Connections → name →** Open/Disconnect, tunnel, default route, Edit.
    private func refreshConnectionsMenu() {
        guard let menu = connectionsMenu else { return }
        menu.removeAllItems()
        let rows = engine.profilesByFreshness()
        if rows.isEmpty {
            let empty = NSMenuItem(title: L10n.t("none"), action: nil, keyEquivalent: "")
            empty.isEnabled = false
            menu.addItem(empty)
            return
        }
        let simple = engine.store.simple
        for p in rows {
            let name = p.name.isEmpty ? Palette.hostOf(p.url) : p.name
            let it = NSMenuItem(title: name, action: nil, keyEquivalent: "")
            it.state = p.id == engine.displayID ? .on : .off
            it.submenu = connectionSubmenu(p, simple: simple)
            menu.addItem(it)
        }
    }

    /// Same actions as the popover switcher for one profile.
    private func connectionSubmenu(_ p: LoftProfile, simple: Bool) -> NSMenu {
        let sub = NSMenu()
        sub.autoenablesItems = false
        let st = engine.state(of: p.id)
        let act = NSMenuItem(title: engine.actionTitle(of: p.id), action: #selector(menuConnToggle(_:)), keyEquivalent: "")
        act.target = self
        act.representedObject = p.id
        act.isEnabled = st != .disconnecting
        sub.addItem(act)
        if !simple {
            sub.addItem(.separator())
            let tun = NSMenuItem(title: L10n.t("net_tag"), action: #selector(menuConnTun(_:)), keyEquivalent: "")
            tun.target = self
            tun.representedObject = p.id
            tun.state = p.tun ? .on : .off
            tun.isEnabled = p.tun ? p.socksOn : true
            sub.addItem(tun)
            let route = NSMenuItem(title: L10n.t("default_route"), action: #selector(menuConnDefault(_:)), keyEquivalent: "")
            route.target = self
            route.representedObject = p.id
            route.state = engine.defaultArmed(of: p.id) ? .on : .off
            route.isEnabled = st == .on && engine.tunLive(of: p.id)
            sub.addItem(route)
        }
        sub.addItem(.separator())
        let setup = NSMenuItem(title: L10n.t("edit"), action: #selector(menuConnEdit(_:)), keyEquivalent: "")
        setup.target = self
        setup.representedObject = p.id
        sub.addItem(setup)
        return sub
    }

    @objc private func menuConnToggle(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        LoftLog.shared.tap("menu", sender.title)
        engine.toggle(id)
    }

    @objc private func menuConnTun(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        guard let p = engine.store.profiles.first(where: { $0.id == id }) else { return }
        engine.setProfileTun(id, on: !p.tun)
    }

    @objc private func menuConnDefault(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        engine.setSessionDefault(id, on: !engine.defaultArmed(of: id))
    }

    @objc private func menuConnEdit(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        guard let p = engine.store.profiles.first(where: { $0.id == id }) else { return }
        LoftLog.shared.tap("menu", "edit")
        showEdit(p)
    }

    /// Check sits in the icon column; `.state` would add a second indent for Tools.
    private func paintSplitMenuItem() {
        guard let item = splitMenuItem else { return }
        item.state = .off
        item.image = engine.store.split ? Palette.checkMenuIcon() : Palette.blankMenuIcon()
    }

    /// Tools → Split routing. Same persist path as the Settings toggle.
    @objc private func menuToggleSplit(_ sender: NSMenuItem) {
        engine.store.split.toggle()
        engine.store.savePrefs()
        engine.syncSplitRoutes()
        paintSplitMenuItem()
        settingsWCReload()
        NotificationCenter.default.post(name: .loftState, object: engine)
    }

    /// Loft → Language. Same persist path as the Settings picker.
    @objc private func menuSetLang(_ sender: NSMenuItem) {
        guard let code = sender.representedObject as? String,
              let next = L10n.Lang(rawValue: code),
              next != L10n.lang else { return }
        engine.store.lang = next.rawValue
        L10n.lang = next
        engine.store.savePrefs()
        settingsWCReload()
        NotificationCenter.default.post(name: .loftState, object: engine)
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
        popPanel.setFrame(NSRect(origin: origin, size: sz), display: false)
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

    /// Coalesces `.loftState` so handshake stages share one frame.
    private func scheduleRefresh() {
        refreshWork?.cancel()
        let w = DispatchWorkItem { [weak self] in self?.refresh() }
        refreshWork = w
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.03, execute: w)
    }

    /// True when a visible surface is showing byte rates or uptime.
    private func uiShowsRates() -> Bool {
        popPanel.isVisible || listWC?.window?.isVisible == true
    }

    /// Uptime / rates / reconnect titles without regenerating the menu icon.
    private func tickLiveUI() {
        if popPanel.isVisible {
            fitPopover(popView.render(engine: engine))
        }
        if listWC?.window?.isVisible == true {
            listWC?.rebuild()
        }
    }

    /// Resizes the popover only when the fitted size actually changed.
    private func fitPopover(_ sz: NSSize) {
        var f = popPanel.frame
        if abs(sz.height - f.size.height) < 0.5 && abs(sz.width - f.size.width) < 0.5 { return }
        f.origin.y -= sz.height - f.size.height
        f.size = sz
        popPanel.setFrame(f, display: false)
    }

    /// Updates language, status-item icon, popover size, and the connections list.
    private func refresh() {
        L10n.apply(engine.store.lang)
        installKeyMenu()
        let on = engine.store.multi ? engine.anyOn : engine.state == .on
        let err = engine.store.multi
            ? (engine.anyError || engine.holdArmed)
            : (engine.state == .error || engine.holdArmed)
        if lastIconOn != on || lastIconErr != err {
            item.button?.image = Palette.menuIcon(on: on, err: err)
            item.button?.image?.isTemplate = false
            lastIconOn = on
            lastIconErr = err
        }
        if popPanel.isVisible {
            fitPopover(popView.render(engine: engine))
        }
        if listWC?.window?.isVisible == true {
            listWC?.rebuild()
        }
        if splitWC?.window?.isVisible == true {
            splitWC?.reload()
        }
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

    /// Open Loft opens the connections list. The old status pane showed one session.
    @objc func showStatus() {
        showList()
    }

    /// Shows the connections list window.
    @objc func showList() {
        if listWC == nil { listWC = ConnectionsWindowController(engine: engine) }
        present(listWC)
        listWC?.rebuild()
    }

    /// Reloads Settings if that window is open (quiet checkbox wrote prefs).
    func settingsWCReload() {
        settingsWC?.reload()
    }

    /// GitHub latest in the background when the launch pref is on. Dialog only if a newer DMG exists.
    private func maybeOfferUpdate() {
        guard engine.store.checkUpdates else { return }
        if launchUpdater == nil { launchUpdater = Updater() }
        launchUpdater?.check(current: engine.software) { [weak self] result in
            guard let self = self, self.engine.store.checkUpdates else { return }
            if self.aboutWC?.window?.isVisible == true { return }
            if self.updateOfferWC?.window?.isVisible == true { return }
            guard case .success(let offer?) = result else { return }
            self.showUpdateOffer(offer)
        }
    }

    /// Offer window: Update / Later / don’t ask again.
    private func showUpdateOffer(_ offer: Updater.Offer) {
        if updateOfferWC == nil || updateOfferWC?.window?.isVisible != true {
            updateOfferWC = UpdateOfferWindowController(offer: offer)
        }
        present(updateOfferWC)
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
        aboutWC?.reload()
        present(aboutWC)
    }

    /// Shows the journal window.
    @objc func showJournal() {
        if journalWC == nil { journalWC = JournalWindowController() }
        present(journalWC)
        journalWC?.reload()
    }

    /// Opens the profile editor for `p`.
    func showEdit(_ p: LoftProfile) {
        editWC = EditWindowController(engine: engine, profile: p)
        present(editWC)
    }

    /// Opens the split-routing lists (Settings, the loft window, and the loft menu).
    @objc func showSplit() {
        if splitWC == nil { splitWC = SplitWindowController(engine: engine) }
        present(splitWC)
        splitWC?.reload()
    }

    /// Keenetic / loft-routes import sheet. `zone` is the default when the file has no `REM zone:`.
    func showSplitImport(zone: String) {
        splitImportWC = SplitImportWindowController(engine: engine, zone: zone)
        present(splitImportWC)
    }

    /// Keenetic / loft-routes export sheet. `zone` is the Keenetic bag when that format is chosen.
    func showSplitExport(zone: String) {
        splitExportWC = SplitExportWindowController(engine: engine, zone: zone)
        present(splitExportWC)
    }

    /// Raw .bat repository sheet for one zone.
    func showSplitRepo(zone: String) {
        splitRepoWC = SplitRepoWindowController(engine: engine, zone: zone)
        present(splitRepoWC)
    }

    /// Catalog of lists in the loft repository. A row fills that zone’s URL (or the add-zone draft).
    func showSplitLists(back: SplitListBack) {
        splitListsWC = SplitListWindowController(engine: engine, back: back)
        present(splitListsWC)
    }

    /// Add or edit one named CIDR set. `zone` is membership, not direction.
    func showSplitAdd(_ set: SplitSet?, zone: String) {
        splitAddWC = SplitAddWindowController(engine: engine, set: set, zone: zone)
        present(splitAddWC)
    }

    /// Zone list: Local, Global, then any added zones.
    func showSplitZones() {
        splitZonesWC = SplitZonesWindowController(engine: engine)
        present(splitZonesWC)
        splitZonesWC?.reload()
    }

    /// Edit one zone’s name, direction, and repository.
    func showSplitZoneEdit(_ id: String) {
        splitZoneEditWC = SplitZoneEditWindowController(engine: engine, zone: id)
        present(splitZoneEditWC)
    }

    /// New user zone. `fresh` clears the draft.
    func showSplitZoneAdd(fresh: Bool) {
        if fresh { zoneAddDraft = SplitZoneDraft() }
        splitZoneAddWC = SplitZoneAddWindowController(engine: engine, draft: zoneAddDraft)
        present(splitZoneAddWC)
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
            p.arguments = ["-e", src]
            try? p.start(at: "/usr/bin/osascript")
        } else {
            let name = (path as NSString).lastPathComponent.replacingOccurrences(of: ".app", with: "")
            let src = "tell application \"System Events\" to delete login item \"\(name)\""
            let p = Process()
            p.arguments = ["-e", src]
            try? p.start(at: "/usr/bin/osascript")
        }
    }
}
