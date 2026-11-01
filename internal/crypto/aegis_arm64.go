//go:build arm64 && !purego

package crypto

func init() {
	if useAESLAsm {
		aegisEncBlocksAsm = aegisEncNI
		aegisDecBlocksAsm = aegisDecNI
	}
}

//go:noescape
func aegisEncNI(s *blk, in, out *byte, n uint64)

//go:noescape
func aegisDecNI(s *blk, in, out *byte, n uint64)
