//go:build !darwin && !linux

package protect

func setBoundIf(int, int, bool) error { return nil }
