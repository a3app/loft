//go:build amd64 && !purego

package crypto

import "golang.org/x/sys/cpu"

var useAESLAsm = cpu.X86.HasAES

func aesl(x blk) blk {
	if useAESLAsm {
		var y blk
		aeslAsm(&y, &x)
		return y
	}
	return aeslSoft(x)
}

//go:noescape
func aeslAsm(dst, src *blk)
