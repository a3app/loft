# Security Policy

**English** · [Русский](#политика-безопасности) · [中文](#安全策略)

Flaws in loft are reported at this address, which GitHub shows on the Security tab. A flaw in the protocol or the reference code that would allow a third party to break isolation, steal a session, or run code is sent here. A public issue is not opened for that.

Reports are private: [github.com/a3app/loft/security/advisories/new](https://github.com/a3app/loft/security/advisories/new)

## Supported versions

Protocol **loft/1.0**. Software version is [`VERSION`](VERSION); the current release is **1.0.32**.

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

Acknowledgement is expected within a week. If the report stands, a fix is published in a patch release together with an advisory. If it does not, a short reason is given. There is one maintainer; that is not an SLA.

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

Протокол **loft/1.0**. Версия ПО — [`VERSION`](VERSION); текущий релиз **1.0.32**.

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

---

# 安全策略

这是 loft 的漏洞报送地址。GitHub 会在 Security 页签里展示。协议或参考代码里，能让外人拆开隔离、偷走会话、或跑代码的洞，走这里。别为此开公开 issue。

私下报送：[github.com/a3app/loft/security/advisories/new](https://github.com/a3app/loft/security/advisories/new)

## 哪些版本会修

协议 **loft/1.0**。软件版本见 [`VERSION`](VERSION)；当前发布是 **1.0.32**。

| | 安全更新 |
|--|--|
| 最新 [GitHub Release](https://github.com/a3app/loft/releases/latest) | 会 |
| 更早的 `1.0.x` 标签 | 不会；装最新的 |
| 没打标签的 `main` | 没有单独回移植 |

loft/1.0 规范里的洞也算范围内，哪怕节点和客户端版本已经对齐。

## 怎么报

1. 开一份 [私下 advisory](https://github.com/a3app/loft/security/advisories/new)。Private vulnerability reporting 是开着的。
2. 写清打到了哪（节点、`loft-front`、macOS 应用、`loft-priv`、分享链接、线协议）。
3. 步骤或 PoC 打在**你自己的**节点或客户端上。别拿别人的前置或公网 CDN 当证据去探。
4. 如果手里已经有成员令牌 `lt_`，写一声。令牌就是凭证；用偷来的令牌能进去，这不算漏洞。

预期：一周内有回音。站得住就在补丁版里修，并发布 advisory。站不住就短说为什么。维护者就一个人；这不是 SLA。

## 范围内

- **没有**有效成员令牌就能进覆盖网、读 mux、或塞帧
- 伪造 TLS ClientHello / sid，让陌生人被收下
- 从线上用的 ChaCha20-Poly1305、HKDF 或 X25519 里抠出密钥
- 在 loft-node、loft-front、macOS 应用或 setuid 助手 loft-priv 里远程执行代码
- loft-priv 拿到的权限超过文档里的路由 / TUN 配置
- 一条路把 `lt_`、节点 X25519 或覆盖网明文漏给非对端
- Dest-shaped TLS：不靠成员令牌就能把会话和真 HTTPS 分开的区分器
- 分享链接 / `.loft` / YAML 导入会跑意外代码，或写到配置目录外面

## 范围外

- 中间盒掐掉前置、诱饵对不上源站、或「会话长得像 HTTPS 然后有人封了 443」
- 运营方自己的锅：令牌公开了、debug 没关、SNI 太弱
- 偷来的 `lt_` 按设计能用（它就是在认证那个成员）
- 对着公网源站或 CDN 做拒绝服务
- 本来就需要成员机器上本地管理员的洞
- 功能许愿和打包毛刺

线协议：[docs/protocol_zh.md](docs/protocol_zh.md)。
