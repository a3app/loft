// Spawns bundled loft-front, parses STATUS lines, and owns the TUN unix-socket path.

import Foundation
import Darwin

/// Spawns bundled loft-front, parses STATUS lines, and owns the TUN unix-socket path.
final class FrontHelper {
    private var proc: Process?
    private let lock = NSLock()
    private(set) var overlayIP = ""
    private(set) var overlayGW = ""
    private(set) var overlayDNS = ""
    private(set) var overlayMTU = 0
    /// Managed CIDRs from STATUS `routes=` (lab LAN behind a member).
    private(set) var overlayRoutes: [String] = []
    /// Compact Status front map (`get=/static/v3/build,ws=/api/v1/sync,quic`).
    private(set) var overlayFronts = ""
    private(set) var tunLive = false
    private(set) var lastError = ""
    private(set) var lastStage = ""
    private(set) var tunSock = ""
    private(set) var bytesUp: Int64 = 0
    private(set) var bytesDown: Int64 = 0
    /// Last `socks5 host:port` (or `off`) from loft-front stderr.
    private var socksBind = ""
    /// Increments on each `socks5` line so a live rebind can wait for a fresh report.
    private var socksEpoch: UInt64 = 0
    var onLine: ((String) -> Void)?
    var onExit: ((Int32) -> Void)?
    private var stopping = false
    private var waitSem: DispatchSemaphore?
    private var tunSem = DispatchSemaphore(value: 0)
    private var socksSem = DispatchSemaphore(value: 0)
    private var outRest = ""
    private var errRest = ""

    /// SIGTERM leftover loft-front from this bundle. Leaves debug binaries (other paths) alone.
    /// Uses libproc so boot cannot stall on a full `ps` pipe (long argv of a live loft-front).
    static func reapOrphans() {
        guard let bin = binaryPath() else { return }
        let selfPid = getpid()
        var pids = [pid_t](repeating: 0, count: 4096)
        let nbytes = pids.withUnsafeMutableBufferPointer { buf -> Int32 in
            guard let base = buf.baseAddress else { return 0 }
            return proc_listpids(UInt32(PROC_ALL_PIDS), 0, base, Int32(buf.count * MemoryLayout<pid_t>.stride))
        }
        guard nbytes > 0 else { return }
        let n = min(Int(nbytes) / MemoryLayout<pid_t>.stride, pids.count)
        var pathBuf = [CChar](repeating: 0, count: 4 * 1024)
        var killed = 0
        for i in 0..<n {
            let pid = pids[i]
            if pid <= 1 || pid == selfPid { continue }
            let plen = pathBuf.withUnsafeMutableBufferPointer { buf -> Int32 in
                proc_pidpath(pid, buf.baseAddress, UInt32(buf.count))
            }
            if plen <= 0 { continue }
            if String(cString: pathBuf) == bin {
                kill(pid, SIGTERM)
                killed += 1
            }
        }
        if killed > 0 { Thread.sleep(forTimeInterval: 0.15) }
    }

    /// CLI `-path` for loft-front. GET keeps the profile path; other fronts omit a
    /// leftover GET cover so loft-front uses its per-kind default (`/api/v1/tunnel` for h2/h3/l3).
    static func cliPath(kind: String, path: String) -> String? {
        let k = kind.lowercased()
        if k == "sstp" || k == "vless" || k == "trojan" { return nil }
        let p = path.trimmingCharacters(in: .whitespacesAndNewlines)
        switch k {
        case "get", "auto":
            return p.isEmpty ? nil : p
        case "h2", "h3", "l3":
            if p.isEmpty { return nil }
            if p.hasPrefix("/static/") || p.hasSuffix(".js") { return nil }
            return p
        default:
            return nil
        }
    }

    /// Path to bundled `loft-front` next to the app executable.
    static func binaryPath() -> String? {
        var paths: [String] = []
        let bundle = Bundle.main.bundlePath
        paths.append((bundle as NSString).appendingPathComponent("Contents/MacOS/loft-front"))
        if let exe = Bundle.main.executablePath {
            paths.append((exe as NSString).deletingLastPathComponent + "/loft-front")
        }
        for p in paths where runnableFile(p) { return p }
        LoftLog.shared.add("front", "no loft-front tried=\(paths.joined(separator: ","))")
        return nil
    }

    /// True if `path` is a regular file we can hand to Process (x-bit optional).
    private static func runnableFile(_ path: String) -> Bool {
        var dir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: path, isDirectory: &dir), !dir.boolValue else {
            return false
        }
        return true
    }

    /// Launches loft-front for `profile`. `socksBind` is `host:port` or empty (off). `probe` skips TUN.
    func start(profile: LoftProfile, socksBind: String, probe: Bool = false, instanceFile: String = "") throws {
        stop()
        lock.lock()
        stopping = false
        outRest = ""
        errRest = ""
        lock.unlock()
        guard let bin = FrontHelper.binaryPath() else {
            throw NSError(domain: "loft", code: 20, userInfo: [NSLocalizedDescriptionKey: "no loft-front"])
        }
        let kind = profile.kind.isEmpty ? "auto" : profile.kind
        let path = FrontHelper.cliPath(kind: kind, path: profile.path)
        let socks = socksBind
        _ = Priv.physicalGateway()
        let iff = Priv.cachedIface()
        let ifLog = iff.isEmpty ? "auto" : "auto \(iff)"
        if profile.isSSTP {
            LoftLog.shared.add("front", "spawn sstp \(profile.url) user=\(profile.user) socks=\(socks.isEmpty ? "off" : socks) tun=\(profile.tun ? 1 : 0) if=\(ifLog)")
        } else if profile.isVLESS {
            LoftLog.shared.add("front", "spawn vless \(profile.url) socks=\(socks.isEmpty ? "off" : socks) tun=\(profile.tun ? 1 : 0) if=\(ifLog)")
        } else if profile.isTrojan {
            LoftLog.shared.add("front", "spawn trojan \(profile.url) socks=\(socks.isEmpty ? "off" : socks) tun=\(profile.tun ? 1 : 0) if=\(ifLog)")
        } else {
            LoftLog.shared.add("front", "spawn \(kind) \(profile.url) path=\(path ?? "") socks=\(socks.isEmpty ? "off" : socks) tun=\(profile.tun ? 1 : 0) mux=\(profile.mux ? 1 : 0) if=\(ifLog)")
        }
        let p = Process()
        var args = [
            "-token", profile.token,
            "-url", profile.url,
            "-type", kind,
            "-socks", socks.isEmpty ? "off" : socks,
        ]
        if let path = path, !path.isEmpty {
            args += ["-path", path]
        }
        if !profile.mux || kind.lowercased() == "l3" { args.append("-mux=0") }
        if profile.isSSTP, !profile.user.isEmpty {
            args += ["-user", profile.user]
        }
        if profile.isSSTP || profile.isVLESS || profile.isTrojan {
            if !profile.tlsCheck { args.append("-insecure") }
            let sni = profile.sni.trimmingCharacters(in: .whitespacesAndNewlines)
            if !sni.isEmpty { args += ["-sni", sni] }
        }
        if profile.isSSTP {
            if profile.papCHAP { args.append("-pap") }
            let dns = LoftProfile.normalizeDNS(profile.dns)
            if !dns.isEmpty { args += ["-dns", dns] }
            if profile.dnsReplace { args.append("-dns-replace") }
        }
        let cdn = profile.cdn.trimmingCharacters(in: .whitespacesAndNewlines)
        if !cdn.isEmpty {
            args += ["-cdn", cdn]
        } else if profile.kind == "get", let host = URL(string: profile.url)?.host, host.contains("cdn.") {
            args += ["-cdn", "yandex"]
        }
        let pbk = profile.maskPub.trimmingCharacters(in: .whitespacesAndNewlines)
        if !pbk.isEmpty { args += ["-pbk", pbk] }
        if !instanceFile.isEmpty {
            let dir = (instanceFile as NSString).deletingLastPathComponent
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            let inst = profile.instanceID.trimmingCharacters(in: .whitespacesAndNewlines)
            if !inst.isEmpty {
                try? (inst + "\n").write(toFile: instanceFile, atomically: true, encoding: .utf8)
            }
            args += ["-instance-file", instanceFile]
        }
        tunSock = ""
        if profile.tun && !probe {
            let tag = profile.id.isEmpty ? "x" : profile.id
            tunSock = NSTemporaryDirectory() + "loft-tun-\(getpid())-\(tag).sock"
            try? FileManager.default.removeItem(atPath: tunSock)
            args += ["-tun-sock", tunSock]
        }
        if probe { args.append("-probe") }
        p.arguments = args
        let out = Pipe()
        let err = Pipe()
        p.standardOutput = out
        p.standardError = err
        p.standardInput = Pipe()
        overlayIP = ""
        overlayGW = ""
        overlayDNS = ""
        overlayMTU = 0
        overlayRoutes = []
        overlayFronts = ""
        tunLive = false
        lastError = ""
        lastStage = ""
        bytesUp = 0
        bytesDown = 0
        self.socksBind = ""
        self.socksEpoch = 0
        let sem = DispatchSemaphore(value: 0)
        out.fileHandleForReading.readabilityHandler = { [weak self] h in
            self?.eat(h, err: false, sem: sem)
        }
        err.fileHandleForReading.readabilityHandler = { [weak self] h in
            self?.eat(h, err: true, sem: sem)
        }
        p.terminationHandler = { [weak self] proc in
            out.fileHandleForReading.readabilityHandler = nil
            err.fileHandleForReading.readabilityHandler = nil
            self?.flush(err: false)
            self?.flush(err: true)
            let code = proc.terminationStatus
            self?.lock.lock()
            let stop = self?.stopping ?? true
            self?.lock.unlock()
            if code != 0 {
                if self?.lastError.isEmpty != false {
                    self?.lastError = "front \(code)"
                }
            }
            LoftLog.shared.add("front", stop ? "stop \(code)" : "exit \(code)")
            if !stop {
                self?.onExit?(code)
            }
            sem.signal()
        }
        try p.start(at: bin)
        lock.lock()
        proc = p
        waitSem = sem
        let stopNow = stopping
        lock.unlock()
        if stopNow {
            if p.processIdentifier > 0 { kill(p.processIdentifier, SIGKILL) }
            lock.lock(); waitSem = nil; lock.unlock()
            throw NSError(domain: "loft", code: 24, userInfo: [NSLocalizedDescriptionKey: "cancelled"])
        }
        let deadline = Date().addingTimeInterval(25)
        while overlayIP.isEmpty && lastError.isEmpty {
            lock.lock(); let stop = stopping; lock.unlock()
            if stop { break }
            if !p.isRunning { break }
            let left = deadline.timeIntervalSinceNow
            if left <= 0 { break }
            _ = sem.wait(timeout: .now() + left)
        }
        lock.lock()
        waitSem = nil
        let cancelled = stopping
        lock.unlock()
        if cancelled {
            stop()
            throw NSError(domain: "loft", code: 24, userInfo: [NSLocalizedDescriptionKey: "cancelled"])
        }
        if overlayIP.isEmpty && p.isRunning {
            LoftLog.shared.add("front", "timeout 25s")
        }
        if overlayIP.isEmpty {
            let running = p.isRunning
            let err = lastError
            stop()
            if !err.isEmpty {
                throw NSError(domain: "loft", code: 21, userInfo: [NSLocalizedDescriptionKey: err])
            }
            if !running {
                throw NSError(domain: "loft", code: 22, userInfo: [NSLocalizedDescriptionKey: "front"])
            }
            throw NSError(domain: "loft", code: 23, userInfo: [NSLocalizedDescriptionKey: "front timeout"])
        }
    }

    /// Kills loft-front immediately so a blocked GET handshake cannot hold Cancel.
    func interrupt() {
        lock.lock()
        stopping = true
        let p = proc
        let sem = waitSem
        lock.unlock()
        sem?.signal()
        tunSem.signal()
        socksSem.signal()
        if let p = p {
            let pid = p.processIdentifier
            LoftLog.shared.add("front", "interrupt pid=\(pid) running=\(p.isRunning ? 1 : 0)")
            if pid > 0 { kill(pid, SIGKILL) }
        } else {
            LoftLog.shared.add("front", "interrupt no-proc")
        }
    }

    /// Terminates loft-front (SIGKILL after 1.5s) and removes the TUN socket.
    func stop() {
        lock.lock()
        stopping = true
        let p = proc
        proc = nil
        lock.unlock()
        waitSem?.signal()
        tunSem.signal()
        socksSem.signal()
        if let p = p, p.isRunning {
            let gone = DispatchSemaphore(value: 0)
            let prev = p.terminationHandler
            p.terminationHandler = { proc in
                prev?(proc)
                gone.signal()
            }
            p.terminate()
            if p.isRunning, gone.wait(timeout: .now() + 1.5) == .timedOut {
                if p.processIdentifier > 0 { kill(p.processIdentifier, SIGKILL) }
                _ = gone.wait(timeout: .now() + 0.4)
            }
        }
        if !tunSock.isEmpty {
            try? FileManager.default.removeItem(atPath: tunSock)
            tunSock = ""
        }
    }

    /// Blocks until loft-front reports `tun up`, or `seconds` elapse.
    func waitTun(seconds: TimeInterval) {
        let deadline = Date().addingTimeInterval(seconds)
        while !tunLive {
            lock.lock(); let stop = stopping; lock.unlock()
            if stop { return }
            let left = deadline.timeIntervalSinceNow
            if left <= 0 { return }
            _ = tunSem.wait(timeout: .now() + left)
        }
    }

    /// Blocks until a `socks5` STATUS newer than `gen` arrives, or `seconds` elapse.
    func waitSocks(after gen: UInt64, seconds: TimeInterval) {
        let deadline = Date().addingTimeInterval(seconds)
        while socksGen() == gen {
            lock.lock(); let stop = stopping; lock.unlock()
            if stop { return }
            let left = deadline.timeIntervalSinceNow
            if left <= 0 { return }
            _ = socksSem.wait(timeout: .now() + left)
        }
    }

    /// Reads available pipe data and splits complete lines into `ingest`.
    private func eat(_ h: FileHandle, err: Bool, sem: DispatchSemaphore) {
        let data = h.availableData
        if data.isEmpty { return }
        let s = String(data: data, encoding: .utf8) ?? String(data: data, encoding: .isoLatin1) ?? ""
        if s.isEmpty { return }
        if err { fputs(s, stderr) }
        lock.lock()
        if err { errRest += s } else { outRest += s }
        let blob = err ? errRest : outRest
        var parts = blob.components(separatedBy: "\n")
        let rest = parts.popLast() ?? ""
        if err { errRest = rest } else { outRest = rest }
        lock.unlock()
        for raw in parts {
            let line = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            if line.isEmpty { continue }
            ingest(line, err: err, sem: sem)
        }
    }

    /// Ingests a leftover partial line when a pipe closes.
    private func flush(err: Bool) {
        lock.lock()
        let rest = (err ? errRest : outRest).trimmingCharacters(in: .whitespacesAndNewlines)
        if err { errRest = "" } else { outRest = "" }
        lock.unlock()
        if rest.isEmpty { return }
        ingest(rest, err: err, sem: nil)
    }

    /// Logs a loft-front line, updates STATUS fields, and signals handshake waiters.
    private func ingest(_ line: String, err: Bool, sem: DispatchSemaphore?) {
        if line.hasPrefix("tun up") || line.hasPrefix("tun fd ") {
            tunLive = true
            tunSem.signal()
        }
        let low = line.lowercased()
        if low.hasPrefix("socks5 ") {
            let rest = String(line.dropFirst(7)).trimmingCharacters(in: .whitespacesAndNewlines)
            lock.lock()
            socksBind = rest
            socksEpoch += 1
            lock.unlock()
            socksSem.signal()
        }
        if !err && line.hasPrefix("STATUS ") {
            let first = overlayIP.isEmpty
            handle(line)
            onLine?(line)
            if first || line.contains("state=error") || line.contains("state=connecting") {
                LoftLog.shared.add("front", FrontExplain.display(line))
            }
            if line.contains("state=on") || line.contains("state=error") {
                sem?.signal()
            }
            return
        }
        LoftLog.shared.add("front", FrontExplain.display(line))
        onLine?(line)
        if !err {
            handle(line)
            if line.contains("state=on") || line.contains("state=error") {
                sem?.signal()
            }
        }
    }

    /// Parses a `STATUS ` line into overlay IP, TUN, and error fields.
    private func handle(_ line: String) {
        guard line.hasPrefix("STATUS ") else { return }
        let st = FrontExplain.parseStatus(line)
        lock.lock()
        if !st.dns.isEmpty { overlayDNS = st.dns }
        if st.mtu > 0 { overlayMTU = st.mtu }
        if let n = st.up { bytesUp = n }
        if let n = st.down { bytesDown = n }
        if !st.ip.isEmpty { overlayIP = st.ip }
        if !st.gw.isEmpty { overlayGW = st.gw }
        if st.state == "on" {
            overlayRoutes = st.routes
            if !st.fronts.isEmpty { overlayFronts = st.fronts }
        }
        if st.tun == "1" { tunLive = true }
        if st.state == "connecting" { lastStage = st.msg }
        if st.state == "error" { lastError = st.msg.isEmpty ? "front" : FrontExplain.display(st.msg) }
        if st.state == "on", !st.ip.isEmpty { lastError = "" }
        let gotIP = !overlayIP.isEmpty
        let gotErr = !lastError.isEmpty
        let gotTun = tunLive
        lock.unlock()
        if gotTun { tunSem.signal() }
        if gotIP || gotErr { waitSem?.signal() }
    }

    /// Latest (up, down) byte counters from STATUS.
    func traffic() -> (Int64, Int64) {
        lock.lock()
        defer { lock.unlock() }
        return (bytesUp, bytesDown)
    }

    /// Current loft-front SOCKS bind (`host:port` or `off`).
    func socksListen() -> String {
        lock.lock()
        defer { lock.unlock() }
        return socksBind
    }

    /// Count of `socks5` reports from loft-front. Bumps on each rebind.
    func socksGen() -> UInt64 {
        lock.lock()
        defer { lock.unlock() }
        return socksEpoch
    }

    /// Writes one control line to loft-front stdin (`socks host:port` / `socks off`).
    func send(_ line: String) {
        lock.lock()
        let p = proc
        lock.unlock()
        guard let pipe = p?.standardInput as? Pipe else { return }
        var s = line
        if !s.hasSuffix("\n") { s.append("\n") }
        guard let d = s.data(using: .utf8) else { return }
        pipe.fileHandleForWriting.write(d)
    }
}

/// Parses loft-front STATUS lines and maps errors to L10n strings.
enum FrontExplain {
    /// Fields from a `STATUS state=… ip=…` line.
    struct Status {
        var state = ""
        var ip = ""
        var gw = ""
        var tun = ""
        var dns = ""
        var mtu = 0
        var msg = ""
        var up: Int64?
        var down: Int64?
        var routes: [String] = []
        var fronts = ""
        /// `-1` omitted, `0` mux down, `1` mux live.
        var mux = -1
    }

    /// `msg` is the remainder after `msg=`, so handshake errors keep their spaces.
    static func parseStatus(_ line: String) -> Status {
        var st = Status()
        var body = line
        if body.hasPrefix("STATUS ") { body = String(body.dropFirst(7)) }
        var head = body
        if let r = body.range(of: "msg=") {
            let atStart = r.lowerBound == body.startIndex
            let spaced = !atStart && body[body.index(before: r.lowerBound)] == " "
            if atStart || spaced {
                st.msg = String(body[r.upperBound...])
                head = String(body[..<r.lowerBound])
            }
        }
        for part in head.split(separator: " ") {
            let kv = part.split(separator: "=", maxSplits: 1)
            if kv.count != 2 { continue }
            let k = String(kv[0])
            let v = String(kv[1])
            switch k {
            case "state": st.state = v
            case "ip": st.ip = v
            case "gw": st.gw = v
            case "dns": st.dns = v
            case "tun": st.tun = v
            case "mux": st.mux = Int(v) ?? -1
            case "mtu": st.mtu = Int(v) ?? 0
            case "up": st.up = Int64(v)
            case "down": st.down = Int64(v)
            case "routes":
                st.routes = v.split(separator: ",").map { String($0) }.filter { !$0.isEmpty }
            case "fronts":
                st.fronts = v
            default: break
            }
        }
        return st
    }

    /// Origin forgot GET sid, or mux already closed: drop 0/1 while loft-front still runs.
    static func isMuxDown(_ line: String) -> Bool {
        if line.hasPrefix("STATUS ") {
            return parseStatus(line).mux == 0
        }
        let low = line.lowercased()
        if low.contains("sid lost") { return true }
        if low.contains("mux: closed") || low.contains("mux:closed") { return true }
        if low.contains("get front") && low.contains("status 404") && !low.contains("retry") {
            return true
        }
        return false
    }

    /// Supervise finished a fresh handshake; 0/1 can go back on the live tun.
    static func isMuxUp(_ line: String) -> Bool {
        if line.hasPrefix("STATUS ") {
            return parseStatus(line).mux == 1
        }
        let t = line.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return t.hasPrefix("resume ")
    }

    /// Connecting-stage token for the spark ticker, or nil.
    static func stageToken(from line: String) -> String? {
        if line.hasPrefix("STATUS ") {
            let st = parseStatus(line)
            if st.state == "connecting", !st.msg.isEmpty { return st.msg }
            return nil
        }
        let low = line.lowercased()
        if low.hasPrefix("socks5 ") { return "socks" }
        if low.hasPrefix("tun up") { return "tun" }
        if low.hasPrefix("front get:") { return "get" }
        if low.hasPrefix("front ws:") { return "ws" }
        if low.hasPrefix("front h2:") { return "h2" }
        if low.hasPrefix("front h3:") { return "h3" }
        if low.hasPrefix("front quic:") { return "quic" }
        if low.hasPrefix("front l3:") { return "l3" }
        return nil
    }

    /// Localized or short label for a stage token.
    static func stageLabel(_ token: String) -> String {
        switch token.lowercased() {
        case "spawn", "front": return L10n.t("st_front")
        case "get": return "GET"
        case "ws": return "WebSocket"
        case "h2": return "H2"
        case "h3": return "H3"
        case "quic": return "QUIC"
        case "l3": return "L3"
        case "tls": return "TLS"
        case "sstp": return "SSTP"
        case "vless": return "VLESS"
        case "trojan": return "Trojan"
        case "reality": return "REALITY"
        case "xhttp": return "XHTTP"
        case "tcp": return "TCP"
        case "http": return "HTTP"
        case "ppp": return "PPP"
        case "ack": return "SSTP"
        case "handshake": return L10n.t("st_handshake")
        case "overlay": return "overlay"
        case "socks", "socks5": return "SOCKS5"
        case "tun": return "TUN"
        case "mtu": return "MTU"
        default: return token
        }
    }

    /// Human line for the journal and spark: STATUS, auth, GET, and net errors.
    static func display(_ line: String) -> String {
        var s = line
        if s.hasPrefix("STATUS ") {
            let st = parseStatus(s)
            if st.state == "connecting" {
                if st.msg.isEmpty { return L10n.t("connecting") }
                return L10n.t("connecting") + " · " + stageLabel(st.msg)
            }
            if st.state == "error" {
                s = st.msg.isEmpty ? "front" : st.msg
            } else if st.state == "on" {
                return st.ip.isEmpty ? "overlay" : "overlay \(st.ip)"
            }
        }
        let low = s.lowercased()
        if isHTTP2Mask(s, low: low) {
            return L10n.t("err_h2_mask")
        }
        // Server spoke loft: auth and node codes first.
        if low.contains("auth_limit") { return L10n.t("err_auth_limit") }
        if low.contains("auth_already_online") { return L10n.t("err_auth_online") }
        if low.contains("auth_bad_key") || low.contains("auth_revoked") { return L10n.t("err_auth_key") }
        if low.contains("auth_not_authorized") { return L10n.t("err_auth_denied") }
        if low.contains("auth_expired") { return L10n.t("err_auth_expired") }
        if low.contains("get handshake") && low.contains("404") { return L10n.t("err_auth_key") }
        if low.contains("header too large") || low.contains("status 413") { return L10n.t("err_get_413") }
        if low.contains("method not allowed") || low.contains("status 405") { return L10n.t("err_get_405") }
        if low.contains("requires mux") { return L10n.t("err_need_mux") }
        if low.contains("net_disabled") { return L10n.t("err_net_off") }
        if low.contains("ip_pool") { return L10n.t("err_ip_pool") }
        if low.contains("policy_drop") { return L10n.t("err_policy") }
        if low.contains("echo timeout") { return L10n.t("err_stale") }
        if isTransportFail(low) || (low.contains("get handshake") && !low.contains("empty session") && !low.contains("status")) {
            if !LocalNet.hasUplink() { return L10n.t("err_no_net") }
            if isNoReply(low) || low.contains("get handshake") { return L10n.t("err_no_reply") }
            if low.contains("connection refused") { return L10n.t("err_refused") }
            if low.contains("connection reset") { return L10n.t("err_reset") }
            if low.contains("no such host") || low.contains("nodename nor servname") {
                return L10n.t("err_dns")
            }
            if low.contains("network is unreachable") || low.contains("no route") {
                return L10n.t("err_unreach")
            }
            return L10n.t("err_no_reply")
        }
        if low.contains("get handshake") {
            if low.contains("empty session") { return L10n.t("err_get_empty") }
            if low.contains("status") { return L10n.t("err_get_status") }
            return L10n.t("err_get_hs")
        }
        if low.contains("reality: dest") || low.contains("reality dest") || low.contains("vless reality") {
            return L10n.t("err_reality")
        }
        if low.contains("vless uuid") { return L10n.t("err_vless_uuid") }
        if low.contains("vless transport") { return L10n.t("err_vless_transport") }
        if low.hasPrefix("vless:") || low.hasPrefix("vless ") { return L10n.t("err_vless") }
        if low.contains("trojan password") { return L10n.t("err_trojan_password") }
        if low.contains("trojan transport") { return L10n.t("err_trojan_transport") }
        if low.hasPrefix("trojan:") || low.hasPrefix("trojan ") { return L10n.t("err_trojan") }
        if low.contains("chap failure") || low.contains("chap authenticator") || low.contains("pap rejected") {
            return L10n.t("err_sstp_chap")
        }
        if low.contains("sstp http") { return L10n.t("err_sstp_http") }
        if low.hasPrefix("sstp up") || low.hasPrefix("sstp: ") {
            return s
        }
        if low.hasPrefix("sstp ") {
            let rest = String(s.dropFirst(5)).trimmingCharacters(in: .whitespaces)
            switch rest.lowercased() {
            case "tls", "http", "ack", "ppp", "sstp":
                return rest
            default:
                break
            }
            if !LocalNet.hasUplink() { return L10n.t("err_no_net") }
            if rest.isEmpty { return L10n.t("err_sstp") }
            return rest
        }
        if low.contains("wss dial") { return L10n.t("err_ws") }
        if low.contains("front closed") { return L10n.t("err_front_closed") }
        if low.contains("no loft-front") { return L10n.t("err_no_front") }
        if low.hasPrefix("socks ") {
            let rest = String(s.dropFirst(6)).trimmingCharacters(in: .whitespaces)
            return rest.isEmpty ? L10n.t("err_socks") : L10n.t("err_socks") + " " + rest
        }
        if low.hasPrefix("front ") {
            let rest = String(s.dropFirst(6)).trimmingCharacters(in: .whitespaces)
            if !rest.isEmpty && rest.unicodeScalars.allSatisfy({ CharacterSet.decimalDigits.contains($0) }) {
                return L10n.t("err_front_exit") + " (\(rest))"
            }
        }
        if low == "front" { return L10n.t("err_front") }
        if low == "get" { return L10n.t("err_get_hs") }
        if low == "ws" { return L10n.t("err_ws") }
        if low == "eof" || low.hasSuffix(" eof") { return L10n.t("err_eof") }
        return s
    }

    /// True when the line is only “front exited / did not start”, with no STATUS reason.
    static func isGenericExit(_ s: String) -> Bool {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { return true }
        if t == "front" { return true }
        let low = t.lowercased()
        if low.hasPrefix("front ") {
            let rest = low.dropFirst(6)
            if !rest.isEmpty && rest.unicodeScalars.allSatisfy({ CharacterSet.decimalDigits.contains($0) }) {
                return true
            }
        }
        let exit = L10n.t("err_front_exit")
        if t == exit || t.hasPrefix(exit + " ") || t.hasPrefix(exit + " (") { return true }
        if t == L10n.t("err_front") { return true }
        return false
    }

    /// Prefers the STATUS error loft-front already printed; exit code is the fallback.
    static func exitMessage(lastError: String, code: Int32) -> String {
        let t = lastError.trimmingCharacters(in: .whitespacesAndNewlines)
        if !t.isEmpty, !isGenericExit(t) {
            return t
        }
        return display(t.isEmpty ? "front \(code)" : t)
    }

    /// True when the address is an HTTP/2 GET mask, not WebSocket.
    private static func isHTTP2Mask(_ line: String, low: String) -> Bool {
        if low.contains("malformed http") { return true }
        let range = line.range(of: "\\\\x00\\\\x00\\\\x[0-9a-fA-F]{1,2}\\\\x04", options: .regularExpression)
        return range != nil
    }

    /// Timeout / deadline: the front never answered.
    private static func isNoReply(_ low: String) -> Bool {
        low.contains("i/o timeout")
            || low.contains("timed out")
            || low.contains("timeout")
            || low.contains("deadline exceeded")
            || low.contains("no response")
    }

    /// Dial/DNS/route failures, including those wrapped as `get handshake: …`.
    private static func isTransportFail(_ low: String) -> Bool {
        isNoReply(low)
            || low.contains("connection refused")
            || low.contains("connection reset")
            || low.contains("network is unreachable")
            || low.contains("no route to host")
            || low.contains("network is down")
            || low.contains("no such host")
            || low.contains("nodename nor servname")
            || low.contains("host is down")
            || low.contains("broken pipe")
            || low.contains("connection failed")
    }
}

/// Non-loopback IPv4/IPv6 on the Mac, ignoring loft utun and link-local.
enum LocalNet {
    /// True if some interface besides 127.0.0.1 / utun / link-local is up.
    static func hasUplink() -> Bool {
        var ifaddr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddr) == 0 else { return true }
        defer { freeifaddrs(ifaddr) }
        var ptr = ifaddr
        while let p = ptr {
            ptr = p.pointee.ifa_next
            let flags = Int32(p.pointee.ifa_flags)
            if flags & IFF_UP == 0 { continue }
            if flags & IFF_LOOPBACK != 0 { continue }
            let name = String(cString: p.pointee.ifa_name)
            if name.hasPrefix("utun") || name.hasPrefix("awdl") || name.hasPrefix("llw")
                || name.hasPrefix("gif") || name.hasPrefix("stf") { continue }
            guard let sa = p.pointee.ifa_addr else { continue }
            switch Int32(sa.pointee.sa_family) {
            case AF_INET:
                let ip = sa.withMemoryRebound(to: sockaddr_in.self, capacity: 1) { $0.pointee.sin_addr.s_addr }
                let a = Int(ip & 0xff)
                let b = Int((ip >> 8) & 0xff)
                if a == 127 || (a == 169 && b == 254) { continue }
                return true
            case AF_INET6:
                let addr = sa.withMemoryRebound(to: sockaddr_in6.self, capacity: 1) { $0.pointee.sin6_addr }
                let t = addr.__u6_addr.__u6_addr8
                if t.0 == 0xfe && (t.1 & 0xc0) == 0x80 { continue }
                if t.0 == 0xff { continue }
                let zero = t.0 == 0 && t.1 == 0 && t.2 == 0 && t.3 == 0
                    && t.4 == 0 && t.5 == 0 && t.6 == 0 && t.7 == 0
                    && t.8 == 0 && t.9 == 0 && t.10 == 0 && t.11 == 0
                    && t.12 == 0 && t.13 == 0 && t.14 == 0 && t.15 == 0
                if zero { continue }
                if t.15 == 1 && t.0 == 0 && t.1 == 0 && t.2 == 0 && t.3 == 0
                    && t.4 == 0 && t.5 == 0 && t.6 == 0 && t.7 == 0
                    && t.8 == 0 && t.9 == 0 && t.10 == 0 && t.11 == 0
                    && t.12 == 0 && t.13 == 0 && t.14 == 0 { continue }
                return true
            default:
                break
            }
        }
        return false
    }
}
