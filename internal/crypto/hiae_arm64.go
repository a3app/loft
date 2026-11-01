//go:build arm64 && !purego && (hiae || (!ios && !android && !darwin))

package crypto

func init() {
	if useAESLAsm {
		hiaeEncBlocksAsm = hiaeEncNI
		hiaeDecBlocksAsm = hiaeDecNI
	}
}

//go:noescape
func hiaeEncNI(s *blk, off *int, in, out *byte, n uint64)

//go:noescape
func hiaeDecNI(s *blk, off *int, in, out *byte, n uint64)
