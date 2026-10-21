# Plan · loft on KeeneticOS

**English** · [Русский](PLAN_ru.md) · [中文](PLAN_zh.md)

Software 1.0.0. Protocol 1.0. This plan does not raise either version.

## Goal

A routed loft client on Keenetic that shows up as **one connection** in Connection Priorities and split routing. Operators set token and fronts on a LAN page gated by the Keenetic web-admin session.

## Out of scope

- Native “Add loft” type next to WireGuard in KeeneticOS (needs an NDM component from Keenetic).
- Patching stock `:80` JS as the settings form.
- SSTP / WireGuard / OpenVPN in this package.
- loft named lists as the router’s split brain (Keenetic DNS routes and policies do that).

## Phases

### 0. Catalog — this tree

Docs, plan, HTML mock. No daemon.

### 1. Session on TUN

Cross-compile `loft` for linux `aarch64` / `armv7` / `mipsel` (`CGO_ENABLED=0`). Init script in `/opt/etc/init.d/`. YAML in `/opt/etc/loft/client.yaml`.

- `listen.tun: auto`, `default_route` / kill-switch **off**.
- After Status: `ndmc` creates/updates `OpkgTun0` (description Loft, overlay IP, MTU, `up`).
- Host routes for outbound IPs via the ISP interface (no hairpin).
- Stop: `OpkgTun0` down, process gone.

Smoke: ping overlay gateway; Keenetic shows Loft in priorities.

### 2. Panel + Keenetic session

HTTP on LAN only (`:8090` default). No loft password.

- Forward browser `Cookie` to `127.0.0.1/auth` (or `/rci/show/system`). 200 → page; 401 → redirect to stock login with return URL.
- Require configurator/admin, not a guest account.
- Bind LAN IP + `127.0.0.1`. Do not publish on KeenDNS.
- Do not expose `localhost:79` (unauthenticated RCI) to the browser.

KeeneticOS 5.2: if `/auth` advertises `x-ndw4-authenticate`, use the one-shot redirect token.

UI v1: status (up/down, overlay IP, front, bytes), edit (token, URL, type, path, cdn, fingerprint), start/stop. Fronts are loft only.

### 3. Package

`.ipk` for the three Entware arches. `postinst` registers OpkgTun once. `prerm` removes it. README with OPKG/TUN/TAP and KeeneticOS 5.0+. Optional GitHub Actions job later; not in the first tag.

### 4. VLESS

Second outbound type in the same process and the same OpkgTun. Panel grows a VLESS block (UUID, SNI, reality). loft remains default. No extra NDM interface.

## Order of work in the Go tree

1. Flag or YAML: “Keenetic mode” = TUN up, no default route, call a hook with overlay IP/MTU/gateway.
2. `RememberGateway` already exists; reuse for the exclude-host pin.
3. Panel is a new `clients/Keenetic/cmd` (or `loft -panel`) so `loft-front` stays the macOS helper.

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
