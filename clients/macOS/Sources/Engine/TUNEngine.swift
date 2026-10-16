// Leftover in-process TUN: utun fd from loft-priv plus the C userspace stack.
// Live connect uses loft-front's TUN socket and Priv.upTUN; SessionController
// still holds a TUNEngine for stop() and holdArmed.

import Foundation
import Darwin

/// Retains a mux stream for C stack TCP callbacks.
private final class StreamBox: NSObject {
    let stream: LoftStream
    var stop = false
    /// Wraps `stream` for Unmanaged retain/release from C.
    init(stream: LoftStream) { self.stream = stream }
}

/// Leftover in-process TUN: utun + C stack + GETFront. Not started on the live path.
final class TUNEngine: NSObject {
    private(set) var name = ""
    private(set) var ip = ""
    private var fd: Int32 = -1
    private var stack: OpaquePointer?
    private var stopFlag = false
    private var front: GETFront?
    private var exclude: [String] = []
    private var gw = ""
    private var hold = true
    private var blackhole = false
    private let boxLock = NSLock()
    private var boxes: [UnsafeMutableRawPointer: StreamBox] = [:]

    /// Opens utun via loft-priv, builds the C stack, and pumps packets.
    func start(front: GETFront, overlayIP: String, mtu: UInt16, hold: Bool) throws {
        self.front = front
        self.hold = hold
        stopFlag = false
        var rawIP = overlayIP
        if rawIP.contains("/") { rawIP = String(rawIP.split(separator: "/")[0]) }
        if ipv4(rawIP) == nil { rawIP = "10.7.0.2" }
        ip = rawIP
        guard let our = ipv4(rawIP) else {
            throw NSError(domain: "loft", code: 41, userInfo: [NSLocalizedDescriptionKey: "overlay ip"])
        }
        var peerU = our
        peerU ^= 1
        let peer = ipv4str(peerU)
        let useMTU = mtu > 0 ? mtu : 1280
        gw = Priv.gateway()
        exclude = Priv.resolve(front.frontHost)
        try takeUtun(peer: peer, mtu: useMTU)

        var cbs = loft_stack_cbs()
        cbs.ctx = Unmanaged.passUnretained(self).toOpaque()
        cbs.tcp_open = tunTcpOpen
        cbs.tcp_ready = tunTcpReady
        cbs.tcp_send = tunTcpSend
        cbs.tcp_close = tunTcpClose
        cbs.udp_send = tunUdpSend
        cbs.output = tunOutput
        stack = loft_stack_new(our, useMTU, cbs)
        front.onDgram { [weak self] host, port, data in
            guard let self = self, let st = self.stack, !self.stopFlag else { return }
            data.withUnsafeBytes { raw in
                host.withCString { c in
                    loft_stack_udp_rx(st, c, port, raw.bindMemory(to: UInt8.self).baseAddress, data.count)
                }
            }
        }
        Thread.detachNewThreadSelector(#selector(readLoop), toTarget: self, with: nil)
        Thread.detachNewThreadSelector(#selector(tickLoop), toTarget: self, with: nil)
    }

    /// Frees the stack, closes utun, and optionally blackholes 0/0 if `hold`.
    func stop() {
        let had = fd >= 0
        stopFlag = true
        front?.onDgram(nil)
        boxLock.lock()
        let live = Array(boxes.values)
        boxLock.unlock()
        for b in live { b.stop = true; b.stream.close() }
        Thread.sleep(forTimeInterval: 0.15)
        if let st = stack {
            loft_stack_free(st)
            stack = nil
        }
        if fd >= 0 { loft_utun_close(fd); fd = -1 }
        name = ""
        if !had && !blackhole { return }
        var undo = "route -n delete -net 0.0.0.0/1 2>/dev/null; route -n delete -net 128.0.0.0/1 2>/dev/null"
        for h in exclude {
            undo += "; route -n delete -host \(h) 2>/dev/null"
        }
        if hold && had {
            undo += "; route -n add -inet 0.0.0.0/0 127.0.0.1 -blackhole"
            blackhole = true
        }
        try? Priv.runAdmin(undo)
    }

    /// Asks loft-priv (admin) to open utun and receive the fd over a unix socket.
    private func takeUtun(peer: String, mtu: UInt16) throws {
        let helper = (Bundle.main.executableURL?.deletingLastPathComponent().appendingPathComponent("loft-priv").path)
            ?? ""
        if helper.isEmpty || !FileManager.default.isExecutableFile(atPath: helper) {
            throw NSError(domain: "loft", code: 40, userInfo: [NSLocalizedDescriptionKey: "loft-priv"])
        }
        let sock = NSTemporaryDirectory() + "loft-\(getpid()).sock"
        let lfd = loft_utun_listen(sock)
        if lfd < 0 {
            throw NSError(domain: "loft", code: 40, userInfo: [NSLocalizedDescriptionKey: "utun sock"])
        }
        var cname = [CChar](repeating: 0, count: 16)
        var nfd: Int32 = -1
        let g = DispatchGroup()
        g.enter()
        DispatchQueue.global(qos: .userInitiated).async {
            nfd = loft_utun_recv_fd(lfd, &cname, 120000)
            g.leave()
        }
        var cmd = "'\(helper.replacingOccurrences(of: "'", with: "'\\''"))' --sock '\(sock)' --ip \(ip) --peer \(peer) --mtu \(mtu)"
        if !gw.isEmpty { cmd += " --gw \(gw)" }
        if !exclude.isEmpty { cmd += " --exclude \(exclude.joined(separator: ","))" }
        if blackhole { cmd += " --clear-blackhole"; blackhole = false }
        do {
            try Priv.runAdmin(cmd)
        } catch {
            Darwin.close(lfd)
            g.wait()
            unlink(sock)
            throw error
        }
        g.wait()
        Darwin.close(lfd)
        unlink(sock)
        if nfd < 0 {
            throw NSError(domain: "loft", code: 40, userInfo: [NSLocalizedDescriptionKey: "utun"])
        }
        fd = nfd
        name = String(cString: cname)
    }

    /// Deletes the blackhole default if `stop` installed one.
    func resetHold() {
        if blackhole {
            try? Priv.runAdmin("route -n delete -inet 0.0.0.0/0")
            blackhole = false
        }
    }

    var holdArmed: Bool { blackhole }

    /// Reads IPv4 packets from utun into the C stack.
    @objc private func readLoop() {
        var buf = [UInt8](repeating: 0, count: 2048)
        while !stopFlag {
            let n = loft_utun_read(fd, &buf, buf.count)
            if n <= 0 {
                if stopFlag { break }
                Thread.sleep(forTimeInterval: 0.05)
                continue
            }
            if let st = stack { loft_stack_input(st, buf, Int(n)) }
        }
    }

    /// Retransmit/timeout tick for the C stack, 250 ms.
    @objc private func tickLoop() {
        while !stopFlag {
            Thread.sleep(forTimeInterval: 0.25)
            if let st = stack { loft_stack_tick(st) }
        }
    }

    /// Opens a GETFront mux stream; cookie is a retained StreamBox pointer.
    fileprivate func openTCP(_ host: String, _ port: UInt16) -> UnsafeMutableRawPointer? {
        guard let stream = front?.open(host: host, port: port) else { return nil }
        let box = StreamBox(stream: stream)
        let ptr = Unmanaged.passRetained(box).toOpaque()
        boxLock.lock(); boxes[ptr] = box; boxLock.unlock()
        return ptr
    }

    /// Reads mux bytes into the C stack until EOF.
    fileprivate func readyTCP(_ cookie: UnsafeMutableRawPointer) {
        boxLock.lock(); let box = boxes[cookie]; boxLock.unlock()
        guard let box = box, let st = stack else { return }
        Thread.detachNewThread {
            while !box.stop {
                guard let chunk = box.stream.read(8192) else { break }
                if chunk.isEmpty { break }
                chunk.withUnsafeBytes { raw in
                    loft_stack_tcp_rx(st, cookie, raw.bindMemory(to: UInt8.self).baseAddress, chunk.count)
                }
            }
            if !box.stop { loft_stack_tcp_eof(st, cookie) }
        }
    }

    /// Writes stack TCP payload onto the mux stream.
    fileprivate func sendTCP(_ cookie: UnsafeMutableRawPointer, _ p: UnsafePointer<UInt8>, _ n: Int) -> Int32 {
        boxLock.lock(); let box = boxes[cookie]; boxLock.unlock()
        guard let box = box else { return -1 }
        let d = Data(bytes: p, count: n)
        return box.stream.write(d) ? 0 : -1
    }

    /// Closes the mux stream and releases the StreamBox.
    fileprivate func closeTCP(_ cookie: UnsafeMutableRawPointer) {
        boxLock.lock(); let box = boxes.removeValue(forKey: cookie); boxLock.unlock()
        box?.stop = true
        box?.stream.close()
        Unmanaged<StreamBox>.fromOpaque(cookie).release()
    }

    /// Sends a UDP datagram through GETFront.
    fileprivate func sendUDP(_ host: String, _ port: UInt16, _ p: UnsafePointer<UInt8>, _ n: Int) -> Int32 {
        let d = Data(bytes: p, count: n)
        return front?.writeDgram(host: host, port: port, data: d) == true ? 0 : -1
    }

    /// Writes a stack IP packet to utun.
    fileprivate func output(_ pkt: UnsafePointer<UInt8>, _ n: Int) -> Int32 {
        if fd < 0 { return -1 }
        return Int32(loft_utun_write(fd, pkt, n))
    }
}

/// C stack callback: open a mux TCP stream.
private func tunTcpOpen(_ ctx: UnsafeMutableRawPointer?, _ ip: UnsafePointer<CChar>?, _ port: UInt16) -> UnsafeMutableRawPointer? {
    guard let ctx = ctx, let ip = ip else { return nil }
    let eng = Unmanaged<TUNEngine>.fromOpaque(ctx).takeUnretainedValue()
    return eng.openTCP(String(cString: ip), port)
}

/// C stack callback: start reading the mux stream.
private func tunTcpReady(_ ctx: UnsafeMutableRawPointer?, _ cookie: UnsafeMutableRawPointer?) {
    guard let ctx = ctx, let cookie = cookie else { return }
    Unmanaged<TUNEngine>.fromOpaque(ctx).takeUnretainedValue().readyTCP(cookie)
}

/// C stack callback: write payload to the mux stream.
private func tunTcpSend(_ ctx: UnsafeMutableRawPointer?, _ cookie: UnsafeMutableRawPointer?, _ p: UnsafePointer<UInt8>?, _ n: Int) -> Int32 {
    guard let ctx = ctx, let cookie = cookie, let p = p else { return -1 }
    return Unmanaged<TUNEngine>.fromOpaque(ctx).takeUnretainedValue().sendTCP(cookie, p, n)
}

/// C stack callback: close and release the mux stream.
private func tunTcpClose(_ ctx: UnsafeMutableRawPointer?, _ cookie: UnsafeMutableRawPointer?) {
    guard let ctx = ctx, let cookie = cookie else { return }
    Unmanaged<TUNEngine>.fromOpaque(ctx).takeUnretainedValue().closeTCP(cookie)
}

/// C stack callback: send a UDP datagram through GETFront.
private func tunUdpSend(_ ctx: UnsafeMutableRawPointer?, _ ip: UnsafePointer<CChar>?, _ port: UInt16, _ p: UnsafePointer<UInt8>?, _ n: Int) -> Int32 {
    guard let ctx = ctx, let ip = ip, let p = p else { return -1 }
    return Unmanaged<TUNEngine>.fromOpaque(ctx).takeUnretainedValue().sendUDP(String(cString: ip), port, p, n)
}

/// C stack callback: write an IP packet to utun.
private func tunOutput(_ ctx: UnsafeMutableRawPointer?, _ pkt: UnsafePointer<UInt8>?, _ n: Int) -> Int32 {
    guard let ctx = ctx, let pkt = pkt else { return -1 }
    return Unmanaged<TUNEngine>.fromOpaque(ctx).takeUnretainedValue().output(pkt, n)
}

/// Parses dotted IPv4 to host-order u32.
private func ipv4(_ s: String) -> UInt32? {
    let p = s.split(separator: ".")
    guard p.count == 4,
          let a = UInt32(p[0]), let b = UInt32(p[1]), let c = UInt32(p[2]), let d = UInt32(p[3]),
          a < 256, b < 256, c < 256, d < 256 else { return nil }
    return (a << 24) | (b << 16) | (c << 8) | d
}

/// Formats host-order u32 as dotted IPv4.
private func ipv4str(_ n: UInt32) -> String {
    return "\(n >> 24).\((n >> 16) & 255).\((n >> 8) & 255).\(n & 255)"
}
