# loft on KeeneticOS

**English** · [Русский](keenetic_ru.md)

How loft runs on a Keenetic router. Catalog: [clients/Keenetic/](../clients/Keenetic/). Plan: [clients/Keenetic/PLAN.md](../clients/Keenetic/PLAN.md). Mock: [clients/Keenetic/design/index.html](../clients/Keenetic/design/index.html). Protocol: [protocol.md](protocol.md).

The package is not built yet. This file is the integration specification.

---

## 1. Two planes

KeeneticOS owns **where** packets go (priorities, policies, DNS routes). loft owns **how** the tunnel looks on the wire (loft fronts).

```
LAN host
  → Keenetic policy
      → OpkgTun0  (NDM interface, description Loft)
          → loft TUN  (no default route)
              → loft  get|ws|h2|quic|h3
                  → TLS 1.3 → CDN or node
```

VLESS, when added, is another loft outbound on the same OpkgTun. SSTP stays the stock Keenetic client.

## 2. OpkgTun

KeeneticOS 5.0+ maps Entware TUN/TAP into NDM (`OpkgTun` / `OpkgTap`) and allows those interfaces in connection policies and static routes.

Sketch after a live Status:

```
interface OpkgTun0 type OpkgTun
interface OpkgTun0 description Loft
interface OpkgTun0 security-level public
interface OpkgTun0 ip address <overlay-ip> 255.255.255.255
interface OpkgTun0 ip tcp adjust-mss pmtu
interface OpkgTun0 up
```

`ndmc` only. Never `ip route` from the shell — NDM rebuilds tables.

The loft process **must not** install `0/0` or a kill-switch. `listen.tun: auto` is enough.

## 3. Split routing

In the Keenetic UI:

- **Connection priorities** — put Loft in the main policy or a named policy.
- **Policies on devices / segments** — phones via Loft, IoT via ISP.
- **DNS routes** — domain list → gateway = Loft.

loft `use:` lists on the router are off by default. The controller may still publish lists; this client ignores them unless an operator turns that on later.

## 4. Hairpin

A policy that sends `0/0` into Loft also sends the GET/WS to the CDN/node into the tunnel. Before `up`, pin outbound IPs on the ISP interface (same idea as a WireGuard endpoint). loft already excludes session hosts from TUN on desktops; Keenetic mode does the same via `ndmc` host routes.

## 5. Panel auth

HTTP `http://<lan>:8090/`. The process forwards the browser `Cookie` to `127.0.0.1/auth`. 200 and an admin/configurator role → UI. 401 → redirect to the stock web admin.

Cookie is host-only, `Path=/`, so `:80` and `:8090` share it. `localhost:79` (RCI without a password) stays daemon-only.

KeeneticOS 5.2 may advertise `x-ndw4-authenticate`; then the panel uses the one-shot redirect token.

## 6. What this file is not

Not a Keenetic component. Not a new row in “Add connection”. Not a patch of `/usr/share/htdocs_`. Those need Keenetic or a per-firmware inject; the panel does not depend on them.
