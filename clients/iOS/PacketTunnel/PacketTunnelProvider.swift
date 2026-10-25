import Foundation
import Network
import NetworkExtension
import Mobile

/// Thin Packet Tunnel: pumps IP, installs routes/DNS from Snapshot.
/// Hold: if the overlay dies, keep 0/1 and drop packets until Connect or Reset routes.
/// `LoftPacketSink` is the gomobile protocol; Swift cannot inherit the stub class.
final class PacketTunnelProvider: NEPacketTunnelProvider, LoftPacketSink {
    private var engine: MobileEngine?
    private let goQueue = DispatchQueue(label: "app.loft.ios.engine")
    private var pathMon: NWPathMonitor?
    private var parked = false
    private var pumping = false
    private var watching = false

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

    func onPacket(_ pkt: Data?) {
        guard let pkt, !pkt.isEmpty else { return }
        let v6 = pkt[0] >> 4 == 6
        packetFlow.writePackets([pkt], withProtocols: [NSNumber(value: v6 ? AF_INET6 : AF_INET)])
    }

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

    private func startOverlay(completion: @escaping @Sendable (Error?) -> Void) {
        var body = Group.yaml.trimmingCharacters(in: .whitespacesAndNewlines)
        if body.isEmpty {
            completion(Self.err("empty session"))
            return
        }
        engine?.stop()
        guard let eng = MobileNewEngine() else {
            completion(Self.err("no engine"))
            return
        }
        engine = eng
        LoftEngineSetSink(eng, self)
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

    private func park(completion: @escaping @Sendable (Error?) -> Void) {
        engine?.stop()
        engine = nil
        parked = true
        Group.parked = true
        applyBlackhole(completion: completion)
    }

    private func pumpReads() {
        if pumping { return }
        pumping = true
        loopReads()
    }

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

    private func watchEngine() {
        watching = true
        pollEngine()
    }

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

    private static func exclude(_ csv: String) -> [NEIPv4Route] {
        csv.split(separator: ",").compactMap { part in
            let s = part.trimmingCharacters(in: .whitespaces)
            guard !s.isEmpty, s.contains(".") else { return nil }
            return NEIPv4Route(destinationAddress: s, subnetMask: "255.255.255.255")
        }
    }

    private static func err(_ s: String) -> NSError {
        NSError(domain: "app.loft.ios", code: 1, userInfo: [NSLocalizedDescriptionKey: s])
    }
}
