import Foundation

enum Group {
    static let id = "group.app.loft"
    static let yamlKey = "session.yaml"
    static let holdKey = "hold"

    static var defaults: UserDefaults {
        UserDefaults(suiteName: id) ?? .standard
    }

    static var yaml: String {
        get { defaults.string(forKey: yamlKey) ?? "" }
        set { defaults.set(newValue, forKey: yamlKey) }
    }

    static var hold: Bool {
        get { defaults.bool(forKey: holdKey) }
        set { defaults.set(newValue, forKey: holdKey) }
    }
}
