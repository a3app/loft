import Foundation
import Network
import NetworkExtension
import Mobile

/// Thin Packet Tunnel: pumps IP, installs routes/DNS from Snapshot.
/// Session is Go (clients/mobile). Call Start off the provider queue.
final class PacketTunnelProvider: NEPacketTunnelProvider, MobilePacketSink {
    private var engine: MobileEngine?
    private let goQueue = DispatchQueue(label: "app.loft.ios.engine")
    private var pathMon: NWPathMonitor?

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        var body = Group.yaml.trimmingCharacters(in: .whitespacesAndNewlines)
        if body.isEmpty {
            completionHandler(Self.err("empty session"))
            return
        }
        let eng = MobileNewEngine()
        engine = eng
        eng.setSink(self)
        goQueue.async {
            var yaml = body
            if !yaml.contains("\n"), yaml.lowercased().contains("://") {
                do {
                    yaml = try MobileParseShare(yaml)
                } catch {
                    completionHandler(error)
                    return
                }
            }
            do {
                try eng.start(yaml)
            } catch {
                completionHandler(error)
                return
            }
            guard let snap = eng.snapshot() else {
                completionHandler(Self.err("no snapshot"))
                return
            }
            self.apply(snap) { err in
                if err == nil {
                    self.pumpReads()
                    self.watchPath()
                }
                completionHandler(err)
            }
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        pathMon?.cancel()
        pathMon = nil
        goQueue.async {
            self.engine?.stop()
            self.engine = nil
            completionHandler()
        }
    }

    func onPacket(_ pkt: Data?) {
        guard let pkt, !pkt.isEmpty else { return }
        let v6 = pkt[0] >> 4 == 6
        packetFlow.writePackets([pkt], withProtocols: [NSNumber(value: v6 ? AF_INET6 : AF_INET)])
    }

    private func pumpReads() {
        packetFlow.readPackets { packets, _ in
            for p in packets {
                try? self.engine?.injectPacket(p)
            }
            self.pumpReads()
        }
    }

    private func watchPath() {
        let mon = NWPathMonitor()
        mon.pathUpdateHandler = { [weak self] _ in
            self?.engine?.notifyPathChange()
            if let snap = self?.engine?.snapshot() {
                self?.apply(snap) { _ in }
            }
        }
        mon.start(queue: goQueue)
        pathMon = mon
    }

    private func apply(_ snap: MobileSnapshot, completion: @escaping (Error?) -> Void) {
        let ip = snap.ip().trimmingCharacters(in: .whitespaces)
        if ip.isEmpty {
            completion(Self.err("no overlay ip"))
            return
        }
        let gw = snap.gateway().isEmpty ? ip : snap.gateway()
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: gw)
        let v4 = NEIPv4Settings(addresses: [ip], subnetMasks: ["255.255.255.0"])
        v4.includedRoutes = [NEIPv4Route.default()]
        var excluded: [NEIPv4Route] = []
        for part in snap.frontIPs().split(separator: ",") {
            let s = part.trimmingCharacters(in: .whitespaces)
            guard !s.isEmpty, s.contains(".") else { continue }
            excluded.append(NEIPv4Route(destinationAddress: s, subnetMask: "255.255.255.255"))
        }
        v4.excludedRoutes = excluded
        settings.ipv4Settings = v4
        if snap.mtu() > 0 {
            settings.mtu = NSNumber(value: snap.mtu())
        }
        let dns = snap.dns().split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        if !dns.isEmpty {
            settings.dnsSettings = NEDNSSettings(servers: dns)
        }
        setTunnelNetworkSettings(settings, completionHandler: completion)
    }

    private static func err(_ s: String) -> NSError {
        NSError(domain: "app.loft.ios", code: 1, userInfo: [NSLocalizedDescriptionKey: s])
    }
}
