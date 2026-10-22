# gooverlay

Подмены stdlib Darwin `crypto/x509` для `go build -overlay`.

Go 1.22+ биндит `SecTrustEvaluateWithError` (macOS 10.14) и
`SecTrustCopyCertificateChain` (12.0) через `//go:cgo_import_dynamic` даже при
`CGO_ENABLED=0`. На 10.13 dyld падает до `main`.

Эта копия берёт `SecTrustEvaluate` и `SecTrustGetCertificateCount` /
`SecTrustGetCertificateAtIndex` (с 10.3, в текущих SDK ещё есть). Makefile
macOS ставит `LC_BUILD_VERSION` флагом `-macos=10.13`.
