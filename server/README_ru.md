# loft-node

[English](README.md) · **Русский** · [中文](README_zh.md)

Реализация ноды в двух ролях: controller и relay. Бинарник один, роль задаётся в YAML.

Эксплуатация: [docs/guide_ru.md](../docs/guide_ru.md). Протокол: [docs/protocol_ru.md](../docs/protocol_ru.md). Устройство: [docs/architecture_ru.md](../docs/architecture_ru.md).

```bash
go build -o loft-node ./server/cmd/loft-node
./loft-node -c server/configs/server.yaml -dir /var/lib/loft
./loft-node -c server/configs/relay.yaml -dir /var/lib/loft-relay
./loft-node --version
```

| Флаг | Смысл |
|------|---------|
| `-c` | YAML: роль, decoy, фронты, TLS |
| `-dir` | `network.conf`, seed контроллера |
| `-listen` `-cert` `-key` | Переопределяют значения из конфига |
| `-trace` | Журнал событий сессии (GET seq); пусто = выключен |
| `--version` | Версии программы и протокола |

`server/origin` обслуживает HTTPS origin: cover-страницу, GET, WebSocket, HTTP/2 CONNECT, публичный roster и списки. Пресеты для CDN/края сети: `internal/cdn`.

Контроллер держит ключи, аренды и Ed25519 seed. Relay терминирует публичный TLS и копирует расшифрованный трафик в транк (`hub:1` / `hub:2`, `ctl:1`). Удостоверение участника остаётся на контроллере.

Хост Debian (systemd, установщик, `.deb`): [debian/README_ru.md](debian/README_ru.md).

```bash
make dist-node
# скопировать dist/node/linux-amd64/loft-node-*-linux-amd64.tar.gz на хост
sudo ./install.sh
```
