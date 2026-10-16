# Пакеты loft-node

[English](DIST.md) · **Русский**

Собирает `make dist-node`. Протокол 1.0. Версия ПО — `VERSION`.

```
dist/node/
  linux-amd64/
    loft-node-<ver>-linux-amd64.tar.gz   POSIX-установщик (бинарник + install.sh)
    loft-node-<ver>-linux-amd64/         распакованное дерево (для make deb)
    loft-node_<ver>_amd64.deb            пакет Debian
  linux-arm64/
    … те же имена с arm64 …
  SHA256SUMS
```

Tarball на хосте:

```bash
tar -C /tmp -xzf loft-node-*-linux-amd64.tar.gz
cd /tmp/loft-node-*-linux-amd64
sudo ./install.sh
```

Пакет Debian:

```bash
dpkg -i loft-node_*_amd64.deb
```

Флаги установщика и unit: [README_ru.md](README_ru.md) в этом каталоге (`server/debian/README_ru.md` в дереве).

GitHub Actions на теге `v*` (должен совпасть с `VERSION`) кладёт эти файлы в Release.
