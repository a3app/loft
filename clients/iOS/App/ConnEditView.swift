import SwiftUI

/// DESIGN Edit. Camera / YAML disk / Share QR later.
struct ConnEditView: View {
    var isNew: Bool
    @State var draft: Conn
    var onSave: (Conn) -> Void
    var onDelete: () -> Void
    var onCancel: () -> Void
    @Environment(\.locale) private var locale
    /// Entry by share link (true) or by separate fields (false).
    @State private var linkMode = true
    @State private var showSecret = false
    @State private var confirmDelete = false

    private let fronts = ["auto", "get", "ws", "h2", "quic", "h3", "l3"]

    var body: some View {
        ZStack {
            Color.loftPhone.ignoresSafeArea()
            VStack(alignment: .leading, spacing: 0) {
                Button(L10n.t("cancel", locale), action: onCancel)
                    .font(LoftTheme.medium(13))
                    .foregroundStyle(Color.loftLamp)
                    .buttonStyle(.plain)
                    .padding(.horizontal, 16)
                    .padding(.top, 4)
                    .padding(.bottom, 6)
                header
                ScrollView(.vertical, showsIndicators: false) {
                    VStack(alignment: .leading, spacing: 0) {
                        LoftField(label: L10n.t("name", locale), text: $draft.name)
                        typeRow
                        if draft.kind == .loft || draft.kind == .vless {
                            entrySeg
                        }
                        kindFields
                        LoftGoButton(title: L10n.t("save", locale), kind: .go) {
                            onSave(draft)
                        }
                        .padding(.top, 16)
                        if !isNew {
                            LoftGoButton(title: L10n.t("delete", locale), kind: .danger) {
                                confirmDelete = true
                            }
                            .padding(.top, 8)
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.bottom, 24)
                }
            }
        }
        .preferredColorScheme(.dark)
        .confirmationDialog(
            String(format: L10n.t("delTitle", locale), title),
            isPresented: $confirmDelete,
            titleVisibility: .visible
        ) {
            Button(L10n.t("delete", locale), role: .destructive, action: onDelete)
            Button(L10n.t("cancel", locale), role: .cancel) {}
        } message: {
            Text(L10n.t("delSub", locale))
        }
    }

    private var title: String {
        draft.name.isEmpty ? draft.host : draft.name
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 8) {
            Text(L10n.t(isNew ? "newTitle" : "editTitle", locale))
                .font(LoftTheme.demi(20))
                .foregroundStyle(Color.loftSnow)
                .tracking(-0.5)
                .frame(maxWidth: .infinity, alignment: .leading)
            if !isNew {
                ShareLink(item: draft.exportText(), preview: SharePreview(draft.fileName)) {
                    Text(L10n.t("shareBtn", locale))
                        .font(LoftTheme.demi(10))
                        .foregroundStyle(Color.loftSnow)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 6)
                        .background(Color.loftGhost)
                        .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                }
            }
        }
        .padding(.horizontal, 16)
        .padding(.bottom, 12)
    }

    private var typeRow: some View {
        HStack(alignment: .center, spacing: 8) {
            Text(L10n.t("vpnType", locale))
                .font(LoftTheme.body(13.5))
                .foregroundStyle(Color.loftSnow)
                .frame(maxWidth: .infinity, alignment: .leading)
            if isNew {
                HStack(spacing: 6) {
                    ForEach(HomeSnapshot.Kind.allCases, id: \.self) { k in
                        typeChip(k, on: draft.kind == k) {
                            draft.kind = k
                        }
                    }
                }
            } else {
                typeChip(draft.kind, on: true, action: {})
            }
        }
        .padding(.bottom, 14)
    }

    private func typeChip(_ k: HomeSnapshot.Kind, on: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(typeLabel(k))
                .font(LoftTheme.demi(12))
                .padding(.horizontal, 8)
                .padding(.vertical, 7)
                .foregroundStyle(on ? Color.loftInk : Color.loftStone)
                .background(on ? Color.loftLamp : Color.loftField)
                .clipShape(Capsule())
        }
        .buttonStyle(.plain)
        .disabled(!isNew)
    }

    private func typeLabel(_ k: HomeSnapshot.Kind) -> String {
        switch k {
        case .loft: return L10n.t("vpnLoft", locale)
        case .sstp: return L10n.t("vpnSstp", locale)
        case .vless: return L10n.t("vpnVless", locale)
        case .trojan: return L10n.t("vpnTrojan", locale)
        }
    }

    private var entrySeg: some View {
        HStack(spacing: 2) {
            entryBtn(true, L10n.t("entryLink", locale))
            entryBtn(false, L10n.t("entryMan", locale))
        }
        .padding(3)
        .background(Color.loftField)
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        .padding(.bottom, 14)
    }

    private func entryBtn(_ link: Bool, _ title: String) -> some View {
        Button {
            linkMode = link
        } label: {
            Text(title)
                .font(LoftTheme.demi(11))
                .frame(maxWidth: .infinity)
                .padding(.vertical, 8)
                .foregroundStyle(linkMode == link ? Color.loftSnow : Color.loftStone)
                .background(linkMode == link ? Color.loftGhost : Color.clear)
                .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
        }
        .buttonStyle(.plain)
    }

    /// The fields for the chosen protocol. loft and VLESS can be entered as
    /// one link or by parts; SSTP and Trojan always use separate fields.
    @ViewBuilder
    private var kindFields: some View {
        switch draft.kind {
        case .loft:
            if linkMode {
                secretField(L10n.t("loftUri", locale), $draft.uri)
            } else {
                secretField(L10n.t("eKey", locale), $draft.secret)
                hostPort
                frontRow
                LoftField(label: L10n.t("ePath", locale), text: $draft.path, mono: true)
            }
        case .vless:
            if linkMode {
                secretField(L10n.t("vlessUrl", locale), $draft.uri)
            } else {
                secretField(L10n.t("vlessUuid", locale), $draft.secret)
                hostPort
            }
        case .sstp:
            LoftField(label: L10n.t("sstpUser", locale), text: $draft.user)
            secretField(L10n.t("sstpPass", locale), $draft.secret)
            LoftField(label: L10n.t("sstpUrl", locale), text: $draft.uri, mono: true)
        case .trojan:
            secretField(L10n.t("trojanPass", locale), $draft.secret)
            LoftField(label: L10n.t("trojanUrl", locale), text: $draft.uri, mono: true)
        }
    }

    private var hostPort: some View {
        HStack(alignment: .top, spacing: 8) {
            LoftField(label: L10n.t("vlessHost", locale), text: $draft.host, mono: true)
            LoftField(label: L10n.t("vlessPort", locale), text: $draft.port, mono: true)
                .frame(width: 80)
        }
    }

    private var frontRow: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(L10n.t("urlType", locale))
                .font(LoftTheme.body(12))
                .foregroundStyle(Color.loftMute)
            HStack(spacing: 2) {
                ForEach(fronts, id: \.self) { f in
                    Button {
                        draft.front = f
                    } label: {
                        Text(f == "auto" ? L10n.t("kindAuto", locale) : f.uppercased())
                            .font(LoftTheme.demi(10))
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 8)
                            .foregroundStyle(draft.front == f ? Color.loftSnow : Color.loftStone)
                            .background(draft.front == f ? Color.loftGhost : Color.clear)
                            .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(3)
            .background(Color.loftField)
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        }
        .padding(.bottom, 12)
    }

    private func secretField(_ label: String, _ text: Binding<String>) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label)
                .font(LoftTheme.body(12))
                .foregroundStyle(Color.loftMute)
            HStack(spacing: 0) {
                secretInput(text)
                    .font(LoftTheme.mono(11))
                    .foregroundStyle(Color.loftSnow)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .padding(.horizontal, 12)
                    .padding(.vertical, 10)
                Button {
                    showSecret.toggle()
                } label: {
                    Text(L10n.t(showSecret ? "hideSecret" : "showSecret", locale))
                        .font(LoftTheme.demi(11))
                        .foregroundStyle(Color.loftMute)
                        .padding(.horizontal, 12)
                }
                .buttonStyle(.plain)
            }
            .background(Color.loftField)
            .overlay(
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .stroke(Color.loftCardLine, lineWidth: 1)
            )
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        }
        .padding(.bottom, 12)
    }

    @ViewBuilder
    private func secretInput(_ text: Binding<String>) -> some View {
        if showSecret {
            TextField("", text: text)
        } else {
            SecureField("", text: text)
        }
    }
}

/// A labelled text field in the loft style.
struct LoftField: View {
    var label: String
    @Binding var text: String
    var mono: Bool = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label)
                .font(LoftTheme.body(12))
                .foregroundStyle(Color.loftMute)
            TextField("", text: $text)
                .font(mono ? LoftTheme.mono(12) : LoftTheme.body(14))
                .foregroundStyle(Color.loftSnow)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
                .background(Color.loftField)
                .overlay(
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .stroke(Color.loftCardLine, lineWidth: 1)
                )
                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        }
        .padding(.bottom, 12)
    }
}

#Preview("Edit · Home") {
    ConnEditView(
        isNew: false,
        draft: ConnStore.sample.items[0],
        onSave: { _ in },
        onDelete: {},
        onCancel: {}
    )
}

#Preview("Edit · new") {
    ConnEditView(
        isNew: true,
        draft: .blank(),
        onSave: { _ in },
        onDelete: {},
        onCancel: {}
    )
}
