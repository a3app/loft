// Package mobile is the shared Go session for iOS Packet Tunnel and Android
// VpnService. No GUI. No loft-front. Protocol 1.0 is unchanged.
//
// Bind with gomobile (or a C ABI) from this package. The OS adapter pumps IP
// packets and installs routes/DNS from Snapshot.
//
// The phone OS decides when a VPN may run and owns the tun interface. This
// package decides how the traffic looks on the wire. The Swift and Java apps
// are a thin shell: they hand Engine.Start a client YAML, copy raw IP packets
// in (InjectPacket) and out (PullPacket or a PacketSink), and read Snapshot to
// set the tunnel address, DNS and routes.
//
// One Engine speaks one of four outbounds:
//   - loft: the overlay protocol over one of its fronts (get, ws, h2, quic,
//     h3, l3). A front is one way of packing the same inner stream protocol
//     into HTTPS. A userspace TCP/IP stack (gVisor) turns the packets from
//     the OS into mux streams.
//   - SSTP: Microsoft's VPN over TLS with PPP inside; IP packets go through
//     as they are.
//   - VLESS and Trojan: proxy protocols over TCP+TLS; the same userspace
//     stack turns packets into proxied connections.
//
// share.go turns QR codes, pasted links and .loft files into that YAML, so
// both apps import connections the same way.
package mobile
