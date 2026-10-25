import Foundation

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
