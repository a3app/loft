# loft на Sailfish OS

[English](sailfish.md) · **Русский** · [中文](sailfish_zh.md)

Как loft работает на телефоне с Sailfish OS. Каталог: [clients/Sailfish/](../clients/Sailfish/). План: [clients/Sailfish/PLAN.md](../clients/Sailfish/PLAN.md). Макет: [clients/Sailfish/design/index.html](../clients/Sailfish/design/index.html). Протокол: [protocol_ru.md](protocol_ru.md). URI импорта: [share_ru.md](share_ru.md).

ОС Аврора — пакет OMP этого клиента: [aurora_ru.md](aurora_ru.md), [`clients/Sailfish/aurora/`](../clients/Sailfish/aurora/).

GUI в Dist ещё нет. Этот файл — спецификация стыковки.

---

## 1. Три части

Sailfish решает **когда** есть tun (строка VPN ConnMan, одна сразу). `loft-front` решает **как** сессия выглядит в канале. Приложение QML пишет YAML и просит ConnMan стартовать.

```
приложения
  → tun у connman-vpnd
      → плагин ConnMan loft  (harbour-loft или app.loft.aurora)
          → loft-front
              → loft  Holder (get|ws|h2|quic|h3)
              → SSTP  TLS + PPP, IP из IPCP
              → VLESS TCP+TLS
                  → IP сервера мимо tun
```

Контейнер протокол не говорит. Он пишет `client.yaml` и просит ConnMan стартовать тип `loft` (D-Bus `net.connman.vpn`). Если плагина нет — запускает `loft-front` с SOCKS на loopback. loft, SSTP и VLESS — исходящие одной строки. OpenVPN остаётся штатным типом Настроек.

Помощник: `loft-connman` exec `loft-front -tun NAME -tun-noexec`. ConnMan ставит адрес, DNS и маршруты из скрипта notify.

Bind `clients/mobile` нет. Sailfish — GNU/Linux; помощник — тот же `loft-front`, что запускает macOS.

## 2. TUN

После живой сессии (Status loft или IPCP SSTP) плагин говорит ConnMan:

| Status | ConnMan |
|--------|---------|
| `ip` / префикс | адрес tun |
| `gateway` оверлея | remote для маршрута по умолчанию |
| `mtu` | MTU tun |
| `dns` | серверы имён |
| `ip6` / `gateway6` | если есть |

IP фронтов **должны** сидеть мимо tun, иначе сессия зациклится. Списки / зоны → маршруты ConnMan (`via` / `direct`). `drop` — отброс в помощнике, не маршрут ОС.

Удержание держит default занятым, если сессия умерла: у телефона нет пути LTE/Wi-Fi, пока снова Подключить или сброс в Настройках. Копирайт: *маршрут* / *прямой интернет*.

## 3. Смена пути

Wi-Fi → LTE (и обратно) — новый default. loft-front уже закрывает живой фронт и поднимает свежий `0x02` с тем же `instance_id` (IP оверлея тот же). SSTP перенабирает TLS+PPP. Внутренний TCP всё равно RST.

Тот же контракт, что macOS / CLI. Это не новая cap.

## 4. Идентификаторы

| | Sailfish | Аврора |
|--|----------|--------|
| Приложение | `harbour-loft` | `app.loft.aurora` |
| Помощник | `loft-front` в RPM | тот же |
| VPN-клиент | `loft-connman` (задача ConnMan) | тот же |
| Тип ConnMan | `loft` | `loft` |
| Имя строки | **Loft** | **Loft** |
| Схема URL | `loft` | `loft` |
| SDK | Sailfish SDK (`sfdk`) | Аврора SDK (`mb2`) |
| CMake | `-DLOFT_OS=sailfish` | `-DLOFT_OS=aurora` |

Движок (`loft-front`): **linux/arm64**. GUI: Qt 5 / QML Silica. Если glibc не примет статический бинарник — пересборка в Build Engine этой ОС.

## 5. Чем это не является

SOCKS5 на `127.0.0.1` — не вход, которым пользуются чужие приложения. Хост QML поднимает его только если плагина ConnMan нет.

macOS `loft-priv` в RPM не кладём. TUN создаёт ConnMan.
