# loft на ОС Аврора

[English](aurora.md) · **Русский** · [中文](aurora_zh.md)

Как loft работает на телефоне с ОС Аврора. Каталог: [clients/Aurora/](../clients/Aurora/). План: [clients/Aurora/PLAN_ru.md](../clients/Aurora/PLAN_ru.md). Макет: [clients/Aurora/design/index.html](../clients/Aurora/design/index.html). Протокол: [protocol_ru.md](protocol_ru.md). URI импорта: [share_ru.md](share_ru.md).

GUI в Dist ещё нет. Этот файл — спецификация стыковки.

---

## 1. Три части

Аврора решает **когда** есть tun (строка VPN ConnMan, одна сразу). `loft-front` решает **как** сессия выглядит в канале. Приложение QML пишет YAML и просит ConnMan стартовать.

```
приложения
  → tun у connman-vpnd
      → плагин ConnMan loft  (app.loft.aurora)
          → loft-front
              → loft  Holder (get|ws|h2|quic|h3)
              → SSTP  TLS + PPP, IP из IPCP
              → VLESS TCP+TLS
                  → IP сервера мимо tun
```

Контейнер (`app.loft.aurora`) протокол не говорит. Он пишет `client.yaml` и просит ConnMan стартовать тип `loft` (D-Bus `net.connman.vpn`). Если плагина нет — запускает `loft-front` с SOCKS на loopback. loft, SSTP и VLESS — исходящие одной строки. OpenVPN остаётся штатным типом Настроек.

Помощник: `loft-connman` exec `loft-front -tun NAME -tun-noexec`. ConnMan ставит адрес, DNS и маршруты из скрипта notify.

Bind `clients/mobile` нет. Аврора — GNU/Linux; помощник — тот же `loft-front`, что запускает macOS.

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

| | |
|--|--|
| Приложение | `app.loft.aurora` |
| Помощник | `loft-front` в RPM |
| VPN-клиент | `loft-connman` (задача ConnMan) |
| Тип ConnMan | `loft` |
| Имя строки | **Loft** |
| Схема URL | `loft` |

Аврора SDK: приложение Qt/QML + привилегированный RPM плагина. Сначала sideload на устройство разработчика.

Движок (`loft-front`): **linux/arm64**, glibc Авроры 5. GUI: ОС Аврора **5.x**.

## 5. Чем это не является

SOCKS5 на `127.0.0.1` — не вход, которым пользуются чужие приложения Авроры. Хост QML поднимает его только если плагина ConnMan нет.

macOS `loft-priv` в RPM не кладём. TUN создаёт ConnMan.
