// Launch-time offer: a newer GitHub release, Update / Later, and “don’t ask again”.
// AppDelegate opens this window when the launch-time update check
// (Engine/Updater.swift) finds a newer release. The window only drives the
// flow: Updater does the download, verification and app swap. Widgets such as
// PillButton and LoftCheck come from App/Chrome.swift.

import AppKit

/// Small loft window when launch check finds a newer DMG.
final class UpdateOfferWindowController: NSWindowController {
    /// The release being offered (version and DMG asset).
    private let offer: Updater.Offer
    /// Own updater instance; it lives as long as this window does.
    private let updater = Updater()
    private let go = PillButton(frame: .zero)
    private let later = GhostButton(frame: .zero)
    private let check = LoftCheck(frame: .zero)
    /// True while a download or install runs; blocks Later and the quiet checkbox.
    private var busy = false

    /// Builds the floating, centered window for `offer`.
    init(offer: Updater.Offer) {
        self.offer = offer
        let w = makeLoftWindow(size: NSSize(width: 360, height: 280))
        w.level = .floating
        super.init(window: w)
        build()
        w.center()
    }

    /// Not used: the window is built in code, never from a nib.
    required init?(coder: NSCoder) { fatalError() }

    /// Gold Update, Later, and the quiet checkbox.
    private func build() {
        let c = loftBody(window!)
        var y: CGFloat = 14
        let title = DrawLab(frame: NSRect(x: 28, y: y, width: 304, height: 28))
        title.text = L10n.t("upd_offer_title")
        title.font = Palette.font(20)
        title.color = Palette.snow
        title.alignment = .center
        title.pillNudge = false
        c.addSubview(title)
        y += 34
        let bodyH = max(32, Palette.textHeight(L10n.t("upd_offer_body"), font: Palette.body(13.5), width: 304))
        let body = Palette.wrap(L10n.t("upd_offer_body"), font: Palette.body(13.5), color: Palette.mute, frame: NSRect(x: 28, y: y, width: 304, height: bodyH), lines: 3)
        body.alignment = .center
        c.addSubview(body)
        y += bodyH + 16
        go.compact = true
        go.frame = NSRect(x: 28, y: y, width: 304, height: 36)
        go.lampOn = true
        go.title = String(format: L10n.t("upd_install"), offer.version)
        go.target = self
        go.action = #selector(goTap)
        c.addSubview(go)
        y += 44
        later.frame = NSRect(x: 28, y: y, width: 304, height: 22)
        later.title = L10n.t("upd_later")
        later.font = Palette.body(12.5)
        later.tone = Palette.mute
        later.alignment = .center
        later.target = self
        later.action = #selector(laterTap)
        c.addSubview(later)
        y += 28
        check.frame = NSRect(x: 28, y: y + 2, width: 18, height: 18)
        check.onChange = { [weak self] on in self?.writeQuiet(on) }
        c.addSubview(check)
        let lab = Palette.label(L10n.t("upd_quiet"), font: Palette.body(13), color: Palette.snow, frame: NSRect(x: 52, y: y, width: 280, height: 18))
        c.addSubview(lab)
        let hit = ActionButton(frame: NSRect(x: 28, y: y, width: 304, height: 22))
        hit.drawsWash = false
        hit.target = self
        hit.action = #selector(quietTap)
        c.addSubview(hit)
        y += 20
        let hintH = max(16, Palette.textHeight(L10n.t("upd_quiet_d"), font: Palette.body(11.5), width: 280))
        let hint = Palette.wrap(L10n.t("upd_quiet_d"), font: Palette.body(11.5), color: Palette.mute, frame: NSRect(x: 52, y: y, width: 280, height: hintH), lines: 2)
        c.addSubview(hint)
        y += hintH + 16
        loftResize(window!, bodyHeight: y)
    }

    /// Click on the "don't ask again" row (box or its label) toggles the box.
    @objc private func quietTap() {
        guard !busy else { return }
        check.on.toggle()
        check.onChange?(check.on)
    }

    /// Later: just close; the offer comes back on the next launch unless the box is ticked.
    @objc private func laterTap() {
        guard !busy else { return }
        window?.close()
    }

    /// Update: start the download unless one already runs.
    @objc private func goTap() {
        if busy { return }
        startInstall()
    }

    /// Quiet on → launch check off. The box is the same pref as Settings.
    private func writeQuiet(_ on: Bool) {
        guard let store = AppDelegate.shared?.engine.store else { return }
        store.checkUpdates = !on
        store.savePrefs()
        AppDelegate.shared?.settingsWCReload()
    }

    /// Updates the main button's text, gold lamp and enabled state. Later stays
    /// hidden while `busy`.
    private func setBtn(_ title: String, lamp: Bool, enabled: Bool) {
        go.title = title
        go.lampOn = lamp
        go.isEnabled = enabled
        go.needsDisplay = true
        later.isHidden = busy
    }

    /// Runs `Updater.install`: shows download percent, then "installing", and on
    /// success quits the app so the relaunch script can replace the bundle. On
    /// failure the button becomes enabled again with the error text, so a click retries.
    private func startInstall() {
        busy = true
        later.isHidden = true
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
                self.later.isHidden = false
                self.setBtn(self.failText(err), lamp: false, enabled: true)
                return
            }
            self.setBtn(L10n.t("upd_relaunch"), lamp: false, enabled: false)
            // After the GCD drain returns; terminate from a drain deadlocks halt's main.async reply.
            NSApp.perform(#selector(NSApplication.terminate(_:)), with: nil, afterDelay: 0)
        })
    }

    /// Maps an updater error to a short localized message for the button.
    private func failText(_ e: Error) -> String {
        guard let f = e as? Updater.Fail else { return L10n.t("upd_err") }
        switch f {
        case .noAsset: return L10n.t("upd_err_asset")
        case .checksum: return L10n.t("upd_err_sum")
        case .mount: return L10n.t("upd_err_mount")
        case .copy: return L10n.t("upd_err_copy")
        case .network: return L10n.t("upd_err_net")
        case .badHTTP: return L10n.t("upd_err")
        }
    }
}
