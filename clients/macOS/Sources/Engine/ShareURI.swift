// Share-string detector: loft://, sstp://, vless://, trojan://, YAML, SSTP .vpn JSON (docs/share.md).

import Foundation

/// Client-side import of a paste, file, or QR payload. Not a wire feature.
enum ShareURI {
    /// Profiles in `raw`. Empty when the string is not a known share.
    static func profiles(from raw: String, fileName: String) -> [LoftProfile] {
        parse(raw, fileName: fileName, allowB64: true)
    }

    /// One pass of the detector in docs/share.md §1.
    private static func parse(_ raw: String, fileName: String, allowB64: Bool) -> [LoftProfile] {
        var s = raw
        if s.hasPrefix("\u{FEFF}") { s.removeFirst() }
        s = s.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.isEmpty { return [] }
        let lines = s.components(separatedBy: .newlines)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        if lines.count > 1 {
            var out: [LoftProfile] = []
            var seen = Set<String>()
            for line in lines {
                for p in parse(line, fileName: fileName, allowB64: allowB64) {
                    let k = p.kind + "\n" + p.url + "\n" + p.token
                    if seen.insert(k).inserted { out.append(p) }
                }
            }
            if !out.isEmpty { return out }
        }
        let low = s.lowercased()
        if low.hasPrefix("loft://") {
            if let p = loft(s, fileName: fileName) { return [p] }
            return []
        }
        if low.hasPrefix("vless://") {
            if let p = vless(s, fileName: fileName) { return [p] }
            return []
        }
        if low.hasPrefix("trojan://") {
            if let p = trojan(s, fileName: fileName) { return [p] }
            return []
        }
        if low.hasPrefix("sstp://") {
            if let p = sstp(s, fileName: fileName) { return [p] }
            return []
        }
        if allowB64, let decoded = unwrapBase64(s) {
            return parse(decoded, fileName: fileName, allowB64: false)
        }
        return []
    }

    /// `loft://<token>@<host>[:port][/path][?query][#name]`.
    static func loft(_ raw: String, fileName: String) -> LoftProfile? {
        guard let c = URLComponents(string: raw.trimmingCharacters(in: .whitespacesAndNewlines)) else { return nil }
        guard (c.scheme ?? "").lowercased() == "loft" else { return nil }
        let token = decode(c.percentEncodedUser ?? c.user ?? "")
        let host = c.host ?? ""
        if token.isEmpty || host.isEmpty { return nil }
        let port = c.port ?? 443
        var path = c.path
        if path == "/" { path = "" }
        let q = query(c)
        let frontKeys = ["get", "ws", "h2", "h3", "quic"]
        let hasFronts = frontKeys.contains { q.keys.contains($0) }
        var type = (q["type"] ?? "get").lowercased()
        var wsPath = ""
        var h2Path = ""
        var h3Path = ""
        var quic = false
        var listedFronts: [String] = []
        if hasFronts {
            if q.keys.contains("get") {
                path = q["get"] ?? ""
                if path.isEmpty { path = LoftProfile.defaultGETPath }
                type = "get"
            }
            if q.keys.contains("ws") {
                wsPath = q["ws"] ?? ""
                if wsPath.isEmpty { wsPath = LoftProfile.defaultWSPath }
                if type != "get" { type = "ws" }
            }
            if q.keys.contains("h2") {
                h2Path = q["h2"] ?? ""
                if h2Path.isEmpty { h2Path = LoftProfile.defaultH2Path }
                if type != "get" && wsPath.isEmpty { type = "h2" }
            }
            if q.keys.contains("h3") {
                h3Path = q["h3"] ?? ""
                if h3Path.isEmpty { h3Path = LoftProfile.defaultH2Path }
            }
            quic = q.keys.contains("quic")
            if type != "get" && wsPath.isEmpty && h2Path.isEmpty && q.keys.contains("get") == false {
                if q.keys.contains("h3") { type = "h3" }
                else if quic { type = "quic" }
            }
            for f in LoftProfile.muxOrder where q.keys.contains(f) {
                listedFronts.append(f)
            }
        }
        if !LoftProfile.frontKinds.contains(type) { return nil }
        let scheme: String
        if let s = q["scheme"]?.lowercased(), ["https", "http", "wss", "ws"].contains(s) {
            scheme = s
        } else {
            scheme = type == "ws" ? "wss" : "https"
        }
        let omit = port == 443 && (scheme == "https" || scheme == "wss")
        let hostPart = bracketHost(host, port: port, omitDefault: omit)
        let name = decode(c.percentEncodedFragment ?? c.fragment ?? "")
        let cdn = q["cdn"] ?? ""
        let url: String
        let outPath: String
        if type == "get" || hasFronts {
            url = "\(scheme == "wss" || scheme == "ws" ? "https" : scheme)://\(hostPart)"
            outPath = path
        } else {
            let pth = (path.isEmpty || path == "/") ? "" : path
            url = "\(scheme)://\(hostPart)\(pth)"
            outPath = ""
        }
        return LoftProfile(
            id: fileName,
            name: name.isEmpty ? host : name,
            token: token,
            url: url,
            kind: type,
            path: outPath,
            cdn: cdn,
            socks: "auto",
            tun: true,
            mux: type != "l3",
            instanceID: "",
            file: fileName,
            maskPub: q["pbk"] ?? "",
            wsPath: wsPath,
            h2Path: h2Path,
            h3Path: h3Path,
            quic: quic,
            listedFronts: listedFronts
        )
    }

    /// Canonical `loft://token@host[:port][/path][?type=&cdn=&scheme=][#name]`. Empty when there is nothing to emit.
    static func emit(_ p: LoftProfile) -> String {
        if p.isSSTP { return emitSSTP(p) }
        if p.isVLESS || p.isTrojan { return p.url }
        let token = p.token.trimmingCharacters(in: .whitespacesAndNewlines)
        let u = URL(string: p.url)
        let host = u?.host ?? ""
        if token.isEmpty && host.isEmpty { return "" }
        var c = URLComponents()
        c.scheme = "loft"
        if !token.isEmpty { c.user = token }
        if host.isEmpty { return c.string ?? "loft://" }
        c.host = host
        let type = p.kind.lowercased()
        let kind = LoftProfile.frontKinds.contains(type) ? type : "get"
        let scheme = (u?.scheme ?? (kind == "ws" ? "wss" : "https")).lowercased()
        if let port = u?.port {
            let def = (scheme == "http" || scheme == "ws") ? 80 : 443
            if port != def { c.port = port }
        }
        let mux = p.muxFronts()
        let explicit = !p.listedFronts.isEmpty
        let extra = explicit ? mux.count > 1 : (mux.count > 1 || p.hasMuxExtras)
        var items: [URLQueryItem] = []
        if kind == "l3" {
            let urlPath = u?.path ?? ""
            if !urlPath.isEmpty && urlPath != "/" {
                c.path = urlPath.hasPrefix("/") ? urlPath : "/" + urlPath
            }
            items.append(URLQueryItem(name: "type", value: "l3"))
        } else if explicit || extra || kind == "get" {
            if !explicit || mux.contains("get") {
                items.append(URLQueryItem(name: "get", value: p.path.isEmpty ? LoftProfile.defaultGETPath : p.path))
            }
            if mux.contains("ws") || (!explicit && !p.wsPath.isEmpty) {
                items.append(URLQueryItem(name: "ws", value: p.wsPath.isEmpty ? LoftProfile.defaultWSPath : p.wsPath))
            }
            if mux.contains("h2") || (!explicit && !p.h2Path.isEmpty) {
                items.append(URLQueryItem(name: "h2", value: p.h2Path.isEmpty ? LoftProfile.defaultH2Path : p.h2Path))
            }
            if mux.contains("h3") || (!explicit && !p.h3Path.isEmpty) {
                items.append(URLQueryItem(name: "h3", value: p.h3Path.isEmpty ? LoftProfile.defaultH2Path : p.h3Path))
            }
            if mux.contains("quic") || (!explicit && p.quic) { items.append(URLQueryItem(name: "quic", value: nil)) }
        } else {
            let urlPath = u?.path ?? ""
            let uriPath: String
            if !urlPath.isEmpty && urlPath != "/" {
                uriPath = urlPath
            } else {
                uriPath = p.path
            }
            if !uriPath.isEmpty {
                c.path = uriPath.hasPrefix("/") ? uriPath : "/" + uriPath
            }
            if kind != "get" { items.append(URLQueryItem(name: "type", value: kind)) }
        }
        let defScheme = kind == "ws" ? "wss" : "https"
        if scheme != defScheme && kind != "get" && !extra { items.append(URLQueryItem(name: "scheme", value: scheme)) }
        if !p.cdn.isEmpty { items.append(URLQueryItem(name: "cdn", value: p.cdn)) }
        if !p.maskPub.isEmpty { items.append(URLQueryItem(name: "pbk", value: p.maskPub)) }
        c.queryItems = items.isEmpty ? nil : items
        if !p.name.isEmpty { c.fragment = p.name }
        return c.string ?? "loft://"
    }

    /// Host, port, scheme, and path of a loft outbound for the field form.
    static func loftEndpoint(_ p: LoftProfile) -> (host: String, port: Int, scheme: String, path: String) {
        let u = URL(string: p.url)
        let host = u?.host ?? ""
        let scheme = (u?.scheme ?? (p.kind.lowercased() == "ws" ? "wss" : "https")).lowercased()
        let def = (scheme == "http" || scheme == "ws") ? 80 : 443
        let port = u?.port ?? def
        let type = p.kind.lowercased()
        let path: String
        if type == "get" {
            path = p.path
        } else {
            let up = u?.path ?? ""
            path = (up == "/" ? "" : up)
        }
        return (host, port, scheme, path)
    }

    /// Writes token / host / port / front paths into a loft profile. Keeps cdn and pbk.
    /// A switch that is off keeps the typed path and drops that front from Hello.
    static func applyLoftManual(_ p: inout LoftProfile, token: String, host: String, port: Int, getPath: String, wsPath: String, h2Path: String, h3Path: String, getOn: Bool, wsOn: Bool, h2On: Bool, h3On: Bool, quic: Bool) {
        p.token = token
        p.mux = true
        let cur = loftEndpoint(p)
        let sch = (cur.scheme == "http" || cur.scheme == "https") ? cur.scheme : "https"
        if host.isEmpty { return }
        let def = sch == "http" ? 80 : 443
        let portUse = (port > 0 && port < 65536) ? port : def
        let hostPart = bracketHost(host, port: portUse, omitDefault: portUse == def)
        func slash(_ s: String) -> String {
            var t = s.trimmingCharacters(in: .whitespacesAndNewlines)
            if !t.isEmpty, !t.hasPrefix("/") { t = "/" + t }
            return t
        }
        p.url = "\(sch)://\(hostPart)"
        p.path = slash(getPath)
        p.wsPath = slash(wsPath)
        p.h2Path = slash(h2Path)
        p.h3Path = slash(h3Path)
        var listed: [String] = []
        if getOn { listed.append("get") }
        if wsOn { listed.append("ws") }
        if h2On { listed.append("h2") }
        if quic { listed.append("quic") }
        if h3On { listed.append("h3") }
        if listed.isEmpty { listed = ["get"] }
        p.listedFronts = listed
        p.kind = listed.contains("get") ? "get" : listed[0]
        p.quic = listed.contains("quic")
    }

    /// `vless://<uuid>@<host>[:port]?…#Name` (DuckSoft / Xray share).
    static func vless(_ raw: String, fileName: String) -> LoftProfile? {
        guard let s = parseVLESS(raw) else { return nil }
        if s.uuid.isEmpty || s.host.isEmpty { return nil }
        return LoftProfile(
            id: fileName,
            name: s.name.isEmpty ? s.host : s.name,
            token: s.uuid,
            url: raw.trimmingCharacters(in: .whitespacesAndNewlines),
            kind: "vless",
            path: "",
            socks: "auto",
            tun: true,
            mux: false,
            instanceID: "",
            file: fileName,
            sni: s.sni,
            tlsCheck: !s.insecure
        )
    }

    /// Fields of a DuckSoft `vless://` share. Link and Manual edit the same object.
    struct VLESSShare {
        var uuid = ""
        var host = ""
        var port = 443
        var name = ""
        var security = "tls" // tls | reality
        var network = "tcp"  // tcp | xhttp
        var sni = ""
        var fingerprint = ""
        var publicKey = ""
        var shortID = ""
        var vision = false
        var path = ""
        var headerHost = ""
        var mode = ""
        var insecure = false
        var reality: Bool { security == "reality" }
        var xhttp: Bool { network == "xhttp" }
    }

    /// DuckSoft query + userinfo. Nil when the string is not `vless://`.
    static func parseVLESS(_ raw: String) -> VLESSShare? {
        let s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let c = URLComponents(string: s),
              (c.scheme ?? "").lowercased() == "vless" else { return nil }
        var v = VLESSShare()
        v.uuid = decode(c.percentEncodedUser ?? c.user ?? "")
        v.host = c.host ?? ""
        v.port = c.port ?? 443
        v.name = decode(c.percentEncodedFragment ?? c.fragment ?? "")
        let q = query(c)
        v.sni = q["sni"] ?? ""
        v.fingerprint = (q["fp"] ?? "").lowercased()
        v.publicKey = q["pbk"] ?? ""
        v.shortID = q["sid"] ?? ""
        v.path = q["path"] ?? ""
        v.headerHost = q["host"] ?? ""
        v.mode = (q["mode"] ?? "").lowercased()
        let sec = (q["security"] ?? "").lowercased()
        if sec == "reality" || !v.publicKey.isEmpty || !v.shortID.isEmpty {
            v.security = "reality"
        } else if sec == "none" {
            v.security = "tls"
        } else {
            v.security = "tls"
        }
        var net = (q["type"] ?? "tcp").lowercased()
        if net == "splithttp" { net = "xhttp" }
        v.network = net == "xhttp" ? "xhttp" : "tcp"
        v.vision = (q["flow"] ?? "").lowercased().contains("vision")
        let ins = (q["allowinsecure"] ?? q["insecure"] ?? "").lowercased()
        v.insecure = ins == "1" || ins == "true"
        return v
    }

    /// Profile token / sni / tlsCheck overlay a parsed `vless://`, or host:port.
    static func vlessShare(_ p: LoftProfile) -> VLESSShare {
        var v = parseVLESS(p.url) ?? VLESSShare()
        if v.host.isEmpty {
            if let u = URL(string: p.url), let host = u.host, !host.isEmpty {
                v.host = host
                v.port = u.port ?? 443
            } else {
                let raw = p.url.trimmingCharacters(in: .whitespacesAndNewlines)
                if let at = splitHostPort(raw) {
                    v.host = at.host
                    v.port = at.port
                }
            }
        }
        if !p.token.isEmpty { v.uuid = p.token }
        if !p.sni.isEmpty { v.sni = p.sni }
        v.insecure = !p.tlsCheck
        if v.name.isEmpty { v.name = p.name }
        return v
    }

    /// Canonical DuckSoft share. Default port 443 and empty optionals omitted.
    static func emitVLESS(_ v: VLESSShare) -> String {
        var c = URLComponents()
        c.scheme = "vless"
        if !v.uuid.isEmpty { c.user = v.uuid }
        if !v.host.isEmpty { c.host = v.host }
        if v.port > 0, v.port != 443 { c.port = v.port }
        var items: [URLQueryItem] = []
        let sec = v.reality ? "reality" : "tls"
        items.append(URLQueryItem(name: "security", value: sec))
        items.append(URLQueryItem(name: "encryption", value: "none"))
        if v.xhttp {
            items.append(URLQueryItem(name: "type", value: "xhttp"))
            let mode = v.mode.isEmpty ? "stream-one" : v.mode
            items.append(URLQueryItem(name: "mode", value: mode))
            if !v.path.isEmpty { items.append(URLQueryItem(name: "path", value: v.path)) }
            if !v.headerHost.isEmpty { items.append(URLQueryItem(name: "host", value: v.headerHost)) }
        }
        if v.vision, !v.xhttp {
            items.append(URLQueryItem(name: "flow", value: "xtls-rprx-vision"))
        }
        if !v.sni.isEmpty { items.append(URLQueryItem(name: "sni", value: v.sni)) }
        let fp = v.fingerprint.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if !fp.isEmpty {
            items.append(URLQueryItem(name: "fp", value: fp))
        } else if v.reality {
            items.append(URLQueryItem(name: "fp", value: "chrome"))
        }
        if v.reality {
            if !v.publicKey.isEmpty { items.append(URLQueryItem(name: "pbk", value: v.publicKey)) }
            if !v.shortID.isEmpty { items.append(URLQueryItem(name: "sid", value: v.shortID)) }
        }
        if v.insecure { items.append(URLQueryItem(name: "allowInsecure", value: "1")) }
        c.queryItems = items
        if !v.name.isEmpty { c.fragment = v.name }
        return c.string ?? "vless://"
    }

    /// `host:port` or a bare host. Port defaults to 443.
    private static func splitHostPort(_ raw: String) -> (host: String, port: Int)? {
        let s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.isEmpty || s.contains("://") { return nil }
        if s.hasPrefix("["), let r = s.range(of: "]:") {
            let host = String(s[s.index(after: s.startIndex)..<r.lowerBound])
            let port = Int(s[r.upperBound...]) ?? 443
            if host.isEmpty { return nil }
            return (host, port > 0 ? port : 443)
        }
        if s.filter({ $0 == ":" }).count == 1, let r = s.range(of: ":") {
            let host = String(s[..<r.lowerBound])
            if host.isEmpty { return nil }
            let port = Int(s[r.upperBound...]) ?? 443
            return (host, port > 0 ? port : 443)
        }
        if s.contains(":") { return nil }
        return (s, 443)
    }

    /// `sstp://<user>:<password>@<host>[:port][/path]?sni=…&pap=1#Name` (docs/share.md §7).
    /// `url` on the profile is the HTTPS dial address. Password is required.
    static func sstp(_ raw: String, fileName: String) -> LoftProfile? {
        let s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let c = URLComponents(string: s),
              (c.scheme ?? "").lowercased() == "sstp" else { return nil }
        let user = decode(c.percentEncodedUser ?? c.user ?? "")
        let pass = decode(c.percentEncodedPassword ?? c.password ?? "")
        let host = c.host ?? ""
        if host.isEmpty || pass.isEmpty { return nil }
        let port = c.port ?? 443
        var path = c.path
        if path == "/" { path = "" }
        let q = query(c)
        let sni = q["sni"] ?? ""
        let papRaw = (q["pap"] ?? "").lowercased()
        let pap = papRaw == "1" || papRaw == "true" || papRaw == "on"
        let name = decode(c.percentEncodedFragment ?? c.fragment ?? "")
        let https = sstpHTTPS(host: host, port: port, path: path)
        return LoftProfile(
            id: fileName,
            name: name.isEmpty ? host : name,
            token: pass,
            user: user,
            url: https,
            kind: "sstp",
            path: "",
            socks: "auto",
            tun: true,
            mux: false,
            instanceID: "",
            file: fileName,
            sni: sni,
            papCHAP: pap
        )
    }

    /// Share string for an SSTP profile. Empty when the server host is missing.
    static func emitSSTP(_ p: LoftProfile) -> String {
        var user = p.user.trimmingCharacters(in: .whitespacesAndNewlines)
        var pass = p.token.trimmingCharacters(in: .whitespacesAndNewlines)
        var dial = p.url.trimmingCharacters(in: .whitespacesAndNewlines)
        var sni = p.sni.trimmingCharacters(in: .whitespacesAndNewlines)
        var pap = p.papCHAP
        var name = p.name.trimmingCharacters(in: .whitespacesAndNewlines)
        if dial.lowercased().hasPrefix("sstp://"), let q = sstp(dial, fileName: p.id) {
            user = q.user
            pass = q.token
            dial = q.url
            if sni.isEmpty { sni = q.sni }
            pap = q.papCHAP
            if name.isEmpty { name = q.name }
        }
        guard let u = URL(string: dial), let host = u.host, !host.isEmpty else { return "" }
        let scheme = (u.scheme ?? "https").lowercased()
        let def = (scheme == "http") ? 80 : 443
        let port = u.port ?? def
        var path = u.path
        if path == "/" { path = "" }
        var c = URLComponents()
        c.scheme = "sstp"
        if !user.isEmpty { c.user = user }
        if !pass.isEmpty { c.password = pass }
        c.host = host
        if port != 443 { c.port = port }
        if !path.isEmpty { c.path = path.hasPrefix("/") ? path : "/" + path }
        var items: [URLQueryItem] = []
        if !sni.isEmpty { items.append(URLQueryItem(name: "sni", value: sni)) }
        if pap { items.append(URLQueryItem(name: "pap", value: "1")) }
        c.queryItems = items.isEmpty ? nil : items
        if !name.isEmpty { c.fragment = name }
        return c.string ?? ""
    }

    /// HTTPS dial URL for an SSTP share host. Port 443 is omitted.
    private static func sstpHTTPS(host: String, port: Int, path: String) -> String {
        let hostPart = bracketHost(host, port: port, omitDefault: port == 443)
        var https = "https://\(hostPart)"
        if !path.isEmpty && path != "/" {
            https += path.hasPrefix("/") ? path : "/" + path
        }
        return https
    }

    /// `trojan://<password>@<host>[:port]?…#Name` (DuckSoft / Xray share).
    static func trojan(_ raw: String, fileName: String) -> LoftProfile? {
        let s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let c = URLComponents(string: s),
              (c.scheme ?? "").lowercased() == "trojan" else { return nil }
        let password = decode(c.percentEncodedUser ?? c.user ?? "")
        let host = c.host ?? ""
        if password.isEmpty || host.isEmpty { return nil }
        let q = query(c)
        let name = decode(c.percentEncodedFragment ?? c.fragment ?? "")
        let sni = q["sni"] ?? ""
        return LoftProfile(
            id: fileName,
            name: name.isEmpty ? host : name,
            token: password,
            url: s,
            kind: "trojan",
            path: "",
            socks: "auto",
            tun: true,
            mux: false,
            instanceID: "",
            file: fileName,
            sni: sni
        )
    }

    /// Query keys, last value wins, percent-decoded.
    private static func query(_ c: URLComponents) -> [String: String] {
        var out: [String: String] = [:]
        for it in c.queryItems ?? [] {
            let k = it.name.lowercased()
            let v = it.value ?? ""
            if !k.isEmpty { out[k] = v }
        }
        return out
    }

    /// Percent-decode `s`, or `s` itself when it is already plain.
    private static func decode(_ s: String) -> String {
        s.removingPercentEncoding ?? s
    }

    /// IPv6 in brackets; omit port 443 when `omitDefault`.
    private static func bracketHost(_ host: String, port: Int, omitDefault: Bool) -> String {
        let h = host.contains(":") ? "[\(host)]" : host
        if omitDefault { return h }
        return "\(h):\(port)"
    }

    /// Subscription-style base64 of URI lines, once.
    private static func unwrapBase64(_ s: String) -> String? {
        if s.contains("://") { return nil }
        let compact = s.replacingOccurrences(of: "\n", with: "")
            .replacingOccurrences(of: "\r", with: "")
            .replacingOccurrences(of: " ", with: "")
        if compact.count < 16 || compact.count > 8192 { return nil }
        let allowed = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/-_=")
        if compact.unicodeScalars.contains(where: { !allowed.contains($0) }) { return nil }
        var b64 = compact.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        while b64.count % 4 != 0 { b64.append("=") }
        guard let data = Data(base64Encoded: b64), let t = String(data: data, encoding: .utf8) else { return nil }
        let out = t.trimmingCharacters(in: .whitespacesAndNewlines)
        if out.contains("://") { return out }
        return nil
    }
}
