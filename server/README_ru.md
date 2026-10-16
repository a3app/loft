# loft-node

[English](README.md) · **Русский**

Реализация ноды: controller или relay. Один бинарник, роль выбирается в YAML.

Эксплуатация: [docs/guide_ru.md](../docs/guide_ru.md). Провод: [docs/protocol_ru.md](../docs/protocol_ru.md). Раскладка: [docs/architecture_ru.md](../docs/architecture_ru.md).

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
| `-listen` `-cert` `-key` | Переопределения |
| `-trace` | Журнал сессии (GET seq); пусто = выкл |
| `--version` | Версии программы и протокола |

`server/origin` отдаёт HTTPS-источник: cover, GET, WebSocket, HTTP/2 CONNECT, публичный roster и списки. Пресеты края: `internal/cdn`.

Контроллер держит ключи, аренды и Ed25519 seed. Relay завершает публичный TLS и копирует plaintext на транк (`hub:1` / `hub:2`, `ctl:1`). Идентичность члена остаётся на контроллере.

Хост Debian (systemd, установщик, `.deb`): [debian/README_ru.md](debian/README_ru.md).

```bash
make dist-node
# скопировать dist/node/linux-amd64/loft-node-*-linux-amd64.tar.gz на хост
sudo ./install.sh
```
