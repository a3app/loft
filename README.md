# loft

**English** · [Русский](README_ru.md)

From the street, ordinary HTTPS. Inside, a holdable admin channel for lossy and intermittent paths.

**Inside:** loft overlay for TLS · protocol loft/1.0 · HTTPS-shaped fronts · mux · ChaCha20-Poly1305 · spec and reference node/clients.

**loft** is loft overlay for TLS. Protocol **loft/1.0**. Share URI **`loft://`**.

A private overlay in the on-wire shape of ordinary HTTPS: multiplexed streams over GET, WebSocket, HTTP/2, QUIC, or HTTP/3, sealed with ChaCha20-Poly1305. This repository is the open specification and a reference node plus clients (macOS app, Linux and macOS CLI).

Software `1.0.3`. Protocol `loft/1.0`. Anyone can implement loft from [docs/protocol.md](docs/protocol.md).

## Operators

loft is a private overlay for people who run networks. The job is a stable channel for secure server administration when the public path is lossy or drops for a few seconds: lab LAN from outside, a server fleet, site-to-site. Traffic on the public path is ordinary HTTPS, so it works through the CDNs, reverse proxies, and TLS middleboxes those sites already use. The path canary is the origin’s testimony that TLS is still ordinary HTTPS. If a middlebox mangles the handshake, the session does not come up.

## Download

Files from the [latest GitHub Release](https://github.com/a3app/loft/releases/latest). Dist uploads these names on every `v*` tag, so the links follow the current release.

| | Apple Silicon / arm64 | Intel / amd64 |
|--|--|--|
| macOS app | [DMG](https://github.com/a3app/loft/releases/latest/download/loft-macos-arm64.dmg) | [DMG](https://github.com/a3app/loft/releases/latest/download/loft-macos-x86_64.dmg) |
| macOS CLI | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-darwin-arm64.tar.gz) | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-darwin-amd64.tar.gz) |
| Linux CLI | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-linux-arm64.tar.gz) | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-linux-amd64.tar.gz) |
| Node | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-node-linux-arm64.tar.gz) · [deb](https://github.com/a3app/loft/releases/latest/download/loft-node_arm64.deb) | [tar.gz](https://github.com/a3app/loft/releases/latest/download/loft-node-linux-amd64.tar.gz) · [deb](https://github.com/a3app/loft/releases/latest/download/loft-node_amd64.deb) |

Checksums: [clients](https://github.com/a3app/loft/releases/latest/download/loft-clients-SHA256SUMS.txt) · [node](https://github.com/a3app/loft/releases/latest/download/loft-node-SHA256SUMS.txt). macOS DMG is the menu-bar app (ad-hoc signed). CLI archives are `loft`, `loft-front`, `loft-probe`, `loft-bench`.

### Why loft?

**loft overlay for TLS.** Recursive: the product is the expansion. The word is an attic, a floor above ordinary HTTPS.

Share URI `loft://`. YAML `kind: loft`. Protocol version `loft/1.0`.

## Protocol

```
application → SOCKS5 / TUN
    mux (Open / Data / Close / Dgram / Leave)
      ChaCha20-Poly1305
        get | ws | h2 | quic | h3
          TLS 1.3
            TCP 443 or UDP 443
```

| | |
|--|--|
| [docs/protocol.md](docs/protocol.md) | Wire. A third party can implement from this file. |
| [docs/architecture.md](docs/architecture.md) | How this tree is laid out. |
| [docs/guide.md](docs/guide.md) | Build, configure, run, Debian. |
| [docs/yandex-cdn_ru.md](docs/yandex-cdn_ru.md) | Yandex Cloud CDN operator guide (RU). |
| [docs/keenetic.md](docs/keenetic.md) | KeeneticOS Entware client (OpkgTun). Catalog: [clients/Keenetic/](clients/Keenetic/). |
| [docs/ios.md](docs/ios.md) | iOS Packet Tunnel client. Catalog: [clients/iOS/](clients/iOS/). |
| [docs/android.md](docs/android.md) | Android VpnService client. Catalog: [clients/Android/](clients/Android/). |
| [docs/share.md](docs/share.md) | Client share URI `loft://` and `.loft` (one or several connections). |

## Repository

```
docs/             protocol, architecture, guide
internal/         wire, mux, crypto, transport, overlay
server/           loft-node (controller or relay)
clients/go        loft, loft-front, loft-probe
clients/macOS     menu-bar app (launches loft-front)
clients/Keenetic  Entware / OpkgTun (plan and mock)
clients/iOS       Packet Tunnel (thin host + appex)
clients/Android   VpnService (thin Java host)
clients/mobile    shared Go engine (gomobile bind)
brand/            mark and icons
```

## Build

Go 1.24+. From the repository root:

```bash
make test
make node       # ./loft-node
make client     # dist/clients/go/loft
make macos      # dist/clients/macOS/loft.app
make macos-dmg  # dist/clients/macOS/loft-*-macos-<arch>.dmg
make macos-dmgs # both arm64 and x86_64 DMGs (CI)
make dist       # node tarballs/.deb + CLI archives
```

Tag `vX.Y.Z` (must match `VERSION`) runs GitHub Actions: tests, `make dist`, macOS DMGs arm64 and x86_64 (ad-hoc), and a Release. Manual: Actions → Dist.

```bash
export LOFT_TOKEN='change-me-long-random'
./loft-node -c server/configs/server.yaml -dir /var/lib/loft
./dist/clients/go/loft -c clients/go/configs/client.yaml
ALL_PROXY=socks5://127.0.0.1:1080 curl https://example.com
```

Debian package and installer: [server/debian/README.md](server/debian/README.md).

## License

[Apache License 2.0](LICENSE). loft/1.0 may be implemented from [docs/protocol.md](docs/protocol.md) without a fee.
