import Foundation

enum SparkStyle: String, Codable, CaseIterable {
    case lines, bars
}

/// Settings snapshot. Canvas / sim; Packet Tunnel later reads `hold` from the app group.
struct LoftPrefs: Equatable, Codable {
    var simple = false
    var bits = true
    var spark: SparkStyle = .lines
    /// `en` / `ru` / `zh`. Empty follows the system locale.
    var lang = ""
    var diag = false
    var defaultRoute = true
    var hold = true
    var split = true
    var autoConnect = false
    var reconnect = true
    var demand = false
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
    }
}
