// About window: mark, version, protocol blurb, credit, GitHub update.

import AppKit

/// About window: mark, version, protocol rows, blurb, credit, and updates.
final class AboutWindowController: NSWindowController {
    private let software: String
    private let updater = Updater()
    private let statusLab = NSTextField(labelWithString: "")
    private let actionBtn = PillButton(frame: .zero)
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
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Asks GitHub for a newer DMG. Skips while a check or install is running.
    func checkNow() {
        guard !busy else { return }
        busy = true
        offer = nil
        statusLab.stringValue = L10n.t("upd_checking")
        statusLab.textColor = Palette.mute
        actionBtn.title = L10n.t("upd_check")
        actionBtn.lampOn = false
        actionBtn.isEnabled = false
        actionBtn.isHidden = false
        updater.check(current: software) { [weak self] result in
            guard let self = self else { return }
            self.busy = false
            switch result {
            case .failure(let e):
                self.statusLab.stringValue = self.failText(e)
                self.statusLab.textColor = Palette.err
                self.actionBtn.title = L10n.t("upd_check")
                self.actionBtn.lampOn = false
                self.actionBtn.isEnabled = true
                self.actionBtn.isHidden = false
            case .success(let offer):
                if let offer = offer {
                    self.offer = offer
                    self.statusLab.stringValue = String(format: L10n.t("upd_available"), offer.version)
                    self.statusLab.textColor = Palette.snow
                    self.actionBtn.title = String(format: L10n.t("upd_install"), offer.version)
                    self.actionBtn.lampOn = true
                    self.actionBtn.isEnabled = true
                    self.actionBtn.isHidden = false
                } else {
                    self.statusLab.stringValue = L10n.t("upd_current")
                    self.statusLab.textColor = Palette.mute
                    self.actionBtn.title = L10n.t("upd_check")
                    self.actionBtn.lampOn = false
                    self.actionBtn.isEnabled = true
                    self.actionBtn.isHidden = false
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

        let kick = Palette.label(L10n.t("protos"), font: Palette.body(11), color: Palette.mute, frame: NSRect(x: 20, y: 148, width: 320, height: 14))
        kick.alignment = .center
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

        let blurb = NSTextField(wrappingLabelWithString: L10n.t("blurb"))
        blurb.font = Palette.body(13.5)
        blurb.textColor = Palette.stone
        blurb.alignment = .center
        blurb.frame = NSRect(x: 28, y: blurbY, width: 304, height: blurbH)

        statusLab.font = Palette.body(12)
        statusLab.textColor = Palette.mute
        statusLab.alignment = .center
        statusLab.frame = NSRect(x: 20, y: 284, width: 320, height: 16)
        statusLab.stringValue = L10n.t("upd_checking")
        Palette.fitLine(statusLab)

        actionBtn.compact = true
        actionBtn.frame = NSRect(x: 28, y: 308, width: 304, height: 36)
        actionBtn.title = L10n.t("upd_check")
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

        for v in [mark, title, ver, blurb, statusLab, actionBtn, left, heart, right] as [NSView] { c.addSubview(v) }
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
        actionBtn.isEnabled = false
        actionBtn.isHidden = true
        statusLab.textColor = Palette.mute
        statusLab.stringValue = String(format: L10n.t("upd_downloading"), 0)
        updater.install(offer, progress: { [weak self] p in
            guard let self = self else { return }
            if p >= 1 {
                self.statusLab.stringValue = L10n.t("upd_installing")
            } else {
                self.statusLab.stringValue = String(format: L10n.t("upd_downloading"), Int((p * 100).rounded()))
            }
        }, done: { [weak self] err in
            guard let self = self else { return }
            if let err = err {
                self.busy = false
                self.statusLab.stringValue = self.failText(err)
                self.statusLab.textColor = Palette.err
                self.actionBtn.title = String(format: L10n.t("upd_install"), offer.version)
                self.actionBtn.lampOn = true
                self.actionBtn.isEnabled = true
                self.actionBtn.isHidden = false
                return
            }
            self.statusLab.stringValue = L10n.t("upd_relaunch")
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
