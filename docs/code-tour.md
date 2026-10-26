# Code tour

**English** · [Русский](code-tour_ru.md) · [中文](code-tour_zh.md)

This page is for someone opening the loft sources for the first time: which files to read first, how one connection travels through the code from an application to the internet, and what the words in the comments mean. The full directory tree is in [architecture.md](architecture.md); the protocol rules are in [protocol.md](protocol.md).

---

## 1. Where to start

Five places, read in order, explain the whole design:

1. `internal/mux/mux.go` — the multiplexer. Every user connection becomes a stream of one mux, and the mux becomes frames that go over the network. This is the heart of the protocol.
2. `internal/crypto/box.go` — the `0x02` handshake (`MakeAuth`) and frame encryption (`Seal` / `Open`).
3. `internal/transport/dial.go` — how the client picks a front, connects, and reconnects (`StartSelected`, `Supervise`).
4. `internal/transport/get.go` — the hardest and most important front: all traffic rides ordinary GET requests, so it passes through a CDN.
5. `internal/transport/hub.go`, function `proxyStream` — what the node does with each stream: hands it to the overlay or opens a connection to the internet.

The rest of `internal/transport` is the same thing for other fronts (WebSocket, HTTP/2, QUIC, HTTP/3, L3) plus protection against detection (decoy, mask).

---

## 2. One connection, end to end

Example: the `loft` client runs on a laptop and a browser opens `https://example.com`.

### On the client

1. **Start.** `clients/go/cmd/loft/main.go` reads `client.yaml` (`internal/config`) and calls `transport.SuperviseOn`, which keeps the connection alive for the life of the program.
2. **Front choice.** `StartSelected` (`dial.go`) tries the fronts from the config (`get`, `ws`, `h2`, `quic`, `h3`, `l3`) and takes the first one that comes up.
3. **TLS.** `tls.go` opens TLS 1.3 with a browser-like ClientHello (the uTLS library). To the network this is ordinary HTTPS.
4. **Handshake.** The client sends `0x02` (`crypto.MakeAuth`): a random nonce, an ephemeral X25519 key, and an HMAC that proves it knows the member key. Hello follows (`wire`, `newClientHello` in `dial.go`): version, capabilities, `instance_id`.
5. **Node answer.** The node sends Status: the overlay address, timers, the list of fronts, a `ticket`. From here both sides share a session key and every frame is encrypted with it.
6. **Local inbound.** `clients/go/inbound/socks5.go` (or `tun.go` in TUN mode) accepts the browser connection and calls `Dialer.Open("example.com:443")`.
7. **Stream.** `mux.Session.Open` creates a stream with an odd number and queues an Open frame. The browser's bytes follow as Data frames.
8. **Send.** The front takes frames from the mux (`Pull`), encrypts them (`crypto.Seal`), and sends them: in the `Cookie` header of the next GET, or as a binary WebSocket message.

### On the node

1. **Start.** `server/cmd/loft-node/main.go` reads `server.yaml`, `network.conf`, and `members.keys`, builds the HTTP handlers (`server/origin`), and listens on port 443.
2. **Sorting incoming connections.** `decoy.go` looks at the first TLS packet (ClientHello, parsed in `hellopeek.go`). If it is not loft, the whole connection is handed to a real website (decoy), so a scanner sees an ordinary site. If it is loft, the node serves it.
3. **Key check.** `GETServer.ServeHTTP` (`get.go`) or `WSSServer` (`wss.go`) extracts `0x02` and checks the HMAC against the member keys (`auth.go`, `internal/identity`). For a foreign key the node answers like an ordinary site (404 on GET) and gives nothing away.
4. **Status.** The node leases an overlay address (`internal/netconf`), builds Status (`node.go`), and encrypts it with the session key.
5. **Streams.** `serveMux` (`wss.go`) accepts streams from the mux and calls `proxyStream` (`hub.go`) for each.
6. **Where a stream goes.** An overlay address is handled by `Overlay.Handle` (`overlay.go`) and forwarded to another member. An internet address goes to `dialOut` (`egress.go`), which opens TCP along the exit rules (`internal/egress`). The node's own addresses (loopback, link-local, cloud metadata) are refused.
7. **Back.** The site's answer travels the same stream the other way: Data frames are encrypted and reach the client in GET responses or WebSocket messages.

### When the link drops

- `hold.go`: on a short drop the node does not close the mux at once; it parks it for `gap_hold_ms`. The client reconnects with the same `instance_id` and gets the same streams back — inner TCP is not torn down.
- `holder.go`: on the client, SOCKS5 and TUN keep working while the connection underneath is replaced.
- `path.go`: the client notices a network change (Wi-Fi → LTE) and reconnects without waiting for a timeout.

---

## 3. Glossary

| Term | What it is | Where in the code |
|--|--|--|
| **member** | A device or person with an `lt_…` key. The node keeps the key in `members.keys` and its hash in `network.conf`. | `internal/identity`, `transport/auth.go` |
| **node** | A loft server. Roles: controller (the main one) and relay (an extra entry point that forwards to the controller). | `server/cmd/loft-node` |
| **front** | A way to carry the mux over HTTPS: `get`, `ws`, `h2`, `quic`, `h3`, `l3`, `connect`. The inner protocol is the same; only the packaging changes. | `transport/get.go`, `wss.go`, `h2.go`, `quic.go`, `h3.go`, `l3.go`, `connect.go` |
| **mux** | The multiplexer: many streams in one connection. The client numbers streams with odd numbers, the node with even ones. | `internal/mux` |
| **frame** | One mux unit: Open, Data, Close, Reset, Dgram, Ping, Leave… | `internal/wire/frame.go` |
| **Pull / Puller** | Pull takes the frames that are ready to send out of the mux. The Puller is whoever is allowed to do that right now. GET has exactly one, so frames leave strictly in order. | `mux.go` (`Pull`), `get.go` (`pump`) |
| **`0x02`** | The client's first message: proof of the member key and an ephemeral key for the shared session key. | `crypto/box.go` (`MakeAuth`) |
| **Hello / Status** | Hello says what the client can do. Status is the node's answer: address, timers, fronts, error codes. | `internal/wire` |
| **sid / ticket** | The GET session identifier. The node sends it in Status as `ticket`; other fronts join the session with it. | `get.go`, `attach.go` |
| **attach (`0x04`)** | Join one more front (for example WebSocket) to a live GET session without a new handshake. | `attach.go`, `attach_client.go` |
| **hold / park** | The node keeps the mux alive after a drop so the client can come back without losing connections. | `hold.go` |
| **instance_id** | A persistent id of one client install. The node recognises "the same" client after a reconnect by it. | `instance.go` |
| **decoy** | A real website behind the node. Everything that is not loft goes there, so a scanner sees an ordinary site. | `decoy.go` |
| **mask** | A mode in which the node presents a certificate shaped like the decoy site's, making loft even harder to tell from the site by TLS. | `mask.go`, `maskcert.go` |
| **overlay** | The members' virtual network: each has its own IP and the node acts as a switch (hub, mesh, VLAN). | `overlay.go`, `internal/netconf` |
| **roster** | A controller-signed list of live fronts and relays. The client downloads it and knows where to fail over. | `roster_fetch.go`, `fleet.go` |
| **lists** | Signed sets of domains and subnets. The client uses them to decide what goes through loft and what goes direct. | `lists.go` |
| **egress** | The node's rules for reaching the internet: direct, another interface, SOCKS5, HTTP, or SSTP. | `internal/egress`, `transport/egress.go` |
| **canary** | A check that no middlebox rewrote the TLS handshake. If one did, the session does not come up. | `tls.go`, `node.go` |
| **inner** | Extra frame encryption inside TLS: ChaCha20-Poly1305, AES-256-GCM, or none on direct fronts. | `crypto/inner.go`, `transport/inner.go` |
| **ctl:1, hub:1** | Service addresses inside the mux between a relay and the controller: control and traffic forwarding. | `fleet.go`, `relay_ctl.go`, `hub.go` |

---

## 4. Reading further

- **Tests live next to the code** (`*_test.go`). They are the best usage examples: most of them start a client and a node in one process and push traffic through them.
- **Test vectors** (`internal/crypto/vectors_test.go`, `internal/transport/vectors_test.go`) show the handshake bytes; they are handy for checking your own implementation.
- **`*_darwin.go`, `*_linux.go`, `*_other.go`** are one function for different operating systems.
- **`server/panel`** is a separate program, the web panel. It does not implement the protocol: it configures the node through the root helper `loft-panel-adm`.
- **`clients/mobile`** is the shared Go engine for iOS and Android; the Swift and Java apps are a thin shell around it.
