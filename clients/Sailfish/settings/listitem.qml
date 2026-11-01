import QtQuick 2.6
import Sailfish.Silica 1.0
import Sailfish.Settings.Networking.Vpn 1.0

// The files in settings/ plug loft into the system Settings > VPN pages, so
// a "Loft" row can be created and edited like an OpenVPN one. ConnMan then
// starts it through the loft plugin (connman/loft-plugin.c).

// The "Loft" entry in the list of VPN types. Import of config files is off.
VpnTypeItem {
    name: "Loft"
    description: "loft overlay"
    canImport: false
    vpnType: "loft"
}
