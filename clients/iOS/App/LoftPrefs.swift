import Foundation

/// How the traffic graph on Home is drawn.
enum SparkStyle: String, Codable, CaseIterable {
    case lines, bars
}

/// Settings snapshot. `save()` writes hold / demand / trusted Wi‑Fi into the app group.
struct LoftPrefs: Equatable, Codable {
    var simple = false
    var bits = true
    var spark: SparkStyle = .lines
    /// `en` / `ru` / `zh`. Empty follows the system locale.
    var lang = ""
    var diag = false
    var defaultRoute = true
    /// Kill switch: block traffic while the session is down.
    var hold = true
    var split = true
    var autoConnect = false
    var reconnect = true
    /// Connect On Demand on cellular and untrusted Wi-Fi.
    var demand = false
    /// Comma-separated Wi-Fi names where On Demand stays off.
    var trustedWiFi = ""
    var showExtIP = false

    static let `default` = LoftPrefs()
    private static let storeKey = "ui.prefs"

    var locale: Locale {
        switch lang {
        case "ru": return Locale(identifier: "ru")
        case "zh": return Locale(identifier: "zh-Hans")
        case "en": return Locale(identifier: "en")
        default: return .current
        }
    }

    static func load() -> LoftPrefs {
        guard let data = Group.defaults.data(forKey: storeKey),
              let p = try? JSONDecoder().decode(LoftPrefs.self, from: data)
        else { return .default }
        return p
    }

    func save() {
        if let data = try? JSONEncoder().encode(self) {
            Group.defaults.set(data, forKey: Self.storeKey)
        }
        Group.hold = hold
        Group.demand = demand
        Group.trustedWiFi = trustedWiFi
    }
}
