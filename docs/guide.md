# Running loft

**English** · [Русский](guide_ru.md)

Software 1.0.9. Protocol 1.0 (loft/1.0). Spec: [protocol.md](protocol.md). Layout: [architecture.md](architecture.md).

This is a reference implementation.

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

A **controller** holds keys, leases, tickets, ACLs, and the signing key for roster and lists. It MAY listen on 443 for members.

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

A member key is `lt_` plus 48 hex characters (24 bytes). `network.conf` stores SHA-256. Plaintext lives in `{dir}/members.keys` (mode 0600), because HMAC and HKDF cannot run from the hash. With `keep_keys enable` (the default) an operator can re-read that file to emit client YAML again. `keep_keys disable` shows YAML only at issue; the node still needs the plaintext file.

When `members.keys` is empty, the node still accepts `LOFT_TOKEN` as the client secret (the old shared-token shortcut). Once any member key is present, clients authenticate with that key; `LOFT_TOKEN` remains the node secret for mask and the controller seed.

The client YAML carries `member_id` and that member’s `token`. Hello `member_id` is bound from the matching key, not from the client’s claim.

| `peers` | Overlay Open to another member |
|---------|--------------------------------|
| `hub` | Reset `policy_drop`. Internet Open unchanged. |
| `mesh` | Direct TCP if `p2p` allows, otherwise splice through the node. |
| `vlan` | As mesh, same VLAN only. |

Optional commands: `create vlan`, `ip route`, `ip access-list` (Cisco extended ACL, first match, `permit`/`deny`), `session` timers (`handshake_ms`, `idle_ms`, `keepalive_ms`, `lease_hold_ms`, `gap_hold_ms`).

The client MAY set `p2p: false` and then uses hub splice only. Direct TCP: the client listens, announces `loft.peer:<port>`, and Status carries the host the node observed on TLS. Behind NAT the attempt fails and the same flow uses the node. Through a relay the observed host is the relay.

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
| `token` | Node secret (`LOFT_TOKEN`): mask, controller seed. Member keys are `members.keys`. |
| `get_path` `ws_path` `h2_path` `h3_path` | Front paths |
| `get_h2` | GET HTTP/2 connections, 1–4 (default 1). Total inflight is shared across them. |
| `fronts` | Empty = all. Otherwise only the listed names |
| `decoy` | `host:port` list. First entry is the node identity. Empty = off |
| `mask` | Dest-shaped ECDSA handshake for loft clients; probes still splice |
| `role` | `controller` |
| `advertise` | Public URL of this front in the roster |
| `roster_path` | Public GET, default `/static/v3/manifest.json` |
| `enrol` | `list`: id must be in `relays:`. `open`: any `CapRelay` trunk |
| `relays` | Allowlist: id, url, decoy, fronts, weight, path |
| `lists` | Name → addresses (domain, `.suffix`, CIDR) |
| `lists_path` | Public GET, default `/static/v3/lists.json` |
| `canary` | Path canary: `fat_hello`, `split_hello`, `pad` |
| `cover_dir` | Static site root. Empty = built-in page |
| `replicas` | Unsigned extra `host:port` for failover |
| `quic_alpn` | Default `h3` |

Signing seed: `{dir}/controller.seed`. Give the public key to clients as `controller_pub`.

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
| `id` | Must match `relays[].id` when `enrol: list` |
| `controller` | Hub URL: `wss://host/api/v1/sync` or `https://host` |
| `advertise` | URL clients use for this edge |
| `decoy` `mask` | Identity of this IP |
| `token` | Same as the controller, or `relay_token` on both |
| `fronts` | Fronts this edge serves |
| `cert` `key` | Public TLS of this IP |

The relay opens a trunk with Hello cap `relay`, then `ctl:1` (enrol, beat) and, per member, `hub:1` or `hub:2`.

Certificates, DNS, and the machine itself are provisioned out of band. Enrol is how the controller learns that the edge is live.

---

## 6. Client

Binary `loft`. Example `clients/go/configs/client.yaml`.

```bash
./dist/clients/go/loft -c clients/go/configs/client.yaml
ALL_PROXY=socks5://127.0.0.1:1080 curl https://example.com
```

Local listeners: SOCKS5 (`127.0.0.1:1080` in `client.yaml` and the GUI). CLI `loft` / `loft-front` without an explicit port bind `127.0.0.1:18080` so they can sit beside the open client. TUN if `tun: auto`. `-if en0` (or a local/gateway IPv4) pins front sockets to that path; same flag on `loft-front`, `loft-probe`, `loft-bench`.

Wi-Fi / LTE / VPN path change: SOCKS and TUN stay up, overlay IP stays, the helper process stays. The front TCP is redialed with the resume ticket. With cap `hold`, mux streams stay for `gap_hold_ms` (default 8 s) and inner TCP stays up. Leave still tears the session.

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
| `fronts` | Fronts this client refuses |
| `bootstrap` | HTTPS URL of the roster manifest, before auth |
| `controller_pub` | 32-byte Ed25519 (64 hex). Empty = trust on first use |
| `outbounds` | `type`, `url`, `path`, `cdn`, `fingerprint` |
| `use` | List name → `direct` \| `via` \| `drop` |
| `default` | Action when no list matches (default `via`) |
| `p2p` | `false` disables direct overlay attempts |
| `lists_url` | Override GET for address lists |
| `listen.socks5` | Bind address |
| `listen.tun` | Empty = off; `auto` creates utun |
| `listen.tun_addr` | Prefix for the TUN; the address is the overlay IP from Status |
| `listen.kill_switch` | Hold native default when TUN dies |

Outbound fields: `name`, `type` (`get` \| `ws` \| `h2` \| `quic` \| `h3`), `url`, `path`, `cdn` (`yandex` \| `cloudflare` \| empty), `fingerprint` (`chrome` \| `firefox` \| `go`), `header_budget`, `inflight`, `insecure_skip_verify`.

Omitted `header_budget` is 16 KiB of HTTP header (Yandex CDN profile pins 12 KiB). Omitted `inflight` is 8 in this tree (protocol range 2–8, spec default 4). Later GET uplink is header `X-Request-Id`; the first GET is Cookie `session=` plus `X-Auth: 1`. Overlay ICMP is CmdDgram to `dst:1`; the node answers the gateway and emits internet echo.

The client does not choose decoy SNI. The node advertises it. A CDN front keeps the CDN hostname.

Yandex Cloud CDN (origin group, resource, cache off, 12 KiB GET): [yandex-cdn_ru.md](yandex-cdn_ru.md).

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

Unauthorized TCP (any SNI, broken hello, plaintext) is spliced to `decoy:`. UDP on the same port is spliced to the same origin. A loft ClientHello (valid `session_id`) is handled by the node. With `mask: true` that path presents a dest-shaped ECDSA leaf; probes still see the origin. `cdn_peers` lists CIDRs allowed to pull origin on own SNI without sid.

`session_id` is 32 bytes. sid v3 is HKDF under ECDH with the node X25519 (`mask_pub` / share-URI `pbk`). sid v2 (fallback) is HKDF under the member key. A stolen member key marks v2 hellos only.

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
| Relay `enrol_no id` | id missing from `relays:` with `enrol: list` |
| QUIC fails with `h3` on | UDP ALPN `h3` is taken by CONNECT; fail over to `h3` |
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

---

## 12. GitHub Actions

Push and pull request on `main` run `make test`.

A tag `vX.Y.Z` must match the root `VERSION` file. That tag builds:

- node tarballs and `.deb` (linux amd64, arm64)
- CLI archives (`loft`, `loft-front`, `loft-probe`, `loft-bench` for linux/darwin, amd64/arm64)
- macOS DMGs, ad-hoc signed: `loft-*-macos-arm64.dmg` and `loft-*-macos-x86_64.dmg` (`BUMP=0` so CI does not rewrite `clients/macOS/VERSION`). The runner is Apple Silicon; Intel is a Swift/Go cross-compile (`ARCH=x86_64`).

and publishes a GitHub Release. Dist also fetches the tag into [proxy.golang.org](https://proxy.golang.org) and requests [pkg.go.dev](https://pkg.go.dev/github.com/a3app/loft) so the module page follows the release. The same files are also uploaded without the version in the name (`loft-macos-arm64.dmg`, `loft-linux-amd64.tar.gz`, …) so [README download links](../README.md#download) at `/releases/latest/download/…` follow the current tag. Re-running the workflow on the same tag replaces the files. Without a tag, Actions → Dist → Run workflow only uploads artifacts.

Local packages: `make dist`. Local DMG without bumping the macOS marketing version: `make macos-dmg BUMP=0`. Developer ID and notarization stay on a Mac with a keychain; they are not in Actions.
