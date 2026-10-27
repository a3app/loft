// engine.h: the Engine class, the C++ side of the Sailfish / Aurora app.
// main.cpp exposes one Engine to QML (qml/pages/MainPage.qml binds to its
// properties). The implementation is in engine.cpp.
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
    // Construction loads the saved client.yaml; destruction ends the session.
    explicit Engine(QObject *parent = 0);
    ~Engine();

    // Property accessors. yaml is the pasted text, line the one-line status
    // shown under it, up "session is live", busy "connect in progress".
    QString yaml() const { return yaml_; }
    void setYaml(const QString &v);
    QString line() const { return line_; }
    bool up() const { return up_; }
    bool busy() const { return busy_; }

    // Called from the Connect / Disconnect buttons in QML.
    Q_INVOKABLE void connectSession();
    Q_INVOKABLE void disconnectSession();

signals:
    // Change notifications for the properties above.
    void yamlChanged();
    void lineChanged();
    void upChanged();
    void busyChanged();

private slots:
    // Output and exit of the fallback loft-front process, and state
    // changes of the ConnMan VPN row.
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

    // Helpers: find loft-front and the data dir, parse the pasted text into
    // a Session, build loft-front flags, drive the ConnMan row, update the
    // properties, and save client.yaml. See engine.cpp for each.
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

    // Backing fields of the properties.
    QString yaml_;
    QString line_;
    bool up_;
    bool busy_;
    /** The fallback loft-front child process; 0 when not running. */
    QProcess *proc_;
    /** Unfinished last line of loft-front's stdout / stderr. */
    QString outRest_;
    QString errRest_;
    /** D-Bus path of the ConnMan VPN row in use; empty when not using ConnMan. */
    QString connmanPath_;
};

#endif
