// Loft wire: mux frames, hello, status, and host:port targets.

import Foundation

/// Loft wire: mux frames, hello, status, and host:port targets.
enum LoftWire {
    static let proto: UInt16 = 0x0100
    static let protoMin: UInt16 = 0x0100
    static let headerSize = 7
    static let maxPayload = 16 * 1024

    static let cmdOpen: UInt8 = 1
    static let cmdData: UInt8 = 2
    static let cmdClose: UInt8 = 3
    static let cmdPing: UInt8 = 4
    static let cmdPong: UInt8 = 5
    static let cmdPad: UInt8 = 6
    static let cmdReset: UInt8 = 7
    static let cmdDgram: UInt8 = 8

    static let capGET: UInt64 = 1 << 5
    static let capWS: UInt64 = 1 << 6
    static let capStatus: UInt64 = 1 << 7
    static let capTimers: UInt64 = 1 << 8
    static let capHelloCaps: UInt64 = 1 << 9
    static let capReset: UInt64 = 1 << 10
    static let capQUIC: UInt64 = 1 << 11
    static let capDgram: UInt64 = 1 << 12
    static let capClockless: UInt64 = 1 << 13
    static let capTicket: UInt64 = 1 << 14
    static let capDecoy: UInt64 = 1 << 15
    static let capH2: UInt64 = 1 << 16
    static let capH3: UInt64 = 1 << 17
    static let capMask: UInt64 = 1 << 18
    static let capRoster: UInt64 = 1 << 20
    static let capLists: UInt64 = 1 << 21
    static let capResume: UInt64 = 1 << 1

    static let clientCaps: UInt64 =
        capGET | capWS | capStatus | capTimers | capHelloCaps | capReset | capQUIC | capDgram |
        capClockless | capTicket | capDecoy | capH2 | capH3 | capMask | capRoster | capLists | capResume

    /// One mux frame: 2-byte length, cmd, stream id, payload.
    struct Frame {
        var cmd: UInt8
        var streamID: UInt32
        var payload: Data
        /// Encodes this frame; nil if the payload exceeds `maxPayload`.
        func encode() -> Data? {
            if payload.count > maxPayload { return nil }
            var b = Data()
            b.reserveCapacity(headerSize + payload.count)
            LoftWire.putU16(&b, UInt16(payload.count))
            b.append(cmd)
            LoftWire.putU32(&b, streamID)
            b.append(payload)
            return b
        }
    }

    /// Decodes one frame from the front of `buf`, plus bytes consumed.
    static func decodeFrame(_ buf: Data) -> (Frame, Int)? {
        if buf.count < headerSize { return nil }
        let n = Int(u16(buf, 0))
        if n > maxPayload { return nil }
        let need = headerSize + n
        if buf.count < need { return nil }
        let cmd = buf[2]
        let sid = u32(buf, 3)
        let payload = n > 0 ? buf.subdata(in: headerSize..<need) : Data()
        return (Frame(cmd: cmd, streamID: sid, payload: payload), need)
    }

    /// Concatenates encoded frames; nil if any payload is too large.
    static func encodeBatch(_ frames: [Frame]) -> Data? {
        var out = Data()
        for f in frames {
            guard let b = f.encode() else { return nil }
            out.append(b)
        }
        return out
    }

    /// Decodes a complete batch; nil on a truncated or oversize frame.
    static func decodeBatch(_ buf: Data) -> [Frame]? {
        var frames: [Frame] = []
        var rest = buf
        while !rest.isEmpty {
            guard let (f, n) = decodeFrame(rest) else { return nil }
            frames.append(f)
            rest = rest.dropFirst(n)
        }
        return frames
    }

    /// Length-prefixed UTF-8 string, capped at 255 bytes.
    static func putStr(_ s: String) -> Data {
        let u = Array(s.utf8)
        let n = min(u.count, 255)
        var d = Data([UInt8(n)])
        d.append(contentsOf: u.prefix(n))
        return d
    }

    /// Client hello: ids, protocol range, caps, program, software.
    static func encodeHello(memberID: String, instanceID: String, program: String, software: String, caps: UInt64) -> Data {
        let m = Array(memberID.utf8)
        let i = Array(instanceID.utf8)
        var b = Data()
        b.append(1)
        b.append(UInt8(min(m.count, 255)))
        b.append(contentsOf: m.prefix(255))
        b.append(UInt8(min(i.count, 255)))
        b.append(contentsOf: i.prefix(255))
        putU16(&b, protoMin)
        putU16(&b, proto)
        putU64(&b, caps)
        b.append(putStr(program))
        b.append(putStr(software))
        return b
    }

    /// Node status after handshake: overlay IP, MTU, timers, ticket.
    struct Status {
        var code: UInt16 = 0
        var ip = ""
        var gateway = ""
        var instanceID = ""
        var mtu: UInt16 = 0
        var leaseSec: UInt32 = 0
        var keepaliveMS: UInt32 = 0
        var idleMS: UInt32 = 0
        var ticket = Data()
        var ok: Bool { code == 0x0000 || code == 0x0001 }
    }

    /// Parses a status blob. `ok` is true for 0x0000 / 0x0001.
    static func decodeStatus(_ b: Data) -> Status? {
        if b.count < 28 + 3 { return nil }
        var s = Status()
        s.code = u16(b, 0)
        s.leaseSec = u32(b, 2)
        s.mtu = u16(b, 6)
        s.keepaliveMS = u32(b, 10)
        s.idleMS = u32(b, 14)
        var off = 28
        guard let ip = takeStr(b, &off) else { return nil }
        guard let gw = takeStr(b, &off) else { return nil }
        guard let inst = takeStr(b, &off) else { return nil }
        s.ip = ip; s.gateway = gw; s.instanceID = inst
        if off + 4 <= b.count { off += 4 }
        if off + 14 <= b.count { off += 14 }
        if off < b.count {
            let (_, n1) = getStr(b, off); off += n1
            let (_, n2) = getStr(b, off); off += n2
            if off + 2 <= b.count {
                let ln = Int(u16(b, off)); off += 2
                if ln > 0, off + ln <= b.count {
                    s.ticket = b.subdata(in: off..<(off + ln))
                }
            }
        }
        return s
    }

    /// Host, port, and bytes consumed from a target payload.
    static func decodeTarget(_ b: Data) -> (String, UInt16, Int)? {
        if b.count < 4 { return nil }
        let n = Int(u16(b, 0))
        if b.count < 2 + n + 2 { return nil }
        let host = String(data: b.subdata(in: 2..<(2 + n)), encoding: .utf8) ?? ""
        let port = u16(b, 2 + n)
        return (host, port, 2 + n + 2)
    }

    /// Length-prefixed host plus big-endian port.
    static func encodeTarget(host: String, port: UInt16) -> Data {
        let h = Array(host.utf8)
        var b = Data()
        putU16(&b, UInt16(h.count))
        b.append(contentsOf: h)
        putU16(&b, port)
        return b
    }

    /// Appends `v` as big-endian u16.
    static func putU16(_ b: inout Data, _ v: UInt16) {
        b.append(UInt8(v >> 8))
        b.append(UInt8(v & 0xff))
    }
    /// Appends `v` as big-endian u32.
    static func putU32(_ b: inout Data, _ v: UInt32) {
        b.append(UInt8(v >> 24))
        b.append(UInt8((v >> 16) & 0xff))
        b.append(UInt8((v >> 8) & 0xff))
        b.append(UInt8(v & 0xff))
    }
    /// Appends `v` as big-endian u64.
    static func putU64(_ b: inout Data, _ v: UInt64) {
        b.append(UInt8(v >> 56))
        b.append(UInt8((v >> 48) & 0xff))
        b.append(UInt8((v >> 40) & 0xff))
        b.append(UInt8((v >> 32) & 0xff))
        b.append(UInt8((v >> 24) & 0xff))
        b.append(UInt8((v >> 16) & 0xff))
        b.append(UInt8((v >> 8) & 0xff))
        b.append(UInt8(v & 0xff))
    }
    /// Big-endian u16 at offset `o`.
    static func u16(_ b: Data, _ o: Int) -> UInt16 {
        UInt16(b[o]) << 8 | UInt16(b[o + 1])
    }
    /// Big-endian u32 at offset `o`.
    static func u32(_ b: Data, _ o: Int) -> UInt32 {
        UInt32(b[o]) << 24 | UInt32(b[o + 1]) << 16 | UInt32(b[o + 2]) << 8 | UInt32(b[o + 3])
    }
    /// Reads a length-prefixed string and advances `off`.
    static func takeStr(_ b: Data, _ off: inout Int) -> String? {
        if off >= b.count { return nil }
        let n = Int(b[off]); off += 1
        if off + n > b.count { return nil }
        let s = String(data: b.subdata(in: off..<(off + n)), encoding: .utf8) ?? ""
        off += n
        return s
    }
    /// Length-prefixed string at `off` without mutating the offset.
    static func getStr(_ b: Data, _ off: Int) -> (String, Int) {
        if off >= b.count { return ("", 0) }
        let n = Int(b[off])
        if off + 1 + n > b.count { return ("", 0) }
        let s = String(data: b.subdata(in: (off + 1)..<(off + 1 + n)), encoding: .utf8) ?? ""
        return (s, 1 + n)
    }

    /// Wire status/auth code name, or `0xNNNN`.
    static func codeName(_ c: UInt16) -> String {
        switch c {
        case 0x0000: return "ok"
        case 0x0001: return "ok_resumed"
        case 0x0101: return "auth_bad_key"
        case 0x0106: return "auth_already_online"
        case 0x0203: return "protocol"
        default: return String(format: "0x%04x", c)
        }
    }
}
