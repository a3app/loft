# loft-node packages

**English** · [Русский](DIST_ru.md)

Built by `make dist-node`. Protocol 1.0. Software version is `VERSION`.

```
dist/node/
  linux-amd64/
    loft-node-<ver>-linux-amd64.tar.gz   POSIX installer (binary + install.sh)
    loft-node-<ver>-linux-amd64/         unpacked tree (for make deb)
    loft-node_<ver>_amd64.deb            Debian package
  linux-arm64/
    … same names with arm64 …
  SHA256SUMS
```

Tarball on the host:

```bash
tar -C /tmp -xzf loft-node-*-linux-amd64.tar.gz
cd /tmp/loft-node-*-linux-amd64
sudo ./install.sh
```

Debian package:

```bash
dpkg -i loft-node_*_amd64.deb
```

Installer flags and unit: [README.md](README.md) in this directory (`server/debian/README.md` in the tree).

GitHub Actions on tag `v*` (must match `VERSION`) publishes these files on a Release.
