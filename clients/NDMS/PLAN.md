# Plan · loft on KeeneticOS

Software 1.0.0. Protocol 1.0. This plan does not raise either version.

## Goal

A routed loft client on Keenetic where every connection (loft profile, VLESS or Trojan) shows up as **its own connection** in Connection Priorities and split routing. Operators set token and fronts on a LAN page gated by the Keenetic web-admin session.

## Out of scope

- Native “Add loft” type next to WireGuard in KeeneticOS (needs an NDM component from Keenetic).
- Patching stock `:80` JS as the settings form.
- SSTP / WireGuard / OpenVPN in this package.
- loft named lists and node route profiles as the router’s split brain. Keenetic DNS-based routes and policies do that; the panel never writes them.

## Phases

### 0. Catalog — this tree

Docs, plan, HTML mock. No daemon.

### 1. Session on TUN

Cross-compile `loft` for linux `aarch64` / `armv7` / `mipsel` (`CGO_ENABLED=0`). Init script in `/opt/etc/init.d/`. YAML in `/opt/etc/loft/client.yaml`.

- `listen.tun: auto`, `default_route` / kill-switch **off**.
- After Status: `ndmc` creates/updates the connection’s `OpkgTunN` (description = connection name, overlay IP, MTU, `up`).
- Host routes for outbound IPs via the ISP interface (no hairpin).
- Stop: that `OpkgTunN` down, its session gone.

Smoke: ping overlay gateway; Keenetic shows Loft in priorities.

### 2. Panel + Keenetic session

HTTP on LAN only (`:8090` default). No loft password.

- Forward browser `Cookie` to `127.0.0.1/auth` (or `/rci/show/system`). 200 → page; 401 → redirect to stock login with return URL.
- Require configurator/admin, not a guest account.
- Bind LAN IP + `127.0.0.1`. Do not publish on KeenDNS.
- Do not expose `localhost:79` (unauthenticated RCI) to the browser.

KeeneticOS 5.2: if `/auth` advertises `x-ndw4-authenticate`, use the one-shot redirect token.

UI: shared `lanui` v2. Connection list with on/off per connection; add by link (`loft://`, `vless://`, `trojan://`), manual fields, or import (`client.yaml`, `.loft`, `.vpn`, QR picture); per-connection status (overlay, node and reserve, front, QUIC mode, bytes), fronts with QUIC mode (off / bbr / fixed with up and down Mbit/s), journal, package screen. See design/DESIGN.md.

### 3. Package

`.ipk` for the three Entware arches. Each connection takes the next free OpkgTunN when it is created; deleting it removes the interface. `prerm` removes all of them. The panel can update the package: GitHub latest `a3app/loft`, the `.ipk` for this arch, SHA256 check, `opkg install`, sessions restart. README with OPKG/TUN/TAP and KeeneticOS 5.0+. Optional GitHub Actions job later; not in the first tag.

### 4. VLESS and Trojan

A separate connection with its own OpkgTun, edited like loft: link or manual (UUID, host, port, TLS / REALITY, TCP / XHTTP, SNI, fingerprint, public key, short ID, Vision).

Trojan is the same: link `trojan://` or manual (password, host, port, TLS / REALITY, TCP / XHTTP, SNI, fingerprint, public key, short ID).

## Order of work in the Go tree

1. Flag or YAML: “Keenetic mode” = TUN up, no default route, call a hook with overlay IP/MTU/gateway.
2. `RememberGateway` already exists; reuse for the exclude-host pin.
3. Panel is `clients/go/lanui` v2 with a Keenetic block (OpkgTun names, priorities link, web-admin session login). MikroTik keeps its uplink block on the same page.

## Risks

| Risk | Mitigation |
|------|------------|
| Go RSS on 128 MiB models | Ship aarch64 first (Giga/Peak/Hopper). Measure RSS before mipsel. |
| OpkgTun missing on 4.x | Document 5.0+. Refuse start with a clear log. |
| Cookie not sent to :8090 | Same host, `Path=/`. Fallback: proxy `/auth` on :8090. |
| Firmware UI redesign | Panel does not patch `:80`. A dashboard *link* is optional and versioned. |
| Hairpin | Pin front IPs on ISP before `0/0` via OpkgTun. |

## Done when

An operator on KeeneticOS 5.x with Entware installs the ipk, pastes a member key, sees **Loft** in priorities, assigns a policy, and LAN devices in that policy exit through loft.
