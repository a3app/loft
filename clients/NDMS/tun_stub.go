//go:build !linux

package ndms

// ensureTUN is a no-op off Linux. The Keenetic client is built for Linux;
// tests of the ndmc script run on the developer machine.
func ensureTUN(string) error { return nil }
