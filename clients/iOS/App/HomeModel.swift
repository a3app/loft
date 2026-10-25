import Foundation

/// Canvas / UI snapshot. No Packet Tunnel, no gomobile.
struct HomeSnapshot: Equatable {
    enum Mode: String, Equatable {
        case off
        case connecting
        case on
        case simple
    }

    enum Kind: String, Equatable {
        case loft, sstp, vless, trojan
    }

    var mode: Mode
    var kind: Kind = .loft
    var profile: String = "Home"
    var uptime: String = "0:12"
    var downMbit: String = "12.4"
    var upMbit: String = "1.1"
    var externalIP: String = "203.0.113.17"
    var overlayIP: String = "10.8.0.14"
    var route: String = "10.8.0.1"
    var sparkDown: [Double] = HomeSnapshot.sampleDown
    var sparkUp: [Double] = HomeSnapshot.sampleUp

    var lampOn: Bool { mode == .on || mode == .simple }
    var isLive: Bool { lampOn }
    var showPlates: Bool { mode != .simple }
    var showSpark: Bool { mode != .simple }
    var showIPs: Bool { mode == .on }
    var showRates: Bool { mode == .on }

    static let off = HomeSnapshot(mode: .off)
    static let connecting = HomeSnapshot(mode: .connecting)
    static let on = HomeSnapshot(mode: .on)
    static let simple = HomeSnapshot(mode: .simple)

    /// Last-minute samples from the HTML mock.
    static let sampleDown: [Double] = [
        0.32, 0.38, 0.35, 0.46, 0.52, 0.44, 0.4, 0.55, 0.63, 0.58,
        0.5, 0.47, 0.56, 0.68, 0.74, 0.66, 0.6, 0.64, 0.72, 0.8,
        0.7, 0.62, 0.58, 0.66, 0.75, 0.83, 0.78, 0.7, 0.64, 0.69,
        0.77, 0.86, 0.8, 0.72, 0.68, 0.74, 0.82, 0.9, 0.84, 0.78,
    ]
    static let sampleUp: [Double] = [
        0.1, 0.12, 0.11, 0.14, 0.16, 0.13, 0.12, 0.15, 0.18, 0.16,
        0.14, 0.13, 0.15, 0.19, 0.2, 0.18, 0.16, 0.17, 0.2, 0.22,
        0.19, 0.17, 0.16, 0.18, 0.2, 0.23, 0.21, 0.19, 0.17, 0.18,
        0.21, 0.24, 0.22, 0.2, 0.18, 0.2, 0.22, 0.25, 0.23, 0.21,
    ]

    mutating func tapPrimary() {
        switch mode {
        case .off, .connecting:
            mode = .on
        case .on, .simple:
            mode = .off
        }
    }

    mutating func cancel() {
        mode = .off
    }
}
