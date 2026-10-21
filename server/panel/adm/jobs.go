package adm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/catalog"
)

// JobUnit is the transient unit a job runs in. One name for every job:
// systemd itself refuses a second one while the first is alive.
const JobUnit = "loft-panel-job"

// logTail is how much of the job log a reply carries.
const logTail = 48 << 10

func (o *Ops) stateDir() string { return firstNonEmpty(o.State, "/var/lib/loft-panel-adm") }
func (o *Ops) jobFile() string  { return filepath.Join(o.stateDir(), "job.json") }
func (o *Ops) jobLog() string   { return filepath.Join(o.stateDir(), "job.log") }

func (o *Ops) readJob() (admproto.Job, error) {
	var j admproto.Job
	b, err := os.ReadFile(o.jobFile())
	if err != nil {
		return j, err
	}
	return j, json.Unmarshal(b, &j)
}

func (o *Ops) saveJob(j admproto.Job) error {
	j.Log = ""
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return storeWrite(o.jobFile(), append(b, '\n'), 0o600)
}

func (o *Ops) jobAlive(ctx context.Context) bool {
	st, _ := o.Systemctl(ctx, "is-active", JobUnit+".service")
	st = strings.TrimSpace(st)
	return st == "active" || st == "activating" || st == "deactivating"
}

// JobGet returns the current or last job; withLog adds the log tail. A job
// still "running" on disk whose unit is gone was killed (reboot, OOM): it is
// marked interrupted.
func (o *Ops) JobGet(ctx context.Context, withLog bool) (admproto.Job, error) {
	o.jobMu.Lock()
	defer o.jobMu.Unlock()
	j, err := o.readJob()
	if errors.Is(err, os.ErrNotExist) {
		return admproto.Job{}, nil
	}
	if err != nil {
		return j, err
	}
	if j.State == "running" && !o.jobAlive(ctx) {
		// The runner writes its end before it exits: read again before judging.
		if j2, err := o.readJob(); err == nil {
			j = j2
		}
		if j.State == "running" {
			j.State, j.Error, j.Ended = "interrupted", "the job stopped before it finished", time.Now().UTC()
			o.saveJob(j)
		}
	}
	if withLog {
		j.Log = tail(o.jobLog(), logTail)
	}
	if j.Backup != nil {
		j.Backup.Phrase = ""
	}
	return j, nil
}

func tail(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := st.Size() - n
	if off < 0 {
		off = 0
	}
	f.Seek(off, io.SeekStart)
	b, _ := io.ReadAll(f)
	if off > 0 {
		if i := strings.IndexByte(string(b), '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return string(b)
}

// StartPackages checks plan against the catalog and starts it.
func (o *Ops) StartPackages(ctx context.Context, by string, plan catalog.Plan) (admproto.Job, error) {
	if err := plan.Check(); err != nil {
		return admproto.Job{}, err
	}
	if err := plan.Consistent(o.installedKeys(ctx)); err != nil {
		return admproto.Job{}, err
	}
	return o.startJob(ctx, admproto.Job{Kind: "packages", By: by, Plan: &plan})
}

var tagRe = regexp.MustCompile(`^v\d{1,4}\.\d{1,4}\.\d{1,6}(-[0-9A-Za-z.]{1,32})?$`)

// StartUpdate starts installing release tag.
func (o *Ops) StartUpdate(ctx context.Context, by string, req admproto.UpdateApply) (admproto.Job, error) {
	if !tagRe.MatchString(req.Tag) {
		return admproto.Job{}, fmt.Errorf("%q is not a release tag", req.Tag)
	}
	return o.startJob(ctx, admproto.Job{Kind: "update", By: by, Tag: req.Tag})
}

func (o *Ops) startJob(ctx context.Context, j admproto.Job) (admproto.Job, error) {
	o.jobMu.Lock()
	defer o.jobMu.Unlock()
	if cur, err := o.readJob(); err == nil && cur.State == "running" {
		if o.jobAlive(ctx) {
			return admproto.Job{}, errors.New("another job is running: wait for it to finish")
		}
		cur.State, cur.Error, cur.Ended = "interrupted", "the job stopped before it finished", time.Now().UTC()
		o.saveJob(cur)
	}
	if err := os.MkdirAll(o.stateDir(), 0o700); err != nil {
		return admproto.Job{}, err
	}
	b := make([]byte, 8)
	rand.Read(b)
	j.ID, j.State, j.Started = hex.EncodeToString(b), "running", time.Now().UTC()
	if err := os.WriteFile(o.jobLog(), nil, 0o600); err != nil {
		return admproto.Job{}, err
	}
	if err := o.saveJob(j); err != nil {
		return admproto.Job{}, err
	}
	exe := o.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return admproto.Job{}, err
		}
	}
	args := []string{"--unit", JobUnit, "--collect", "--quiet", "--description", "loft-panel " + j.Kind + " job",
		exe, "run-job", "-state", o.stateDir(), "-dir", o.Dir, "-node-bin", o.NodeBin}
	run := o.SystemdRun
	if run == nil {
		run = func(ctx context.Context, args ...string) error {
			out, err := exec.CommandContext(ctx, "systemd-run", args...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	if err := run(ctx, args...); err != nil {
		j.State, j.Error, j.Ended = "error", "could not start the job: "+err.Error(), time.Now().UTC()
		o.saveJob(j)
		return j, errors.New(j.Error)
	}
	return j, nil
}
