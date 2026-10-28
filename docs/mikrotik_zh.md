# loft 在 RouterOS 上

[English](mikrotik.md) · [Русский](mikrotik_ru.md) · **中文**

loft 在 MikroTik 板上怎么跑。目录：[clients/MikroTik/](../clients/MikroTik/)。模型：[clients/MikroTik/design/index.html](../clients/MikroTik/design/index.html)。协议：[protocol_zh.md](protocol_zh.md)。

镜像用 `make -C clients/MikroTik image` 打。暂不进 Dist。RouterOS 额外包只有厂商能发；loft 以容器镜像交付。

---

## 1. 两个平面

RouterOS 管包**往哪走**（mangle、路由表、address-list）。loft 管隧道在信道上**长什么样**（loft 前置）。

```
LAN 主机
  → SOCKS 172.31.20.2:1080            （有 socks5 即可）
  → [tun up] mangle 列表 loft-lan
      → 表 loft → 172.31.20.2
          → loft TUN
              → loft  get|ws|h2|quic|h3  走 veth-loft
                  → TLS 1.3 → CDN 或节点
```

SSTP、WireGuard、IPsec、L2TP 仍是 RouterOS 原装客户端。loft 不进 Winbox VPN。

## 2. 容器，不是 NPK

RouterOS 7.12+，额外包 `container`（arm、arm64、x86 / CHR）。做一次 `device-mode container=yes`，再按 Reset（CHR：断电）。`root-dir` 和 `tmpdir` 放磁盘——一般是 `disk1`，不要放 NAND。

```
/interface/veth        veth-loft  172.31.20.2/24  gw 172.31.20.1
/interface/bridge      br-loft
/ip/address            172.31.20.1/24 在 br-loft
/ip/firewall/nat       masquerade src 172.31.20.0/24
/container             name=loft  mount /data  file=loft.tar
```

入口：`loft -c /data/client.yaml -if 172.31.20.1`。`-if` 给的是 veth 网关地址，不是接口名：RouterOS 容器里的接口名跟 veth 一样（`veth-loft`），没有 `eth0`，loft 绑定到子网包含该地址的接口。YAML 是运营方的文件；LAN 页 `http://172.31.20.2:8090/` 会写它。镜像：scratch + CA 证书 + 静态 linux `loft`。

## 3. 先 SOCKS

`listen.socks5: 0.0.0.0:1080` 是总能走的路径。LAN 里的笔记本（或路由器自己）连 `172.31.20.2:1080`。RouterOS 没有自带 SOCKS 客户端；浏览器、curl、SSH `-o ProxyCommand` 有。

不要只靠 SOCKS 就跑 `policy.rsc`。

## 4. tun up 之后才当网关

RouterOS 容器里的 `/dev/net/tun` 不保证有。日志出现 `tun up` 时：

- 表 `loft`，默认路由经 `172.31.20.2`
- address-list `loft-lan`（前缀自己改）
- mangle prerouting：src 列表，dst `!172.31.20.0/24`，`dst-address-type=!local`，mark-routing `loft`。没有 `!local`，LAN 就连不上路由器本身：它的 DNS、WebFig、Winbox 都会进隧道

RouterOS 自身不装新的 default route。没有 TUN 时，0/0 经容器 IP 会打环（容器默认网关就是 ROS 的 veth）。

## 5. LAN 页和出口

`listen.http: 0.0.0.0:8090` 是小运营页（状态、密钥、URL、前置、出口）。可选 `listen.http_pass`。Winbox 的 cookie 到不了。

**出口** 是 RouterOS 的 WAN 名（`ether1`、`lte1`、`pppoe-out1`）。容器始终把前置绑在 `veth-loft`。保存会写 `/data/uplink.rsc`。导入：

```
/import disk1/loft/data/uplink.rsc
```

把 `172.31.20.0/24` 钉进表 `loft-wan` → 那个接口。空出口删掉规则（走默认表）。

## 6. 发夹

策略把 LAN 的 `0/0` 丢进容器时，去节点的 GET/WS 也会被吞。loft 把前置绑在 `veth-loft`（`-if 172.31.20.1`）。mangle 跳过 `172.31.20.0/24`。YAML 里 `kill_switch: false`；netns 里面 loft 仍会装 split default——容器这样才能当网关。

## 7. 板子

| | |
|--|--|
| 能跑 | arm、arm64、x86、x86_64 — RB5009、hAP ax²/ax³、CCR2004+、CHR |
| 不能 | mipsbe / mmips / smips — hEX、hAP lite 和大部分便宜板 |

`/system/resource` 的 `architecture-name` 必须和包名对上（`arm` / `arm64` / x86 与 x86_64 用 `amd64`）。

## 8. 这份文件不是什么

不是厂商 `.npk`。不是 Winbox 里新的 VPN 类型。不是同一块板上的 loft-node。不是给 WebFig 打补丁。
