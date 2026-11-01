# loft-node 包

[English](DIST.md) · [Русский](DIST_ru.md) · **中文**

`make dist-node` 打出来。协议 1.0。软件版本是 `VERSION`。

```
dist/node/
  linux-amd64/
    loft-node-<ver>-linux-amd64.tar.gz   POSIX 安装脚本（二进制 + install.sh）
    loft-node-<ver>-linux-amd64/         解开的树（给 make deb）
    loft-node_<ver>_amd64.deb            Debian 包
  linux-arm64/
    … 同样的名字，换成 arm64 …
  SHA256SUMS
```

主机上的 tarball：

```bash
tar -C /tmp -xzf loft-node-*-linux-amd64.tar.gz
cd /tmp/loft-node-*-linux-amd64
sudo ./install.sh
```

Debian 包：

```bash
dpkg -i loft-node_*_amd64.deb
```

安装参数和 unit：本目录的 [README_zh.md](README_zh.md)（仓库里是 `server/debian/README_zh.md`）。

GitHub Actions 在 `v*` 标签上（必须和 `VERSION` 一致）把这些文件发到 Release。
