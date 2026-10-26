package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

// The adapter address is set with the IP Helper API, like the real client
// will do it, not with netsh. The API changes only the live configuration:
// nothing is saved in the registry, so the next run starts clean.
//
// MIB_UNICASTIPADDRESS_ROW is 80 bytes on both x86 and x64. It is kept as a
// byte array with fixed offsets, because Go aligns uint64 differently on x86.
const (
	rowSize      = 80
	offFamily    = 0  // Address.si_family
	offIPv4      = 4  // Address.Ipv4.sin_addr
	offLUID      = 32 // InterfaceLuid
	offIndex     = 40 // InterfaceIndex
	offPrefixLen = 60 // OnLinkPrefixLength
	offDadState  = 64 // DadState

	afInet            = 2
	dadStatePreferred = 4
	errAlreadyExists  = 5010 // ERROR_OBJECT_ALREADY_EXISTS
)

type ipRow [rowSize]byte

func newIPRow(luid uint64, ip string, prefix byte) *ipRow {
	var r ipRow
	iphlpapi.NewProc("InitializeUnicastIpAddressEntry").Call(uintptr(unsafe.Pointer(&r)))
	binary.LittleEndian.PutUint16(r[offFamily:], afInet)
	copy(r[offIPv4:offIPv4+4], net.ParseIP(ip).To4())
	binary.LittleEndian.PutUint64(r[offLUID:], luid)
	r[offPrefixLen] = prefix
	binary.LittleEndian.PutUint32(r[offDadState:], dadStatePreferred)
	return &r
}

// setAddress puts ip/prefix on the adapter. It reports what it found on the
// way, so a failure in the log says where a stale copy of the address was.
func setAddress(luid uint64, ip string, prefix byte) (string, error) {
	note := ""
	// Another interface holding the same address blocks ours: take it off.
	for _, idx := range addressHolders(ip, luid) {
		r := newIPRow(0, ip, prefix)
		binary.LittleEndian.PutUint32(r[offIndex:], idx)
		if e, _, _ := iphlpapi.NewProc("DeleteUnicastIpAddressEntry").Call(uintptr(unsafe.Pointer(r))); e == 0 {
			note += fmt.Sprintf("снят с интерфейса %d; ", idx)
		}
	}
	r := newIPRow(luid, ip, prefix)
	e, _, _ := iphlpapi.NewProc("CreateUnicastIpAddressEntry").Call(uintptr(unsafe.Pointer(r)))
	switch e {
	case 0:
		return note, nil
	case errAlreadyExists:
		// Windows may restore an address saved by an older run on its own.
		// It is already on our adapter, which is all the test needs.
		if g, _, _ := iphlpapi.NewProc("GetUnicastIpAddressEntry").Call(uintptr(unsafe.Pointer(newIPRow(luid, ip, prefix)))); g == 0 {
			return note + "уже стоял на адаптере", nil
		}
		return note, fmt.Errorf("адрес занят, но не на нашем адаптере (%v)", syscall.Errno(e))
	default:
		return note, syscall.Errno(e)
	}
}

// clearAddress takes ip off the adapter when the test ends.
func clearAddress(luid uint64, ip string, prefix byte) {
	iphlpapi.NewProc("DeleteUnicastIpAddressEntry").Call(uintptr(unsafe.Pointer(newIPRow(luid, ip, prefix))))
}

// addressHolders lists interfaces other than ours that have ip.
func addressHolders(ip string, own uint64) []uint32 {
	var table uintptr
	if e, _, _ := iphlpapi.NewProc("GetUnicastIpAddressTable").Call(afInet, uintptr(unsafe.Pointer(&table))); e != 0 || table == 0 {
		return nil
	}
	defer iphlpapi.NewProc("FreeMibTable").Call(table)
	want := net.ParseIP(ip).To4()
	n := *(*uint32)(unsafe.Pointer(table))
	var idx []uint32
	// The rows start at offset 8: the count is padded to the row's 8-byte alignment.
	for i := uint32(0); i < n; i++ {
		row := (*ipRow)(unsafe.Pointer(table + 8 + uintptr(i)*rowSize))
		if binary.LittleEndian.Uint16(row[offFamily:]) != afInet || !net.IP(row[offIPv4:offIPv4+4]).Equal(want) {
			continue
		}
		if binary.LittleEndian.Uint64(row[offLUID:]) == own {
			continue
		}
		idx = append(idx, binary.LittleEndian.Uint32(row[offIndex:]))
	}
	return idx
}
