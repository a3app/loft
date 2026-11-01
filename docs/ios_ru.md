# loft на iOS

[English](ios.md) · **Русский** · [中文](ios_zh.md)

Как loft работает на iPhone, а позже и на Apple TV (tvOS 17+). Каталог: [clients/iOS/](../clients/iOS/). План: [clients/iOS/PLAN.md](../clients/iOS/PLAN.md) (Apple TV — фаза 6). Макет: [clients/iOS/design/index.html](../clients/iOS/design/index.html). Протокол: [protocol_ru.md](protocol_ru.md). URI импорта: [share_ru.md](share_ru.md).

Контейнер: [`clients/iOS/`](../clients/iOS/). `make ios` собирает macOS-приложение `dist/clients/iOS/loft.app` (по двойному клику запускается Simulator `loft-ios`) и неподписанный `iphonesimulator/loft.app`. Для IPA на устройство нужен entitlement Packet Tunnel.

---

## 1. Два процесса

iOS решает, **когда** туннель разрешён (Personal VPN, активен только один, On Demand). Движок Go решает, **как** туннель выглядит в канале.

```
приложение (Safari, Почта, …)
  → UTUN у Network Extension
      → NEPacketTunnelProvider  (appex app.loft.ios.packet-tunnel)
          → clients/mobile
              → loft  Holder (get|ws|h2|quic|h3)
              → SSTP  TLS + PPP, IP из IPCP
              → VLESS TCP+TLS
                  → IP сервера вне туннеля
```

Контейнер (`app.loft.ios`) сам по протоколу не общается. Он записывает YAML в App Group и вызывает `startVPNTunnel`. loft, SSTP и VLESS — исходящие того же провайдера. IKEv2 остаётся штатным клиентом Настроек.

## 2. Packet Tunnel

`NEPacketTunnelNetworkSettings` после установления сессии (Status от loft или IPCP от SSTP):

| Status | Настройки |
|--------|-----------|
| `ip` / префикс | `NEIPv4Settings.addresses` |
| шлюз оверлея | remote address маршрута по умолчанию |
| `mtu` | `tunnelOverheadBytes` / MTU |
| `dns` | `NEDNSSettings` |
| `ip6` / `gateway6` | `NEIPv6Settings`, если есть |

IP фронтов **должны** быть в `excludedRoutes`, чтобы сессия не зациклилась. Списки протокола отображаются в `includedRoutes` (`via`) и `excludedRoutes` (`direct`). `drop` — это отброс пакетов в провайдере, а не маршрут ОС.

Удержание реализовано через `includeAllNetworks`: если сессия оборвалась, у телефона нет выхода через сотовую сеть или Wi-Fi, пока пользователь снова не нажмёт «Подключить» или не сбросит настройку в Настройках.

## 3. Смена пути

Переход Wi-Fi → LTE (и обратно) означает новый путь по умолчанию. Адаптер ОС вызывает `NotifyPathChange`. loft закрывает живой фронт и возобновляет сессию свежим `0x02` с тем же `instance_id` (IP оверлея сохраняется). SSTP заново устанавливает TLS+PPP и, если IPCP выдал новый IP, повторно применяет настройки. Внутренние TCP-соединения всё равно получают RST.

Поведение то же, что при смене пути на macOS / CLI. Новой возможности протокола нет. `WatchPath` внутри Packet Tunnel не используется (иначе egress шёл бы через оверлей).

## 4. Идентификаторы

| | |
|--|--|
| Приложение | `app.loft.ios` |
| Packet Tunnel | `app.loft.ios.packet-tunnel` |
| App Group | `group.app.loft` |
| Тип VPN | Personal VPN (Packet Tunnel) |

Портал Apple Developer: Network Extensions → Packet Tunnel, App Groups. TestFlight использует те же id.

Движок (appex + `clients/mobile`): **iOS 15+**. Контейнер SwiftUI: **iOS 16+**. Загрузка в App Store: Xcode 26 и iOS 26 SDK; 15 — самый низкий deployment target, который этот toolchain ещё собирает.

Схема URL `loft` (импорт `loft://…`). Камера — для QR. Фото / Файлы — для картинки или `.loft` (одно или несколько подключений). Детектор: [share_ru.md](share_ru.md).

## 5. Чем это не является

SOCKS5 на `127.0.0.1` — не вход. Чужие приложения iOS нельзя направить в локальный SOCKS без системного VPN.

`loft-front` и `loft-priv` из macOS в IPA не входят.
