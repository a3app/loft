//go:build arm64 && !purego

#include "textflag.h"

// AESL(x) = AESMC(AESE(x, 0)) = MixColumns(ShiftRows(SubBytes(x))).
// func aeslAsm(dst, src *blk)
TEXT ·aeslAsm(SB), NOSPLIT, $0-16
	MOVD src+8(FP), R1
	VLD1 (R1), [V0.B16]
	VEOR V1.B16, V1.B16, V1.B16
	AESE V1.B16, V0.B16
	AESMC V0.B16, V0.B16
	MOVD dst+0(FP), R0
	VST1 [V0.B16], (R0)
	RET
