package adm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/backup"
)

func TestBackupRestore(t *testing.T) {
	o, _ := pkgOps(t)
	ctx := context.Background()
	o.PanelEtc = filepath.Join(o.Root, "etc/loft-panel")
	put := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(o.Root, p)), 0o755)
		os.WriteFile(filepath.Join(o.Root, p), []byte(s), 0o640)
	}
	get := func(p string) string { b, _ := os.ReadFile(filepath.Join(o.Root, p)); return string(b) }
	put("/var/lib/loft/network.conf", "network main\n")
	put("/etc/loft/node.conf", "role controller\n")
	put("/var/lib/loft-panel/users.json", `{"v":1}`)
	put("/var/vmail/example.net/a/Maildir/new/1", "letter")

	if _, err := o.BackupRun(ctx, "t", admproto.BackupJob{Parts: []string{"node", "panel"}}); err == nil {
		t.Fatal("a backup without a recovery key")
	}
	phrase := backup.NewPhrase()
	k, _ := backup.NewKey(phrase)
	if _, err := o.BackupKeySet(ctx, admproto.BackupKey{Public: k.Public, Salt: k.Salt}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.BackupRun(ctx, "t", admproto.BackupJob{Parts: []string{"mail"}}); err == nil {
		t.Fatal("a copy without the node and the panel")
	}
	if _, err := o.BackupRun(ctx, "t", admproto.BackupJob{Parts: []string{"node", "panel"}, Dir: "/etc/x", Keep: 3}); err == nil {
		t.Fatal("a store in /etc")
	}

	var log bytes.Buffer
	j := admproto.Job{Kind: "backup", Backup: &admproto.BackupJob{Parts: []string{"node", "panel", "mail"}, Dir: "/srv/copies", Keep: 2}}
	r := &runner{o: o, j: &j, w: &log}
	if err := r.backup(ctx, j.Backup); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	name := j.Backup.Name
	if !backupNameRe.MatchString(name) || get("/srv/copies/"+name) == "" {
		t.Fatalf("copy %q not in the store", name)
	}
	if b := get("/var/lib/loft-panel/backup/" + name); strings.Contains(b, "role controller") || !strings.HasPrefix(b, backup.Magic) {
		t.Fatal("the copy is not encrypted")
	}
	st, _ := o.BackupState(ctx)
	if len(st.Local) != 1 || st.Key == nil {
		t.Fatalf("state: %+v", st)
	}

	// Things change, then the copy comes back.
	put("/etc/loft/node.conf", "broken\n")
	put("/var/lib/loft/extra", "x")
	os.Remove(filepath.Join(o.Root, "/var/vmail/example.net/a/Maildir/new/1"))
	if _, err := o.BackupRestore(ctx, "t", admproto.BackupJob{Parts: []string{"node"}, File: "../../etc/passwd", Phrase: phrase}); err == nil {
		t.Fatal("a path outside the copies")
	}
	jr := admproto.Job{Kind: "restore", Backup: &admproto.BackupJob{Parts: []string{"node", "mail"}, File: name}}
	r = &runner{o: o, j: &jr, w: &log}
	if err := r.restore(ctx, jr.Backup, backup.NewPhrase()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("another phrase: %v", err)
	}
	if get("/etc/loft/node.conf") != "broken\n" {
		t.Fatal("a failed restore changed the server")
	}
	if err := r.restore(ctx, jr.Backup, phrase); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if get("/etc/loft/node.conf") != "role controller\n" || get("/var/vmail/example.net/a/Maildir/new/1") != "letter" {
		t.Fatal("not restored")
	}
	if get("/var/lib/loft/extra") != "" {
		t.Fatal("a restored part keeps files the copy did not have")
	}
	if get("/var/lib/loft-panel/users.json") != `{"v":1}` {
		t.Fatal("a part not asked for was touched")
	}
	if !strings.HasSuffix(jr.Backup.Pre, "-before-restore.lpbak") || get("/var/lib/loft-panel/backup/"+jr.Backup.Pre) == "" {
		t.Fatalf("no copy of the state before: %q", jr.Backup.Pre)
	}
	if _, err := os.Stat(filepath.Join(o.Root, "/etc/loft.before-restore")); err == nil {
		t.Fatal("the old state is left beside")
	}
}
