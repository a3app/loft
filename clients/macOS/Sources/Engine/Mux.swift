// In-process mux: TCP streams and datagrams over loft wire frames.

import Foundation

/// One mux TCP stream: blocking read queue and write/close via `LoftMux`.
final class LoftStream: NSObject {
    let id: UInt32
    private weak var session: LoftMux?
    private let lock = NSCondition()
    private var incoming = Data()
    private var closed = false

    /// Stream `id` owned by `session`.
    init(id: UInt32, session: LoftMux) {
        self.id = id
        self.session = session
    }

    /// Enqueues inbound bytes and wakes a blocked `read`.
    func push(_ d: Data) {
        lock.lock()
        incoming.append(d)
        lock.broadcast()
        lock.unlock()
    }

    /// Marks the stream closed and wakes a blocked `read`.
    func markClosed() {
        lock.lock()
        closed = true
        lock.broadcast()
        lock.unlock()
    }

    /// Blocks until up to `max` bytes arrive, or nil when closed and empty.
    func read(_ max: Int) -> Data? {
        lock.lock()
        while incoming.isEmpty && !closed { lock.wait() }
        if incoming.isEmpty { lock.unlock(); return nil }
        let n = min(max, incoming.count)
        let out = incoming.prefix(n)
        incoming.removeFirst(n)
        lock.unlock()
        return Data(out)
    }

    /// Sends a data frame. False if the mux is already closed.
    func write(_ d: Data) -> Bool {
        return session?.send(LoftWire.Frame(cmd: LoftWire.cmdData, streamID: id, payload: d)) ?? false
    }

    /// Sends a close frame and marks the stream closed.
    func close() {
        _ = session?.send(LoftWire.Frame(cmd: LoftWire.cmdClose, streamID: id, payload: Data()))
        markClosed()
    }
}

/// Multiplexes TCP streams and datagrams onto one loft session.
final class LoftMux {
    private let lock = NSLock()
    private var streams: [UInt32: LoftStream] = [:]
    private var outq: [LoftWire.Frame] = []
    private let outCond = NSCondition()
    private var nextID: UInt32 = 1
    private var closed = false
    private(set) var bytesUp: Int64 = 0
    private(set) var bytesDown: Int64 = 0
    var onDgram: ((String, UInt16, Data) -> Void)?

    /// Allocates an odd stream id and sends OPEN for `host:port`.
    func open(host: String, port: UInt16) -> LoftStream? {
        lock.lock()
        let id = nextID
        nextID &+= 2
        let st = LoftStream(id: id, session: self)
        streams[id] = st
        lock.unlock()
        let payload = LoftWire.encodeTarget(host: host, port: port)
        if !send(LoftWire.Frame(cmd: LoftWire.cmdOpen, streamID: id, payload: payload)) {
            return nil
        }
        return st
    }

    /// Sends a datagram, truncated to `maxPayload` minus the target header.
    func writeDgram(host: String, port: UInt16, data: Data) -> Bool {
        var pl = LoftWire.encodeTarget(host: host, port: port)
        let room = LoftWire.maxPayload - pl.count
        if room <= 0 { return false }
        if data.count > room {
            pl.append(data.prefix(room))
        } else {
            pl.append(data)
        }
        return send(LoftWire.Frame(cmd: LoftWire.cmdDgram, streamID: 0, payload: pl))
    }

    /// Enqueues `f` for the next uplink pull.
    func send(_ f: LoftWire.Frame) -> Bool {
        outCond.lock()
        if closed { outCond.unlock(); return false }
        outq.append(f)
        bytesUp += Int64(f.payload.count)
        outCond.broadcast()
        outCond.unlock()
        return true
    }

    /// Waits up to `wait` and returns frames totaling about `budget` bytes.
    func pull(budget: Int, wait: TimeInterval) -> [LoftWire.Frame] {
        outCond.lock()
        if outq.isEmpty {
            _ = outCond.wait(until: Date().addingTimeInterval(wait))
        }
        var out: [LoftWire.Frame] = []
        var used = 0
        while !outq.isEmpty {
            let f = outq[0]
            let n = 7 + f.payload.count
            if !out.isEmpty && used + n > budget { break }
            out.append(outq.removeFirst())
            used += n
            if used >= budget { break }
        }
        outCond.unlock()
        return out
    }

    /// Dispatches inbound frames to streams, pong, or `onDgram`.
    func push(_ frames: [LoftWire.Frame]) {
        for f in frames {
            switch f.cmd {
            case LoftWire.cmdData:
                lock.lock(); let st = streams[f.streamID]; lock.unlock()
                bytesDown += Int64(f.payload.count)
                st?.push(f.payload)
            case LoftWire.cmdClose, LoftWire.cmdReset:
                lock.lock(); let st = streams[f.streamID]; lock.unlock()
                st?.markClosed()
            case LoftWire.cmdPing:
                _ = send(LoftWire.Frame(cmd: LoftWire.cmdPong, streamID: f.streamID, payload: f.payload))
            case LoftWire.cmdDgram:
                if let (host, port, n) = LoftWire.decodeTarget(f.payload) {
                    let rest = n < f.payload.count ? f.payload.subdata(in: n..<f.payload.count) : Data()
                    onDgram?(host, port, rest)
                }
            default:
                break
            }
        }
    }

    /// Closes the outbound queue and every live stream.
    func close() {
        outCond.lock(); closed = true; outCond.broadcast(); outCond.unlock()
        lock.lock(); let all = Array(streams.values); lock.unlock()
        for s in all { s.markClosed() }
    }
}
