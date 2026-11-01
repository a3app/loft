/*
 ConnectionsView.swift - the Connections screen: the list of saved
 connections (ConnStore, App/ConnStore.swift) with Connect / Disconnect per
 row, swipe and context-menu actions, the Add sheet and the full-screen
 editor (App/ConnEditView.swift). At this stage LoftShell's Connections tab
 shows a placeholder, and the row buttons only change the Home snapshot.
*/
import SwiftUI
import UIKit

/// DESIGN Connections list. Mini Connect switches the Home snapshot; it does not start the Packet Tunnel.
struct ConnectionsView: View {
    @Binding var store: ConnStore
    @Binding var home: HomeSnapshot
    @Environment(\.locale) private var locale

    /// Add sheet visible; connection waiting for delete confirmation;
    /// connection open in the editor.
    @State private var showAdd = false
    @State private var pendingDelete: Conn?
    @State private var draft: ConnDraft?

    /// Header, subtitle and the list, plus the Add and Delete dialogs and
    /// the editor cover. Saving or deleting in the editor updates the store.
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Text(L10n.t("connsSub", locale))
                .font(LoftTheme.body(12.5))
                .foregroundStyle(Color.loftMute)
                .padding(.horizontal, 16)
                .padding(.bottom, 10)
            List {
                ForEach(store.items) { c in
                    ConnRow(
                        conn: c,
                        selected: c.id == store.activeId,
                        live: isLive(c)
                    ) {
                        store.toggle(c.id, home: &home)
                    } onOpen: {
                        draft = ConnDraft(conn: c, isNew: false)
                    }
                    .listRowInsets(EdgeInsets(top: 0, leading: 16, bottom: 8, trailing: 16))
                    .listRowSeparator(.hidden)
                    .listRowBackground(Color.clear)
                    .swipeActions(edge: .leading, allowsFullSwipe: false) {
                        Button(L10n.t("edit", locale)) {
                            draft = ConnDraft(conn: c, isNew: false)
                        }
                        .tint(Color.loftLamp)
                    }
                    .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                        Button(role: .destructive) {
                            pendingDelete = c
                        } label: {
                            Text(L10n.t("delete", locale))
                        }
                    }
                    .contextMenu {
                        Button(L10n.t("edit", locale)) {
                            draft = ConnDraft(conn: c, isNew: false)
                        }
                        if c.id == store.activeId {
                            ShareLink(item: c.exportText(), preview: SharePreview(c.fileName)) {
                                Text(L10n.t("export", locale) + "  .loft")
                            }
                        }
                        Button(L10n.t("delete", locale), role: .destructive) {
                            pendingDelete = c
                        }
                    }
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .environment(\.defaultMinListRowHeight, 0)
        }
        .confirmationDialog(L10n.t("addSheet", locale), isPresented: $showAdd, titleVisibility: .visible) {
            Button(L10n.t("addScan", locale)) { openNew(paste: false, manual: false) }
            Button(L10n.t("addPaste", locale)) { openNew(paste: true, manual: false) }
            Button(L10n.t("fromFile", locale)) { openNew(paste: false, manual: false) }
            Button(L10n.t("addManual", locale)) { openNew(paste: false, manual: true) }
            Button(L10n.t("cancel", locale), role: .cancel) {}
        }
        .confirmationDialog(deleteTitle, isPresented: Binding(
            get: { pendingDelete != nil },
            set: { if !$0 { pendingDelete = nil } }
        ), titleVisibility: .visible) {
            Button(L10n.t("delete", locale), role: .destructive) {
                if let c = pendingDelete {
                    store.remove(c.id, home: &home)
                }
                pendingDelete = nil
            }
            Button(L10n.t("cancel", locale), role: .cancel) {
                pendingDelete = nil
            }
        } message: {
            Text(L10n.t("delSub", locale))
        }
        .fullScreenCover(item: $draft) { item in
            ConnEditView(
                isNew: item.isNew,
                draft: item.conn,
                onSave: { c in
                    store.apply(c, home: &home)
                    draft = nil
                },
                onDelete: {
                    store.remove(item.conn.id, home: &home)
                    draft = nil
                },
                onCancel: { draft = nil }
            )
            .environment(\.locale, locale)
        }
    }

    /// Title and the "+" button that opens the Add sheet.
    private var header: some View {
        HStack(alignment: .center, spacing: 8) {
            Text(L10n.t("conns", locale))
                .font(LoftTheme.demi(20))
                .foregroundStyle(Color.loftSnow)
                .tracking(-0.5)
                .frame(maxWidth: .infinity, alignment: .leading)
            Button {
                showAdd = true
            } label: {
                Text("+")
                    .font(LoftTheme.demi(16))
                    .foregroundStyle(Color.loftInk)
                    .frame(width: 28, height: 28)
                    .background(Color.loftLamp)
                    .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
            }
            .buttonStyle(.plain)
            .accessibilityLabel(L10n.t("addSheet", locale))
        }
        .padding(.horizontal, 16)
        .padding(.bottom, 8)
        .padding(.top, 4)
    }

    /// Title of the delete confirmation with the connection name.
    private var deleteTitle: String {
        let name = pendingDelete.map { $0.name.isEmpty ? $0.host : $0.name } ?? ""
        return String(format: L10n.t("delTitle", locale), name)
    }

    /// Whether Home shows this connection as connected or connecting
    /// (matched by display name).
    private func isLive(_ c: Conn) -> Bool {
        let title = c.name.isEmpty ? c.host : c.name
        return (home.isLive || home.mode == .connecting) && home.profile == title
    }

    /// Opens the editor for a new connection. With paste, the clipboard
    /// text becomes the link and its host is pre-filled.
    private func openNew(paste: Bool, manual: Bool) {
        var c = Conn.blank()
        if manual {
            c.front = "auto"
        }
        if paste, let s = UIPasteboard.general.string?.trimmingCharacters(in: .whitespacesAndNewlines), !s.isEmpty {
            c.uri = s
            if let host = URL(string: s)?.host {
                c.host = host
            }
        }
        draft = ConnDraft(conn: c, isNew: true)
    }
}

/// A connection open in the editor, and whether it is new.
struct ConnDraft: Identifiable {
    var id: String { conn.id }
    var conn: Conn
    var isNew: Bool
}

/// One card in the list: a Connect / Disconnect button and the
/// name, host and protocol, which open the editor.
struct ConnRow: View {
    /// `selected`: the active connection (amber border). `live`: Home
    /// shows it connected, so the button reads Disconnect.
    var conn: Conn
    var selected: Bool
    var live: Bool
    var onToggle: () -> Void
    var onOpen: () -> Void
    @Environment(\.locale) private var locale

    var body: some View {
        HStack(spacing: 8) {
            Button(action: onToggle) {
                Text(live ? L10n.t("disconnect", locale) : L10n.t("popConnect", locale))
                    .font(LoftTheme.demi(10))
                    .foregroundStyle(live ? Color.loftInk : Color.loftSnow)
                    .frame(width: 90, height: 28)
                    .background(live ? Color.loftLamp : Color.loftGhost)
                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
            }
            .buttonStyle(.plain)

            Button(action: onOpen) {
                HStack(spacing: 8) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(conn.name.isEmpty ? conn.host : conn.name)
                            .font(LoftTheme.demi(13.5))
                            .foregroundStyle(Color.loftSnow)
                            .lineLimit(1)
                        Text(conn.host)
                            .font(LoftTheme.mono(11))
                            .foregroundStyle(Color.loftMute)
                            .lineLimit(1)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)

                    VStack(alignment: .trailing, spacing: 2) {
                        if live {
                            Text(L10n.t("active", locale))
                                .font(LoftTheme.body(10.5))
                                .foregroundStyle(Color.loftLamp)
                        }
                        Text(conn.side)
                            .font(LoftTheme.body(10.5))
                            .foregroundStyle(Color.loftStone)
                    }
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .background(Color.loftField)
        .overlay(
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .stroke(selected ? Color.loftLamp.opacity(0.45) : Color.loftCardLine, lineWidth: 1)
        )
        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
    }
}

#Preview("Connections") {
    LoftShell(initial: .on, tab: .connections, store: .sample)
}

#Preview("Connections · off") {
    LoftShell(initial: .off, tab: .connections, store: .sample)
}

#Preview("Connections · ru") {
    LoftShell(initial: .on, tab: .connections, prefs: LoftPrefs(lang: "ru"), store: .sample)
}
