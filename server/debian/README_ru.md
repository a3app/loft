# loft-node на Debian

[English](README.md) · **Русский** · [中文](README_zh.md)

ПО 1.0.35. Протокол 1.0. Спецификация: [docs/protocol_ru.md](../../docs/protocol_ru.md). Эксплуатация: [docs/guide_ru.md](../../docs/guide_ru.md).

Здесь собрано всё для установки ноды на Debian: POSIX-установщик, unit systemd, sysctl, командный конфиг и `.deb`.

---

## 1. Размещение файлов на машине

| Путь | Роль |
|------|------|
| `/usr/local/bin/loft-node` | бинарник (установщик из tarball) |
| `/usr/bin/loft-node` | бинарник (`.deb`) |
| `/etc/loft/node.conf` | роль, фронты, decoy, advertise |
| `/etc/loft/loft.env` | `LOFT_TOKEN` (mode 0640, группа `loft`) |
| `/etc/loft/tls/fullchain.pem` | публичный сертификат |
| `/etc/loft/tls/privkey.pem` | закрытый ключ |
| `/var/lib/loft/network.conf` | CIDR оверлея, участники, пиры |
| `/var/lib/loft/members.keys` | ключи участников открытым текстом (mode 0600), их выдаёт оператор |
| `/var/lib/loft/controller.seed` | Ed25519 seed, пишется при первом старте |
| `/etc/systemd/system/loft-node.service` | unit |
| `/etc/sysctl.d/99-loft.conf` | UDP-буферы для QUIC |
| `/etc/sysctl.d/99-loft-ping.conf` | Только если нужно: `ping_group_range`, расширенный ровно до группы loft, чтобы участники могли пинговать интернет через ноду. Уже заданный диапазон не сужается |

Процесс работает от пользователя `loft`. TCP и UDP 443 он занимает благодаря `CAP_NET_BIND_SERVICE`. Токен никогда не передаётся в командной строке.

Новая установка записывает `fronts get` и `auth shared disable`, а также ключ `operator` в `members.keys`. `/root/loft-client.yaml` работает только через GET и использует этот `lt_`, а не `LOFT_TOKEN`. `LOFT_TOKEN` в `loft.env` остаётся секретом ноды (mask, seed). Чтобы сохранить старый оверлей на одном токене, пропишите `auth shared enable` в `node.conf`, пока `members.keys` пуст. После обновления бинарника нода на общем токене без этой строки не пустит клиентов, пока вы не добавите строку или не выдадите ключи. `ws`, `h2`, `quic`, `h3` включаются явным перечислением в `fronts`.

---

## 2. Установщик из tarball

На машине сборки:

```bash
make dist-node
```

Команда создаёт `dist/node/linux-amd64/` и `dist/node/linux-arm64/` (tarball, распакованный установщик, `.deb`) плюс `dist/node/SHA256SUMS`. Состав: [DIST_ru.md](DIST_ru.md).

На хосте Debian:

```bash
tar -C /tmp -xzf loft-node-*-linux-amd64.tar.gz
cd /tmp/loft-node-*-linux-amd64
sudo ./install.sh
```

Полезные флаги:

| Флаг | Смысл |
|------|---------|
| `--dry-run` | напечатать шаги, ничего не меняя |
| `--unattended`, `-y` | без вопросов |
| `--role=controller` | по умолчанию |
| `--role=relay` | TLS-край; нужны `--controller=` и `--id=` |
| `--advertise=https://host` | публичный URL в roster |
| `--listen=:443` | адрес для прослушивания |
| `--decoy=host:443` | сплайс origin |
| `--mask` | маскировка чужим сертификатом (вместе с decoy) |
| `--cert=` `--key=` | готовые PEM; иначе самоподписанный |
| `--peers=hub` | политика оверлея (`hub` \| `mesh` \| `vlan`) |
| `--token=lt_…` | иначе генерируется |
| `--no-firewall` | не трогать ufw |
| `--uninstall` | снять службу, данные оставить |
| `--remove` | снять службу и стереть данные |

Повторный запуск заменяет бинарник и перезапускает unit. Токен, конфиг и сертификаты сохраняются.

Поддерживаются Debian 12 и 13, Ubuntu 22.04 / 24.04 / 26.04. На Debian 10/11 и Ubuntu 16/18/20 установщик откажется работать. Для прочих систем семейства Debian он спросит подтверждение.

---

## 3. Пакет Debian

```bash
make dist-node            # tarball + .deb для amd64 и arm64
make deb                  # только amd64 .deb, сначала нужен dist-debian
make deb ARCH=arm64
```

```bash
dpkg -i dist/node/linux-amd64/loft-node_*_amd64.deb
# править /etc/loft/node.conf
systemctl enable --now loft-node
```

`postinst` создаёт пользователя, токен, самоподписанный сертификат и конфиг по умолчанию. Unit при этом не запускается. `apt purge loft-node` удаляет `/etc/loft` и `/var/lib/loft`.

---

## 4. После установки

`install.sh` пишет `/root/loft-client.yaml` (mode 0600) с токеном, advertise URL и `controller_pub`. Пока сертификат самоподписанный, `insecure_skip_verify` включён. Замените PEM на публичный сертификат и выключите это поле.

```bash
journalctl -u loft-node -f
loft-node --version
loft-node -dir /var/lib/loft -print-pub
```

Откройте 443/tcp и 443/udp на всём пути до хоста. QUIC и HTTP/3 работают поверх UDP.

---

## 5. Relay

На крае:

```bash
sudo ./install.sh --role=relay \
  --id=ru1 \
  --controller=wss://controller.example/api/v1/sync \
  --advertise=https://203.0.113.10
```

Если `enrol: list`, тот же `id` должен быть указан в `relays:` в YAML контроллера.
