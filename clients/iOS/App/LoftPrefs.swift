/*
 LoftPrefs.swift - the user settings of the iOS app (the Settings tab).
 The whole struct is stored as JSON in the App Group (Shared/Group.swift).
 Three of the values also matter to the system VPN: hold (kill switch),
 On Demand and the trusted Wi-Fi list. save() copies them into their own
 App Group keys, and TunnelControl (App/TunnelControl.swift) puts them into
 the iOS VPN profile.
*/
import Foundation

/// How the traffic graph on Home is drawn.
enum SparkStyle: String, Codable, CaseIterable {
    case lines, bars
}

/// Settings snapshot. `save()` writes hold / demand / trusted Wi‑Fi into the app group.
struct LoftPrefs: Equatable, Codable {
    /// Reduced Home: only the lamp, title and button, no plates or graph.
    var simple = false
    /// Rates in megabits per second (true) or kilobytes per second (false).
    var bits = true
    var spark: SparkStyle = .lines
    /// `en` / `ru` / `zh`. Empty follows the system locale.
    var lang = ""
    /// Settings toggles that are stored but not acted on anywhere in the
    /// iOS app yet: diagnostics, default route, split routing, auto connect
    /// and reconnect.
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
    /// Show the External IP row on Home.
    var showExtIP = false

    /// Factory settings ("Defaults" in Settings resets to this).
    static let `default` = LoftPrefs()
    /// App Group key that holds the JSON-encoded struct.
    private static let storeKey = "ui.prefs"

    /// The locale the UI should use for `lang`.
    var locale: Locale {
        switch lang {
        case "ru": return Locale(identifier: "ru")
        case "zh": return Locale(identifier: "zh-Hans")
        case "en": return Locale(identifier: "en")
        default: return .current
        }
    }

    /// Reads saved settings; missing or unreadable data gives the defaults.
    static func load() -> LoftPrefs {
        guard let data = Group.defaults.data(forKey: storeKey),
              let p = try? JSONDecoder().decode(LoftPrefs.self, from: data)
        else { return .default }
        return p
    }

    /// Writes the settings and mirrors hold, On Demand and trusted Wi-Fi
    /// into their own App Group keys, which the Packet Tunnel reads.
    func save() {
        if let data = try? JSONEncoder().encode(self) {
            Group.defaults.set(data, forKey: Self.storeKey)
        }
        Group.hold = hold
        Group.demand = demand
        Group.trustedWiFi = trustedWiFi
    }
}
