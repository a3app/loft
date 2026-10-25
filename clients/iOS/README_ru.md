# loft · iOS

[English](README.md) · **Русский** · [中文](README_zh.md)

Клиент iOS: один **Packet Tunnel**, который система видит как Personal VPN. Контейнер держит профили и лампу; Network Extension ведёт сессию Go (loft, SSTP или VLESS).

Протокол: [docs/protocol_ru.md](../../docs/protocol_ru.md). Интеграция: [docs/ios.md](../../docs/ios.md). URI импорта: [docs/share_ru.md](../../docs/share_ru.md). План: [PLAN.md](PLAN.md). UX: [design/DESIGN_ru.md](design/DESIGN_ru.md). Макет: [design/index.html](design/index.html).

Home и Settings на SwiftUI (`HomeView`, `SettingsView`, Xcode Canvas `#Preview`) и Packet Tunnel в этом дереве. Сначала bind движка: `make -C ../mobile ios`. YAML-хост остаётся в `ContentView`.

## Рамки

| На телефоне | Роль |
|-------------|------|
| loft (get / ws / h2 / quic / h3 / l3) | оверлей loft. Один `NEPacketTunnelProvider`. |
| SSTP | Тот же туннель. TLS + PPP, IP из IPCP. Go `internal/sstp`. Импорт YAML `type: sstp` или `sstp://`. |
| VLESS | Тот же туннель, когда loft режут. Импорт — QR `vless://` (камера или файл). |
| SOCKS5 как вход продукта | На iOS нет. Чужие приложения не ходят в локальный SOCKS. Вход — системный VPN. |
| IKEv2, WireGuard в этом приложении | Нет. IKEv2 остаётся Настройки → VPN. |

iOS держит **один** Personal VPN. Несколько профилей loft — переключение, не параллельные TUN.

Расширение должно вынести IP фронтов из туннеля, чтобы сессия не зациклилась.

## Раскладка

```
clients/iOS/
  README.md          этот файл
  PLAN.md            фазы сборки (Apple TV = фаза 6)
  design/            спецификация UX и HTML-макет
  App/               хост SwiftUI: HomeView + SettingsView (Canvas Preview) + YAML ContentView
  PacketTunnel/      Network Extension → clients/mobile
  Loft.xcodeproj
  ../mobile/         общий движок Go (iOS + Android)
```

Сессия — [`clients/mobile`](../mobile/) (`internal/` + inbound-стек) в appex. Процесса `loft-front` нет. На macOS `loft-front` остаётся отдельным процессом; на iOS сессия в процессе расширения.

## Требования

| | Версия |
|--|--|
| Движок Go (`clients/mobile`) | **iOS 15+** (минимум Xcode 26 для загрузки в App Store) |
| Контейнер SwiftUI | **iOS 16+** (`NavigationStack`, макет) |
| Загрузка в Store | Xcode 26+, iOS 26 SDK. Deployment 15 — минимум toolchain. |

Apple Developer Program для entitlement Packet Tunnel. App group `group.app.loft` для профилей между приложением и appex. Архитектуры: arm64 на устройстве, arm64 симулятор.

## Сборка

Из корня репозитория (Xcode, SDK симулятора):

```bash
make ios                 # dist/clients/iOS/loft.app
make -C clients/iOS run  # поставить и запустить на loft-ios
```

Промежуточное — в `dist/clients/iOS/`, не в `clients/iOS/`. Unsigned Debug для симулятора. IPA на устройство / TestFlight — позже.
