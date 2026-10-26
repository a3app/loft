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
