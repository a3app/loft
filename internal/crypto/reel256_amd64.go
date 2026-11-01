//go:build amd64 && !purego && (reel256 || (!ios && !android && !darwin))

package crypto

func init() {
	if useAESLAsm {
		reelEncBlocksAsm = reelEncNI
		reelDecBlocksAsm = reelDecNI
	}
}

//go:noescape
func reelEncNI(s *blk, off *int, in, out *byte, n uint64)

//go:noescape
func reelDecNI(s *blk, off *int, in, out *byte, n uint64)
