//go:build darwin

package inbound

import "testing"

func TestUtunWriteRejectsOversize(t *testing.T) {
	u := &utunRWC{fd: -1}
	if _, err := u.Write(make([]byte, utunMaxIP+1)); err == nil {
		t.Fatal("oversize write")
	}
	if u.wbuf != nil {
		t.Fatal("allocated before reject")
	}
}

func TestUtunWriteAllocIsCapped(t *testing.T) {
	u := &utunRWC{fd: -1}
	_, _ = u.Write(make([]byte, utunMaxIP))
	if cap(u.wbuf) != utunBufCap {
		t.Fatalf("wbuf cap %d", cap(u.wbuf))
	}
}

func TestUtunReadAllocIsCapped(t *testing.T) {
	u := &utunRWC{fd: -1}
	_, _ = u.Read(make([]byte, 1<<20))
	if cap(u.rbuf) != utunBufCap {
		t.Fatalf("rbuf cap %d", cap(u.rbuf))
	}
}
