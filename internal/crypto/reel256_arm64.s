//go:build arm64 && !purego && (reel256 || (!ios && !android && !darwin))

#include "textflag.h"

#define SLOT(i, Rp) \
	MOVD R10, R2 \
	ADD $i, R2 \
	AND $15, R2 \
	LSL $4, R2 \
	MOVD R8, Rp \
	ADD R2, Rp

#define AESL_XOR(v, k) \
	AESE V31.B16, v.B16 \
	AESMC v.B16, v.B16 \
	VEOR k.B16, v.B16, v.B16

// func reelEncNI(s *blk, off *int, in, out *byte, n uint64)
TEXT ·reelEncNI(SB), NOSPLIT, $0-40
	MOVD s+0(FP), R8
	MOVD off+8(FP), R9
	MOVD in+16(FP), R12
	MOVD out+24(FP), R13
	MOVD n+32(FP), R11
	MOVD (R9), R10
	VEOR V31.B16, V31.B16, V31.B16
	CBZ R11, encDone

encLoop:
	VLD1 (R12), [V2.B16]

	SLOT(0, R3)
	VLD1 (R3), [V0.B16]
	SLOT(3, R4)
	VLD1 (R4), [V1.B16]
	VEOR V1.B16, V0.B16, V0.B16
	AESL_XOR(V0, V2)

	SLOT(8, R5)
	VLD1 (R5), [V1.B16]
	VEOR V1.B16, V0.B16, V3.B16
	VST1 [V3.B16], (R13)

	SLOT(12, R4)
	VLD1 (R4), [V4.B16]
	SLOT(9, R6)
	VLD1 (R6), [V5.B16]
	VEOR V5.B16, V4.B16, V4.B16
	AESL_XOR(V4, V0)

	VEOR V2.B16, V1.B16, V1.B16
	VST1 [V1.B16], (R5)
	VST1 [V4.B16], (R3)

	ADD $16, R12
	ADD $16, R13
	ADD $1, R10
	AND $15, R10
	SUB $1, R11
	CBNZ R11, encLoop

encDone:
	MOVD R10, (R9)
	RET

// func reelDecNI(s *blk, off *int, in, out *byte, n uint64)
TEXT ·reelDecNI(SB), NOSPLIT, $0-40
	MOVD s+0(FP), R8
	MOVD off+8(FP), R9
	MOVD in+16(FP), R12
	MOVD out+24(FP), R13
	MOVD n+32(FP), R11
	MOVD (R9), R10
	VEOR V31.B16, V31.B16, V31.B16
	CBZ R11, decDone

decLoop:
	VLD1 (R12), [V2.B16]

	SLOT(8, R5)
	VLD1 (R5), [V1.B16]
	VEOR V1.B16, V2.B16, V0.B16

	SLOT(0, R3)
	VLD1 (R3), [V3.B16]
	SLOT(3, R4)
	VLD1 (R4), [V4.B16]
	VEOR V4.B16, V3.B16, V3.B16
	AESL_XOR(V3, V0)
	VST1 [V3.B16], (R13)

	SLOT(12, R4)
	VLD1 (R4), [V4.B16]
	SLOT(9, R6)
	VLD1 (R6), [V5.B16]
	VEOR V5.B16, V4.B16, V4.B16
	AESL_XOR(V4, V0)

	VEOR V3.B16, V1.B16, V1.B16
	VST1 [V1.B16], (R5)
	VST1 [V4.B16], (R3)

	ADD $16, R12
	ADD $16, R13
	ADD $1, R10
	AND $15, R10
	SUB $1, R11
	CBNZ R11, decLoop

decDone:
	MOVD R10, (R9)
	RET
