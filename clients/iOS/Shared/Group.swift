/*
 Group.swift - the small key/value store shared by the two iOS processes.
 The container app (app.loft.ios) and the Packet Tunnel extension
 (app.loft.ios.packet-tunnel) run as separate processes, so they exchange
 state through UserDefaults in the App Group suite `group.app.loft`.
 This file is compiled into both targets: App/TunnelControl.swift and
 App/LoftPrefs.swift write these keys, PacketTunnel/PacketTunnelProvider.swift
 reads them and reports back through `parked` / `holding` / `frontIPs`.
*/
import Foundation

/// Settings shared between the app and the Packet Tunnel extension through
/// the App Group `group.app.loft`. They are two processes, so UserDefaults in
/// the group suite is how the app hands over the connection YAML and the
/// extension reports whether it is parked.
enum Group {
    /// App Group identifier; must match the entitlement of both targets.
    static let id = "group.app.loft"
    /// UserDefaults key names, one per property below.
    static let yamlKey = "session.yaml"
    static let holdKey = "hold"
    static let demandKey = "demand"
    static let trustedKey = "trusted"
    static let parkedKey = "parked"
    static let holdingKey = "holding"
    static let frontIPsKey = "front.ips"

    /// The shared suite. Falls back to the process's own defaults when the
    /// group is not available (no entitlement); then the app and the
    /// extension no longer see each other's values.
    static var defaults: UserDefaults {
        UserDefaults(suiteName: id) ?? .standard
    }

    /// The connection the tunnel should run: client YAML, or a share link.
    static var yaml: String {
        get { defaults.string(forKey: yamlKey) ?? "" }
        set { defaults.set(newValue, forKey: yamlKey) }
    }

    /// Missing key is on: Settings default is hold armed.
    static var hold: Bool {
        get {
            if defaults.object(forKey: holdKey) == nil { return true }
            return defaults.bool(forKey: holdKey)
        }
        set { defaults.set(newValue, forKey: holdKey) }
    }

    /// Connect On Demand: iOS brings the tunnel up by itself.
    static var demand: Bool {
        get { defaults.bool(forKey: demandKey) }
        set { defaults.set(newValue, forKey: demandKey) }
    }

    /// Comma-separated Wi-Fi names (SSIDs) where On Demand stays off.
    static var trustedWiFi: String {
        get { defaults.string(forKey: trustedKey) ?? "" }
        set { defaults.set(newValue, forKey: trustedKey) }
    }

    /// User Disconnect with hold: stay in the extension, drop the overlay.
    static var parked: Bool {
        get { defaults.bool(forKey: parkedKey) }
        set { defaults.set(newValue, forKey: parkedKey) }
    }

    /// Packet Tunnel is up as a blackhole (no overlay).
    static var holding: Bool {
        get { defaults.bool(forKey: holdingKey) }
        set { defaults.set(newValue, forKey: holdingKey) }
    }

    /// Last session front IPs, comma-separated. Blackhole excludes them so a later Start can dial.
    static var frontIPs: String {
        get { defaults.string(forKey: frontIPsKey) ?? "" }
        set { defaults.set(newValue, forKey: frontIPsKey) }
    }
}
