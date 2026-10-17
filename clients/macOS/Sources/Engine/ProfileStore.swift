// Profiles and prefs on disk under Application Support/loft.

import Foundation

/// One loft connection: key, front URL, SOCKS/TUN/mux flags.
struct LoftProfile {
    var id: String
    var name: String
    var token: String
    var user: String = ""
    var url: String
    var kind: String
    var path: String
    var cdn: String = ""
    var socks: String
    var tun: Bool
    var mux: Bool
    var instanceID: String
    var file: String
    var yaml: String = ""
    /// SSTP TLS ServerName; empty uses the URL host.
    var sni: String = ""
    /// SSTP: verify the HTTPS server certificate (SSTP Connect default).
    var tlsCheck: Bool = true
    /// SSTP: allow PAP and CHAP-MD5. Default is MSCHAPv2 only.
    var papCHAP: Bool = false
    /// Extra DNS IPv4, comma or space. Empty loft/SSTP = handshake Status (node / IPCP).
    var dns: String = ""
    /// SSTP: use only `dns`, skip IPCP and the gateway.
    var dnsReplace: Bool = false
    /// Write resolvers into macOS Network Services while this session's TUN is up.
    var dnsSystem: Bool = false
    /// Node X25519 public (share-URI `pbk` / YAML `mask_pub`). Empty = learn from Status.
    var maskPub: String = ""

    /// Canonical SSTP DNS field: unique IPv4, comma-separated.
    static func normalizeDNS(_ raw: String) -> String {
        let parts = raw.components(separatedBy: CharacterSet(charactersIn: ",; \t\n\r"))
        var out: [String] = []
        var seen = Set<String>()
        for p in parts {
            let s = p.trimmingCharacters(in: .whitespacesAndNewlines)
            let oct = s.split(separator: ".", omittingEmptySubsequences: false)
            guard oct.count == 4,
                  let a = UInt8(oct[0]), let b = UInt8(oct[1]),
                  let c = UInt8(oct[2]), let d = UInt8(oct[3]) else { continue }
            if a == 0 && b == 0 && c == 0 && d == 0 { continue }
            let ip = "\(a).\(b).\(c).\(d)"
            if seen.contains(ip) { continue }
            seen.insert(ip)
            out.append(ip)
        }
        return out.joined(separator: ", ")
    }

    /// CSV for loft-priv `--dns`. Custom list wins; else Status overlay/IPCP resolvers.
    func systemDNSCSV(overlay: String) -> String {
        let custom = LoftProfile.normalizeDNS(dns)
        let raw = custom.isEmpty ? overlay : custom
        return LoftProfile.normalizeDNS(raw)
            .components(separatedBy: ",")
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
            .joined(separator: ",")
    }

    /// Loft fronts this GUI can open. SSTP is a different VPN, not a front.
    static let frontKinds = ["get", "ws", "h2", "quic", "h3"]
    static let frontTitles = ["GET", "WS", "H2", "QUIC", "H3"]

    /// True for Microsoft SSTP (PPP over HTTPS), not a loft front.
    var isSSTP: Bool { kind.lowercased() == "sstp" }
    /// True for VLESS (TCP+TLS / REALITY / XHTTP).
    var isVLESS: Bool { kind.lowercased() == "vless" }
    /// True for Trojan (TCP+TLS / REALITY / XHTTP).
    var isTrojan: Bool { kind.lowercased() == "trojan" }

    /// Index of `kind` in `frontKinds`, or 0 for unknown.
    static func frontIndex(_ kind: String) -> Int {
        frontKinds.firstIndex(of: kind.lowercased()) ?? 0
    }

    /// Kind at `i`, or get.
    static func frontKind(_ i: Int) -> String {
        (i >= 0 && i < frontKinds.count) ? frontKinds[i] : "get"
    }

    /// True for get/ws/h2/quic/h3, sstp, vless, or trojan.
    static func knownFront(_ kind: String) -> Bool {
        let k = kind.lowercased()
        return frontKinds.contains(k) || k == "sstp" || k == "vless" || k == "trojan"
    }

    /// True if SOCKS is on (non-empty, not off/false/0/-).
    var socksOn: Bool {
        let s = socks.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return !s.isEmpty && s != "false" && s != "off" && s != "0" && s != "-"
    }

    /// Host and dedicated port from `socks`, or nil when SOCKS is off or uses the Settings port.
    func socksParts() -> (host: String, port: UInt16)? {
        let s = socks.trimmingCharacters(in: .whitespacesAndNewlines)
        if !socksOn { return nil }
        let low = s.lowercased()
        if low == "auto" || low == "default" || low == "true" || low == "on" || low == "1" { return nil }
        if let u = UInt16(s), u > 0 { return ("127.0.0.1", u) }
        if s.hasPrefix(":"), let u = UInt16(s.dropFirst()), u > 0 { return ("127.0.0.1", u) }
        if let r = s.range(of: ":", options: .backwards) {
            let host = String(s[..<r.lowerBound])
            if let u = UInt16(String(s[r.upperBound...])), u > 0 {
                return (host.isEmpty ? "127.0.0.1" : host, u)
            }
        }
        return nil
    }

    /// Dedicated port when it differs from `defaultPort`. Nil means the Settings port.
    func dedicatedPort(relativeTo defaultPort: Int) -> UInt16? {
        guard let p = socksParts() else { return nil }
        if Int(p.port) == defaultPort { return nil }
        return p.port
    }

    /// `host:port` for display in the editor when this profile has its own port.
    func dedicatedSocksDisplay(defaultPort: Int) -> String {
        guard let port = dedicatedPort(relativeTo: defaultPort), let parts = socksParts() else { return "" }
        return "\(parts.host):\(port)"
    }

    /// Turns a typed field into `auto` or `host:port`.
    static func normalizeSocks(_ raw: String, defaultPort: Int) -> String {
        let s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.isEmpty { return "auto" }
        let tmp = LoftProfile(id: "", name: "", token: "x", user: "", url: "x", kind: "get", path: "", cdn: "", socks: s, tun: false, mux: true, instanceID: "", file: "")
        if !tmp.socksOn { return "auto" }
        guard let p = tmp.socksParts() else { return "auto" }
        if Int(p.port) == defaultPort { return "auto" }
        return "\(p.host):\(p.port)"
    }
}

/// YAML profiles and prefs.yaml under Application Support/loft.
final class ProfileStore {
    /// When the Dock icon is shown.
    enum DockMode: String {
        case always, window, never
    }

    private(set) var profiles: [LoftProfile] = []
    var activeID: String = ""
    var multi = false
    var sharedSocks = true
    var socksPort = 1080
    var hold = false
    var failover = true
    var split = false
    var defaultRoute = true
    var diag = false
    var simple = false
    var rateBits = true
    var login = false
    var dock: DockMode = .window
    var autoConnect = false
    var reconnect = true
    var showExtIP = true
    var extIPURL = ProfileStore.defaultExtIPURL
    var lang = "ru"
    let routes = SplitStore()

    /// Plain-text echo used when the pref is empty or garbage.
    static let defaultExtIPURL = "https://api.ipify.org"

    /// http(s) URL, or the factory echo.
    static func normalizeExtIPURL(_ s: String) -> String {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { return defaultExtIPURL }
        guard let u = URL(string: t), let sch = u.scheme?.lowercased(), sch == "http" || sch == "https", u.host != nil else {
            return defaultExtIPURL
        }
        return t
    }

    /// 1...65535; garbage becomes 1080.
    static func clampPort(_ n: Int) -> Int {
        if n < 1 || n > 65535 { return 1080 }
        return n
    }

    var dir: URL {
        let b = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        return b.appendingPathComponent("loft", isDirectory: true)
    }

    /// Loads prefs.yaml, active id, and every profile YAML.
    func load() {
        let fm = FileManager.default
        let profilesDir = dir.appendingPathComponent("profiles", isDirectory: true)
        try? fm.createDirectory(at: profilesDir, withIntermediateDirectories: true)
        let prefs = dir.appendingPathComponent("prefs.yaml")
        var hadSocksPort = false
        if let s = try? String(contentsOf: prefs, encoding: .utf8) {
            /// True/false for `k:` in the prefs blob, or nil if absent.
            func flag(_ k: String) -> Bool? {
                if s.contains("\(k): true") { return true }
                if s.contains("\(k): false") { return false }
                return nil
            }
            /// Integer after `k:` on its own prefs line.
            func intVal(_ k: String) -> Int? {
                for raw in s.components(separatedBy: "\n") {
                    let t = raw.trimmingCharacters(in: .whitespaces)
                    let prefix = k + ":"
                    if t.lowercased().hasPrefix(prefix) {
                        let v = t.dropFirst(prefix.count).trimmingCharacters(in: .whitespaces)
                        return Int(v)
                    }
                }
                return nil
            }
            /// Remainder after `k:` on its own prefs line.
            func strVal(_ k: String) -> String? {
                for raw in s.components(separatedBy: "\n") {
                    let t = raw.trimmingCharacters(in: .whitespaces)
                    let prefix = k + ":"
                    if t.lowercased().hasPrefix(prefix) {
                        return String(t.dropFirst(prefix.count)).trimmingCharacters(in: .whitespaces)
                    }
                }
                return nil
            }
            if let v = flag("multi") { multi = v }
            if let v = flag("shared_socks") { sharedSocks = v }
            if s.contains("socks: 0") { sharedSocks = false }
            if let n = intVal("socks_port") {
                socksPort = Self.clampPort(n)
                hadSocksPort = true
            }
            if let v = flag("simple") { simple = v }
            if let v = flag("rate_bits") { rateBits = v }
            if let v = flag("hold") { hold = v }
            if let v = flag("failover") { failover = v }
            if let v = flag("split") { split = v }
            if let v = flag("default_route") { defaultRoute = v }
            if let v = flag("diag") { diag = v }
            if let v = flag("login") { login = v }
            if s.contains("dock: always") { dock = .always }
            else if s.contains("dock: never") { dock = .never }
            else if s.contains("dock: window") { dock = .window }
            else if let v = flag("dock") { dock = v ? .always : .window }
            if let v = flag("auto") { autoConnect = v }
            if let v = flag("reconnect") { reconnect = v }
            if let v = flag("ext_ip") { showExtIP = v }
            if let v = strVal("ext_ip_url"), !v.isEmpty { extIPURL = Self.normalizeExtIPURL(v) }
            if let v = strVal("lang"), L10n.Lang(rawValue: v.lowercased()) != nil {
                lang = v.lowercased()
            }
        }
        let activeFile = dir.appendingPathComponent("active")
        activeID = (try? String(contentsOf: activeFile, encoding: .utf8))?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        var list: [LoftProfile] = []
        if let files = try? fm.contentsOfDirectory(at: profilesDir, includingPropertiesForKeys: nil) {
            for u in files where u.pathExtension == "yaml" || u.pathExtension == "loft" {
                if let p = parse(url: u) { list.append(p) }
            }
        }
        profiles = list.sorted { $0.name < $1.name }
        routes.load(dir: dir)
        ensureActive()
        if !hadSocksPort {
            for i in profiles.indices {
                var p = profiles[i]
                if let parts = p.socksParts(), parts.port == 1080 {
                    p.socks = "auto"
                    if !p.yaml.isEmpty { p.yaml = upsert(p.yaml, "socks5", "auto") }
                    save(p)
                }
            }
            savePrefs()
        }
    }

    /// Factory prefs: toggles, SOCKS port, units. Language and profiles stay.
    func restoreDefaults() {
        multi = false
        sharedSocks = true
        socksPort = 1080
        hold = false
        failover = true
        split = false
        defaultRoute = true
        diag = false
        simple = false
        rateBits = true
        login = false
        dock = .window
        autoConnect = false
        reconnect = true
        showExtIP = true
        extIPURL = Self.defaultExtIPURL
        savePrefs()
    }

    /// Writes prefs.yaml and the active id file.
    func savePrefs() {
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let s = """
        multi: \(multi)
        shared_socks: \(sharedSocks)
        socks_port: \(socksPort)
        simple: \(simple)
        rate_bits: \(rateBits)
        hold: \(hold)
        failover: \(failover)
        split: \(split)
        default_route: \(defaultRoute)
        diag: \(diag)
        login: \(login)
        dock: \(dock.rawValue)
        auto: \(autoConnect)
        reconnect: \(reconnect)
        ext_ip: \(showExtIP)
        ext_ip_url: \(extIPURL)
        lang: \(lang)
        """
        try? s.write(to: dir.appendingPathComponent("prefs.yaml"), atomically: true, encoding: .utf8)
        try? activeID.write(to: dir.appendingPathComponent("active"), atomically: true, encoding: .utf8)
    }

    /// Writes `p` as YAML, preserving original keys when `p.yaml` is set.
    func save(_ p: LoftProfile) {
        var p = p
        let profilesDir = dir.appendingPathComponent("profiles", isDirectory: true)
        try? FileManager.default.createDirectory(at: profilesDir, withIntermediateDirectories: true)
        let yaml: String
        if !p.yaml.isEmpty {
            yaml = upsert(upsert(p.yaml, "instance_id", p.instanceID), "token", p.token)
        } else {
            yaml = exportYAML(p)
        }
        p.yaml = yaml
        let u = profilesDir.appendingPathComponent(p.id + ".yaml")
        try? yaml.write(to: u, atomically: true, encoding: .utf8)
        if let i = profiles.firstIndex(where: { $0.id == p.id }) { profiles[i] = p } else { profiles.append(p) }
    }

    /// Saves `p` from the editor, regenerating YAML.
    func saveEdited(_ p: LoftProfile) {
        var p = p
        p.yaml = ""
        save(p)
    }

    /// Replaces or appends `key: val` in a YAML blob.
    private func upsert(_ src: String, _ key: String, _ val: String) -> String {
        var found = false
        var out: [String] = []
        for line in src.components(separatedBy: "\n") {
            let t = line.trimmingCharacters(in: .whitespaces)
            if t.hasPrefix(key + ":") {
                let indent = String(line.prefix { $0 == " " || $0 == "\t" })
                out.append("\(indent)\(key): \(yamlQ(val))")
                found = true
            } else {
                out.append(line)
            }
        }
        if !found { out.append("\(key): \(yamlQ(val))") }
        return out.joined(separator: "\n")
    }

    /// Parses `url`. One outbound → one profile; several → one profile each.
    func importFile(_ url: URL) -> LoftProfile? {
        let list = parseAll(url: url)
        guard !list.isEmpty else { return nil }
        var saved: [LoftProfile] = []
        for var p in list {
            p.yaml = ""
            p.id = uniqueID(p.name)
            save(p)
            saved.append(p)
        }
        profiles.sort { $0.name < $1.name }
        let primary = saved.first(where: { $0.kind == "get" }) ?? saved.first
        ensureActive()
        if activeID.isEmpty, let p = primary { activeID = p.id; savePrefs() }
        return primary
    }

    /// Slug from `name`, with `-2`, `-3`… if that id is already in the store.
    func uniqueID(_ name: String) -> String {
        var id = slug(name)
        if id.isEmpty { id = UUID().uuidString }
        let base = id
        var n = 2
        while profiles.contains(where: { $0.id == id }) {
            id = "\(base)-\(n)"
            n += 1
        }
        return id
    }

    /// Deletes the profile YAML and picks a new active id if needed.
    func remove(_ id: String) {
        if id.isEmpty { return }
        profiles.removeAll { $0.id == id }
        let u = dir.appendingPathComponent("profiles", isDirectory: true).appendingPathComponent(id + ".yaml")
        try? FileManager.default.removeItem(at: u)
        if activeID == id || !profiles.contains(where: { $0.id == activeID }) {
            activeID = profiles.first?.id ?? ""
            savePrefs()
        }
    }

    /// If `activeID` is missing or empty, points it at the first profile.
    func ensureActive() {
        if !activeID.isEmpty, profiles.contains(where: { $0.id == activeID }) { return }
        let next = profiles.first?.id ?? ""
        if activeID == next { return }
        activeID = next
        savePrefs()
    }

    /// Canonical client.yaml for `p`, or the original blob if still set.
    func exportYAML(_ p: LoftProfile) -> String {
        if !p.yaml.isEmpty { return p.yaml }
        if p.isVLESS {
            let sniLine = p.sni.isEmpty ? "" : "\nsni: \(yamlQ(p.sni))"
            return """
            name: \(yamlQ(p.name))
            kind: vless
            uuid: \(yamlQ(p.token))\(sniLine)
            tls_check: \(p.tlsCheck)\(dnsYAML(p))
            listen:
              socks5: \(p.socksOn ? yamlQ(p.socks) : "false")
              tun: \(p.tun ? "auto" : "false")
            outbounds:
              - name: \(yamlQ(p.name))
                type: vless
                url: \(yamlQ(p.url))
            """
        }
        if p.isTrojan {
            let sniLine = p.sni.isEmpty ? "" : "\nsni: \(yamlQ(p.sni))"
            return """
            name: \(yamlQ(p.name))
            kind: trojan
            password: \(yamlQ(p.token))\(sniLine)
            tls_check: \(p.tlsCheck)\(dnsYAML(p))
            listen:
              socks5: \(p.socksOn ? yamlQ(p.socks) : "false")
              tun: \(p.tun ? "auto" : "false")
            outbounds:
              - name: \(yamlQ(p.name))
                type: trojan
                url: \(yamlQ(p.url))
            """
        }
        if p.isSSTP {
            let sniLine = p.sni.isEmpty ? "" : "\nsni: \(yamlQ(p.sni))"
            return """
            name: \(yamlQ(p.name))
            username: \(yamlQ(p.user))
            password: \(yamlQ(p.token))\(sniLine)\(dnsYAML(p))
            tls_check: \(p.tlsCheck)
            pap_chap: \(p.papCHAP)
            listen:
              socks5: \(p.socksOn ? yamlQ(p.socks) : "false")
              tun: \(p.tun ? "auto" : "false")
            outbounds:
              - name: \(yamlQ(p.name))
                type: sstp
                url: \(yamlQ(p.url))
            """
        }
        return """
        name: \(yamlQ(p.name))
        token: \(yamlQ(p.token))
        instance_id: \(yamlQ(p.instanceID))\(dnsYAML(p))
        select: auto
        listen:
          socks5: \(p.socksOn ? yamlQ(p.socks) : "false")
          tun: \(p.tun ? "auto" : "false")
        mux: \(p.mux)
        outbounds:
          - name: \(yamlQ(p.name))
            type: \(p.kind)
            url: \(yamlQ(p.url))
            path: \(yamlQ(p.path))\(p.cdn.isEmpty ? "" : "\n            cdn: \(yamlQ(p.cdn))")\(p.maskPub.isEmpty ? "" : "\n            mask_pub: \(yamlQ(p.maskPub))")
        """
    }

    /// Active profile, or the first if the id is missing.
    func active() -> LoftProfile? {
        profiles.first(where: { $0.id == activeID }) ?? profiles.first
    }

    /// Sets the active id and writes prefs.
    func select(_ id: String) {
        activeID = id
        savePrefs()
    }

    /// Persist path for this profile's overlay `instance_id` (one file per connection).
    func instanceFile(for id: String) -> String {
        let d = dir.appendingPathComponent("sessions", isDirectory: true)
        try? FileManager.default.createDirectory(at: d, withIntermediateDirectories: true)
        return d.appendingPathComponent(id + ".instance").path
    }

    /// Local SOCKS bind for `p`. Shared uses `socksPort`; otherwise unique, dedicated first.
    /// `taken` are ports already bound by live sessions so a second loft-front does not collide.
    func socksBind(for p: LoftProfile, taken: Set<UInt16> = []) -> String {
        var list = profiles
        if let i = list.firstIndex(where: { $0.id == p.id }) {
            list[i] = p
        } else {
            list.append(p)
        }
        return socksMap(list, taken: taken)[p.id] ?? ""
    }

    /// Bind address per profile id from `list`.
    private func socksMap(_ list: [LoftProfile], taken: Set<UInt16> = []) -> [String: String] {
        var used = taken
        for q in list where q.socksOn {
            if let d = q.dedicatedPort(relativeTo: socksPort) { used.insert(d) }
        }
        var map: [String: String] = [:]
        let def = UInt16(clamping: Self.clampPort(socksPort))
        for q in list {
            if !q.socksOn {
                map[q.id] = ""
                continue
            }
            let host = q.socksParts()?.host ?? "127.0.0.1"
            if let d = q.dedicatedPort(relativeTo: socksPort) {
                map[q.id] = "\(host):\(d)"
                continue
            }
            if sharedSocks && !taken.contains(def) {
                map[q.id] = "\(host):\(def)"
                continue
            }
            var port = def
            let start = port
            while used.contains(port) {
                port = port == 65535 ? 1024 : port &+ 1
                if port == start { break }
            }
            used.insert(port)
            map[q.id] = "\(host):\(port)"
        }
        return map
    }

    /// SSTP GUI export (`{"profiles":[…],"encrypted":false}`). One profile per entry.
    private func parseVPNJSON(_ s: String, fileName: String) -> [LoftProfile] {
        guard let data = s.data(using: .utf8),
              let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [] }
        if (root["encrypted"] as? Bool) == true { return [] }
        var rows: [[String: Any]] = []
        if let arr = root["profiles"] as? [[String: Any]] {
            rows = arr
        } else if root["server"] != nil || root["username"] != nil {
            rows = [root]
        }
        return rows.compactMap { d in
            let proto = jsonInt(d["proto"])
            if proto != 0 { return nil }
            let server = jsonStr(d["server"]).trimmingCharacters(in: .whitespaces)
            if server.isEmpty { return nil }
            let port = jsonStr(d["port"])
            let host = (port.isEmpty || port == "443") ? server : "\(server):\(port)"
            let user = jsonStr(d["username"])
            let pass = decodeVPNPass(jsonStr(d["password"]))
            if user.isEmpty || pass.isEmpty { return nil }
            let name = jsonStr(d["name"])
            let tun = jsonBool(d["defaultRoute"], true)
            let sni = jsonStr(d["hostname"])
            return LoftProfile(
                id: fileName,
                name: name.isEmpty ? server : name,
                token: pass,
                user: user,
                url: "https://\(host)",
                kind: "sstp",
                path: "",
                socks: "auto",
                tun: tun,
                mux: false,
                instanceID: "",
                file: fileName + ".vpn",
                sni: sni,
                tlsCheck: jsonBool(d["tlsCheck"], true),
                papCHAP: jsonBool(d["insecureLogin"], false)
            )
        }
    }

    /// JSON string or number as text.
    private func jsonStr(_ v: Any?) -> String {
        if let s = v as? String { return s }
        if let n = v as? NSNumber { return n.stringValue }
        return ""
    }

    /// JSON bool, or `fallback` when the key is missing.
    private func jsonBool(_ v: Any?, _ fallback: Bool) -> Bool {
        if let b = v as? Bool { return b }
        if let n = v as? NSNumber { return n.boolValue }
        return fallback
    }

    /// JSON int, or 0.
    private func jsonInt(_ v: Any?) -> Int {
        if let n = v as? NSNumber { return n.intValue }
        if let s = v as? String, let n = Int(s) { return n }
        return 0
    }

    /// Lowercase id from `name`: alphanumerics, dash, underscore.
    func slug(_ name: String) -> String {
        let s = name.lowercased().replacingOccurrences(of: " ", with: "-")
        let extra = CharacterSet(charactersIn: "-_")
        return String(s.unicodeScalars.filter { CharacterSet.alphanumerics.contains($0) || extra.contains($0) })
    }

    /// Parses a client.yaml / .loft file. Nil without a token.
    private func parse(url: URL) -> LoftProfile? {
        parseAll(url: url).first
    }

    /// Profiles from a `.loft` / YAML / `.vpn` file, a share URI, or a QR image.
    func parseShare(url: URL) -> [LoftProfile] {
        parseAll(url: url)
    }

    /// One profile per outbound. Shared token / SOCKS / TUN / mux from the file head.
    private func parseAll(url: URL) -> [LoftProfile] {
        let fileName = url.deletingPathExtension().lastPathComponent
        if QRScan.isImage(url) {
            var out: [LoftProfile] = []
            var seen = Set<String>()
            for payload in QRScan.payloads(from: url) {
                for p in parseBlob(payload, fileName: fileName) {
                    let k = p.kind + "\n" + p.url + "\n" + p.token
                    if seen.insert(k).inserted { out.append(p) }
                }
            }
            return out
        }
        guard let s = try? String(contentsOf: url, encoding: .utf8) else { return [] }
        return parseBlob(s, fileName: fileName)
    }

    /// URI / YAML / SSTP JSON detector for a UTF-8 blob.
    private func parseBlob(_ s: String, fileName: String) -> [LoftProfile] {
        let share = ShareURI.profiles(from: s, fileName: fileName)
        if !share.isEmpty { return share }
        let trimmed = s.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasPrefix("{") {
            let vpn = parseVPNJSON(trimmed, fileName: fileName)
            if !vpn.isEmpty { return vpn }
        }
        var token = ""
        var instanceID = ""
        var socks = "auto"
        var tun = true
        var mux = true
        var headName = ""
        var headURL = ""
        var headKind = ""
        var headPath = ""
        var headUser = ""
        var headSNI = ""
        var headDNS = ""
        var dnsReplace = false
        var dnsSystem = false
        var tlsCheck = true
        var papCHAP = false
        var inOutbounds = false
        var items: [(name: String, type: String, url: String, path: String, cdn: String, user: String, maskPub: String)] = []
        var curName = ""
        var curType = ""
        var curURL = ""
        var curPath = ""
        var curCDN = ""
        var curUser = ""
        var curMask = ""
        /// Pushes the current outbound if it has a URL.
        func flush() {
            if curURL.isEmpty { return }
            items.append((curName, curType, curURL, curPath, curCDN, curUser, curMask))
            curName = ""; curType = ""; curURL = ""; curPath = ""; curCDN = ""; curUser = ""; curMask = ""
        }
        for raw in s.components(separatedBy: "\n") {
            let trimmed = raw.trimmingCharacters(in: .whitespaces)
            if trimmed.hasPrefix("#") || trimmed.isEmpty { continue }
            if trimmed == "outbounds:" || trimmed.hasPrefix("outbounds:") {
                inOutbounds = true
                continue
            }
            var line = trimmed
            let dashed = line.hasPrefix("- ")
            if dashed {
                line = String(line.dropFirst(2)).trimmingCharacters(in: .whitespaces)
                if inOutbounds { flush() }
            }
            let kv = line.split(separator: ":", maxSplits: 1)
            if kv.count < 2 { continue }
            let k = kv[0].trimmingCharacters(in: .whitespaces)
            let v = kv[1].trimmingCharacters(in: CharacterSet(charactersIn: " \t\"'"))
            if inOutbounds {
                switch k {
                case "name": curName = v
                case "type": curType = v
                case "url": curURL = v
                case "path": curPath = v
                case "cdn": curCDN = v
                case "mask_pub", "pbk": curMask = v
                case "username", "user": curUser = v
                case "password": if token.isEmpty { token = v }
                default: break
                }
                continue
            }
            switch k {
            case "name": headName = v
            case "kind":
                let k = v.lowercased()
                if k == "sstp" || k == "vless" || k == "trojan" { headKind = k }
            case "token", "uuid": token = v
            case "instance_id": instanceID = v
            case "url": headURL = v
            case "type": headKind = v
            case "path": headPath = v
            case "username", "user": headUser = v
            case "password": if token.isEmpty { token = v }
            case "sni", "hostname": headSNI = v
            case "dns": headDNS = LoftProfile.normalizeDNS(v)
            case "dns_replace":
                let low = v.lowercased()
                dnsReplace = low == "true" || low == "on" || low == "1"
            case "dns_system":
                let low = v.lowercased()
                dnsSystem = low == "true" || low == "on" || low == "1"
            case "tls_check", "tlsCheck":
                let low = v.lowercased()
                tlsCheck = !(low == "false" || low == "off" || low == "0")
            case "pap_chap", "pap":
                let low = v.lowercased()
                papCHAP = low == "true" || low == "on" || low == "1"
            case "socks5":
                let low = v.lowercased()
                if v.isEmpty || low == "false" || low == "off" || low == "0" || low == "-" { socks = "" }
                else { socks = v }
            case "tun": tun = (v == "auto" || v == "true")
            case "mux":
                let low = v.lowercased()
                mux = !(low == "false" || low == "off" || low == "0")
            default: break
            }
        }
        flush()
        if token.isEmpty {
            for ob in items {
                let u = ob.url.lowercased()
                if u.hasPrefix("vless://") || u.hasPrefix("trojan://"),
                   let c = URLComponents(string: ob.url) {
                    let user = (c.percentEncodedUser ?? c.user ?? "").removingPercentEncoding ?? (c.user ?? "")
                    if !user.isEmpty {
                        token = user
                        break
                    }
                }
            }
        }
        if token.isEmpty { return [] }
        if items.isEmpty && !headURL.isEmpty {
            items.append((headName, headKind, headURL, headPath, "", headUser, ""))
        }
        if items.isEmpty { return [] }
        let file = fileName
        return items.map { ob in
            let rawKind = ob.type.lowercased()
            let head = headKind.lowercased()
            let kind: String
            if LoftProfile.knownFront(rawKind) { kind = rawKind }
            else if LoftProfile.knownFront(head) { kind = head }
            else { kind = "get" }
            let name = ob.name.isEmpty ? (headName.isEmpty ? fileName : headName) : ob.name
            let path = ob.path.isEmpty ? (headPath.isEmpty ? "/static/js" : headPath) : ob.path
            let user = ob.user.isEmpty ? headUser : ob.user
            return LoftProfile(
                id: fileName,
                name: name,
                token: token,
                user: user,
                url: ob.url,
                kind: kind,
                path: path,
                cdn: ob.cdn,
                socks: socks,
                tun: tun,
                mux: mux,
                instanceID: instanceID,
                file: file,
                yaml: "",
                sni: headSNI,
                tlsCheck: tlsCheck,
                papCHAP: papCHAP,
                dns: headDNS,
                dnsReplace: dnsReplace,
                dnsSystem: dnsSystem,
                maskPub: ob.maskPub
            )
        }
    }

    /// Optional dns / dns_replace / dns_system lines for a profile YAML.
    private func dnsYAML(_ p: LoftProfile) -> String {
        let dns = LoftProfile.normalizeDNS(p.dns)
        var s = ""
        if !dns.isEmpty { s += "\ndns: \(yamlQ(dns))" }
        if p.isSSTP, !dns.isEmpty, p.dnsReplace { s += "\ndns_replace: true" }
        if p.dnsSystem { s += "\ndns_system: true" }
        return s
    }

    /// Quotes `s` when it contains `:`, `#`, or a space.
    private func yamlQ(_ s: String) -> String {
        if s.contains(":") || s.contains("#") || s.contains(" ") { return "\"\(s.replacingOccurrences(of: "\"", with: "\\\""))\"" }
        return s
    }

    /// SSTP Connect `.vpn` stores the password as standard Base64.
    private func decodeVPNPass(_ s: String) -> String {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { return t }
        guard let data = Data(base64Encoded: t), !data.isEmpty,
              let out = String(data: data, encoding: .utf8), !out.isEmpty else { return t }
        return out
    }
}
