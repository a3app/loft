import Foundation
import NetworkExtension

/// Personal VPN manager: hold (`includeAllNetworks`), On Demand, Reset routes.
///
/// The app side of the tunnel. It never speaks the protocol: it saves the
/// VPN profile in iOS preferences, starts and stops the Packet Tunnel
/// extension, and sends it short commands ("park", "go"). `up`, `busy` and
/// `held` mirror the system VPN status for the UI.
///
/// Hold is the kill switch. With `includeAllNetworks` iOS sends all traffic
/// to the tunnel even while it has no session, so nothing leaks. Disconnect
/// with hold on only parks the extension (a blackhole); the real stop is
/// Reset routes.
@MainActor
final class TunnelControl: ObservableObject {
    @Published var up = false
    @Published var busy = false
    /// The tunnel is up but parked: it drops traffic instead of carrying it.
    @Published var held = false
    @Published var line = "VPN is in iOS Settings"

    /// Last saved (hold, demand, trusted Wi-Fi), so unchanged prefs do not
    /// rewrite the VPN profile.
    private var lastPolicy: (Bool, Bool, String)?
    private var statusObs: NSObjectProtocol?

    init() {
        statusObs = NotificationCenter.default.addObserver(
            forName: .NEVPNStatusDidChange, object: nil, queue: .main
        ) { [weak self] _ in
            Task { @MainActor in await self?.refresh() }
        }
    }

    deinit {
        if let statusObs { NotificationCenter.default.removeObserver(statusObs) }
    }

    /// Reads the system VPN status and the App Group flags into the UI state.
    func refresh() async {
        let st = await status()
        busy = st == .connecting || st == .reasserting
        up = st == .connected
        held = up && Group.holding
        switch st {
        case .connected:
            line = Group.holding ? "route closed" : "Connected"
        case .connecting, .reasserting:
            line = "Connecting…"
        case .disconnecting:
            line = "Disconnecting…"
        default:
            line = "Disconnected"
        }
    }

    /// Saves hold and On Demand into the VPN profile when they changed.
    func applyPolicy(_ prefs: LoftPrefs) async {
        let key = (prefs.hold, prefs.demand, prefs.demand ? prefs.trustedWiFi : "")
        if let last = lastPolicy, last == key { return }
        do {
            _ = try await ensureManager(prefs)
            lastPolicy = key
        } catch {
            line = error.localizedDescription
        }
    }

    /// Starts the tunnel. If it is already up (parked by hold), it asks the
    /// extension to start the session again ("go") instead.
    func connect() {
        Group.parked = false
        busy = true
        line = "Connecting…"
        Task {
            do {
                let mgr = try await ensureManager(LoftPrefs.load())
                if mgr.connection.status == .connected {
                    _ = await tell(mgr, "go")
                } else {
                    try mgr.connection.startVPNTunnel()
                }
            } catch {
                busy = false
                up = false
                line = error.localizedDescription
            }
            await refresh()
        }
    }

    /// With hold on and the tunnel up, parks the extension: the session ends
    /// but traffic stays blocked. Otherwise stops the tunnel.
    func disconnect() {
        Task {
            let mgr = (try? await loadMgr()) ?? nil
            if Group.hold, let mgr, mgr.connection.status == .connected {
                Group.parked = true
                _ = await tell(mgr, "park")
                await refresh()
                return
            }
            Group.parked = false
            Group.holding = false
            mgr?.connection.stopVPNTunnel()
            up = false
            held = false
            busy = false
            line = "Disconnected"
        }
    }

    /// Drops hold and On Demand, stops the Packet Tunnel. Native default comes back.
    func resetRoutes() async {
        Group.parked = false
        Group.holding = false
        Group.hold = false
        Group.demand = false
        lastPolicy = nil
        var p = LoftPrefs.load()
        p.hold = false
        p.demand = false
        p.save()
        do {
            let mgr = try await ensureManager(p)
            mgr.isOnDemandEnabled = false
            mgr.onDemandRules = []
            if let proto = mgr.protocolConfiguration as? NETunnelProviderProtocol {
                proto.includeAllNetworks = false
                mgr.protocolConfiguration = proto
            }
            try await mgr.saveToPreferences()
            try await mgr.loadFromPreferences()
            mgr.connection.stopVPNTunnel()
        } catch {
            line = error.localizedDescription
        }
        up = false
        held = false
        busy = false
        line = "Disconnected"
        lastPolicy = (false, false, LoftPrefs.load().trustedWiFi)
    }

    private func status() async -> NEVPNStatus {
        (try? await loadMgr())?.connection.status ?? .invalid
    }

    private func loadMgr() async throws -> NETunnelProviderManager? {
        let all = try await NETunnelProviderManager.loadAllFromPreferences()
        return all.first(where: { $0.protocolConfiguration is NETunnelProviderProtocol })
    }

    /// Creates or updates the single loft VPN profile in iOS preferences and
    /// reloads it, as NetworkExtension requires before the profile is used.
    private func ensureManager(_ prefs: LoftPrefs) async throws -> NETunnelProviderManager {
        let all = try await NETunnelProviderManager.loadAllFromPreferences()
        let mgr = all.first ?? NETunnelProviderManager()
        let proto = (mgr.protocolConfiguration as? NETunnelProviderProtocol) ?? NETunnelProviderProtocol()
        proto.providerBundleIdentifier = "app.loft.ios.packet-tunnel"
        proto.serverAddress = "loft"
        proto.includeAllNetworks = prefs.hold
        proto.disconnectOnSleep = false
        mgr.protocolConfiguration = proto
        mgr.localizedDescription = "loft"
        mgr.isEnabled = true
        mgr.onDemandRules = Self.demandRules(prefs)
        mgr.isOnDemandEnabled = prefs.demand && !(mgr.onDemandRules?.isEmpty ?? true)
        try await mgr.saveToPreferences()
        try await mgr.loadFromPreferences()
        return mgr
    }

    /// Connect on cellular and untrusted Wi‑Fi. Trusted SSIDs stay off. First match wins.
    private static func demandRules(_ prefs: LoftPrefs) -> [NEOnDemandRule] {
        guard prefs.demand else { return [] }
        var rules: [NEOnDemandRule] = []
        let ssid = prefs.trustedWiFi
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        if !ssid.isEmpty {
            let skip = NEOnDemandRuleDisconnect()
            skip.interfaceTypeMatch = .wiFi
            skip.ssidMatch = ssid
            rules.append(skip)
        }
        let wifi = NEOnDemandRuleConnect()
        wifi.interfaceTypeMatch = .wiFi
        rules.append(wifi)
        let cell = NEOnDemandRuleConnect()
        cell.interfaceTypeMatch = .cellular
        rules.append(cell)
        return rules
    }

    /// Sends a command to the running extension (`handleAppMessage`) and
    /// returns its text reply. Nil when the tunnel is not connected.
    private func tell(_ mgr: NETunnelProviderManager, _ cmd: String) async -> String? {
        guard let session = mgr.connection as? NETunnelProviderSession,
              mgr.connection.status == .connected
        else { return nil }
        return await withCheckedContinuation { cont in
            do {
                try session.sendProviderMessage(Data(cmd.utf8)) { data in
                    cont.resume(returning: data.flatMap { String(data: $0, encoding: .utf8) })
                }
            } catch {
                cont.resume(returning: nil)
            }
        }
    }
}
