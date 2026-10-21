package adm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/a3app/loft/server/panel/admproto"
)

// Linux accounts. Root and the people's accounts (UID 1000 and up) are
// shown; system accounts are not, and nothing here touches them. sudo is the
// "sudo" group; without a password it is also a file in /etc/sudoers.d,
// checked with visudo before it is put in place.
//
// The helper's sandbox hides /home and /root, so every read and write goes
// through the host.

const sudoersDir = "/etc/sudoers.d"

var (
	loginRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`)
	homeRe  = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
)

// reserved are names a person's account must not take: they are services'
// or would read as the panel's own.
var reserved = []string{"root", "daemon", "bin", "sys", "sync", "games", "man", "lp", "mail", "news", "uucp", "proxy",
	"www-data", "backup", "list", "irc", "nobody", "systemd-network", "systemd-resolve", "messagebus", "sshd",
	"loft", "loft-panel", "vmail", "postfix", "dovecot", "dovenull", "opendkim", "_rspamd", "debian-tor", "admin"}

func sudoersFile(login string) string { return sudoersDir + "/loft-panel-" + login }

type sysAcct struct {
	admproto.SysUser
	home, group string
}

// hostIn is host with stdin (chpasswd reads the password there, never from
// the command line, where ps would show it).
func (o *Ops) hostIn(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	if o.Exec != nil {
		return o.Exec(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, "systemd-run", append([]string{"--wait", "--pipe", "--collect", "--quiet", name}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(stdin), &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s: %v %s", name, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

const sysReadScript = `getent passwd; echo @@; getent group; echo @@; getent shadow | cut -d: -f1,2,8; echo @@
sshd -T 2>/dev/null | grep -Ei '^(passwordauthentication|permitrootlogin) '; echo @@; ls ` + sudoersDir + ` 2>/dev/null; echo @@
getent passwd | awk -F: '($3==0||$3>=1000)&&$3!=65534{print $1":"$6}' | while IFS=: read u h; do echo "##$u"; cat "$h/.ssh/authorized_keys" 2>/dev/null; done`

// readAccts lists root and the people's accounts.
func (o *Ops) readAccts(ctx context.Context) ([]sysAcct, admproto.SysUsers, error) {
	out, err := o.host(ctx, "sh", "-c", sysReadScript)
	if err != nil && !strings.Contains(out, "@@") {
		return nil, admproto.SysUsers{}, err
	}
	part := strings.SplitN(out, "@@", 6)
	for len(part) < 6 {
		part = append(part, "")
	}
	view := admproto.SysUsers{RootLogin: "prohibit-password"}
	for _, ln := range strings.Split(part[3], "\n") {
		f := strings.Fields(strings.ToLower(ln))
		if len(f) == 2 && f[0] == "passwordauthentication" {
			view.PasswordAuth = f[1] == "yes"
		}
		if len(f) == 2 && f[0] == "permitrootlogin" {
			view.RootLogin = f[1]
		}
	}
	sudoers := map[string]bool{}
	for _, f := range strings.Fields(part[4]) {
		sudoers[f] = true
	}
	inSudo := map[string]bool{}
	gname := map[string]string{}
	for _, ln := range strings.Split(part[1], "\n") {
		f := strings.Split(strings.TrimSpace(ln), ":")
		if len(f) < 4 {
			continue
		}
		gname[f[2]] = f[0]
		if f[0] == "sudo" || f[0] == "admin" || f[0] == "wheel" {
			for _, m := range strings.Split(f[3], ",") {
				inSudo[m] = true
			}
		}
	}
	shadow := map[string][2]string{}
	for _, ln := range strings.Split(part[2], "\n") {
		f := strings.Split(strings.TrimSpace(ln), ":")
		if len(f) == 3 {
			shadow[f[0]] = [2]string{f[1], f[2]}
		}
	}
	keys := map[string][]string{}
	cur := ""
	for _, ln := range strings.Split(part[5], "\n") {
		ln = strings.TrimSpace(ln)
		if u, ok := strings.CutPrefix(ln, "##"); ok {
			cur = u
			continue
		}
		if cur != "" && ln != "" && !strings.HasPrefix(ln, "#") {
			keys[cur] = append(keys[cur], ln)
		}
	}
	today := int(time.Now().Unix() / 86400)
	var accts []sysAcct
	for _, ln := range strings.Split(part[0], "\n") {
		f := strings.Split(strings.TrimSpace(ln), ":")
		if len(f) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(f[2])
		if !(uid == 0 && f[0] == "root") && (uid < 1000 || uid == 65534) {
			continue
		}
		a := sysAcct{SysUser: admproto.SysUser{Login: f[0], UID: uid, Keys: keys[f[0]], Root: uid == 0}, home: f[5], group: gname[f[3]]}
		if a.Keys == nil {
			a.Keys = []string{}
		}
		sh := shadow[f[0]]
		a.Password = sh[0] != "" && !strings.HasPrefix(sh[0], "!") && !strings.HasPrefix(sh[0], "*")
		if exp, err := strconv.Atoi(sh[1]); err == nil && exp <= today {
			a.Locked = true
		}
		switch {
		case a.Root:
		case sudoers["loft-panel-"+a.Login]:
			a.Sudo = "nopasswd"
		case inSudo[a.Login]:
			a.Sudo = "password"
		}
		accts = append(accts, a)
	}
	for _, a := range accts {
		view.Users = append(view.Users, a.SysUser)
	}
	if view.Users == nil {
		view.Users = []admproto.SysUser{}
	}
	return accts, view, nil
}

// SysUsers lists the accounts.
func (o *Ops) SysUsers(ctx context.Context) (admproto.SysUsers, error) {
	_, v, err := o.readAccts(ctx)
	return v, err
}

// canRoot says whether a reaches root over ssh with a key: root itself when
// sshd lets root in, or a sudo account. Locked accounts and accounts
// without keys do not count.
func canRoot(a admproto.SysUser, rootLogin string) bool {
	if a.Locked || len(a.Keys) == 0 {
		return false
	}
	if a.Root {
		return rootLogin != "no"
	}
	return a.Sudo != ""
}

// lockout refuses a change that leaves no account able to log in by key and
// become root — unless there was none before (passwords, a console).
func lockout(before, after []admproto.SysUser, rootLogin string) error {
	count := func(l []admproto.SysUser) int {
		n := 0
		for _, a := range l {
			if canRoot(a, rootLogin) {
				n++
			}
		}
		return n
	}
	if count(before) > 0 && count(after) == 0 {
		return errors.New("this leaves no account that can log in by key and become root")
	}
	return nil
}

// cleanKeys checks every line with the OpenSSH parser and drops repeats.
func cleanKeys(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, k := range in {
		k = strings.TrimSpace(k)
		if k == "" || strings.HasPrefix(k, "#") {
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k))
		if err != nil || strings.ContainsAny(k, "\r\n") {
			return nil, fmt.Errorf("not an SSH public key: %.40s", k)
		}
		fp := ssh.FingerprintSHA256(pub)
		if seen[fp] {
			continue
		}
		seen[fp] = true
		out = append(out, k)
	}
	if len(out) > 100 {
		return nil, errors.New("at most 100 keys")
	}
	return out, nil
}

// SysUserSave creates or changes an account.
func (o *Ops) SysUserSave(ctx context.Context, req admproto.SysUserSave) (admproto.SysUsers, error) {
	o.sysMu.Lock()
	defer o.sysMu.Unlock()
	accts, view, err := o.readAccts(ctx)
	if err != nil {
		return view, err
	}
	if !loginRe.MatchString(req.Login) {
		return view, errors.New("login: lowercase Latin letters, digits, _ and -, up to 31")
	}
	if !slices.Contains([]string{"", "password", "nopasswd"}, req.Sudo) {
		return view, errors.New("bad sudo level")
	}
	if req.Password != "" && (len([]rune(req.Password)) < 9 || strings.ContainsAny(req.Password, "\r\n:")) {
		return view, errors.New("password: at least 9 characters, no colons or line breaks")
	}
	keys, err := cleanKeys(req.Keys)
	if err != nil {
		return view, err
	}
	i := slices.IndexFunc(accts, func(a sysAcct) bool { return a.Login == req.Login })
	if req.New != (i < 0) {
		if req.New {
			return view, fmt.Errorf("account %s exists", req.Login)
		}
		return view, errors.New("no such account")
	}
	if req.New && slices.Contains(reserved, req.Login) {
		return view, fmt.Errorf("%s is a reserved name", req.Login)
	}
	var a sysAcct
	if i >= 0 {
		a = accts[i]
	}
	if a.Root && (req.Sudo != "" || req.Lock || req.Password != "") {
		return view, errors.New("root: only its SSH keys can change here")
	}
	if a.Root {
		req.Sudo = ""
	}
	// The account as it will be, for the lockout check.
	after := []admproto.SysUser{}
	for _, x := range accts {
		if x.Login != req.Login {
			after = append(after, x.SysUser)
		}
	}
	next := a.SysUser
	next.Login, next.Keys, next.Locked = req.Login, keys, req.Lock
	if !a.Root {
		next.Sudo = req.Sudo
	}
	after = append(after, next)
	if err := lockout(view.Users, after, view.RootLogin); err != nil {
		return view, err
	}

	var sc strings.Builder
	sc.WriteString("set -e\n")
	if req.New {
		sc.WriteString("useradd --create-home --shell /bin/bash -- " + req.Login + "\n")
	}
	if !a.Root {
		switch req.Sudo {
		case "":
			sc.WriteString("if id -nG " + req.Login + " | tr ' ' '\\n' | grep -qx sudo; then gpasswd -d " + req.Login + " sudo >/dev/null; fi\nrm -f " + sudoersFile(req.Login) + "\n")
		case "password":
			sc.WriteString("usermod -aG sudo " + req.Login + "\nrm -f " + sudoersFile(req.Login) + "\n")
		case "nopasswd":
			put, err := o.stage([]hostFile{{Path: sudoersFile(req.Login), Data: req.Login + " ALL=(ALL:ALL) NOPASSWD: ALL\n", Mode: "0440", Owner: "root", Group: "root"}})
			if err != nil {
				return view, err
			}
			// visudo checks the staged copy first: a broken sudoers file
			// takes sudo away from everyone.
			if tmp := stagedPath(put); tmp != "" {
				sc.WriteString("visudo -cqf " + shq(tmp) + "\n")
			}
			sc.WriteString("usermod -aG sudo " + req.Login + "\n" + put)
		}
		if req.Lock {
			sc.WriteString("usermod --expiredate 1 " + req.Login + "\n")
		} else {
			sc.WriteString("usermod --expiredate '' " + req.Login + "\n")
		}
	}
	// Home and group of a new account come from useradd: read them in the
	// same script.
	home, group := a.home, a.group
	if req.New {
		home, group = "$(getent passwd "+req.Login+" | cut -d: -f6)", "$(id -gn "+req.Login+")"
	} else if !homeRe.MatchString(home) || strings.Contains(home, "..") {
		return view, fmt.Errorf("%s: unusual home directory %q", req.Login, home)
	}
	put, err := o.stage([]hostFile{{Path: "/authorized_keys.loft", Data: strings.Join(keys, "\n") + "\n", Mode: "0600"}})
	if err != nil {
		return view, err
	}
	tmp := stagedPath(put)
	hq := `"` + home + `"`
	if !req.New {
		hq = shq(home)
	}
	sc.WriteString(fmt.Sprintf("H=%s; G=%s\ninstall -d -m 0700 -o %s -g \"$G\" \"$H/.ssh\"\n", hq, group, req.Login))
	if tmp != "" {
		sc.WriteString(fmt.Sprintf("install -m 0600 -o %s -g \"$G\" %s \"$H/.ssh/authorized_keys\" && rm -f %s\n", req.Login, shq(tmp), shq(tmp)))
	}
	if _, err := o.host(ctx, "sh", "-c", sc.String()); err != nil {
		return view, fmt.Errorf("%s: %v", req.Login, err)
	}
	if req.Password != "" {
		if _, err := o.hostIn(ctx, req.Login+":"+req.Password+"\n", "chpasswd"); err != nil {
			return view, fmt.Errorf("password of %s: %v", req.Login, err)
		}
	}
	_, view, err = o.readAccts(ctx)
	return view, err
}

// stagedPath is the staged source in an install line from stage.
func stagedPath(put string) string {
	f := strings.Fields(put)
	for i, x := range f {
		if strings.HasPrefix(x, "'") && i+1 < len(f) {
			return strings.Trim(x, "'")
		}
	}
	return ""
}

// SysUserDelete removes an account.
func (o *Ops) SysUserDelete(ctx context.Context, req admproto.SysUserDelete) (admproto.SysUsers, error) {
	o.sysMu.Lock()
	defer o.sysMu.Unlock()
	accts, view, err := o.readAccts(ctx)
	if err != nil {
		return view, err
	}
	i := slices.IndexFunc(accts, func(a sysAcct) bool { return a.Login == req.Login })
	if i < 0 || !loginRe.MatchString(req.Login) {
		return view, errors.New("no such account")
	}
	if accts[i].Root {
		return view, errors.New("root cannot be deleted")
	}
	var after []admproto.SysUser
	for _, x := range accts {
		if x.Login != req.Login {
			after = append(after, x.SysUser)
		}
	}
	if err := lockout(view.Users, after, view.RootLogin); err != nil {
		return view, err
	}
	args := []string{"userdel"}
	if req.Home {
		args = append(args, "--remove")
	}
	if _, err := o.host(ctx, "sh", "-c", strings.Join(append(args, req.Login), " ")+" 2>&1 && rm -f "+sudoersFile(req.Login)); err != nil {
		return view, fmt.Errorf("%s: %v", req.Login, err)
	}
	_, view, err = o.readAccts(ctx)
	return view, err
}
