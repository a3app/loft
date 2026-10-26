// loft-wintun-check tests whether the Wintun driver works on this machine.
// The main target is Windows 7 SP1, x86 and x64.
//
// What it does:
//  1. Prints the Windows version, bitness and the SHA-2 updates.
//  2. Loads wintun.dll from the program's folder.
//  3. Creates the network adapter "LoftTest" (Windows installs the driver here).
//  4. Gives the adapter 10.255.0.1/24.
//  5. Pings 10.255.0.2. The packets go into the adapter, the program reads
//     them and answers itself. If ping gets replies, the driver works both ways.
//  6. Removes the adapter and the driver, so nothing is left behind.
//
// Everything printed on screen also goes to loft-wintun-check.txt next to the program.
// The output is in Russian: the test is run by Russian-speaking testers.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	adapterName = "LoftTest"
	localIP     = "10.255.0.1"
	peerIP      = "10.255.0.2"
)

var out io.Writer = os.Stdout

func say(format string, a ...interface{}) { fmt.Fprintf(out, format+"\n", a...) }

func step(name string, ok bool, detail string) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
	}
	if detail != "" {
		say("[%s] %s: %s", mark, name, detail)
	} else {
		say("[%s] %s", mark, name)
	}
}

func main() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)

	if !isAdmin() {
		// The driver needs admin rights: start again through the UAC prompt.
		if relaunchAsAdmin(exe) {
			return
		}
		fmt.Println("Нужны права администратора: запустите программу через «Запуск от имени администратора».")
		pause()
		os.Exit(1)
	}

	logPath := filepath.Join(dir, "loft-wintun-check.txt")
	if f, err := os.Create(logPath); err == nil {
		defer f.Close()
		f.Write([]byte{0xef, 0xbb, 0xbf}) // BOM, so Windows 7 Notepad opens the file as UTF-8
		out = io.MultiWriter(os.Stdout, f)
	}

	ok := run(dir)
	say("")
	if ok {
		say("ИТОГ: Wintun на этой машине РАБОТАЕТ.")
	} else {
		say("ИТОГ: Wintun на этой машине НЕ работает. Пришлите файл %s", logPath)
	}
	pause()
	if !ok {
		os.Exit(1)
	}
}

func run(dir string) bool {
	say("loft-wintun-check · %s", time.Now().Format("2006-01-02 15:04:05"))
	say("Сборка: %s, %s", runtime.GOARCH, runtime.Version())

	// ── 1. System ──
	major, minor, build, sp := osVersion()
	say("Windows: %d.%d сборка %d %s", major, minor, build, sp)
	if wow64() {
		step("Разрядность", false, "это 64-битная Windows, а программа 32-битная. Запустите версию из папки x64.")
		return false
	}
	hot := hotfixes()
	for _, kb := range []string{"KB4474419", "KB4490628"} {
		has := strings.Contains(hot, kb)
		state := "нет"
		if has {
			state = "есть"
		}
		say("Обновление %s (SHA-2): %s", kb, state)
	}

	// ── 2. DLL ──
	dllPath := filepath.Join(dir, "wintun.dll")
	dll, err := syscall.LoadDLL(dllPath)
	if err != nil {
		step("Загрузка wintun.dll", false, err.Error())
		return false
	}
	w, err := bindWintun(dll)
	if err != nil {
		step("Загрузка wintun.dll", false, err.Error())
		return false
	}
	step("Загрузка wintun.dll", true, dllPath)
	w.setLogger.Call(loggerCallback)

	// ── 3. Adapter ──
	name, _ := syscall.UTF16PtrFromString(adapterName)
	ttype, _ := syscall.UTF16PtrFromString("Loft")
	// A fixed GUID: a second run reuses the same adapter instead of adding another.
	guid := [16]byte{0x6c, 0x6f, 0x66, 0x74, 0x2d, 0x77, 0x69, 0x6e, 0x74, 0x75, 0x6e, 0x2d, 0x63, 0x68, 0x6b, 0x01}
	adapter, _, err := w.createAdapter.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(ttype)), uintptr(unsafe.Pointer(&guid)))
	if adapter == 0 {
		step("Создание адаптера (установка драйвера)", false, err.Error())
		return false
	}
	defer func() {
		w.closeAdapter.Call(adapter)
		if r, _, _ := w.deleteDriver.Call(); r != 0 {
			say("Адаптер и драйвер удалены.")
		} else {
			say("Адаптер удалён, драйвер остался (его держит другой адаптер Wintun).")
		}
	}()
	ver, _, _ := w.driverVersion.Call()
	step("Создание адаптера (установка драйвера)", true, fmt.Sprintf("драйвер %d.%d", (ver>>16)&0xffff, ver&0xffff))

	// ── 4. Address ──
	var luid uint64
	w.adapterLUID.Call(adapter, uintptr(unsafe.Pointer(&luid)))
	idx, err := luidToIndex(luid)
	if err != nil {
		step("Номер интерфейса", false, err.Error())
		return false
	}
	cmd := exec.Command("netsh", "interface", "ipv4", "set", "address",
		fmt.Sprintf("name=%d", idx), "source=static", "address="+localIP, "mask=255.255.255.0")
	if b, err := cmd.CombinedOutput(); err != nil {
		step("Адрес "+localIP+"/24", false, fmt.Sprintf("%v %s", err, oem(b)))
		return false
	}
	step("Адрес "+localIP+"/24", true, fmt.Sprintf("интерфейс %d", idx))

	// ── 5. Session and ping ──
	session, _, err := w.startSession.Call(adapter, 0x400000)
	if session == 0 {
		step("Сессия", false, err.Error())
		return false
	}
	defer w.endSession.Call(session)
	step("Сессия", true, "")

	var got, answered int32
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		echoLoop(w, session, stop, &got, &answered)
	}()

	// Windows needs a couple of seconds to bring the address up on a new interface.
	time.Sleep(3 * time.Second)
	b, err := exec.Command("ping", "-n", "4", "-w", "1000", peerIP).CombinedOutput()
	close(stop)
	<-done
	say("--- ping %s ---", peerIP)
	say("%s", oem(b))
	say("---")
	say("Пакетов принято из адаптера: %d, ответов отправлено в адаптер: %d", atomic.LoadInt32(&got), atomic.LoadInt32(&answered))
	pingOK := err == nil && atomic.LoadInt32(&answered) > 0
	step("Ping через адаптер (приём и отправка)", pingOK, "")
	return pingOK
}

// echoLoop reads packets from the adapter and answers ICMP echo requests to peerIP.
func echoLoop(w *wintun, session uintptr, stop chan struct{}, got, answered *int32) {
	ev, _, _ := w.readEvent.Call(session)
	peer := [4]byte{10, 255, 0, 2}
	for {
		select {
		case <-stop:
			return
		default:
		}
		var size uint32
		p, _, err := w.receive.Call(session, uintptr(unsafe.Pointer(&size)))
		if p == 0 {
			if en, ok := err.(syscall.Errno); ok && en == 259 { // ERROR_NO_MORE_ITEMS: the queue is empty
				syscall.WaitForSingleObject(syscall.Handle(ev), 200)
				continue
			}
			say("Чтение из адаптера: %v", err)
			return
		}
		pkt := make([]byte, size)
		copy(pkt, unsafe.Slice((*byte)(unsafe.Pointer(p)), size))
		w.releaseRecv.Call(session, p)
		atomic.AddInt32(got, 1)

		reply := echoReply(pkt, peer)
		if reply == nil {
			continue
		}
		buf, _, err := w.allocSend.Call(session, uintptr(len(reply)))
		if buf == 0 {
			say("Отправка в адаптер: %v", err)
			continue
		}
		copy(unsafe.Slice((*byte)(unsafe.Pointer(buf)), len(reply)), reply)
		w.send.Call(session, buf)
		atomic.AddInt32(answered, 1)
	}
}

// echoReply builds an ICMP echo reply to an echo request for peer. Otherwise nil.
func echoReply(p []byte, peer [4]byte) []byte {
	if len(p) < 20 || p[0]>>4 != 4 || p[9] != 1 {
		return nil
	}
	ihl := int(p[0]&0x0f) * 4
	if len(p) < ihl+8 || !bytes.Equal(p[16:20], peer[:]) || p[ihl] != 8 {
		return nil
	}
	r := make([]byte, len(p))
	copy(r, p)
	copy(r[12:16], p[16:20]) // the reply comes from the address that was pinged
	copy(r[16:20], p[12:16]) // and goes to the sender
	r[8] = 64                // TTL
	r[ihl] = 0               // type: echo reply
	r[ihl+2], r[ihl+3] = 0, 0
	c := checksum(r[ihl:])
	r[ihl+2], r[ihl+3] = byte(c>>8), byte(c)
	r[10], r[11] = 0, 0
	c = checksum(r[:ihl])
	r[10], r[11] = byte(c>>8), byte(c)
	return r
}

func checksum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return ^uint16(s)
}

// ── Wintun API ──

type wintun struct {
	createAdapter, closeAdapter, deleteDriver, driverVersion, adapterLUID,
	startSession, endSession, readEvent, receive, releaseRecv, allocSend, send,
	setLogger *syscall.Proc
}

func bindWintun(d *syscall.DLL) (*wintun, error) {
	w := &wintun{}
	for name, dst := range map[string]**syscall.Proc{
		"WintunCreateAdapter":           &w.createAdapter,
		"WintunCloseAdapter":            &w.closeAdapter,
		"WintunDeleteDriver":            &w.deleteDriver,
		"WintunGetRunningDriverVersion": &w.driverVersion,
		"WintunGetAdapterLUID":          &w.adapterLUID,
		"WintunStartSession":            &w.startSession,
		"WintunEndSession":              &w.endSession,
		"WintunGetReadWaitEvent":        &w.readEvent,
		"WintunReceivePacket":           &w.receive,
		"WintunReleaseReceivePacket":    &w.releaseRecv,
		"WintunAllocateSendPacket":      &w.allocSend,
		"WintunSendPacket":              &w.send,
		"WintunSetLogger":               &w.setLogger,
	} {
		p, err := d.FindProc(name)
		if err != nil {
			return nil, err
		}
		*dst = p
	}
	return w, nil
}

func logLine(level uintptr, msg *uint16) {
	lv := [...]string{"info", "warn", "err"}
	l := "?"
	if int(level) < len(lv) {
		l = lv[level]
	}
	say("  wintun %s: %s", l, utf16z(msg))
}

func utf16z(p *uint16) string {
	if p == nil {
		return ""
	}
	var s []uint16
	for ptr := unsafe.Pointer(p); ; ptr = unsafe.Add(ptr, 2) {
		c := *(*uint16)(ptr)
		if c == 0 {
			break
		}
		s = append(s, c)
	}
	return syscall.UTF16ToString(s)
}

// ── Windows system calls ──

var (
	ntdll    = syscall.NewLazyDLL("ntdll.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	iphlpapi = syscall.NewLazyDLL("iphlpapi.dll")
)

func osVersion() (major, minor, build uint32, sp string) {
	type osvi struct {
		size, major, minor, build, platform uint32
		csd                                 [128]uint16
	}
	v := osvi{}
	v.size = uint32(unsafe.Sizeof(v))
	ntdll.NewProc("RtlGetVersion").Call(uintptr(unsafe.Pointer(&v)))
	return v.major, v.minor, v.build, syscall.UTF16ToString(v.csd[:])
}

func wow64() bool {
	var b int32
	p := kernel32.NewProc("IsWow64Process")
	if p.Find() != nil {
		return false
	}
	h, _ := syscall.GetCurrentProcess()
	p.Call(uintptr(h), uintptr(unsafe.Pointer(&b)))
	return b != 0
}

func isAdmin() bool {
	r, _, _ := shell32.NewProc("IsUserAnAdmin").Call()
	return r != 0
}

func relaunchAsAdmin(exe string) bool {
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	r, _, _ := shell32.NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1)
	return r > 32
}

func luidToIndex(luid uint64) (uint32, error) {
	var idx uint32
	r, _, _ := iphlpapi.NewProc("ConvertInterfaceLuidToIndex").Call(uintptr(unsafe.Pointer(&luid)), uintptr(unsafe.Pointer(&idx)))
	if r != 0 {
		return 0, syscall.Errno(r)
	}
	return idx, nil
}

func hotfixes() string {
	b, _ := exec.Command("wmic", "qfe", "get", "HotFixID").CombinedOutput()
	return string(b)
}

// oem converts output of Windows console tools (ping, netsh) from the OEM code page
// (cp866 on Russian Windows) to a normal string.
func oem(b []byte) string {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return ""
	}
	conv := kernel32.NewProc("MultiByteToWideChar")
	buf := make([]uint16, len(b)*2)
	n, _, _ := conv.Call(1, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))) // 1 = CP_OEMCP
	return strings.ReplaceAll(syscall.UTF16ToString(buf[:n]), "\r\n", "\n")
}

func pause() {
	fmt.Println("\nНажмите Enter, чтобы закрыть окно.")
	fmt.Scanln()
}
