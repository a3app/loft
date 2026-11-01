// GitHub Releases: check, download, and swap loft.app.
// Self-updater for the macOS client. It asks GitHub Releases whether a newer
// loft build exists, downloads the DMG for this CPU (arm64 or x86_64), checks
// its size and SHA-256, copies loft.app out of the mounted image and hands the
// final swap to a small shell script that runs after this process quits.
// The UI side (the offer window and progress) lives in App/UpdateOffer.swift.

import Foundation

/// Latest macOS DMG from `a3app/loft` releases, then a relaunch swap.
final class Updater: NSObject, URLSessionDownloadDelegate {
    /// GitHub "owner/name" that publishes the release assets.
    static let repo = "a3app/loft"

    /// One available update: the release version and the DMG asset to fetch.
    /// `sha256` is nil when the release has no SHA256SUMS file; `size` is the
    /// byte count GitHub reports (0 when unknown), used as a cheap sanity check.
    struct Offer {
        let version: String
        let dmgURL: URL
        let dmgName: String
        let sha256: String?
        let size: Int64
    }

    /// Why a check or an install failed. The UI maps each case to a message.
    enum Fail: Error {
        /// GitHub answered with a non-2xx status (the code is kept for the message).
        case badHTTP(Int)
        /// The release has no DMG for this CPU architecture.
        case noAsset
        /// Downloaded size or SHA-256 does not match what the release promised.
        case checksum
        /// The DMG could not be mounted, or it holds no loft.app.
        case mount
        /// Copying the download or the app bundle failed, or the app folder is read-only.
        case copy
        /// No connection, a timeout, or a reply that could not be parsed.
        case network
    }

    /// Own URL session so the download delegate callbacks below reach this object.
    private var session: URLSession!
    /// Progress callback of the running `install` (fraction 0...1), nil when idle.
    private var progress: ((Double) -> Void)?
    /// Completion of the running download, nil when idle.
    private var finish: ((Result<URL, Error>) -> Void)?
    /// True after `didFinishDownloadingTo` copied the DMG. Trailing URLSession
    /// errors on the GitHub→Azure redirect must not wipe a good file.
    private var acceptedDownload = false

    /// Builds an ephemeral session (no cookies or cache on disk) with a 30 s
    /// request timeout and a 10 minute ceiling for the whole download.
    /// Delegate callbacks arrive on the main queue.
    override init() {
        super.init()
        let cfg = URLSessionConfiguration.ephemeral
        cfg.timeoutIntervalForRequest = 30
        cfg.timeoutIntervalForResource = 600
        session = URLSession(configuration: cfg, delegate: self, delegateQueue: OperationQueue.main)
    }

    /// Cancels any download still in flight when the updater goes away.
    deinit {
        session.invalidateAndCancel()
    }

    /// Asset name for the running slice.
    static var dmgName: String {
        #if arch(arm64)
        return "loft-macos-arm.dmg"
        #else
        return "loft-macos-intel.dmg"
        #endif
    }

    /// True when `remote` is a higher semver than `local` (`v` prefix ignored).
    static func newer(_ remote: String, than local: String) -> Bool {
        /// Splits "v1.2.3" into up to three integers; missing or junk parts count as 0.
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
                // No GitHub Release (tag-only repo) is "nothing newer", not offline.
                if code == 404 {
                    DispatchQueue.main.async { done(.success(nil)) }
                    return
                }
                guard let data = data, (200..<300).contains(code) else {
                    DispatchQueue.main.async { done(.failure(Fail.badHTTP(code))) }
                    return
                }
                do {
                    let offer = try self.parse(data, current: current)
                    DispatchQueue.main.async { done(.success(offer)) }
                } catch let e as Fail {
                    DispatchQueue.main.async { done(.failure(e)) }
                } catch {
                    DispatchQueue.main.async { done(.failure(Fail.network)) }
                }
            }
        }.resume()
    }

    /// Downloads the DMG, checks SHA-256 when GitHub published sums, then swaps and relaunches.
    func install(_ offer: Offer, progress: @escaping (Double) -> Void, done: @escaping (Error?) -> Void) {
        acceptedDownload = false
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
                    } catch let e as Fail {
                        DispatchQueue.main.async { done(e) }
                    } catch {
                        DispatchQueue.main.async { done(Fail.mount) }
                    }
                }
            }
        }
        var req = URLRequest(url: offer.dmgURL)
        req.setValue("loft-macos/\(offer.version)", forHTTPHeaderField: "User-Agent")
        session.downloadTask(with: req).resume()
    }

    /// Download progress from URLSession; forwarded as a fraction when the total size is known.
    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didWriteData bytesWritten: Int64, totalBytesWritten: Int64, totalBytesExpectedToWrite: Int64) {
        guard totalBytesExpectedToWrite > 0 else { return }
        progress?(Double(totalBytesWritten) / Double(totalBytesExpectedToWrite))
    }

    /// Download finished: URLSession deletes `location` once this returns, so the
    /// file is copied to a fixed temp path first, then the install continues.
    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didFinishDownloadingTo location: URL) {
        let dest = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("loft-update.dmg")
        try? FileManager.default.removeItem(at: dest)
        do {
            try FileManager.default.copyItem(at: location, to: dest)
            acceptedDownload = true
            finish?(.success(dest))
        } catch {
            finish?(.failure(Fail.copy))
        }
        progress = nil
        finish = nil
    }

    /// Task ended. Reports a network failure only when no good file was accepted,
    /// because GitHub redirects to a CDN and a late error may follow a complete download.
    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        if acceptedDownload { return }
        guard error != nil else { return }
        finish?(.failure(Fail.network))
        progress = nil
        finish = nil
    }

    /// Reads the "latest release" JSON. Returns nil when the tag is not newer than
    /// `current`. Otherwise picks the DMG for this CPU (a versioned asset name wins
    /// over the old unversioned one) and, if a SHA256SUMS file is attached, fetches
    /// it and pulls out the expected hash.
    private func parse(_ data: Data, current: String) throws -> Offer? {
        let obj: Any
        do {
            obj = try JSONSerialization.jsonObject(with: data)
        } catch {
            throw Fail.network
        }
        guard let root = obj as? [String: Any] else { throw Fail.network }
        let tag = (root["tag_name"] as? String) ?? ""
        var ver = tag
        if ver.hasPrefix("v") || ver.hasPrefix("V") { ver = String(ver.dropFirst()) }
        if ver.isEmpty { throw Fail.network }
        if !Updater.newer(ver, than: current) { return nil }
        // Walk the asset list once: remember the DMG and the checksum file.
        let want = Updater.dmgName
        guard let assets = root["assets"] as? [[String: Any]] else { throw Fail.noAsset }
        var dmgURL: URL?
        var size: Int64 = 0
        var sumsURL: URL?
        for a in assets {
            let name = (a["name"] as? String) ?? ""
            let u = (a["browser_download_url"] as? String).flatMap { URL(string: $0) }
            if let u = u, Self.asset(name, matches: want) {
                let versioned = name != want
                if versioned || dmgURL == nil {
                    dmgURL = u
                    size = (a["size"] as? NSNumber)?.int64Value ?? 0
                }
            }
            if name == "loft-clients-\(ver)-SHA256SUMS.txt" || name == "loft-clients-SHA256SUMS.txt", let u = u {
                if name.hasPrefix("loft-clients-\(ver)-") || sumsURL == nil {
                    sumsURL = u
                }
            }
        }
        guard let dmgURL = dmgURL else { throw Fail.noAsset }
        // The checksum is optional: older releases shipped without one.
        var sha: String?
        if let sumsURL = sumsURL, let sums = fetchText(sumsURL, ua: "loft-macos/\(current)") {
            sha = parseSum(sums, name: want) ?? parseSum(sums, name: dmgURL.lastPathComponent)
        }
        return Offer(version: ver, dmgURL: dmgURL, dmgName: want, sha256: sha, size: size)
    }

    /// Finds the hash for file `name` in "sha256sum" output ("<hash>  <file>" or
    /// "<hash> *<file>" lines). Returns it lowercased, or nil if the file is not listed.
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

    /// Verifies and stages the update, then arms the relaunch script. Runs off the
    /// main thread. Steps: check size and hash, mount the DMG, copy loft.app to a
    /// temp folder, and make sure the current app's folder is writable. The DMG is
    /// detached and deleted on every exit path.
    private func swap(_ dmg: URL, offer: Offer) throws {
        let n = (try? FileManager.default.attributesOfItem(atPath: dmg.path)[.size] as? NSNumber)?.int64Value ?? 0
        if offer.size > 0, n != offer.size { throw Fail.checksum }
        if n < 64 * 1024 { throw Fail.mount }
        // 1. Verify the download before trusting anything inside it.
        if let want = offer.sha256 {
            guard let got = sha256(dmg), got == want else { throw Fail.checksum }
        }
        // 2. Mount read-only and copy the app out, so the image can be detached right away.
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
        // 3. The script replaces the bundle in place, so its parent folder must be writable.
        let dest = Bundle.main.bundleURL
        guard FileManager.default.isWritableFile(atPath: dest.deletingLastPathComponent().path) else { throw Fail.copy }
        try writeRelaunch(from: staged, onto: dest)
    }

    /// Dist names `loft-1.0.33-macos-arm.dmg` / `macos-intel.dmg`, plus the
    /// older arm64 / x86_64 / aarch64 / amd64 tails and the unversioned name.
    private static func asset(_ name: String, matches want: String) -> Bool {
        if name == want { return true }
        #if arch(arm64)
        let tails = ["-macos-arm.dmg", "-macos-arm64.dmg", "-macos-aarch64.dmg"]
        #else
        let tails = ["-macos-intel.dmg", "-macos-x86_64.dmg", "-macos-amd64.dmg"]
        #endif
        return tails.contains { name.hasSuffix($0) }
    }

    /// Fetches a small text file synchronously (waits up to about 16 s).
    /// Used only from the background queue in `parse`, never on the main thread.
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

    /// Mounts the DMG read-only without showing it in Finder and returns the mount
    /// point. The quarantine attributes are cleared first so Gatekeeper does not
    /// block the mount; hdiutil's plist output is parsed for the "mount-point".
    private func attach(_ dmg: URL) throws -> URL {
        _ = run("/usr/bin/xattr", ["-c", dmg.path])
        let r = runCapture("/usr/bin/hdiutil", ["attach", "-plist", "-nobrowse", "-readonly", "-noverify", dmg.path])
        guard r.status == 0, let data = r.out, !data.isEmpty else { throw Fail.mount }
        let obj: Any
        do {
            obj = try PropertyListSerialization.propertyList(from: data, options: [], format: nil)
        } catch {
            throw Fail.mount
        }
        guard let root = obj as? [String: Any],
              let ents = root["system-entities"] as? [[String: Any]] else { throw Fail.mount }
        for e in ents {
            if let p = e["mount-point"] as? String, !p.isEmpty {
                return URL(fileURLWithPath: p)
            }
        }
        throw Fail.mount
    }

    /// Writes and starts a bash script that waits for this process (by PID) to
    /// exit, then replaces `dest` with the staged app via a ".new" copy and opens
    /// it again. A running app cannot overwrite its own bundle safely, hence the
    /// detached script. The script deletes itself when done.
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
        do {
            try body.write(to: script, atomically: true, encoding: .utf8)
        } catch {
            throw Fail.copy
        }
        _ = run("/bin/chmod", ["+x", script.path])
        let p = Process()
        p.arguments = [script.path]
        try? p.start(at: "/bin/bash")
    }

    /// SHA-256 of a file as lowercase hex, read in 64 KB chunks so a large DMG
    /// is never loaded into memory at once.
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

    /// Runs a tool, discards its output and returns its exit status (-1 if it could not start).
    @discardableResult
    private func run(_ path: String, _ args: [String]) -> Int32 {
        let p = Process()
        p.arguments = args
        p.standardOutput = Pipe()
        p.standardError = Pipe()
        do { try p.start(at: path) } catch { return -1 }
        p.waitUntilExit()
        return p.terminationStatus
    }

    /// Runs a tool and returns its exit status together with everything it wrote to stdout.
    private func runCapture(_ path: String, _ args: [String]) -> (status: Int32, out: Data?) {
        let p = Process()
        p.arguments = args
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = Pipe()
        do { try p.start(at: path) } catch { return (-1, nil) }
        p.waitUntilExit()
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        return (p.terminationStatus, data)
    }
}
