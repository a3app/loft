# План · loft на iOS

[English](PLAN.md) · **Русский**

ПО 1.0.0. Протокол 1.0. План версии не повышает.

## Цель

Клиент loft / SSTP / VLESS на iPhone как Personal VPN. Пользователь вставляет ключ, сканирует QR (`loft://`, `sstp://`, `vless://`) или открывает `.loft`, нажимает Подключить — трафик устройства идёт в сессию. На loft смена Wi-Fi / LTE сохраняет IP оверлея (билет `0x03`).

## Вне рамок

- Запуск `loft-front` как отдельного процесса (модель macOS). Appex *и есть* сессия.
- SOCKS5 как вход, которым пользуются чужие приложения.
- Несколько Packet Tunnel сразу (iOS держит один Personal VPN).
- IKEv2 / WireGuard внутри этого приложения. SSTP — Go в процессе (`internal/sstp`) на том же Packet Tunnel.
- Страница App Store в первой выкладке сборки (достаточно TestFlight).
- Виджеты, Пункт управления, Apple Watch, вёрстка «сначала iPad».
- Dist / IPA в GitHub Release `v*` до следующего тега.
- VIDAA / Tizen / webOS (Packet Tunnel нет). Эти телевизоры — Keenetic или HDMI-стик.

## Фазы

### 0. Каталог — это дерево

Документы, план, HTML-макет. URI импорта [docs/share_ru.md](../../docs/share_ru.md). Проекта Xcode нет.

### 1. Контейнер + пустой туннель

Xcode: приложение `app.loft.ios` + appex `app.loft.ios.packet-tunnel`. Написано (тонкий Connect / Disconnect, вставка YAML). Полный GUI по макету — позже.

### 2. Сессия в appex

`gomobile bind` [`clients/mobile`](../mobile/) (`make -C ../mobile ios` → `Mobile.xcframework`). PacketTunnelProvider качает `packetFlow` и ставит Snapshot. Написано.

- `packetFlow` ↔ стек TUN (IP оверлея, MTU, шлюз из Status).
- Хостовые маршруты IP исходящих вне туннеля (без петли). `RememberGateway` в Go уже есть.
- `NWPathMonitor` (или обновление пути провайдера): закрыть фронт, продолжить билетом `0x03` и тем же `instance_id`. SOCKS нет; туннель остаётся.
- DNS из Status `dns` через `NEDNSSettings`.
- Пересечение caps после Status, `select: auto`.

Проверка: ping шлюза оверлея; HTTPS в Safari телефона выходит через loft.

### 3. Профили и политика машины

App Group `group.app.loft`: YAML-профили, указатель активного, prefs. Ключ `lt_…` в Keychain. Импорт/экспорт `.loft` (тот же файл, что на macOS).

- Один активный профиль (лимит OS: один VPN).
- Удержание: `includeAllNetworks`, чтобы мёртвая сессия не светила IP сотовой/Wi-Fi. В копирайте *маршрут* / *прямой Интернет*, не «kill switch».
- Сплит: списки протокола → `includedRoutes` / `excludedRoutes` (`via` / `direct` / `drop`).
- Простой вид: Подключить/Отключить и имя. IP, фронт, байты — за переключателем.
- Сканер: камера QR + картинка/файл. Детектор: `loft://`, `sstp://`, `vless://`, YAML / `.loft` (одно или несколько подключений). Схема URL `loft`. Подтвердить, затем сохранить. QR `loft://` выбранного исходящего. Экспорт выбранного подключения как `.loft`. Список: свайп Править/Удалить, долгое нажатие, экспорт когда выбрано. Подпись тип · транспорт · хост.

### 4. Пакет

Подписанный IPA, TestFlight. Entitlement Network Extension на команде. GitHub Actions позже; не в первом теге. Ad-hoc на устройство разработчика достаточно, чтобы гонять самим.

### 5. VLESS и SSTP

Тот же процесс, тот же Packet Tunnel. Сканер уже принимает `vless://` и `sstp://` (камера, Фото, Файлы, вставка). Фаза 5 подключает эти профили к исходящему. loft остаётся по умолчанию. По-прежнему одна строка VPN.

SSTP — L3: IP-пакеты из `packetFlow` идут в `sstp.Session` (PPP/IPCP). gVisor на этом пути нет. Адрес/DNS/MTU — из IPCP, не из Status loft. Смена пути — повторный набор TLS+PPP (без билета `0x03`); настройки туннеля применить снова, если IPCP выдал новый IP.

### 6. Apple TV (tvOS 17+)

После GUI iPhone. Тот же Packet Tunnel (`NEPacketTunnelProvider`, TN3134: tvOS 17.0, per-app VPN нет). Сессия — Go в appex. Дерево позже: [`clients/tvOS/`](../tvOS/) (ещё нет). Официальный `gomobile bind` tvOS не режет; слайс `appletvos` (форк или свой CGO) в тот же xcframework, что iOS.

- Пол: **tvOS 17+** (Apple TV HD 2015 и все 4K, которые дотягивают до 17).
- 10-foot SwiftUI (фокус / Siri Remote). Контейнер iPhone — не UI телевизора.
- Камеры нет: обратный QR, клавиатура iPhone / Continuity, вставка. Детектор тот же (`loft://`, `.loft`).
- Петля: `excludedRoutes` на IP фронтов, как на iPhone. Смена пути: Ethernet ↔ Wi-Fi → `NotifyPathChange`, билет `0x03` на loft.
- RSS мерить на Apple TV HD (2 ГиБ), прежде чем обещать эту модель. Сначала 4K.

## Порядок работ в дереве Go

1. API [`clients/mobile`](../mobile/) — `Engine.Start` YAML, Stop, Inject/Pull, Snapshot, ParseShare. Без флагов CLI. Написано.
2. loft: билет `0x03` / `RememberGateway`. SSTP: повторный набор. Смена пути — `NotifyPathChange` от OS. Возможности протокола не добавлять.
3. Разбор `loft://`, `sstp://`, `vless://` (URI импорта, не протокол). Написано в `share.go`.
4. `loft-front` оставить для macOS. Не учить его iOS или tvOS.
5. Bind tvOS — поздний слайс [`clients/mobile`](../mobile/), после стабильного appex iPhone.

## Риски

| Риск | Смягчение |
|------|-----------|
| RSS Go в Network Extension на 50 МиБ | Замер на устройстве в фазе 2. Убрать лишние транспорты из bind при необходимости. |
| gomobile и потоки расширения | Holder на отдельном потоке; `packetFlow` на очереди провайдера. |
| Петля | IP фронтов исключить до `0/0` через туннель. |
| Смена пути | Билет `0x03`, тот же `instance_id`. Внутренний TCP всё равно RST. |
| Entitlement | Developer Program; Personal VPN. Шаги портала — в `docs/ios.md`. |
| Фон | Провайдер живёт; контейнер может уснуть. Статус в приложении — снимок. |
| QR с ключом участника | Экран подтверждения; URI в журнал не писать. Камера необязательна, если только вставка или файл. |
| У gomobile нет стокового tvOS | Фаза 6: слайс appletvos (форк в духе NetBird или свой CGO). Путь iPhone этим не блокировать. |
| RSS Go на Apple TV HD | Замер сначала на 4K. HD необязателен. |

## Готово когда

**iPhone.** Участник на iOS 16+ (GUI) ставит сборку TestFlight, импортирует `.loft` или QR `loft://`, нажимает Подключить, видит лампу, и Safari идёт с IP оверлея. Переход Wi-Fi → LTE этот IP сохраняет. Движок Go в appex собирается под iOS 15+.

**Apple TV.** Фаза 6: та же сессия на tvOS 17+ с Siri Remote. Импорт без камеры ТВ, Подключить, приложения ТВ идут с IP оверлея. Ethernet → Wi-Fi этот IP сохраняет.
