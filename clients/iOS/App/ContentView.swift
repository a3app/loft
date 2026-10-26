import SwiftUI

/// Live YAML host (Packet Tunnel). LoftApp shows `LoftShell` / Home; this stays for the session until profiles.
/// A bare editor: the YAML (or a share link) typed here is written to the
/// App Group, where the Packet Tunnel extension reads it on Connect.
struct ContentView: View {
    @StateObject private var tunnel = TunnelControl()
    @State private var draft = Group.yaml

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 16) {
                Text("From the street, ordinary HTTPS.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                TextEditor(text: $draft)
                    .font(.system(.body, design: .monospaced))
                    .frame(minHeight: 180)
                    .border(.quaternary)
                HStack {
                    Button(tunnel.busy ? "Connecting…" : "Connect") {
                        Group.yaml = draft
                        tunnel.connect()
                    }
                    .disabled(tunnel.busy || tunnel.up)
                    Button("Disconnect") { tunnel.disconnect() }
                        .disabled(!tunnel.up && !tunnel.busy)
                }
                .buttonStyle(.borderedProminent)
                Text(tunnel.line)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                Spacer()
            }
            .padding()
            .navigationTitle("loft")
            .task { await tunnel.refresh() }
        }
    }
}
