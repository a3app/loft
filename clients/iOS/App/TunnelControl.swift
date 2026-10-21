import Foundation
import NetworkExtension

@MainActor
final class TunnelControl: ObservableObject {
    @Published var up = false
    @Published var busy = false
    @Published var line = "VPN is in iOS Settings"

    func refresh() async {
        if let m = try? await NETunnelProviderManager.loadAllFromPreferences(),
           let live = m.first(where: { $0.protocolConfiguration is NETunnelProviderProtocol }) {
            up = live.connection.status == .connected
            busy = live.connection.status == .connecting
        }
    }

    func connect() {
        busy = true
        line = "Connecting…"
        Task {
            do {
                let mgr = try await ensureManager()
                try mgr.connection.startVPNTunnel()
                up = true
                busy = false
                line = "Connected"
            } catch {
                busy = false
                up = false
                line = error.localizedDescription
            }
        }
    }

    func disconnect() {
        Task {
            let all = (try? await NETunnelProviderManager.loadAllFromPreferences()) ?? []
            for m in all {
                m.connection.stopVPNTunnel()
            }
            up = false
            busy = false
            line = "Disconnected"
        }
    }

    private func ensureManager() async throws -> NETunnelProviderManager {
        let all = try await NETunnelProviderManager.loadAllFromPreferences()
        let mgr = all.first ?? NETunnelProviderManager()
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = "app.loft.ios.packet-tunnel"
        proto.serverAddress = "loft"
        mgr.protocolConfiguration = proto
        mgr.localizedDescription = "loft"
        mgr.isEnabled = true
        try await mgr.saveToPreferences()
        try await mgr.loadFromPreferences()
        return mgr
    }
}
