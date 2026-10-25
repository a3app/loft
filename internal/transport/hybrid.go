package transport

// X25519MLKEM768 (RFC 10024, PQ/T hybrid key agreement for TLS 1.3, group 0x11EC) for the mask handshake.
//
// Chrome and current TLS servers select this group when the ClientHello
// offers it. The shared secret is ML-KEM-768 || X25519 (64 bytes) and is the
// TLS 1.3 ECDHE input. sid v3 stays on the standalone X25519 share: the
// hybrid half is a different key. A ClientHello with only X25519 is answered
// with X25519, so an older peer still completes.

import (
	"crypto/mlkem"
	"crypto/rand"
	"errors"

	"golang.org/x/crypto/curve25519"
)

// x25519MLKEM768 is TLS supported group 4588 (RFC 10024).
const x25519MLKEM768 uint16 = 0x11ec

// kexName is the ServerHello group for drive and bench lines.
func kexName(group uint16) string {
	switch group {
	case x25519MLKEM768:
		return "X25519MLKEM768"
	case x25519Group:
		return "X25519"
	default:
		return "other"
	}
}

// clientKX is what the mask client needs to finish the key schedule.
type clientKX struct {
	raw     []byte
	xPriv   []byte // standalone X25519, also the sid v3 scalar when present
	hybridX []byte // X25519 scalar inside the hybrid share; may differ
	mlkem   *mlkem.DecapsulationKey768
}

// hybridServerShare is the ServerHello key_exchange length for X25519MLKEM768.
func hybridServerShare() int { return mlkem.CiphertextSize768 + 32 }

// hybridClientShare is the ClientHello key_exchange length for X25519MLKEM768.
func hybridClientShare() int { return mlkem.EncapsulationKeySize768 + 32 }

// maskKEX builds the server share and the TLS 1.3 ECDHE secret.
// Hybrid is selected only when the ClientHello carried a well-formed share.
func maskKEX(hello clientHello) (shared, serverShare []byte, group uint16, err error) {
	eph := make([]byte, 32)
	if _, err = rand.Read(eph); err != nil {
		return nil, nil, 0, err
	}
	ephPub, err := curve25519.X25519(eph, curve25519.Basepoint)
	if err != nil {
		return nil, nil, 0, err
	}
	if len(hello.hybridEK) == mlkem.EncapsulationKeySize768 && len(hello.hybridX) == 32 {
		ek, err := mlkem.NewEncapsulationKey768(hello.hybridEK)
		if err != nil {
			return nil, nil, 0, err
		}
		mlkemSS, ct := ek.Encapsulate()
		xSS, err := curve25519.X25519(eph, hello.hybridX)
		if err != nil {
			return nil, nil, 0, err
		}
		return append(mlkemSS, xSS...), append(ct, ephPub...), x25519MLKEM768, nil
	}
	if len(hello.KeyShare) != 32 {
		return nil, nil, 0, errors.New("mask: no x25519 share")
	}
	xSS, err := curve25519.X25519(eph, hello.KeyShare)
	if err != nil {
		return nil, nil, 0, err
	}
	return xSS, ephPub, x25519Group, nil
}

// clientShared is the client's TLS 1.3 ECDHE secret for the server share.
func clientShared(group uint16, peer []byte, kx clientKX) ([]byte, error) {
	if group == x25519MLKEM768 {
		if kx.mlkem == nil || len(peer) != hybridServerShare() {
			return nil, errors.New("mask: hybrid")
		}
		mlkemSS, err := kx.mlkem.Decapsulate(peer[:mlkem.CiphertextSize768])
		if err != nil {
			return nil, err
		}
		xpriv := kx.hybridX
		if len(xpriv) != 32 {
			xpriv = kx.xPriv
		}
		if len(xpriv) != 32 {
			return nil, errors.New("mask: hybrid x25519")
		}
		xSS, err := curve25519.X25519(xpriv, peer[mlkem.CiphertextSize768:])
		if err != nil {
			return nil, err
		}
		return append(mlkemSS, xSS...), nil
	}
	if group != x25519Group || len(peer) != 32 || len(kx.xPriv) != 32 {
		return nil, errors.New("mask: x25519")
	}
	return curve25519.X25519(kx.xPriv, peer)
}
