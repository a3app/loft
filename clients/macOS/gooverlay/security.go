// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// loft overlay for macOS 10.13: drop SecTrustEvaluateWithError (10.14)
// and SecTrustCopyCertificateChain (12.0). Those cgo_import_dynamic
// binds abort dyld on High Sierra even with CGO_ENABLED=0. Evaluate +
// GetCertificateCount/AtIndex exist from 10.3 and remain in current SDKs.
//
// This file replaces crypto/x509/internal/macos/security.go at build time
// through `go build -overlay` (see README.md and root_darwin.go). It holds
// cgo-free bindings to Security.framework: each exported function calls the
// C function through an assembly trampoline (security.s) and turns its
// status code into a Go error. The //go:cgo_import_dynamic lines tell the
// linker which framework symbols to bind; any symbol listed here must exist
// on macOS 10.13, or dyld refuses to start the binary.

//go:build darwin

package macos

import (
	"errors"
	"internal/abi"
	"strconv"
	"unsafe"
)

// Security.framework linker flags for the external linker. See Issue 42459.
//
//go:cgo_ldflag "-framework"
//go:cgo_ldflag "Security"

// SecTrustResultType from Security.h. Evaluate success is Proceed or Unspecified.
const (
	trustResultProceed     = 1
	trustResultUnspecified = 4
)

// OSStatus is the error for a failed Security.framework call: the function
// name and the numeric OSStatus code it returned.
type OSStatus struct {
	call   string
	status int32
}

// Error formats the error as "<call> error: <code>".
func (s OSStatus) Error() string {
	return s.call + " error: " + strconv.Itoa(int(s.status))
}

// TrustResultOK reports whether a SecTrustEvaluate result means the chain is
// trusted (Proceed or Unspecified; every other value is a rejection).
func TrustResultOK(result CFRef) bool {
	r := int(result)
	return r == trustResultProceed || r == trustResultUnspecified
}

//go:cgo_import_dynamic x509_SecTrustCreateWithCertificates SecTrustCreateWithCertificates "/System/Library/Frameworks/Security.framework/Versions/A/Security"

// SecTrustCreateWithCertificates creates a trust object for certs (leaf
// first, then intermediates) checked under policies. The caller releases it.
func SecTrustCreateWithCertificates(certs CFRef, policies CFRef) (CFRef, error) {
	var trustObj CFRef
	ret := syscall(abi.FuncPCABI0(x509_SecTrustCreateWithCertificates_trampoline), uintptr(certs), uintptr(policies),
		uintptr(unsafe.Pointer(&trustObj)), 0, 0, 0)
	if int32(ret) != 0 {
		return 0, OSStatus{"SecTrustCreateWithCertificates", int32(ret)}
	}
	return trustObj, nil
}

// x509_SecTrustCreateWithCertificates_trampoline is implemented in
// security.s; it jumps to the framework function bound by the
// cgo_import_dynamic line above. Each *_trampoline below works the same way.
func x509_SecTrustCreateWithCertificates_trampoline()

//go:cgo_import_dynamic x509_SecCertificateCreateWithData SecCertificateCreateWithData "/System/Library/Frameworks/Security.framework/Versions/A/Security"

// SecCertificateCreateWithData turns DER bytes into a SecCertificateRef. The
// caller releases it.
func SecCertificateCreateWithData(b []byte) (CFRef, error) {
	data := BytesToCFData(b)
	defer CFRelease(data)
	ret := syscall(abi.FuncPCABI0(x509_SecCertificateCreateWithData_trampoline), kCFAllocatorDefault, uintptr(data), 0, 0, 0, 0)
	// Returns NULL if the data passed in the data parameter is not a valid
	// DER-encoded X.509 certificate.
	if ret == 0 {
		return 0, errors.New("SecCertificateCreateWithData: invalid certificate")
	}
	return CFRef(ret), nil
}

// x509_SecCertificateCreateWithData_trampoline is implemented in security.s.
func x509_SecCertificateCreateWithData_trampoline()

//go:cgo_import_dynamic x509_SecPolicyCreateSSL SecPolicyCreateSSL "/System/Library/Frameworks/Security.framework/Versions/A/Security"

// SecPolicyCreateSSL creates a policy for checking a TLS server certificate,
// bound to host name name when it is not empty.
func SecPolicyCreateSSL(name string) (CFRef, error) {
	var hostname CFString
	if name != "" {
		hostname = StringToCFString(name)
		defer CFRelease(CFRef(hostname))
	}
	ret := syscall(abi.FuncPCABI0(x509_SecPolicyCreateSSL_trampoline), 1 /* true */, uintptr(hostname), 0, 0, 0, 0)
	if ret == 0 {
		return 0, OSStatus{"SecPolicyCreateSSL", int32(ret)}
	}
	return CFRef(ret), nil
}

// x509_SecPolicyCreateSSL_trampoline is implemented in security.s.
func x509_SecPolicyCreateSSL_trampoline()

//go:cgo_import_dynamic x509_SecTrustSetVerifyDate SecTrustSetVerifyDate "/System/Library/Frameworks/Security.framework/Versions/A/Security"

// SecTrustSetVerifyDate makes the trust object check validity at dateRef
// instead of the current time.
func SecTrustSetVerifyDate(trustObj CFRef, dateRef CFRef) error {
	ret := syscall(abi.FuncPCABI0(x509_SecTrustSetVerifyDate_trampoline), uintptr(trustObj), uintptr(dateRef), 0, 0, 0, 0)
	if int32(ret) != 0 {
		return OSStatus{"SecTrustSetVerifyDate", int32(ret)}
	}
	return nil
}

// x509_SecTrustSetVerifyDate_trampoline is implemented in security.s.
func x509_SecTrustSetVerifyDate_trampoline()

//go:cgo_import_dynamic x509_SecTrustEvaluate SecTrustEvaluate "/System/Library/Frameworks/Security.framework/Versions/A/Security"

// SecTrustEvaluate runs the trust check and returns the SecTrustResultType
// value; pass it to TrustResultOK. An error means the check itself could not
// run. This call exists since macOS 10.3, unlike SecTrustEvaluateWithError.
func SecTrustEvaluate(trustObj CFRef) (CFRef, error) {
	var result CFRef
	ret := syscall(abi.FuncPCABI0(x509_SecTrustEvaluate_trampoline), uintptr(trustObj), uintptr(unsafe.Pointer(&result)), 0, 0, 0, 0)
	if int32(ret) != 0 {
		return 0, OSStatus{"SecTrustEvaluate", int32(ret)}
	}
	return CFRef(result), nil
}

// x509_SecTrustEvaluate_trampoline is implemented in security.s.
func x509_SecTrustEvaluate_trampoline()

//go:cgo_import_dynamic x509_SecCertificateCopyData SecCertificateCopyData "/System/Library/Frameworks/Security.framework/Versions/A/Security"

// SecCertificateCopyData returns the DER bytes of a SecCertificateRef.
func SecCertificateCopyData(cert CFRef) ([]byte, error) {
	ret := syscall(abi.FuncPCABI0(x509_SecCertificateCopyData_trampoline), uintptr(cert), 0, 0, 0, 0, 0)
	if ret == 0 {
		return nil, errors.New("x509: invalid certificate object")
	}
	b := CFDataToSlice(CFRef(ret))
	CFRelease(CFRef(ret))
	return b, nil
}

// x509_SecCertificateCopyData_trampoline is implemented in security.s.
func x509_SecCertificateCopyData_trampoline()

//go:cgo_import_dynamic x509_SecTrustGetCertificateCount SecTrustGetCertificateCount "/System/Library/Frameworks/Security.framework/Versions/A/Security"

// SecTrustGetCertificateCount returns the length of the chain built by the
// last evaluation. Together with SecTrustGetCertificateAtIndex it replaces
// SecTrustCopyCertificateChain, which needs macOS 12.
func SecTrustGetCertificateCount(trustObj CFRef) int {
	ret := syscall(abi.FuncPCABI0(x509_SecTrustGetCertificateCount_trampoline), uintptr(trustObj), 0, 0, 0, 0, 0)
	return int(ret)
}

// x509_SecTrustGetCertificateCount_trampoline is implemented in security.s.
func x509_SecTrustGetCertificateCount_trampoline()

//go:cgo_import_dynamic x509_SecTrustGetCertificateAtIndex SecTrustGetCertificateAtIndex "/System/Library/Frameworks/Security.framework/Versions/A/Security"

// SecTrustGetCertificateAtIndex returns a borrowed SecCertificateRef (do not CFRelease).
func SecTrustGetCertificateAtIndex(trustObj CFRef, i int) CFRef {
	ret := syscall(abi.FuncPCABI0(x509_SecTrustGetCertificateAtIndex_trampoline), uintptr(trustObj), uintptr(i), 0, 0, 0, 0)
	return CFRef(ret)
}

// x509_SecTrustGetCertificateAtIndex_trampoline is implemented in security.s.
func x509_SecTrustGetCertificateAtIndex_trampoline()
