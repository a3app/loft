// engine.cpp: the Engine class behind the Sailfish / Aurora QML app.
// It turns the pasted client YAML or share link (loft://, sstp://, vless://,
// trojan://) into a VPN row for connman-vpnd, the system VPN service, and
// tracks its state for the UI. When ConnMan cannot be used it runs
// loft-front (the client engine binary) itself as a SOCKS-only proxy.
// The ConnMan side of the chain lives in ../connman/.
#include "engine.h"

// Package name, used to find this flavour's libexec dir (harbour-loft on
// Sailfish, app.loft.aurora on Aurora); the build sets it.
#ifndef APP_ID
#define APP_ID "harbour-loft"
#endif

#include <QCoreApplication>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QStandardPaths>
#include <QTextStream>
#include <QUrl>
#include <QUrlQuery>

#ifdef HAVE_QT_DBUS
#include <QDBusArgument>
#include <QDBusConnection>
#include <QDBusInterface>
#include <QDBusMessage>
#include <QDBusObjectPath>
#include <QDBusReply>
#endif

// engine.cpp: the QML-facing host. See engine.h for the two ways it
// connects (ConnMan row or a loft-front child process).

// Strips one pair of matching quotes around a YAML scalar.
static QString unquote(QString v)
{
    v = v.trimmed();
    if (v.size() >= 2) {
        const QChar a = v[0];
        const QChar b = v[v.size() - 1];
        if ((a == QLatin1Char('"') && b == QLatin1Char('"'))
            || (a == QLatin1Char('\'') && b == QLatin1Char('\''))) {
            return v.mid(1, v.size() - 2);
        }
    }
    return v;
}

// A minimal YAML read: the value of the first line (at any depth) that
// starts with "key:". Enough for the flat fields this host needs; it is not
// a YAML parser, so with several outbounds the first one wins.
static QString yamlValue(const QString &body, const QString &key)
{
    const QString prefix = key + QLatin1Char(':');
    const QStringList lines = body.split(QLatin1Char('\n'));
    for (int i = 0; i < lines.size(); ++i) {
        const QString t = lines.at(i).trimmed();
        if (t.startsWith(prefix)) {
            return unquote(t.mid(prefix.size()));
        }
    }
    return QString();
}

// Starts idle and loads the client.yaml saved by the last Connect, so the
// text field is filled again after an app restart.
Engine::Engine(QObject *parent)
    : QObject(parent)
    , up_(false)
    , busy_(false)
    , proc_(0)
{
    line_ = QStringLiteral("ConnMan owns the tun");
    connmanPath_.clear();
    const QString path = dataDir() + QStringLiteral("/client.yaml");
    QFile f(path);
    if (f.open(QIODevice::ReadOnly | QIODevice::Text)) {
        yaml_ = QString::fromUtf8(f.readAll());
    }
}

// Closing the app also ends the session: the ConnMan row is disconnected
// and the fallback loft-front, if any, is stopped.
Engine::~Engine()
{
    stopConnMan();
    stopProc();
}

// Setter for the yaml property (bound to the text field in QML).
void Engine::setYaml(const QString &v)
{
    if (yaml_ == v) {
        return;
    }
    yaml_ = v;
    emit yamlChanged();
}

// Parses the pasted text, saves it as client.yaml, and connects: through
// ConnMan when possible, otherwise by starting loft-front with SOCKS only.
void Engine::connectSession()
{
    if (busy_ || up_) {
        return;
    }
    // 1. Parse the text; a parse error is shown and nothing starts.
    const QString body = yaml_.trimmed();
    if (body.isEmpty()) {
        setLine(QStringLiteral("paste YAML or loft://"));
        return;
    }
    Session s;
    QString err;
    if (!parseBody(body, &s, &err)) {
        setLine(err);
        return;
    }
    // 2. Save it and drop any earlier session.
    persistYaml(body);
    stopProc();
    stopConnMan();
    setBusy(true);
    setLine(QStringLiteral("Connecting…"));
    // 3. Prefer ConnMan: it gives a real tun for every app.
    if (connectConnMan(s)) {
        return;
    }
    // 4. Fallback: run loft-front ourselves with only a local SOCKS proxy.
    const QString bin = binaryPath();
    if (bin.isEmpty()) {
        setBusy(false);
        setLine(QStringLiteral("no loft-front"));
        return;
    }
    proc_ = new QProcess(this);
    proc_->setProcessChannelMode(QProcess::SeparateChannels);
    connect(proc_, SIGNAL(readyReadStandardOutput()), this, SLOT(onStdout()));
    connect(proc_, SIGNAL(readyReadStandardError()), this, SLOT(onStderr()));
    connect(proc_, SIGNAL(finished(int,QProcess::ExitStatus)), this, SLOT(onFinished(int)));
    proc_->start(bin, argsFor(s));
    if (!proc_->waitForStarted(4000)) {
        setBusy(false);
        setLine(QStringLiteral("loft-front failed to start"));
        stopProc();
    }
}

// Ends the session. A ConnMan row is disconnected at once. A fallback
// loft-front is terminated (killed if it does not exit), and onFinished
// then resets the state.
void Engine::disconnectSession()
{
    if (!connmanPath_.isEmpty()) {
        stopConnMan();
        setUp(false);
        setBusy(false);
        setLine(QStringLiteral("Disconnected"));
        return;
    }
    if (proc_ == 0) {
        setUp(false);
        setBusy(false);
        setLine(QStringLiteral("Disconnected"));
        return;
    }
    proc_->terminate();
    if (!proc_->waitForFinished(1500)) {
        proc_->kill();
        proc_->waitForFinished(500);
    }
}

// Finds loft-front: next to the app, in the app's libexec, in either
// flavour's libexec (Sailfish or Aurora), or in /usr/bin.
QString Engine::binaryPath() const
{
    QStringList paths;
    const QString here = QCoreApplication::applicationDirPath();
    paths << (here + QStringLiteral("/loft-front"));
    paths << (here + QStringLiteral("/../libexec/") + QLatin1String(APP_ID) + QStringLiteral("/loft-front"));
    paths << (QStringLiteral("/usr/libexec/") + QLatin1String(APP_ID) + QStringLiteral("/loft-front"));
    paths << QStringLiteral("/usr/libexec/harbour-loft/loft-front");
    paths << QStringLiteral("/usr/libexec/app.loft.aurora/loft-front");
    paths << QStringLiteral("/usr/bin/loft-front");
    for (int i = 0; i < paths.size(); ++i) {
        const QFileInfo fi(paths.at(i));
        if (fi.exists() && fi.isFile()) {
            return fi.absoluteFilePath();
        }
    }
    return QString();
}

// The app's writable data dir (client.yaml, instance file), created if
// missing.
QString Engine::dataDir() const
{
    const QString d = QStandardPaths::writableLocation(QStandardPaths::AppDataLocation);
    QDir().mkpath(d);
    return d;
}

// Host part of the session URL, without scheme, port, path, query or
// fragment; IPv6 without brackets.
QString Engine::hostOf(const Session &s) const
{
    QString raw = s.url;
    const int scheme = raw.indexOf(QStringLiteral("://"));
    if (scheme >= 0) {
        raw = raw.mid(scheme + 3);
    }
    if (raw.startsWith(QLatin1Char('['))) {
        const int end = raw.indexOf(QLatin1Char(']'));
        if (end > 0) {
            return raw.mid(1, end - 1);
        }
    }
    int cut = raw.indexOf(QLatin1Char('/'));
    const int q = raw.indexOf(QLatin1Char('?'));
    const int h = raw.indexOf(QLatin1Char('#'));
    if (q >= 0 && (cut < 0 || q < cut)) {
        cut = q;
    }
    if (h >= 0 && (cut < 0 || h < cut)) {
        cut = h;
    }
    if (cut >= 0) {
        raw = raw.left(cut);
    }
    const int colon = raw.lastIndexOf(QLatin1Char(':'));
    if (colon > 0) {
        raw = raw.left(colon);
    }
    return raw;
}

// Connects through connman-vpnd. Reuses an existing VPN row of type
// "loft" for the same host, or creates one named "Loft" with the session
// fields as Loft.* properties (the plugin turns them into helper flags).
// Then it listens for the row's State and calls Connect. Returns false
// when there is no system bus, no ConnMan VPN service, or Create fails, so
// the caller falls back to loft-front.
bool Engine::connectConnMan(const Session &s)
{
#ifdef HAVE_QT_DBUS
    QDBusConnection bus = QDBusConnection::systemBus();
    if (!bus.isConnected()) {
        return false;
    }
    QDBusInterface mgr(QStringLiteral("net.connman.vpn"),
                       QStringLiteral("/"),
                       QStringLiteral("net.connman.vpn.Manager"),
                       bus);
    if (!mgr.isValid()) {
        return false;
    }

    // 1. Look for an existing "loft" row for the same host, so repeated
    //    Connects do not pile up rows in Settings.
    const QString host = hostOf(s);
    QString path;

    QDBusMessage get = QDBusMessage::createMethodCall(
        QStringLiteral("net.connman.vpn"),
        QStringLiteral("/"),
        QStringLiteral("net.connman.vpn.Manager"),
        QStringLiteral("GetConnections"));
    const QDBusMessage got = bus.call(get, QDBus::Block, 2000);
    if (got.type() == QDBusMessage::ReplyMessage && !got.arguments().isEmpty()) {
        QDBusArgument arr = got.arguments().at(0).value<QDBusArgument>();
        if (arr.currentType() == QDBusArgument::ArrayType) {
            arr.beginArray();
            while (!arr.atEnd()) {
                QDBusObjectPath op;
                QVariantMap props;
                arr.beginStructure();
                arr >> op >> props;
                arr.endStructure();
                if (props.value(QStringLiteral("Type")).toString() == QLatin1String("loft")
                    && (host.isEmpty()
                        || props.value(QStringLiteral("Host")).toString() == host)) {
                    path = op.path();
                    break;
                }
            }
            arr.endArray();
        }
    }

    // 2. None found: create the row with the session as Loft.* properties.
    if (path.isEmpty()) {
        QVariantMap props;
        props.insert(QStringLiteral("Type"), QStringLiteral("loft"));
        props.insert(QStringLiteral("Name"), QStringLiteral("Loft"));
        props.insert(QStringLiteral("Domain"), QStringLiteral("loft"));
        if (!host.isEmpty()) {
            props.insert(QStringLiteral("Host"), host);
        }
        if (!s.token.isEmpty()) {
            props.insert(QStringLiteral("Loft.Token"), s.token);
        }
        if (!s.url.isEmpty()) {
            props.insert(QStringLiteral("Loft.URL"), s.url);
        }
        props.insert(QStringLiteral("Loft.Type"),
                     s.type.isEmpty() ? QStringLiteral("auto") : s.type);
        if (!s.path.isEmpty()) {
            props.insert(QStringLiteral("Loft.Path"), s.path);
        }
        if (!s.cdn.isEmpty()) {
            props.insert(QStringLiteral("Loft.CDN"), s.cdn);
        }
        if (!s.user.isEmpty()) {
            props.insert(QStringLiteral("Loft.User"), s.user);
        }
        if (!s.sni.isEmpty()) {
            props.insert(QStringLiteral("Loft.SNI"), s.sni);
        }
        QDBusMessage create = QDBusMessage::createMethodCall(
            QStringLiteral("net.connman.vpn"),
            QStringLiteral("/"),
            QStringLiteral("net.connman.vpn.Manager"),
            QStringLiteral("Create"));
        create << QVariant::fromValue(props);
        const QDBusMessage created = bus.call(create, QDBus::Block, 4000);
        if (created.type() != QDBusMessage::ReplyMessage
            || created.arguments().isEmpty()) {
            return false;
        }
        path = created.arguments().at(0).value<QDBusObjectPath>().path();
        if (path.isEmpty()) {
            return false;
        }
    }

    // 3. Watch the row's State and ask it to connect; the answer comes
    //    through onConnManProperty.
    connmanPath_ = path;
    bus.connect(QStringLiteral("net.connman.vpn"),
                path,
                QStringLiteral("net.connman.vpn.Connection"),
                QStringLiteral("PropertyChanged"),
                this,
                SLOT(onConnManProperty(QString,QDBusVariant)));
    QDBusInterface conn(QStringLiteral("net.connman.vpn"),
                        path,
                        QStringLiteral("net.connman.vpn.Connection"),
                        bus);
    conn.asyncCall(QStringLiteral("Connect"));
    return true;
#else
    Q_UNUSED(s);
    return false;
#endif
}

// Stops listening to the ConnMan row and asks it to disconnect.
void Engine::stopConnMan()
{
#ifdef HAVE_QT_DBUS
    if (connmanPath_.isEmpty()) {
        return;
    }
    QDBusConnection bus = QDBusConnection::systemBus();
    bus.disconnect(QStringLiteral("net.connman.vpn"),
                   connmanPath_,
                   QStringLiteral("net.connman.vpn.Connection"),
                   QStringLiteral("PropertyChanged"),
                   this,
                   SLOT(onConnManProperty(QString,QDBusVariant)));
    QDBusInterface conn(QStringLiteral("net.connman.vpn"),
                        connmanPath_,
                        QStringLiteral("net.connman.vpn.Connection"),
                        bus);
    if (conn.isValid()) {
        conn.asyncCall(QStringLiteral("Disconnect"));
    }
    connmanPath_.clear();
#endif
}

#ifdef HAVE_QT_DBUS
// Maps ConnMan's row State (ready, failure, idle, disconnect) onto
// up/busy/line.
void Engine::onConnManProperty(const QString &name, const QDBusVariant &value)
{
    if (name != QLatin1String("State")) {
        return;
    }
    const QString state = value.variant().toString();
    if (state == QLatin1String("ready")) {
        setBusy(false);
        setUp(true);
        setLine(QStringLiteral("Connected"));
        return;
    }
    if (state == QLatin1String("failure")) {
        setBusy(false);
        setUp(false);
        setLine(QStringLiteral("ConnMan failed"));
        return;
    }
    if (state == QLatin1String("idle") || state == QLatin1String("disconnect")) {
        setBusy(false);
        setUp(false);
        if (!line_.startsWith(QLatin1String("Connecting"))) {
            setLine(QStringLiteral("Disconnected"));
        }
    }
}
#endif

// Saves the pasted text as client.yaml in the app data dir; the
// constructor loads it back on the next start.
void Engine::persistYaml(const QString &body) const
{
    QFile f(dataDir() + QStringLiteral("/client.yaml"));
    if (!f.open(QIODevice::WriteOnly | QIODevice::Truncate | QIODevice::Text)) {
        return;
    }
    f.write(body.toUtf8());
    if (!body.endsWith(QLatin1Char('\n'))) {
        f.write("\n");
    }
}

// One line with "://" is a share link; anything else is YAML.
bool Engine::parseBody(const QString &body, Session *out, QString *err) const
{
    if (!body.contains(QLatin1Char('\n')) && body.contains(QStringLiteral("://"))) {
        return parseURI(body.trimmed(), out, err);
    }
    return parseYAML(body, out, err);
}

// Reads loft://token@host/path?type=…&cdn=…, or sstp/vless/trojan links
// (secret from the password part, else the user part). The full share
// syntax lives in loft-front; this only pulls out what ConnMan needs.
bool Engine::parseURI(const QString &raw, Session *out, QString *err) const
{
    const QUrl u(raw);
    if (!u.isValid()) {
        *err = QStringLiteral("bad URI");
        return false;
    }
    const QString scheme = u.scheme().toLower();
    out->kind = scheme;
    if (scheme == QLatin1String("loft")) {
        out->token = u.userName();
        const QString host = u.host();
        if (host.isEmpty() || out->token.isEmpty()) {
            *err = QStringLiteral("loft:// needs token@host");
            return false;
        }
        out->url = QStringLiteral("https://") + host;
        out->path = u.path();
        out->type = QUrlQuery(u).queryItemValue(QStringLiteral("type"));
        if (out->type.isEmpty()) {
            out->type = QStringLiteral("auto");
        }
        out->cdn = QUrlQuery(u).queryItemValue(QStringLiteral("cdn"));
        out->kind = QStringLiteral("loft");
        return true;
    }
    if (scheme == QLatin1String("sstp") || scheme == QLatin1String("vless")
        || scheme == QLatin1String("trojan")) {
        out->type = scheme;
        out->token = u.password().isEmpty() ? u.userName() : u.password();
        out->user = u.userName();
        const int port = u.port();
        out->url = u.host();
        if (port > 0) {
            out->url += QLatin1Char(':') + QString::number(port);
        }
        if (out->url.isEmpty()) {
            *err = scheme + QStringLiteral(":// needs a host");
            return false;
        }
        return true;
    }
    *err = QStringLiteral("unsupported URI");
    return false;
}

// Reads the flat fields from client YAML. A url and a token (or user) are
// required.
bool Engine::parseYAML(const QString &raw, Session *out, QString *err) const
{
    out->token = yamlValue(raw, QStringLiteral("token"));
    out->url = yamlValue(raw, QStringLiteral("url"));
    out->type = yamlValue(raw, QStringLiteral("type"));
    out->path = yamlValue(raw, QStringLiteral("path"));
    out->user = yamlValue(raw, QStringLiteral("user"));
    out->cdn = yamlValue(raw, QStringLiteral("cdn"));
    out->sni = yamlValue(raw, QStringLiteral("sni"));
    out->kind = yamlValue(raw, QStringLiteral("kind"));
    if (out->type.isEmpty()) {
        out->type = QStringLiteral("auto");
    }
    if (out->url.isEmpty() || (out->token.isEmpty() && out->user.isEmpty())) {
        *err = QStringLiteral("YAML needs token and url");
        return false;
    }
    return true;
}

// loft-front flags for the fallback mode: SOCKS5 on 127.0.0.1:1080, and an
// instance file so instance_id (how the node recognises this install after
// a reconnect) survives restarts.
QStringList Engine::argsFor(const Session &s) const
{
    QStringList a;
    if (!s.token.isEmpty()) {
        a << QStringLiteral("-token") << s.token;
    }
    a << QStringLiteral("-url") << s.url;
    a << QStringLiteral("-type") << (s.type.isEmpty() ? QStringLiteral("auto") : s.type);
    a << QStringLiteral("-socks") << QStringLiteral("127.0.0.1:1080");
    if (!s.path.isEmpty() && s.type != QLatin1String("sstp")
        && s.type != QLatin1String("vless") && s.type != QLatin1String("trojan")) {
        a << QStringLiteral("-path") << s.path;
    }
    if (!s.user.isEmpty()) {
        a << QStringLiteral("-user") << s.user;
    }
    if (!s.cdn.isEmpty()) {
        a << QStringLiteral("-cdn") << s.cdn;
    }
    if (!s.sni.isEmpty()) {
        a << QStringLiteral("-sni") << s.sni;
    }
    a << QStringLiteral("-instance-file") << (dataDir() + QStringLiteral("/instance"));
    return a;
}

// Setters for the QML-facing properties; they emit the change signal only
// when the value really changes.
void Engine::setLine(const QString &s)
{
    if (line_ == s) {
        return;
    }
    line_ = s;
    emit lineChanged();
}

void Engine::setUp(bool v)
{
    if (up_ == v) {
        return;
    }
    up_ = v;
    emit upChanged();
}

void Engine::setBusy(bool v)
{
    if (busy_ == v) {
        return;
    }
    busy_ = v;
    emit busyChanged();
}

// Stops the fallback loft-front: SIGTERM, then SIGKILL if it hangs.
void Engine::stopProc()
{
    if (proc_ == 0) {
        return;
    }
    QProcess *p = proc_;
    proc_ = 0;
    p->disconnect(this);
    if (p->state() != QProcess::NotRunning) {
        p->terminate();
        if (!p->waitForFinished(800)) {
            p->kill();
            p->waitForFinished(400);
        }
    }
    p->deleteLater();
}

// loft-front prints machine-readable "STATUS key=value …" lines on stdout.
void Engine::onStdout()
{
    if (proc_ == 0) {
        return;
    }
    outRest_ += QString::fromUtf8(proc_->readAllStandardOutput());
    int nl;
    while ((nl = outRest_.indexOf(QLatin1Char('\n'))) >= 0) {
        const QString line = outRest_.left(nl).trimmed();
        outRest_ = outRest_.mid(nl + 1);
        if (line.startsWith(QLatin1String("STATUS "))) {
            handleStatus(line);
        }
    }
}

// Only the "socks5 …" line (the proxy address) from stderr is shown.
void Engine::onStderr()
{
    if (proc_ == 0) {
        return;
    }
    errRest_ += QString::fromUtf8(proc_->readAllStandardError());
    int nl;
    while ((nl = errRest_.indexOf(QLatin1Char('\n'))) >= 0) {
        const QString line = errRest_.left(nl).trimmed();
        errRest_ = errRest_.mid(nl + 1);
        if (line.startsWith(QLatin1String("socks5 "))) {
            setLine(line);
        }
    }
}

// state=up/on or any ip= means connected; other states (except down) are
// shown as they are.
void Engine::handleStatus(const QString &line)
{
    QString state;
    QString ip;
    const QStringList parts = line.split(QLatin1Char(' '));
    for (int i = 1; i < parts.size(); ++i) {
        const int eq = parts.at(i).indexOf(QLatin1Char('='));
        if (eq <= 0) {
            continue;
        }
        const QString k = parts.at(i).left(eq);
        const QString v = parts.at(i).mid(eq + 1);
        if (k == QLatin1String("state")) {
            state = v;
        } else if (k == QLatin1String("ip")) {
            ip = v;
        }
    }
    if (state == QLatin1String("up") || state == QLatin1String("on") || !ip.isEmpty()) {
        setBusy(false);
        setUp(true);
        if (!ip.isEmpty()) {
            setLine(QStringLiteral("Connected ") + ip);
        } else {
            setLine(QStringLiteral("Connected"));
        }
        return;
    }
    if (!state.isEmpty() && state != QLatin1String("down")) {
        setLine(state);
    }
}

// The fallback loft-front exited: the session is over.
void Engine::onFinished(int code)
{
    Q_UNUSED(code);
    setBusy(false);
    setUp(false);
    if (line_.startsWith(QLatin1String("Connecting"))) {
        setLine(QStringLiteral("Disconnected"));
    }
    if (proc_ != 0) {
        proc_->deleteLater();
        proc_ = 0;
    }
}
