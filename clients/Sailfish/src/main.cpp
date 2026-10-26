#include <QGuiApplication>
#include <QQmlContext>
#include <QDir>
#include <QUrl>

#ifdef AURORA
#include <auroraapp.h>
#include <QQuickView>
#elif defined(SAILFISH)
#include <sailfishapp.h>
#include <QQuickView>
#else
#include <QQmlApplicationEngine>
#endif

#include "engine.h"

// One source, three builds: Aurora OS, Sailfish OS (Silica), or plain Qt
// for running the QML on a desktop. Each exposes Engine to QML as "engine".
int main(int argc, char *argv[])
{
#ifdef AURORA
    QScopedPointer<QGuiApplication> app(Aurora::Application::application(argc, argv));
    app->setOrganizationName(QStringLiteral("loft"));
    app->setApplicationName(QStringLiteral("aurora"));
    Engine engine;
    QScopedPointer<QQuickView> view(Aurora::Application::createView());
    view->rootContext()->setContextProperty(QStringLiteral("engine"), &engine);
    view->setSource(Aurora::Application::pathTo(QStringLiteral("qml/loft.qml")));
    view->show();
    return app->exec();
#elif defined(SAILFISH)
    QScopedPointer<QGuiApplication> app(SailfishApp::application(argc, argv));
    app->setOrganizationName(QStringLiteral("loft"));
    app->setApplicationName(QStringLiteral("loft"));
    Engine engine;
    QScopedPointer<QQuickView> view(SailfishApp::createView());
    view->rootContext()->setContextProperty(QStringLiteral("engine"), &engine);
    view->setSource(SailfishApp::pathTo(QStringLiteral("qml/loft.qml")));
    view->show();
    return app->exec();
#else
    QGuiApplication app(argc, argv);
    app.setOrganizationName(QStringLiteral("loft"));
    app.setApplicationName(QStringLiteral("loft"));
    Engine engine;
    QQmlApplicationEngine qml;
    qml.rootContext()->setContextProperty(QStringLiteral("engine"), &engine);
    const QString local = QDir(QCoreApplication::applicationDirPath())
                              .absoluteFilePath(QStringLiteral("../qml/loft.qml"));
    qml.load(QUrl::fromLocalFile(local));
    if (qml.rootObjects().isEmpty()) {
        return 1;
    }
    return app.exec();
#endif
}
