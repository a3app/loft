import SwiftUI

/// L-roof + lamp in the 96×96 brand space. On = snow body / lamp window; off = mute / slate.
struct LoftMark: View {
    var on: Bool
    var size: CGFloat = LoftTheme.mark

    var body: some View {
        ZStack {
            LoftMarkBody().fill(on ? Color.loftSnow : Color.loftMute)
            LoftMarkLamp().fill(on ? Color.loftLamp : Color.loftSlate)
        }
        .frame(width: size, height: size)
        .accessibilityHidden(true)
    }
}

struct LoftMarkBody: Shape {
    func path(in rect: CGRect) -> Path {
        LoftMarkGeometry.scaled(LoftMarkGeometry.body, in: rect)
    }
}

struct LoftMarkLamp: Shape {
    func path(in rect: CGRect) -> Path {
        LoftMarkGeometry.scaled(LoftMarkGeometry.lamp, in: rect)
    }
}

private enum LoftMarkGeometry {
    static let body: Path = {
        var p = Path()
        let a = CGPoint(x: 37.76, y: 6.72)
        p.move(to: a)
        svgArc(&p, from: a, rx: 0.96, ry: 0.96, large: false, sweep: true, to: CGPoint(x: 39.01, y: 6.74))
        p.addLine(to: CGPoint(x: 57.6, y: 29.52))
        p.addLine(to: CGPoint(x: 57.6, y: 63.76))
        svgArc(&p, from: CGPoint(x: 57.6, y: 63.76), rx: 4.4, ry: 4.4, large: false, sweep: false, to: CGPoint(x: 62, y: 68.16))
        p.addLine(to: CGPoint(x: 84.8, y: 68.16))
        svgArc(&p, from: CGPoint(x: 84.8, y: 68.16), rx: 3.2, ry: 3.2, large: false, sweep: true, to: CGPoint(x: 88, y: 71.36))
        p.addLine(to: CGPoint(x: 88, y: 86.16))
        svgArc(&p, from: CGPoint(x: 88, y: 86.16), rx: 3.84, ry: 3.84, large: false, sweep: true, to: CGPoint(x: 84.16, y: 90))
        p.addLine(to: CGPoint(x: 21.44, y: 90))
        svgArc(&p, from: CGPoint(x: 21.44, y: 90), rx: 3.84, ry: 3.84, large: false, sweep: true, to: CGPoint(x: 17.6, y: 86.16))
        p.addLine(to: CGPoint(x: 17.6, y: 29.52))
        p.closeSubpath()
        return p
    }()

    static let lamp = Path(
        roundedRect: CGRect(x: 28.8, y: 36.24, width: 17.6, height: 21.84),
        cornerRadius: 2.56
    )

    static func scaled(_ path: Path, in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 96
        guard s > 0 else { return Path() }
        let t = CGAffineTransform(translationX: rect.midX - 48 * s, y: rect.midY - 48 * s)
            .scaledBy(x: s, y: s)
        return path.applying(t)
    }
}

/// SVG elliptical arc → cubics. Same conversion as macOS `MarkView`.
private func svgArc(
    _ path: inout Path,
    from p1: CGPoint,
    rx inRx: CGFloat,
    ry inRy: CGFloat,
    large: Bool,
    sweep: Bool,
    to p2: CGPoint
) {
    var rx = abs(inRx), ry = abs(inRy)
    if rx < 0.001 || ry < 0.001 {
        path.addLine(to: p2)
        return
    }
    let dx = (p1.x - p2.x) / 2, dy = (p1.y - p2.y) / 2
    let lam = (dx * dx) / (rx * rx) + (dy * dy) / (ry * ry)
    if lam > 1 {
        let s = sqrt(lam)
        rx *= s
        ry *= s
    }
    let sign: CGFloat = (large == sweep) ? -1 : 1
    let num = rx * rx * ry * ry - rx * rx * dy * dy - ry * ry * dx * dx
    let den = rx * rx * dy * dy + ry * ry * dx * dx
    let co = sign * sqrt(max(0, num / max(den, 0.0001)))
    let cxp = co * (rx * dy / ry)
    let cyp = co * -(ry * dx / rx)
    let cx = cxp + (p1.x + p2.x) / 2
    let cy = cyp + (p1.y + p2.y) / 2
    func ang(_ ux: CGFloat, _ uy: CGFloat, _ vx: CGFloat, _ vy: CGFloat) -> CGFloat {
        let u = sqrt(ux * ux + uy * uy), v = sqrt(vx * vx + vy * vy)
        let a = acos(max(-1, min(1, (ux * vx + uy * vy) / max(u * v, 0.0001))))
        return (ux * vy - uy * vx < 0) ? -a : a
    }
    let θ1 = ang(1, 0, (dx - cxp) / rx, (dy - cyp) / ry)
    var dθ = ang((dx - cxp) / rx, (dy - cyp) / ry, (-dx - cxp) / rx, (-dy - cyp) / ry)
    if !sweep && dθ > 0 { dθ -= 2 * .pi }
    if sweep && dθ < 0 { dθ += 2 * .pi }
    let n = max(1, Int(ceil(abs(dθ) / (.pi / 2))))
    let step = dθ / CGFloat(n)
    for i in 0..<n {
        let a1 = θ1 + CGFloat(i) * step
        let a2 = a1 + step
        let k = tan(step / 4) * 4 / 3
        func pt(_ a: CGFloat) -> CGPoint {
            CGPoint(x: rx * cos(a) + cx, y: ry * sin(a) + cy)
        }
        let pa = pt(a1), pb = pt(a2)
        path.addCurve(
            to: pb,
            control1: CGPoint(x: pa.x + k * (-rx * sin(a1)), y: pa.y + k * (ry * cos(a1))),
            control2: CGPoint(x: pb.x - k * (-rx * sin(a2)), y: pb.y - k * (ry * cos(a2)))
        )
    }
}
