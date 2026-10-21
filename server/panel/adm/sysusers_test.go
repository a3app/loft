package adm

import (
	"context"
	"strings"
	"testing"

	"github.com/a3app/loft/server/panel/admproto"
)

const (
	keyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGb0vd7Rb6ULYhvSL6y5mU9kKqPB7cH6P3eq0hGg0Gbq a@laptop"
	keyB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIC+Q7dKX3qQ4pqA7m8y1nJm2cU3rQ7hTt8m5w0lU0aBz b@laptop"
)

type fakeSys struct {
	read    string
	scripts []string
	stdin   []string
}

func (f *fakeSys) exec(_ context.Context, name string, args ...string) (string, error) {
	if name == "sh" && strings.Contains(args[1], "getent passwd; echo @@") {
		return f.read, nil
	}
	if name == "chpasswd" {
		f.stdin = append(f.stdin, name)
		return "", nil
	}
	f.scripts = append(f.scripts, strings.Join(append([]string{name}, args...), " "))
	return "", nil
}

func sysRead(rootKeys, deployKeys string, deploySudo bool, rootLogin string) string {
	g := "sudo:x:27:"
	if deploySudo {
		g += "deploy"
	}
	return "root:x:0:0:root:/root:/bin/bash\ndaemon:x:1:1::/usr/sbin:/usr/sbin/nologin\ndeploy:x:1000:1000::/home/deploy:/bin/bash\nbob:x:1001:1001::/home/bob:/bin/bash\nnobody:x:65534:65534::/nonexistent:/usr/sbin/nologin\n" +
		"@@" + g + "\nroot:x:0:\ndeploy:x:1000:\nbob:x:1001:\n" +
		"@@root:!:\ndeploy:$6$abc$def:\nbob:!:19000\n" +
		"@@passwordauthentication no\npermitrootlogin " + rootLogin + "\n" +
		"@@README\n" +
		"@@##root\n" + rootKeys + "\n##deploy\n" + deployKeys + "\n##bob\n"
}

func sysOps(t *testing.T, read string) (*Ops, *fakeSys) {
	o, _ := pkgOps(t)
	f := &fakeSys{read: read}
	o.Exec = f.exec
	return o, f
}

func TestSysUsersRead(t *testing.T) {
	o, _ := sysOps(t, sysRead(keyA, keyB, true, "prohibit-password"))
	v, err := o.SysUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Users) != 3 || v.Users[0].Login != "root" || !v.Users[0].Root {
		t.Fatalf("root and people only, not daemon or nobody: %+v", v.Users)
	}
	d, b := v.Users[1], v.Users[2]
	if d.Sudo != "password" || !d.Password || len(d.Keys) != 1 || d.Locked {
		t.Fatalf("deploy: %+v", d)
	}
	if !b.Locked || b.Password || b.Sudo != "" {
		t.Fatalf("bob is expired and has no password: %+v", b)
	}
	if v.PasswordAuth || v.RootLogin != "prohibit-password" {
		t.Fatalf("sshd: %+v", v)
	}
}

func TestSysUserLockout(t *testing.T) {
	// deploy is the only way in as root (sshd keeps root out).
	o, _ := sysOps(t, sysRead(keyA, keyB, true, "no"))
	ctx := context.Background()
	for _, req := range []admproto.SysUserSave{
		{Login: "deploy", Sudo: "password", Keys: nil},
		{Login: "deploy", Sudo: "", Keys: []string{keyB}},
		{Login: "deploy", Sudo: "password", Keys: []string{keyB}, Lock: true},
	} {
		if _, err := o.SysUserSave(ctx, req); err == nil || !strings.Contains(err.Error(), "no account that can log in") {
			t.Fatalf("%+v: want the lockout refusal, got %v", req, err)
		}
	}
	if _, err := o.SysUserDelete(ctx, admproto.SysUserDelete{Login: "deploy"}); err == nil {
		t.Fatal("deleting the last way in was allowed")
	}
	if _, err := o.SysUserDelete(ctx, admproto.SysUserDelete{Login: "root"}); err == nil {
		t.Fatal("root deleted")
	}
	// With root reachable by key, deploy may lose its keys.
	o, _ = sysOps(t, sysRead(keyA, keyB, true, "prohibit-password"))
	if _, err := o.SysUserSave(ctx, admproto.SysUserSave{Login: "deploy", Sudo: "password"}); err != nil {
		t.Fatal(err)
	}
}

func TestSysUserSave(t *testing.T) {
	o, f := sysOps(t, sysRead(keyA, keyB, true, "prohibit-password"))
	ctx := context.Background()
	bad := []admproto.SysUserSave{
		{Login: "Bob", New: true},
		{Login: "x;rm -rf /", New: true},
		{Login: "www-data", New: true},
		{Login: "carol", New: true, Keys: []string{"ssh-ed25519 notbase64!"}},
		{Login: "carol", New: true, Password: "short"},
		{Login: "carol", New: true, Password: "has:colon-in-it"},
		{Login: "bob", New: true},
		{Login: "ghost"},
		{Login: "root", Sudo: "password", Keys: []string{keyA}},
		{Login: "root", Keys: []string{keyA}, Password: "long enough pass"},
	}
	for _, req := range bad {
		if _, err := o.SysUserSave(ctx, req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
	if _, err := o.SysUserSave(ctx, admproto.SysUserSave{Login: "carol", New: true, Sudo: "nopasswd", Keys: []string{keyA, keyA, ""}, Password: "carol's long pass"}); err != nil {
		t.Fatal(err)
	}
	sc := strings.Join(f.scripts, "\n")
	for _, w := range []string{"useradd --create-home --shell /bin/bash -- carol", "usermod -aG sudo carol", "usermod --expiredate '' carol", "install -d -m 0700 -o carol"} {
		if !strings.Contains(sc, w) {
			t.Fatalf("script lacks %q:\n%s", w, sc)
		}
	}
	if len(f.stdin) != 1 {
		t.Fatal("the password goes to chpasswd on stdin")
	}
	if strings.Contains(sc, "carol's long pass") {
		t.Fatal("the password is on a command line")
	}
	f.scripts = nil
	if _, err := o.SysUserSave(ctx, admproto.SysUserSave{Login: "bob", Sudo: "", Keys: []string{keyB}, Lock: true}); err != nil {
		t.Fatal(err)
	}
	if sc := strings.Join(f.scripts, "\n"); !strings.Contains(sc, "usermod --expiredate 1 bob") || !strings.Contains(sc, "H='/home/bob'") {
		t.Fatalf("lock:\n%s", sc)
	}
}
