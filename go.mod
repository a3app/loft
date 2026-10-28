module github.com/a3app/loft

go 1.26.0

require (
	github.com/coder/websocket v1.8.15
	github.com/liyue201/goqr v0.0.0-20200803022322-df443203d4ea
	github.com/pkg/sftp v1.13.9
	github.com/quic-go/quic-go v0.59.1
	github.com/refraction-networking/utls v1.8.2
	github.com/songgao/water v0.0.0-20200317203138-2b4b6d7c09d8
	golang.org/x/crypto v0.58.0
	golang.org/x/net v0.61.0
	golang.org/x/sys v0.49.0
	golang.org/x/time v0.7.0
	gopkg.in/yaml.v3 v3.0.1
	gvisor.dev/gvisor v0.0.0-20250503011706-39ed1f5ac29c
	rsc.io/qr v0.2.0
)

require (
	github.com/andybalholm/brotli v1.0.6 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	golang.org/x/mobile v0.0.0-20261011034528-197770444867 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
)

tool (
	golang.org/x/mobile/cmd/gobind
	golang.org/x/mobile/cmd/gomobile
)

replace github.com/quic-go/quic-go => github.com/a3app/quic-go v0.59.1-loft.1
