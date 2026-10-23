# loft · мобильный движок

[English](README.md) · **Русский** · [中文](README_zh.md)

Общая сессия Go для **iOS Packet Tunnel** и **Android VpnService** (flavor телефона и TV, тот же AAR). Позже тот же bind может дать слайс tvOS (Apple TV, iOS PLAN фаза 6). Без GUI. Без `loft-front`. Протокол 1.0 не меняется.

Каталог iOS: [../iOS/](../iOS/). Каталог Android: [../Android/](../Android/). URI импорта: [docs/share_ru.md](../../docs/share_ru.md).

## Исходящие

Один процесс, одна строка системного VPN. YAML `outbounds[].type` выбирает путь:

| type | Протокол | Внутренний IP | Пакеты |
|------|--------|---------------|--------|
| `get` `ws` `h2` `quic` `h3` | loft mux | оверлей из Status | gVisor → `Holder.Open` |
| `l3` | loft IP | оверлей из Status | IP поверх H2 CONNECT |
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
Snapshot() *Snapshot        // также Status(); IP/IP6, Prefix/Prefix6, Routes, FrontIPs
ParseShare(s) (yaml, error) // loft://, sstp://, vless://, trojan://, YAML, .loft, .vpn
ParseShareAll(s) (yaml, error) // то же, каждое подключение через ---
```

`WatchPath` внутри полного туннеля не запускать (egress стал бы оверлеем). Адаптер OS вызывает `NotifyPathChange`. GET пересобирает пул HTTP/2 (тот же sid). Остальные loft-фронты — Park, затем свежий `0x02` и тот же `instance_id`. Лишние mux-фронты садятся на живой GET через `0x04` (пути из Status). SSTP/VLESS/Trojan — повторный набор; IPCP может выдать новый IP, адаптер OS заново ставит настройки туннеля. GET `inflight` по умолчанию 8, как у loft-front.

IP фронта / сервера SSTP / VLESS — в `Snapshot.FrontIPs` (через запятую), адаптер исключает их из туннеля (без петли).

## Bind

```bash
make ios        # build/Mobile.xcframework   (Xcode, iOS 15)
make android    # build/engine.aar           (NDK, API 24, страницы 16 КиБ)
```

Из корня репо: `make mobile-ios` / `make mobile-android`.

Swift `PacketTunnelProvider` и Java `VpnService` качают пакеты и ставят маршруты/DNS из `Snapshot()`. Android зовёт `SetProtector` (`VpnService.protect`). iOS исключает IP фронтов в `NEPacketTunnelNetworkSettings`.

## Минимальные версии ОС

| | Движок (этот пакет) | GUI | Загрузка в магазин |
|--|--|--|--|
| iOS | **15+** | **16+** | Xcode 26, iOS 26 SDK |
| Android | **API 24** (7.0) | **API 26** (8.0) | `targetSdk` **36** (Android 16) |

Play не задаёт `minSdk`. Новые приложения для телефона с 31 авг 2026 **таргетят** API 36. Always-on VPN — API 24. Каналы уведомлений (foreground VpnService) — API 26. Нативный `.so` для arm64 должен быть с страницами 16 КиБ (правило Play для `targetSdk` 35+).

Исходники: `engine.go` (сессия), `packets.go` (IP-очередь), `share.go` (QR / вставка), `yaml.go`, `split.go` (хосты без петли). Биндить этот пакет; GUI отсюда не поставлять.
