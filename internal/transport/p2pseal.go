package transport

// Sealed direct P2P connections.
//
// A direct connection between two members crosses the open internet without
// TLS, so everything on it is encrypted and nothing on it is a fixed
// constant an observer could match:
//
//  1. The initiator sends a fresh X25519 public key, then a sealed header
//     (its overlay IP, the port it wants, the time, and a proof that it
//     holds its member P2P key), then random padding. The key for the
//     header comes from that fresh key and the responder's P2P key from the
//     Status hints, so only the responder can read it.
//  2. The responder checks the header: the time is close to its own, the
//     fresh key has not been seen before (no replay), and the proof matches
//     the initiator's P2P key from its own hints. It answers with its own
//     fresh public key, a sealed padding length, and that padding.
//  3. Both sides derive one data key per direction from all three
//     shared secrets (fresh × fresh, fresh × responder, member × member),
//     so the data stays secret even if a member key leaks later.
//  4. Data goes in records: a sealed 2-byte length, then the sealed bytes.
//     ChaCha20-Poly1305 with a counter nonce per direction.
//
// The top bit of a X25519 public key is always 0, which would mark every
// first byte group; it is set at random on the wire (X25519 ignores it).

import (
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/a3app/loft/internal/wire"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	// p2pHeadLen is the header plaintext: src_ipv4, dest_port, unix time,
	// proof, data cipher asked for, padding length.
	p2pHeadLen = 4 + 2 + 8 + 16 + 1 + 1
	// p2pReplyLen is the reply plaintext: data cipher chosen, padding length.
	p2pReplyLen = 1 + 1
	p2pTag      = 16
	p2pSkew     = 60 * time.Second // header time allowed off the responder's clock
	p2pRecMax   = 16 << 10         // largest data record
)

var errP2PHello = errors.New("p2p: hello")

// p2pWirePub is k's public key with a random top bit.
func p2pWirePub(k *ecdh.PrivateKey) []byte {
	b := append([]byte(nil), k.PublicKey().Bytes()...)
	var r [1]byte
	_, _ = rand.Read(r[:])
	b[31] |= r[0] & 0x80
	return b
}

// p2pECDH is k with a public key as it came off the wire.
func p2pECDH(k *ecdh.PrivateKey, wirePub []byte) ([]byte, error) {
	pk, err := ecdh.X25519().NewPublicKey(wirePub)
	if err != nil {
		return nil, err
	}
	return k.ECDH(pk)
}

// p2pKDF is HKDF-SHA256 of secret, salted with the handshake bytes so far.
func p2pKDF(secret, salt []byte, info string, n int) []byte {
	out := make([]byte, n)
	_, _ = io.ReadFull(hkdf.New(sha256.New, secret, salt, []byte(info)), out)
	return out
}

func p2pAEAD(key []byte) cipher.AEAD {
	a, _ := chacha20poly1305.New(key)
	return a
}

// p2pProof shows the initiator holds the member P2P key behind src4.
func p2pProof(static, ePub, src4 []byte, port uint16, at uint64) []byte {
	m := hmac.New(sha256.New, static)
	m.Write([]byte("loft p2p proof"))
	m.Write(ePub)
	m.Write(src4)
	_ = binary.Write(m, binary.BigEndian, port)
	_ = binary.Write(m, binary.BigEndian, at)
	return m.Sum(nil)[:16]
}

func p2pPad() []byte {
	var n [1]byte
	_, _ = rand.Read(n[:])
	pad := make([]byte, int(n[0]))
	_, _ = rand.Read(pad)
	return pad
}

var zeroNonce [chacha20poly1305.NonceSize]byte

// p2pDial runs the initiator side on c to reach destPort on the member whose
// P2P public key is peerPub, and returns the sealed connection.
func p2pDial(c net.Conn, destPort string, peerPub []byte) (net.Conn, error) {
	my, err := EnsureP2PKey()
	if err != nil {
		return nil, err
	}
	return p2pDialAs(c, my, overlayIPv4(), wire.Port(destPort), peerPub)
}

// p2pDialAs is p2pDial for member key my at overlay address src.
func p2pDialAs(c net.Conn, my *ecdh.PrivateKey, src net.IP, port uint16, peerPub []byte) (net.Conn, error) {
	if len(src) != 4 || len(peerPub) != 32 {
		return nil, errP2PHello
	}
	static, err := p2pECDH(my, peerPub)
	if err != nil {
		return nil, err
	}
	e, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	es, err := p2pECDH(e, peerPub)
	if err != nil {
		return nil, err
	}
	ePub := p2pWirePub(e)
	msg := p2pHelloMsg(ePub, es, static, src, port, uint64(time.Now().Unix()), wire.InnerChaCha, p2pPad())

	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	defer c.SetDeadline(time.Time{})
	if _, err := c.Write(msg); err != nil {
		return nil, err
	}
	reply := make([]byte, 32+p2pReplyLen+p2pTag)
	if _, err := io.ReadFull(c, reply); err != nil {
		return nil, err
	}
	rPub := reply[:32]
	k2 := p2pKDF(es, append(append([]byte(nil), ePub...), rPub...), "loft p2p reply", chacha20poly1305.KeySize)
	n, err := p2pAEAD(k2).Open(nil, zeroNonce[:], reply[32:], rPub)
	if err != nil || n[0] != wire.InnerChaCha {
		return nil, errP2PHello
	}
	if _, err := io.CopyN(io.Discard, c, int64(n[1])); err != nil {
		return nil, err
	}
	ee, err := p2pECDH(e, rPub)
	if err != nil {
		return nil, err
	}
	send, recv := p2pDataKeys(ee, es, static, ePub, rPub)
	return newP2PConn(c, send, recv), nil
}

// p2pHelloMsg is the initiator's first message: ePub || sealed header ||
// pad. es is X25519(fresh, responder), static X25519(member, responder).
// ask is the data cipher wanted, with the ids of the Hello inner byte.
func p2pHelloMsg(ePub, es, static, src []byte, port uint16, at uint64, ask byte, pad []byte) []byte {
	head := make([]byte, 0, p2pHeadLen)
	head = append(head, src...)
	head = binary.BigEndian.AppendUint16(head, port)
	head = binary.BigEndian.AppendUint64(head, at)
	head = append(head, p2pProof(static, ePub, src, port, at)...)
	head = append(head, ask, byte(len(pad)))
	k1 := p2pKDF(es, ePub, "loft p2p hello", chacha20poly1305.KeySize)
	msg := append(append([]byte(nil), ePub...), p2pAEAD(k1).Seal(nil, zeroNonce[:], head, ePub)...)
	return append(msg, pad...)
}

// p2pReplyMsg is the responder's answer: rPub || sealed (chosen cipher,
// pad length) || pad.
func p2pReplyMsg(rPub, es, ePub []byte, chosen byte, pad []byte) []byte {
	k2 := p2pKDF(es, append(append([]byte(nil), ePub...), rPub...), "loft p2p reply", chacha20poly1305.KeySize)
	msg := append(append([]byte(nil), rPub...), p2pAEAD(k2).Seal(nil, zeroNonce[:], []byte{chosen, byte(len(pad))}, rPub)...)
	return append(msg, pad...)
}

// p2pDataKeys are the initiator→responder and responder→initiator keys.
func p2pDataKeys(ee, es, static, ePub, rPub []byte) (ir, ri []byte) {
	secret := append(append(append([]byte(nil), ee...), es...), static...)
	salt := append(append([]byte(nil), ePub...), rPub...)
	k := p2pKDF(secret, salt, "loft p2p data", 2*chacha20poly1305.KeySize)
	return k[:32], k[32:]
}

// p2pSeen remembers fresh initiator keys for twice the clock skew, so a
// recorded hello cannot be played again to open a local port.
var p2pSeen = struct {
	sync.Mutex
	at map[[32]byte]time.Time
}{at: map[[32]byte]time.Time{}}

func p2pFirstUse(pub []byte, now time.Time) bool {
	var k [32]byte
	copy(k[:], pub)
	k[31] &= 0x7f // the top bit is noise
	p2pSeen.Lock()
	defer p2pSeen.Unlock()
	for x, t := range p2pSeen.at {
		if now.Sub(t) > 2*p2pSkew {
			delete(p2pSeen.at, x)
		}
	}
	if _, ok := p2pSeen.at[k]; ok {
		return false
	}
	p2pSeen.at[k] = now
	return true
}

// p2pAccept runs the responder side on c. It returns the sealed connection,
// the port the initiator wants and its overlay IP.
func p2pAccept(c net.Conn) (net.Conn, uint16, net.IP, error) {
	my, err := EnsureP2PKey()
	if err != nil {
		return nil, 0, nil, err
	}
	return p2pAcceptAs(c, my, hintPub)
}

// p2pAcceptAs is p2pAccept for member key my; pubOf gives the P2P key of
// the member at an overlay address, nil if it is not known.
func p2pAcceptAs(c net.Conn, my *ecdh.PrivateKey, pubOf func(ip string) []byte) (net.Conn, uint16, net.IP, error) {
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	defer c.SetDeadline(time.Time{})
	first := make([]byte, 32+p2pHeadLen+p2pTag)
	if _, err := io.ReadFull(c, first); err != nil {
		return nil, 0, nil, err
	}
	ePub := first[:32]
	es, err := p2pECDH(my, ePub)
	if err != nil {
		return nil, 0, nil, errP2PHello
	}
	k1 := p2pKDF(es, ePub, "loft p2p hello", chacha20poly1305.KeySize)
	head, err := p2pAEAD(k1).Open(nil, zeroNonce[:], first[32:], ePub)
	if err != nil {
		return nil, 0, nil, errP2PHello
	}
	src := net.IP(append([]byte(nil), head[:4]...))
	port := binary.BigEndian.Uint16(head[4:6])
	at := binary.BigEndian.Uint64(head[6:14])
	now := time.Now()
	if d := now.Sub(time.Unix(int64(at), 0)); d > p2pSkew || d < -p2pSkew {
		return nil, 0, nil, errP2PHello
	}
	// The initiator must be a member in this client's hints, with the key
	// the proof was made with.
	pub := pubOf(src.String())
	if len(pub) != 32 {
		return nil, 0, nil, errP2PHello
	}
	static, err := p2pECDH(my, pub)
	if err != nil {
		return nil, 0, nil, errP2PHello
	}
	if !hmac.Equal(head[14:30], p2pProof(static, ePub, head[:4], port, at)) {
		return nil, 0, nil, errP2PHello
	}
	if !p2pFirstUse(ePub, now) {
		return nil, 0, nil, errP2PHello
	}
	if _, err := io.CopyN(io.Discard, c, int64(head[31])); err != nil {
		return nil, 0, nil, err
	}
	r, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, 0, nil, err
	}
	rPub := p2pWirePub(r)
	// ChaCha20-Poly1305 is the only data cipher on P2P for now; any other
	// ask gets ChaCha (as with the Hello inner byte, clear is never chosen
	// here: there is no TLS under a direct connection).
	if _, err := c.Write(p2pReplyMsg(rPub, es, ePub, wire.InnerChaCha, p2pPad())); err != nil {
		return nil, 0, nil, err
	}
	ee, err := p2pECDH(r, ePub)
	if err != nil {
		return nil, 0, nil, err
	}
	ir, ri := p2pDataKeys(ee, es, static, ePub, rPub)
	return newP2PConn(c, ri, ir), port, src, nil
}

// p2pConn seals data records on a direct connection.
type p2pConn struct {
	net.Conn
	wmu  sync.Mutex
	send cipher.AEAD
	sn   uint64
	rmu  sync.Mutex
	recv cipher.AEAD
	rn   uint64
	buf  []byte // opened bytes not yet read
}

func newP2PConn(c net.Conn, send, recv []byte) *p2pConn {
	return &p2pConn{Conn: c, send: p2pAEAD(send), recv: p2pAEAD(recv)}
}

func p2pNonce(n uint64) []byte {
	var b [chacha20poly1305.NonceSize]byte
	binary.LittleEndian.PutUint64(b[:], n)
	return b[:]
}

// Write sends b as records of at most p2pRecMax bytes: sealed u16 length,
// then the sealed bytes.
func (p *p2pConn) Write(b []byte) (int, error) {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	done := 0
	for done < len(b) {
		n := min(len(b)-done, p2pRecMax)
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(n))
		rec := p.send.Seal(nil, p2pNonce(p.sn), l[:], nil)
		rec = p.send.Seal(rec, p2pNonce(p.sn+1), b[done:done+n], nil)
		p.sn += 2
		if _, err := p.Conn.Write(rec); err != nil {
			return done, err
		}
		done += n
	}
	return done, nil
}

// Read returns opened bytes; a record that fails to open ends the
// connection.
func (p *p2pConn) Read(b []byte) (int, error) {
	p.rmu.Lock()
	defer p.rmu.Unlock()
	if len(p.buf) == 0 {
		hdr := make([]byte, 2+p2pTag)
		if _, err := io.ReadFull(p.Conn, hdr); err != nil {
			return 0, err
		}
		l, err := p.recv.Open(nil, p2pNonce(p.rn), hdr, nil)
		if err != nil {
			p.Conn.Close()
			return 0, errP2PHello
		}
		body := make([]byte, int(binary.BigEndian.Uint16(l))+p2pTag)
		if _, err := io.ReadFull(p.Conn, body); err != nil {
			return 0, err
		}
		if p.buf, err = p.recv.Open(body[:0], p2pNonce(p.rn+1), body, nil); err != nil {
			p.Conn.Close()
			return 0, errP2PHello
		}
		p.rn += 2
	}
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	return n, nil
}
