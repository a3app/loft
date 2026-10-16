package transport

// Dest-shaped ECDSA leaf and CertificateVerify for mask TLS.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"time"

	"golang.org/x/crypto/cryptobyte"
	"golang.org/x/crypto/hkdf"
)

const sigECDSA_P256_SHA256 uint16 = 0x0403

// certSerial is 16 bytes bound to the auth ECDH and client_random.
func certSerial(authShared, clientRandom []byte) []byte {
	out := make([]byte, 16)
	if _, err := io.ReadFull(hkdf.New(sha256.New, authShared, clientRandom, []byte("loft-cert-serial")), out); err != nil {
		m := hmac.New(sha256.New, authShared)
		m.Write(clientRandom)
		sum := m.Sum(nil)
		copy(out, sum[:16])
	}
	if out[0]&0x80 != 0 {
		out[0] &= 0x7f // keep SerialNumber positive in ASN.1
	}
	if allZero(out) {
		out[15] = 1
	}
	return out
}

func allZero(b []byte) bool {
	var v byte
	for _, x := range b {
		v |= x
	}
	return v == 0
}

// serialEqual reports whether cert.SerialNumber equals the 16-byte bind.
func serialEqual(cert *x509.Certificate, want []byte) bool {
	if cert == nil || len(want) == 0 {
		return false
	}
	got := cert.SerialNumber.Bytes()
	padded := make([]byte, 16)
	if len(got) > 16 {
		return false
	}
	copy(padded[16-len(got):], got)
	return hmac.Equal(padded, want)
}

// mintDestLeaf copies dest identity onto a fresh ECDSA P-256 leaf so
// CertificateVerify is ecdsa_secp256r1_sha256, the scheme dests actually use.
func mintDestLeaf(originDER, serial []byte, sni string) (leafDER []byte, key *ecdsa.PrivateKey, err error) {
	key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	sn := new(big.Int).SetBytes(serial)
	if sn.Sign() <= 0 {
		sn = big.NewInt(1)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          sn,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		DNSNames:              []string{sni},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
	}
	if orig, perr := x509.ParseCertificate(originDER); perr == nil && orig != nil {
		tmpl.Subject = orig.Subject
		if len(orig.DNSNames) > 0 {
			tmpl.DNSNames = append([]string(nil), orig.DNSNames...)
		}
		if !orig.NotBefore.IsZero() {
			tmpl.NotBefore = orig.NotBefore
		}
		if orig.NotAfter.After(tmpl.NotBefore) {
			tmpl.NotAfter = orig.NotAfter
		}
		if len(orig.ExtKeyUsage) > 0 {
			tmpl.ExtKeyUsage = append([]x509.ExtKeyUsage(nil), orig.ExtKeyUsage...)
		}
		if orig.KeyUsage != 0 {
			tmpl.KeyUsage = orig.KeyUsage
		}
		tmpl.IPAddresses = orig.IPAddresses
		tmpl.EmailAddresses = orig.EmailAddresses
		tmpl.URIs = orig.URIs
	}
	if tmpl.Subject.CommonName == "" && sni != "" {
		tmpl.Subject.CommonName = sni
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	return der, key, nil
}

// maskChain is a dest-shaped leaf plus the origin intermediates (size match).
func maskChain(origin [][]byte, serial []byte, sni string) (certs [][]byte, key *ecdsa.PrivateKey, err error) {
	var leafIn []byte
	if len(origin) > 0 {
		leafIn = origin[0]
	}
	leaf, key, err := mintDestLeaf(leafIn, serial, sni)
	if err != nil {
		return nil, nil, err
	}
	certs = [][]byte{leaf}
	if len(origin) > 1 {
		certs = append(certs, origin[1:]...)
	}
	return certs, key, nil
}

// marshalCertVerifyScheme builds CertificateVerify with the given TLS scheme.
func marshalCertVerifyScheme(scheme uint16, sig []byte) []byte {
	var b cryptobyte.Builder
	b.AddUint8(hsCertificateVerify)
	b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint16(scheme)
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(sig) })
	})
	msg, _ := b.Bytes()
	return msg
}

// signMaskCV is ECDSA-P256-SHA256 over the TLS 1.3 CertificateVerify input.
func signMaskCV(priv *ecdsa.PrivateKey, transcript []byte) ([]byte, error) {
	sum := sha256.Sum256(certVerifySigned(transcript, true))
	return ecdsa.SignASN1(rand.Reader, priv, sum[:])
}

// verifyMaskCV checks CertificateVerify against the leaf public key.
func verifyMaskCV(msg, transcript []byte, leaf *x509.Certificate) error {
	if len(msg) < 8 {
		return errors.New("mask: short cert verify")
	}
	body := msg[4:]
	scheme := binary.BigEndian.Uint16(body[0:2])
	ln := int(binary.BigEndian.Uint16(body[2:4]))
	if 4+ln > len(body) {
		return errors.New("mask: short sig")
	}
	sig := body[4 : 4+ln]
	toSign := certVerifySigned(transcript, true)
	switch scheme {
	case sigECDSA_P256_SHA256:
		if leaf == nil {
			return errors.New("mask: no leaf")
		}
		pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("mask: leaf not ecdsa")
		}
		sum := sha256.Sum256(toSign)
		if !ecdsa.VerifyASN1(pub, sum[:], sig) {
			return errors.New("mask: bad certificate verify")
		}
		return nil
	default:
		return errors.New("mask: sig scheme")
	}
}

// leafFromCertMsg parses the first certificate in a TLS 1.3 Certificate message.
func leafFromCertMsg(msg []byte) (*x509.Certificate, error) {
	if len(msg) < 8 || msg[0] != hsCertificate {
		return nil, errors.New("mask: not certificate")
	}
	n := int(msg[1])<<16 | int(msg[2])<<8 | int(msg[3])
	if 4+n > len(msg) {
		n = len(msg) - 4
	}
	body := msg[4 : 4+n]
	if len(body) < 4 {
		return nil, errors.New("mask: short cert")
	}
	ctxLen := int(body[0])
	off := 1 + ctxLen
	if off+3 > len(body) {
		return nil, errors.New("mask: short list")
	}
	listLen := int(body[off])<<16 | int(body[off+1])<<8 | int(body[off+2])
	off += 3
	end := off + listLen
	if end > len(body) {
		end = len(body)
	}
	if off+3 > end {
		return nil, errors.New("mask: empty list")
	}
	certLen := int(body[off])<<16 | int(body[off+1])<<8 | int(body[off+2])
	off += 3
	if off+certLen > end {
		return nil, errors.New("mask: short leaf")
	}
	return x509.ParseCertificate(body[off : off+certLen])
}
