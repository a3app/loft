# Debian 上的 loft-node

[English](README.md) · [Русский](README_ru.md) · **中文**

软件 1.0.31。协议 1.0。协议规范：[docs/protocol_zh.md](../../docs/protocol_zh.md)。运营：[docs/guide_zh.md](../../docs/guide_zh.md)。

这个目录是节点的 Debian 布局：POSIX 安装脚本、systemd unit、sysctl、命令配置、还有 `.deb`。

---

## 1. 机器上的布局

| 路径 | 作用 |
|------|------|
| `/usr/local/bin/loft-node` | 二进制（tarball 安装脚本） |
| `/usr/bin/loft-node` | 二进制（`.deb`） |
| `/etc/loft/node.conf` | 角色、前置、诱饵、advertise |
| `/etc/loft/loft.env` | `LOFT_TOKEN`（mode 0640，组 `loft`） |
| `/etc/loft/tls/fullchain.pem` | 公网证书 |
| `/etc/loft/tls/privkey.pem` | 私钥 |
| `/var/lib/loft/network.conf` | 覆盖网 CIDR、成员、对端 |
| `/var/lib/loft/members.keys` | 明文成员密钥（mode 0600），运营方签发 |
| `/var/lib/loft/controller.seed` | Ed25519 seed，第一次启动时写入 |
| `/etc/systemd/system/loft-node.service` | unit |
| `/etc/sysctl.d/99-loft.conf` | QUIC 用的 UDP 缓冲；internet ICMP 用的 ping_group_range |

进程以用户 `loft` 跑。TCP 和 UDP 443 靠 `CAP_NET_BIND_SERVICE` 绑定。令牌从不出现在命令行。

新安装会写 `fronts get` 和 `auth shared disable`，并在 `members.keys` 里放一把 `operator` 密钥。`/root/loft-client.yaml` 只有 GET，拿到的是这个 `lt_`，不是 `LOFT_TOKEN`。`loft.env` 里的 `LOFT_TOKEN` 仍是节点密钥（伪装、控制器 seed）。要留着旧的单令牌覆盖网，在 `members.keys` 还空着时把 `auth shared enable` 写进 `node.conf`。二进制升级之后，已有的共享令牌节点如果没有这行，会拒绝客户端，直到你补上这行或签发密钥。要 `ws`、`h2`、`quic`、`h3` 就写进 `fronts`。

---

## 2. tarball 安装脚本

在编译机上：

```bash
make dist-node
```

写出 `dist/node/linux-amd64/` 和 `dist/node/linux-arm64/`（tarball、解开的安装树、`.deb`）以及 `dist/node/SHA256SUMS`。布局：[DIST_zh.md](DIST_zh.md)。

在 Debian 主机上：

```bash
tar -C /tmp -xzf loft-node-*-linux-amd64.tar.gz
cd /tmp/loft-node-*-linux-amd64
sudo ./install.sh
```

常用参数：

| 参数 | 含义 |
|------|---------|
| `--dry-run` | 打印每一步，什么都不改 |
| `--unattended`, `-y` | 不问 |
| `--role=controller` | 默认 |
| `--role=relay` | TLS 边缘；需要 `--controller=` 和 `--id=` |
| `--advertise=https://host` | 名册里的公网 URL |
| `--listen=:443` | 绑定地址 |
| `--decoy=host:443` | 诱饵拼接源站 |
| `--mask` | 借用证书伪装（配合诱饵） |
| `--cert=` `--key=` | 已有 PEM；否则自签证书 |
| `--peers=hub` | 覆盖网策略（`hub` \| `mesh` \| `vlan`） |
| `--token=lt_…` | 否则自动生成 |
| `--no-firewall` | 不动 ufw |
| `--uninstall` | 卸掉服务，留下数据 |
| `--remove` | 卸掉服务并擦掉数据 |

再跑一遍会换掉二进制并重启 unit。令牌、配置和证书留下。

接受 Debian 12 和 13、Ubuntu 22.04 / 24.04 / 26.04。Debian 10/11 和 Ubuntu 16/18/20 会拒绝。其他 Debian 家族会问一声。

---

## 3. Debian 包

```bash
make dist-node            # tarball + .deb，amd64 和 arm64
make deb                  # 只要 amd64 .deb，先要 dist-debian
make deb ARCH=arm64
```

```bash
dpkg -i dist/node/linux-amd64/loft-node_*_amd64.deb
# 改 /etc/loft/node.conf
systemctl enable --now loft-node
```

`postinst` 会建用户、令牌、自签证书和默认配置。不启动 unit。`apt purge loft-node` 会删掉 `/etc/loft` 和 `/var/lib/loft`。

---

## 4. 装完之后

`install.sh` 写出 `/root/loft-client.yaml`（mode 0600），里面有令牌、advertise URL 和 `controller_pub`。证书还是自签时，`insecure_skip_verify` 是开的。换成公网证书后把这个字段关掉。

```bash
journalctl -u loft-node -f
loft-node --version
loft-node -dir /var/lib/loft -print-pub
```

主机前面的路径上打开 443/tcp 和 443/udp。QUIC 和 HTTP/3 走 UDP。

---

## 5. 中继

在边缘上：

```bash
sudo ./install.sh --role=relay \
  --id=ru1 \
  --controller=wss://controller.example/api/v1/sync \
  --advertise=https://203.0.113.10
```

`enrol: list` 时，控制器 YAML 的 `relays:` 下必须有同一个 `id`。
