# 导入 URI

[English](share.md) · [Русский](share_ru.md) · **中文**

客户端导入串。这**不是** loft/1.0 的线能力，也不抬协议版本。节点看不到这个 URL。

loft 地址是 `loft://`：类型加传输写在一串里。一条 URI 一个出站。`.loft` YAML 文件装着**一条或多条连接**，**可以**混用所有支持的类型（`loft`、`sstp`、`vless`、`trojan`）。文件格式：[§8](#8-loft-文件)。

协议：[protocol_zh.md](protocol_zh.md)。iOS UX：[clients/iOS/design/DESIGN_zh.md](../clients/iOS/design/DESIGN_zh.md)。Android UX：[clients/Android/design/DESIGN_zh.md](../clients/Android/design/DESIGN_zh.md)。macOS UX：[clients/macOS/design/DESIGN_zh.md](../clients/macOS/design/DESIGN_zh.md)。Sailfish UX：[clients/Sailfish/design/DESIGN_zh.md](../clients/Sailfish/design/DESIGN_zh.md)。

---

## 1. 检测器

能接二维码、粘贴、照片或文件的客户端，按这个顺序试：

1. 去掉 UTF-8 首尾空白。去掉开头的 UTF-8 BOM。
2. `loft://` → [§2](#2-loft)。
3. `vless://` → VLESS 分享串（[§5](#5-vless)）。
4. `trojan://` → Trojan 分享串（[§6](#6-trojan)）。
5. `sstp://` → SSTP 分享串（[§7](#7-sstp)）。
6. 带 `token:` / `password:` / `outbounds:` / `connections:` 的 YAML → 当 `.loft` 导入（[§8](#8-loft-文件)）。
7. 否则拒绝。

同一个检测器：摄像头二维码、照片或文件里的图、`.txt` / `.loft` / YAML 文件、粘贴栏。

二维码载荷是 UTF-8 的 URI（或 YAML）。纠错 **M** 就够。普通 `loft://` 能塞进 QR 版本 10。多条连接的包可能塞不进二维码；那就用文件。

---

## 2. loft://

规范 loft 地址：

```
loft://<token>@<host>[:port][/path][?query][#name]
```

客户端发出并接受 `loft://`。

RFC 3986。`token` 和 `name` 要百分号编码（fragment 不能用 `application/x-www-form-urlencoded`）。默认端口 **443**。IPv6 主机加方括号。

scheme **本身就是**连接类型（`loft`）。Query 的 `type` 是**传输**（`get` `ws` `h2` `quic` `h3`）。列表行两个都显示：`loft · get · cdn.example.com`。

| 部分 | 对应到 `client.yaml` |
|------|----------------------|
| scheme `loft` | `kind: loft` |
| `token`（userinfo） | `token`（`lt_…`） |
| `host`[:`port`] | 出站 `url` 的主机 |
| `/path` | 出站 `path`（可以空） |
| `#name` | 连接显示名；空则用 `host` |

### Query

| 键 | 取值 | 默认 | YAML |
|----|------|------|------|
| `type` | `get` `ws` `h2` `quic` `h3` | `get` | 出站 `type`（传输） |
| `cdn` | 配置名（`yandex`，…） | 不写 | 出站 `cdn` |
| `fp` | TLS 指纹（`chrome`，…） | `chrome` | 出站 `fingerprint` |
| `scheme` | `https` `http` `wss` `ws` | 随 `type` | `url` 的 scheme |
| `pbk` | 节点 X25519 公钥，base64url 或 hex，32 字节 | 不写 | 出站 `mask_pub` |

默认 `scheme`：`ws` → `wss`；其余 → `https`。

出站 URL 拼成 `{scheme}://{host}:{port}{path}`。

不要放 `insecure_skip_verify`、`header_budget` 和 `inflight`。客户端用自己的默认值。访问密钥不要放进 query 参数。

未知 query 键必须忽略（向前兼容）。未知 `type` 是错误。

### 例子

CDN 上的 GET：

```
loft://lt_change-me-long-random@cdn.example.com/static/v3/build?type=get&cdn=yandex&fp=chrome#Home
```

变成：

```yaml
name: Home
kind: loft
token: "lt_change-me-long-random"
select: auto
outbounds:
  - name: Home
    type: get
    cdn: yandex
    url: https://cdn.example.com
    path: /static/v3/build
    fingerprint: chrome
```

WebSocket：

```
loft://lt_change-me-long-random@app.example.com/api/v1/sync?type=ws&fp=chrome#Office
```

→ `url: wss://app.example.com/api/v1/sync`。

QUIC，没有路径：

```
loft://lt_change-me-long-random@app.example.com:443?type=quic#Fast
```

→ `url: https://app.example.com:443`。

### 导出

会分享的客户端，给**当前选中**连接的**当前选中**出站发 `loft://`（一个二维码，一个前置）。这条连接有多个前置，或多条连接：导出 `.loft`（[§8](#8-loft-文件)）。URI 是秘密：里面有访问密钥。在设备上显示；不要写进日志。

iOS 注册 URL scheme `loft`。Android 注册同样的 intent filter。Sailfish（以及 Aurora）注册 `x-scheme-handler/loft`。在信息里点 `loft://…` 会打开应用并提示导入。

---

## 3. 相机和文件

| 来源 | API |
|------|-----|
| 实时二维码（iOS） | `AVCaptureMetadataOutput`，类型 QR。`NSCameraUsageDescription`。 |
| 照片 | iOS：`PHPicker`。Android：Photo Picker。macOS：打开面板。静态图上的二维码。 |
| 文件 | 图片、`public.plain-text`、YAML、`.loft`、`.vpn`。从静态图解码二维码；否则按 §1 当字节处理。 |

命中之后：确认屏显示**名称**、**类型**、**传输**、**主机**。令牌藏着。保存写入 Keychain + App Group YAML。打开是另一次点击。

---

## 4. 这不是什么

不是 Hello/Status 能力。不是新的 mux 命令。不是替代 `.loft`。不是客户端去轮询的订阅 URL。

---

## 5. VLESS

现有 VLESS 客户端用的标准分享串。类型 `vless`。传输是 VLESS 的 `type` / `security`。支持：TCP+TLS、REALITY、Vision（`xtls-rprx-vision`）、XHTTP。列表副标题：`vless · reality · host.example.com`。

```
vless://<uuid>@<host>[:port]?security=…&sni=…&type=…&flow=…&pbk=…&sid=…&fp=…#Name
```

解析规则和 `internal/vless`（`ParseURL`）一样：`security` `tls` / `xtls` / `reality`，`type`（`tcp`、`xhttp`），`flow` 里带 `vision`，`pbk` / `sid` 表示 Reality。默认端口 443。

编辑器对同一条分享串有两种视图，和 v2rayN / NekoBox / Hiddify / 3x-ui 一样：

- **链接** — 一行 `vless://`（从面板或 Telegram 粘贴）。
- **手动** — 地址、端口、UUID、TLS/REALITY、TCP/XHTTP，然后这个模式要的字段（SNI、指纹、`pbk` / `sid`、Vision、XHTTP 路径/Host）。

手动会拼出 `vless://`。粘贴进来的链接若这版不支持（`ws`、`grpc`、`kcp`），原样留着，直到切到手动。

在 iOS / Android 上，这是同一条系统 VPN 上的 loft 连接（计划第 5 阶段）。不是第二条 VPN 行。

---

## 6. Trojan

现有 Trojan 客户端（trojan-gfw / Xray）用的标准分享串。类型 `trojan`。密码是 URI 的 userinfo。支持：TCP+TLS、REALITY、XHTTP stream-one、UDP ASSOCIATE。没有 WS / gRPC / kcp。列表副标题：`trojan · reality · host.example.com`。

```
trojan://<password>@<host>[:port]?security=…&sni=…&type=…&pbk=…&sid=…&fp=…#Name
```

解析规则和 `internal/trojan`（`ParseURL`）一样：`security` `tls` / `reality`，`type`（`tcp`、`xhttp`），`pbk` / `sid` 表示 Reality，`allowInsecure=1` 跳过 dest 证书校验。默认端口 443。Vision 只有 VLESS 才有。

在 iOS / Android 上，这和 loft 走同一条系统 VPN。代码：`internal/trojan`。

---

## 7. SSTP

和 loft 走同一条系统 VPN。类型 `sstp`。传输是 TLS + PPP。列表副标题：`sstp · tls · vpn.example.com`。代码：`internal/sstp`。不是 loft/1.0 能力。

```
sstp://<user>:<password>@<host>[:port][/path]?sni=…&pap=1#Name
```

默认端口 **443**。默认路径为空（客户端用普通 SSTP Connect URI）。`user` 和 `password` 百分号编码。Query `sni` 是 TLS ServerName；`pap=1` 允许 PAP/CHAP-MD5（默认只用 MSCHAPv2）。

YAML 映射（和 macOS 导出一样）：

```yaml
name: Office
kind: sstp
username: alice
password: "secret"
outbounds:
  - name: Office
    type: sstp
    url: https://vpn.example.com
```

已有 host / user / password 的 `.vpn` JSON 也当成这条连接。路径切换会重拨；IPCP 可能发新 IP。

---

## 8. `.loft` 文件

所有 loft 应用的全系格式（iOS、Android、macOS、Sailfish，以后还有 TV）。YAML。文件**本身就是**秘密。

两种形态。都合法。

### 一条连接

就是现在的 `client.yaml`，加上可选的 `kind`。不写 `kind` 就从 `outbounds[].type` 推断（`get`/`ws`/`h2`/`quic`/`h3` → `loft`，`sstp` → `sstp`，`vless` → `vless`，`trojan` → `trojan`）。

```yaml
name: Home
kind: loft
token: "lt_change-me-long-random"
select: auto
outbounds:
  - name: cdn
    type: get
    url: https://cdn.example.com
    path: /static/v3/build
```

### 多条连接

顶层 `connections:` 是列表。每项是任意类型的一条连接。一个文件**可以**混 loft、SSTP、VLESS 和 Trojan。

```yaml
connections:
  - name: Home
    kind: loft
    token: "lt_change-me-long-random"
    select: auto
    outbounds:
      - name: cdn
        type: get
        url: https://cdn.example.com
        path: /static/v3/build
  - name: Office
    kind: sstp
    username: alice
    password: "secret"
    outbounds:
      - name: Office
        type: sstp
        url: https://vpn.example.com
  - name: Travel
    kind: vless
    outbounds:
      - name: Travel
        type: vless
        url: vless://uuid@de1.example.com:443?security=reality&sni=www.example.com
  - name: Edge
    kind: trojan
    password: secret
    outbounds:
      - name: Edge
        type: trojan
        url: trojan://secret@de1.example.com:443?security=reality&sni=www.example.com
```

文档**不得**把顶层 `token:` / `outbounds:` 和非空 `connections:` 写在一起。

导入：列表每项变成磁盘上的一个配置。手机应用仍然同时只跑**一条**隧道。macOS 可以同时挂几条会话。

### 导出

| 选中什么 | 文件 |
|----------|------|
| 一条连接，一个前置 | 二维码 / 粘贴 `loft://`（或 `sstp://` / `vless://` / `trojan://`） |
| 一条连接，多个前置 | `Name.loft`（一份文档） |
| 当前选中的连接 | `Name.loft`（一份文档） |
| 多条 / 全部连接 | `Family.loft`（`connections:`） |

导出当前连接是列表动作（iOS/Android：灯那一行上的芯片；长按；macOS：行的上下文菜单）。设置里**可以**仍导出全部。

连接在界面上的描述是 **类型 · 传输 · 主机**，从不只写传输。
