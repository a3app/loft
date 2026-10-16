// In-memory journal with optional rotating diag file under ~/Library/Logs/loft.

import Foundation

extension Notification.Name {
    /// Posted on the main queue after a journal line is added or the log is cleared.
    static let loftLog = Notification.Name("loft.log")
}

/// In-memory journal with optional rotating diag file under ~/Library/Logs/loft.
final class LoftLog {
    static let shared = LoftLog()
    private let lock = NSLock()
    private var lines: [String] = []
    private let cap = 500
    private let diagCap = 4000
    private let io = DispatchQueue(label: "app.loft.diag")
    private let maxFile = 1_500_000
    private let keepFiles = 3

    var diag = false {
        didSet {
            if oldValue == diag { return }
            if diag {
                add("loft", "diag on \(fileURL.path)")
                snapshot("enable")
            } else {
                add("loft", "diag off")
            }
        }
    }

    var isEmpty: Bool {
        lock.lock(); defer { lock.unlock() }
        return lines.isEmpty
    }

    var dirURL: URL {
        FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Logs/loft", isDirectory: true)
    }

    var fileURL: URL { dirURL.appendingPathComponent("diag.log") }

    /// Button tap. Journal + diag.log only while extended log is on.
    func tap(_ where_: String, _ title: String) {
        guard diag else { return }
        add("ui", "tap \(where_) \(title)")
    }

    /// Appends a redacted `src` line; writes the file when diag is on.
    func add(_ src: String, _ text: String) {
        let t = LoftLog.redact(text.trimmingCharacters(in: .whitespacesAndNewlines))
        if t.isEmpty { return }
        let line = "\(LoftLog.clock())\t\(src)\t\(t)"
        lock.lock()
        lines.append(line)
        let keep = diag ? diagCap : cap
        if lines.count > keep {
            lines.removeFirst(lines.count - keep)
        }
        let persist = diag
        lock.unlock()
        if persist { io.async { self.appendFile(line) } }
        DispatchQueue.main.async {
            NotificationCenter.default.post(name: .loftLog, object: self)
        }
    }

    /// Joined in-memory lines for the journal window and copy.
    func dump() -> String {
        lock.lock(); defer { lock.unlock() }
        return lines.joined(separator: "\n")
    }

    /// Drops in-memory lines and the diag file if diag is on.
    func clear() {
        lock.lock(); lines.removeAll(); lock.unlock()
        if diag {
            io.async {
                try? FileManager.default.removeItem(at: self.fileURL)
            }
        }
        DispatchQueue.main.async {
            NotificationCenter.default.post(name: .loftLog, object: self)
        }
    }

    /// Routes, DNS, orig-gw. No-op unless diag is on.
    func snapshot(_ tag: String) {
        guard diag else { return }
        add("diag", "--- \(tag) ---")
        for line in Priv.diagLines() {
            add("diag", line)
        }
    }

    /// Appends `line` to diag.log, rotating at 1.5 MB.
    private func appendFile(_ line: String) {
        let fm = FileManager.default
        try? fm.createDirectory(at: dirURL, withIntermediateDirectories: true)
        let url = fileURL
        if !fm.fileExists(atPath: url.path) {
            fm.createFile(atPath: url.path, contents: nil)
        }
        if let n = try? fm.attributesOfItem(atPath: url.path)[.size] as? NSNumber, n.intValue > maxFile {
            rotate()
            fm.createFile(atPath: url.path, contents: nil)
        }
        guard let h = try? FileHandle(forWritingTo: url) else { return }
        h.seekToEndOfFile()
        if let d = (line + "\n").data(using: .utf8) { h.write(d) }
        h.closeFile()
    }

    /// Shifts diag.log → diag.1.log … diag.N.log and drops the oldest.
    private func rotate() {
        let fm = FileManager.default
        let last = dirURL.appendingPathComponent("diag.\(keepFiles - 1).log")
        try? fm.removeItem(at: last)
        var i = keepFiles - 1
        while i >= 1 {
            let dst = dirURL.appendingPathComponent("diag.\(i).log")
            let src = i == 1 ? fileURL : dirURL.appendingPathComponent("diag.\(i - 1).log")
            if fm.fileExists(atPath: src.path) {
                try? fm.removeItem(at: dst)
                try? fm.moveItem(at: src, to: dst)
            }
            i -= 1
        }
    }

    /// Local `HH:mm:ss` prefix for a journal line.
    private static func clock() -> String {
        let c = Calendar.current
        let d = Date()
        return String(format: "%02d:%02d:%02d",
                      c.component(.hour, from: d),
                      c.component(.minute, from: d),
                      c.component(.second, from: d))
    }

    /// Replaces `lt_` plus 8+ hex chars with `lt_…`.
    static func redact(_ s: String) -> String {
        var out = s
        if let r = out.range(of: "lt_") {
            var i = r.upperBound
            var n = 0
            while i < out.endIndex, n < 64 {
                let ch = out[i]
                if (ch >= "0" && ch <= "9") || (ch >= "a" && ch <= "f") || (ch >= "A" && ch <= "F") {
                    n += 1
                    i = out.index(after: i)
                } else {
                    break
                }
            }
            if n >= 8 {
                out.replaceSubrange(r.lowerBound..<i, with: "lt_…")
            }
        }
        return out
    }
}
