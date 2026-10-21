# loft-node

[English](README.md) · [Русский](README_ru.md) · **中文**

节点实现：控制器或中继。一个二进制，角色在 YAML 里选。

运营：[docs/guide_zh.md](../docs/guide_zh.md)。协议：[docs/protocol_zh.md](../docs/protocol_zh.md)。布局：[docs/architecture_zh.md](../docs/architecture_zh.md)。

```bash
go build -o loft-node ./server/cmd/loft-node
./loft-node -c server/configs/server.yaml -dir /var/lib/loft
./loft-node -c server/configs/relay.yaml -dir /var/lib/loft-relay
./loft-node --version
```

| 参数 | 含义 |
|------|---------|
| `-c` | YAML：角色、诱饵、前置、TLS |
| `-dir` | `network.conf`、控制器 seed |
| `-listen` `-cert` `-key` | 改写 |
| `-trace` | 会话日志（GET seq）；空 = 关 |
| `--version` | 程序和协议版本 |

`server/origin` 提供 HTTPS 源站：掩护页、GET、WebSocket、HTTP/2 CONNECT、公开名册和列表。边缘预设：`internal/cdn`。

控制器拿着密钥、租约和 Ed25519 seed。中继终结公网 TLS，把明文拷到干线（`hub:1` / `hub:2`，`ctl:1`）。成员身份留在控制器上。

Debian 主机（systemd、安装脚本、`.deb`）：[debian/README_zh.md](debian/README_zh.md)。

```bash
make dist-node
# 把 dist/node/linux-amd64/loft-node-*-linux-amd64.tar.gz 拷到主机
sudo ./install.sh
```
