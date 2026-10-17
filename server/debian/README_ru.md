# loft-node на Debian

[English](README.md) · **Русский**

ПО 1.0.5. Протокол 1.0. Спецификация: [docs/protocol_ru.md](../../docs/protocol_ru.md). Эксплуатация: [docs/guide_ru.md](../../docs/guide_ru.md).

Этот каталог — Debian-раскладка ноды: POSIX-установщик, unit systemd, sysctl, конфиг команды и `.deb`.

---

## 1. Раскладка на машине

| Путь | Роль |
|------|------|
| `/usr/local/bin/loft-node` | бинарник (установщик из tarball) |
| `/usr/bin/loft-node` | бинарник (`.deb`) |
| `/etc/loft/node.conf` | роль, фронты, decoy, advertise |
| `/etc/loft/loft.env` | `LOFT_TOKEN` (mode 0640, группа `loft`) |
| `/etc/loft/tls/fullchain.pem` | публичный сертификат |
| `/etc/loft/tls/privkey.pem` | закрытый ключ |
| `/var/lib/loft/network.conf` | CIDR оверлея, члены, пиры |
| `/var/lib/loft/members.keys` | ключи членов открытым текстом (mode 0600), выдаёт оператор |
| `/var/lib/loft/controller.seed` | Ed25519 seed, пишется при первом старте |
| `/etc/systemd/system/loft-node.service` | unit |
| `/etc/sysctl.d/99-loft.conf` | UDP-буферы для QUIC; ping_group_range для ICMP в интернет |

Процесс идёт от пользователя `loft`. TCP и UDP 443 биндит с `CAP_NET_BIND_SERVICE`. Токен на командной строке не бывает.

---

## 2. Установщик из tarball

На машине сборки:

```bash
make dist-node
```

Пишет `dist/node/linux-amd64/` и `dist/node/linux-arm64/` (tarball, распакованный установщик, `.deb`) плюс `dist/node/SHA256SUMS`. Раскладка: [DIST_ru.md](DIST_ru.md).

На хосте Debian:

```bash
tar -C /tmp -xzf loft-node-*-linux-amd64.tar.gz
cd /tmp/loft-node-*-linux-amd64
sudo ./install.sh
```

Полезные флаги:

| Флаг | Смысл |
|------|---------|
| `--dry-run` | печать шагов, ничего не менять |
| `--unattended`, `-y` | без вопросов |
| `--role=controller` | по умолчанию |
| `--role=relay` | TLS-край; нужны `--controller=` и `--id=` |
| `--advertise=https://host` | публичный URL в roster |
| `--listen=:443` | адрес бинда |
| `--decoy=host:443` | сплайс origin |
| `--mask` | маска чужим сертификатом (с decoy) |
| `--cert=` `--key=` | готовые PEM; иначе самоподписанный |
| `--peers=hub` | политика оверлея (`hub` \| `mesh` \| `vlan`) |
| `--token=lt_…` | иначе генерируется |
| `--no-firewall` | не трогать ufw |
| `--uninstall` | снять службу, данные оставить |
| `--remove` | снять службу и стереть данные |

Повторный запуск меняет бинарник и перезапускает unit. Токен, конфиг и сертификаты остаются.

Принимаются Debian 12 и 13, Ubuntu 22.04 / 24.04 / 26.04. Debian 10/11 и Ubuntu 16/18/20 отклоняются. Прочие Debian-семейства спрашиваются.

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

`postinst` создаёт пользователя, токен, самоподписанный сертификат и дефолтный конфиг. Unit не стартует. `apt purge loft-node` удаляет `/etc/loft` и `/var/lib/loft`.

---

## 4. После установки

`install.sh` пишет `/root/loft-client.yaml` (mode 0600) с токеном, advertise URL и `controller_pub`. `insecure_skip_verify` включён, пока сертификат самоподписанный. Замените PEM на публичный сертификат и выключите поле.

```bash
journalctl -u loft-node -f
loft-node --version
loft-node -dir /var/lib/loft -print-pub
```

На пути перед хостом откройте 443/tcp и 443/udp. QUIC и HTTP/3 идут по UDP.

---

## 5. Relay

На крае:

```bash
sudo ./install.sh --role=relay \
  --id=ru1 \
  --controller=wss://controller.example/api/v1/sync \
  --advertise=https://203.0.113.10
```

В YAML контроллера тот же `id` должен быть в `relays:`, если `enrol: list`.
