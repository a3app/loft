import QtQuick 2.6
import Sailfish.Silica 1.0

// Advanced section of the Loft row editor: SSTP user name, a TLS SNI
// override, and a config path (passed on, but the helper ignores it so far).
Column {
    width: parent.width

    function setProperties() {
        userField.text = getProperty("Loft.User")
        sniField.text = getProperty("Loft.SNI")
        configField.text = getProperty("Loft.Config")
    }

    function updateProperties() {
        updateProvider("Loft.User", userField.text)
        updateProvider("Loft.SNI", sniField.text)
        updateProvider("Loft.Config", configField.text)
    }

    TextField {
        id: userField
        width: parent.width
        label: "User"
        placeholderText: "SSTP username"
        inputMethodHints: Qt.ImhNoPredictiveText
    }
    TextField {
        id: sniField
        width: parent.width
        label: "SNI"
        placeholderText: "TLS ServerName"
        inputMethodHints: Qt.ImhNoPredictiveText | Qt.ImhUrlCharactersOnly
    }
    TextField {
        id: configField
        width: parent.width
        label: "Config"
        placeholderText: "optional YAML path"
        inputMethodHints: Qt.ImhNoPredictiveText
    }
}
