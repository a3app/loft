/*
 LoftApp.swift - entry point of the iOS container app.
 It only opens one window with LoftShell (App/HomeView.swift), which holds the
 tab bar and all screens. The VPN itself runs in the separate Packet Tunnel
 extension (PacketTunnel/PacketTunnelProvider.swift).
*/
import SwiftUI

/// The container app (`app.loft.ios`). It only edits connections and
/// settings and starts the system VPN; the protocol runs in the Packet
/// Tunnel extension.
@main
struct LoftApp: App {
    var body: some Scene {
        WindowGroup {
            LoftShell()
        }
    }
}
