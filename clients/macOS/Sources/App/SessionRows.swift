// Several on: the other live sessions as collapsed rows under the main one.

import AppKit

/// One collapsed live session: lamp-dot, name, uptime or status, and the profile's action (Disconnect / Cancel / Retry).
/// A click on the row makes it the main session.
final class SessionRow: FlippedView {
    static let height: CGFloat = 30

    let plate = LoftPlate(frame: .zero)
    private let hit = ActionButton(frame: .zero)
    private let dot = FlippedView(frame: .zero)
    private let name = PassLab(labelWithString: "")
    private let time = PassLab(labelWithString: "")
    private let act = PillButton(frame: .zero)
    private var pid = ""
    var onPrefer: ((String) -> Void)?
    var onToggle: ((String) -> Void)?

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        plate.radius = 10
        hit.drawsWash = false
        hit.target = self
        hit.action = #selector(tapRow)
        dot.wantsLayer = true
        dot.layer?.cornerRadius = 3.5
        name.font = Palette.body(12.5)
        name.textColor = Palette.snow
        time.font = Palette.body(11.5)
        time.alignment = .right
        act.compact = true
        act.target = self
        act.action = #selector(tapAct)
        for t in [name, time] {
            t.backgroundColor = .clear
            t.isBezeled = false
            t.drawsBackground = false
            t.isEditable = false
            t.isSelectable = false
            Palette.fitLine(t)
        }
        for v in [plate, hit, dot, name, time, act] as [NSView] { addSubview(v) }
    }

    required init?(coder: NSCoder) { fatalError() }

    /// Fills the row from one profile's live state and lays it out in `width`.
    func fill(_ p: LoftProfile, engine: SessionController, width: CGFloat, inset: CGFloat) {
        pid = p.id
        let st = engine.state(of: p.id)
        let err = st == .error
        let label = p.name.isEmpty ? Palette.hostOf(p.url) : p.name
        name.stringValue = label
        if err {
            time.stringValue = L10n.t("err")
        } else if st == .connecting {
            time.stringValue = L10n.t("connecting")
        } else {
            time.stringValue = engine.connectedAt(of: p.id).map { Palette.uptime($0) } ?? ""
        }
        time.textColor = err ? Palette.err : Palette.mute
        dot.layer?.backgroundColor = (err ? Palette.err : (st == .on ? Palette.lamp : Palette.mute)).cgColor
        act.title = engine.actionTitle(of: p.id)
        act.lampOn = false

        let h = Self.height
        frame.size = NSSize(width: width, height: h)
        plate.frame = bounds
        hit.frame = bounds
        let actFont = Palette.font(11.5)
        let actW = max(64, Palette.textWidth(act.title, font: actFont) + 18)
        let actH: CGFloat = 22
        act.frame = NSRect(x: width - inset - actW, y: round((h - actH) / 2), width: actW, height: actH)
        let dotS: CGFloat = 7
        dot.frame = NSRect(x: inset, y: round((h - dotS) / 2), width: dotS, height: dotS)
        // The name keeps at least ~55% of the room; uptime or status gets what is left.
        let nameX = inset + dotS + 8
        let room = act.frame.minX - 8 - nameX
        let nameNeed = Palette.textWidth(label, font: name.font ?? Palette.body(12.5)) + 4
        let timeNeed = Palette.textWidth(time.stringValue, font: time.font ?? Palette.body(11.5)) + 4
        let nameMin = min(nameNeed, round(room * 0.55))
        let timeW = max(0, min(timeNeed, room - 8 - nameMin))
        let timeX = act.frame.minX - 8 - timeW
        time.frame = NSRect(x: timeX, y: round((h - 18) / 2), width: timeW, height: 18)
        name.frame = NSRect(x: nameX, y: round((h - 18) / 2), width: max(20, timeX - 8 - nameX), height: 18)
    }

    @objc private func tapRow() {
        LoftLog.shared.tap("session-row", "prefer")
        onPrefer?(pid)
    }

    @objc private func tapAct() {
        LoftLog.shared.tap("session-row", act.title)
        onToggle?(pid)
    }
}

/// Keeps one `SessionRow` per profile id inside a host view, reusing rows across renders.
final class SessionRows {
    private var rows: [String: SessionRow] = [:]
    private weak var host: NSView?
    var onPrefer: ((String) -> Void)?
    var onToggle: ((String) -> Void)?
    /// Plate fill behind each row; nil leaves rows flat (popover).
    var plateFill: NSColor?

    init(host: NSView) { self.host = host }

    /// Places rows for `profiles` from `y` downward with `gap` between them; returns the new `y`.
    func place(_ profiles: [LoftProfile], engine: SessionController, x: CGFloat, y: CGFloat,
               width: CGFloat, inset: CGFloat, gap: CGFloat) -> CGFloat {
        var y = y
        var seen = Set<String>()
        for p in profiles {
            seen.insert(p.id)
            let row: SessionRow
            if let r = rows[p.id] {
                row = r
            } else {
                row = SessionRow(frame: .zero)
                row.onPrefer = { [weak self] id in self?.onPrefer?(id) }
                row.onToggle = { [weak self] id in self?.onToggle?(id) }
                rows[p.id] = row
                host?.addSubview(row)
            }
            row.plate.fill = plateFill
            row.fill(p, engine: engine, width: width, inset: inset)
            row.frame.origin = NSPoint(x: x, y: y)
            y += SessionRow.height + gap
        }
        for (id, row) in rows where !seen.contains(id) {
            row.removeFromSuperview()
            rows.removeValue(forKey: id)
        }
        return profiles.isEmpty ? y : y - gap
    }
}
