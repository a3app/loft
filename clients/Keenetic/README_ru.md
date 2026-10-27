# loft · KeeneticOS

[English](README.md) · **Русский** · [中文](README_zh.md)

Клиент для Entware: по интерфейсу **OpkgTun** на подключение, их KeeneticOS использует в приоритетах подключений и политиках маршрутизации. Настройки доступны на LAN-порту, вход — через сессию веб-админки Keenetic.

Протокол: [docs/protocol_ru.md](../../docs/protocol_ru.md). Интеграция: [docs/keenetic_ru.md](../../docs/keenetic_ru.md). План: [PLAN.md](PLAN.md). UX: [design/DESIGN_ru.md](design/DESIGN_ru.md). Макет: [design/index.html](design/index.html).

Бинарник — `loft-keenetic`. Он отдаёт страницу в LAN и на каждое включённое подключение запускает себя же с `-session`. `clients/go/lanui` остаётся страницей MikroTik.

## Рамки

| На роутере | Роль |
|------------|------|
| loft (get / ws / h2 / quic / h3 / l3) | оверлей loft. Свой OpkgTun на каждое подключение. |
| VLESS, Trojan | Отдельные подключения, у каждого свой OpkgTun. |
| SSTP, WireGuard, OpenVPN, IPsec, OpenConnect | Штатные средства KeeneticOS, не этот пакет. |

Клиент не ставит маршрут по умолчанию: таблицей владеет NDM. До `interface OpkgTunN up` адреса фронтов прибиваются командой `ip route <ipv4>/32 <ISP>`.

## Сборка

KeeneticOS **5.0+** (OpkgTun в политиках). Компоненты: OPKG и TUN/TAP. Entware в NAND или на USB с EXT4.

```bash
make -C clients/Keenetic test
make -C clients/Keenetic ipk       # aarch64: Giga, Peak, Hopper
make -C clients/Keenetic ipk-all   # aarch64, armv7 soft-float, mipsel soft-float
```

На роутере: `opkg install loft-keenetic-<version>-<arch>.ipk`. Страница: `http://<lan>:8090/`. Образец конфига — `/opt/etc/loft/client.yaml` (`check_updates: true`). Поле `enabled`, если его нет в YAML, значит выключено.

`prerm` снимает OpkgTun0…OpkgTun15 только при удалении пакета. При обновлении процесс останавливается, интерфейсы остаются, приоритеты на них сохраняются.

Панель ставит более новый `.ipk` из последнего релиза `a3app/loft`, когда в нём есть этот файл и его `.sha256`. Собирает Makefile. Состав Dist не менялся.

## Состав

```
clients/Keenetic/
  cmd/loft-keenetic   родитель и дочерний процесс -session
  web/index.html      страница LAN (та, что в пакете)
  pack/               S99loft, prerm, postinst, образец, mkipk
  design/             UX и макет
```
