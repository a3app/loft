// About window: mark, version, protocol blurb, credit, GitHub update.

import AppKit

/// About window: mark, version, protocol rows, blurb, credit, and updates.
final class AboutWindowController: NSWindowController {
    private let software: String
    private let updater = Updater()
    private let actionBtn = PillButton(frame: .zero)
    private let kick = NSTextField(labelWithString: "")
    private let blurb = NSTextField(wrappingLabelWithString: "")
    private var offer: Updater.Offer?
    private var busy = false

    /// Builds the bordered about window around `software` (bundle version).
    init(software: String) {
        self.software = software
        let w = makeLoftWindow(size: NSSize(width: 360, height: 448))
        w.level = .normal
        super.init(window: w)
        build()
        w.center()
        reload()
        checkNow()
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Starts the GitHub check whenever this window is shown.
    override func showWindow(_ sender: Any?) {
        super.showWindow(sender)
        reload()
        checkNow()
    }

    /// Captions follow the current UI language.
    @objc func reload() {
        kick.stringValue = L10n.t("protos")
        kick.alignment = .center
        blurb.stringValue = L10n.t("blurb")
        blurb.alignment = .center
    }

    /// Asks GitHub for a newer DMG. Skips while a check or install is running.
    func checkNow() {
        guard !busy else { return }
        busy = true
        offer = nil
        setBtn(L10n.t("upd_checking"), lamp: false, enabled: false)
        updater.check(current: software) { [weak self] result in
            guard let self = self else { return }
            self.busy = false
            switch result {
            case .failure(let e):
                self.setBtn(self.failText(e), lamp: false, enabled: true)
            case .success(let offer):
                if let offer = offer {
                    self.offer = offer
                    self.setBtn(String(format: L10n.t("upd_install"), offer.version), lamp: true, enabled: true)
                } else {
                    self.setBtn(L10n.t("upd_current"), lamp: false, enabled: true)
                }
            }
        }
    }

    private func build() {
        let c = loftBody(window!)
        let mark = MarkView(frame: NSRect(x: 144, y: 8, width: 72, height: 72))
        mark.state = .on
        let title = Palette.label("loft", font: Palette.font(22), color: Palette.snow, frame: NSRect(x: 20, y: 84, width: 320, height: 28))
        title.alignment = .center
        let ver = Palette.label(software, font: Palette.body(12.5), color: Palette.mute, frame: NSRect(x: 20, y: 112, width: 320, height: 18))
        ver.alignment = .center

        kick.font = Palette.body(11)
        kick.textColor = Palette.mute
        kick.backgroundColor = .clear
        kick.isBezeled = false
        kick.drawsBackground = false
        kick.isEditable = false
        kick.isSelectable = false
        kick.alignment = .center
        kick.frame = NSRect(x: 20, y: 148, width: 320, height: 14)
        c.addSubview(kick)

        let keyFont = Palette.body(12.5)
        let valFont = Palette.mono(12)
        let keys = ["loft/1.0", "SSTP", "VLESS"]
        let vals = [
            LoftProfile.frontKinds.joined(separator: " · ") + " · mask",
            "ppp · mschapv2 · pap",
            "tcp · tls",
        ]
        let pad: CGFloat = 16
        let specGap: CGFloat = 10
        let keyW = max(70, Palette.textWidth("loft/1.0", font: keyFont) + 2)
        let valW = 360 - pad * 2 - keyW - specGap
        let originX: CGFloat = pad
        for i in 0..<keys.count {
            let y = CGFloat(166 + i * 18)
            let k = Palette.label(keys[i], font: keyFont, color: Palette.mute, frame: NSRect(x: originX, y: y, width: keyW, height: 16))
            k.alignment = .right
            let v = Palette.label(vals[i], font: valFont, color: Palette.snow, frame: NSRect(x: originX + keyW + specGap, y: y, width: valW, height: 16))
            c.addSubview(k)
            c.addSubview(v)
        }

        let bodyH: CGFloat = 420
        let madeH: CGFloat = 20
        let madeY = bodyH - 14 - madeH
        let blurbH: CGFloat = 48
        let blurbY: CGFloat = 228

        blurb.font = Palette.body(13.5)
        blurb.textColor = Palette.stone
        blurb.backgroundColor = .clear
        blurb.isBezeled = false
        blurb.drawsBackground = false
        blurb.isEditable = false
        blurb.isSelectable = false
        blurb.alignment = .center
        blurb.maximumNumberOfLines = 3
        blurb.preferredMaxLayoutWidth = 304
        blurb.lineBreakMode = .byWordWrapping
        blurb.cell?.wraps = true
        blurb.frame = NSRect(x: 28, y: blurbY, width: 304, height: blurbH)

        // Middle of the gap between blurb (ends 276) and the colophon (madeY).
        let btnH: CGFloat = 36
        let btnY = round(blurbY + blurbH + (madeY - (blurbY + blurbH) - btnH) / 2)
        actionBtn.compact = true
        actionBtn.frame = NSRect(x: 28, y: btnY, width: 304, height: btnH)
        actionBtn.title = L10n.t("upd_checking")
        actionBtn.lampOn = false
        actionBtn.isEnabled = false
        actionBtn.target = self
        actionBtn.action = #selector(tapUpdate)

        let madeFont = Palette.body(12)
        let leftStr = "Made with love"
        let rightStr = "by A³"
        let heartW: CGFloat = 12
        let heartH: CGFloat = 10
        let gap: CGFloat = 4
        let leftW = Palette.textWidth(leftStr, font: madeFont)
        let rightW = Palette.textWidth(rightStr, font: madeFont)
        let rowW = leftW + gap + heartW + gap + rightW
        var x = round((360 - rowW) / 2)
        let left = Palette.label(leftStr, font: madeFont, color: Palette.mute, frame: NSRect(x: x, y: madeY, width: leftW, height: madeH))
        x += leftW + gap
        let heart = HeartView(frame: NSRect(x: x - 2, y: madeY + round((madeH - heartH) / 2), width: heartW, height: heartH))
        x += heartW + gap
        let right = Palette.label(rightStr, font: madeFont, color: Palette.mute, frame: NSRect(x: x - 1, y: madeY, width: rightW, height: madeH))

        for v in [mark, title, ver, blurb, actionBtn, left, heart, right] as [NSView] { c.addSubview(v) }
    }

    /// Title, lamp, and enabled on the one update pill. No status line above it.
    private func setBtn(_ title: String, lamp: Bool, enabled: Bool) {
        actionBtn.title = title
        actionBtn.lampOn = lamp
        actionBtn.isEnabled = enabled
        actionBtn.isHidden = false
        actionBtn.needsDisplay = true
    }

    @objc private func tapUpdate() {
        if let offer = offer {
            startInstall(offer)
            return
        }
        checkNow()
    }

    private func startInstall(_ offer: Updater.Offer) {
        guard !busy else { return }
        busy = true
        setBtn(String(format: L10n.t("upd_downloading"), 0), lamp: false, enabled: false)
        updater.install(offer, progress: { [weak self] p in
            guard let self = self else { return }
            if p >= 1 {
                self.setBtn(L10n.t("upd_installing"), lamp: false, enabled: false)
            } else {
                self.setBtn(String(format: L10n.t("upd_downloading"), Int((p * 100).rounded())), lamp: false, enabled: false)
            }
        }, done: { [weak self] err in
            guard let self = self else { return }
            if let err = err {
                self.busy = false
                self.setBtn(self.failText(err), lamp: false, enabled: true)
                return
            }
            self.setBtn(L10n.t("upd_relaunch"), lamp: false, enabled: false)
            NSApp.terminate(nil)
        })
    }

    private func failText(_ e: Error) -> String {
        guard let f = e as? Updater.Fail else { return L10n.t("upd_err") }
        switch f {
        case .noAsset: return L10n.t("upd_err_asset")
        case .checksum: return L10n.t("upd_err_sum")
        case .mount: return L10n.t("upd_err_mount")
        case .copy: return L10n.t("upd_err_copy")
        case .network, .badHTTP: return L10n.t("upd_err_net")
        }
    }
}
