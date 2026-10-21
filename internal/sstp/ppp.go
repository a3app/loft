package sstp

// PPP over SSTP: LCP, PAP, CHAP/MSCHAPv2, CCP/MPPE, IPCP.

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
)

const (
	pppLCP  uint16 = 0xc021
	pppPAP  uint16 = 0xc023
	pppCHAP uint16 = 0xc223
	pppCCP  uint16 = 0x80fd
	pppIPCP uint16 = 0x8021
	pppIP   uint16 = 0x0021
	pppMPPE uint16 = 0x00fd
)

const (
	cpConfReq = 1
	cpConfAck = 2
	cpConfNak = 3
	cpConfRej = 4
	cpTermReq = 5
	cpTermAck = 6
	cpEchoReq = 9
	cpEchoRep = 10
)

type ppp struct {
	user, pass string
	magic      uint32
	id         byte
	lcpOpen    bool
	ipcpOpen   bool
	authDone   bool
	acfc, pfc  bool
	authProto  uint16
	chapAlg    byte
	peerChal   []byte
	authChal   []byte
	ntResp     []byte
	hlak       []byte
	sendMPPE   *mppe
	recvMPPE   *mppe
	mppeOn     bool
	ip         net.IP
	peer       net.IP
	dns        []net.IP
	out        [][]byte // PPP frames to send
	papCHAP    bool     // PAP + CHAP-MD5; false = MSCHAPv2 only
	ourMRU     uint16
	peerMRU    uint16
}

func newPPP(user, pass string, papCHAP bool) *ppp {
	var m [4]byte
	_, _ = rand.Read(m[:])
	return &ppp{user: user, pass: pass, magic: binary.BigEndian.Uint32(m[:]), id: 1, papCHAP: papCHAP, ourMRU: 1500, peerMRU: 1500}
}

func (p *ppp) nextID() byte {
	p.id++
	return p.id
}

func (p *ppp) send(proto uint16, info []byte) {
	// Always HDLC address/control and a 2-byte protocol. Peers may compress.
	f := []byte{0xff, 0x03, byte(proto >> 8), byte(proto)}
	f = append(f, info...)
	p.out = append(p.out, f)
}

func (p *ppp) start() {
	p.sendLCP(cpConfReq, p.lcpOpts())
}

func (p *ppp) lcpOpts() []byte {
	mru := p.ourMRU
	if mru < 576 {
		mru = 1500
		p.ourMRU = 1500
	}
	var o []byte
	o = append(o, 1, 4, byte(mru>>8), byte(mru))
	var mag [4]byte
	binary.BigEndian.PutUint32(mag[:], p.magic)
	o = append(o, 5, 6)
	o = append(o, mag[:]...)
	o = append(o, 8, 2) // ACFC
	o = append(o, 7, 2) // PFC
	return o
}

func (p *ppp) sendLCP(code byte, opts []byte) {
	id := p.nextID()
	info := []byte{code, id, 0, 0}
	info = append(info, opts...)
	binary.BigEndian.PutUint16(info[2:4], uint16(len(info)))
	p.send(pppLCP, info)
}

func (p *ppp) sendCP(proto uint16, code, id byte, opts []byte) {
	info := []byte{code, id, 0, 0}
	info = append(info, opts...)
	binary.BigEndian.PutUint16(info[2:4], uint16(len(info)))
	p.send(proto, info)
}

func (p *ppp) handle(frame []byte) error {
	proto, info, err := parsePPP(frame)
	if err != nil {
		return err
	}
	switch proto {
	case pppLCP:
		return p.handleLCP(info)
	case pppPAP:
		return p.handlePAP(info)
	case pppCHAP:
		return p.handleCHAP(info)
	case pppCCP:
		return p.handleCCP(info)
	case pppIPCP:
		return p.handleIPCP(info)
	case pppIP, pppMPPE:
		return nil
	default:
		if len(info) >= 2 {
			rej := []byte{byte(proto >> 8), byte(proto)}
			rej = append(rej, info...)
			p.sendCP(pppLCP, 8, p.nextID(), rej) // Protocol-Reject
		}
	}
	return nil
}

func parsePPP(f []byte) (uint16, []byte, error) {
	if len(f) < 3 {
		return 0, nil, fmt.Errorf("ppp short")
	}
	off := 0
	if f[0] == 0xff && f[1] == 0x03 {
		off = 2
	}
	if off >= len(f) {
		return 0, nil, fmt.Errorf("ppp")
	}
	if f[off]&1 == 1 {
		return uint16(f[off]), f[off+1:], nil
	}
	if off+2 > len(f) {
		return 0, nil, fmt.Errorf("ppp proto")
	}
	return binary.BigEndian.Uint16(f[off : off+2]), f[off+2:], nil
}

func (p *ppp) handleLCP(info []byte) error {
	if len(info) < 4 {
		return fmt.Errorf("lcp")
	}
	code, id := info[0], info[1]
	n := int(binary.BigEndian.Uint16(info[2:4]))
	if n > len(info) {
		n = len(info)
	}
	opts := info[4:n]
	switch code {
	case cpConfReq:
		ack, nak, rej := p.lcpSplit(opts)
		if len(rej) > 0 {
			p.sendCP(pppLCP, cpConfRej, id, rej)
			return nil
		}
		if len(nak) > 0 {
			p.sendCP(pppLCP, cpConfNak, id, nak)
			return nil
		}
		p.sendCP(pppLCP, cpConfAck, id, ack)
		p.seePeerLCP(opts)
		if !p.lcpOpen {
			p.sendLCP(cpConfReq, p.lcpOpts())
		}
	case cpConfAck:
		p.lcpOpen = true
		if p.authProto == 0 {
			p.authDone = true
			p.hlak = make([]byte, 32)
			p.afterAuth()
		} else {
			p.maybeAuth()
		}
	case cpConfNak:
		p.applyLCPNak(opts)
		p.sendLCP(cpConfReq, p.lcpOpts())
	case cpConfRej:
		if lcpHasMRU(opts) {
			p.ourMRU = 1500
		}
		p.sendLCP(cpConfReq, p.lcpOptsBare())
	case cpTermReq:
		p.sendCP(pppLCP, cpTermAck, id, nil)
		return fmt.Errorf("lcp terminate")
	case cpEchoReq:
		// RFC 1661: Echo-Reply Magic-Number is the sender's, not a copy of the request.
		// Copying the peer magic looks like a loopback and pppd sends Terminate-Request.
		p.sendCP(pppLCP, cpEchoRep, id, p.echoReplyData(opts))
	}
	return nil
}

func (p *ppp) echoReplyData(req []byte) []byte {
	var mag [4]byte
	binary.BigEndian.PutUint32(mag[:], p.magic)
	if len(req) > 4 {
		return append(mag[:], req[4:]...)
	}
	return mag[:]
}

func (p *ppp) lcpOptsBare() []byte {
	var mag [4]byte
	binary.BigEndian.PutUint32(mag[:], p.magic)
	o := []byte{5, 6}
	o = append(o, mag[:]...)
	return o
}

func (p *ppp) lcpSplit(opts []byte) (ack, nak, rej []byte) {
	for len(opts) >= 2 {
		t, l := opts[0], int(opts[1])
		if l < 2 || l > len(opts) {
			break
		}
		opt := opts[:l]
		opts = opts[l:]
		switch t {
		case 1, 2, 5, 7, 8: // MRU, asyncmap, magic, PFC, ACFC
			ack = append(ack, opt...)
		case 3: // auth
			if l >= 4 {
				proto := binary.BigEndian.Uint16(opt[2:4])
				alg := byte(0)
				if l >= 5 {
					alg = opt[4]
				}
				if p.wantAuth(proto, alg) {
					p.authProto = proto
					p.chapAlg = alg
					ack = append(ack, opt...)
				} else {
					nak = append(nak, 3, 5, 0xc2, 0x23, 0x81)
				}
			}
		default:
			rej = append(rej, opt...)
		}
	}
	return
}

func (p *ppp) seePeerLCP(opts []byte) {
	for len(opts) >= 2 {
		t, l := opts[0], int(opts[1])
		if l < 2 || l > len(opts) {
			break
		}
		opt := opts[:l]
		opts = opts[l:]
		switch t {
		case 1:
			if l >= 4 {
				v := binary.BigEndian.Uint16(opt[2:4])
				if v >= 576 {
					p.peerMRU = v
				}
			}
		case 7:
			p.pfc = true
		case 8:
			p.acfc = true
		case 3:
			if l >= 4 {
				p.authProto = binary.BigEndian.Uint16(opt[2:4])
				if l >= 5 {
					p.chapAlg = opt[4]
				}
			}
		}
	}
}

func lcpHasMRU(opts []byte) bool {
	for len(opts) >= 2 {
		t, l := opts[0], int(opts[1])
		if l < 2 || l > len(opts) {
			break
		}
		if t == 1 {
			return true
		}
		opts = opts[l:]
	}
	return false
}

func (p *ppp) applyLCPNak(opts []byte) {
	for len(opts) >= 2 {
		t, l := opts[0], int(opts[1])
		if l < 2 || l > len(opts) {
			break
		}
		opt := opts[:l]
		opts = opts[l:]
		if t == 1 && l >= 4 {
			v := binary.BigEndian.Uint16(opt[2:4])
			if v >= 576 && v <= 16384 {
				p.ourMRU = v
			}
		}
	}
}

func (p *ppp) infoMTU() int {
	a, b := int(p.ourMRU), int(p.peerMRU)
	if a < 576 {
		a = 1500
	}
	if b < 576 {
		b = 1500
	}
	if a > b {
		return b
	}
	return a
}

func (p *ppp) wantAuth(proto uint16, alg byte) bool {
	if proto == pppCHAP && alg == 0x81 {
		return true
	}
	if !p.papCHAP {
		return false
	}
	if proto == pppPAP {
		return true
	}
	return proto == pppCHAP && (alg == 0x05 || alg == 0)
}

func (p *ppp) maybeAuth() {
	if p.authProto == pppPAP {
		u := []byte(p.user)
		pw := []byte(p.pass)
		body := []byte{byte(len(u))}
		body = append(body, u...)
		body = append(body, byte(len(pw)))
		body = append(body, pw...)
		p.sendCP(pppPAP, 1, p.nextID(), body)
	}
}

func (p *ppp) handlePAP(info []byte) error {
	if len(info) < 1 {
		return nil
	}
	switch info[0] {
	case 2:
		p.authDone = true
		p.hlak = make([]byte, 32)
		p.afterAuth()
	case 3:
		return fmt.Errorf("pap rejected")
	}
	return nil
}

func (p *ppp) handleCHAP(info []byte) error {
	if len(info) < 4 {
		return fmt.Errorf("chap")
	}
	code, id := info[0], info[1]
	n := int(binary.BigEndian.Uint16(info[2:4]))
	if n > len(info) {
		n = len(info)
	}
	body := info[4:n]
	switch code {
	case 1: // challenge
		if len(body) < 1 {
			return fmt.Errorf("chap chal")
		}
		cl := int(body[0])
		if 1+cl > len(body) {
			return fmt.Errorf("chap chal len")
		}
		chal := body[1 : 1+cl]
		p.authChal = append([]byte(nil), chal...)
		if p.chapAlg == 0x81 || (p.chapAlg != 0x05 && cl == 16) {
			return p.mschapv2Resp(id, chal)
		}
		if !p.papCHAP {
			return fmt.Errorf("chap failure")
		}
		// CHAP-MD5 (RFC 1994). codeql[go/weak-sensitive-data-hashing]
		h := md5.New()
		h.Write([]byte{id})
		h.Write([]byte(p.pass))
		h.Write(chal)
		sum := h.Sum(nil)
		val := append([]byte{16}, sum...)
		val = append(val, []byte(samUser(p.user))...)
		p.sendCP(pppCHAP, 2, id, val)
	case 3:
		if p.ntResp != nil && p.authChal != nil {
			mar := authenticatorResponse(p.pass, p.ntResp, p.peerChal, p.authChal, p.user)
			if !strings.HasPrefix(string(body), mar) {
				return fmt.Errorf("chap authenticator")
			}
		}
		p.authDone = true
		if p.hlak == nil {
			p.hlak = make([]byte, 32)
		}
		p.afterAuth()
	case 4:
		return fmt.Errorf("chap failure")
	}
	return nil
}

func (p *ppp) mschapv2Resp(id byte, authChal []byte) error {
	peer := make([]byte, 16)
	_, _ = rand.Read(peer)
	p.peerChal = peer
	nt := generateNTResponse(authChal, peer, p.user, p.pass)
	p.ntResp = nt
	p.hlak = hlakFromMSCHAPv2(p.pass, nt)
	send, recv := mppeMasterKeys(p.pass, nt)
	p.sendMPPE = newMPPE(send)
	p.recvMPPE = newMPPE(recv)
	val := make([]byte, 1+49)
	val[0] = 49
	copy(val[1:17], peer)
	copy(val[25:49], nt)
	val = append(val, []byte(samUser(p.user))...)
	p.sendCP(pppCHAP, 2, id, val)
	return nil
}

func (p *ppp) afterAuth() {
	p.sendCP(pppCCP, cpConfReq, p.nextID(), []byte{18, 6, 0x01, 0x00, 0x00, 0x40}) // MPPE 128 stateless
	p.sendIPCP()
}

func (p *ppp) handleCCP(info []byte) error {
	if len(info) < 4 {
		return nil
	}
	code, id := info[0], info[1]
	n := int(binary.BigEndian.Uint16(info[2:4]))
	if n > len(info) {
		n = len(info)
	}
	opts := info[4:n]
	switch code {
	case cpConfReq:
		// ACK MPPE 128 if offered, else reject unknown
		ack, rej := splitCCP(opts)
		if len(rej) > 0 && len(ack) == 0 {
			p.sendCP(pppCCP, cpConfRej, id, rej)
			return nil
		}
		if len(ack) > 0 {
			p.sendCP(pppCCP, cpConfAck, id, ack)
			p.mppeOn = p.sendMPPE != nil
		} else {
			p.sendCP(pppCCP, cpConfRej, id, opts)
		}
	case cpConfAck:
		p.mppeOn = p.sendMPPE != nil
	case cpConfNak, cpConfRej:
		// proceed without MPPE
		p.mppeOn = false
	}
	return nil
}

func splitCCP(opts []byte) (ack, rej []byte) {
	for len(opts) >= 2 {
		t, l := opts[0], int(opts[1])
		if l < 2 || l > len(opts) {
			break
		}
		opt := opts[:l]
		opts = opts[l:]
		if t == 18 && l >= 6 {
			ack = append(ack, opt...)
		} else {
			rej = append(rej, opt...)
		}
	}
	return
}

func (p *ppp) sendIPCP() {
	// IP-Address 0.0.0.0, DNS 0.0.0.0
	opts := []byte{
		3, 6, 0, 0, 0, 0,
		129, 6, 0, 0, 0, 0,
		131, 6, 0, 0, 0, 0,
	}
	p.sendCP(pppIPCP, cpConfReq, p.nextID(), opts)
}

func (p *ppp) handleIPCP(info []byte) error {
	if len(info) < 4 {
		return nil
	}
	code, id := info[0], info[1]
	n := int(binary.BigEndian.Uint16(info[2:4]))
	if n > len(info) {
		n = len(info)
	}
	opts := info[4:n]
	switch code {
	case cpConfReq:
		p.takeIPCP(opts, true)
		p.sendCP(pppIPCP, cpConfAck, id, opts)
	case cpConfAck:
		p.takeIPCP(opts, false)
		if p.ip != nil {
			p.ipcpOpen = true
		}
	case cpConfNak:
		p.takeIPCP(opts, false)
		p.sendCP(pppIPCP, cpConfReq, p.nextID(), rebuildIPCP(p.ip, p.dns))
	case cpConfRej:
		p.sendCP(pppIPCP, cpConfReq, p.nextID(), []byte{3, 6, 0, 0, 0, 0})
	}
	return nil
}

func (p *ppp) takeIPCP(opts []byte, fromPeer bool) {
	if !fromPeer {
		p.dns = nil
	}
	for len(opts) >= 2 {
		t, l := opts[0], int(opts[1])
		if l < 2 || l > len(opts) {
			break
		}
		opt := opts[:l]
		opts = opts[l:]
		if l >= 6 {
			ip := net.IP(opt[2:6])
			switch t {
			case 3:
				if !ip.IsUnspecified() {
					got := append(net.IP(nil), ip...)
					if fromPeer {
						p.peer = got
					} else {
						p.ip = got
					}
				}
			case 129, 131:
				if !fromPeer && !ip.IsUnspecified() {
					p.dns = append(p.dns, append(net.IP(nil), ip...))
				}
			}
		}
	}
}

func rebuildIPCP(ip net.IP, dns []net.IP) []byte {
	a := []byte{0, 0, 0, 0}
	if ip != nil {
		if v4 := ip.To4(); v4 != nil {
			copy(a, v4)
		}
	}
	opts := []byte{3, 6, a[0], a[1], a[2], a[3]}
	for i, d := range dns {
		id := byte(129)
		if i > 0 {
			id = 131
		}
		b := d.To4()
		if b == nil {
			continue
		}
		opts = append(opts, id, 6, b[0], b[1], b[2], b[3])
	}
	return opts
}

func (p *ppp) wrapIP(ip []byte) []byte {
	payload := ip
	proto := uint16(pppIP)
	if p.mppeOn && p.sendMPPE != nil {
		payload = p.sendMPPE.crypt(ip)
		proto = pppMPPE
	}
	f := []byte{0xff, 0x03, byte(proto >> 8), byte(proto)}
	return append(f, payload...)
}

func (p *ppp) unwrapIP(frame []byte) ([]byte, bool) {
	proto, info, err := parsePPP(frame)
	if err != nil {
		return nil, false
	}
	switch proto {
	case pppIP:
		return info, true
	case pppMPPE:
		if p.recvMPPE == nil {
			return nil, false
		}
		b, err := p.recvMPPE.decrypt(info)
		if err != nil {
			return nil, false
		}
		return b, true
	default:
		return nil, false
	}
}
