import SwiftUI

/// loft tokens. Inspector edits padding/font/color on the views that use these.
enum LoftTheme {
    static let mark: CGFloat = 56
    static let title: CGFloat = 22
    static let go: CGFloat = 15
    static let plateKey: CGFloat = 9
    static let plateVal: CGFloat = 12.5
    static let pill: CGFloat = 11.5
    static let tab: CGFloat = 9.5
    static let goCorner: CGFloat = 14
    static let plateCorner: CGFloat = 10
    static let sparkH: CGFloat = 40

    static func demi(_ size: CGFloat) -> Font {
        .custom("AvenirNext-DemiBold", size: size)
    }

    static func body(_ size: CGFloat) -> Font {
        .custom("AvenirNext-Regular", size: size)
    }

    static func medium(_ size: CGFloat) -> Font {
        .custom("AvenirNext-Medium", size: size)
    }

    static func mono(_ size: CGFloat) -> Font {
        .system(size: size, design: .monospaced)
    }
}

extension Color {
    static let loftInk = Color(red: 26 / 255, green: 23 / 255, blue: 20 / 255)
    static let loftInk2 = Color(red: 36 / 255, green: 31 / 255, blue: 27 / 255)
    static let loftSlate = Color(red: 58 / 255, green: 51 / 255, blue: 46 / 255)
    static let loftStone = Color(red: 183 / 255, green: 169 / 255, blue: 154 / 255)
    static let loftLamp = Color(red: 232 / 255, green: 184 / 255, blue: 74 / 255)
    static let loftLampHi = Color(red: 243 / 255, green: 210 / 255, blue: 122 / 255)
    static let loftSnow = Color(red: 243 / 255, green: 237 / 255, blue: 228 / 255)
    static let loftPaper = Color(red: 232 / 255, green: 223 / 255, blue: 210 / 255)
    static let loftMute = Color(red: 138 / 255, green: 126 / 255, blue: 114 / 255)
    /// Phone chrome in the mock (`#181512`), not the light paper token.
    static let loftPhone = Color(red: 24 / 255, green: 21 / 255, blue: 18 / 255)
    static let loftField = Color(red: 33 / 255, green: 28 / 255, blue: 25 / 255)
    static let loftGhost = Color(red: 42 / 255, green: 36 / 255, blue: 32 / 255)
    static let loftLine = Color(red: 243 / 255, green: 237 / 255, blue: 228 / 255).opacity(0.12)
    static let loftErr = Color(red: 226 / 255, green: 75 / 255, blue: 75 / 255)
    static let loftDangerFill = Color(red: 226 / 255, green: 75 / 255, blue: 75 / 255).opacity(0.15)
    static let loftDangerText = Color(red: 240 / 255, green: 160 / 255, blue: 160 / 255)
    /// Spark ↑ stroke in the mock (`#A89A8C`).
    static let loftAsh = Color(red: 168 / 255, green: 154 / 255, blue: 140 / 255)
}
