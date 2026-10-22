# gooverlay

Подмены stdlib Darwin `crypto/x509` для `go build -overlay`.

Совпадает со строкой `go` в модуле (`1.26.0`). Dist `setup-go` берёт этот
GOROOT. `go build -overlay` не умеет подменять файлы в GOMODCACHE, overlay
нельзя нацелить на toolchain из кэша модулей.

Go 1.22+ биндит `SecTrustEvaluateWithError` (macOS 10.14) и
`SecTrustCopyCertificateChain` (12.0) через `//go:cgo_import_dynamic` даже при
`CGO_ENABLED=0`. На 10.13 dyld падает до `main`.

Эта копия берёт `SecTrustEvaluate` и `SecTrustGetCertificateCount` /
`SecTrustGetCertificateAtIndex` (с 10.3, в текущих SDK ещё есть) и оставляет
`loadSystemRoots` Go 1.26 в `root_darwin.go`. Линкер Go 1.26 по-прежнему
ставит `LC_BUILD_VERSION` 12.0 (флага `-macos` нет); 10.13 грузит такой
бинарник, когда символов Security из 12.0 уже нет.
