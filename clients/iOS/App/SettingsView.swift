/*
 SettingsView.swift - the Settings tab and its sub-pages (About, Journal),
 plus the small reusable pieces they are built from: a settings row, the
 loft-style switch and a Back frame. Changes go into LoftPrefs
 (App/LoftPrefs.swift); LoftShell (App/HomeView.swift) saves them and passes
 hold / On Demand to TunnelControl (App/TunnelControl.swift).
*/
import SwiftUI

/// Sub-pages opened from Settings.
enum SettingsPage: Hashable {
    case lists, journal, about
}

/// DESIGN Settings. Toggles write `LoftPrefs`; hold / demand / Reset routes go to TunnelControl.
struct SettingsView: View {
    /// `onOpen` opens a sub-page, `onResetRoutes` runs the Reset routes action.
    @Binding var prefs: LoftPrefs
    var onOpen: (SettingsPage) -> Void = { _ in }
    var onResetRoutes: () -> Void = {}
    @Environment(\.locale) private var locale
    /// Shows the "reset to defaults" confirmation.
    @State private var confirmDefaults = false

    /// Three groups (general, routing, client) and the links row at the bottom.
    var body: some View {
        ScrollView(.vertical, showsIndicators: false) {
            VStack(alignment: .leading, spacing: 0) {
                header
                group("setGen")
                toggle("simple", "simpleD", $prefs.simple)
                chips("rateUnit", "rateUnitD", [
                    ("uKbit", prefs.bits),
                    ("uKb", !prefs.bits),
                ]) { prefs.bits = $0 == 0 }
                chips("sparkK", "sparkD", [
                    ("sparkLines", prefs.spark == .lines),
                    ("sparkBars", prefs.spark == .bars),
                ]) { prefs.spark = $0 == 0 ? .lines : .bars }
                chips("lang", "langD", [
                    ("langEn", L10n.lang(locale) == "en"),
                    ("langRu", L10n.lang(locale) == "ru"),
                    ("langZh", L10n.lang(locale) == "zh"),
                ]) { i in
                    prefs.lang = ["en", "ru", "zh"][i]
                }
                toggle("diag", "diagD", $prefs.diag)

                group("setRoute")
                toggle("defRoute", "defRouteD", $prefs.defaultRoute)
                toggle("hold", "holdD", $prefs.hold)
                splitRow

                group("setClient")
                toggle("auto", "autoD", $prefs.autoConnect)
                toggle("reconnect", "reconnectD", $prefs.reconnect)
                toggle("demand", "demandD", $prefs.demand)
                trustedField
                toggle("extIp", "extIpD", $prefs.showExtIP)

                links
            }
            .padding(.horizontal, 16)
            .padding(.top, 4)
            .padding(.bottom, 20)
        }
        .confirmationDialog(L10n.t("cfDefT", locale), isPresented: $confirmDefaults, titleVisibility: .visible) {
            Button(L10n.t("cfDefGo", locale), role: .destructive) { prefs = .default }
            Button(L10n.t("cancel", locale), role: .cancel) {}
        } message: {
            Text(L10n.t("cfDefS", locale))
        }
    }

    /// Title and the Reset button. Reset turns off hold and On Demand and
    /// asks the shell to stop the VPN, so normal routing comes back.
    private var header: some View {
        HStack(alignment: .center, spacing: 8) {
            Text(L10n.t("settings", locale))
                .font(LoftTheme.demi(20))
                .foregroundStyle(Color.loftSnow)
                .tracking(-0.5)
                .frame(maxWidth: .infinity, alignment: .leading)
            Button(L10n.t("reset", locale)) {
                prefs.hold = false
                prefs.demand = false
                onResetRoutes()
            }
                .font(LoftTheme.demi(10))
                .foregroundStyle(Color.loftSnow)
                .padding(.horizontal, 8)
                .padding(.vertical, 6)
                .background(Color.loftGhost)
                .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                .buttonStyle(.plain)
        }
        .padding(.bottom, 8)
    }

    /// Upper-case group heading.
    private func group(_ key: String) -> some View {
        Text(L10n.t(key, locale))
            .font(LoftTheme.demi(11))
            .tracking(0.9)
            .textCase(.uppercase)
            .foregroundStyle(Color.loftMute)
            .padding(.top, 14)
            .padding(.bottom, 2)
    }

    /// A row with a switch; title and subtitle are L10n keys.
    private func toggle(_ title: String, _ sub: String, _ on: Binding<Bool>) -> some View {
        LoftSetRow(title: L10n.t(title, locale), subtitle: L10n.t(sub, locale)) {
            LoftToggle(on: on)
        }
    }

    /// A row with a choice of chips. Each item is (L10n key, selected);
    /// `pick` gets the tapped index.
    private func chips(_ title: String, _ sub: String, _ items: [(String, Bool)], pick: @escaping (Int) -> Void) -> some View {
        LoftSetRow(title: L10n.t(title, locale), subtitle: L10n.t(sub, locale)) {
            HStack(spacing: 6) {
                ForEach(Array(items.enumerated()), id: \.offset) { i, item in
                    Button {
                        pick(i)
                    } label: {
                        Text(L10n.t(item.0, locale))
                            .font(LoftTheme.demi(12))
                            .padding(.horizontal, 8)
                            .padding(.vertical, 7)
                            .foregroundStyle(item.1 ? Color.loftInk : Color.loftStone)
                            .background(item.1 ? Color.loftLamp : Color.loftField)
                            .clipShape(Capsule())
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    /// Split routing switch plus a link to the routing lists page.
    private var splitRow: some View {
        VStack(alignment: .leading, spacing: 0) {
            LoftSetRow(title: L10n.t("split", locale), subtitle: L10n.t("splitD", locale)) {
                LoftToggle(on: $prefs.split)
            }
            Button(L10n.t("split_lists", locale)) { onOpen(.lists) }
                .font(LoftTheme.demi(12))
                .foregroundStyle(Color.loftLamp)
                .buttonStyle(.plain)
                .padding(.bottom, 10)
        }
    }

    /// Text field for trusted Wi-Fi names (comma-separated), used by On Demand.
    private var trustedField: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(L10n.t("trusted", locale))
                .font(LoftTheme.body(12))
                .foregroundStyle(Color.loftMute)
            TextField("Home-WiFi, Office", text: $prefs.trustedWiFi)
                .font(LoftTheme.body(14))
                .foregroundStyle(Color.loftSnow)
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
                .background(Color.loftField)
                .overlay(
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .stroke(Color.loftSlate.opacity(0.7), lineWidth: 1)
                )
                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        }
        .padding(.top, 10)
        .padding(.bottom, 4)
    }

    /// Bottom links: reset to defaults, Journal, About.
    private var links: some View {
        HStack(spacing: 0) {
            link("defaults") { confirmDefaults = true }
            link("journal") { onOpen(.journal) }
            link("about") { onOpen(.about) }
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 16)
    }

    /// One small grey text link.
    private func link(_ key: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(L10n.t(key, locale))
                .font(LoftTheme.medium(11.5))
                .foregroundStyle(Color.loftMute)
                .lineLimit(1)
                .minimumScaleFactor(0.85)
                .frame(maxWidth: .infinity)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

/// One settings row: title, grey explanation, and a control on the right.
struct LoftSetRow<Trailing: View>: View {
    var title: String
    var subtitle: String
    @ViewBuilder var trailing: () -> Trailing

    var body: some View {
        HStack(alignment: .center, spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(LoftTheme.body(14))
                    .foregroundStyle(Color.loftSnow)
                Text(subtitle)
                    .font(LoftTheme.body(11))
                    .foregroundStyle(Color.loftMute)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            trailing()
        }
        .padding(.vertical, 12)
        .overlay(alignment: .bottom) { Color.loftLine.frame(height: 1) }
    }
}

/// The loft-styled on/off switch.
struct LoftToggle: View {
    @Binding var on: Bool

    var body: some View {
        Button {
            on.toggle()
        } label: {
            Capsule()
                .fill(on ? Color.loftLamp : Color.loftSlate)
                .frame(width: 42, height: 26)
                .overlay(alignment: on ? .trailing : .leading) {
                    Circle()
                        .fill(Color.loftSnow)
                        .frame(width: 22, height: 22)
                        .padding(2)
                }
        }
        .buttonStyle(.plain)
        .accessibilityValue(on ? "on" : "off")
    }
}

/// A sub-page frame with a Back button and a title.
struct LoftBackChrome<Content: View>: View {
    var titleKey: String
    var onBack: () -> Void
    @ViewBuilder var content: () -> Content
    @Environment(\.locale) private var locale

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button(L10n.t("backSet", locale), action: onBack)
                .font(LoftTheme.medium(13))
                .foregroundStyle(Color.loftLamp)
                .buttonStyle(.plain)
                .padding(.horizontal, 16)
                .padding(.top, 4)
                .padding(.bottom, 6)
            content()
        }
    }
}

/// About page: app version and the supported protocols.
struct AboutView: View {
    var onBack: () -> Void
    @Environment(\.locale) private var locale
    /// App version from Info.plist, or a fixed fallback.
    private var version: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "1.0.28"
    }

    var body: some View {
        LoftBackChrome(titleKey: "about", onBack: onBack) {
            ScrollView {
                VStack(spacing: 0) {
                    LoftMark(on: true, size: 72)
                        .padding(.top, 8)
                        .padding(.bottom, 10)
                    Text("loft")
                        .font(LoftTheme.demi(22))
                        .foregroundStyle(Color.loftSnow)
                        .tracking(-0.8)
                    Text(version)
                        .font(LoftTheme.body(12.5))
                        .foregroundStyle(Color.loftMute)
                        .padding(.top, 4)
                    Text(L10n.t("protos", locale))
                        .font(LoftTheme.body(11))
                        .foregroundStyle(Color.loftMute)
                        .padding(.top, 18)
                        .padding(.bottom, 8)
                    proto("loft/1.0", "get · ws · h2 · quic · h3 · l3 · mask")
                    proto("SSTP", "ppp · mschapv2 · pap")
                    proto("Trojan", "tcp · tls · reality · xhttp")
                    proto("VLESS", "tcp · tls")
                    Text(L10n.t("blurb", locale))
                        .font(LoftTheme.body(13.5))
                        .foregroundStyle(Color.loftSnow)
                        .multilineTextAlignment(.center)
                        .padding(.top, 16)
                    Text("Made with love by A³")
                        .font(LoftTheme.body(12))
                        .foregroundStyle(Color.loftMute)
                        .padding(.top, 14)
                        .padding(.bottom, 20)
                }
                .frame(maxWidth: .infinity)
                .padding(.horizontal, 16)
            }
        }
    }

    /// One protocol line: name and its variants.
    private func proto(_ k: String, _ v: String) -> some View {
        HStack {
            Text(k).foregroundStyle(Color.loftSnow)
            Spacer()
            Text(v).foregroundStyle(Color.loftMute)
        }
        .font(LoftTheme.mono(11))
        .padding(.vertical, 6)
        .overlay(alignment: .bottom) { Color.loftLine.frame(height: 1) }
    }
}

/// Connection log page. The log text is still a fixed sample from the mock.
struct JournalView: View {
    var onBack: () -> Void
    @Environment(\.locale) private var locale

    var body: some View {
        LoftBackChrome(titleKey: "journal", onBack: onBack) {
            VStack(alignment: .leading, spacing: 0) {
                Text(L10n.t("journal", locale))
                    .font(LoftTheme.demi(22))
                    .foregroundStyle(Color.loftSnow)
                    .padding(.horizontal, 16)
                Text(L10n.t("journalD", locale))
                    .font(LoftTheme.body(12.5))
                    .foregroundStyle(Color.loftMute)
                    .padding(.horizontal, 16)
                    .padding(.top, 6)
                    .padding(.bottom, 12)
                Text("""
                get   cdn.example.com  hello ok
                status  ip=10.8.0.14  mtu=1280  front=get
                tun   packet-tunnel  up
                hold  armed
                """)
                .font(LoftTheme.mono(11))
                .foregroundStyle(Color.loftStone)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(10)
                .background(Color.loftField)
                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                .padding(.horizontal, 16)
                HStack(spacing: 6) {
                    ghost("copy")
                    ghost("shareBtn")
                    ghost("clearLog")
                }
                .padding(.horizontal, 16)
                .padding(.top, 10)
                Spacer()
            }
        }
    }

    /// A button-looking label (Copy, Share, Clear); it has no action yet.
    private func ghost(_ key: String) -> some View {
        Text(L10n.t(key, locale))
            .font(LoftTheme.demi(12))
            .foregroundStyle(Color.loftSnow)
            .frame(maxWidth: .infinity)
            .padding(.vertical, 10)
            .background(Color.loftGhost)
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}

#Preview("Settings") {
    LoftShell(tab: .settings, followTunnel: false)
}

#Preview("Settings · ru") {
    LoftShell(tab: .settings, prefs: LoftPrefs(lang: "ru"), followTunnel: false)
}

#Preview("About") {
    ZStack {
        Color.loftPhone.ignoresSafeArea()
        AboutView(onBack: {})
    }
    .preferredColorScheme(.dark)
}
