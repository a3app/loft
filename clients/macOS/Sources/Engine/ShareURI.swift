// Share-string detector: loft://, vless://, trojan://, YAML, SSTP .vpn JSON (docs/share.md).

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
        let type = (q["type"] ?? "get").lowercased()
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
        if type == "get" {
            url = "\(scheme)://\(hostPart)"
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
            mux: true,
            instanceID: "",
            file: fileName,
            maskPub: q["pbk"] ?? ""
        )
    }

    /// Canonical `loft://token@host[:port][/path][?type=&cdn=&scheme=][#name]`. Empty when there is nothing to emit.
    static func emit(_ p: LoftProfile) -> String {
        if p.isSSTP || p.isVLESS || p.isTrojan { return p.url }
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
        let urlPath = u?.path ?? ""
        let uriPath: String
        if kind == "get" {
            uriPath = p.path
        } else if !urlPath.isEmpty && urlPath != "/" {
            uriPath = urlPath
        } else {
            uriPath = ""
        }
        if !uriPath.isEmpty {
            c.path = uriPath.hasPrefix("/") ? uriPath : "/" + uriPath
        }
        var items: [URLQueryItem] = []
        if kind != "get" { items.append(URLQueryItem(name: "type", value: kind)) }
        let defScheme = kind == "ws" ? "wss" : "https"
        if scheme != defScheme { items.append(URLQueryItem(name: "scheme", value: scheme)) }
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

    /// Writes token / host / port / path / kind into a loft profile. Keeps cdn and pbk.
    static func applyLoftManual(_ p: inout LoftProfile, token: String, host: String, port: Int, path: String, kind: String) {
        p.token = token
        let type = LoftProfile.frontKinds.contains(kind.lowercased()) ? kind.lowercased() : "get"
        p.kind = type
        let cur = loftEndpoint(p)
        let sch: String
        if type == "ws" {
            sch = (cur.scheme == "ws" || cur.scheme == "wss") ? cur.scheme : "wss"
        } else if cur.scheme == "http" || cur.scheme == "https" {
            sch = cur.scheme
        } else {
            sch = "https"
        }
        if host.isEmpty { return }
        let def = (sch == "http" || sch == "ws") ? 80 : 443
        let portUse = (port > 0 && port < 65536) ? port : def
        let hostPart = bracketHost(host, port: portUse, omitDefault: portUse == def)
        var pth = path.trimmingCharacters(in: .whitespacesAndNewlines)
        if !pth.isEmpty, !pth.hasPrefix("/") { pth = "/" + pth }
        if type == "get" {
            p.url = "\(sch)://\(hostPart)"
            p.path = pth
        } else {
            p.url = "\(sch)://\(hostPart)\(pth)"
            p.path = ""
        }
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
