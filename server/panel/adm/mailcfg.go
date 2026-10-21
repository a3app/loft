package adm

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/a3app/loft/server/panel/admproto"
)

// Mail configuration, rendered from mail.json. The panel keeps the intent
// and these functions write the daemons' files from it, so a file edited by
// hand is replaced on the next save rather than merged.
//
// Every file is the panel's own: Postfix maps in /etc/postfix/loft, one
// Dovecot conf.d file, OpenDKIM's tables. main.cf and master.cf have no
// include mechanism, so there only the keys listed in mailParams change.

const (
	mailPostfixDir   = "/etc/postfix/loft"
	mailDovecotConf  = "/etc/dovecot/conf.d/99-loft-panel.conf"
	mailDovecotUsers = "/etc/dovecot/loft/users"
	mailDovecotAuth  = "/etc/dovecot/conf.d/10-auth.conf"
	mailRoot         = "/var/vmail"
	mailUser         = "vmail"

	dkimConf      = "/etc/opendkim.conf"
	dkimKeyTable  = "/etc/opendkim/key.table"
	dkimSignTable = "/etc/opendkim/signing.table"
	dkimTrusted   = "/etc/opendkim/trusted.hosts"
	dkimKeyDir    = "/etc/opendkim/keys"
	dkimPort      = "8891"
	spamPort      = "11332" // rspamd's proxy worker in milter mode

	// webmailSMTP is submission for Roundcube on the loopback: no TLS
	// there, so the outside ports can demand it without exceptions.
	webmailSMTP   = "10587"
	roundcubeConf = "/etc/roundcube/config.inc.php"
	roundcubeRoot = "/var/lib/roundcube/public_html"
	webmailSite   = "/etc/nginx/sites-available/loft-panel-mail.conf"
	webmailLink   = "/etc/nginx/sites-enabled/loft-panel-mail.conf"
)

// mailStore is mail.json.
type mailStore struct {
	Domains []admproto.MailDomain `json:"domains"`
	Boxes   []admproto.MailBox    `json:"boxes"`
	Aliases []admproto.MailAlias  `json:"aliases"`
	// DESKey encrypts Roundcube sessions; kept so a save does not sign
	// everyone out of webmail.
	DESKey string `json:"des_key,omitempty"`
	// Error is the last apply that failed, shown until one succeeds.
	Error string `json:"error,omitempty"`
}

// mailEnv is what the machine has, found before rendering.
type mailEnv struct {
	Dovecot24  bool            // Dovecot 2.4 syntax (Debian 13), else 2.3
	UID, GID   int             // vmail
	Certs      map[string]bool // mail.<domain> → a certificate is installed
	Spam       bool            // rspamd is running
	Webmail    bool            // Roundcube is installed
	PHPSock    string          // php-fpm socket for Roundcube
	Listen     string          // the panel's HTTP address, for ACME HTTP-01
	Hostname   string          // the machine's name, Postfix's when no domain is left
	DKIMPublic map[string]string
}

// hostFile is one file to put on the machine.
type hostFile struct {
	Path         string
	Data         string
	Mode         string // octal for install(1)
	Owner, Group string
}

func mailHost(domain string) string { return "mail." + domain }

func mailTLS(host string) (crt, key string) {
	return "/etc/loft-panel/mail-tls/" + host + ".crt", "/etc/loft-panel/mail-tls/" + host + ".key"
}

func dkimKeyPath(d admproto.MailDomain) string {
	return dkimKeyDir + "/" + d.Name + "." + dkimSel(d) + ".key"
}

func dkimSel(d admproto.MailDomain) string { return firstNonEmpty(d.Selector, "mail") }

func splitAddr(a string) (local, domain string) {
	i := strings.LastIndexByte(a, '@')
	if i < 0 {
		return a, ""
	}
	return a[:i], a[i+1:]
}

// on lists the enabled domains, in the order they were added.
func (s *mailStore) on() []admproto.MailDomain {
	var out []admproto.MailDomain
	for _, d := range s.Domains {
		if d.Enabled {
			out = append(out, d)
		}
	}
	return out
}

func (s *mailStore) domainOn(name string) bool {
	return slices.ContainsFunc(s.Domains, func(d admproto.MailDomain) bool { return d.Name == name && d.Enabled })
}

// live are the boxes Postfix and Dovecot know: enabled, in an enabled domain.
func (s *mailStore) live() []admproto.MailBox {
	var out []admproto.MailBox
	for _, b := range s.Boxes {
		if _, d := splitAddr(b.Address); b.Enabled && s.domainOn(d) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// helo is the name Postfix greets with: the first domain's mail host. One
// name for the whole server, so the PTR record names it.
func (s *mailStore) helo() string {
	if on := s.on(); len(on) > 0 {
		return mailHost(on[0].Name)
	}
	return ""
}

func mailHead(what string) string {
	return "# Written by loft-panel-adm: " + what + ".\n# Edits are replaced the next time mail is saved in the panel.\n"
}

func renderVDomains(s *mailStore) string {
	var b strings.Builder
	b.WriteString(mailHead("domains this server takes mail for"))
	for _, d := range s.on() {
		fmt.Fprintf(&b, "%s\tOK\n", d.Name)
	}
	return b.String()
}

// renderVMailbox: the path only tells Postfix the box exists; Dovecot
// stores the letter (LMTP) and takes the path from its own userdb.
func renderVMailbox(s *mailStore) string {
	var b strings.Builder
	b.WriteString(mailHead("mailboxes; Dovecot stores the letters"))
	for _, x := range s.live() {
		l, d := splitAddr(x.Address)
		fmt.Fprintf(&b, "%s\t%s/%s/\n", x.Address, d, l)
	}
	return b.String()
}

// renderVAlias is aliases, forwarding and catch-alls.
//
// A forward keeps the box itself among the recipients: a copy goes out,
// the letter stays. A catch-all "@domain" would also match existing boxes,
// because Postfix rewrites aliases before it looks for a box; so with a
// catch-all every box of the domain maps to itself and the exact match wins.
func renderVAlias(s *mailStore) string {
	var b strings.Builder
	b.WriteString(mailHead("aliases, forwarding and catch-alls"))
	have := map[string]bool{}
	var lines []string
	for _, a := range s.Aliases {
		if _, d := splitAddr(a.From); !s.domainOn(d) || len(a.To) == 0 {
			continue
		}
		have[a.From] = true
		lines = append(lines, a.From+"\t"+strings.Join(a.To, ", "))
	}
	catch := map[string]bool{}
	for _, d := range s.on() {
		if len(d.CatchAll) > 0 {
			catch[d.Name] = true
		}
	}
	for _, x := range s.live() {
		_, d := splitAddr(x.Address)
		switch {
		case len(x.Forwards) > 0:
			lines = append(lines, x.Address+"\t"+strings.Join(append([]string{x.Address}, x.Forwards...), ", "))
		case catch[d] && !have[x.Address]:
			lines = append(lines, x.Address+"\t"+x.Address)
		}
	}
	sort.Strings(lines)
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	for _, d := range s.on() {
		if len(d.CatchAll) > 0 {
			fmt.Fprintf(&b, "@%s\t%s\n", d.Name, strings.Join(d.CatchAll, ", "))
		}
	}
	return b.String()
}

// renderVLogin says which login may send as which address. Without it any
// box could sign its letters with any address of the server — and DKIM
// would then vouch for the forgery. An alias may be used by the boxes it
// delivers to.
func renderVLogin(s *mailStore) string {
	var b strings.Builder
	b.WriteString(mailHead("who may send as which address"))
	live := map[string]bool{}
	for _, x := range s.live() {
		live[x.Address] = true
		fmt.Fprintf(&b, "%s\t%s\n", x.Address, x.Address)
	}
	var lines []string
	for _, a := range s.Aliases {
		if live[a.From] {
			continue
		}
		var who []string
		for _, t := range a.To {
			if live[t] {
				who = append(who, t)
			}
		}
		if len(who) > 0 {
			lines = append(lines, a.From+"\t"+strings.Join(who, ","))
		}
	}
	sort.Strings(lines)
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	return b.String()
}

// renderNoRecv refuses outside mail to send-only boxes.
func renderNoRecv(s *mailStore) string {
	var b strings.Builder
	b.WriteString(mailHead("send-only boxes: outside mail to them is refused"))
	for _, x := range s.live() {
		if x.SendOnly {
			fmt.Fprintf(&b, "%s\tREJECT this address does not receive mail\n", x.Address)
		}
	}
	return b.String()
}

// renderSNI picks the certificate by the name a client asks for. postmap -F
// reads the files into the map.
func renderSNI(s *mailStore, e mailEnv) string {
	var b strings.Builder
	b.WriteString(mailHead("certificates by server name"))
	for _, d := range s.on() {
		h := mailHost(d.Name)
		if e.Certs[h] {
			crt, key := mailTLS(h)
			fmt.Fprintf(&b, "%s\t%s %s\n", h, key, crt)
		}
	}
	return b.String()
}

// defaultCert is the first domain's certificate: for clients that send no
// server name, and for the greeting on port 25.
func defaultCert(s *mailStore, e mailEnv) string {
	for _, d := range s.on() {
		if h := mailHost(d.Name); e.Certs[h] {
			return h
		}
	}
	return ""
}

// renderUsers is Dovecot's passwd-file: name:pass:uid:gid:gecos:home:shell:extra.
// Seven colons in all; with six the quota landed in the shell field and was
// silently ignored. A disabled box is left out, so it cannot log in.
func renderUsers(s *mailStore, e mailEnv) string {
	var b strings.Builder
	b.WriteString(mailHead("mailboxes and their password hashes"))
	for _, x := range s.live() {
		if x.Hash == "" {
			continue
		}
		line := x.Address + ":" + x.Hash + "::::::"
		if x.QuotaMB > 0 {
			if e.Dovecot24 {
				line += fmt.Sprintf("userdb_quota_storage_size=%dM", x.QuotaMB)
			} else {
				line += fmt.Sprintf("userdb_quota_rule=*:storage=%dM", x.QuotaMB)
			}
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// renderDovecot is the panel's conf.d file, in the syntax of the installed
// version: 2.4 rejects 2.3's and the other way round.
func renderDovecot(s *mailStore, e mailEnv) string {
	if e.Dovecot24 {
		return renderDovecot24(s, e)
	}
	var b strings.Builder
	b.WriteString(mailHead("mailboxes are virtual, one Maildir per address"))
	b.WriteString(`
mail_location = maildir:` + mailRoot + `/%d/%n/Maildir
mail_home = ` + mailRoot + `/%d/%n
mail_uid = ` + mailUser + `
mail_gid = ` + mailUser + `
# vmail is a system account; the default floor (500) would refuse it.
first_valid_uid = ` + fmt.Sprint(e.UID) + `
first_valid_gid = ` + fmt.Sprint(e.GID) + `

# The address is the login; the domain is never cut off (the stock lmtp
# block does, and then letters for user@domain find no box).
auth_username_format = %Lu
passdb {
  driver = passwd-file
  args = scheme=ARGON2ID username_format=%Lu ` + mailDovecotUsers + `
}
userdb {
  driver = passwd-file
  args = username_format=%Lu ` + mailDovecotUsers + `
}

mail_plugins = $mail_plugins quota
plugin {
  quota = maildir:User quota
}
# A full box refuses at SMTP time, so the sender learns it at once.
lmtp_rcpt_check_quota = yes
protocol imap {
  mail_plugins = $mail_plugins imap_quota
}
protocol lmtp {
  auth_username_format = %Lu
  mail_plugins = $mail_plugins sieve
}

# Postfix hands letters over and asks for passwords here. The sockets live
# in its queue directory, where its daemons can see them.
service lmtp {
  unix_listener /var/spool/postfix/private/dovecot-lmtp {
    mode = 0600
    user = postfix
    group = postfix
  }
}
service auth {
  unix_listener /var/spool/postfix/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
}
`)
	if h := defaultCert(s, e); h != "" {
		crt, key := mailTLS(h)
		fmt.Fprintf(&b, "\nssl_cert = <%s\nssl_key = <%s\n", crt, key)
	}
	for _, d := range s.on() {
		h := mailHost(d.Name)
		if !e.Certs[h] {
			continue
		}
		crt, key := mailTLS(h)
		fmt.Fprintf(&b, "local_name %s {\n  ssl_cert = <%s\n  ssl_key = <%s\n}\n", h, crt, key)
	}
	return b.String()
}

func renderDovecot24(s *mailStore, e mailEnv) string {
	var b strings.Builder
	b.WriteString(mailHead("mailboxes are virtual, one Maildir per address"))
	box := mailRoot + "/%{user | domain}/%{user | username}"
	b.WriteString(`
mail_driver = maildir
mail_path = ` + box + `/Maildir
mail_inbox_path = ` + box + `/Maildir
mail_home = ` + box + `
mail_uid = ` + mailUser + `
mail_gid = ` + mailUser + `
first_valid_uid = ` + fmt.Sprint(e.UID) + `
first_valid_gid = ` + fmt.Sprint(e.GID) + `

# The login is the whole address, lowercased (2.4's default); only the
# stock lmtp block cuts the domain off, so lmtp gets its own below.
passdb loft_boxes {
  driver = passwd-file
  passwd_file_path = ` + mailDovecotUsers + `
  default_password_scheme = ARGON2ID
}
userdb loft_boxes {
  driver = passwd-file
  passwd_file_path = ` + mailDovecotUsers + `
}

mail_plugins {
  quota = yes
}
quota "User quota" {
  driver = count
}
quota_storage_size = unlimited
lmtp_rcpt_check_quota = yes
protocol imap {
  mail_plugins {
    imap_quota = yes
  }
}
protocol lmtp {
  auth_username_format = %{user | lower}
  mail_plugins {
    sieve = yes
  }
}

service lmtp {
  unix_listener /var/spool/postfix/private/dovecot-lmtp {
    mode = 0600
    user = postfix
    group = postfix
  }
}
service auth {
  unix_listener /var/spool/postfix/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
}
`)
	if h := defaultCert(s, e); h != "" {
		crt, key := mailTLS(h)
		fmt.Fprintf(&b, "\nssl_server_cert_file = %s\nssl_server_key_file = %s\n", crt, key)
	}
	for _, d := range s.on() {
		h := mailHost(d.Name)
		if !e.Certs[h] {
			continue
		}
		crt, key := mailTLS(h)
		fmt.Fprintf(&b, "local_name %s {\n  ssl_server_cert_file = %s\n  ssl_server_key_file = %s\n}\n", h, crt, key)
	}
	return b.String()
}

// mailParams are the main.cf keys the panel owns.
func mailParams(s *mailStore, e mailEnv) [][2]string {
	p := [][2]string{
		// Local delivery off: boxes are virtual, and root@ must not pile up
		// in /var/mail.
		{"mydestination", "localhost"},
		{"virtual_mailbox_domains", "hash:" + mailPostfixDir + "/vdomains"},
		{"virtual_mailbox_maps", "hash:" + mailPostfixDir + "/vmailbox"},
		{"virtual_alias_maps", "hash:" + mailPostfixDir + "/valias"},
		{"virtual_transport", "lmtp:unix:private/dovecot-lmtp"},
		{"smtpd_sasl_type", "dovecot"},
		{"smtpd_sasl_path", "private/auth"},
		{"smtpd_sasl_auth_enable", "yes"},
		{"smtpd_sender_login_maps", "hash:" + mailPostfixDir + "/vlogin"},
		// Passwords only over TLS: otherwise port 25 offers AUTH in the
		// clear and a misconfigured client sends the password to everyone
		// on the way.
		{"smtpd_tls_auth_only", "yes"},
		{"smtpd_tls_security_level", "may"},
		{"smtp_tls_security_level", "may"},
		{"smtpd_recipient_restrictions", "permit_mynetworks, permit_sasl_authenticated, check_recipient_access hash:" + mailPostfixDir + "/norecv, reject_unauth_destination"},
		{"milter_protocol", "6"},
		// A milter that does not answer must not stop mail: unsigned is
		// better than a queue up to the ceiling.
		{"milter_default_action", "accept"},
		{"milter_mail_macros", "i {mail_addr} {client_addr} {client_name} {auth_authen}"},
	}
	if h := firstNonEmpty(s.helo(), e.Hostname); h != "" {
		p = append(p, [2]string{"myhostname", h})
	}
	milters := "inet:127.0.0.1:" + dkimPort
	if e.Spam {
		milters += ", inet:127.0.0.1:" + spamPort
	}
	p = append(p, [2]string{"smtpd_milters", milters}, [2]string{"non_smtpd_milters", "inet:127.0.0.1:" + dkimPort})
	if h := defaultCert(s, e); h != "" {
		crt, key := mailTLS(h)
		p = append(p, [2]string{"smtpd_tls_chain_files", key + ", " + crt}, [2]string{"tls_server_sni_maps", "hash:" + mailPostfixDir + "/sni"})
	} else {
		p = append(p, [2]string{"smtpd_tls_chain_files", ""}, [2]string{"tls_server_sni_maps", ""})
	}
	return p
}

// mailServices are master.cf lines. chroot is off ("n", the fifth field):
// inside /var/spool/postfix smtpd cannot open the maps in /etc/postfix/loft,
// and an unreadable sender map answers "Sender address rejected" for
// everyone.
func mailServices() [][2]string {
	return [][2]string{
		{"smtp/inet", "smtp inet n - n - - smtpd"},
		{"submission/inet", "submission inet n - n - - smtpd"},
		{"submissions/inet", "submissions inet n - n - - smtpd"},
		{"127.0.0.1:" + webmailSMTP + "/inet", "127.0.0.1:" + webmailSMTP + " inet n - n - - smtpd"},
	}
}

// mailServiceParams are the -o settings of the submission services: only
// with a password, only as one's own address.
func mailServiceParams() [][2]string {
	var out [][2]string
	add := func(svc string, kv ...[2]string) {
		for _, p := range kv {
			out = append(out, [2]string{svc + "/" + p[0], p[1]})
		}
	}
	authed := [][2]string{
		{"smtpd_sasl_auth_enable", "yes"},
		{"smtpd_client_restrictions", "permit_sasl_authenticated,reject"},
		{"smtpd_relay_restrictions", "permit_sasl_authenticated,reject"},
		// reject_sender_login_mismatch only refuses a forgery; on a match it
		// passes the decision on, so permit must follow it before reject.
		{"smtpd_sender_restrictions", "reject_sender_login_mismatch,permit_sasl_authenticated,reject"},
		{"smtpd_recipient_restrictions", "permit_sasl_authenticated,reject"},
		// Tells the milters the letter is ours: sign it, do not score it.
		{"milter_macro_daemon_name", "ORIGINATING"},
	}
	add("submission/inet", append(slices.Clone(authed),
		[2]string{"syslog_name", "postfix/submission"},
		[2]string{"smtpd_tls_security_level", "encrypt"},
		[2]string{"smtpd_tls_auth_only", "yes"})...)
	add("submissions/inet", append(slices.Clone(authed),
		[2]string{"syslog_name", "postfix/submissions"},
		[2]string{"smtpd_tls_wrappermode", "yes"},
		[2]string{"smtpd_tls_auth_only", "yes"})...)
	add("127.0.0.1:"+webmailSMTP+"/inet", append(slices.Clone(authed),
		[2]string{"syslog_name", "postfix/webmail"},
		[2]string{"smtpd_tls_security_level", "none"},
		[2]string{"smtpd_tls_auth_only", "no"})...)
	return out
}

// renderDKIMConf: OpenDKIM only signs (mode s); checking incoming mail is
// the spam filter's job. Mail from the loopback and from logged-in senders
// is signed.
func renderDKIMConf() string {
	return mailHead("OpenDKIM signs outgoing mail") +
		"Syslog                  yes\n" +
		"SyslogSuccess           yes\n" +
		"UMask                   007\n" +
		"Mode                    s\n" +
		"SubDomains              no\n" +
		"Canonicalization        relaxed/simple\n" +
		"OversignHeaders         From\n" +
		"Socket                  inet:" + dkimPort + "@127.0.0.1\n" +
		"PidFile                 /run/opendkim/opendkim.pid\n" +
		"UserID                  opendkim\n" +
		"KeyTable                file:" + dkimKeyTable + "\n" +
		"SigningTable            refile:" + dkimSignTable + "\n" +
		"InternalHosts           " + dkimTrusted + "\n"
}

func renderKeyTable(s *mailStore) string {
	var b strings.Builder
	b.WriteString(mailHead("DKIM keys"))
	for _, d := range s.on() {
		if d.DKIM {
			fmt.Fprintf(&b, "%s._domainkey.%s %s:%s:%s\n", dkimSel(d), d.Name, d.Name, dkimSel(d), dkimKeyPath(d))
		}
	}
	return b.String()
}

func renderSignTable(s *mailStore) string {
	var b strings.Builder
	b.WriteString(mailHead("which key signs which sender"))
	for _, d := range s.on() {
		if d.DKIM {
			fmt.Fprintf(&b, "*@%s %s._domainkey.%s\n", d.Name, dkimSel(d), d.Name)
		}
	}
	return b.String()
}

func renderTrusted() string {
	return mailHead("hosts whose mail is signed") + "127.0.0.1\n::1\nlocalhost\n"
}

// phpStr quotes s for a PHP single-quoted string.
func phpStr(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// renderRoundcube is config.inc.php. IMAP and SMTP go over the loopback;
// the address is the login, typed in full.
func renderRoundcube(s *mailStore) string {
	return "<?php\n// Written by loft-panel-adm. Edits are replaced the next time mail is saved.\n\n" +
		"include_once('/etc/roundcube/debian-db-roundcube.php');\n\n" +
		"$config['imap_host'] = 'localhost:143';\n" +
		"$config['smtp_host'] = 'localhost:" + webmailSMTP + "';\n" +
		"$config['smtp_user'] = '%u';\n" +
		"$config['smtp_pass'] = '%p';\n" +
		"$config['username_domain'] = '';\n" +
		"$config['login_lc'] = 2;\n" +
		"$config['product_name'] = 'Mail';\n" +
		"$config['skin'] = 'elastic';\n" +
		"$config['support_url'] = '';\n" +
		"$config['temp_dir'] = '/var/lib/roundcube/temp';\n" +
		"$config['log_dir'] = '/var/log/roundcube';\n" +
		"$config['des_key'] = " + phpStr(s.DESKey) + ";\n" +
		// The installer changes settings from a browser without a password.
		"$config['enable_installer'] = false;\n" +
		"$config['plugins'] = ['archive', 'zipdownload', 'managesieve'];\n" +
		"$config['managesieve_host'] = 'localhost';\n"
}

// renderMailSite is the nginx site for mail.<domain>: HTTP-01 for its
// certificate on :80, and webmail behind the node (127.0.0.1:8443, PROXY
// line) when Roundcube is installed and the name has a certificate.
func renderMailSite(s *mailStore, e mailEnv) string {
	var hosts []string
	for _, d := range s.on() {
		hosts = append(hosts, mailHost(d.Name))
	}
	if len(hosts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(mailHead("mail server names: certificates and webmail"))
	fmt.Fprintf(&b, `
server {
	listen 80;
	listen [::]:80;
	server_name %s;

	location /.well-known/acme-challenge/ {
		proxy_pass http://%s;
		proxy_set_header Host $host;
		proxy_set_header X-Forwarded-For $remote_addr;
	}
	location / {
		return 301 https://$host$request_uri;
	}
}
`, strings.Join(hosts, " "), e.Listen)
	if !e.Webmail || e.PHPSock == "" {
		return b.String()
	}
	for _, h := range hosts {
		if !e.Certs[h] {
			continue
		}
		crt, key := mailTLS(h)
		fmt.Fprintf(&b, `
server {
	listen 127.0.0.1:8443 ssl http2 proxy_protocol;
	server_name %s;

	set_real_ip_from 127.0.0.1;
	real_ip_header proxy_protocol;

	ssl_certificate     %s;
	ssl_certificate_key %s;
	ssl_protocols TLSv1.2 TLSv1.3;

	root %s;
	index index.php;
	client_max_body_size 64m;
	access_log /var/log/nginx/loft-panel-mail.access.log;

	add_header X-Frame-Options SAMEORIGIN always;
	add_header X-Content-Type-Options nosniff always;
	add_header Referrer-Policy same-origin always;

	# The package links /etc/roundcube and its logs in here.
	location ~ ^/(config|temp|logs|bin|SQL|installer)/ { deny all; }
	location ~ /\. { deny all; }
	location / {
		try_files $uri $uri/ =404;
	}
	location ~ \.php$ {
		try_files $uri =404;
		fastcgi_split_path_info ^(.+\.php)(/.+)$;
		fastcgi_pass unix:%s;
		fastcgi_index index.php;
		include fastcgi_params;
		fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
		fastcgi_param PATH_INFO $fastcgi_path_info;
		fastcgi_param HTTPS on;
		fastcgi_read_timeout 300s;
	}
}
`, h, crt, key, roundcubeRoot, e.PHPSock)
	}
	return b.String()
}

// webHosts are the names webmail answers on.
func webHosts(s *mailStore, e mailEnv) []string {
	out := []string{}
	if !e.Webmail || e.PHPSock == "" {
		return out
	}
	for _, d := range s.on() {
		if h := mailHost(d.Name); e.Certs[h] {
			out = append(out, h)
		}
	}
	return out
}

// disableSystemAuth comments out the stock "!include auth-system.conf.ext":
// with it every account of the machine is also a mail login with the same
// password, on IMAP and on submission. Dovecot cannot drop a passdb from a
// later file, so this is the one foreign file the panel edits — a comment,
// reversible by hand.
func disableSystemAuth(conf string) (string, bool) {
	lines := strings.Split(conf, "\n")
	changed := false
	for i, l := range lines {
		if strings.TrimSpace(l) == "!include auth-system.conf.ext" {
			lines[i] = "# Off (loft-panel): mailboxes are virtual; the machine's accounts are not mail logins.\n#!include auth-system.conf.ext"
			changed = true
		}
	}
	return strings.Join(lines, "\n"), changed
}
