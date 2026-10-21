// Package mobile is the shared Go session for iOS Packet Tunnel and Android
// VpnService. No GUI. No loft-front. Protocol 1.0 is unchanged.
//
// Bind with gomobile (or a C ABI) from this package. The OS adapter pumps IP
// packets and installs routes/DNS from Snapshot.
package mobile
