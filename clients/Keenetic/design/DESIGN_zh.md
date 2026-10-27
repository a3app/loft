# loft · Keenetic 面板

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

交互模型：[`index.html`](index.html)。品牌：[`brand/`](../../../docs/brand/)。对接：[docs/keenetic_zh.md](../../../docs/keenetic_zh.md)。

面板是小 LAN 站。连接优先级、策略、DNS 路由还在 Keenetic。这个 UI 只保持 loft 和 VLESS 会话在线，并写它们的 YAML。

---

## 1. 产品

多个具名连接。每个连接一个 OpkgTun、在 Keenetic 里一行，loft 配置和 VLESS 都一样。所有已启用的连接同时保持会话；走哪一个、断开后切到哪一个，由 Keenetic 的优先级和策略决定。

操作者：

1. 登录 Keenetic 网页后台。
2. 打开 `http://<lan-ip>:8090/`（cookie 仍有效，否则走原生登录再回来）。
3. 添加连接：`loft://` 或 `vless://` 链接、手动填写，或导入文件。
4. 在 Keenetic：**网络规则 → 连接优先级** 把连接放进策略；**DNS 路由** 把域名列表指向它。

没有第二个密码。没有 SSTP 表单：SSTP 仍是 Keenetic 原生客户端。

## 2. 界面

| 界面 | 内容 |
|------|------|
| 登录 | 检查 Keenetic 会话。401 → 原生 `/` 带 `return=`。 |
| 连接 | 列表：灯、名称、类型 · 前端 · OpkgTunN、状态标签（在线 / 错误 / 关闭）、开关。按钮：导入、添加。 |
| 状态 | 每个连接：状态、覆盖网 IP / 网关、MTU、节点（roster 中“另有 N 个备用”）、当前前端、流量。停止、前端、“Keenetic 优先级”。 |
| 连接 · loft | 名称；类型切换 loft / VLESS；模式切换 **链接 / 手动**，与桌面客户端一致。链接：`loft://` 地址。手动：访问密钥、地址和端口、连接方式（GET · WS · H2 · QUIC · H3 · L3）、路径。保存、返回、删除。 |
| 连接 · VLESS | 同一框架。链接：`vless://`。手动：UUID、地址和端口、安全 TLS / REALITY、传输 TCP / XHTTP、TLS 主机名、TLS 指纹、公钥、short ID、Vision。 |
| 导入 | 拖入或选择 `client.yaml`、`.loft`、`.vpn` 或二维码图片。在下一个空闲 OpkgTun 上新建连接。 |
| 前端 | 每个 loft 连接：get / ws / h2 / quic / h3 / l3。顺序 = 尝试顺序。与 Status `caps` 取交集。 |
| 日志 | 所有连接的最近 80 行，行首带名称。密钥会缩短。 |

## 3. Keenetic 原生界面

不改 Keenetic `:80` 的样式。模型展示*他们*的两个页面：

- **其他连接：** 每个连接一行类似 WAN 的条目（Loft Home = OpkgTun0，VLESS NL = OpkgTun1）。开关是 NDM `up`，不是我们的启动。我们的启动拉起会话，然后 `ndmc` 设置 `up`。
- **优先级：** 策略示例（“笔记本”：Loft Home → VLESS NL，ISP 关闭；“电视”：VLESS NL → ISP），以及分流在 DNS 路由里的提示。

面板不写优先级、策略和 DNS 路由。节点下发的路由配置不在路由器上应用。

以后可选：仪表盘上一张只链接到 `:8090` 的卡片。版本化注入，绝不是设置表单。

## 4. 页面

页面是共用的 `lanui` v2（`clients/go/lanui`）：连接列表、导入、编辑 loft 和 VLESS、日志。平台区块：Keenetic 显示 OpkgTun 名称和优先级入口；MikroTik 保留出口接口和 `uplink.rsc`。Keenetic 上只用网页后台会话登录。

## 5. 视觉

与 macOS 相同的 loft 色板：Ink `#1A1714`、Lamp `#E8B84A`、Paper `#E8DFD2`、Avenir Next。模型里的 Keenetic 框是浅色（他们的后台），让操作者同时看到两边。

字体：标题 Demi，正文 Regular，密钥 SF Mono。已连接 = 灯亮。错误 = 红灯。关闭 = 石头色。

底栏显示监听地址：`http://192.168.1.1:8090/` — 只在 LAN。

## 6. 文案

见 [DESIGN.md](DESIGN.md) 的表格。字段名取自桌面客户端（`clients/macOS/lang`）。
