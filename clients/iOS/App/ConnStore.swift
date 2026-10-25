import Foundation

/// In-memory / App Group list. YAML profiles come in a later slice.
struct Conn: Identifiable, Equatable, Codable {
    var id: String
    var name: String
    var host: String
    var kind: HomeSnapshot.Kind
    var front: String
    var port: String
    var path: String
    var uri: String
    var secret: String
    var user: String

    var side: String {
        switch kind {
        case .loft:
            let f = front.isEmpty ? "get" : front
            return "loft · \(f)"
        case .sstp: return "sstp"
        case .vless: return "vless"
        case .trojan: return "trojan"
        }
    }

    static func blank(kind: HomeSnapshot.Kind = .loft) -> Conn {
        Conn(
            id: UUID().uuidString,
            name: "",
            host: "",
            kind: kind,
            front: "auto",
            port: "443",
            path: "",
            uri: "",
            secret: "",
            user: ""
        )
    }

    func exportText() -> String {
        var lines = ["# \(fileName)", "kind: \(kind.rawValue)", "host: \(host)"]
        if !port.isEmpty { lines.append("port: \(port)") }
        if !uri.isEmpty { lines.append("uri: \(uri)") }
        return lines.joined(separator: "\n") + "\n"
    }

    var fileName: String {
        let base = name.isEmpty ? host : name
        let safe = base.isEmpty ? "loft" : base
        return "\(safe).loft"
    }
}

struct ConnStore: Equatable, Codable {
    var items: [Conn]
    var activeId: String

    static let sample = ConnStore(
        items: [
            Conn(
                id: "home",
                name: "Home",
                host: "cdn.example.com",
                kind: .loft,
                front: "get",
                port: "443",
                path: "/static/v3/build",
                uri: "loft://lt_••••••@cdn.example.com/static/v3/build?type=get&cdn=yandex#Home",
                secret: "",
                user: ""
            ),
            Conn(
                id: "office",
                name: "Office",
                host: "vpn.example.com",
                kind: .sstp,
                front: "",
                port: "443",
                path: "",
                uri: "https://vpn.example.com",
                secret: "",
                user: "alice"
            ),
            Conn(
                id: "travel",
                name: "Travel",
                host: "de1.example.com",
                kind: .vless,
                front: "",
                port: "443",
                path: "",
                uri: "vless://••••••@de1.example.com:443?security=reality&sni=www.example.com&fp=chrome",
                secret: "",
                user: ""
            ),
        ],
        activeId: "home"
    )

    static let `default` = sample
    private static let storeKey = "ui.conns"

    var active: Conn? { item(activeId) }

    func item(_ id: String) -> Conn? {
        items.first { $0.id == id }
    }

    static func load() -> ConnStore {
        guard let data = Group.defaults.data(forKey: storeKey),
              let s = try? JSONDecoder().decode(ConnStore.self, from: data),
              !s.items.isEmpty
        else { return .sample }
        return s
    }

    func save() {
        if let data = try? JSONEncoder().encode(self) {
            Group.defaults.set(data, forKey: Self.storeKey)
        }
    }

    mutating func apply(_ c: Conn) {
        if let i = items.firstIndex(where: { $0.id == c.id }) {
            items[i] = c
        } else {
            items.append(c)
        }
    }

    mutating func apply(_ c: Conn, home: inout HomeSnapshot) {
        apply(c)
        if c.id == activeId {
            home.profile = c.name.isEmpty ? c.host : c.name
            home.kind = c.kind
        }
    }

    mutating func toggle(_ id: String, home: inout HomeSnapshot) {
        guard let c = item(id) else { return }
        let title = c.name.isEmpty ? c.host : c.name
        let thisUp = (home.isLive || home.mode == .connecting) && home.profile == title
        activeId = id
        home.profile = title
        home.kind = c.kind
        home.mode = thisUp ? .off : .on
    }

    mutating func remove(_ id: String, home: inout HomeSnapshot) {
        let wasActive = id == activeId
        let title = item(id).map { $0.name.isEmpty ? $0.host : $0.name }
        items.removeAll { $0.id == id }
        if wasActive {
            if let title, (home.isLive || home.mode == .connecting), home.profile == title {
                home.mode = .off
            }
            activeId = items.first?.id ?? ""
            if let c = active {
                home.profile = c.name.isEmpty ? c.host : c.name
                home.kind = c.kind
            } else {
                home.profile = ""
                home.mode = .off
            }
        }
    }
}
