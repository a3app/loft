// Split-routing lists: Keenetic / loft-routes files, local store, and a git raw repo.

import Foundation

/// One named CIDR set. Direction comes from the zone, not from the row.
struct SplitSet {
    var id: String
    var name: String
    var zone: String
    var cidrs: [String]
    var source: SplitSource
}

/// Policy bag: through loft or around loft, plus its own list URL.
struct SplitZone {
    var id: String
    var name: String
    var builtin: Bool
    /// True = through loft, false = around loft.
    var def: Bool
    var repo: String
    var auto: Bool
    var repoLast: String
    var repoAt: Date?
    var repoCount: Int

    static func local() -> SplitZone {
        SplitZone(id: "local", name: "Local", builtin: true, def: false, repo: "", auto: true, repoLast: "", repoAt: nil, repoCount: 0)
    }

    static func global() -> SplitZone {
        SplitZone(id: "global", name: "Global", builtin: true, def: true, repo: SplitListCatalog.rawBase + "global.bat", auto: true, repoLast: "", repoAt: nil, repoCount: 0)
    }
}

/// Where a set came from. Repo updates leave `me` and `ignore` rows in place.
enum SplitSource: String {
    case me
    case repo
    /// CIDRs that stay out of the kernel table and out of imported rows.
    case ignore
}

/// Parsed Keenetic / loft-routes file.
struct SplitFile {
    var loftHeader: Bool
    var sets: [SplitSet]
    var lineCount: Int
    /// True when no `REM zone:` and no `| via` / `| bypass` — import asks which zone.
    var needsWhere: Bool
}

/// One published list from loft-routes `catalog.json`.
struct SplitListEntry {
    var id: String
    var file: String
    var names: [String: String]

    var url: String { SplitListCatalog.rawBase + file }

    /// Localized title; English, then Russian, then the file name.
    func title() -> String {
        let lang = L10n.lang.rawValue
        return names[lang] ?? names["en"] ?? names["ru"] ?? file
    }
}

/// Catalog of named `.bat` files in a3app/loft-routes.
enum SplitListCatalog {
    static let rawBase = "https://raw.githubusercontent.com/a3app/loft-routes/main/"
    static let catalogURL = rawBase + "catalog.json"
    static let legacyPrefix = "https://raw.githubusercontent.com/a3app/loft/main/lists/"

    /// Built-in index when the bundle and the network both miss.
    static let fallback: [SplitListEntry] = [
        SplitListEntry(id: "global", file: "global.bat", names: [
            "en": "World", "ru": "Мир", "de": "Welt", "fr": "Monde", "pt": "Mundo",
            "zh": "全球", "it": "Mondo", "ja": "世界", "ko": "세계", "vi": "Thế giới",
        ]),
        SplitListEntry(id: "ru", file: "ru.bat", names: [
            "en": "Russia", "ru": "Россия", "de": "Russland", "fr": "Russie", "pt": "Rússia",
            "zh": "俄罗斯", "it": "Russia", "ja": "ロシア", "ko": "러시아", "vi": "Nga",
        ]),
        SplitListEntry(id: "cn", file: "cn.bat", names: [
            "en": "China", "ru": "Китай", "de": "China", "fr": "Chine", "pt": "China",
            "zh": "中国", "it": "Cina", "ja": "中国", "ko": "중국", "vi": "Trung Quốc",
        ]),
        SplitListEntry(id: "ir", file: "ir.bat", names: [
            "en": "Iran", "ru": "Иран", "de": "Iran", "fr": "Iran", "pt": "Irã",
            "zh": "伊朗", "it": "Iran", "ja": "イラン", "ko": "이란", "vi": "Iran",
        ]),
        SplitListEntry(id: "kz", file: "kz.bat", names: [
            "en": "Kazakhstan", "ru": "Казахстан", "de": "Kasachstan", "fr": "Kazakhstan", "pt": "Cazaquistão",
            "zh": "哈萨克斯坦", "it": "Kazakistan", "ja": "カザフスタン", "ko": "카자흐스탄", "vi": "Kazakhstan",
        ]),
        SplitListEntry(id: "by", file: "by.bat", names: [
            "en": "Belarus", "ru": "Беларусь", "de": "Belarus", "fr": "Biélorussie", "pt": "Bielorrússia",
            "zh": "白俄罗斯", "it": "Bielorussia", "ja": "ベラルーシ", "ko": "벨라루스", "vi": "Belarus",
        ]),
    ]

    /// loft/lists/ URLs from 1.0.13 and the old `ru-ru.bat` name.
    static func rewriteLegacy(_ url: String) -> String {
        var u = url.trimmingCharacters(in: .whitespacesAndNewlines)
        if u.hasPrefix(legacyPrefix) {
            u = rawBase + String(u.dropFirst(legacyPrefix.count))
        }
        if u.hasSuffix("ru-ru.bat") {
            u = String(u.dropLast("ru-ru.bat".count)) + "ru.bat"
        }
        return u
    }

    /// Reads `lists/catalog.json` from the app bundle, else `fallback`.
    static func bundled() -> [SplitListEntry] {
        let bundle = Bundle.main
        let url = bundle.url(forResource: "catalog", withExtension: "json", subdirectory: "lists")
            ?? bundle.url(forResource: "catalog", withExtension: "json")
        guard let url = url, let data = try? Data(contentsOf: url) else { return fallback }
        return parse(data) ?? fallback
    }

    /// Parses `{ "lists": [ { "id", "file", lang… } ] }`.
    static func parse(_ data: Data) -> [SplitListEntry]? {
        guard let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let arr = obj["lists"] as? [[String: Any]] else { return nil }
        var out: [SplitListEntry] = []
        for item in arr {
            guard let id = item["id"] as? String, let file = item["file"] as? String, !file.isEmpty else { continue }
            var names: [String: String] = [:]
            for (k, v) in item {
                if k == "id" || k == "file" { continue }
                if let s = v as? String { names[k] = s }
            }
            out.append(SplitListEntry(id: id, file: file, names: names))
        }
        return out.isEmpty ? nil : out
    }

    /// GitHub raw catalog, falling back to the bundled index. `done` runs on the main queue.
    static func fetch(done: @escaping ([SplitListEntry]) -> Void) {
        let local = bundled()
        guard let url = URL(string: catalogURL) else {
            DispatchQueue.main.async { done(local) }
            return
        }
        let req = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 15)
        URLSession.shared.dataTask(with: req) { data, _, _ in
            DispatchQueue.main.async {
                if let data = data, let rows = parse(data) {
                    done(rows)
                } else {
                    done(local)
                }
            }
        }.resume()
    }
}

/// Local split lists plus per-zone repositories.
final class SplitStore {
    var zones: [SplitZone] = []
    var sets: [SplitSet] = []

    private var dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("loft", isDirectory: true)

    func zoneById(_ id: String) -> SplitZone? { zones.first { $0.id == id } }

    func zoneIndex(_ id: String) -> Int? { zones.firstIndex { $0.id == id } }

    /// Built-ins Local, Global, then any added zones.
    func zoneIds() -> [String] {
        var ids: [String] = []
        let add = { (id: String) in
            if !id.isEmpty, !ids.contains(id) { ids.append(id) }
        }
        add("local")
        add("global")
        for z in zones { add(z.id) }
        for s in sets { add(s.zone) }
        return ids
    }

    func zoneLabel(_ id: String) -> String {
        let k = "split.zone." + id
        let s = L10n.t(k)
        if s != k { return s }
        if let z = zoneById(id), !z.name.isEmpty { return z.name }
        return id.uppercased()
    }

    /// True = through loft.
    func zoneDef(_ id: String) -> Bool {
        if let z = zoneById(id) { return z.def }
        return id != "local"
    }

    func sets(in zone: String) -> [SplitSet] { sets.filter { $0.zone == zone } }

    /// Kernel bag: through loft. Derived from zone.def.
    func viaSets() -> [SplitSet] { sets.filter { zoneDef($0.zone) } }
    /// Kernel bag: around loft.
    func bypassSets() -> [SplitSet] { sets.filter { !zoneDef($0.zone) } }

    /// Called after a list or zone mutation so the kernel table can follow.
    var onChange: (() -> Void)?

    /// Unique IPv4 CIDRs in the through-loft or around-loft bag. Ignore rows stay out.
    func bagCIDRs(via: Bool) -> [String] {
        let src = via ? viaSets() : bypassSets()
        var ignores: [String: [String]] = [:]
        for s in src where s.source == .ignore {
            ignores[s.zone, default: []].append(contentsOf: s.cidrs.compactMap(SplitBat.normalizeCIDR))
        }
        var seen = Set<String>()
        var out: [String] = []
        for s in src where s.source != .ignore {
            let ig = ignores[s.zone] ?? []
            for raw in s.cidrs {
                guard let c = SplitBat.normalizeCIDR(raw) else { continue }
                if SplitBat.hitsIgnore(c, ig) { continue }
                if seen.insert(c).inserted { out.append(c) }
            }
        }
        return out
    }

    private func listsChanged() { onChange?() }

    func cidrCount(in zone: String) -> Int {
        sets(in: zone).reduce(0) { $0 + $1.cidrs.count }
    }

    /// Loads `routes.txt` from Application Support/loft.
    func load(dir: URL) {
        self.dir = dir
        let u = dir.appendingPathComponent("routes.txt")
        guard let s = try? String(contentsOf: u, encoding: .utf8) else {
            ensureBuiltins()
            seedDefaults()
            save()
            return
        }
        parseStore(s)
        ensureBuiltins()
        if migrateLegacyRepos() { save() }
    }

    /// Writes `routes.txt`.
    func save() {
        let body = emitStore()
        let dest = dir
        DispatchQueue.global(qos: .utility).async {
            try? FileManager.default.createDirectory(at: dest, withIntermediateDirectories: true)
            try? body.write(to: dest.appendingPathComponent("routes.txt"), atomically: true, encoding: .utf8)
        }
    }

    /// Adds or merges `incoming`. Repo rows of `zone` replace previous repo rows of that zone when `asRepo`.
    /// Ignore rows survive a repo pull; incoming `ignore` / `REM ignore` merge into the zone ignore set.
    func ingest(_ incoming: [SplitSet], asRepo: Bool, zone: String? = nil) {
        var incoming = incoming
        if let z = zone, !z.isEmpty {
            for i in incoming.indices { incoming[i].zone = z }
        }
        if asRepo {
            let zid = zone ?? ""
            if zid.isEmpty {
                sets.removeAll { $0.source == .repo }
            } else {
                sets.removeAll { $0.source == .repo && $0.zone == zid }
            }
        }
        for s in incoming where !s.cidrs.isEmpty {
            var s = s
            if s.zone.isEmpty { s.zone = "global" }
            if s.source == .ignore || s.name.lowercased() == "ignore" {
                rememberIgnore(cidrs: s.cidrs, zone: s.zone)
                continue
            }
            if asRepo { s.source = .repo }
            merge(s)
        }
        applyIgnores()
        save()
        listsChanged()
    }

    /// Inserts `s`, merging CIDRs into an existing same-name same-zone row. Ignore rows stay apart.
    func merge(_ s: SplitSet) {
        if s.source == .ignore {
            rememberIgnore(cidrs: s.cidrs, zone: s.zone)
            return
        }
        if let i = sets.firstIndex(where: {
            $0.source != .ignore
                && $0.name.caseInsensitiveCompare(s.name) == .orderedSame
                && $0.zone == s.zone
        }) {
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
        var s = s
        if s.source == .ignore, s.name.isEmpty { s.name = "ignore" }
        if let i = sets.firstIndex(where: { $0.id == s.id }) {
            sets[i] = s
        } else {
            if s.id.isEmpty { s.id = uniqueID(s.source == .ignore ? "ignore-\(s.zone)" : s.name) }
            sets.append(s)
        }
        applyIgnores()
        save()
        listsChanged()
    }

    func remove(id: String) {
        sets.removeAll { $0.id == id }
        save()
        listsChanged()
    }

    /// CIDRs that must not be installed in `zone`. Survives a repository pull.
    func rememberIgnore(cidrs: [String], zone: String) {
        let z = zone.isEmpty ? "global" : zone
        let cleaned = cidrs.compactMap(SplitBat.normalizeCIDR)
        guard !cleaned.isEmpty else { return }
        if let i = sets.firstIndex(where: { $0.source == .ignore && $0.zone == z && $0.name.lowercased() == "ignore" }) {
            var cur = sets[i]
            var seen = Set(cur.cidrs)
            for c in cleaned where seen.insert(c).inserted { cur.cidrs.append(c) }
            sets[i] = cur
        } else if let i = sets.firstIndex(where: { $0.source == .ignore && $0.zone == z }) {
            var cur = sets[i]
            var seen = Set(cur.cidrs)
            for c in cleaned where seen.insert(c).inserted { cur.cidrs.append(c) }
            sets[i] = cur
        } else {
            sets.append(SplitSet(id: uniqueID("ignore-\(z)"), name: "ignore", zone: z, cidrs: cleaned, source: .ignore))
        }
        applyIgnores()
    }

    /// Drops ignored CIDRs from route rows of the same zone. Empty route rows go away.
    func applyIgnores() {
        var byZone: [String: [String]] = [:]
        for s in sets where s.source == .ignore {
            byZone[s.zone, default: []].append(contentsOf: s.cidrs.compactMap(SplitBat.normalizeCIDR))
        }
        guard !byZone.isEmpty else { return }
        for i in sets.indices where sets[i].source != .ignore {
            let ig = byZone[sets[i].zone] ?? []
            if ig.isEmpty { continue }
            sets[i].cidrs.removeAll { c in
                guard let n = SplitBat.normalizeCIDR(c) else { return false }
                return SplitBat.hitsIgnore(n, ig)
            }
        }
        sets.removeAll { $0.source != .ignore && $0.cidrs.isEmpty }
    }

    func upsertZone(_ z: SplitZone) {
        if let i = zoneIndex(z.id) {
            zones[i] = z
        } else {
            zones.append(z)
        }
        save()
        listsChanged()
    }

    /// User zones only. Services move to Global.
    func deleteZone(_ id: String) {
        guard let z = zoneById(id), !z.builtin else { return }
        for i in sets.indices where sets[i].zone == id {
            sets[i].zone = "global"
        }
        zones.removeAll { $0.id == id }
        save()
        listsChanged()
    }

    func zoneSlug(_ name: String) -> String {
        let ascii = name.lowercased().folding(options: .diacriticInsensitive, locale: .current)
            .replacingOccurrences(of: "[^a-z0-9]+", with: "", options: .regularExpression)
        let base = String(ascii.prefix(16))
        let taken = { (id: String) in self.zones.contains { $0.id == id } }
        if !base.isEmpty {
            var id = base, n = 2
            while taken(id) { id = base + String(n); n += 1 }
            return id
        }
        var n = 1, id = "z1"
        while taken(id) { n += 1; id = "z\(n)" }
        return id
    }

    /// Pulls the zone's list URL. `done` runs on the main queue.
    func pullRepo(zone id: String, done: ((String?) -> Void)?) {
        guard let z = zoneById(id) else {
            done?(L10n.t("split_repo_none"))
            return
        }
        let raw = z.repo.trimmingCharacters(in: .whitespacesAndNewlines)
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
                self.ingest(file.sets, asRepo: true, zone: id)
                if let i = self.zoneIndex(id) {
                    let n = file.sets.reduce(0) { $0 + $1.cidrs.count }
                    self.zones[i].repoCount = n
                    self.zones[i].repoAt = Date()
                    self.zones[i].repoLast = SplitStore.stamp(n)
                    self.save()
                }
                self.listsChanged()
                done?(nil)
            }
        }.resume()
    }

    /// Daily auto-pull per zone when auto is on and a URL is set.
    func maybePull() {
        for z in zones {
            guard z.auto, !z.repo.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { continue }
            if let t = z.repoAt, Date().timeIntervalSince(t) < 20 * 60 * 60 { continue }
            pullRepo(zone: z.id, done: nil)
        }
    }

    /// Sample rows for `--shot`. Writes, if any, go under `/tmp` so Application Support stays put.
    func seedShot() {
        let tmp = URL(fileURLWithPath: "/tmp/loft-shot-routes", isDirectory: true)
        try? FileManager.default.createDirectory(at: tmp, withIntermediateDirectories: true)
        dir = tmp
        var global = SplitZone.global()
        global.repoCount = 214
        global.repoAt = Date()
        global.repoLast = SplitStore.stamp(214)
        zones = [SplitZone.local(), global]
        sets = [
            SplitSet(id: "yt", name: "YouTube", zone: "global", cidrs: ["142.250.0.0/16", "172.217.0.0/16", "74.125.0.0/16"], source: .repo),
            SplitSet(id: "cf", name: "Cloudflare", zone: "global", cidrs: ["1.1.1.0/24", "104.16.0.0/13"], source: .repo),
            SplitSet(id: "gpt", name: "ChatGPT", zone: "global", cidrs: ["104.18.32.0/24"], source: .me),
            SplitSet(id: "yandex", name: "Yandex", zone: "local", cidrs: ["5.255.255.0/24", "77.88.8.0/24"], source: .me),
            SplitSet(id: "lan", name: "LAN", zone: "local", cidrs: ["10.0.0.0/8", "192.168.0.0/16", "172.16.0.0/12"], source: .me),
            SplitSet(id: "ignore-local", name: "ignore", zone: "local", cidrs: ["104.16.0.0/12"], source: .ignore),
        ]
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

    private func ensureBuiltins() {
        if zoneById("local") == nil { zones.insert(SplitZone.local(), at: 0) }
        if zoneById("global") == nil {
            let g = SplitZone.global()
            if let i = zoneIndex("local") {
                zones.insert(g, at: i + 1)
            } else {
                zones.insert(g, at: 0)
            }
        }
        if let i = zoneIndex("local") { zones[i].builtin = true }
        if let i = zoneIndex("global") { zones[i].builtin = true }
    }

    private func migrateLegacyRepos() -> Bool {
        var changed = false
        for i in zones.indices {
            let next = SplitListCatalog.rewriteLegacy(zones[i].repo)
            if next != zones[i].repo {
                zones[i].repo = next
                changed = true
            }
        }
        return changed
    }

    private func seedDefaults() {
        if sets.isEmpty {
            sets.append(SplitSet(
                id: "lan",
                name: "LAN",
                zone: "local",
                cidrs: ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"],
                source: .me
            ))
        }
    }

    private func parseStore(_ s: String) {
        sets = []
        zones = []
        var cur: SplitSet?
        var curZ: SplitZone?
        var pendingVia: Bool?
        var legacyURL = ""
        var legacyVia = true
        var legacyAuto = true
        var legacyLast = ""
        var legacyCount = 0
        var legacyAt: Date?
        func flushSet() {
            if var c = cur, !c.cidrs.isEmpty {
                if c.zone.isEmpty { c.zone = pendingVia == false ? "local" : "global" }
                sets.append(c)
            }
            cur = nil
            pendingVia = nil
        }
        func flushZone() {
            if let z = curZ { zones.append(z) }
            curZ = nil
        }
        for raw in s.components(separatedBy: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.isEmpty || line.hasPrefix("#") { continue }
            let low = line.lowercased()
            if cur == nil && curZ == nil {
                if low.hasPrefix("repo_url:") {
                    legacyURL = String(line.dropFirst(9)).trimmingCharacters(in: .whitespaces)
                    continue
                }
                if low.hasPrefix("repo_via:") {
                    legacyVia = low.contains("true")
                    continue
                }
                if low.hasPrefix("repo_auto:") {
                    legacyAuto = !low.contains("false")
                    continue
                }
                if low.hasPrefix("repo_last:") {
                    legacyLast = String(line.dropFirst(10)).trimmingCharacters(in: .whitespaces)
                    continue
                }
                if low.hasPrefix("repo_count:") {
                    legacyCount = Int(String(line.dropFirst(11)).trimmingCharacters(in: .whitespaces)) ?? 0
                    continue
                }
                if low.hasPrefix("repo_at:") {
                    let t = String(line.dropFirst(8)).trimmingCharacters(in: .whitespaces)
                    if let v = Double(t) { legacyAt = Date(timeIntervalSince1970: v) }
                    continue
                }
            }
            if low.hasPrefix("set:") {
                flushZone()
                flushSet()
                let id = String(line.dropFirst(4)).trimmingCharacters(in: .whitespaces)
                cur = SplitSet(id: id.isEmpty ? "net" : id, name: id, zone: "", cidrs: [], source: .me)
                continue
            }
            if low.hasPrefix("zone:") && cur == nil {
                flushZone()
                let id = String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces)
                var z = id == "local" ? SplitZone.local() : (id == "global" ? SplitZone.global() : SplitZone(id: id.isEmpty ? "z" : id, name: id, builtin: false, def: true, repo: "", auto: true, repoLast: "", repoAt: nil, repoCount: 0))
                if id == "global" { z.repo = "" }
                z.id = id.isEmpty ? z.id : id
                curZ = z
                continue
            }
            if var c = cur {
                if low.hasPrefix("name:") {
                    c.name = String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces)
                } else if low.hasPrefix("via:") {
                    pendingVia = low.contains("true")
                } else if low.hasPrefix("zone:") {
                    c.zone = String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces)
                } else if low.hasPrefix("src:") {
                    let v = String(line.dropFirst(4)).trimmingCharacters(in: .whitespaces).lowercased()
                    c.source = SplitSource(rawValue: v) ?? .me
                } else if low.hasPrefix("cidr:") {
                    if let n = SplitBat.normalizeCIDR(String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces)) {
                        c.cidrs.append(n)
                    }
                }
                cur = c
                continue
            }
            if var z = curZ {
                if low.hasPrefix("name:") {
                    z.name = String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces)
                } else if low.hasPrefix("builtin:") {
                    z.builtin = low.contains("true")
                } else if low.hasPrefix("def:") {
                    z.def = low.contains("true")
                } else if low.hasPrefix("repo:") {
                    z.repo = SplitListCatalog.rewriteLegacy(String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces))
                } else if low.hasPrefix("auto:") {
                    z.auto = !low.contains("false")
                } else if low.hasPrefix("repo_last:") {
                    z.repoLast = String(line.dropFirst(10)).trimmingCharacters(in: .whitespaces)
                } else if low.hasPrefix("repo_count:") {
                    z.repoCount = Int(String(line.dropFirst(11)).trimmingCharacters(in: .whitespaces)) ?? 0
                } else if low.hasPrefix("repo_at:") {
                    let t = String(line.dropFirst(8)).trimmingCharacters(in: .whitespaces)
                    if let v = Double(t) { z.repoAt = Date(timeIntervalSince1970: v) }
                }
                curZ = z
            }
        }
        flushSet()
        flushZone()
        if !legacyURL.isEmpty {
            let tid = legacyVia ? "global" : "local"
            if let i = zoneIndex(tid), zones[i].repo.isEmpty {
                zones[i].repo = SplitListCatalog.rewriteLegacy(legacyURL)
                zones[i].auto = legacyAuto
                zones[i].repoLast = legacyLast
                zones[i].repoCount = legacyCount
                zones[i].repoAt = legacyAt
            }
        }
    }

    private func emitStore() -> String {
        var out = ""
        for z in zones {
            out += "zone: \(z.id)\n"
            out += "name: \(z.name)\n"
            out += "builtin: \(z.builtin)\n"
            out += "def: \(z.def)\n"
            out += "repo: \(z.repo)\n"
            out += "auto: \(z.auto)\n"
            out += "repo_last: \(z.repoLast)\n"
            out += "repo_count: \(z.repoCount)\n"
            out += "repo_at: \(z.repoAt.map { String($0.timeIntervalSince1970) } ?? "")\n\n"
        }
        for s in sets {
            out += "set: \(s.id)\n"
            out += "name: \(s.name)\n"
            out += "zone: \(s.zone)\n"
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
        var currentZone: String?
        var sawZone = false
        var lineCount = 0
        var rows: [(name: String, zone: String, cidr: String, ignore: Bool)] = []
        for raw in text.components(separatedBy: .newlines) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.isEmpty { continue }
            let up = line.uppercased()
            if up.hasPrefix("REM ") || up.hasPrefix("::") {
                let rest = String(line.drop(while: { $0 == ":" || $0 == " " }))
                let body = rest.lowercased()
                if body.contains("loft-routes") { loftHeader = true }
                if let cidr = ignoreCIDR(in: rest) {
                    rows.append(("ignore", currentZone ?? "", cidr, true))
                    lineCount += 1
                    continue
                }
                if let r = body.range(of: "zone:") {
                    let id = String(body[r.upperBound...]).trimmingCharacters(in: .whitespaces)
                    let word = id.split(whereSeparator: { $0 == " " || $0 == "\t" }).first.map(String.init) ?? ""
                    if !word.isEmpty {
                        currentZone = word
                        sawZone = true
                    }
                }
                if let d = direction(in: body, key: "list:") {
                    currentZone = d ? "global" : "local"
                    sawZone = true
                }
                continue
            }
            if up.hasPrefix("@ECHO") { continue }
            guard let row = parseRoute(line) else { continue }
            lineCount += 1
            var zone = ""
            if let dir = row.via {
                zone = dir ? "global" : "local"
                sawZone = true
            } else if let z = currentZone {
                zone = z
            }
            let ignore = row.name.lowercased() == "ignore"
            rows.append((row.name, zone, row.cidr, ignore))
        }
        var buckets: [String: SplitSet] = [:]
        var order: [String] = []
        for r in rows {
            let ignore = r.ignore || r.name.lowercased() == "ignore"
            let name = ignore ? "ignore" : (r.name.isEmpty ? r.cidr : r.name)
            let key = r.zone + ":" + (ignore ? "ignore" : name.lowercased())
            if buckets[key] == nil {
                order.append(key)
                buckets[key] = SplitSet(id: "", name: name, zone: r.zone, cidrs: [], source: ignore ? .ignore : .me)
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
        let needsWhere = !sets.isEmpty && !sawZone
        return SplitFile(loftHeader: loftHeader, sets: sets, lineCount: lineCount, needsWhere: needsWhere)
    }

    /// loft-routes (all zones, `REM zone:`) or Keenetic (one zone, no zone marker).
    static func emit(_ sets: [SplitSet], loft: Bool, zoneOrder: [String] = []) -> String {
        var out = "@echo off\r\n"
        func zoneOf(_ s: SplitSet) -> String { s.zone.isEmpty ? "global" : s.zone }
        func writeIgnore(_ s: SplitSet) {
            for c in s.cidrs {
                out += "REM ignore \(c)\r\n"
            }
        }
        func write(_ s: SplitSet) {
            if s.source == .ignore {
                writeIgnore(s)
                return
            }
            for c in s.cidrs {
                guard let pair = splitCIDR(c) else { continue }
                out += "route ADD \(pair.ip) MASK \(pair.mask) 0.0.0.0"
                if !s.name.isEmpty { out += " :: rem \(s.name)" }
                out += "\r\n"
            }
        }
        if loft {
            out += "REM loft-routes 1.0\r\n"
            let present = Set(sets.map { zoneOf($0) })
            var ids: [String] = []
            for id in zoneOrder + sets.map({ zoneOf($0) }) {
                if present.contains(id), !ids.contains(id) { ids.append(id) }
            }
            for zid in ids {
                out += "REM zone: \(zid)\r\n"
                for s in sets where zoneOf(s) == zid && s.source == .ignore {
                    writeIgnore(s)
                }
                for s in sets where zoneOf(s) == zid && s.source != .ignore {
                    write(s)
                }
            }
        } else {
            for s in sets where s.source == .ignore { writeIgnore(s) }
            for s in sets where s.source != .ignore { write(s) }
        }
        return out
    }

    /// True if `cidr` equals, covers, or is covered by an ignore prefix.
    static func hitsIgnore(_ cidr: String, _ ignores: [String]) -> Bool {
        for ig in ignores {
            if cidr == ig || covers(ig, cidr) || covers(cidr, ig) { return true }
        }
        return false
    }

    /// True when `outer` contains `inner` (same prefix or wider).
    static func covers(_ outer: String, _ inner: String) -> Bool {
        guard let o = cidrParts(outer), let i = cidrParts(inner) else { return false }
        if o.bits > i.bits { return false }
        let shift = 32 - o.bits
        if shift == 32 { return true }
        return (o.ip >> UInt32(shift)) == (i.ip >> UInt32(shift))
    }

    /// `REM ignore 104.16.0.0/12` or `:: rem ignore 10.0.0.0/8`.
    private static func ignoreCIDR(in rest: String) -> String? {
        var payload = rest.trimmingCharacters(in: .whitespaces)
        let low = payload.lowercased()
        if low.hasPrefix("rem ") {
            payload = String(payload.dropFirst(4)).trimmingCharacters(in: .whitespaces)
        }
        let plow = payload.lowercased()
        guard plow.hasPrefix("ignore ") || plow.hasPrefix("ignore\t") else { return nil }
        let raw = String(payload.dropFirst(7)).trimmingCharacters(in: .whitespaces)
        let token = raw.split(whereSeparator: { $0 == " " || $0 == "\t" }).first.map(String.init) ?? ""
        return normalizeCIDR(token)
    }

    private static func cidrParts(_ c: String) -> (ip: UInt32, bits: Int)? {
        let p = c.split(separator: "/", maxSplits: 1).map(String.init)
        guard let ipStr = ipv4(p[0]) else { return nil }
        let oct = ipStr.split(separator: ".")
        guard oct.count == 4 else { return nil }
        var ip: UInt32 = 0
        for o in oct {
            guard let v = UInt32(o), v <= 255 else { return nil }
            ip = (ip << 8) | v
        }
        let bits = p.count == 2 ? (Int(p[1]) ?? 32) : 32
        guard bits >= 0, bits <= 32 else { return nil }
        return (ip, bits)
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
