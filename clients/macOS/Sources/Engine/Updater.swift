// GitHub Releases: check, download, and swap loft.app.

import Foundation

/// Latest macOS DMG from `a3app/loft` releases, then a relaunch swap.
final class Updater: NSObject, URLSessionDownloadDelegate {
    static let repo = "a3app/loft"

    struct Offer {
        let version: String
        let dmgURL: URL
        let dmgName: String
        let sha256: String?
        let size: Int64
    }

    enum Fail: Error {
        case badHTTP(Int)
        case noAsset
        case checksum
        case mount
        case copy
        case network
    }

    private var session: URLSession!
    private var progress: ((Double) -> Void)?
    private var finish: ((Result<URL, Error>) -> Void)?

    override init() {
        super.init()
        let cfg = URLSessionConfiguration.ephemeral
        cfg.timeoutIntervalForRequest = 30
        cfg.timeoutIntervalForResource = 600
        session = URLSession(configuration: cfg, delegate: self, delegateQueue: OperationQueue.main)
    }

    deinit {
        session.invalidateAndCancel()
    }

    /// Asset name for the running slice.
    static var dmgName: String {
        #if arch(arm64)
        return "loft-macos-arm64.dmg"
        #else
        return "loft-macos-x86_64.dmg"
        #endif
    }

    /// True when `remote` is a higher semver than `local` (`v` prefix ignored).
    static func newer(_ remote: String, than local: String) -> Bool {
        func parts(_ s: String) -> [Int] {
            var t = s.trimmingCharacters(in: .whitespacesAndNewlines)
            if t.hasPrefix("v") || t.hasPrefix("V") { t = String(t.dropFirst()) }
            return t.split(separator: ".").prefix(3).map { Int($0) ?? 0 }
        }
        let a = parts(remote)
        let b = parts(local)
        let n = max(a.count, b.count)
        for i in 0..<n {
            let x = i < a.count ? a[i] : 0
            let y = i < b.count ? b[i] : 0
            if x != y { return x > y }
        }
        return false
    }

    /// Asks GitHub for the latest release. `nil` offer means this build is current.
    func check(current: String, done: @escaping (Result<Offer?, Error>) -> Void) {
        guard let url = URL(string: "https://api.github.com/repos/\(Updater.repo)/releases/latest") else {
            DispatchQueue.main.async { done(.failure(Fail.network)) }
            return
        }
        var req = URLRequest(url: url)
        req.setValue("loft-macos/\(current)", forHTTPHeaderField: "User-Agent")
        req.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        session.dataTask(with: req) { data, resp, err in
            DispatchQueue.global(qos: .userInitiated).async {
                if err != nil {
                    DispatchQueue.main.async { done(.failure(Fail.network)) }
                    return
                }
                let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
                guard let data = data, (200..<300).contains(code) else {
                    DispatchQueue.main.async { done(.failure(Fail.badHTTP(code))) }
                    return
                }
                do {
                    let offer = try self.parse(data, current: current)
                    DispatchQueue.main.async { done(.success(offer)) }
                } catch {
                    DispatchQueue.main.async { done(.failure(error)) }
                }
            }
        }.resume()
    }

    /// Downloads the DMG, checks SHA-256 when GitHub published sums, then swaps and relaunches.
    func install(_ offer: Offer, progress: @escaping (Double) -> Void, done: @escaping (Error?) -> Void) {
        self.progress = progress
        self.finish = { result in
            switch result {
            case .failure(let e):
                done(e)
            case .success(let dmg):
                progress(1)
                DispatchQueue.global(qos: .userInitiated).async {
                    do {
                        try self.swap(dmg, offer: offer)
                        DispatchQueue.main.async { done(nil) }
                    } catch {
                        DispatchQueue.main.async { done(error) }
                    }
                }
            }
        }
        var req = URLRequest(url: offer.dmgURL)
        req.setValue("loft-macos/\(offer.version)", forHTTPHeaderField: "User-Agent")
        session.downloadTask(with: req).resume()
    }

    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didWriteData bytesWritten: Int64, totalBytesWritten: Int64, totalBytesExpectedToWrite: Int64) {
        guard totalBytesExpectedToWrite > 0 else { return }
        progress?(Double(totalBytesWritten) / Double(totalBytesExpectedToWrite))
    }

    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didFinishDownloadingTo location: URL) {
        let dest = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("loft-update.dmg")
        try? FileManager.default.removeItem(at: dest)
        do {
            try FileManager.default.copyItem(at: location, to: dest)
            finish?(.success(dest))
        } catch {
            finish?(.failure(Fail.copy))
        }
        progress = nil
        finish = nil
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        if let error = error {
            finish?(.failure(error))
            progress = nil
            finish = nil
        }
    }

    private func parse(_ data: Data, current: String) throws -> Offer? {
        guard let root = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw Fail.network }
        let tag = (root["tag_name"] as? String) ?? ""
        var ver = tag
        if ver.hasPrefix("v") || ver.hasPrefix("V") { ver = String(ver.dropFirst()) }
        if ver.isEmpty { throw Fail.network }
        if !Updater.newer(ver, than: current) { return nil }
        let want = Updater.dmgName
        guard let assets = root["assets"] as? [[String: Any]] else { throw Fail.noAsset }
        var dmgURL: URL?
        var size: Int64 = 0
        var sumsURL: URL?
        for a in assets {
            let name = (a["name"] as? String) ?? ""
            let u = (a["browser_download_url"] as? String).flatMap { URL(string: $0) }
            if let u = u, Self.asset(name, matches: want) {
                if name == want || dmgURL == nil {
                    dmgURL = u
                    size = (a["size"] as? NSNumber)?.int64Value ?? 0
                }
            }
            if name == "loft-clients-SHA256SUMS.txt", let u = u {
                sumsURL = u
            }
        }
        guard let dmgURL = dmgURL else { throw Fail.noAsset }
        var sha: String?
        if let sumsURL = sumsURL, let sums = fetchText(sumsURL, ua: "loft-macos/\(current)") {
            sha = parseSum(sums, name: want) ?? parseSum(sums, name: dmgURL.lastPathComponent)
        }
        return Offer(version: ver, dmgURL: dmgURL, dmgName: want, sha256: sha, size: size)
    }

    private func parseSum(_ text: String, name: String) -> String? {
        for raw in text.split(whereSeparator: { $0.isNewline }) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.isEmpty || line.hasPrefix("#") { continue }
            let bits = line.split(whereSeparator: { $0 == " " || $0 == "\t" }).map(String.init).filter { !$0.isEmpty }
            guard bits.count >= 2 else { continue }
            let file = bits.last!.replacingOccurrences(of: "*", with: "")
            if file == name || file.hasSuffix("/" + name) {
                return bits[0].lowercased()
            }
        }
        return nil
    }

    private func swap(_ dmg: URL, offer: Offer) throws {
        if let want = offer.sha256 {
            guard let got = sha256(dmg), got == want else { throw Fail.checksum }
        }
        let mount = try attach(dmg)
        defer {
            _ = run("/usr/bin/hdiutil", ["detach", mount.path, "-quiet"])
            try? FileManager.default.removeItem(at: dmg)
        }
        let srcApp = mount.appendingPathComponent("loft.app")
        guard FileManager.default.fileExists(atPath: srcApp.path) else { throw Fail.mount }
        let staged = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("loft-update.app")
        try? FileManager.default.removeItem(at: staged)
        guard run("/usr/bin/ditto", [srcApp.path, staged.path]) == 0 else { throw Fail.copy }
        let dest = Bundle.main.bundleURL
        guard FileManager.default.isWritableFile(atPath: dest.deletingLastPathComponent().path) else { throw Fail.copy }
        try writeRelaunch(from: staged, onto: dest)
    }

    /// Unversioned `loft-macos-arm64.dmg` or `loft-1.0.10-macos-arm64.dmg`.
    private static func asset(_ name: String, matches want: String) -> Bool {
        if name == want { return true }
        #if arch(arm64)
        let tails = ["-macos-arm64.dmg", "-macos-aarch64.dmg"]
        #else
        let tails = ["-macos-x86_64.dmg", "-macos-amd64.dmg"]
        #endif
        return tails.contains { name.hasSuffix($0) }
    }

    private func fetchText(_ url: URL, ua: String) -> String? {
        var req = URLRequest(url: url)
        req.setValue(ua, forHTTPHeaderField: "User-Agent")
        req.timeoutInterval = 15
        let sem = DispatchSemaphore(value: 0)
        var text: String?
        URLSession.shared.dataTask(with: req) { data, _, _ in
            if let data = data { text = String(data: data, encoding: .utf8) }
            sem.signal()
        }.resume()
        _ = sem.wait(timeout: .now() + 16)
        return text
    }

    private func attach(_ dmg: URL) throws -> URL {
        let r = runCapture("/usr/bin/hdiutil", ["attach", "-plist", "-nobrowse", "-readonly", dmg.path])
        guard r.status == 0, let data = r.out else { throw Fail.mount }
        let obj = try PropertyListSerialization.propertyList(from: data, options: [], format: nil)
        guard let root = obj as? [String: Any],
              let ents = root["system-entities"] as? [[String: Any]] else { throw Fail.mount }
        for e in ents {
            if let p = e["mount-point"] as? String, !p.isEmpty {
                return URL(fileURLWithPath: p)
            }
        }
        throw Fail.mount
    }

    private func writeRelaunch(from src: URL, onto dest: URL) throws {
        let script = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("loft-relaunch.sh")
        let pid = ProcessInfo.processInfo.processIdentifier
        let body = """
        #!/bin/bash
        PID="\(pid)"
        SRC="\(src.path)"
        DEST="\(dest.path)"
        while /bin/kill -0 "$PID" 2>/dev/null; do /bin/sleep 0.2; done
        /bin/sleep 0.4
        /bin/rm -rf "$DEST.new"
        /usr/bin/ditto "$SRC" "$DEST.new" || exit 1
        /bin/rm -rf "$DEST"
        /bin/mv "$DEST.new" "$DEST" || exit 1
        /bin/rm -rf "$SRC"
        /usr/bin/open "$DEST"
        /bin/rm -f "$0"
        """
        try body.write(to: script, atomically: true, encoding: .utf8)
        _ = run("/bin/chmod", ["+x", script.path])
        let p = Process()
        p.launchPath = "/bin/bash"
        p.arguments = [script.path]
        p.launch()
    }

    private func sha256(_ url: URL) -> String? {
        guard let fh = try? FileHandle(forReadingFrom: url) else { return nil }
        defer { fh.closeFile() }
        var ctx = CC_SHA256_CTX()
        CC_SHA256_Init(&ctx)
        while true {
            let chunk = fh.readData(ofLength: 64 * 1024)
            if chunk.isEmpty { break }
            chunk.withUnsafeBytes { raw in
                _ = CC_SHA256_Update(&ctx, raw.baseAddress, CC_LONG(chunk.count))
            }
        }
        var dig = [UInt8](repeating: 0, count: Int(CC_SHA256_DIGEST_LENGTH))
        CC_SHA256_Final(&dig, &ctx)
        return dig.map { String(format: "%02x", $0) }.joined()
    }

    @discardableResult
    private func run(_ path: String, _ args: [String]) -> Int32 {
        let p = Process()
        p.launchPath = path
        p.arguments = args
        p.standardOutput = Pipe()
        p.standardError = Pipe()
        p.launch()
        p.waitUntilExit()
        return p.terminationStatus
    }

    private func runCapture(_ path: String, _ args: [String]) -> (status: Int32, out: Data?) {
        let p = Process()
        p.launchPath = path
        p.arguments = args
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = Pipe()
        p.launch()
        p.waitUntilExit()
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        return (p.terminationStatus, data)
    }
}
