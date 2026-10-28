# loft · RouterOS 走查

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

交互模型：[`index.html`](index.html)。品牌：[`brand/`](../../../docs/brand/)。对接：[docs/mikrotik_zh.md](../../../docs/mikrotik_zh.md)。

模型是运营方在 Winbox / New Terminal 里的路径，加上 LAN 页 `http://172.31.20.2:8090/`。mangle、路由表、address-list 仍在 RouterOS。loft 是扛会话的容器。

YAML 仍在磁盘上。页面会写它。

---

## 1. 产品

一个名叫 `loft` 的容器。一条 veth `172.31.20.2/24`。第一次启动就是 SOCKS5。只有日志出现 `tun up` 才当 LAN 网关。

运营方：

1. 在 New Terminal 粘贴 `check.rsc`。
2. 把 `loft.tar` 和 `client.yaml` 放到磁盘。
3. 粘贴 `install.rsc`，等到 `stopped`，启动容器。
4. 打开 `http://172.31.20.2:8090/` — 成员密钥、节点 URL、**RouterOS 出口**（`ether1`、`lte1`、`pppoe-out1`）。
5. 若改了出口：在 New Terminal `/import disk1/loft/data/uplink.rsc`。
6. 笔记本指向 SOCKS `172.31.20.2:1080`。
7. 日志有 `tun up` 再粘贴 `policy.rsc`，给一份 LAN 列表。

没有厂商 `.npk`。Winbox「接口 / VPN」不加新行。SSTP 和 WireGuard 仍是 RouterOS 原装客户端。

## 2. 屏幕

| 屏幕 | 内容 |
|--------|------|
| 检查 | 主机名、版本、`architecture-name`、额外包 `container`、`device-mode`、磁盘。任何一项失败就不要 install。 |
| 文件 | `<disk>/loft/loft.tar` 和 `<disk>/loft/data/client.yaml`。密钥和节点 URL 在 YAML 里。 |
| 容器 | 名称 `loft`、veth-loft、`start-on-boot`、日志。状态 `running`。 |
| SOCKS | 日志有 `socks5` 即可。浏览器和 SSH 走 `172.31.20.2:1080`。 |
| 面板 :8090 | 状态、密钥、URL、前置、**出口**。写 `client.yaml` 和 `uplink.rsc`。可选 `listen.http_pass`。 |
| 网关 | `policy.rsc`：表 `loft`，0/0 经 172.31.20.2，列表 `loft-lan`，mangle 跳过 `172.31.20.0/24`。没有 `tun up` 就藏着。 |
| 日志 | `/log print where topics~"container"` 的尾。没有密钥材料。 |

简化界面（默认）：检查 → 文件 → 容器 → 面板。网关在第二组。

出口是 RouterOS 的 WAN 名。容器绑定仍是 `veth-loft`。空出口 = 默认表。

## 3. RouterOS 外壳

不要改 Winbox 的样子。模型只说明 **loft** 在 *他们* 的 Container、Files、New Terminal 里长什么样：一个有名字的容器、一条 veth、一张运营方自己粘贴的路由表。

Winbox「VPN」（SSTP、WireGuard、IPsec、L2TP）是原装。loft 不进那一行。

## 4. 视觉

跟 macOS 同一套 loft 记号：Ink `#1A1714`，Lamp `#E8B84A`，Paper `#E8DFD2`，Avenir Next。模型里的 Winbox 框是浅灰（他们的管理界面），好让运营方看见两边。

字体：标题 Demi，正文 Regular，密钥 SF Mono。L 标和 macOS 同一条 96×96 路径：石色本体，slate 窗关，灯窗开。运行中 = 灯窗亮。已停止 = 灯灭，石头色 L。

底栏显示活路径：LAN 页是 `http://172.31.20.2:8090/`，SOCKS 是 `SOCKS 172.31.20.2:1080`，连接页是 `/data/client.yaml · /data/uplink.rsc`。

## 5. 文案

见 [DESIGN.md](DESIGN.md) 的表。
