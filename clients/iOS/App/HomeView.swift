import SwiftUI

/// Root chrome: Home, Connections stub, Settings. Tab bar matches the mock.
struct LoftShell: View {
    @State private var tab: LoftTab
    @State private var home: HomeSnapshot
    @State private var prefs: LoftPrefs
    @State private var settingsPage: SettingsPage?
    @StateObject private var tunnel = TunnelControl()
    /// Canvas previews keep the snapshot. The live app follows Packet Tunnel status.
    var followTunnel: Bool

    init(initial: HomeSnapshot = .off, tab: LoftTab = .home, prefs: LoftPrefs? = nil, followTunnel: Bool = true) {
        _home = State(initialValue: initial)
        _tab = State(initialValue: tab)
        _prefs = State(initialValue: prefs ?? LoftPrefs.load())
        self.followTunnel = followTunnel
    }

    var body: some View {
        ZStack {
            Color.loftPhone.ignoresSafeArea()
            switch tab {
            case .home:
                HomeView(snapshot: $home, prefs: prefs, tunnel: tunnel, followTunnel: followTunnel) { tab = .connections }
            case .connections:
                LoftStubRoom(titleKey: "conns")
            case .settings:
                settingsRoom
            }
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            LoftTabBar(tab: $tab)
        }
        .environment(\.locale, prefs.locale)
        .preferredColorScheme(.dark)
        .onChange(of: prefs) { p in
            p.save()
            if followTunnel {
                Task { await tunnel.applyPolicy(p) }
            }
        }
        .onChange(of: tunnel.up) { _ in syncHome() }
        .onChange(of: tunnel.busy) { _ in syncHome() }
        .onChange(of: tunnel.held) { _ in syncHome() }
        .task {
            if followTunnel {
                prefs.save()
                await tunnel.applyPolicy(prefs)
                await tunnel.refresh()
                syncHome()
            }
        }
    }

    /// Maps the system VPN state onto the Home mode. A parked (held) tunnel
    /// shows as off, because it carries no traffic.
    private func syncHome() {
        guard followTunnel else { return }
        if tunnel.busy {
            home.mode = .connecting
        } else if tunnel.held {
            home.mode = .off
        } else if tunnel.up {
            home.mode = prefs.simple ? .simple : .on
        } else {
            home.mode = .off
        }
    }

    @ViewBuilder
    private var settingsRoom: some View {
        switch settingsPage {
        case .none:
            SettingsView(prefs: $prefs, onOpen: { settingsPage = $0 }, onResetRoutes: {
                Task { await tunnel.resetRoutes() }
            })
        case .lists:
            LoftBackChrome(titleKey: "split_lists", onBack: { settingsPage = nil }) {
                LoftStubRoom(titleKey: "split_lists")
            }
        case .journal:
            JournalView(onBack: { settingsPage = nil })
        case .about:
            AboutView(onBack: { settingsPage = nil })
        }
    }
}

enum LoftTab: String, Hashable {
    case home, connections, settings
}

/// DESIGN Home. Live Connect talks to Packet Tunnel; Canvas previews keep the snapshot.
struct HomeView: View {
    @Binding var snapshot: HomeSnapshot
    var prefs: LoftPrefs = .default
    @ObservedObject var tunnel: TunnelControl
    var followTunnel: Bool = true
    var onPill: () -> Void = {}
    @Environment(\.locale) private var locale

    var body: some View {
        ScrollView(.vertical, showsIndicators: false) {
            VStack(spacing: 0) {
                hero
                LoftGoButton(title: actionTitle, kind: actionKind) {
                    tapPrimary()
                }
                .padding(.top, 14)

                if snapshot.showPlates && !prefs.simple {
                    plates
                        .padding(.top, 16)
                }
                if snapshot.showSpark && !prefs.simple {
                    SparkView(
                        down: snapshot.sparkDown,
                        up: snapshot.sparkUp,
                        handshake: snapshot.mode == .connecting ? L10n.t("handshake", locale) : nil,
                        downText: "↓ \(snapshot.downMbit) \(rateUnit)",
                        upText: "↑ \(snapshot.upMbit) \(rateUnit)",
                        showRates: snapshot.showRates,
                        style: prefs.spark
                    )
                    .padding(.top, 14)
                }
                if snapshot.showIPs && !prefs.simple {
                    ipRows
                        .padding(.top, 12)
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, 4)
            .padding(.bottom, 12)
        }
    }

    private var hero: some View {
        VStack(spacing: 0) {
            LoftMark(on: snapshot.lampOn)
                .padding(.bottom, 8)
            Text("loft")
                .font(LoftTheme.demi(LoftTheme.title))
                .foregroundStyle(Color.loftSnow)
                .tracking(-0.8)
            Button(action: onPill) {
                HStack(spacing: 8) {
                    Circle()
                        .fill(snapshot.lampOn ? Color.loftLamp : Color.loftMute)
                        .frame(width: 7, height: 7)
                    Text(pillText)
                        .font(LoftTheme.body(LoftTheme.pill))
                        .foregroundStyle(Color.loftMute)
                }
                .padding(.leading, 8)
                .padding(.trailing, 12)
                .padding(.vertical, 4)
                .background(Color.loftField)
                .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
            }
            .buttonStyle(.plain)
            .padding(.top, 10)
        }
        .frame(maxWidth: .infinity)
    }

    private var plates: some View {
        HStack(spacing: 6) {
            ForEach(plateItems, id: \.key) { item in
                VStack(alignment: .leading, spacing: 6) {
                    Text(item.key)
                        .font(LoftTheme.demi(LoftTheme.plateKey))
                        .tracking(0.8)
                        .foregroundStyle(Color.loftMute)
                    Text(item.value)
                        .font(LoftTheme.demi(LoftTheme.plateVal))
                        .foregroundStyle(Color.loftSnow)
                        .lineLimit(1)
                }
                .padding(.horizontal, 8)
                .padding(.top, 8)
                .padding(.bottom, 10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.loftField)
                .clipShape(RoundedRectangle(cornerRadius: LoftTheme.plateCorner, style: .continuous))
            }
        }
    }

    private var rateUnit: String {
        prefs.bits ? L10n.t("mbit", locale) : L10n.t("kbs", locale)
    }

    private var ipRows: some View {
        VStack(spacing: 0) {
            if prefs.showExtIP {
                ipRow(L10n.t("extIpK", locale), snapshot.externalIP)
            }
            ipRow(L10n.t("overIpK", locale), snapshot.overlayIP)
            ipRow(L10n.t("routeK", locale), snapshot.route)
        }
        .overlay(alignment: .top) {
            Color.loftLine.frame(height: 1)
        }
    }

    private func ipRow(_ key: String, _ value: String) -> some View {
        HStack {
            Text(key)
                .foregroundStyle(Color.loftMute)
            Spacer()
            Text(value)
                .font(LoftTheme.mono(12))
                .foregroundStyle(Color.loftSnow)
        }
        .font(LoftTheme.body(12.5))
        .padding(.vertical, 8)
        .padding(.horizontal, 2)
        .overlay(alignment: .bottom) {
            Color.loftLine.frame(height: 1)
        }
    }

    private var pillText: String {
        switch snapshot.mode {
        case .on, .simple:
            return String(format: L10n.t("pill_on", locale), snapshot.profile, snapshot.uptime)
        case .connecting:
            return String(format: L10n.t("pill_busy", locale), snapshot.profile)
        case .off:
            return String(format: L10n.t("pill_off", locale), snapshot.profile)
        }
    }

    private var actionTitle: String {
        switch snapshot.mode {
        case .connecting: return L10n.t("cancel", locale)
        case .on, .simple: return L10n.t("disconnect", locale)
        case .off: return L10n.t("connect", locale)
        }
    }

    private var actionKind: LoftGoButton.Kind {
        switch snapshot.mode {
        case .connecting: return .ghost
        case .on, .simple: return .danger
        case .off: return .go
        }
    }

    private func tapPrimary() {
        if followTunnel {
            if snapshot.mode == .connecting || tunnel.busy {
                tunnel.disconnect()
            } else if snapshot.isLive || tunnel.up {
                tunnel.disconnect()
            } else {
                tunnel.connect()
            }
            return
        }
        if snapshot.mode == .connecting {
            snapshot.cancel()
        } else {
            snapshot.tapPrimary()
        }
    }

    /// The three info plates under the button. The values are fixed labels
    /// per protocol, not read from the session.
    private var plateItems: [(key: String, value: String)] {
        let live = snapshot.isLive
        switch snapshot.kind {
        case .loft:
            return [
                (L10n.t("protoK", locale), "loft/1.0"),
                (L10n.t("frontK", locale), live ? "GET" : L10n.t("kindAuto", locale)),
                (L10n.t("optK", locale), live ? "mux · mask" : "—"),
            ]
        case .sstp:
            return [
                (L10n.t("protoK", locale), "SSTP"),
                (L10n.t("transK", locale), "PPP"),
                (L10n.t("authK", locale), "CHAP"),
            ]
        case .vless:
            return [
                (L10n.t("protoK", locale), "VLESS"),
                (L10n.t("transK", locale), "TCP"),
                (L10n.t("secK", locale), "REALITY"),
            ]
        case .trojan:
            return [
                (L10n.t("protoK", locale), "Trojan"),
                (L10n.t("transK", locale), "TCP"),
                (L10n.t("secK", locale), "TLS"),
            ]
        }
    }
}

/// The big full-width action button (Connect, Cancel, Disconnect).
struct LoftGoButton: View {
    enum Kind { case go, ghost, danger }
    var title: String
    var kind: Kind
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(title)
                .font(LoftTheme.demi(LoftTheme.go))
                .frame(maxWidth: .infinity)
                .padding(.vertical, 12)
                .padding(.horizontal, 16)
                .foregroundStyle(fg)
                .background(bg)
                .clipShape(RoundedRectangle(cornerRadius: LoftTheme.goCorner, style: .continuous))
        }
        .buttonStyle(.plain)
    }

    private var fg: Color {
        switch kind {
        case .go: return .loftInk
        case .ghost: return .loftSnow
        case .danger: return .loftDangerText
        }
    }

    private var bg: Color {
        switch kind {
        case .go: return .loftLamp
        case .ghost: return .loftGhost
        case .danger: return .loftDangerFill
        }
    }
}

/// Bottom tab bar: Home, Connections, Settings.
struct LoftTabBar: View {
    @Binding var tab: LoftTab
    @Environment(\.locale) private var locale

    var body: some View {
        HStack(spacing: 0) {
            tabBtn(.home, "tabHome", LoftTabGlyph.home)
            tabBtn(.connections, "conns", LoftTabGlyph.list)
            tabBtn(.settings, "settings", LoftTabGlyph.settings)
        }
        .padding(.top, 6)
        .padding(.horizontal, 8)
        .padding(.bottom, 8)
        .background(Color.loftPhone)
        .overlay(alignment: .top) { Color.loftLine.frame(height: 1) }
    }

    private func tabBtn(_ id: LoftTab, _ key: String, _ glyph: LoftTabGlyph) -> some View {
        let on = tab == id
        return Button {
            tab = id
        } label: {
            VStack(spacing: 3) {
                glyph.path
                    .stroke(
                        on ? Color.loftLamp : Color.loftMute,
                        style: StrokeStyle(lineWidth: 1.8, lineCap: .round, lineJoin: .round)
                    )
                    .frame(width: 22, height: 22)
                Text(L10n.t(key, locale))
                    .font(LoftTheme.demi(LoftTheme.tab))
            }
            .foregroundStyle(on ? Color.loftLamp : Color.loftMute)
            .frame(maxWidth: .infinity)
            .padding(.vertical, 2)
        }
        .buttonStyle(.plain)
    }
}

/// Tab icons drawn as paths on a 22×22 grid.
private enum LoftTabGlyph {
    case home, list, settings

    var path: Path {
        switch self {
        case .home:
            var p = Path()
            p.move(to: CGPoint(x: 3.7, y: 10.1))
            p.addLine(to: CGPoint(x: 11, y: 3.7))
            p.addLine(to: CGPoint(x: 18.3, y: 10.1))
            p.addLine(to: CGPoint(x: 18.3, y: 18.3))
            p.addLine(to: CGPoint(x: 13.8, y: 18.3))
            p.addLine(to: CGPoint(x: 13.8, y: 12.8))
            p.addLine(to: CGPoint(x: 8.2, y: 12.8))
            p.addLine(to: CGPoint(x: 8.2, y: 18.3))
            p.addLine(to: CGPoint(x: 3.7, y: 18.3))
            p.closeSubpath()
            return p
        case .list:
            var p = Path()
            p.move(to: CGPoint(x: 7.3, y: 5.5))
            p.addLine(to: CGPoint(x: 18.3, y: 5.5))
            p.move(to: CGPoint(x: 7.3, y: 11))
            p.addLine(to: CGPoint(x: 18.3, y: 11))
            p.move(to: CGPoint(x: 7.3, y: 16.5))
            p.addLine(to: CGPoint(x: 18.3, y: 16.5))
            p.addEllipse(in: CGRect(x: 2.7, y: 4.6, width: 1.8, height: 1.8))
            p.addEllipse(in: CGRect(x: 2.7, y: 10.1, width: 1.8, height: 1.8))
            p.addEllipse(in: CGRect(x: 2.7, y: 15.6, width: 1.8, height: 1.8))
            return p
        case .settings:
            var p = Path()
            p.move(to: CGPoint(x: 3.7, y: 6.4))
            p.addLine(to: CGPoint(x: 12.8, y: 6.4))
            p.move(to: CGPoint(x: 16.5, y: 6.4))
            p.addLine(to: CGPoint(x: 18.3, y: 6.4))
            p.move(to: CGPoint(x: 3.7, y: 15.6))
            p.addLine(to: CGPoint(x: 5.5, y: 15.6))
            p.move(to: CGPoint(x: 9.2, y: 15.6))
            p.addLine(to: CGPoint(x: 18.3, y: 15.6))
            p.addEllipse(in: CGRect(x: 13.3, y: 4.6, width: 3.6, height: 3.6))
            p.addEllipse(in: CGRect(x: 5.9, y: 13.8, width: 3.6, height: 3.6))
            return p
        }
    }
}

/// Placeholder screen with just a title, for rooms not built yet.
struct LoftStubRoom: View {
    var titleKey: String
    @Environment(\.locale) private var locale

    var body: some View {
        VStack {
            Text(L10n.t(titleKey, locale))
                .font(LoftTheme.demi(18))
                .foregroundStyle(Color.loftSnow)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 16)
                .padding(.top, 8)
            Spacer()
        }
    }
}

#Preview("Home · off") {
    LoftShell(initial: .off, followTunnel: false)
}

#Preview("Home · on") {
    LoftShell(initial: .on, followTunnel: false)
}

#Preview("Home · simple") {
    LoftShell(initial: .simple, followTunnel: false)
}

#Preview("Home · connecting") {
    LoftShell(initial: .connecting, followTunnel: false)
}

#Preview("Home · off · ru") {
    LoftShell(initial: .off, followTunnel: false)
        .environment(\.locale, Locale(identifier: "ru"))
}

#Preview("Home · on · zh") {
    LoftShell(initial: .on, followTunnel: false)
        .environment(\.locale, Locale(identifier: "zh-Hans"))
}
