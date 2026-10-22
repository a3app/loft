# gooverlay

`go build -overlay` replacements for Go stdlib Darwin `crypto/x509`.

Go 1.22+ binds `SecTrustEvaluateWithError` (macOS 10.14) and
`SecTrustCopyCertificateChain` (12.0) through `//go:cgo_import_dynamic` even
with `CGO_ENABLED=0`. dyld then aborts on 10.13 before `main`.

This copy uses `SecTrustEvaluate` plus `SecTrustGetCertificateCount` /
`SecTrustGetCertificateAtIndex` (10.3+, still in current SDKs). The macOS
Makefile stamps `LC_BUILD_VERSION` with `-macos=10.13`.
