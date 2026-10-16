// Decode QR payloads from a still image (screenshot or photo of a share URI).

import AppKit
import CoreImage

/// CIDetector QR scan of a file or `NSImage`.
enum QRScan {
    static let imageExt: Set<String> = [
        "png", "jpg", "jpeg", "gif", "tif", "tiff", "bmp", "webp", "heic", "heif",
    ]

    /// True when `url` looks like a raster image loft should try as a QR.
    static func isImage(_ url: URL) -> Bool {
        imageExt.contains(url.pathExtension.lowercased())
    }

    /// Distinct UTF-8 payloads from QR codes in the file, or empty.
    static func payloads(from url: URL) -> [String] {
        guard let img = NSImage(contentsOf: url) else { return [] }
        return payloads(from: img)
    }

    /// Distinct UTF-8 payloads from QR codes in `image`.
    static func payloads(from image: NSImage) -> [String] {
        guard let ci = ciImage(image) else { return [] }
        var seen = Set<String>()
        var out: [String] = []
        for candidate in variants(ci) {
            for p in scan(candidate) {
                let t = p.trimmingCharacters(in: .whitespacesAndNewlines)
                if t.isEmpty { continue }
                if seen.insert(t).inserted { out.append(t) }
            }
            if !out.isEmpty { return out }
        }
        return out
    }

    /// `CIImage` from an `NSImage` bitmap.
    private static func ciImage(_ image: NSImage) -> CIImage? {
        guard let tiff = image.tiffRepresentation,
              let rep = NSBitmapImageRep(data: tiff),
              let cg = rep.cgImage else { return nil }
        return CIImage(cgImage: cg)
    }

    /// Original, scaled, and contrast variants so a small screenshot still reads.
    private static func variants(_ ci: CIImage) -> [CIImage] {
        let e = ci.extent
        let side = max(e.width, e.height)
        if side < 1 { return [] }
        var list: [CIImage] = [ci]
        if side < 280 {
            let f = 280 / side
            list.append(ci.transformed(by: CGAffineTransform(scaleX: f, y: f)))
        }
        if side > 2048 {
            let f = 2048 / side
            list.append(ci.transformed(by: CGAffineTransform(scaleX: f, y: f)))
        }
        if let filter = CIFilter(name: "CIColorControls") {
            filter.setValue(ci, forKey: kCIInputImageKey)
            filter.setValue(1.4, forKey: kCIInputContrastKey)
            if let out = filter.outputImage { list.append(out) }
        }
        return list
    }

    /// High-accuracy QR features in `ci`.
    private static func scan(_ ci: CIImage) -> [String] {
        let det = CIDetector(
            ofType: CIDetectorTypeQRCode,
            context: nil,
            options: [CIDetectorAccuracy: CIDetectorAccuracyHigh]
        )
        let feats = det?.features(in: ci) as? [CIQRCodeFeature] ?? []
        return feats.compactMap { $0.messageString }
    }
}
