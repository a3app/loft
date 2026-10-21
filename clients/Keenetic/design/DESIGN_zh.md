# loft · Keenetic 面板

[English](DESIGN.md) · [Русский](DESIGN_ru.md) · **中文**

交互模型：[`index.html`](index.html)。品牌：[`brand/`](../../../docs/brand/)。对接：[docs/keenetic_zh.md](../../../docs/keenetic_zh.md)。

面板是小 LAN 站。连接优先级、策略、DNS 路由还在 Keenetic。这个 UI 只拉起 loft 会话、写 YAML。

---

## 1. 产品

一条命名连接。一个 OpkgTun。loft 里面多条前置（`select: auto`）。VLESS 以后是同一接口上的出站。

运营方：

1. 登录 Keenetic 网页管理。
2. 打开 `http://<lan-ip>:8090/`（cookie 已经有效，或走系统登录再回来）。
3. 粘贴访问密钥和节点 URL，选前置，保存，启动。
4. 在 Keenetic：**网络规则 → 连接优先级** — 拖 **Loft**。

没有第二份密码。没有 SSTP 表单。

## 2. 屏幕

| 屏幕 | 内容 |
|--------|------|
| 入口 | 正在检查 Keenetic 会话。401 → 系统 `/`，带 `return=`。 |
| 状态 | 灯，Overlay IP / 网关 / MTU，活前置，上下行字节，启动/停止。链接：「在 Keenetic 优先级里打开」。 |
| 连接 | 名称、`lt_…` 密钥（保存后隐藏）、URL、类型、路径，可选 CDN 配置和 uTLS 指纹。 |
| 前置 | 勾选 get / ws / h2 / quic / h3。顺序 = 尝试顺序。握手之后和 Status `caps` 做交集。 |
| VLESS | 关掉的块。文案：「同一条隧道，以后。」 |
| 日志 | 守护进程最近 80 行。没有密钥材料。 |

简化界面（默认）：状态 + 连接。前置、日志、VLESS 藏在「高级」后面。

## 3. Keenetic 外壳

不要改 Keenetic `:80` 的样子。模型只说明 **Loft** 在 *他们* 的其他连接 / 优先级里长什么样：像 WAN 的一行，描述 Loft，开关是 NDM `up`，不是我们的启动。我们的启动拉起进程，然后 `ndmc` 置 `up`。

以后可选：仪表盘 *卡片*，只是指向 `:8090` 的链接。带版本的注入，永远不是设置表单。

## 4. 视觉

跟 macOS 同一套 loft 记号：Ink `#1A1714`，Lamp `#E8B84A`，Paper `#E8DFD2`，Avenir Next。模型里的 Keenetic 框是浅色（他们的管理界面），好让运营方看见两边。

字体：标题 Demi，正文 Regular，密钥 SF Mono。已连接 = 灯窗亮。已关闭 = 灯灭，石头色 L。

底栏显示监听地址：`http://192.168.1.1:8090/` — 只在 LAN。

## 5. 文案

| EN | RU |
|----|----|
| Loft | Loft |
| Checking Keenetic session… | Проверяем сессию Keenetic… |
| Connection | Подключение |
| Member key | Ключ участника |
| Use Keenetic for split routing | Маршруты — в Keenetic |
| VLESS later | VLESS позже |
| SSTP is the stock Keenetic client | SSTP — штатный клиент Keenetic |
