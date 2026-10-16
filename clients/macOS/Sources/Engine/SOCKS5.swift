// Local SOCKS5 listener that opens mux streams through SessionController.

import Foundation
import Darwin

/// Local SOCKS5 listener that opens mux streams through SessionController.
final class SOCKS5Server: NSObject {
    private var listener: FileHandle?
    private var sockfd: Int32 = -1
    private weak var engine: SessionController?
    private var running = false

    /// Binds `host:port` (default 127.0.0.1:1080) and accepts in a new thread.
    func start(addr: String, engine: SessionController) throws {
        self.engine = engine
        let parts = addr.split(separator: ":")
        let port = UInt16(parts.last.flatMap { UInt16($0) } ?? 1080)
        var hints = addrinfo(ai_flags: AI_PASSIVE, ai_family: AF_INET, ai_socktype: SOCK_STREAM, ai_protocol: IPPROTO_TCP, ai_addrlen: 0, ai_canonname: nil, ai_addr: nil, ai_next: nil)
        var res: UnsafeMutablePointer<addrinfo>?
        let host = parts.count > 1 ? String(parts.dropLast().joined(separator: ":")) : "127.0.0.1"
        guard getaddrinfo(host, String(port), &hints, &res) == 0, let info = res else {
            throw NSError(domain: "loft", code: 20, userInfo: [NSLocalizedDescriptionKey: "socks bind"])
        }
        defer { freeaddrinfo(info) }
        let fd = Darwin.socket(info.pointee.ai_family, info.pointee.ai_socktype, info.pointee.ai_protocol)
        var yes: Int32 = 1
        Darwin.setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &yes, socklen_t(MemoryLayout<Int32>.size))
        if Darwin.bind(fd, info.pointee.ai_addr, info.pointee.ai_addrlen) != 0 || Darwin.listen(fd, 32) != 0 {
            Darwin.close(fd)
            throw NSError(domain: "loft", code: 21, userInfo: [NSLocalizedDescriptionKey: "socks listen \(addr)"])
        }
        sockfd = fd
        running = true
        Thread.detachNewThreadSelector(#selector(acceptLoop), toTarget: self, with: nil)
    }

    /// Closes the listen socket so `acceptLoop` exits.
    func stop() {
        running = false
        if sockfd >= 0 { Darwin.close(sockfd); sockfd = -1 }
    }

    /// Accepts clients and handles each on its own thread.
    @objc private func acceptLoop() {
        while running {
            var addr = sockaddr()
            var len: socklen_t = socklen_t(MemoryLayout<sockaddr>.size)
            let cfd = Darwin.accept(sockfd, &addr, &len)
            if cfd < 0 { continue }
            let fd = cfd
            Thread.detachNewThread {
                self.handle(fd)
            }
        }
    }

    /// SOCKS5 no-auth CONNECT; IPv6 ATYP is rejected. Relays to a mux stream.
    private func handle(_ fd: Int32) {
        defer { Darwin.close(fd) }
        var hello = [UInt8](repeating: 0, count: 2)
        if readn(fd, &hello, 2) != 2 || hello[0] != 5 { return }
        let nmethods = Int(hello[1])
        var methods = [UInt8](repeating: 0, count: max(nmethods, 1))
        if nmethods > 0 { _ = readn(fd, &methods, nmethods) }
        let pick: [UInt8] = [5, 0]
        _ = pick.withUnsafeBytes { Darwin.write(fd, $0.baseAddress, 2) }
        var req = [UInt8](repeating: 0, count: 4)
        if readn(fd, &req, 4) != 4 || req[0] != 5 { return }
        let cmd = req[1]
        let atyp = req[3]
        var host = ""
        var port: UInt16 = 0
        if atyp == 1 {
            var ip = [UInt8](repeating: 0, count: 4)
            if readn(fd, &ip, 4) != 4 { return }
            host = "\(ip[0]).\(ip[1]).\(ip[2]).\(ip[3])"
        } else if atyp == 3 {
            var ln: UInt8 = 0
            if readn(fd, &ln, 1) != 1 { return }
            var name = [UInt8](repeating: 0, count: Int(ln))
            if readn(fd, &name, Int(ln)) != Int(ln) { return }
            host = String(bytes: name, encoding: .utf8) ?? ""
        } else {
            let fail: [UInt8] = [5, 8, 0, 1, 0, 0, 0, 0, 0, 0]
            _ = fail.withUnsafeBytes { Darwin.write(fd, $0.baseAddress, 10) }
            return
        }
        var pb = [UInt8](repeating: 0, count: 2)
        if readn(fd, &pb, 2) != 2 { return }
        port = (UInt16(pb[0]) << 8) | UInt16(pb[1])
        if cmd != 1 {
            let fail: [UInt8] = [5, 7, 0, 1, 0, 0, 0, 0, 0, 0]
            _ = fail.withUnsafeBytes { Darwin.write(fd, $0.baseAddress, 10) }
            return
        }
        guard let stream = engine?.openStream(host: host, port: port) else {
            let fail: [UInt8] = [5, 1, 0, 1, 0, 0, 0, 0, 0, 0]
            _ = fail.withUnsafeBytes { Darwin.write(fd, $0.baseAddress, 10) }
            return
        }
        let ok: [UInt8] = [5, 0, 0, 1, 0, 0, 0, 0, 0, 0]
        _ = ok.withUnsafeBytes { Darwin.write(fd, $0.baseAddress, 10) }
        let cfd = fd
        let group = DispatchGroup()
        group.enter()
        DispatchQueue.global().async {
            while let chunk = stream.read(16 * 1024), !chunk.isEmpty {
                chunk.withUnsafeBytes { _ = Darwin.write(cfd, $0.baseAddress, chunk.count) }
            }
            group.leave()
        }
        group.enter()
        DispatchQueue.global().async {
            var buf = [UInt8](repeating: 0, count: 16 * 1024)
            while true {
                let n = Darwin.read(cfd, &buf, buf.count)
                if n <= 0 { break }
                if !stream.write(Data(buf.prefix(n))) { break }
            }
            stream.close()
            group.leave()
        }
        group.wait()
    }

    /// Reads exactly `n` bytes, or fewer on EOF/error.
    private func readn(_ fd: Int32, _ buf: UnsafeMutableRawPointer, _ n: Int) -> Int {
        var got = 0
        while got < n {
            let r = Darwin.read(fd, buf.advanced(by: got), n - got)
            if r <= 0 { return got }
            got += r
        }
        return got
    }
}
