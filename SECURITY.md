# Security Policy

**English** · [Русский](#политика-безопасности)

This is the reporting address for loft. GitHub shows it on the Security tab. Use it for a flaw in the protocol or the reference code that lets a third party break isolation, steal a session, or run code. Do not open a public issue for that.

Report privately: [github.com/a3app/loft/security/advisories/new](https://github.com/a3app/loft/security/advisories/new)

## Supported versions

Protocol **loft/1.0**. Software version is [`VERSION`](VERSION); the current release is **1.0.5**.

| | Security updates |
|--|--|
| Latest [GitHub Release](https://github.com/a3app/loft/releases/latest) | yes |
| Older `1.0.x` tags | no; install the latest |
| Untagged `main` | no dedicated backport |

A protocol bug in loft/1.0 is in scope even if the node and the clients already match.

## How to report

1. Open a [private advisory](https://github.com/a3app/loft/security/advisories/new). Private vulnerability reporting is on.
2. Say what is affected (node, `loft-front`, macOS app, `loft-priv`, share URI, the wire).
3. Steps or a PoC against **your** node or client. Do not probe other people's fronts or a public CDN as proof.
4. If you already have a member token `lt_`, say so. The token is the credential; using a leaked token is not a vulnerability.

Expected: acknowledgement within a week. If the report stands, a fix in a patch release and a published advisory. If it does not, a short reason. There is one maintainer; that is not an SLA.

## In scope

- Joining the overlay, reading mux, or injecting frames **without** a valid member token
- Forging the TLS ClientHello / sid so a stranger is accepted
- Recovering keys from ChaCha20-Poly1305, HKDF, or X25519 as used on the wire
- Remote code execution in loft-node, loft-front, the macOS app, or the setuid helper loft-priv
- loft-priv gaining more than the documented route / TUN setup
- A path that leaks `lt_`, node X25519, or overlay plaintext to someone who is not the peer
- Dest-shaped TLS: a distinguisher that works **without** the member token
- Share URI / `.loft` / YAML import that runs unexpected code or writes outside the config directory

## Out of scope

- A middlebox resetting a front, a decoy that does not match the origin, or “the session looks like HTTPS and someone blocked 443”
- Operator error: published token, debug left on, weak SNI
- Using a stolen `lt_` as designed (it authenticates that member)
- Denial of service against a public origin or CDN
- Bugs that already require local admin on the member host
- Feature requests and build/packaging nits

Wire description: [docs/protocol.md](docs/protocol.md).

---

# Политика безопасности

Это адрес для уязвимостей loft. GitHub показывает файл во вкладке Security. Сюда — изъян протокола или эталонного кода, который даёт постороннему чужую сессию, чужой оверлей или выполнение кода. Публичный issue для этого не открывайте.

Закрытый отчёт: [github.com/a3app/loft/security/advisories/new](https://github.com/a3app/loft/security/advisories/new)

## Какие версии чиним

Протокол **loft/1.0**. Версия ПО — [`VERSION`](VERSION); текущий релиз **1.0.5**.

| | Обновления безопасности |
|--|--|
| Последний [релиз на GitHub](https://github.com/a3app/loft/releases/latest) | да |
| Старые теги `1.0.x` | нет; ставьте последний |
| `main` без тега | отдельных бэкпортов нет |

Ошибка в спецификации loft/1.0 — в зоне ответственности, даже если нода и клиенты уже совпадают.

## Как сообщить

1. Откройте [закрытый advisory](https://github.com/a3app/loft/security/advisories/new). Private vulnerability reporting включён.
2. Что затронуто (нода, `loft-front`, приложение macOS, `loft-priv`, share URI, провод).
3. Шаги или PoC на **своей** ноде или клиенте. Чужие фронты и публичный CDN как доказательство не трогайте.
4. Если уже есть ключ участника `lt_`, напишите. Ключ — учётные данные; воспользоваться украденным ключом — не уязвимость.

Ожидание: ответ в течение недели. Если отчёт стоит — правка в патч-релизе и опубликованный advisory. Если нет — коротко почему. Maintainer один; это не SLA.

## В зоне

- Попасть в оверлей, читать mux или подставить кадр **без** действующего ключа участника
- Подделать TLS ClientHello / sid так, что постороннего принимают
- Достать ключи из ChaCha20-Poly1305, HKDF или X25519, как они стоят на проводе
- Удалённое выполнение кода в loft-node, loft-front, приложении macOS или setuid-хелпере loft-priv
- loft-priv получает больше, чем описанная настройка маршрута / TUN
- Путь, который отдаёт `lt_`, X25519 ноды или открытый текст оверлея не пиру
- Dest-shaped TLS: отличитель, который работает **без** ключа участника
- Импорт share URI / `.loft` / YAML, который запускает чужой код или пишет вне каталога конфигурации

## Вне зоны

- Посредник рвёт фронт, decoy не совпал с origin, «сессия как HTTPS и кто-то закрыл 443»
- Ошибка оператора: ключ выложен, debug не выключен, слабый SNI
- Украденный `lt_` работает как задумано (он аутентифицирует участника)
- Отказ в обслуживании публичного origin или CDN
- Баги, для которых уже нужен локальный администратор на машине участника
- Пожелания и мелочи сборки

Провод: [docs/protocol_ru.md](docs/protocol_ru.md).
