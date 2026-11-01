// Command loft-panel-adm is the root helper of loft-panel. It listens on a
// unix socket that only the panel's user may use (file mode and SO_PEERCRED),
// and does a short, fixed list of node operations (server/panel/admproto).
//
//	loft-panel-adm [flags]                 serve the socket (systemd unit)
//	loft-panel-adm wire-node [-conf] [-decoy]  point node decoy at the panel's nginx (installer)
//	loft-panel-adm nginx-site                  write the panel's nginx site from node.conf and panel.env (installer)
//	loft-panel-adm net-revert -id ID           undo a network change nobody confirmed (a transient timer)
//	loft-panel-adm net-boot                    apply /etc/loft-panel/net.json (loft-panel-net.service)
//	loft-panel-adm run-job                     do the job in job.json (the transient unit loft-panel-job)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/server/panel/adm"
	"github.com/a3app/loft/server/panel/admproto"
)

// main runs one of the one-shot subcommands named in the package doc, or,
// with no subcommand, serves the socket for the panel until a signal.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "wire-node" {
		wireNode(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "nginx-site" {
		nginxSite(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "net-revert" || os.Args[1] == "net-boot") {
		netCmd(os.Args[1], os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "run-job" {
		runJob(os.Args[2:])
		return
	}
	sock := flag.String("socket", admproto.SocketPath, "socket path")
	panelUser := flag.String("panel-user", "loft-panel", "the only user allowed to connect")
	nodeUser := flag.String("node-user", "loft", "owner of the node's files")
	dir := flag.String("dir", "/var/lib/loft", "node data directory (network.conf, members.keys)")
	nodeBin := flag.String("node-bin", "/usr/bin/loft-node", "loft-node binary, for its version")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVer {
		fmt.Println("loft-panel-adm", version.Version)
		return
	}
	lg := log.New(os.Stderr, "", 0)
	if err := run(*sock, *panelUser, *nodeUser, *dir, *nodeBin, lg); err != nil {
		lg.Fatalf("loft-panel-adm: %v", err)
	}
}

// lookup returns the numeric user and primary group IDs of a system user.
func lookup(name string) (uid, gid int, err error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, err
	}
	uid, _ = strconv.Atoi(u.Uid)
	gid, _ = strconv.Atoi(u.Gid)
	return uid, gid, nil
}

// run is the helper's main loop. It must run as root. It creates the unix
// socket so that only root and the panel's group can reach it (directory
// 0750, socket 0660), and accepts only connections whose peer is the panel
// user. Node files it writes get the node user as owner. It returns when the
// socket is closed by SIGINT or SIGTERM.
func run(sock, panelUser, nodeUser, dir, nodeBin string, lg *log.Logger) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("must run as root")
	}
	// 1. Resolve the panel and node users to numeric IDs.
	puid, pgid, err := lookup(panelUser)
	if err != nil {
		return fmt.Errorf("panel user: %w", err)
	}
	nuid, ngid, err := lookup(nodeUser)
	if err != nil {
		return fmt.Errorf("node user: %w", err)
	}
	ops := adm.Defaults()
	ops.Dir, ops.UID, ops.GID, ops.NodeBin = dir, nuid, ngid, nodeBin

	// 2. Create the socket with tight ownership and mode; a stale socket
	// file from an earlier run is removed first.
	sd := filepath.Dir(sock)
	if err := os.MkdirAll(sd, 0o750); err != nil {
		return err
	}
	if err := os.Chown(sd, 0, pgid); err != nil {
		return err
	}
	os.Chmod(sd, 0o750)
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	if err := os.Chown(sock, 0, pgid); err != nil {
		ln.Close()
		return err
	}
	if err := os.Chmod(sock, 0o660); err != nil {
		ln.Close()
		return err
	}
	// 3. Close the listener on a signal, which ends Serve below.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		ln.Close()
	}()
	// 4. Serve requests; a background loop re-applies routing every 20 s.
	lg.Printf("loft-panel-adm %s on %s for %s (uid %d)", version.Version, sock, panelUser, puid)
	srv := &adm.Server{Ops: ops, Allow: adm.PeerUID(puid), Log: lg}
	// Channel interfaces come and go: keep their routing tables in place.
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			ops.Reconcile(ctx)
			cancel()
			time.Sleep(20 * time.Second)
		}
	}()
	err = srv.Serve(ln)
	os.Remove(sock)
	return err
}

// wireNode is the installer's step: exit 0 with a note when nothing can or
// needs to change, 10 when node.conf changed (the node must restart), 1 on error.
func wireNode(args []string) {
	fs := flag.NewFlagSet("wire-node", flag.ExitOnError)
	conf := fs.String("conf", "/etc/loft/node.conf", "node.conf")
	decoy := fs.String("decoy", "127.0.0.1:8443", "the panel's nginx")
	fs.Parse(args)
	changed, note, err := adm.WireNode(*conf, *decoy)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wire-node:", err)
		os.Exit(1)
	}
	fmt.Println(note)
	if changed {
		os.Exit(10)
	}
}

// nginxSite writes /etc/nginx/sites-available/loft-panel.conf (and the
// sites-enabled link) for the names in node.conf and panel.env, checked with
// nginx -t. The caller reloads nginx.
func nginxSite(args []string) {
	fs := flag.NewFlagSet("nginx-site", flag.ExitOnError)
	etc := fs.String("etc", "/etc/loft", "node config directory")
	fs.Parse(args)
	ops := adm.Defaults()
	ops.Etc = *etc
	if _, err := ops.WriteSite(); err != nil {
		fmt.Fprintln(os.Stderr, "nginx-site:", err)
		os.Exit(1)
	}
	fmt.Println("wrote /etc/nginx/sites-available/loft-panel.conf")
}

// runJob is started by the helper through systemd-run, so it lives outside
// the helper's sandbox and outlives restarts of the panel and the helper.
func runJob(args []string) {
	fs := flag.NewFlagSet("run-job", flag.ExitOnError)
	state := fs.String("state", "/var/lib/loft-panel-adm", "job.json and job.log")
	dir := fs.String("dir", "/var/lib/loft", "node data directory")
	nodeBin := fs.String("node-bin", "/usr/bin/loft-node", "loft-node binary")
	releases := fs.String("releases", adm.Releases, "release download base (a test mirror; the helper never sets it)")
	fs.Parse(args)
	ops := adm.Defaults()
	ops.State, ops.Dir, ops.NodeBin, ops.ReleaseBase = *state, *dir, *nodeBin, *releases
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	if err := ops.RunJob(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "run-job:", err)
		os.Exit(1)
	}
}

// netCmd runs net-boot (apply the saved network setup at boot) or
// net-revert (undo the held network change -id that the user did not
// confirm in time). Either step gets one minute.
func netCmd(cmd string, args []string) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	id := fs.String("id", "", "the held change")
	state := fs.String("state", "/var/lib/loft-panel-adm", "helper state directory")
	fs.Parse(args)
	ops := adm.Defaults()
	ops.State = *state
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var err error
	if cmd == "net-boot" {
		err = ops.NetBoot(ctx)
	} else {
		err = ops.NetRevert(ctx, *id)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, cmd+":", err)
		os.Exit(1)
	}
}
