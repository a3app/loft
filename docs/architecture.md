# Architecture of the reference node

**English** · [Русский](architecture_ru.md)

Software 1.0.5. Protocol 1.0 (loft/1.0). Spec: [protocol.md](protocol.md). Operation: [guide.md](guide.md).

This document describes the Go tree in this repository. A third-party implementation MAY differ in layout and still speak the protocol.

---

## 1. Abstract

The reference node is one binary, `loft-node`, in two roles: controller and relay. The reference client is `loft` (CLI: SOCKS5 and optional TUN). `loft-front` is the same session without CLI flags for system routes; the macOS menu-bar app launches it as a helper. `loft-probe` checks ICMP/TCP/UDP/DNS/HTTP through a session. Shared code lives in `internal/`.

---

## 2. Tree

```
docs/                 protocol, architecture, guide
internal/
  wire/               frames, Hello, Status, roster, lists, fleet, caps, codes
  crypto/             HKDF, ChaCha20-Poly1305, auth 0x02, tickets 0x03, Ed25519 seed
  identity/           member key lt_ + 48 hex; SHA-256 in network.conf; plaintext in members.keys
  mux/                streams, backpressure, CmdDgram, CmdLeave
  netconf/            network.conf, leases, ACL, VLAN, DNS, session timers
  transport/          fronts, decoy, mask, relay trunk, overlay, p2p, DNS stub, roster fetch
  config/             client.yaml, server.yaml
  cdn/                edge profiles (Yandex, Cloudflare); not caps. Operator: docs/yandex-cdn_ru.md
  version/            program name, semver, protocol range
server/
  cmd/loft-node
  origin/             cover page, GET, WebSocket, HTTP/2 CONNECT, public roster/lists
  configs/            server.yaml, relay.yaml
  debian/             installer, systemd unit, .deb
clients/
  go/cmd/loft         CLI: SOCKS5, TUN, kill-switch
  go/cmd/loft-front   session helper (macOS bundle Contents/MacOS/loft-front)
  go/cmd/loft-probe   overlay ICMP/TCP/UDP/DNS/HTTP without rewriting routes
  go/tools/wsprobe    one-shot GET handshake (manual; not in dist)
  macOS/Sources       Swift 5 + AppKit menu bar; launches loft-front
  macOS/pack          DMG (Applications symlink), sign, entitlements
  macOS/design        UX spec and mock
  macOS/tests         RFC 8439 AEAD vector (make -C clients/macOS test-crypto)
  Keenetic/           Entware OpkgTun catalog (plan and mock; no binary yet)
  iOS/                Packet Tunnel (thin host + appex; gomobile xcframework; Apple TV in PLAN phase 6)
  Android/            VpnService (thin Java host; gomobile AAR; UX mock; Android TV / Google TV in PLAN phase 4)
  mobile/             shared Go engine for iOS/Android (loft, SSTP, VLESS)
brand/                mark and icons
```

`wire` is the protocol. `transport` is how this tree carries it. `cdn` is a deployment profile: header order and SNI of a named edge. It MUST NOT appear on the wire as a capability.

---

## 3. Session

A member session is a dial, an auth blob, a Status, then mux.

```
client.Holder
    Supervise(cfg)                 rotate outbounds and fronts
        Open(target)               get | ws | h2 | quic | h3
            TLS 1.3 (uTLS)
            auth 0x02 | ticket 0x03
            AEAD(Status)
            mux Open / Data / Close / Dgram / Leave
```

`Supervise` tries configured outbounds in list order when `select: auto`. After the first Status it keeps only the intersection of fronts. On failure it moves to the next remaining front, then unsigned `replicas`, then the signed roster.

`WatchPath` samples the default IPv4 egress about four times a second. A Wi-Fi / LTE / VPN address change drops the live front so Supervise can resume with ticket `0x03` and the same `instance_id`. SOCKS5 and TUN stay bound (`Holder`); the overlay IP does not change. With cap `hold`, mux streams stay for `gap_hold_ms` (default 8 s) and inner TCP does not RST. `loft` and `loft-front` both run this loop.

The client does not choose decoy SNI. Status, roster, or a probe certificate supplies it. A CDN outbound keeps the CDN hostname.

Local inbound (SOCKS5, TUN) calls `Holder.Open(host:port)` or sends a datagram. Overlay destinations are resolved by `NetSwitch` on the node. Status `dns` is a list of resolver IPs; the node answers `gateway:53` from `dns.upstream` (DoH, DoT, or UDP). Optional Status tail `ip6` / `gateway6` is IPv4-only when omitted.

---

## 4. Node roles

### 4.1 Controller

Holds:

- member keys as SHA-256 in `network.conf`; plaintext in `{dir}/members.keys` (`keep_keys` lets an operator re-read that file)
- leases and overlay IP
- ACL and VLAN
- Ed25519 seed `{dir}/controller.seed`
- live roster and address lists

It MAY listen on 443 for members. It MUST listen for relay trunks.

Signing seed is created on first start. The public key is given to clients as `controller_pub`.

### 4.2 Relay

Terminates public TLS. Opens one trunk to the controller with Hello cap `relay`. On that session:

- `ctl:1` — enrol, beat, slice, drain
- per member — `hub:1` (HTTP/1.1) or `hub:2` (HTTP/2)

The relay copies inner plaintext. It does not mint leases. It does not store member keys. Overlay IP is assigned on the controller.

A relay IP is disposable. Removing it from the roster is a serial bump. Clients already holding a Status keep the session until it fails, then fetch the new roster.

### 4.3 Origin

`server/origin` is the HTTPS application the public path sees:

- cover page (`cover_dir`, or a built-in page)
- GET front, WebSocket, HTTP/2 CONNECT
- public roster and lists JSON

A decoy-spliced probe never reaches this handler: it is forwarded to the configured origin before HTTP.

### 4.4 Debian host

systemd unit, installer, and `.deb`: [server/debian/README.md](../server/debian/README.md). Token in `/etc/loft/loft.env`. Overlay document in `/var/lib/loft`. Process user `loft`, bind 443 with `CAP_NET_BIND_SERVICE`.

---

## 5. Decoy and mask

`transport.WrapDecoy` peeks the TCP ClientHello.

```
Accept
  read ClientHello (or leftover bytes)
  if valid session_id and own SNI
      node certificate (member on advertise, CDN after sid)
  else if own SNI from cdn_peers
      node certificate (CDN origin pull)
  else if session_id = sid v3 (node X25519) or sid v2 (member HMAC)
      if mask ready → dest-shaped ECDSA leaf + record pad
      else            → loft HTTP handler
  else
      TCP splice to decoy origin (SNI match, else primary)
UDP
  decoy on → datagram splice to dest
  decoy off → loft QUIC/H3
```

The splice destination is node configuration. The client discovers it. Example hostnames in YAML are examples. They are not protocol constants.

Mask mints an ECDSA P-256 leaf that copies the origin identity and signs CertificateVerify with `ecdsa_secp256r1_sha256`. Serial is bound to ECDH with the node X25519. Probes still splice. Application records MAY follow origin ciphertext lengths.

---

## 6. Overlay fabric

`transport.Overlay` binds each admitted session to an overlay IP. Open to an address inside the network CIDR is classified by `peers`:

| mode | action |
|------|--------|
| hub | Reset `policy_drop` |
| mesh | splice to the peer’s mux session as Open `127.0.0.1:port` |
| vlan | mesh if VLAN matches |

The receiving client accepts that Open and dials the local port. Internet Open (outside the CIDR) is unchanged.

Optional P2P: the client listens, announces `loft.peer:<port>`, and Status carries TLS-observed host plus port. Direct TCP uses the 38-byte `LFT2` preface. Failure falls back to hub splice. `p2p: false` on the client skips the listen.

---

## 7. Address lists

The controller publishes named sets. The client maps names to actions (`use:`). Matching order when several lists hit: drop, then direct, then via. Default for unmatched destinations is `via`.

SOCKS matches the hostname of the request. TUN matches the destination IP against CIDR entries. Domain kinds do not apply to raw IP sockets.

Lists are fetched when Status `lists_serial` or `lists_hash` is newer than the cache. Signature is the same Ed25519 key as the roster.

---

## 8. Local inbound

| listener | default | notes |
|----------|---------|-------|
| SOCKS5 | `127.0.0.1:1080` (GUI / `client.yaml`); `127.0.0.1:18080` (`loft` / `loft-front` when the port is omitted) | RFC 1928. CONNECT and UDP ASSOCIATE when `dgram` is on. |
| TUN | off; `tun: auto` creates utun | gVisor user stack. UDP via CmdDgram. ICMP echo: self locally; overlay and internet dests as Dgram `:1` (node emits internet echo). Darwin NATs dummy `169.254.8.1` ↔ overlay IP on ICMP. |
| kill-switch | `listen.kill_switch` | when the last TUN dies, native default stays down until the user restores it |

TUN excludes outbound hosts of the loft session (CDN, node) so the tunnel does not swallow its own path.

The macOS menu-bar client is Swift 5 + AppKit under [`clients/macOS/Sources/`](../clients/macOS/Sources/). UX: [`clients/macOS/design/DESIGN.md`](../clients/macOS/design/DESIGN.md). A live session is `loft-front` in the app bundle (`Contents/MacOS/loft-front`), not the in-process `GETFront.swift` type. Hello of that helper is the Go client (GET and WS). Route hold, failover, and split lists in the design document are machine policy. They sit above the Go CLI flags.

---

## 9. Failover

Order of attempts after a dead session:

1. Remaining fronts in the intersection, same outbound.
2. Next outbound in `select: auto` order.
3. Unsigned `replicas` from Status.
4. Signed roster entries, weighted, skipping drain.

Reconnect timers come from Status: `reconnect_min_ms`, `reconnect_max_ms`, `reconnect_fast_n`, `reconnect_fast_for_ms`. Fast tries first, then exponential backoff.

A ticket (`0x03`) MAY be used on a replica without sharing the nonce cache. The client caches the last Status ticket across Supervise `Start` so a path change does not mint a new overlay instance.

Mux `Pull` drains Open/Ping/Pong/Dgram/Leave and small Data (≤ 4 KiB) before bulk Data/Pad so a full download does not stall ICMP, TCP ACKs, and Cloudflare loaded-ping. Close/Reset stay in the Data queue so a graceful close cannot skip remaining bytes. The Data queue is 48 frames. WS prefetch is 2×48 KiB; control frames get their own 8 KiB message.

---

## 10. Concurrency

One mux session per member instance. Streams are goroutines with a bounded receive buffer. Mux payload is ≤ 16384; `Stream.Write` and `EncodeBatch` split CmdData so a 32 KiB `io.Copy` does not drop the frame.

The GET front has a bounded inflight window (protocol 2–8, default 4; this tree defaults to 48). The client pump is the sole uplink Puller and assigns GET `seq` in Pull order. The node applies uplink in seq order; only the in-order GET Pulls mux downlink so a dropped HTTP response cannot lose frames already taken from the mux (YouTube/Speedtest TLS). Idle GET pace is `keepalive_ms / 2` (floor 1 s) with ±⅓ jitter. Open/tiny Data wait 15 ms and datagrams 80 ms so ICMP can ride the same body. Generic GET `header_budget` in this tree is 16 KiB of HTTP header (decoded mux ~12 KiB after base64url+AEAD, ~13 KiB after Cookie Z85); the Yandex profile pins 16 KiB encoded. Status `h2_conns` (node `get_h2`, 1–4) is extra HTTP/2 connections for a second header-budget envelope; inflight is still the total. A GET whose uplink already has ≥ 2 KiB CmdData caps the body at 96 KiB so Cookie slots recycle; download-only stays 256 KiB.

Relay trunks are one mux session per relay process, with many `hub:*` streams.

`Supervise` is a single loop per client process. Multiple YAML profiles (macOS multi) are multiple processes or multiple Holders. They are not cap `multi`. Cap `multi` is several `instance_id` values on one member key.

---

## 11. What lives elsewhere

Virtual machines, public DNS, public certificates, and binary updates are outside this protocol. systemd units, the POSIX installer, and the `.deb` live in [`server/debian/`](../server/debian/). The wire carries enrol, beat, slice, drain, roster, and lists. An operator issues per-member keys and writes `network.conf` plus `members.keys`.
