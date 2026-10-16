// Leftover in-process GET front. The GUI talks to loft-front via FrontHelper;
// this URLSession client is unused on the live connect path.

import Foundation

/// Leftover GET front: cookie-masked HTTPS GETs and an in-process mux.
/// Live connect uses loft-front (`FrontHelper`); this type is not started by SessionController.
final class GETFront: NSObject, URLSessionDelegate {
    let token: Data
    let base: String
    let path: String
    let headerBudget: Int
    let inflight: Int
    private let mux = LoftMux()
    private var key: Data?
    private var sid = "new"
    private var seq: UInt64 = 0
    private var started = false
    private var stopFlag = false
    private let lock = NSLock()
    private var session: URLSession!
    private(set) var status: LoftWire.Status?
    var instanceID: String
    let software: String

    /// Cookie-masked GET client against `url`/`path`. `token` is the access key.
    init(token: String, url: String, path: String?, instanceID: String, software: String) {
        self.token = Data(token.utf8)
        self.base = GETFront.origin(url)
        self.path = (path?.isEmpty == false) ? path! : "/static/js"
        self.headerBudget = 12 * 1024
        self.inflight = 4
        self.instanceID = instanceID
        self.software = software
        super.init()
        let cfg = URLSessionConfiguration.ephemeral
        cfg.httpShouldSetCookies = false
        cfg.httpCookieAcceptPolicy = .never
        cfg.requestCachePolicy = .reloadIgnoringLocalCacheData
        cfg.timeoutIntervalForRequest = 30
        cfg.urlCache = nil
        session = URLSession(configuration: cfg, delegate: self, delegateQueue: nil)
    }

    /// HTTPS origin of `url`; maps `ws(s)://` to `http(s)://`.
    private static func origin(_ url: String) -> String {
        var s = url.trimmingCharacters(in: .whitespacesAndNewlines)
        let ws = s.hasPrefix("wss://") || s.hasPrefix("ws://")
        if s.hasPrefix("wss://") { s = "https://" + s.dropFirst(6) }
        else if s.hasPrefix("ws://") { s = "http://" + s.dropFirst(5) }
        if ws, let u = URL(string: s), let host = u.host {
            var b = "\(u.scheme ?? "https")://\(host)"
            if let port = u.port { b += ":\(port)" }
            return b
        }
        return s.hasSuffix("/") ? String(s.dropLast()) : s
    }

    var overlayIP: String { status?.ip ?? "" }
    var bytesUp: Int64 { mux.bytesUp }
    var bytesDown: Int64 { mux.bytesDown }

    /// Hello + auth GET, then inflight poll threads. Throws if the front is not loft.
    func start() throws {
        lock.lock()
        if started { lock.unlock(); return }
        lock.unlock()
        let hello = LoftWire.encodeHello(memberID: "", instanceID: instanceID, program: "loft", software: software, caps: LoftWire.clientCaps)
        let auth = LoftCrypto.makeAuth(token: token)
        var blob = auth.proof
        blob.append(hello)
        let derived = LoftCrypto.deriveKey(token: token, nonce: auth.nonce)
        lock.lock(); key = derived; sid = "new"; lock.unlock()
        let body = try roundTrip(auth: blob, uplink: nil)
        lock.lock(); let curSid = sid; let curKey = key; lock.unlock()
        if curSid == "new" { throw NSError(domain: "loft", code: 1, userInfo: [NSLocalizedDescriptionKey: L10n.t("not_front")]) }
        guard let curKey = curKey, let plain = LoftCrypto.open(key: curKey, blob: body, aad: Data(curSid.utf8)) else {
            throw NSError(domain: "loft", code: 2, userInfo: [NSLocalizedDescriptionKey: "status decrypt"])
        }
        guard let st = LoftWire.decodeStatus(plain) else {
            throw NSError(domain: "loft", code: 3, userInfo: [NSLocalizedDescriptionKey: "status decode"])
        }
        if !st.ok {
            throw NSError(domain: "loft", code: Int(st.code), userInfo: [NSLocalizedDescriptionKey: LoftWire.codeName(st.code)])
        }
        status = st
        if !st.instanceID.isEmpty { instanceID = st.instanceID }
        lock.lock(); started = true; stopFlag = false; lock.unlock()
        for _ in 0..<inflight {
            Thread.detachNewThreadSelector(#selector(loop), toTarget: self, with: nil)
        }
    }

    /// Opens a mux stream to `host:port`, starting the front if needed.
    func open(host: String, port: UInt16) -> LoftStream? {
        if !started { try? start() }
        return mux.open(host: host, port: port)
    }

    /// Queues a datagram frame for the next GET uplink.
    func writeDgram(host: String, port: UInt16, data: Data) -> Bool {
        return mux.writeDgram(host: host, port: port, data: data)
    }

    /// Callback for inbound datagrams from the mux.
    func onDgram(_ cb: ((String, UInt16, Data) -> Void)?) {
        mux.onDgram = cb
    }

    var frontHost: String {
        if let u = URL(string: base) { return u.host ?? "" }
        return ""
    }

    /// Stops poll threads, closes the mux, and invalidates the URLSession.
    func close() {
        stopFlag = true
        mux.close()
        started = false
        session.invalidateAndCancel()
    }

    /// Poll thread: pulls mux frames (or pad) and round-trips them as GET.
    @objc private func loop() {
        while !stopFlag {
            var up = mux.pull(budget: headerBudget / 2, wait: 1.0)
            if up.isEmpty {
                let pad = LoftCrypto.randomBytes(24 + Int(arc4random_uniform(80)))
                up = [LoftWire.Frame(cmd: LoftWire.cmdPad, streamID: 0, payload: pad)]
            }
            guard let raw = LoftWire.encodeBatch(up) else { continue }
            do { _ = try roundTrip(auth: nil, uplink: raw) } catch {
                Thread.sleep(forTimeInterval: 0.4)
            }
        }
    }

    /// One GET: cookie is `session=` + base64url blob; decrypts the body into the mux.
    private func roundTrip(auth: Data?, uplink: Data?) throws -> Data {
        let blob: Data
        if let auth = auth {
            blob = auth
        } else {
            guard let key = key, let up = uplink, let sealed = LoftCrypto.seal(key: key, plaintext: up, aad: Data(sid.utf8)) else {
                throw NSError(domain: "loft", code: 4, userInfo: [NSLocalizedDescriptionKey: "seal"])
            }
            blob = sealed
        }
        lock.lock(); seq += 1; let n = seq; let curSid = sid; lock.unlock()
        let ext = ".js"
        let urlStr = "\(base)\(path)/\(curSid)/\(n)\(ext)"
        guard let url = URL(string: urlStr) else { throw NSError(domain: "loft", code: 5, userInfo: [NSLocalizedDescriptionKey: "url"]) }
        let req = NSMutableURLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 30)
        req.httpMethod = "GET"
        req.setValue("*/*", forHTTPHeaderField: "Accept")
        req.setValue("ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7", forHTTPHeaderField: "Accept-Language")
        req.setValue("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36", forHTTPHeaderField: "User-Agent")
        req.setValue(base, forHTTPHeaderField: "Origin")
        req.setValue(base + "/", forHTTPHeaderField: "Referer")
        let b64 = blob.base64URL()
        req.setValue("session=\(b64)", forHTTPHeaderField: "Cookie")
        if auth != nil { req.setValue("1", forHTTPHeaderField: "X-Auth") }
        req.httpShouldHandleCookies = false

        let sem = DispatchSemaphore(value: 0)
        var respData: Data?
        var resp: URLResponse?
        var err: Error?
        let task = session.dataTask(with: req as URLRequest) { d, r, e in
            respData = d; resp = r; err = e; sem.signal()
        }
        task.resume()
        _ = sem.wait(timeout: .now() + 35)
        if let err = err { throw err }
        guard let http = resp as? HTTPURLResponse else {
            throw NSError(domain: "loft", code: 6, userInfo: [NSLocalizedDescriptionKey: "no response"])
        }
        if http.statusCode == 404 {
            throw NSError(domain: "loft", code: 0x0101, userInfo: [NSLocalizedDescriptionKey: "auth_bad_key"])
        }
        if http.statusCode != 200 {
            throw NSError(domain: "loft", code: http.statusCode, userInfo: [NSLocalizedDescriptionKey: "GET \(http.statusCode)"])
        }
        if let hsid = httpHeader(http, "X-Session"), !hsid.isEmpty {
            lock.lock(); sid = hsid; lock.unlock()
        } else if let cookie = httpHeader(http, "Set-Cookie"), cookie.contains("session=") {
            if let r = cookie.range(of: "session=") {
                var v = String(cookie[r.upperBound...])
                if let cut = v.firstIndex(where: { $0 == ";" || $0 == " " }) { v = String(v[..<cut]) }
                if !v.isEmpty { lock.lock(); sid = v; lock.unlock() }
            }
        }
        let body = respData ?? Data()
        if auth == nil, let key = key {
            lock.lock(); let aad = Data(sid.utf8); lock.unlock()
            if let plain = LoftCrypto.open(key: key, blob: body, aad: aad), let frames = LoftWire.decodeBatch(plain) {
                mux.push(frames)
            }
        }
        return body
    }

    /// Accepts the server trust as presented (mask fronts often use odd certs).
    func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge, completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        if challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
           let trust = challenge.protectionSpace.serverTrust {
            completionHandler(.useCredential, URLCredential(trust: trust))
            return
        }
        completionHandler(.performDefaultHandling, nil)
    }

    /// Case-insensitive header lookup.
    private func httpHeader(_ http: HTTPURLResponse, _ name: String) -> String? {
        for (k, v) in http.allHeaderFields {
            if String(describing: k).caseInsensitiveCompare(name) == .orderedSame {
                let s = String(describing: v)
                return s.isEmpty ? nil : s
            }
        }
        return nil
    }
}

private extension Data {
    /// Base64url without padding, for the session cookie.
    func base64URL() -> String {
        return self.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}
