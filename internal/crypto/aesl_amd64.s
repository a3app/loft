//go:build amd64 && !purego

#include "textflag.h"

// AESL(x) = AESENC(x, 0) = MixColumns(ShiftRows(SubBytes(x))).
// func aeslAsm(dst, src *blk)
TEXT ·aeslAsm(SB), NOSPLIT, $0-16
	MOVQ src+8(FP), AX
	MOVUPS (AX), X0
	PXOR X1, X1
	AESENC X1, X0
	MOVQ dst+0(FP), AX
	MOVUPS X0, (AX)
	RET
