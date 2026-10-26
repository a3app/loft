import Foundation

/// Settings shared between the app and the Packet Tunnel extension through
/// the App Group `group.app.loft`. They are two processes, so UserDefaults in
/// the group suite is how the app hands over the connection YAML and the
/// extension reports whether it is parked.
enum Group {
    static let id = "group.app.loft"
    static let yamlKey = "session.yaml"
    static let holdKey = "hold"
    static let demandKey = "demand"
    static let trustedKey = "trusted"
    static let parkedKey = "parked"
    static let holdingKey = "holding"
    static let frontIPsKey = "front.ips"

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
