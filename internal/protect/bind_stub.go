//go:build !darwin && !linux && !windows

package protect

// setBoundIf is a no-op where the OS has no per-socket interface pin.
func setBoundIf(int, int, bool) error { return nil }
