# loft on RouterOS

**English** · [Русский](mikrotik_ru.md) · [中文](mikrotik_zh.md)

How loft runs on a MikroTik board. Catalog: [clients/MikroTik/](../clients/MikroTik/). Mock: [clients/MikroTik/design/index.html](../clients/MikroTik/design/index.html). Protocol: [protocol.md](protocol.md).

The image is built with `make -C clients/MikroTik image`. Dist does not ship it yet. MikroTik extra packages are vendor-only; loft ships as a container image.

---

## 1. Two planes

RouterOS owns **where** packets go (mangle, routing tables, address-lists). loft owns **how** the session looks on the wire (loft fronts).

```
LAN host
  → SOCKS 172.31.20.2:1080            (always, after socks5)
  → [tun up] mangle list loft-lan
      → table loft → 172.31.20.2
          → loft TUN
              → loft  get|ws|h2|quic|h3  on veth-loft
                  → TLS 1.3 → CDN or node
```

SSTP, WireGuard, IPsec, L2TP stay the stock RouterOS clients. loft is not a row in Winbox VPN.

## 2. Container, not NPK

RouterOS 7.12+ extra package `container` (arm, arm64, x86 / CHR). `device-mode container=yes` once, then Reset (CHR: power cycle). Disk for `root-dir` and `tmpdir` — usually `disk1`, never NAND.

```
/interface/veth        veth-loft  172.31.20.2/24  gw 172.31.20.1
/interface/bridge      br-loft
/ip/address            172.31.20.1/24 on br-loft
/ip/firewall/nat       masquerade src 172.31.20.0/24
/container             name=loft  mount /data  file=loft.tar
```

Entrypoint: `loft -c /data/client.yaml -if 172.31.20.1`. `-if` gets the veth gateway address, not an interface name: inside a RouterOS container the interface is named after the veth (`veth-loft`), there is no `eth0`, and loft binds to the interface whose subnet holds that address. YAML is the operator file; the LAN page at `http://172.31.20.2:8090/` writes it. Image is scratch + CA store + static linux `loft`.

## 3. SOCKS first

`listen.socks5: 0.0.0.0:1080` is the path that always works. A laptop on the LAN (or the router itself) uses `172.31.20.2:1080`. RouterOS has no native SOCKS client; browsers, curl, and SSH `-o ProxyCommand` do.

Do not run `policy.rsc` on SOCKS alone.

## 4. Gateway after tun up

`/dev/net/tun` inside a RouterOS container is not guaranteed. If the log shows `tun up`:

- table `loft`, default via `172.31.20.2`
- address-list `loft-lan` (edit the prefix)
- mangle prerouting: src list, dst `!172.31.20.0/24`, `dst-address-type=!local`, mark-routing `loft`. Without `!local` the LAN loses the router itself: its DNS, WebFig and Winbox go into the tunnel

RouterOS itself does not get a new default route. Without TUN, 0/0 via the container IP loops (container default is the ROS veth gateway).

**Mode: `nat` or `route`.** Inside the container loft sends 0/1 and 128/1 into its TUN, so an answer to a LAN address would loop back into the TUN instead of returning to RouterOS. Two ways out; `listen.gateway` in `client.yaml` and `:local mode` in `policy.rsc` must say the same:

| | `nat` (default) | `route` |
|--|--|--|
| RouterOS | masquerade `loft-lan` → `br-loft` (rule `loft-lan-nat`) | no masquerade |
| loft | nothing extra | policy rule: what leaves the TUN uses table 176, default via the veth gateway |
| The container sees | every LAN host as `172.31.20.1` | real LAN addresses |
| Use when | always, unless you need real addresses | the container must tell LAN hosts apart, or later overlay → LAN access |

The same choice is called "NAT for clients" on a Keenetic VPN server. `route` is only for a dedicated gateway: on an ordinary Linux host with IP forwarding on, that rule would let overlay members route through the machine. Mismatched modes (loft `nat`, RouterOS `route`) leave the LAN without internet; SOCKS keeps working.

## 5. LAN page and uplink

`listen.http: 0.0.0.0:8090` serves a small operator page (status, key, URL, front, uplink). Optional `listen.http_pass`. Winbox cookie is not shared.

**Uplink** is a RouterOS WAN name (`ether1`, `lte1`, `pppoe-out1`). The container always binds fronts on `veth-loft`. Save writes `/data/uplink.rsc`. Import it:

```
/import disk1/loft/data/uplink.rsc
```

That pins `172.31.20.0/24` to table `loft-wan` → the named interface. Empty uplink removes the rule (main table).

## 6. Hairpin

A policy that sends LAN `0/0` into the container also wants to swallow GET/WS to the node. loft binds fronts on `veth-loft` (`-if 172.31.20.1`). The mangle skips `172.31.20.0/24`. `kill_switch: false` in the YAML; loft still adds split defaults *inside* the netns, which is what makes the container a gateway.

## 7. Boards

| | |
|--|--|
| Runs | arm, arm64, x86, x86_64 — RB5009, hAP ax²/ax³, CCR2004+, CHR |
| Does not run | mipsbe / mmips / smips — hEX, hAP lite, most cheap boards |

`architecture-name` from `/system/resource` must match the tarball (`arm` / `arm64` / `amd64` for x86 and x86_64).

## 8. What this file is not

Not a vendor `.npk`. Not a new VPN type in Winbox. Not loft-node on the same board. Not a patch of WebFig.
