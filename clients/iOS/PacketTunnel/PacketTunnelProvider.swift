/*
 PacketTunnelProvider.swift - the iOS Network Extension that carries the VPN.
 iOS runs it as its own process; the container app (App/TunnelControl.swift)
 only starts it and sends "park" / "go". It runs the Go engine from
 clients/mobile (built with gomobile as the `Mobile` framework), moves IP
 packets between iOS and the engine, and turns the engine's status into
 routes, MTU and DNS. Shared state lives in Shared/Group.swift.
*/
import Foundation
import Network
import NetworkExtension
import Mobile

/// Thin Packet Tunnel: pumps IP, installs routes/DNS from Snapshot.
/// Hold: if the overlay dies, keep 0/1 and drop packets until Connect or Reset routes.
/// `LoftPacketSink` is the gomobile protocol; Swift cannot inherit the stub class.
///
/// This is the Network Extension process (`app.loft.ios.packet-tunnel`). iOS
/// starts it when the user connects; the app itself never speaks the
/// protocol. The provider reads the connection YAML the app left in the App
/// Group, runs the Go engine (`clients/mobile`), copies IP packets between
/// `packetFlow` and the engine, and turns the engine's Snapshot into tunnel
/// settings.
///
/// "Park" here is an iOS-side state, not the node's park/hold of a mux: the
/// engine is stopped but the tunnel stays up as a blackhole that claims the
/// default route and drops every packet. That is the hold (kill-switch)
/// feature: nothing leaks to the open network while loft is down. "0/1" in
/// the line above means that default route.
final class PacketTunnelProvider: NEPacketTunnelProvider, LoftPacketSink {
    /// The running Go engine; nil while parked or stopped.
    private var engine: MobileEngine?
    /// Serial queue for all engine calls, since some of them block.
    private let goQueue = DispatchQueue(label: "app.loft.ios.engine")
    /// Watches network changes (Wi-Fi <-> cellular).
    private var pathMon: NWPathMonitor?
    /// True while the tunnel is a blackhole without an engine.
    private var parked = false
    /// The packet read loop has been started (it runs only once).
    private var pumping = false
    /// The once-a-second engine poll is active.
    private var watching = false

    /// iOS asks for the tunnel. Packet reading, path watching and engine
    /// polling start only after the first settings are applied.
    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        goQueue.async {
            self.boot { err in
                if err == nil {
                    self.pumpReads()
                    self.watchPath()
                    self.watchEngine()
                }
                completionHandler(err)
            }
        }
    }

    /// iOS stops the tunnel: stop watching, stop the engine and clear the
    /// parked / holding flags so the app sees a clean state.
    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        pathMon?.cancel()
        pathMon = nil
        watching = false
        goQueue.async {
            self.engine?.stop()
            self.engine = nil
            Group.holding = false
            Group.parked = false
            completionHandler()
        }
    }

    /// Commands from the app while the tunnel is up: "park" drops the
    /// overlay but keeps the blackhole (Disconnect with hold on), "go"
    /// starts the overlay again from the current YAML (Connect while held).
    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)? = nil) {
        let cmd = String(data: messageData, encoding: .utf8) ?? ""
        goQueue.async {
            switch cmd {
            case "park":
                self.park { _ in
                    completionHandler?("held".data(using: .utf8))
                }
            case "go":
                self.startOverlay { err in
                    let s = err == nil ? "on" : (err?.localizedDescription ?? "err")
                    completionHandler?(s.data(using: .utf8))
                }
            default:
                completionHandler?(nil)
            }
        }
    }

    /// A packet from the engine toward iOS. The protocol family is read
    /// from the IP version nibble of the first byte.
    func onPacket(_ pkt: Data?) {
        guard let pkt, !pkt.isEmpty else { return }
        let v6 = pkt[0] >> 4 == 6
        packetFlow.writePackets([pkt], withProtocols: [NSNumber(value: v6 ? AF_INET6 : AF_INET)])
    }

    /// Decides what the tunnel starts as: still parked from last time (with
    /// hold on) stays parked; otherwise the overlay starts, and if that fails
    /// with hold on, the tunnel comes up as a blackhole instead of failing.
    private func boot(completion: @escaping @Sendable (Error?) -> Void) {
        if Group.parked && Group.hold {
            park(completion: completion)
            return
        }
        let yaml = Group.yaml.trimmingCharacters(in: .whitespacesAndNewlines)
        if !yaml.isEmpty {
            startOverlay { err in
                if err == nil {
                    completion(nil)
                    return
                }
                if Group.hold {
                    self.park(completion: completion)
                } else {
                    completion(err)
                }
            }
            return
        }
        if Group.hold {
            park(completion: completion)
            return
        }
        completion(Self.err("empty session"))
    }

    /// Starts a fresh engine from the App Group YAML and applies its
    /// settings. A one-line body that contains "://" is a share link the
    /// app stored as is; it is turned into YAML first. `eng.start` blocks
    /// until the session is up or times out (30 s in the engine).
    private func startOverlay(completion: @escaping @Sendable (Error?) -> Void) {
        var body = Group.yaml.trimmingCharacters(in: .whitespacesAndNewlines)
        if body.isEmpty {
            completion(Self.err("empty session"))
            return
        }
        // Replace any previous engine and point its packet output at self.
        engine?.stop()
        guard let eng = MobileNewEngine() else {
            completion(Self.err("no engine"))
            return
        }
        engine = eng
        LoftEngineSetSink(eng, self)
        // A single-line link is converted to YAML before the engine sees it.
        if !body.contains("\n"), body.lowercased().contains("://") {
            var parseErr: NSError?
            body = MobileParseShare(body, &parseErr)
            if let parseErr {
                completion(parseErr)
                return
            }
        }
        do {
            try eng.start(body)
        } catch {
            engine = nil
            completion(error)
            return
        }
        guard let snap = eng.snapshot() else {
            engine = nil
            completion(Self.err("no snapshot"))
            return
        }
        parked = false
        Group.parked = false
        apply(snap, completion: completion)
    }

    /// Stops the engine and switches the tunnel to the blackhole settings.
    /// The flag is also saved so a restart of the extension stays parked.
    private func park(completion: @escaping @Sendable (Error?) -> Void) {
        engine?.stop()
        engine = nil
        parked = true
        Group.parked = true
        applyBlackhole(completion: completion)
    }

    /// Starts the packet read loop once.
    private func pumpReads() {
        if pumping { return }
        pumping = true
        loopReads()
    }

    /// Reads packets from iOS forever and hands them to the engine. While
    /// parked, packets are read and dropped: that is the blackhole.
    private func loopReads() {
        packetFlow.readPackets { packets, _ in
            if !self.parked, let eng = self.engine {
                for p in packets {
                    try? eng.injectPacket(p)
                }
            }
            self.loopReads()
        }
    }

    /// Tells the engine about Wi-Fi <-> LTE changes. The engine cannot watch
    /// the default route itself because inside a full tunnel that route is
    /// the tunnel. Settings are re-applied in case the address changed.
    private func watchPath() {
        if pathMon != nil { return }
        let mon = NWPathMonitor()
        mon.pathUpdateHandler = { [weak self] _ in
            guard let self, !self.parked else { return }
            self.engine?.notifyPathChange()
            if let snap = self.engine?.snapshot() {
                self.apply(snap) { _ in }
            }
        }
        mon.start(queue: goQueue)
        pathMon = mon
    }

    /// Starts the engine health poll.
    private func watchEngine() {
        watching = true
        pollEngine()
    }

    /// Once a second checks whether the engine gave up. Then the tunnel
    /// either parks (hold on) or is torn down.
    private func pollEngine() {
        goQueue.asyncAfter(deadline: .now() + 1) { [weak self] in
            guard let self, self.watching else { return }
            defer { self.pollEngine() }
            if self.parked { return }
            let st = self.engine?.snapshot()?.state ?? ""
            if st == "error" || st == "stopped" {
                if Group.hold {
                    self.park { _ in }
                } else {
                    self.cancelTunnelWithError(Self.err("session ended"))
                }
            }
        }
    }

    /// Installs the tunnel settings from a Snapshot: the overlay address,
    /// the default route into the tunnel, host routes outside it for the
    /// server addresses (so the session does not loop into itself), MTU and
    /// DNS. The server addresses are remembered for the blackhole.
    private func apply(_ snap: MobileSnapshot, completion: @escaping @Sendable (Error?) -> Void) {
        let ip = snap.ip.trimmingCharacters(in: .whitespaces)
        if ip.isEmpty {
            completion(Self.err("no overlay ip"))
            return
        }
        if !snap.frontIPs.isEmpty {
            Group.frontIPs = snap.frontIPs
        }
        Group.holding = false
        let gw = snap.gateway.isEmpty ? ip : snap.gateway
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: gw)
        let v4 = NEIPv4Settings(addresses: [ip], subnetMasks: ["255.255.255.0"])
        v4.includedRoutes = [NEIPv4Route.default()]
        v4.excludedRoutes = Self.exclude(snap.frontIPs)
        settings.ipv4Settings = v4
        if snap.mtu > 0 {
            settings.mtu = NSNumber(value: snap.mtu)
        }
        let dns = snap.dns.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        if !dns.isEmpty {
            settings.dnsSettings = NEDNSSettings(servers: dns)
        }
        setTunnelNetworkSettings(settings, completionHandler: completion)
    }

    /// Tunnel settings with no engine behind them: placeholder addresses,
    /// IPv4 and IPv6 default routes into the tunnel, and only the last known
    /// server addresses left outside so a later Connect can still dial.
    private func applyBlackhole(completion: @escaping @Sendable (Error?) -> Void) {
        Group.holding = true
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "198.18.0.1")
        let v4 = NEIPv4Settings(addresses: ["198.18.0.1"], subnetMasks: ["255.255.255.0"])
        v4.includedRoutes = [NEIPv4Route.default()]
        v4.excludedRoutes = Self.exclude(Group.frontIPs)
        settings.ipv4Settings = v4
        let v6 = NEIPv6Settings(addresses: ["fd00:18::1"], networkPrefixLengths: [64])
        v6.includedRoutes = [NEIPv6Route.default()]
        settings.ipv6Settings = v6
        settings.mtu = 1280
        setTunnelNetworkSettings(settings, completionHandler: completion)
    }

    /// One /32 route per IPv4 address in the comma list; IPv6 is skipped.
    private static func exclude(_ csv: String) -> [NEIPv4Route] {
        csv.split(separator: ",").compactMap { part in
            let s = part.trimmingCharacters(in: .whitespaces)
            guard !s.isEmpty, s.contains(".") else { return nil }
            return NEIPv4Route(destinationAddress: s, subnetMask: "255.255.255.255")
        }
    }

    /// Wraps a message in an NSError for iOS completion handlers.
    private static func err(_ s: String) -> NSError {
        NSError(domain: "app.loft.ios", code: 1, userInfo: [NSLocalizedDescriptionKey: s])
    }
}
