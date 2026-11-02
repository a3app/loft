//go:build linux

package ndms

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Linux tun ioctls. TUNSETIFF is _IOW('T', 202, int). The kernel still
// reads a name and flags from the pointer. IFF_TUN|IFF_NO_PI matches ndmc.
const (
	tunSetIFF     = 0x400454ca
	tunSetPersist = 0x400454cb
	siocGIFFLAGS  = 0x8913
	siocSIFFLAGS  = 0x8914
	iffTUN        = 0x0001
	iffNOPI       = 0x1000
	iffUP         = 0x1
)

// ensureTUN makes sure name is a persistent tun, admin-up, and that nobody
// holds its queue. A new device is created and the fd is closed, so a later
// TUNSETIFF can attach. An existing device is only brought up.
func ensureTUN(name string) error {
	if len(name) == 0 || len(name) >= 16 {
		return fmt.Errorf("tun name %q", name)
	}
	if _, err := os.Stat("/sys/class/net/" + name); err != nil {
		if err := createTUN(name); err != nil {
			return err
		}
	}
	return linkUp(name)
}

// createTUN attaches name, marks it persistent, and closes the fd.
func createTUN(name string) error {
	f, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	var req [32]byte
	copy(req[:], name)
	*(*uint16)(unsafe.Pointer(&req[16])) = iffTUN | iffNOPI
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), tunSetIFF, uintptr(unsafe.Pointer(&req[0]))); errno != 0 {
		return fmt.Errorf("tun %s: %w", name, errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), tunSetPersist, 1); errno != 0 {
		return fmt.Errorf("tun %s persist: %w", name, errno)
	}
	return nil
}

// linkUp sets IFF_UP. ndmc answers "interface is up" without doing it when
// the NDM object already says up and the kernel device was just created.
// The queue then stays admin-down and the read loop sees no packets.
func linkUp(name string) error {
	var req [32]byte
	copy(req[:], name)
	s, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(s)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(s), siocGIFFLAGS, uintptr(unsafe.Pointer(&req[0]))); errno != 0 {
		return fmt.Errorf("tun %s flags: %w", name, errno)
	}
	*(*uint16)(unsafe.Pointer(&req[16])) |= iffUP
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(s), siocSIFFLAGS, uintptr(unsafe.Pointer(&req[0]))); errno != 0 {
		return fmt.Errorf("tun %s up: %w", name, errno)
	}
	return nil
}
