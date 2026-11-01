# loft на Sailfish OS

[English](sailfish.md) · **Русский** · [中文](sailfish_zh.md)

Как loft работает на телефоне с Sailfish OS. Каталог: [clients/Sailfish/](../clients/Sailfish/). План: [clients/Sailfish/PLAN.md](../clients/Sailfish/PLAN.md). Макет: [clients/Sailfish/design/index.html](../clients/Sailfish/design/index.html). Протокол: [protocol_ru.md](protocol_ru.md). URI импорта: [share_ru.md](share_ru.md).

Для ОС Аврора этот клиент упаковывается как пакет OMP: [aurora_ru.md](aurora_ru.md), [`clients/Sailfish/aurora/`](../clients/Sailfish/aurora/).

GUI в Dist пока не собирается. Этот файл — спецификация интеграции.

---

## 1. Три части

Sailfish решает, **когда** существует tun (строка VPN в ConnMan, активна только одна). `loft-front` решает, **как** сессия выглядит в канале. Приложение QML записывает YAML и просит ConnMan запустить подключение.

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

Контейнер сам по протоколу не общается. Он записывает `client.yaml` и просит ConnMan запустить тип `loft` (D-Bus `net.connman.vpn`). Если плагина нет, он запускает `loft-front` с SOCKS на loopback. loft, SSTP и VLESS — исходящие одной и той же строки. OpenVPN остаётся штатным типом Настроек.

Помощник: `loft-connman` запускает через exec `loft-front -tun NAME -tun-noexec`. ConnMan применяет адрес, DNS и маршруты из скрипта notify.

Bind из `clients/mobile` здесь не используется. Sailfish — это GNU/Linux, и помощником служит тот же `loft-front`, который запускает macOS.

## 2. TUN

Когда сессия установлена (Status от loft или IPCP от SSTP), плагин передаёт ConnMan:

| Status | ConnMan |
|--------|---------|
| `ip` / префикс | адрес tun |
| `gateway` оверлея | remote для маршрута по умолчанию |
| `mtu` | MTU tun |
| `dns` | серверы имён |
| `ip6` / `gateway6` | если есть |

IP фронтов **должны** идти мимо tun, иначе сессия зациклится. Списки и зоны протокола превращаются в маршруты ConnMan (`via` / `direct`). `drop` — это отброс пакетов в помощнике, а не маршрут ОС.

Удержание оставляет маршрут по умолчанию занятым, даже если сессия оборвалась: у телефона нет выхода через LTE/Wi-Fi, пока пользователь снова не нажмёт «Подключить» или не сбросит настройку в Настройках. В текстах интерфейса: *маршрут* / *прямой интернет*.

## 3. Смена пути

Переход Wi-Fi → LTE (и обратно) означает новый путь по умолчанию. loft-front уже умеет закрывать живой фронт и возобновлять сессию свежим `0x02` с тем же `instance_id` (IP оверлея сохраняется). SSTP заново устанавливает TLS+PPP. Внутренние TCP-соединения всё равно получают RST.

Контракт тот же, что на macOS / CLI. Новой cap здесь нет.

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

Движок (`loft-front`): **linux/arm64**. GUI: Qt 5 / QML Silica. Если glibc не принимает статический бинарник, его пересобирают в Build Engine соответствующей ОС.

## 5. Чем это не является

SOCKS5 на `127.0.0.1` — не вход, которым пользуются чужие приложения. Хост QML поднимает его, только если плагина ConnMan нет.

`loft-priv` из macOS в RPM не входит. TUN создаёт ConnMan.
