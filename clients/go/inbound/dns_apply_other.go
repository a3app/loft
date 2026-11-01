//go:build !darwin && !linux

package inbound

// dns_apply_other.go is a no-op DNS apply on OSes without TUN DNS support.

// applyDNS does nothing off Darwin and Linux.
func applyDNS(string, []string) func() { return func() {} }
