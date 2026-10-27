/*
 ConnStore.swift - the list of saved connections (Conn) and which one is
 active. It is kept as JSON in the App Group (Shared/Group.swift). The
 Connections screen (App/ConnectionsView.swift) and the editor
 (App/ConnEditView.swift) change it, and the active entry's name and protocol
 are copied into the Home snapshot (App/HomeModel.swift).
*/
import Foundation

/// In-memory / App Group list. YAML profiles come in a later slice.
///
/// One saved connection as the UI edits it. `uri` is a share link
/// (loft://, vless://, …) or a URL; `secret` is the member key, UUID or
/// password typed by hand; `front` is the loft front (get, ws, h2, quic, h3,
/// l3) or "auto".
struct Conn: Identifiable, Equatable, Codable {
    /// Stable id (a UUID for new entries). `name` may be empty; then the
    /// host is shown instead. `user` is the SSTP user name.
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

    /// Short protocol label for the list row, e.g. "loft · get".
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

    /// A new empty connection with a fresh id, port 443 and front "auto".
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

    /// A plain-text summary for sharing: kind, host, port and uri (which,
    /// for a share link, carries the key). `secret` is not written.
    func exportText() -> String {
        var lines = ["# \(fileName)", "kind: \(kind.rawValue)", "host: \(host)"]
        if !port.isEmpty { lines.append("port: \(port)") }
        if !uri.isEmpty { lines.append("uri: \(uri)") }
        return lines.joined(separator: "\n") + "\n"
    }

    /// Suggested file name for sharing: "<name or host>.loft".
    var fileName: String {
        let base = name.isEmpty ? host : name
        let safe = base.isEmpty ? "loft" : base
        return "\(safe).loft"
    }
}

/// The saved connections and which one is active, stored as JSON in the
/// App Group. An empty or unreadable store falls back to the sample list.
struct ConnStore: Equatable, Codable {
    var items: [Conn]
    var activeId: String

    /// Three demo connections (loft, SSTP, VLESS) with placeholder keys.
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

    /// What a fresh install starts with.
    static let `default` = sample
    /// App Group key that holds the JSON-encoded store.
    private static let storeKey = "ui.conns"

    /// The active connection, if its id is still in the list.
    var active: Conn? { item(activeId) }

    /// Finds a connection by id.
    func item(_ id: String) -> Conn? {
        items.first { $0.id == id }
    }

    /// Reads the saved store; missing, unreadable or empty data gives the sample.
    static func load() -> ConnStore {
        guard let data = Group.defaults.data(forKey: storeKey),
              let s = try? JSONDecoder().decode(ConnStore.self, from: data),
              !s.items.isEmpty
        else { return .sample }
        return s
    }

    /// Writes the store as JSON into the App Group.
    func save() {
        if let data = try? JSONEncoder().encode(self) {
            Group.defaults.set(data, forKey: Self.storeKey)
        }
    }

    /// Replaces the connection with the same id, or appends it if new.
    mutating func apply(_ c: Conn) {
        if let i = items.firstIndex(where: { $0.id == c.id }) {
            items[i] = c
        } else {
            items.append(c)
        }
    }

    /// Saves an edited connection and, if it is the active one, updates
    /// the name and protocol shown on Home.
    mutating func apply(_ c: Conn, home: inout HomeSnapshot) {
        apply(c)
        if c.id == activeId {
            home.profile = c.name.isEmpty ? c.host : c.name
            home.kind = c.kind
        }
    }

    /// The row's Connect / Disconnect button: makes id active and flips the
    /// Home state. Connections are matched to Home by display title.
    mutating func toggle(_ id: String, home: inout HomeSnapshot) {
        guard let c = item(id) else { return }
        let title = c.name.isEmpty ? c.host : c.name
        let thisUp = (home.isLive || home.mode == .connecting) && home.profile == title
        activeId = id
        home.profile = title
        home.kind = c.kind
        home.mode = thisUp ? .off : .on
    }

    /// Deletes a connection. If it was active, the first remaining one
    /// becomes active and a live Home state for it is turned off.
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
