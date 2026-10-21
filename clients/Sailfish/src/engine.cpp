#include "engine.h"

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

Engine::~Engine()
{
    stopConnMan();
    stopProc();
}

void Engine::setYaml(const QString &v)
{
    if (yaml_ == v) {
        return;
    }
    yaml_ = v;
    emit yamlChanged();
}

void Engine::connectSession()
{
    if (busy_ || up_) {
        return;
    }
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
    persistYaml(body);
    stopProc();
    stopConnMan();
    setBusy(true);
    setLine(QStringLiteral("Connecting…"));
    if (connectConnMan(s)) {
        return;
    }
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

QString Engine::dataDir() const
{
    const QString d = QStandardPaths::writableLocation(QStandardPaths::AppDataLocation);
    QDir().mkpath(d);
    return d;
}

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

bool Engine::parseBody(const QString &body, Session *out, QString *err) const
{
    if (!body.contains(QLatin1Char('\n')) && body.contains(QStringLiteral("://"))) {
        return parseURI(body.trimmed(), out, err);
    }
    return parseYAML(body, out, err);
}

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
