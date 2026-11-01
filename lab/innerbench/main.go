// Command innerbench measures Seal+Open of loft inner AEADs on this CPU.
// Payload 12 KiB is mux streamChunk; 1 MiB is bulk. GOMAXPROCS=1.
//
//	go run ./lab/innerbench
package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
	"golang.org/x/sys/cpu"
)

type spec struct {
	name string
	id   byte
}

func main() {
	runtime.GOMAXPROCS(1)
	fmt.Printf("cpu %s\n", cpuModel())
	fmt.Printf("go %s %s/%s GOMAXPROCS=1 AES=%v AVX2=%v PCLMUL=%v\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH,
		cpu.X86.HasAES, cpu.X86.HasAVX2, cpu.X86.HasPCLMULQDQ)
	fmt.Printf("hiae=%v\n", ecrypto.HiAEEnabled())

	ciphers := []spec{
		{"chacha", wire.InnerChaCha},
		{"aes", wire.InnerAES},
	}
	if ecrypto.HiAEEnabled() {
		ciphers = append(ciphers, spec{"hiae", wire.InnerHiAE})
	}

	sizes := []int{12 << 10, 1 << 20}
	dur := 3 * time.Second
	fmt.Printf("%-8s %8s %10s %10s %10s %8s\n", "cipher", "size", "seal_MB/s", "open_MB/s", "both_Gbit", "n")
	for _, sz := range sizes {
		payload := bytes.Repeat([]byte{0x5a}, sz)
		for _, c := range ciphers {
			if !ecrypto.InnerOK(c.id) {
				fmt.Printf("%-8s %8s skipped (not built)\n", c.name, sizeName(sz))
				continue
			}
			key := make([]byte, ecrypto.KeySize)
			if _, err := rand.Read(key); err != nil {
				fmt.Fprintf(os.Stderr, "rand: %v\n", err)
				os.Exit(1)
			}
			if c.id != wire.InnerChaCha {
				ecrypto.BindInner(key, c.id)
			}
			sealMBs, openMBs, bothGbit, n := bench(key, payload, dur)
			fmt.Printf("%-8s %8s %10.1f %10.1f %10.2f %8d\n",
				c.name, sizeName(sz), sealMBs, openMBs, bothGbit, n)
		}
	}
}

func bench(key, payload []byte, d time.Duration) (sealMBs, openMBs, bothGbit float64, n int) {
	for i := 0; i < 32; i++ {
		s, err := ecrypto.Seal(key, payload, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "seal warmup: %v\n", err)
			os.Exit(1)
		}
		p, err := ecrypto.Open(key, s, nil)
		if err != nil || !bytes.Equal(p, payload) {
			fmt.Fprintf(os.Stderr, "open warmup: %v\n", err)
			os.Exit(1)
		}
	}
	var sealNs, openNs int64
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		t0 := time.Now()
		s, err := ecrypto.Seal(key, payload, nil)
		sealNs += time.Since(t0).Nanoseconds()
		if err != nil {
			fmt.Fprintf(os.Stderr, "seal: %v\n", err)
			os.Exit(1)
		}
		t1 := time.Now()
		p, err := ecrypto.Open(key, s, nil)
		openNs += time.Since(t1).Nanoseconds()
		if err != nil || !bytes.Equal(p, payload) {
			fmt.Fprintf(os.Stderr, "open: %v\n", err)
			os.Exit(1)
		}
		n++
	}
	if n == 0 || sealNs == 0 || openNs == 0 {
		return 0, 0, 0, n
	}
	bytesN := float64(len(payload)) * float64(n)
	sealMBs = bytesN / (float64(sealNs) / 1e9) / 1e6
	openMBs = bytesN / (float64(openNs) / 1e9) / 1e6
	bothGbit = 8 * bytesN / (float64(sealNs+openNs) / 1e9) / 1e9
	return
}

func sizeName(n int) string {
	if n%(1<<20) == 0 {
		return fmt.Sprintf("%dMiB", n>>20)
	}
	if n%1024 == 0 {
		return fmt.Sprintf("%dKiB", n>>10)
	}
	return fmt.Sprintf("%d", n)
}

func cpuModel() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return runtime.GOARCH
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return runtime.GOARCH
}
