import QtQuick 2.6
import Sailfish.Silica 1.0
import Sailfish.Settings.Networking.Vpn 1.0

// Create / edit dialog for a Loft VPN row in Settings. Each field is a
// Loft.* property of the row; the plugin passes them to the helper as flags.
VpnPlatformEditDialog {
    vpnType: "loft"
    newTitle: "New Loft"
    editTitle: "Edit Loft"

    // Host part of the front URL (no scheme, port or path). It is saved as
    // the row's Host, which ConnMan shows and uses as the server address.
    function hostFromUrl(raw) {
        var s = raw ? raw.toString() : ""
        var i = s.indexOf("://")
        if (i >= 0)
            s = s.substring(i + 3)
        if (s.charAt(0) === "[") {
            var j = s.indexOf("]")
            return j > 0 ? s.substring(1, j) : s
        }
        var cut = s.search(/[/?:#]/)
        if (cut >= 0)
            s = s.substring(0, cut)
        var colon = s.lastIndexOf(":")
        if (colon > 0)
            s = s.substring(0, colon)
        return s
    }

    Column {
        width: parent.width

        TextField {
            id: urlField
            width: parent.width
            label: "Front URL"
            placeholderText: "https://cdn.example.com"
            inputMethodHints: Qt.ImhNoPredictiveText | Qt.ImhUrlCharactersOnly
        }
        PasswordField {
            id: tokenField
            width: parent.width
            label: "Member key"
            placeholderText: "lt_…"
        }
        TextField {
            id: typeField
            width: parent.width
            label: "Type"
            placeholderText: "auto"
            text: "auto"
            inputMethodHints: Qt.ImhNoPredictiveText | Qt.ImhLatinOnly
        }
        TextField {
            id: pathField
            width: parent.width
            label: "Path"
            placeholderText: "/static/js"
            inputMethodHints: Qt.ImhNoPredictiveText
        }
        TextField {
            id: cdnField
            width: parent.width
            label: "CDN"
            placeholderText: "yandex or cloudflare"
            inputMethodHints: Qt.ImhNoPredictiveText | Qt.ImhLatinOnly
        }
    }

    Component.onCompleted: {
        init()
        var url = getProviderProperty("Loft.URL")
        if (!url || url.length === 0)
            url = getProviderProperty("Host")
        urlField.text = url
        tokenField.text = getProviderProperty("Loft.Token")
        var t = getProviderProperty("Loft.Type")
        typeField.text = t && t.length ? t : "auto"
        pathField.text = getProviderProperty("Loft.Path")
        cdnField.text = getProviderProperty("Loft.CDN")
    }

    onAccepted: {
        var url = urlField.text
        var host = hostFromUrl(url)
        updateProvider("Loft.URL", url)
        updateProvider("Loft.Token", tokenField.text)
        updateProvider("Loft.Type", typeField.text)
        updateProvider("Loft.Path", pathField.text)
        updateProvider("Loft.CDN", cdnField.text)
        if (host.length)
            updateProvider("Host", host)
        saveConnection()
    }
}
