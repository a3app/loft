package adm

import (
	"archive/tar"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/backup"
)

// Backups. The helper only checks requests and starts the job; the job
// (root, outside the sandbox) makes the copy into the panel's backup
// directory, which the panel reads to carry it to S3 or SFTP, or restores
// one. The recovery key's public half is all the server keeps.

const (
	backupLocal = "/var/lib/loft-panel/backup"
	panelUser   = "loft-panel"
)

var (
	backupNameRe = regexp.MustCompile(`^loft-[a-z0-9.-]{1,100}-\d{8}-\d{6}(-before-restore)?\.lpbak$`)
	backupDirRe  = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,200}$`)
)

func (o *Ops) backupKeyFile() string {
	return filepath.Join(firstNonEmpty(o.PanelEtc, "/etc/loft-panel"), "backup-key.json")
}

func (o *Ops) readBackupKey() (*admproto.BackupKey, error) {
	b, err := os.ReadFile(o.backupKeyFile())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var k admproto.BackupKey
	return &k, json.Unmarshal(b, &k)
}

// BackupKeySet keeps the public half of a new recovery key. Copies made
// before stay readable with the old phrase only.
func (o *Ops) BackupKeySet(ctx context.Context, k admproto.BackupKey) (admproto.BackupState, error) {
	pub, err := base64.StdEncoding.DecodeString(k.Public)
	salt, err2 := base64.StdEncoding.DecodeString(k.Salt)
	if err != nil || err2 != nil || len(pub) != 32 || len(salt) != 16 {
		return admproto.BackupState{}, errors.New("bad backup key")
	}
	k.Set = time.Now().UTC()
	b, _ := json.MarshalIndent(k, "", "  ")
	if err := os.MkdirAll(filepath.Dir(o.backupKeyFile()), 0o755); err != nil {
		return admproto.BackupState{}, err
	}
	if err := storeWrite(o.backupKeyFile(), append(b, '\n'), 0o600); err != nil {
		return admproto.BackupState{}, err
	}
	return o.BackupState(ctx)
}

// BackupState is the key and the copies in the local directory.
func (o *Ops) BackupState(ctx context.Context) (admproto.BackupState, error) {
	st := admproto.BackupState{Local: listCopies(o.sys(backupLocal))}
	k, err := o.readBackupKey()
	st.Key = k
	return st, err
}

func listCopies(dir string) []admproto.BackupFile {
	out := []admproto.BackupFile{}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if !backupNameRe.MatchString(e.Name()) {
			continue
		}
		if fi, err := e.Info(); err == nil {
			out = append(out, admproto.BackupFile{Name: e.Name(), Size: fi.Size(), At: fi.ModTime().UTC()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out
}

func checkParts(parts []string, backupSide bool) error {
	if len(parts) == 0 {
		return errors.New("choose what to restore")
	}
	for _, p := range parts {
		if _, ok := backup.PartByName(p); !ok {
			return fmt.Errorf("unknown part %q", p)
		}
	}
	if backupSide && (!slices.Contains(parts, "node") || !slices.Contains(parts, "panel")) {
		return errors.New("a copy always holds the node and the panel")
	}
	return nil
}

// checkStoreDir refuses system directories as a store: a copy written into
// /etc or the node's own directory would be restored into itself.
func checkStoreDir(d string) error {
	d = filepath.Clean(d)
	if !backupDirRe.MatchString(d) || strings.Contains(d, "..") {
		return fmt.Errorf("%q is not a directory path", d)
	}
	for _, bad := range []string{"/", "/etc", "/usr", "/bin", "/sbin", "/lib", "/boot", "/proc", "/sys", "/dev", "/run", "/var/lib/loft", "/var/lib/loft-panel", "/var/vmail", "/tmp"} {
		if d == bad || (bad != "/" && strings.HasPrefix(d, bad+"/")) {
			return fmt.Errorf("%s cannot hold copies", d)
		}
	}
	return nil
}

// BackupRun starts a backup.
func (o *Ops) BackupRun(ctx context.Context, by string, b admproto.BackupJob) (admproto.Job, error) {
	if err := checkParts(b.Parts, true); err != nil {
		return admproto.Job{}, err
	}
	if b.Dir != "" {
		if err := checkStoreDir(b.Dir); err != nil {
			return admproto.Job{}, err
		}
		if b.Keep < 1 || b.Keep > 1000 {
			return admproto.Job{}, errors.New("keep 1 to 1000 copies")
		}
	}
	if k, err := o.readBackupKey(); err != nil || k == nil {
		return admproto.Job{}, errors.New("set a recovery phrase first")
	}
	b.File, b.Phrase, b.Name, b.Size, b.Pre = "", "", "", 0, ""
	return o.startJob(ctx, admproto.Job{Kind: "backup", By: by, Backup: &b})
}

// BackupRestore starts a restore.
func (o *Ops) BackupRestore(ctx context.Context, by string, b admproto.BackupJob) (admproto.Job, error) {
	if err := checkParts(b.Parts, false); err != nil {
		return admproto.Job{}, err
	}
	if _, err := o.copyPath(b.File); err != nil {
		return admproto.Job{}, err
	}
	if len(backup.NormPhrase(b.Phrase)) < 20 {
		return admproto.Job{}, errors.New("the recovery phrase is too short")
	}
	b.Dir, b.Keep, b.Name, b.Size, b.Pre = "", 0, "", 0, ""
	return o.startJob(ctx, admproto.Job{Kind: "restore", By: by, Backup: &b})
}

// copyPath is a copy's file: a name in the local directory, or a path in a
// directory store.
func (o *Ops) copyPath(f string) (string, error) {
	if backupNameRe.MatchString(f) || f == "upload.lpbak" {
		return o.sys(filepath.Join(backupLocal, f)), nil
	}
	if filepath.IsAbs(f) && backupNameRe.MatchString(filepath.Base(f)) && checkStoreDir(filepath.Dir(f)) == nil {
		return o.sys(f), nil
	}
	return "", fmt.Errorf("%q is not a copy", f)
}

// BackupDir lists the copies in a directory store, creating it.
func (o *Ops) BackupDir(ctx context.Context, d admproto.BackupDir) ([]admproto.BackupFile, error) {
	if err := checkStoreDir(d.Dir); err != nil {
		return nil, err
	}
	dir := filepath.Clean(d.Dir)
	out, err := o.host(ctx, "sh", "-c", "install -d -m 0700 "+shq(dir)+" && test -w "+shq(dir)+" && find "+shq(dir)+
		" -maxdepth 1 -name 'loft-*.lpbak' -printf '%f\\t%s\\t%T@\\n'")
	if err != nil {
		return nil, fmt.Errorf("%s: %v", dir, err)
	}
	list := []admproto.BackupFile{}
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(ln), "\t")
		if len(f) != 3 || !backupNameRe.MatchString(f[0]) {
			continue
		}
		size, _ := strconv.ParseInt(f[1], 10, 64)
		sec, _ := strconv.ParseFloat(f[2], 64)
		list = append(list, admproto.BackupFile{Name: f[0], Size: size, At: time.Unix(int64(sec), 0).UTC()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name > list[j].Name })
	return list, nil
}

/* ── the job ── */

// move renames, or moves across filesystems (/etc and /var may differ).
func (r *runner) move(ctx context.Context, src, dst string) error {
	err := os.Rename(src, dst)
	if errors.Is(err, syscall.EXDEV) {
		return r.cmd(ctx, "mv", "-T", "--", src, dst)
	}
	return err
}

func streamFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func (r *runner) backupHost() string {
	if in, err := r.o.NodeInfo(); err == nil && in.Host != "" {
		return strings.ToLower(in.Host)
	}
	h, _ := os.Hostname()
	return strings.ToLower(firstNonEmpty(h, "server"))
}

func copyName(host string, at time.Time, suffix string) string {
	host = regexp.MustCompile(`[^a-z0-9.-]`).ReplaceAllString(host, "-")
	return "loft-" + host + "-" + at.Format("20060102-150405") + suffix + ".lpbak"
}

// makeCopy writes a copy of parts into the local directory.
func (r *runner) makeCopy(parts []string, k backup.Key, suffix string) (string, int64, error) {
	dir := r.path(backupLocal)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	r.chownPanel(dir)
	host := r.backupHost()
	name := copyName(host, time.Now(), suffix)
	tmp := filepath.Join(dir, "."+name+".part")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tmp)
	w, err := backup.Seal(f, k, backup.Header{Host: host, Created: time.Now().UTC(), Parts: parts})
	if err == nil {
		var m backup.Manifest
		m, err = backup.Write(w, r.o.Root, host, parts)
		if err == nil {
			err = w.Close()
		}
		r.say("%d files", len(m.Files))
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	final := filepath.Join(dir, name)
	if err := os.Rename(tmp, final); err != nil {
		return "", 0, err
	}
	r.chownPanel(final)
	fi, _ := os.Stat(final)
	return name, fi.Size(), nil
}

func (r *runner) chownPanel(p string) {
	if u, err := user.Lookup(panelUser); err == nil {
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		os.Chown(p, uid, gid)
	}
}

// prune keeps the newest keep copies of this server in dir.
func prune(dir string, keep int, before bool) {
	var mine []string
	for _, c := range listCopies(dir) {
		if strings.HasSuffix(c.Name, "-before-restore.lpbak") == before {
			mine = append(mine, c.Name)
		}
	}
	for i, n := range mine { // newest first
		if i >= keep {
			os.Remove(filepath.Join(dir, n))
		}
	}
}

func (r *runner) backup(ctx context.Context, b *admproto.BackupJob) error {
	kp, err := r.o.readBackupKey()
	if err != nil || kp == nil {
		return errors.New("set a recovery phrase first")
	}
	r.step("archive")
	r.say("copy %s", strings.Join(b.Parts, ", "))
	name, size, err := r.makeCopy(b.Parts, backup.Key{Public: kp.Public, Salt: kp.Salt}, "")
	if err != nil {
		return fmt.Errorf("the copy failed: %v", err)
	}
	b.Name, b.Size = name, size
	r.say("%s, %d bytes", name, size)
	if b.Dir != "" {
		r.step("store")
		dst := r.path(filepath.Clean(b.Dir))
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return err
		}
		if err := streamFile(filepath.Join(r.path(backupLocal), name), filepath.Join(dst, name)); err != nil {
			return fmt.Errorf("copy to %s: %v", b.Dir, err)
		}
		prune(dst, b.Keep, false)
	}
	// The local directory keeps the two newest: one to carry away, one
	// to restore from without a download.
	prune(r.path(backupLocal), 2, false)
	r.o.saveJob(*r.j)
	return nil
}

// restoreOwner sets an extracted entry's owner from its names in the archive: the
// same accounts get other numbers on another server.
func restoreOwner(path string, h *tar.Header) error {
	uid, gid := h.Uid, h.Gid
	if h.Uname != "" {
		if u, err := user.Lookup(h.Uname); err == nil {
			uid, _ = strconv.Atoi(u.Uid)
		}
	}
	if h.Gname != "" {
		if g, err := user.LookupGroup(h.Gname); err == nil {
			gid, _ = strconv.Atoi(g.Gid)
		}
	}
	if os.Geteuid() != 0 {
		return nil // tests
	}
	return os.Lchown(path, uid, gid)
}

// units stopped while a part is swapped in.
var partUnits = map[string][]string{"node": {"loft-node"}, "panel": {"loft-panel"}, "mail": {"postfix", "dovecot", "opendkim"}}

func (r *runner) restore(ctx context.Context, b *admproto.BackupJob, phrase string) error {
	src, err := r.o.copyPath(b.File)
	if err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("the copy: %v", err)
	}
	defer f.Close()
	r.step("open")
	h, plain, err := backup.Open(f, phrase)
	if err != nil {
		return err
	}
	r.say("copy of %s made %s, parts %s", h.Host, h.Created.Format(time.RFC3339), strings.Join(h.Parts, ", "))
	for _, p := range b.Parts {
		if !slices.Contains(h.Parts, p) {
			return fmt.Errorf("the copy has no %s part", p)
		}
	}
	stage := filepath.Join(r.o.stateDir(), "restore")
	os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	r.step("unpack")
	if _, err := backup.Extract(plain, stage, b.Parts, restoreOwner); err != nil {
		return err
	}
	r.say("unpacked and checked")

	// A copy of what is here now, before anything changes.
	r.step("before")
	k := backup.Key{Public: h.Public, Salt: h.Salt}
	if kp, _ := r.o.readBackupKey(); kp != nil {
		k = backup.Key{Public: kp.Public, Salt: kp.Salt}
	}
	pre, _, err := r.makeCopy(b.Parts, k, "-before-restore")
	if err != nil {
		return fmt.Errorf("the copy of the current state failed, nothing was restored: %v", err)
	}
	b.Pre = pre
	r.o.saveJob(*r.j)
	prune(r.path(backupLocal), 1, true)
	r.say("current state saved as %s", pre)

	r.step("swap")
	var units []string
	for _, p := range b.Parts {
		units = append(units, partUnits[p]...)
	}
	for _, u := range units {
		r.o.Systemctl(ctx, "stop", u)
	}
	type moved struct{ live, aside string }
	var done []moved
	undo := func() {
		for i := len(done) - 1; i >= 0; i-- {
			os.RemoveAll(done[i].live)
			r.move(ctx, done[i].aside, done[i].live)
		}
	}
	for _, name := range b.Parts {
		p, _ := backup.PartByName(name)
		for _, root := range p.Roots {
			staged := filepath.Join(stage, name, root)
			if _, err := os.Lstat(staged); err != nil {
				continue // not in the copy: left as it is
			}
			live := r.path(root)
			aside := live + ".before-restore"
			os.RemoveAll(aside)
			if _, err := os.Lstat(live); err == nil {
				if err := r.move(ctx, live, aside); err != nil {
					undo()
					return fmt.Errorf("move %s aside: %v", root, err)
				}
			}
			if err := r.move(ctx, staged, live); err != nil {
				r.move(ctx, aside, live)
				undo()
				return fmt.Errorf("put %s in place: %v", root, err)
			}
			done = append(done, moved{live, aside})
			// What a copy leaves out stays: the backup directory itself.
			for _, s := range p.Skip {
				if strings.HasPrefix(s, root+"/") {
					rel := strings.TrimPrefix(s, root)
					if _, err := os.Lstat(aside + rel); err == nil {
						os.RemoveAll(live + rel)
						r.move(ctx, aside+rel, live+rel)
					}
				}
			}
		}
	}
	for _, d := range done {
		os.RemoveAll(d.aside)
	}
	r.step("start")
	for _, u := range units {
		r.o.Systemctl(ctx, "start", u)
	}
	if slices.Contains(b.Parts, "panel") || slices.Contains(b.Parts, "node") {
		if changed, err := r.o.WriteSite(); err == nil && changed {
			r.o.reloadNginx(ctx)
		}
		r.o.Systemctl(ctx, "restart", "loft-panel-adm")
		r.o.Systemctl(ctx, "restart", "loft-panel")
	}
	if (slices.Contains(b.Parts, "panel") || slices.Contains(b.Parts, "mail")) && r.o.mailInstalled() {
		if _, err := r.o.MailApply(ctx); err != nil {
			r.say("mail configuration: %v", err)
		}
	}
	r.say("restored %s", strings.Join(b.Parts, ", "))
	return nil
}
