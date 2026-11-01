//go:build (!amd64 && !arm64) || purego

package crypto

func aesl(x blk) blk { return aeslSoft(x) }
