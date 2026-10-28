# loft · RouterOS

[English](README.md) · [Русский](README_ru.md) · **中文**

MikroTik **RouterOS 7** 上的容器客户端。就是 Linux CLI 那份 `loft`，跑在 `/container` 里。不是厂商 `.npk`。

协议：[docs/protocol_zh.md](../../docs/protocol_zh.md)。对接：[docs/mikrotik_zh.md](../../docs/mikrotik_zh.md)。UX：[design/DESIGN_zh.md](design/DESIGN_zh.md)。模型：[design/index.html](design/index.html)。

## 运营方拿到什么

| | |
|--|--|
| SOCKS5 `172.31.20.2:1080` | 容器 `running` 且日志有 `socks5` 即可，有无 TUN 都行。无密码：`listen.socks5: off` 或 LAN 页可关闭，也在那里改端口。 |
| LAN 页 `http://172.31.20.2:8090/` | 状态、成员密钥、节点 URL、**RouterOS 出口**（`ether1`、`lte1` …）。写 YAML 和 `uplink.rsc`。 |
| LAN 网关 | 日志出现 `tun up` 之后。然后 `policy.rsc` 把地址列表标进 `loft` 表。 |
| 前置 | 绑在 `veth-loft`（`loft -if 172.31.20.1`），GET/WS/H2/QUIC/H3 不会绕进覆盖网。 |

RouterOS 管包**往哪走**（mangle、路由表、address-list）。loft 管隧道在信道上**长什么样**。

## 板子

| | |
|--|--|
| 能跑 | arm、arm64、x86 / CHR — RB5009、hAP ax²/ax³、CCR2004+、CHR |
| 不能 | mipsbe / mmips / smips — hEX、hAP lite 和大部分便宜板 |

需要 RouterOS **7.12+**、额外包 `container`、`device-mode container=yes`（按一次 Reset），以及磁盘（`/disk/print`，一般是 `disk1`）。

## 目录

```
clients/MikroTik/
  README.md          本文件
  design/            UX 和 HTML 走查
  pack/              Dockerfile、client.yaml、*.rsc
  Makefile           linux loft + docker save
```

## 安装

电脑上（Docker）：

```bash
make -C clients/MikroTik image GOARCH=arm64   # 或 arm | amd64
```

上传 `pack/loft-linux-<arch>.tar` → `<disk>/loft/loft.tar`，`pack/client.yaml` → `<disk>/loft/data/client.yaml`。填 `token` 和 `url`。

路由器 New Terminal：

1. `pack/check.rsc`
2. `pack/install.rsc`（改 `:local disk`）
3. `/container/start loft`
4. 页面：`http://172.31.20.2:8090/` — 密钥、URL、出口。若改了 WAN：`/import disk1/loft/data/uplink.rsc`。
5. SOCKS：`172.31.20.2:1080`。网关：日志有 `tun up` 再跑 `pack/policy.rsc`。

拆除：`pack/uninstall.rsc`。磁盘上的 YAML 保留。
