# Running loft

**English** · [Русский](guide_ru.md) · [中文](guide_zh.md)

Software 1.0.31. Protocol 1.0 (loft/1.0). Spec: [protocol.md](protocol.md). Layout: [architecture.md](architecture.md).

A reference implementation is described.

---

## 1. Build

Go 1.24 or newer, from the repository root:

```bash
make test
make node
make client    # dist/clients/go/loft
make probe     # dist/clients/go/loft-probe
make macos     # dist/clients/macOS/loft.app
make macos-dmg # dist/clients/macOS/loft-*-macos-<arch>.dmg  (Applications symlink)
make macos-dmgs # both arm64 and x86_64 DMGs (CI)
make android   # dist/clients/Android/loft.app + loft-tv.app + loft-gtv.app + APKs
make ios       # dist/clients/iOS/loft.app (macOS) + iphonesimulator/loft.app
make windows   # dist/clients/Windows/x64 and x86
make dist      # node tarballs/.deb + CLI archives
```

```bash
go test ./...
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o loft-node ./server/cmd/loft-node
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft ./clients/go/cmd/loft
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft-probe ./clients/go/cmd/loft-probe
```

`--version` prints program name, semver, and protocol range.

`make client` is `dist/clients/go/loft`. `make probe` is `loft-probe`. The macOS app also needs `loft-front`:

```bash
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft-front ./clients/go/cmd/loft-front
```

---

## 2. Node roles

A **controller** holds keys, leases, ACLs, and the signing key for roster and lists. It MAY listen on 443 for members.

A **relay** terminates public TLS and copies plaintext to the controller on a trunk. Its IP is disposable. Member identity and overlay IP remain on the controller.

Both roles are the same binary. Role is YAML `role: controller` or `role: relay`.

---

## 3. Overlay network

Path: `{dir}/network.conf` (legacy `{dir}/network.yaml` still loads). The node will not start without it.

```
network home
name home
enable
ipif overlay 10.8.0.0/24
  gateway 10.8.0.1
  pool 10.8.0.10 10.8.0.220
  mtu 1280
  peers hub
  p2p enable
  dns enable
  dns 10.8.0.1
  dns upstream https://1.1.1.1/dns-query
  dns upstream tls://1.1.1.1
  listen 0.0.0.0 443 front ws
create member alice
config member alice multi enable 4
config member alice key_sha256 <hex>
enable member alice
ip access-list overlay
  permit tcp host 10.8.0.2 host 10.8.0.5 eq 22
```

`dns.servers` is pushed in Status and programmed on the client TUN. Queries to the gateway on port 53 (UDP CmdDgram and TCP Open) are answered by the node. `dns.upstream` is how the node fetches answers: `https://` is DNS-over-HTTPS, `tls://` is DNS-over-TLS (port 853), a bare IP is UDP/53. `quic://` is reserved. Empty `servers` means the gateway. `enabled: false` leaves OS DNS alone.

A member key is `lt_` plus 48 hex characters (24 bytes). `network.conf` stores SHA-256. Plaintext lives in `{dir}/members.keys` (mode 0600), because HMAC and HKDF cannot run from the hash. An operator can re-read that file to emit client YAML again. Old configs may still carry `keep_keys`; the node ignores it.

**Reload.** `systemctl reload loft-node` (SIGHUP) re-reads `network.conf` and `members.keys` without stopping the node. Members whose entry did not change stay connected; a removed, disabled or edited member is dropped and reconnects with the new settings. A change that clients learn at Hello (CIDR, gateway, MTU, peers, p2p, DNS, routes, VLANs, session timers; rules when p2p is on) reconnects everyone, still without a restart. A file that does not load is logged and the running config stays. `node.conf`, the certificate and the token still need a restart.

**Exit channels.** By default members leave through the server's own route. `egress.conf` next to `network.conf` (written by loft-panel) sends them elsewhere:

```
channel wg-nl dev wg-nl check ping probe 1.1.1.1 every 10 fails 3   # a kernel interface (WireGuard, OpenVPN, ZeroTier)
channel wg-fi dev wg-fi
channel px socks5 192.0.2.40:1080 user u pass "p w" udp proxy check tcp probe example.com:443
channel web http 192.0.2.41:3128 tls      # HTTP or HTTPS proxy (CONNECT)
channel office sstp vpn.example.org:443 user office pass x auth mschapv2
prio main wg-nl wg-fi px then direct limit wg-fi 50/10   # a priority list: the first working channel, then direct or block; wg-fi capped at 50/10 Mbit/s
prio both wg-nl wg-fi then direct multi  # multipath: new connections take the working channels in turn
prio work office then block
rule member bob via work
rule tag staff via main
rule list blocked via main                # a list from node.conf lists
default via main
```

Rules match in order, then `default`; each names a priority list (or `direct`, `block`). A list takes its first channel that may carry traffic, then the next, then its `then` step. A channel is skipped while it is `off`, has no interface, failed a connect in the last 20 s, or fails its link check: `check ping` (kernel interfaces), `check tcp`, `check tls` (handshake after the connect) or `check auto` (HTTPS to ya.ru, nic.ru, google.com; any one is enough) sends a probe every `every` seconds (10) and calls the link lost after `fails` failures in a row (3); one success brings it back. `direct check KIND …` is the same probe of the host default route; while it fails, priority lists skip `direct`. A rule may still name a channel; then that channel's `fail` chain (a channel, `direct`, `block`) is the order, as in older files. `multi` turns a list into multipath: each new TCP connection starts one channel further (a member's UDP to one host keeps one way). `limit CHANNEL IN/OUT` caps that channel for everyone who leaves by the list, in Mbit/s (IN towards members, 0 = none). A proxy may go `via` another channel. `dev` binds the socket to the interface (SO_BINDTODEVICE); the system needs a routing rule for it (`ip rule add oif DEV lookup TABLE` with a default route in TABLE), which loft-panel sets up. TCP streams and member datagrams follow the rules; ICMP and DNS queries to the node leave directly. The node re-reads the file on SIGHUP and writes `egress-status.json` (state, bytes, connections) every 5 s.

`LOFT_TOKEN` is the node secret (mask). Clients use per-member keys in `members.keys`. `auth shared enable` (`auth_shared: true` in YAML) accepts `LOFT_TOKEN` as the client secret when the ring is empty — the old one-token overlay. A non-empty ring never mixes the node token, even with that flag on. New installs leave `auth shared disable` and issue an `operator` key.

The client YAML carries `member_id` and that member’s `token`. Hello `member_id` is bound from the matching key, not from the client’s claim.

| `peers` | Overlay Open to another member |
|---------|--------------------------------|
| `hub` | Reset `policy_drop`. Internet Open unchanged. Managed CIDR behind a member still splices. |
| `mesh` | Direct TCP if `p2p` allows, otherwise splice through the node. |
| `vlan` | As mesh, same VLAN only. |

Optional commands: `create vlan`, `ip route DST VIA|member ID|tag NAME` (next hop is an overlay IP, a member id, or the first live/sticky member with that tag), `ip access-list` (Cisco extended ACL, first match, `permit`/`deny`, `tag NAME` / `group NAME`, `default deny`), `config member ID tags …`, `session` timers (`handshake_ms`, `idle_ms`, `keepalive_ms`, `lease_hold_ms`, `gap_hold_ms`, `ttl_ms`) and `session rate_bps`. `ttl_ms` is the live-session max (0 = off). `lease_hold_ms` is IP hold after disconnect. `rate_bps` is the bulk Data/Dgram cap for a member session, both directions (0 = off). Override with `config member ID rate_bps N`.

```
config member lab ipaddress 10.8.0.5
config member lab tags lab
config member alice tags ops
ip route 192.168.10.0/24 member lab
ip route 10.20.0.0/16 tag lab
ip access-list overlay
  default deny
  permit tcp tag ops tag db eq 5432
  permit ip tag ops 192.168.10.0/24
session ttl_ms 86400000
```

`default deny` still allows the overlay gateway (DNS). Mesh without that line stays open between members. Status pushes the CIDR to every member except the one whose overlay IP is VIA (that host already has the LAN). TCP Open to `192.168.10.5:22` splices as `192.168.10.5:22` on the lab member; overlay-IP Open still splices as `127.0.0.1:port`. Direct P2P uses the same ACL (Status policy). An old Status without policy skips that check.

The client MAY set `p2p: false` and then uses hub splice only. Direct TCP: the client listens, announces `loft.peer:<port>` plus a 32-byte X25519 pub, and Status carries the host the node observed on TLS. The connection is encrypted from its first byte (pair ECDH and fresh keys) and has no fixed bytes. Behind NAT the attempt fails and the same flow uses the node. Through a relay the observed host is the relay. The client re-handshakes at about 80% of `session_ttl_ms`.

---

## 4. Controller

Example: `server/configs/server.yaml`.

```bash
export LOFT_TOKEN='long-secret'
./loft-node -c server/configs/server.yaml -dir /var/lib/loft
```

| Flag | Meaning |
|------|---------|
| `-c` | YAML: role, decoy, fronts, roster, TLS |
| `-dir` | `network.conf`, `members.keys`, `controller.seed` |
| `-listen` | Override listen address |
| `-cert` `-key` | PEM if omitted from YAML |
| `--version` | Versions |

| Field | Meaning |
|-------|---------|
| `listen` | `:443` |
| `cert` `key` | Node certificate. With decoy+mask a loft client is shown a dest-shaped leaf. |
| `token` | Node secret (`LOFT_TOKEN`): mask. Member keys are `members.keys`. |
| `get_path` `ws_path` `h2_path` `h3_path` | Front paths |
| `get_h2` | GET HTTP/2 connections, 1–4 (default 1). Total inflight is shared across them. |
| `fronts` | Empty = `get`. List `ws`, `h2`, `quic`, `h3` to opt in. `connect` is mux-off HTTP CONNECT splice; off unless listed. `l3` is HTTP/2 CONNECT then IP datagrams; off unless listed |
| `inner` | Extra data ciphers the node may confirm: `clear`, `aes`. Empty = ChaCha20-Poly1305 only. ChaCha20-Poly1305 stays allowed and cannot be disabled. `get` never confirms `clear` or `aes`. A restart applies it. SIGHUP does not re-read `node.conf`. |
| `cc` | Congestion control clients may ask for: `bbr`, `fixed` (`off` or empty = every session stays on the system default). Each client decides whether to ask; the node only allows. Only sessions over QUIC switch (`quic`, `h3`, or `l3` over HTTP/3); TCP fronts keep the kernel's default. Not through a relay, not on `get`. A restart applies it. |
| `cc_max UP DOWN` | `fixed` caps in Mbit/s: how fast a client may send and how fast the node sends to it. `0` = no cap. |
| `edge` | GET preset `plain` or `tight` (Status byte 0 or 1). `tight`: Cookie budget 16 KiB, 16 inflight, 6 s trip. For an origin behind a CDN or reverse proxy that drops a fat concurrent HEADERS burst. Default `plain`. |
| `decoy` | `host:port` list. First entry is the node identity. Empty = off |
| `decoy_proxy` | `enable`: each TCP splice starts with a PROXY v1 line carrying the client address. Only for your own web server (nginx `listen … proxy_protocol`); a public site would break. Default off. UDP is not affected |
| `mask` | Dest-shaped ECDSA handshake for loft clients; probes still splice |
| `role` | `controller` |
| `advertise` | Public URL of this front in the roster |
| `roster_path` | Public GET, default `/static/v3/manifest.json` |
| `auth shared` | `enable`: `LOFT_TOKEN` as client secret when `members.keys` is empty. Default `disable`. |
| `relays` | Relays allowed to enrol: id, url, decoy, fronts, weight, path, `key_sha256`. Fields set here override that relay’s url, decoy, fronts, and weight. The relay keys themselves are in `{dir}/relays.keys` (id → key, mode 0600) |
| `lists` | Name → addresses. A leading `.` or `*.` is a suffix; a CIDR is kind 3; any other string is an exact domain |
| `lists_path` | Public GET, default `/static/v3/lists.json` |
| `canary` | Path canary: `fat_hello`, `split_hello`, `pad` |
| `cover_dir` | Static site root. Empty = built-in page |
| `replicas` | Unsigned extra `host:port` for failover |
| `quic_alpn` | Default `h3` |

Signing seed: `{dir}/controller.seed`, random, made on first start (or by `-print-pub`) and not derived from the token. Give the public key to clients as `controller_pub`. To replace the key without reissuing clients: `loft-node -dir {dir} -roll-key 30` (or the panel, Access → Change key), then restart; clients and relays that pin the old key follow a signed hand-over for 30 days (protocol §10.4).

Live roster = `advertise` plus heartbeating relays that are not drain. Three missed beats (~30 s) or a dead `ctl` stream drop the relay and increment serial.

Paths SHOULD look like assets or APIs of an ordinary site. Defaults in this tree: `/static/v3/build`, `/api/v1/sync`, `/api/v1/tunnel`, `/static/v3/manifest.json`, `/static/v3/lists.json`.

---

## 5. Relay

Example: `server/configs/relay.yaml`.

```bash
./loft-node -c server/configs/relay.yaml -dir /var/lib/loft-relay
```

| Field | Meaning |
|-------|---------|
| `role` | `relay` |
| `id` | Must match `relays[].id` on the controller and the id its key was issued for |
| `controller` | Hub URL: `wss://host/api/v1/sync` or `https://host` |
| `advertise` | URL clients use for this edge |
| `decoy` `mask` | Identity of this IP |
| `controller_pub` | The controller’s public key (64 hex); slices signed by any other key are refused |
| `{dir}/relay.key` | The key the controller issued for this relay (mode 0600). `LOFT_TOKEN` here is this relay’s own secret (mask), not the controller’s |
| `fronts` | Fronts this edge serves |
| `cert` `key` | Public TLS of this IP |

The relay opens a trunk with Hello cap `relay`, then `ctl:1` (enrol, beat) and, per member, `hub:1` or `hub:2`.

Certificates, DNS, and the machine itself are provisioned out of band. Enrol is how the controller learns that the edge is live.

### Standby controller

A standby is a relay that keeps a copy of the controller's state and can take its place. Mark the relay "Standby controller" in the panel (`relay … replica` in `node.conf`) and install it with `--role replica`; its install command already lists the controller. After `enrol_ok` the controller sends it `network.conf`, `members.keys`, `relays.keys`, `controller.seed`, `controller.roll`, `lists.conf`, `node.conf` and the node token, and sends them again when any of them changes; the standby keeps them in `{dir}/standby`, and the panel shows when it last got them. Every relay's install lists the standbys after the controller (`controller URL standby-URL …`), so relays try them in that order.

To take over when the controller is gone, on the standby as root:

```bash
loft-node -dir /var/lib/loft -c /etc/loft/node.conf -promote
systemctl restart loft-node
```

The standby becomes the controller with the same members, relay keys, controller key and mask secret, so clients and relays need no new links; its own address, certificate and decoy stay. Clients find it through the roster, relays through their controller list. The old controller must not come back as a controller next to it: reinstall it as a standby of the new one. If it is started anyway, the first relay that reaches it (relays try every controller address every two minutes) shows it the takeover; it stops, writes `fenced` in its node directory and does not start as a controller again until that file is removed.

**Automatic promotion.** Tick "Promote automatically" for the standby (`auto_promote` on its relay line). Relays are then witnesses: about every 10 s each relay sends each standby a report, signed with its relay key and dated, saying whether it reaches the controller (an HTTPS POST inside TLS to the standby's own public address, the standbys being the later addresses in the relay's `controller` line; one connection is kept and reports go every 10 s give or take 2 s, so no extra port is needed). The standby takes over when it has not reached the controller for `promote_after` (300 s by default) and more than half of the relays, at least two, report the same in the last 30 s, so a broken link between the standby and the controller alone does not promote it. It writes `promoted` into `{dir}` and exits; systemd restarts it as the controller from its copy. `loft-node -promote` afterwards makes that permanent in `node.conf` and `loft.env`.

---

## 6. Client

Binary `loft`. Example `clients/go/configs/client.yaml`.

```bash
./dist/clients/go/loft -c clients/go/configs/client.yaml
ALL_PROXY=socks5://127.0.0.1:1080 curl https://example.com
```

Local listeners: SOCKS5 (`127.0.0.1:1080` in `client.yaml` and the GUI). CLI `loft` / `loft-front` without an explicit port bind `127.0.0.1:18080` so they can sit beside the open client. TUN if `tun: auto`. `-if en0` (or a local/gateway IPv4) pins front sockets to that path; same flag on `loft-front`, `loft-probe`, `loft-bench`. `loft-front` / `loft-bench` `-h2-max-age` is YAML `h2_max_age_ms` (0 = keep until error).

Wi-Fi / LTE / VPN path change: SOCKS and TUN stay up, overlay IP stays, the helper process stays. GET replaces HTTP/2 from the new egress; sid and mux stay. Other fronts Park; resume is a fresh `0x02` and the same `instance_id`. With cap `hold`, Parked mux streams stay for `gap_hold_ms` (default 8 s) and inner TCP stays up. Leave still tears the session.

Client YAML `pause: starlink` skips sending mux Data around the known 15 s LEO handover windows. Control frames still go. This is local; it is not a protocol field.

### Overlay egress probe

`loft-probe` checks ICMP, TCP, UDP, DNS, HTTP, and HTTPS through loft without rewriting system routes and without opening utun. Use it when ping works and sites do not, or when default-route is off.

```bash
make probe
./dist/clients/go/loft-probe                                          # icmp + tcp + dns + http/https
./dist/clients/go/loft-probe 8.8.8.8                                  # ICMP echo via the overlay
./dist/clients/go/loft-probe -proto tcp 1.1.1.1:443
./dist/clients/go/loft-probe -proto http https://ifconfig.me/ip       # public IP as the node sees it
./dist/clients/go/loft-probe -via socks -proto tcp example.com:443    # live loft SOCKS, no extra session
./dist/clients/go/loft-probe -c "$HOME/Library/Application Support/loft/profiles/example.yaml" 8.8.8.8
./dist/clients/go/loft-probe -if en0 -c "$HOME/Library/Application Support/loft/profiles/example.yaml" 8.8.8.8
```

`-via mux` opens its own GET/WS session (covers ICMP). `-via socks` uses a live SOCKS5 (GUI `127.0.0.1:1080`, debug CLI `127.0.0.1:18080`; TCP/UDP/HTTP; ICMP skipped). `-via auto` prefers a profile, then live SOCKS.

| Field | Meaning |
|-------|---------|
| `token` | Member key |
| `instance_id` | Overlay instance. Empty: `{config}.instance` next to the YAML, created on first run |
| `select` | `auto` or an outbound name |
| `fronts` | Extra filter. Empty = outbound types (stock is GET) |
| `inner` | Data-cipher ask: `clear` or `aes`. Empty or `chacha` omits the Hello byte (ChaCha20-Poly1305). The node confirms that id or the session stays on ChaCha20-Poly1305. `get` stays ChaCha20-Poly1305. `loft-front -inner` is this field. |
| `cc` | Congestion control ask: `off` (default), `bbr`, `fixed`. Applies only on `quic` and `h3`; TCP fronts keep the default. Leave it off on a network that singles out unusual traffic. The node answers with what both sides use; a node that does not allow it keeps the default. |
| `cc_up_mbps`, `cc_down_mbps` | For `fixed`: how fast this client sends and how fast it can receive, Mbit/s. Set them to the real line speed: fixed rate does not slow down on loss. `0` leaves that direction on the default. |
| `bootstrap` | HTTPS URL of the roster manifest, before auth |
| `controller_pub` | 32-byte Ed25519 (64 hex). Required to accept a roster and address lists |
| `tofu` | `true` pins the first valid signer when `controller_pub` is empty (default off) |
| `outbounds` | `type`, `url`, `path`, `cdn`, `fingerprint` |
| `use` | List name → `direct` \| `via` \| `drop` |
| `default` | Action when no list matches (default `via`) |
| `p2p` | `false` disables direct overlay attempts. Direct TCP is encrypted (pair ECDH) |
| `lists_url` | Override GET for address lists |
| `listen.socks5` | Bind address |
| `listen.tun` | Empty = off; `auto` creates utun |
| `listen.tun_addr` | Prefix for the TUN; the address is the overlay IP from Status |
| `listen.kill_switch` | Hold native default when TUN dies |
| `listen.http` | LAN operator page (MikroTik `:8090`) |
| `listen.http_pass` | Optional password for that page |
| `listen.bind` | Same as `-if`; CLI `-if` wins |
| `listen.uplink` | RouterOS WAN name for container egress (`ether1`, `lte1`) |

Outbound fields: `name`, `type` (`get` \| `ws` \| `h2` \| `quic` \| `h3`), `url`, `path`, `cdn` (`yandex` \| `cloudflare` \| empty), `fingerprint` (`chrome` \| `firefox` \| `go`), `header_budget`, `inflight`, `h2_max_age_ms`, `insecure_skip_verify`.

`fingerprint` `chrome` is a uTLS Chrome ClientHello. `firefox` is the other browser shape. `go` is the stock Go cipher list; JA3/JA4 filters already mark it as a non-browser. The protocol requires the browser shape on a path that filters browsers. It does not require uTLS.

Omitted `header_budget` is 16 KiB of HTTP header (the Yandex CDN profile pins the same 16 KiB). Omitted `inflight` is 8 in this tree (protocol range 2–8, spec default 4). Omitted `h2_max_age_ms` is 0: keep the GET HTTP/2 connection until error. A positive value (floor 15 s, cap 1 h) replaces that TCP; mux and GET session id stay. GET uplink is Cookie `session=`; the first GET is tag `0x02` with the session id already in the path. Overlay ICMP is CmdDgram to `dst:1`; the node answers the gateway and emits internet echo.

The client does not choose decoy SNI. The node advertises it. A CDN front keeps the CDN hostname.

Yandex Cloud CDN (origin group, resource, cache off, 16 KiB GET): [yandex-cdn_ru.md](yandex-cdn_ru.md).

`bootstrap` MUST be a host with a real certificate or a CDN. GET to a decoy IP without session HMAC is spliced to the origin.

Roster: `GET /static/v3/manifest.json` → `{"v":1,"p":"<base64url>"}`. No member key in the file. After handshake, Status updates the roster. On failure the client tries fronts, unsigned `replicas`, then the roster.

### Address lists

The controller publishes named sets. The client maps names to actions:

```yaml
# server.yaml
lists:
  banks: [example-bank.test, .bank.example]
  video: [youtube.com]
  ads: [ads.example]

# client.yaml
default: via
use:
  banks: direct
  video: via
  ads: drop
```

If a host matches several mapped lists: drop, then direct, then via. SOCKS matches hostname. TUN matches IP (CIDR). Fetch: `GET /static/v3/lists.json`. Status carries serial and hash.

List names are operator vocabulary. The protocol treats them as opaque strings.

---

## 7. Decoy

Unauthorized TCP (any SNI, broken hello, plaintext) is spliced to `decoy:`. UDP on the same port is spliced to the same origin, except loft QUIC whose Initial carries the loft mark: the quic and h3 fronts keep working behind the decoy (clients from this release on). A loft ClientHello (valid `session_id`) is handled by the node. With `mask: true` that path presents a dest-shaped ECDSA leaf; probes still see the origin. `cdn_peers` lists CIDRs allowed to pull origin on own SNI without sid. When the origin is this host's nginx, `decoy_proxy enable` lets it see the real client address (logs, limits, the panel's login guard).

`session_id` is 32 bytes. sid v3 is HKDF under ECDH with the node X25519 (`mask_pub` / share-URI `pbk`). sid v2 (fallback) is HKDF under the member key. A stolen member key marks v2 hellos only.

With mask on, a hello whose SNI is the decoy name is admitted only as sid v3 (`mask_pub` / `pbk`); sid v2 is spliced to the origin. If the ClientHello offers X25519MLKEM768 (group `0x11EC`), mask selects that group ([RFC 10024](https://www.rfc-editor.org/rfc/rfc10024), Post-Quantum Traditional (PQ/T) Hybrid Key Agreement Mechanisms for TLS 1.3). The leaf is still not a public CA chain. A repeat of the same `client_random` within 10 minutes is spliced as a probe. On a mask node a QUIC Initial is admitted only with the v3 mark.

Origin hostnames in example YAML are examples. Choose origins whose public site the path is unwilling to drop, and whose TLS the node can splice. They are not compiled into the protocol.

---

## 8. Out of band

Virtual machines, DNS, public certificates, and binary updates are outside the protocol. The wire carries enrol, beat, slice, drain, roster, and lists.

A typical deployment:

1. Issue a certificate for the public name (or terminate TLS on a CDN).
2. Write `network.conf` and start the controller with `-dir`.
3. Publish `controller_pub` and a `bootstrap` URL on a host that is not a decoy-spliced IP.
4. Start relays with `id` matching `relays:`.
5. Give members a key and a `client.yaml` (or a `.loft` file).

---

## 9. Faults

| Symptom | Check |
|---------|--------|
| `token is required` | `LOFT_TOKEN` or `token:` |
| `network disabled` | `enable` in `network.conf` |
| TLS hangs | decoy SNI, mask, certificate, `session_id` |
| Manifest is foreign HTML | bootstrap hit a decoy IP without HMAC |
| Relay `enrol_no id` | id missing from `relays:`, drained, or not the id the relay key was issued for |
| `auth_already_online` | another instance holds the key; enable `multi` or stop the other |
| `auth_limit` | `multi_limit` |
| `policy_drop` | overlay Open under `peers: hub`, or an ACL drop |
| SOCKS works, TUN silent | `tun: auto`, privileges, excluded outbound hosts |
| Native default gone after TUN death | `kill_switch`; restore routes or reconnect |
| Ping ~1 s on GET | idle GET pace is `keepalive/2` (floor 1 s); reconnect so the client has catching-down after Dgram |
| Upload stalls / TLS bad record | GET `inflight` omitted used to default to 1; this tree now defaults to 8 |

---

## 10. Debian

The node on a Debian host: [server/debian/README.md](../server/debian/README.md).

```bash
make dist-node
# copy dist/node/linux-amd64/loft-node-<version>-linux-amd64.tar.gz to the host
tar -xzf loft-node-*-linux-amd64.tar.gz
cd loft-node-*-linux-amd64
sudo ./install.sh --unattended
```

Relay:

```bash
sudo ./install.sh --role=relay --id=ru1 \
  --controller=wss://controller.example/api/v1/sync \
  --advertise=https://203.0.113.10
```

Debian package: `make dist-node`, then `dpkg -i dist/node/linux-amd64/loft-node_*_amd64.deb`.

The installer creates user `loft`, `/etc/loft/loft.env` (token), a self-signed certificate, systemd unit, and `/root/loft-client.yaml`. Token stays in the environment file. `loft-node -print-pub` prints `controller_pub`.

---

## 11. Tests

```bash
make test
go test ./internal/wire ./internal/crypto ./internal/mux ./internal/netconf ./internal/transport
```

Wire tests cover frames, Hello/Status, roster tamper, lists, fleet. Transport tests cover decoy session_id, roster fetch, overlay policy. Mask on TCP loopback MAY skip on some hosts. `make test` also runs `server/debian/install_os_test.sh`.

**Tor reachability.** `loft-reach` (`make reach`) checks whether Tor can reach its network from the current line: with no bridges, through each of Tor Browser's built-in bridges (obfs4, snowflake, meek, ...), through bridges from a file (`-bridges`), and through a running loft client (`-loft-socks`, default `127.0.0.1:1080`). Every case starts its own tor with an empty data directory, waits for `Bootstrapped 100%`, then asks check.torproject.org whether the request came out of Tor. tor and the transports are taken from Tor Browser (`-tor-browser`, found in the usual install places by default). The results go to `loft-reach-<UTC time>/report.md` and `logs/<case>.log`; IP addresses, bridge fingerprints and certificates, and the home folder are replaced in both.

```bash
loft-reach -network "MTS mobile 4G" -place Moscow
loft-reach -cases direct,builtin:obfs4,loft -timeout 5m
loft-reach -list
```

---

## 12. GitHub Actions

Push and pull request on `main` run `make test`.

A tag `vX.Y.Z` must match the root `VERSION` file. That tag builds:

- node tarballs and `.deb` (linux amd64, arm64)
- CLI archives (`loft`, `loft-front`, `loft-probe`, `loft-bench` for linux/darwin, amd64/arm64)
- macOS DMGs, ad-hoc signed: `loft-*-macos-arm64.dmg` and `loft-*-macos-x86_64.dmg` (`BUMP=0` so CI does not rewrite `clients/macOS/VERSION`). The runner is Apple Silicon; Intel is a Swift/Go cross-compile (`ARCH=x86_64`).

and publishes a GitHub Release. Dist also fetches the tag into [proxy.golang.org](https://proxy.golang.org) and requests [pkg.go.dev](https://pkg.go.dev/github.com/a3app/loft) so the module page follows the release. Asset names include the version (`loft-1.0.31-macos-arm64.dmg`, `loft-node_1.0.31_amd64.deb`). Re-running the workflow on the same tag replaces the files. Without a tag, Actions → Dist → Run workflow only uploads artifacts.

Local packages: `make dist`. Local DMG without bumping the macOS marketing version: `make macos-dmg BUMP=0`. Developer ID and notarization stay on a Mac with a keychain; they are not in Actions.
