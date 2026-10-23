// Session owner: loft-front via FrontHelper, TUN via loft-priv, reconnect, and UI state.
// Several outbounds (`prefs.multi`): one LiveSession per profile, each with its own loft-front and utun.

import AppKit
import Foundation

extension Notification.Name {
    /// Posted on the main queue when connect state, overlay, or prefs change.
    static let loftState = Notification.Name("loft.state")
}

/// Per-profile outbound: loft-front process, overlay, optional utun.
final class LiveSession {
    let id: String
    var state: SessionController.State = .off
    var errorText = ""
    var overlayIP = ""
    var frontName = ""
    var tunLive = false
    var defaultArmed = false
    var socksDisplay = ""
    var upRate = 0
    var downRate = 0
    var connectedAt: Date?
    var progressText = ""
    var progressParts: [String] = []
    let helper = FrontHelper()
    var tunArmed = false
    var tunURL = ""
    var tunName = ""
    var generation = 0
    var reconnectWork: DispatchWorkItem?
    var reconnectAttempt = 0
    var reconnectAt: Date?
    /// Higher = more recently connected. Survives disconnect so the list stays sorted by freshness.
    var freshSeq: UInt64 = 0
    /// Public address of this outbound, from the echo API.
    var extIP = ""
    var extIPBusy = false
    var extIPGen = 0
    /// loft-front SOCKS bind used for echo-API and TCP probes, even when the profile hides SOCKS.
    var probeSocks = ""
    /// Mux died while loft-front still runs; 0/1 was dropped until resume.
    var muxDown = false

    init(id: String) { self.id = id }
}

/// Session owner: loft-front via FrontHelper, TUN via loft-priv, reconnect, and UI state.
final class SessionController {
    /// Connect lifecycle shown in the popover and status window.
    enum State: String {
        case off, connecting, disconnecting, on, error
    }

    let store = ProfileStore()
    private var lives: [String: LiveSession] = [:]
    private let relay = SocksRelay()
    private let queue = DispatchQueue(label: "app.loft.session")
    private var haltDone = false
    private var lastFail = ""
    /// Popover, advertised SOCKS, and default route follow this id (last successful connect).
    private var currentID = ""
    private var nextFresh: UInt64 = 0
    /// Last on-link physical gw seen by notePath (sleep/wake / Wi-Fi hop).
    private var lastPathGW = ""
    private var emptyGWTicks = 0
    private var pathWork: DispatchWorkItem?
    private var wakeProbe: DispatchWorkItem?

    /// `"ws"` for ws(s) URLs, otherwise `"get"`.
    static func kindForURL(_ url: String) -> String {
        let s = url.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if s.hasPrefix("ws://") || s.hasPrefix("wss://") { return "ws" }
        if s.hasPrefix("vless://") { return "vless" }
        if s.hasPrefix("trojan://") { return "trojan" }
        return "get"
    }

    var software: String {
        (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "1.0.22"
    }

    /// Popover session: last successful connect, else the list pick.
    var displayID: String { currentID.isEmpty ? store.activeID : currentID }

    private var displayLive: LiveSession? { lives[displayID] }

    /// Profile shown in the popover.
    func displayProfile() -> LoftProfile? {
        store.profiles.first(where: { $0.id == displayID }) ?? store.active()
    }

    var state: State { state(of: displayID) }
    var errorText: String { displayLive?.errorText ?? "" }
    var overlayIP: String { displayLive?.overlayIP ?? "" }
    var frontName: String { displayLive?.frontName ?? "" }
    var tunLive: Bool { displayLive?.tunLive ?? false }
    var defaultArmed: Bool { lives.values.contains { $0.defaultArmed } }
    var socksDisplay: String {
        if store.multi, store.sharedSocks, relay.isListening {
            return "127.0.0.1:\(store.socksPort)"
        }
        return displayLive?.socksDisplay ?? ""
    }
    var upRate: Int { displayLive?.upRate ?? 0 }
    var downRate: Int { displayLive?.downRate ?? 0 }
    var connectedAt: Date? { displayLive?.connectedAt }
    var progressText: String { displayLive?.progressText ?? "" }

    /// True when any outbound is up.
    var anyOn: Bool { lives.values.contains { $0.state == .on } }
    /// True when any outbound is in error.
    var anyError: Bool { lives.values.contains { $0.state == .error } }
    /// Count of live outbounds.
    var liveCount: Int { lives.values.filter { $0.state == .on }.count }

    private var splitGen = 0

    /// Loads profiles, installs loft-priv if needed, and reaps stale holders.
    func boot() {
        store.load()
        try? Priv.ensureSetuid()
        FrontHelper.reapOrphans()
        Priv.reapStale()
        lastPathGW = Priv.cachedGateway()
        Priv.onPathChange = { [weak self] in self?.networkChanged() }
        Priv.startPathWatch()
        store.routes.onChange = { [weak self] in self?.syncSplitRoutes() }
        syncSplitRoutes()
        store.routes.maybePull()
    }

    /// Installs or clears extra zone CIDRs in the system table.
    /// Around-loft always, while split is on. Through-loft whenever TUN is live:
    /// 0/1 is not enough — a more-specific around-loft prefix (LAN 10/8, a
    /// regional list) would steal Global destinations that must stay on utun.
    func syncSplitRoutes() {
        splitGen += 1
        let gen = splitGen
        queue.async { [weak self] in
            guard let self = self, gen == self.splitGen else { return }
            self.applySplitRoutes()
        }
    }

    /// Computes the two bags and asks loft-priv to diff them into the kernel.
    private func applySplitRoutes() {
        if !store.split {
            Priv.clearSplit()
            return
        }
        let owner: LiveSession? = {
            if let s = lives[currentID], s.tunArmed, s.state == .on { return s }
            return lives.values
                .filter { $0.tunArmed && $0.state == .on }
                .max { $0.freshSeq < $1.freshSeq }
        }()
        let tunLive = owner != nil
        let ifname = owner?.tunName ?? ""
        let gw = Priv.savedGW()
        let via: [String]
        if tunLive && !ifname.isEmpty {
            var extra = store.routes.bagCIDRs(via: true)
            // Overlay /24 is already on utun at tun up. Putting it in the
            // via bag lets --clear-split delete the member subnet.
            let overlayIP = owner?.helper.overlayIP ?? ""
            func skipOverlay(_ c: String) -> Bool {
                if overlayIP.isEmpty { return false }
                let host = c.split(separator: "/").first.map(String.init) ?? c
                let bits = c.split(separator: "/").dropFirst().first
                if let bits, bits != "24" { return false }
                return Priv.sameSlash24(host, overlayIP)
            }
            extra.removeAll(where: skipOverlay)
            if let routes = owner?.helper.overlayRoutes {
                for c in routes where !extra.contains(c) && !skipOverlay(c) {
                    extra.append(c)
                }
            }
            via = extra
        } else {
            via = []
        }
        // Equal prefix: through-loft wins. loft-priv also drops these from
        // the direct bag; filtering here keeps the want files honest.
        let viaSet = Set(via)
        let direct = store.routes.bagCIDRs(via: false).filter { !viaSet.contains($0) }
        Priv.applySplit(via: via, direct: direct, ifname: ifname, gw: gw)
    }

    /// Host pins for live/connecting/error sessions. Empty list clears leftover /32s.
    private func rebuildPins(except: String? = nil) {
        var ips: [String] = []
        var seen = Set<String>()
        for s in lives.values {
            if s.id == except { continue }
            switch s.state {
            case .on, .connecting, .error: break
            default: continue
            }
            let url: String = {
                if !s.tunURL.isEmpty { return s.tunURL }
                return store.profiles.first(where: { $0.id == s.id })?.url ?? ""
            }()
            if url.isEmpty { continue }
            for ip in Priv.excludeHosts(url: url, overlayIP: s.overlayIP) {
                if seen.insert(ip).inserted { ips.append(ip) }
            }
        }
        Priv.replacePins(ips)
    }

    /// Session for `id`, creating a slot if needed.
    private func ensure(_ id: String) -> LiveSession {
        if let s = lives[id] { return s }
        let s = LiveSession(id: id)
        lives[id] = s
        return s
    }

    /// Connect lifecycle of one profile.
    func state(of id: String) -> State { lives[id]?.state ?? .off }
    /// Overlay IP of one profile, or empty.
    func overlayIP(of id: String) -> String { lives[id]?.overlayIP ?? "" }
    /// SOCKS bind of one live profile, or empty.
    func socksDisplay(of id: String) -> String { lives[id]?.socksDisplay ?? "" }
    /// TUN lamp for one profile.
    func tunLive(of id: String) -> Bool { lives[id]?.tunLive ?? false }
    /// Error line for one profile.
    func errorText(of id: String) -> String { lives[id]?.errorText ?? "" }
    /// True when this profile currently owns the machine default route.
    func defaultArmed(of id: String) -> Bool { lives[id]?.defaultArmed ?? false }
    /// Echo API address of the popover session.
    var extIP: String { displayLive?.extIP ?? "" }
    /// True while the popover session is asking the echo API.
    var extIPBusy: Bool { displayLive?.extIPBusy ?? false }
    /// Echo API address of one profile.
    func extIP(of id: String) -> String { lives[id]?.extIP ?? "" }
    /// True while that profile is asking the echo API.
    func extIPBusy(of id: String) -> Bool { lives[id]?.extIPBusy ?? false }

    /// Connect time of one live profile.
    func connectedAt(of id: String) -> Date? { lives[id]?.connectedAt }

    /// `--shot` only: sets one session's state without spawning anything.
    func shotSet(_ id: String, _ st: State, since: Date? = nil, err: String = "", fresh: UInt64 = 0) {
        let s = ensure(id)
        s.state = st
        s.connectedAt = since
        s.errorText = err
        s.freshSeq = fresh
    }

    /// `--shot` only: makes `id` the main session; empty clears all fake sessions.
    func shotMain(_ id: String) {
        if id.isEmpty { lives.removeAll() }
        currentID = id
    }

    /// Several on: the other up, connecting, or failed sessions, freshest first.
    func otherLive() -> [LoftProfile] {
        guard store.multi else { return [] }
        return profilesByFreshness().filter { p in
            guard p.id != displayID else { return false }
            switch state(of: p.id) {
            case .on, .connecting, .error: return true
            case .off, .disconnecting: return false
            }
        }
    }

    /// Profiles for the Connections list: last connected first, then name.
    func profilesByFreshness() -> [LoftProfile] {
        store.profiles.sorted { a, b in
            let fa = lives[a.id]?.freshSeq ?? 0
            let fb = lives[b.id]?.freshSeq ?? 0
            if fa != fb { return fa > fb }
            return a.name.localizedCaseInsensitiveCompare(b.name) == .orderedAscending
        }
    }

    /// Other live session with the highest `freshSeq`.
    private func previousLive(besides id: String, tunOnly: Bool = false) -> LiveSession? {
        lives.values
            .filter { $0.id != id && $0.state == .on && (!tunOnly || $0.tunArmed) }
            .max { $0.freshSeq < $1.freshSeq }
    }

    /// Marks `s` as the freshest connect: popover, SOCKS relay, list pointer.
    private func markFresh(_ s: LiveSession) {
        nextFresh += 1
        s.freshSeq = nextFresh
        currentID = s.id
        store.select(s.id)
        updateRelay()
    }

    /// Shared `:socksPort` splices to the current loft-front unique SOCKS.
    private func updateRelay() {
        if !store.multi || !store.sharedSocks {
            relay.stop()
            return
        }
        let target: String
        if let s = lives[currentID], !s.socksDisplay.isEmpty {
            target = s.socksDisplay
        } else {
            target = ""
        }
        relay.setTarget(target)
        if target.isEmpty {
            relay.stop()
            return
        }
        do {
            try relay.start(listen: "127.0.0.1:\(store.socksPort)")
        } catch {
            LoftLog.shared.add("loft", "socks relay: \(error.localizedDescription)")
        }
    }

    /// Ports already bound by live loft-front SOCKS listeners.
    private func takenSocks(except: String = "") -> Set<UInt16> {
        var out = Set<UInt16>()
        if store.multi && store.sharedSocks {
            out.insert(UInt16(clamping: ProfileStore.clampPort(store.socksPort)))
        }
        for s in lives.values where s.id != except && (s.state == .on || s.state == .connecting) {
            let addr = s.probeSocks.isEmpty ? s.socksDisplay : s.probeSocks
            let p = Palette.socksPort(addr)
            if let u = UInt16(p), !addr.isEmpty { out.insert(u) }
        }
        return out
    }

    /// Loopback SOCKS for echo-API / TCP probes when the profile has SOCKS off.
    private func internalSocks(taken: Set<UInt16>) -> String {
        var p: UInt16 = 18080
        while taken.contains(p) {
            if p == 65535 { break }
            p += 1
        }
        return "127.0.0.1:\(p)"
    }

    /// Other live/connecting sessions besides `id`.
    private func othersBusy(besides id: String) -> [LiveSession] {
        lives.values.filter { $0.id != id && ($0.state == .on || $0.state == .connecting || $0.state == .disconnecting) }
    }

    /// Starts loft-front, then overlay TUN; default route only if SOCKS TCP works.
    func connect() { connect(nil) }

    /// Opens one profile. Exclusive mode tears the others first.
    func connect(_ id: String?) {
        let pid = (id?.isEmpty == false) ? id! : displayID
        guard let p0 = store.profiles.first(where: { $0.id == pid }) ?? store.active() else {
            fail(L10n.t("none"))
            return
        }
        var p = p0
        if !store.multi {
            store.select(p.id)
            let leftover = othersBusy(besides: p.id)
            if !leftover.isEmpty {
                for s in leftover { s.generation += 1 }
                set(p.id, .disconnecting, err: "", ip: overlayIP(of: p.id))
                queue.async {
                    for s in leftover { self.tearDown(s) }
                    DispatchQueue.main.async { self.connect(p.id) }
                }
                return
            }
        }
        let s = ensure(p.id)
        cancelReconnect(s)
        if s.state == .connecting { return }
        if p.url.isEmpty {
            fail(L10n.t("need_key"), id: p.id)
            return
        }
        if p.isSSTP {
            if p.user.isEmpty || p.token.isEmpty {
                fail(L10n.t("need_sstp"), id: p.id)
                return
            }
        } else if p.isVLESS {
            if p.token.isEmpty && !p.url.lowercased().hasPrefix("vless://") {
                fail(L10n.t("need_vless"), id: p.id)
                return
            }
        } else if p.isTrojan {
            if p.token.isEmpty && !p.url.lowercased().hasPrefix("trojan://") {
                fail(L10n.t("need_trojan"), id: p.id)
                return
            }
        } else if p.token.isEmpty {
            fail(L10n.t("need_key"), id: p.id)
            return
        }
        if !p.tun && !p.socksOn {
            fail(L10n.t("need_inbound"), id: p.id)
            return
        }
        s.generation += 1
        let ticket = s.generation
        if lives[currentID]?.state != .on {
            currentID = p.id
            store.select(p.id)
        }
        set(p.id, .connecting, err: "", ip: "")
        queue.async {
            if ticket != s.generation { return }
            s.helper.stop()
            do {
                if p.kind.isEmpty { p.kind = Self.kindForURL(p.url) }
                if p.isSSTP {
                    LoftLog.shared.add("loft", "open \(p.name) sstp \(p.url)")
                } else if p.isVLESS {
                    LoftLog.shared.add("loft", "open \(p.name) vless \(p.url)")
                } else if p.isTrojan {
                    LoftLog.shared.add("loft", "open \(p.name) trojan \(p.url)")
                } else {
                    LoftLog.shared.add("loft", "open \(p.name) \(p.kind) \(p.url) path=\(p.path)")
                }
                s.helper.onLine = { [weak self] line in
                    guard let self = self else { return }
                    if s.state == .connecting, let tok = FrontExplain.stageToken(from: line) {
                        DispatchQueue.main.async {
                            guard s.state == .connecting else { return }
                            self.appendStage(s, tok)
                            NotificationCenter.default.post(name: .loftState, object: self)
                        }
                        return
                    }
                    if s.state == .on {
                        if FrontExplain.isMuxDown(line) {
                            self.queue.async { self.noteMuxDown(s) }
                        } else if FrontExplain.isMuxUp(line) {
                            self.queue.async { self.noteMuxUp(s) }
                        }
                    }
                }
                s.helper.onExit = { [weak self] code in
                    guard let self = self else { return }
                    let raw = s.helper.lastError
                    DispatchQueue.main.async {
                        if s.state == .on || s.state == .connecting {
                            let shown = FrontExplain.exitMessage(lastError: raw, code: code)
                            LoftLog.shared.add("loft", "front died \(code) id=\(p.id) err=\(shown)")
                            self.queue.async {
                                self.tearDown(s, died: true)
                                DispatchQueue.main.async {
                                    self.set(p.id, .error, err: shown, ip: "")
                                    self.scheduleReconnect(s)
                                }
                            }
                        }
                    }
                }
                let advertised = self.store.socksBind(for: p, taken: self.takenSocks(except: p.id))
                let socksAddr = advertised.isEmpty ? self.internalSocks(taken: self.takenSocks(except: p.id)) : advertised
                Priv.pinFront(url: p.url)
                try s.helper.start(profile: p, socksBind: socksAddr, instanceFile: self.store.instanceFile(for: p.id))
                if ticket != s.generation { return }
                s.frontName = p.kind
                let ip = s.helper.overlayIP
                s.probeSocks = socksAddr
                s.socksDisplay = advertised
                s.tunLive = false
                s.tunArmed = false
                s.defaultArmed = false
                s.muxDown = false
                s.tunURL = p.url
                LoftLog.shared.add("loft", "on id=\(p.id) overlay=\(ip) socks=\(socksAddr)")
                self.markFresh(s)
                self.set(p.id, .on, err: "", ip: ip)
                if p.tun, !ip.isEmpty, !s.helper.tunSock.isEmpty {
                    do {
                        let takeDefault = self.store.defaultRoute
                        let dns = p.dnsSystem ? p.systemDNSCSV(overlay: s.helper.overlayDNS) : ""
                        try Priv.upTUN(sock: s.helper.tunSock, ip: ip, url: p.url, defaultRoute: false, dns: dns, mtu: s.helper.overlayMTU, key: p.id)
                        s.helper.waitTun(seconds: 8)
                        if ticket != s.generation { return }
                        let tunName = Priv.tunName(for: p.id)
                        if !s.helper.tunLive || tunName.isEmpty {
                            LoftLog.shared.add("loft", "tun: loft-front did not take fd id=\(p.id)")
                            let others = self.lives.values.contains { $0.id != p.id && ($0.tunArmed || $0.state == .on) }
                            Priv.downTUN(url: p.url, hold: false, ip: ip, restoreDefault: false, key: p.id, othersRemain: others)
                            self.rebuildPins()
                        } else {
                            s.tunLive = true
                            s.tunArmed = true
                            s.tunName = tunName
                            s.defaultArmed = false
                            LoftLog.shared.add("loft", "tun live id=\(p.id) if=\(s.tunName)")
                            LoftLog.shared.snapshot("live")
                            self.syncSystemDNS()
                            self.applySplitRoutes()
                            if takeDefault {
                                if socksAddr.isEmpty {
                                    LoftLog.shared.add("loft", "tun default skipped: no socks to probe tcp")
                                } else {
                                    let ifname = s.tunName
                                    Priv.netQueue.async {
                                        let ok = Priv.loftCarriesTCP(socksAddr)
                                        self.queue.async {
                                            if ticket != s.generation || !s.tunArmed { return }
                                            if ok {
                                                for o in self.lives.values where o.id != p.id { o.defaultArmed = false }
                                                Priv.setDefaultRoute(true, url: p.url, overlayIP: ip, ifname: ifname)
                                                s.defaultArmed = true
                                                LoftLog.shared.add("loft", "tun default on id=\(p.id) if=\(ifname)")
                                                self.applySplitRoutes()
                                            } else {
                                                LoftLog.shared.add("loft", L10n.t("err_no_tcp"))
                                            }
                                            DispatchQueue.main.async {
                                                NotificationCenter.default.post(name: .loftState, object: self)
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    } catch {
                        LoftLog.shared.add("loft", "tun: \(error.localizedDescription)")
                    }
                    if ticket != s.generation { return }
                    self.set(p.id, .on, err: "", ip: ip)
                }
                self.probeExtIP(s)
            } catch {
                if ticket != s.generation { return }
                let raw = error.localizedDescription
                if raw == "cancelled" { return }
                s.helper.stop()
                let shown = FrontExplain.display(raw)
                LoftLog.shared.add("loft", "error \(shown)")
                self.set(p.id, .error, err: shown, ip: "")
                self.rebuildPins()
                self.scheduleReconnect(s)
            }
        }
    }

    /// Makes `id` active. Exclusive live session reconnects onto it; multi sets priority.
    func selectProfile(_ id: String) {
        if id.isEmpty { return }
        if store.multi {
            prefer(id)
            return
        }
        if id == store.activeID { return }
        store.select(id)
        if anyOn || lives.values.contains(where: { $0.state == .connecting }) {
            reconnectAfterClose(to: id)
            return
        }
        NotificationCenter.default.post(name: .loftState, object: self)
    }

    /// Multi: this outbound owns the popover, shared SOCKS, and default route.
    /// Does not bump freshness — the menu and list keep their order.
    func prefer(_ id: String) {
        guard store.profiles.contains(where: { $0.id == id }) else { return }
        store.select(id)
        let s = ensure(id)
        if currentID != id {
            LoftLog.shared.add("loft", "prefer \(id)")
        }
        currentID = id
        updateRelay()
        syncSystemDNS()
        if s.state == .on, s.tunArmed, store.defaultRoute, !s.defaultArmed {
            setSessionDefault(id, on: true)
            return
        }
        NotificationCenter.default.post(name: .loftState, object: self)
    }

    /// Turns Several outbounds on or off. Off keeps only the current session.
    func setMulti(_ on: Bool) {
        store.multi = on
        store.savePrefs()
        if !on {
            let keep = (lives[currentID]?.state == .on) ? currentID : store.activeID
            store.select(keep)
            currentID = keep
            for s in lives.values where s.id != keep {
                if s.state == .on || s.state == .connecting || s.state == .error {
                    disconnect(s.id)
                }
            }
        }
        updateRelay()
        NotificationCenter.default.post(name: .loftState, object: self)
    }

    /// Profile TUN flag. A live session restarts so loft-front can open or drop the utun.
    func setProfileTun(_ id: String, on: Bool) {
        guard var p = store.profiles.first(where: { $0.id == id }) else { return }
        if !on && !p.socksOn { return }
        if p.tun == on { return }
        p.tun = on
        store.saveEdited(p)
        let st = state(of: id)
        if st == .on || st == .connecting || st == .error {
            restart(id)
        } else {
            NotificationCenter.default.post(name: .loftState, object: self)
        }
    }

    /// Saves `p`. SOCKS on a live session rebinds without a new handshake; TUN still restarts.
    func applyEdited(_ p: LoftProfile) {
        let wasTun = store.profiles.first(where: { $0.id == p.id })?.tun
        store.saveEdited(p)
        syncSystemDNS()
        let st = state(of: p.id)
        if st == .on || st == .connecting || st == .error {
            if wasTun != p.tun {
                restart(p.id)
                return
            }
            queue.async {
                self.applySocksLive(p)
                DispatchQueue.main.async {
                    NotificationCenter.default.post(name: .loftState, object: self)
                }
            }
            return
        }
        NotificationCenter.default.post(name: .loftState, object: self)
    }

    /// Rebinds loft-front SOCKS. Hidden SOCKS stays on an ephemeral port for TUN probes.
    private func applySocksLive(_ p: LoftProfile) {
        guard let s = lives[p.id], s.state == .on else { return }
        let advertised = store.socksBind(for: p, taken: takenSocks(except: p.id))
        let want = advertised.isEmpty ? "127.0.0.1:0" : advertised
        if !advertised.isEmpty, s.socksDisplay == advertised, s.probeSocks == advertised {
            return
        }
        if advertised.isEmpty, s.socksDisplay.isEmpty, !s.probeSocks.isEmpty {
            return
        }
        let gen = s.helper.socksGen()
        s.helper.send("socks \(want)")
        s.helper.waitSocks(after: gen, seconds: 2)
        let got = s.helper.socksListen()
        if s.helper.socksGen() == gen {
            LoftLog.shared.add("loft", "socks live id=\(p.id) bind-fail want=\(want) keep=\(s.probeSocks)")
            return
        }
        if advertised.isEmpty {
            if got == "off" || got.isEmpty {
                LoftLog.shared.add("loft", "socks live id=\(p.id) bind-fail keep=\(s.probeSocks)")
                return
            }
            s.probeSocks = got
            s.socksDisplay = ""
        } else if got != advertised && Palette.socksPort(got) != Palette.socksPort(advertised) {
            LoftLog.shared.add("loft", "socks live id=\(p.id) bind-fail want=\(advertised) keep=\(got)")
            return
        } else {
            s.probeSocks = got
            s.socksDisplay = got
        }
        LoftLog.shared.add("loft", "socks live id=\(p.id) bind=\(got) show=\(s.socksDisplay.isEmpty ? "off" : s.socksDisplay)")
        updateRelay()
    }

    /// Mux died while loft-front still runs: drop 0/1 so apps use en0 until resume.
    /// Hold keeps 0/1 (kill-switch).
    private func noteMuxDown(_ s: LiveSession) {
        if s.state != .on || !s.tunArmed { return }
        if s.muxDown { return }
        s.muxDown = true
        if store.hold { return }
        if !s.defaultArmed { return }
        Priv.setDefaultRoute(false, url: s.tunURL, overlayIP: s.overlayIP, ifname: s.tunName)
        s.defaultArmed = false
        LoftLog.shared.add("loft", "tun default off mux-down id=\(s.id)")
        applySplitRoutes()
        DispatchQueue.main.async {
            NotificationCenter.default.post(name: .loftState, object: self)
        }
    }

    /// Supervise finished a fresh handshake: put 0/1 back after TCP works.
    private func noteMuxUp(_ s: LiveSession) {
        if s.state != .on || !s.tunArmed || !s.muxDown { return }
        if store.hold || !store.defaultRoute || s.defaultArmed {
            s.muxDown = false
            return
        }
        let socks = s.probeSocks.isEmpty ? s.socksDisplay : s.probeSocks
        if socks.isEmpty {
            LoftLog.shared.add("loft", "tun default skipped: no socks to probe tcp")
            s.muxDown = false
            return
        }
        let ticket = s.generation
        let ifname = s.tunName
        let url = s.tunURL
        let ip = s.overlayIP
        let id = s.id
        Priv.netQueue.async {
            let ok = Priv.loftCarriesTCP(socks)
            self.queue.async {
                if ticket != s.generation || s.state != .on || !s.tunArmed { return }
                if !ok {
                    LoftLog.shared.add("loft", L10n.t("err_no_tcp"))
                    return
                }
                s.muxDown = false
                for o in self.lives.values where o.id != id { o.defaultArmed = false }
                Priv.setDefaultRoute(true, url: url, overlayIP: ip, ifname: ifname)
                s.defaultArmed = true
                LoftLog.shared.add("loft", "tun default on mux-up id=\(id) if=\(ifname)")
                self.applySplitRoutes()
                DispatchQueue.main.async {
                    NotificationCenter.default.post(name: .loftState, object: self)
                }
            }
        }
    }

    /// Puts or clears the machine default on a live TUN session.
    func setSessionDefault(_ id: String, on: Bool) {
        if on {
            store.defaultRoute = true
            store.savePrefs()
        }
        queue.async {
            guard let s = self.lives[id], s.state == .on, s.tunArmed else {
                DispatchQueue.main.async {
                    NotificationCenter.default.post(name: .loftState, object: self)
                }
                return
            }
            if on {
                if s.socksDisplay.isEmpty {
                    LoftLog.shared.add("loft", "tun default skipped: no socks to probe tcp")
                } else if !Priv.loftCarriesTCP(s.socksDisplay) {
                    LoftLog.shared.add("loft", L10n.t("err_no_tcp"))
                } else {
                    for o in self.lives.values where o.id != id { o.defaultArmed = false }
                    Priv.setDefaultRoute(true, url: s.tunURL, overlayIP: s.overlayIP, ifname: s.tunName)
                    s.defaultArmed = true
                }
            } else if s.defaultArmed {
                Priv.setDefaultRoute(false, url: s.tunURL, overlayIP: s.overlayIP, ifname: s.tunName)
                s.defaultArmed = false
            }
            self.applySplitRoutes()
            DispatchQueue.main.async {
                NotificationCenter.default.post(name: .loftState, object: self)
            }
        }
    }

    /// Tears a live/connecting session and opens it again with current profile flags.
    private func restart(_ id: String) {
        guard let s = lives[id] else {
            connect(id)
            return
        }
        cancelReconnect(s)
        s.reconnectAttempt = 0
        s.generation += 1
        set(id, .disconnecting, err: "", ip: overlayIP(of: id))
        queue.async {
            self.tearDown(s)
            DispatchQueue.main.async { self.connect(id) }
        }
    }

    /// Write or restore system DNS from the preferred live TUN that opted in.
    func syncSystemDNS() {
        queue.async { [weak self] in
            guard let self = self else { return }
            let owner: LiveSession? = {
                if let s = self.lives[self.currentID], s.tunArmed, s.state == .on,
                   let p = self.store.profiles.first(where: { $0.id == s.id }), p.dnsSystem {
                    return s
                }
                return self.lives.values
                    .filter { s in
                        s.tunArmed && s.state == .on &&
                        (self.store.profiles.first(where: { $0.id == s.id })?.dnsSystem == true)
                    }
                    .max { $0.freshSeq < $1.freshSeq }
            }()
            if let s = owner, let p = self.store.profiles.first(where: { $0.id == s.id }) {
                let csv = p.systemDNSCSV(overlay: s.helper.overlayDNS)
                if !csv.isEmpty {
                    Priv.applySystemDNS(csv)
                    return
                }
            }
            Priv.restoreSystemDNS()
        }
    }

    /// User disconnect: cancel reconnect and tear down on the session queue.
    func disconnect() { disconnect(nil) }

    /// Closes one profile, or the current popover session.
    func disconnect(_ id: String?) {
        let pid = (id?.isEmpty == false) ? id! : displayID
        let s = lives[pid]
        if s == nil || s?.state == .off || s?.state == .disconnecting { return }
        cancelReconnect(s)
        if let s = s { s.reconnectAttempt = 0; s.generation += 1 }
        set(pid, .disconnecting, err: "", ip: overlayIP(of: pid))
        queue.async {
            if let s = s { self.tearDown(s) }
            self.set(pid, .off, err: "", ip: "")
        }
    }

    /// Quit path: tearDown on the session queue. `done` runs on main when helpers are gone.
    func halt(done: (() -> Void)? = nil) {
        if haltDone {
            done?()
            return
        }
        haltDone = true
        store.flush()
        for s in lives.values {
            cancelReconnect(s)
            s.reconnectAttempt = 0
            s.generation += 1
        }
        queue.async {
            for s in self.lives.values { self.tearDown(s) }
            self.relay.stop()
            FrontHelper.reapOrphans()
            Priv.stopHeld(key: nil)
            DispatchQueue.main.async {
                for s in self.lives.values { self.set(s.id, .off, err: "", ip: "") }
                done?()
            }
        }
    }

    /// Cancels every pending reconnect timer.
    func cancelReconnect() {
        for s in lives.values { cancelReconnect(s) }
    }

    /// Stops the automatic reconnect countdown. Same as disconnect: the error is cleared.
    func stopReconnect(_ id: String? = nil) {
        let pid = (id?.isEmpty == false) ? id! : displayID
        guard let s = lives[pid] else { return }
        guard s.reconnectWork != nil || s.state == .error else { return }
        cancelReconnect(s)
        s.reconnectAttempt = 0
        s.generation += 1
        LoftLog.shared.add("loft", "reconnect stopped id=\(pid)")
        set(pid, .off, err: "", ip: "")
        queue.async {
            s.state = .off
            self.rebuildPins(except: pid)
        }
    }

    /// Cancels a pending reconnect timer for one session.
    private func cancelReconnect(_ s: LiveSession?) {
        s?.reconnectWork?.cancel()
        s?.reconnectWork = nil
        s?.reconnectAt = nil
    }

    /// Seconds left on the reconnect countdown of the popover session, or nil.
    var reconnectLeft: Int? { reconnectLeft(of: displayID) }

    /// Seconds left on the reconnect countdown, or nil if none.
    func reconnectLeft(of id: String) -> Int? {
        guard let s = lives[id], s.reconnectWork != nil, s.state == .error, let at = s.reconnectAt else { return nil }
        return max(1, Int(ceil(at.timeIntervalSinceNow)))
    }

    /// Caption for the main connect pill (current session).
    func actionTitle() -> String { actionTitle(of: displayID) }

    /// Open / Disconnect / Retry caption for one profile.
    func actionTitle(of id: String) -> String {
        switch state(of: id) {
        case .on: return L10n.t("disconnect")
        case .disconnecting: return L10n.t("disconnecting")
        case .connecting: return L10n.t("cancel")
        case .error:
            if let n = reconnectLeft(of: id) {
                return String(format: L10n.t("retry_wait"), n)
            }
            return L10n.t("retry")
        case .off: return L10n.t("pop_connect")
        }
    }

    /// Front died or handshake threw. Retry with 1/2/4/8/15s backoff while the pref is on.
    private func scheduleReconnect(_ s: LiveSession) {
        let go = {
            guard self.store.reconnect else { return }
            guard s.state == .error else { return }
            if s.reconnectWork != nil { return }
            s.reconnectWork?.cancel()
            let delay = min(15.0, 3.0 * pow(2.0, Double(min(s.reconnectAttempt, 2))))
            s.reconnectAttempt += 1
            s.reconnectAt = Date().addingTimeInterval(delay)
            LoftLog.shared.add("loft", "reconnect in \(Int(delay))s id=\(s.id)")
            let ticket = s.generation
            let work = DispatchWorkItem { [weak self] in
                guard let self = self else { return }
                guard ticket == s.generation else { return }
                guard self.store.reconnect else { return }
                self.connect(s.id)
            }
            s.reconnectWork = work
            DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
            NotificationCenter.default.post(name: .loftState, object: self)
        }
        if Thread.isMainThread { go() } else { DispatchQueue.main.async(execute: go) }
    }

    /// Stops an in-flight handshake. UI goes off immediately; teardown is async.
    func cancelConnect() { cancelConnect(displayID) }

    /// Cancels an in-flight handshake for one profile.
    func cancelConnect(_ id: String) {
        guard let s = lives[id], s.state == .connecting else { return }
        cancelReconnect(s)
        s.reconnectAttempt = 0
        s.generation += 1
        LoftLog.shared.add("loft", "cancel connect id=\(id)")
        s.helper.interrupt()
        set(id, .off, err: "", ip: "")
        queue.async {
            s.helper.stop()
            s.state = .off
            self.rebuildPins(except: id)
        }
    }

    /// Exclusive switch: close the live session, then open `id`.
    private func reconnectAfterClose(to id: String) {
        for s in lives.values {
            cancelReconnect(s)
            s.reconnectAttempt = 0
            s.generation += 1
        }
        set(store.activeID, .disconnecting, err: "", ip: overlayIP)
        queue.async {
            for s in self.lives.values { self.tearDown(s) }
            DispatchQueue.main.async { self.connect(id) }
        }
    }

    /// Drops 0/1 first if this session owned the split default, then loft-front, then loft-priv.
    private func tearDown(_ s: LiveSession, died: Bool = false) {
        LoftLog.shared.add("loft", "close id=\(s.id) armed=\(s.defaultArmed ? 1 : 0) hold=\(store.hold ? 1 : 0)")
        LoftLog.shared.snapshot("close")
        s.helper.onLine = nil
        s.helper.onExit = nil
        let ip = s.helper.overlayIP.isEmpty ? s.overlayIP : s.helper.overlayIP
        let others = lives.values.contains { $0.id != s.id && ($0.tunArmed || $0.state == .on) }
        let wasCurrent = s.id == currentID
        if s.defaultArmed { Priv.dropSplitDefault(ifdown: false) }
        if !died { s.helper.stop() }
        if s.tunArmed {
            let hold = store.hold && s.defaultArmed && !others
            Priv.downTUN(url: s.tunURL, hold: hold, ip: ip, restoreDefault: s.defaultArmed && !others, key: s.id, othersRemain: others)
            if s.defaultArmed, others, store.failover, store.defaultRoute {
                if let other = previousLive(besides: s.id, tunOnly: true) {
                    if !other.socksDisplay.isEmpty, Priv.loftCarriesTCP(other.socksDisplay) {
                        Priv.setDefaultRoute(true, url: other.tunURL, overlayIP: other.overlayIP, ifname: other.tunName)
                        other.defaultArmed = true
                    }
                }
            }
        }
        s.tunArmed = false
        s.defaultArmed = false
        s.muxDown = false
        s.tunLive = false
        s.tunName = ""
        s.socksDisplay = ""
        rebuildPins(except: s.id)
        syncSystemDNS()
        applySplitRoutes()
        if wasCurrent, store.failover, let other = previousLive(besides: s.id) {
            currentID = other.id
            store.select(other.id)
            LoftLog.shared.add("loft", "handoff \(s.id) → \(other.id)")
        }
        updateRelay()
    }

    /// Asks loft-priv to restore pre-loft routes (and drop a hold).
    /// 0/1 is dropped first so Reset cannot leave a utun loop.
    func resetRoutes() {
        queue.async {
            for s in self.lives.values { s.defaultArmed = false }
            Priv.refreshGateway()
            Priv.restoreRoutes()
            self.applySplitRoutes()
            DispatchQueue.main.async {
                NotificationCenter.default.post(name: .loftState, object: self)
            }
        }
    }

    /// Poll from the spark timer: physical gw changed (home Wi-Fi after a phone hotspot).
    /// Spawns are on the net queue; the session queue applies the result.
    func notePath() {
        Priv.netQueue.async { [weak self] in
            let gw = Priv.physicalGateway()
            self?.queue.async { self?.applyPath(gw) }
        }
    }

    /// Compares `gw` to the last hop and rebinds pins when the path moved.
    private func applyPath(_ gw: String) {
        if gw.isEmpty {
            emptyGWTicks += 1
            if emptyGWTicks < 3 { return }
        } else {
            emptyGWTicks = 0
        }
        if gw == lastPathGW { return }
        lastPathGW = gw
        let busy = lives.values.contains {
            $0.state == .on || $0.state == .connecting || $0.state == .error || $0.tunArmed
        }
        if busy { networkChanged() }
    }

    /// Lid-open: re-pin the front, then probe live sessions. A dead SSTP after
    /// sleep still looks .on until TCP finally errors; echo uses wall clock but
    /// the GUI probe does not wait for the 30s ticker.
    func noteWake() {
        LoftLog.shared.add("loft", "wake")
        networkChanged()
        wakeProbe?.cancel()
        let work = DispatchWorkItem { [weak self] in
            self?.probeLiveSessions(pass: 1)
        }
        wakeProbe = work
        queue.asyncAfter(deadline: .now() + 3.0, execute: work)
    }

    /// SOCKS CONNECT through each .on session. TCP runs on the net queue so a
    /// wedged SYN cannot stall disconnect/restore. Three passes (3s / 5s / 8s)
    /// after lid-open so DHCP/WPA is not a false death.
    private func probeLiveSessions(pass: Int) {
        struct Target { let id: String; let gen: Int; let socks: String }
        var targets: [Target] = []
        for s in lives.values where s.state == .on {
            let socks = !s.socksDisplay.isEmpty ? s.socksDisplay : s.probeSocks
            if socks.isEmpty { continue }
            targets.append(Target(id: s.id, gen: s.generation, socks: socks))
        }
        if targets.isEmpty { return }
        Priv.netQueue.async { [weak self] in
            let stale = targets.filter { !Priv.loftCarriesTCP($0.socks) }
            self?.queue.async { self?.finishWakeProbe(pass: pass, stale: stale.map { ($0.id, $0.gen) }) }
        }
    }

    /// Apply wake TCP results on the session queue. Passes 1–2 reschedule; pass 3 drops.
    private func finishWakeProbe(pass: Int, stale: [(String, Int)]) {
        var dead: [LiveSession] = []
        for (id, gen) in stale {
            guard let s = lives[id], s.state == .on, s.generation == gen else { continue }
            LoftLog.shared.add("loft", "wake stale id=\(id) pass=\(pass)")
            dead.append(s)
        }
        for s in lives.values where s.state == .on && !stale.contains(where: { $0.0 == s.id }) {
            LoftLog.shared.add("loft", "wake live id=\(s.id)")
        }
        if dead.isEmpty { return }
        if pass < 3 {
            let delay: TimeInterval = pass == 1 ? 5.0 : 8.0
            let work = DispatchWorkItem { [weak self] in
                self?.probeLiveSessions(pass: pass + 1)
            }
            wakeProbe = work
            queue.asyncAfter(deadline: .now() + delay, execute: work)
            return
        }
        for s in dead {
            guard s.state == .on else { continue }
            LoftLog.shared.add("loft", "wake drop id=\(s.id)")
            tearDown(s, died: true)
            DispatchQueue.main.async {
                self.set(s.id, .error, err: L10n.t("err_stale"), ip: "")
                self.scheduleReconnect(s)
            }
        }
    }

    /// Lid-open / DHCP: re-pin the front via any on-link hop so handshake can reach the server.
    func networkChanged() {
        Priv.flushGatewayCache()
        pathWork?.cancel()
        let work = DispatchWorkItem { [weak self] in
            self?.applyNetworkChange()
        }
        pathWork = work
        queue.asyncAfter(deadline: .now() + 0.4, execute: work)
    }

    /// Rebind exclude pins; do not drop 0/1 here (Reset Routes does that).
    private func applyNetworkChange() {
        LoftLog.shared.add("loft", "net change gw=\(Priv.physicalGateway())")
        LoftLog.shared.snapshot("net")
        Priv.refreshGateway()
        for s in lives.values {
            let url: String = {
                if !s.tunURL.isEmpty { return s.tunURL }
                return store.profiles.first(where: { $0.id == s.id })?.url ?? ""
            }()
            if url.isEmpty { continue }
            if s.state == .on || s.state == .connecting || s.state == .error || s.tunArmed {
                Priv.pinFront(url: url, overlayIP: s.overlayIP)
            }
        }
        applySplitRoutes()
        DispatchQueue.main.async {
            NotificationCenter.default.post(name: .loftState, object: self)
        }
    }

    /// Applies or clears the 0/1 split default if TUN is live and TCP works.
    func syncDefaultRoute() {
        let on = store.defaultRoute
        queue.async {
            let current = self.lives[self.currentID]
            let owner: LiveSession? = {
                if let s = current, s.tunArmed, s.state == .on { return s }
                return self.lives.values
                    .filter { $0.tunArmed && $0.state == .on }
                    .max { $0.freshSeq < $1.freshSeq }
            }()
            if let s = owner {
                if on && (s.socksDisplay.isEmpty || !Priv.loftCarriesTCP(s.socksDisplay)) {
                    LoftLog.shared.add("loft", s.socksDisplay.isEmpty
                        ? "tun default skipped: no socks to probe tcp"
                        : L10n.t("err_no_tcp"))
                    s.defaultArmed = false
                    LoftLog.shared.snapshot("default-skip")
                } else {
                    Priv.setDefaultRoute(on, url: s.tunURL, overlayIP: s.overlayIP, ifname: s.tunName)
                    s.defaultArmed = on
                    for o in self.lives.values where o.id != s.id { o.defaultArmed = false }
                }
            }
            self.applySplitRoutes()
            DispatchQueue.main.async {
                NotificationCenter.default.post(name: .loftState, object: self)
            }
        }
    }

    var tunName: String { displayLive?.tunName ?? Priv.tunName }
    var holdArmed: Bool {
        if anyOn { return false }
        if lives.values.contains(where: { $0.state == .connecting || $0.state == .disconnecting }) { return false }
        return store.hold && Priv.hasHolders()
    }

    /// Machine path for this connection. SOCKS is a local inbound, not the route.
    var routeCaption: String { routeCaption(of: displayID) }

    /// Overlay/PPP gateway caption for one profile.
    func routeCaption(of id: String) -> String {
        if holdArmed { return L10n.t("held") }
        let tunOn = store.profiles.first(where: { $0.id == id })?.tun ?? false
        let wantDefault = tunOn && store.defaultRoute
        if state(of: id) == .on {
            let gw = overlayGatewayDisplay(of: id)
            if !gw.isEmpty { return gw }
            if lives[id]?.defaultArmed == true { return L10n.t("full") }
            if tunLive(of: id) { return L10n.t("net_tag") }
            return "SOCKS5"
        }
        if wantDefault { return L10n.t("full") }
        if tunOn { return L10n.t("net_tag") }
        return "SOCKS5"
    }

    /// Overlay/PPP gateway from STATUS, else .1 of the overlay /24.
    func overlayGatewayDisplay() -> String { overlayGatewayDisplay(of: displayID) }

    /// Overlay/PPP gateway for one profile.
    func overlayGatewayDisplay(of id: String) -> String {
        if let gw = lives[id]?.helper.overlayGW, !gw.isEmpty { return gw }
        return Self.slash24Gateway(overlayIP(of: id))
    }

    /// Typical overlay gateway: last octet 1, skipped when the address already is .1.
    static func slash24Gateway(_ ip: String) -> String {
        let p = ip.split(separator: ".")
        guard p.count == 4, p[3] != "1" else { return "" }
        return "\(p[0]).\(p[1]).\(p[2]).1"
    }

    /// Connects from off/error, disconnects from on, cancels an in-flight handshake.
    func toggle() { toggle(nil) }

    /// Same as `toggle()`, for one profile. Exclusive: selecting another row switches the session.
    /// A countdown «Повтор (N)» tap stops auto-reconnect instead of opening a new session.
    func toggle(_ id: String?) {
        let pid = (id?.isEmpty == false) ? id! : displayID
        LoftLog.shared.tap("toggle", "\(pid) \(state(of: pid).rawValue)")
        if reconnectLeft(of: pid) != nil {
            stopReconnect(pid)
            return
        }
        if store.multi {
            switch state(of: pid) {
            case .on: disconnect(pid)
            case .off, .error: connect(pid)
            case .connecting: cancelConnect(pid)
            case .disconnecting: return
            }
            return
        }
        if pid != store.activeID {
            selectProfile(pid)
            if state == .off || state == .error { connect(pid) }
            return
        }
        switch state {
        case .on: disconnect()
        case .off, .error: connect()
        case .connecting: cancelConnect()
        case .disconnecting: return
        }
    }

    /// Overlay IP reported by loft-front of the current session.
    func helperIP() -> String { displayLive?.helper.overlayIP ?? "" }

    /// Copies loft-front byte counters into `upRate`/`downRate` once a second.
    func tickRates() {
        for s in lives.values {
            if s.state == .on {
                let t = s.helper.traffic()
                s.upRate = Int(t.0)
                s.downRate = Int(t.1)
            } else if s.upRate != 0 || s.downRate != 0 {
                s.upRate = 0
                s.downRate = 0
            }
        }
    }

    /// Asks the echo API through every live outbound. Invalidates in-flight lookups first.
    func refreshExtIP() {
        let go = {
            for s in self.lives.values {
                s.extIPGen += 1
                s.extIPBusy = false
            }
            if !self.store.showExtIP {
                NotificationCenter.default.post(name: .loftState, object: self)
                return
            }
            for s in self.lives.values where s.state == .on {
                self.probeExtIP(s)
            }
        }
        if Thread.isMainThread { go() } else { DispatchQueue.main.async(execute: go) }
    }

    /// GET the echo API through this session’s SOCKS (or the default route if that is all we have).
    private func probeExtIP(_ s: LiveSession) {
        let go = {
            guard self.store.showExtIP else { return }
            guard s.state == .on else { return }
            let url = ProfileStore.normalizeExtIPURL(self.store.extIPURL)
            let socks = s.probeSocks.isEmpty ? s.socksDisplay : s.probeSocks
            let viaDefault = s.defaultArmed
            if socks.isEmpty && !viaDefault { return }
            s.extIPGen += 1
            let gen = s.extIPGen
            let ticket = s.generation
            let id = s.id
            s.extIPBusy = true
            NotificationCenter.default.post(name: .loftState, object: self)
            DispatchQueue.global(qos: .utility).async {
                let ip = ExtIP.lookup(url: url, socks: socks, useDirect: socks.isEmpty && viaDefault)
                DispatchQueue.main.async {
                    guard let s = self.lives[id], s.generation == ticket, s.extIPGen == gen, s.state == .on else { return }
                    s.extIPBusy = false
                    s.extIP = ip
                    if ip.isEmpty {
                        LoftLog.shared.add("loft", "ext-ip id=\(id) fail")
                    } else {
                        LoftLog.shared.add("loft", "ext-ip id=\(id) \(ip)")
                    }
                    NotificationCenter.default.post(name: .loftState, object: self)
                }
            }
        }
        if Thread.isMainThread { go() } else { DispatchQueue.main.async(execute: go) }
    }

    /// Bottom spark line: ticker while connecting, the real error on fail, front/proto/opts when on.
    func sparkLine() -> (text: String, color: NSColor, scroll: Bool, pair: Bool) {
        if holdArmed {
            return (L10n.t("held"), Palette.err, false, false)
        }
        switch state {
        case .error:
            let t = errorText.isEmpty ? L10n.t("err") : errorText
            return (t, Palette.err, true, false)
        case .on:
            return (sessionSpark(), Palette.mute, true, true)
        case .connecting:
            let t = progressText.isEmpty ? L10n.t("connecting") : progressText
            return (t, Palette.mute, true, false)
        case .disconnecting:
            return (L10n.t("disconnecting"), Palette.mute, true, false)
        case .off:
            return (L10n.t("none"), Palette.mute, false, false)
        }
    }

    /// `loft/1.0 · get · mux · mask` for loft; `sstp · ppp` for SSTP.
    func sessionSpark() -> String {
        let front = frontName.isEmpty ? (displayProfile()?.kind ?? "get") : frontName
        if front == "sstp" {
            return tunLive ? "sstp  ·  ppp  ·  tun" : "sstp  ·  ppp"
        }
        if front == "vless" {
            return tunLive ? "vless  ·  tcp  ·  tun" : "vless  ·  tcp"
        }
        if front == "trojan" {
            return tunLive ? "trojan  ·  tls  ·  tun" : "trojan  ·  tls"
        }
        let major = Int(LoftWire.proto >> 8)
        let minor = Int(LoftWire.proto & 0xff)
        let inner = front == "l3" ? "ip" : ((displayProfile()?.mux ?? true) ? "mux" : "connect")
        return "loft/\(major).\(minor)  ·  \(front)  ·  \(inner)  ·  mask"
    }

    /// Appends a unique connecting-stage label to the spark ticker.
    private func appendStage(_ s: LiveSession, _ token: String) {
        let label = FrontExplain.stageLabel(token)
        if s.progressParts.contains(label) { return }
        s.progressParts.append(label)
        s.progressText = ([L10n.t("connecting")] + s.progressParts).joined(separator: "  ·  ")
    }

    /// Sets `.error` from a local fail (missing key/profile/inbound).
    private func fail(_ s: String, id: String? = nil) {
        LoftLog.shared.add("loft", "error \(s)")
        set(id ?? store.activeID, .error, err: FrontExplain.display(s), ip: "")
    }

    /// Publishes state on the main queue and posts `.loftState`.
    private func set(_ id: String, _ st: State, err: String, ip: String) {
        let apply = {
            let s = self.ensure(id)
            if st == .on {
                if s.state != .on { s.connectedAt = Date() }
                s.reconnectAttempt = 0
            } else if st != .disconnecting {
                s.connectedAt = nil
            }
            if st == .connecting {
                let seed: String
                if let p = self.store.profiles.first(where: { $0.id == id }) {
                    if p.isSSTP { seed = "SSTP" }
                    else if p.isVLESS { seed = "VLESS" }
                    else if p.isTrojan { seed = "Trojan" }
                    else { seed = L10n.t("st_front") }
                } else {
                    seed = L10n.t("st_front")
                }
                s.progressParts = [seed]
                s.progressText = L10n.t("connecting") + "  ·  " + seed
            } else if st != .disconnecting {
                s.progressParts = []
                s.progressText = ""
            }
            if st != .on {
                s.extIP = ""
                s.extIPBusy = false
                s.extIPGen += 1
            }
            var shown = err
            if st == .error, s.state == .error,
               FrontExplain.isGenericExit(shown), !FrontExplain.isGenericExit(s.errorText) {
                shown = s.errorText
            }
            s.state = st
            s.errorText = shown
            s.overlayIP = ip
            if st == .error && !shown.isEmpty { self.lastFail = shown }
            if st == .on || st == .off { self.lastFail = "" }
            NotificationCenter.default.post(name: .loftState, object: self)
        }
        if Thread.isMainThread { apply() } else { DispatchQueue.main.async(execute: apply) }
    }
}
