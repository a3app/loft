package web

// This file holds the panel's HTTP handlers and background loop for backups.
// The panel runs without root, so the work that needs privileges (making a
// copy, restoring it, writing a directory store) is done by the helper,
// loft-panel-adm, reached through admproto calls (s.Adm(...).Call). The panel
// keeps its own settings and history in backup.json; dest.go knows how to
// talk to the S3 and SFTP stores; jobs.go runs the panel's own long jobs.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/backup"
	"github.com/a3app/loft/server/panel/store"
)

// Backups, the panel's side: the schedule, the stores and the history. The
// helper makes a copy into the backup directory (a job); the panel then
// carries it to S3 or SFTP and keeps the newest Keep there. A directory
// store is written by the helper itself.

// backupStore is one place where copies are kept, as saved in backup.json.
// Which fields matter depends on Kind: S3 uses the endpoint/bucket/keys,
// SFTP uses host/user/dir and pins the server's host key, and a "dir" store
// is a directory on this server that only the helper writes to.
type backupStore struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // s3 | sftp | dir
	// s3
	Endpoint  string `json:"endpoint,omitempty"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	// sftp
	Host    string `json:"host,omitempty"` // host or host:port
	User    string `json:"user,omitempty"`
	Dir     string `json:"dir,omitempty"` // also the directory store's path
	HostKey string `json:"host_key,omitempty"`
}

// backupRun is one line of the backup history shown in the panel: a backup
// or a restore, who started it, what it contained and how it ended. While
// the helper is still working, Job holds the helper's job id so the run can
// be followed (also after a panel restart).
type backupRun struct {
	ID     string    `json:"id"`
	Kind   string    `json:"kind"` // backup | restore
	At     time.Time `json:"at"`
	Name   string    `json:"name,omitempty"`
	Size   int64     `json:"size,omitempty"`
	Store  string    `json:"store,omitempty"` // store id; empty: this server only
	Parts  []string  `json:"parts"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
	By     string    `json:"by,omitempty"`
	Job    string    `json:"job,omitempty"`    // the helper's job, while it runs
	Upload bool      `json:"upload,omitempty"` // being carried to the store
}

// backupConf is the whole content of backup.json: the schedule, how many
// copies to keep, the chosen store, the list of stores and the recent runs.
type backupConf struct {
	Schedule string        `json:"schedule"` // daily | weekly | off
	Keep     int           `json:"keep"`
	Store    string        `json:"store"`
	Mail     bool          `json:"mail"`
	Stores   []backupStore `json:"stores"`
	Runs     []backupRun   `json:"runs"`
	LastAuto time.Time     `json:"last_auto,omitzero"`
}

// backups is the panel's in-memory backup state. mu guards every read-modify-
// write of backup.json; busy makes sure only one followBackups loop runs.
type backups struct {
	mu   sync.Mutex
	dir  string
	busy bool // a run is being followed
}

// SetBackupDir is the panel's data directory: backup.json and backup/.
func (s *Server) SetBackupDir(dir string) { s.bk.dir = dir }

// backupPath returns name inside the panel's data directory.
func (s *Server) backupPath(name string) string { return filepath.Join(s.bk.dir, name) }

// loadBackup reads backup.json. A missing or broken file gives the defaults
// (daily, keep 14, include mail), so the page always has something to show.
// The caller holds s.bk.mu.
func (s *Server) loadBackup() backupConf {
	c := backupConf{Schedule: "daily", Keep: 14, Mail: true}
	if b, err := os.ReadFile(s.backupPath("backup.json")); err == nil {
		json.Unmarshal(b, &c)
	}
	if c.Stores == nil {
		c.Stores = []backupStore{}
	}
	if c.Runs == nil {
		c.Runs = []backupRun{}
	}
	return c
}

// saveBackup writes backup.json atomically (a crash cannot leave half a file)
// and keeps only the 60 newest runs so the history does not grow forever.
// The caller holds s.bk.mu.
func (s *Server) saveBackup(c backupConf) error {
	if len(c.Runs) > 60 {
		c.Runs = c.Runs[:60]
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return store.WriteAtomic(s.backupPath("backup.json"), append(b, '\n'), 0o600)
}

// store finds a store by id and returns a pointer into c.Stores (so the
// caller can change it in place), or nil when there is none.
func (c *backupConf) store(id string) *backupStore {
	i := slices.IndexFunc(c.Stores, func(x backupStore) bool { return x.ID == id })
	if i < 0 {
		return nil
	}
	return &c.Stores[i]
}

// destOf builds the client for a remote store (S3 or SFTP). SFTP logs in
// with the panel's own SSH key. A "dir" store has no client here because
// only the helper may write to it.
func (s *Server) destOf(st backupStore) (dest, error) {
	switch st.Kind {
	case "s3":
		return newS3(st)
	case "sftp":
		k, err := s.sftpKey()
		if err != nil {
			return nil, err
		}
		return &sftpDest{st: st, signer: k}, nil
	}
	return nil, fmt.Errorf("a %s store is the helper's", st.Kind)
}

// storeView hides the secret key: it is set, not shown again.
type storeView struct {
	backupStore
	SecretKey bool   `json:"secret_key"`
	Label     string `json:"label"`
}

// storeLabel is the short human name of a store, e.g. "bucket · host/prefix"
// or "user@host:dir", used in the page and in the audit log.
func storeLabel(st backupStore) string {
	switch st.Kind {
	case "s3":
		host := strings.TrimPrefix(strings.TrimPrefix(st.Endpoint, "https://"), "http://")
		return st.Bucket + " · " + host + "/" + strings.Trim(st.Prefix, "/")
	case "sftp":
		return st.User + "@" + st.Host + ":" + st.Dir
	}
	return st.Dir
}

// backupGet returns everything the Backups page shows: settings, stores
// (without secret keys), history, the helper's view (recovery key, copies on
// this server), the panel's SSH public key to add on an SFTP server, and the
// helper's current job when it is a backup or a restore.
func (s *Server) backupGet(w http.ResponseWriter, r *http.Request) {
	s.bk.mu.Lock()
	c := s.loadBackup()
	s.bk.mu.Unlock()
	var st admproto.BackupState
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpBackupState, nil, &st); err != nil {
		admFail(w, err)
		return
	}
	var j admproto.Job
	s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpJobGet, nil, &j)
	stores := []storeView{}
	for _, x := range c.Stores {
		v := storeView{backupStore: x, SecretKey: x.SecretKey != "", Label: storeLabel(x)}
		v.backupStore.SecretKey = ""
		stores = append(stores, v)
	}
	pub := ""
	if k, err := s.sftpKey(); err == nil {
		pub = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k.PublicKey()))) + " loft-panel@" + firstOf(s.hostName(), "server")
	}
	out := map[string]any{"schedule": c.Schedule, "keep": c.Keep, "store": c.Store, "mail": c.Mail, "stores": stores,
		"runs": c.Runs, "key": st.Key, "local": st.Local, "ssh_key": pub}
	if j.Kind == "backup" || j.Kind == "restore" {
		out["job"] = j
	}
	ok(w, out)
}

// hostName is the panel's domain from the settings (may be empty).
func (s *Server) hostName() string {
	st, _ := s.Settings.Get()
	return st.Domain
}

// backupSettings saves the schedule, the store and what goes in.
func (s *Server) backupSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Schedule string `json:"schedule"`
		Keep     int    `json:"keep"`
		Store    string `json:"store"`
		Mail     bool   `json:"mail"`
	}
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	if !slices.Contains([]string{"daily", "weekly", "off"}, in.Schedule) || in.Keep < 1 || in.Keep > 1000 {
		fail(w, http.StatusBadRequest, "schedule: daily, weekly or off; keep 1 to 1000 copies")
		return
	}
	s.bk.mu.Lock()
	defer s.bk.mu.Unlock()
	c := s.loadBackup()
	if in.Store != "" && c.store(in.Store) == nil {
		fail(w, http.StatusBadRequest, "no such store")
		return
	}
	c.Schedule, c.Keep, c.Store, c.Mail = in.Schedule, in.Keep, in.Store, in.Mail
	if err := s.saveBackup(c); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, struct{}{})
}

// backupPhrase makes a new recovery phrase: the server keeps its public
// key, the phrase is shown once and kept nowhere.
func (s *Server) backupPhrase(w http.ResponseWriter, r *http.Request) {
	phrase := backup.NewPhrase()
	k, err := backup.NewKey(phrase)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpBackupKey, admproto.BackupKey{Public: k.Public, Salt: k.Salt}, nil); err != nil {
		admFail(w, err)
		return
	}
	ok(w, map[string]string{"phrase": phrase})
}

// cleanStore tidies a store sent by the browser (trims spaces, adds https://
// to a bare S3 endpoint, splits "bucket/path") and rejects one that is
// missing required fields or holds whitespace. It does not contact the store.
func cleanStore(in backupStore) (backupStore, error) {
	t := strings.TrimSpace
	in.Endpoint, in.Bucket, in.Prefix, in.AccessKey, in.Region = t(in.Endpoint), t(in.Bucket), strings.Trim(t(in.Prefix), "/"), t(in.AccessKey), t(in.Region)
	in.Host, in.User, in.Dir = t(in.Host), t(in.User), t(in.Dir)
	bad := func(v string) bool { return strings.ContainsAny(v, "\n\r\t ") }
	switch in.Kind {
	case "s3":
		if in.Endpoint != "" && !strings.Contains(in.Endpoint, "://") {
			in.Endpoint = "https://" + in.Endpoint
		}
		// "bucket/path" in one field, as the dialog asks.
		if b, p, ok := strings.Cut(in.Bucket, "/"); ok {
			in.Bucket, in.Prefix = b, strings.Trim(p+"/"+in.Prefix, "/")
		}
		if in.Bucket == "" || in.AccessKey == "" || bad(in.Bucket) || bad(in.AccessKey) || bad(in.Prefix) {
			return in, errors.New("S3 needs a bucket and an access key")
		}
		if _, err := newS3(backupStore{Endpoint: in.Endpoint, Bucket: in.Bucket, AccessKey: in.AccessKey, SecretKey: "x"}); err != nil {
			return in, err
		}
	case "sftp":
		if in.Host == "" || in.User == "" || bad(in.Host) || bad(in.User) || bad(in.Dir) {
			return in, errors.New("SFTP needs a server and a user")
		}
	case "dir":
		if !strings.HasPrefix(in.Dir, "/") || bad(in.Dir) {
			return in, errors.New("the directory must be an absolute path")
		}
		in.Dir = filepath.Clean(in.Dir)
	default:
		return in, errors.New("store: s3, sftp or dir")
	}
	return in, nil
}

// backupStoreSave adds, checks (Test: nothing saved) or deletes a store.
func (s *Server) backupStoreSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		backupStore
		Test   bool `json:"test"`
		Delete bool `json:"delete"`
	}
	if !readJSON(w, r, 16<<10, &in) {
		return
	}
	s.bk.mu.Lock()
	c := s.loadBackup()
	s.bk.mu.Unlock()
	if in.Delete {
		auditTarget(r, in.ID)
		s.bk.mu.Lock()
		defer s.bk.mu.Unlock()
		c = s.loadBackup()
		c.Stores = slices.DeleteFunc(c.Stores, func(x backupStore) bool { return x.ID == in.ID })
		if c.Store == in.ID {
			c.Store = ""
		}
		s.saveBackup(c)
		ok(w, struct{}{})
		return
	}
	st, err := cleanStore(in.backupStore)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if old := c.store(st.ID); old != nil && st.SecretKey == "" {
		st.SecretKey = old.SecretKey // kept when the field is left empty
	}
	if st.Kind == "s3" && st.SecretKey == "" {
		fail(w, http.StatusBadRequest, "S3 needs the secret key")
		return
	}
	// Contact the store. A new SFTP host key is learnt here (pin) and an S3
	// region is taken from the store's reply, so both are saved below.
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	// Every store is checked by writing to it before it is saved.
	if st.Kind == "dir" {
		var l []admproto.BackupFile
		if err := s.Adm(userOf(r).Login).Call(ctx, admproto.OpBackupDir, admproto.BackupDir{Dir: st.Dir}, &l); err != nil {
			admFail(w, err)
			return
		}
	} else {
		st.HostKey = ""
		if old := c.store(st.ID); old != nil && old.Host == st.Host {
			st.HostKey = old.HostKey
		}
		d, err := s.destOf(st)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		if sd, okk := d.(*sftpDest); okk {
			sd.pin = func(fp string) { st.HostKey = fp }
		}
		if err := d.Check(ctx); err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		if sd, okk := d.(*s3dest); okk {
			st.Region = sd.region // learnt from the store's answer
		}
	}
	if in.Test {
		auditSkip(r)
		ok(w, map[string]string{"host_key": st.HostKey})
		return
	}
	// Save: a new store gets an id; the first store becomes the default.
	auditTarget(r, storeLabel(st))
	s.bk.mu.Lock()
	defer s.bk.mu.Unlock()
	c = s.loadBackup()
	if st.ID == "" {
		st.ID = newID("s-")
		c.Stores = append(c.Stores, st)
	} else if p := c.store(st.ID); p != nil {
		*p = st
	} else {
		fail(w, http.StatusNotFound, "no such store")
		return
	}
	if c.Store == "" {
		c.Store = st.ID
	}
	if err := s.saveBackup(c); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, map[string]string{"id": st.ID})
}

// backupParts is what a copy holds: the node and the panel always, mail
// when it is installed and chosen.
func (s *Server) backupParts(ctx context.Context, c backupConf) []string {
	parts := []string{"node", "panel"}
	if c.Mail {
		var v admproto.MailView
		if err := s.Adm("backup").Call(ctx, admproto.OpMail, nil, &v); err == nil && v.Installed {
			parts = append(parts, "mail")
		}
	}
	return parts
}

// startBackup starts the helper's job and follows it.
func (s *Server) startBackup(ctx context.Context, by string, manual bool) (backupRun, error) {
	s.bk.mu.Lock()
	c := s.loadBackup()
	s.bk.mu.Unlock()
	run := backupRun{ID: newID("r-"), Kind: "backup", At: time.Now().UTC(), Store: c.Store, Parts: s.backupParts(ctx, c), By: by}
	if !manual {
		run.By = "schedule"
	}
	req := admproto.BackupJob{Parts: run.Parts}
	if st := c.store(c.Store); st != nil && st.Kind == "dir" {
		req.Dir, req.Keep = st.Dir, c.Keep
	}
	var j admproto.Job
	if err := s.Adm(by).Call(ctx, admproto.OpBackupRun, req, &j); err != nil {
		return run, admErr(err)
	}
	run.Job = j.ID
	s.bk.mu.Lock()
	c = s.loadBackup()
	c.Runs = append([]backupRun{run}, c.Runs...)
	if !manual {
		c.LastAuto = time.Now()
	}
	s.saveBackup(c)
	s.bk.mu.Unlock()
	go s.followBackups(context.WithoutCancel(ctx))
	return run, nil
}

// backupRunNow is the "Back up now" button: it starts a manual backup.
func (s *Server) backupRunNow(w http.ResponseWriter, r *http.Request) {
	run, err := s.startBackup(r.Context(), userOf(r).Login, true)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	ok(w, run)
}

// followBackups waits for the runs whose helper job is still going, then
// carries the copy to its store. One follower at a time; it also resumes
// runs a restart of the panel left behind.
func (s *Server) followBackups(ctx context.Context) {
	s.bk.mu.Lock()
	if s.bk.busy {
		s.bk.mu.Unlock()
		return
	}
	s.bk.busy = true
	s.bk.mu.Unlock()
	defer func() { s.bk.mu.Lock(); s.bk.busy = false; s.bk.mu.Unlock() }()
	for {
		s.bk.mu.Lock()
		c := s.loadBackup()
		s.bk.mu.Unlock()
		// 1. Take the next run still waiting for its helper job; stop when none.
		i := slices.IndexFunc(c.Runs, func(x backupRun) bool { return x.Job != "" })
		if i < 0 {
			return
		}
		run := c.Runs[i]
		var j admproto.Job
		if err := s.Adm("backup").Call(ctx, admproto.OpJobGet, nil, &j); err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		// 2. Still running: poll again shortly. Otherwise record how it ended.
		// The helper runs one job at a time, so a different job id means ours
		// was replaced.
		if j.ID == run.Job && j.State == "running" {
			time.Sleep(2 * time.Second)
			continue
		}
		jobID := run.Job
		run.Job = ""
		switch {
		case j.ID != jobID:
			run.Error = "the job was replaced by another one"
		case j.State != "done":
			run.Error = firstOf(j.Error, "the job "+j.State)
		default:
			if j.Backup != nil {
				run.Name, run.Size = j.Backup.Name, j.Backup.Size
				if run.Kind == "restore" {
					run.Name = firstOf(run.Name, j.Backup.File)
				}
			}
			run.OK = true
		}
		// 3. A finished backup for a remote store is uploaded now; Upload marks
		// the run as "being carried" so the page can show it.
		if run.OK && run.Kind == "backup" {
			if st := c.store(run.Store); st != nil && st.Kind != "dir" {
				run.Upload = true
				s.putRun(run)
				if err := s.carry(ctx, *st, run.Name, c.Keep); err != nil {
					run.OK, run.Error = false, "the copy was made but not carried to the store: "+err.Error()
				}
				run.Upload = false
			}
		}
		s.putRun(run)
		// 4. Tell the admin when a scheduled backup failed (nobody is watching it).
		if !run.OK && run.By == "schedule" {
			st, _ := s.Settings.Get()
			if eventOn(st, EventBackup) {
				s.notify(ctx, st, "Backup failed", "The scheduled backup did not finish: "+run.Error, func(*store.Notify) {})
			}
		}
	}
}

// putRun replaces the saved run with the same id, so the history shows the
// latest state of a run while it is being followed.
func (s *Server) putRun(run backupRun) {
	s.bk.mu.Lock()
	defer s.bk.mu.Unlock()
	c := s.loadBackup()
	if i := slices.IndexFunc(c.Runs, func(x backupRun) bool { return x.ID == run.ID }); i >= 0 {
		c.Runs[i] = run
		s.saveBackup(c)
	}
}

// carry uploads a copy and keeps the newest keep copies in the store.
func (s *Server) carry(ctx context.Context, st backupStore, name string, keep int) error {
	d, err := s.destOf(st)
	if err != nil {
		return err
	}
	p, err := copyIn(s.backupPath("backup"), name)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, _ := f.Stat()
	if err := d.Put(ctx, name, bufio.NewReaderSize(f, 1<<20), fi.Size()); err != nil {
		return err
	}
	l, err := d.List(ctx)
	if err != nil {
		return nil // uploaded; pruning waits for the next run
	}
	n := 0
	for _, x := range l { // newest first
		if strings.HasSuffix(x.Name, "-before-restore.lpbak") {
			continue
		}
		if n++; n > keep {
			d.Delete(ctx, x.Name)
		}
	}
	return nil
}

// watchBackups runs the schedule: daily or on Sundays at 04:00 server time.
func (s *Server) watchBackups(ctx context.Context) {
	if s.bk.dir == "" {
		return
	}
	s.bk.mu.Lock()
	c := s.loadBackup()
	s.bk.mu.Unlock()
	if slices.ContainsFunc(c.Runs, func(x backupRun) bool { return x.Job != "" }) {
		go s.followBackups(ctx)
	}
	if c.Schedule == "off" {
		return
	}
	// The due time is today at 04:00 (for weekly: last Sunday at 04:00). A run
	// starts only within 6 hours after it and only if none ran since, so a panel
	// that was down at 04:00 still catches up the same morning.
	now := time.Now()
	due := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, time.Local)
	if c.Schedule == "weekly" {
		due = due.AddDate(0, 0, -int(due.Weekday()))
	}
	if now.Before(due) || !c.LastAuto.Before(due) || now.Sub(due) > 6*time.Hour {
		return
	}
	if _, err := s.startBackup(ctx, "schedule", false); err != nil {
		s.logf("scheduled backup: %v", err) // a job may be running: the next minute tries again
	} else {
		s.sysEvent("backup", "")
	}
}

// backupFetch brings a copy from a store into the backup directory, so it
// can be restored (a panel job: copies can be large).
func (s *Server) backupFetch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Store string `json:"store"`
		Name  string `json:"name"`
	}
	if !readJSON(w, r, 2<<10, &in) {
		return
	}
	if !copyRe.MatchString(in.Name) {
		fail(w, http.StatusBadRequest, "not a copy")
		return
	}
	s.bk.mu.Lock()
	c := s.loadBackup()
	s.bk.mu.Unlock()
	st := c.store(in.Store)
	if st == nil {
		fail(w, http.StatusNotFound, "no such store")
		return
	}
	if st.Kind == "dir" {
		p, err := copyIn(st.Dir, in.Name)
		if err != nil {
			fail(w, http.StatusBadRequest, "not a copy")
			return
		}
		ok(w, map[string]string{"file": p})
		return
	}
	auditSkip(r)
	// A remote copy is downloaded in a panel job: it is written to a ".part"
	// file and renamed at the end, so a broken download never looks like a copy.
	stc := *st
	s.startJob(w, userOf(r).Login, func(ctx context.Context) (any, error) {
		d, err := s.destOf(stc)
		if err != nil {
			return nil, err
		}
		rc, err := d.Get(ctx, in.Name)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		dir := s.backupPath("backup")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		final, err := copyIn(dir, in.Name)
		if err != nil {
			return nil, err
		}
		tmp, err := fileIn(dir, "."+in.Name+".part")
		if err != nil {
			return nil, err
		}
		_ = os.Remove(tmp)
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(f, rc)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(tmp)
			return nil, err
		}
		return map[string]string{"file": in.Name}, os.Rename(tmp, final)
	})
}

// backupUpload takes a copy from the browser (Restore → From a file). The
// body streams to disk; the first bytes must be a copy's.
func (s *Server) backupUpload(w http.ResponseWriter, r *http.Request) {
	// The server's 30 s read limit is for forms; a copy takes longer.
	rc := http.NewResponseController(w)
	rc.SetReadDeadline(time.Now().Add(3 * time.Hour))
	rc.SetWriteDeadline(time.Now().Add(3 * time.Hour))
	r.Body = http.MaxBytesReader(w, r.Body, 64<<30)
	br := bufio.NewReaderSize(r.Body, 64<<10)
	if head, err := br.Peek(len(backup.Magic)); err != nil || string(head) != backup.Magic {
		fail(w, http.StatusBadRequest, "not a loft backup")
		return
	}
	dir := s.backupPath("backup")
	os.MkdirAll(dir, 0o700)
	// Stream to a temporary file, then rename to upload.lpbak and read its
	// header so the page can show where and when the copy was made.
	tmp := filepath.Join(dir, ".upload.part")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := io.Copy(f, br)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		fail(w, http.StatusBadRequest, "upload: "+err.Error())
		return
	}
	if err := os.Rename(tmp, filepath.Join(dir, "upload.lpbak")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	g, err := os.Open(filepath.Join(dir, "upload.lpbak"))
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	h, _, err := backup.ReadHeader(g)
	g.Close()
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]any{"file": "upload.lpbak", "size": n, "host": h.Host, "created": h.Created, "parts": h.Parts})
}

// backupRestore starts a restore of a copy already on the server.
func (s *Server) backupRestore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		File   string   `json:"file"`
		Parts  []string `json:"parts"`
		Phrase string   `json:"phrase"`
	}
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	auditTarget(r, in.File+" ("+strings.Join(in.Parts, ", ")+")")
	var j admproto.Job
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpBackupRestore, admproto.BackupJob{File: in.File, Parts: in.Parts, Phrase: in.Phrase}, &j); err != nil {
		admFail(w, err)
		return
	}
	run := backupRun{ID: newID("r-"), Kind: "restore", At: time.Now().UTC(), Name: filepath.Base(in.File), Parts: in.Parts, By: userOf(r).Login, Job: j.ID}
	s.bk.mu.Lock()
	c := s.loadBackup()
	c.Runs = append([]backupRun{run}, c.Runs...)
	s.saveBackup(c)
	s.bk.mu.Unlock()
	go s.followBackups(context.WithoutCancel(r.Context()))
	ok(w, j)
}

// backupJob is the helper's current job, for the screen to follow a backup
// or a restore (the panel itself may restart during a restore).
func (s *Server) backupJob(w http.ResponseWriter, r *http.Request) {
	var j admproto.Job
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpJobGet, nil, &j); err != nil {
		admFail(w, err)
		return
	}
	ok(w, j)
}
