// Command loft-panel is the web admin for one loft server. It runs as its own
// unprivileged user behind nginx and asks loft-panel-adm (root) to change the
// node. Subcommands: serve (default), passwd LOGIN, users.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/auth"
	"github.com/a3app/loft/server/panel/store"
	"github.com/a3app/loft/server/panel/web"
)

const defaultData = "/var/lib/loft-panel"

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "passwd":
		err = passwd(args)
	case "users":
		err = users(args)
	case "version":
		fmt.Println("loft-panel", version.Version)
	default:
		err = fmt.Errorf("unknown command %q (serve, passwd, users, version)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "loft-panel:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8091", "HTTP listen address (nginx proxies to it)")
	data := fs.String("data", defaultData, "data directory (users.json, setup.code)")
	sock := fs.String("adm", admproto.SocketPath, "loft-panel-adm socket")
	hosts := fs.String("hosts", "", "comma-separated names the panel answers to; empty = any")
	ui := fs.String("ui", "", "serve the UI from this directory instead of the built-in copy (development)")
	acmeDir := fs.String("acme", web.LetsEncrypt, "ACME directory URL (Let's Encrypt staging for tests)")
	showVer := fs.Bool("version", false, "print version and exit")
	fs.Parse(args)
	if *showVer {
		fmt.Println("loft-panel", version.Version)
		return nil
	}
	lg := log.New(os.Stderr, "", 0)
	if err := os.MkdirAll(*data, 0o700); err != nil {
		return err
	}
	st, err := store.Open(*data)
	if err != nil {
		return err
	}
	sess := auth.NewSessions()
	settings := store.OpenSettings(*data)
	if cur, err := settings.Get(); err == nil {
		sess.SetLimits(web.SessionLimits(cur))
	}
	if err := sess.Persist(filepath.Join(*data, "sessions.json")); err != nil {
		return err
	}
	srv := &web.Server{Store: st, Sessions: sess, Limit: auth.NewLimiter(), UIDir: *ui, Log: lg,
		Settings: settings, Audit: store.OpenAudit(*data), ACME: &web.ACME{Dir: *data, URL: *acmeDir},
		Adm: func(by string) web.Adm { return admproto.Client{Path: *sock, By: by} }}
	srv.SetSeenFile(*data)
	srv.SetDomainsFile(*data)
	srv.SetBackupDir(*data)
	srv.LoadGuard()
	for _, h := range strings.Split(*hosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			srv.Hosts = append(srv.Hosts, h)
		}
	}
	if !st.Empty() {
		os.Remove(filepath.Join(*data, "setup.code")) // an owner exists: the code is spent
	}
	if st.Empty() {
		code, err := setupCode(*data)
		if err != nil {
			return err
		}
		srv.SetupCode, srv.SetupFile = code, filepath.Join(*data, "setup.code")
		lg.Printf("no owner yet: open the panel and enter the setup code from %s", filepath.Join(*data, "setup.code"))
	}
	if *ui != "" {
		lg.Printf("WARNING: UI served from %s, not the built-in copy", *ui)
	}
	h, err := srv.Handler()
	if err != nil {
		return err
	}
	hs := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 120 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	go srv.Renew(ctx)
	go srv.FollowReleases(ctx)
	go srv.Watch(ctx)
	lg.Printf("loft-panel %s on %s", version.Version, *listen)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(sctx)
}

// setupCode reads or creates data/setup.code (0600): two groups of six hex.
func setupCode(dir string) (string, error) {
	p := filepath.Join(dir, "setup.code")
	if b, err := os.ReadFile(p); err == nil {
		if c := strings.TrimSpace(string(b)); c != "" {
			return c, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	h := hex.EncodeToString(b)
	code := h[:6] + "-" + h[6:]
	return code, store.WriteAtomic(p, []byte(code+"\n"), 0o600)
}

// passwd sets LOGIN's password, creating the user (owner if the panel has
// none yet) — the way back in when every password is lost.
func passwd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	data := fs.String("data", defaultData, "data directory")
	role := fs.String("role", "", "role for a new user: owner, operator, viewer (default: owner if none, else viewer)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: loft-panel passwd [-role R] LOGIN")
	}
	login := fs.Arg(0)
	st, err := store.Open(*data)
	if err != nil {
		return err
	}
	// Run as root, the rewrite would leave users.json owned by root and
	// unreadable for the panel: hand it back to the data directory's owner.
	defer keepOwner(*data, "users.json")
	pw, err := readPassword()
	if err != nil {
		return err
	}
	h, err := auth.Hash(pw)
	if err != nil {
		return err
	}
	if _, err := st.Get(login); errors.Is(err, store.ErrNotFound) {
		r := *role
		if r == "" {
			r = store.RoleViewer
			if st.Empty() {
				r = store.RoleOwner
			}
		}
		if err := st.Add(store.User{Login: login, Role: r, Hash: h}); err != nil {
			return err
		}
		os.Remove(filepath.Join(*data, "setup.code"))
		fmt.Printf("created %s (%s)\n", login, r)
	} else if err != nil {
		return err
	} else {
		if err := st.Update(login, func(u *store.User) {
			u.Hash = h
			u.Disabled = false
			if *role != "" {
				u.Role = *role
			}
		}); err != nil {
			return err
		}
		fmt.Printf("password of %s changed; restart loft-panel to sign out its sessions\n", login)
	}
	return nil
}

func keepOwner(dir, name string) {
	st, err := os.Stat(dir)
	if err != nil {
		return
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		os.Chown(filepath.Join(dir, name), int(s.Uid), int(s.Gid))
	}
}

// readPassword asks twice on a terminal (echo off via stty), or reads one line from a pipe.
func readPassword() (string, error) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	in := bufio.NewReader(os.Stdin)
	if fi.Mode()&os.ModeCharDevice == 0 {
		s, err := in.ReadString('\n')
		if err != nil && s == "" {
			return "", errors.New("no password on stdin")
		}
		return strings.TrimRight(s, "\r\n"), nil
	}
	stty := func(a ...string) {
		c := exec.Command("stty", a...)
		c.Stdin = os.Stdin
		c.Run()
	}
	stty("-echo")
	defer stty("echo")
	ask := func(p string) (string, error) {
		fmt.Fprint(os.Stderr, p)
		s, err := in.ReadString('\n')
		fmt.Fprintln(os.Stderr)
		return strings.TrimRight(s, "\r\n"), err
	}
	a, err := ask("New password: ")
	if err != nil {
		return "", err
	}
	b, err := ask("Again: ")
	if err != nil {
		return "", err
	}
	if a != b {
		return "", errors.New("passwords do not match")
	}
	return a, nil
}

func users(args []string) error {
	fs := flag.NewFlagSet("users", flag.ExitOnError)
	data := fs.String("data", defaultData, "data directory")
	fs.Parse(args)
	st, err := store.Open(*data)
	if err != nil {
		return err
	}
	for _, u := range st.List() {
		state := "enabled"
		if u.Disabled {
			state = "disabled"
		}
		seen := "never"
		if !u.Seen.IsZero() {
			seen = u.Seen.Format(time.RFC3339)
		}
		fmt.Printf("%-20s %-9s %-8s %s\n", u.Login, u.Role, state, seen)
	}
	return nil
}
