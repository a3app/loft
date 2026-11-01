import QtQuick 2.6
import Sailfish.Silica 1.0

// The app's cover on the Sailfish home screen: a lamp that lights when
// connected, the status line, and a play/stop action.
CoverBackground {
    Column {
        anchors.centerIn: parent
        spacing: Theme.paddingMedium
        width: parent.width - 2 * Theme.paddingLarge

            // Status lamp: amber when the session is up.
        Rectangle {
            width: Theme.iconSizeSmall
            height: Theme.iconSizeSmall
            radius: 3
            color: engine.up ? "#E8B84A" : Theme.secondaryColor
            anchors.horizontalCenter: parent.horizontalCenter
        }
        Label {
            width: parent.width
            horizontalAlignment: Text.AlignHCenter
            text: "Loft"
            font.pixelSize: Theme.fontSizeLarge
        }
        Label {
            width: parent.width
            horizontalAlignment: Text.AlignHCenter
            wrapMode: Text.Wrap
            color: Theme.secondaryColor
            font.pixelSize: Theme.fontSizeExtraSmall
            text: engine.line
        }
    }

        // One button on the cover: connect when idle, disconnect when up.
    CoverActionList {
        CoverAction {
            iconSource: engine.up
                        ? "image://theme/icon-cover-cancel"
                        : "image://theme/icon-cover-play"
            onTriggered: engine.up ? engine.disconnectSession() : engine.connectSession()
        }
    }
}
