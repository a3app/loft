# loft 在 KeeneticOS 上

[English](keenetic.md) · [Русский](keenetic_ru.md) · **中文**

loft 在 Keenetic 路由器上怎么跑。目录：[clients/Keenetic/](../clients/Keenetic/)。计划：[clients/Keenetic/PLAN.md](../clients/Keenetic/PLAN.md)。模型：[clients/Keenetic/design/index.html](../clients/Keenetic/design/index.html)。协议：[protocol_zh.md](protocol_zh.md)。

包还没打。这份文件是对接规格。

---

## 1. 两个平面

KeeneticOS 管包**往哪走**（优先级、策略、DNS 路由）。loft 管隧道在信道上**长什么样**（loft 前置）。

```
LAN 主机
  → Keenetic 策略
      → OpkgTun0  (NDM 接口，描述 Loft)
          → loft TUN  (没有默认路由)
              → loft  get|ws|h2|quic|h3
                  → TLS 1.3 → CDN 或节点
```

每个连接（loft 配置、VLESS 或 Trojan）有自己的 OpkgTun：OpkgTun0、OpkgTun1……所有已启用的连接都在线，由 Keenetic 优先级挑选。SSTP 仍是 Keenetic 原装客户端。

## 2. OpkgTun

KeeneticOS 5.0+ 把 Entware 的 TUN/TAP 映射进 NDM（`OpkgTun` / `OpkgTap`），这些接口可以进连接策略和静态路由。

会话活了之后的草稿：

```
interface OpkgTun0 type OpkgTun
interface OpkgTun0 description Loft
interface OpkgTun0 security-level public
interface OpkgTun0 ip address <overlay-ip> 255.255.255.255
interface OpkgTun0 ip tcp adjust-mss pmtu
interface OpkgTun0 up
```

只用 `ndmc`。千万别在 shell 里 `ip route`——NDM 会重建表。

loft 进程**不得**装 `0/0` 或 kill-switch。`listen.tun: auto` 就够。

## 3. 分流

在 Keenetic 界面里：

- **连接优先级** — 把 Loft 放进主策略或一个有名字的策略。
- **设备 / 网段策略** — 手机走 Loft，IoT 走运营商。
- **DNS 路由** — 域名列表 → 网关 = Loft。

路由器上 loft 的 `use:` 列表默认关。控制器仍可以发列表；这个客户端不接，除非运营方以后打开。

## 4. 发夹

策略把 `0/0` 丢进 Loft 时，去 CDN/节点的 GET/WS 也会进隧道。`up` 之前，把出站 IP 钉在运营商接口上（和 WireGuard endpoint 一个思路）。桌面上 loft 已经把会话主机排除出 TUN；Keenetic 模式用 `ndmc` 主机路由做同样的事。

## 5. 面板登录

HTTP `http://<lan>:8090/`。进程把浏览器 `Cookie` 转到 `127.0.0.1/auth`。200 且角色是 admin/configurator → 界面。401 → 重定向到原装 Web 管理。

Cookie 只绑主机，`Path=/`，所以 `:80` 和 `:8090` 共用。`localhost:79`（无密码的 RCI）只给守护进程。

KeeneticOS 5.2 **可以**声明 `x-ndw4-authenticate`；那时面板用一次性重定向令牌。

## 6. 这份文件不是什么

不是 Keenetic 组件。不是「添加连接」里的新行。不是给 `/usr/share/htdocs_` 打补丁。那些得 Keenetic 自己做，或按固件注入；面板不依赖它们。
