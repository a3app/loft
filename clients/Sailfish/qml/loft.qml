import QtQuick 2.6
import Sailfish.Silica 1.0
import "pages"

// App window. `engine` (the C++ Engine) is set as a context property in
// main.cpp; the pages bind to its yaml, line, up and busy.
ApplicationWindow {
    id: win
    objectName: "loft"
    initialPage: Component { MainPage { } }
    cover: Qt.resolvedUrl("cover/CoverPage.qml")
    allowedOrientations: defaultAllowedOrientations
}
