# loft · KeeneticOS

**English** · [Русский](README_ru.md)

Entware client: one **OpkgTun** interface that KeeneticOS uses in connection priorities and policy routing. Settings live on a LAN port; login is the Keenetic web-admin session.

Wire: [docs/protocol.md](../../docs/protocol.md). Integration: [docs/keenetic.md](../../docs/keenetic.md). Plan: [PLAN.md](PLAN.md). UX: [design/DESIGN.md](design/DESIGN.md). Mock: [design/index.html](design/index.html).

This directory is the catalog. No binary yet.

## Scope

| On the router | Role |
|---------------|------|
| loft (get / ws / h2 / quic / h3) | loft overlay. One OpkgTun. |
| VLESS | Later. Second loft outbound when loft is filtered. |
| SSTP, WireGuard, OpenVPN, IPsec, OpenConnect | Stock KeeneticOS. Not this package. |

The client must not install a default route. NDM owns the table. Front IPs stay on the ISP path so the session does not hairpin.

## Layout (planned)

```
clients/Keenetic/
  README.md          this file
  PLAN.md            build phases
  design/            UX spec and HTML mock
  cmd/               (later) loft-keenetic: session + panel
  pack/              (later) init.d, ipk, ndmc helpers
```

The session is the Go client in `clients/go` with TUN and `default_route` off. The panel is a small HTTP server that checks the Keenetic `/auth` cookie.

## Requires

KeeneticOS **5.0+** (OpkgTun in policies). Components: OPKG, TUN/TAP. Entware on NAND or USB EXT4. Architectures: aarch64, armv7, mipsel.
