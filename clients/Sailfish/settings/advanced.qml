import QtQuick 2.6
import Sailfish.Silica 1.0

// Advanced section of the Loft row editor: SSTP user name, a TLS SNI
// override, and a config path (passed on, but the helper ignores it so far).
Column {
    width: parent.width

    // Fills the fields from the row's saved Loft.* properties when the
    // editor opens. getProperty comes from the Settings VPN page that hosts
    // this section.
    function setProperties() {
        userField.text = getProperty("Loft.User")
        sniField.text = getProperty("Loft.SNI")
        configField.text = getProperty("Loft.Config")
    }

    // Writes the fields back to the row when the editor is accepted.
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
