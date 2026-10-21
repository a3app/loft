# loft on Aurora OS

**English** · [Русский](aurora_ru.md) · [中文](aurora_zh.md)

How loft runs on an Aurora OS phone. Catalog: [clients/Aurora/](../clients/Aurora/). Plan: [clients/Aurora/PLAN.md](../clients/Aurora/PLAN.md). Mock: [clients/Aurora/design/index.html](../clients/Aurora/design/index.html). Protocol: [protocol.md](protocol.md). Import URI: [share.md](share.md).

The GUI is not built in Dist yet. This file is the integration specification.

---

## 1. Three pieces

Aurora owns **when** the tun exists (ConnMan VPN row, one at a time). `loft-front` owns **how** the session looks on the wire. The QML app writes YAML and asks ConnMan to start.

```
apps
  → tun owned by connman-vpnd
      → ConnMan loft plugin  (app.loft.aurora)
          → loft-front
              → loft  Holder (get|ws|h2|quic|h3)
              → SSTP  TLS + PPP, IP from IPCP
              → VLESS TCP+TLS
                  → exclude server IP from the tun
```

The container (`app.loft.aurora`) does not speak the protocol. It writes `client.yaml` and asks ConnMan to start type `loft` (D-Bus `net.connman.vpn`). If the plugin is missing, it starts `loft-front` with SOCKS on loopback. loft, SSTP, and VLESS are outbounds of the same row. OpenVPN stays the stock Settings type.

Helper: `loft-connman` execs `loft-front -tun NAME -tun-noexec`. ConnMan applies address, DNS, and routes from the notify script.

There is no `clients/mobile` bind. Aurora is GNU/Linux; the helper is the same `loft-front` macOS launches.

## 2. TUN

After a live session (loft Status, or SSTP IPCP) the plugin tells ConnMan:

| Status | ConnMan |
|--------|---------|
| `ip` / prefix | tun address |
| overlay `gateway` | remote for the default route |
| `mtu` | tun MTU |
| `dns` | nameservers |
| `ip6` / `gateway6` | when present |

Front IPs MUST sit off the tun so the session does not hairpin. Protocol lists / zones map to ConnMan routes (`via` / `direct`). `drop` is a discard in the helper, not an OS route.

Hold keeps the default claimed if the session dies: the phone has no cell/Wi-Fi path until Connect again or the user resets in Settings. Copy uses *route* / *direct Internet*.

## 3. Path change

Wi-Fi → LTE (and the reverse) is a new default path. loft-front already closes the live front and resumes with a fresh `0x02` and the same `instance_id` (overlay IP stays). SSTP redials TLS+PPP. Inner TCP still RSTs.

This is the same contract as macOS / CLI. It is not a new cap.

## 4. Identifiers

| | |
|--|--|
| App | `app.loft.aurora` |
| Helper | `loft-front` in the RPM |
| VPN client | `loft-connman` (ConnMan Task) |
| ConnMan type | `loft` |
| VPN row name | **Loft** |
| URL scheme | `loft` |

Aurora SDK: Qt/QML application + privileged plugin RPM. Developer device sideload first.

Engine (`loft-front`): **linux/arm64**, glibc of Aurora 5. GUI: Aurora OS **5.x**.

## 5. What this is not

SOCKS5 on `127.0.0.1` is not the inbound other Aurora apps use. The QML host binds it only when the ConnMan plugin is missing.

macOS `loft-priv` does not ship in the RPM. ConnMan creates the tun.
