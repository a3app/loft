#ifndef LOFT_SAILFISH_ENGINE_H
#define LOFT_SAILFISH_ENGINE_H

#include <QObject>
#include <QProcess>
#include <QString>
#include <QStringList>

#ifdef HAVE_QT_DBUS
#include <QDBusVariant>
#endif

/** Thin host: paste YAML or a share URI, Connect via ConnMan or loft-front. */
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
    QString outRest_;
    QString errRest_;
    QString connmanPath_;
};

#endif
