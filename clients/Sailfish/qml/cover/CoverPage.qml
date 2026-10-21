import QtQuick 2.6
import Sailfish.Silica 1.0

CoverBackground {
    Column {
        anchors.centerIn: parent
        spacing: Theme.paddingMedium
        width: parent.width - 2 * Theme.paddingLarge

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

    CoverActionList {
        CoverAction {
            iconSource: engine.up
                        ? "image://theme/icon-cover-cancel"
                        : "image://theme/icon-cover-play"
            onTriggered: engine.up ? engine.disconnectSession() : engine.connectSession()
        }
    }
}
