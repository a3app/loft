# 计划 · RouterOS 上的 loft

[English](PLAN.md) · [Русский](PLAN_ru.md) · **中文**

软件版本跟仓库 `VERSION`。协议 1.0。本计划不升版本。

## 目标

MikroTik 上的 loft 成员，运营方用 Winbox/WebFig 就能拉起，LAN 前面不必再垫一台 Linux。第一次启动就是 SOCKS5；容器里有 TUN 时再当 LAN 网关。

## 不做

- 厂商 `.npk`（只有 MikroTik 能发）。
- Winbox「接口 / VPN」里新加一行。
- 同一块板上跑 loft-node / decoy。
- RouterOS 已有的 SSTP、WireGuard、IPsec、L2TP。
- MIPS 板。

## 阶段

### 0. 骨架 — 本目录

文档、计划、HTML 走查、Dockerfile、`client.yaml`、`check.rsc` / `install.rsc` / `policy.rsc` / `uninstall.rsc`。`make image` 打 OCI 包。暂不进 Dist。

### 1. CHR 和一块 ARM64 冒烟

- CHR 用 `linux/amd64`，RB5009 或 hAP ax³ 用 `linux/arm64`。
- 成员密钥 + GET 前置。日志：`socks5`，然后 `outbound … up overlay=`。
- LAN 主机 SOCKS：`curl --socks5 172.31.20.2:1080 https://example.net`。
- TUN 起来时前置仍走 `eth0`（`-if eth0`）。

### 2. 网关

- 有 `/dev/net/tun`：`tun up`，再跑 `policy.rsc`。
- `loft-lan` 里的主机 ping 覆盖网网关。
- 没有 TUN：继续 SOCKS；日志一行；不要跑 `policy.rsc`。

### 3. LAN 页

`172.31.20.2:8090`（`listen.http`）。状态、密钥、URL、前置、RouterOS 出口。写 YAML 和 `uplink.rsc`。Winbox cookie 到不了。可选 `listen.http_pass`。

### 4. Dist

以后可以把 `loft-linux-arm64.tar` / `amd64` / `arm` 跟 CLI 一起发。第一次 RouterOS 标签不必等 Dist。

## 完成标准

RB5009 或 CHR 上运营方粘贴 `check.rsc`、`install.rsc`，启动 `loft`，LAN 里的笔记本经 SOCKS 进覆盖网。有 TUN 时，`policy.rsc` 把设备列表送进覆盖网，RouterOS 自身不装 default route。
