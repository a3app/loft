// UI strings: one pack per language in lang/<code>.lang (same files the design mock loads).

import Foundation

/// Lookup for every UI caption. Default locale is Russian, matching prefs.
enum L10n {
    enum Lang: String, CaseIterable {
        /// Picker order: English first, then Latin/Cyrillic by English name, then ideographic.
        case en, fr, de, it, pt, ru, vi, zh, ja, ko

        /// Name of this language in that language, so the picker stays findable.
        var nativeName: String {
            switch self {
            case .ru: return "Русский"
            case .en: return "English"
            case .de: return "Deutsch"
            case .fr: return "Français"
            case .pt: return "Português"
            case .zh: return "中文"
            case .it: return "Italiano"
            case .ja: return "日本語"
            case .ko: return "한국어"
            case .vi: return "Tiếng Việt"
            }
        }

        /// Foundation locale for dates and numbers.
        var localeID: String {
            switch self {
            case .ru: return "ru_RU"
            case .en: return "en_US"
            case .de: return "de_DE"
            case .fr: return "fr_FR"
            case .pt: return "pt_BR"
            case .zh: return "zh_CN"
            case .it: return "it_IT"
            case .ja: return "ja_JP"
            case .ko: return "ko_KR"
            case .vi: return "vi_VN"
            }
        }
    }

    /// Active UI language. `t` reads this.
    static var lang: Lang = .ru

    /// Sets `lang` from a prefs code; unknown codes stay Russian.
    static func apply(_ code: String) {
        lang = Lang(rawValue: code) ?? .ru
    }

    /// Looks up `k` in the active pack; English, then Russian, then the key itself.
    /// Underscore keys also try the dotted mock form (`split_zone` → `split.zone`).
    static func t(_ k: String) -> String {
        if let s = lookup(k) { return s }
        if k.contains("_") {
            let dotted = k.replacingOccurrences(of: "_", with: ".")
            if dotted != k, let s = lookup(dotted) { return s }
        }
        return k
    }

    private static func lookup(_ k: String) -> String? {
        if let s = packs[lang.rawValue]?[k] { return s }
        if lang != .en, let s = packs["en"]?[k] { return s }
        if lang != .ru, let s = packs["ru"]?[k] { return s }
        return nil
    }

    private static let packs: [String: [String: String]] = loadPacks()

    /// Reads `lang/<code>.lang` from the app bundle (copied from clients/macOS/lang).
    private static func loadPacks() -> [String: [String: String]] {
        var out: [String: [String: String]] = [:]
        let bundle = Bundle.main
        for code in Lang.allCases.map({ $0.rawValue }) {
            let url = bundle.url(forResource: code, withExtension: "lang", subdirectory: "lang")
                ?? bundle.url(forResource: code, withExtension: "lang")
            guard let url = url, let data = try? Data(contentsOf: url) else { continue }
            guard let pack = parsePack(data) else { continue }
            out[code] = pack
        }
        return out
    }

    /// Accepts a JSON object, or `L10N_PACKS["xx"] = { ... };`.
    private static func parsePack(_ data: Data) -> [String: String]? {
        var slice = data
        if let s = String(data: data, encoding: .utf8),
           let a = s.firstIndex(of: "{"),
           let b = s.lastIndex(of: "}"),
           a <= b {
            slice = Data(s[a...b].utf8)
        }
        guard let obj = try? JSONSerialization.jsonObject(with: slice) as? [String: Any] else { return nil }
        var pack: [String: String] = [:]
        pack.reserveCapacity(obj.count)
        for (k, v) in obj {
            if let str = v as? String { pack[k] = str }
        }
        return pack
    }
}
