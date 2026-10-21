package adm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// The panel's nginx site, one template for the installer and the wizard.
// loft-node passes every TLS connection that is not a loft client to
// 127.0.0.1:8443 with a PROXY line (decoy_proxy enable).
//
// One name: the panel answers on the node's name with the node certificate.
// Two names: the panel answers only on its own name with its own
// certificate; anything else that reaches nginx through the node — a probe
// of the node's name — gets nginx's plain default page and the node's
// certificate, which says nothing about the panel.
var siteTmpl = template.Must(template.New("site").Parse(`# Written by loft-panel-adm; edits are replaced when the names change.
# Node: {{.Node}}   Panel: {{.Panel}}

# :80 is nginx's own: Let's Encrypt HTTP-01 goes to the panel, the rest to https.
server {
	listen 80;
	listen [::]:80;
	server_name {{.Names80}};

	location /.well-known/acme-challenge/ {
		proxy_pass http://{{.Listen}};
		proxy_set_header Host $host;
		proxy_set_header X-Forwarded-For $remote_addr;
	}
	location / {
		return 301 https://$host$request_uri;
	}
}

# The panel. ` + "`listen … http2`" + ` rather than ` + "`http2 on`" + `: Debian 12 ships nginx 1.22.
server {
	listen 127.0.0.1:8443 ssl http2 proxy_protocol{{if not .Separate}} default_server{{end}};
	server_name {{.Panel}};

	set_real_ip_from 127.0.0.1;
	real_ip_header proxy_protocol;

	ssl_certificate     {{.PanelCert}};
	ssl_certificate_key {{.PanelKey}};
	ssl_protocols TLSv1.2 TLSv1.3;

	client_max_body_size 2m;
	access_log /var/log/nginx/loft-panel.access.log;
	gzip on;
	gzip_types text/css application/javascript text/javascript application/json;

	# System → Restore → From a file: a copy can be gigabytes; it streams
	# to the panel instead of being buffered here.
	location = /api/system/backup/upload {
		client_max_body_size 64g;
		proxy_request_buffering off;
		proxy_pass http://{{.Listen}};
		proxy_http_version 1.1;
		proxy_set_header Host $host;
		proxy_set_header X-Forwarded-For $proxy_protocol_addr;
		proxy_set_header X-Forwarded-Proto https;
		proxy_read_timeout 3600s;
		proxy_send_timeout 3600s;
	}
	location / {
		proxy_pass http://{{.Listen}};
		proxy_http_version 1.1;
		proxy_set_header Host $host;
		proxy_set_header X-Forwarded-For $proxy_protocol_addr;
		proxy_set_header X-Forwarded-Proto https;
		proxy_read_timeout 120s;
	}
}
{{if .Separate}}
# Everything else the node hands over (probes of {{.Node}}): a plain site.
server {
	listen 127.0.0.1:8443 ssl http2 proxy_protocol default_server;
	server_name _;

	set_real_ip_from 127.0.0.1;
	real_ip_header proxy_protocol;

	ssl_certificate     {{.NodeCert}};
	ssl_certificate_key {{.NodeKey}};
	ssl_protocols TLSv1.2 TLSv1.3;

	root /var/www/html;
	index index.html index.nginx-debian.html;
	location / {
		try_files $uri $uri/ =404;
	}
}
{{end}}`))

// Site is what the template needs.
type Site struct {
	Node, Panel         string
	Listen              string // the panel's HTTP address
	NodeCert, NodeKey   string
	PanelCert, PanelKey string
}

func (s Site) Separate() bool { return s.Panel != "" && s.Panel != s.Node }
func (s Site) Names80() string {
	if s.Separate() {
		return s.Node + " " + s.Panel
	}
	return s.Node
}

// RenderSite is the nginx site text.
func RenderSite(s Site) string {
	if s.Panel == "" {
		s.Panel = s.Node
	}
	var b bytes.Buffer
	siteTmpl.Execute(&b, s)
	return b.String()
}

func (o *Ops) panelTLS() string { return firstNonEmpty(o.PanelTLS, "/etc/loft-panel/tls") }

// currentSite reads the names from node.conf (advertise) and panel.env
// (PANEL_HOSTS) and picks the panel certificate when one exists.
func (o *Ops) currentSite() (Site, error) {
	in, err := o.NodeInfo()
	if err != nil {
		return Site{}, err
	}
	if in.Host == "" {
		return Site{}, errors.New("node.conf has no advertise name")
	}
	s := Site{Node: in.Host, Listen: "127.0.0.1:8091",
		NodeCert: filepath.Join(o.tlsDir(), "fullchain.pem"), NodeKey: filepath.Join(o.tlsDir(), "privkey.pem")}
	if p, _, _ := strings.Cut(in.Panel, ","); strings.TrimSpace(p) != "" {
		s.Panel = strings.TrimSpace(p)
	}
	if b, err := os.ReadFile(o.panelEnv()); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(ln), "PANEL_LISTEN="); ok && v != "" {
				s.Listen = v
			}
		}
	}
	s.PanelCert, s.PanelKey = s.NodeCert, s.NodeKey
	pc, pk := filepath.Join(o.panelTLS(), "fullchain.pem"), filepath.Join(o.panelTLS(), "privkey.pem")
	if s.Separate() && exists(pc) && exists(pk) {
		s.PanelCert, s.PanelKey = pc, pk
	}
	return s, nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// WriteSite renders the site from the current names, checks it with
// nginx -t, and keeps the old file when the check fails. No reload.
func (o *Ops) WriteSite() (changed bool, err error) {
	s, err := o.currentSite()
	if err != nil {
		return false, err
	}
	body := RenderSite(s)
	old, oldErr := os.ReadFile(o.nginxSite())
	if oldErr == nil && string(old) == body {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(o.nginxSite()), 0o755); err != nil {
		return false, err
	}
	if err := o.writeOwned(o.nginxSite(), []byte(body), 0o644, 0, 0); err != nil {
		return false, err
	}
	if o.NginxEnabled != "" {
		os.Remove(o.NginxEnabled)
		if err := os.Symlink(o.nginxSite(), o.NginxEnabled); err != nil {
			return false, err
		}
	}
	if err := o.nginxTest(); err != nil {
		if oldErr == nil {
			o.writeOwned(o.nginxSite(), old, 0o644, 0, 0)
		} else {
			os.Remove(o.nginxSite())
			if o.NginxEnabled != "" {
				os.Remove(o.NginxEnabled)
			}
		}
		return false, err
	}
	return true, nil
}

// reloadNginx reloads after a successful WriteSite or certificate change.
func (o *Ops) reloadNginx(ctx context.Context) error {
	if _, err := o.Systemctl(ctx, "reload", "nginx"); err != nil {
		return fmt.Errorf("nginx reload: %v", err)
	}
	return nil
}
