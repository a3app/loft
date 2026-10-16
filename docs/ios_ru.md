# loft на iOS

[English](ios.md) · **Русский**

Как loft сидит на iPhone, позже Apple TV (tvOS 17+). Каталог: [clients/iOS/](../clients/iOS/). План: [clients/iOS/PLAN_ru.md](../clients/iOS/PLAN_ru.md) (Apple TV — фаза 6). Макет: [clients/iOS/design/index.html](../clients/iOS/design/index.html). Протокол: [protocol_ru.md](protocol_ru.md). URI шаринга: [share_ru.md](share_ru.md).

Приложения ещё нет. Этот файл — договор.

---

## 1. Два процесса

iOS решает **когда** туннель можно (Personal VPN, один сразу, On Demand). Движок Go решает **как** туннель выглядит в канале.

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

Контейнер (`app.loft.ios`) канал не поднимает. Он пишет YAML в App Group и вызывает `startVPNTunnel`. loft, SSTP и VLESS — исходящие того же провайдера. IKEv2 остаётся штатным клиентом Настроек.

## 2. Packet Tunnel

`NEPacketTunnelNetworkSettings` после живой сессии (Status loft или IPCP SSTP):

| Status | Настройки |
|--------|-----------|
| `ip` / префикс | `NEIPv4Settings.addresses` |
| шлюз оверлея | remote address маршрута по умолчанию |
| `mtu` | `tunnelOverheadBytes` / MTU |
| `dns` | `NEDNSSettings` |
| `ip6` / `gateway6` | `NEIPv6Settings`, если есть |

IP фронтов ДОЛЖНЫ быть в `excludedRoutes`, чтобы сессия не зациклилась. Списки протокола — в `includedRoutes` (`via`) и `excludedRoutes` (`direct`). `drop` — отброс в провайдере, не маршрут OS.

Удержание — `includeAllNetworks`: если сессия умерла, у телефона нет пути сотовой/Wi-Fi, пока снова Подключить или сброс в Настройках.

## 3. Смена пути

Wi-Fi → LTE (и обратно) — новый путь по умолчанию. Адаптер OS вызывает `NotifyPathChange`. loft: закрыть живой фронт и продолжить билетом `0x03` и тем же `instance_id` (IP оверлея тот же). SSTP: повторный набор TLS+PPP; настройки применить снова, если IPCP выдал новый IP. Внутренний TCP всё равно RST.

Это тот же договор, что смена пути на macOS / CLI. Новой возможности протокола нет. `WatchPath` внутри Packet Tunnel не крутить (egress стал бы оверлеем).

## 4. Идентификаторы

| | |
|--|--|
| Приложение | `app.loft.ios` |
| Packet Tunnel | `app.loft.ios.packet-tunnel` |
| App Group | `group.app.loft` |
| Тип VPN | Personal VPN (Packet Tunnel) |

Портал Apple Developer: Network Extensions → Packet Tunnel, App Groups. TestFlight — те же id.

Движок (appex + `clients/mobile`): **iOS 15+**. Контейнер SwiftUI: **iOS 16+**. Загрузка в App Store: Xcode 26 и iOS 26 SDK; 15 — самый низкий deployment, который этот toolchain ещё собирает.

Схема URL `loft` (импорт `loft://…`). Камера для QR. Фото / Файлы для картинки или `.loft` (одно или несколько подключений). Детектор: [share_ru.md](share_ru.md).

## 5. Чем это не является

SOCKS5 на `127.0.0.1` — не вход. Чужие приложения iOS нельзя направить в локальный SOCKS без системного VPN.

macOS `loft-front` + `loft-priv` в IPA не входят.
