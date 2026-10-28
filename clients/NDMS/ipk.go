package ndms

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"
)

// IPKMeta is one Entware package. Arch is the opkg architecture
// (aarch64-3.10, armv7-3.2, mipsel-3.4), not the asset name.
type IPKMeta struct {
	Version  string
	Arch     string
	Bin      []byte
	Init     []byte
	Sample   []byte
	Prerm    []byte
	Postinst []byte
}

// WriteIPK writes a SysV ar of debian-binary, control.tar.gz and data.tar.gz.
func WriteIPK(path string, m IPKMeta) error {
	if m.Version == "" || m.Arch == "" || len(m.Bin) == 0 {
		return fmt.Errorf("ipk: version, arch and binary")
	}
	ctrl := tarGzip([]tarFile{
		{Name: "control", Mode: 0o644, Body: []byte(controlText(m))},
		{Name: "prerm", Mode: 0o755, Body: m.Prerm},
		{Name: "postinst", Mode: 0o755, Body: m.Postinst},
	})
	data := tarGzip([]tarFile{
		{Name: "opt/", Mode: 0o755, Dir: true},
		{Name: "opt/bin/", Mode: 0o755, Dir: true},
		{Name: "opt/etc/", Mode: 0o755, Dir: true},
		{Name: "opt/etc/init.d/", Mode: 0o755, Dir: true},
		{Name: "opt/etc/loft/", Mode: 0o755, Dir: true},
		{Name: "opt/bin/loft-ndms", Mode: 0o755, Body: m.Bin},
		{Name: "opt/etc/init.d/S99loft", Mode: 0o755, Body: m.Init},
		{Name: "opt/etc/loft/client.yaml.sample", Mode: 0o644, Body: m.Sample},
	})
	var buf bytes.Buffer
	if err := writeAR(&buf, []arFile{
		{Name: "debian-binary", Body: []byte("2.0\n")},
		{Name: "control.tar.gz", Body: ctrl},
		{Name: "data.tar.gz", Body: data},
	}); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// controlText is the opkg control file. The description's second line
// is indented: that is the continuation opkg's RFC 822 parser requires.
// Maintainer is a name only. The package has no Depends line.
func controlText(m IPKMeta) string {
	return fmt.Sprintf(`Package: loft
Version: %s
Architecture: %s
Maintainer: A3
Section: net
Source: https://github.com/a3app/loft
Description: loft client for KeeneticOS
 Each connection is an OpkgTun. The LAN page listens on port 8090.
`, m.Version, m.Arch)
}

// tarFile is one member of control.tar.gz or data.tar.gz.
// Directory members have a trailing slash and no body. Paths have no
// leading slash; opkg extracts them under the filesystem root.
type tarFile struct {
	Name string
	Mode int64
	Body []byte
	Dir  bool
}

// tarGzip is a gzip-compressed ustar archive. Directory entries are
// written before the files that live in them.
func tarGzip(files []tarFile) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: f.Name, Mode: mode}
		if f.Dir {
			h.Typeflag = tar.TypeDir
		} else {
			h.Typeflag = tar.TypeReg
			h.Size = int64(len(f.Body))
		}
		_ = tw.WriteHeader(h)
		if !f.Dir {
			_, _ = tw.Write(f.Body)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// arFile is one System V ar member. Name is without the trailing slash
// that writeAR appends to mark a System V name.
type arFile struct {
	Name string
	Body []byte
}

// writeAR writes a System V archive, magic "!<arch>\n" and a 60-byte header
// per member. The size field is the unpadded body length. An odd-sized
// member is followed by a newline that is not part of the member; opkg
// skips that byte because size%2 == 1. Counting the pad inside the size
// makes the next gzip reader treat the newline as a second member and fail.
func writeAR(w io.Writer, files []arFile) error {
	if _, err := io.WriteString(w, "!<arch>\n"); err != nil {
		return err
	}
	for _, f := range files {
		name := f.Name + "/"
		if len(name) > 16 {
			return fmt.Errorf("ar name %s", f.Name)
		}
		size := len(f.Body)
		hdr := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8s%-10d`\n", name, 0, 0, 0, "100644", size)
		if len(hdr) != 60 {
			return fmt.Errorf("ar header %d", len(hdr))
		}
		if _, err := io.WriteString(w, hdr); err != nil {
			return err
		}
		if _, err := w.Write(f.Body); err != nil {
			return err
		}
		if size%2 == 1 {
			if _, err := w.Write([]byte{'\n'}); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReadARNames returns member names in order. Used by the package test.
func ReadARNames(b []byte) ([]string, error) {
	if !strings.HasPrefix(string(b), "!<arch>\n") {
		return nil, fmt.Errorf("ar magic")
	}
	rest := b[len("!<arch>\n"):]
	var names []string
	for len(rest) > 0 {
		if len(rest) < 60 {
			return nil, fmt.Errorf("ar short")
		}
		hdr := rest[:60]
		rest = rest[60:]
		name := strings.TrimRight(strings.TrimSpace(string(hdr[0:16])), "/")
		var size int
		if _, err := fmt.Sscanf(string(hdr[48:58]), "%d", &size); err != nil {
			return nil, err
		}
		if size < 0 || size > len(rest) {
			return nil, fmt.Errorf("ar size")
		}
		rest = rest[size:]
		if size%2 == 1 && len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		}
		names = append(names, name)
	}
	return names, nil
}
