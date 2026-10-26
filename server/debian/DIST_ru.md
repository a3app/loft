# Пакеты loft-node

[English](DIST.md) · **Русский** · [中文](DIST_zh.md)

Собираются командой `make dist-node`. Протокол 1.0. Версия ПО берётся из `VERSION`.

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

Установка из tarball на хосте:

```bash
tar -C /tmp -xzf loft-node-*-linux-amd64.tar.gz
cd /tmp/loft-node-*-linux-amd64
sudo ./install.sh
```

Установка пакета Debian:

```bash
dpkg -i loft-node_*_amd64.deb
```

Флаги установщика и unit-файл описаны в [README_ru.md](README_ru.md) в этом же каталоге (`server/debian/README_ru.md` в дереве).

При пуше тега `v*` (он должен совпадать с `VERSION`) GitHub Actions выкладывает эти файлы в Release.
