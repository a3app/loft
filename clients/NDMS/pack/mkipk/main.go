// Command mkipk writes one Entware .ipk from a built loft-ndms binary.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/a3app/loft/clients/NDMS"
)

// main reads the binary, the init scripts and the sample YAML, then writes one .ipk.
func main() {
	bin := flag.String("bin", "", "loft-ndms binary")
	out := flag.String("out", "", "output .ipk path")
	ver := flag.String("version", "", "package version")
	arch := flag.String("arch", "", "opkg architecture (aarch64-3.10, armv7-3.2, mipsel-3.4)")
	init := flag.String("init", "clients/NDMS/pack/S99loft", "init script")
	pre := flag.String("prerm", "clients/NDMS/pack/prerm", "prerm")
	post := flag.String("postinst", "clients/NDMS/pack/postinst", "postinst")
	sample := flag.String("sample", "clients/NDMS/pack/client.yaml.sample", "sample yaml")
	flag.Parse()
	if *bin == "" || *out == "" || *ver == "" || *arch == "" {
		fmt.Fprintln(os.Stderr, "mkipk: -bin -out -version -arch")
		os.Exit(2)
	}
	meta := ndms.IPKMeta{Version: *ver, Arch: *arch}
	var err error
	if meta.Bin, err = os.ReadFile(*bin); err != nil {
		exit(err)
	}
	if meta.Init, err = os.ReadFile(*init); err != nil {
		exit(err)
	}
	if meta.Prerm, err = os.ReadFile(*pre); err != nil {
		exit(err)
	}
	if meta.Postinst, err = os.ReadFile(*post); err != nil {
		exit(err)
	}
	if meta.Sample, err = os.ReadFile(*sample); err != nil {
		exit(err)
	}
	if err := ndms.WriteIPK(*out, meta); err != nil {
		exit(err)
	}
}

// exit prints err and stops the packager with status 1.
func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
