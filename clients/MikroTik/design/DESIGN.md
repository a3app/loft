# loft · RouterOS walkthrough

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

Interactive mock: [`index.html`](index.html). Brand: [`brand/`](../../../docs/brand/). Integration: [docs/mikrotik.md](../../../docs/mikrotik.md).

The mock is the operator path in Winbox / New Terminal plus the LAN page at `http://172.31.20.2:8090/`. RouterOS keeps mangle, routing tables, and address-lists. loft is the container that holds the session.

YAML still lives on disk. The LAN page writes it.

---

## 1. Product

One container named `loft`. One veth `172.31.20.2/24`. SOCKS5 on first boot. LAN gateway only after the log shows `tun up`.

The operator:

1. Pastes `check.rsc` in New Terminal.
2. Puts `loft.tar` and `client.yaml` on the disk.
3. Pastes `install.rsc`, waits for `stopped`, starts the container.
4. Opens `http://172.31.20.2:8090/` — member key, node URL, **RouterOS uplink** (`ether1`, `lte1`, `pppoe-out1`).
5. If uplink changed: `/import disk1/loft/data/uplink.rsc` in New Terminal.
6. Points a laptop at SOCKS `172.31.20.2:1080`.
7. If the log has `tun up`, pastes `policy.rsc` for a LAN list.

No vendor `.npk`. No new row in Winbox “Interfaces / VPN”. SSTP and WireGuard stay the stock RouterOS clients.

## 2. Screens

| Screen | What |
|--------|------|
| Check | Identity, version, `architecture-name`, extra package `container`, `device-mode`, disks. Stop before install if any of those fail. |
| Files | `<disk>/loft/loft.tar` and `<disk>/loft/data/client.yaml`. Token and node URL live in the YAML. |
| Container | Name `loft`, veth-loft, `start-on-boot`, logging. Status `running`. |
| SOCKS | Always, after `socks5` in the log. Tools and browsers use `172.31.20.2:1080`. |
| Panel :8090 | Status, key, URL, front, **uplink**. Writes `client.yaml` and `uplink.rsc`. Optional `listen.http_pass`. |
| Gateway | `policy.rsc`: table `loft`, 0/0 via 172.31.20.2, list `loft-lan`, mangle skip `172.31.20.0/24`. Hidden until `tun up`. |
| Log | Last lines from `/log print where topics~"container"`. No key material. |

Simple view (default): Check → Files → Container → Panel. Gateway behind the second group.

Uplink is a RouterOS WAN name. The container bind stays `eth0`. Empty uplink = default RouterOS table.

## 3. RouterOS chrome

Do not restyle Winbox. The mock shows how **loft** looks in *their* Container, Files, and New Terminal: a named container, a veth, a routing table the operator pastes.

Winbox “VPN” (SSTP, WireGuard, IPsec, L2TP) is stock. loft is not a row there.

## 4. Visual

Same loft tokens as macOS: Ink `#1A1714`, Lamp `#E8B84A`, Paper `#E8DFD2`, Avenir Next. The Winbox frame in the mock is light grey (their admin), so the operator sees both worlds.

Type: headings Demi, body Regular, keys SF Mono. The L-mark is the same 96×96 path as macOS: stone body, slate window off, lamp window on. Running = lamp window on. Stopped = lamp off, stone L.

Footer shows the live path: `http://172.31.20.2:8090/` on the LAN page, `SOCKS 172.31.20.2:1080` on SOCKS, `/data/client.yaml · /data/uplink.rsc` on Connection.

## 5. Copy

| EN | RU |
|----|----|
| Loft | Loft |
| Check this board | Проверка платы |
| Extra packages are vendor-only; loft ships as a container image | Доппакеты только у MikroTik; loft едет контейнером |
| SOCKS is ready | SOCKS готов |
| RouterOS uplink interface | Исходящий интерфейс RouterOS |
| Gateway after tun up | Шлюз после tun up |
| Do not run policy.rsc without TUN | policy.rsc без TUN не гонять |
| SSTP is the stock RouterOS client | SSTP — штатный клиент RouterOS |
