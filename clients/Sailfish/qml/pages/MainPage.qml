import QtQuick 2.6
import Sailfish.Silica 1.0

Page {
    id: page

    SilicaFlickable {
        anchors.fill: parent
        contentHeight: col.height + Theme.paddingLarge

        Column {
            id: col
            width: parent.width

            PageHeader { title: "loft" }

            Label {
                x: Theme.horizontalPageMargin
                width: parent.width - 2 * Theme.horizontalPageMargin
                wrapMode: Text.Wrap
                color: Theme.secondaryColor
                font.pixelSize: Theme.fontSizeSmall
                text: "From the street, ordinary HTTPS."
            }

            TextArea {
                id: body
                width: parent.width
                label: "YAML or loft:// / sstp:// / vless:// / trojan://"
                placeholderText: "kind: loft\ntoken: lt_…\nurl: https://cdn.example.com"
                text: engine.yaml
                font.family: "Sail Sans"
                onTextChanged: engine.yaml = text
            }

            Row {
                x: Theme.horizontalPageMargin
                spacing: Theme.paddingMedium

                Button {
                    text: engine.busy ? "Connecting…" : "Connect"
                    enabled: !engine.busy && !engine.up
                    onClicked: engine.connectSession()
                }
                Button {
                    text: "Disconnect"
                    enabled: engine.up || engine.busy
                    onClicked: engine.disconnectSession()
                }
            }

            Label {
                x: Theme.horizontalPageMargin
                width: parent.width - 2 * Theme.horizontalPageMargin
                wrapMode: Text.Wrap
                color: Theme.secondaryColor
                font.pixelSize: Theme.fontSizeExtraSmall
                text: engine.line
            }
        }
    }
}
