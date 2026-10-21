// TCP splice: shared SOCKS port → the current loft-front unique SOCKS.

import Foundation
import Darwin

/// Listens on the shared SOCKS port and splices each client to `target`.
final class SocksRelay: NSObject {
    private let lock = NSLock()
    private var listenFD: Int32 = -1
    private var targetHost = "127.0.0.1"
    private var targetPort: UInt16 = 0
    private var running = false
    private var listenAddr = ""
    private static let copies = DispatchQueue(label: "app.loft.socks-copy", qos: .userInitiated, attributes: .concurrent)
    private static let copyGate = DispatchSemaphore(value: 32)

    /// True while the shared port is bound.
    var isListening: Bool {
        lock.lock(); defer { lock.unlock() }
        return listenFD >= 0 && running
    }

    /// loft-front SOCKS `host:port`, or empty to drop new clients.
    func setTarget(_ addr: String) {
        lock.lock()
        defer { lock.unlock() }
        let t = addr.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty {
            targetPort = 0
            return
        }
        let parts = t.split(separator: ":")
        if parts.count >= 2, let p = UInt16(parts.last!) {
            var host = parts.dropLast().joined(separator: ":")
            if host.isEmpty { host = "127.0.0.1" }
            targetHost = host
            targetPort = p
        } else {
            targetPort = 0
        }
    }

    /// Binds `addr` (`127.0.0.1:1080`) once; later calls only refresh the listen if the port changed.
    func start(listen addr: String) throws {
        lock.lock()
        let same = running && listenFD >= 0 && listenAddr == addr
        lock.unlock()
        if same { return }
        stop()
        let parts = addr.split(separator: ":")
        let port = UInt16(parts.last.flatMap { UInt16($0) } ?? 1080)
        let host = parts.count > 1 ? String(parts.dropLast().joined(separator: ":")) : "127.0.0.1"
        var hints = addrinfo(
            ai_flags: AI_PASSIVE, ai_family: AF_INET, ai_socktype: SOCK_STREAM,
            ai_protocol: IPPROTO_TCP, ai_addrlen: 0, ai_canonname: nil, ai_addr: nil, ai_next: nil
        )
        var res: UnsafeMutablePointer<addrinfo>?
        guard getaddrinfo(host, String(port), &hints, &res) == 0, let info = res else {
            throw NSError(domain: "loft", code: 20, userInfo: [NSLocalizedDescriptionKey: "socks relay bind"])
        }
        defer { freeaddrinfo(info) }
        let fd = Darwin.socket(info.pointee.ai_family, info.pointee.ai_socktype, info.pointee.ai_protocol)
        var yes: Int32 = 1
        Darwin.setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &yes, socklen_t(MemoryLayout<Int32>.size))
        if Darwin.bind(fd, info.pointee.ai_addr, info.pointee.ai_addrlen) != 0 || Darwin.listen(fd, 64) != 0 {
            Darwin.close(fd)
            throw NSError(domain: "loft", code: 21, userInfo: [NSLocalizedDescriptionKey: "socks relay listen \(addr)"])
        }
        lock.lock()
        listenFD = fd
        listenAddr = addr
        running = true
        lock.unlock()
        Thread.detachNewThreadSelector(#selector(acceptLoop), toTarget: self, with: nil)
    }

    /// Drops the listen socket so `acceptLoop` exits.
    func stop() {
        lock.lock()
        running = false
        let fd = listenFD
        listenFD = -1
        listenAddr = ""
        lock.unlock()
        if fd >= 0 { Darwin.close(fd) }
    }

    /// Accepts clients and splices each to the current target.
    @objc private func acceptLoop() {
        while true {
            lock.lock()
            let lfd = listenFD
            let go = running
            lock.unlock()
            if !go || lfd < 0 { return }
            var addr = sockaddr()
            var len = socklen_t(MemoryLayout<sockaddr>.size)
            let cfd = Darwin.accept(lfd, &addr, &len)
            if cfd < 0 { continue }
            Self.tuneTCP(cfd)
            lock.lock()
            let host = targetHost
            let port = targetPort
            lock.unlock()
            Self.copies.async {
                Self.copyGate.wait()
                self.splice(cfd, host: host, port: port)
                Self.copyGate.signal()
            }
        }
    }

    /// Connects `cfd` to `host:port` and copies both ways.
    private func splice(_ cfd: Int32, host: String, port: UInt16) {
        defer { Darwin.close(cfd) }
        if port == 0 { return }
        var hints = addrinfo(
            ai_flags: 0, ai_family: AF_INET, ai_socktype: SOCK_STREAM,
            ai_protocol: IPPROTO_TCP, ai_addrlen: 0, ai_canonname: nil, ai_addr: nil, ai_next: nil
        )
        var res: UnsafeMutablePointer<addrinfo>?
        guard getaddrinfo(host, String(port), &hints, &res) == 0, let info = res else { return }
        defer { freeaddrinfo(info) }
        let rfd = Darwin.socket(info.pointee.ai_family, info.pointee.ai_socktype, info.pointee.ai_protocol)
        if rfd < 0 { return }
        defer { Darwin.close(rfd) }
        if Darwin.connect(rfd, info.pointee.ai_addr, info.pointee.ai_addrlen) != 0 { return }
        Self.tuneTCP(cfd)
        Self.tuneTCP(rfd)
        let sem = DispatchSemaphore(value: 0)
        Self.copies.async {
            Self.copy(cfd, rfd)
            Darwin.shutdown(rfd, SHUT_WR)
            sem.signal()
        }
        Self.copy(rfd, cfd)
        Darwin.shutdown(cfd, SHUT_WR)
        _ = sem.wait(timeout: .now() + 30)
    }

    /// TCP_NODELAY + NOSIGPIPE on a splice fd (SOCKS handshake is tiny writes).
    private static func tuneTCP(_ fd: Int32) {
        var yes: Int32 = 1
        Darwin.setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &yes, socklen_t(MemoryLayout<Int32>.size))
        Darwin.setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &yes, socklen_t(MemoryLayout<Int32>.size))
    }

    /// Blocking copy until EOF or error.
    private static func copy(_ from: Int32, _ to: Int32) {
        var buf = [UInt8](repeating: 0, count: 16_384)
        while true {
            let n = buf.withUnsafeMutableBytes { Darwin.read(from, $0.baseAddress, 16_384) }
            if n <= 0 { return }
            var off = 0
            while off < n {
                let w = buf.withUnsafeBytes { Darwin.write(to, $0.baseAddress! + off, n - off) }
                if w <= 0 { return }
                off += Int(w)
            }
        }
    }
}
