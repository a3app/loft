// Split-routing lists: Keenetic / loft-routes files, local store, and a git raw repo.

import Foundation

/// One named CIDR set: through loft, or past loft on the remembered gateway.
struct SplitSet {
    var id: String
    var name: String
    var via: Bool
    var cidrs: [String]
    var source: SplitSource
}

/// Where a set came from. Repo updates leave `me` rows in place.
enum SplitSource: String {
    case me
    case repo
}

/// Parsed Keenetic / loft-routes file.
struct SplitFile {
    var loftHeader: Bool
    /// Nil when the file has no `| via` / `| bypass` and no `REM list:`.
    var defaultVia: Bool?
    var sets: [SplitSet]
    var lineCount: Int

    var mixed: Bool {
        let v = sets.contains { $0.via }
        let b = sets.contains { !$0.via }
        return v && b
    }

    var needsWhere: Bool { defaultVia == nil && !mixed && !sets.isEmpty }
}

/// Local split lists plus the optional raw-.bat repository.
final class SplitStore {
    var sets: [SplitSet] = []
    var repoURL = ""
    var repoVia = true
    var repoAuto = true
    var repoLast = ""
    var repoAt: Date?
    var repoCount = 0

    private var dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("loft", isDirectory: true)

    /// Sets through loft.
    func viaSets() -> [SplitSet] { sets.filter { $0.via } }
    /// Sets past loft.
    func bypassSets() -> [SplitSet] { sets.filter { !$0.via } }

    func cidrCount(_ via: Bool) -> Int {
        (via ? viaSets() : bypassSets()).reduce(0) { $0 + $1.cidrs.count }
    }

    /// Loads `routes.txt` from Application Support/loft.
    func load(dir: URL) {
        self.dir = dir
        let u = dir.appendingPathComponent("routes.txt")
        guard let s = try? String(contentsOf: u, encoding: .utf8) else { return }
        parseStore(s)
    }

    /// Writes `routes.txt`.
    func save() {
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try? emitStore().write(to: dir.appendingPathComponent("routes.txt"), atomically: true, encoding: .utf8)
    }

    /// Adds or merges `incoming`. Repo rows replace previous repo rows when `asRepo`.
    func ingest(_ incoming: [SplitSet], asRepo: Bool) {
        var incoming = incoming
        if asRepo {
            sets.removeAll { $0.source == .repo }
            incoming = incoming.map {
                var s = $0
                s.source = .repo
                return s
            }
        }
        for s in incoming where !s.cidrs.isEmpty {
            merge(s)
        }
        save()
    }

    /// Inserts `s`, merging CIDRs into an existing same-name same-direction row.
    func merge(_ s: SplitSet) {
        if let i = sets.firstIndex(where: { $0.name.caseInsensitiveCompare(s.name) == .orderedSame && $0.via == s.via }) {
            var cur = sets[i]
            var seen = Set(cur.cidrs)
            for c in s.cidrs where seen.insert(c).inserted { cur.cidrs.append(c) }
            if cur.source != .me { cur.source = s.source }
            sets[i] = cur
            return
        }
        var s = s
        if s.id.isEmpty { s.id = uniqueID(s.name) }
        sets.append(s)
    }

    /// Replaces the set with `id`, or appends when missing.
    func upsert(_ s: SplitSet) {
        if let i = sets.firstIndex(where: { $0.id == s.id }) {
            sets[i] = s
        } else {
            var s = s
            if s.id.isEmpty { s.id = uniqueID(s.name) }
            sets.append(s)
        }
        save()
    }

    func remove(id: String) {
        sets.removeAll { $0.id == id }
        save()
    }

    /// Pulls `repoURL` on a background queue. `done` runs on the main queue.
    func pullRepo(done: ((String?) -> Void)?) {
        let raw = repoURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: raw), let sch = url.scheme?.lowercased(), sch == "http" || sch == "https" else {
            done?(L10n.t("split_repo_none"))
            return
        }
        let req = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 30)
        URLSession.shared.dataTask(with: req) { [weak self] data, _, err in
            DispatchQueue.main.async {
                guard let self = self else { return }
                if err != nil {
                    done?(L10n.t("split_repo_fail"))
                    return
                }
                let text = String(data: data ?? Data(), encoding: .utf8)
                    ?? String(data: data ?? Data(), encoding: .windowsCP1251)
                    ?? ""
                let file = SplitBat.parse(text)
                if file.sets.isEmpty {
                    done?(L10n.t("split_import_none"))
                    return
                }
                var rows = file.sets
                if file.needsWhere {
                    for i in rows.indices { rows[i].via = self.repoVia }
                }
                self.ingest(rows, asRepo: true)
                self.repoCount = rows.reduce(0) { $0 + $1.cidrs.count }
                self.repoAt = Date()
                self.repoLast = SplitStore.stamp(self.repoCount)
                self.save()
                done?(nil)
            }
        }.resume()
    }

    /// Daily auto-pull when `repoAuto` is on and a URL is set.
    func maybePull() {
        guard repoAuto, !repoURL.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        if let t = repoAt, Date().timeIntervalSince(t) < 20 * 60 * 60 { return }
        pullRepo(done: nil)
    }

    /// Sample rows for `--shot`.
    func seedShot() {
        sets = [
            SplitSet(id: "yt", name: "YouTube", via: true, cidrs: ["142.250.0.0/16", "172.217.0.0/16", "74.125.0.0/16"], source: .repo),
            SplitSet(id: "cf", name: "Cloudflare", via: true, cidrs: ["1.1.1.0/24", "104.16.0.0/13"], source: .repo),
            SplitSet(id: "gpt", name: "ChatGPT", via: true, cidrs: ["104.18.32.0/24"], source: .me),
            SplitSet(id: "gos", name: "Госуслуги", via: false, cidrs: ["91.226.245.0/24", "5.61.16.0/21"], source: .me),
            SplitSet(id: "lan", name: "Локальная сеть", via: false, cidrs: ["10.0.0.0/8", "192.168.0.0/16", "172.16.0.0/12"], source: .me),
        ]
        repoURL = "https://raw.githubusercontent.com/a3app/loft-routes/main/via.bat"
        repoVia = true
        repoAuto = true
        repoCount = 214
        repoAt = Date()
        repoLast = SplitStore.stamp(214)
    }

    private func uniqueID(_ name: String) -> String {
        var base = name.lowercased().replacingOccurrences(of: " ", with: "-")
        let extra = CharacterSet(charactersIn: "-_")
        base = String(base.unicodeScalars.filter { CharacterSet.alphanumerics.contains($0) || extra.contains($0) })
        if base.isEmpty { base = "net" }
        var id = base
        var n = 2
        while sets.contains(where: { $0.id == id }) {
            id = "\(base)-\(n)"
            n += 1
        }
        return id
    }

    static func stamp(_ n: Int) -> String {
        let day = DateFormatter()
        day.locale = Locale(identifier: L10n.lang.localeID)
        day.dateStyle = .medium
        day.timeStyle = .none
        return "\(day.string(from: Date())) · \(n)"
    }

    private func parseStore(_ s: String) {
        sets = []
        var cur: SplitSet?
        /// Flushes `cur` into `sets`.
        func flush() {
            if let c = cur, !c.cidrs.isEmpty { sets.append(c) }
            cur = nil
        }
        for raw in s.components(separatedBy: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.isEmpty || line.hasPrefix("#") { continue }
            if line.lowercased().hasPrefix("repo_url:") {
                repoURL = String(line.dropFirst(9)).trimmingCharacters(in: .whitespaces)
                continue
            }
            if line.lowercased().hasPrefix("repo_via:") {
                repoVia = line.lowercased().contains("true")
                continue
            }
            if line.lowercased().hasPrefix("repo_auto:") {
                repoAuto = !line.lowercased().contains("false")
                continue
            }
            if line.lowercased().hasPrefix("repo_last:") {
                repoLast = String(line.dropFirst(10)).trimmingCharacters(in: .whitespaces)
                continue
            }
            if line.lowercased().hasPrefix("repo_count:") {
                repoCount = Int(String(line.dropFirst(11)).trimmingCharacters(in: .whitespaces)) ?? 0
                continue
            }
            if line.lowercased().hasPrefix("repo_at:") {
                let t = String(line.dropFirst(8)).trimmingCharacters(in: .whitespaces)
                if let v = Double(t) { repoAt = Date(timeIntervalSince1970: v) }
                continue
            }
            if line.lowercased().hasPrefix("set:") {
                flush()
                let id = String(line.dropFirst(4)).trimmingCharacters(in: .whitespaces)
                cur = SplitSet(id: id.isEmpty ? "net" : id, name: id, via: true, cidrs: [], source: .me)
                continue
            }
            guard var c = cur else { continue }
            if line.lowercased().hasPrefix("name:") {
                c.name = String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces)
            } else if line.lowercased().hasPrefix("via:") {
                c.via = line.lowercased().contains("true")
            } else if line.lowercased().hasPrefix("src:") {
                let v = String(line.dropFirst(4)).trimmingCharacters(in: .whitespaces).lowercased()
                c.source = v == "repo" ? .repo : .me
            } else if line.lowercased().hasPrefix("cidr:") {
                if let n = SplitBat.normalizeCIDR(String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces)) {
                    c.cidrs.append(n)
                }
            }
            cur = c
        }
        flush()
    }

    private func emitStore() -> String {
        var out = """
        repo_url: \(repoURL)
        repo_via: \(repoVia)
        repo_auto: \(repoAuto)
        repo_last: \(repoLast)
        repo_count: \(repoCount)
        repo_at: \(repoAt.map { String($0.timeIntervalSince1970) } ?? "")

        """
        for s in sets {
            out += "set: \(s.id)\n"
            out += "name: \(s.name)\n"
            out += "via: \(s.via)\n"
            out += "src: \(s.source.rawValue)\n"
            for c in s.cidrs { out += "cidr: \(c)\n" }
            out += "\n"
        }
        return out
    }
}

/// Keenetic `route ADD` and loft-routes 1.0.
enum SplitBat {
    /// Parses a `.bat` / `.txt` / `.loft-routes` body.
    static func parse(_ text: String) -> SplitFile {
        var loftHeader = false
        var listVia: Bool?
        var lineCount = 0
        var rows: [(name: String, via: Bool?, cidr: String)] = []
        for raw in text.components(separatedBy: .newlines) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.isEmpty { continue }
            let up = line.uppercased()
            if up.hasPrefix("REM ") || up.hasPrefix("::") {
                let rest = String(line.drop(while: { $0 == ":" || $0 == " " }))
                let body = rest.lowercased()
                if body.contains("loft-routes") { loftHeader = true }
                if let d = direction(in: body, key: "list:") { listVia = d }
                continue
            }
            if up.hasPrefix("@ECHO") { continue }
            guard let row = parseRoute(line) else { continue }
            lineCount += 1
            rows.append(row)
        }
        var buckets: [String: SplitSet] = [:]
        var order: [String] = []
        for r in rows {
            let via = r.via ?? listVia ?? true
            let name = r.name.isEmpty ? r.cidr : r.name
            let key = (via ? "v:" : "b:") + name.lowercased()
            if buckets[key] == nil {
                order.append(key)
                buckets[key] = SplitSet(id: "", name: name, via: via, cidrs: [], source: .me)
            }
            buckets[key]?.cidrs.append(r.cidr)
        }
        var sets: [SplitSet] = []
        for k in order {
            if var s = buckets[k] {
                s.id = ""
                sets.append(s)
            }
        }
        let defaultVia: Bool?
        if rows.contains(where: { $0.via != nil }) {
            defaultVia = listVia
        } else {
            defaultVia = listVia
        }
        return SplitFile(loftHeader: loftHeader, defaultVia: defaultVia, sets: sets, lineCount: lineCount)
    }

    /// loft-routes (both lists) or Keenetic (one list, no `| via`).
    static func emit(_ sets: [SplitSet], loft: Bool) -> String {
        var out = "@echo off\r\n"
        if loft {
            out += "REM loft-routes 1.0\r\nREM\r\n"
        }
        for s in sets {
            for c in s.cidrs {
                guard let pair = splitCIDR(c) else { continue }
                var rem = s.name
                if loft { rem += s.via ? " | via" : " | bypass" }
                out += "route ADD \(pair.ip) MASK \(pair.mask) 0.0.0.0"
                if !rem.isEmpty { out += " :: rem \(rem)" }
                out += "\r\n"
            }
        }
        return out
    }

    /// `1.1.1.1` → `1.1.1.1/32`; `10.0.0.0/8` stays. Nil if garbage.
    static func normalizeCIDR(_ raw: String) -> String? {
        let t = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { return nil }
        let parts = t.split(separator: "/", maxSplits: 1).map(String.init)
        guard let ip = ipv4(parts[0]) else { return nil }
        if parts.count == 1 { return ip + "/32" }
        guard let n = Int(parts[1]), n >= 0, n <= 32 else { return nil }
        return "\(ip)/\(n)"
    }

    private static func parseRoute(_ line: String) -> (name: String, via: Bool?, cidr: String)? {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        let up = trimmed.uppercased()
        guard up.hasPrefix("ROUTE") else { return nil }
        var rest = String(trimmed.dropFirst(5)).trimmingCharacters(in: .whitespaces)
        if rest.uppercased().hasPrefix("ADD") {
            rest = String(rest.dropFirst(3)).trimmingCharacters(in: .whitespaces)
        }
        var comment = ""
        if let r = remRange(rest) {
            comment = String(rest[r.upperBound...]).trimmingCharacters(in: .whitespaces)
            rest = String(rest[..<r.lowerBound]).trimmingCharacters(in: .whitespaces)
        }
        let toks = rest.split(whereSeparator: { $0 == " " || $0 == "\t" }).map(String.init)
        guard let dest = toks.first else { return nil }
        var cidr: String?
        if dest.contains("/") {
            cidr = normalizeCIDR(dest)
        } else if let ip = ipv4(dest) {
            var mask = "255.255.255.255"
            if let i = toks.firstIndex(where: { $0.uppercased() == "MASK" }), i + 1 < toks.count {
                mask = toks[i + 1]
            }
            if let bits = prefix(fromMask: mask) {
                cidr = "\(ip)/\(bits)"
            }
        }
        guard let c = cidr else { return nil }
        let (name, via) = nameAndDir(comment)
        return (name, via, c)
    }

    private static func remRange(_ s: String) -> Range<String.Index>? {
        let u = s.lowercased()
        if let r = u.range(of: ":: rem") { return r }
        if let r = u.range(of: "::rem") { return r }
        if let r = u.range(of: "& rem") { return r }
        if let r = u.range(of: "&rem") { return r }
        return nil
    }

    private static func nameAndDir(_ comment: String) -> (String, Bool?) {
        let t = comment.trimmingCharacters(in: .whitespaces)
        if t.isEmpty { return ("", nil) }
        if let bar = t.range(of: "|", options: .backwards) {
            let name = t[..<bar.lowerBound].trimmingCharacters(in: .whitespaces)
            let dir = t[bar.upperBound...].trimmingCharacters(in: .whitespaces).lowercased()
            return (name, directionWord(dir))
        }
        return (t, nil)
    }

    private static func direction(in body: String, key: String) -> Bool? {
        guard let r = body.range(of: key) else { return nil }
        return directionWord(String(body[r.upperBound...]))
    }

    private static func directionWord(_ s: String) -> Bool? {
        let w = s.trimmingCharacters(in: .whitespaces).lowercased()
        if w.hasPrefix("via") || w.hasPrefix("loft") { return true }
        if w.hasPrefix("bypass") || w.hasPrefix("direct") || w.hasPrefix("past") { return false }
        return nil
    }

    private static func ipv4(_ s: String) -> String? {
        let oct = s.split(separator: ".", omittingEmptySubsequences: false)
        guard oct.count == 4 else { return nil }
        var out: [String] = []
        for p in oct {
            guard let n = Int(p), n >= 0, n <= 255 else { return nil }
            out.append(String(n))
        }
        return out.joined(separator: ".")
    }

    private static func prefix(fromMask mask: String) -> Int? {
        let oct = mask.split(separator: ".").compactMap { UInt32($0) }
        guard oct.count == 4 else { return nil }
        var n = (oct[0] << 24) | (oct[1] << 16) | (oct[2] << 8) | oct[3]
        var bits = 0
        while n & 0x8000_0000 != 0 {
            bits += 1
            n <<= 1
        }
        return n == 0 ? bits : nil
    }

    private static func splitCIDR(_ c: String) -> (ip: String, mask: String)? {
        let p = c.split(separator: "/", maxSplits: 1).map(String.init)
        guard let ip = ipv4(p[0]) else { return nil }
        let bits = p.count == 2 ? (Int(p[1]) ?? 32) : 32
        guard bits >= 0, bits <= 32 else { return nil }
        let n: UInt32 = bits == 0 ? 0 : ~UInt32(0) << (32 - bits)
        let mask = "\((n >> 24) & 255).\((n >> 16) & 255).\((n >> 8) & 255).\(n & 255)"
        return (ip, mask)
    }
}
