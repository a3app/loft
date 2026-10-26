# gooverlay

Подменённые файлы Darwin-части `crypto/x509` из stdlib для `go build -overlay`.

Версия совпадает со строкой `go` в модуле (`1.26.0`). Сборка dist через `setup-go` использует
этот GOROOT. `go build -overlay` не умеет подменять файлы в GOMODCACHE, поэтому overlay
нельзя направлять на toolchain, скачанный в кэш модулей.

Go 1.22+ привязывает `SecTrustEvaluateWithError` (macOS 10.14) и
`SecTrustCopyCertificateChain` (12.0) через `//go:cgo_import_dynamic` даже при
`CGO_ENABLED=0`. Из-за этого на 10.13 dyld падает ещё до `main`.

Эта копия использует `SecTrustEvaluate` и `SecTrustGetCertificateCount` /
`SecTrustGetCertificateAtIndex` (доступны с 10.3 и всё ещё есть в текущих SDK) и сохраняет
`loadSystemRoots` из Go 1.26 в `root_darwin.go`. Линкер Go 1.26 по-прежнему
прописывает `LC_BUILD_VERSION` 12.0 (флага `-macos` нет), но 10.13 загружает такой
бинарник, если в нём не осталось символов Security из 12.0.
