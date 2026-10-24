# loft-panel

[English](README.md) · [Русский](README_ru.md) · **中文**

单台 loft 服务器的网页管理面板：节点、其网络与成员、IP 地址与上游通道、域名（DNS 记录与证书）、邮件、软件包、面板用户。目标：不手改文件即可安装并配置 loft 服务器。

交互规范：[design/DESIGN_zh.md](design/DESIGN_zh.md)。交互原型：[design/index.html](design/index.html)。

## 状态

第一个可用切片。目前真实可用：

- 登录、会话、凭安装码创建首个所有者、`loft-panel passwd` / `users`；
- **网段**：以交换机视图和配置编辑器编辑节点的 `network.conf`，应用时节点即时重读（仅受影响的成员重连）并自动回滚；
- 新成员在 `members.keys` 中获得真实的 `lt_` 密钥；应用后已删除成员的密钥随之移除；面板可保存密钥副本，以便再次显示链接、二维码和 `.loft`（设置，默认关闭）；
- 概览中的节点状态；
- 安装向导：所有者、域名与 DNS（Cloudflare、DigitalOcean、Namecheap 或手工；面板创建 A/AAAA 和 CAA）、Let’s Encrypt 证书、第一个成员、首批软件包；
- **域名**：分区、DNS 记录（通过提供商 API 或手工，并检查手工记录）、Let’s Encrypt 证书（HTTP-01 与 DNS-01）和 PEM 上传；A/AAAA 随服务器地址自动更新；
- **软件包**：从封闭目录安装、升级、停用、删除软件包（Telegram MTProto 代理、WireGuard、OpenVPN、ZeroTier、fail2ban；节点、nginx、SSTP 为基础），任务日志直接显示在页面上；
- **loft 节点**：在界面上编辑 node.conf（监听、前端、节点后的 Web 服务器、伪装、路径、中继、CDN 地址段、共享令牌、会话日志）、地址列表、更换节点令牌、服务日志；在线情况来自节点状态文件；
- **用户**：带“分区 × 无/查看/修改”矩阵的角色（每个请求都检查）、邀请、API 令牌（`Authorization: Bearer lpt_…`）、会话、修改日志；
- **互联网**：连接（WireGuard、SSTP、OpenVPN、ZeroTier、HTTP/HTTPS/SOCKS5 代理、Xray）和出口规则；连接优先级——默认策略、无访问和自定义访问策略（策略应用）；服务器地址与路由（未确认的修改 2 分钟后撤销）；
- **邮件**：邮件域、邮箱、DNS 记录和 mail.<domain> 的证书（在安装邮件软件包后）；
- **系统**：fail2ban、加密备份和 Linux 账户；
- **从发布版更新**：节点与面板来自 GitHub Releases，按 SHA256SUMS 校验，服务未能启动则回滚。

## 目录

```
server/cmd/loft-panel       网页服务，独立用户，无特权
server/cmd/loft-panel-adm   root 助手（Linux），unix 套接字，固定操作列表
server/panel/web            HTTP：/api、登录、内嵌 UI（ui/index.html + ui/live.js）
server/panel/adm            助手的工作：network.conf、members.keys、systemctl
server/panel/admproto       面板 ↔ 助手协议
server/panel/catalog        面板可安装的软件包（键、apt 包名、GitHub 二进制、软件源、单元）
server/panel/auth           argon2id、会话、登录限制
server/panel/store          users.json, settings.json, member-keys.json
server/panel/debian         install.sh、.deb、systemd 单元、nginx 站点
server/panel/devstand       在笔记本上运行真实面板与助手（systemctl 为模拟）
server/panel/design         交互规范与原型
```

`web/ui/index.html` 为生成文件：`make panel-ui` 复制 `design/index.html` 并加入 `live.js`，在已有后端的地方用 API 替换示例数据。同时复制 `en.js`、`zh.js` 和 `i18n.js`（源语言为俄语，另有英语和简体中文）。直接从磁盘打开的原型仍是原型。

## 安装

先装面板，由面板拉起节点：压缩包的 `node/` 中带有 loft-node，服务器上没有节点时会自动安装。已运行 loft-node 的服务器上，面板直接装在其上。

```bash
make dist-panel     # dist/panel/linux-{amd64,arm64}：含 install.sh 的压缩包与 .deb
sudo sh install.sh --host panel.example.net        # 在解压后的目录中
sudo apt install ./loft-panel_VERSION_amd64.deb    # 或使用软件包（Depends: loft-node）
```

两种方式都会创建 `loft-panel` 用户，安装 nginx 并在 127.0.0.1:8443 放置面板站点，把节点的 decoy 指向它并开启 `decoy_proxy enable`（`loft-panel-adm wire-node`；指向他人网站的 decoy 不会被改动），启动两个服务并打印安装码。再次运行即升级；`--uninstall` / `--remove` 用于卸载。

## 构建与试用

```bash
make panel          # loft-panel（本机）与 loft-panel-adm（linux）
make panel-dev      # http://127.0.0.1:8091，安装码 000000-000000
go test ./server/panel/...
```

## 端口

```
:443  loft-node ── loft 客户端 ──▶ 覆盖网
          └─ 其余流量 ──▶ nginx 127.0.0.1:8443 ──▶ 面板 127.0.0.1:8091、网页邮箱、按 SNI 转 Xray
:80   nginx（ACME HTTP-01）
:8448 Telegram MTProto 代理（安装该软件包后）
```

## 安全

- 助手只接受面板用户的 uid（`SO_PEERCRED`，套接字 0660），拒绝未知操作与未知 JSON 字段，并限制请求大小。
- 写入前用节点自身的解析器检查 `network.conf`。写入为带 fsync 的原子操作；旧文件保留为 `network.conf.prev`。
- 应用时须附上编辑所基于文件的 SHA-256，两个编辑者不会互相覆盖。
- 应用会让节点重读配置（SIGHUP；单元没有 ExecReload 时改为重启）。若数秒后 `loft-node` 未处于 active，旧的 `network.conf` 与 `members.keys` 会被恢复，节点再次重读。
- 软件包：浏览器只发送目录键；apt 包名、软件源、签名密钥（内置于二进制）、GitHub 压缩包的 SHA-256、单元和数据目录均来自目录。助手在临时单元 `loft-panel-job`（`systemd-run`）中运行任务：一次一个，在助手沙箱之外，面板和助手重启后任务继续。`job.json` 与 `job.log` 位于 `/var/lib/loft-panel-adm`。
- 更新只安装最近一次检查找到的发布版（页面无法指定更旧的标签）。下载内容必须与发布版的 `SHA256SUMS` 一致。安装后节点或面板未处于 active 时：压缩包安装会放回保存的二进制，.deb 安装会重新安装当前版本的 .deb。默认模式：检查并通知。
- 首个所有者需要 `/var/lib/loft-panel/setup.code` 中的安装码，第一个访客无法抢占面板。
- 会话使用 HttpOnly、SameSite=Strict 的 cookie（HTTPS 后为 Secure），另加 CSRF 令牌请求头。未知 `Host` 返回 421。
- 失败登录会封禁客户端地址（10 分钟内 5 次 → 24 小时）。开启 `decoy_proxy enable` 后节点会把地址传给 nginx。未开启时所有客户端都显示为 127.0.0.1，失败只会让该登录变慢（最多 30 秒），而不会把所有人锁在外面。

## 需要节点补充的能力

| 缺失 | 影响 |
|------|------|
| 状态 API（租约、会话、中继、流量） | 在线指示灯、最近登录、计数 |
| 单节点多网络 | 网段中的第二个网络 |
