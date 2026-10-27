// Fetches the public address of one outbound through that session’s SOCKS (or the default route).
// The echo API is a web service that answers with the caller's address.
// Asking it through a session's local SOCKS port shows which address the
// internet sees for that tunnel; the popover and status window display it.

import Foundation
import Darwin

/// HTTP GET to the configured echo API. SOCKS binds the request to one loft-front.
enum ExtIP {
    private static let lock = NSLock()
    private static var sessions: [String: URLSession] = [:]

    /// Reuses an ephemeral session per SOCKS bind (or "direct").
    private static func session(socks: String, useDirect: Bool) -> URLSession {
        let key = useDirect && socks.isEmpty ? "direct" : socks
        lock.lock()
        if let s = sessions[key] { lock.unlock(); return s }
        lock.unlock()
        let cfg = URLSessionConfiguration.ephemeral
        cfg.timeoutIntervalForRequest = 8
        cfg.timeoutIntervalForResource = 10
        cfg.requestCachePolicy = .reloadIgnoringLocalCacheData
        cfg.httpShouldSetCookies = false
        cfg.urlCache = nil
        if let parts = socksParts(socks) {
            cfg.connectionProxyDictionary = [
                kCFNetworkProxiesSOCKSEnable: true,
                kCFNetworkProxiesSOCKSProxy: parts.0,
                kCFNetworkProxiesSOCKSPort: parts.1,
                kCFNetworkProxiesHTTPEnable: false,
                kCFNetworkProxiesHTTPSEnable: false,
            ]
        }
        let session = URLSession(configuration: cfg)
        lock.lock()
        sessions[key] = session
        lock.unlock()
        return session
    }

    /// Blocking lookup. Call off the main queue. Empty string on failure.
    static func lookup(url raw: String, socks: String, useDirect: Bool) -> String {
        guard let url = URL(string: raw), let scheme = url.scheme?.lowercased(),
              scheme == "http" || scheme == "https", url.host != nil else { return "" }
        if socks.isEmpty && !useDirect { return "" }
        var req = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 8)
        req.setValue("loft", forHTTPHeaderField: "User-Agent")
        req.setValue("close", forHTTPHeaderField: "Connection")
        let session = session(socks: socks, useDirect: useDirect)
        let sem = DispatchSemaphore(value: 0)
        var body = Data()
        var ok = false
        let task = session.dataTask(with: req) { data, resp, _ in
            if let data = data, let http = resp as? HTTPURLResponse, (200...299).contains(http.statusCode) {
                body = data
                ok = true
            }
            sem.signal()
        }
        task.resume()
        _ = sem.wait(timeout: .now() + 12)
        guard ok else { return "" }
        return parse(body)
    }

    /// Host and port of `127.0.0.1:1080`. Wildcard binds become loopback.
    static func socksParts(_ addr: String) -> (String, Int)? {
        let t = addr.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { return nil }
        guard let r = t.range(of: ":", options: .backwards) else { return nil }
        let hostRaw = String(t[..<r.lowerBound])
        guard let port = Int(t[r.upperBound...]), port > 0, port <= 65535 else { return nil }
        let wild = hostRaw.isEmpty || hostRaw == "0.0.0.0" || hostRaw == "*" || hostRaw == "::" || hostRaw == "[::]"
        return (wild ? "127.0.0.1" : hostRaw, port)
    }

    /// Plain address, JSON `ip`/`query`/`origin`, or the first IPv4/IPv6 in the body.
    static func parse(_ data: Data) -> String {
        let s = String(data: data, encoding: .utf8) ?? String(data: data, encoding: .ascii) ?? ""
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        if let ip = asIP(t) { return ip }
        if t.hasPrefix("{"),
           let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
            for k in ["ip", "query", "origin", "address", "ip_addr", "externalIp", "external_ip"] {
                guard let v = obj[k] as? String else { continue }
                let piece = v.split(separator: ",").first.map(String.init)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                if let ip = asIP(piece) { return ip }
            }
        }
        if let r = t.range(of: #"\b\d{1,3}(?:\.\d{1,3}){3}\b"#, options: .regularExpression) {
            let cand = String(t[r])
            if let ip = asIP(cand) { return ip }
        }
        return ""
    }

    /// `s` when it is a dotted IPv4 or a literal IPv6.
    static func asIP(_ s: String) -> String? {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { return nil }
        var v4 = in_addr()
        var v6 = in6_addr()
        if t.withCString({ inet_pton(AF_INET, $0, &v4) }) == 1 { return t }
        if t.withCString({ inet_pton(AF_INET6, $0, &v6) }) == 1 { return t }
        return nil
    }
}
