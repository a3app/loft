# 计划 · loft 在 KeeneticOS

[English](PLAN.md) · [Русский](PLAN_ru.md) · **中文**

软件 1.0.0。协议 1.0。本计划不抬任一版本。

## 目标

Keenetic 上可路由的 loft 客户端：在连接优先级和分流里显示为 **一条连接**。运营方在 LAN 页上设令牌和前置，入口是 Keenetic 网页管理会话。

## 不做

- KeeneticOS 里跟 WireGuard 并列的原生「添加 loft」类型（要 Keenetic 出 NDM 组件）。
- 把系统 `:80` 的 JS 补丁成设置表单。
- 这个包里的 SSTP / WireGuard / OpenVPN。
- 用 loft 命名列表当路由器的分流大脑（那是 Keenetic 的 DNS 路由和策略）。

## 阶段

### 0. 目录 — 就是这里

文档、计划、HTML 模型。还没有守护进程。

### 1. TUN 上的会话

交叉编译 `loft` 到 linux `aarch64` / `armv7` / `mipsel`（`CGO_ENABLED=0`）。Init 脚本在 `/opt/etc/init.d/`。YAML 在 `/opt/etc/loft/client.yaml`。

- `listen.tun: auto`，`default_route` / kill-switch **关**。
- Status 之后：`ndmc` 创建/更新 `OpkgTun0`（描述 Loft、Overlay IP、MTU、`up`）。
- 出站 IP 的主机路由走运营商接口（不要发夹）。
- 停止：`OpkgTun0` down，进程走掉。

冒烟：ping 覆盖网网关；Keenetic 在优先级里显示 Loft。

### 2. 面板 + Keenetic 会话

HTTP 只在 LAN（默认 `:8090`）。没有 loft 密码。

- 把浏览器 `Cookie` 转到 `127.0.0.1/auth`（或 `/rci/show/system`）。200 → 页面；401 → 重定向到系统登录，带返回 URL。
- 要 configurator/admin，不要访客账号。
- 绑 LAN IP + `127.0.0.1`。不要发到 KeenDNS。
- 不要把 `localhost:79`（无密码 RCI）暴露给浏览器。

KeeneticOS 5.2：如果 `/auth` 声明 `x-ndw4-authenticate`，用一次性重定向令牌。

UI v1：状态（开/关、Overlay IP、前置、字节）、编辑（令牌、URL、类型、路径、cdn、fingerprint）、启动/停止。前置只走 loft。

### 3. 打包

三个 Entware 架构的 `.ipk`。`postinst` 注册一次 OpkgTun。`prerm` 卸掉。README 写 OPKG/TUN/TAP 和 KeeneticOS 5.0+。GitHub Actions job 以后再说；不进第一个标签。

### 4. VLESS

同一进程、同一个 OpkgTun 上的第二种出站。面板加上 VLESS 块（UUID、SNI、reality）。loft 仍是默认。不另加 NDM 接口。

## Go 目录里的顺序

1. 参数或 YAML：「Keenetic 模式」= TUN 起来、没有默认路由、用 Overlay IP/MTU/网关调钩子。
2. `RememberGateway` 已经有；排除主机也用它钉。
3. 面板是新的 `clients/Keenetic/cmd`（或 `loft -panel`），好让 `loft-front` 继续当 macOS 助手。

## 风险

| 风险 | 怎么挡 |
|------|------------|
| 128 MiB 机型上的 Go RSS | 先出 aarch64（Giga/Peak/Hopper）。量完 RSS 再碰 mipsel。 |
| 4.x 上没有 OpkgTun | 文档写 5.0+。启动直接拒绝，日志说清楚。 |
| Cookie 送不到 :8090 | 同一个主机，`Path=/`。退路：在 :8090 上代理 `/auth`。 |
| 固件网页改版 | 面板不补丁 `:80`。仪表盘 *链接* 可选，并且带版本。 |
| 发夹 | `0/0` 走 OpkgTun 之前，把前置 IP 钉在 ISP 上。 |

## 做到什么算完

KeeneticOS 5.x 加 Entware 的运营方装上 ipk，粘贴访问密钥，在优先级里看见 **Loft**，派一条策略，这条策略里的 LAN 设备从 loft 出去。
