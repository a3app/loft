# Loft · Windows

[English](README.md) · **Русский** · [中文](README_zh.md)

Клиент в трее. x86 и x86-64: Windows 7 SP1 и новее. arm64: Windows 10 on ARM / Windows 11 ARM. Окно вторично. Продукт, профили, маршрут и тексты совпадают с клиентом macOS. Здесь только Windows-часть: оболочка Win32, служба с Wintun и уже существующий сеанс `loft-front`.

Наложение для устойчивого канала администратора. Это не средство обхода блокировок.

## Состав

| Файл | От чьего имени | Роль |
|------|----------------|------|
| `loft.exe` | пользователь | трей, окна, ключи DPAPI, запуск `loft-front` |
| `loft-svc.exe` | LocalSystem | адаптер Wintun `Loft`, адрес, маршруты, канал пакетов |
| `loft-front.exe` | пользователь | сеанс. Отдельный бинарь Go, в оболочку не линкуется |
| `wintun.dll` | грузит служба | Wintun 0.14.1, в одном каталоге с `loft-svc.exe`. В git нет |

SOCKS работает без службы. Первое включение туннеля предлагает её поставить. MSI нет. Манифест службы — `asInvoker`, чтобы SCM мог её запустить. `--install` поднимает права через `runas`, если вызывающий не администратор.

## Сборка

Из `clients/Windows`, нужен mingw-w64:

```
make
make test
make front GO_WIN7=/path/to/go-win7/bin/go GOROOT=/path/to/go-win7
```

`make` пишет `dist/clients/Windows/x86` и `dist/clients/Windows/x64`: `loft.exe`, `loft-svc.exe`, `lang/`, `tray/`, `wintun.dll`. С `LLVM_MINGW` или `aarch64-w64-mingw32-clang` ещё и `arm64`. Каталог `dist/` в gitignore. `wintun.dll` копируется из `wintun-check/dist`. Makefile того теста качает `wintun-0.14.1.zip` и сверяет SHA-256 `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`. В zip также есть `wintun/bin/arm64/wintun.dll`.

`make test` собирает переносимые разборщики хостовым компилятором и гоняет их. Окно не открывает.

`make front` ничего не делает, если `GO_WIN7` пуст. Модуль — Go 1.26. Обычный Go 1.21 и новее Windows 7 не собирает. Нужен патченный тулчейн, которым уже проверен `wintun-check` на Windows 7 SP1 x64 (go1.27.3). `GOROOT` — дерево этого Go.

Версия клиента — `clients/Windows/VERSION` (1.0.0). Это не корневой `VERSION` репозитория.

## Поставка

`make release` собирает два канала.

GitHub portable zip:

| Файл | Что это |
|------|---------|
| `loft-<версия>-windows-x86-64.zip` и `loft-<версия>-windows-x86.zip` | Portable. Windows 7 SP1 и новее. Распаковать и запустить. Службу ставит первое включение туннеля. |
| `loft-<версия>-windows-arm64.zip` | Portable. Windows 10 on ARM / Windows 11 ARM. Та же раскладка. На железе здесь не гоняли. |
| `loft-windows-x64-setup.exe` и `loft-windows-x86-setup.exe` | Установщик. Program Files, служба loft, меню Пуск, удаление. MSI нет. Только x86-64 и x86. |

`make portable` пишет эти zip и `loft-windows-<версия>-SHA256SUMS.txt`. Сборка GitHub при запуске читает последний релиз `a3app/loft`. Более новый тег с подходящим zip заменяет каталог запущенного exe и стартует снова. Dist на теге `v*` собирает zip с `VER` из корневого `VERSION` и кладёт их в Release. `loft-front.exe` попадает в пакет, если его уже собрал `make front`. Workflow собирает этот бинарь закреплённым SDK XTLS/go-win7.

Microsoft Store, Windows 10 версии 1809 и новее, только x64: `dist/clients/Windows/store/x64`. Без Wintun и без службы. SOCKS работает. Туннель остаётся в сборке GitHub. Здесь пишется раскладка и `AppxManifest.xml`. Упаковка и подпись — на Windows:

```
makeappx pack /d dist\clients\Windows\store\x64 /p Loft.msix
```

Перед упаковкой заменить `Publisher="CN=a3app"` на subject сертификата Partner Center. Обновления Store заменяют проверку GitHub.

## Запуск

На Windows 7 нужны KB4474419 и KB4490628, иначе подпись Wintun не принимается. Скопировать каталог одной архитектуры. `loft.exe`, `loft-svc.exe`, `loft-front.exe` и `wintun.dll` лежат рядом, рядом же `lang` и `tray`.

`loft-svc.exe` (или `--install`) регистрирует службу `loft` (отображаемое имя `Loft`, автозапуск, зависимость Tcpip). `--uninstall` останавливает и удаляет её. `--uninstall-driver` дополнительно вызывает `WintunDeleteDriver`. Обычное отключение драйвер не снимает.

Двойной щелчок по `loft.exe`. Второй запуск отдаёт командную строку уже работающему экземпляру (`Local\LoftClient`).

Файлы пользователя — в `%APPDATA%\loft`: профили, ключи DPAPI, настройки, маршруты, журнал и файл экземпляра профиля, который `loft-front` получает как `-instance-file`. В YAML профиля ключа нет. Экспорт в `.loft` ключ включает и сначала спрашивает.

## Ограничения

- На этой машине сборки окно не запускалось. Проверены `make test` и линковка mingw `loft.exe` и `loft-svc.exe` для x86 и x64.
- Wintun 0.14.1 проверен на железе только на Windows 7 SP1 x64. Сборка x86 есть, прогона на железе нет.
- Маршруты по умолчанию IPv6 не снимаются, поэтому путь IPv6 может идти мимо туннеля IPv4.
- `%ProgramData%\loft\svc.log` останавливается на 1 МБ. Служба после сбоя сама не перезапускается.
- Адрес Overlay IP ставится как /32. Внешний IP запрашивается через SOCKS по HTTP, в том числе если в настройке указан https: на Windows 7 может не быть TLS 1.2.
- В окне «О программе» слово loft рисуется шрифтом Segoe UI. В SVG бренда пока текстовый элемент, контуры в v1 не обводятся.

Решения записаны в [`design/DESIGN_ru.md`](design/DESIGN_ru.md) §10.
