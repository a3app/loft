// Command mkipk writes one Entware .ipk from a built loft-keenetic binary.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/a3app/loft/clients/Keenetic"
)

func main() {
	bin := flag.String("bin", "", "loft-keenetic binary")
	out := flag.String("out", "", "output .ipk path")
	ver := flag.String("version", "", "package version")
	arch := flag.String("arch", "", "opkg architecture (aarch64-3.10, armv7-3.2, mipsel-3.4)")
	init := flag.String("init", "clients/Keenetic/pack/S99loft", "init script")
	pre := flag.String("prerm", "clients/Keenetic/pack/prerm", "prerm")
	post := flag.String("postinst", "clients/Keenetic/pack/postinst", "postinst")
	sample := flag.String("sample", "clients/Keenetic/pack/client.yaml.sample", "sample yaml")
	flag.Parse()
	if *bin == "" || *out == "" || *ver == "" || *arch == "" {
		fmt.Fprintln(os.Stderr, "mkipk: -bin -out -version -arch")
		os.Exit(2)
	}
	meta := keenetic.IPKMeta{Version: *ver, Arch: *arch}
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
	if err := keenetic.WriteIPK(*out, meta); err != nil {
		exit(err)
	}
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
