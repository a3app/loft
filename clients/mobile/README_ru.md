# loft · мобильный движок

[English](README.md) · **Русский**

Общая сессия Go для **iOS Packet Tunnel** и **Android VpnService**. Позже тот же bind растит flavor Android TV / Google TV (тот же AAR) и слайс tvOS (Apple TV, фаза 6 плана). Без GUI. Без `loft-front`. Протокол 1.0 не меняется.

Каталог iOS: [../iOS/](../iOS/). Каталог Android: [../Android/](../Android/). URI шаринга: [docs/share_ru.md](../../docs/share_ru.md).

## Исходящие

Один процесс, одна строка системного VPN. YAML `outbounds[].type` выбирает путь:

| type | Провод | Внутренний IP | Пакеты |
|------|--------|---------------|--------|
| `get` `ws` `h2` `quic` `h3` | loft | оверлей из Status | gVisor → `Holder.Open` |
| `sstp` | TLS + PPP (`internal/sstp`) | IPCP | L3-памп в `sstp.Session` |
| `vless` | VLESS TCP+TLS / REALITY / XHTTP (`internal/vless`) | синтетический / стек | gVisor → набор VLESS |
| `trojan` | Trojan TCP+TLS / REALITY / XHTTP (`internal/trojan`) | синтетический / стек | gVisor → набор Trojan |

IKEv2 остаётся штатным клиентом OS. WireGuard — не этот пакет.

## API

```
NewEngine() *Engine
Start(yaml string) error
Stop()
InjectPacket([]byte) error  // IP от OS
PullPacket() []byte         // IP в OS; nil если пусто
SetSink(PacketSink)         // вместо PullPacket
NotifyPathChange()          // Wi-Fi ↔ LTE от OS
Snapshot() *Snapshot        // также Status()
ParseShare(s) (yaml, error) // loft://, sstp://, vless://, trojan://, YAML, .loft, .vpn
```

`WatchPath` внутри полного туннеля не крутить (egress стал бы оверлеем). Адаптер OS вызывает `NotifyPathChange`. Смена пути loft — билет `0x03` и тот же `instance_id`. SSTP/VLESS/Trojan — повторный набор; IPCP может выдать новый IP, адаптер OS заново ставит настройки туннеля.

IP фронта / сервера SSTP / VLESS — в `Snapshot.FrontIPs` (через запятую), адаптер исключает их из туннеля (без петли).

## Bind

```bash
make ios        # build/Mobile.xcframework   (Xcode, iOS 15)
make android    # build/engine.aar           (NDK, API 24, страницы 16 КиБ)
```

Из корня репо: `make mobile-ios` / `make mobile-android`.

Swift `PacketTunnelProvider` и Java `VpnService` качают пакеты и ставят маршруты/DNS из `Snapshot()`. Android зовёт `SetProtector` (`VpnService.protect`). iOS исключает IP фронтов в `NEPacketTunnelNetworkSettings`.

## Полы OS

| | Движок (этот пакет) | GUI | Загрузка в магазин |
|--|--|--|--|
| iOS | **15+** | **16+** | Xcode 26, iOS 26 SDK |
| Android | **API 24** (7.0) | **API 26** (8.0) | `targetSdk` **36** (Android 16) |

Play не задаёт `minSdk`. Новые приложения для телефона с 31 авг 2026 **таргетят** API 36. Always-on VPN — API 24. Каналы уведомлений (foreground VpnService) — API 26. Нативный `.so` для arm64 должен быть с страницами 16 КиБ (правило Play для `targetSdk` 35+).

Исходники: `engine.go` (сессия), `packets.go` (IP-очередь), `share.go` (QR / вставка), `yaml.go`, `split.go` (хосты без петли). Биндить этот пакет; GUI отсюда не поставлять.
