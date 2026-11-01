# gooverlay

`go build -overlay` replacements for Go stdlib Darwin `crypto/x509`.

Matched to the `go` line in the module (`1.26.0`). Dist `setup-go` uses
that GOROOT. `go build -overlay` cannot replace files under GOMODCACHE,
so do not point the overlay at a toolchain downloaded into the module cache.

Go 1.22+ binds `SecTrustEvaluateWithError` (macOS 10.14) and
`SecTrustCopyCertificateChain` (12.0) through `//go:cgo_import_dynamic` even
with `CGO_ENABLED=0`. dyld then aborts on 10.13 before `main`.

This copy uses `SecTrustEvaluate` plus `SecTrustGetCertificateCount` /
`SecTrustGetCertificateAtIndex` (10.3+, still in current SDKs) and keeps
Go 1.26 `loadSystemRoots` in `root_darwin.go`. The Go 1.26 linker still
stamps `LC_BUILD_VERSION` 12.0 (no `-macos` flag); 10.13 loads that binary
once the 12.0 Security symbols are gone.
