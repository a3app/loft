# Loft · macOS 客户端

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

菜单栏客户端。窗口是次要的。标志、色板、Avenir Next：[`brand/`](../../../docs/brand/)。交互模型：[`index.html`](index.html)。协议：[docs/protocol_zh.md](../../../../docs/protocol_zh.md)。

Swift 5 + AppKit 程序在 [`../Sources/`](../Sources/)。活着的会话是拷进 `loft.app/Contents/MacOS/` 的 Go 进程 `loft-front`。进程内的 `GETFront.swift` 不是应用走的那条路。`loft-front` 的 Hello 是 Go 客户端（GET、WS、H2、QUIC、H3，协议 1.0）。SSTP 是同一 GUI 里单独的 PPP-over-HTTPS。VLESS 是第三种类型（TCP+TLS、REALITY、Vision、XHTTP）。VLESS 编辑器是 **链接**（`vless://`）或 **手动**（字段）；两种都写成 DuckSoft URI。Trojan/Shadowsocks 同一家，以后再说。

---

## 1. 产品

客户端说协议 1.0。本机露出 SOCKS5 和 TUN（Go CLI 里已经有）。凭证是成员访问密钥 `lt_…`。没有密码。

v1 已带：密钥、经 `loft-front` 的出站 `get` / `ws` / `h2` / `quic` / `h3`、若干 **连接**（配置）、导入/导出 `.loft`、改配置、打开/断开、TUN 和 Hold。配置里的 `select: auto` 还在 Go CLI。

一条连接就是一份有名字的 `client.yaml`。几条连接就是几个会话。这和 cap `multi` 不是一回事（同一把密钥上多个 `instance_id`）。

共用文件（所有 loft 应用）：`.loft` 里是 **一条或多条连接**，类型 `loft` / `sstp` / `vless` / `trojan` 可以混。列表副标题是 **类型 · 传输 · 主机**。loft 地址是 `loft://`。规范：[docs/share_zh.md](../../../docs/share_zh.md)。

---

## 2. 接入协议（1.0）

软件 `1.0.22`。协议 `1.0`（Hello 里 `0x0100`）。SOCKS 和 TUN 留在本机。规范：名册、诱饵、伪装、**具名地址列表**、覆盖网转发，过滤路径上的客户端都必须有。

| 应用里 | 信道上 |
|--------|--------|
| 连接（配置，`.loft`） | 一份 `client.yaml` **或** `connections:` 下的一项：`kind`、`token` / 凭证、`outbounds`、`select`，可选 `bootstrap` / `controller_pub` / `fronts` / `lists_url` / `use` / `default` / `p2p` |
| 密钥 `lt_` + 48 hex | 认证 blob `0x02` + HMAC；节点存 SHA-256 |
| URL + 类型 + path | 一条出站：`type` `get`\|`ws`\|`h2`\|`quic`\|`h3`，`url`，`path` |
| 状态面板里的前置 | Hello/Status `caps` 求交之后活着的出站 |
| Overlay IP | Status `ip`（以及 `mtu`、覆盖网 `gateway`） |
| `select: auto` | 剩下的前置 → 下一条出站 → Status `replicas` → 名册 |
| 多个出站 | 多份配置 = 多个会话。和 cap `multi` 不是一回事 |
| Cap `multi` | 一把密钥，多个 `instance_id`。节点：`auth_already_online` / `auth_limit` |
| — | 按配置持久化 `instance_id`；空 → 节点签发，存下来 |
| — | 在 `lease_hold` 内重连用新的 `0x02` |
| 关于：前置 / 伪装 | **这个客户端** 放进 Hello 的 cap |
| SNI 诱饵 | 客户端不选。Status / 名册 / 探测。CDN 前置保住 CDN 主机名 |
| 名册 | 认证前 GET `{roster_path}`（`bootstrap`）；之后 Status `roster`。文件里没有密钥 |
| 分流 | 产品上是 **区域**。区域经 Loft 或绕过 Loft；服务只属于一个区域。内核仍是两个 CIDR 袋（`via` / `direct`），从 `zone.def` 来。控制器的具名列表（`use:`）这个界面还没有。 |
| Keenetic `.bat` / 本地添加 | 区域里多出来的本机服务，不上信道。loft-routes 里的目录文件是列表，不是区域。`source: me` / `repo`。 |
| TUN UDP | Cap `dgram`，mux `CmdDgram` |
| 到另一成员的覆盖网 | Status `peers` hub\|mesh\|vlan。Cap `p2p` 可选；`p2p: false` = 只做 hub 拼接 |

活着的助手（`loft-front`，成员，不是中继）的 Hello：配置打开的前置（`get` / `ws` / `h2` / `quic` / `h3`），加上 `status timers hello_caps reset dgram clockless ticket decoy mask roster resume lists`。Cap `p2p` 只在配置允许监听时开。Cap `relay` 关。`hub:*` 和 `ctl:1` 从不打开。

第一次 Status 之后，只留下前置的交集，按这个客户端的顺序。非 OK 的 Status 禁止 mux。认证时 GET 404 是 `auth_bad_key`。

列表：Status `lists_serial` / `lists_hash` → `GET {lists_path}`（默认 `/static/v3/lists.json`），和名册同一把 Ed25519 密钥。SOCKS 匹配主机名；TUN 匹配 CIDR。命中多条：**drop，然后 direct，然后 via**。

Hold、故障切换、Packet Tunnel 对助手、「重置路由」是这张映射之上的机器策略。它们不是 cap。

**AppKit 分流和模型一致（区域）。** 还落后于线上的：手动屏只有 GET\|WS；没有 `bootstrap` / `controller_pub`；不拉控制器具名列表；没有 `drop`；故障切换之后不换活着的前置。往系统表里加额外 CIDR 是经 loft-priv 的 `viaSets` / `bypassSets`（`--apply-split`）。完整的 `.loft` 已经带着这些 YAML 字段。

---

## 3. 配置

一条连接就是一份有名字的配置：类型（`loft` / `sstp` / `vless` / `trojan`）加凭证加出站。行的副标题是 类型 · 传输 · 主机（`loft · get · cdn.example.com`）。行上的上下文菜单：编辑 / 导出 `.loft`（选中的这条连接）/ 删除。全部导出成 `Family.loft`。

两种模式（设置 →「多个出站」，列表上同一个开关）：

| 模式 | 行为 |
|------|------|
| 一条当前（默认） | 换配置会撕掉会话，重新握手。列表标「当前」。弹出层：一颗打开/断开。 |
| 多个出站 | 每条配置自己的打开/断开和状态。打开一条，别的不动。菜单里的灯：任何会话活着就亮。SOCKS：共用 `:1080`，或每条配置一个端口。 |
| 简化界面 | 主屏幕：打开和断开。IP、前置、SOCKS、TUN、spark：只在简化界面关掉时才有。设置仍是全的。 |

`select: auto` 在 YAML 配置 **里面** 选出站。

| 哪里 | 文件 |
|------|------|
| 配置 | `~/Library/Application Support/loft/profiles/<id>.yaml` |
| 指针 | `~/Library/Application Support/loft/active`（只在一条当前模式） |
| 会话 | `~/Library/Application Support/loft/sessions/<id>`（多个出站时哪些是开着的） |
| 偏好 | `~/Library/Application Support/loft/prefs.yaml`（`multi: true`，`split: true`） |
| 分流列表 | `~/Library/Application Support/loft/routes.txt`（先区域，后服务） |
| 密钥 | 钥匙串。保存之后，界面不再用常驻明文框显示密钥。 |

导入：`.loft` 或 `client.yaml`。`.loft` **可以** 是今天的单份文档 **或** 带 `connections:` 的包（一条或多条，协议可混）。每一项变成一份配置。导出选中的连接为 `Office.loft`；多条为 `Family.loft`。文件里有密钥；界面警告不要给外人。

loft 地址栏是 `loft://<token>@host…`。一个前置 → 二维码 `loft://`。一条连接的多个前置 → 这条连接的 `.loft`。

编辑：名称、类型，然后该类型的块。页签 **连接 / 高级**。loft 和 VLESS 共用一颗安静的胶囊 **链接 / 手动**，段里是链条和铅笔。loft：粘贴 `loft://`，或密钥 / 主机 / 端口 / GET·WS·H2·QUIC·H3 / 路径。两种都写入同一份配置。VLESS：粘贴 `vless://`，或地址 / 端口 / UUID / TLS·REALITY / TCP·XHTTP 以及该模式要的字段。两种都写成 `vless://`。SOCKS5、隧道、DNS 和 mux 在高级。多个出站时，这个屏幕还有这条配置的状态和打开/断开。

---

## 4. 路由 / TUN

机器只有一条默认路由。SOCKS **可以** 按配置各有一份。TUN 不行。

| 什么 | 哪里 | 为什么 |
|------|------|--------|
| 全部流量 | 在 **连接** 上 | 这个会话是默认路由的候选人。只有 SOCKS 的路由从不拿走。活着的 TUN 最多一条拥有路由。 |
| 交给仍在线的 | **客户端** | 路由主人死了：路由挪到另一条开着的 TUN。三个会话，两个死了 → 路由在最后一条活着的上。 |
| 不要恢复直连网络 | **客户端** | 最后一条 TUN 死了：直连路由保持关掉。机器没有路，也不会用自己的 IP 往外打。 |
| 断线时在菜单里 | **客户端** | 所有连接都关着时，菜单里显示「重置路由」。关：只在设置里。 |
| 分流 | **客户端** | 开关。开：列表进系统表。关：loft 不装这些路由。 |
| 共用 SOCKS | **客户端** | 每个活着的出站一条 `:1080`。分流开着，用同一套从区域来的经/绕袋。关：每条配置一个端口。 |
| 简化界面 | **客户端** | 显示。弹出层、状态、列表、编辑：名称和打开/断开。设置、分流、导入仍看得见。 |
| 重置路由 | 动作 | 用户把直连网络还回去。若「断线时在菜单里」开着，出现在弹出层。 |

Hold 和故障切换是机器策略。把它们放到连接上，策略会打架。

行为：

1. 活着的 TUN 会话：路由、对外的 SOCKS、弹出层坐在最后连上的那条（最新）。连接列表按这个新旧排。
2. 这条会话死了，故障切换开着，还有别的活着的 → 弹出层、SOCKS、路由按新旧跳到上一条活着的。
3. 没有活着的 TUN，Hold 开着 → 网络关掉，灯红，「路由已卡住」。若「断线时在菜单里」开着，弹出层显示「重置路由」；否则只在设置里。
4. Hold 关 → 最后一次断开时直连路由回来（真实 IP 可达）。

文案说 *路由*、*直连网络*、*重置*。不说「kill switch」和「盾」。

### 4.1 后端

一个客户端，两套引擎。按系统版本选，不是设置项。

| macOS | 引擎 | 为什么 |
|-------|------|--------|
| 10.13–10.14 | 特权助手（`SMJobBless`） | 带 Developer ID 的 Packet Tunnel 不可用：appex 只有 App Store。 |
| 10.15+ | Packet Tunnel，**system extension** | 从 Catalina 起，商店外的 Network Extension 只许 sysex。 |

不装单独的 SOCKS 助手。引擎在第一次「全部流量」时起来。

10.15+ 上助手不 bless，就算 sysex 被拒：TUN 保持关，SOCKS 活着。一台机器上两种改表的办法禁止。

路由策略是一个对象（`RouteEngine`）。变的只是进系统的那条路。

**助手（10.13–10.14）。** 应用打开 `utun`，自己跑栈。助手经 XPC：`ifconfig`、`route add/delete`、网关快照、Hold、重置。不读流量。第一次安装弹一次管理员对话框。

**Packet Tunnel（10.15+）。** System extension，entitlement `packet-tunnel-provider-systemextension`。第一次 TUN：激活 sysex 并保存配置。系统设置里这是一份网络配置；loft 界面里这是「路由」，配置名 `loft`。

策略怎么落到 `NEPacketTunnelNetworkSettings`：

| 状态 | includedRoutes | excludedRoutes | provider |
|------|----------------|----------------|----------|
| loft 活着，全部流量 | default | bypass（+ 前置主机） | 已启动 |
| loft 活着，分流 | 经 Loft 袋（`def` 为 true 的区域） | 绕过 Loft 袋（`def` 为 false）+ 前置主机 | 已启动 |
| Hold，loft 死了 | default（进 utun 的包 **丢掉**） | bypass | **保持运行** |
| 重置 / 断开时 Hold 关 | — | — | 停 |

Hold 和「Loft 关着时绕过 Loft」在 Network Extension 上只在 provider 跑着时成立。停掉它，直连默认路由就回来。

以后要是上 App Store：还是同一套 Packet Tunnel（appex 或 sysex，按商店规则）。MAS 里不带助手。

---

## 5. 分流

整机一份映射，不是每条配置一份列表。不然三条 TUN 会话会抢前缀。

**产品。** **区域** 是策略：经 Loft 或绕过 Loft，外加可选的列表 URL。**服务**（名字 + CIDR）只属于一个区域。方向不是行上的字段。内置区域是 Local（绕过 Loft，空仓库，出厂 LAN）和 Global（经 Loft，`global.bat`）。额外区域从区域编辑器来。`global.bat`、`ru.bat`、`cn.bat` 来自 [a3app/loft-routes](https://github.com/a3app/loft-routes) 是目录，不是区域：用户把文件挂到区域的仓库上。

**内核。** 系统表里两个 CIDR 袋，从 `zone.def` 来：经 Loft（`viaSets`）进 utun，绕过 Loft（`bypassSets`）走记住的直连下一跳。loft-priv 做 diff（`--apply-split` / `--clear-split`）。默认路由已经在 0/1 时，经 Loft 的 CIDR 不再重复装。

**协议列表**（控制器的具名集合，`use:` → via / direct / drop）留到以后。不会变成页签。以后的修订 **可以** 把控制器列表导入区域，不覆盖 `source: me`。

**开关。** 区域是配置。只有分流开着才进系统表。关：多余路由撤掉；机器表现得像没有分流。

**直连下一跳。** TUN 在抢走 `0.0.0.0/0` 之前已经拍过同一张快照（Hold/恢复需要一个把网络还回去的地址）。分流不拍第二张。绕过 Loft 在这个下一跳上装更窄的路由。

绕过 Loft **在 Loft 关着时也有效**：Hold 关掉了默认路由；LAN 和地区门户仍走向记住的网关。没有活着的 TUN 时，经 Loft 会撤掉（没地方送）。

| 袋 | 分流开，loft 活着 | 分流开，loft 关着 | 分流关 |
|----|-------------------|-------------------|--------|
| **经 Loft**（`zone.def` true） | 进 utun | 撤 | 撤 |
| **绕过 Loft**（`zone.def` false） | 记住的直连 | 记住的直连 | 撤 |
| 默认 | TUN / Hold | Hold 或直连 | 和以前一样 |

快照：分流打开时，如果默认还是直连，现在就记住。如果 loft 已经拿着默认，用上次打开时的快照。没有快照：「等 loft 拿走默认时再记」。

**共用 SOCKS。** 一条 `:1080`。分流开着，用同一套从区域来的袋。关着，全部 SOCKS 进活着的 loft（路由主人或第一条活着的）。共用关掉：每条配置自己的端口；应用自己选出站。

### 5.1 文件格式

Keenetic 吃 Windows `.bat` 的 `route ADD`。从 KeeneticOS 5.0 起，行尾注释：`:: rem` 或 `& rem`。loft 两种都读，写出 `:: rem`。

**Keenetic（标准）** — loft 原样导入：

```
route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0
route ADD 1.1.1.0 MASK 255.255.255.0 0.0.0.0 :: rem Cloudflare
```

网关忽略（和 Keenetic 绑到接口时一样）。`route add` 不分大小写。没有注释，服务名为空。整个文件进一个区域：导入时问是哪个。

**loft** — 同一个 `.bat`。Keenetic 认（REM 对它是文本）：

```
@echo off
REM loft-routes 1.0
REM zone: global
route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube
REM zone: local
route ADD 10.0.0.0 MASK 255.0.0.0 0.0.0.0 :: rem LAN
```

`REM zone:` 给后面的 `route ADD` 命名区域，直到下一个 `REM zone:`。服务名只在 `:: rem`。方向不写在服务行上。

旧导入仍接受名字后面的 `| via` / `| bypass`（`loft` / `direct` 是同义词）并映射到 Global / Local。新文件不这么写。

导出：

| 给谁 | 文件 | 内容 |
|------|------|------|
| Keenetic | `YouTube.bat` | 一个区域，`:: rem YouTube`，没有 `REM zone:` |
| loft | `loft-routes.bat` | 全部区域，`REM zone: local` / `global` |

导入：`.bat`、`.txt`、`.loft-routes`。旧的混合文件把 `| via` 放到 Global，`| bypass` 放到 Local。纯 Keenetic 文件会问放到哪个区域。带 `REM zone:` 的文件填那个区域。

这些文件是本机路由表。它们和控制器地址列表（`client.yaml` 里的 `use:`）不是一回事，后者由协议下发。以后的修订 **可以** 把控制器列表导进同一张表。

### 5.2 仓库

每个区域自己的仓库：一份 raw `.bat` 的 URL（GitHub 一类）。可选。这个区域每天自动更新一次。仓库来的行标 `source: repo`。更新不动 `source: me` 的行。URL 空就是没有列表。

URL 栏很短，右边一颗 **列表** 按钮。打开 [a3app/loft-routes](https://github.com/a3app/loft-routes) 的目录：`catalog.json`。点一行就填 URL。自定义地址仍可手贴。

```
https://raw.githubusercontent.com/a3app/loft-routes/main/catalog.json
https://raw.githubusercontent.com/a3app/loft-routes/main/global.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/ru.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/cn.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/ir.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/kz.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/by.bat
```

### 5.3 目录

列表窗口是一份具名服务的目录，按 **区域** 分组，不按方向。内置区域是 Local 和 Global。额外区域只从区域编辑器来。地区文件不创建区域。

| 代号 | 含义 | 方向 |
|------|------|------|
| **Local** | LAN 和地区列表（ru, cn, ir, kz, by） | 绕过 Loft |
| **Global** | 世界服务（YouTube, ChatGPT, …） | 经 Loft |

路由属于区域。方向只在区域上：经 Loft 或绕过 Loft。区域里的服务都这么走。把区域翻过来，整份列表跟着走。一次性例外是另一个区域（或把服务挪走）。

每个区域自己的仓库 URL。Local 出厂带一个服务，LAN（`10.0.0.0/8` `172.16.0.0/12` `192.168.0.0/16`）。俄国用户给 Local 选 `ru.bat`；中国用户选 `cn.bat`。内置区域不能改名或删除；方向和仓库可以改。删掉自建区域，服务移到 Global。

loft-routes 的目录文件是一份列表。填进哪个区域，看哪个区域的仓库 URL 指向它。

### 5.4 窗口

三扇门进目录：设置 → 路由 → **列表**；状态窗口底栏 **列表**；菜单 工具 → **路由**。工具里还有 **分流** 勾选，和设置里同一个开关。

设置里的路由：真正的开关（把列表写入表）和一颗金色的 **列表** ghost。这一行本身点不进目录。

目录窗口，440 × ~600：

1. 标题 + 同一个分流开关。记住的直连下一跳说明。
2. 区域页签（Local、Global，然后加上的），上面有服务数。**区域** 打开编辑器。
3. 安静的策略行：`Local · 绕过 Loft`。这个屏幕上方向不是控件。
4. 搜索框，按服务名过滤当前区域。
5. 行：名字、前几个 CIDR、条数、来源（仓库 / 自己的 / 忽略）。点行编辑名字、网段、区域。行上没有经/绕。
6. 加号：添加服务或忽略（网段、名字、区域、类型 路由 / 忽略）。忽略 CIDR 不写入系统表，拉取同区域仓库时从导入行里扣掉。保存一条仓库行并删掉 CIDR 时，这些 CIDR 进入该区域的忽略集。
7. 底栏：导入 / 导出 / 仓库。仓库改的是 **当前区域** 的列表 URL（输入框 + 列表选择）、自动更新和立即更新。

区域编辑器：行是名字、方向、服务数、内置标记、有仓库则显示列表文件。没有额外标题 — 名字就是名字。加号添加区域（名字、方向、可选 URL）。点行编辑。内置：名字锁住，方向和仓库可改。自建：改名、方向、仓库、删除（服务 → Global）。方向（经 Loft / 绕过 Loft）只在这里和添加区域时出现。

导入旧的混合 loft-routes 把 `| via` 放进 Global，`| bypass` 放进 Local。仓库 URL 填它的区域；那个文件里的 `| via` / `| bypass` 忽略。带 `REM zone:` 的文件填那个区域。`REM ignore 104.16.0.0/12`（以及名为 `ignore` 的服务）进入该区域的忽略集，不当路由。

空区域：安静的一行，没有假行。

### 5.5 存储

`~/Library/Application Support/loft/routes.txt`。先区域块，后服务。set 上没有 `via:`。

```
zone: local
name: Local
builtin: true
def: false
repo:
auto: true

zone: global
name: Global
builtin: true
def: true
repo: https://raw.githubusercontent.com/a3app/loft-routes/main/global.bat
auto: true

set: lan
name: LAN
zone: local
src: me
cidr: 10.0.0.0/8

set: ignore-local
name: ignore
zone: local
src: ignore
cidr: 104.16.0.0/12
```

`def: true` 是经 Loft。`src: ignore` 是永不安装的 CIDR；同区域仓库拉取不会把它加回来。旧文件在 set 上有 `via:`：false→Local，true→Global。顶层 `repo_url` 在 `repo_via` 时落到 Global，否则 Local，只在该区域仓库为空时。

---

## 6. 信息架构

```
NSStatusItem（L + 灯的窗口）
  └ 弹出层 300 × auto
       exclusive:
         ├ 打开 / 断开  |  出错时重试
         ├ 重置路由  （全部关掉、Hold、「断线时在菜单里」）
         ├ 连接 ›  （切换器）
         ├ Overlay IP、前置、SOCKS5
       multi:
         ├ 头：loft · 「已打开 · N」 | 「错误」 | 「路由已卡住」
         ├ 重置路由  （全部关掉、Hold、「断线时在菜单里」）
         ├ 每条配置一行：
         │    灯点  名称
         │    Overlay IP · 端口   |  「失败」
         │    打开 / 断开 / 重试
         └ 头上的灯 = 有人活着就黄，出错或 Hold 就红
       ├ 打开 Loft…
       └ 退出

主窗口 / 状态        — 覆盖网、前置、SOCKS；底栏 日志 / 重置 / 列表 / 设置
主窗口 / 连接        — 列表；底部 导入 / 导出 / 手动；多个出站开关
编辑                 — 名称、loft://（或 SSTP/VLESS 字段）、SOCKS、隧道；
                       高级页 — mux、DNS
首次打开             — 密钥 → 地址，或导入文件
设置                 — 登录、自动连接、启动时检查更新、简化界面、多个出站、共用 SOCKS、
                       路由（故障切换 / Hold / 断线时在菜单里 / 分流开关 + 列表）
分流                 — 目录页签 Local / Global（+ 自建），安静行 `Local · 绕过 Loft`，
                       搜索，服务行没有 via/bypass，忽略条目，区域编辑器，Keenetic+loft 导入
关于                 — 版本、标题 **协议**、规范行紧挨在下面
                       （loft/1.0、SSTP、VLESS）、blurb、题记 A³。空闲按钮 **检查
                       更新**，点之前不查；然后 正在检查 / 已是最新 / 更新到 N / 下载 /
                       安装 / 重新启动。启动时检查（设置）是单独的窗口：更新 / 稍后 /
                       不再询问（关掉启动检查）。
```

应用是 `LSUIElement`。

Loft 窗口是当前窗口时，菜单栏是 **Loft** | **连接** | **工具** | **编辑**。Loft 菜单：关于 Loft、打开 Loft、语言、隐藏、退出。打开 Loft 带窗口图标。语言项带地球图标。英语包是 **Language**；其他包是 `{本地名} (Language)`，好找。子菜单是各语言自己的名字，当前包打勾。工具：设置、分流、路由、日志 — 齿轮、勾选画在图标列（不再多一截缩进）、分支、文档。连接是顶层的隔壁项：每条配置按名字 → 打开 / 断开、隧道、默认路由、编辑 — 和弹出层切换器同一套动作。简化界面藏起隧道和默认路由。应用是 **Loft**；协议是 **loft** / **LOFT**。

---

## 7. 菜单栏

图标是 L 的 template（系统给身体上色）。窗口是单独一层，不是 template：那是灯。

| 状态 | 身体 | 窗口 |
|------|------|------|
| disconnected | template | 暗的，不亮 |
| connecting | template | 灯，脉动 |
| connected | template | 灯 `#E8B84A`，一点光晕 |
| error | template | 红 `#E24B4B`，光晕 |

多个出站时：任何会话活着就是黄；任何会话出错就是红（红赢）。全部关掉：窗口熄灭。

实现：`NSStatusItem` 用 custom view 或合成图（template 身体 + 窗口的彩色矩形）。整个字形不会灌成铜色。

### 7.1 弹出层 · multi

这是活着的会话列表，不是单一当前配置的切换器。

- 头：标志，灯和菜单里的一样；行「已打开 · 2」/「已打开 · 1 · 错误」/「Loft 已关闭」。
- 行：点（灯 / 灰 / 红），名称，下面 Overlay IP 和 SOCKS 端口。出错时 IP 换成「失败」。
- 右边一个动作：断开 | 打开 | 重试。
- IP 和端口是用户要看到的：会话活着，应用往哪指。

弹出层跟系统外观。品牌强调是灯（黄 / 红）。弹出层本身不灌品牌色。

---

## 8. 第一次打开

两步。没有密钥和地址，打开不可用。

1. **访问密钥。** 「输入发给你的访问密钥。」字段 `lt_…`。校验：前缀 `lt_` + 48 hex。提示：以 `lt_` 开头；只显示一次 — 请保存。
2. **地址。** 「输入和密钥一起发给你的地址。」URL，类型 GET | WebSocket，可选 path。「连接」→ 握手，然后弹出层。
3. **或导入文件**，从密钥屏幕。

这个屏幕不提 peer、SHA-256、吊销。

---

## 9. 主窗口

深墨 `#1A1714`，雪色文字，灯只在连上时亮。不抄系统设置。

Exclusive：

- 头：标志 + `loft` + 当前配置名
- 大号打开（开着时灯色填充）
- 行：Overlay IP · 前置名（`cdn-get`）· `127.0.0.1:1080`
- Sparkline 上/下，没有国家画廊
- 出站列表：名称、类型、URL 主机、点（live / standby / dead）
- `select: auto` — 从上往下试；活着的用灯标

多个出站：不是一个打开，而是配置列表，每条有灯、Overlay IP、SOCKS 端口、打开/断开。头显示「N · 已打开」。

**简化界面。** 设置里的开关。弹出层、状态、列表、编辑：名称和打开/断开（外加从菜单换连接）。没有 Overlay IP、前置、SOCKS、spark、TUN 徽章、GET/WS、path。列表没有「多个」开关。设置、分流、导入/导出照旧。

---

## 10. 文案

规范语言是英语。文案在 [`lang/`](../lang/) — 每种语言一个 `{code}.lang`；应用和这个模型用同一套（`?lang=zh`、`en`、`fr`、`de`、`it`、`ja`、`ko`、`pt`、`ru`、`vi`）。`shots/` 里有俄语和英语截图。选择器先是 English，再按英语名排拉丁字母和西里尔字母，汉字系语言（中文、日语、韩语）在最下。应用带英语、法语、德语、意大利语、葡萄牙语、俄语、越南语、中文、日语、韩语。Loft 菜单的语言项：英语包是 **Language**；其他包是 `{本地名} (Language)`。

| id | English |
|----|---------|
| connect | Connect |
| disconnect | Disconnect |
| connecting | Opening… |
| key.title | Access key |
| key.body | Enter the access key you were given. |
| key.hint | Starts with lt_. It is shown once — keep it. |
| url.title | Address |
| url.body | Enter the address you were given with the key. |
| url.get / url.ws / url.h2 / url.quic / url.h3 | GET / WS / H2 / QUIC / H3 |
| front | Front |
| socks | SOCKS5 |
| launch | Open at login |
| auto | Connect automatically |
| quit | Quit |
| menu_quit | Quit Loft |
| open | Open Loft |
| menu_about | About Loft |
| menu_tools | Tools |
| menu_lang | Language |
| conns.sub | Each has its own key and address. One is active. |
| conns.sub.multi | Each opens on its own. |
| conns.multi | Several |
| conn.up / conn.down | open / closed |
| conn.err / conn.fail | error / failed |
| conn.retry | Retry |
| edit | Edit |
| edit.tun | All traffic |
| pref.multi | Several outbounds |
| pref.simple | Simple view |
| pref.socks.share | Shared SOCKS |
| pref.socks.port | SOCKS5 port (Settings; a connection may set its own) |
| pref.fail | Hand to a live one |
| pref.hold | Keep direct Internet closed |
| pref.popreset | In the menu on break |
| split | Split routing |
| split.d | A zone goes through Loft or around Loft. A service only belongs to a zone. |
| split.via | Through Loft |
| split.bypass | Around Loft |
| split.zone | Zone |
| split.zones | Zones (editor) |
| split.rdef | Direction of the zone (editor only) |
| split.repo.pick | Lists (button next to the URL) |
| split.lists | List catalog |
| split.lists.body | From the loft-routes repository. You can still paste any URL. |
| route.reset | Reset routes (Settings footer) |
| route.held | route closed |
| open.one / close.one | Open / Disconnect |
| about.ver | 1.0.22 |
| about.proto | protocols (caption; spec rows sit tight under it) |
| about.loft | loft/1.0 · get · ws · h2 · quic · h3 · mask |
| about.sstp | SSTP · ppp · mschapv2 · pap |
| about.vless | VLESS · tcp · tls |
| about.blurb | From the street, ordinary HTTPS. Inside, a private network. |
| about.sig | A3 |
| about.upd | One pill, vertically centered between the blurb and the colophon. Idle until tap: Check for updates. Then checking / current / Update to N / download % / install. No GitHub on open. No status line above it. |
| pref.upd | Check for updates at launch |
| upd.offer.title | loft update |
| upd.later | Later |
| upd.quiet | Don’t ask again (turns the launch check off) |

这些面上的词：overlay、loft、front、key。关于是两行加签名 A3。不讲标志怎么画。不说「VPN」、「protected」、「盾」。

---

## 11. 外壳

| 面 | 尺寸 | 备注 |
|----|------|------|
| 弹出层 | 300 × hug | `NSPopover.behavior = .transient` |
| 主窗口 | 400 × 560 | 隐藏 titlebar，红绿灯。简化界面 — hug。 |
| 首次打开 | 440 × 520 | 居中，不能缩放 |
| 编辑 | 440 × hug | 名称、类型、页签；高于屏幕时字段 overlay 滚动。保存钉在底部。VLESS TLS/REALITY 和 TCP/XHTTP 同一行。 |
| 设置 | 480 × hug | 盖在主窗口上的 sheet |
| 关于 | 360 × 448 | 版本、协议标题 + 规范行紧挨下面、blurb、GitHub 更新、A³ |
| 更新提议 | 360 × hug | 启动时发现新 DMG：更新 / 稍后 / 不再询问 |

窗口和弹出层 **hug** 内容。客户端没有常驻滚动槽。列表超出窗口：系统 overlay（`NSScrollView`，手势时出现）。

UI 字体：Avenir Next Medium 13 / 标题 Demi 15 / 次要 Regular 13。密钥和 IP 用等宽：SF Mono 12。

---

## 12. v1 以外

网络地图、国家选择、账号/密码、Windows 托盘不在这个目录。Windows 是单独的 `clients/windows`。产品名 split-tunnel 不在；§5 规定的分流在。
