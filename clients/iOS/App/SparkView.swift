import SwiftUI

/// Last-minute ↓ (lamp fill + stroke) and ↑ (ash). Connecting shows the handshake label.
struct SparkView: View {
    var down: [Double]
    var up: [Double]
    var handshake: String?
    var downText: String
    var upText: String
    var showRates: Bool
    var style: SparkStyle = .lines

    var body: some View {
        HStack(alignment: .center, spacing: 12) {
            sparkBody
                .frame(maxWidth: .infinity, minHeight: LoftTheme.sparkH)

            if showRates {
                VStack(alignment: .trailing, spacing: 4) {
                    Text(downText)
                        .foregroundStyle(Color.loftLamp)
                    Text(upText)
                        .foregroundStyle(Color.loftStone)
                }
                .font(LoftTheme.mono(12))
                .monospacedDigit()
            }
        }
        .frame(minHeight: LoftTheme.sparkH)
    }

    @ViewBuilder
    private var sparkBody: some View {
        if let handshake {
            Text(handshake)
                .font(LoftTheme.body(11.5))
                .foregroundStyle(Color.loftMute)
                .frame(maxWidth: .infinity, alignment: .leading)
        } else {
            SparkChart(down: down, up: up, style: style)
        }
    }
}

private struct SparkChart: View {
    var down: [Double]
    var up: [Double]
    var style: SparkStyle

    var body: some View {
        Canvas { ctx, size in
            if style == .bars {
                bars(ctx: ctx, size: size, arr: down, color: .loftLamp, y0: 0, h: size.height * 0.55)
                bars(ctx: ctx, size: size, arr: up, color: Color.loftAsh.opacity(0.9), y0: size.height * 0.58, h: size.height * 0.38)
                return
            }
            let dn = points(down, in: size)
            let upPts = points(up, in: size)
            guard let last = dn.last else { return }

            var fill = curve(dn)
            fill.addLine(to: CGPoint(x: last.x, y: size.height))
            fill.addLine(to: CGPoint(x: 0, y: size.height))
            fill.closeSubpath()
            ctx.fill(fill, with: .linearGradient(
                Gradient(colors: [
                    Color.loftLamp.opacity(0.32),
                    Color.loftLamp.opacity(0),
                ]),
                startPoint: CGPoint(x: 0, y: 0),
                endPoint: CGPoint(x: 0, y: size.height)
            ))

            ctx.stroke(
                curve(dn),
                with: .color(.loftLamp),
                style: StrokeStyle(lineWidth: 1.6, lineCap: .round, lineJoin: .round)
            )
            ctx.stroke(
                curve(upPts),
                with: .color(Color.loftAsh.opacity(0.9)),
                style: StrokeStyle(lineWidth: 1.2, lineCap: .round, lineJoin: .round)
            )

            if let u = upPts.last {
                ctx.fill(Path(ellipseIn: CGRect(x: last.x - 5, y: last.y - 5, width: 10, height: 10)), with: .color(Color.loftLamp.opacity(0.18)))
                ctx.fill(Path(ellipseIn: CGRect(x: last.x - 2.4, y: last.y - 2.4, width: 4.8, height: 4.8)), with: .color(.loftLamp))
                ctx.fill(Path(ellipseIn: CGRect(x: u.x - 1.9, y: u.y - 1.9, width: 3.8, height: 3.8)), with: .color(.loftAsh))
            }
        }
        .frame(height: LoftTheme.sparkH)
    }

    private func bars(ctx: GraphicsContext, size: CGSize, arr: [Double], color: Color, y0: CGFloat, h: CGFloat) {
        guard !arr.isEmpty else { return }
        let n = arr.count
        let gap: CGFloat = 1.2
        let w = max((size.width - gap * CGFloat(n - 1)) / CGFloat(n), 1)
        for (i, v) in arr.enumerated() {
            let bh = max(1, CGFloat(v) * h)
            let x = CGFloat(i) * (w + gap)
            let fade = 0.35 + 0.65 * CGFloat(i) / CGFloat(max(n - 1, 1))
            let r = CGRect(x: x, y: y0 + h - bh, width: w, height: bh)
            ctx.fill(Path(roundedRect: r, cornerRadius: 1), with: .color(color.opacity(fade)))
        }
    }

    private func points(_ arr: [Double], in size: CGSize) -> [CGPoint] {
        guard arr.count > 1 else { return [] }
        let w = max(size.width - 4, 1)
        let h = size.height
        return arr.enumerated().map { i, v in
            CGPoint(
                x: CGFloat(i) * w / CGFloat(arr.count - 1),
                y: h - 3 - CGFloat(v) * (h - 8)
            )
        }
    }

    private func curve(_ pts: [CGPoint]) -> Path {
        var path = Path()
        guard let first = pts.first else { return path }
        path.move(to: first)
        for i in 1..<pts.count {
            let p0 = pts[max(i - 2, 0)]
            let p1 = pts[i - 1]
            let p2 = pts[i]
            let p3 = pts[min(i + 1, pts.count - 1)]
            let c1 = CGPoint(x: p1.x + (p2.x - p0.x) / 6, y: p1.y + (p2.y - p0.y) / 6)
            let c2 = CGPoint(x: p2.x - (p3.x - p1.x) / 6, y: p2.y - (p3.y - p1.y) / 6)
            path.addCurve(to: p2, control1: c1, control2: c2)
        }
        return path
    }
}
