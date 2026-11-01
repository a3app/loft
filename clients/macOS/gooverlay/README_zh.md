# gooverlay

用 `go build -overlay` 替换 Go 标准库 Darwin `crypto/x509`。

对齐模块里的 `go` 行（`1.26.0`）。Dist 的 `setup-go` 用这个 GOROOT。
`go build -overlay` 不能替换 GOMODCACHE 里的文件，所以 overlay 不要指向模块缓存里的 toolchain。

Go 1.22+ 即使 `CGO_ENABLED=0` 也会通过 `//go:cgo_import_dynamic` 绑定
`SecTrustEvaluateWithError`（macOS 10.14）和 `SecTrustCopyCertificateChain`（12.0）。
10.13 上 dyld 在 `main` 之前退出。

这份拷贝用 `SecTrustEvaluate` 以及 `SecTrustGetCertificateCount` /
`SecTrustGetCertificateAtIndex`（10.3 起，当前 SDK 仍在），并保留 Go 1.26 的
`loadSystemRoots` 在 `root_darwin.go`。Go 1.26 链接器仍写 `LC_BUILD_VERSION` 12.0
（没有 `-macos` 标志）；去掉 12.0 的 Security 符号后，10.13 会加载这个二进制。
