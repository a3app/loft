// Command devstand runs the real panel and the real adm ops on a laptop:
// node files live in a temp directory and systemctl is faked (the "node"
// accepts any config unless it contains "# crash"). Development only.
//
//	go run ./server/panel/devstand [-listen 127.0.0.1:8091] [-ui server/panel/web/ui]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/server/panel/adm"
	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/auth"
	"github.com/a3app/loft/server/panel/store"
	"github.com/a3app/loft/server/panel/web"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8091", "HTTP address")
	ui := flag.String("ui", "", "UI directory (default: built-in)")
	flag.Parse()
	lg := log.New(os.Stderr, "devstand: ", 0)

	root, err := os.MkdirTemp("", "loft-devstand-")
	if err != nil {
		lg.Fatal(err)
	}
	node := filepath.Join(root, "node")
	// The panel's data where the helper's jobs look for it (fs/ is the
	// faked system root).
	data := filepath.Join(root, "fs", "var/lib/loft-panel")
	os.MkdirAll(node, 0o755)
	os.MkdirAll(data, 0o700)
	seed(node)

	var mu sync.Mutex
	state := "active"
	etc := filepath.Join(root, "etc")
	os.MkdirAll(filepath.Join(etc, "tls"), 0o755)
	os.WriteFile(filepath.Join(etc, "node.conf"), []byte("role controller\nlisten :443\nget_path /static/js\nadvertise https://vpn.example.net\ndecoy 127.0.0.1:8443\ndecoy_proxy enable\n"), 0o640)
	os.WriteFile(filepath.Join(etc, "panel.env"), []byte("PANEL_HOSTS=vpn.example.net\n"), 0o640)
	os.WriteFile(filepath.Join(node, "controller.seed"), []byte("devstand-controller-seed-32bytes"), 0o600)
	ops := &adm.Ops{Dir: node, Unit: "loft-node.service", UID: -1, GID: -1, Settle: 300 * time.Millisecond,
		Etc: etc, PanelEnv: filepath.Join(etc, "panel.env"), NginxSite: filepath.Join(etc, "nginx-site.conf"), NginxTest: func() error { return nil }, PanelTLS: filepath.Join(etc, "panel-tls"),
		Systemctl: func(_ context.Context, args ...string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			switch args[0] {
			case "restart":
				b, _ := os.ReadFile(filepath.Join(node, "network.conf"))
				state = "active"
				if strings.Contains(string(b), "# crash") {
					state = "failed"
				}
				lg.Printf("systemctl restart → %s", state)
			case "is-active":
				return state, nil
			case "show":
				return "ActiveState=" + state + "\nActiveEnterTimestamp=" + time.Now().Format(time.RFC1123), nil
			}
			return "", nil
		}}
	// The mail server is "installed": its packages answer dpkg-query, and
	// the helper's host commands are faked; its files land under fs/.
	fs := filepath.Join(root, "fs")
	os.MkdirAll(filepath.Join(fs, "usr/sbin"), 0o755)
	os.WriteFile(filepath.Join(fs, "usr/sbin/postconf"), nil, 0o755)
	os.MkdirAll(filepath.Join(fs, "etc/dovecot/conf.d"), 0o755)
	os.WriteFile(filepath.Join(fs, "etc/dovecot/conf.d/10-auth.conf"), []byte("!include auth-system.conf.ext\n"), 0o644)
	os.MkdirAll(filepath.Join(fs, "usr/bin"), 0o755)
	os.WriteFile(filepath.Join(fs, "usr/bin/fail2ban-client"), nil, 0o755)
	ops.Root, ops.PanelEtc, ops.State = fs, filepath.Join(fs, "etc/loft-panel"), filepath.Join(root, "adm-state")
	// Jobs (backups, packages) run in this process instead of a unit.
	ops.SystemdRun = func(context.Context, ...string) error {
		go func() {
			if err := ops.RunJob(context.Background()); err != nil {
				lg.Printf("job: %v", err)
			}
		}()
		return nil
	}
	mailPkgs := map[string]bool{"postfix": true, "dovecot-imapd": true, "dovecot-pop3d": true, "dovecot-lmtpd": true, "dovecot-sieve": true, "dovecot-managesieved": true, "opendkim": true, "opendkim-tools": true, "fail2ban": true, "python3-systemd": true}
	ops.Exec = func(_ context.Context, name string, args ...string) (string, error) {
		switch name {
		case "dpkg-query":
			var b strings.Builder
			for _, a := range args {
				if mailPkgs[a] {
					fmt.Fprintf(&b, "%s\tii \t1:2.3.21-dev\t2048\n", a)
				}
			}
			return b.String(), nil
		case "dovecot":
			return "2.3.21 (devstand)\n", nil
		case "hostname":
			return "devstand.example\n", nil
		case "systemctl":
			if len(args) > 0 && args[0] == "is-active" {
				return strings.Repeat("active\n", len(args)-1), nil
			}
		case "sh":
			if len(args) > 1 && strings.Contains(args[1], "getent passwd; echo @@") {
				return devAccounts, nil
			}
			if len(args) > 1 && strings.Contains(args[1], "useradd") {
				return "118\n124\n", nil
			}
			if len(args) > 1 && strings.Contains(args[1], "postqueue") {
				return "0\n@@\n", nil
			}
		}
		return "", nil
	}
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("lpdev-%d.sock", os.Getpid()))
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		lg.Fatal(err)
	}
	defer os.Remove(sock)
	go (&adm.Server{Ops: ops, Log: lg}).Serve(ln)

	st, _ := store.Open(data)
	srv := &web.Server{Store: st, Sessions: auth.NewSessions(), Limit: auth.NewLimiter(), SetupCode: "000000-000000", UIDir: *ui, Log: lg,
		Settings: store.OpenSettings(data), ACME: &web.ACME{Dir: data},
		Adm: func(by string) web.Adm { return admproto.Client{Path: sock, By: by} }}
	srv.SetBackupDir(data)
	h, err := srv.Handler()
	if err != nil {
		lg.Fatal(err)
	}
	lg.Printf("node files in %s; setup code 000000-000000; http://%s/", node, *listen)
	lg.Fatal(http.ListenAndServe(*listen, h))
}

// seed writes a small network like the mock's, with real keys.
func seed(dir string) {
	var b strings.Builder
	b.WriteString("network main\nname Основная\nenable\nipif overlay 10.77.0.0/16\n  gateway 10.77.0.1\n  mtu 1280\n  peers vlan\n  dns enable\n  dns upstream https://1.1.1.1/dns-query\n")
	b.WriteString("create vlan 10 name family\nconfig vlan 10 cidr 10.77.10.0/24\ncreate vlan 20 name office\nconfig vlan 20 cidr 10.77.20.0/24\ncreate vlan 30 name guest\nconfig vlan 30 cidr 10.77.30.0/24\nconfig vlan 30 peers hub\n")
	keys := ""
	for _, m := range []struct {
		id, name, ip string
		vlan         int
	}{{"alice-phone", "Alice", "10.77.10.14", 10}, {"office-kn", "Office router", "10.77.20.2", 20}, {"bob", "Bob", "10.77.20.21", 20}, {"guest-01", "Guest", "10.77.30.40", 30}} {
		k, sha, _ := identity.NewKey()
		keys += m.id + ": " + k + "\n"
		fmt.Fprintf(&b, "create member %s\nconfig member %s name %s\nconfig member %s ipaddress %s\nconfig member %s vlan %d\nconfig member %s key_sha256 %s\nenable member %s\n",
			m.id, m.id, m.name, m.id, m.ip, m.id, m.vlan, m.id, sha, m.id)
	}
	b.WriteString("ip access-list overlay\n  permit tcp any any eq 445 3389 vlan 20 10\n  deny ip any any vlan 30\n")
	os.WriteFile(filepath.Join(dir, "network.conf"), []byte(b.String()), 0o640)
	os.WriteFile(filepath.Join(dir, "members.keys"), []byte(keys), 0o600)
}

// devAccounts is what the account screen reads on the stand.
const devAccounts = "root:x:0:0:root:/root:/bin/bash\ndeploy:x:1000:1000::/home/deploy:/bin/bash\nbob:x:1001:1001::/home/bob:/bin/bash\n" +
	"@@sudo:x:27:deploy\n@@root:!:\ndeploy:$6$x$y:\nbob:!:19000\n@@passwordauthentication no\npermitrootlogin prohibit-password\n@@\n" +
	"@@##root\nssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGb0vd7Rb6ULYhvSL6y5mU9kKqPB7cH6P3eq0hGg0Gbq root@laptop\n##deploy\nssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIC+Q7dKX3qQ4pqA7m8y1nJm2cU3rQ7hTt8m5w0lU0aBz deploy@laptop\n##bob\n"
