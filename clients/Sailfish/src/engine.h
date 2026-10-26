#ifndef LOFT_SAILFISH_ENGINE_H
#define LOFT_SAILFISH_ENGINE_H

#include <QObject>
#include <QProcess>
#include <QString>
#include <QStringList>

#ifdef HAVE_QT_DBUS
#include <QDBusVariant>
#endif

/** Thin host: paste YAML or a share URI, Connect via ConnMan or loft-front.
 *
 * The app does not speak the protocol. Connect first asks ConnMan (D-Bus
 * net.connman.vpn) to start a VPN row of type "loft", which the loft plugin
 * serves with a real tun. If ConnMan or the plugin is missing, it starts
 * loft-front itself with only a SOCKS5 proxy on 127.0.0.1:1080 (other apps
 * are not routed through it). up/busy/line feed the QML page. */
class Engine : public QObject {
    Q_OBJECT
    Q_PROPERTY(QString yaml READ yaml WRITE setYaml NOTIFY yamlChanged)
    Q_PROPERTY(QString line READ line NOTIFY lineChanged)
    Q_PROPERTY(bool up READ up NOTIFY upChanged)
    Q_PROPERTY(bool busy READ busy NOTIFY busyChanged)

public:
    explicit Engine(QObject *parent = 0);
    ~Engine();

    QString yaml() const { return yaml_; }
    void setYaml(const QString &v);
    QString line() const { return line_; }
    bool up() const { return up_; }
    bool busy() const { return busy_; }

    Q_INVOKABLE void connectSession();
    Q_INVOKABLE void disconnectSession();

signals:
    void yamlChanged();
    void lineChanged();
    void upChanged();
    void busyChanged();

private slots:
    void onStdout();
    void onStderr();
    void onFinished(int code);
#ifdef HAVE_QT_DBUS
    void onConnManProperty(const QString &name, const QDBusVariant &value);
#endif

private:
    /** The few fields the app reads from the pasted text. token is the
     * member key (or the VLESS/Trojan/SSTP secret), type the front or "auto",
     * kind the protocol (loft, sstp, vless, trojan). */
    struct Session {
        QString token;
        QString url;
        QString type;
        QString path;
        QString user;
        QString cdn;
        QString sni;
        QString kind;
    };

    QString binaryPath() const;
    QString dataDir() const;
    bool parseBody(const QString &body, Session *out, QString *err) const;
    bool parseURI(const QString &raw, Session *out, QString *err) const;
    bool parseYAML(const QString &raw, Session *out, QString *err) const;
    QStringList argsFor(const Session &s) const;
    QString hostOf(const Session &s) const;
    bool connectConnMan(const Session &s);
    void stopConnMan();
    void setLine(const QString &s);
    void setUp(bool v);
    void setBusy(bool v);
    void stopProc();
    void handleStatus(const QString &line);
    void persistYaml(const QString &body) const;

    QString yaml_;
    QString line_;
    bool up_;
    bool busy_;
    QProcess *proc_;
    /** Unfinished last line of loft-front's stdout / stderr. */
    QString outRest_;
    QString errRest_;
    /** D-Bus path of the ConnMan VPN row in use; empty when not using ConnMan. */
    QString connmanPath_;
};

#endif
