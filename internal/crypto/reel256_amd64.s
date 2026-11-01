//go:build amd64 && !purego && (reel256 || (!ios && !android && !darwin))

#include "textflag.h"

#define SLOT(i, reg) \
	MOVL R10, reg \
	ADDL $i, reg \
	ANDL $15, reg \
	SHLL $4, reg

// func reelEncNI(s *blk, off *int, in, out *byte, n uint64)
// t = AESENC(S0⊕S3, m); c = t⊕S8; S0 ← AESENC(S12⊕S9, t); S8⊕=m; off++
TEXT ·reelEncNI(SB), NOSPLIT, $0-40
	MOVQ s+0(FP), R8
	MOVQ off+8(FP), R9
	MOVQ in+16(FP), SI
	MOVQ out+24(FP), DI
	MOVQ n+32(FP), R11
	MOVQ (R9), R10
	TESTQ R11, R11
	JZ encDone

encLoop:
	MOVUPS (SI), X2

	SLOT(0, AX)
	MOVUPS (R8)(AX*1), X0
	SLOT(3, BX)
	MOVUPS (R8)(BX*1), X1
	PXOR X1, X0
	AESENC X2, X0

	SLOT(8, DX)
	MOVUPS (R8)(DX*1), X1
	MOVUPS X0, X3
	PXOR X1, X3
	MOVUPS X3, (DI)

	SLOT(12, BX)
	MOVUPS (R8)(BX*1), X4
	SLOT(9, R12)
	MOVUPS (R8)(R12*1), X5
	PXOR X5, X4
	AESENC X0, X4

	PXOR X2, X1
	MOVUPS X1, (R8)(DX*1)
	MOVUPS X4, (R8)(AX*1)

	ADDQ $16, SI
	ADDQ $16, DI
	ADDL $1, R10
	ANDL $15, R10
	SUBQ $1, R11
	JNZ encLoop

encDone:
	MOVQ R10, (R9)
	RET

// func reelDecNI(s *blk, off *int, in, out *byte, n uint64)
// t = c⊕S8; m = AESENC(S0⊕S3, t); S0 ← AESENC(S12⊕S9, t); S8⊕=m; off++
TEXT ·reelDecNI(SB), NOSPLIT, $0-40
	MOVQ s+0(FP), R8
	MOVQ off+8(FP), R9
	MOVQ in+16(FP), SI
	MOVQ out+24(FP), DI
	MOVQ n+32(FP), R11
	MOVQ (R9), R10
	TESTQ R11, R11
	JZ decDone

decLoop:
	MOVUPS (SI), X2

	SLOT(8, DX)
	MOVUPS (R8)(DX*1), X1
	MOVUPS X2, X0
	PXOR X1, X0

	SLOT(0, AX)
	MOVUPS (R8)(AX*1), X3
	SLOT(3, BX)
	MOVUPS (R8)(BX*1), X4
	PXOR X4, X3
	AESENC X0, X3
	MOVUPS X3, (DI)

	SLOT(12, BX)
	MOVUPS (R8)(BX*1), X4
	SLOT(9, R12)
	MOVUPS (R8)(R12*1), X5
	PXOR X5, X4
	AESENC X0, X4

	PXOR X3, X1
	MOVUPS X1, (R8)(DX*1)
	MOVUPS X4, (R8)(AX*1)

	ADDQ $16, SI
	ADDQ $16, DI
	ADDL $1, R10
	ANDL $15, R10
	SUBQ $1, R11
	JNZ decLoop

decDone:
	MOVQ R10, (R9)
	RET
