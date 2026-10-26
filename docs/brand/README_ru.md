# loft · фирменный стиль

[English](README.md) · **Русский** · [中文](README_zh.md)

Сборка и запуск: [docs/guide_ru.md](../guide_ru.md). Протокол: [docs/protocol_ru.md](../protocol_ru.md).

Знак — L-крыша с лампой: буква L и окно, которое горит, когда туннель поднят.

Сплошная заливка допускается только в знаке и в трее. Лёгкое свечение окна используется только на иконке приложения.

## Палитра

| Токен | Hex | Где |
|-------|-----|-----|
| Ink | `#1A1714` | фон иконки приложения, знак на светлом |
| Stone | `#B7A99A` | тело L на тёмной иконке |
| Copper | `#C67B3A` | трей Windows |
| Lamp | `#E8B84A` | окно, подключено |
| Lamp hi | `#F3D27A` | блик окна на иконке приложения |
| Snow | `#F3EDE4` | знак на тёмном, трей на тёмном |
| Paper | `#E8DFD2` | поля, превью |
| Mute | `#8A7E72` | подписи |

## Шрифт

**Avenir Next** (Linotype, Adrian Frutiger / Akira Kobayashi, 2004). Геометрический гуманистический гротеск: круглая «o» перекликается с окном лампы.

| Роль | Начертание | Правило |
|------|------------|---------|
| Вордмарк / lockup | Demi 600 | строчные `loft`, трекинг −0.04em |
| Слоган | Regular 400 | *Loft Overlay For TLS*, title case, трекинг −0.02em |
| Заголовки UI | Demi 600 | sentence case, трекинг −0.02em |
| Кнопки, вкладки | Medium 500 | без капса, кроме коротких статусов |
| Текст, документация | Regular 400 | межстрочный 1.45 |
| Код, ключи `lt_…` | SF Mono / Menlo / Consolas | никогда не Avenir |

Ultra Light, Heavy и курсив в lockup не использовать.

Стек: `"Avenir Next", Avenir, "Helvetica Neue", "Segoe UI", ui-sans-serif, system-ui, sans-serif`.

На macOS шрифт системный и поддерживает кириллицу. Чтобы на Windows вордмарк совпадал попиксельно, нужны SVG-контуры или установленный файл шрифта.

## Файлы

| Файл | Назначение |
|------|------------|
| `svg/loft-mark.svg` | протокол, сайт |
| `svg/loft-lockup.svg` | знак + слово *loft* |
| `svg/loft-lockup-readme.svg` / `png/loft-lockup-readme.png` | README: lockup как social preview (обрезка) + *Loft Overlay For TLS* |
| `png/type-specimen.png` | таблица шрифта |
| `svg/loft-og.svg` / `png/github-social-preview.png` | GitHub social preview 1280×640. Settings → General → Social preview |
| `png/a3-github-avatar.png` | аватар профиля GitHub для a3app (A³, 2048×2048). Settings → Profile picture |
| `svg/a3-mark.svg` | авторский знак A³, живой текст Avenir Next |
| `svg/a3-heart.svg` | пиксельное сердце 7×6, лампа loft |
| `svg/a3-colophon.svg` | `Made with love` + сердце + `by A³` |
| `png/a3-colophon.png` | концепт колофона (строка About) |
| `svg/loft-icon.svg` | иконка приложения (тёмная) |
| `svg/loft-icon-light.svg` | иконка на светлом |
| `svg/loft-favicon.svg` | 32px |
| `svg/loft-menubar-template.svg` | шаблон macOS `NSStatusItem` |
| `app/loft.icns` | бандл macOS |
| `windows/loft.ico` | приложение Windows: `loft.exe`, установщик, ярлыки. 16–256 px, 16 из пиксельной сетки |
| `windows/loft-file.ico` | файлы `.loft` в Проводнике: бумажный лист с горящим знаком |
| `windows/loft-tray{,-off,-err}.ico` | трей Windows ресурсами: подключено, отключено, ошибка (окно `#E24B4B`) |
| `windows/loft-tray-light{,-off,-err}.ico` | то же с телом Ink для светлой панели задач |
| `windows/store/` | ресурсы MSIX: `Square44x44Logo` (targetsize + unplated), `Square71x71`, `Square150x150`, `Wide310x150`, `Square310x310`, `StoreLogo`, `SplashScreen`, масштаб 100–400 |
| `tray/loft-tray-16.png` | область уведомлений Windows, подключено |
| `tray/loft-tray-off-16.png` | область уведомлений Windows, отключено |
| `tray/loft-tray{,-light}{,-off,-err}-{16,20,24,32}.png` | все состояния трея для 100 / 125 / 150 / 200 %, их читает `tray.c` |
| `menubar/loftTemplate.png` (`@2x` `@3x`) | строка меню macOS, подключено |
| `menubar/loftTemplate-off-*.png` | строка меню macOS, отключено |

## Правила

1. Крыша накрывает только вертикальную ножку L, а основание уходит вправо. Это именно L.
2. При размере ≤16px используется сетка `PIX16` из `generate.py`. Не уменьшать с 1024.
3. Значок в строке меню macOS — **template image** (чёрный + альфа). Система раскрашивает его сама.
4. Трей Windows по умолчанию — **copper + lamp**.
5. Подключено: лампа горит. Отключено: то же L, окно погашено (macOS: без выреза).
6. Слово *loft* — строчными, Avenir Next Demi, трекинг −0.04em.
7. Слоган — рекурсивная аббревиатура *Loft Overlay For TLS* (LOFT). Title case. Вордмарк остаётся строчным `loft`.
8. Колофон только по-английски: `Made with love` + пиксельное сердце + `by A³`. Сердце — 7×6 клеток лампы. A³ — знак автора, а не знак протокола.

Пересборка: `/tmp/loft-brand/bin/python generate.py`. Только набор Windows, без Chrome: `generate.py windows`
