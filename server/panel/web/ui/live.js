/* loft-panel live layer.
   Markup and drawing come from design/index.html (the mock). Served by
   loft-panel, this file replaces every sample with the server's data or an
   empty state: sign-in, the setup wizard (owner, domain and DNS, certificate, first
   member, packages), the overview, members, Segments and Domains. Opened from disk
   (no /api), the page stays a plain mock. */
(() => {
  const L = { csrf: '', me: null, sha: '', node: null, info: null, wz: null };
  window.LIVE = L;
  const netRight = k => (k === 'prio' || k === 'iface-priorities' || k === 'channels' || k === 'addrs' || k === 'adapters' || k === 'network') ? 'network' : k;

  async function api(method, path, body) {
    const opt = { method, headers: { Accept: 'application/json' }, credentials: 'same-origin' };
    if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
    if (method !== 'GET' && L.csrf) opt.headers['X-CSRF-Token'] = L.csrf;
    let r;
    try { r = await fetch(path, opt); } catch (e) {
      throw Object.assign(new Error('Нет связи с панелью — проверьте подключение и повторите'), { status: 0, offline: true });
    }
    let j = {};
    try { j = await r.json(); } catch (e) { /* empty body */ }
    if (r.status === 401 && path !== '/api/login') { L.me = null; go('login'); }
    if (!r.ok) throw Object.assign(new Error(ru(j.error || ('HTTP ' + r.status))), { status: r.status, raw: j.error });
    return j;
  }
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  // a second click while a save is in flight is dropped
  const once = fn => { let busy = false; return async (...a) => { if (busy) return; busy = true; try { return await fn(...a); } finally { busy = false; } }; };
  const LOC = () => ({ en: 'en-GB', zh: 'zh-CN', ru: 'ru-RU' }[(window.I18N && I18N.lang) || 'ru'] || 'ru-RU'); // dates follow the language

  /* The server speaks English (it is an API); the page speaks Russian. */
  const RU = [
    [/^wrong login or password$/, () => 'Неверный логин или пароль'],
    [/^wrong setup code$/, () => 'Неверный код установки'],
    [/^too many attempts/, () => 'Слишком много попыток — подождите и попробуйте снова'],
    [/^password: at least (\d+) characters$/, m => `Пароль — не короче ${m[1]} знаков`],
    [/^password: no line breaks or colons$/, () => 'В пароле не должно быть переводов строки и двоеточий'],
    [/^login: lowercase Latin letters, digits, _ and -, up to 31$/, () => 'Логин: строчная латиница, цифры, _ и -, до 31 знака'],
    [/^account (\S+) exists$/, m => `Учётная запись ${m[1]} уже есть`],
    [/^no such account$/, () => 'Такой учётной записи нет'],
    [/^(\S+) is a reserved name$/, m => `Имя ${m[1]} занято системой`],
    [/^root: only its SSH keys can change here$/, () => 'У root здесь меняются только SSH-ключи'],
    [/^root cannot be deleted$/, () => 'root удалить нельзя'],
    [/^this leaves no account that can log in by key and become root$/, () => 'Так на сервер будет не войти: не останется учётки, которая входит по ключу и может стать root'],
    [/^not an SSH public key: (.*)$/, m => `Это не открытый SSH-ключ: ${m[1]}`],
    [/^at most 100 keys$/, () => 'Не больше 100 ключей'],
    [/^password: at least 9 characters, no colons or line breaks$/, () => 'Пароль — не короче 9 знаков, без двоеточий и переводов строки'],
    [/^(\S+): unusual home directory/, m => `У ${m[1]} необычный домашний каталог — правьте его вручную`],
    [/^failed logins: 1 to 100$/, () => 'Неудачных входов: от 1 до 100'],
    [/^window: 1 to 1440 minutes$/, () => 'Окно: от 1 до 1440 минут'],
    [/^ban: a minute to a year, or for good$/, () => 'Блокировка: от минуты до года или навсегда'],
    [/^ban: 1 day, 7 days or for good$/, () => 'Блокировка: 24 часа, 7 дней или навсегда'],
    [/^"(.*)" is not an address or a subnet$/, m => `«${m[1]}» — не адрес и не подсеть`],
    [/^at most 200 allowed addresses$/, () => 'В белом списке не больше 200 адресов'],
    [/^the guard package \(fail2ban\) is not installed$/, () => 'Пакет «Защитник» не установлен'],
    [/^fail2ban did not accept the configuration: (.*)$/, m => `fail2ban не принял настройки, прежние возвращены: ${m[1]}`],
    [/^fail2ban did not reload: (.*)$/, m => `fail2ban не перезапустился: ${m[1]}`],
    [/^that would ban your own address (.*)$/, m => `Так заблокируется ваш собственный адрес ${m[1]}`],
    [/^the loopback cannot be banned$/, () => 'Петлю заблокировать нельзя'],
    [/^(\S+) is on the allowed list$/, m => `${m[1]} — в белом списке`],
    [/^set a recovery phrase first$/, () => 'Сначала задайте фразу восстановления'],
    [/^a copy always holds the node and the panel$/, () => 'В копию всегда входят нода и панель'],
    [/^choose what to restore$/, () => 'Выберите, что восстановить'],
    [/^"(.*)" is not a copy$/, m => `«${m[1]}» — не копия`],
    [/^the recovery phrase is too short$/, () => 'Фраза восстановления слишком короткая'],
    [/^the recovery phrase does not match this copy$/, () => 'Фраза не подходит к этой копии'],
    [/^not a loft backup/, () => 'Это не копия loft'],
    [/^the copy has no (\S+) part$/, m => `В копии нет части «${({ node: 'нода', panel: 'панель', mail: 'почта' })[m[1]] || m[1]}»`],
    [/^the copy('s archive| is cut off| is damaged)(.*)$/, m => 'Копия повреждена или обрезана' + (m[2] ? ': ' + m[2].replace(/^[:\s]+/, '') : '')],
    [/^the copy of the current state failed, nothing was restored: (.*)$/, m => `Не удалась копия текущего состояния, ничего не восстановлено: ${m[1]}`],
    [/^the copy was made but not carried to the store: (.*)$/, m => `Копия сделана, но не перенесена в хранилище: ${ru(m[1])}`],
    [/^the copy failed: (.*)$/, m => `Копия не сделана: ${m[1]}`],
    [/^the job was replaced by another one$/, () => 'Задание вытеснено другим'],
    [/^(S3 needs .*|S3 needs the secret key)$/, () => 'Для S3 нужны адрес, бакет и оба ключа'],
    [/^SFTP needs a server and a user$/, () => 'Для SFTP нужны сервер и пользователь'],
    [/^the directory must be an absolute path$/, () => 'Каталог — полный путь, от /'],
    [/^"(.*)" is not a directory path$/, m => `«${m[1]}» — не путь к каталогу`],
    [/^(\S+) cannot hold copies$/, m => `В ${m[1]} копии держать нельзя`],
    [/^"(.*)" is not an https:\/\/ address$/, m => `«${m[1]}» — не адрес https://`],
    [/^(\S+) did not let the key in/, m => `${m[1]} не пустил по ключу — добавьте ключ сервера в authorized_keys`],
    [/^the server's host key changed/, () => 'Ключ хоста сервера изменился: если его переустановили, добавьте хранилище заново'],
    [/^the copy is over 5 GB/, () => 'Копия больше 5 ГБ — столько S3 за одну загрузку не принимает'],
    [/^keep 1 to 1000 copies$/, () => 'Хранить от 1 до 1000 копий'],
    [/^schedule: daily, weekly or off; keep 1 to 1000 copies$/, () => 'Расписание и число копий заданы неверно'],
    [/^no such store$/, () => 'Такого хранилища нет'],
    [/^(catch-all|forwarding): (.*)$/, m => `${m[1] === 'catch-all' ? 'Ловить всё' : 'Пересылка'}: ${ru(m[2])}`],
    [/^"(.*)" is not an e-mail address$/, m => `«${m[1]}» — не адрес почты`],
    [/^"(.*)" is not a domain name$/, m => `«${m[1]}» — не доменное имя`],
    [/^"(.*)" is not a DKIM selector$/, m => `«${m[1]}» — не селектор DKIM: латиница, цифры и дефис`],
    [/^(\S+) is not a mail domain of this server$/, m => `${m[1]} — не почтовый домен этого сервера`],
    [/^at most (\d+) addresses$/, m => `Не больше ${m[1]} адресов`],
    [/^a box cannot forward to itself$/, () => 'Ящик не может пересылать сам себе'],
    [/^a new mailbox needs a password$/, () => 'Новому ящику нужен пароль'],
    [/^(\S+) is an alias$/, m => `${m[1]} — уже псевдоним`],
    [/^(\S+) is a mailbox$/, m => `${m[1]} — уже ящик`],
    [/^alias (\S+) exists$/, m => `Псевдоним ${m[1]} уже есть`],
    [/^an alias needs somewhere to deliver$/, () => 'Укажите, куда доставлять'],
    [/^an alias cannot deliver to itself$/, () => 'Псевдоним не может доставлять сам себе'],
    [/^no such (mail domain|mailbox|alias)$/, m => ({ 'mail domain': 'Такого почтового домена нет', mailbox: 'Такого ящика нет', alias: 'Такого псевдонима нет' })[m[1]]],
    [/^no panel user (.*)$/, m => `Нет пользователя панели ${m[1]}`],
    [/^at most (\d+) (mail domains|mailboxes|aliases)$/, m => `Не больше ${m[1]} ${({ 'mail domains': 'почтовых доменов', mailboxes: 'ящиков', aliases: 'псевдонимов' })[m[2]]}`],
    [/^quota: 0 to \d+ MB$/, () => 'Объём ящика: от 0 до 1 ТБ'],
    [/^the mail server package is not installed$/, () => 'Пакет «Почтовый сервер» не установлен'],
    [/^the mail configuration did not pass its check: (.*)$/, m => `Настройка не прошла проверку Postfix или Dovecot, прежние файлы возвращены: ${m[1]}`],
    [/^mail did not restart: (.*)$/, m => `Почта не перезапустилась, прежние файлы возвращены: ${m[1]}`],
    [/^webmail site: (.*)$/, m => `Сайт веб-почты не принят nginx: ${m[1]}`],
    [/^DKIM key of (\S+): (.*)$/, m => `Ключ DKIM для ${m[1]}: ${m[2]}`],
    [/^(\S+) is in no zone on the Domains screen$/, m => `${m[1]} нет среди зон в «Доменах»`],
    [/^zone (\S+) is managed by hand$/, m => `Зона ${m[1]} ведётся вручную: записи вносятся у регистратора`],
    [/^(\S+) does not resolve yet/, m => `Имя ${m[1]} пока не находится в DNS: добавьте A-запись на этот сервер и подождите несколько минут`],
    [/^(\S+) points at (.+), this server is (.+)$/, m => `${m[1]} указывает на ${m[2]}, а этот сервер — ${m[3]}. Исправьте A-запись`],
    [/^http:\/\/(\S+)\/ does not reach the panel \((.*)\): port 80/, m => `Запрос http://${m[1]}/ не дошёл до панели (${m[2]}): порт 80 должен быть открыт и вести на этот сервер`],
    [/^Let's Encrypt could not check http:\/\/(\S+)\/: (.*)$/, m => `Let’s Encrypt не смог проверить http://${m[1]}/: ${m[2]}`],
    [/^member keys are not kept$/, () => 'Ключи членов в панели не хранятся'],
    [/^key is not stored$/, () => 'Ключ этого члена в панели не сохранён'],
    [/^no stored key for this member$/, () => 'Ключ этого члена в панели не сохранён'],
    [/^member id required$/, () => 'Укажите идентификатор члена'],
    [/^member (\S+) already exists$/, m => `Член сети ${m[1]} уже есть — выберите другой идентификатор`],
    [/^member id "(.*)": /, m => `Идентификатор «${m[1]}»: только латиница, цифры, точка, дефис, подчёркивание; до 64 знаков`],
    [/^network\.conf changed since it was opened/, () => 'network.conf изменили, пока вы его правили: обновите страницу и примените снова'],
    [/^loft-node did not start with (.*?)(, the (old|previous) .* is back)?: (.*)$/, m => `Нода не запустилась с новыми настройками${m[2] ? ', прежние возвращены' : ''}: ${m[4]}`],
    [/^loft-panel-adm is not running$/, () => 'Служба loft-panel-adm не запущена'],
    [/^"(.*)" is not a DNS name/, m => `«${m[1]}» — не имя вида vpn.example.net`],
    [/^your role cannot do this$/, () => 'Вашей роли это недоступно'],
    [/^stale page: reload$/, () => 'Страница устарела — обновите её'],
    [/^another job is running/, () => 'Уже идёт другое задание — дождитесь его окончания'],
    [/^a package job is running/, () => 'Идёт задание с пакетами — дождитесь его окончания'],
    [/^the job stopped before it finished$/, () => 'Задание прервалось, не закончив (сервер перезагружался?) — запустите его снова'],
    [/^could not start the job: (.*)$/, m => `Задание не запустилось: ${m[1]}`],
    [/^(install|remove|upgrade) (\S+): (.*)$/, m => `${{ install: 'Установка', remove: 'Удаление', upgrade: 'Обновление' }[m[1]]} «${PT[m[2]] || m[2]}»: ${ru(m[3])}`],
    [/^apt-get (\S+) failed: (.*)$/, m => `apt-get ${m[1]} завершился с ошибкой (${m[2]}) — подробности в журнале`],
    [/^(\S+) did not start \(its log is above\)$/, m => `служба ${m[1]} не запустилась — её журнал ниже`],
    [/^the (\S+) repository has no packages for this OS \((.*)\)$/, m => `В репозитории ${m[1]} нет пакетов для этой ОС (${m[2]})`],
    [/^release (\S+) did not come up \((.*)\): (\S+) is back$/, m => `Версия ${m[1]} не поднялась (${m[2]}) — возвращена ${m[3]}`],
    [/^(\S+): SHA-256 .* does not match the catalog$/, m => `${m[1]}: контрольная сумма не совпала с каталогом — файл не установлен`],
    [/^(\S+): SHA-256 .* does not match/, m => `${m[1]}: контрольная сумма не совпала с SHA256SUMS — файл не установлен`],
    [/^port (\d+) is used by (.*)$/, m => `Порт ${m[1]} занят: ${m[2]}`],
    [/^package "mtproto" is not installed$/, () => 'Прокси Telegram MTProto не установлен'],
    [/^"(.*)" is not a DNS name like cloudflare.com$/, m => `«${m[1]}» — не имя вида cloudflare.com`],
    [/^package "(\S+)" has no build for (\S+)$/, m => `Для «${PT[m[1]] || m[1]}» нет сборки под ${m[2]}`],
    [/^version (\S+) is already installed$/, m => `Версия ${m[1]} уже установлена`],
    [/^GitHub is not reachable: (.*)$/, m => `GitHub недоступен: ${m[1]}`],
    [/^GitHub answered HTTP (\d+)$/, m => `GitHub ответил HTTP ${m[1]}`],
    [/^no release carries the panel yet$/, () => 'В релизах пока нет сборки панели'],
    [/^notify: recipient is not an e-mail address$/, () => '«Кому» — не адрес почты'],
    [/^notify: sender is not an e-mail address$/, () => '«Отправитель» — не адрес почты'],
    [/^notify: SMTP server is not a host name$/, () => 'SMTP-сервер — имя вида smtp.example.net'],
    [/^notify: SMTP port is out of range$/, () => 'Порт — число от 1 до 65535'],
    [/^notify: save the recipient and the SMTP server first$/, () => 'Сначала сохраните получателя и SMTP-сервер'],
    [/^SMTP login: (.*)$/, m => `SMTP-сервер не принял логин или пароль: ${m[1]}`],
    [/^SMTP (STARTTLS|sender|recipient)?:? ?(.*)$/, m => `Ошибка SMTP${m[1] ? ' (' + ({ STARTTLS: 'STARTTLS', sender: 'отправитель', recipient: 'получатель' }[m[1]]) + ')' : ''}: ${m[2]}`],
    [/^unknown time zone/, () => 'Такого часового пояса сервер не знает'],
    [/^bad settings$/, () => 'Недопустимое значение'],
    [/^sign in$/, () => 'Войдите снова'],
    [/^the provider refused the token$/, () => 'Провайдер не принял токен'],
    [/^the provider answered HTTP (\d+): (.*)$/, m => `Провайдер ответил HTTP ${m[1]}: ${m[2]}`],
    [/^the current password is wrong$/, () => 'Текущий пароль неверен'],
    [/^access check failed: (.*)$/, m => `Доступ не проверен: ${ru(m[1])}`],
    [/^Namecheap: the API user and key do not match, or API access is off .*$/, () => 'Namecheap: пользователь API и ключ не подходят друг к другу или API выключен (Profile → Tools → API Access). Пользователь — имя аккаунта, не e-mail'],
    [/^zone (\S+) is not in this Cloudflare account$/, m => `Зоны ${m[1]} нет в этом аккаунте Cloudflare`],
    [/^the provider does not serve (\S+): (.*)$/, m => `Провайдер не обслуживает ${m[1]}: ${ru(m[2])}`],
    [/^zone: a domain like/, () => 'Зона — домен вида example.com'],
    [/^zone (\S+) uses this provider/, m => `Зона ${m[1]} использует этот доступ — сначала перенесите или удалите её`],
    [/^zone saved, but: (.*)$/, m => `Зона сохранена, но: ${m[1]}`],
    [/^record name: /, () => 'Имя записи — @ или имя внутри зоны'],
    [/^A: an IPv4 address$/, () => 'A — адрес IPv4'],
    [/^AAAA: an IPv6 address$/, () => 'AAAA — адрес IPv6'],
    [/^(CNAME|NS): a host name$/, m => `${m[1]} — имя хоста`],
    [/^MX: /, () => 'MX — почтовый сервер и приоритет 0–65535'],
    [/^CAA: like /, () => 'CAA — вида 0 issue "letsencrypt.org"'],
    [/^SRV: weight port target$/, () => 'SRV — вес, порт и цель'],
    [/^(\S+) is in no zone on the Domains screen/, m => `${m[1]} нет ни в одной зоне раздела «Домены» — добавьте её зону с DNS-провайдером`],
    [/^zone (\S+) is managed by hand: DNS-01 needs a DNS provider$/, m => `Зона ${m[1]} ведётся вручную — для DNS-01 нужен DNS-провайдер`],
    [/^a \*\. name needs DNS-01$/, () => 'Имени с *. нужна проверка DNS-01'],
    [/^the certificate must cover (\S+)$/, m => `Сертификат должен включать имя ${m[1]}`],
    [/^the DNS-01 record (\S+) did not show up in 3 minutes$/, m => `Запись DNS-01 ${m[1]} не появилась за 3 минуты`],
    [/^the certificate is not PEM with DNS names$/, () => 'Сертификат — не PEM с именами DNS'],
    [/^the certificate has expired$/, () => 'Срок сертификата истёк'],
    [/^the key is not a PEM private key$/, () => 'Ключ — не закрытый ключ PEM'],
    [/^the provider needs its token/, () => 'Нужен токен (для Namecheap — и пользователь API)'],
    [/^Namecheap: (.*)$/, m => `Namecheap: ${m[1]}`],
    [/^listen "(.*)": /, m => `«${m[1]}» — адрес вида :443 или 0.0.0.0:443`],
    [/^at least one front$/, () => 'Нужен хотя бы один фронт'],
    [/^the first web server \((.*)\) is the panel's own/, m => `Первый веб-сервер (${m[1]}) — свой у панели, он остаётся первым`],
    [/^request paths must differ$/, () => 'Пути запросов должны различаться'],
    [/^path "(.*)": /, m => `Путь «${m[1]}»: начинается с /, латиница, цифры, . _ ~ - /`],
    [/^relay (\S+): (.*)$/, m => `Ретранслятор ${m[1]}: ${m[2]}`],
    [/^loft-node did not start with the new node\.conf/, () => 'Нода не запустилась с новыми настройками — прежние возвращены'],
    [/^list name "(.*)": /, m => `Имя списка «${m[1]}»: строчная латиница, цифры, точка, дефис`],
    [/^Cloudflare is not reachable: (.*)$/, m => `Cloudflare недоступен: ${m[1]}`],
    [/^unknown role$/, () => 'Такой роли нет'],
    [/^set a password or make an invitation$/, () => 'Задайте пароль или сделайте ссылку-приглашение'],
    [/^user (\S+) exists$/, m => `Пользователь ${m[1]} уже есть`],
    [/^you cannot switch off your own sign-in$/, () => 'Себе вход не выключить'],
    [/^you cannot delete yourself$/, () => 'Себя не удалить'],
    [/^the last owner cannot be removed or demoted$/, () => 'Последнего владельца нельзя удалить или понизить'],
    [/^the invitation is used or expired$/, () => 'Приглашение уже использовано или истекло'],
    [/^a role named "(.*)" exists$/, m => `Роль «${m[1]}» уже есть`],
    [/^(\S+) has this role: give them another one first$/, m => `Эта роль у ${m[1]} — сначала дайте другую`],
    [/^built-in roles stay$/, () => 'Встроенные роли не удаляются'],
    [/^role name: /, () => 'Название роли — от 1 до 40 знаков'],
    [/^no such user$/, () => 'Такого пользователя нет'],
    [/^login "(.*)": /, m => `Логин «${m[1]}»: строчная латиница, цифры, точка, дефис, подчёркивание`],
    [/^the service failed: (.*)$/, m => `служба упала: ${m[1]}`],
    [/^authorise this node in the network's controller$/, () => 'разрешите этот узел в контроллере сети'],
    [/^channel name "(.*)": /, m => `Имя «${m[1]}»: строчная латинская буква, затем строчные буквы, цифры или дефис, до 12 знаков`],
    [/^connection name: up to 40 characters$/, () => 'Имя подключения — до 40 знаков'],
    [/^a connection named "(.*)" exists$/, m => `Подключение «${m[1]}» уже есть`],
    [/^private key: /, () => 'Частный ключ — 44 знака base64'],
    [/^address: like /, () => 'Адрес в туннеле — вида 10.8.0.2/24'],
    [/^a WireGuard channel needs a peer$/, () => 'Каналу WireGuard нужен пир'],
    [/^peer (\d+): public key/, m => `Пир ${m[1]}: публичный ключ — 44 знака base64`],
    [/^peer (\d+): preshared key/, m => `Пир ${m[1]}: общий ключ — 44 знака base64`],
    [/^peer (\d+): allowed subnets/, m => `Пир ${m[1]}: разрешённые подсети, например 0.0.0.0/0`],
    [/^peer (\d+): (.*)$/, m => `Пир ${m[1]}: ${ru(m[2])}`],
    [/^"(.*)" is not host:port$/, m => `«${m[1]}» — не адрес:порт`],
    [/^(.*) overlaps the loft network (.*): pick another subnet$/, m => `${m[1]} пересекается с сетью loft ${m[2]} — выберите другую подсеть`],
    [/^SSTP needs a user name and a password$/, () => 'Для SSTP нужны имя пользователя и пароль'],
    [/^the \.ovpn line "(.*)" is set by the panel or not allowed: remove it$/, m => `Строку .ovpn «${m[1]}» задаёт панель или она запрещена — уберите её`],
    [/^OpenVPN configuration: /, () => 'Вставьте файл .ovpn (в нём должна быть строка remote)'],
    [/^OpenVPN login: /, () => 'Вход OpenVPN: и имя, и пароль — или ни того, ни другого'],
    [/^network ID: /, () => 'ID сети — 16 шестнадцатеричных знаков'],
    [/^proxy address: (.*)$/, m => `Адрес прокси: ${ru(m[1])}`],
    [/^server: (.*)$/, m => `Сервер: ${ru(m[1])}`],
    [/^UDP: direct, block/, () => 'UDP через прокси — только для SOCKS5'],
    [/^link: (.*)$/, m => `Ссылка: ${m[1]}`],
    [/^exit rules send traffic to this channel/, () => 'Правила выхода отправляют трафик в этот канал — сначала измените их'],
    [/^channel (\S+) uses it/, m => `Его использует канал ${m[1]} (при сбое или «подключаться через»)`],
    [/^channel (\S+) is not used for internet exit$/, m => `Канал ${m[1]} не используется для выхода в интернет`],
    [/^only WireGuard, OpenVPN and ZeroTier channels take incoming connections$/, () => 'Принимать входящие умеют только каналы WireGuard, OpenVPN и ZeroTier'],
    [/^zerotier-cli join: /, () => 'ZeroTier не подключился: установлен и запущен ли пакет ZeroTier-клиент?'],
    [/^Xray did not start/, () => 'Xray не запустился: установлен ли пакет Xray-клиент?'],
    [/^(\S+) did not start: (.*)$/, m => `${m[1]} не запустился: ${m[2]}`],
  ];
  const ru = msg => { for (const [re, f] of RU) { const m = String(msg).match(re); if (m) return f(m); } return msg; };

  /* Jobs: the node restarts during them and drops this very connection, so
     a failed poll is retried until the job ends (or 4 minutes pass). */
  async function job(path, body, onWait) {
    const { job: id } = await api('POST', path, body);
    const until = Date.now() + 240e3;
    for (let n = 0; Date.now() < until; n++) {
      await sleep(n < 3 ? 700 : 1500);
      let j;
      try { j = await api('GET', '/api/jobs/' + id); } catch (e) { if (e.status === 404 || e.status === 401) throw e; onWait && onWait(true); continue; }
      onWait && onWait(false);
      if (j.state === 'done') return j.result || {};
      if (j.state === 'error') throw new Error(ru(j.error));
    }
    throw new Error('Сервер долго не отвечает — обновите страницу через минуту');
  }

  /* ── empty states: nothing on these screens talks to the server yet ── */
  const SOON = {
  };
  const empty = (k) => pageHead(SOON[k][0], '') + `<div class="empty"><div class="empty-ic">${ic('i-gear', 28)}</div><b>Раздел в разработке</b><p>${esc(SOON[k][1])}</p></div>`;

  function wrapViews() {
    for (const k of Object.keys(SOON)) V[k] = () => empty(k);
    V.packages = packagesView;
    V.settings = settingsView;
    V.network = networkView;
    V.channels = channelsPage;
    V.adapters = addrsPage;
    V.addrs = addrsPage;
    V.prio = prioPage;
    V['iface-priorities'] = prioPage;
    V.users = usersView;
    V.node = nodeView;
    V.domains = domainsView;
    V.invite = inviteView;
    FULL.invite = 1;
    window.renderMenu = renderMenuLive;
    V.mail = mailView;
    V.system = systemView;
    V.me = meView;
    window.pkgOn = n => !!(L.pk && L.pk.packages.some(p => PT[p.key] === n && p.installed));
    window.openPicker = () => { if (L.pk) { L.P.pick = null; L.P.q = ''; picker(); } };
    for (const k of Object.keys(EXTRA)) delete EXTRA[k];
    Object.setPrototypeOf(EXTRA, new Proxy({}, { get: () => ['loft', '0 / 0', 'никогда', 0] }));

    V.main = () => {
      const st = L.node && L.node.active, ov = L.ov || {}, nd = ov.node || {}, sv = ov.server || {}, w = L.wz || {};
      const all = NETS.flatMap(n => n.members);
      const dm = L.dm || { zones: [], certs: [], providers: [] };
      const nc = (dm.certs || []).find(c => c.id === 'node'), until = w.cert_until || (nc && nc.until);
      const cert = until ? untilCell(until) : pill('самоподписанный', 'warn');
      const dnsNames = [];
      for (const p of dm.providers || []) {
        const n = (DNSK[p.kind] || [])[0] || p.kind;
        if (n && !dnsNames.includes(n)) dnsNames.push(n);
      }
      if ((dm.zones || []).some(z => z.provider === 'manual') && !dnsNames.includes('Вручную')) dnsNames.push('Вручную');
      const certRows = (dm.certs || []).slice(0, 4).map(c => `<div><dt class="mono">${esc((c.names || []).join(', ') || '—')}</dt><dd>${untilCell(c.until)}</dd></div>`).join('');
      const emptyDom = !(dm.zones || []).length && !(dm.certs || []).length;
      const domBody = emptyDom
        ? `<p class="hint" style="margin:0 0 16px">Зон нет. Добавьте домен, чтобы панель вела его записи.</p>${L.can('domains') ? `<button class="btn" type="button" onclick="go('domains')">${ic('i-plus')}Добавить домен</button>` : ''}`
        : `<dl class="kv">${certRows}${dnsNames.length ? `<div><dt>DNS</dt><dd>${esc(dnsNames.join(', '))}</dd></div>` : (dm.zones || []).length ? `<div><dt>Зоны</dt><dd class="num">${dm.zones.length}</dd></div>` : ''}</dl>`;
      const host = (nd.advertise || '').replace(/^https?:\/\//, '').replace(/\/.*$/, '');
      const ips = (ov.addrs || []).map(a => ({ ...a, ip: a.addr.split('/')[0] }));
      const v4 = ips.filter(a => !a.ip.includes(':')), v6 = ips.filter(a => a.ip.includes(':'));
      const main4 = v4.find(a => a.primary) || v4[0], more4 = v4.filter(a => a !== main4 && !a.channel), chans = ips.filter(a => a.channel);
      const kv = (k, v, c = '') => `<div><dt>${k}</dt><dd${c ? ` class="${c}"` : ''}>${v}</dd></div>`;
      const gb = b => (b / 1073741824).toFixed(b < 10737418240 ? 1 : 0);
      const ev = L.ev || [];
      return pageHead('Обзор', 'Состояние сервера и ноды loft. Карточки ведут в разделы.') + `<div class="cards">
        <section class="card"><div class="card-h"><h2>Нода loft</h2>${!st ? pill('нет данных') : st === 'active' ? pill('работает', 'ok') : pill(st, 'warn')}${L.can('node', 1) ? '<a href="#node" style="margin-left:auto">Подробно</a>' : ''}</div>
          <dl class="kv">${kv('Роль', esc(nd.role || '—'))}${kv('Адрес', esc(host || '—'), 'mono')}${kv('Слушает', esc(nd.listen || '—'), 'mono')}${kv('Фронты', esc((nd.fronts || []).join(', ') || 'get'))}
          ${kv('Прочий трафик', esc((nd.decoy || []).join(', ') || 'не пересылается'), 'mono')}${kv('Сертификат', cert)}${kv('Версия', esc(L.node && L.node.version || '—'))}${kv('Работает с', esc(since(L.node && L.node.since)))}</dl></section>
        <section class="card"><div class="card-h"><h2>Члены сети</h2><a href="#members" style="margin-left:auto">Все</a></div>
          <div style="display:flex;gap:32px;margin-bottom:16px"><div><div class="big num">${all.filter(m => m.authorized).length}</div><div class="mute">включено</div></div><div><div class="big num">${all.filter(m => (EXTRA[m.id] || [])[3]).length}</div><div class="mute">в сети</div></div><div><div class="big num">${all.filter(m => !m.authorized).length}</div><div class="mute">выключено</div></div></div>
          ${L.can('members') ? `<button class="btn pri" type="button" onclick="openMember('new')">${ic('i-plus')}Добавить члена</button>` : ''}</section>
        <section class="card"><div class="card-h"><h2>Интернет</h2>${L.can('network', 1) ? '<a href="#adapters" style="margin-left:auto">Подробно</a>' : ''}</div>
          <dl class="kv">${kv('Основной IP', esc(main4 ? main4.ip : '—'), 'mono')}${more4.length ? kv('Доп. IP', esc(more4.map(a => a.ip).join(', ')), 'mono') : ''}${v6.length ? kv('IPv6', esc(v6.filter(a => !a.channel).map(a => a.ip).join(', ') || '—'), 'mono') : ''}${chans.map(a => kv('Канал «' + esc(a.channel) + '»', esc(a.ip), 'mono')).join('')}</dl></section>
        ${L.can('domains', 1) ? `<section class="card"><div class="card-h"><h2>Домены</h2><a href="#domains" style="margin-left:auto">Все</a></div>${domBody}</section>` : ''}
        <section class="card"><div class="card-h"><h2>Сервер</h2><span class="mute" style="margin-left:auto">${esc([sv.os, sv.arch].filter(Boolean).join(' · '))}</span></div>
          <dl class="kv">${kv('CPU', sv.mem_total ? Math.round(sv.cpu) + '%' : '—', 'num')}${kv('Память', sv.mem_total ? gb(sv.mem_used) + ' / ' + gb(sv.mem_total) + ' ГБ' : '—', 'num')}${kv('Диск', sv.disk_size ? gb(sv.disk_used) + ' / ' + gb(sv.disk_size) + ' ГБ' : '—', 'num')}
          ${kv(!sv.hours || sv.hours >= 23.5 ? 'Трафик за сутки' : 'Трафик за ' + Math.max(1, Math.round(sv.hours || 0)) + ' ч', sv.hours ? '↓ ' + gb(sv.rx) + ' ГБ · ↑ ' + gb(sv.tx) + ' ГБ' : '—', 'num')}</dl></section>
        ${L.can('users', 1) ? `<section class="card"><div class="card-h"><h2>События</h2><a href="#users" style="margin-left:auto" onclick="S.tab.users='log'">Журнал</a></div>
          ${ev.slice(0, 5).map(x => `<div class="ev"><time>${when2(x.at || x.time)}</time><span><b>${esc(whoText(x.user))}</b> ${esc(opText(x))}</span></div>`).join('') || '<p class="mute" style="margin:0">Событий пока нет.</p>'}</section>` : ''}
      </div>`;
    };
    // the config text is checked by the node's own parser too: its word is final
    const cfgInput0 = window.cfgInput;
    window.cfgInput = el => {
      cfgInput0(el);
      clearTimeout(L._cc);
      const text = el.value, id = curNet().id;
      L._cc = setTimeout(async () => {
        let r;
        try { r = await api('POST', '/api/network/check', { text }); } catch (e) { return; }
        if (!$('#cfgTx') || $('#cfgTx').value !== text || r.ok) return;
        const m = /^line (\d+): (.*)$/.exec(r.error || ''), line = m ? +m[1] : 0, msg = ru(m ? m[2] : r.error);
        CFG[id] = { text, err: { line, msg } };
        $('#cfgSt').innerHTML = `<span class="pill warn">ошибка</span> Нода: ${line ? 'строка ' + line + ': ' : ''}${esc(msg)}`;
        $('.cfg').classList.add('bad');
        const ln = line && $('#cfgLn').children[line - 1]; if (ln) ln.classList.add('e');
      }, 600);
    };
    // search: what this role may see, from live data (loaded on first open)
    window.searchItems = () => {
      const o = [];
      for (const g of MENU) for (const [k, t, pkg] of g.items) if ((!pkg || pkgOn(pkg)) && L.can(netRight(k), 1)) o.push({ g: 'Разделы', t, go: () => go(k) });
      o.push({ g: 'Разделы', t: 'Личный кабинет', go: () => go('me') });
      if (L.can('members', 1)) NETS.forEach(n => n.members.forEach(m => o.push({ g: 'Члены сети', t: m.id, s: m.name || '', go: () => goThen('members', () => openMember(m.id)) })));
      if (L.ch) L.ch.channels.forEach(c => o.push({ g: 'Внешние каналы', t: c.title || c.name, s: c.name, go: () => goThen('channels', () => L.openChan(c.name)) }));
      if (L.dm) L.dm.zones.forEach(z => o.push({ g: 'Домены', t: z.name, go: () => go('domains/' + z.name) }));
      if (L.ml) L.ml.boxes.forEach((b, i) => o.push({ g: 'Почта', t: b.address, go: () => { L.mlDom = b.address.split('@')[1]; goThen('mail', () => L.openBox(i)); } }));
      if (L.us) L.us.users.forEach(u => o.push({ g: 'Пользователи', t: u.login, s: u.name || '', go: () => goThen('users', () => L.openUser(u.login)) }));
      if (L.pk) L.pk.packages.forEach(p => o.push({ g: 'Пакеты', t: PT[p.key] || p.key, s: p.installed ? 'установлен' : '', go: () => go('packages') }));
      return o;
    };
    const openSearch0 = window.openSearch;
    window.openSearch = () => {
      openSearch0();
      const want = [[!L.ch && L.can('network', 1), loadChannels], [!L.dm && L.can('domains', 1), loadDomains], [!L.ml && L.can('mail', 1) && pkgOn('Почтовый сервер'), loadMail], [!L.us && L.can('users', 1), loadUsers], [!L.pk && L.can('packages', 1), loadPackages]].filter(x => x[0]);
      if (want.length) Promise.allSettled(want.map(x => x[1]())).then(() => { if ($('#sq')) searchDraw(); });
    };
    const segments = V.segments;
    V.segments = () => segments()
      .replace(/<button type="button" class="btn" onclick="openNet\('new'\)">[^]*?<\/button>/, ''); // the node holds one network
    V.setup = wizardView;
    const login = V.login; // the mock's sample name → this server's
    V.login = () => login().replace('<span>vpn.example.net</span>', `<span>${esc(location.hostname)}</span>`);
  }

  /* ── sign-in ── */
  window.submitLogin = async (f) => {
    const btn = f.querySelector('button[type=submit]');
    btn.disabled = true;
    try {
      signedIn(await api('POST', '/api/login', { login: f.querySelector('#lg1').value.trim(), password: f.querySelector('#lg2').value, remember: !!(f.querySelector('#lgRemember') && f.querySelector('#lgRemember').checked) }));
    } catch (e) {
      toast(e.message);
    } finally { btn.disabled = false; }
  };
  // systemd ActiveEnterTimestamp "Tue 2026-10-20 10:22:29 UTC" → local time
  const since = v => { const m = String(v || '').match(/(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2}:\d{2}) (UTC|[+-]\d{2,4})?/); if (!m) return v || '—'; const d = new Date(m[1] + 'T' + m[2] + (m[3] === 'UTC' || !m[3] ? 'Z' : m[3].length === 3 ? m[3] + ':00' : m[3])); return isNaN(d) ? v : d.toLocaleString(LOC(), { dateStyle: 'short', timeStyle: 'short' }); };
  L.since = since;
  const showHost = () => { const h = $('.hdr-host'); if (h) h.textContent = (L.info && L.info.host) || location.hostname; };
  async function signedIn(me, st) {
    L.me = me; L.csrf = me.csrf; L.pk = null;
    loadPackages().then(() => { if (S.route !== 'packages') render(); }); // the menu shows Mail only with the mail server
    $('#userBtn').textContent = me.login + ' · ' + ({ owner: 'владелец', operator: 'оператор', viewer: 'наблюдатель' }[me.role] || me.role_name || me.role);
    await loadData();
    if (me.role === 'owner' && (st ? st.wizard : !(L.wz && L.wz.done))) {
      // Resume at the first step not done yet (names, certificate, member).
      const w = L.wz || {};
      const certs = w.cert_until && (!w.panel_domain || w.panel_cert_until);
      S.wz.step = w.members > 0 ? 5 : certs ? 4 : w.domain ? 3 : 2;
      go('setup'); render(); return;
    }
    // The section asked for in the address, once signed in.
    const w0 = (L.want || '').split('/')[0], want = w0 && V[w0] && !FULL[w0] ? L.want : 'main';
    L.want = '';
    go(want);
    if (location.hash.slice(1) === want) render();
  }
  window.goLogout = async () => {
    try { await api('POST', '/api/logout', {}); } catch (e) { /* already out */ }
    location.replace('#login'); location.reload();
  };
  window.goOut = window.goLogout;
  document.querySelectorAll('[onclick="go(\'login\')"]').forEach(b => b.setAttribute('onclick', 'goLogout()'));

  /* ── data ── */
  async function loadOverview() {
    const [ov, ev] = await Promise.allSettled([api('GET', '/api/overview'), L.can('users', 1) ? api('GET', '/api/audit') : Promise.resolve([])]);
    if (ov.status === 'fulfilled') L.ov = ov.value;
    if (ev.status === 'fulfilled') L.ev = ev.value || [];
  }
  async function loadData() {
    const calls = [api('GET', '/api/network'), api('GET', '/api/node')];
    if (L.me && L.me.role === 'owner') calls.push(api('GET', '/api/wizard'));
    const [net, node, wz] = await Promise.allSettled(calls);
    if (node.status === 'fulfilled') L.node = node.value;
    if (wz && wz.status === 'fulfilled') { L.wz = wz.value; L.info = wz.value.node || null; }
    showHost();
    if (net.status === 'fulfilled') {
      const n = net.value;
      L.sha = n.sha256 || '';
      NETS.length = 0;
      Object.keys(APPLIED).forEach(k => delete APPLIED[k]);
      Object.keys(CFG).forEach(k => delete CFG[k]);
      if (n.text) {
        try {
          const p = parseConf(n.text);
          NETS.push(p);
          APPLIED[p.id] = renderConf(p); // the panel writes the node's canonical form
          S.net = p.id;
        } catch (e) { toast('network.conf на сервере не разобран панелью: ' + (e.msg || e)); }
      }
    } else toast('Сеть ноды не прочитана: ' + net.reason.message);
    await Promise.all([loadPresence(), loadOverview(), L.can('domains', 1) ? loadDomains() : Promise.resolve()]);
    render();
  }

  // Writes the draft network.conf and reloads the node. True when the node
  // took it. Switches and dialogs have no Save: they look stored, so they
  // must hit the node (draft-only vanished on the next sign-in).
  // One apply at a time; a mutation during the job is applied after it.
  async function applyNow() {
    if (!NETS[0]) return false;
    let ok = false, sent = '';
    const run = async () => {
      const n = NETS[0];
      if (!n || !changed().length) { ok = true; return; }
      sent = renderConf(n);
      try {
        const r = await job('/api/network/apply', { text: sent, expect: L.sha });
        L.sha = r.sha256; APPLIED[n.id] = r.text;
        Object.keys(CFG).forEach(k => delete CFG[k]);
        api('GET', '/api/node').then(s => { L.node = s; }).catch(() => {});
        ok = true;
      } catch (e) { toast(e.message); ok = false; }
    };
    const p = (L._netQ || Promise.resolve()).then(run, run);
    L._netQ = p.then(() => {}, () => {});
    await p;
    if (ok && sent && NETS[0] && renderConf(NETS[0]) !== sent) return applyNow();
    return ok;
  }
  async function commitNet(okMsg) {
    if (!NETS[0] || !changed().length) return true;
    const ok = await applyNow();
    if (ok && okMsg) toast(okMsg);
    render();
    return ok;
  }
  // Mock mutators only touch NETS. After each one, push the draft to the node.
  function afterNet(name, msg) {
    const fn = window[name];
    if (typeof fn !== 'function') return;
    window[name] = function (...a) {
      const n = NETS[0];
      const before = n ? renderConf(n) : '';
      const ret = fn.apply(this, a);
      const n2 = NETS[0];
      if (n2 && renderConf(n2) !== before && renderConf(n2) !== APPLIED[n2.id]) commitNet(msg);
      return ret;
    };
  }
  afterNet('delMember', 'Член удалён');
  afterNet('saveNet');
  afterNet('saveVlan');
  afterNet('addSeg');
  afterNet('saveSeg');
  afterNet('delVlan');
  const saveSeg0 = window.saveSeg;
  window.saveSeg = function (id) {
    const el = document.getElementById('sgVid');
    const want = id && el ? +String(el.value || '').trim() : 0;
    saveSeg0(id);
    if (!id || !want || want === id || !L.ch || !L.ch.rules) return;
    const n = NETS[0];
    if (!n || !n.vlans.some(v => v.id === want) || n.vlans.some(v => v.id === id)) return;
    const R = L.ch.rules.rules.map(r => (r.match === 'vlan' && String(r.value) === String(id))
      ? Object.assign({}, r, { value: String(want) }) : r);
    if (!R.some((r, i) => r.value !== L.ch.rules.rules[i].value)) return;
    api('POST', '/api/channels/rules', { rules: R, default: L.ch.rules.default || 'direct' })
      .then(ch => { L.ch = ch; })
      .catch(e => toast(e.message));
  };
  afterNet('saveRule');
  afterNet('mvRule');
  afterNet('dropRule');
  afterNet('bulkAuth');
  afterNet('bulkVlan');
  afterNet('cellVlan');
  afterNet('joinSeg');
  afterNet('renameSeg');
  afterNet('setMemberAuth');
  const delVlan0 = window.delVlan;
  window.delVlan = function (id) {
    delVlan0(id);
    if (!L.ch || !L.ch.rules) return;
    const R = L.ch.rules.rules.filter(r => !(r.match === 'vlan' && String(r.value) === String(id)));
    if (R.length !== L.ch.rules.rules.length) saveRules(R);
  };
  window.applyAll = async () => {
    closeDlg();
    toast('Применяю…');
    if (await applyNow()) toast('Применено: нода перечитала сеть, переподключились только затронутые члены');
    render();
  };
  window.revertAll = () => loadData().then(() => toast('Черновик сброшен к тому, что работает'));

  /* a new member from the Members screen: real key, then apply so the node
     and the next page load both keep the row (draft-only vanished on sign-in). */
  const saveMember0 = window.saveMember;
  window.saveMember = async (id) => {
    if (id) {
      saveMember0(id);
      await commitNet();
      return;
    }
    const nid = $('#m2').value.trim();
    if (findMember(nid)[1]) { toast('Такой член уже есть'); return; }
    let k;
    try { k = await api('POST', '/api/members/key', { id: nid }); } catch (e) { toast(e.message); return; }
    saveMember0(null);
    const [, m] = findMember(nid);
    if (!m) return;
    m.key_sha256 = k.key_sha256;
    toast('Применяю…');
    const ok = await applyNow();
    render();
    showKey(nid, k, ok
      ? (L.keepKeys ? 'Ключ хранится в панели — его можно показать снова. Нода уже пускает этот ключ.' : 'Нода уже пускает этот ключ.')
      : (L.keepKeys ? 'Ключ хранится в панели. После «Применить» нода начнёт пускать этот ключ.' : 'После «Применить» нода начнёт пускать этот ключ.'));
  };
  // The key with its link, QR code (drawn by the panel) and .loft file.
  function showKey(id, k, note) {
    L.kf = { id, file: k.file };
    const head = L.keepKeys ? note : ('Показан один раз. ' + note);
    dialog('Ключ члена ' + esc(id), head, `
      ${k.link ? `<div class="cards" style="grid-template-columns:repeat(auto-fill,minmax(min(220px,100%),1fr))">${k.qr ? `<div class="qr" style="align-self:start">${k.qr}</div>` : ''}
        <div style="display:flex;flex-direction:column;gap:12px;min-width:0"><div class="dlg-sec" style="margin:0">Ссылка для приложения loft</div><div class="uri" id="klink">${esc(k.link)}</div>
        <p class="hint" style="margin:0">Откройте приложение loft на устройстве и отсканируйте код или вставьте ссылку.</p></div></div>` : ''}
      <div class="dlg-sec" style="margin:0">Ключ</div><div class="uri" id="kkey">${esc(k.key)}</div>
      <div class="acts" style="margin:0">${k.link ? `<button class="btn sm pri" type="button" onclick="LIVE.copy('#klink')">Копировать ссылку</button>` : ''}${k.file ? `<button class="btn sm" type="button" onclick="LIVE.download(LIVE.kf.id + '.loft', LIVE.kf.file)">${ic('i-down', 16)}Скачать .loft</button>` : ''}<button class="btn sm" type="button" onclick="LIVE.copy('#kkey')">Копировать ключ</button></div>`,
      `<button class="btn pri" type="button" onclick="closeDlg()">Готово</button>`);
  }
  window.showStoredKey = id => L.viewKey(id);
  L.viewKey = async id => {
    let k;
    try { k = await api('GET', '/api/members/key?id=' + encodeURIComponent(id)); } catch (e) { toast(e.message); return; }
    showKey(id, k, 'Ключ хранится в панели — его можно показать снова.');
  };
  window.reissueKey = id => L.reissue(id);
  L.reissue = id => {
    if (L.keepKeys) {
      dialog('Перевыпустить ключ?', 'Новый ключ заменит сохранённый и сразу применится на ноде. Старый перестанет действовать.', '',
        `<button class="btn danger" type="button" onclick="LIVE.reissueGo('${esc(id)}')">Перевыпустить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
      return;
    }
    L.reissueGo(id);
  };
  L.reissueGo = async id => {
    closeDlg();
    let k;
    try { k = await api('POST', '/api/members/key', { id }); } catch (e) { toast(e.message); return; }
    const [, m] = findMember(id);
    if (m) m.key_sha256 = k.key_sha256;
    toast('Применяю…');
    const ok = await applyNow();
    render();
    showKey(id, k, ok
      ? (L.keepKeys ? 'Новый ключ сохранён в панели. Нода уже пускает его, старый больше не действует.' : 'Нода уже пускает новый ключ, старый больше не действует.')
      : (L.keepKeys ? 'Новый ключ сохранён в панели. После «Применить» нода начнёт пускать его, старый перестанет действовать.' : 'После «Применить» нода начнёт пускать новый ключ, старый перестанет действовать.'));
  };
  // The page is served by the panel itself, so a blob link downloads normally.
  L.download = (name, text) => {
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([text], { type: 'application/x-yaml' }));
    a.download = name.replace(/[^A-Za-z0-9._-]+/g, '-');
    document.body.appendChild(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  };
  L.copy = (sel) => { const t = $(sel).textContent || $(sel).value; navigator.clipboard.writeText(t).then(() => toast('Скопировано'), () => toast('Выделите и скопируйте вручную')); };

  /* ── packages: the catalog lives in loft-panel-adm; titles and notes come
     from the mock's PKG table. The job runs in its own unit on the server,
     so it goes on when this page or the panel restarts. ── */
  const PT = { node: 'Нода loft', nginx: 'Веб-сервер', sstp: 'SSTP-клиент', mtproto: 'Прокси Telegram MTProto', wireguard: 'WireGuard-клиент', openvpn: 'OpenVPN-клиент', zerotier: 'ZeroTier-клиент', fail2ban: 'fail2ban', proxy: 'Прокси-клиент', xray: 'Xray-клиент', mail: 'Почтовый сервер', antispam: 'Антиспам', webmail: 'Веб-почта' };
  // what needs what (the catalog's Requires)
  const PREQ = { antispam: 'mail', webmail: 'mail' };
  const deps = k => Object.keys(PREQ).filter(d => PREQ[d] === k);
  const PG = { base: 'Основа', proto: 'Серверы протоколов', vpn: 'VPN-клиенты · вышестоящие каналы', mail: 'Почта', protect: 'Защита' };
  const pTitle = k => PT[k] || k;
  const pNote = k => ((PKG.find(p => p[0] === PT[k]) || [])[1] || '');
  const upstream = v => String(v || '').replace(/^\d+:/, '').replace(/-[^-]*$/, '');
  const owner = () => L.me && L.me.role === 'owner';
  L.pk = null; L.upd = null;
  const jobRunning = () => !!(L.pk && L.pk.job && L.pk.job.state === 'running');

  async function loadPackages() {
    const [pk, up] = await Promise.allSettled([api('GET', '/api/packages'), api('GET', '/api/update')]);
    if (pk.status === 'fulfilled') L.pk = pk.value; else toast('Пакеты не прочитаны: ' + pk.reason.message);
    if (up.status === 'fulfilled') L.upd = up.value;
    if (jobRunning()) watchJob();
  }
  L.loadPackages = loadPackages;
  // Opening Packages or Settings always reads the server again.
  window.addEventListener('hashchange', () => {
    if (location.hash === '#packages' && L.me) loadPackages().then(() => { if (S.route === 'packages') render(); });
    if ((S.route === 'channels' || S.route === 'network') && L.me) loadChannels().then(() => { if (S.route === 'channels' || S.route === 'network') render(); });
    if ((S.route === 'adapters' || S.route === 'addrs') && L.me) loadNet().then(() => { if (S.route === 'adapters' || S.route === 'addrs') render(); });
    if (S.route === 'iface-priorities' && L.me) loadChannels().then(() => { if (S.route === 'iface-priorities') render(); });
    if (S.route === 'domains' && L.me) loadDomains().then(() => { if (S.route === 'domains') render(); });
    if (location.hash === '#node' && L.me) loadNode().then(() => { if (S.route === 'node') render(); });
    if (['#members', '#segments', '#main', ''].includes(location.hash) && L.me) loadPresence().then(() => render());
    if (location.hash === '#system' && L.me && L.sy) loadSystem().then(() => { if (S.route === 'system') render(); });
    if (location.hash === '#mail' && L.me && L.ml) loadMail().then(() => Promise.all([loadMailDNS(), loadMailStats()])).then(() => { if (S.route === 'mail') render(); });
    if (S.route === 'me' && L.me) { L.my = null; render(); }
    if (S.route === 'main' && L.me) loadOverview().then(() => { if (S.route === 'main') render(); });
    if (location.hash === '#users' && L.me) { L.sess = L.log = null; loadUsers().then(() => { if (S.route === 'users') render(); }); }
    if (location.hash === '#settings' && L.me && L.can('settings', 1)) loadSettings().then(() => { if (S.route === 'settings') render(); });
  });

  const verb = { install: 'установка', remove: 'удаление', upgrade: 'обновление' };
  const cap = s => s.charAt(0).toUpperCase() + s.slice(1);
  function jobWhat(j) {
    if (j.kind === 'update') return `Обновление до ${esc(j.tag || '')}`;
    if (j.kind === 'backup') return 'Резервная копия';
    if (j.kind === 'restore') return 'Восстановление из копии';
    const p = j.plan || {}, parts = [];
    for (const k of ['remove', 'install', 'upgrade']) if ((p[k] || []).length) parts.push(verb[k] + ' ' + p[k].map(x => '«' + esc(pTitle(x)) + '»').join(', '));
    return cap(parts.join('; ') || 'задание');
  }
  const logTail = s => esc(String(s || '').split('\n').slice(-40).join('\n').trimEnd());
  function jobCard() {
    const j = L.pk && L.pk.job;
    if (!j) return '';
    const ended = j.ended ? new Date(j.ended) : null;
    if (j.state === 'running') {
      const d = j.kind === 'update' ? `${jobWhat(j)} — идёт. Панель перезапустится, страница переподключится сама.` : `${jobWhat(j)} — идёт, это займёт минуты.`;
      return blk('Выполняется сейчас', d, `<div class="bar"><i></i></div><div class="joblog" id="pjlog">${logTail(j.log)}</div>`);
    }
    if (j.state === 'done' && !(ended && Date.now() - ended < 600e3)) return '';
    const st = j.state === 'done' ? pill('выполнено', 'ok') : pill(j.state === 'interrupted' ? 'прервано' : 'ошибка', 'warn');
    return blk('Последнее задание', `${jobWhat(j)}${ended ? ' · ' + ended.toLocaleString(LOC(), { dateStyle: 'short', timeStyle: 'short' }) : ''}`,
      `<p class="note-s">${st} ${j.error ? esc(ru(j.error)) : ''}</p><details class="adv"${j.state === 'done' ? '' : ' open'}><summary>Журнал</summary><div class="joblog" style="margin-top:12px">${logTail(j.log)}</div></details>`);
  }

  let watching = false;
  async function watchJob() {
    if (watching) return;
    watching = true;
    const kind = L.pk.job.kind;
    try {
      for (let lost = 0; ; ) {
        await sleep(2000);
        let j;
        try { j = await api('GET', '/api/packages/job'); lost = 0; } catch (e) { if (e.status === 401) return; if (++lost > 150) return; continue; }
        L.pk.job = j;
        const el = $('#pjlog');
        if (j.state === 'running') { if (el && S.route === 'packages') { el.innerHTML = logTail(j.log); el.scrollTop = el.scrollHeight; } continue; }
        if (kind === 'update' && j.state === 'done') { toast('Обновлено — перезагружаю страницу'); await sleep(1200); location.reload(); return; }
        toast(j.state === 'done' ? 'Готово: ' + jobWhat(j).toLowerCase() : 'Задание не выполнено: ' + ru(j.error || ''));
        await loadPackages();
        if (S.route === 'packages') render();
        return;
      }
    } finally { watching = false; }
  }

  async function startPlan(plan) {
    closeDlg();
    try {
      const j = await api('POST', '/api/packages/apply', plan);
      if (L.pk) L.pk.job = j;
      toast('Задание поставлено в очередь: смотрите «Выполняется сейчас»');
      await loadPackages();
      render();
    } catch (e) { toast(e.message); }
  }
  L.pkgInstall = k => startPlan({ install: [k] });
  L.pkgUpgrade = k => startPlan({ upgrade: [k] });
  const withDeps = k => [k, ...deps(k).filter(d => L.pk.packages.some(p => p.key === d && p.installed))];
  L.pkgRemove = k => startPlan({ remove: withDeps(k), purge: !!($('#pkwipe') && $('#pkwipe').checked) });
  L.pkgAskRemove = k => dialog(`Удалить «${esc(pTitle(k))}»?`, withDeps(k).length > 1 ? `Пакет удаляется с сервера. Вместе с ним удалятся: ${withDeps(k).slice(1).map(d => '«' + esc(pTitle(d)) + '»').join(', ')}.` : 'Пакет удаляется с сервера.',
    `<label class="chk"><input id="pkwipe" type="checkbox"><span>Стереть данные пакета<span class="hint" style="display:block;margin:0">Без галочки данные (настройки, ключи) остаются и вернутся при повторной установке.</span></span></label>`,
    `<button class="btn danger" type="button" onclick="LIVE.pkgRemove('${k}')">Удалить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.mtpOpen = async () => {
    try { L.mtp = await api('GET', '/api/packages/mtproto'); } catch (e) { toast(e.message); return; }
    const s = L.mtp || {};
    if (!s.installed) { toast('Прокси Telegram MTProto не установлен'); return; }
    dialog('Прокси Telegram MTProto', 'Клиенты добавляют этот прокси в Telegram: Настройки → Данные и память → Прокси.', `
      <div class="fld"><label for="mtp-host">Сервер</label><input id="mtp-host" class="mono" value="${esc(s.host || '')}" readonly></div>
      <div class="fgrid" style="grid-template-columns:1fr 1fr"><div class="fld"><label for="mtp-port">Порт</label><input id="mtp-port" class="num" value="${s.port || 8448}"></div>
       <div class="fld"><label for="mtp-dom">TLS-домен</label><input id="mtp-dom" class="mono" value="${esc(s.domain || '')}"></div></div>
      <div class="hint">Порт не должен совпадать с 80, 443 и 8443 (веб-сервер и нода). TLS-домен входит в секрет: клиенты Telegram предъявляют его как SNI.</div>
      <div class="dlg-sec">Секрет</div>
      <div class="uri" id="mtp-sec">${esc(s.secret || '')}</div>
      <div class="acts" style="margin:0"><button class="btn sm" type="button" onclick="LIVE.copy('#mtp-sec')">Копировать секрет</button><button class="btn sm" type="button" onclick="LIVE.mtpSave(true)">Новый секрет</button></div>
      <div class="dlg-sec">Ссылка для Telegram</div>
      <div class="uri" id="mtp-link">${esc(s.link || '')}</div>
      <div class="uri" id="mtp-https" hidden>${esc(s.link_https || '')}</div>
      <div class="acts" style="margin:0"><button class="btn sm pri" type="button" onclick="LIVE.copy('#mtp-link')">Копировать ссылку</button>${s.link_https ? `<button class="btn sm" type="button" onclick="LIVE.copy('#mtp-https')">https://t.me/proxy</button>` : ''}</div>`,
      `<button class="btn pri" type="button" onclick="LIVE.mtpSave(false)">Сохранить</button><button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`);
  };
  L.mtpSave = async rotate => {
    const port = +($('#mtp-port') && $('#mtp-port').value || 0), domain = ($('#mtp-dom') && $('#mtp-dom').value || '').trim();
    try {
      L.mtp = await api('POST', '/api/packages/mtproto', { port, domain, rotate_secret: !!rotate });
      toast(rotate ? 'Секрет заменён' : 'Сохранено');
      L.mtpOpen();
    } catch (e) { toast(e.message); }
  };
  L.pkgUnit = async (k, on) => {
    try {
      const p = await api('POST', '/api/packages/unit', { key: k, on });
      const i = L.pk.packages.findIndex(x => x.key === k);
      if (i >= 0) L.pk.packages[i] = p;
      toast(on ? `«${pTitle(k)}» включён` : `«${pTitle(k)}» выключен`);
    } catch (e) { toast(e.message); }
    render();
  };

  function pkState(p) {
    if (p.busy) return pill({ install: 'устанавливается…', remove: 'удаляется…', upgrade: 'обновляется…' }[p.busy], 'busy');
    if (!p.installed) return '<span class="mute">не установлен</span>';
    if (!p.service) return pill('установлен', 'ok');
    if (p.active === 'active') return pill('работает', 'ok');
    if (p.active === 'failed') return pill('сбой', 'warn');
    if (!p.enabled) return pill('выключен');
    return pill(esc(p.active || 'нет данных'));
  }
  function pkRow(p) {
    const can = L.can('packages') && !jobRunning() && !p.busy;
    let a = '';
    if (p.key === 'node' || (p.key === 'mail' && p.installed)) a += `<button class="icb" type="button" aria-label="Настройки" onclick="go('${p.key === 'mail' ? 'mail' : 'node'}')">${ic('i-gear')}</button>`;
    if (p.key === 'mtproto' && p.installed && owner()) a += `<button class="icb" type="button" aria-label="Настройки" onclick="LIVE.mtpOpen()">${ic('i-gear')}</button>`;
    const parent = PREQ[p.key] && L.pk.packages.find(x => x.key === PREQ[p.key]);
    if (can && p.installed && p.candidate) a += `<button class="btn sm" type="button" onclick="LIVE.pkgUpgrade('${p.key}')">Обновить до ${esc(upstream(p.candidate) === upstream(p.version) ? p.candidate.replace(/^\d+:/, '') : upstream(p.candidate))}</button>`;
    if (can && !p.installed && (!parent || parent.installed)) a += `<button class="btn sm pri" type="button" onclick="LIVE.pkgInstall('${p.key}')">Установить</button>`;
    if (can && !p.installed && parent && !parent.installed) a += `<span class="mute" style="font-size:13px">нужен «${esc(pTitle(parent.key))}»</span>`;
    if (can && p.installed && p.service && !p.base) a += p.active === 'active' || p.enabled
      ? `<button class="btn sm" type="button" onclick="LIVE.pkgUnit('${p.key}',false)">Выключить</button>`
      : `<button class="btn sm" type="button" onclick="LIVE.pkgUnit('${p.key}',true)">Включить</button>`;
    a += can && p.installed && !p.base ? `<button class="icb del" type="button" aria-label="Удалить" onclick="LIVE.pkgAskRemove('${p.key}')">${ic('i-trash')}</button>` : `<span class="icb" aria-hidden="true" style="visibility:hidden"></span>`;
    const disk = p.disk_mb ? (p.installed ? '' : '≈ ') + p.disk_mb + ' MB' : '—';
    return `<tr><td><b>${esc(pTitle(p.key))}</b><span class="sub">${esc(pNote(p.key))}</span></td><td>${pkState(p)}</td><td class="mono mute">${p.installed && p.version ? esc(upstream(p.version)) : '—'}</td><td class="mono mute">${esc(p.ports || '—')}<br>${disk}</td><td class="r"><div class="cell-acts">${a}</div></td></tr>`;
  }

  const UM = [['auto', 'Устанавливать автоматически'], ['notify', 'Проверять и сообщать'], ['off', 'Не проверять']];
  const UC = [['stable', 'Только стабильные'], ['pre', 'Включая pre-release']];
  const UW = [['daily', 'Ежедневно 04:00–05:00'], ['sunday', 'По воскресеньям 04:00–05:00'], ['now', 'Сразу после выхода']];
  const opts = (l, v) => l.map(([k, t]) => `<option value="${k}" ${k === v ? 'selected' : ''}>${t}</option>`).join('');
  const when = v => v ? new Date(v).toLocaleString(LOC(), { dateStyle: 'short', timeStyle: 'short' }) : '—';
  function updBlock() {
    const u = L.upd;
    if (!u) return '';
    const state = u.error ? `<span style="color:var(--bad)">${esc(ru(u.error))}</span>` : !u.checked ? 'не проверялось' : u.available ? `доступна ${esc(u.latest.replace(/^v/, ''))}` : 'актуально';
    const dis = L.can('packages') ? '' : 'disabled';
    return blk('Обновление из релизов', 'github.com/a3app/loft · Releases. Нода и панель обновляются вместе; архив сверяется с SHA256SUMS, при сбое возвращается прежняя версия.',
      `<div class="card"><dl class="kv" style="margin-bottom:16px"><div><dt>Установлено</dt><dd class="mono">${esc(u.version)}</dd></div><div><dt>Последний релиз</dt><dd class="mono">${u.latest ? esc(u.latest.replace(/^v/, '')) + (u.latest_date ? ' · ' + new Date(u.latest_date).toLocaleDateString(LOC()) : '') : '—'}</dd></div><div><dt>Состояние</dt><dd>${state}</dd></div><div><dt>Последняя проверка</dt><dd>${when(u.checked)}</dd></div></dl>
       ${L.can('packages') ? `<div class="acts"><button class="btn" type="button" onclick="LIVE.updCheck(this)">Проверить сейчас</button>${u.available && !jobRunning() ? `<button class="btn pri" type="button" onclick="LIVE.updApply()">Обновить до ${esc(u.latest.replace(/^v/, ''))}</button>` : ''}</div>` : ''}
       <div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(min(260px,100%),1fr));gap:20px">
        <div class="fld"><label for="um">Режим</label><select id="um" ${dis} onchange="LIVE.updSave()">${opts(UM, u.mode)}</select></div>
        <div class="fld"><label for="uc">Канал</label><select id="uc" ${dis} onchange="LIVE.updSave()">${opts(UC, u.channel)}</select></div>
        <div class="fld"><label for="uw">Окно установки</label><select id="uw" ${dis} onchange="LIVE.updSave()">${opts(UW, u.window)}</select></div>
       </div></div>`);
  }
  L.updCheck = async b => {
    b.disabled = true;
    try { L.upd = await api('POST', '/api/update/check', {}); toast(L.upd.error ? ru(L.upd.error) : L.upd.available ? 'Есть новая версия' : 'Стоит последняя версия'); } catch (e) { toast(e.message); }
    render();
  };
  L.updSave = async () => {
    try { L.upd = await api('POST', '/api/update/settings', { mode: $('#um').value, channel: $('#uc').value, window: $('#uw').value }); toast('Сохранено'); } catch (e) { toast(e.message); }
    render();
  };
  L.updApply = () => dialog(`Обновить до ${esc(L.upd.latest.replace(/^v/, ''))}?`, 'Нода и панель перезапустятся: подключения прервутся на несколько секунд, страница переподключится сама. Если новая версия не поднимется, вернётся прежняя.', '',
    `<button class="btn pri" type="button" onclick="LIVE.updGo()">Обновить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.updGo = async () => {
    closeDlg();
    try { const j = await api('POST', '/api/update/apply', { tag: L.upd.latest }); L.pk = L.pk || { packages: [] }; L.pk.job = j; render(); watchJob(); } catch (e) { toast(e.message); }
  };

  function packagesView() {
    if (!L.pk) { loadPackages().then(() => { if (S.route === 'packages') render(); }); return pageHead('Пакеты', '') + '<p class="mute">Загружаю…</p>'; }
    const l = L.pk.packages, n = l.filter(p => !p.base);
    let grp = '', rows = '';
    for (const p of l) {
      if (p.group !== grp) { grp = p.group; rows += `<tr class="grp"><td colspan="5">${esc(PG[grp] || grp)}</td></tr>`; }
      rows += pkRow(p);
    }
    return pageHead('Пакеты', 'Ставится только то, что вы выбрали. Удаление сохраняет данные, пока вы не отметите «стереть».') + jobCard() + updBlock() +
      blk('Пакеты сервера', `${n.filter(p => p.installed).length} из ${n.length} установлено`,
        (L.can('packages') ? `<div class="acts"><button class="btn pri" type="button" ${jobRunning() ? 'disabled' : ''} onclick="openPicker()">Выбрать пакеты</button></div>` : '') +
        table(['Пакет', 'Состояние', 'Версия', 'Порты / диск', { t: '', r: 1 }], rows));
  }

  /* the picker, as in the mock: check what must be on the server */
  const P = { pick: null, q: '', wipe: false };
  function picker() {
    const l = L.pk.packages;
    if (!P.pick) P.pick = new Set(l.filter(p => !p.base && p.installed).map(p => p.key));
    const q = P.q.trim().toLowerCase();
    const add = l.filter(p => !p.base && !p.installed && P.pick.has(p.key)), del = l.filter(p => !p.base && p.installed && !P.pick.has(p.key));
    const mb = add.reduce((s, p) => s + (p.disk_mb || 0), 0);
    let grp = '', rows = '';
    for (const p of l) {
      const t = pTitle(p.key), d = pNote(p.key);
      if (PREQ[p.key] && !P.pick.has(PREQ[p.key])) continue;
      if (q && !(t + ' ' + d).toLowerCase().includes(q)) continue;
      if (p.group !== grp) { grp = p.group; rows += `<div class="pp-g">${esc(PG[grp] || grp)}</div>`; }
      const on = p.base || P.pick.has(p.key), chg = !p.base && on !== p.installed;
      const st = p.base ? 'основа' : p.installed ? 'установлен' : 'не установлен';
      rows += `<label class="pp ${PREQ[p.key] ? 'sub' : ''} ${chg ? 'chg' : ''}"><span class="pp-t"><b>${esc(t)}</b><span class="hint" style="margin:2px 0 0">${esc(d)}</span></span>
        <span class="pp-s">${chg ? (on ? 'поставить' : 'удалить') : st}</span><input type="checkbox" ${on ? 'checked' : ''} ${p.base ? 'disabled' : ''} aria-label="${esc(t)}" onchange="LIVE.pick('${p.key}',this.checked)"></label>`;
    }
    const all = l.filter(p => !p.base).map(p => p.key);
    dialog('Выбрать пакеты', 'Отметьте, что должно стоять на сервере. Основа не удаляется.', `
      <div class="pp-sets"><button type="button" class="btn sm" onclick="LIVE.pickSet(${esc(JSON.stringify(all))})">Всё</button><button type="button" class="btn sm ghost" onclick="LIVE.pickSet(null)">Как сейчас</button></div>
      <div class="fld"><label for="pq">Поиск</label><input id="pq" value="${esc(P.q)}" placeholder="wireguard, zerotier…" oninput="LIVE.pickQ(this.value)"></div>
      <div class="pp-list">${rows || '<p class="mute">Ничего не нашлось.</p>'}</div>
      ${del.length ? `<label class="chk"><input type="checkbox" ${P.wipe ? 'checked' : ''} onchange="LIVE.P.wipe=this.checked"><span>Стереть данные удаляемых пакетов<span class="hint" style="display:block;margin:0">Без галочки данные (настройки, ключи) остаются и вернутся при повторной установке.</span></span></label>` : ''}`,
      `<span class="pp-sum">${add.length || del.length ? `${add.length ? `поставить ${add.length} · ≈ ${mb} МБ` : ''}${add.length && del.length ? ' · ' : ''}${del.length ? `удалить ${del.length}` : ''}` : 'Изменений нет'}</span>
       <button class="btn pri" type="button" ${add.length || del.length ? '' : 'disabled'} onclick="LIVE.pickApply()">Применить</button><button class="btn" type="button" onclick="LIVE.P.pick=null;closeDlg()">Отмена</button>`);
  }
  L.P = P;
  L.pick = (k, on) => { if (on) P.pick.add(k); else { P.pick.delete(k); deps(k).forEach(d => P.pick.delete(d)); } picker(); };
  L.pickSet = l => { P.pick = l ? new Set(l) : null; if (!l) P.q = ''; picker(); };
  L.pickQ = v => { P.q = v; clearTimeout(L._pq); L._pq = setTimeout(() => { if (!$('#pq')) return; picker(); const e = $('#pq'); e.focus(); e.setSelectionRange(e.value.length, e.value.length); }, 200); };
  L.pickApply = () => {
    const l = L.pk.packages;
    const plan = { install: l.filter(p => !p.base && !p.installed && P.pick.has(p.key)).map(p => p.key), remove: l.filter(p => !p.base && p.installed && !P.pick.has(p.key)).map(p => p.key), purge: P.wipe };
    P.pick = null; P.wipe = false;
    startPlan(plan);
  };

  /* ── settings: the mock's blocks with the server's values ── */
  L.st = null; L.tz = null;
  const IDLE = [['30m', 'через 30 минут'], ['8h', 'через 8 часов'], ['off', 'не выходить']];
  const SMAX = [['7d', '7 дней'], ['30d', '30 дней']];
  const LANGS = [['en', 'English'], ['ru', 'Русский'], ['zh', '中文']];
  const SEC = [['starttls', 'STARTTLS'], ['tls', 'SSL/TLS'], ['none', 'Без шифрования']];
  const EV = [['cert', 'Сертификат истекает через 14 дней'], ['update', 'Установлено обновление или откат'], ['node', 'Нода loft не запустилась'], ['backup', 'Резервная копия по расписанию не удалась']];
  async function loadSettings() {
    const [st, tz] = await Promise.allSettled([api('GET', '/api/settings'), api('GET', '/api/settings/timezone')]);
    if (st.status === 'fulfilled') L.st = st.value; else toast('Настройки не прочитаны: ' + st.reason.message);
    if (tz.status === 'fulfilled') L.tz = tz.value;
  }
  function settingsView() {
    if (!L.can('settings', 1)) return pageHead('Настройки', '') + `<div class="empty"><div class="empty-ic">${ic('i-gear', 28)}</div><b>Нет доступа</b><p>Вашей роли этот раздел не виден.</p></div>`;
    if (!L.st) { loadSettings().then(() => { if (S.route === 'settings') render(); }); return pageHead('Настройки', '') + '<p class="mute">Загружаю…</p>'; }
    const st = L.st, n = st.notify, tz = L.tz;
    const zones = tz ? tz.zones.map(z => `<option ${z === tz.zone ? 'selected' : ''}>${esc(z)}</option>`).join('') : '<option>—</option>';
    return pageHead('Настройки', 'Настройки самой панели: адрес, язык, часовой пояс, вход, уведомления.') +
      blk('Мастер установки', 'Пройти шаги первой настройки заново: домен, сертификат, первый член сети, пакеты. Уже сделанное не стирается.', `<button class="btn" type="button" onclick="S.wz.step=2;go('setup')">Открыть мастер</button>`) +
      blk('Панель', '', grid(`<div><div class="fld"><label for="st1">Адрес панели</label><input id="st1" class="mono" value="${esc(st.panel || location.hostname)}" readonly></div><div class="hint">Имена и сертификаты меняются в мастере установки — шаги «Домен и DNS» и «Сертификат».</div></div>
        <div><div class="fld"><label for="st2">Язык по умолчанию</label><select id="st2" onchange="LIVE.stPanel()">${opts(LANGS, st.lang)}</select></div><div class="hint">Для страницы входа и тех, кто не выбрал язык сам.</div></div>
        <div><div class="fld"><label for="st3">Часовой пояс сервера</label><select id="st3" ${tz ? '' : 'disabled'} onchange="LIVE.stTz(this)">${zones}</select></div><div class="hint">По нему считается окно установки обновлений.</div></div>`) +
        `<div><label class="chk"><input id="stKeep" type="checkbox" ${st.keep_member_keys ? 'checked' : ''} onchange="LIVE.stPanel()"><span>Хранить ключи членов</span></label><div class="hint">Ссылка, QR-код и файл .loft открываются снова без перевыпуска. Копия лежит в каталоге данных панели, права 0600. Выключение стирает копию в панели; файл на ноде остаётся.</div></div>`) +
      blk('Файлы .loft', 'Какие части входят в имя исходящего в профиле члена. Ссылка loft:// не меняется.', loftOutboundBlock(st)) +
      blk('Вход', 'Блокировка адресов после неудачных входов — в <a href="#system">Системе</a>.', grid(`<div class="fld"><label for="st4">Выходить при бездействии</label><select id="st4" onchange="LIVE.stPanel()">${opts(IDLE, st.session_idle)}</select></div>
        <div class="fld"><label for="st5">Срок сессии</label><select id="st5" onchange="LIVE.stPanel()">${opts(SMAX, st.session_max)}</select></div>`)) +
      blk('Уведомления', 'Письма о событиях уходят через SMTP-сервер, указанный здесь.', `<div class="card">${grid(`<div class="fld"><label for="st6">Кому</label><input id="st6" type="email" value="${esc(n.to)}" placeholder="admin@example.net"></div>
        <div class="fld"><label for="st7">Отправитель</label><input id="st7" type="email" value="${esc(n.from)}" placeholder="panel@example.net"></div>
        <div class="fld"><label for="st8">SMTP-сервер</label><input id="st8" class="mono" value="${esc(n.host)}" placeholder="smtp.example.net"></div>
        <div class="fld"><label for="st9">Порт</label><input id="st9" class="mono" inputmode="numeric" value="${esc(n.port || 587)}"></div>
        <div class="fld"><label for="st10">Шифрование</label><select id="st10">${opts(SEC, n.security)}</select></div>
        <div class="fld"><label for="st11">Логин</label><input id="st11" value="${esc(n.user)}" autocomplete="off"></div>
        <div><div class="fld"><label for="st12">Пароль</label><input id="st12" type="password" autocomplete="new-password" placeholder="${n.has_password ? 'сохранён' : ''}" data-pw-optional></div><div class="hint">${n.has_password ? 'Пусто — оставить сохранённый.' : 'Если SMTP-сервер требует вход.'}</div></div>`)}
        <div style="display:flex;flex-direction:column;gap:4px;margin-top:16px">${EV.map(([k, t]) => `<label class="chk"><input type="checkbox" data-ev="${k}" ${n.events.includes(k) ? 'checked' : ''}>${t}</label>`).join('')}</div>
        <div class="acts" style="margin:16px 0 0"><button class="btn pri sm" type="button" onclick="LIVE.stNotify(this)">Сохранить</button><button class="btn sm" type="button" ${n.host && n.to ? '' : 'disabled'} onclick="LIVE.stTest(this)">Отправить пробное</button></div></div>`);
  }
  function loftObName(o, host, member, front) {
    const h = !!o.host, m = !!o.member, f = !!o.front || (!o.host && !o.member);
    const p = [];
    if (h && host) p.push(host);
    if (m && member) p.push(member);
    if (f && front) p.push(front);
    return p.join('-') || front;
  }
  function loftOutboundBlock(st) {
    const o = st.loft_outbound || { member: true, front: true };
    const host = (L.node && L.node.host) || st.node || location.hostname;
    const member = (NETS[0] && NETS[0].members[0] && NETS[0].members[0].id) || 'bob';
    const name = loftObName(o, host, member, 'get');
    return `<div class="chk-row">
      <label class="chk"><input id="stLoftHost" type="checkbox" ${o.host ? 'checked' : ''} onchange="LIVE.stLoft()"><span>Хост</span></label>
      <label class="chk"><input id="stLoftMember" type="checkbox" ${o.member ? 'checked' : ''} onchange="LIVE.stLoft()"><span>Член</span></label>
      <label class="chk"><input id="stLoftFront" type="checkbox" ${o.front ? 'checked' : ''} onchange="LIVE.stLoft()"><span>Фронт</span></label>
    </div>
    <pre class="mono" id="stLoftPrev" style="margin:12px 0 0">outbounds:\n  - name: ${esc(name)}</pre>
    <div class="hint">Меняется в новых файлах и при повторном скачивании, если ключи хранятся в панели.</div>`;
  }
  L.stLoft = () => {
    const o = { host: $('#stLoftHost').checked, member: $('#stLoftMember').checked, front: $('#stLoftFront').checked };
    if (!o.host && !o.member && !o.front) { $('#stLoftFront').checked = true; o.front = true; }
    if (L.st) L.st.loft_outbound = o;
    const prev = $('#stLoftPrev');
    if (prev) {
      const host = (L.node && L.node.host) || (L.st && L.st.node) || location.hostname;
      const member = (NETS[0] && NETS[0].members[0] && NETS[0].members[0].id) || 'bob';
      prev.textContent = 'outbounds:\n  - name: ' + loftObName(o, host, member, 'get');
    }
    L.stPanelGo($('#stKeep') ? $('#stKeep').checked : !!L.keepKeys);
  };
  L.stPanel = () => {
    const keep = $('#stKeep') ? $('#stKeep').checked : !!L.keepKeys;
    if (L.keepKeys && !keep) {
      dialog('Выключить хранение ключей?', 'Копия в панели будет стёрта. Файл на ноде останется. Ссылку, QR-код и .loft снова можно будет только перевыпустить.', '',
        `<button class="btn danger" type="button" onclick="LIVE.stPanelGo(false)">Выключить</button><button class="btn" type="button" onclick="closeDlg();render()">Отмена</button>`);
      return;
    }
    L.stPanelGo(keep);
  };
  L.stPanelGo = async keep => {
    closeDlg();
    try {
      const loft = $('#stLoftHost') ? {
        host: $('#stLoftHost').checked, member: $('#stLoftMember').checked, front: $('#stLoftFront').checked
      } : undefined;
      if (loft && !loft.host && !loft.member && !loft.front) loft.front = true;
      L.st = await api('POST', '/api/settings/panel', { session_idle: $('#st4').value, session_max: $('#st5').value, lang: $('#st2').value, keep_member_keys: keep, loft_outbound: loft });
      L.keepKeys = !!L.st.keep_member_keys;
      if (window.I18N) I18N.def(L.st.lang);
      toast('Сохранено');
    } catch (e) { toast(e.message); }
    render();
  };
  L.stTz = async sel => {
    sel.disabled = true;
    try { L.tz = await api('POST', '/api/settings/timezone', { zone: sel.value }); toast('Часовой пояс сервера: ' + L.tz.zone); } catch (e) { toast(e.message); }
    render();
  };
  L.stNotify = async b => {
    const port = parseInt($('#st9').value, 10) || 0;
    const body = { to: $('#st6').value.trim(), from: $('#st7').value.trim(), host: $('#st8').value.trim(), port, security: $('#st10').value,
      user: $('#st11').value.trim(), password: $('#st12').value, events: [...document.querySelectorAll('[data-ev]')].filter(i => i.checked).map(i => i.dataset.ev) };
    b.disabled = true;
    try { L.st = await api('POST', '/api/settings/notify', body); toast('Сохранено'); render(); } catch (e) { toast(e.message); b.disabled = false; }
  };
  L.stTest = async b => {
    b.disabled = true;
    try { await api('POST', '/api/settings/notify/test', {}); toast('Пробное письмо отправлено'); } catch (e) { toast(e.message); }
    b.disabled = false;
  };

  /* ── Network and IP: addresses and routes ── */
  L.net = null;
  const PROC = { 'loft-node': 'нода loft', nginx: 'nginx', 'loft-panel': 'панель', master: 'postfix', 'zerotier-one': 'zerotier' };
  function listenText(l) {
    const by = {};
    for (const x of l || []) {
      const m = x.match(/^(\S+) :(\d+)\/(\w+)$/);
      if (!m || m[1] === '?') continue; // a socket of another namespace: no process to name
      const n = PROC[m[1]] || m[1], p = ':' + m[2] + (m[3] === 'udp' ? ' udp' : '');
      (by[n] = by[n] || []).includes(p) || by[n].push(p);
    }
    const t = Object.entries(by).map(([n, ps]) => `${esc(n)} ${ps.join(', ')}`).join(' · ');
    return t || '<span class="mute">свободен</span>';
  }
  async function loadNet() {
    try { L.net = await api('GET', '/api/net'); } catch (e) { toast('Адреса не прочитаны: ' + e.message); L.net = L.net || { addrs: [], routes: [], devs: [] }; }
    pendTick();
  }
  let pendTimer = 0;
  function pendTick() {
    clearInterval(pendTimer);
    const p = L.net && L.net.pending;
    if (!p) return;
    pendTimer = setInterval(() => {
      const el = $('#npLeft'), left = Math.max(0, Math.round((new Date(p.deadline) - Date.now()) / 1000));
      if (el) el.textContent = Math.floor(left / 60) + ':' + String(left % 60).padStart(2, '0');
      if (left === 0) { clearInterval(pendTimer); setTimeout(() => loadNet().then(() => { if (S.route === 'adapters' || S.route === 'addrs' || S.route === 'network') render(); }), 3000); }
    }, 1000);
  }
  const chgText = c => ({ 'add-addr': `добавлен адрес ${c.addr}`, 'del-addr': `убран адрес ${c.addr}`, 'add-route': `добавлен маршрут ${c.dst}`, 'del-route': `убран маршрут ${c.dst}` }[c.op] || c.op);
  function netAddrLive() {
    const n = L.net, p = n.pending, own = L.can('network');
    const bar = p ? `<div class="note" role="status"><span><b>Применено: ${esc(chgText(p.change))}.</b> <span class="mute">Подтвердите, иначе через <b id="npLeft">2:00</b> изменение откатится само.</span></span>
      ${own ? `<span class="note-acts"><button class="btn sm ghost" type="button" onclick="LIVE.netHeld('revert')">Откатить сейчас</button><button class="btn sm pri" type="button" onclick="LIVE.netHeld('confirm')">Подтвердить</button></span>` : ''}</div>` : '';
    const del = (fn) => own && !p ? `<button class="icb del" type="button" aria-label="Удалить" onclick="event.stopPropagation();${fn}">${ic('i-trash')}</button>` : '';
    return bar + blk('Адреса сервера', 'Изменение адреса или маршрута откатывается само через 2 минуты, если его не подтвердить.',
      (own ? `<div class="acts"><button class="btn" type="button" ${p ? 'disabled' : ''} onclick="LIVE.openAddr()">${ic('i-plus')}Добавить IP</button></div>` : '') +
      table(['Адрес', 'Интерфейс', 'Шлюз', 'Кто слушает', 'PTR', { t: '', r: 1 }], n.addrs.map(a => `<tr><td class="mono">${esc(a.addr)}${a.primary ? ' <span class="mute">основной</span>' : ''}${a.panel ? ' <span class="mute">добавлен в панели</span>' : ''}</td><td class="mono">${esc(a.dev)}</td><td class="mono">${esc(a.gateway || '—')}</td><td>${listenText(a.listen)}</td><td class="mono mute">${esc(a.ptr || '—')}</td>
        <td class="r"><div class="cell-acts">${a.panel ? del(`LIVE.netDel('addr','${esc(a.addr)}','${esc(a.dev)}')`) : ''}</div></td></tr>`).join(''))) +
      blk('Маршруты', '', (own ? `<div class="acts"><button class="btn" type="button" ${p ? 'disabled' : ''} onclick="LIVE.openRoute()">${ic('i-plus')}Добавить маршрут</button></div>` : '') +
        table(['Назначение', 'Через', 'Интерфейс', 'Источник', { t: '', r: 1 }], n.routes.map(r => `<tr><td class="mono">${esc(r.dst)}</td><td class="mono">${esc(r.via || '—')}</td><td class="mono">${esc(r.dev)}</td><td class="mute">${{ system: 'система', panel: 'панель', channel: 'канал' }[r.source] || esc(r.source)}</td>
          <td class="r"><div class="cell-acts">${r.source === 'panel' ? del(`LIVE.netDel('route','${esc(r.dst)}','${esc(r.dev)}','${esc(r.via || '')}')`) : ''}</div></td></tr>`).join(''))) +
      `<p class="blk-d">Сети loft и их VLAN с диапазонами адресов — в разделе <a href="#segments">Сегменты сети</a>.</p>`;
  }
  const devOpts = () => (L.net.devs || []).map(d => `<option>${esc(d)}</option>`).join('');
  L.openAddr = () => dialog('Добавить IP', 'Адрес появится на сервере сразу. Если не подтвердить, через 2 минуты он уберётся сам.',
    `<div><div class="fld"><label for="na1">Адрес с префиксом</label><input id="na1" class="mono" placeholder="203.0.113.11/32"></div><div class="hint">Адрес, выданный хостером для этого сервера.</div></div>
     <div class="fld"><label for="na2">Интерфейс</label><select id="na2">${devOpts()}</select></div>`,
    `<button class="btn pri" type="button" onclick="LIVE.netAdd('add-addr')">Добавить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.openRoute = () => dialog('Добавить маршрут', 'Маршрут появится на сервере сразу. Если не подтвердить, через 2 минуты он уберётся сам.',
    `<div class="fld"><label for="nr1">Назначение</label><input id="nr1" class="mono" placeholder="198.51.100.0/24"></div>
     <div><div class="fld"><label for="nr2">Через (шлюз)</label><input id="nr2" class="mono" placeholder="203.0.113.1"></div><div class="hint">Пусто — прямо в интерфейс.</div></div>
     <div class="fld"><label for="nr3">Интерфейс</label><select id="nr3">${devOpts()}</select></div>`,
    `<button class="btn pri" type="button" onclick="LIVE.netAdd('add-route')">Добавить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.netAdd = async op => {
    const body = op === 'add-addr' ? { op, addr: $('#na1').value.trim(), dev: $('#na2').value } : { op, dst: $('#nr1').value.trim(), via: $('#nr2').value.trim(), dev: $('#nr3').value };
    try { await api('POST', '/api/net/change', body); closeDlg(); await loadNet(); render(); toast('Применено — подтвердите в течение 2 минут'); } catch (e) { toast(e.message); }
  };
  L.netDel = async (kind, x, dev, via) => {
    const body = kind === 'addr' ? { op: 'del-addr', addr: x, dev } : { op: 'del-route', dst: x, dev, via };
    try { await api('POST', '/api/net/change', body); await loadNet(); render(); toast('Применено — подтвердите в течение 2 минут'); } catch (e) { toast(e.message); }
  };
  L.netHeld = async what => {
    try { await api('POST', '/api/net/' + what, { id: L.net.pending.id }); toast(what === 'confirm' ? 'Изменение сохранено' : 'Изменение откачено'); } catch (e) { toast(e.message); }
    await loadNet(); render();
  };
  /* ── Network and IP: external channels and exit rules ── */
  L.ch = null;
  const CT = { wg: 'WireGuard', sstp: 'SSTP', ovpn: 'OpenVPN', zt: 'ZeroTier', px: 'Прокси', xr: 'Xray' };
  const STW = { up: ['работает', 'ok'], connecting: ['подключается…', 'busy'], down: ['не работает', 'warn'], nolink: ['нет связи', 'warn'], off: ['выключен', ''], denied: ['ждёт авторизации', 'warn'] };
  let chPend = null, chAt = 0;
  async function loadChannels() {
    if (L.ch && Date.now() - chAt < 5000) return;
    if (chPend) return chPend;
    chPend = (async () => {
      try {
        L.ch = await api('GET', '/api/channels');
        chAt = Date.now();
      } catch (e) {
        toast('Каналы не прочитаны: ' + e.message);
        L.ch = L.ch || { channels: [], status: [], rules: { rules: [], default: 'direct' }, prios: [], lists: [] };
      } finally { chPend = null; }
      if (!L.pk) loadPackages().then(() => {
        if (S.route === 'channels' || S.route === 'network' || S.route === 'iface-priorities' || S.route === 'adapters' || S.route === 'addrs') render();
      });
    })();
    return chPend;
  }
  const pkgHas = k => !!(L.pk && L.pk.packages.some(p => p.key === k && p.installed));
  const chHas = t => !!(L.ch && L.ch.channels.some(c => c.type === t));
  const showKind = (pkg, ...types) => pkgHas(pkg) || types.some(chHas);
  const chOf = n => L.ch.channels.find(c => c.name === n);
  const stOf = n => (L.ch.status || []).find(s => s.name === n) || { state: 'down' };
  const bytes = n => { n = n || 0; if (n < 1024) return n + ' Б'; const u = ['КБ', 'МБ', 'ГБ', 'ТБ']; let i = -1; do { n /= 1024; i++; } while (n >= 1024 && i < 3); return n.toFixed(n < 10 ? 1 : 0) + ' ' + u[i]; };
  const ago = t => { if (!t) return '—'; const s = Math.round(Date.now() / 1000 - t); return s < 60 ? s + ' с назад' : s < 3600 ? Math.round(s / 60) + ' мин назад' : Math.round(s / 3600) + ' ч назад'; };
  const tt = c => (c && (c.title || c.name)) || '';
  // Priority lists: channels in order, the first working one carries the traffic.
  const prOf = id => (L.ch.prios || []).find(p => p.id === id);
  const lastT = x => x === 'block' ? 'блокировать' : 'напрямую';
  const prName = v => !v || v === 'direct' ? 'напрямую' : v === 'block' ? 'блокировать' : (prOf(v) || {}).title || v;
  const isNative = n => n === 'direct';
  const nativeDev = () => ((L.net && L.net.addrs.find(a => a.primary)) || {}).dev || '';
  const connTitle = n => isNative(n) ? (nativeDev() || 'Напрямую') : (tt(chOf(n)) || n);
  const connState = n => {
    if (isNative(n)) {
      const d = nativeDev();
      const st = (L.ch.status || []).find(s => s.name === 'direct');
      const down = st && st.state === 'nolink';
      return `<span class="sub ${d && !down ? 'okt' : ''}"><span class="mono">${esc(d || 'direct')}</span>${down ? ' · адаптер · нет связи' : d ? ' · адаптер · работает' : ' · адаптер'}</span>`;
    }
    const c = chOf(n);
    return c ? chState(c) : `<span class="sub"><span class="mono">${esc(n)}</span></span>`;
  };
  const chUp = n => {
    if (isNative(n)) { const st = (L.ch.status || []).find(s => s.name === 'direct'); return !(st && st.state === 'nolink'); }
    const c = chOf(n); return !!(c && c.on && stOf(n).state === 'up');
  };
  const prLive = use => use.find(chUp);
  const prChain = p => p.use.map(n => `<span class="${chUp(n) ? '' : 'mute'}">${esc(connTitle(n))}</span>`).concat(`<span class="mute">${lastT(p.last)}</span>`).join(' → ');
  const prLink = id => prOf(id) ? `<span class="tail"><a href="#iface-priorities" onclick="event.stopPropagation();LIVE.toPrio('${esc(id)}')">${prChain(prOf(id))}</a></span>` : '';
  function chState(c) {
    const st = stOf(c.name), [t, k] = STW[st.state] || [st.state, ''];
    return `<span class="sub ${st.state === 'up' ? 'okt' : ''}"><span class="mono">${esc(c.name)}</span> · ${esc(t)}${st.detail ? ' · ' + esc(ru(st.detail)) : ''}</span>`;
  }
  const chSw = c => `<label class="sw" onclick="event.stopPropagation()"><input type="checkbox" ${c.on ? 'checked' : ''} ${L.can('network') ? '' : 'disabled'} aria-label="Включён" onchange="LIVE.chSwitch('${c.name}',this.checked)"><i></i></label>`;
  const chName = c => `<td>${chSw(c)}<span style="margin-left:10px"><b>${esc(tt(c))}</b>${chState(c)}</span></td>`;
  const chDel = c => L.can('network') ? `<td class="r"><div class="cell-acts"><button class="icb del" type="button" aria-label="Удалить канал" onclick="event.stopPropagation();LIVE.chAskDel('${c.name}')">${ic('i-trash')}</button></div></td>` : '<td></td>';
  function chTable(types) {
    const rows = L.ch.channels.filter(c => types.includes(c.type)), t = types[0];
    if (!rows.length) return '<p class="mute" style="margin:0">Подключений нет.</p>';
    const cols = {
      wg: [['Подключение', 'Адрес', 'Пир', 'Отправлено', 'Принято', 'Адрес и порт пира', 'Последнее рукопожатие'], (c, s) => `<td class="mono">${esc(c.wg.address)}${c.wg.listen_port ? ':' + c.wg.listen_port : ''}</td><td>${esc((c.wg.peers[0] || {}).name || '—')}</td><td class="num">${bytes(s.tx)}</td><td class="num">${bytes(s.rx)}</td><td class="mono">${esc(s.endpoint || (c.wg.peers[0] || {}).endpoint || '—')}</td><td>${esc(ago(s.handshake))}</td>`],
      sstp: [['Подключение', 'Тип VPN', 'Сервер', 'Отправлено', 'Принято', 'Адрес'], (c, s) => `<td>${c.type === 'sstp' ? 'SSTP' : 'OpenVPN'}</td><td class="mono">${esc(c.type === 'sstp' ? c.sstp.server : ((c.ovpn.config.match(/^\s*remote\s+(\S+)(?:\s+(\d+))?/m) || []).slice(1).filter(Boolean).join(':') || '—'))}</td><td class="num">${bytes(s.tx)}</td><td class="num">${bytes(s.rx)}</td><td class="mono">${esc((s.addrs || [])[0] || '—')}</td>`],
      zt: [['Подключение', 'Сеть', 'ID сети', 'Локальный ID', 'IP-адрес', 'Отправлено', 'Принято'], (c, s) => `<td>${esc(s.net_name || '—')}</td><td class="mono">${esc(c.zt.network)}</td><td class="mono">${esc(s.node_id || '—')}</td><td class="mono">${esc((s.addrs || [])[0] || '—')}</td><td class="num">${bytes(s.tx)}</td><td class="num">${bytes(s.rx)}</td>`],
      px: [['Подключение', 'Тип', 'Адрес', 'Вход', 'Отправлено', 'Принято', 'Через'], (c, s) => `<td>${{ http: 'HTTP', https: 'HTTPS', socks5: 'SOCKS5' }[c.px.kind]}</td><td class="mono">${esc(c.px.addr)}</td><td>${c.px.user ? 'логин' : 'нет'}</td><td class="num">${bytes(s.tx)}</td><td class="num">${bytes(s.rx)}</td><td>${c.via ? `<b>${esc(c.via)}</b>` : '<span class="mute">напрямую</span>'}</td>`],
      xr: [['Подключение', 'Сервер', 'Через', 'Отправлено', 'Принято'], (c, s) => `<td class="mono">${esc(xrServer(c.xr.link))}</td><td>${c.via ? `<b>${esc(c.via)}</b>` : '<span class="mute">напрямую</span>'}</td><td class="num">${bytes(s.tx)}</td><td class="num">${bytes(s.rx)}</td>`],
    }[t === 'ovpn' ? 'sstp' : t];
    return table(cols[0].concat([{ t: '', r: 1 }]), rows.map(c => `<tr class="click" onclick="LIVE.openChan('${c.name}')">${chName(c)}${cols[1](c, stOf(c.name))}${chDel(c)}</tr>`).join(''));
  }
  const xrServer = l => { const m = String(l).match(/@([^?#/]+)/); return m ? m[1] : '—'; };
  function channelsView() {
    if (!L.ch) { loadChannels().then(() => { if (S.route === 'channels' || S.route === 'network') render(); }); return '<p class="mute">Загружаю…</p>'; }
    const add = (t, l, x = '') => L.can('network') ? `<div class="acts"><button class="btn" type="button" onclick="LIVE.openChan('new:${t}')">${ic('i-plus')}${l}</button>${x}</div>` : '';
    const missing = L.pk ? [['wireguard', 'WireGuard'], ['openvpn', 'OpenVPN'], ['zerotier', 'ZeroTier'], ['xray', 'Xray']].filter(([k]) => !pkgHas(k)).map(x => x[1]) : [];
    let h = `<p class="blk-d">Разделы — по установленным VPN-клиентам. SSTP и прокси встроены в loft.${missing.length ? ` ${missing.join(', ')} появятся после установки пакета в <a href="#packages">Пакетах</a>.` : ''}</p>`;
    if (showKind('wireguard', 'wg')) h += blk('WireGuard', 'Канал к провайдеру VPN или своему серверу. Можно загрузить файл .conf от провайдера.', add('wg', 'Добавить подключение', L.can('network') ? `<label class="btn" style="cursor:pointer">${ic('i-up')}Загрузить из файла<input type="file" accept=".conf,text/plain" hidden onchange="LIVE.wgFile(this)"></label>` : '') + chTable(['wg']));
    h += blk('VPN-подключения', pkgHas('openvpn') ? 'Подключения к удалённым сетям по SSTP и OpenVPN.' : 'Подключения к удалённым сетям по SSTP.', add('vpn', 'Добавить подключение') + chTable(['sstp', 'ovpn']));
    if (showKind('zerotier', 'zt')) h += blk('Сети ZeroTier', 'Подключение к сети ZeroTier по 16-значному ID сети. Новому участнику может понадобиться авторизация у администратора сети.', add('zt', 'Добавить подключение') + chTable(['zt']));
    h += blk('Прокси-подключения', 'Выход через прокси-сервер по HTTP, HTTPS или SOCKS5. Можно пользоваться само по себе или как первым звеном цепочки для Xray.', add('px', 'Добавить подключение') + chTable(['px']));
    if (showKind('xray', 'xr')) h += blk('Xray', 'Канал по ссылке vless://, trojan:// или ss://. «Через» — канал, через который Xray подключается к своему серверу.', add('xr', 'Добавить подключение') + chTable(['xr']));
    h += rulesBlock() + acceptBlock();
    return h;
  }
  function ruleText(r) {
    const what = { member: `Член <b>${esc(r.value)}</b>`, tag: `Члены с тегом <b>${esc(r.value)}</b>`, vlan: `Сегмент <b>${esc(segTitle(r.value))}</b>`, list: `Адреса из списка <b class="mono">${esc(r.value)}</b>` }[r.match];
    return `${what} → <b>${esc(prName(r.via))}</b>`;
  }
  function rulesBlock() {
    const R = L.ch.rules.rules, own = L.can('network');
    const tail = r => prLink(r.via);
    const acts = i => own ? `<span class="cell-acts"><button class="icb" type="button" aria-label="Выше" ${i ? '' : 'disabled'} onclick="event.stopPropagation();LIVE.ruleMove(${i},-1)">↑</button><button class="icb" type="button" aria-label="Ниже" ${i < R.length - 1 ? '' : 'disabled'} onclick="event.stopPropagation();LIVE.ruleMove(${i},1)">↓</button><button class="icb del" type="button" aria-label="Удалить правило" onclick="event.stopPropagation();LIVE.ruleDel(${i})">${ic('i-trash')}</button></span>` : '';
    const def = L.ch.rules.default || 'default';
    return blk('Правила выхода', 'Проверяются сверху вниз, срабатывает первое подходящее, и трафик уходит по выбранной политике. Сам сервер — панель, обновления, почта — всегда выходит напрямую.',
      (own ? `<div class="acts"><button class="btn" type="button" onclick="LIVE.openRule(-1)">${ic('i-plus')}Добавить правило</button></div>` : '') +
      `<div class="rules">${R.map((r, i) => `<div class="rule ${own ? 'click' : ''}" ${own ? `onclick="LIVE.openRule(${i})"` : ''}><span class="n">${i + 1}</span><span>${ruleText(r)}</span>${tail(r)}${acts(i)}</div>`).join('')}
       <div class="rule" style="background:var(--bg)"><span class="n">∗</span><span>Политика по умолчанию → <b>${esc(prName(def))}</b></span>${prLink(def)}</div></div>
       <p class="hint">ICMP (ping) и DNS-запросы к ноде идут напрямую.</p>`);
  }
  function acceptBlock() {
    const a = L.ch.channels.filter(c => c.accept);
    if (!a.length) return '';
    return blk('Приём через канал', 'Внешний IP канала становится адресом сервера; ответы уходят тем же каналом. Только для каналов со своим внешним IP.',
      a.map(c => { const s = stOf(c.name); return `<div class="card"><dl class="kv"><div><dt>Канал</dt><dd>${esc(tt(c))}</dd></div><div><dt>Внешний адрес</dt><dd class="mono">${esc(s.external_ip || '—')}</dd></div><div><dt>Адрес в канале</dt><dd class="mono">${esc((s.addrs || [])[0] || '—')}</dd></div><div><dt>Слушают</dt><dd>нода loft :443</dd></div></dl></div>`; }).join(''));
  }

  /* channel dialogs */
  // Link check: the node probes through the channel; while it fails, priority lists skip it.
  const chCheck = c => {
    const k = c.check || {}, kind = k.kind || 'off', ping = ['wg', 'ovpn', 'zt'].includes(c.type);
    const need = kind === 'ping' || kind === 'tcp' || kind === 'tls';
    const modes = PC_MODES.filter(([v]) => v !== 'ping' || ping).map(([v, l]) => `<option value="${v}" ${kind === v ? 'selected' : ''}>${l}</option>`).join('');
    return `<div class="dlg-sec">Проверка доступности интернета (Ping Check)</div>
     <div class="fld"><label for="ck">Режим</label><select id="ck" onchange="$('#ckf').hidden=this.value==='off';$('#ckw').hidden=!(this.value==='ping'||this.value==='tcp'||this.value==='tls')">${modes}</select></div>
     <div id="ckf" ${kind === 'off' ? 'hidden' : ''} style="display:flex;flex-direction:column;gap:18px">
      <div class="fld"><label for="ckb">Периодичность проверки, сек</label><input id="ckb" class="num" value="${k.every || 10}"></div>
      <div class="fld"><label for="ckc">Порог срабатывания</label><input id="ckc" class="num" value="${k.fails || 5}"></div>
      <div id="ckw" ${need ? '' : 'hidden'}><div class="fld"><label for="cka">Проверять IP-адрес или домен</label><input id="cka" class="mono" value="${esc(k.probe || '')}" placeholder="1.1.1.1 или example.com"></div>
       <div class="hint">Для TCP без порта берётся 80, для TLS — 443.</div></div>
     </div>`;
  };
  const chVia = (id, self, kinds, cur, hint) => `<div><div class="fld"><label for="${id}">Подключаться через</label><select id="${id}"><option value="">Любое интернет-подключение</option>${L.ch.channels.filter(c => c.name !== self && kinds.includes(c.type)).map(c => `<option value="${esc(c.name)}" ${cur === c.name ? 'selected' : ''}>${esc(tt(c))} · ${CT[c.type]}</option>`).join('')}</select></div>${hint ? `<div class="hint">${hint}</div>` : ''}</div>`;
  const chHead = (c, nw) => `<div><div class="fld"><label for="c1">Имя подключения</label><input id="c1" value="${esc(c.title || c.name || '')}" placeholder="Например, Офис в Амстердаме" maxlength="40"></div>${nw ? '' : '<div class="hint">Как подключение называется в панели. Системное имя: <span class="mono">' + esc(c.name) + '</span>.</div>'}</div>
    <label class="chk"><input id="c0" type="checkbox" ${nw || c.exit ? 'checked' : ''}><span>Использовать для выхода в интернет<span class="hint" style="display:block;margin:0">Канал можно поставить в списки приоритетов.</span></span></label>`;
  const chFoot = nw => `<button class="btn pri" type="button" onclick="LIVE.chSave()">${nw ? 'Добавить' : 'Сохранить'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`;
  const advBox = x => `<details class="adv"><summary>Дополнительные настройки</summary><div style="display:flex;flex-direction:column;gap:18px;margin-top:16px">${x}</div></details>`;
  const pwF = (id, label, has) => `<div><div class="fld"><label for="${id}">${label}</label><input id="${id}" type="password" autocomplete="new-password" placeholder="${has ? 'сохранён' : ''}" data-pw-optional></div>${has ? '<div class="hint">Пусто — оставить сохранённый.</div>' : ''}</div>`;
  let D = null; // the channel being edited
  L.openChan = id => {
    const nw = id.startsWith('new:');
    const t0 = nw ? id.slice(4) : chOf(id).type;
    const t = t0 === 'vpn' ? 'sstp' : t0;
    D = nw ? { _nw: true, type: t, on: true, exit: true, wg: { address: '', peers: [{ allowed_ips: ['0.0.0.0/0'], keepalive: 25 }] }, sstp: { auth: 'auto' }, ovpn: { routes: false }, zt: { managed: true }, px: { kind: 'socks5', udp: 'direct' }, xr: {} } : JSON.parse(JSON.stringify(chOf(id)));
    drawChan(nw, t0 === 'vpn');
  };
  function drawChan(nw, vpnPick) {
    const c = D, t = c.type, title = nw ? 'Новое подключение · ' + (t === 'sstp' || t === 'ovpn' ? 'VPN' : CT[t]) : 'Подключение ' + esc(tt(c));
    let body = chHead(c, nw), sub = '';
    if (t === 'sstp' || t === 'ovpn') {
      sub = 'Данные, выданные администратором VPN-сервера.';
      if (nw) body += `<div class="fld"><label for="vt">Тип (протокол)</label><select id="vt" onchange="LIVE.vpnType(this.value)"><option value="sstp" ${t === 'sstp' ? 'selected' : ''}>SSTP — встроен в loft</option>${pkgHas('openvpn') ? `<option value="ovpn" ${t === 'ovpn' ? 'selected' : ''}>OpenVPN — пакет OpenVPN-клиент</option>` : ''}</select></div>`;
      if (t === 'sstp') body += `<div class="fld"><label for="s1">Адрес сервера</label><input id="s1" class="mono" value="${esc(c.sstp.server || '')}" placeholder="vpn.example.org[:443]"></div>
        <div class="fld"><label for="s2">Имя пользователя</label><input id="s2" value="${esc(c.sstp.user || '')}" autocomplete="off"></div>${pwF('s3', 'Пароль', c.sstp.has_password)}
        <div><div class="fld"><label for="s4">Тип аутентификации</label><select id="s4">${[['auto', 'Авто'], ['mschapv2', 'MS-CHAP v2'], ['pap', 'PAP']].map(([k, l]) => `<option value="${k}" ${(c.sstp.auth || 'auto') === k ? 'selected' : ''}>${l}</option>`).join('')}</select></div><div class="hint">Клиент SSTP в loft умеет MS-CHAP v2 и PAP.</div></div>
        <label class="chk"><input id="s5" type="checkbox" ${c.sstp.insecure ? 'checked' : ''}>Не проверять сертификат сервера</label>
        ${advBox(chCheck(c))}`;
      else body += `<div><div class="fld"><label for="o1">Конфигурация OpenVPN</label><textarea id="o1" class="mono ta" rows="6" placeholder="Вставьте содержимое файла .ovpn">${esc(c.ovpn.config || '')}</textarea></div><div class="acts" style="margin:10px 0 0"><label class="btn sm" style="cursor:pointer">${ic('i-up', 16)}Загрузить .ovpn<input type="file" accept=".ovpn,.conf,text/plain" hidden onchange="LIVE.fileInto(this,'#o1')"></label></div></div>
        <div class="fld"><label for="o2">Имя пользователя</label><input id="o2" value="${esc(c.ovpn.user || '')}" placeholder="если требует сервер" autocomplete="off"></div>${pwF('o3', 'Пароль', c.ovpn.has_password)}
        <label class="chk"><input id="ca" type="checkbox" ${c.accept ? 'checked' : ''}>Принимать входящие на внешний IP канала</label>
        ${advBox(`<label class="chk"><input id="o4" type="checkbox" ${c.ovpn.routes ? 'checked' : ''}><span>Получать маршруты от удалённой стороны<span class="hint" style="display:block;margin:0">Маршрут по умолчанию от сервера не берётся никогда.</span></span></label>${chCheck(c)}`)}`;
    } else if (t === 'px') {
      sub = 'Данные, выданные администратором прокси-сервера. Многие прокси передают только TCP.';
      body += `<div class="fld"><label for="p1">Тип (протокол)</label><select id="p1">${[['http', 'HTTP'], ['https', 'HTTPS'], ['socks5', 'SOCKS5']].map(([k, l]) => `<option value="${k}" ${c.px.kind === k ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
        <div class="fld"><label for="p2">Адрес прокси-сервера</label><input id="p2" class="mono" value="${esc(c.px.addr || '')}" placeholder="адрес:порт"></div>
        <div class="fld"><label for="p3">Тип аутентификации</label><select id="p3" onchange="$('#pAuth').hidden=this.value==='n'"><option value="n">Не использовать</option><option value="a" ${c.px.user ? 'selected' : ''}>Логин и пароль</option></select></div>
        <div id="pAuth" ${c.px.user ? '' : 'hidden'} style="display:flex;flex-direction:column;gap:18px"><div class="fld"><label for="p4">Имя пользователя</label><input id="p4" value="${esc(c.px.user || '')}" autocomplete="off"></div>${pwF('p5', 'Пароль', c.px.has_password)}</div>
        ${advBox(`${chVia('cv', c.name, ['wg', 'ovpn', 'zt', 'px', 'sstp'], c.via, '')}
          <div><div class="fld"><label for="p7">UDP</label><select id="p7">${[['direct', 'Напрямую'], ['proxy', 'Через прокси (только SOCKS5, если сервер умеет)'], ['block', 'Блокировать']].map(([k, l]) => `<option value="${k}" ${(c.px.udp || 'direct') === k ? 'selected' : ''}>${l}</option>`).join('')}</select></div><div class="hint">Так пойдут DNS и QUIC членов, у которых выход через этот прокси.</div></div>
          ${chCheck(c)}`)}`;
    } else if (t === 'xr') {
      sub = 'Ссылка от провайдера или своего сервера Xray.';
      body += `<div><div class="fld"><label for="x1">Ссылка</label><input id="x1" class="mono" value="${esc(c.xr.link || '')}" onchange="LIVE.xrParse()"></div><div class="hint">vless://, trojan:// или ss://.</div></div><div id="xkv"></div>
        ${chVia('cv', c.name, ['wg', 'ovpn', 'zt', 'px'], c.via, 'Цепочка: Xray подключается к своему серверу через другой канал.')}
        ${chCheck(c)}`;
    } else if (t === 'zt') {
      sub = 'Введите 16-значный ID сети ZeroTier. Новому участнику может понадобиться авторизация у администратора сети — в ZeroTier Central или в своём контроллере.';
      body += `<div><div class="fld"><label for="z1">ID сети</label><input id="z1" class="mono" value="${esc(c.zt.network || '')}" maxlength="16" placeholder="16 шестнадцатеричных знаков"></div>${nw ? '' : `<div class="hint">Сеть: <b>${esc(stOf(c.name).net_name || '—')}</b> · локальный ID <span class="mono">${esc(stOf(c.name).node_id || '—')}</span></div>`}</div>
        <label class="chk"><input id="ca" type="checkbox" ${c.accept ? 'checked' : ''}>Принимать входящие на внешний IP канала</label>
        ${advBox(`<label class="chk"><input id="z2" type="checkbox" ${c.zt.managed ? 'checked' : ''}><span>Получать адреса и маршруты от сети<span class="hint" style="display:block;margin:0">Маршрут по умолчанию из сети не берётся никогда.</span></span></label>${chCheck(c)}`)}`;
    } else {
      sub = 'Данные от провайдера VPN или из файла .conf.';
      const w = c.wg;
      body += `<label class="chk"><input id="ca" type="checkbox" ${c.accept ? 'checked' : ''}>Принимать входящие на внешний IP канала</label>
        <div><div class="fld"><label for="c2">Частный ключ</label><input id="c2" class="mono" type="password" value="${esc(w.private_key || '')}" placeholder="${w.has_key ? 'частный ключ установлен' : ''}" data-pw-optional></div>
        <button class="btn sm" type="button" style="margin-top:10px" onclick="LIVE.wgGen()">Генерация пары ключей</button></div>
        <div><div class="fld"><label for="c2p">Публичный ключ</label><input id="c2p" class="mono" readonly value="${esc(w.public_key || '')}"></div><div class="hint">Передайте его администратору сервера.</div></div>
        <div class="fld"><label for="c3">Адрес в туннеле</label><input id="c3" class="mono" value="${esc(w.address || '')}" placeholder="10.8.0.2/24"></div>
        <div class="fld"><label for="c3p">Порт прослушивания</label><input id="c3p" class="num" value="${w.listen_port || ''}" placeholder="случайный"></div>
        ${advBox(`<div class="fld"><label for="cmtu">Размер MTU</label><input id="cmtu" class="num" value="${w.mtu || ''}" placeholder="1420"></div>${chCheck(c)}`)}
        <div class="dlg-sec">Настройка пира</div>
        ${w.peers.map((p, i) => `<div class="peer"><div class="peer-h"><b>Пир ${i + 1}</b>${w.peers.length > 1 ? `<button class="btn sm ghost" type="button" onclick="LIVE.wgPeer(${i})">${ic('i-trash', 16)}Удалить пир</button>` : ''}</div>
          <div class="fld"><label for="w${i}a">Имя пира</label><input id="w${i}a" value="${esc(p.name || '')}"></div>
          <div class="fld"><label for="w${i}b">Публичный ключ</label><input id="w${i}b" class="mono" value="${esc(p.public_key || '')}"></div>
          ${pwF('w' + i + 'c', 'Общий ключ (необязательно)', p.has_psk)}
          <div class="fld"><label for="w${i}d">Адрес и порт пира</label><input id="w${i}d" class="mono" value="${esc(p.endpoint || '')}" placeholder="vpn.example.net:51820"></div>
          <div><div class="fld"><label for="w${i}e">Разрешённые подсети</label><input id="w${i}e" class="mono" value="${esc((p.allowed_ips || []).join(', '))}" placeholder="0.0.0.0/0"></div><div class="hint">0.0.0.0/0 — весь интернет через канал (для правил выхода). Остальные подсети попадут в маршруты сервера.</div></div>
          <div><div class="fld"><label for="w${i}g">Проверка активности, с</label><input id="w${i}g" class="num" value="${p.keepalive || ''}"></div><div class="hint">За NAT нужно 25. Пусто — не слать.</div></div></div>`).join('')}
        <button class="btn sm" type="button" style="align-self:flex-start" onclick="LIVE.wgPeer(-1)">${ic('i-plus', 16)}Добавить пир</button>`;
    }
    dialog(title, sub, body, chFoot(nw));
    if (t === 'xr' && c.xr.link) L.xrParse();
  }
  // read the dialog back into D (what the page has typed so far)
  function readChan() {
    const c = D, v = id => { const e = $('#' + id); return e ? (e.type === 'checkbox' ? e.checked : e.value.trim()) : undefined; };
    if (v('c1') !== undefined) c.title = v('c1');
    c.exit = v('c0');
    if (v('ck') !== undefined) c.check = v('ck') === 'off' ? null : { kind: v('ck'), probe: v('cka'), every: parseInt(v('ckb'), 10) || 0, fails: parseInt(v('ckc'), 10) || 0 };
    if (v('cv') !== undefined) c.via = v('cv');
    if (v('ca') !== undefined) c.accept = v('ca');
    const t = c.type;
    if (t === 'sstp') c.sstp = { server: v('s1'), user: v('s2'), password: $('#s3').value, auth: v('s4'), insecure: v('s5') };
    if (t === 'ovpn') c.ovpn = { config: $('#o1').value, user: v('o2'), password: $('#o3').value, routes: !!v('o4') };
    if (t === 'px') { const auth = v('p3') === 'a'; c.px = { kind: v('p1'), addr: v('p2'), user: auth ? v('p4') : '', password: auth ? $('#p5').value : '', udp: v('p7') }; }
    if (t === 'xr') c.xr = { link: v('x1') };
    if (t === 'zt') c.zt = { network: v('z1'), managed: !!v('z2') };
    if (t === 'wg') {
      const w = c.wg;
      w.private_key = $('#c2').value.trim(); w.address = v('c3'); w.listen_port = parseInt(v('c3p'), 10) || 0; w.mtu = parseInt(v('cmtu') || '', 10) || 0;
      w.peers = w.peers.map((p, i) => ({ name: v(`w${i}a`), public_key: v(`w${i}b`), psk: $(`#w${i}c`).value.trim(), endpoint: v(`w${i}d`),
        allowed_ips: v(`w${i}e`).split(/[\s,]+/).filter(Boolean), keepalive: parseInt(v(`w${i}g`), 10) || 0, has_psk: p.has_psk }));
    }
  }
  L.vpnType = t => { readChan(); D.type = t; drawChan(true, true); };
  L.wgPeer = i => { readChan(); if (i < 0) D.wg.peers.push({ allowed_ips: ['0.0.0.0/0'], keepalive: 25 }); else D.wg.peers.splice(i, 1); drawChan(!!D._nw); };
  L.wgGen = async () => { try { const k = await api('POST', '/api/channels/wgkey', {}); $('#c2').value = k.private; $('#c2p').value = k.public; toast('Пара ключей создана — сохраните подключение'); } catch (e) { toast(e.message); } };
  L.fileInto = (inp, sel) => { const f = inp.files[0]; inp.value = ''; if (!f) return; f.text().then(t => { $(sel).value = t; }); };
  L.xrParse = async () => {
    const box = $('#xkv'), link = $('#x1').value.trim();
    if (!box || !link) return;
    try {
      const x = await api('POST', '/api/channels/xray', { link });
      box.innerHTML = `<dl class="kv"><div><dt>Протокол</dt><dd>${esc({ vless: 'VLESS', trojan: 'Trojan', shadowsocks: 'Shadowsocks' }[x.protocol])}</dd></div><div><dt>Сервер</dt><dd class="mono">${esc(x.host + ':' + x.port)}</dd></div><div><dt>Защита</dt><dd>${esc({ none: 'нет', tls: 'TLS', reality: 'REALITY' }[x.security] || x.security)}${x.flow ? ' · Vision' : ''}</dd></div><div><dt>Транспорт</dt><dd>${esc(x.transport)}</dd></div></dl>`;
      if (window.I18N) I18N.apply(box);
    } catch (e) { box.innerHTML = `<p class="note-s" style="color:var(--bad)">${esc(e.message)}</p>`; }
  };
  // WireGuard .conf from a provider: open a new channel filled from it.
  L.wgFile = inp => {
    const f = inp.files[0]; inp.value = '';
    if (!f) return;
    f.text().then(t => {
      L.openChan('new:wg');
      const w = D.wg, peers = [];
      let sec = '', p = null;
      for (const raw of t.split(/\r?\n/)) {
        const ln = raw.replace(/#.*/, '').trim();
        const m = ln.match(/^\[(\w+)\]$/);
        if (m) { sec = m[1].toLowerCase(); if (sec === 'peer') peers.push(p = { allowed_ips: [] }); continue; }
        const kv = ln.match(/^(\w+)\s*=\s*(.+)$/);
        if (!kv) continue;
        const k = kv[1].toLowerCase(), val = kv[2].trim();
        if (sec === 'interface') ({ privatekey: () => { w.private_key = val; }, address: () => { w.address = val; }, listenport: () => { w.listen_port = +val; }, mtu: () => { w.mtu = +val; } }[k] || (() => {}))();
        if (sec === 'peer' && p) ({ publickey: () => { p.public_key = val; }, presharedkey: () => { p.psk = val; }, endpoint: () => { p.endpoint = val; }, allowedips: () => { p.allowed_ips = val.split(/\s*,\s*/); }, persistentkeepalive: () => { p.keepalive = +val; } }[k] || (() => {}))();
      }
      if (peers.length) w.peers = peers;
      D.title = f.name.replace(/\.conf$/, '').slice(0, 40);
      drawChan(true);
      toast('Поля заполнены из ' + f.name + ' — проверьте и добавьте');
    });
  };
  L.chSave = once(async () => {
    readChan();
    const c = D, nw = !!c._nw, body = { name: nw ? '' : c.name, title: c.title || '', type: c.type, on: nw ? true : c.on, exit: !!c.exit, accept: !!c.accept, check: c.check || null, via: c.via || '' };
    body[c.type] = c[c.type];
    toast(nw ? 'Добавляю подключение…' : 'Сохраняю…');
    const title = (body.title || '').replace(/\s+/g, ' ').trim();
    const before = new Set((L.ch && L.ch.channels || []).map(x => x.name));
    const landed = v => (v.channels || []).some(x => nw
      ? x.type === body.type && !before.has(x.name) && (!title || x.title === title)
      : x.name === body.name);
    try {
      L.ch = await api('POST', '/api/channels/save', body);
    } catch (e) {
      // Reload of the node can drop the HTTPS session; the helper still writes.
      if (!nw || (!e.offline && e.status !== 0)) { toast(e.message); return; }
      let got = null;
      for (let i = 0; i < 10 && !got; i++) {
        await sleep(500);
        try { const v = await api('GET', '/api/channels'); if (landed(v)) got = v; } catch (_) {}
      }
      if (!got) { toast(e.message); return; }
      L.ch = got;
    }
    chAt = Date.now();
    closeDlg();
    toast('Сохранено — нода перечитала каналы');
    render();
  });
  L.chSwitch = async (n, on) => { const t = tt(chOf(n)); try { L.ch = await api('POST', '/api/channels/switch', { name: n, on }); toast(on ? `«${t}» включён` : `«${t}» выключен`); } catch (e) { toast(e.message); } render(); };
  L.chAskDel = n => dialog(`Удалить подключение «${esc(tt(chOf(n)))}»?`, 'Канал отключится и его настройки удалятся с сервера.', '', `<button class="btn danger" type="button" onclick="LIVE.chDel('${n}')">Удалить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.chDel = async n => { closeDlg(); try { L.ch = await api('POST', '/api/channels/delete', { name: n }); toast('Подключение удалено'); } catch (e) { toast(e.message); } render(); };

  /* exit rules */
  const saveRules = async (rules, def) => { try { L.ch = await api('POST', '/api/channels/rules', { rules, default: def || L.ch.rules.default || 'direct' }); closeDlg(); toast('Правила сохранены — нода перечитала их'); } catch (e) { toast(e.message); } render(); };
  L.ruleMove = (i, d) => { const r = L.ch.rules.rules.slice(); [r[i], r[i + d]] = [r[i + d], r[i]]; saveRules(r); };
  L.ruleDel = i => { const r = L.ch.rules.rules.slice(); r.splice(i, 1); saveRules(r); };
  const exits = cur => `<option value="direct" ${cur === 'direct' ? 'selected' : ''}>Напрямую</option>${(L.ch.prios || []).map(p => `<option value="${esc(p.id)}" ${cur === p.id ? 'selected' : ''}>${esc(p.title)}</option>`).join('')}<option value="block" ${cur === 'block' ? 'selected' : ''}>Блокировать</option>`;
  function ruleValues(m) {
    if (m === 'member') return [...new Set(NETS.flatMap(n => n.members.map(x => x.id)))].map(v => [v, v]);
    if (m === 'tag') return [...new Set(NETS.flatMap(n => n.members.flatMap(x => x.tags || [])))].map(v => [v, v]);
    if (m === 'vlan') return segsOf().map(s => [String(s.id), s.id ? s.name + ' · VLAN ' + s.id : s.name]);
    return (L.ch.lists || []).map(v => [v, v]);
  }
  L.ruleMatch = m => { const vals = ruleValues(m); $('#ru2').innerHTML = vals.length ? vals.map(([v, t]) => `<option value="${esc(v)}">${esc(t)}</option>`).join('') : '<option value="">—</option>'; $('#ruH').textContent = m === 'list' && !vals.length ? 'В node.conf нет списков адресов (lists).' : m === 'tag' && !vals.length ? 'Ни у одного члена нет тегов.' : m === 'vlan' && !vals.length ? 'Сегментов пока нет.' : ''; };
  L.openRule = i => {
    const r = i >= 0 ? L.ch.rules.rules[i] : { match: 'member', value: '', via: ((L.ch.prios || []).find(p => p.id !== 'default') || prOf('default') || {}).id || 'direct' };
    const vals = ruleValues(r.match);
    dialog(i >= 0 ? `Правило ${i + 1}` : 'Новое правило', 'Чей трафик и куда он выходит.',
      `<div class="fld"><label for="ru1">Кого касается</label><select id="ru1" onchange="LIVE.ruleMatch(this.value)">${[['member', 'Член сети'], ['tag', 'Члены с тегом'], ['vlan', 'Сегмент'], ['list', 'Адреса из списка']].map(([k, l]) => `<option value="${k}" ${r.match === k ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
       <div><div class="fld"><label for="ru2">Кто или что</label><select id="ru2">${vals.map(([v, t]) => `<option value="${esc(v)}" ${v === r.value ? 'selected' : ''}>${esc(t)}</option>`).join('') || '<option value="">—</option>'}</select></div><div class="hint" id="ruH"></div></div>
       <div><div class="fld"><label for="ru3">Политика</label><select id="ru3">${exits(r.via)}</select></div>${(L.ch.prios || []).length ? '' : '<div class="hint">Политик пока нет — их создают в «Приоритетах подключений».</div>'}</div>`,
      `<button class="btn pri" type="button" onclick="LIVE.ruleSave(${i})">${i >= 0 ? 'Сохранить' : 'Добавить'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  };
  L.ruleSave = i => {
    const r = { match: $('#ru1').value, value: $('#ru2').value, via: $('#ru3').value };
    if (!r.value) { toast('Выберите, кого касается правило'); return; }
    const all = L.ch.rules.rules.slice();
    if (i >= 0) all[i] = r; else all.push(r);
    saveRules(all);
  };
  /* ── connection priorities: lists of channels and who leaves by which ── */
  const modeT = p => p.multi ? 'Многопутевой режим' : 'Режим резервирования';
  const limT = (p, n) => { const l = (p.limits || {})[n]; return l && (l.in || l.out) ? [l.in ? `↓ ${l.in} Мбит/с` : '', l.out ? `↑ ${l.out} Мбит/с` : ''].filter(Boolean).join(' · ') : '—'; };
  const DRAG = `<span class="drag" aria-hidden="true">${'<i></i>'.repeat(6)}</span>`;
  let dragCh = null, dragChip = null;
  const prioSys = id => id === 'direct' || id === 'block';
  const ownPrios = () => (L.ch.prios || []).filter(p => p.id !== 'default');
  function prioPage() {
    if (!L.ch) { loadChannels().then(() => { if (S.route === 'iface-priorities') render(); }); return pageHead('Приоритеты подключений', '') + '<p class="mute">Загружаю…</p>'; }
    if (!L.net) loadNet().then(() => { if (S.route === 'iface-priorities') render(); });
    const t = S.tab.prio || 'lists';
    const P = ownPrios();
    if (S.prio === 'default') S.prio = 'direct';
    if (t !== 'use' && S.prio === 'block') S.prio = 'direct';
    if (!prioSys(S.prio) && !P.some(p => p.id === S.prio)) S.prio = 'direct';
    return pageHead('Приоритеты подключений', 'Политика — набор подключений для выхода в интернет и кто ею пользуется. Политика по умолчанию включает адаптер сервера и все каналы с выходом; члены без назначения идут по ней. Дополнительные политики — резервирование или многопутевая передача по выбранным каналам.') +
      `<div class="tabs" role="tablist">${[['lists', 'Политики доступа в интернет', 'iface-priorities'], ['use', 'Применение политик', 'iface-priorities/use']].map(([k, x, h]) => `<button class="tab ${t === k ? 'on' : ''}" role="tab" aria-selected="${t === k}" type="button" onclick="location.hash='${h}'">${x}</button>`).join('')}</div>` +
      (t === 'use' ? prioUse() : prioLists());
  }
  function prioSide(own, count, dropId) {
    const P = ownPrios();
    const n = id => count ? ` <span class="mute">(${count(id)})</span>` : '';
    const drop = id => dropId && own ? `ondragover="event.preventDefault()" ondrop="event.preventDefault();LIVE.chipDrop('${esc(id)}')"` : '';
    const sys = (id, title, sub) => `<div class="pl-item ${S.prio === id ? 'on' : ''}" onclick="LIVE.prioPick('${id}')" ${drop(id)}><div><b>${title}${n(id)}</b><span class="sub">${sub}</span></div></div>`;
    const item = (p, i) => S.prioEdit === p.id && !dropId ? `<div class="pl-item on edit"><div class="fld"><label for="pe1">Название политики</label><input id="pe1" value="${esc(p.title)}" maxlength="40"></div>
        <label class="chk"><input id="pe2" type="checkbox" ${p.multi ? 'checked' : ''}><span>Многопутевая передача<span class="hint" style="display:block;margin:0">Соединения расходятся по всем работающим каналам политики, а не идут в первый.</span></span></label>
        <div class="acts" style="margin:0"><button class="btn sm pri" type="button" onclick="LIVE.prioRename()">Сохранить</button><button class="btn sm" type="button" onclick="S.prioEdit=null;render()">Отменить</button></div></div>`
      : `<div class="pl-item ${p.id === S.prio ? 'on' : ''}" onclick="LIVE.prioPick('${esc(p.id)}')" ${drop(p.id)}><div><b>${esc(p.title)}${n(p.id)}</b><span class="sub">${modeT(p)}</span></div>
        ${p.id === S.prio && own && !dropId ? `<span class="cell-acts"><button class="icb" type="button" aria-label="Изменить" onclick="event.stopPropagation();S.prioEdit='${esc(p.id)}';render()">${ic('i-edit')}</button><button class="icb del" type="button" aria-label="Удалить политику" ${prUsed(p.id) ? 'disabled title="Выбрана в правилах выхода"' : ''} onclick="event.stopPropagation();LIVE.prioAskDel()">${ic('i-trash')}</button></span>` : ''}</div>`;
    const defP = prOf('default');
    const block = (S.tab.prio || 'lists') === 'use' ? sys('block', 'Без доступа в интернет', 'интернет закрыт') : '';
    return `<div class="pl-side">${sys('direct', 'Политика по умолчанию', defP ? modeT(defP) : 'выход напрямую')}${P.map(item).join('')}${block}${own ? `<div class="pl-add"><button class="btn" type="button" onclick="LIVE.prioNew()">${ic('i-plus')}Добавить политику</button></div>` : ''}</div>`;
  }
  const prUsed = id => L.ch.rules.rules.some(r => r.via === id) || (L.ch.rules.default || 'default') === id;
  function prioLists() {
    const own = L.can('network'), wrap = (main, extra) => `<div class="pl-wrap"><div class="pl-head"><span>Политика</span><span>Подключение</span><span>Ограничение скорости</span></div>${prioSide(own)}<div class="pl-main">${main}${extra || ''}</div></div>`;
    const p = prCur();
    if (!p) return wrap('<p class="mute" style="padding:16px">Политика не найдена.</p>');
    const use = prUse(p);
    const live = prLive(use);
    const extra = L.ch.channels.filter(c => c.exit || p.use.includes(c.name) || use.includes(c.name)).map(c => c.name);
    const avail = ['direct'].concat(extra);
    const order = use.concat(avail.filter(n => !use.includes(n)));
    const rows = order.map(n => {
      const on = use.includes(n), st = isNative(n) ? (nativeDev() ? 'up' : 'down') : stOf(n).state;
      const dnd = own && on ? `draggable="true" ondragstart="LIVE.pdDrag('${esc(n)}')" ondragover="event.preventDefault()" ondrop="event.preventDefault();LIVE.pdDrop('${esc(n)}')"` : '';
      return `<div class="pl-row ${on ? '' : 'off'}" ${dnd}>${on && own ? DRAG : '<span class="drag"></span>'}<label class="chk"><input type="checkbox" ${on ? 'checked' : ''} ${own ? '' : 'disabled'} aria-label="Использовать" onchange="LIVE.pdUse('${esc(n)}',this.checked)"></label><span class="dot ${st === 'up' ? 'on' : ''}"></span>
        <div class="pl-name"><b>${esc(connTitle(n))}</b>${n === live && !p.multi ? ' <span class="pill ok">сейчас</span>' : ''}${connState(n)}</div>
        <div class="pl-lim">${on && !isNative(n) ? limT(p, n) : ''}</div><div>${on && own && !isNative(n) ? `<button class="icb" type="button" aria-label="Ограничение скорости" onclick="LIVE.prioLim('${esc(n)}')">${ic('i-edit')}</button>` : ''}</div></div>`;
    }).join('') || '<p class="mute" style="padding:16px">Нет подключений с «Использовать для выхода в интернет».</p>';
    const now = p.multi ? 'Многопутевая передача: новые соединения раскладываются по работающим каналам по очереди.' : live ? `Сейчас трафик идёт через <b>${esc(connTitle(live))}</b>.` : `Ни один канал не работает — трафик ${p.last === 'block' ? 'блокируется' : 'идёт напрямую'}.`;
    const hostHint = S.prio === 'direct' ? '<p class="hint" style="margin:0">Сам сервер — панель, обновления, почта — всегда выходит напрямую.</p>' : '';
    const saveBar = own && prioDirty(p) ? `<div class="pl-save"><button class="btn pri" type="button" onclick="LIVE.prioSave()">Сохранить</button><button class="btn" type="button" onclick="LIVE.prioCancel()">Отменить</button></div>` : '';
    return wrap(rows, `<div class="pl-foot"><label class="chk"><input type="checkbox" ${p.auto ? 'checked' : ''} ${own ? '' : 'disabled'} onchange="LIVE.prioSet({auto:this.checked})">Добавлять новые подключения автоматически</label>
        <div class="fld" style="max-width:360px"><label for="pr2">Если ни один не работает</label><select id="pr2" ${own ? '' : 'disabled'} onchange="LIVE.prioSet({last:this.value})"><option value="direct" ${p.last === 'direct' ? 'selected' : ''}>Выходить напрямую</option><option value="block" ${p.last === 'block' ? 'selected' : ''}>Блокировать</option></select></div>
        <p class="hint" style="margin:0">${now} Порядок — перетаскиванием. Проверка связи — в окне канала во «Внешних каналах».</p>${hostHint}${saveBar}</div>`);
  }
  // Order and “use” ticks stay local until Save; auto, last, limits and rename still go at once.
  const prioPost = async (body, msg) => {
    try { L.ch = await api('POST', '/api/channels/prio', body); closeDlg(); toast(msg); return true; } catch (e) { toast(e.message); return false; } finally { render(); }
  };
  const prCur = () => (L.ch.prios || []).find(p => p.id === (S.prio === 'direct' ? 'default' : S.prio));
  const sameUse = (a, b) => a.length === b.length && a.every((x, i) => x === b[i]);
  const prUse = p => (S.prioDraft && p && S.prioDraft.id === p.id) ? S.prioDraft.use : (p.use || []);
  const prioDirty = p => !!(p && S.prioDraft && S.prioDraft.id === p.id && !sameUse(S.prioDraft.use, p.use || []));
  const prioTouch = use => {
    const p = prCur();
    if (!p) return;
    S.prioDraft = sameUse(use, p.use || []) ? null : { id: p.id, use: use.slice() };
    render();
  };
  L.prioSet = x => { const p = Object.assign({}, prCur(), x); return prioPost(p, 'Политика сохранена — нода перечитала её'); };
  L.toPrio = id => { S.prio = id === 'default' ? 'direct' : id; S.tab.prio = 'lists'; S.prioEdit = null; };
  L.prioPick = id => { S.prio = id; S.prioEdit = null; render(); };
  L.pdUse = (n, on) => { const use = prUse(prCur()).filter(x => x !== n); if (on) use.push(n); prioTouch(use); };
  L.pdDrag = n => { dragCh = n; };
  L.pdDrop = n => {
    const use = prUse(prCur()).slice(), from = use.indexOf(dragCh), to = use.indexOf(n);
    dragCh = null;
    if (from < 0 || to < 0 || from === to) return;
    use.splice(to, 0, use.splice(from, 1)[0]);
    prioTouch(use);
  };
  L.prioSave = once(async () => {
    const p = prCur();
    if (!prioDirty(p)) return;
    try {
      L.ch = await api('POST', '/api/channels/prio', Object.assign({}, p, { use: S.prioDraft.use }));
      S.prioDraft = null;
      toast('Политика сохранена — нода перечитала её');
    } catch (e) { toast(e.message); }
    render();
  });
  L.prioCancel = () => { S.prioDraft = null; render(); };
  L.prioRename = () => { const p = prCur(); S.prioEdit = null; prioPost(Object.assign({}, p, { title: $('#pe1').value.trim(), multi: $('#pe2').checked }), 'Политика сохранена'); };
  L.prioLim = n => {
    if (isNative(n)) return;
    const p = prCur(), l = (p.limits || {})[n] || {};
    const sel = (id, v, lab) => `<div class="fld"><label for="${id}">${lab}</label><select id="${id}"><option value="0">Отключено</option>${[1, 5, 10, 20, 50, 100, 200, 500].concat(v && ![1, 5, 10, 20, 50, 100, 200, 500].includes(v) ? [v] : []).map(x => `<option value="${x}" ${v === x ? 'selected' : ''}>${x} Мбит/с</option>`).join('')}</select></div>`;
    dialog('Ограничение скорости', `Для подключения «${esc(tt(chOf(n)))}» в политике «${esc(p.title)}». Делится на всех, кто выходит по этой политике.`,
      sel('pl1', l.in || 0, 'Входящий трафик (к членам)') + sel('pl2', l.out || 0, 'Исходящий трафик (от членов)'),
      `<button class="btn pri" type="button" onclick="LIVE.prioLimSave('${esc(n)}')">Сохранить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  };
  L.prioLimSave = n => { const limits = Object.assign({}, prCur().limits || {}); limits[n] = { in: +$('#pl1').value, out: +$('#pl2').value }; L.prioSet({ limits }); };
  L.prioNew = () => {
    const P = L.ch.prios || [];
    dialog('Новая политика', 'Порядок каналов можно скопировать из другой политики и поправить.',
      `<div class="fld"><label for="pr1">Название</label><input id="pr1" placeholder="Например, Офис" maxlength="40"></div>
       <div class="fld"><label for="pr3">Начать с</label><select id="pr3"><option value="">Пустой политики</option>${P.map(x => `<option value="${esc(x.id)}">Копии «${esc(x.title)}»</option>`).join('')}</select></div>
       <label class="chk"><input id="pr4" type="checkbox">Многопутевая передача</label>`,
      `<button class="btn pri" type="button" onclick="LIVE.prioAdd()">Добавить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  };
  L.prioAdd = async () => {
    const src = (L.ch.prios || []).find(p => p.id === $('#pr3').value);
    const body = { id: '', title: $('#pr1').value.trim(), use: src ? src.use : [], last: src ? src.last : 'direct', multi: $('#pr4').checked, auto: src ? !!src.auto : false, limits: src ? src.limits : undefined };
    if (await prioPost(body, 'Политика добавлена')) { S.prio = ownPrios().slice(-1)[0].id; render(); }
  };
  L.prioAskDel = () => { const p = prCur(); dialog(`Удалить политику «${esc(p.title)}»?`, 'Подключения останутся, удалится только порядок.', '', `<button class="btn danger" type="button" onclick="LIVE.prioDel()">Удалить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`); };
  L.prioDel = async () => {
    const p = prCur();
    closeDlg();
    try { L.ch = await api('POST', '/api/channels/prio/delete', { name: p.id }); S.prio = 'direct'; if (S.prioDraft && S.prioDraft.id === p.id) S.prioDraft = null; toast('Политика удалена'); } catch (e) { toast(e.message); }
    render();
  };

  // Consumers: member, tag and segment chips dragged onto a policy.
  function prioUse() {
    const own = L.can('network'), R = L.ch.rules.rules;
    const mems = [...new Set(NETS.flatMap(n => n.members.map(m => m.id)))], tags = [...new Set(NETS.flatMap(n => n.members.flatMap(m => m.tags || [])))], segs = segsOf();
    const ruleOf = (m, v) => (R.find(r => r.match === m && r.value === v) || {}).via;
    const tagsOf = id => NETS.flatMap(n => n.members.filter(m => m.id === id).flatMap(m => m.tags || []));
    const vlanOf = id => (NETS.flatMap(n => n.members).find(m => m.id === id) || {}).vlan || 0;
    const leftover = m => !ruleOf('member', m) && !tagsOf(m).some(t => ruleOf('tag', t)) && !ruleOf('vlan', String(vlanOf(m)));
    const memIn = id => mems.filter(m => id === 'direct' ? leftover(m) || ruleOf('member', m) === 'default' : ruleOf('member', m) === id);
    const tagIn = id => tags.filter(t => id === 'direct' ? !ruleOf('tag', t) || ruleOf('tag', t) === 'default' : ruleOf('tag', t) === id);
    const segIn = id => segs.filter(s => { const v = ruleOf('vlan', String(s.id)); return id === 'direct' ? !v || v === 'default' : v === id; });
    const count = id => memIn(id).length + tagIn(id).length + segIn(id).length;
    const chip = (kind, v, label) => `<span class="m-chip ${kind === 'tag' ? 'tag' : kind === 'vlan' ? 'seg' : ''}" ${own ? `draggable="true" ondragstart="LIVE.chipDrag('${kind}','${esc(v)}')"` : ''}>${own ? DRAG : ''}${kind === 'tag' ? '# ' : ''}${esc(label || v)}</span>`;
    const m = memIn(S.prio), t = tagIn(S.prio), g = segIn(S.prio);
    return `<div class="pl-wrap use"><div class="pl-head"><span>Политика</span><span>Кто выходит по ней</span></div>
      ${prioSide(own, count, true)}
      <div class="pl-main" style="padding:16px 20px">
       <div class="pl-sec">Члены сети</div><div class="m-chips">${m.length ? m.map(x => chip('member', x)).join('') : '<span class="mute">—</span>'}</div>
       <div class="pl-sec">Теги</div><div class="m-chips">${t.length ? t.map(x => chip('tag', x)).join('') : '<span class="mute">—</span>'}</div>
       <div class="pl-sec">Сегменты</div><div class="m-chips">${g.length ? g.map(s => chip('vlan', String(s.id), s.name)).join('') : '<span class="mute">—</span>'}</div>
       <p class="hint">Перетащите плашку на политику слева. Правило члена важнее правила тега, тег важнее сегмента. Списки адресов (по сайтам) — во «Внешних каналах» → «Правила выхода».</p>
      </div></div>`;
  }
  L.chipDrag = (kind, v) => { dragChip = [kind, v]; };
  // Drop on default clears the rule; drop on a policy or «no access» writes via.
  L.chipDrop = id => {
    if (!dragChip) return;
    const [kind, v] = dragChip;
    dragChip = null;
    const R = L.ch.rules.rules.filter(r => !(r.match === kind && r.value === v));
    if (id && id !== 'direct' && id !== 'default') {
      const r = { match: kind, value: v, via: id };
      if (kind === 'member') R.unshift(r);
      else {
        const after = kind === 'tag' ? x => x.match === 'list' || x.match === 'vlan' : x => x.match === 'list';
        const k = R.findIndex(after);
        R.splice(k < 0 ? R.length : k, 0, r);
      }
    }
    saveRules(R);
  };

  function addrsPage() {
    if (!L.net) { loadNet().then(() => { if (S.route === 'adapters' || S.route === 'addrs') render(); }); return pageHead('Адаптеры', '') + '<p class="mute">Загружаю…</p>'; }
    if (!L.ch) loadChannels().then(() => { if (S.route === 'adapters' || S.route === 'addrs') render(); });
    const k = (L.ch && L.ch.direct_check) || { kind: 'off', every: 10, fails: 5, probe: '' };
    const st = ((L.ch && (L.ch.status || []).find(s => s.name === 'direct')) || {}).state || (k.kind && k.kind !== 'off' ? 'up' : '');
    return pageHead('Адаптеры', 'Сетевые адаптеры сервера, их адреса и маршруты. Сети loft — в Сегментах, туннели — во Внешних каналах.') + pingCheckBox(k, st, true, L.can('network')) + netAddrLive();
  }
  window.pcSaved = () => {
    const k = (L.ch && L.ch.direct_check) || { kind: 'off', every: 10, fails: 5, probe: '' };
    return { kind: k.kind || 'off', probe: k.probe || '', every: k.every || 10, fails: k.fails || 5 };
  };
  L.pcSave = async () => {
    if (!pcValid()) { pcTick(); return; }
    const a = pcNow();
    const body = a.kind === 'off' ? {} : a;
    try { L.ch = await api('POST', '/api/channels/direct-check', body); toast('Проверка сохранена — нода перечитала её'); } catch (e) { toast(e.message); }
    render();
  };
  window.pcSave = L.pcSave;
  window.pcCancel = () => render();
  function channelsPage() {
    if (!L.ch) { loadChannels().then(() => { if (S.route === 'channels') render(); }); return pageHead('Внешние каналы', '') + '<p class="mute">Загружаю…</p>'; }
    return pageHead('Внешние каналы', 'VPN-каналы и прокси для выхода в интернет и приёма. SSTP и прокси встроены в loft, остальные — пакетами.') + channelsView();
  }
  function networkView() { return channelsPage(); }

  /* ── panel users: accounts, roles, sessions, log ── */
  L.us = null; L.sess = null; L.log = null;
  const AREA_RU = { main: 'Обзор', node: 'Нода loft', members: 'Члены сети', segments: 'Сегменты сети', network: 'Интернет', domains: 'Домены', mail: 'Почта', packages: 'Пакеты', users: 'Пользователи', system: 'Система', settings: 'Настройки' };
  const ROLE_RU = { owner: 'Владелец', operator: 'Оператор', viewer: 'Наблюдатель' };
  const roleName = r => (r && (r.builtin || r.id === 'owner') ? ROLE_RU[r.id] : r && r.name) || '—';
  const roleOf = id => ((L.us && L.us.roles) || []).find(r => r.id === id);
  L.can = (a, l = 2) => !!(L.me && L.me.perms && L.me.perms[a] >= l);
  const when2 = v => v && !String(v).startsWith('0001') ? new Date(v).toLocaleString(LOC(), { dateStyle: 'short', timeStyle: 'short' }) : '—';
  const agoT = v => { if (!v || String(v).startsWith('0001')) return '—'; const s = (Date.now() - new Date(v)) / 1000; return s < 90 ? 'сейчас' : s < 3600 ? Math.round(s / 60) + ' мин назад' : s < 86400 ? Math.round(s / 3600) + ' ч назад' : Math.round(s / 86400) + ' дн. назад'; };
  const device = ua => { ua = ua || ''; const b = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : /curl\//.test(ua) ? 'curl' : ua.split(/[ /]/)[0] || '—'; const o = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''; return o ? `${b} · ${o}` : b; };
  async function loadUsers() {
    const t = S.tab.users;
    try {
      L.us = await api('GET', '/api/users');
      if (t === 'sess') L.sess = await api('GET', '/api/sessions');
      if (t === 'log') L.log = await api('GET', '/api/audit');
    } catch (e) { toast(e.message); L.us = L.us || { users: [], roles: [], areas: [] }; }
  }
  L.usersTab = t => { S.tab.users = t; L.sess = L.log = null; render(); };
  function usersView() {
    if (!L.can('users', 1)) return pageHead('Пользователи панели', '') + `<div class="empty"><div class="empty-ic">${ic('i-gear', 28)}</div><b>Нет доступа</b><p>Вашей роли этот раздел не виден.</p></div>`;
    const t = S.tab.users;
    if (!L.us || (t === 'sess' && !L.sess) || (t === 'log' && !L.log)) { loadUsers().then(() => { if (S.route === 'users') render(); }); return pageHead('Пользователи панели', '') + '<p class="mute">Загружаю…</p>'; }
    const own = L.can('users');
    let body;
    if (t === 'roles') {
      const n = id => L.us.users.filter(u => u.role === id).length;
      const sum = r => { const ch = Object.keys(AREA_RU).filter(a => r.perms[a] === 2), vw = Object.keys(AREA_RU).filter(a => r.perms[a] === 1); return r.id === 'owner' ? 'всё, включая пользователей, пакеты и обновления' : [ch.length ? 'менять: ' + ch.map(a => AREA_RU[a]).join(', ') : '', vw.length ? 'смотреть: ' + vw.map(a => AREA_RU[a]).join(', ') : ''].filter(Boolean).join('; ') || 'ничего'; };
      body = blk('Роли', 'Роль задаёт, какие разделы пользователь видит и что может менять.', (own ? `<div class="acts"><button class="btn" type="button" onclick="LIVE.openRole('')">${ic('i-plus')}Добавить роль</button></div>` : '') +
        table(['Роль', 'Что может', 'Пользователей'], L.us.roles.map(r => `<tr class="click" onclick="LIVE.openRole('${r.id}')"><td><b>${esc(roleName(r))}</b></td><td style="white-space:normal;min-width:260px">${esc(sum(r))}</td><td class="num">${n(r.id)}</td></tr>`).join('')));
    } else if (t === 'sess') {
      const rows = L.sess.sessions.sort((a, b) => new Date(b.seen) - new Date(a.seen)).map(x => `<tr><td><b>${esc(x.user)}</b>${x.me ? ' <span class="mute">(это вы)</span>' : ''}</td><td class="mono">${esc(x.ip)}</td><td>${esc(device(x.agent))}</td><td>${when2(x.created)}</td><td>${agoT(x.seen)}</td><td class="r">${x.me || !own ? '' : `<button class="btn sm" type="button" onclick="LIVE.endSess('${x.key}')">Завершить</button>`}</td></tr>`).join('') +
        (L.sess.tokens || []).map(x => `<tr><td><b>${esc(x.user)}</b></td><td class="mono">API-токен</td><td class="mono mute">lpt_…${esc(x.tail)}</td><td>${when2(x.issued)}</td><td>${agoT(x.used)}</td><td class="r">${own ? `<button class="btn sm" type="button" onclick="LIVE.tokRevoke('${esc(x.user)}')">Отозвать</button>` : ''}</td></tr>`).join('');
      body = blk('Сессии', 'Кто сейчас вошёл в панель.', table(['Пользователь', 'Откуда', 'Устройство', 'Вход', 'Активность', { t: '', r: 1 }], rows));
    } else if (t === 'log') {
      const who = L.logWho || '';
      const list = L.log.filter(x => !who || x.user === who);
      body = blk('Журнал', 'Все изменения, сделанные через панель и API.', `<div class="acts"><div class="fld" style="margin:0;min-width:min(240px,100%)"><label for="lg1">Кто</label><select id="lg1" onchange="LIVE.logWho=this.value;render()"><option value="">Все</option>${[...new Set(L.log.map(x => x.user))].map(u => `<option value="${esc(u)}" ${u === who ? 'selected' : ''}>${esc(whoText(u))}</option>`).join('')}</select></div></div>` +
        table(['Когда', 'Кто', 'Действие', 'Откуда'], list.map(x => `<tr><td class="num">${when2(x.at)}</td><td><b>${esc(whoText(x.user))}</b></td><td>${esc(opText(x))}</td><td class="mono mute">${x.from === 'token' ? 'API-токен' : esc(x.from || '—')}</td></tr>`).join('') || `<tr><td colspan="4" class="mute">Записей нет.</td></tr>`));
    } else {
      const st = u => u.invited ? pill('приглашён · до ' + when2(u.invite_until), 'busy') : u.disabled ? pill('выключен') : pill('может входить', 'ok');
      body = blk('Пользователи', '', (own ? `<div class="acts"><button class="btn pri" type="button" onclick="LIVE.openUser('')">${ic('i-plus')}Добавить пользователя</button></div>` : '') +
        table(['Логин', 'Роль', 'API-токен', 'Был', 'Состояние'], L.us.users.map(u => `<tr class="click" onclick="LIVE.openUser('${u.login}')"><td><b>${esc(u.login)}</b><span class="sub">${esc(u.name || '')}</span></td><td>${esc(roleName(roleOf(u.role)).toLowerCase())}</td><td class="mono mute">${u.token_tail ? 'lpt_…' + esc(u.token_tail) : 'нет'}</td><td>${agoT(u.seen)}</td><td>${st(u)}</td></tr>`).join('')));
    }
    return pageHead('Пользователи панели', 'Кто входит в эту панель. Это не члены сети: у члена сети свой ключ и профиль подключения.') +
      `<div class="tabs" role="tablist">${[['list', 'Пользователи'], ['roles', 'Роли'], ['sess', 'Сессии'], ['log', 'Журнал']].map(([k, tt]) => `<button class="tab ${t === k ? 'on' : ''}" role="tab" aria-selected="${t === k}" type="button" onclick="LIVE.usersTab('${k}')">${tt}</button>`).join('')}</div>` + body;
  }
  // what a log entry says: the route and its target
  const OPS = {
    'POST /api/login': 'Вошёл в панель', 'POST /api/logout': 'Вышел', 'POST /api/invite': 'Принял приглашение и задал пароль', 'POST /api/setup': 'Создал владельца',
    'POST /api/network/apply': 'Применил сеть loft', 'POST /api/members/key': 'Выдал ключ члену', 'POST /api/wizard/member': 'Создал члена сети', 'POST /api/node/restart': 'Перезапустил ноду loft',
    'POST /api/domain': 'Сменил имена сервера', 'POST /api/cert/issue': 'Выпустил сертификаты', 'POST /api/wizard/done': 'Закончил мастер установки',
    'POST /api/packages/apply': 'Запустил задание с пакетами', 'POST /api/packages/unit': 'Включил или выключил пакет', 'POST /api/update/settings': 'Изменил настройки обновлений', 'POST /api/update/check': 'Проверил обновления', 'POST /api/update/apply': 'Запустил обновление',
    'POST /api/net/change': 'Изменил адрес или маршрут', 'POST /api/net/confirm': 'Подтвердил изменение сети', 'POST /api/net/revert': 'Откатил изменение сети',
    'POST /api/channels/save': 'Сохранил канал', 'POST /api/channels/delete': 'Удалил канал', 'POST /api/channels/switch': 'Включил или выключил канал', 'POST /api/channels/rules': 'Изменил правила выхода', 'POST /api/channels/wgkey': 'Создал пару ключей WireGuard',
    'POST /api/settings/panel': 'Изменил настройки панели', 'POST /api/settings/timezone': 'Сменил часовой пояс сервера', 'POST /api/settings/notify': 'Изменил уведомления', 'POST /api/settings/notify/test': 'Отправил пробное письмо',
    'POST /api/channels/prio': 'Сохранил список приоритетов', 'POST /api/channels/prio/delete': 'Удалил список приоритетов', 'POST /api/channels/direct-check': 'Изменил проверку доступности интернета',
    'POST /api/domains/zone': 'Изменил зону', 'POST /api/domains/record': 'Изменил DNS-запись', 'POST /api/domains/provider': 'Изменил DNS-провайдера', 'POST /api/domains/cert': 'Выпустил сертификат', 'POST /api/domains/cert/upload': 'Загрузил сертификат', 'POST /api/domains/cert/delete': 'Удалил сертификат',
    'POST /api/mail/domain': 'Изменил почтовый домен', 'POST /api/mail/box': 'Изменил ящик', 'POST /api/mail/alias': 'Изменил псевдоним', 'POST /api/mail/apply': 'Применил настройки почты', 'POST /api/mail/dns': 'Создал записи почты в DNS', 'POST /api/mail/cert': 'Выпустил сертификат почты',
    'POST /api/nodecfg': 'Изменил настройки ноды', 'POST /api/nodecfg/lists': 'Изменил списки ноды', 'POST /api/nodecfg/token': 'Сменил токен ноды', 'POST /api/packages/mtproto': 'Изменил MTProto-прокси',
    'POST /api/system/guard': 'Изменил защиту', 'POST /api/system/guard/ban': 'Заблокировал адрес', 'POST /api/system/guard/unban': 'Разблокировал адрес', 'POST /api/system/user': 'Изменил учётную запись Linux', 'POST /api/system/user/delete': 'Удалил учётную запись Linux',
    'POST /api/system/backup/run': 'Сделал резервную копию', 'POST /api/system/backup/settings': 'Изменил расписание копий', 'POST /api/system/backup/store': 'Изменил хранилище копий', 'POST /api/system/backup/phrase': 'Создал фразу восстановления', 'POST /api/system/backup/restore': 'Восстановил из копии', 'POST /api/system/backup/fetch': 'Скачал копию из хранилища', 'POST /api/system/backup/upload': 'Загрузил копию',
    'POST /api/me/password': 'Сменил свой пароль', 'POST /api/me/token': 'Выпустил или отозвал свой API-токен', 'POST /api/me/sessions/end': 'Завершил свою сессию',
    'system cert': 'Продлил сертификат', 'system cert-failed': 'Не смог продлить сертификат', 'system dns-ips': 'Обновил DNS-записи на новые адреса сервера', 'system backup': 'Начал резервную копию по расписанию', 'system update': 'Начал автообновление до', 'system login-block': 'Заблокировал вход после неудачных попыток с',
    'POST /api/users/save': 'Сохранил пользователя', 'POST /api/users/delete': 'Удалил пользователя', 'POST /api/users/token': 'Выпустил или отозвал API-токен', 'POST /api/roles/save': 'Сохранил роль', 'POST /api/roles/delete': 'Удалил роль', 'POST /api/sessions/end': 'Завершил сессию',
  };
  const opText = x => (OPS[x.op] || x.op) + (x.target ? ' ' + x.target : '');
  const whoText = u => u === 'system' ? 'система' : u;

  /* ── #me: own profile, password, API token, sessions ── */
  L.my = null;
  const RIGHT = ['нет', 'смотрит', 'меняет'];
  function meView() {
    if (!L.my) { api('GET', '/api/me').then(r => { L.my = r; if (S.route === 'me') render(); }, e => toast(e.message)); return pageHead('Личный кабинет', '') + '<p class="mute">Загружаю…</p>'; }
    const M = L.my, me = M.me, tk = M.token;
    return pageHead('Личный кабинет', 'Ваш вход в панель: права роли, пароль, API-токен и сессии.') +
      blk('Профиль', '', `<dl class="kv"><div><dt>Логин</dt><dd class="mono">${esc(me.login)}</dd></div>${me.name ? `<div><dt>Имя</dt><dd>${esc(me.name)}</dd></div>` : ''}<div><dt>Роль</dt><dd>${esc(ROLE_RU[me.role] || me.role_name || me.role)}</dd></div><div><dt>Последний вход</dt><dd>${agoT(M.seen)}</dd></div></dl>
        <details class="adv" style="margin-top:12px"><summary>Права роли</summary>${table(['Раздел', 'Доступ'], Object.keys(AREA_RU).map(a => `<tr><td>${AREA_RU[a]}</td><td>${RIGHT[me.perms[a] || 0]}</td></tr>`).join(''))}</details>`) +
      blk('Пароль', 'После смены остальные ваши сессии завершатся.', `<button class="btn" type="button" onclick="LIVE.openMyPw()">Сменить пароль</button>`) +
      blk('API-токен', 'Для скриптов и интеграций: заголовок Authorization: Bearer …. Права — как у вашей роли.', tk
        ? `<div class="uri" style="margin-bottom:16px">lpt_…${esc(tk.tail)} · выпущен ${when2(tk.issued)} · последний раз ${agoT(tk.used)}</div><div class="acts"><button class="btn" type="button" onclick="LIVE.myTok(false)">Перевыпустить</button><button class="btn danger" type="button" onclick="LIVE.myTok(true)">Отозвать</button></div>`
        : `<p class="hint" style="margin:0 0 16px">Токена нет.</p><button class="btn" type="button" onclick="LIVE.myTok(false)">Выпустить токен</button>`) +
      blk('Сессии', 'Где вы сейчас вошли в панель.', `<div class="acts">${M.sessions.length > 1 ? `<button class="btn" type="button" onclick="LIVE.myEnd('')">Завершить остальные</button>` : ''}<button class="btn" type="button" onclick="goOut()">${ic('i-out', 18)}Выйти</button></div>` +
        table(['Устройство', 'IP', 'Вход', 'Активность', { t: '', r: 1 }], M.sessions.sort((a, b) => new Date(b.seen) - new Date(a.seen)).map(x => `<tr><td>${esc(device(x.agent))}${x.me ? ' <span class="mute">(это устройство)</span>' : ''}</td><td class="mono">${esc(x.ip)}</td><td>${when2(x.created)}</td><td>${agoT(x.seen)}</td><td class="r">${x.me ? '' : `<button class="btn sm" type="button" onclick="LIVE.myEnd('${esc(x.key)}')">Завершить</button>`}</td></tr>`).join('') || `<tr><td colspan="5" class="mute">Вход по API-токену: сессий нет.</td></tr>`));
  }
  const reMe = async () => { try { L.my = await api('GET', '/api/me'); } catch (e) { /* 401 goes to sign-in */ } render(); };
  L.openMyPw = () => dialog('Сменить пароль', 'Остальные ваши сессии завершатся.', `
      <div class="fld"><label for="me1">Текущий пароль</label><input id="me1" type="password" autocomplete="current-password"></div>
      <div><div class="fld"><label for="me2">Новый пароль</label><input id="me2" type="password" autocomplete="new-password" data-pw-min="9"></div><div class="hint" data-pw-hint>Не короче 9 знаков.</div></div>`,
    `<button class="btn pri" type="button" onclick="LIVE.myPw()">Сменить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.myPw = once(async () => {
    if (!pwCheck($('#dlgRoot'), true)) { toast('Проверьте пароль'); return; }
    try { await api('POST', '/api/me/password', { current: $('#me1').value, password: $('#me2').value }); closeDlg(); toast('Пароль изменён'); await reMe(); } catch (e) { toast(e.message); }
  });
  L.myTok = once(async revoke => {
    try {
      const r = await api('POST', '/api/me/token', { revoke });
      await reMe();
      if (revoke) toast('Токен отозван');
      else dialog('Ваш API-токен', 'Показан один раз. Передавайте в заголовке Authorization: Bearer ….', `<div class="uri" id="tokv">${esc(r.token)}</div><div class="acts" style="margin:0"><button class="btn sm pri" type="button" onclick="LIVE.copy('#tokv')">Копировать токен</button></div>`, `<button class="btn pri" type="button" onclick="closeDlg()">Готово</button>`);
    } catch (e) { toast(e.message); }
  });
  L.myEnd = once(async key => {
    try { await api('POST', '/api/me/sessions/end', key ? { key } : { others: true }); toast(key ? 'Сессия завершена' : 'Остальные сессии завершены'); } catch (e) { toast(e.message); }
    await reMe();
  });
  L.openUser = login => {
    const nw = !login, u = nw ? { login: '', name: '', role: 'viewer' } : L.us.users.find(x => x.login === login), own = L.can('users');
    const owners = L.us.users.filter(x => x.role === 'owner' && !x.disabled).length, lastOwner = u.role === 'owner' && owners < 2;
    const ro = own ? '' : 'disabled';
    dialog(nw ? 'Новый пользователь' : esc(u.login), nw ? 'Пароль задаёт сам пользователь по ссылке-приглашению или вы — здесь.' : lastOwner ? 'Владелец: роль не меняется, пока он единственный.' : '', `
      <div class="fld"><label for="pu1">Логин</label><input id="pu1" class="mono" value="${esc(u.login)}" ${nw ? '' : 'readonly'} placeholder="bob"></div>
      <div class="fld"><label for="pu2">Имя</label><input id="pu2" value="${esc(u.name || '')}" ${ro}></div>
      <div class="fld"><label for="pu3">Роль</label><select id="pu3" ${lastOwner || !own ? 'disabled' : ''}>${L.us.roles.map(r => `<option value="${r.id}" ${r.id === u.role ? 'selected' : ''}>${esc(roleName(r))}</option>`).join('')}</select></div>
      ${own ? `<div><div class="fld"><label for="pu4">Пароль</label><input id="pu4" type="password" autocomplete="new-password" placeholder="${nw ? '' : 'не менять'}" data-pw-min="9" ${nw ? '' : 'data-pw-optional'}></div>${nw ? `<button class="btn sm" type="button" style="margin-top:10px" onclick="LIVE.userSave(true)">Ссылка-приглашение на 24 часа</button>` : u.invited ? '<div class="hint">Приглашение ещё не принято. Пароль здесь заменит его.</div>' : ''}</div>` : ''}
      ${nw ? '' : `<div class="dlg-sec">API-токен</div>
      ${!u.token_tail ? `<p class="hint" style="margin:0">Нет токена. Токен даёт те же права, что роль, для скриптов и интеграций.</p>${own ? `<button class="btn sm" type="button" style="align-self:flex-start" onclick="LIVE.tokIssue('${esc(u.login)}')">Выпустить токен</button>` : ''}`
        : `<div class="uri">lpt_…${esc(u.token_tail)} · выпущен ${when2(u.token_issued)} · последний раз ${agoT(u.token_used)}</div>${own ? `<div class="acts" style="margin:0"><button class="btn sm" type="button" onclick="LIVE.tokIssue('${esc(u.login)}')">Перевыпустить</button><button class="btn sm danger" type="button" onclick="LIVE.tokRevoke('${esc(u.login)}')">Отозвать</button></div>` : ''}`}
      ${lastOwner || u.login === L.me.login ? '' : `<label class="chk"><input id="pu5" type="checkbox" ${u.disabled ? '' : 'checked'} ${ro}>Может входить</label>`}`}`,
      own ? `<button class="btn pri" type="button" onclick="LIVE.userSave(false)">${nw ? 'Создать' : 'Сохранить'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>${nw || lastOwner || u.login === L.me.login ? '' : `<button class="icb del" type="button" aria-label="Удалить пользователя" style="margin-left:auto" onclick="LIVE.userDel('${esc(u.login)}')">${ic('i-trash')}</button>`}`
        : `<button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`);
    L.edUser = { nw, login: u.login };
  };
  L.userSave = async invite => {
    const nw = L.edUser.nw, pw = $('#pu4').value;
    if (!invite && (nw || pw) && !pwCheck($('#dlgRoot'), !nw)) { toast('Проверьте пароль'); return; }
    if (nw && !invite && !pw) { toast('Задайте пароль или сделайте ссылку-приглашение'); return; }
    const body = { new: nw, login: nw ? $('#pu1').value.trim() : L.edUser.login, name: $('#pu2').value.trim(), role: $('#pu3').value, password: invite ? '' : pw, invite: !!invite, disabled: $('#pu5') ? !$('#pu5').checked : false };
    try {
      const r = await api('POST', '/api/users/save', body);
      await loadUsers(); render();
      if (r.invite) {
        const link = location.origin + '/' + r.invite;
        dialog('Приглашение для ' + esc(body.login), 'Ссылка действует 24 часа и открывается один раз. Передайте её пользователю — пароль он задаст сам.',
          `<div class="uri" id="invl">${esc(link)}</div><div class="acts" style="margin:0"><button class="btn sm pri" type="button" onclick="LIVE.copy('#invl')">Копировать ссылку</button></div>`, `<button class="btn pri" type="button" onclick="closeDlg()">Готово</button>`);
      } else { closeDlg(); toast('Сохранено'); }
    } catch (e) { toast(e.message); }
  };
  L.userDel = login => dialog(`Удалить пользователя «${esc(login)}»?`, 'Его сессии и API-токен перестанут действовать.', '', `<button class="btn danger" type="button" onclick="LIVE.userDelGo('${esc(login)}')">Удалить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.userDelGo = async login => { closeDlg(); try { await api('POST', '/api/users/delete', { login }); toast('Пользователь удалён'); } catch (e) { toast(e.message); } await loadUsers(); render(); };
  L.tokIssue = async login => {
    try {
      const r = await api('POST', '/api/users/token', { login });
      await loadUsers(); render();
      dialog('API-токен ' + esc(login), 'Показан один раз. Передавайте в заголовке Authorization: Bearer ….', `<div class="uri" id="tokv">${esc(r.token)}</div><div class="acts" style="margin:0"><button class="btn sm pri" type="button" onclick="LIVE.copy('#tokv')">Копировать токен</button></div>`, `<button class="btn pri" type="button" onclick="closeDlg()">Готово</button>`);
    } catch (e) { toast(e.message); }
  };
  L.tokRevoke = async login => { closeDlg(); try { await api('POST', '/api/users/token', { login, revoke: true }); toast('Токен отозван'); } catch (e) { toast(e.message); } L.sess = null; await loadUsers(); render(); };
  L.endSess = async key => { try { await api('POST', '/api/sessions/end', { key }); toast('Сессия завершена'); } catch (e) { toast(e.message); } L.sess = null; await loadUsers(); render(); };
  L.openRole = id => {
    const nw = !id, r = nw ? { id: '', name: '', perms: {} } : roleOf(id), fixed = id === 'owner' || !L.can('users');
    dialog(nw ? 'Новая роль' : 'Роль «' + esc(roleName(r)) + '»', id === 'owner' ? 'У владельца все права, их не изменить.' : 'Что видно в меню и что можно менять.', `
      ${nw ? '<div class="fld"><label for="ro1">Название</label><input id="ro1"></div>' : ''}
      <div class="tw" style="border:1px solid var(--line)"><table class="t"><thead><tr><th scope="col">Раздел</th>${['нет', 'смотреть', 'менять'].map(x => `<th scope="col" class="c">${x}</th>`).join('')}</tr></thead><tbody>
      ${Object.keys(AREA_RU).map(a => `<tr><td>${AREA_RU[a]}</td>${[0, 1, 2].map(v => `<td class="c"><input type="radio" name="pa-${a}" value="${v}" aria-label="${AREA_RU[a]}: ${['нет', 'смотреть', 'менять'][v]}" ${(r.perms[a] || 0) === v ? 'checked' : ''} ${fixed ? 'disabled' : ''} style="width:18px;height:18px;accent-color:var(--ink)"></td>`).join('')}</tr>`).join('')}</tbody></table></div>
      <p class="hint" style="margin:0">Раздел без права «смотреть» пропадает из меню. Пароли, ключи и токены видны только тем, кто может «менять».</p>`,
      fixed ? `<button class="btn" type="button" onclick="closeDlg()">Закрыть</button>` : `<button class="btn pri" type="button" onclick="LIVE.roleSave('${r.id}')">${nw ? 'Создать' : 'Сохранить'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>${nw || r.builtin ? '' : `<button class="icb del" type="button" aria-label="Удалить роль" style="margin-left:auto" onclick="LIVE.roleDel('${r.id}')">${ic('i-trash')}</button>`}`);
  };
  L.roleSave = async id => {
    const perms = {};
    Object.keys(AREA_RU).forEach(a => { const c = document.querySelector(`input[name="pa-${a}"]:checked`); perms[a] = c ? +c.value : 0; });
    try { await api('POST', '/api/roles/save', { id, name: id ? '' : $('#ro1').value.trim(), perms }); closeDlg(); toast('Роль сохранена'); } catch (e) { toast(e.message); }
    await loadUsers(); render();
  };
  L.roleDel = async id => { closeDlg(); try { await api('POST', '/api/roles/delete', { id }); toast('Роль удалена'); } catch (e) { toast(e.message); } await loadUsers(); render(); };

  /* invitation page: #invite/TOKEN */
  const INV = { login: '', token: '', error: '' };
  function inviteView() {
    return `<div class="fs"><div class="fs-card" style="max-width:420px">${fullHead(esc(location.hostname))}<h1>Приглашение в панель</h1>
      ${INV.error ? `<p class="note-s" style="color:var(--bad)">${esc(INV.error)}</p>` : `<p class="fs-d">Задайте пароль для входа как <b class="mono">${esc(INV.login)}</b>.</p>
      <form class="fs-form" onsubmit="event.preventDefault();LIVE.inviteGo()">
       <div><div class="fld"><label for="iv1">Пароль</label><input id="iv1" type="password" autocomplete="new-password" data-pw-min="9"></div><div class="hint" data-pw-hint>Не короче 9 знаков.</div></div>
       <div><div class="fld"><label for="iv2">Пароль ещё раз</label><input id="iv2" type="password" autocomplete="new-password" data-pw-same="iv1"></div><div class="hint" data-pw-hint></div></div>
       <button class="btn pri" type="submit" style="width:100%">Задать пароль и войти</button></form>`}</div></div>`;
  }
  L.inviteGo = async () => {
    if (!pwCheck($('#full'), true)) { toast('Проверьте пароль'); return; }
    try { const me = await api('POST', '/api/invite', { token: INV.token, password: $('#iv1').value }); history.replaceState(null, '', '#main'); signedIn(me); } catch (e) { toast(e.message); }
  };
  async function startInvite(tok) {
    INV.token = tok;
    try { INV.login = (await api('GET', '/api/invite/' + encodeURIComponent(tok))).login; } catch (e) { INV.error = e.message; }
    S.route = 'invite'; render();
  }

  // the menu shows only what the role may see (and Mail only with its package)
  // the node line at the foot of the phone menu
  const mobSt = () => {
    const e = $('#mobSt'), n = L.node || {};
    if (e) e.innerHTML = `<span class="dot ${n.active === 'active' ? 'on' : ''}"></span>нода loft${n.version ? ' ' + esc(n.version) : ''} · ${!n.active ? 'нет данных' : n.active === 'active' ? 'работает' : esc(n.active)}`;
  };
  function renderMenuLive() {
    mobSt();
    const vis = MENU.map(g => ({ ...g, items: g.items.filter(i => (!i[2] || pkgOn(i[2])) && L.can(netRight(i[0]), 1)) })).filter(g => g.items.length);
    $('#menu').innerHTML = vis.map(g => {
      const on = g.items.some(i => i[0] === S.route);
      return `<div class="grp ${on ? 'on' : ''}"><div class="grp-h" ${S.rail ? `role="link" tabindex="0" title="${g.t}" onclick="go('${g.items[0][0]}')"` : ''}>${ic(g.ic, 24)}<span>${g.t}</span></div>
        ${g.items.map(([k, t]) => `<a class="lnk ${k === S.route ? 'on' : ''}" href="#${k}" ${k === S.route ? 'aria-current="page"' : ''}>${t}</a>`).join('')}</div>`;
    }).join('');
    $('#app').classList.toggle('rail', S.rail);
    $('#foldTx').textContent = S.rail ? 'Развернуть меню' : 'Свернуть меню';
    $('#foldIc').innerHTML = `<use href="#${S.rail ? 'i-menu' : 'i-back'}"/>`;
  }

  /* ── the loft node: node.conf, lists, relays, access ── */
  L.nd = null; L.ndDraft = null; L.pres = null;
  const FR = [['get', 'HTTP GET', 'Публичный путь по умолчанию. Обычный HTTPS GET, в том числе через CDN.'], ['ws', 'WebSocket', 'Опция. Один WebSocket на путь API. Прямой origin, не CDN.'], ['h2', 'HTTP/2 CONNECT', 'Опция. HTTP/2 CONNECT на прямом origin. Больше объёма, чем GET.'], ['quic', 'QUIC', 'Опция. Своё QUIC по UDP/443.'], ['h3', 'HTTP/3', 'Опция. HTTP/3 CONNECT поверх QUIC.'], ['l3', 'L3', 'Опция. HTTP/2 CONNECT на прямом origin, внутри — IP-пакеты без mux.']];
  async function loadNode() {
    const [c, p] = await Promise.allSettled([api('GET', '/api/nodecfg'), api('GET', '/api/presence')]);
    if (c.status === 'fulfilled') { L.nd = c.value; L.ndDraft = JSON.parse(JSON.stringify(c.value)); } else toast('Настройки ноды не прочитаны: ' + c.reason.message);
    if (p.status === 'fulfilled') L.pres = p.value;
  }
  const ndKeys = ['listen', 'fronts', 'get_h2', 'decoy', 'mask', 'cover_dir', 'get_path', 'ws_path', 'h2_path', 'roster_path', 'lists_path', 'enrol', 'auth_shared', 'trace', 'relays', 'cdn_peers'];
  const ndChanged = () => L.nd && ndKeys.filter(k => JSON.stringify(L.nd[k]) !== JSON.stringify(L.ndDraft[k]));
  L.ndSet = (k, v) => { L.ndDraft[k] = v; render(); scheduleNdApply(); };
  L.ndFront = (f, on) => { const s = new Set(L.ndDraft.fronts); on ? s.add(f) : s.delete(f); L.ndDraft.fronts = FR.map(x => x[0]).filter(x => s.has(x)); render(); scheduleNdApply(); };
  L.ndRevert = () => { clearTimeout(L._ndT); L.ndDraft = JSON.parse(JSON.stringify(L.nd)); render(); toast('Черновик сброшен'); };
  const scheduleNdApply = () => {
    clearTimeout(L._ndT);
    if (!ndChanged().length) return;
    L._ndT = setTimeout(() => { if (ndChanged().length) L.ndApply(); }, 500);
  };
  L.ndApply = once(async () => {
    const sent = JSON.parse(JSON.stringify(L.ndDraft));
    const onlyFleet = ndKeys.filter(k => JSON.stringify(L.nd[k]) !== JSON.stringify(sent[k])).every(k => k === 'relays' || k === 'enrol');
    toast(onlyFleet ? 'Применяю…' : 'Применяю: нода перезапускается…');
    try {
      const v = await job('/api/nodecfg', sent);
      const keep = {};
      ndKeys.forEach(k => { if (JSON.stringify(L.ndDraft[k]) !== JSON.stringify(sent[k])) keep[k] = L.ndDraft[k]; });
      L.nd = v;
      L.ndDraft = Object.assign(JSON.parse(JSON.stringify(v)), keep);
      toast(onlyFleet ? 'Применено: нода перечитала ретрансляторы' : 'Применено: нода перезапущена');
    } catch (e) { toast(e.message); }
    render();
    if (ndChanged().length) scheduleNdApply();
  });
  function ndBar() {
    const ch = ndChanged();
    if (!ch || !ch.length) return '';
    const onlyFleet = ch.every(k => k === 'relays' || k === 'enrol');
    return `<div class="note" role="status"><span><b>Не применено: ${ch.length} ${plural(ch.length, 'настройка', 'настройки', 'настроек')}.</b> <span class="mute">${onlyFleet ? 'Нода перечитает ретрансляторы на лету.' : 'Применение перезапустит ноду: подключения прервутся на несколько секунд.'}</span></span>${L.can('node') ? `<span class="note-acts"><button class="btn sm ghost" type="button" onclick="LIVE.ndRevert()">Отменить</button><button class="btn sm pri" type="button" onclick="LIVE.ndApply()">Применить</button></span>` : ''}</div>`;
  }
  function nodeView() {
    if (!L.can('node', 1)) return pageHead('Нода loft', '') + `<div class="empty"><div class="empty-ic">${ic('i-gear', 28)}</div><b>Нет доступа</b><p>Вашей роли этот раздел не виден.</p></div>`;
    if (!L.nd) { loadNode().then(() => { if (S.route === 'node') render(); }); return pageHead('Нода loft', '') + '<p class="mute">Загружаю…</p>'; }
    const t = S.tab.node, tabs = [['main', 'Основное'], ['mask', 'Маскировка'], ['relay', 'Ретрансляторы и CDN'], ['lists', 'Списки'], ['access', 'Доступ']];
    return pageHead('Нода loft', 'Сервер оверлея loft: на каком адресе он принимает членов, как прячется за веб-сервером и какие списки раздаёт клиентам.') + ndBar() +
      `<div class="tabs" role="tablist">${tabs.map(([k, x]) => `<button class="tab ${t === k ? 'on' : ''}" role="tab" aria-selected="${t === k}" type="button" onclick="S.tab.node='${k}';render()">${x}</button>`).join('')}</div>` +
      ({ main: ndMain, mask: ndMask, relay: ndRelay, lists: ndLists, access: ndAccess }[t] || ndMain)();
  }
  const dis = () => L.can('node') ? '' : 'disabled';
  function ndMain() {
    const c = L.nd, d = L.ndDraft, n = c.node || {}, udpDecoy = (c.decoy || []).length > 0;
    return blk('Состояние', '', `<div class="card"><dl class="kv" style="margin-bottom:16px"><div><dt>Роль</dt><dd>${c.role === 'relay' ? 'ретранслятор' : 'контроллер'}</dd></div><div><dt>Версия</dt><dd class="mono">${esc((n.version || '').replace(/^loft-node /, '').replace(/ \(.*/, '') || '—')}</dd></div><div><dt>Работает с</dt><dd>${esc(L.since(n.since))}</dd></div>
        <div><dt>TCP 443</dt><dd>нода loft</dd></div><div><dt>UDP 443</dt><dd>${udpDecoy ? 'нода loft; чужие пакеты → веб-сервер' : 'нода loft'}</dd></div><div><dt>Ключей членов</dt><dd class="num">${c.member_keys}</dd></div></dl>
        <div class="acts" style="margin:0">${L.can('node') ? `<button class="btn" type="button" onclick="LIVE.ndRestart()">Перезапустить</button>` : ''}<button class="btn" type="button" onclick="LIVE.ndLog()">Журнал службы</button></div></div>`) +
      blk('Подключение', 'Адрес для клиентов попадает в ссылки и профили членов.', grid(`
        <div><div class="fld"><label for="nd1">Роль</label><input id="nd1" value="${c.role === 'relay' ? 'Ретранслятор' : 'Контроллер'}" readonly></div><div class="hint">Контроллер держит членов и ключи. Роль задаётся при установке ноды.</div></div>
        <div class="fld"><label for="nd2">Слушать</label><input id="nd2" class="mono" value="${esc(d.listen)}" ${dis()} onchange="LIVE.ndSet('listen',this.value.trim())"></div>
        <div><div class="fld"><label for="nd3">Адрес для клиентов</label><input id="nd3" class="mono" value="${esc(c.advertise.replace(/^https:\/\//, ''))}" readonly></div><div class="hint">Меняется в мастере установки — шаг «Домен и DNS».</div></div>
        <div><div class="fld"><label for="nd4">Сертификат</label><input id="nd4" class="mono" value="${esc((c.cert_names || []).join(', ') || '—')}${c.cert_until ? ' · до ' + new Date(c.cert_until).toLocaleDateString(LOC()) : ''}" readonly></div><div class="hint">Выпускается в мастере установки и продлевается сам.</div></div>`)) +
      blk('Фронты', 'Публичный путь — GET. Остальные фронты включаются здесь, если нужен прямой origin без CDN.',
        `<div class="rules">${FR.map(([k, l, h]) => `<label class="rule chk" style="cursor:pointer"><input type="checkbox" ${d.fronts.includes(k) ? 'checked' : ''} ${dis()} onchange="LIVE.ndFront('${k}',this.checked)"><span><b>${l}</b> <span class="mono mute">${k}</span><span class="hint" style="display:block;margin:0">${h}${udpDecoy && (k === 'quic' || k === 'h3') ? ' С decoy клиент помечает QUIC Initial (quic_mark / h3_mark).' : ''}</span></span></label>`).join('')}</div>
        <div class="fgrid" style="margin-top:20px"><div><div class="fld"><label for="nd5">Соединений HTTP/2 для GET</label><select id="nd5" ${dis()} onchange="LIVE.ndSet('get_h2',+this.value)">${[1, 2, 3, 4].map(x => `<option ${d.get_h2 === x ? 'selected' : ''}>${x}</option>`).join('')}</select></div><div class="hint">Сколько параллельных HTTP/2-соединений клиент держит для фронта GET.</div></div></div>`);
  }
  function ndMask() {
    const d = L.ndDraft, own = L.can('node');
    return blk('Веб-сервер за нодой', 'Всё, что не похоже на клиента loft, нода молча передаёт веб-серверу. Снаружи сервер выглядит как обычный сайт.',
        table(['Адрес', '', { t: '', r: 1 }], d.decoy.map((a, i) => `<tr><td class="mono">${esc(a)}</td><td>${i ? '' : pill('основной · панель и сайт')}</td><td class="r"><div class="cell-acts">${i && own ? `<button class="icb del" type="button" aria-label="Удалить" onclick="LIVE.ndDecoyDel(${i})">${ic('i-trash')}</button>` : ''}</div></td></tr>`).join('') || '<tr><td colspan="3" class="mute">Нет: всё постороннее сбрасывается.</td></tr>') +
        (own ? `<div class="acts" style="margin-top:16px"><button class="btn" type="button" onclick="LIVE.ndDecoyAdd()">${ic('i-plus')}Добавить адрес</button></div>` : '') + '<p class="hint">Первый адрес принимает постороннее; следующие — запасные.</p>') +
      blk('Маскировка рукопожатия', 'Рукопожатие loft прячется в обычный TLS. Ключ маскировки выводится из токена ноды и входит в ссылки членов (pbk).',
        `<div class="card"><label class="sw" style="margin-bottom:16px"><input type="checkbox" ${d.mask ? 'checked' : ''} ${dis()} aria-label="Маскировка" onchange="LIVE.ndSet('mask',this.checked)"><i></i><span>Маскировать рукопожатие</span></label>
        <div class="uri" id="ndpbk" style="margin-bottom:12px">${esc(L.nd.mask_pub || '—')}</div>${L.nd.mask_pub ? `<div class="acts" style="margin:0"><button class="btn sm" type="button" onclick="LIVE.copy('#ndpbk')">Копировать</button></div>` : ''}</div>`) +
      blk('Сайт для посторонних', 'Что видит браузер, открывший адрес ноды, если веб-сервера за нодой нет.', grid(`<div class="fld"><label for="mk1">Каталог сайта</label><input id="mk1" class="mono" value="${esc(d.cover_dir)}" placeholder="встроенная страница" ${dis()} onchange="LIVE.ndSet('cover_dir',this.value.trim())"></div>`)) +
      blk('Пути запросов', 'Пути, похожие на статику и API обычного сайта. Менять их стоит, только если их кто-то начал блокировать — новые ссылки членам придётся выдать заново.', grid(
        [['get_path', 'GET'], ['ws_path', 'WebSocket'], ['h2_path', 'HTTP/2 и HTTP/3'], ['roster_path', 'Список ретрансляторов'], ['lists_path', 'Списки']].map(([k, l]) => `<div class="fld"><label for="pp-${k}">${l}</label><input id="pp-${k}" class="mono" value="${esc(d[k])}" ${dis()} onchange="LIVE.ndSet('${k}',this.value.trim())"></div>`).join('')));
  }
  L.ndDecoyDel = i => { const a = L.ndDraft.decoy.slice(); a.splice(i, 1); L.ndSet('decoy', a); };
  L.ndDecoyAdd = () => dialog('Добавить адрес веб-сервера', 'Запасной адрес, куда нода передаёт постороннее, если первый не отвечает.', `<div class="fld"><label for="dc1">Адрес</label><input id="dc1" class="mono" placeholder="127.0.0.1:9443"></div>`,
    `<button class="btn pri" type="button" onclick="LIVE.ndDecoyAddGo()">Добавить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.ndDecoyAddGo = () => { const v = $('#dc1').value.trim(); if (!/^[^\s:]+:\d+$/.test(v) && !/^\[.+\]:\d+$/.test(v)) { toast('Адрес вида host:port'); return; } closeDlg(); L.ndSet('decoy', L.ndDraft.decoy.concat([v])); };
  function ndRelay() {
    const d = L.ndDraft, own = L.can('node'), live = ((L.pres || {}).relays || []);
    const st = r => { const l = live.find(x => x.id === r.id); return r.drain ? [pill('выведен'), '0', '—'] : l ? [pill('в сети', 'ok'), String(l.conns), agoT(l.beat)] : [pill('не в сети'), '—', '—']; };
    const cf = (d.cdn_peers || []).length;
    return blk('Ретрансляторы', 'Другие серверы, которые принимают членов и передают трафик сюда. Ключей и адресов членов у них нет.',
        `<div class="acts">${own ? `<button class="btn pri" type="button" onclick="LIVE.openRelay(-1)">${ic('i-plus')}Добавить ретранслятор</button>` : ''}<div class="fld" style="margin:0;min-width:min(260px,100%)"><label for="rl0">Кто может подключиться</label><select id="rl0" ${dis()} onchange="LIVE.ndSet('enrol',this.value)"><option value="list" ${d.enrol === 'list' ? 'selected' : ''}>Только из списка</option><option value="open" ${d.enrol === 'open' ? 'selected' : ''}>Любой с токеном</option></select></div></div>` +
        table(['Ретранслятор', 'Адрес для клиентов', 'Фронты', 'Вес', 'Состояние', 'Подключений', 'Отклик'], d.relays.map((r, i) => { const s = st(r); return `<tr class="click" onclick="LIVE.openRelay(${i})"><td><b>${esc(r.id)}</b></td><td class="mono">${esc((r.url || '—').replace(/^https:\/\//, ''))}</td><td>${esc((r.fronts || []).join(', ') || 'get')}</td><td class="num">${r.weight || 100}</td><td>${s[0]}</td><td class="num">${s[1]}</td><td>${s[2]}</td></tr>`; }).join('') || `<tr><td colspan="7" class="mute">Ретрансляторов нет.</td></tr>`)) +
      blk('CDN', 'Члены могут подключаться через CDN по фронту GET. Нода принимает запросы со своим именем от адресов CDN.',
        `<div class="card"><label class="chk" style="margin-bottom:16px"><input type="checkbox" ${cf ? 'checked' : ''} ${dis()} onchange="LIVE.ndCDN(this.checked)">Cloudflare</label>
        <dl class="kv"><div><dt>Диапазоны CDN</dt><dd>${cf ? cf + ' ' + plural(cf, 'подсеть', 'подсети', 'подсетей') : 'нет'}</dd></div></dl>${cf && own ? `<div class="acts" style="margin:16px 0 0"><button class="btn sm" type="button" onclick="LIVE.ndCDN(true)">Обновить диапазоны</button></div>` : ''}</div>`);
  }
  L.ndCDN = async on => {
    if (!on) { L.ndSet('cdn_peers', []); return; }
    try { const r = await api('POST', '/api/nodecfg/cdn', {}); L.ndSet('cdn_peers', r.ranges); toast('Диапазоны Cloudflare получены'); } catch (e) { toast(e.message); render(); }
  };
  L.openRelay = i => {
    const nw = i < 0, r = nw ? { id: '', url: '', fronts: ['get'], weight: 100, drain: false } : L.ndDraft.relays[i], own = L.can('node');
    const rf = r.fronts && r.fronts.length ? r.fronts : ['get'];
    dialog(nw ? 'Новый ретранслятор' : 'Ретранслятор ' + esc(r.id), 'Сервер с нодой loft в роли ретранслятора. Он подключается сюда сам, по токену ноды.', `
      <div class="fld"><label for="ry1">Имя</label><input id="ry1" class="mono" value="${esc(r.id)}" ${nw ? '' : 'readonly'} placeholder="de-1"></div>
      <div class="fld"><label for="ry2">Адрес для клиентов</label><input id="ry2" class="mono" value="${esc(r.url)}" placeholder="https://de1.example.net"></div>
      <div><div class="dlg-sec" style="margin:0">Фронты</div><div class="chk-row">${FR.map(([k, l]) => `<label class="chk"><input type="checkbox" data-rf="${k}" ${rf.includes(k) ? 'checked' : ''}>${l}</label>`).join('')}</div></div>
      <div><div class="fld"><label for="ry3">Вес</label><input id="ry3" class="num" value="${r.weight || 100}"></div><div class="hint">Чем больше, тем чаще клиенты выбирают этот ретранслятор (1–255).</div></div>
      <label class="chk"><input id="ry4" type="checkbox" ${r.drain ? 'checked' : ''}>Вывести<span class="hint" style="display:block;margin:0">Новые клиенты сюда не идут; подключение ретранслятора закрывается.</span></label>`,
      own ? `<button class="btn pri" type="button" onclick="LIVE.relaySave(${i})">${nw ? 'Добавить' : 'Готово'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>${nw ? '' : `<button class="icb del" type="button" aria-label="Удалить ретранслятор" style="margin-left:auto" onclick="LIVE.relayDel(${i})">${ic('i-trash')}</button>`}` : `<button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`);
  };
  L.relaySave = i => {
    const fr = [...document.querySelectorAll('[data-rf]')].filter(x => x.checked).map(x => x.dataset.rf);
    if (!fr.length) { toast('Нужен хотя бы один фронт'); return; }
    const r = { id: $('#ry1').value.trim(), url: $('#ry2').value.trim(), fronts: fr, weight: parseInt($('#ry3').value, 10) || 100, drain: $('#ry4').checked };
    if (!/^[a-z][a-z0-9-]{0,11}$/.test(r.id)) { toast('Имя: латинская буква, затем буквы, цифры или дефис'); return; }
    const all = L.ndDraft.relays.slice();
    if (i < 0) all.push(r); else all[i] = r;
    closeDlg(); L.ndSet('relays', all);
  };
  L.relayDel = i => { const all = L.ndDraft.relays.slice(); all.splice(i, 1); closeDlg(); L.ndSet('relays', all); };
  function ndLists() {
    const lists = L.nd.lists || {}, own = L.can('node');
    const names = Object.keys(lists).sort();
    return blk('Списки', 'Подписанные нодой списки доменов и подсетей. Клиент сам решает, что из списка вести через сервер, напрямую или блокировать; правила выхода во «Внешних каналах» тоже на них ссылаются.',
      (own ? `<div class="acts"><button class="btn" type="button" onclick="LIVE.openList('')">${ic('i-plus')}Создать список</button><label class="btn" style="cursor:pointer">${ic('i-up')}Загрузить из файла<input type="file" accept=".txt,.list,text/plain" hidden onchange="LIVE.listFile(this)"></label></div>` : '') +
      table(['Список', 'Записей', 'Что внутри'], names.map(n => `<tr class="click" onclick="LIVE.openList('${esc(n)}')"><td class="mono"><b>${esc(n)}</b></td><td class="num">${lists[n].length.toLocaleString(LOC())}</td><td class="mono mute">${esc(lists[n].slice(0, 3).join(', '))}${lists[n].length > 3 ? ', …' : ''}</td></tr>`).join('') || `<tr><td colspan="3" class="mute">Списков нет.</td></tr>`) +
      '<p class="hint">Запись — домен (example.com), домен со всеми поддоменами (.example.com) или подсеть (198.51.100.0/24).</p>');
  }
  const saveLists = async (lists, msg) => {
    toast('Сохраняю списки…');
    try { const v = await job('/api/nodecfg/lists', { lists }); L.nd = v; L.ndDraft.lists = v.lists; closeDlg(); toast(msg); } catch (e) { toast(e.message); }
    render();
  };
  L.openList = name => {
    const nw = !name, l = nw ? [] : L.nd.lists[name], own = L.can('node');
    dialog(nw ? 'Новый список' : 'Список ' + esc(name), 'По записи на строку: домен, .домен со всеми поддоменами или подсеть.', `
      <div class="fld"><label for="ls1">Имя</label><input id="ls1" class="mono" value="${esc(name)}" ${nw ? '' : 'readonly'} placeholder="blocked"></div>
      <div class="fld"><label for="ls2">Записи</label><textarea id="ls2" class="mono ta" rows="10" ${own ? '' : 'readonly'}>${esc(l.join('\n'))}</textarea></div>`,
      own ? `<button class="btn pri" type="button" onclick="LIVE.listSave(${nw})">${nw ? 'Создать' : 'Сохранить'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>${nw ? '' : `<button class="icb del" type="button" aria-label="Удалить список" style="margin-left:auto" onclick="LIVE.listDel('${esc(name)}')">${ic('i-trash')}</button>`}` : `<button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`);
  };
  const entries = t => [...new Set(t.split(/\r?\n/).map(x => x.replace(/#.*/, '').trim()).filter(Boolean))];
  L.listSave = nw => {
    const n = $('#ls1').value.trim().toLowerCase(), e = entries($('#ls2').value);
    if (!/^[a-z0-9][a-z0-9._-]{0,31}$/.test(n)) { toast('Имя списка: строчная латиница, цифры, точка, дефис'); return; }
    if (nw && L.nd.lists[n]) { toast('Список с таким именем уже есть'); return; }
    if (!e.length) { toast('В списке нет записей'); return; }
    saveLists({ ...L.nd.lists, [n]: e }, 'Список сохранён — нода перечитала списки');
  };
  L.listDel = n => { const all = { ...L.nd.lists }; delete all[n]; saveLists(all, 'Список удалён'); };
  L.listFile = inp => {
    const f = inp.files[0]; inp.value = '';
    if (!f) return;
    f.text().then(t => { L.openList(''); $('#ls1').value = f.name.replace(/\.[^.]*$/, '').toLowerCase().replace(/[^a-z0-9._-]+/g, '-').slice(0, 32); $('#ls2').value = entries(t).join('\n'); });
  };
  function ndAccess() {
    const d = L.ndDraft, own = L.can('node'), pub = L.nd.controller_pub || '';
    return blk('Ключ контроллера', 'Клиенты сверяют по нему подпись списка ретрансляторов и списков. Входит в файл .loft.',
        `<div class="uri" id="ctlpub" style="margin-bottom:12px">${pub ? 'ed25519:' + esc(pub) : '—'}</div><div class="acts"><button class="btn sm" type="button" onclick="LIVE.copy('#ctlpub')">Копировать</button></div>`) +
      blk('Общий токен', 'Вход по одному токену вместо личных ключей. Работает, только пока в сети нет ни одного члена с ключом.',
        `<label class="sw"><input type="checkbox" ${d.auth_shared ? 'checked' : ''} ${dis()} aria-label="Общий токен" onchange="LIVE.ndSet('auth_shared',this.checked)"><i></i><span>Разрешить вход по общему токену</span></label>`) +
      (own ? blk('Смена токена ноды', 'Меняет ключ маскировки (pbk) и токен для ретрансляторов. Ключ контроллера и ключи членов остаются; при включённой маскировке ссылки членов придётся выдать заново.',
        `<button class="btn danger" type="button" onclick="LIVE.ndTokenAsk()">Сменить токен…</button>`) : '') +
      blk('Диагностика', '', `<label class="sw"><input type="checkbox" ${d.trace ? 'checked' : ''} ${dis()} aria-label="Журнал сессий" onchange="LIVE.ndSet('trace',this.checked)"><i></i><span>Писать журнал сессий <span class="mute">/var/lib/loft/trace.log</span></span></label>`);
  }
  L.ndTokenAsk = () => dialog('Сменить токен ноды?', 'Нода перезапустится. Ретрансляторам нужен будет новый токен; при маскировке члены получат новый ключ pbk только с новыми ссылками.', '',
    `<button class="btn danger" type="button" onclick="LIVE.ndToken()">Сменить токен</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.ndToken = async () => { closeDlg(); toast('Меняю токен: нода перезапускается…'); try { const v = await job('/api/nodecfg/token', {}); L.nd = v; L.ndDraft = JSON.parse(JSON.stringify(v)); toast('Токен ноды сменён'); } catch (e) { toast(e.message); } render(); };
  L.ndRestart = async () => { toast('Перезапускаю ноду…'); try { await job('/api/node/restart', {}); toast('Нода перезапущена'); await loadNode(); } catch (e) { toast(e.message); } render(); };
  L.ndLog = async () => {
    try { const l = await api('GET', '/api/nodecfg/log'); dialog('Журнал службы loft-node', 'Последние 300 строк.', `<div class="joblog" style="max-height:60vh;overflow:auto">${esc(l.text || '—')}</div>`, `<button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`); } catch (e) { toast(e.message); }
  };

  /* who is online: the node's report, the panel's memory of last seen */
  async function loadPresence() {
    try { L.pres = await api('GET', '/api/presence'); } catch (e) { return; }
    for (const id of Object.keys(L.pres.members || {})) {
      const x = L.pres.members[id];
      EXTRA[id] = [x.front ? 'loft · ' + x.front : 'loft', `${x.online} / ${Math.max(x.devices, x.online)}`, x.online ? 'сейчас' : agoT(x.last), x.online ? 1 : 0];
    }
  }
  L.loadPresence = loadPresence;

  /* ── Mail: domains, boxes, aliases, DNS; the helper writes the daemons' files ── */
  L.ml = null; L.mlDom = null; L.mlDNS = null; L.mlStats = null;
  const ML_EMPTY = { installed: false, domains: [], boxes: [], aliases: [], services: [], users: [], zones: [], web_hosts: [] };
  async function loadMail() {
    try { L.ml = await api('GET', '/api/mail'); } catch (e) { toast('Почта не прочитана: ' + e.message); L.ml = L.ml || ML_EMPTY; }
    if (!L.mlDom || !L.ml.domains.some(d => d.name === L.mlDom)) L.mlDom = (L.ml.domains[0] || {}).name || null;
  }
  async function loadMailDNS() {
    L.mlDNS = null;
    if (!L.mlDom) return;
    try { L.mlDNS = await api('GET', '/api/mail/dns?domain=' + encodeURIComponent(L.mlDom)); } catch (e) { L.mlDNS = { error: e.message }; }
  }
  async function loadMailStats() { try { L.mlStats = await api('GET', '/api/mail/stats'); } catch (e) { /* the line stays out */ } }
  const mlRe = () => { if (S.route === 'mail') render(); };
  const inDom = (a, d) => a.endsWith('@' + d);
  const gb = mb => mb >= 1024 ? (mb / 1024).toFixed(mb % 1024 ? 1 : 0) + ' ГБ' : Math.round(mb) + ' МБ';
  const usedCell = b => {
    const u = b.used_mb || 0, q = b.quota_mb, g = mb => String(+(mb / 1024).toFixed(mb >= 10240 ? 0 : 1));
    const t = !q ? gb(u) + ' · без ограничения' : q >= 1024 ? `${g(u)} / ${g(q)} ГБ` : `${Math.round(u)} / ${q} МБ`;
    return `<span class="qbar"><i style="width:${q ? Math.min(100, Math.round(u / q * 100)) : 0}%"></i></span><span class="num mute" style="margin-left:8px">${t}</span>`;
  };
  const boxState = b => !b.enabled ? pill('выключен') : b.send_only ? pill('только отправка', 'ok') : pill('активен', 'ok');
  const SVC = { postfix: 'Postfix · SMTP', dovecot: 'Dovecot · IMAP', opendkim: 'OpenDKIM · подпись', rspamd: 'Rspamd · антиспам' };
  const svcPill = s => s === 'active' ? pill('работает', 'ok') : s === 'absent' ? pill('не установлен', 'busy') : s === 'activating' ? pill('запускается…', 'busy') : pill('не работает', 'warn');
  const REC = { mx: 'MX', spf: 'SPF', helo: 'SPF', dkim: 'DKIM', dmarc: 'DMARC' };
  const recPill = r => r.state === 'ok' ? pill(r.now ? 'своя запись' : 'совпадает', 'ok') : r.state === 'missing' ? pill('нет записи', 'warn') : pill('другое значение', 'warn');
  const evRow = (k, name, val, right, plain) => `<div class="ev"><span class="mono" style="flex:0 0 64px">${k}</span><span style="flex:1 1 auto;min-width:0"><span class="mono" style="display:block;overflow-wrap:anywhere">${name}</span>${val ? `<span class="${plain ? '' : 'mono '}hint" style="display:block;margin:2px 0 0;overflow-wrap:anywhere">${val}</span>` : ''}</span><span>${right}</span></div>`;
  // Long TXT values (DKIM) are cut on screen; the button copies them whole.
  const longVal = v => v.length <= 72 ? esc(v) : `${esc(v.slice(0, 64))}… <button class="btn sm" type="button" style="margin-left:4px" onclick="navigator.clipboard.writeText(${esc(JSON.stringify(v))}).then(()=>toast('Значение скопировано'))">Скопировать</button>`;

  function mailView() {
    if (!L.can('mail', 1)) return pageHead('Почта', '') + `<div class="empty"><div class="empty-ic">${ic('i-gear', 28)}</div><b>Нет доступа</b><p>Вашей роли этот раздел не виден.</p></div>`;
    if (!L.ml) { loadMail().then(() => Promise.all([loadMailDNS(), loadMailStats()])).then(mlRe); return pageHead('Почта', '') + '<p class="mute">Загружаю…</p>'; }
    const M = L.ml, own = L.can('mail');
    const head = pageHead('Почта', 'Почтовые домены, ящики и псевдонимы, записи DNS для доставки и службы почты.');
    if (!M.installed) return head + `<div class="empty"><div class="empty-ic">${ic('i-gear', 28)}</div><b>Почтовый сервер не установлен</b><p>Поставьте пакет «Почтовый сервер» в <a href="#packages">Пакетах</a> — домены и ящики заводятся здесь.</p></div>`;
    const bad = M.error ? `<div class="note bad" role="alert"><span><b>Настройка почты не применилась.</b> <span class="mute">${esc(ru(M.error))} Почта работает с прежними настройками.</span></span>${own ? `<span class="note-acts"><button class="btn sm" type="button" onclick="LIVE.mailApply()">Применить снова</button></span>` : ''}</div>` : '';
    if (!M.domains.length) return head + bad + `<div class="empty"><div class="empty-ic">${ic('i-gear', 28)}</div><b>Почтовых доменов нет</b><p>Добавьте домен — сервер начнёт принимать для него почту.</p>${own ? `<button class="btn pri" type="button" onclick="LIVE.openMDom('')">${ic('i-plus')}Новый домен</button>` : ''}</div>`;
    const D = M.domains.find(d => d.name === L.mlDom) || M.domains[0], h = 'mail.' + D.name;
    const boxes = M.boxes.filter(b => inDom(b.address, D.name)), al = M.aliases.filter(a => inDom(a.from, D.name));
    let o = head + bad + `<div class="netbar"><div class="fld" style="margin:0;min-width:220px"><label for="mdz">Домен</label><select id="mdz" onchange="LIVE.pickMDom(this.value)">${M.domains.map(d => `<option ${d.name === D.name ? 'selected' : ''} value="${esc(d.name)}">${esc(d.name)}${d.enabled ? '' : ' · выключен'}</option>`).join('')}</select></div>
      ${own ? `<button class="btn" type="button" style="align-self:flex-end" onclick="LIVE.openMDom('${esc(D.name)}')">Настройки домена</button><button class="btn" type="button" style="align-self:flex-end" onclick="LIVE.openMDom('')">${ic('i-plus')}Новый домен</button>` : ''}</div>`;
    if (!D.enabled) o += `<div class="note" role="status"><span><b>Домен выключен.</b> <span class="mute">Ящики хранятся, но почту для ${esc(D.name)} сервер не принимает и войти в них нельзя.</span></span></div>`;
    o += blk('Ящики', '', `<div class="acts">${own ? `<button class="btn pri" type="button" onclick="LIVE.openBox(-1)">${ic('i-plus')}Новый ящик</button>` : ''}${M.web_hosts.includes(h) ? `<a class="btn" href="https://${esc(h)}/" target="_blank" rel="noopener">Открыть веб-почту</a>` : ''}</div>` +
      table(['Адрес', 'Владелец', 'Занято', 'Пересылка', 'Состояние'], boxes.map(b => `<tr class="click" onclick="LIVE.openBox(${M.boxes.indexOf(b)})"><td class="mono"><b>${esc(b.address)}</b></td><td>${esc(b.owner || '—')}</td><td>${usedCell(b)}</td><td class="mono mute" style="white-space:normal">${esc((b.forwards || []).join(', ') || '—')}</td><td>${boxState(b)}</td></tr>`).join('') || `<tr><td colspan="5" class="mute">Ящиков нет.</td></tr>`));
    o += blk('Псевдонимы', 'Адреса без своего ящика: письма уходят в указанные ящики.', (own ? `<div class="acts"><button class="btn" type="button" onclick="LIVE.openAlias(-1)">${ic('i-plus')}Добавить псевдоним</button></div>` : '') +
      table(['Адрес', 'Куда'], al.map(a => `<tr class="click" onclick="LIVE.openAlias(${M.aliases.indexOf(a)})"><td class="mono">${esc(a.from)}</td><td class="mono mute" style="white-space:normal">${esc(a.to.join(', '))}</td></tr>`).join('') +
        `<tr class="click" onclick="LIVE.openMDom('${esc(D.name)}')"><td class="mono mute">*@${esc(D.name)}</td><td class="mute">${(D.catch_all || []).length ? `<span class="mono">${esc(D.catch_all.join(', '))}</span>` : 'ловить всё — выключено'}</td></tr>`));
    o += `<div class="cards">` + mailDNSCard(D, own) + mailSvcCard(M, h) + `</div>`;
    return o;
  }
  function mailDNSCard(D, own) {
    const N = L.mlDNS, h = 'mail.' + D.name;
    let c = `<section class="card"><div class="card-h"><h2>Записи DNS</h2><button class="btn sm" type="button" style="margin-left:auto" onclick="LIVE.mailCheck()">Проверить</button></div>`;
    if (!N) return c + '<p class="mute">Проверяю записи…</p></section>';
    if (N.error) return c + `<p class="mute">${esc(ru(N.error))}</p></section>`;
    const want = r => longVal((r.prio ? r.prio + ' ' : '') + r.value);
    c += N.records.map(r => evRow(REC[r.key] || r.type, esc(r.name), r.state === 'differs' ? `${longVal(r.now || '—')}<br>нужно: ${want(r)}` : r.state === 'ok' && r.now ? longVal(r.now) : want(r), recPill(r))).join('');
    c += (N.ptr || []).map(p => evRow('PTR', esc(p.ip), esc(p.now ? p.now + (p.state === 'ok' ? '' : ' · нужно ' + p.want) : 'нет · нужно ' + p.want), p.state === 'ok' ? pill('совпадает', 'ok') : pill('задаётся у хостера', 'warn'))).join('');
    c += evRow('TLS', esc(h), D.cert ? 'сертификат до ' + new Date(D.cert_until).toLocaleDateString(LOC()) : 'сертификата нет: почтовые программы предупредят о самоподписанном', D.cert ? pill('есть', 'ok') : own ? `<button class="btn sm" type="button" onclick="LIVE.mailCert('${esc(D.name)}')">Выпустить</button>` : pill('нет', 'warn'), true);
    if (own && N.api && N.records.some(r => r.auto)) c += `<div class="acts" style="margin:16px 0 8px"><button class="btn" type="button" onclick="LIVE.mailDNSFix()">Создать недостающие</button></div>`;
    const ptr = (N.ptr || []).filter(p => p.state !== 'ok').map(p => p.ip);
    c += `<p class="hint" style="margin:${own && N.api ? 0 : '16px 0 0'}">${N.api ? `Зона ${esc(N.zone)} ведётся через DNS-провайдера — панель создаст недостающие и поправит чужой MX, A и DKIM; чужие SPF и DMARC не трогает.` : N.zone ? `Зона ${esc(N.zone)} ведётся вручную: внесите эти записи у регистратора.` : 'Домена нет в «Доменах»: внесите записи у регистратора или добавьте зону с DNS-провайдером.'}${ptr.length ? ` PTR для ${esc(ptr.join(', '))} ставится в кабинете хостера: <span class="mono">${esc((N.ptr[0] || {}).want || '')}</span>.` : ''}</p></section>`;
    return c;
  }
  function mailSvcCard(M, h) {
    const st = L.mlStats;
    return `<section class="card"><div class="card-h"><h2>Службы</h2></div>` +
      M.services.map(s => `<div class="ev"><span style="flex:1 1 auto">${SVC[s.key] || esc(s.key)}</span>${svcPill(s.state)}</div>`).join('') +
      (st ? `<p class="hint" style="margin:12px 0 0">Очередь отправки: ${st.queue} · за сутки доставлено в ящики ${st.received}, отправлено ${st.sent}, отклонено ${st.rejected}.</p>` : '') +
      `<dl class="kv" style="margin-top:16px"><div><dt>IMAP</dt><dd class="mono">${esc(h)}:993 · SSL/TLS</dd></div><div><dt>SMTP</dt><dd class="mono">${esc(h)}:465 · SSL/TLS, 587 · STARTTLS</dd></div><div><dt>Логин</dt><dd>адрес ящика целиком</dd></div></dl></section>`;
  }
  L.pickMDom = d => { L.mlDom = d; loadMailDNS().then(mlRe); render(); };
  L.mailCheck = async () => { L.mlDNS = null; render(); toast('Проверяю записи в DNS…'); await loadMailDNS(); render(); };
  // A save answers with the whole state; a failed apply comes back as an
  // error and is shown from the reloaded state.
  async function mailPost(path, body, done) {
    toast('Сохраняю…');
    try { L.ml = await api('POST', path, body); closeDlg(); if (done) toast(done); } catch (e) { toast(e.message); await loadMail(); }
    if (!L.ml.domains.some(d => d.name === L.mlDom)) L.mlDom = (L.ml.domains[0] || {}).name || null;
    render(); loadMailDNS().then(mlRe);
  }
  L.mailApply = () => mailPost('/api/mail/apply', {}, 'Настройка почты применена');
  L.mailCert = async d => {
    toast('Выпускаю сертификат для mail.' + d + '…');
    try { await job('/api/mail/cert', { domain: d }); toast('Сертификат для mail.' + d + ' выпущен'); } catch (e) { toast(e.message); }
    await loadMail(); render();
  };
  L.mailDNSFix = once(async () => {
    toast('Создаю записи DNS…');
    try { const r = await api('POST', '/api/mail/dns', { domain: L.mlDom }); toast(r.created ? 'Создано записей: ' + r.created : 'Создавать нечего'); } catch (e) { toast(e.message); }
    await L.mailCheck();
  });
  const addrs = v => v.split(/[\s,;]+/).map(x => x.trim()).filter(Boolean);
  L.openMDom = name => {
    const nw = !name, D = nw ? { name: '', enabled: true, dkim: true, catch_all: [], selector: '' } : L.ml.domains.find(d => d.name === name);
    const zl = (L.ml.zones || []).filter(z => !L.ml.domains.some(d => d.name === z)).map(z => `<option value="${esc(z)}">`).join('');
    dialog(nw ? 'Новый почтовый домен' : 'Домен ' + esc(name), nw ? 'Сервер станет принимать почту для домена. Записи DNS и сертификат для mail.&lt;домен&gt; — на этой же странице.' : '', `
      <div><div class="fld"><label for="md1">Домен</label><input id="md1" class="mono" value="${esc(D.name)}" placeholder="example.net" ${nw ? '' : 'readonly'} list="mdl"><datalist id="mdl">${zl}</datalist></div><div class="hint">Имя почтового сервера — mail.${esc(D.name) || '&lt;домен&gt;'}: MX, IMAP и SMTP для программ, веб-почта.</div></div>
      <label class="chk"><input id="md4" type="checkbox" ${D.enabled ? 'checked' : ''}><span>Принимать почту<span class="hint" style="display:block;margin:0">Выключенный домен хранит ящики, но писем не принимает.</span></span></label>
      <label class="chk"><input id="md5" type="checkbox" ${D.dkim ? 'checked' : ''}>Подписывать исходящие письма DKIM</label>
      <div><div class="fld"><label for="md2">Ловить всё — письма на несуществующие адреса</label><input id="md2" class="mono" value="${esc((D.catch_all || []).join(', '))}" placeholder="пусто — отказывать"></div><div class="hint">Ящики этого сервера или внешние адреса, через запятую. Ловушка собирает спам — включайте, только если нужна.</div></div>
      <details class="adv"><summary>Дополнительно</summary><div class="fld" style="margin-top:12px"><label for="md3">Селектор DKIM</label><input id="md3" class="mono" value="${esc(D.selector || 'mail')}"></div><div class="hint">Смена селектора создаёт новый ключ; старая запись в DNS станет не нужна.</div></details>`,
      `<button class="btn pri" type="button" onclick="LIVE.mdSave(${nw})">${nw ? 'Добавить' : 'Сохранить'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>${nw ? '' : `<button class="icb del" type="button" aria-label="Удалить домен" style="margin-left:auto" onclick="LIVE.mdDel('${esc(name)}')">${ic('i-trash')}</button>`}`);
  };
  L.mdSave = async nw => {
    const name = $('#md1').value.trim().toLowerCase(), sel = $('#md3').value.trim();
    if (nw) L.mlDom = name;
    await mailPost('/api/mail/domain', { name, enabled: $('#md4').checked, dkim: $('#md5').checked, selector: sel === 'mail' ? '' : sel, catch_all: addrs($('#md2').value) }, nw ? 'Домен ' + name + ' добавлен' : 'Домен сохранён');
  };
  L.mdDel = name => dialog(`Удалить почтовый домен «${esc(name)}»?`, 'Уйдут его ящики и псевдонимы, сервер перестанет принимать для него почту.', '<label class="chk"><input id="mdd" type="checkbox">Удалить и письма с диска</label>',
    `<button class="btn danger" type="button" onclick="LIVE.mdDelGo('${esc(name)}')">Удалить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.mdDelGo = name => mailPost('/api/mail/domain', { name, delete: true, drop_mail: $('#mdd').checked }, 'Домен удалён');
  const QUOTAS = [[1024, '1 ГБ'], [3072, '3 ГБ'], [10240, '10 ГБ'], [0, 'без ограничения']];
  L.genPw = id => {
    const a = 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789', b = new Uint8Array(14);
    crypto.getRandomValues(b);
    const el = $('#' + id); el.value = Array.from(b, x => a[x % a.length]).join(''); el.type = 'text';
    el.dispatchEvent(new Event('input'));
  };
  L.openBox = i => {
    const nw = i < 0, D = L.mlDom, b = nw ? { address: '', owner: L.me.login, quota_mb: 3072, forwards: [], send_only: false, enabled: true } : L.ml.boxes[i], own = L.can('mail');
    const q = QUOTAS.some(x => x[0] === b.quota_mb) ? QUOTAS : QUOTAS.concat([[b.quota_mb, gb(b.quota_mb)]]);
    const host = 'mail.' + (nw ? D : b.address.split('@')[1]);
    dialog(nw ? 'Новый ящик' : esc(b.address), nw ? 'Пароль покажется один раз; потом его можно только сменить.' : '', `
      <div class="fld"><label for="mb1">Адрес</label><div style="display:flex;align-items:center;gap:6px"><input id="mb1" class="mono" value="${esc(b.address.split('@')[0])}" placeholder="name" ${nw ? '' : 'readonly'} style="flex:1 1 auto"><span class="mono mute">@${esc(nw ? D : b.address.split('@')[1])}</span></div></div>
      <div class="fld"><label for="mb2">Владелец</label><select id="mb2">${(L.ml.users || []).map(u => `<option value="${esc(u)}" ${u === b.owner ? 'selected' : ''}>${esc(u)}</option>`).join('')}<option value="" ${b.owner ? '' : 'selected'}>— без пользователя панели</option></select></div>
      <div><div class="fld"><label for="mb3">Пароль</label><input id="mb3" type="password" autocomplete="new-password" placeholder="${nw ? '' : 'не менять'}" data-pw-min="9" ${nw ? '' : 'data-pw-optional'}></div><button class="btn sm" type="button" style="margin-top:10px" onclick="LIVE.genPw('mb3')">Сгенерировать</button></div>
      <div class="fld"><label for="mb4">Объём ящика</label><select id="mb4">${q.map(([v, l]) => `<option value="${v}" ${v === b.quota_mb ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
      <div class="fld"><label for="mb5">Пересылать копии на</label><input id="mb5" class="mono" value="${esc((b.forwards || []).join(', '))}" placeholder="адрес, через запятую"></div>
      <label class="chk"><input id="mb6" type="checkbox" ${b.send_only ? 'checked' : ''}>Только отправка — входящие не принимать</label>
      <label class="chk"><input id="mb7" type="checkbox" ${b.enabled ? 'checked' : ''}>Ящик включён</label>
      ${nw ? '' : `<div class="dlg-sec">Настройка почтовой программы</div><dl class="kv"><div><dt>IMAP</dt><dd class="mono">${esc(host)}:993</dd></div><div><dt>SMTP</dt><dd class="mono">${esc(host)}:465 / 587</dd></div><div><dt>Логин</dt><dd class="mono">${esc(b.address)}</dd></div></dl>`}`,
      own ? `<button class="btn pri" type="button" onclick="LIVE.boxSave(${i})">${nw ? 'Создать' : 'Сохранить'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>${nw ? '' : `<button class="icb del" type="button" aria-label="Удалить ящик" style="margin-left:auto" onclick="LIVE.boxDel(${i})">${ic('i-trash')}</button>`}` : `<button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`);
  };
  L.boxSave = async i => {
    if (!pwCheck($('#dlgRoot'), true)) { toast('Проверьте пароль'); return; }
    const nw = i < 0, address = nw ? $('#mb1').value.trim().toLowerCase() + '@' + L.mlDom : L.ml.boxes[i].address, password = $('#mb3').value;
    const body = { address, owner: $('#mb2').value, password, quota_mb: +$('#mb4').value, forwards: addrs($('#mb5').value), send_only: $('#mb6').checked, enabled: $('#mb7').checked };
    toast('Сохраняю…');
    try { L.ml = await api('POST', '/api/mail/box', body); } catch (e) { toast(e.message); await loadMail(); render(); return; }
    render();
    const host = 'mail.' + address.split('@')[1];
    if (nw || password) dialog(nw ? `Ящик ${esc(address)} создан` : `Пароль ${esc(address)} изменён`, 'Пароль показан один раз — передайте его владельцу ящика.', `<dl class="kv"><div><dt>Логин</dt><dd class="mono">${esc(address)}</dd></div><div><dt>Пароль</dt><dd class="mono" id="mbpw">${esc(password)}</dd></div><div><dt>IMAP</dt><dd class="mono">${esc(host)}:993</dd></div><div><dt>SMTP</dt><dd class="mono">${esc(host)}:465 / 587</dd></div></dl>`,
      `<button class="btn pri" type="button" onclick="closeDlg()">Готово</button><button class="btn" type="button" onclick="navigator.clipboard.writeText($('#mbpw').textContent).then(()=>toast('Пароль скопирован'))">Скопировать пароль</button>`);
    else { closeDlg(); toast('Ящик сохранён'); }
  };
  L.boxDel = i => { const a = L.ml.boxes[i].address; dialog(`Удалить ящик ${esc(a)}?`, 'Войти в него больше не получится; письма на этот адрес будут отклоняться.', '<label class="chk"><input id="mbd" type="checkbox">Удалить и письма с диска</label>',
    `<button class="btn danger" type="button" onclick="LIVE.boxDelGo(${i})">Удалить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`); };
  L.boxDelGo = i => mailPost('/api/mail/box', { address: L.ml.boxes[i].address, delete: true, drop_mail: $('#mbd').checked }, 'Ящик удалён');
  L.openAlias = i => {
    const nw = i < 0, a = nw ? { from: '', to: [] } : L.ml.aliases[i], own = L.can('mail'), d = nw ? L.mlDom : a.from.split('@')[1];
    dialog(nw ? 'Новый псевдоним' : esc(a.from), '', `<div class="fld"><label for="al1">Адрес</label><div style="display:flex;align-items:center;gap:6px"><input id="al1" class="mono" value="${esc(a.from.split('@')[0])}" placeholder="info" style="flex:1 1 auto"><span class="mono mute">@${esc(d)}</span></div></div>
      <div><div class="fld"><label for="al2">Куда доставлять</label><input id="al2" class="mono" value="${esc(a.to.join(', '))}"></div><div class="hint">Ящики этого сервера или внешние адреса, через запятую. Ящики из списка могут и отправлять письма с этого адреса.</div></div>`,
      own ? `<button class="btn pri" type="button" onclick="LIVE.aliasSave(${i})">${nw ? 'Добавить' : 'Сохранить'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>${nw ? '' : `<button class="icb del" type="button" aria-label="Удалить псевдоним" style="margin-left:auto" onclick="LIVE.aliasDel(${i})">${ic('i-trash')}</button>`}` : `<button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`);
  };
  L.aliasSave = i => {
    const old = i < 0 ? '' : L.ml.aliases[i].from, d = old ? old.split('@')[1] : L.mlDom;
    return mailPost('/api/mail/alias', { from: $('#al1').value.trim().toLowerCase() + '@' + d, to: addrs($('#al2').value), old }, 'Псевдоним сохранён');
  };
  L.aliasDel = i => mailPost('/api/mail/alias', { from: L.ml.aliases[i].from, to: [], delete: true }, 'Псевдоним удалён');

  /* ── System: the guard (fail2ban), backups, Linux accounts ── */
  L.sy = null;
  async function loadSystem() {
    const [u, g, b] = await Promise.allSettled([api('GET', '/api/system/users'), api('GET', '/api/system/guard'), api('GET', '/api/system/backup')]);
    L.sy = { users: u.value || null, uerr: u.reason && u.reason.message, guard: g.value || null, gerr: g.reason && g.reason.message, bk: b.value || null, berr: b.reason && b.reason.message };
  }
  const syRe = () => { if (S.route === 'system') render(); };
  const BANS = [[3600, '1 час'], [86400, '24 часа'], [604800, '7 дней'], [-1, 'навсегда']];
  const banT = s => (BANS.find(x => x[0] === s) || [0, s >= 86400 ? Math.round(s / 86400) + ' дн.' : Math.round(s / 3600) + ' ч'])[1];
  const leftT = until => {
    if (!until) return 'навсегда';
    const m = Math.max(0, (new Date(until) - Date.now()) / 60e3);
    return m < 60 ? Math.ceil(m) + ' мин' : m < 48 * 60 ? Math.round(m / 60) + ' ч' : Math.round(m / 1440) + ' дн.';
  };
  const JAIL = { 'loft-panel': 'перебор паролей панели', sshd: 'перебор паролей ssh', dovecot: 'перебор паролей почты', 'postfix-sasl': 'перебор паролей почты' };
  const banWhy = b => {
    if (b.jail === 'loft-scan') { const m = String(b.what || '').match(/"([A-Z]+) (\S+)/); return 'сканер' + (m ? ': ' + m[1] + ' ' + m[2] : ''); }
    if (String(b.jail).startsWith('loft-manual')) return 'вручную' + (b.reason ? ': ' + b.reason : '');
    return JAIL[b.jail] || b.jail;
  };
  const SCHED = { daily: 'ежедневно 04:00', weekly: 'по воскресеньям 04:00', off: 'не делать сами' };
  const PARTS = { node: 'Нода loft: настройки, сети, ключи членов', panel: 'Панель: пользователи, домены, каналы, почтовые настройки', mail: 'Почта: ящики и письма' };
  const SKIND = { s3: 'S3', sftp: 'SFTP', dir: 'Каталог' };
  const PNAME = { node: 'нода', panel: 'панель', mail: 'почта' };
  const RIGHTS = { '': 'без sudo', password: 'sudo — с паролем', nopasswd: 'sudo — без пароля' };
  const stLabel = (B, id) => { const s = B.stores.find(x => x.id === id); return s ? s.label.split(' · ')[0] + ' · ' + SKIND[s.kind] : 'этот сервер'; };
  const whenT = t => { const d = new Date(t), now = new Date(), y = new Date(now - 864e5); const hm = d.toLocaleTimeString(LOC(), { hour: '2-digit', minute: '2-digit' }); return d.toDateString() === now.toDateString() ? 'сегодня ' + hm : d.toDateString() === y.toDateString() ? 'вчера ' + hm : d.toLocaleDateString(LOC(), { day: '2-digit', month: '2-digit' }) + ' ' + hm; };
  const entry = (u, U) => {
    if (u.locked) return pill('заблокирован', 'warn');
    if (u.root && U.root_login === 'no') return 'вход root запрещён';
    const k = u.keys.length, pw = U.password_auth && u.password && (!u.root || U.root_login === 'yes');
    return k && pw ? 'ключ и пароль' : k ? 'только ключ' : pw ? 'только пароль' : 'входа нет';
  };

  function systemView() {
    if (!L.can('system', 1)) return pageHead('Система', '') + `<div class="empty"><div class="empty-ic">${ic('i-gear', 28)}</div><b>Нет доступа</b><p>Вашей роли этот раздел не виден.</p></div>`;
    if (!L.sy) { loadSystem().then(syRe); return pageHead('Система', '') + '<p class="mute">Загружаю…</p>'; }
    const own = L.can('system');
    let o = pageHead('Система', 'Защита от сканеров и перебора паролей, резервные копии, учётные записи Linux.');
    const G = L.sy.guard;
    if (L.sy.gerr) o += blk('Защита', '', `<p class="mute">${esc(L.sy.gerr)}</p>`);
    else if (G && G.guard.installed) {
      const c = G.conf, gb = G.guard.banned;
      o += blk('Защита', 'Защитник блокирует адреса брандмауэром на всех портах. Белый список не блокируется никогда.',
        (G.guard.active !== 'active' ? `<div class="note" role="status"><span><b>Защитник не запущен.</b> <span class="mute">Состояние службы fail2ban: ${esc(G.guard.active || 'неизвестно')}. Включите её в «Пакетах».</span></span></div>` : '') +
        `<div class="card" style="margin-bottom:16px"><dl class="kv" style="margin-bottom:16px"><div><dt>Блок после неудачных входов</dt><dd>${c.fails} за ${c.find_min} мин → ${banT(c.ban_sec)}</dd></div><div><dt>Блок сканеров</dt><dd>${c.scanners ? 'включён' : 'выключен'}</dd></div><div><dt>Белый список</dt><dd class="mono">${esc(c.allow.join(', ') || '—')}</dd></div><div><dt>Заблокировано сейчас</dt><dd class="num">${gb.length}</dd></div></dl>
        ${own ? `<div class="acts" style="margin:0"><button class="btn" type="button" onclick="LIVE.openGuard()">Настроить</button><button class="btn" type="button" onclick="LIVE.openBlock()">Заблокировать адрес</button></div>` : ''}</div>` +
        table(['Адрес', 'Причина', 'Осталось', { t: '', r: 1 }], gb.map(b => `<tr><td class="mono">${esc(b.ip)}</td><td style="white-space:normal">${esc(banWhy(b))}</td><td>${leftT(b.until)}</td><td class="r">${own ? `<button class="btn sm" type="button" onclick="LIVE.unban('${esc(b.ip)}')">Снять</button>` : ''}</td></tr>`).join('') || `<tr><td colspan="4" class="mute">Сейчас никто не заблокирован.</td></tr>`));
    }
    o += backupBlock(own) + linuxBlock(own);
    return o;
  }

  function backupBlock(own) {
    const B = L.sy.bk;
    if (!B) return blk('Резервные копии', '', `<p class="mute">${esc(L.sy.berr || '')}</p>`);
    const parts = 'нода и члены, панель' + (B.mail && pkgOn('Почтовый сервер') ? ', почта' : '');
    const J = B.job && B.job.state === 'running' ? B.job : null;
    let h = '';
    if (!B.key) h += `<div class="note" role="status"><span><b>Фраза восстановления не задана.</b> <span class="mute">Без неё копии не делаются: ею шифруется каждая копия.</span></span>${own ? `<span class="note-acts"><button class="btn sm" type="button" onclick="LIVE.newPhrase()">Задать фразу</button></span>` : ''}</div>`;
    if (J) h += `<div class="note" role="status"><span><b>${J.kind === 'restore' ? 'Идёт восстановление' : 'Делается копия'}…</b> <span class="mute">${esc({ archive: 'архив и шифрование', store: 'копирование в каталог', open: 'проверка фразы', unpack: 'распаковка и проверка', before: 'копия текущего состояния', swap: 'замена данных', start: 'запуск служб' }[J.now] || '')}</span></span></div>`;
    h += `<div class="card" style="margin-bottom:16px"><dl class="kv" style="margin-bottom:16px"><div><dt>Расписание</dt><dd>${SCHED[B.schedule]}</dd></div><div><dt>Хранить</dt><dd>${B.keep} ${plural(B.keep, 'копию', 'копии', 'копий')}</dd></div><div><dt>Куда</dt><dd>${esc(B.store ? stLabel(B, B.store) : 'только на этом сервере')}</dd></div><div><dt>Что входит</dt><dd>${parts}</dd></div><div><dt>Фраза восстановления</dt><dd>${B.key ? 'задана ' + new Date(B.key.set).toLocaleDateString(LOC()) : pill('не задана', 'warn')}</dd></div></dl>
      ${own ? `<div class="acts" style="margin:0"><button class="btn pri" type="button" ${B.key && !J ? '' : 'disabled'} onclick="LIVE.backupNow()">Сделать сейчас</button><button class="btn" type="button" ${J ? 'disabled' : ''} onclick="LIVE.openRestore('')">Восстановить…</button><button class="btn" type="button" onclick="LIVE.openBackupCfg()">Настроить</button><button class="btn" type="button" onclick="LIVE.openStores()">Хранилища</button></div>` : ''}</div>`;
    const res = r => r.job ? pill('идёт…', 'busy') : r.upload ? pill('переносится в хранилище…', 'busy') : r.ok ? pill(r.kind === 'restore' ? 'восстановлено' : 'успешно', 'ok') : pill('ошибка: ' + ru(r.error || ''), 'warn');
    h += table(['Когда', 'Размер', 'Куда', 'Итог', { t: '', r: 1 }], B.runs.map(r => `<tr><td>${whenT(r.at)}</td><td class="num">${r.size ? bytes(r.size) : '—'}</td><td>${r.kind === 'restore' ? 'восстановление · ' + r.parts.map(p => PNAME[p] || esc(p)).join(', ') : esc(stLabel(B, r.store))}</td><td style="white-space:normal">${res(r)}</td><td class="r">${own && r.kind === 'backup' && r.ok && !J ? `<button class="btn sm" type="button" onclick="LIVE.openRestore('${r.id}')">Восстановить</button>` : ''}</td></tr>`).join('') || `<tr><td colspan="5" class="mute">Копий ещё не было.</td></tr>`);
    if (J && !L.syPoll) { L.syPoll = 1; setTimeout(async () => { L.syPoll = 0; await loadSystem(); syRe(); }, 3000); }
    else if (!J && B.runs.some(r => r.job || r.upload) && !L.syPoll) { L.syPoll = 1; setTimeout(async () => { L.syPoll = 0; await loadSystem(); syRe(); }, 4000); }
    return blk('Резервные копии', 'Нода и члены сети, панель, почта. Копия шифруется ключом фразы восстановления: без фразы её не открыть даже на этом сервере.', h);
  }

  function linuxBlock(own) {
    const U = L.sy.users;
    if (!U) return blk('Учётные записи Linux', '', `<p class="mute">${esc(L.sy.uerr || '')}</p>`);
    return blk('Учётные записи Linux', U.password_auth ? 'Пользователи самого сервера для входа по ssh. Вход по паролю в ssh на сервере включён — безопаснее оставить только ключи (PasswordAuthentication no в настройках sshd).' : 'Пользователи самого сервера для входа по ssh. Вход по паролю в ssh на сервере выключен: только ключи.',
      (own ? `<div class="acts"><button class="btn" type="button" onclick="LIVE.openLinux(-1)">${ic('i-plus')}Добавить</button></div>` : '') +
      table(['Логин', 'Права', 'Вход', 'SSH-ключей'], U.users.map((u, i) => `<tr class="click" onclick="LIVE.openLinux(${i})"><td><b>${esc(u.login)}</b></td><td>${u.root ? 'администратор' : RIGHTS[u.sudo]}</td><td>${entry(u, U)}</td><td class="num">${u.keys.length}</td></tr>`).join('')));
  }

  /* guard */
  L.openGuard = () => {
    const c = L.sy.guard.conf, you = L.sy.guard.you;
    const bans = BANS.some(x => x[0] === c.ban_sec) ? BANS : BANS.concat([[c.ban_sec, banT(c.ban_sec)]]);
    dialog('Настройки защитника', '', `
      <div class="fgrid" style="grid-template-columns:1fr 1fr"><div class="fld"><label for="g1">Неудачных входов</label><input id="g1" class="num" value="${c.fails}"></div><div class="fld"><label for="g2">За, минут</label><input id="g2" class="num" value="${c.find_min}"></div></div>
      <div class="fld"><label for="g3">Блокировать на</label><select id="g3">${bans.map(([v, l]) => `<option value="${v}" ${v === c.ban_sec ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
      <label class="chk"><input id="g5" type="checkbox" ${c.scanners ? 'checked' : ''}><span>Блокировать сканеры<span class="hint" style="display:block;margin:0">Запросы к /wp-login.php, /.env, /phpmyadmin и подобному — сразу, с первого раза.</span></span></label>
      <label class="chk"><input id="g6" type="checkbox" ${c.logins ? 'checked' : ''}>Считать и входы в почту и ssh</label>
      <div><div class="fld"><label for="g4">Белый список</label><textarea id="g4" class="mono ta" rows="3">${esc(c.allow.join('\n'))}</textarea></div><div class="hint" id="g4h"></div></div>`,
      `<button class="btn pri" type="button" onclick="LIVE.guardSave()">Сохранить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
    const hint = () => { const inList = $('#g4').value.split(/\s+/).filter(Boolean).some(a => ipIn(you, a)); $('#g4h').textContent = `По адресу или подсети в строке. Ваш текущий адрес ${you} ${inList ? 'сюда попадает' : 'сюда не попадает'}.`; };
    $('#g4').addEventListener('input', hint); hint();
  };
  // ipIn: is IPv4/IPv6 address a in the address or subnet n (IPv4 subnets only; IPv6 by exact address)
  const ipIn = (a, n) => {
    if (!n.includes('/')) return a === n;
    const [net, bits] = n.split('/'), v4 = s => s.split('.').reduce((x, o) => x * 256 + (+o), 0);
    if (!a.includes('.') || !net.includes('.')) return false;
    const m = bits >= 32 ? 0 : 2 ** (32 - bits);
    return m ? Math.floor(v4(a) / m) === Math.floor(v4(net) / m) : v4(a) === v4(net);
  };
  L.guardSave = async () => {
    const body = { fails: parseInt($('#g1').value, 10) || 0, find_min: parseInt($('#g2').value, 10) || 0, ban_sec: +$('#g3').value, scanners: $('#g5').checked, logins: $('#g6').checked, allow: $('#g4').value.split(/\s+/).filter(Boolean) };
    try { L.sy.guard = await api('POST', '/api/system/guard', body); closeDlg(); toast('Настройки защитника сохранены'); } catch (e) { toast(e.message); return; }
    render();
  };
  L.openBlock = () => dialog('Заблокировать адрес', 'Свой адрес, петлю и адреса из белого списка заблокировать нельзя.', `<div class="fld"><label for="b1">Адрес или подсеть</label><input id="b1" class="mono" placeholder="203.0.113.0/24"></div>
      <div class="fld"><label for="b2">На сколько</label><select id="b2"><option value="86400">24 часа</option><option value="604800">7 дней</option><option value="-1">навсегда</option></select></div>
      <div class="fld"><label for="b3">Причина</label><input id="b3" placeholder="для журнала" maxlength="200"></div>`,
    `<button class="btn pri" type="button" onclick="LIVE.banGo()">Заблокировать</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.banGo = async () => {
    const ip = $('#b1').value.trim();
    try { L.sy.guard = await api('POST', '/api/system/guard/ban', { ip, sec: +$('#b2').value, reason: $('#b3').value.trim() }); closeDlg(); toast(ip + ' заблокирован'); } catch (e) { toast(e.message); return; }
    render();
  };
  L.unban = async ip => {
    try { L.sy.guard = await api('POST', '/api/system/guard/unban', { ip }); toast(ip + ' разблокирован'); } catch (e) { toast(e.message); }
    render();
  };

  /* backups */
  L.backupNow = async () => {
    try { await api('POST', '/api/system/backup/run', {}); toast('Копия делается: займёт пару минут'); } catch (e) { toast(e.message); }
    await loadSystem(); render();
  };
  L.newPhrase = () => dialog('Новая фраза восстановления', L.sy.bk.key ? 'Новая фраза действует для следующих копий. Старые копии открываются только старой фразой — её не выбрасывайте.' : 'Ею шифруется каждая копия. Панель её не хранит: без неё копии не открыть даже на этом сервере.', '',
    `<button class="btn pri" type="button" onclick="LIVE.phraseGo()">Создать фразу</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.phraseGo = async () => {
    let r;
    try { r = await api('POST', '/api/system/backup/phrase', {}); } catch (e) { toast(e.message); return; }
    L.reSys();
    dialog('Фраза восстановления', 'Запишите её не на этом сервере. Панель её не хранит и больше не покажет: без неё копии не открыть.', `<div class="uri" id="bkph" style="font-size:18px;letter-spacing:.04em">${esc(r.phrase)}</div><div class="acts" style="margin:0"><button class="btn sm" type="button" onclick="navigator.clipboard.writeText($('#bkph').textContent).then(()=>toast('Фраза скопирована'))">Скопировать</button></div>`,
      `<button class="btn pri" type="button" onclick="closeDlg()">Записал</button>`);
  };
  L.reSys = async () => { await loadSystem(); render(); };
  L.openBackupCfg = () => {
    const B = L.sy.bk, mailOn = pkgOn('Почтовый сервер');
    dialog('Резервные копии', '', `
      <div class="fld"><label for="k1">Расписание</label><select id="k1">${Object.entries(SCHED).map(([k, v]) => `<option value="${k}" ${k === B.schedule ? 'selected' : ''}>${v}</option>`).join('')}</select></div>
      <div class="fld"><label for="k2">Хранить копий</label><input id="k2" class="num" value="${B.keep}"></div>
      <div><div class="fld"><label for="k3">Куда</label><select id="k3">${B.stores.map(s => `<option value="${s.id}" ${s.id === B.store ? 'selected' : ''}>${esc(stLabel(B, s.id))}</option>`).join('')}<option value="" ${B.store ? '' : 'selected'}>только на этом сервере</option></select></div><div class="hint">На сервере всегда лежат две последние копии — для быстрого восстановления.</div></div>
      <div class="dlg-sec">Что входит</div>
      <label class="chk"><input type="checkbox" checked disabled>${PARTS.node}</label>
      <label class="chk"><input type="checkbox" checked disabled>${PARTS.panel}</label>
      ${mailOn ? `<label class="chk"><input id="k4" type="checkbox" ${B.mail ? 'checked' : ''}>${PARTS.mail}</label>` : ''}
      <div class="dlg-sec">Фраза восстановления</div>
      <p class="hint" style="margin:0">${B.key ? `Задана ${new Date(B.key.set).toLocaleDateString(LOC())}. Если её потерять, старые копии не открыть. Новая фраза действует для следующих копий.` : 'Не задана — копии не делаются.'}</p>
      <button class="btn sm" type="button" style="align-self:flex-start" onclick="LIVE.newPhrase()">${B.key ? 'Задать новую фразу…' : 'Задать фразу…'}</button>`,
      `<button class="btn pri" type="button" onclick="LIVE.backupCfgSave()">Сохранить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  };
  L.backupCfgSave = async () => {
    const body = { schedule: $('#k1').value, keep: parseInt($('#k2').value, 10) || 0, store: $('#k3').value, mail: $('#k4') ? $('#k4').checked : L.sy.bk.mail };
    try { await api('POST', '/api/system/backup/settings', body); closeDlg(); toast('Настройки копий сохранены'); } catch (e) { toast(e.message); return; }
    await loadSystem(); render();
  };
  L.openStores = () => {
    const B = L.sy.bk;
    dialog('Хранилища копий', '', `<div class="pp-list">${B.stores.map(s => `<div class="pp" style="cursor:default"><span class="pp-t"><b>${esc(s.label.split(' · ')[0])} <span class="mute" style="font-weight:400">· ${SKIND[s.kind]}</span></b><span class="mono hint" style="margin:2px 0 0;overflow-wrap:anywhere">${esc(s.kind === 'dir' ? s.dir : s.label)}</span></span><button class="icb del" type="button" aria-label="Удалить хранилище" onclick="LIVE.storeDel('${s.id}')">${ic('i-trash')}</button></div>`).join('') || '<p class="mute" style="margin:0">Хранилищ нет: копии лежат только на этом сервере.</p>'}</div>` + `
      <div class="dlg-sec">Добавить</div>
      <div class="fld"><label for="st0">Тип</label><select id="st0" onchange="['s3','sftp','dir'].forEach(k=>$('#st_'+k).hidden=k!==this.value)"><option value="s3">S3-совместимое</option><option value="sftp">SFTP</option><option value="dir">Каталог на сервере</option></select></div>
      <div id="st_s3" style="display:flex;flex-direction:column;gap:18px"><div class="fld"><label for="sa1">Адрес (endpoint)</label><input id="sa1" class="mono" placeholder="https://s3.example-cloud.com"></div><div class="fld"><label for="sa2">Бакет и путь</label><input id="sa2" class="mono" placeholder="backup-bucket/loft"></div><div class="fld"><label for="sa3">Ключ доступа</label><input id="sa3" class="mono" autocomplete="off"></div><div class="fld"><label for="sa4">Секретный ключ</label><input id="sa4" class="mono" type="password" autocomplete="off"></div></div>
      <div id="st_sftp" hidden style="display:flex;flex-direction:column;gap:18px"><div class="fld"><label for="sb1">Сервер и порт</label><input id="sb1" class="mono" placeholder="backup.example.org:22"></div><div class="fld"><label for="sb2">Пользователь</label><input id="sb2" autocomplete="off"></div><div class="fld"><label for="sb3">Каталог</label><input id="sb3" class="mono" placeholder="/srv/backups/loft"></div><div><div class="hint">Вход по ключу сервера — добавьте его в authorized_keys на той стороне. Ключ хоста запоминается при первой проверке.</div><div class="uri">${esc(B.ssh_key)}</div></div></div>
      <div id="st_dir" hidden><div class="fld"><label for="sc1">Каталог</label><input id="sc1" class="mono" value="/var/backups/loft"></div><div class="hint">Копия на том же диске не спасёт при потере сервера.</div></div>
      <button class="btn sm" type="button" style="align-self:flex-start" onclick="LIVE.storeGo(true)">Проверить доступ</button>`,
      `<button class="btn pri" type="button" onclick="LIVE.storeGo(false)">Добавить</button><button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`);
  };
  L.storeGo = once(async test => {
    const k = $('#st0').value, body = { kind: k, test };
    if (k === 's3') Object.assign(body, { endpoint: $('#sa1').value, bucket: $('#sa2').value, access_key: $('#sa3').value, secret_key: $('#sa4').value });
    if (k === 'sftp') Object.assign(body, { host: $('#sb1').value, user: $('#sb2').value, dir: $('#sb3').value });
    if (k === 'dir') body.dir = $('#sc1').value;
    toast('Проверяю доступ…');
    try { await api('POST', '/api/system/backup/store', body); } catch (e) { toast(e.message); return; }
    if (test) { toast('Доступ есть: проба записана и удалена'); return; }
    toast('Хранилище добавлено'); await loadSystem(); if (L.me) L.openStores(); render();
  });
  L.storeDel = async id => {
    try { await api('POST', '/api/system/backup/store', { id, kind: 'dir', delete: true }); toast('Хранилище удалено'); } catch (e) { toast(e.message); }
    await loadSystem(); if (L.me) L.openStores(); render();
  };
  L.openRestore = runId => {
    const B = L.sy.bk;
    const runs = B.runs.filter(r => r.kind === 'backup' && r.ok && r.name);
    const pre = B.local.filter(f => f.name.endsWith('-before-restore.lpbak'));
    const opt = runs.map(r => `<option value="run:${r.id}" ${r.id === runId ? 'selected' : ''}>${whenT(r.at)} · ${bytes(r.size)} · ${esc(stLabel(B, r.store))}</option>`).join('') +
      pre.map(f => `<option value="local:${esc(f.name)}">${whenT(f.at)} · ${bytes(f.size)} · перед восстановлением</option>`).join('') + '<option value="file">Из файла…</option>';
    dialog('Восстановить из копии', 'Текущие данные выбранных частей будут заменены. Перед восстановлением панель сама сделает копию текущего состояния.', `
      <div class="fld"><label for="rs1">Копия</label><select id="rs1" onchange="LIVE.rsPick()">${opt}</select></div>
      <div id="rsf" ${runs.length || pre.length ? 'hidden' : ''}><div class="fld"><label for="rs3">Файл копии</label><input id="rs3" type="file" accept=".lpbak"></div></div>
      <div class="dlg-sec">Что восстановить</div>
      ${Object.entries(PARTS).map(([k, t]) => `<label class="chk"><input type="checkbox" data-rp="${k}" ${k !== 'mail' ? 'checked' : ''}>${t}</label>`).join('')}
      <div class="fld"><label for="rs2">Фраза восстановления</label><input id="rs2" type="password" autocomplete="off"></div>`,
      `<button class="btn danger" type="button" onclick="LIVE.restoreGo()">Восстановить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
    L.rsPick();
  };
  // Only the parts the chosen copy holds can be restored.
  L.rsPick = () => {
    const v = $('#rs1').value, run = L.sy.bk.runs.find(r => 'run:' + r.id === v);
    $('#rsf').hidden = v !== 'file';
    document.querySelectorAll('[data-rp]').forEach(i => {
      const has = !run || run.parts.includes(i.dataset.rp);
      i.closest('label').hidden = !has;
      if (!has) i.checked = false;
    });
  };
  // waitJob follows the helper's job through a panel restart (a restore
  // of the panel restarts it): failed polls are retried for 15 minutes.
  async function waitJob(id) {
    const until = Date.now() + 900e3;
    while (Date.now() < until) {
      await sleep(2000);
      let j;
      try { const r = await fetch('/api/system/backup/job', { credentials: 'same-origin' }); if (r.status === 401) return { relogin: true }; if (!r.ok) continue; j = await r.json(); } catch (e) { continue; }
      if (j.id === id && j.state !== 'running') return j;
    }
    return { state: 'error', error: 'Сервер долго не отвечает — обновите страницу' };
  }
  L.restoreGo = async () => {
    const B = L.sy.bk, src = $('#rs1').value, phrase = $('#rs2').value, parts = [...document.querySelectorAll('[data-rp]')].filter(i => i.checked).map(i => i.dataset.rp);
    if (!parts.length) { toast('Выберите, что восстановить'); return; }
    if (!phrase.trim()) { toast('Введите фразу восстановления'); return; }
    let file = '';
    try {
      if (src === 'file') {
        const f = $('#rs3').files[0];
        if (!f) { toast('Выберите файл копии'); return; }
        toast('Загружаю копию…');
        const r = await fetch('/api/system/backup/upload', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': L.csrf, 'Content-Type': 'application/octet-stream' }, body: f });
        const j = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(ru(j.error || 'HTTP ' + r.status));
        file = j.file;
      } else if (src.startsWith('local:')) file = src.slice(6);
      else {
        const run = B.runs.find(r => 'run:' + r.id === src);
        if (B.local.some(f => f.name === run.name)) file = run.name;
        else {
          toast('Забираю копию из хранилища…');
          const r = await api('POST', '/api/system/backup/fetch', { store: run.store, name: run.name });
          file = r.file || (await waitPanelJob(r.job)).file;
        }
      }
      closeDlg();
      toast('Восстановление запущено');
      const j = await api('POST', '/api/system/backup/restore', { file, parts, phrase });
      await loadSystem(); render();
      const end = await waitJob(j.id);
      if (end.relogin) { toast('Восстановлено — войдите снова'); await sleep(1500); location.reload(); return; }
      if (end.state === 'done') { toast('Восстановлено'); if (parts.includes('panel')) { await sleep(1200); location.reload(); return; } }
      else toast(ru(end.error || 'Восстановление не удалось'));
    } catch (e) { toast(e.message); }
    await loadSystem(); render();
  };
  async function waitPanelJob(id) {
    for (;;) {
      await sleep(1500);
      const j = await api('GET', '/api/jobs/' + id);
      if (j.state === 'done') return j.result || {};
      if (j.state === 'error') throw new Error(ru(j.error));
    }
  }

  /* Linux accounts */
  L.openLinux = i => {
    const U = L.sy.users, nw = i < 0, u = nw ? { login: '', sudo: '', keys: [], locked: false } : U.users[i], root = !!u.root, own = L.can('system');
    dialog(nw ? 'Новая учётная запись' : 'Учётная запись ' + esc(u.login), root ? 'Учётная запись root: удалить, заблокировать и сменить ей права или пароль здесь нельзя — только ключи.' : '', `
      <div class="fld"><label for="lx1">Логин</label><input id="lx1" class="mono" value="${esc(u.login)}" ${nw ? '' : 'readonly'} placeholder="deploy" maxlength="31"></div>
      ${root ? '' : `<div class="fld"><label for="lx2">Права</label><select id="lx2">${Object.entries(RIGHTS).map(([k, v]) => `<option value="${k}" ${k === u.sudo ? 'selected' : ''}>${v}</option>`).join('')}</select></div>
      <div><div class="fld"><label for="lx4">Пароль</label><input id="lx4" type="password" autocomplete="new-password" placeholder="${nw ? 'необязательно' : u.password ? 'не менять' : 'не задан'}" data-pw-min="9" data-pw-optional></div><button class="btn sm" type="button" style="margin-top:10px" onclick="LIVE.genPw('lx4')">Сгенерировать</button><div class="hint">${U.password_auth ? 'Нужен для sudo с паролем. Вход по паролю в ssh на сервере включён — с ним можно войти и по ssh.' : 'Нужен для sudo с паролем. В ssh пароль не действует — вход только по ключу.'}</div></div>`}
      <div><div class="fld"><label for="lx3">SSH-ключи</label><textarea id="lx3" class="mono ta" rows="4" placeholder="ssh-ed25519 AAAA… по ключу в строке">${esc(u.keys.join('\n'))}</textarea></div><div class="hint">Панель не даст оставить сервер без учётки, которая входит по ключу и может стать root.</div></div>
      ${root || nw ? '' : `<label class="chk"><input id="lx5" type="checkbox" ${u.locked ? 'checked' : ''}>Временно заблокировать вход</label>`}`,
      own ? `<button class="btn pri" type="button" onclick="LIVE.linuxSave(${i})">${nw ? 'Создать' : 'Сохранить'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>${nw || root ? '' : `<button class="icb del" type="button" aria-label="Удалить учётку" style="margin-left:auto" onclick="LIVE.linuxDel(${i})">${ic('i-trash')}</button>`}` : `<button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`);
  };
  L.linuxSave = async i => {
    if (!pwCheck($('#dlgRoot'), true)) { toast('Проверьте пароль'); return; }
    const nw = i < 0, u = nw ? {} : L.sy.users.users[i];
    const password = $('#lx4') ? $('#lx4').value : '';
    const body = { login: nw ? $('#lx1').value.trim() : u.login, new: nw, sudo: $('#lx2') ? $('#lx2').value : '', keys: $('#lx3').value.split('\n').map(x => x.trim()).filter(Boolean), password, lock: $('#lx5') ? $('#lx5').checked : false };
    try { L.sy.users = await api('POST', '/api/system/user', body); } catch (e) { toast(e.message); return; }
    closeDlg(); render();
    if (password) dialog('Пароль ' + esc(body.login), 'Показан один раз — передайте его владельцу учётной записи.', `<div class="uri" id="lxpw">${esc(password)}</div>`, `<button class="btn pri" type="button" onclick="closeDlg()">Готово</button><button class="btn" type="button" onclick="navigator.clipboard.writeText($('#lxpw').textContent).then(()=>toast('Пароль скопирован'))">Скопировать пароль</button>`);
    else toast(nw ? 'Учётная запись создана' : 'Учётная запись сохранена');
  };
  L.linuxDel = i => { const l = L.sy.users.users[i].login; dialog(`Удалить учётную запись ${esc(l)}?`, 'Войти на сервер под ней больше не получится.', '<label class="chk"><input id="lxd" type="checkbox">Удалить и домашний каталог</label>',
    `<button class="btn danger" type="button" onclick="LIVE.linuxDelGo('${esc(l)}')">Удалить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`); };
  L.linuxDelGo = async login => {
    try { L.sy.users = await api('POST', '/api/system/user/delete', { login, home: $('#lxd').checked }); closeDlg(); toast('Учётная запись удалена'); } catch (e) { toast(e.message); return; }
    render();
  };

  /* ── Domains: zones, records, certificates, DNS providers ── */
  L.dm = null; L.dz = null; L.dzRecs = null;
  const DNSK = { cloudflare: ['Cloudflare', 'API-токен с правом Zone.DNS — Edit'], digitalocean: ['DigitalOcean', 'API-токен с правом записи'], namecheap: ['Namecheap', 'API-ключ; IP этого сервера должен быть в белом списке API'], manual: ['Вручную', 'записи вносите у регистратора сами'] };
  const TTLS = [[0, 'авто'], [300, '5 минут'], [3600, '1 час'], [86400, '1 день']];
  const USE = { node: 'нода loft', panel: 'панель', mail: 'почта (SMTP, IMAP)' };
  async function loadDomains() {
    try { L.dm = await api('GET', '/api/domains'); } catch (e) { toast('Домены не прочитаны: ' + e.message); L.dm = L.dm || { providers: [], zones: [], certs: [] }; }
    if (!L.dz || !L.dm.zones.some(z => z.name === L.dz)) L.dz = (L.dm.zones[0] || {}).name || null;
    L.dzRecs = null;
  }
  async function loadRecords(check) {
    if (!L.dz) { L.dzRecs = []; return; }
    try { L.dzRecs = (await api('GET', '/api/domains/records?zone=' + encodeURIComponent(L.dz) + (check ? '&check=1' : ''))).records; } catch (e) { toast(e.message); L.dzRecs = []; }
  }
  const provName = id => { if (id === 'manual') return 'Вручную'; const p = (L.dm.providers || []).find(x => x.id === id); return p ? DNSK[p.kind][0] : '—'; };
  const dayLeft = t => Math.round((new Date(t) - Date.now()) / 86400e3);
  const untilCell = t => { if (!t) return '—'; const d = dayLeft(t); return d < 30 ? pill(d < 0 ? 'истёк' : 'через ' + d + ' дн.', 'warn') : new Date(t).toLocaleDateString(LOC()); };
  const certIn = (c, z) => c.names.some(n => { n = n.replace(/^\*\./, ''); return n === z || n.endsWith('.' + z); });
  const certRows = l => l.map(c => `<tr class="click" onclick="LIVE.openCert('${c.id}')"><td class="mono" style="white-space:normal">${esc(c.names.join(', '))}</td><td>${esc(c.issuer || '—')}</td><td>${c.method === 'upload' ? 'загружен' : c.method.toUpperCase()}</td><td>${untilCell(c.until)}</td><td>${USE[c.use] || esc(c.use)}</td></tr>`).join('');
  const CERT_H = ['Имена', 'Издатель', 'Проверка', 'Действует до', 'Где используется'];
  // #domains lists zones and certificates; #domains/<zone> is the zone's own page.
  function domainsView() {
    if (!L.can('domains', 1)) return pageHead('Домены', '') + `<div class="empty"><div class="empty-ic">${ic('i-gear', 28)}</div><b>Нет доступа</b><p>Вашей роли этот раздел не виден.</p></div>`;
    if (!L.dm) { loadDomains().then(() => { if (S.route === 'domains') render(); }); return pageHead('Домены', '') + '<p class="mute">Загружаю…</p>'; }
    const own = L.can('domains');
    if (S.arg) return zoneView(own);
    return pageHead('Домены', 'Зоны, DNS-записи и сертификаты. Откройте зону, чтобы увидеть и править её записи.') +
      blk('Зоны', '', (own ? `<div class="acts"><button class="btn pri" type="button" onclick="LIVE.openZone('')">${ic('i-plus')}Добавить домен</button><button class="btn" type="button" onclick="LIVE.openProviders()">DNS-провайдеры</button></div>` : '') +
        table(['Зона', 'DNS', 'Записей', 'Регистрация до', { t: '', r: 1 }], L.dm.zones.map(z => `<tr class="click" onclick="go('domains/${esc(z.name)}')"><td class="mono"><b>${esc(z.name)}</b></td><td>${esc(provName(z.provider))}${z.provider === 'manual' ? '' : ' <span class="mute">· API</span>'}</td><td class="num">${z.records >= 0 ? z.records : '—'}</td><td class="num">${untilCell(z.reg_until)}</td><td class="r mute">${ic('i-next', 18)}</td></tr>`).join('') || `<tr><td colspan="5" class="mute">Зон нет. Добавьте домен, чтобы панель вела его записи.</td></tr>`)) +
      blk('Сертификаты', 'Выпуск и продление через Let’s Encrypt. Продление — за 30 дней до конца срока, само.', (own ? `<div class="acts"><button class="btn" type="button" onclick="LIVE.openCert('')">${ic('i-plus')}Выпустить сертификат</button><button class="btn" type="button" onclick="LIVE.openPem()">Загрузить PEM</button></div>` : '') +
        table(CERT_H, certRows(L.dm.certs) || `<tr><td colspan="5" class="mute">Сертификатов нет. Сертификат ноды выпускается в мастере установки.</td></tr>`));
  }
  function zoneView(own) {
    const Z = L.dm.zones.find(z => z.name === S.arg), back = `<a class="crumb" href="#domains">${ic('i-back', 18)}Домены</a>`;
    if (!Z) return back + pageHead(esc(S.arg), '') + `<div class="empty"><b>Такой зоны нет</b><p>Её удалили или адрес набран с ошибкой.</p></div>`;
    if (L.dz !== Z.name) { L.dz = Z.name; L.dzRecs = null; }
    if (!L.dzRecs) loadRecords().then(() => { if (S.route === 'domains' && S.arg === Z.name) render(); });
    const manual = Z.provider === 'manual', recs = L.dzRecs || [], types = [...new Set(recs.map(r => r.type))].sort();
    const zc = L.dm.certs.filter(c => certIn(c, Z.name));
    return back + pageHead(esc(Z.name), (manual ? 'DNS ведётся у регистратора вручную: панель покажет, какие записи внести, и проверит их.' : `DNS через ${esc(provName(Z.provider))}: панель сама создаёт и меняет записи.`) + (Z.reg_until ? ` Регистрация до ${new Date(Z.reg_until).toLocaleDateString(LOC())}.` : '')) +
      blk('Записи', '<span class="dot on"></span>создано панелью — обновится при смене IP',
        `<div class="acts">${own ? `<button class="btn pri" type="button" onclick="LIVE.openRec(-1)">${ic('i-plus')}Добавить запись</button>` : ''}${manual ? `<button class="btn" type="button" onclick="LIVE.checkRecs()">Проверить записи</button>` : ''}${own ? `<button class="btn" type="button" onclick="LIVE.openZone('${esc(Z.name)}')">Настройки зоны</button>` : ''}</div>` +
        (L.dzRecs ? (recs.length > 8 ? `<div class="netbar"><div class="fld" style="margin:0;flex:1;min-width:200px"><label for="rq">Поиск</label><input id="rq" placeholder="имя, тип или значение" oninput="LIVE.recFilter()"></div><div class="fld" style="margin:0;min-width:140px"><label for="rt">Тип</label><select id="rt" onchange="LIVE.recFilter()"><option value="">все</option>${types.map(t => `<option>${esc(t)}</option>`).join('')}</select></div></div>` : '') +
          table(['Имя', 'Тип', 'Значение', 'TTL'].concat(manual ? ['Проверка'] : []), recs.map((r, i) => `<tr class="click" data-t="${esc(r.type)}" data-q="${esc((r.name + ' ' + r.type + ' ' + r.value).toLowerCase())}" onclick="LIVE.openRec(${i})"><td class="mono">${r.ours ? '<span class="dot on"></span>' : '<span class="dot" style="visibility:hidden"></span>'}${esc(r.name)}</td><td class="mono">${esc(r.type)}</td><td class="mono" style="white-space:normal;overflow-wrap:anywhere;min-width:220px">${r.priority && ['MX', 'SRV'].includes(r.type) ? r.priority + ' ' : ''}${esc(r.value)}</td><td class="mute">${r.auto ? 'авто' : esc((TTLS.find(x => x[0] === (r.ttl || 0)) || [0, (r.ttl || 0) + ' с'])[1])}</td>${manual ? `<td>${r.check === 'ok' ? pill('есть', 'ok') : r.check === 'missing' ? pill('нет', 'warn') : r.check === 'differs' ? pill('другое значение', 'warn') : '<span class="mute">—</span>'}</td>` : ''}</tr>`).join('') || `<tr><td colspan="5" class="mute">Записей нет.</td></tr>`) : '<p class="mute">Загружаю записи…</p>')) +
      blk('Сертификаты зоны', '', (own ? `<div class="acts"><button class="btn" type="button" onclick="LIVE.openCert('', ['${esc(Z.name)}'])">${ic('i-plus')}Выпустить сертификат</button></div>` : '') +
        table(CERT_H, certRows(zc) || `<tr><td colspan="5" class="mute">Для имён этой зоны сертификатов нет.</td></tr>`));
  }
  L.recFilter = () => {
    const q = $('#rq').value.trim().toLowerCase(), t = $('#rt').value;
    document.querySelectorAll('tr[data-q]').forEach(tr => { tr.hidden = (q && !tr.dataset.q.includes(q)) || (t && tr.dataset.t !== t); });
  };
  L.checkRecs = async () => { toast('Проверяю записи в DNS…'); await loadRecords(true); render(); };
  L.openZone = name => {
    const nw = !name, Z = nw ? { name: '', provider: (L.dm.providers[0] || {}).id || 'manual' } : L.dm.zones.find(z => z.name === name);
    const opts2 = (L.dm.providers || []).map(p => `<option value="${p.id}" ${Z.provider === p.id ? 'selected' : ''}>${DNSK[p.kind][0]} · ${esc(p.hint)}</option>`).join('') + `<option value="manual" ${Z.provider === 'manual' ? 'selected' : ''}>Вручную</option>`;
    dialog(nw ? 'Новый домен' : 'Зона ' + esc(name), nw ? 'Панель проверит, что провайдер обслуживает зону, и создаст записи для сервера.' : '', `
      <div class="fld"><label for="zz1">Зона</label><input id="zz1" class="mono" value="${esc(Z.name)}" placeholder="example.com" ${nw ? '' : 'readonly'} list="zzl"><datalist id="zzl"></datalist></div>
      <div><div class="fld"><label for="zz2">Где управляется DNS</label><select id="zz2" onchange="LIVE.zoneProv(this.value)">${opts2}</select></div><div class="hint">Через API панель сама создаёт и обновляет записи. «Вручную» — покажет, что внести у регистратора. Доступы — в «DNS-провайдерах».</div></div>
      ${nw ? `<div class="dlg-sec">Сразу создать</div><label class="chk"><input id="zz3" type="checkbox" checked>A и AAAA для имён сервера в этой зоне → адреса сервера</label><label class="chk"><input id="zz4" type="checkbox" checked>CAA для Let’s Encrypt</label><p class="hint">Записи почты (MX, SPF, DKIM, DMARC) создаёт раздел «Почта».</p>` : `<dl class="kv"><div><dt>Регистрация до</dt><dd>${untilCell(Z.reg_until)}</dd></div></dl>`}`,
      `<button class="btn pri" type="button" onclick="LIVE.zoneSave(${nw})">${nw ? 'Добавить' : 'Сохранить'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>${nw ? '' : `<button class="icb del" type="button" aria-label="Удалить зону" style="margin-left:auto" onclick="LIVE.zoneDel('${esc(name)}')">${ic('i-trash')}</button>`}`);
    if (nw) L.zoneProv($('#zz2').value);
  };
  L.zoneProv = async id => {
    const dl = $('#zzl');
    if (!dl || id === 'manual') { if (dl) dl.innerHTML = ''; return; }
    try { const z = await api('GET', '/api/domains/provider-zones?id=' + encodeURIComponent(id)); dl.innerHTML = z.zones.map(x => `<option value="${esc(x)}">`).join(''); } catch (e) { /* typed by hand */ }
  };
  L.zoneSave = once(async nw => {
    const create = nw ? [$('#zz3').checked ? 'addr' : '', $('#zz4').checked ? 'caa' : ''].filter(Boolean) : [];
    const name = $('#zz1').value.trim();
    toast('Сохраняю зону…');
    try { await api('POST', '/api/domains/zone', { name, provider: $('#zz2').value, create }); closeDlg(); toast('Зона сохранена'); } catch (e) { toast(e.message); }
    await loadDomains(); L.dzRecs = null; if (nw) go('domains/' + name.toLowerCase()); else render();
  });
  L.zoneDel = name => dialog(`Удалить зону «${esc(name)}» из панели?`, 'Записи у провайдера останутся; панель просто перестанет вести эту зону.', '', `<button class="btn danger" type="button" onclick="LIVE.zoneDelGo('${esc(name)}')">Удалить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.zoneDelGo = async name => { closeDlg(); try { await api('POST', '/api/domains/zone', { name, delete: true }); toast('Зона удалена из панели'); } catch (e) { toast(e.message); } L.dz = null; await loadDomains(); go('domains'); };
  L.openRec = i => {
    const nw = i < 0, r = nw ? { type: 'A', name: '', value: '', priority: 10, ttl: 0 } : L.dzRecs[i], own = L.can('domains');
    dialog(nw ? 'Новая запись' : 'Запись ' + esc(r.name) + ' ' + esc(r.type), esc(L.dz) + (r.ours ? ' · создана панелью' : ''), `
      <div class="fld"><label for="rr1">Тип</label><select id="rr1" ${nw ? '' : 'disabled'} onchange="$('#rrP').hidden=!['MX','SRV'].includes(this.value);$('#rrA').hidden=!['A','AAAA'].includes(this.value)">${['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'CAA', 'SRV', 'NS'].map(t => `<option ${t === r.type ? 'selected' : ''}>${t}</option>`).join('')}</select></div>
      <div><div class="fld"><label for="rr2">Имя</label><input id="rr2" class="mono" value="${esc(r.name)}" placeholder="@" ${nw ? '' : 'readonly'}></div><div class="hint">@ — сама зона ${esc(L.dz)}.</div></div>
      <div id="rrP" ${['MX', 'SRV'].includes(r.type) ? '' : 'hidden'}><div class="fld"><label for="rr3">Приоритет</label><input id="rr3" class="num" value="${r.priority || 10}"></div></div>
      <div class="fld"><label for="rr4">Значение</label><input id="rr4" class="mono" value="${esc(r.value)}" placeholder="${{ A: '203.0.113.10', AAAA: '2001:db8::10', MX: 'mail.example.net', CAA: '0 issue &quot;letsencrypt.org&quot;', SRV: '5 443 vpn.example.net' }[r.type] || ''}"></div>
      <div class="fld"><label for="rr5">TTL</label><select id="rr5">${TTLS.map(([v, l]) => `<option value="${v}" ${(r.ttl || 0) === v ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
      <label class="chk" id="rrA" ${['A', 'AAAA'].includes(r.type) ? '' : 'hidden'}><input id="rr6" type="checkbox" ${r.auto ? 'checked' : ''}>Обновлять при смене IP сервера</label>`,
      own ? `<button class="btn pri" type="button" onclick="LIVE.recSave(${i})">${nw ? 'Добавить' : 'Сохранить'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>${nw ? '' : `<button class="icb del" type="button" aria-label="Удалить запись" style="margin-left:auto" onclick="LIVE.recDel(${i})">${ic('i-trash')}</button>`}` : `<button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`);
  };
  L.recSave = once(async i => {
    const old = i >= 0 ? L.dzRecs[i] : {};
    const rec = { id: old.id || '', type: $('#rr1').value, name: $('#rr2').value.trim() || '@', value: $('#rr4').value.trim(), priority: parseInt($('#rr3').value, 10) || 0, ttl: +$('#rr5').value };
    try { await api('POST', '/api/domains/record', { zone: L.dz, record: rec, auto: $('#rr6').checked }); closeDlg(); toast('Запись сохранена'); } catch (e) { toast(e.message); }
    await loadRecords(); render();
  });
  L.recDel = async i => { closeDlg(); try { await api('POST', '/api/domains/record', { zone: L.dz, record: { id: L.dzRecs[i].id }, delete: true }); toast('Запись удалена'); } catch (e) { toast(e.message); } await loadRecords(); render(); };
  L.openCert = (id, pre) => {
    const nw = !id, c = nw ? { names: pre || [], method: 'http-01', use: 'node' } : L.dm.certs.find(x => x.id === id), own = L.can('domains');
    const mailOn = pkgOn('Почтовый сервер');
    dialog(nw ? 'Новый сертификат' : 'Сертификат ' + esc(c.names[0]), nw ? 'Let’s Encrypt. Проверка DNS-01 нужна для имён, которые не смотрят на этот сервер, и для *.' : '', `
      <div><div class="fld"><label for="ce1">Имена</label><input id="ce1" class="mono" value="${esc(c.names.join(', '))}" placeholder="vpn.example.net, mail.example.net"></div><div class="hint">Через запятую. *.example.net — только с проверкой DNS-01.${nw ? '' : ' Сертификат ноды должен включать её имя, панели — имя панели.'}</div></div>
      <div class="fld"><label for="ce2">Проверка</label><select id="ce2"><option value="dns-01" ${c.method === 'dns-01' ? 'selected' : ''}>DNS-01 — через DNS-провайдера зоны</option><option value="http-01" ${c.method !== 'dns-01' ? 'selected' : ''}>HTTP-01 — через веб-сервер на :80</option></select></div>
      <div class="fld"><label for="ce3">Где использовать</label><select id="ce3" ${nw ? '' : 'disabled'}>${Object.entries(USE).filter(([k]) => k !== 'mail' || mailOn || c.use === 'mail').map(([k, l]) => `<option value="${k}" ${c.use === k ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
      ${nw ? '' : `<dl class="kv"><div><dt>Издатель</dt><dd>${esc(c.issuer || '—')}</dd></div><div><dt>Действует до</dt><dd>${c.until ? new Date(c.until).toLocaleDateString(LOC()) : '—'}</dd></div><div><dt>Ключ</dt><dd>${esc(c.key || '—')}</dd></div></dl>`}`,
      own ? `<button class="btn pri" type="button" onclick="LIVE.certIssue('${nw ? '' : c.id}')">${nw ? 'Выпустить' : 'Выпустить заново'}</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>${nw || c.fixed ? '' : `<button class="icb del" type="button" aria-label="Удалить сертификат" style="margin-left:auto" onclick="LIVE.certDel('${c.id}')">${ic('i-trash')}</button>`}` : `<button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`);
  };
  L.certIssue = once(async id => {
    const names = $('#ce1').value.split(/[\s,]+/).filter(Boolean), method = $('#ce2').value, use = $('#ce3').value;
    closeDlg(); toast('Выпускаю сертификат: это займёт до пары минут…');
    try { await job('/api/domains/cert', { names, method, use, id: id === 'node' || id === 'panel' ? '' : id }); toast('Сертификат выпущен'); } catch (e) { toast(e.message); }
    await loadDomains(); render();
  });
  L.certDel = async id => { closeDlg(); try { await api('POST', '/api/domains/cert/delete', { id }); toast('Сертификат удалён'); } catch (e) { toast(e.message); } await loadDomains(); render(); };
  L.openPem = () => dialog('Загрузить сертификат', 'Свой сертификат от другого центра. Продлевать его вы будете сами.', `
      <div class="fld"><label for="pm1">Сертификат и цепочка (PEM)</label><textarea id="pm1" class="mono ta" rows="5" placeholder="-----BEGIN CERTIFICATE-----"></textarea></div>
      <div class="fld"><label for="pm2">Закрытый ключ (PEM)</label><textarea id="pm2" class="mono ta" rows="4" placeholder="-----BEGIN PRIVATE KEY-----"></textarea></div>
      <div class="acts" style="margin:0"><label class="btn sm" style="cursor:pointer">${ic('i-up', 16)}Выбрать файлы<input type="file" multiple accept=".pem,.crt,.key,.cer,text/plain" hidden onchange="LIVE.pemFiles(this)"></label></div>
      <div class="fld"><label for="pm3">Где использовать</label><select id="pm3">${Object.entries(USE).filter(([k]) => k !== 'mail' || pkgOn('Почтовый сервер')).map(([k, l]) => `<option value="${k}">${l}</option>`).join('')}</select></div>`,
    `<button class="btn pri" type="button" onclick="LIVE.pemGo()">Загрузить</button><button class="btn" type="button" onclick="closeDlg()">Отмена</button>`);
  L.pemFiles = inp => { const fs = [...inp.files]; inp.value = ''; for (const f of fs) f.text().then(t => { if (/PRIVATE KEY/.test(t)) $('#pm2').value = t; else $('#pm1').value = t; }); };
  L.pemGo = once(async () => {
    const body = { fullchain: $('#pm1').value, key: $('#pm2').value, use: $('#pm3').value };
    closeDlg(); toast('Устанавливаю сертификат…');
    try { await job('/api/domains/cert/upload', body); toast('Сертификат установлен'); } catch (e) { toast(e.message); }
    await loadDomains(); render();
  });
  L.openProviders = () => {
    const ips = (L.dm.server_ips || []).filter(x => !x.includes(':')).join(', ');
    dialog('DNS-провайдеры', 'Доступы, через которые панель ведёт записи зон и проходит проверку DNS-01. Хранятся только на этом сервере.',
      (L.dm.providers.length ? table(['Провайдер', 'Доступ', 'Зоны', { t: '', r: 1 }], L.dm.providers.map(p => `<tr><td><b>${DNSK[p.kind][0]}</b></td><td class="mono">${esc(p.hint)}${p.user ? ' · ' + esc(p.user) : ''}</td><td class="mono">${esc(p.zones.join(', ') || '—')}</td><td class="r"><div class="cell-acts"><button class="icb del" type="button" aria-label="Удалить доступ" onclick="LIVE.provDel('${p.id}')">${ic('i-trash')}</button></div></td></tr>`).join('')) : '<p class="mute">Доступов нет.</p>') + `
      <div class="dlg-sec">Добавить</div>
      <div class="fld"><label for="dp1">Провайдер</label><select id="dp1" onchange="const nc=this.value==='namecheap';$('#dpNc').hidden=!nc;$('#dpK').textContent=nc?'API-ключ':'API-токен';$('#dpH').textContent=LIVE.dnsHint(this.value)">${['cloudflare', 'digitalocean', 'namecheap'].map(k => `<option value="${k}">${DNSK[k][0]}</option>`).join('')}</select></div>
      <div id="dpNc" hidden><div class="fld"><label for="dp3">Пользователь API</label><input id="dp3" name="nc-api-user" autocomplete="off" spellcheck="false"></div><div class="hint">Имя аккаунта Namecheap, не e-mail. Белый IP для API Namecheap: <span class="mono">${esc(ips || '—')}</span></div></div>
      <div><div class="fld"><label for="dp2" id="dpK">API-токен</label><input id="dp2" name="dns-api-key" class="mono" type="password" autocomplete="new-password"></div><div class="hint" id="dpH">${DNSK.cloudflare[1]}</div></div>`,
      `<button class="btn pri" type="button" onclick="LIVE.provAdd()">Проверить и добавить</button><button class="btn" type="button" onclick="closeDlg()">Закрыть</button>`);
  };
  L.dnsHint = k => DNSK[k][1];
  L.provAdd = once(async () => {
    const b = { kind: $('#dp1').value, token: $('#dp2').value, user: $('#dp3').value };
    toast('Проверяю доступ…');
    try { await api('POST', '/api/domains/provider', b); toast('Доступ проверен и добавлен'); await loadDomains(); if (L.me) L.openProviders(); } catch (e) { toast(e.message); }
  });
  L.provDel = async id => { try { await api('POST', '/api/domains/provider', { id, delete: true }); toast('Доступ удалён'); } catch (e) { toast(e.message); } await loadDomains(); if (L.me) L.openProviders(); };

  /* ── setup wizard ── */
  const parentZone = n => { n = String(n || '').trim().toLowerCase().replace(/\.$/, ''); const i = n.indexOf('.'); return (i < 0 || n.indexOf('.', i + 1) < 0) ? n : n.slice(i + 1); };
  const dnsRows = (zone, hosts, ips) => {
    const rows = [];
    for (const h of (hosts || []).filter(Boolean).map(x => x.trim().toLowerCase().replace(/\.$/, ''))) {
      if (!(h === zone || h.endsWith('.' + zone))) continue;
      const rel = h === zone ? '@' : h.slice(0, -(zone.length + 1));
      for (const ip of ips || []) rows.push([ip.includes(':') ? 'AAAA' : 'A', rel, ip]);
    }
    rows.push(['CAA', '@', '0 issue "letsencrypt.org"']);
    return rows;
  };
  const W = { name: '', check: null, busy: '', member: null, error: '', pk: {} };
  // wizard package ids (the mock's) → catalog keys
  const WK = { mtp: 'mtproto', wg: 'wireguard', ovpn: 'openvpn', zt: 'zerotier', xrayc: 'xray', f2b: 'fail2ban', mail: 'mail', antispam: 'antispam', webmail: 'webmail' };
  const step = () => S.wz.step;
  const wzHead = () => `<ol class="wz-steps" aria-label="Шаги">${WZ.map((t, i) => `<li class="${i + 1 < step() ? 'done' : i + 1 === step() ? 'cur' : ''}" ${i + 1 === step() ? 'aria-current="step"' : ''}><span>${i + 1 < step() ? '✓' : i + 1}</span>${t}</li>`).join('')}</ol>`;
  const busy = () => W.busy ? `<div class="steps-log"><div><span class="sl run">…</span>${esc(W.busy)}</div></div>` : '';
  const err = () => W.error ? `<p class="note-s" style="color:var(--bad)">${esc(W.error)}</p>` : '';

  function wizardView() {
    const s = step(), w = L.wz || {}, info = L.info || {};
    let body = '', next = 'Дальше', skip = '';
    if (s === 1) {
      body = `<h1>Администратор панели</h1><p class="fs-d">Первый пользователь панели — владелец. Других можно добавить потом.</p>
        <div><div class="fld"><label for="w1code">Код установки</label><input id="w1code" class="mono" autocomplete="off" placeholder="из вывода установщика"></div><div class="hint">Показан в конце установки и лежит в <span class="mono">/var/lib/loft-panel/setup.code</span></div></div>
        <div class="fld"><label for="w1a">Имя пользователя</label><input id="w1a" value="admin" autocomplete="username"></div>
        <div><div class="fld"><label for="w1b">Пароль</label><input id="w1b" type="password" autocomplete="new-password" data-pw-min="9"></div><div class="hint" data-pw-hint>Не короче 9 знаков.</div></div>
        <div><div class="fld"><label for="w1c">Пароль ещё раз</label><input id="w1c" type="password" autocomplete="new-password" data-pw-same="w1b"></div><div class="hint" data-pw-hint></div></div>
        <div class="fld"><label for="w1d">Язык панели</label><select id="w1d">${opts(LANGS, (window.I18N && I18N.lang) || 'ru')}</select></div>`;
      next = 'Создать владельца';
    } else if (s === 2) {
      if (!L.dm) loadDomains().then(() => { if (step() === 2) redraw(); });
      const curNode = info.host || '', curPanel = info.panel && info.panel !== info.host ? info.panel : '';
      if (W.sep === undefined) W.sep = !!curPanel;
      if (W.prov === undefined) W.prov = ((L.dm || {}).providers || [])[0] ? L.dm.providers[0].kind : 'cloudflare';
      const name = W.name || w.domain || curNode, panel = W.panel !== undefined ? W.panel : (w.panel_domain || curPanel);
      const zone = W.zone || parentZone(name);
      const apiDNS = W.prov && W.prov !== 'manual';
      const have = ((L.dm || {}).providers || []).find(p => p.kind === W.prov);
      const ips = w.server_ips || [];
      const line = c => !c ? '' : c.error ? `<p class="note-s" style="color:var(--bad)"><span class="mono">${esc(c.name)}</span>: ${c.error === 'does not resolve' ? 'пока не находится в DNS — добавьте A-запись и подождите несколько минут' : 'это не имя вида vpn.example.net'}</p>`
        : c.match ? `<p class="note-s">${pill('ok', 'ok')} <span class="mono">${esc(c.name)}</span> → <span class="mono">${esc(c.ips.join(', '))}</span> — это этот сервер.</p>`
        : `<p class="note-s" style="color:var(--bad)"><span class="mono">${esc(c.name)}</span> указывает на <span class="mono">${esc(c.ips.join(', '))}</span>, а этот сервер — <span class="mono">${esc(c.server_ips.join(', '))}</span>. Исправьте A-запись у регистратора.</p>`;
      const changed = name !== curNode || (W.sep ? panel : '') !== curPanel;
      const hosts = [name].concat(W.sep && panel ? [panel] : []);
      const recs = dnsRows(zone, hosts, ips);
      // what DNS holds now for these records, and the zone's registration
      const nowKey = JSON.stringify([zone, recs]);
      if (W.nowKey !== nowKey && zone.includes('.')) {
        W.nowKey = nowKey; W.now = null;
        api('POST', '/api/domain/now', { zone, records: recs.map(r => ({ type: r[0], name: r[1], value: r[2] })) })
          .then(v => { if (W.nowKey === nowKey) { W.now = v; if (step() === 2) L.redrawKeep(); } }, () => {});
      }
      const nowCell = i => { const x = W.now && W.now.records[i]; return !x ? '<span class="mute">…</span>' : x.check === 'ok' ? pill('уже есть', 'ok') : x.now.length ? `<span class="mono">${esc(x.now.join(', '))}</span>` : '<span class="mute">нет записи</span>'; };
      body = `<h1>Домен сервера</h1><p class="fs-d">Клиенты подключаются по этому имени, на него же выпускается сертификат. Через DNS-провайдера панель сама создаст записи на адрес <span class="mono">${esc(ips.join(', ') || '—')}</span>.</p>
        <div><div class="fld"><label for="w2a">Имя для подключения клиентов</label><input id="w2a" class="mono" value="${esc(name)}" placeholder="api.example.net" oninput="LIVE.W.name=this.value;LIVE.W.check=null;LIVE.W.zone=LIVE.W.zone||''"></div><div class="hint">Попадает в ссылки loft:// и профили членов сети.${W.now && W.now.reg_until ? ` Зона ${esc(zone)} · регистрация до ${new Date(W.now.reg_until).toLocaleDateString(LOC())}.` : ''}</div></div>${line(W.check)}
        <label class="chk"><input id="w2sep" type="checkbox" ${W.sep ? 'checked' : ''} onchange="LIVE.W.sep=this.checked;LIVE.W.pcheck=null;LIVE.redrawKeep()"><span>Отдельное имя для панели<span class="hint" style="display:block;margin:0">Тот, кто проверяет адрес ноды, увидит обычную страницу веб-сервера и сертификат только с именем ноды — панель там не найти. Оба имени указывают на один IP, и по истории DNS это видно.</span></span></label>
        ${W.sep ? `<div class="fld"><label for="w2p">Имя панели</label><input id="w2p" class="mono" value="${esc(panel)}" placeholder="panel.example.net" oninput="LIVE.W.panel=this.value;LIVE.W.pcheck=null"></div>${line(W.pcheck)}` : ''}
        <div><div class="fld"><label for="w2z">Зона</label><input id="w2z" class="mono" value="${esc(zone)}" placeholder="example.net" list="w2zl" oninput="LIVE.W.zone=this.value"></div><div class="hint">В этой зоне панель ведёт записи. Подсказки — из зон провайдера.</div></div><datalist id="w2zl">${(W.pz || []).map(x => `<option value="${esc(x)}">`).join('')}</datalist>
        <div class="dlg-sec">Где управляется DNS зоны</div>
        <div class="opts">${['cloudflare', 'digitalocean', 'namecheap', 'manual'].map(k => `<button type="button" class="opt ${W.prov === k ? 'on' : ''}" aria-pressed="${W.prov === k}" onclick="LIVE.pickProv('${k}')"><b>${DNSK[k][0]}</b><span>${DNSK[k][1]}</span></button>`).join('')}</div>
        ${apiDNS ? (have ? `<p class="note-s">${pill('ok', 'ok')} <b>${DNSK[W.prov][0]}</b> · <span class="mono">${esc(have.hint)}</span>. Доступ уже добавлен.</p>` :
          `${W.prov === 'namecheap' ? `<div><div class="fld"><label for="w2nc">Пользователь API</label><input id="w2nc" name="nc-api-user" autocomplete="off" spellcheck="false" value="${esc(W.ncuser || '')}" oninput="LIVE.W.ncuser=this.value"></div><div class="hint">Имя аккаунта Namecheap, не e-mail. Белый IP для API Namecheap: <span class="mono">${esc((ips.filter(x => !x.includes(':'))[0]) || '—')}</span></div></div>` : ''}
           <div><div class="fld"><label for="w2b">${W.prov === 'namecheap' ? 'API-ключ' : 'API-токен'} ${DNSK[W.prov][0]}</label><input id="w2b" name="dns-api-key" class="mono" type="password" autocomplete="new-password" value="${esc(W.token || '')}" oninput="LIVE.W.token=this.value"></div><div class="hint">${DNSK[W.prov][1]}</div></div>
           <div class="acts" style="margin:0"><button class="btn sm" type="button" onclick="LIVE.checkProv()">Проверить</button></div>`) :
          `<p class="note-s">Внесите записи ниже у регистратора. Сертификат выпустится по HTTP-01, когда имя начнёт указывать на сервер.</p>
           <div class="acts" style="margin:0"><button class="btn sm" type="button" onclick="LIVE.checkName()">Проверить DNS</button></div>
           ${dnsBad() ? `<label class="chk"><input id="w2force" type="checkbox" ${W.force ? 'checked' : ''} onchange="LIVE.W.force=this.checked"><span>Всё равно продолжить<span class="hint" style="display:block;margin:0">Например, DNS ещё обновляется или сервер за NAT. Сертификат Let’s Encrypt не выпустится, пока имя не начнёт указывать сюда.</span></span></label>` : ''}`}
        <div class="dlg-sec">${apiDNS ? 'Панель создаст записи' : 'Нужны записи'}</div>
        ${table(['Тип', 'Имя', 'Значение', 'Сейчас'], recs.map((r, i) => `<tr><td class="mono">${esc(r[0])}</td><td class="mono">${esc(r[1])}</td><td class="mono">${esc(r[2])}</td><td>${nowCell(i)}</td></tr>`).join('') || `<tr><td colspan="4" class="mute">Нет адресов сервера — A и AAAA не создать. CAA для Let’s Encrypt всё равно будет.</td></tr>`)}
        <p class="hint" style="margin:0">Сейчас нода — <span class="mono">${esc(curNode || '—')}</span>, панель — <span class="mono">${esc(curPanel || curNode || '—')}</span>.${changed ? ' При смене имён нода перезапустится — подключения прервутся на пару секунд.' : ''}</p>`;
      next = apiDNS ? 'Создать записи и дальше' : (changed ? 'Сменить имена и дальше' : 'Дальше');
      skip = 'Пропустить — работать по IP';
    } else if (s === 3) {
      const until = w.cert_until ? new Date(w.cert_until) : null;
      const pd = w.panel_domain, puntil = w.panel_cert_until ? new Date(w.panel_cert_until) : null;
      const row = (who, name, u) => `<p class="note-s">${u ? pill('до ' + u.toLocaleDateString(LOC()), 'ok') : pill('самоподписанный', 'warn')} ${who} <span class="mono">${esc(name)}</span></p>`;
      const dns01 = ((L.dm || {}).zones || []).some(z => z.provider && z.provider !== 'manual');
      body = `<h1>Сертификат</h1><p class="fs-d">Let’s Encrypt${pd ? ' — отдельный для ноды и для панели, чтобы имя панели не попало в сертификат ноды' : ''}. ${dns01 ? 'Проверка DNS-01 через DNS-провайдера зоны. Порт 80 не нужен.' : 'Проверка идёт через порт 80 этого сервера — он должен быть открыт.'} Продлевается сам за 30 дней до конца срока.</p>
        ${row('нода', w.domain || info.host || '—', until)}${pd ? row('панель', pd, puntil) : ''}
        ${until && (!pd || puntil) ? '' : '<p class="hint" style="margin:0">С самоподписанным сертификатом браузеры и приложения предупреждают.</p>'}
        <div class="fld"><label for="w3a">Почта для писем Let’s Encrypt (необязательно)</label><input id="w3a" type="email" value="${esc(w.email || '')}" placeholder="admin@example.net"></div>`;
      const all = until && (!pd || puntil);
      next = all ? 'Дальше' : pd ? 'Выпустить сертификаты' : 'Выпустить сертификат';
      skip = all ? '' : 'Оставить самоподписанный';
    } else if (s === 4) {
      const m = W.member;
      body = `<h1>Первый член сети</h1><p class="fs-d">Устройство, которое подключится к серверу первым. Остальных — потом в «Членах сети».</p>` + (m ? `
        <p class="note-s">${pill('создан', 'ok')} Ключ показан один раз — сохраните ссылку или файл.</p>
        <div class="cards" style="grid-template-columns:repeat(auto-fill,minmax(min(220px,100%),1fr))">${m.qr ? `<div class="qr" style="align-self:start">${m.qr}</div>` : ''}
        <div style="display:flex;flex-direction:column;gap:12px;min-width:0"><div class="dlg-sec" style="margin:0">Ссылка для приложения loft</div><div class="uri" id="w4link">${esc(m.link)}</div>
        <p class="hint" style="margin:0">Откройте приложение loft на устройстве и отсканируйте код или вставьте ссылку.</p></div></div>
        <div class="acts" style="margin:0"><button class="btn sm pri" type="button" onclick="LIVE.copy('#w4link')">Копировать ссылку</button><button class="btn sm" type="button" onclick="LIVE.download(LIVE.W.member.id + '.loft', LIVE.W.member.file)">${ic('i-down', 16)}Скачать .loft</button><button class="btn sm" type="button" onclick="LIVE.copy('#w4file')">Копировать файл</button></div>
        <details class="adv"><summary>Файл .loft</summary><textarea id="w4file" class="mono ta" rows="12" readonly style="width:100%;margin-top:12px">${esc(m.file)}</textarea></details>` : `
        <div class="fld"><label for="w4a">Имя</label><input id="w4a" value="Alice" oninput="var i=$('#w4b');if(!i.dataset.touched)i.value=this.value.toLowerCase().replace(/[^a-z0-9._-]+/g,'-').replace(/^-+|-+$/g,'')+'-phone'"></div>
        <div><div class="fld"><label for="w4b">Идентификатор</label><input id="w4b" class="mono" value="alice-phone" oninput="this.dataset.touched=1"></div><div class="hint">Латиница, цифры, точка, дефис, подчёркивание.</div></div>`);
      next = m ? 'Дальше' : 'Создать и выдать ключ';
      skip = m ? '' : 'Пропустить';
    } else {
      // The mock's first choice of packages, limited to what the catalog has.
      const have = new Set(((L.pk && L.pk.packages) || []).filter(p => !p.base && !p.installed).map(p => p.key));
      const groups = WPK.map(([g, items]) => [g, items.filter(x => have.has(WK[x[0]]) && (!x[3] || W.pk[x[3]]))]).filter(([, l]) => l.length);
      const n = groups.flatMap(g => g[1]).filter(x => W.pk[x[0]]);
      const mb = n.reduce((s, x) => s + ((L.pk.packages.find(p => p.key === WK[x[0]]) || {}).disk_mb || 0), 0);
      body = `<h1>Что ещё поставить</h1><p class="fs-d">Нода loft, веб-сервер и панель уже стоят; SSTP-клиент встроен. Остальное — по выбору. Можно ничего не отмечать и поставить позже в «Пакетах».</p>
        ${!L.pk ? '<p class="mute">Загружаю список пакетов…</p>' : groups.map(([g, items]) => `<fieldset class="pk-g"><legend>${g}</legend>${items.map(x => `<label class="pk ${x[3] ? 'sub' : ''}"><input type="checkbox" ${W.pk[x[0]] ? 'checked' : ''} onchange="LIVE.W.pk['${x[0]}']=this.checked;LIVE.redrawKeep()"><span><b>${x[1]}</b><span class="hint" style="display:block;margin:2px 0 0">${x[2]}</span></span></label>`).join('')}</fieldset>`).join('')}
        <p class="hint">${n.length ? `Выбрано ${n.length} · будет скачано около ${mb} МБ. Установка идёт в фоне: панель откроется сразу, на «Пакетах» виден её ход.` : 'Ничего не выбрано.'}</p>`;
      if (!L.pk) loadPackages().then(() => { if (step() === 5) redraw(); });
      next = n.length ? 'Установить выбранное и открыть панель' : 'Открыть панель';
    }
    const nav = `<div class="wz-foot">${s > 2 ? `<button class="btn" type="button" ${W.busy ? 'disabled' : ''} onclick="LIVE.back()">Назад</button>` : ''}
      ${skip ? `<button class="btn ghost" type="button" ${W.busy ? 'disabled' : ''} onclick="LIVE.skip()">${skip}</button>` : ''}
      <button class="btn pri" type="button" style="margin-left:auto" ${W.busy ? 'disabled' : ''} onclick="wzNext()">${next}</button></div>`;
    drawnStep = s;
    return `<div class="fs"><div class="fs-card">${fullHead('настройка сервера')}${wzHead()}<div class="wz-body">${body}${busy()}${err()}</div>${nav}</div></div>`;
  }
  L.W = W;
  // redraw keeps what was typed on the same step (an error must not wipe a password).
  let drawnStep = 0;
  const redraw = () => {
    const keep = {};
    if (drawnStep === step()) $('#full').querySelectorAll('input[id],textarea[id]').forEach(i => { keep[i.id] = i.value; });
    renderFull();
    for (const [id, v] of Object.entries(keep)) { const i = document.getElementById(id); if (i && !i.readOnly) i.value = v; }
  };
  const work = async (label, fn) => {
    W.busy = label; W.error = ''; redraw();
    try { await fn(); } catch (e) { W.error = e.message; }
    W.busy = ''; redraw();
  };
  const advance = async () => { W.error = ''; S.wz.step++; try { L.wz = await api('GET', '/api/wizard'); L.info = L.wz.node; } catch (e) { /* keep */ } redraw(); };
  L.back = () => { W.error = ''; S.wz.step = Math.max(2, step() - 1); redraw(); };
  L.skip = () => advance();
  // Read inputs before work(): it redraws the step to show progress.
  L.redrawKeep = () => redraw();
  const bad = c => c && (c.error || !c.match);
  function dnsBad() { return bad(W.check) || (W.sep && bad(W.pcheck)); }
  const readNames = () => {
    W.name = $('#w2a').value.trim().toLowerCase().replace(/\.$/, '');
    W.sep = !!($('#w2sep') && $('#w2sep').checked);
    W.force = !!($('#w2force') && $('#w2force').checked);
    W.panel = W.sep && $('#w2p') ? $('#w2p').value.trim().toLowerCase().replace(/\.$/, '') : '';
  };
  const checkBoth = async () => {
    W.check = await api('POST', '/api/domain/check', { name: W.name });
    W.pcheck = W.sep ? await api('POST', '/api/domain/check', { name: W.panel }) : null;
  };
  L.checkName = () => { readNames(); return work('Проверяю DNS…', checkBoth); };
  L.pickProv = k => { W.prov = k; W.pz = []; redraw(); if (k !== 'manual') L.loadProvZones(); };
  L.loadProvZones = async () => {
    const have = ((L.dm || {}).providers || []).find(p => p.kind === W.prov);
    if (!have) return;
    try { W.pz = (await api('GET', '/api/domains/provider-zones?id=' + encodeURIComponent(have.id))).zones || []; } catch (e) { W.pz = []; }
    if (step() === 2) redraw();
  };
  L.checkProv = () => work('Проверяю доступ…', async () => {
    await api('POST', '/api/domains/provider', { kind: W.prov, token: ($('#w2b') && $('#w2b').value) || W.token || '', user: ($('#w2nc') && $('#w2nc').value) || W.ncuser || '', check_only: true });
    toast('Доступ проверен');
  });
  const saveWizardDNS = async (name, panel) => {
    const zone = (($('#w2z') && $('#w2z').value) || W.zone || parentZone(name)).trim().toLowerCase().replace(/\.$/, '');
    W.zone = zone;
    const zones = [];
    if (zone) zones.push(zone);
    if (panel) {
      const pz = parentZone(panel);
      if (pz && !zones.includes(pz)) zones.push(pz);
    }
    if (!zones.length) return;
    let pid = 'manual';
    if (W.prov && W.prov !== 'manual') {
      const have = ((L.dm || {}).providers || []).find(p => p.kind === W.prov);
      if (have) pid = have.id;
      else {
        const r = await api('POST', '/api/domains/provider', { kind: W.prov, token: ($('#w2b') && $('#w2b').value) || W.token || '', user: ($('#w2nc') && $('#w2nc').value) || W.ncuser || '' });
        pid = r.id;
      }
    }
    const hosts = [name].concat(panel ? [panel] : []);
    for (const z of zones) {
      await api('POST', '/api/domains/zone', { name: z, provider: pid, create: ['addr', 'caa'], hosts });
    }
    await loadDomains();
  };

  window.wzNext = async () => {
    const s = step();
    if (s === 1) {
      if (!pwCheck($('#full'), true)) { toast('Проверьте пароль'); return; }
      const owner = { login: $('#w1a').value.trim(), password: $('#w1b').value, code: $('#w1code').value.trim() };
      const lang = ($('#w1d') && $('#w1d').value) || 'ru';
      return work('Создаю владельца…', async () => {
        const me = await api('POST', '/api/setup', owner)
          ;
        L.me = me; L.csrf = me.csrf;
        $('#userBtn').textContent = me.login + ' · владелец';
        try { await api('POST', '/api/settings/panel', { session_idle: '30m', session_max: '7d', lang }); } catch (e) { /* language stays in this browser */ }
        await loadData();
        S.wz.step = 2;
        if (window.I18N) I18N.set(lang);
      });
    }
    if (s === 2) {
      readNames();
      W.token = ($('#w2b') && $('#w2b').value) || W.token || '';
      W.ncuser = ($('#w2nc') && $('#w2nc').value) || W.ncuser || '';
      W.zone = ($('#w2z') && $('#w2z').value) || W.zone || '';
      const name = W.name, panel = W.sep ? W.panel : '';
      if (W.sep && (!panel || panel === name)) { W.error = 'Имя панели должно отличаться от имени для клиентов'; redraw(); return; }
      const info = L.info || {}, curPanel = info.panel && info.panel !== info.host ? info.panel : '';
      const apiDNS = W.prov && W.prov !== 'manual';
      if (apiDNS && !((L.dm || {}).providers || []).some(p => p.kind === W.prov) && (!W.token || (W.prov === 'namecheap' && !W.ncuser))) { W.error = 'Нужен токен (для Namecheap — и пользователь API)'; redraw(); return; }
      return work(apiDNS ? 'Создаю записи DNS…' : 'Проверяю DNS…', async () => {
        if (!apiDNS) {
          await checkBoth();
          if (dnsBad() && !W.force) throw new Error('Имена пока не указывают на этот сервер. Исправьте A-записи или отметьте «Всё равно продолжить».');
        }
        await saveWizardDNS(name, panel);
        if (name === info.host && panel === curPanel) return advance();
        W.busy = 'Меняю имена: нода перезапускается…'; redraw();
        await job('/api/domain', { name, panel }, lost => { W.busy = lost ? 'Нода перезапускается, жду ответа…' : 'Меняю имена…'; redraw(); });
        W.check = W.pcheck = null;
        const where = panel || name;
        if (location.hostname !== where && location.hostname !== '127.0.0.1' && location.hostname !== 'localhost') {
          W.busy = 'Панель теперь на https://' + where + '/ — перехожу…'; redraw();
          await sleep(1500);
          location.href = 'https://' + where + '/';
          return;
        }
        await advance();
      });
    }
    if (s === 3) {
      if (L.wz && L.wz.cert_until && (!L.wz.panel_domain || L.wz.panel_cert_until)) return advance();
      const email = $('#w3a').value.trim();
      return work('Проверяю имя и порт 80, запрашиваю сертификат у Let’s Encrypt…', async () => {
        await job('/api/cert/issue', { email }, lost => { W.busy = lost ? 'Устанавливаю сертификат: нода перезапускается…' : 'Запрашиваю сертификат у Let’s Encrypt…'; redraw(); });
        L.wz = await api('GET', '/api/wizard');
        toast('Сертификат выпущен. Обновите страницу, чтобы браузер увидел его.');
      });
    }
    if (s === 4) {
      if (W.member) return advance();
      const id = $('#w4b').value.trim(), name = $('#w4a').value.trim();
      return work('Создаю члена сети…', async () => {
        W.member = await job('/api/wizard/member', { id, name });
        await loadData();
      });
    }
    const wsub = Object.fromEntries(WPK.flatMap(g => g[1]).filter(x => x[3]).map(x => [x[0], x[3]]));
    const install = Object.keys(W.pk).filter(k => W.pk[k] && WK[k] && (!wsub[k] || W.pk[wsub[k]])).map(k => WK[k]);
    if (install.length) {
      try { await api('POST', '/api/packages/apply', { install }); } catch (e) { W.error = e.message; redraw(); return; }
    }
    try { await api('POST', '/api/wizard/done', {}); } catch (e) { if (e.status !== 401) toast(e.message); return; }
    if (L.wz) L.wz.done = true;
    L.pk = null;
    go(install.length ? 'packages' : 'main');
  };

  // Who is online changes on its own: refresh it on the pages that show it.
  setInterval(() => {
    if (L.me && ['main', 'members', 'segments'].includes(S.route) && !$('#dlgRoot').firstChild) loadPresence().then(() => render());
  }, 30000);

  // A page restored from the back/forward cache is stale (it may still show
  // the wizard): load it fresh.
  window.addEventListener('pageshow', e => { if (e.persisted) location.reload(); });
  // #setup / #login left in history or a bookmark must not pin a screen.
  const settle = r => { if (location.hash.slice(1) !== r) history.replaceState(null, '', '#' + r); S.route = r; S.arg = ''; };

  /* boot: only when served by loft-panel */
  // The built page starts hidden (style#lp-boot): nothing of the mock's
  // sample data shows before the panel knows who is looking.
  const reveal = () => { const b = document.getElementById('lp-boot'); if (b) b.remove(); };
  async function boot() {
    try { await boot0(); } finally { reveal(); }
  }
  async function boot0() {
    let st;
    L.want = location.hash.slice(1);
    try { st = await api('GET', '/api/state'); } catch (e) {
      if (location.protocol === 'file:') return; // the mock opened from disk
      wrapViews(); settle('login'); render(); toast(e.message); return;
    }
    wrapViews();
    S.ver = st.version || '';
    L.keepKeys = !!st.keep_member_keys;
    if (window.I18N) I18N.def(st.lang); // the panel's language until this browser picks one
    showHost();
    if (st.setup) { S.wz.step = 1; settle('setup'); render(); return; }
    if (location.hash.startsWith('#invite/')) { startInvite(location.hash.slice(8)); return; }
    if (!st.me) { settle('login'); render(); return; }
    if (location.hash === '#setup' || location.hash === '#login') settle('main');
    await signedIn(st.me, st).catch(e => console.error(e));
  }
  boot();
})();
