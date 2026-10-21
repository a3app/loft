# loft: A Private Overlay inside TLS

**English** · [Русский](protocol_ru.md) · [中文](protocol_zh.md)

**loft** occupies the same place on the wire as ordinary HTTPS. The wire protocol is **loft/1.0**.

**Protocol 1.0.** A third-party implementation that follows this document may speak to a loft node. The Go tree in this repository is one such implementation. Licensed [Apache-2.0](../LICENSE).

Software version (`VERSION`, currently 1.0.22) is independent of protocol version. Both are carried in the handshake. Keywords **MUST**, **SHOULD**, **MAY** follow RFC 2119. Unknown trailing fields in Hello and Status MUST be ignored.

---

## Abstract

A private overlay that relies on a distinctive handshake or a unique destination is brittle on production paths: CDNs, reverse proxies, and TLS middleboxes expect ordinary HTTPS. loft occupies the same place on the wire as ordinary HTTPS: TLS 1.3 on port 443, a browser-shaped ClientHello, and application data which a CDN may terminate. Inner streams are multiplexed and are sealed with ChaCha20-Poly1305. A node MAY present the identity of a real HTTPS origin; probes that lack a member key are spliced to that origin, so that scanners and health checks are shown a website. Identity of the overlay (keys, leases, the list of live fronts) is held on a controller. Relays are replaceable TLS edges. A signed roster of fronts and named address lists is fetched by clients and is mapped to local routing. Members of one network MAY be isolated at a hub, meshed through the node, or permitted a direct TCP attempt. Typical deployments are laboratory LAN access from outside, a server fleet, and site-to-site.

---

## 1. Introduction

Encrypted tunnels fail in two ways on production paths. A distinctive handshake is dropped by a TLS middlebox or CDN that expected a browser. A tunnel that uses a unique destination IP is dropped when that address is not on the allowlist. Both are operational failures for a laboratory or a remote-access overlay.

loft is constructed so that the public path is ordinary HTTPS. The inner protocol is one. The path that carries it (GET, WebSocket, HTTP/2 CONNECT, QUIC, HTTP/3 CONNECT) is chosen per deployment and MAY be changed without a protocol bump. The TLS name sent by a client is chosen by the node. A probe that lacks the member key completes a handshake with a configured origin.

A second cost is operations. An address that is no longer reachable is cheap to replace if another front is already known to clients and the overlay address is kept. A roster of fronts is signed by the controller. Relays enrol, send heartbeats, and are removed from the roster when silent. The member key never appears in that document.

This document specifies the wire. Building and operating the reference node is described in [guide.md](guide.md). Package layout of the reference tree is described in [architecture.md](architecture.md).

---

## 2. Construction

```
application  TCP / UDP     (SOCKS and TUN are local)
    mux      Open Data Close Reset Ping Pong Pad Dgram Leave
      AEAD   ChaCha20-Poly1305
        front  get | ws | h2 | quic | h3
          TLS 1.3
            TCP/443 or UDP/443
```

The TLS ClientHello SHOULD match a current browser (cipher list, extensions, JA3/JA4). CDN vendor names are deployment profiles. They MUST NOT appear as protocol capabilities.

A session has three layers:

1. **TLS.** Public. A CDN, a load balancer, or an origin MAY terminate it.
2. **Auth and AEAD.** Inner. Possession of the member key proves membership. ChaCha20-Poly1305 hides mux from the terminator of TLS.
3. **Mux.** Streams and datagrams between the client and the node (and, through the node, the Internet or other members).

Reserved mux targets on a relay trunk: `hub:1` (HTTP/1.1), `hub:2` (HTTP/2), `ctl:1` (fleet control). Overlay announce: `loft.peer:<port>`.

String fields on Hello, Status, and most fleet records are `u8 length || bytes`, at most 64 bytes. URLs and list values are `u16be length || bytes`, at most 255 bytes unless a section states otherwise. Integers are big-endian.

---

## 3. Transports

A node advertises enabled fronts in Status `caps`. A client advertises the fronts it will use in Hello `caps`. After the first Status the client MUST use only the intersection, in its configured order, and MUST move to the next remaining front when the current one fails.

Omitted `fronts` on a node is `get`. `ws`, `h2`, `quic`, `h3`, and `connect` are off until listed. Default try order, when both sides list every transport front: `get`, `ws`, `h2`, `quic`, `h3`.

### 3.1 Decoy splice

A loft ClientHello has a 32-byte `session_id`. The node accepts either construction, trying v3 first.

**sid v3.** Let `P` be the node’s static X25519 private (HKDF-SHA256 of the node secret, info `"loft-mask-x25519"`), `X` the ClientHello X25519 `key_share`, `R` the 32-byte `client_random`, `S` the lowercase SNI:

```
shared = X25519(P, X)
session_id = HKDF-SHA256(ikm=shared, salt=R, info="loft-sid-v3" || 0x00 || S, L=32)
```

The matching public is Status / roster `mask_pub` (32 bytes) and MAY appear as share-URI query `pbk`. Detection of these ClientHellos requires `P`. A stolen member key does not mark them.

**sid v2 (fallback).** Let `token` be the member key:

```
HKDF-SHA256(ikm=token, salt=R, info="loft-sid-v2" || 0x00 || S || X, L=32)
```

A client that has not yet learned `mask_pub` sends v2. Possession of `token` allows detection of v2 ClientHellos. The binding to `R`, `S`, and `X` prevents a recipe of the form HMAC(`token`, `random`) from matching.

Any other TCP ClientHello is spliced to an origin the node configured. The first origin is the node’s identity. A matching SNI among the node’s origins selects that origin; otherwise the primary is used. Origin hostnames are configuration. They MUST NOT appear as protocol constants.

Without a valid `session_id` the field is a 32-byte string with the same shape as Chrome’s compatibility `session_id`. The client MUST send the node’s primary decoy hostname as SNI on a direct path. It learns that hostname from Status, from the roster, or from the certificate a probe would see. After Status, a mismatch MUST reconnect with the advertised name. A CDN front keeps the CDN hostname.

### 3.2 Decoy mask

When mask is enabled, a loft ClientHello completes TLS 1.3 wearing a dest-shaped certificate: an ECDSA P-256 leaf that copies the origin Subject, SAN, and validity. The leaf serial is

```
HKDF-SHA256(ikm=shared, salt=R, info="loft-cert-serial", L=16)
```

with `shared` the sid-v3 ECDH. CertificateVerify is `ecdsa_secp256r1_sha256` under that leaf. A client that knows `mask_pub` MUST check the serial bind and the ECDSA verify. Application records SHOULD be padded so ciphertext lengths follow a pattern sampled from the origin, including after the first sampled records. Status `mask_pub` and roster `mask_pub` carry the 32-byte X25519 public. Empty means mask is off. Probes continue to splice. An implementation that only splices remains compatible with splice-only nodes.

### 3.3 WebSocket (`ws`)

TLS, then HTTP upgrade. Path is node-local (default `/api/v1/sync`). Client message 1 is the auth blob in the clear. Node message 1 is AEAD(Status). Further messages are AEAD(mux batch). Empty batches MUST NOT be sent. Closing the socket ends the session. The client SHOULD send CmdLeave in a mux batch before that close.

### 3.4 GET (`get`)

For edges that forward GET only. HTTP/2. Paths look like assets: `{base}/{session}/{seq}.js`.

First GET: Cookie `session=<base64url(auth blob)>`. The blob is tag `0x02` (or a leftover `0x03` ticket). The path already carries a client-chosen session id (24 hex). Response 200, `Cache-Control: private, no-store`, body AEAD(Status) with AAD = that session id. The node SHOULD echo `X-Session` with the same id (older clients used path `new` and read this header).

Later GET: Cookie `session=` with base64url or Z85 of AEAD(mux batch), AAD = session id, body AEAD(mux batch). The node SHOULD prepend CmdReport (`applied` = last contiguous ingested GET seq) to that body. A client that receives it MUST stop retrying uplink seqs ≤ `applied`. Handshake is a Cookie that verifies as `0x02`/`0x03`; later GETs are AEAD. A node MUST still accept `X-Auth: 1`, path `new`, and uplink in `X-Request-Id` from older clients. Failed auth MUST be 404 with HTML. Uplink MUST fit one header (Status `header_budget` is encoded Cookie bytes; omitted default is 16 KiB of HTTP header). Status `uplink_enc` 1 is `~` plus Z85 of `u16be length || AEAD blob` (RFC 32); 0 and omitted are base64url. Handshake Cookie stays base64url. A node MUST accept both. Status `h2_conns` (1–4) is HTTP/2 connections to this front; 0 keeps one. Inflight is the total outstanding GETs across those connections. A client MAY replace an HTTP/2 connection after a local max-age (omitted or 0 keeps it until error); mux and GET session id do not change. HTTP 413: halve the budget, floor 1024, and split. HTTP 405: this front is not GET. Inflight 2–8 (protocol default 4; Status `inflight` is a ceiling). A client that sees HTTP/2 `GOAWAY ENHANCE_YOUR_CALM` MUST back off on the same session and MAY send CmdTune event 1 on the next GET that already has mux. The node MAY lower `header_budget` / `inflight` for that front and echo CmdTune event 0 in that GET body. Idle uplink empty: wait at least `keepalive_ms / 2` (and not less than 1 s). A GET that already carries mux Data, Open, Dgram, or Leave is not idle. The node SHOULD hold a GET up to 250 ms for downlink and MUST NOT hold more than 1 s. When the uplink already contains CmdData, the node MAY return as soon as downlink Data is queued. When that uplink has ≥ 2 KiB CmdData, the node SHOULD cap that GET body at 96 KiB so the next Cookie recycles; download-only GETs keep 256 KiB (192 KiB with several streams).

Dropping HTTP/2 without Leave does not end the session. Before the client process exits it MUST send one last GET whose uplink batch contains CmdLeave. The node then closes the mux session, forgets the GET session id, Detach, and Release. `lease_hold` still keeps the overlay IP for `ok_resumed`.

### 3.5 HTTP/2 CONNECT (`h2`)

ALPN `h2`. CONNECT to a node-local path (default `/api/v1/tunnel`). 200, then messages `u32be length || bytes`: auth, AEAD(Status), AEAD(mux batch).

### 3.6 QUIC mux (`quic`)

First client-initiated bidi stream, same length-prefixed messages as `h2`. ALPN MUST be `h3`. If HTTP/3 CONNECT is also enabled on that UDP port, the node tells them apart by the first byte of the first stream: 0 is this mux (a 4-byte length, at most 1 MiB), 1 is an HTTP/3 HEADERS frame. The node sends HTTP/3 SETTINGS before it knows; a mux client ignores that stream.

### 3.7 HTTP/3 CONNECT (`h3`)

ALPN `h3`. CONNECT to the same path shape as `h2`.

### 3.8 Path canary

Status MAY set `canary_flags`: bit 0 fat ClientHello, bit 1 ClientHello split across two TLS records (the node concatenates). `canary_pad` is extra padding bytes, 0–2048.

Members present a browser-shaped ClientHello so CDNs, reverse proxies, and TLS middleboxes see ordinary HTTPS. If a middlebox rewrites the handshake, the session does not come up. This is path integrity: the node refuses a mangled TLS path. It does not hide SNI from a reassembling inspector.

### 3.9 CONNECT splice (`connect`)

GET mux polls a stream protocol over request/response. A node MAY set Status cap `connect` when `fronts` lists `connect` (off by default). After the usual GET/WS handshake (lease, overlay IP), a client MAY Open TCP with HTTP/2 `CONNECT` `:authority` `host:port` instead of mux `CmdOpen`. CONNECT uses the same live GET HTTP/2 client pool as mux GETs (optional `h2_max_age_ms` recycle). That CONNECT MUST carry the same 0x02 Cookie as the GET handshake (`Cookie: session=` base64url). Failed proof is HTTP 404. The node dials that target and splices bytes. It MUST refuse loopback, link-local (including `169.254.169.254`), multicast, unspecified, and `localhost`. Overlay member IPs, UDP, and ICMP stay on mux. A node without `connect` keeps mux for every TCP. Catch-all HTTP CONNECT without this front MUST 404.

---

## 4. Authentication and confidentiality

A member key is `lt_` followed by 48 hex characters (24 bytes). The node stores SHA-256(key). A deployment MAY use one token for every member; that is configuration.

### 4.1 Clockless proof

```
0x02 || nonce[16] || eph_c[32] || HMAC-SHA256(key, "lft-a2" || nonce || eph_c)[0:16] || Hello
```

`eph_c` is the client’s ephemeral X25519 public. Replay of a *new* nonce is a new handshake. The same `nonce||eph_c` inside a 10-minute node-clock cache is an idempotent retry (GET Cookie); a different `eph_c` on that nonce is refused. The client clock is unused.

A node MAY still accept the 1.0.16 proof `0x02 || nonce[16] || HMAC-SHA256(key, nonce)[0:16] || Hello`, session key HKDF(key, nonce), first message `Seal(Status)` without `eph_n`, and tag `0x03` resume tickets. The reference node does this through software 1.0.x. Clients of this revision MUST send the proof in 4.1. Drop the 1.0.16 path in 1.1.

### 4.2 Resume

A path change or process restart sends a fresh `0x02` with a new ephemeral and the same Hello `instance_id`. Hold reattaches the parked mux by the authenticated member plus instance. Replica failover uses `0x02` on the new node; overlay IP follows `lease_hold` and the instance. Status MAY still carry an empty `ticket` field (length 0).

### 4.3 Session key

```
ss = X25519(sk_c, eph_n)
HKDF-SHA256(ikm=ss, salt=nonce, info="loft-v1", L=32)
```

The first node message is `eph_n[32] || Seal(Status)`. Later mux messages are Seal only. `eph_n` is the node’s ephemeral X25519 public for this handshake. HMAC covers `eph_c`, so a stolen Cookie cannot complete a session: the AEAD key needs `sk_c`, which never left the client.

Seal: `nonce[12] || ChaCha20-Poly1305(key, nonce, plaintext, aad)` (RFC 8439). Nonce MUST be unique per session key. The reference uses a 32-bit random prefix plus a 64-bit counter so a parked GET session continues the counter. GET uses AAD = session id; other fronts use empty AAD unless a section states otherwise.

AEAD covers mux. TLS covers the path to the terminator. A CDN that terminates TLS sees ciphertext of mux.

---

## 5. Hello and Status

Hello is appended to the auth blob. The first node message is `eph_n[32] || Seal(Status)`. Status is AEAD plaintext. Neither Hello nor Status is a mux command.

### 5.1 Hello

```
u8    version          (= 1)
u8    member_id_len || member_id
u8    instance_id_len || instance_id
u16be proto_min
u16be proto_max
u64be caps
u8    program_len || program
u8    software_len || software
```

`instance_id` persists on the client. Empty means the node mints one in Status; the client MUST store it. Strings ≤ 64 bytes. Protocol version is `major<<8|minor` (`0x0100` = 1.0). Cap `relay` marks a relay trunk. Cap `p2p` marks a client that will announce a listen port.

An old blob without proto/caps still decodes. Trailing unknown bytes MUST be ignored.

### 5.2 Status

Fields in order:

| field | encoding |
|-------|----------|
| code | u16be |
| lease_sec | u32be |
| mtu | u16be |
| vlan | u16be |
| keepalive_ms, idle_ms, reconnect_min_ms, reconnect_max_ms | u32be each |
| reconnect_fast_n | u16be |
| ip, gateway, instance_id | u8 len \|\| bytes |
| reconnect_fast_for_ms | u32be |
| selected proto, min, max | u16be each |
| caps | u64be |
| program, software | u8 len \|\| bytes |
| ticket | u16be len \|\| bytes |
| decoy | u8 n, then n × (u8 len \|\| bytes) |
| mask_pub | u8 len \|\| bytes (0 or 32) |
| replicas | u8 n, then n × (u8 len \|\| bytes) |
| canary_flags | u8 |
| canary_pad | u16be |
| roster | u16be len \|\| bytes |
| lists_serial | u64be |
| lists_hash | u8 len \|\| bytes (0 or 32) |
| peers | u8 (0 hub, 1 mesh, 2 vlan) |
| p2p | u8 (1 = node allows) |
| peer hints | u8 n ≤ 16, then n × (ip, host, port u16be, vlan u16be) |
| dns | u8 n ≤ 8, then n × (u8 len \|\| bytes) — resolver IPv4/IPv6 for the client TUN. Empty: the client MUST leave OS DNS unchanged. |
| ip6, gateway6 | optional tail: each u8 len \|\| bytes. Omitted entirely when both are empty (IPv4-only). A decoder that does not read them MUST ignore the trailing bytes. |
| header_budget, inflight | optional after ip6/gateway6: u16be encoded GET Cookie budget, u8 inflight. Present when either is non-zero (ip6/gateway6 are then empty strings on IPv4). Handshake values are a ceiling; live CmdTune is authoritative. 0 = client keeps its own. |
| h2_conns, uplink_enc | optional after inflight: u8 HTTP/2 connections (1–4; 0 = client keeps one), u8 uplink encoding (0 base64url, 1 Cookie Z85). Present when either is non-zero. Old clients ignore the extra bytes. |
| gap_hold_ms | optional after uplink_enc: u32be. How long the node keeps mux streams after a front drop without Leave. 0 = client default (8 s). Old clients ignore the extra bytes. |
| routes | optional after gap_hold_ms: u8 n ≤ 16, then n × (dst, via) as u8 len \|\| bytes. Managed CIDRs: a lab LAN behind a member (`via` is that member’s overlay IP, resolved from `ip route` VIA / `member ID` / `tag NAME`). The node splices Open `host:port` onto that member. A client MUST NOT install a route whose `via` is its own overlay IP. Old clients ignore the extra bytes. |
| policy | optional after routes: compact ACL so a client can enforce the same Allow on direct P2P. `u8 n_own_tags` then tags; `u8 acl_default` (0 peers/accept, 1 drop); `u8 n_rules` then rules (action, flags, optional VLAN/CIDR/tag/proto/ports); `u8 n_peers` then overlay IP + tags. Omitted when empty. A decoder that sees this tail MUST set Present. Old clients ignore the extra bytes. |
| session_ttl_ms | optional after policy: u32be live-session max. 0 = off. Distinct from `lease_sec` (IP hold after disconnect). When this field is present without a policy, the encoder writes an empty policy first. Old clients ignore the extra bytes. |
| rate_bps | optional after session_ttl_ms: u32be bulk `CmdData`/`CmdDgram` cap for this session, both directions, bytes per second. 0 = off. Control and small frames (Open, Ping, ACK ≤ 4 KiB, DNS/ICMP Dgram ≤ 512 B) are unshaped. The node enforces. When this field is present without `session_ttl_ms`, the encoder writes ttl 0 first. Old clients ignore the extra bytes. |

Selected protocol is the maximum of the intersection of the two ranges. Empty intersection yields code `protocol` (0x0203) and no mux.

### 5.3 Capability bits

| bit | name | meaning |
|-----|------|---------|
| 0 | multi | several instances on one key |
| 1 | resume | `ok_resumed` and lease hold |
| 2 | sticky_ip | reserved overlay IP on the key |
| 3 | vlan | VLAN from the key |
| 4 | rules | switch ACL |
| 5 | get | GET front |
| 6 | ws | WebSocket front |
| 7 | status | Hello/Status with code |
| 8 | timers | keepalive / idle / reconnect in Status |
| 9 | hello_caps | this cap block |
| 10 | reset | CmdReset |
| 11 | quic | QUIC mux front |
| 12 | dgram | CmdDgram |
| 13 | clockless | auth without client clock |
| 14 | ticket | Status MAY carry a ticket blob (empty in this revision) |
| 15 | decoy | Status lists origins |
| 16 | h2 | HTTP/2 CONNECT |
| 17 | h3 | HTTP/3 CONNECT |
| 18 | mask | borrowed origin certificate |
| 19 | relay | this session is a relay trunk |
| 20 | roster | Status may carry a signed roster |
| 21 | lists | Status may advertise list serial/hash |
| 22 | p2p | peer-to-peer overlay when mesh |
| 23 | dns | Status may carry DNS servers; gateway:53 is a resolver |
| 24 | connect | HTTP/2 CONNECT splice: TCP Open without inner mux |
| 25 | resin | CmdReport and resin/draft GET uplink |
| 26 | hold | mux streams survive a brief front drop; resume with `0x02` and the same member plus `instance_id` |
| 27 | quic_mark | quic on a decoy node: the Initial MUST carry the loft mark (§11.3) |
| 28 | h3_mark | h3 on a decoy node: the Initial MUST carry the loft mark (§11.3) |

A node that disables a front MUST clear that bit. A client that will not use a front MUST clear it. SOCKS, TUN, CDN vendor, OS, git, and origin hostnames are not caps.

Negotiate version: `max(intersection(client.min..client.max, node.min..node.max))`. Caps in Status are the node’s advertised set. The client decides which of those it uses.

---

## 6. Multiplex

After Status, each AEAD plaintext is a concatenation of frames:

```
u16be payload_length   0..16384
u8    cmd
u32be stream_id
bytes payload
```

Client stream identifiers are odd, starting at 1. Node identifiers are even, starting at 2. A live identifier MUST NOT be reused. After Close or Reset it MAY be reused.

| cmd | name | payload |
|-----|------|---------|
| 1 | Open | target |
| 2 | Data | bytes |
| 3 | Close | empty |
| 4 | Ping | 8 bytes, echoed |
| 5 | Pong | those 8 bytes |
| 6 | Pad | ignored |
| 7 | Reset | u16be code |
| 8 | Dgram | Open-target prefix then UDP payload |
| 9 | Leave | empty; stream_id MUST be 0 |
| 10 | Tune | 4 bytes: `u8 event \|\| u8 inflight \|\| u16be header_budget`, optional 5th byte `h2_conns`. Client event ≠ 0 is one front break (1 `ENHANCE_YOUR_CALM`, 2 HTTP 413, 3 HTTP 404, 4 connection reset, 5 HTTP 429, 6 other GOAWAY), sent once on the next GET that already carries mux. Node event 0 sets live GET `header_budget` / `inflight` / `h2_conns` (0 in a field = leave unchanged). The node replies only when those values change. stream_id MUST be 0. |
| 11 | Report | `u64be applied` (highest contiguous GET seq the node has ingested). stream_id MUST be 0. Old clients ignore unknown mux commands. |

A mux frame is **resin** or **draft**. Resin is held until a Report covers its GET seq, then retransmitted on that same seq: Open, Ping, Pong, Leave, Tune, Report, Close, Reset, and Dgram to port 53. Draft is sent once (Data, Pad, other Dgram). Inner TCP or UDP produces a new frame if the application still needs the bytes. One mux Pull becomes up to three GET seqs: control resin, draft Data, then Close/Reset. Close MUST NOT share a Cookie with Data for the same stream. After an HTTP error the client retries the same seq with resin only (or Pad if the Cookie was draft-only) so the seq hole fills and draft is not replayed. A first-attempt HTTP 404 on several inflight GETs is edge noise; three **retry** 404s on the same sid means the origin lost the session. A duplicate Open for a live stream_id MUST be ignored.

Open target: `u16be host_len || host || u16be port`. Dgram payload is that prefix plus the datagram.

Leave ends the mux session. stream_id is 0. Payload is empty. The node MUST close the session, Detach the overlay peer, and Release the lease (`Online=false`). `lease_hold` still keeps the IP for the same `instance_id`. Frames after Leave in the same batch MAY be ignored. An implementation that does not yet handle Leave MUST ignore the command (unknown mux commands MUST be ignored and MUST NOT reset the session). CmdClose ends one stream; it does not end the session.

Implementations MUST batch frames (about 1–16 KiB per AEAD plaintext). They MUST NOT drop Data under buffer pressure: they apply backpressure. Per-stream receive buffer is at least 64 KiB and at most 256 KiB. An Open that cannot be completed is Reset `stream_refused` (0x0501). Ping interval is `keepalive_ms`. Idle timeout is `idle_ms`. On `get`, Pad so plaintext before AEAD is about 400–600 bytes.

Length-prefixed wrappers (`h2`, `quic`, `h3`, `ctl`): `u32be length || payload`, at most 1 MiB. Mux payload inside AEAD remains ≤ 16384.

---

## 7. Identity and timers

A subscriber is a member key. An instance is a client-generated identifier stored locally. Empty instance: the node mints one in Status; the client persists it.

The same instance reconnecting receives `ok_resumed` and the same overlay IP. A second instance with `multi` off receives `auth_already_online`. With `multi` on, further instances receive pool addresses until `multi_limit` (default 8), then `auth_limit`. A sticky IP on the key is given to the first instance.

After disconnect, `lease_hold_ms` (default 45 s) keeps the IP for that instance. Leave Releases `Online` immediately; the hold is only the IP reservation for resume. A GET session that vanishes without Leave stays `Online` until the node next sees that session.

When both Hello and Status advertise `hold`, an unexpected front drop (path change, radio gap) MUST NOT Close mux streams for `gap_hold_ms` (default 8 s). The client redials with a fresh `0x02` and the same `instance_id`; the node reattaches the parked mux only for the member that matches the new proof. Inner TCP stays up. After `gap_hold_ms` with no resume, the node Closes the mux. Leave still Closes immediately.

A client MAY pause draft Data locally (not on the wire) around a known gap; resin still goes.

Defaults: handshake 10 s, idle 120 s, keepalive 15 s, lease hold 45 s, gap hold 8 s, reconnect min 200 ms, max 8 s, fast 5 tries inside 8 s, then exponential backoff.

---

## 8. Status codes

Numeric values are stable. Names MAY grow. Numbers do not move.

| code | name | meaning |
|------|------|---------|
| 0x0000 | ok | new lease |
| 0x0001 | ok_resumed | same instance, same IP |
| 0x0101 | auth_bad_key | unknown key |
| 0x0102 | auth_revoked | key rotated or deleted |
| 0x0103 | auth_expired | join TTL |
| 0x0104 | auth_not_authorized | member disabled |
| 0x0105 | auth_limit | multi_limit |
| 0x0106 | auth_already_online | multi off, other instance |
| 0x0107 | auth_instance | bad instance id |
| 0x0108 | auth_clock | timestamp skew (unused if clockless) |
| 0x0201 | net_disabled | network off |
| 0x0202 | net_unknown | wrong network |
| 0x0203 | protocol | no common version |
| 0x0204 | rate_limit | too many handshakes |
| 0x0205 | timeout | handshake or idle |
| 0x0206 | internal | node fault |
| 0x0301 | ip_pool | pool exhausted |
| 0x0302 | ip_conflict | sticky clash |
| 0x0303 | vlan_denied | VLAN not allowed |
| 0x0401 | policy_drop | ACL or hub isolation |
| 0x0501 | stream_refused | Open failed |
| 0x0502 | stream_reset | peer Reset |

A non-OK Status forbids mux. HTTP 404 on GET auth is treated as `auth_bad_key`.

---

## 9. Overlay

The node loads a network document (CIDR, gateway, members, ACLs). Overlay IP and VLAN appear in Status. Open to an address inside the overlay CIDR is forwarded according to `peers`:

| peers | Overlay Open |
|-------|----------------|
| hub | Reset `policy_drop`. Internet Open is unchanged. Open to a managed CIDR behind a member still splices. |
| mesh | Splice to the peer session: Open `127.0.0.1:port` on that member. |
| vlan | As mesh, only if both ends share a VLAN. |

Hub splice: the receiving client accepts the stream and dials the local port.

`p2p` on the network defaults to on for mesh and vlan. When on, Status carries up to 16 peer hints (overlay IP, TLS-observed host, listen port, VLAN, 32-byte X25519 pub). A client with Hello cap `p2p` listens on TCP, announces with Open `loft.peer:<port>` plus a 32-byte X25519 pub on that stream, and MAY dial `host:port` with a 42-byte preface before falling back to hub splice:

```
"LFT" || 0x00 || src_ipv4[4] || HMAC-SHA256(ss, "lft" || nonce || dest_port || src_ipv4)[0:16] || nonce[16] || dest_port u16be
```

`ss` is X25519(local_sk, peer_pub from the hint). Two members with different `lt_` still share that pair secret, and HMAC uses it. The responder looks up `src_ipv4` in its hints. Missing pub or a failed MAC closes the socket; the initiator uses hub splice. A client with `p2p: false` MUST use hub splice only. Observed host through a relay is the relay; a direct attempt then fails and hub splice is used. When Status carries policy, both the initiator and the responder MUST apply the same Allow as hub splice before splicing local ports. A Status without policy (old node) skips that check.

Each hint record is `u8 ip | ip | u8 host | host | u16be port | u16be vlan`, then optionally `u8 32 | pub[32]`. A decoder MUST treat a following byte other than 32 as the next field (older blobs, or a peer that has not announced a pub).

ACL rules are first match, `accept` or `drop`. Empty rules still apply `peers`. Packet fields: source, destination, source VLAN, destination VLAN, source tag, destination tag, to-gateway, routed (destination matches `ip route` via a member). Address tokens: `any`, `host <IP>`, CIDR, `tag NAME` (`group` is the same token). `default deny` under `ip access-list overlay` keeps traffic to the gateway and drops the rest unless a permit matches. TCP Open uses the same Allow path as overlay ICMP. `ip route DST` next hop is an overlay IP, `member ID`, or `tag NAME` (first live member, else sticky IP).

Overlay ICMP echo is CmdDgram to `dst:1` (IP protocol ICMP) with the IPv4 packet as payload. The node answers the gateway address. Other overlay destinations follow `peers` (hub drops member-to-member; mesh/vlan splice to that member). Echo to an address outside the overlay CIDR is emitted by the node as a real ICMP echo; the reply is returned as CmdDgram `session-ip:1`. A client answers echo for its own overlay IP locally and MUST NOT fabricate replies for other overlay or internet addresses. The node MUST treat the session overlay IP as the packet source (a Darwin utun dummy `169.254.8.1` is rewritten). The client MAY restore that dummy address on echo-reply so the kernel matches the ping socket.

---

## 10. Controller and relays

A controller holds keys, leases, ACLs, and signing. A relay terminates public TLS and copies TLS-plaintext HTTP onto a trunk session: Open `hub:1` or `hub:2`. Inner mux AEAD and FinishAuth stay on the controller; the relay does not decrypt overlay frames and does not store member keys. Only a Hello with cap `relay` MAY Open `hub:*` or `ctl:1`. Members MUST NOT set that bit (it is not a front). The reference client omits it from Hello; `loft-node` in role relay sets it via ExtraCaps. A node with `enrol: list` also requires the authenticated member id to be in `relays:` (empty Hello member id is the shared-token relay; `enrol_req` still checks the id). Cap `hold` and resume `0x02` are independent of this bit. Member authentication and overlay IP remain on the controller.

```
client ──TLS──► relay ──trunk (mux hub:1|hub:2, ctl:1)──► controller
                     ▲
                     member key never stored on the relay
```

### 10.1 Roster

Ed25519 over the body:

```
u64be serial
u8 n
repeat n:
  id                 (u8+bytes)
  url                (u16be+bytes)
  decoy              (u8+bytes)
  fronts             u8 k, k × (u8+bytes), k ≤ 16
  drain              u8
  weight             u8 (0 means 100)
  path               (u8+bytes)
  mask_pub           u8 len, 0..32 bytes
```

Then `u8 pub_len || pub || u16be sig_len || sig`. Signature is Ed25519 over the body. Public key is 32 bytes. Signature is 64 bytes.

The live roster is `advertise` (id `self`) plus heartbeating relays that are not drain. Three missed beats (default interval 10 s) or a dead `ctl` stream remove the relay and increment serial.

Fetch after auth: Status `roster`. Fetch before auth: `GET {roster_path}` (default `/static/v3/manifest.json`), JSON `{"v":1,"p":"<base64url>"}`, `Cache-Control: public, max-age=60`. That GET succeeds on a CDN or a host with a real certificate. A decoy-spliced IP without session HMAC is forwarded to the origin. The document MUST NOT contain a member token. The reference client requires `controller_pub` to accept a roster. Trust-on-first-use (`tofu: true`) is an opt-in client flag, not the default.

### 10.2 Fleet (`ctl:1`)

Each message `u32be length || u8 type || body`.

| type | name | body |
|------|------|------|
| 1 | enrol_req | id, url, decoy, fronts |
| 2 | enrol_ok | signed slice |
| 3 | enrol_no | u16be code, reason |
| 4 | beat | load u8, conns u32be, program, uptime_s u32be |
| 5 | slice | signed slice |
| 6 | drain | id, u8 |

Slice (Ed25519 over the body): serial, id, url, decoy, fronts, drain, weight, beat_ms u32be, expires unix u64be, then pub and sig as in the roster. `enrol: list` requires the id in controller `relays:`. `enrol: open` accepts any authenticated trunk. YAML overrides URL, decoy, fronts, weight when set. Enrol deny: 1 unknown id, 2 bad form, 3 duplicate, 4 disabled.

Provisioning of machines and certificates is outside this protocol.

### 10.3 Address lists

The controller MAY publish named lists of addresses. Names have no protocol meaning. The client maps names to `direct`, `via`, or `drop`. If a host matches several mapped lists, apply drop, then direct, then via. Unmatched destinations use the client default (SHOULD be `via`).

Body (Ed25519, same key as the roster):

```
u64be serial
u8 nsets
repeat:
  name               (u8+bytes)
  u16be n
  repeat n: u8 kind, u16be+bytes value
```

Kind 1 exact domain, 2 suffix, 3 CIDR. Then public key and signature as in the roster.

Status carries `lists_serial` and SHA-256 of the signed blob. The client fetches `GET {lists_path}` (default `/static/v3/lists.json`) when serial or hash is newer. Operator YAML: a leading `.` or `*.` is suffix; a CIDR string is kind 3. Matching is case-insensitive for names. SOCKS matches hostname. TUN matches IP (CIDR).

---

## 11. Path compatibility

The construction is aimed at CDNs, reverse proxies, and TLS middleboxes that expect ordinary HTTPS. It is not a mixnet. Overlay IP, member key, and controller identity are secrets of the deployment, not of the public Internet.

### 11.1 Classification of the handshake

A ClientHello that matches a current browser passes the same JA3/JA4 allowlists that banks and portals already use. loft uses uTLS fingerprints in the reference client. A unique ALPN, a unique GREASE pattern, or a Go-default cipher list is a cheap tell. QUIC ALPN is `h3` for that reason.

`session_id` is 32 random-looking bytes. sid v3 is HKDF under ECDH with the node’s static X25519; a middlebox needs that private key to mark those ClientHellos. A stolen member key marks only sid v2 (the fallback used before `mask_pub` is known). A middlebox without either secret sees a string of the same length Chrome already sends.

Mask presents a dest-shaped ECDSA P-256 leaf and `ecdsa_secp256r1_sha256` CertificateVerify, bound by the serial to the same ECDH. A middlebox that verifies the public CA chain will not accept the leaf; a middlebox that only looks at names, dates, and the signature scheme sees dest-typical values. Application-record lengths on the loft path SHOULD follow a pattern sampled from the origin. Splice-only nodes skip mask.

### 11.2 Classification of the destination

An IP that serves only loft is a single point of failure. A CDN hostname, or an origin that already serves a public site, is the intended front. Relays exist so that an address can leave the roster while member identity stays on the controller.

A decoy-spliced probe sees the origin. Taking the decoy hostname or the origin IP out of service is an operational decision of the path, not a protocol constant.

### 11.3 Active probe

A scanner that opens TLS without a valid `session_id` is spliced to the configured origin, including own SNI (CT / reverse-DNS hostname), empty SNI, and a first flight that is not a TLS ClientHello. It receives the origin’s live certificate and bytes. UDP on that IP is forwarded to the same origin, so a QUIC probe sees dest, not loft. The exception is a marked loft QUIC v1 Initial. Its source connection ID is 20 bytes of `HKDF-SHA256(secret, salt = client_random, info = "loft-quic-v3")`, where client_random is an ephemeral X25519 public (top bit random) and secret is ECDH(that ephemeral, `mask_pub`). A client without `mask_pub` uses info `loft-quic-v2` with its member key as secret. The node opens the Initial with the public Initial keys (RFC 9001 §5.2), checks the mark, holds each client_random single-use for 10 minutes, and gives that client address to its quic/h3 fronts. The node's own connection IDs carry an HMAC, so a short-header packet from a new address stays on loft. Such a node advertises `quic_mark` / `h3_mark` instead of `quic` / `h3`; a client that does not mark MUST NOT use them. Forming sid v3 requires `mask_pub`; overlay Hello still requires the member key. CDN origin pull is the exception: configured `cdn_peers` CIDRs may complete TLS with the node certificate on own SNI without sid.

GET without a valid Cookie returns 404 with HTML. Cover paths (roster, lists, static assets) are ordinary GET.

### 11.4 Replay and clocks

Auth `0x02` is a nonce, an ephemeral X25519 pub, and HMAC under the member key. The same `nonce||eph_c` inside the cache TTL is an idempotent retry. A new nonce is a new handshake. The client clock is unused, so a skewed laptop still joins. Replicas accept a fresh `0x02`; they do not share this cache.

### 11.5 Roster and lists

The signed document is public. Anyone who can GET the CDN path can read live fronts. That is the same surface as a public website. The member key is absent. A client that pins `controller_pub` rejects a substituted key. Trust-on-first-use is opt-in (`tofu`); the reference client does not pin an unknown signer by default.

### 11.6 Overlay

Hub isolation Reset `policy_drop` on member-to-member Open. Mesh splices through the node. P2P is an optional TCP attempt using the TLS-observed host; NAT and relays make that attempt fail, after which hub splice is used. Direct P2P uses Status policy for the same ACL as the node. The controller holds member key hashes, leases, and overlay addresses. Other members learn a peer’s overlay IP when they Open it (mesh/vlan). With `p2p` on, Status peer hints also carry the TLS-observed host, listen port, and X25519 pub. Direct TCP authenticates with LFT (ECDH pair secret). `session_ttl_ms` is a live-session max; `lease_sec` is IP hold after disconnect. A fresh `0x02` with the same `instance_id` starts a new TTL.

### 11.7 What this document does not claim

Operation when a middlebox already has the member key. Operation when every unknown IP is firewalled with no allowlist. Operation through a TLS terminator that also holds the inner overlay key. Those are outside the protocol.

---

## 12. Limits

| limit | value |
|-------|-------|
| mux payload | ≤ 16384 |
| Hello/Status strings | ≤ 64 |
| roster URL | ≤ 255 |
| inflight GET | ≤ 8 |
| streams per session | SHOULD ≤ 256 |
| AEAD batch | ≤ 48 KiB (`ws`), ≤ header budget (`get`) |
| GET cover plaintext | about 400 bytes before AEAD |
| length-prefixed wrapper | ≤ 1 MiB |
| peer hints | ≤ 16 |
| roster entries | ≤ 255 |
| fronts per entry | ≤ 16 |
| nonce cache | TTL 10 min; sweep above 50 000 entries |
| canary_pad | 0–2048 |
| gap_hold_ms | 0–600000 (default 8000) |
| rate_bps | 0 = off; else bytes/s, u32 |

---

## 13. Versioning

Protocol 1.0 remains 1.0 until the first release. Major bump: incompatible framing or auth. Minor bump: new caps; old clients still speak. Handshake selects the highest version in both ranges.

Software version is a string on Hello and Status. Git hashes are omitted. Program name and semver are enough on the wire.

A client MUST implement at least `get` or `ws`, mux, and auth. Roster fetch, decoy, mask, lists, and overlay forwarding are required of a client that will run through a CDN or TLS middlebox, or in a meshed network.

---

## 14. Test vectors

Data, stream 1, payload `hi`:

```
00 02 02 00 00 00 01 68 69
```

Open `example.com:443` (payload of CmdOpen):

```
00 0b 65 78 61 6d 70 6c 65 2e 63 6f 6d 01 bb
```

Reset `stream_refused`: payload `05 01`.

Member key shape: `lt_` + 48 lowercase hex. SHA-256 of the whole string is what the node stores.

---

## 15. References

1. RFC 2119. Key words for use in RFCs to Indicate Requirement Levels.
2. RFC 5869. HMAC-based Extract-and-Expand Key Derivation Function (HKDF).
3. RFC 7539 / RFC 8439. ChaCha20 and Poly1305 for IETF Protocols.
4. RFC 8032. Edwards-Curve Digital Signature Algorithm (Ed25519).
5. RFC 8446. The Transport Layer Security (TLS) Protocol Version 1.3.
6. RFC 9000. QUIC: A UDP-Based Multiplexed and Secure Transport.
7. RFC 9113. HTTP/2.
8. RFC 9114. HTTP/3.
9. RFC 6455. The WebSocket Protocol.
10. RFC 1928. SOCKS Protocol Version 5.

---

## 16. Conclusion

The inner protocol is small: one AEAD, one mux, stable codes, a signed roster, named address lists, and an overlay policy. Diversity is pushed to the path (front, decoy origin, relay IP) so operators can change edges without renumbering members. Member identity and overlay address remain.
