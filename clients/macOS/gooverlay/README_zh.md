# gooverlay

用 `go build -overlay` 替换 Go 标准库 Darwin `crypto/x509`。

Go 1.22+ 即使 `CGO_ENABLED=0` 也会通过 `//go:cgo_import_dynamic` 绑定
`SecTrustEvaluateWithError`（macOS 10.14）和 `SecTrustCopyCertificateChain`（12.0）。
10.13 上 dyld 在 `main` 之前退出。

这份拷贝用 `SecTrustEvaluate` 以及 `SecTrustGetCertificateCount` /
`SecTrustGetCertificateAtIndex`（10.3 起，当前 SDK 仍在）。macOS Makefile
用 `-macos=10.13` 写 `LC_BUILD_VERSION`。
