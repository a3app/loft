# loft · мобильный движок

[English](README.md) · **Русский** · [中文](README_zh.md)

Общая сессия Go для **iOS Packet Tunnel** и **Android VpnService** (flavor телефона и TV, тот же AAR). Позже тот же bind может добавить слайс tvOS (Apple TV, фаза 6 в iOS PLAN). Без GUI. Без `loft-front`. Протокол 1.0 не меняется.

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

IKEv2 остаётся штатным клиентом ОС. WireGuard к этому пакету не относится.

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

`WatchPath` внутри полного туннеля не используется (иначе egress шёл бы через оверлей). Адаптер ОС вызывает `NotifyPathChange`. GET пересобирает пул HTTP/2 (sid тот же). Остальные loft-фронты переходят в Park и затем возобновляются свежим `0x02` с тем же `instance_id`. Лишние mux-фронты присоединяются к живому GET через `0x04` (пути из Status). SSTP/VLESS/Trojan переподключаются; IPCP может выдать новый IP, и тогда адаптер ОС заново применяет настройки туннеля. По умолчанию `inflight` у GET равен 8, как у loft-front.

IP фронтов и серверов SSTP / VLESS перечислены в `Snapshot.FrontIPs` (через запятую), чтобы адаптер мог исключить их из туннеля (без петли).

## Bind

```bash
make ios        # build/Mobile.xcframework   (Xcode, iOS 15)
make android    # build/engine.aar           (NDK, API 24, страницы 16 КиБ)
```

Из корня репозитория: `make mobile-ios` / `make mobile-android`.

Swift `PacketTunnelProvider` и Java `VpnService` перекачивают пакеты и устанавливают маршруты/DNS из `Snapshot()`. Android вызывает `SetProtector` (`VpnService.protect`). iOS исключает IP фронтов в `NEPacketTunnelNetworkSettings`.

## Минимальные версии ОС

| | Движок (этот пакет) | GUI | Загрузка в магазин |
|--|--|--|--|
| iOS | **15+** | **16+** | Xcode 26, iOS 26 SDK |
| Android | **API 24** (7.0) | **API 26** (8.0) | `targetSdk` **36** (Android 16) |

Play не задаёт требований к `minSdk`. Новые приложения для телефонов и планшетов с 31 авг 2026 должны **таргетировать** API 36. Always-on VPN появился в API 24, каналы уведомлений (foreground VpnService) — в API 26. Нативный `.so` для arm64 должен поддерживать страницы 16 КиБ (правило Play для `targetSdk` 35+).

Исходники: `engine.go` (сессия), `packets.go` (IP-очередь), `share.go` (QR / вставка), `yaml.go`, `split.go` (хосты без петли). Bind делается из этого пакета; GUI отсюда не поставлять.
