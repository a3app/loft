// Package keenetic is the Entware client for KeeneticOS 5.
//
// One process (loft-ndms) serves the LAN page and starts a child per
// enabled connection. Each child is the same binary with -session. It opens
// a TUN named opkgtunN, does not install a default route, and asks ndmc to
// publish OpkgTunN. Keenetic priorities and DNS routes choose the path.
// Front addresses are pinned on the ISP interface so the session does not
// hairpin into its own tunnel.
package ndms
