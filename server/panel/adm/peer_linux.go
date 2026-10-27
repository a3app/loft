package adm

// This file checks who is on the other end of the adm socket. Only the
// panel's own user (or root) may send requests to loft-panel-adm, and Linux
// reports the peer's uid through SO_PEERCRED.

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// PeerUID returns a check that the unix socket peer runs as uid (or root).
// It is used as Server.Allow, which runs it on every connection before the
// request is acted on.
func PeerUID(uid int) func(net.Conn) error {
	return func(c net.Conn) error {
		uc, ok := c.(*net.UnixConn)
		if !ok {
			return errors.New("not a unix socket")
		}
		raw, err := uc.SyscallConn()
		if err != nil {
			return err
		}
		var cred *unix.Ucred
		var cerr error
		if err := raw.Control(func(fd uintptr) {
			cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		}); err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
		if int(cred.Uid) != uid && cred.Uid != 0 {
			return fmt.Errorf("uid %d pid %d", cred.Uid, cred.Pid)
		}
		return nil
	}
}
